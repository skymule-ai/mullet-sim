#!/usr/bin/env bash
set -euo pipefail

# Patch lawnmower airframe in PX4 tree before spawning the fleet.

cd "${WORKSPACE_DIR:-/ws}"

ENTRYPOINT_MODE="${ENTRYPOINT_MODE:-sitl}"

CONFIG_FILE="${CONFIG_FILE:-/config/drones.yaml}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "Config file not found: ${CONFIG_FILE}" >&2
  exit 1
fi

AIRFRAME_DST="/opt/PX4-Autopilot/ROMFS/px4fmu_common/init.d-posix/airframes/4011_gz_lawnmower"
AIRFRAME_URL="https://raw.githubusercontent.com/PX4/PX4-Autopilot/main/ROMFS/px4fmu_common/init.d-posix/airframes/4011_gz_lawnmower"

if [[ "$(id -u)" -eq 0 ]]; then
  curl -fL --retry 3 --connect-timeout 10 -o "${AIRFRAME_DST}" "${AIRFRAME_URL}"
elif command -v sudo >/dev/null 2>&1; then
  sudo -n curl -fL --retry 3 --connect-timeout 10 -o "${AIRFRAME_DST}" "${AIRFRAME_URL}" || {
    echo "Unable to patch ${AIRFRAME_DST}: sudo privileges are required." >&2
    exit 1
  }
else
  echo "Unable to patch ${AIRFRAME_DST}: run as root or provide sudo." >&2
  exit 1
fi

# Add source sourcefile.sh to bashrc so that it gets sourced in every new shell, including tmux panes.
if ! grep -qxF "source /scripts/sourcefile.sh" ~/.bashrc; then
  echo "source /scripts/sourcefile.sh" >> ~/.bashrc
fi

set +u
source /scripts/sourcefile.sh
set -u

VENV_DIR="${VENV_DIR:-/ws/venv}"
export PYTHONPATH="${VENV_DIR}/lib/python3.10/site-packages:${PYTHONPATH:-}"

touch "${VENV_DIR}/COLCON_IGNORE"

case "${ENTRYPOINT_MODE}" in
  sitl)
    exec /scripts/spawn_fleet.py --config "${CONFIG_FILE}"
    ;;
  no_sitl)
    echo "PX4_NO_SITL container running. This container is meant for running custom code that connects to the PX4_SITL container. To run PX4 SITL, start the px4_sitl service."
    exec tail -f /dev/null
    ;;
  *)
    echo "Invalid ENTRYPOINT_MODE: ${ENTRYPOINT_MODE}. Expected one of: sitl, no_sitl" >&2
    exit 1
    ;;
esac
