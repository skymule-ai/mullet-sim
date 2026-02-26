import os

from ament_index_python.packages import get_package_share_directory
from launch import LaunchDescription
from launch.actions import SetEnvironmentVariable
from launch_ros.actions import Node


def generate_launch_description():
    offboard_params_file = os.path.join(
        get_package_share_directory("px4_state_machine"),
        "config",
        "offboard_params.yaml",
    )

    return LaunchDescription(
        [
            SetEnvironmentVariable("RCUTILS_COLORIZED_OUTPUT", "1"),
            Node(
                package="px4_state_machine",
                executable="offboard.py",
                name="vtol_sm",
                output="screen",
                parameters=[offboard_params_file],
            ),
        ]
    )
