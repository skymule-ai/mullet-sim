#!/usr/bin/env python3
"""
VTOL Offboard State Machine
============================
MC takeoff → yaw align → FW transition → straight-line FW cruise → back to MC.

FW setpoint design
------------------
OffboardControlMode : position=True  velocity=True
TrajectorySetpoint  : position=[x_lookahead, y_lookahead, z_target]
                      velocity=[vx_ff,      vy_ff,      0.0]
                      yaw=desired_heading

The lookahead projects the vehicle onto a fixed path line (latched at
AWAITING_FW_TRANSITION entry) and places the target a configurable distance ahead
of that projection, correcting cross-track drift without a separate PID.
"""

import math
from enum import IntEnum

import rclpy
from rclpy.node import Node
from rclpy.qos import DurabilityPolicy, HistoryPolicy, QoSProfile, ReliabilityPolicy
from std_msgs.msg import Empty, String

from px4_msgs.msg import (
    AirspeedValidated,
    OffboardControlMode,
    TrajectorySetpoint,
    VehicleCommand,
    VehicleLocalPosition,
    VehicleStatus,
    VtolVehicleStatus,
)


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------

class FlightState(IntEnum):
    WARMUP_SETPOINTS      = 0
    REQUEST_OFFBOARD_ARM  = 1
    MC_TAKEOFF            = 2
    MC_HOVER              = 3
    ALIGN_YAW_FOR_FW      = 4   # hold position, rotate yaw to FW heading
    PRE_TRANSITION_ACCEL  = 5   # accelerate forward in MC until airspeed gate met
    AWAITING_FW_TRANSITION = 6  # cmd sent once, MC hover hold, wait for vtol=FW
    FW_EXECUTE            = 7   # pure FW straight-line cruise
    REQUEST_VTOL_MC       = 8   # back-transition: keep FW setpoint until vtol=MC
    LANDING               = 9


class VtolStateMachine(Node):

    # -----------------------------------------------------------------------
    # Construction
    # -----------------------------------------------------------------------

    def __init__(self) -> None:
        super().__init__("vtol_sm")

        # ---- ROS parameters -----------------------------------------------
        self.fmu_prefix = self.declare_parameter("fmu_prefix", "").value
        self.target_system = int(self.declare_parameter("target_system", 1).value)
        self.target_component = int(self.declare_parameter("target_component", 1).value)

        # Topics
        self.command_topic = str(self.declare_parameter("command_topic", "command").value)
        self.request_fw_topic = str(self.declare_parameter("request_fw_topic", "request_fw").value)
        self.request_mc_topic = str(self.declare_parameter("request_mc_topic", "request_mc").value)
        self.land_topic = str(self.declare_parameter("land_topic", "land_request").value)

        # MC parameters
        self.takeoff_height_m = float(self.declare_parameter("takeoff_height_m", 30.0).value)
        self.hover_yaw_rad = float(self.declare_parameter("hover_yaw_rad", 0.0).value)
        self.setpoint_rate_hz = float(self.declare_parameter("setpoint_rate_hz", 20.0).value)
        self.offboard_warmup_cycles = int(self.declare_parameter("offboard_warmup_cycles", 20).value)
        self.takeoff_reached_tol_m = float(self.declare_parameter("takeoff_reached_tol_m", 0.5).value)
        self.yaw_align_tol_deg = float(self.declare_parameter("yaw_align_tol_deg", 10.0).value)

        # FW parameters
        # NOTE: fw_alt_m is independent of takeoff_height_m — never link them.
        self.fw_heading_deg = float(self.declare_parameter("fw_heading_deg", 0.0).value)
        self.fw_speed_mps = float(self.declare_parameter("fw_speed_mps", 15.0).value)
        self.fw_alt_m = float(self.declare_parameter("fw_alt_m", 30.0).value)
        self.fw_lookahead_m = float(self.declare_parameter("fw_lookahead_m", 80.0).value)
        self.fw_min_arsp_gate_mps = float(self.declare_parameter("fw_min_arsp_gate_mps", 0.0).value)

        # Loitering parameters
        self.loiter = bool(self.declare_parameter("loiter", False).value)
        self.loiter_radius_m = float(self.declare_parameter("loiter_radius_m", 20.0).value)
        self.loiter_lookahead_m = float(self.declare_parameter("loiter_lookahead_m", 10.0).value)

        # Debug
        self.debug_enabled = bool(self.declare_parameter("debug_enabled", False).value)
        self.debug_topic = str(self.declare_parameter("debug_topic", "offboard_debug").value)

        # Safety warnings
        if self.takeoff_height_m < 20.0:
            self.get_logger().warn(
                f"takeoff_height_m={self.takeoff_height_m:.1f} m is below 20 m. "
                "A VTOL transition needs at least 20-30 m AGL for safe quad-chute recovery."
            )
        if self.fw_alt_m < 20.0:
            self.get_logger().warn(
                f"fw_alt_m={self.fw_alt_m:.1f} m is below 20 m. FW at very low altitude is unsafe."
            )

        # ---- Internal state -----------------------------------------------
        self.state = FlightState.WARMUP_SETPOINTS
        self.warmup_counter = 0

        # MC setpoint targets (NED: up = negative Z)
        self.takeoff_x = 0.0
        self.takeoff_y = 0.0
        self.takeoff_z = -abs(self.takeoff_height_m)

        self.mc_x = 0.0
        self.mc_y = 0.0
        self.mc_z = self.takeoff_z
        self.mc_yaw = self.hover_yaw_rad

        # FW straight-line path (latched at AWAITING_FW_TRANSITION entry)
        self.fw_path_origin_x = 0.0
        self.fw_path_origin_y = 0.0
        self.fw_path_heading_rad = 0.0

        # FW loitering state (TB latched)
        self.loiter_center_x = 0.0
        self.loiter_center_y = 0.0
        self.loiter_direction = 1  # 1 = CW, -1 = CCW

        # Transition bookkeeping
        self.vtol_fw_sent = False
        self.vtol_mc_sent = False
        self.landing_sent = False

        # Async command flags (set by callbacks, consumed once per timer tick)
        self.pending_fw = False
        self.pending_mc = False
        self.pending_land = False

        # PX4 telemetry
        self.vehicle_local_position = VehicleLocalPosition()
        self.vehicle_status = VehicleStatus()
        self.vtol_vehicle_status = VtolVehicleStatus()
        self.airspeed_validated = AirspeedValidated()

        self.debug_seq = 0

        # ---- QoS ----------------------------------------------------------
        px4_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )

        # ---- Publishers ---------------------------------------------------
        self.offboard_mode_pub = self.create_publisher(OffboardControlMode, self._topic("/fmu/in/offboard_control_mode"), px4_qos)
        self.trajectory_sp_pub = self.create_publisher(TrajectorySetpoint, self._topic("/fmu/in/trajectory_setpoint"), px4_qos)
        self.vehicle_command_pub = self.create_publisher(VehicleCommand, self._topic("/fmu/in/vehicle_command"), px4_qos)
        self.debug_pub = self.create_publisher(String, self.debug_topic, 10) if self.debug_enabled else None

        # ---- Subscribers --------------------------------------------------
        self.create_subscription(VehicleLocalPosition, self._topic("/fmu/out/vehicle_local_position"), self._local_pos_cb, px4_qos)
        self.create_subscription(VehicleStatus,        self._topic("/fmu/out/vehicle_status_v1"),       self._status_cb,    px4_qos)
        self.create_subscription(VtolVehicleStatus,    self._topic("/fmu/out/vtol_vehicle_status"),     self._vtol_cb,      px4_qos)
        self.create_subscription(AirspeedValidated,    self._topic("/fmu/out/airspeed_validated"),      self._airspeed_cb,  px4_qos)
        self.create_subscription(String, self.command_topic, self._command_cb, 10)
        self.create_subscription(Empty, self.request_fw_topic, self._req_fw_cb, 10)
        self.create_subscription(Empty, self.request_mc_topic, self._req_mc_cb, 10)
        self.create_subscription(Empty, self.land_topic, self._land_cb, 10)

        period_s = 1.0 / max(self.setpoint_rate_hz, 1.0)
        self.create_timer(period_s, self._timer_cb)

        self.get_logger().info("VTOL offboard state machine started")

    # -----------------------------------------------------------------------
    # Helpers
    # -----------------------------------------------------------------------

    def _topic(self, suffix: str) -> str:
        prefix = self.fmu_prefix.strip()
        if prefix and not prefix.startswith("/"):
            prefix = "/" + prefix
        return f"{prefix}{suffix}"

    def _latch_hover(self) -> None:
        self.mc_x = self.vehicle_local_position.x
        self.mc_y = self.vehicle_local_position.y
        self.mc_z = self.vehicle_local_position.z
        self.mc_yaw = self.vehicle_local_position.heading
        self.get_logger().info(
            f"Hover target latched: x={self.mc_x:.2f} y={self.mc_y:.2f} "
            f"z={self.mc_z:.2f} yaw={math.degrees(self.mc_yaw):.1f} deg"
        )

    @staticmethod
    def _wrap_pi(a: float) -> float:
        return (a + math.pi) % (2.0 * math.pi) - math.pi

    def _is_vtol_mc(self) -> bool:
        return self.vtol_vehicle_status.vehicle_vtol_state == VtolVehicleStatus.VEHICLE_VTOL_STATE_MC

    def _is_vtol_fw(self) -> bool:
        return self.vtol_vehicle_status.vehicle_vtol_state == VtolVehicleStatus.VEHICLE_VTOL_STATE_FW

    def _is_offboard(self) -> bool:
        return self.vehicle_status.nav_state == VehicleStatus.NAVIGATION_STATE_OFFBOARD

    def _takeoff_reached(self) -> bool:
        return abs(self.vehicle_local_position.z - self.takeoff_z) <= self.takeoff_reached_tol_m

    def _yaw_aligned(self) -> bool:
        heading = self.vehicle_local_position.heading
        if not math.isfinite(heading):
            return True  # skip alignment if heading unavailable
        return abs(self._wrap_pi(heading - self.mc_yaw)) <= math.radians(self.yaw_align_tol_deg)

    # -----------------------------------------------------------------------
    # State transitions
    # -----------------------------------------------------------------------

    def _set_state(self, new: FlightState) -> None:
        if self.state == new:
            return
        self.get_logger().info(f"State: {self.state.name} -> {new.name}")
        self.state = new

        if new == FlightState.MC_HOVER:
            self._latch_hover()

        elif new == FlightState.ALIGN_YAW_FOR_FW:
            # Hold current XYZ, set desired yaw = FW heading
            self._latch_hover()
            if self.loiter:
                self.loiter_center_x = self.mc_x
                self.loiter_center_y = self.mc_y

            self.mc_yaw = math.radians(self.fw_heading_deg)
            self.get_logger().info(f"Aligning yaw to {self.fw_heading_deg:.1f} deg")

        elif new == FlightState.PRE_TRANSITION_ACCEL:
            # Latch the path origin here — the straight line starts from
            # wherever the vehicle is when forward acceleration begins.
            self.fw_path_origin_x = self.vehicle_local_position.x
            self.fw_path_origin_y = self.vehicle_local_position.y
            self.fw_path_heading_rad = math.radians(self.fw_heading_deg)
            self.get_logger().info(
                f"Pre-transition acceleration started — "
                f"path origin: ({self.fw_path_origin_x:.1f}, {self.fw_path_origin_y:.1f}), "
                f"heading: {self.fw_heading_deg:.1f} deg, "
                f"airspeed gate: {self.fw_min_arsp_gate_mps:.1f} m/s"
            )

        elif new == FlightState.AWAITING_FW_TRANSITION:
            # Latch hover target so the MC hold reference is stable.
            # Path origin was already latched in PRE_TRANSITION_ACCEL.
            # If arriving here without pre-accel, latch path origin now.
            if self.fw_path_origin_x == 0.0 and self.fw_path_origin_y == 0.0:
                self.fw_path_origin_x = self.vehicle_local_position.x
                self.fw_path_origin_y = self.vehicle_local_position.y
                self.fw_path_heading_rad = math.radians(self.fw_heading_deg)
            self._latch_hover()
            self.vtol_fw_sent = False
            self.get_logger().info(
                f"Awaiting FW transition — MC hold at "
                f"({self.mc_x:.1f}, {self.mc_y:.1f}, {self.mc_z:.1f}), "
                f"path heading: {self.fw_heading_deg:.1f} deg"
            )

        elif new == FlightState.REQUEST_VTOL_MC:
            self.vtol_mc_sent = False

        elif new == FlightState.LANDING:
            self.landing_sent = False

    # -----------------------------------------------------------------------
    # Subscribers
    # -----------------------------------------------------------------------

    def _local_pos_cb(self, msg: VehicleLocalPosition) -> None:
        self.vehicle_local_position = msg

    def _airspeed_cb(self, msg: AirspeedValidated) -> None:
        self.airspeed_validated = msg

    def _status_cb(self, msg: VehicleStatus) -> None:
        self.vehicle_status = msg

    def _vtol_cb(self, msg: VtolVehicleStatus) -> None:
        self.vtol_vehicle_status = msg

    def _land_cb(self, _: Empty) -> None:
        self.pending_land = True

    def _req_fw_cb(self, _: Empty) -> None:
        self.get_logger().warn("FW setpoint request received!")
        self.pending_fw = True

    def _req_mc_cb(self, _: Empty) -> None:
        self.pending_mc = True

    def _command_cb(self, msg: String) -> None:
        raw = msg.data.strip()
        if not raw.upper().startswith("FW_SET"):
            self.get_logger().warn(f"Unknown command: '{raw}'")
            return
        parsed = self._parse_fw_set(raw)
        if parsed is None:
            self.get_logger().warn(
                f"Invalid FW_SET: '{raw}' -- expected: FW_SET HDG=<deg> SPD=<m/s> ALT=<m>"
            )
            return
        self.fw_heading_deg = parsed["heading_deg"]
        self.fw_speed_mps = parsed["speed_mps"]
        self.fw_alt_m = parsed["alt_m"]
        self.get_logger().info(
            f"FW params updated: hdg={self.fw_heading_deg:.1f} deg "
            f"spd={self.fw_speed_mps:.1f} m/s alt={self.fw_alt_m:.1f} m"
        )

    @staticmethod
    def _parse_fw_set(text: str):
        parts = text.strip().split()
        if len(parts) < 4:
            return None
        kv = {}
        for token in parts[1:]:
            if "=" in token:
                k, v = token.split("=", 1)
                kv[k.upper()] = v
        try:
            return {
                "heading_deg": float(kv["HDG"]),
                "speed_mps": float(kv["SPD"]),
                "alt_m": abs(float(kv["ALT"])),
            }
        except (KeyError, ValueError):
            return None

    # -----------------------------------------------------------------------
    # Publishers
    # -----------------------------------------------------------------------

    def _heartbeat(self, position_ctrl: bool = True, velocity_ctrl: bool = True) -> None:
        """
        Offboard mode requires a non-empty stream of OffboardControlMode messages.
        This is the "heartbeat" that keeps offboard mode alive and indicates
        which control modes we're using (position, velocity, etc).
        """
        msg = OffboardControlMode()
        msg.position = position_ctrl
        msg.velocity = velocity_ctrl
        msg.acceleration = False
        msg.attitude = False
        msg.body_rate = False
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.offboard_mode_pub.publish(msg)

    def _pub_mc_setpoint(self, x: float, y: float, z: float, yaw: float) -> None:
        msg = TrajectorySetpoint()
        msg.position = [x, y, z]
        msg.velocity = [float("nan")] * 3
        msg.yaw = yaw
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.trajectory_sp_pub.publish(msg)

    def _pursue_target_xy(self, target_x: float, target_y: float) -> tuple[float, float, float]:
        """Pure-pursuit velocity and yaw toward an arbitrary XY lookahead target."""
        cx = self.vehicle_local_position.x
        cy = self.vehicle_local_position.y
        err_x = target_x - cx
        err_y = target_y - cy
        err_norm = math.hypot(err_x, err_y)

        if err_norm > 1e-6:
            ux = err_x / err_norm
            uy = err_y / err_norm
        else:
            ux = math.cos(self.fw_path_heading_rad)
            uy = math.sin(self.fw_path_heading_rad)

        vx = self.fw_speed_mps * ux
        vy = self.fw_speed_mps * uy
        desired_heading = math.atan2(uy, ux)
        return vx, vy, desired_heading

    def _loiter_target_xy(self) -> tuple[float, float]:
        """Continuous circle lookahead target (analytic, no discrete waypoint hopping)."""
        radius = self.loiter_radius_m
        if radius <= 1e-6:
            return self.loiter_center_x, self.loiter_center_y

        cx = self.vehicle_local_position.x
        cy = self.vehicle_local_position.y

        theta = math.atan2(cy - self.loiter_center_y, cx - self.loiter_center_x)
        delta_theta = self.loiter_lookahead_m / radius
        step_sign = -1.0 if self.loiter_direction == 1 else 1.0
        theta_target = theta + step_sign * delta_theta

        target_x = self.loiter_center_x + radius * math.cos(theta_target)
        target_y = self.loiter_center_y + radius * math.sin(theta_target)
        return target_x, target_y

    def _fw_target_xy(self) -> tuple[float, float]:
        """Straight-line lookahead target in XY."""
        cx = self.vehicle_local_position.x
        cy = self.vehicle_local_position.y

        hx = math.cos(self.fw_path_heading_rad)
        hy = math.sin(self.fw_path_heading_rad)

        # Project current position onto the path line
        dx = cx - self.fw_path_origin_x
        dy = cy - self.fw_path_origin_y
        along_track = dx * hx + dy * hy

        # Lookahead point is along_track + lookahead_m ahead on the line
        lx = self.fw_path_origin_x + (along_track + self.fw_lookahead_m) * hx
        ly = self.fw_path_origin_y + (along_track + self.fw_lookahead_m) * hy

        return lx, ly

    def _pub_fw_setpoint(self) -> None:
        """
        FW straight-line setpoint with finite XYZ position and velocity feed-forward.

        PX4 v1.16.1 fixed-wing offboard requires fully finite position for a valid
        altitude setpoint in TECS, so publish [x, y, z] all finite.
        """
        if self.loiter and self.state == FlightState.FW_EXECUTE:
            target_x, target_y = self._loiter_target_xy()
        else:
            target_x, target_y = self._fw_target_xy()

        vx, vy, desired_heading = self._pursue_target_xy(target_x, target_y)
        z_target = -abs(self.fw_alt_m)

        msg = TrajectorySetpoint()
        msg.position = [target_x, target_y, z_target]
        msg.velocity = [vx, vy, 0.0]
        msg.acceleration = [float("nan"), float("nan"), float("nan")]
        msg.jerk = [float("nan"), float("nan"), float("nan")]
        msg.yaw = desired_heading
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.trajectory_sp_pub.publish(msg)

    def _pub_vehicle_cmd(self, command: int, **params: float) -> None:
        msg = VehicleCommand()
        msg.command = command
        msg.param1 = float(params.get("param1", 0.0))
        msg.param2 = float(params.get("param2", 0.0))
        msg.param3 = float(params.get("param3", 0.0))
        msg.param4 = float(params.get("param4", 0.0))
        msg.param5 = float(params.get("param5", 0.0))
        msg.param6 = float(params.get("param6", 0.0))
        msg.param7 = float(params.get("param7", 0.0))
        msg.target_system = self.target_system
        msg.target_component = self.target_component
        msg.source_system = 1
        msg.source_component = 1
        msg.from_external = True
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.vehicle_command_pub.publish(msg)

    def _engage_offboard(self) -> None:
        self._pub_vehicle_cmd(VehicleCommand.VEHICLE_CMD_DO_SET_MODE, param1=1.0, param2=6.0)
        self.get_logger().info("Offboard mode requested")

    def _arm(self) -> None:
        self._pub_vehicle_cmd(VehicleCommand.VEHICLE_CMD_COMPONENT_ARM_DISARM, param1=1.0)
        self.get_logger().info("Arm requested")

    def _cmd_transition_fw(self) -> None:
        self._pub_vehicle_cmd(VehicleCommand.VEHICLE_CMD_DO_VTOL_TRANSITION, param1=4.0)
        self.get_logger().info("VTOL -> FW transition command sent")

    def _cmd_transition_mc(self) -> None:
        self._pub_vehicle_cmd(VehicleCommand.VEHICLE_CMD_DO_VTOL_TRANSITION, param1=3.0)
        self.get_logger().info("VTOL -> MC transition command sent")

    def _cmd_land(self) -> None:
        self._heartbeat(position_ctrl=True, velocity_ctrl=False)
        self._pub_vehicle_cmd(VehicleCommand.VEHICLE_CMD_NAV_LAND)
        self.get_logger().info("Land command sent")

    # -----------------------------------------------------------------------
    # Debug
    # -----------------------------------------------------------------------

    def _dbg(self, extra: str = "") -> None:
        if not self.debug_enabled or self.debug_pub is None:
            return
        p = self.vehicle_local_position
        hdg = math.degrees(p.heading) if math.isfinite(p.heading) else float("nan")
        tas = self.airspeed_validated.true_airspeed_m_s
        ias = self.airspeed_validated.indicated_airspeed_m_s
        msg = String()
        msg.data = (
            f"seq={self.debug_seq} state={self.state.name} "
            f"nav={int(self.vehicle_status.nav_state)} "
            f"vtol={int(self.vtol_vehicle_status.vehicle_vtol_state)} "
            f"pos=({p.x:.1f},{p.y:.1f},{p.z:.1f}) hdg={hdg:.1f} "
            f"tas={tas:.1f} ias={ias:.1f} "
            f"fw_hdg={self.fw_heading_deg:.1f} fw_spd={self.fw_speed_mps:.1f} fw_alt={self.fw_alt_m:.1f}"
            + (f" {extra}" if extra else "")
        )
        self.debug_pub.publish(msg)
        self.debug_seq += 1

    # -----------------------------------------------------------------------
    # Main timer loop
    # -----------------------------------------------------------------------

    def _timer_cb(self) -> None:
        # Consume flags atomically (each acted on at most once per tick)
        want_fw = self.pending_fw;   self.pending_fw = False
        want_mc = self.pending_mc;   self.pending_mc = False
        want_land = self.pending_land; self.pending_land = False

        # Landing is highest priority
        if want_land and self.state != FlightState.LANDING:
            self._set_state(FlightState.LANDING)

        # ------------------------------------------------------------------
        # WARMUP: stream setpoints before arming so PX4 accepts offboard mode
        # ------------------------------------------------------------------
        if self.state == FlightState.WARMUP_SETPOINTS:
            if not self._is_vtol_mc():
                self._heartbeat(position_ctrl=True, velocity_ctrl=False)
                self._pub_mc_setpoint(self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad)
                self._cmd_transition_mc()
                return
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad)
            self.warmup_counter += 1
            if self.warmup_counter >= self.offboard_warmup_cycles:
                self._set_state(FlightState.REQUEST_OFFBOARD_ARM)
            return

        # ------------------------------------------------------------------
        # ARM + OFFBOARD
        # ------------------------------------------------------------------
        if self.state == FlightState.REQUEST_OFFBOARD_ARM:
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad)
            self._engage_offboard()
            self._arm()
            self._set_state(FlightState.MC_TAKEOFF)
            return

        # ------------------------------------------------------------------
        # MC TAKEOFF
        # ------------------------------------------------------------------
        if self.state == FlightState.MC_TAKEOFF:
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad)
            if self._is_offboard() and self._takeoff_reached():
                self._set_state(FlightState.MC_HOVER)
            return

        # ------------------------------------------------------------------
        # MC HOVER
        # ------------------------------------------------------------------
        if self.state == FlightState.MC_HOVER:
            # Safety net: if we somehow land here while the vehicle is physically
            # in FW mode (e.g. PX4 completed a transition after retries exhausted),
            # recover immediately rather than publishing MC setpoints to a fixed-wing.
            if self._is_vtol_fw():
                self.get_logger().warn(
                    "MC_HOVER entered but vehicle is in FW mode — recovering to FW_EXECUTE"
                )
                self._set_state(FlightState.FW_EXECUTE)
                return
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.mc_x, self.mc_y, self.mc_z, self.mc_yaw)
            if want_fw:
                self._set_state(FlightState.ALIGN_YAW_FOR_FW)
            self._dbg()
            return

        # ------------------------------------------------------------------
        # ALIGN YAW: hold XYZ, rotate toward FW heading before transitioning
        # ------------------------------------------------------------------
        if self.state == FlightState.ALIGN_YAW_FOR_FW:
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.mc_x, self.mc_y, self.mc_z, self.mc_yaw)
            if want_mc:
                self._set_state(FlightState.MC_HOVER)
                return
            heading = self.vehicle_local_position.heading
            yaw_err_deg = (
                math.degrees(abs(self._wrap_pi(heading - self.mc_yaw)))
                if math.isfinite(heading) else 0.0
            )
            self._dbg(f"yaw_err={yaw_err_deg:.1f}deg")
            if self._yaw_aligned():
                if self.fw_min_arsp_gate_mps > 0.0:
                    self._set_state(FlightState.PRE_TRANSITION_ACCEL)
                else:
                    self._set_state(FlightState.AWAITING_FW_TRANSITION)
            return

        # ------------------------------------------------------------------
        # PRE-TRANSITION ACCEL: push forward velocity in MC until airspeed gate met
        # ------------------------------------------------------------------
        if self.state == FlightState.PRE_TRANSITION_ACCEL:
            self._heartbeat(position_ctrl=True, velocity_ctrl=True)
            self._pub_fw_setpoint()  # forward velocity in MC — builds airspeed

            if want_mc:
                self._set_state(FlightState.MC_HOVER)
                return

            tas = self.airspeed_validated.true_airspeed_m_s
            arsp_valid = (
                math.isfinite(tas)
                and tas >= self.fw_min_arsp_gate_mps
            )
            self._dbg(f"tas={tas:.1f} gate={self.fw_min_arsp_gate_mps:.1f} ready={int(arsp_valid)}")

            if arsp_valid:
                self._set_state(FlightState.AWAITING_FW_TRANSITION)
            return

        # ------------------------------------------------------------------
        # AWAITING_FW_TRANSITION
        #
        # The transition command is sent exactly once on entry. After that
        # we publish a neutral MC hover setpoint and let PX4's internal
        # transition controller do its job without interference.
        #
        # Key insight from testing: pushing a forward velocity setpoint
        # during the transition fights PX4's own transition sequence.
        # A static MC hold reference is the least-conflicting input and
        # allows PX4 to complete the transition reliably on its own.
        # ------------------------------------------------------------------
        if self.state == FlightState.AWAITING_FW_TRANSITION:
            self._heartbeat(position_ctrl=True, velocity_ctrl=False)
            self._pub_mc_setpoint(self.mc_x, self.mc_y, self.mc_z, self.mc_yaw)

            if not self.vtol_fw_sent:
                self._cmd_transition_fw()
                self.vtol_fw_sent = True
                self.get_logger().info("FW transition command sent, awaiting completion...")
                self._dbg("action=fw_cmd_sent")

            if want_mc:
                self._set_state(FlightState.MC_HOVER)
                return

            if self._is_vtol_fw():
                self.get_logger().info("FW transition complete!")
                self._dbg("action=fw_transition_complete")
                self._set_state(FlightState.FW_EXECUTE)
                self.fw_alt_m = max(0.0, -self.vehicle_local_position.z)
            else:
                self._dbg(f"waiting_vtol_fw")
            return

        # ------------------------------------------------------------------
        # FW EXECUTE: steady straight-line cruise
        # ------------------------------------------------------------------
        if self.state == FlightState.FW_EXECUTE:
            self._heartbeat(position_ctrl=True, velocity_ctrl=True)
            self._pub_fw_setpoint()
            self._dbg()
            if want_mc:
                self._set_state(FlightState.REQUEST_VTOL_MC)
            return

        # ------------------------------------------------------------------
        # REQUEST VTOL MC: back-transition, keep FW setpoint alive
        # ------------------------------------------------------------------
        if self.state == FlightState.REQUEST_VTOL_MC:
            self._heartbeat(position_ctrl=True, velocity_ctrl=True)
            self._pub_fw_setpoint()   # keep airspeed valid during back-transition

            if not self.vtol_mc_sent:
                self._cmd_transition_mc()
                self.vtol_mc_sent = True
                self._dbg("action=mc_cmd_sent")

            if self._is_vtol_mc():
                self._dbg("action=mc_transition_complete")
                self._set_state(FlightState.MC_HOVER)
            return

        # ------------------------------------------------------------------
        # LANDING
        # ------------------------------------------------------------------
        if self.state == FlightState.LANDING:
            if not self.landing_sent:
                self._cmd_land()
                self.landing_sent = True
            rclpy.shutdown()


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main(args=None) -> None:
    node = None
    try:
        rclpy.init(args=args)
        node = VtolStateMachine()
        rclpy.spin(node)
    except KeyboardInterrupt:
        if node is not None and node.state != FlightState.LANDING and rclpy.ok():
            node.get_logger().info("KeyboardInterrupt -- sending land command")
            try:
                node._cmd_land()
            except Exception as exc:
                node.get_logger().warn(f"Could not send land on shutdown: {exc}")
    finally:
        if node is not None:
            node.destroy_node()
        if rclpy.ok():
            rclpy.shutdown()


if __name__ == "__main__":
    main()
