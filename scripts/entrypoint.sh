#!/usr/bin/env bash
set -euo pipefail

cd "${WORKSPACE_DIR:-/ws}"

CONFIG_FILE="${CONFIG_FILE:-/config/drones.yaml}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "Config file not found: ${CONFIG_FILE}" >&2
  exit 1
fi

exec /scripts/spawn_fleet.py --config "${CONFIG_FILE}"
