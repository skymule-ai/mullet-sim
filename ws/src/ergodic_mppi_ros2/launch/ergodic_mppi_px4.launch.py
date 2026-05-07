import os

from ament_index_python.packages import get_package_share_directory
from launch import LaunchDescription
from launch.actions import DeclareLaunchArgument, OpaqueFunction
from launch.substitutions import LaunchConfiguration
from launch_ros.actions import Node


def _parse_instances(context) -> list[int]:
    raw_instances = LaunchConfiguration("px4_instances").perform(context).strip()
    if raw_instances:
        return sorted(int(x.strip()) for x in raw_instances.split(",") if x.strip())

    count = int(LaunchConfiguration("num_robots").perform(context))
    offset = int(LaunchConfiguration("px4_instance_offset").perform(context))
    return list(range(offset, offset + count))


def launch_setup(context, *args, **kwargs):
    instances = _parse_instances(context)
    instances_str = ",".join(str(instance) for instance in instances)
    config_path = LaunchConfiguration("coordinator_config").perform(context)
    mppi_config_path = LaunchConfiguration("mppi_config_path").perform(context)
    drones_config_path = LaunchConfiguration("drones_config_path").perform(context)
    anchor_base = LaunchConfiguration("anchor_base").perform(context)

    return [
        Node(
            package="ergodic_mppi_ros2",
            executable="ergodic_mppi_coordinator_node",
            name="ergodic_mppi_coordinator_node",
            output="screen",
            parameters=[
                config_path,
                {
                    "px4_instances": instances_str,
                    "mppi_config_path": mppi_config_path,
                    "drones_config_path": drones_config_path,
                    "anchor_base": float(anchor_base),
                },
            ],
        )
    ]


def generate_launch_description() -> LaunchDescription:
    package_share = get_package_share_directory("ergodic_mppi_ros2")
    default_cfg = os.path.join(
        package_share,
        "config",
        "ergodic_mppi_coordinator_config.yaml",
    )
    default_mppi_cfg = os.path.join(
        package_share,
        "config",
        "mppi_sitl_100x100.yaml",
    )

    return LaunchDescription(
        [
            DeclareLaunchArgument(
                "px4_instances",
                default_value="1,2",
                description=(
                    "Comma-separated PX4 instance numbers, e.g. '1,2,3'. "
                    "When set, overrides num_robots and px4_instance_offset."
                ),
            ),
            DeclareLaunchArgument(
                "num_robots",
                default_value="1",
                description="Number of robots when explicit instance list is omitted.",
            ),
            DeclareLaunchArgument(
                "px4_instance_offset",
                default_value="1",
                description="First PX4 instance number when list is omitted.",
            ),
            DeclareLaunchArgument(
                "coordinator_config",
                default_value=default_cfg,
                description="YAML configuration for ergodic_mppi_coordinator_node.",
            ),
            DeclareLaunchArgument(
                "mppi_config_path",
                default_value=default_mppi_cfg,
                description="MPPI algorithm configuration YAML.",
            ),
            DeclareLaunchArgument(
                "drones_config_path",
                default_value="/config/drones_x500_mppi.yaml",
                description="drones.yaml file used to derive per-instance map anchors.",
            ),
            DeclareLaunchArgument(
                "anchor_base",
                default_value="50.0",
                description="Map anchor base coordinate for fleet alignment.",
            ),
            OpaqueFunction(function=launch_setup),
        ]
    )
