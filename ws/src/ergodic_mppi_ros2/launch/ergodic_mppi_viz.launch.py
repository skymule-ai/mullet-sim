import os

from ament_index_python.packages import get_package_share_directory
from launch import LaunchDescription
from launch.actions import DeclareLaunchArgument, OpaqueFunction, TimerAction
from launch.conditions import IfCondition
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
    instances_str = ",".join(str(i) for i in instances)

    viz_config = LaunchConfiguration("viz_config").perform(context)
    mppi_config_path = LaunchConfiguration("mppi_config_path").perform(context)
    drones_config_path = LaunchConfiguration("drones_config_path").perform(context)
    anchor_base = float(LaunchConfiguration("anchor_base").perform(context))

    return [
        Node(
            package="ergodic_mppi_ros2",
            executable="ergodic_mppi_rviz_visualizer",
            name="ergodic_mppi_rviz_visualizer",
            output="screen",
            parameters=[
                viz_config,
                {
                    "px4_instances": instances_str,
                    "mppi_config_path": mppi_config_path,
                    "drones_config_path": drones_config_path,
                    "anchor_base": anchor_base,
                },
            ],
        )
    ]


def generate_launch_description() -> LaunchDescription:
    package_share = get_package_share_directory("ergodic_mppi_ros2")
    default_viz_config = os.path.join(
        package_share,
        "config",
        "ergodic_mppi_rviz_visualizer.yaml",
    )
    default_mppi_config = os.path.join(
        package_share,
        "config",
        "mppi_sitl_100x100.yaml",
    )
    default_rviz_config = os.path.join(
        package_share,
        "config",
        "ergodic_mppi_visualization.rviz",
    )

    static_tf = Node(
        package="tf2_ros",
        executable="static_transform_publisher",
        name="map_static_tf",
        output="screen",
        arguments=["0", "0", "0", "0", "0", "0", "1", "world", "map"],
    )

    rviz_node = Node(
        package="rviz2",
        executable="rviz2",
        name="rviz2",
        output="screen",
        arguments=["-d", LaunchConfiguration("rviz_config")],
        condition=IfCondition(LaunchConfiguration("start_rviz")),
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
                "viz_config",
                default_value=default_viz_config,
                description="YAML config for ergodic_mppi_rviz_visualizer.",
            ),
            DeclareLaunchArgument(
                "mppi_config_path",
                default_value=default_mppi_config,
                description="MPPI algorithm config used for density and obstacles.",
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
            DeclareLaunchArgument(
                "start_rviz",
                default_value="true",
                description="Whether to start RViz2 automatically.",
            ),
            DeclareLaunchArgument(
                "rviz_config",
                default_value=default_rviz_config,
                description="RViz config file.",
            ),
            static_tf,
            OpaqueFunction(function=launch_setup),
            TimerAction(period=0.5, actions=[rviz_node]),
        ]
    )
