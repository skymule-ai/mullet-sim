"""Centralized multi-robot ergodic MPPI coordinator for PX4 SITL."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Optional

import jax
import jax.numpy as jnp
import numpy as np
import rclpy
from px4_msgs.msg import VehicleLocalPosition
from rclpy.node import Node
from rclpy.qos import DurabilityPolicy, HistoryPolicy, QoSProfile, ReliabilityPolicy
from std_msgs.msg import Float32MultiArray

from ergodic_mppi_ros2.frame_utils import (
    AnchorState,
    compute_anchor_map_from_drones_config,
    map_position_from_px4,
)
from ergodic_mppi_ros2.params_loader import load_mppi_params, load_run_config
from ergodic_mppi_ros2.runtime import acceleration_to_direction_command, multi_robot_step
from ergodic_mppi_ros2.topic_utils import instance_to_prefix, topic_with_prefix


@dataclass
class RobotObservation:
    """Latest per-robot state observation."""

    state_map: np.ndarray
    stamp_ns: int


class ErgodicMppiCoordinatorNode(Node):
    """Centralized ROS 2 node that runs multi-robot MPPI and publishes commands."""

    def __init__(self) -> None:
        super().__init__("ergodic_mppi_coordinator_node")

        self.declare_parameter("px4_instances", "1,2")
        self.declare_parameter("command_topic", "command")
        self.declare_parameter("pose_topic", "/fmu/out/vehicle_local_position")
        self.declare_parameter("control_rate_hz", 2.0)
        self.declare_parameter("state_timeout_s", 1.0)
        self.declare_parameter("mppi_config_path", "")
        self.declare_parameter("drones_config_path", "/config/drones_x500_mppi.yaml")
        self.declare_parameter("anchor_base", 50.0)

        raw_instances = str(self.get_parameter("px4_instances").value or "")
        self.instances = sorted(int(x.strip()) for x in raw_instances.split(",") if x.strip())
        if not self.instances:
            self.instances = [1]

        self.command_topic = str(self.get_parameter("command_topic").value or "command")
        self.pose_topic = str(
            self.get_parameter("pose_topic").value or "/fmu/out/vehicle_local_position"
        )
        self.control_rate_hz = float(self.get_parameter("control_rate_hz").value or 2.0)
        if self.control_rate_hz <= 0.0:
            self.control_rate_hz = 2.0
        self.state_timeout_s = float(self.get_parameter("state_timeout_s").value or 1.0)
        if self.state_timeout_s <= 0.0:
            self.state_timeout_s = 1.0

        mppi_config_path = str(self.get_parameter("mppi_config_path").value or "").strip()
        if not mppi_config_path:
            raise ValueError("mppi_config_path must be provided")

        drones_config_path = str(
            self.get_parameter("drones_config_path").value or ""
        ).strip()
        anchor_base = float(self.get_parameter("anchor_base").value or 50.0)

        self.params = load_mppi_params(mppi_config_path)
        self.run_cfg = load_run_config(mppi_config_path)
        self._dt = float(self.params.model_params.delta_t)
        self._state_timeout_ns = int(self.state_timeout_s * 1e9)

        self.anchors = compute_anchor_map_from_drones_config(drones_config_path, anchor_base)
        self.anchor_states: Dict[int, AnchorState] = {
            inst: AnchorState(px4_anchor_xy=self.anchors.get(inst))
            for inst in self.instances
        }

        self.observations: Dict[int, RobotObservation] = {}
        self.command_publishers: Dict[int, rclpy.publisher.Publisher] = {}

        px4_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )

        for inst in self.instances:
            prefix = instance_to_prefix(inst)
            pose_topic = topic_with_prefix(prefix, self.pose_topic)
            command_topic = topic_with_prefix(prefix, self.command_topic)

            self.create_subscription(
                VehicleLocalPosition,
                pose_topic,
                lambda msg, idx=inst: self._pose_cb(msg, idx),
                px4_qos,
            )
            self.command_publishers[inst] = self.create_publisher(
                Float32MultiArray,
                command_topic,
                10,
            )

        self._runtime_initialized = False
        self._U_prev_all: Optional[jnp.ndarray] = None
        self._keys_all: Optional[jnp.ndarray] = None
        self._lam_all: Optional[jnp.ndarray] = None
        self._histories_all: Optional[jnp.ndarray] = None
        self._surrogates_all: Optional[jnp.ndarray] = None

        self._last_wait_log_ns = 0
        self._last_cmd_log_ns = 0
        self._wait_log_period_ns = int(5e9)
        self._cmd_log_period_ns = int(3e9)

        self.create_timer(1.0 / self.control_rate_hz, self._timer_cb)

        self.get_logger().info(
            f"Ergodic MPPI coordinator initialized for instances {self.instances}"
        )
        self.get_logger().info(
            f"mppi_config_path='{mppi_config_path}' dt={self._dt:.3f} "
            f"K={self.params.K} T={self.params.T}"
        )

    def _pose_cb(self, msg: VehicleLocalPosition, instance: int) -> None:
        if not bool(getattr(msg, "xy_valid", False)):
            return

        raw_xy = np.array([float(msg.x), float(msg.y)], dtype=float)
        anchor_state = self.anchor_states[instance]
        mapped_xy = map_position_from_px4(raw_xy, anchor_state)

        heading = float(msg.heading) if np.isfinite(float(msg.heading)) else 0.0
        yaw_rate = float(getattr(msg, "heading_rate", 0.0))
        if not np.isfinite(yaw_rate):
            yaw_rate = 0.0

        vx = float(msg.vx) if np.isfinite(float(msg.vx)) else 0.0
        vy = float(msg.vy) if np.isfinite(float(msg.vy)) else 0.0

        state_map = np.array(
            [mapped_xy[0], mapped_xy[1], vx, vy, heading, yaw_rate],
            dtype=np.float32,
        )
        self.observations[instance] = RobotObservation(
            state_map=state_map,
            stamp_ns=self.get_clock().now().nanoseconds,
        )

    def _build_state_matrix(self) -> Optional[np.ndarray]:
        now_ns = self.get_clock().now().nanoseconds
        states = []
        for inst in self.instances:
            obs = self.observations.get(inst)
            if obs is None:
                return None
            if (now_ns - obs.stamp_ns) > self._state_timeout_ns:
                return None
            states.append(obs.state_map)
        return np.stack(states, axis=0)

    def _init_runtime(self, x_all: jnp.ndarray) -> None:
        num_robots = x_all.shape[0]
        self._U_prev_all = jnp.zeros(
            (num_robots, self.params.T, self.params.dim_u),
            dtype=jnp.float32,
        )

        key = jax.random.PRNGKey(self.run_cfg.seed)
        self._keys_all = jax.random.split(key, num_robots)
        self._lam_all = jnp.full((num_robots,), self.params.lam, dtype=jnp.float32)
        self._histories_all = jnp.stack(
            [jnp.broadcast_to(x_all[i, :2], (self.params.history_len, 2)) for i in range(num_robots)],
            axis=0,
        )
        self._surrogates_all = jnp.stack(
            [jnp.broadcast_to(x_all[i, :2], (self.params.T, 2)) for i in range(num_robots)],
            axis=0,
        )
        self._runtime_initialized = True
        self.get_logger().info("MPPI runtime state initialized")

    def _timer_cb(self) -> None:
        now_ns = self.get_clock().now().nanoseconds
        x_all_np = self._build_state_matrix()
        if x_all_np is None:
            if now_ns - self._last_wait_log_ns >= self._wait_log_period_ns:
                self.get_logger().warn(
                    "Waiting for fresh state from all robots before running MPPI"
                )
                self._last_wait_log_ns = now_ns
            return

        x_all = jnp.asarray(x_all_np, dtype=jnp.float32)

        if not self._runtime_initialized:
            self._init_runtime(x_all)

        if (
            self._U_prev_all is None
            or self._keys_all is None
            or self._lam_all is None
            or self._histories_all is None
            or self._surrogates_all is None
        ):
            return

        (
            u0_all,
            U_next_all,
            keys_next_all,
            lam_next_all,
            histories_next_all,
            surrogates_next_all,
        ) = multi_robot_step(
            self.params,
            x_all,
            self._U_prev_all,
            self._keys_all,
            self._lam_all,
            self._histories_all,
            self._surrogates_all,
        )

        self._U_prev_all = U_next_all
        self._keys_all = keys_next_all
        self._lam_all = lam_next_all
        self._histories_all = histories_next_all
        self._surrogates_all = surrogates_next_all

        u0_np = np.asarray(u0_all)
        x_np = np.asarray(x_all)

        for idx, inst in enumerate(self.instances):
            cmd = acceleration_to_direction_command(
                jnp.asarray(x_np[idx], dtype=jnp.float32),
                jnp.asarray(u0_np[idx], dtype=jnp.float32),
                self._dt,
            )
            cmd_np = np.asarray(cmd, dtype=np.float32)
            self.command_publishers[inst].publish(Float32MultiArray(data=cmd_np.tolist()))

        if now_ns - self._last_cmd_log_ns >= self._cmd_log_period_ns:
            first_cmd = acceleration_to_direction_command(
                jnp.asarray(x_np[0], dtype=jnp.float32),
                jnp.asarray(u0_np[0], dtype=jnp.float32),
                self._dt,
            )
            cmd0 = np.asarray(first_cmd)
            self.get_logger().info(
                f"Published MPPI commands for {len(self.instances)} robots; "
                f"instance {self.instances[0]} cmd=({cmd0[0]:.3f}, {cmd0[1]:.3f}, 1)"
            )
            self._last_cmd_log_ns = now_ns


def main(args=None) -> None:
    rclpy.init(args=args)
    node = ErgodicMppiCoordinatorNode()
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        node.destroy_node()
        rclpy.shutdown()


if __name__ == "__main__":
    main()
