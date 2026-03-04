#!/usr/bin/env python3
"""
Lawnmower Offboard Hold
=======================
Arms a PX4 rover, switches to offboard mode, and keeps a fixed hold setpoint
alive with an offboard heartbeat.
"""

import math

import rclpy
from rclpy.node import Node
from rclpy.qos import DurabilityPolicy, HistoryPolicy, QoSProfile, ReliabilityPolicy

from px4_msgs.msg import OffboardControlMode, TrajectorySetpoint, VehicleCommand
from px4_msgs.msg import VehicleLocalPosition, VehicleStatus


class LawnmowerOffboard(Node):
    def __init__(self) -> None:
        super().__init__("lawnmower_offboard")

        instance = int(self.declare_parameter("instance", 0).value)
        self.fmu_prefix = "" if instance == 0 else f"/px4_{instance}"
        self.target_system = instance + 1
        self.target_component = 1

        self.setpoint_rate_hz = float(
            self.declare_parameter("setpoint_rate_hz", 20.0).value
        )
        self.offboard_warmup_cycles = int(
            self.declare_parameter("offboard_warmup_cycles", 20).value
        )
        self.hold_yaw_rad = self._coerce_optional_float(
            self.declare_parameter("hold_yaw_rad", float("nan")).value
        )
        self.hold_x = self._coerce_optional_float(
            self.declare_parameter("hold_x", float("nan")).value
        )
        self.hold_y = self._coerce_optional_float(
            self.declare_parameter("hold_y", float("nan")).value
        )
        self.hold_z = self._coerce_optional_float(
            self.declare_parameter("hold_z", float("nan")).value
        )
        self.rearm_interval_s = float(
            self.declare_parameter("rearm_interval_s", 2.0).value
        )

        self._warmup_counter = 0
        self._hold_latched = False
        self._last_arm_request_us = 0
        self._last_offboard_request_us = 0

        self.vehicle_local_position = VehicleLocalPosition()
        self.vehicle_status = VehicleStatus()

        px4_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )

        self.offboard_mode_pub = self.create_publisher(
            OffboardControlMode, self._topic("/fmu/in/offboard_control_mode"), px4_qos
        )
        self.trajectory_sp_pub = self.create_publisher(
            TrajectorySetpoint, self._topic("/fmu/in/trajectory_setpoint"), px4_qos
        )
        self.vehicle_command_pub = self.create_publisher(
            VehicleCommand, self._topic("/fmu/in/vehicle_command"), px4_qos
        )

        self.create_subscription(
            VehicleLocalPosition,
            self._topic("/fmu/out/vehicle_local_position"),
            self._local_pos_cb,
            px4_qos,
        )
        self.create_subscription(
            VehicleStatus,
            self._topic("/fmu/out/vehicle_status_v1"),
            self._status_cb,
            px4_qos,
        )

        period_s = 1.0 / max(self.setpoint_rate_hz, 1.0)
        self.create_timer(period_s, self._timer_cb)

        self.get_logger().info(
            f"Lawnmower offboard started — instance={instance} "
            f"fmu_prefix='{self.fmu_prefix}' target_system={self.target_system}"
        )

    @staticmethod
    def _coerce_optional_float(value: float) -> float:
        if value is None:
            return float("nan")
        try:
            return float(value)
        except (TypeError, ValueError):
            return float("nan")

    def _topic(self, suffix: str) -> str:
        prefix = self.fmu_prefix.strip()
        if prefix and not prefix.startswith("/"):
            prefix = "/" + prefix
        return f"{prefix}{suffix}"

    def _now_us(self) -> int:
        return int(self.get_clock().now().nanoseconds / 1000)

    def _local_pos_cb(self, msg: VehicleLocalPosition) -> None:
        self.vehicle_local_position = msg

    def _status_cb(self, msg: VehicleStatus) -> None:
        self.vehicle_status = msg

    def _heartbeat(self) -> None:
        msg = OffboardControlMode()
        msg.position = True
        msg.velocity = False
        msg.acceleration = False
        msg.attitude = False
        msg.body_rate = False
        msg.timestamp = self._now_us()
        self.offboard_mode_pub.publish(msg)

    def _pub_hold_setpoint(self) -> None:
        if not self._hold_latched:
            return
        msg = TrajectorySetpoint()
        msg.position = [self.hold_x, self.hold_y, self.hold_z]
        # msg.velocity = [float("nan")] * 3
        msg.yaw = self.hold_yaw_rad
        msg.timestamp = self._now_us()
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
        msg.timestamp = self._now_us()
        self.vehicle_command_pub.publish(msg)

    def _engage_offboard(self) -> None:
        self._pub_vehicle_cmd(
            VehicleCommand.VEHICLE_CMD_DO_SET_MODE, param1=1.0, param2=6.0
        )

    def _arm(self) -> None:
        self._pub_vehicle_cmd(
            VehicleCommand.VEHICLE_CMD_COMPONENT_ARM_DISARM, param1=1.0
        )

    def _latch_hold(self) -> None:
        if not math.isfinite(self.hold_x) or not math.isfinite(self.hold_y):
            self.hold_x = float(self.vehicle_local_position.x)
            self.hold_y = float(self.vehicle_local_position.y)
        if not math.isfinite(self.hold_z):
            self.hold_z = float(self.vehicle_local_position.z)
        if not math.isfinite(self.hold_yaw_rad):
            heading = self.vehicle_local_position.heading
            self.hold_yaw_rad = float(heading) if math.isfinite(heading) else 0.0
        self._hold_latched = True
        self.get_logger().info(
            f"Hold latched at x={self.hold_x:.2f} y={self.hold_y:.2f} "
            f"z={self.hold_z:.2f} yaw={math.degrees(self.hold_yaw_rad):.1f} deg"
        )

    def _timer_cb(self) -> None:
        self._heartbeat()
        if not self._hold_latched:
            self._latch_hold()
        self._pub_hold_setpoint()

        if self._warmup_counter < self.offboard_warmup_cycles:
            self._warmup_counter += 1
            return

        now_us = self._now_us()
        min_interval_us = int(max(self.rearm_interval_s, 0.1) * 1e6)

        if now_us - self._last_offboard_request_us >= min_interval_us:
            self._engage_offboard()
            self._last_offboard_request_us = now_us

        if now_us - self._last_arm_request_us >= min_interval_us:
            self._arm()
            self._last_arm_request_us = now_us


def main(args=None) -> None:
    node = None
    try:
        rclpy.init(args=args)
        node = LawnmowerOffboard()
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        if node is not None:
            node.destroy_node()
        if rclpy.ok():
            rclpy.shutdown()


if __name__ == "__main__":
    main()
