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

# Same precedence as spawn_fleet.py: run.sh --domain > simulation.ros_domain_id > env > 0.
export ROS_DOMAIN_ID="${MULLET_DOMAIN_OVERRIDE:-${config_ros_domain_id:-${ROS_DOMAIN_ID:-0}}}"

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

# An optional venv in the workspace, whatever the image's Python version.
VENV_DIR="${VENV_DIR:-/ws/venv}"
for venv_site in "${VENV_DIR}"/lib/python3*/site-packages; do
  if [ -d "${venv_site}" ]; then
    export PYTHONPATH="${venv_site}:${PYTHONPATH:-}"
  fi
done
