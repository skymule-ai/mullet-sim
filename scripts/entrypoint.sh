#!/usr/bin/env bash
set -euo pipefail

cd "${WORKSPACE_DIR:-/ws}"

CONFIG_FILE="${CONFIG_FILE:-/config/drones.yaml}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "Config file not found: ${CONFIG_FILE}" >&2
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

exec /scripts/spawn_fleet.py --config "${CONFIG_FILE}"
