from launch import LaunchDescription
from launch_ros.actions import Node

def generate_launch_description():
    return LaunchDescription([
        Node(
            package='px4_state_machine',
            executable='main',
            name='drone_state_machine',
            output='screen',
            parameters=[{'use_sim_time': True}, '/path/to/your/config/params.yaml']
        ),
    ])