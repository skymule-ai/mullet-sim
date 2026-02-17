#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

CONFIG_PATH="${CONFIG_PATH:-config/drones.yaml}"
TMUX_CONF_PATH="${TMUX_CONF_PATH:-config/tmux.conf}"

# Allow Gazebo GUI from container.
if command -v xhost >/dev/null 2>&1; then
  xhost +local:docker >/dev/null 2>&1 || true
fi

if [[ ! -f .env ]]; then
  cp .env.example .env
fi

docker compose up -d

SESSION_NAME="mullet_sitl"
CONTAINER_NAME="mullet_px4_sitl"

if [[ -f "${TMUX_CONF_PATH}" ]]; then
  tmux source-file "${TMUX_CONF_PATH}" >/dev/null 2>&1 || true
fi

tmux has-session -t "${SESSION_NAME}" 2>/dev/null && tmux kill-session -t "${SESSION_NAME}"

if [[ ! -f "${CONFIG_PATH}" ]]; then
  echo "Config file not found: ${CONFIG_PATH}" >&2
  exit 1
fi

mapfile -t INSTANCES < <(
  awk '($0 !~ /^[[:space:]]*#/) && ($1=="instance:") {print $2}' "${CONFIG_PATH}" | sort -n
)

if [[ ${#INSTANCES[@]} -eq 0 ]]; then
  echo "No instance entries found in ${CONFIG_PATH}" >&2
  exit 1
fi

first_inst="${INSTANCES[0]}"

# Window "px4": PX4 logs (one pane per instance)
tmux new-session -d -s "${SESSION_NAME}" -n "px4" \
  "bash -lc 'sleep 2; docker exec ${CONTAINER_NAME} bash -lc \"tail -n 200 -F /workspace/.runtime/px4_${first_inst}.log\"'"

for inst in "${INSTANCES[@]:1}"; do
  tmux split-window -t "${SESSION_NAME}:px4" \
    "bash -lc 'sleep 2; docker exec ${CONTAINER_NAME} bash -lc \"tail -n 200 -F /workspace/.runtime/px4_${inst}.log\"'"
  tmux select-layout -t "${SESSION_NAME}:px4" tiled
done

# Window "xrce": MicroXRCEAgent log
tmux new-window -d -t "${SESSION_NAME}" -n "xrce" \
  "bash -lc 'sleep 2; docker exec ${CONTAINER_NAME} bash -lc \"tail -n 200 -F /workspace/.runtime/xrce_agent.log\"'"

# Window "shell": interactive shell inside container
tmux new-window -d -t "${SESSION_NAME}" -n "shell" \
  "bash -lc 'docker exec -it ${CONTAINER_NAME} bash'"

tmux select-window -t "${SESSION_NAME}:px4" || true

tmux attach -t "${SESSION_NAME}"

docker compose down
