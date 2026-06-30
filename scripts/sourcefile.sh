#!/usr/bin/env bash

CONFIG_FILE="${CONFIG_FILE:-/config/drones.yaml}"
config_ros_domain_id=""
if [[ -f "${CONFIG_FILE}" ]]; then
  config_ros_domain_id="$(
    awk '
      /^[[:space:]]*#/ {next}
      /^[[:space:]]*ros_domain_id[[:space:]]*:/ {
        value=$0
        sub(/^[^:]*:[[:space:]]*/, "", value)
        sub(/[[:space:]]*(#.*)?$/, "", value)
        print value
        exit
      }
    ' "${CONFIG_FILE}"
  )"
fi

export ROS_DOMAIN_ID="${config_ros_domain_id:-${ROS_DOMAIN_ID:-0}}"

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

# At the end of sourcefile.sh
VENV_DIR="${VENV_DIR:-/ws/venv}"
if [ -d "${VENV_DIR}" ]; then
  export PYTHONPATH="${VENV_DIR}/lib/python3.10/site-packages:${PYTHONPATH:-}"
fi
