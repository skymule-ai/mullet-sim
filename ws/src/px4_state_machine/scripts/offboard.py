#!/usr/bin/env python3

import math
from enum import IntEnum

import rclpy
from rclpy.node import Node
from rclpy.qos import DurabilityPolicy, HistoryPolicy, QoSProfile, ReliabilityPolicy
from std_msgs.msg import Empty, String

from px4_msgs.msg import (
    OffboardControlMode,
    TrajectorySetpoint,
    VehicleCommand,
    VehicleLocalPosition,
    VehicleStatus,
    VtolVehicleStatus,
)


class FlightState(IntEnum):
    WARMUP_SETPOINTS = 0
    REQUEST_OFFBOARD_ARM = 1
    MC_TAKEOFF = 2
    MC_HOVER = 3
    REQUEST_VTOL_FW = 4
    FW_EXECUTE = 5
    REQUEST_VTOL_MC = 6
    LANDING = 7


class VtolStateMachine(Node):
    def __init__(self) -> None:
        super().__init__("vtol_sm")

        self.fmu_prefix = self.declare_parameter("fmu_prefix", "").value
        self.takeoff_height_m = float(self.declare_parameter("takeoff_height_m", 5.0).value)
        self.hover_yaw_rad = float(self.declare_parameter("hover_yaw_rad", 0.0).value)
        self.setpoint_rate_hz = float(self.declare_parameter("setpoint_rate_hz", 10.0).value)
        self.offboard_warmup_cycles = int(self.declare_parameter("offboard_warmup_cycles", 10).value)
        self.takeoff_reached_tol_m = float(self.declare_parameter("takeoff_reached_tol_m", 0.3).value)
        self.target_system = int(self.declare_parameter("target_system", 1).value)
        self.target_component = int(self.declare_parameter("target_component", 1).value)
        self.command_topic = str(self.declare_parameter("command_topic", "command").value)
        self.land_topic = str(self.declare_parameter("land_topic", "land_request").value)

        self.state = FlightState.WARMUP_SETPOINTS
        self.warmup_counter = 0
        self.landing_command_sent = False
        self.vtol_fw_sent = False
        self.vtol_mc_sent = False

        self.takeoff_x = 0.0
        self.takeoff_y = 0.0
        self.takeoff_z = -abs(self.takeoff_height_m)

        self.mc_x = 0.0
        self.mc_y = 0.0
        self.mc_z = self.takeoff_z
        self.mc_yaw = self.hover_yaw_rad

        self.fw_heading_deg = 0.0
        self.fw_speed_mps = 15.0
        self.fw_alt_m = abs(self.takeoff_height_m)

        self.has_fresh_fw_cmd = False
        self.pending_land = False
        self.last_valid_command_time = None

        self.vehicle_local_position = VehicleLocalPosition()
        self.vehicle_status = VehicleStatus()
        self.vtol_vehicle_status = VtolVehicleStatus()

        px4_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )

        self.offboard_mode_pub = self.create_publisher(
            OffboardControlMode, self._topic("/fmu/in/offboard_control_mode"), px4_qos
        )
        self.trajectory_setpoint_pub = self.create_publisher(
            TrajectorySetpoint, self._topic("/fmu/in/trajectory_setpoint"), px4_qos
        )
        self.vehicle_command_pub = self.create_publisher(
            VehicleCommand, self._topic("/fmu/in/vehicle_command"), px4_qos
        )

        self.create_subscription(
            VehicleLocalPosition,
            self._topic("/fmu/out/vehicle_local_position"),
            self._vehicle_local_position_cb,
            px4_qos,
        )
        self.create_subscription(
            VehicleStatus,
            self._topic("/fmu/out/vehicle_status_v1"),
            self._vehicle_status_cb,
            px4_qos,
        )
        self.create_subscription(
            VtolVehicleStatus,
            self._topic("/fmu/out/vtol_vehicle_status"),
            self._vtol_vehicle_status_cb,
            px4_qos,
        )
        self.create_subscription(String, self.command_topic, self._command_cb, 10)
        self.create_subscription(Empty, self.land_topic, self._land_cb, 10)

        period_s = 1.0 / max(self.setpoint_rate_hz, 1.0)
        self.timer = self.create_timer(period_s, self._timer_cb)

        self.get_logger().info("VTOL offboard state machine started")

    def _topic(self, suffix: str) -> str:
        prefix = self.fmu_prefix.strip()
        if prefix and not prefix.startswith("/"):
            prefix = "/" + prefix
        return f"{prefix}{suffix}"

    def _set_state(self, state: FlightState) -> None:
        if self.state != state:
            self.get_logger().info(f"State: {self.state.name} -> {state.name}")
            self.state = state
            if state == FlightState.MC_HOVER:
                self._latch_mc_hover_target()
            if state == FlightState.REQUEST_VTOL_FW:
                self.vtol_fw_sent = False
            if state == FlightState.REQUEST_VTOL_MC:
                self.vtol_mc_sent = False
            if state == FlightState.LANDING:
                self.landing_command_sent = False

    def _latch_mc_hover_target(self) -> None:
        self.mc_x = self.vehicle_local_position.x
        self.mc_y = self.vehicle_local_position.y
        self.mc_z = self.vehicle_local_position.z
        self.mc_yaw = self.vehicle_local_position.heading
        self.get_logger().info(
            f"Latched MC hover target x={self.mc_x:.2f} y={self.mc_y:.2f} z={self.mc_z:.2f} yaw={self.mc_yaw:.2f}"
        )

    def _vehicle_local_position_cb(self, msg: VehicleLocalPosition) -> None:
        self.vehicle_local_position = msg

    def _vehicle_status_cb(self, msg: VehicleStatus) -> None:
        self.vehicle_status = msg

    def _vtol_vehicle_status_cb(self, msg: VtolVehicleStatus) -> None:
        self.vtol_vehicle_status = msg

    def _land_cb(self, _: Empty) -> None:
        self.pending_land = True
        self.last_valid_command_time = self.get_clock().now().nanoseconds

    def _parse_fw_set(self, text: str):
        parts = text.strip().split()
        if len(parts) < 4 or parts[0].upper() != "FW_SET":
            return None

        values = {}
        for token in parts[1:]:
            if "=" not in token:
                continue
            key, value = token.split("=", 1)
            values[key.upper()] = value

        try:
            heading = float(values["HDG"])
            speed = float(values["SPD"])
            alt = float(values["ALT"])
        except (KeyError, ValueError):
            return None

        return {"heading_deg": heading, "speed_mps": speed, "alt_m": abs(alt)}

    def _command_cb(self, msg: String) -> None:
        raw = msg.data.strip()
        upper = raw.upper()

        if upper == "FW_HOLD":
            self.has_fresh_fw_cmd = True
        elif upper.startswith("FW_SET"):
            parsed = self._parse_fw_set(raw)
            if parsed is None:
                self.get_logger().warn(
                    f"Invalid FW_SET command '{raw}'. Expected: FW_SET HDG=<deg> SPD=<mps> ALT=<m>"
                )
                return
            self.fw_heading_deg = parsed["heading_deg"]
            self.fw_speed_mps = parsed["speed_mps"]
            self.fw_alt_m = parsed["alt_m"]
            self.has_fresh_fw_cmd = True
            self.get_logger().info(
                f"FW target set hdg={self.fw_heading_deg:.1f} spd={self.fw_speed_mps:.1f} alt={self.fw_alt_m:.1f}"
            )
        else:
            self.get_logger().warn(f"Ignoring invalid command: '{raw}'")
            return

        self.last_valid_command_time = self.get_clock().now().nanoseconds

    def _publish_offboard_heartbeat(self) -> None:
        msg = OffboardControlMode()
        msg.position = True
        msg.velocity = True
        msg.acceleration = False
        msg.attitude = False
        msg.body_rate = False
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.offboard_mode_pub.publish(msg)

    def _publish_mc_position_setpoint(self, x: float, y: float, z: float, yaw: float) -> None:
        msg = TrajectorySetpoint()
        msg.position = [x, y, z]
        msg.velocity = [float("nan"), float("nan"), float("nan")]
        msg.yaw = yaw
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.trajectory_setpoint_pub.publish(msg)

    def _publish_fw_setpoint(self) -> None:
        heading_rad = math.radians(self.fw_heading_deg)
        vx = self.fw_speed_mps * math.cos(heading_rad)
        vy = self.fw_speed_mps * math.sin(heading_rad)
        z_target = -abs(self.fw_alt_m)

        msg = TrajectorySetpoint()
        msg.position = [float("nan"), float("nan"), z_target]
        msg.velocity = [vx, vy, 0.0]
        msg.yaw = heading_rad
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.trajectory_setpoint_pub.publish(msg)

    def _publish_vehicle_command(self, command: int, **params: float) -> None:
        msg = VehicleCommand()
        msg.command = command
        msg.param1 = params.get("param1", 0.0)
        msg.param2 = params.get("param2", 0.0)
        msg.param3 = params.get("param3", 0.0)
        msg.param4 = params.get("param4", 0.0)
        msg.param5 = params.get("param5", 0.0)
        msg.param6 = params.get("param6", 0.0)
        msg.param7 = params.get("param7", 0.0)
        msg.target_system = self.target_system
        msg.target_component = self.target_component
        msg.source_system = 1
        msg.source_component = 1
        msg.from_external = True
        msg.timestamp = int(self.get_clock().now().nanoseconds / 1000)
        self.vehicle_command_pub.publish(msg)

    def _engage_offboard_mode(self) -> None:
        self._publish_vehicle_command(
            VehicleCommand.VEHICLE_CMD_DO_SET_MODE, param1=1.0, param2=6.0
        )
        self.get_logger().info("Sent offboard mode command")

    def _arm(self) -> None:
        self._publish_vehicle_command(
            VehicleCommand.VEHICLE_CMD_COMPONENT_ARM_DISARM, param1=1.0
        )
        self.get_logger().info("Sent arm command")

    def _transition_to_fw(self) -> None:
        self._publish_vehicle_command(
            VehicleCommand.VEHICLE_CMD_DO_VTOL_TRANSITION,
            param1=4.0,
        )
        self.get_logger().info("Sent VTOL transition command (to FW)")

    def _transition_to_mc(self) -> None:
        self._publish_vehicle_command(
            VehicleCommand.VEHICLE_CMD_DO_VTOL_TRANSITION,
            param1=3.0,
        )
        self.get_logger().info("Sent VTOL transition command (to MC)")

    def _land(self) -> None:
        self._publish_vehicle_command(VehicleCommand.VEHICLE_CMD_NAV_LAND)
        self.get_logger().info("Sent land command")

    def _is_offboard(self) -> bool:
        return self.vehicle_status.nav_state == VehicleStatus.NAVIGATION_STATE_OFFBOARD

    def _takeoff_reached(self) -> bool:
        return abs(self.vehicle_local_position.z - self.takeoff_z) <= self.takeoff_reached_tol_m

    def _consume_control_flags(self):
        fresh_fw_cmd = self.has_fresh_fw_cmd
        want_land = self.pending_land

        self.pending_land = False

        return fresh_fw_cmd, want_land

    def _timer_cb(self) -> None:
        self._publish_offboard_heartbeat()

        fresh_fw_cmd, want_land = self._consume_control_flags()

        if want_land:
            # Immediate landing
            self._set_state(FlightState.LANDING)

        if self.state == FlightState.WARMUP_SETPOINTS:
            if self.vtol_vehicle_status.vehicle_vtol_state != VtolVehicleStatus.VEHICLE_VTOL_STATE_MC:
                self._transition_to_mc()
                return

            self._publish_mc_position_setpoint(
                self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad
            )
            self.warmup_counter += 1
            if self.warmup_counter >= self.offboard_warmup_cycles:
                self._set_state(FlightState.REQUEST_OFFBOARD_ARM)
            return

        if self.state == FlightState.REQUEST_OFFBOARD_ARM:
            self._publish_mc_position_setpoint(
                self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad
            )
            self._engage_offboard_mode()
            self._arm()
            self._set_state(FlightState.MC_TAKEOFF)
            return

        if self.state == FlightState.MC_TAKEOFF:
            self._publish_mc_position_setpoint(
                self.takeoff_x, self.takeoff_y, self.takeoff_z, self.hover_yaw_rad
            )
            if self._is_offboard() and self._takeoff_reached():
                self._set_state(FlightState.MC_HOVER)
            return

        if self.state == FlightState.MC_HOVER:
            self._publish_mc_position_setpoint(self.mc_x, self.mc_y, self.mc_z, self.mc_yaw)
            if fresh_fw_cmd:
                self._set_state(FlightState.REQUEST_VTOL_FW)
            return

        if self.state == FlightState.REQUEST_VTOL_FW:
            # Keep commanding MC position during transition initiation.
            self._publish_mc_position_setpoint(self.mc_x, self.mc_y, self.mc_z, self.mc_yaw)
            if not self.vtol_fw_sent:
                self._transition_to_fw()
                self.vtol_fw_sent = True
            if self.vtol_vehicle_status.vehicle_vtol_state in (
                VtolVehicleStatus.VEHICLE_VTOL_STATE_TRANSITION_TO_FW,
                VtolVehicleStatus.VEHICLE_VTOL_STATE_FW,
            ):
                self._set_state(FlightState.FW_EXECUTE)
            return

        if self.state == FlightState.FW_EXECUTE:
            self._publish_fw_setpoint()

            if fresh_fw_cmd:
                # Consume command freshness only after one FW execute cycle.
                self.has_fresh_fw_cmd = False
                return

            # Immediate return to MC if no new FW command was received this cycle.
            self._set_state(FlightState.REQUEST_VTOL_MC)
            return

        if self.state == FlightState.REQUEST_VTOL_MC:
            self._publish_fw_setpoint()
            if not self.vtol_mc_sent:
                self._transition_to_mc()
                self.vtol_mc_sent = True

            if self.vtol_vehicle_status.vehicle_vtol_state == VtolVehicleStatus.VEHICLE_VTOL_STATE_MC:
                self._set_state(FlightState.MC_HOVER)
            return

        if self.state == FlightState.LANDING:
            if not self.landing_command_sent:
                self._land()
                self.landing_command_sent = True
            # Exit
            rclpy.shutdown()


def main(args=None) -> None:
    node = None
    try:
        rclpy.init(args=args)
        node = VtolStateMachine()
        rclpy.spin(node)
    except KeyboardInterrupt:
        if node is not None and node.state != FlightState.LANDING:
            node.get_logger().info("Keyboard interrupt received, landing the vehicle...")
            node._land()
    finally:
        if node is not None:
            node.destroy_node()
        rclpy.shutdown()


if __name__ == "__main__":
    main()
