from glob import glob
import os

from setuptools import find_packages, setup

package_name = "ergodic_mppi_ros2"

setup(
    name=package_name,
    version="0.0.1",
    packages=find_packages(exclude=["test"]),
    data_files=[
        ("share/ament_index/resource_index/packages", ["resource/" + package_name]),
        ("share/" + package_name, ["package.xml"]),
        (os.path.join("share", package_name, "launch"), glob("launch/*.launch.py")),
        (os.path.join("share", package_name, "config"), glob("config/*.yaml")),
        (os.path.join("share", package_name, "config"), glob("config/*.rviz")),
    ],
    install_requires=[
        "setuptools",
        "jax>=0.6.2",
        "jaxlib>=0.6.2",
    ],
    zip_safe=True,
    maintainer="mmantovani",
    maintainer_email="mmantovani@unimore.it",
    description="Bootstrap ROS 2 package for multi-robot MPPI ergodic control.",
    license="MIT",
    tests_require=["pytest"],
    entry_points={
        "console_scripts": [
            "ergodic_mppi_coordinator_node = ergodic_mppi_ros2.coordinator_node:main",
            "ergodic_mppi_rviz_visualizer = ergodic_mppi_ros2.rviz_visualizer_node:main",
        ],
    },
)
