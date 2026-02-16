#!/usr/bin/env bash
set -euo pipefail

cd "${WORKSPACE_DIR:-/workspace}"

CONFIG_FILE="${CONFIG_FILE:-/workspace/config/drones.yaml}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "Config file not found: ${CONFIG_FILE}" >&2
  exit 1
fi

export ROS_DOMAIN_ID="${ROS_DOMAIN_ID:-0}"

exec /workspace/scripts/spawn_fleet.py --config "${CONFIG_FILE}"
