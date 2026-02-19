import os

from ament_index_python.packages import get_package_share_directory
from launch import LaunchDescription
from launch_ros.actions import Node


def generate_launch_description():
    params_file = os.path.join(
        get_package_share_directory("px4_state_machine"),
        "config",
        "offboard_params.yaml",
    )

    return LaunchDescription(
        [
            Node(
                package="px4_state_machine",
                executable="offboard.py",
                name="vtol_sm",
                output="screen",
                parameters=[params_file],
            )
        ]
    )
