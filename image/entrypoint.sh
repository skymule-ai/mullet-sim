#!/usr/bin/env bash
set -euo pipefail

export PX4_DIR="${PX4_DIR:-/opt/PX4-Autopilot}"

# If HOME isn't set (can happen with arbitrary UID), fall back.
HOME_DIR="${HOME:-/tmp}"
export PATH="${HOME_DIR}/.local/bin:${PATH}"

exec "$@"
