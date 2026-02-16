#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

docker compose down

SESSION_NAME="mullet_sitl"
tmux has-session -t "${SESSION_NAME}" 2>/dev/null && tmux kill-session -t "${SESSION_NAME}" || true
