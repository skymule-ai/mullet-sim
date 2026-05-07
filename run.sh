#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

CONFIG_PATH="${CONFIG_PATH:-config/drones.yaml}"
TMUX_CONF_PATH="${TMUX_CONF_PATH:-config/tmux.conf}"
CONFIG_DIR="${script_dir}/config"

# Allow Gazebo GUI from container.
if command -v xhost >/dev/null 2>&1; then
  xhost +local:docker >/dev/null 2>&1 || true
fi

if [[ ! -f .env ]]; then
  cp .env.example .env
fi

SESSION_NAME="mullet_sitl"
CONTAINER_NAME="mullet_px4_sitl"
TMUX_CONF_IN_CONTAINER="/config/tmux.conf"
RUNTIME_DIR_IN_CONTAINER="/tmp/runtime"

if [[ ! -f "${CONFIG_PATH}" ]]; then
  echo "Config file not found: ${CONFIG_PATH}" >&2
  exit 1
fi

config_path_abs="$(realpath "${CONFIG_PATH}")"
config_dir_abs="$(realpath "${CONFIG_DIR}")"

if [[ "${config_path_abs}" != "${config_dir_abs}/"* ]]; then
  echo "Config file must be under ${CONFIG_DIR}: ${CONFIG_PATH}" >&2
  exit 1
fi

config_relative_path="${config_path_abs#${config_dir_abs}/}"
config_path_in_container="/config/${config_relative_path}"

CONFIG_FILE="${config_path_in_container}" docker compose up -d px4_sitl

if ! docker exec "${CONTAINER_NAME}" tmux -V >/dev/null 2>&1; then
  echo "tmux not found in container ${CONTAINER_NAME}" >&2
  exit 1
fi

docker exec "${CONTAINER_NAME}" tmux kill-server >/dev/null 2>&1 || true

tmux_base_cmd=(docker exec "${CONTAINER_NAME}" tmux)
tmux_start_cmd=("${tmux_base_cmd[@]}")
if [[ -f "${TMUX_CONF_PATH}" ]]; then
  tmux_start_cmd=("${tmux_base_cmd[@]}" -f "${TMUX_CONF_IN_CONTAINER}")
fi

mapfile -t INSTANCES < <(
  awk '($0 !~ /^[[:space:]]*#/) && ($1=="instance:") {print $2}' "${CONFIG_PATH}" | sort -n
)

if [[ ${#INSTANCES[@]} -eq 0 ]]; then
  echo "No instance entries found in ${CONFIG_PATH}" >&2
  exit 1
fi

drone_count="${#INSTANCES[@]}"
echo "[mullet] config: ${config_path_abs} -> ${config_path_in_container}"
echo "[mullet] detected ${drone_count} drone instance(s): ${INSTANCES[*]}"
first_inst="${INSTANCES[0]}"

# Window "px4": PX4 logs (one pane per instance)
"${tmux_start_cmd[@]}" new-session -d -s "${SESSION_NAME}" -n "px4" \
  "bash -lc 'sleep 2; tail -n 200 -F ${RUNTIME_DIR_IN_CONTAINER}/px4_${first_inst}.log'"

for inst in "${INSTANCES[@]:1}"; do
  "${tmux_base_cmd[@]}" split-window -t "${SESSION_NAME}:px4" \
    "bash -lc 'sleep 2; tail -n 200 -F ${RUNTIME_DIR_IN_CONTAINER}/px4_${inst}.log'"
  "${tmux_base_cmd[@]}" select-layout -t "${SESSION_NAME}:px4" tiled
done

# Window "xrce": MicroXRCEAgent log
"${tmux_base_cmd[@]}" new-window -d -t "${SESSION_NAME}" -n "xrce" \
  "bash -lc 'sleep 2; tail -n 200 -F ${RUNTIME_DIR_IN_CONTAINER}/xrce_agent.log'"


cmd_list=(
)
execute_cmds=("0")  # Indices of commands to execute immediately

# Window "shell": interactive shell inside container (one pane per drone)
shell_pane_id="$("${tmux_base_cmd[@]}" new-window -d -P -F "#{pane_id}" -t "${SESSION_NAME}" -n "shell" bash)"

# Create additional panes so shell pane count matches drone count.
for ((i = 1; i < ${#cmd_list[@]}; i++)); do
  "${tmux_base_cmd[@]}" split-window -d -t "${SESSION_NAME}:shell" bash
  "${tmux_base_cmd[@]}" select-layout -t "${SESSION_NAME}:shell" tiled
done

# For every command in the list, send it to a different pane in the "shell" window with Enter flag to execute immediately.
for i in "${!cmd_list[@]}"; do
  pane_id="$("${tmux_base_cmd[@]}" list-panes -t "${SESSION_NAME}:shell" -F "#{pane_id}" | sed -n "$((i + 1))p")"
  if [[ " ${execute_cmds[@]} " =~ " $i " ]]; then
    "${tmux_base_cmd[@]}" send-keys -t "${pane_id}" "${cmd_list[$i]}" Enter
  else
    "${tmux_base_cmd[@]}" send-keys -t "${pane_id}" "${cmd_list[$i]}"
  fi
done

# Prepare the other commands from the command list, 1 per pane.

# Focus the pane running build/launch.
"${tmux_base_cmd[@]}" select-pane -t "${shell_pane_id}"

"${tmux_base_cmd[@]}" select-window -t "${SESSION_NAME}:px4" || true

docker exec -it "${CONTAINER_NAME}" tmux attach -t "${SESSION_NAME}"

docker compose down
