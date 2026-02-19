#!/usr/bin/env bash

export ROS_DOMAIN_ID="${ROS_DOMAIN_ID:-0}"

if [ -n "${ROS_DISTRO:-}" ] && [ -f "/opt/ros/${ROS_DISTRO}/setup.bash" ]; then
	. "/opt/ros/${ROS_DISTRO}/setup.bash"
else
	for ros_setup in /opt/ros/*/setup.bash; do
		if [ -f "${ros_setup}" ]; then
			. "${ros_setup}"
			break
		fi
	done
fi

if [ -f "/ws/install/setup.bash" ]; then
	. "/ws/install/setup.bash"
fi
