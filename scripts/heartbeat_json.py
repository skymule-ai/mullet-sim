#!/usr/bin/env python3
"""Print mullet_core heartbeats as JSON lines (the TUI reads them through ``docker exec``).

Requires ``rclpy`` and ``mullet_interfaces`` inside the SITL container.

Example:
    python3 /scripts/heartbeat_json.py /mullet/heartbeat
"""
import json
import sys

import rclpy
from mullet_interfaces.msg import DroneHeartbeat
from rclpy.node import Node

FIELDS = (
    "vehicle_instance",
    "drone_id",
    "state_label",
    "armed",
    "offboard",
    "landed",
    "px4_nav_state_label",
    "x",
    "y",
    "z",
    "yaw",
)


def main() -> None:
    """Spin a ROS 2 node that dumps each heartbeat as one JSON object on stdout.

    Args:
        None. Optional topic path is ``sys.argv[1]`` (default ``/mullet/heartbeat``).

    Raises:
        BrokenPipeError: The TUI closed the pipe (process should exit).
    """
    topic = sys.argv[1] if len(sys.argv) > 1 else "/mullet/heartbeat"
    rclpy.init()
    node = Node("mullet_tui_heartbeat")

    def on_heartbeat(msg: DroneHeartbeat) -> None:
        # A closed pipe (TUI gone) raises here and ends the process.
        print(json.dumps({field: getattr(msg, field) for field in FIELDS}), flush=True)

    node.create_subscription(DroneHeartbeat, topic, on_heartbeat, 10)
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
