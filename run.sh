#!/usr/bin/env bash
# Launch the SITL fleet described by a fleet/session YAML and open the tmux view.
# Validates the config, pulls up mullet_px4_sitl, waits for /tmp/runtime/ready,
# optionally runs colcon, then tags host tmux panes. Detaching runs ./stop.sh
# unless --keep. The TUI (./mullet) calls this with --no-attach. --headless skips
# tmux and the GUI: it starts the cores in the background, waits for their
# heartbeats and returns, for the paddock and test harnesses.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

usage() {
  cat <<'EOF'
Usage: ./run.sh [--config PATH] [--no-attach] [--keep] [--headless] [--domain N]

  --config PATH  fleet/session YAML under config/ (default: $CONFIG_PATH or config/drones.yaml)
  --no-attach    set everything up without attaching to tmux (used by the TUI)
  --keep         keep the simulation running after you detach from tmux
  --headless     no Gazebo GUI, no tmux: start the cores, wait for every heartbeat, return
                 with the simulation running (./stop.sh stops it)
  --domain N     ROS_DOMAIN_ID for this run, over simulation.ros_domain_id and the env
EOF
}

CONFIG_PATH="${CONFIG_PATH:-config/drones.yaml}"
TMUX_CONF_PATH="${TMUX_CONF_PATH:-config/tmux.conf}"
ATTACH=1
KEEP=0
HEADLESS=0
DOMAIN_OVERRIDE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG_PATH="${2:?--config needs a path}"; shift 2 ;;
    --no-attach) ATTACH=0; shift ;;
    --keep) KEEP=1; shift ;;
    --headless) HEADLESS=1; shift ;;
    --domain) DOMAIN_OVERRIDE="${2:?--domain needs a ROS domain id}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if [[ -n "${DOMAIN_OVERRIDE}" ]] && ! [[ "${DOMAIN_OVERRIDE}" =~ ^[0-9]+$ && "${DOMAIN_OVERRIDE}" -le 232 ]]; then
  echo "--domain must be a ROS domain id in [0, 232], got '${DOMAIN_OVERRIDE}'" >&2
  exit 2
fi

CONFIG_DIR="${script_dir}/config"
CONTAINER_NAME="mullet_px4_sitl"
RUNTIME_DIR_IN_CONTAINER="/tmp/runtime"
TMUX_SOCKET="mullet"
SESSION_NAME="mullet"
# Robot identity colors (Mullet Dusk), indexed by instance; the TUI re-applies its theme.
ROBOT_COLORS=("#4CC2FF" "#FFB547" "#B18CFF" "#3DDC97" "#FF7AB6" "#FF8F5A" "#6C9CFF" "#C6E86B")
MUTED_COLOR="#8B93A7"

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

export CONFIG_FILE="/config/${config_path_abs#"${config_dir_abs}"/}"

# Validate the config and load it as MULLET_* variables (single parser: spawn_fleet.py).
config_env="$(python3 scripts/spawn_fleet.py --config "${CONFIG_PATH}" --print-env)"
eval "${config_env}"
read -r -a INSTANCES <<< "${MULLET_INSTANCES}"

if [[ ! -f .env ]]; then
  cp .env.example .env
fi
env_file_value() { sed -n "s/^$1=//p" .env | tail -n 1; }

# One ROS_DOMAIN_ID for everything (container, agent, PX4, every pane):
# --domain > config > shell env > .env > 0. The shell env also wins over .env inside compose.
# --domain reaches the container as MULLET_DOMAIN_OVERRIDE, which spawn_fleet.py and
# sourcefile.sh also put first, so the container never falls back to the config's domain.
export MULLET_DOMAIN_OVERRIDE="${DOMAIN_OVERRIDE}"
export ROS_DOMAIN_ID="${DOMAIN_OVERRIDE:-${MULLET_ROS_DOMAIN_ID:-${ROS_DOMAIN_ID:-$(env_file_value ROS_DOMAIN_ID)}}}"
export ROS_DOMAIN_ID="${ROS_DOMAIN_ID:-0}"
export MULLET_HEADLESS="${HEADLESS}"
if [[ -n "${MULLET_IMAGE}" ]]; then export PX4_SITL_IMAGE="${MULLET_IMAGE}"; fi
if [[ -n "${MULLET_RMW}" ]]; then export RMW_IMPLEMENTATION="${MULLET_RMW}"; fi
if [[ -n "${MULLET_RESET_PARAMS:-}" ]]; then export MULLET_RESET_PARAMS; fi

compose=(docker compose -f docker-compose.yml)
gpu="${MULLET_GPU:-auto}"
if [[ "${gpu}" == on ]] || { [[ "${gpu}" == auto ]] && nvidia-smi >/dev/null 2>&1; }; then
  compose+=(-f docker-compose.gpu.yml)
fi
if [[ -f docker-compose.override.yml ]]; then
  compose+=(-f docker-compose.override.yml)
fi

container_running() {
  [[ "$(docker inspect -f '{{.State.Running}}' "${CONTAINER_NAME}" 2>/dev/null)" == true ]]
}

# Host networking: a stray SITL or agent would silently collide with the fleet's ports.
# Our own running container is recreated below, so its ports don't count.
if ! container_running; then
  python3 scripts/spawn_fleet.py --config "${CONFIG_PATH}" --check-ports
fi

# Allow Gazebo/RViz GUI from container.
if command -v xhost >/dev/null 2>&1; then
  xhost +local:docker >/dev/null 2>&1 || true
fi

echo "[mullet] config: ${config_path_abs} -> ${CONFIG_FILE}"
echo "[mullet] instances: ${MULLET_INSTANCES} | namespace: /${MULLET_NS_PREFIX}_<N> | ROS_DOMAIN_ID=${ROS_DOMAIN_ID} | gpu: ${gpu}"

# Always a fresh container: processes started with `docker exec` outlive their tmux panes.
"${compose[@]}" up -d --force-recreate px4_sitl

echo "[mullet] waiting for the PX4 fleet to start..."
max_wait=180
if [[ "${MULLET_PX4_REBUILD:-0}" =~ ^(1|true|yes|on)$ ]]; then max_wait=1800; fi
for ((waited = 0; ; waited++)); do
  if docker exec "${CONTAINER_NAME}" test -f "${RUNTIME_DIR_IN_CONTAINER}/ready" 2>/dev/null; then
    break
  fi
  if ! container_running || ((waited >= max_wait)); then
    echo "[mullet] the fleet did not start; container logs:" >&2
    docker logs --tail 40 "${CONTAINER_NAME}" >&2 || true
    exit 1
  fi
  sleep 1
done
echo "[mullet] fleet started"

if [[ "${MULLET_BUILD}" == 1 ]]; then
  echo "[mullet] building the ROS 2 workspace (colcon build)..."
  if ! docker exec -e ROS_DOMAIN_ID="${ROS_DOMAIN_ID}" "${CONTAINER_NAME}" \
    bash -c "source /scripts/sourcefile.sh && cd /ws && colcon build"; then
    echo "[mullet] colcon build failed; the simulation is still running (./stop.sh to stop it)" >&2
    exit 1
  fi
fi

# core_cmd INSTANCE: the mullet_core command for one simulated drone (tmux pane or headless).
# Its launch file takes no arguments (one YAML per drone), so each instance runs the node on the
# packaged YAML with its instance and namespace overridden. The node's drone_id is uav_<N>.
core_params='$(ros2 pkg prefix mullet_core)/share/mullet_core/config/mullet_core_params.yaml'
core_cmd() {
  local ns_arg=""
  if [[ "$1" != 0 ]]; then ns_arg="-r __ns:=/${MULLET_NS_PREFIX}_$1 "; fi
  printf '%s' "ros2 run mullet_core mullet_core_node --ros-args ${ns_arg}-r __node:=mullet_core_$1 --params-file ${core_params} -p vehicle_instance:=$1 -p px4_namespace_prefix:=${MULLET_NS_PREFIX}"
}

if ((HEADLESS)); then
  drones=()
  if [[ " ${MULLET_WINDOWS} " == *" core "* ]]; then
    for inst in "${INSTANCES[@]}"; do
      docker exec -d -e ROS_DOMAIN_ID="${ROS_DOMAIN_ID}" "${CONTAINER_NAME}" bash -c \
        "source /scripts/sourcefile.sh && exec $(core_cmd "${inst}") > ${RUNTIME_DIR_IN_CONTAINER}/core_${inst}.log 2>&1"
      drones+=("uav_${inst}")
    done
    echo "[mullet] waiting for heartbeats from ${drones[*]}..."
    # Reads the heartbeat stream until every drone has spoken (heartbeat_json.py stops on the
    # closed pipe), so the status that matters is the reader's, not the stream's.
    wait_py='import json, sys
want = set(sys.argv[1:])
for line in sys.stdin:
    try:
        want.discard(json.loads(line).get("drone_id"))
    except ValueError:
        continue
    if not want:
        sys.exit(0)
sys.exit(1)'
    set +o pipefail
    docker exec -e ROS_DOMAIN_ID="${ROS_DOMAIN_ID}" "${CONTAINER_NAME}" bash -c \
      "source /scripts/sourcefile.sh && timeout 90 python3 -u /scripts/heartbeat_json.py" 2>/dev/null |
      python3 -c "${wait_py}" "${drones[@]}"
    heard_all="${PIPESTATUS[1]}"
    set -o pipefail
    if [[ "${heard_all}" != 0 ]]; then
      echo "[mullet] not every drone published a heartbeat within 90 s; see ${RUNTIME_DIR_IN_CONTAINER}/core_<N>.log in ${CONTAINER_NAME}" >&2
      exit 1
    fi
  fi
  echo "[mullet] live on ROS_DOMAIN_ID=${ROS_DOMAIN_ID}: ${drones[*]:-PX4 only (no core window)}. Stop with ./stop.sh"
  exit 0
fi

# ---- tmux (host, dedicated socket so your own tmux is untouched) ----
tmx() { tmux -L "${TMUX_SOCKET}" "$@"; }

# Panes run in the container. Interactive shells source /scripts/sourcefile.sh via ~/.bashrc.
docker_exec() { printf 'docker exec -it -e ROS_DOMAIN_ID=%q %s %s' "${ROS_DOMAIN_ID}" "${CONTAINER_NAME}" "$1"; }
tail_cmd() { docker_exec "tail -n 200 -F ${RUNTIME_DIR_IN_CONTAINER}/$1"; }
robot_color() { echo "${ROBOT_COLORS[($1 + 7) % 8]}"; }
ns_label() { if [[ "$1" == 0 ]]; then echo "instance 0"; else echo "/${MULLET_NS_PREFIX}_$1"; fi; }
has_window() { [[ " ${MULLET_WINDOWS} " == *" $1 "* ]]; }

# Remove panes from a previous run. Panes without a role (the TUI) stay.
tmx list-panes -s -t "${SESSION_NAME}" -F '#{pane_id} #{@mullet_role}' 2>/dev/null |
  awk '$2 != "" {print $1}' | while read -r id; do tmx kill-pane -t "${id}" || true; done || true

# add_pane WINDOW ROLE LABEL COLOR CMD [KEYS [ENTER]]
# Adds a pane to WINDOW (creating the window/session as needed), tags it for the TUI and
# queues KEYS to be typed into it (pressing Enter unless ENTER=0).
pending_panes=()
pending_keys=()
pending_enter=()
add_pane() {
  local window="$1" role="$2" label="$3" color="$4" cmd="$5" keys="${6:-}" enter="${7:-1}" id
  if tmx list-panes -t "${SESSION_NAME}:=${window}" >/dev/null 2>&1; then
    id="$(tmx split-window -d -P -F '#{pane_id}' -t "${SESSION_NAME}:=${window}" "${cmd}")"
    tmx select-layout -t "${SESSION_NAME}:=${window}" tiled >/dev/null
  elif tmx has-session -t "${SESSION_NAME}" 2>/dev/null; then
    id="$(tmx new-window -d -P -F '#{pane_id}' -t "${SESSION_NAME}:" -n "${window}" "${cmd}")"
  else
    id="$(tmx -f "${TMUX_CONF_PATH}" new-session -d -P -F '#{pane_id}' -s "${SESSION_NAME}" \
      -n "${window}" -x 200 -y 60 "${cmd}")"
  fi
  tmx set -p -t "${id}" @mullet_role "${role}"
  tmx set -p -t "${id}" @mullet_label "${label}"
  tmx set -p -t "${id}" @mullet_color "${color}"
  if [[ "${role}" == code:* ]]; then
    tmx set -p -t "${id}" remain-on-exit off
  fi
  if [[ -n "${keys}" ]]; then
    pending_panes+=("${id}")
    pending_keys+=("${keys}")
    pending_enter+=("${enter}")
  fi
}

if has_window px4; then
  for inst in "${INSTANCES[@]}"; do
    add_pane px4 "px4:${inst}" "PX4 · $(ns_label "${inst}")" "$(robot_color "${inst}")" "$(tail_cmd "px4_${inst}.log")"
  done
fi

if has_window xrce; then
  add_pane xrce xrce:0 "XRCE agent" "${MUTED_COLOR}" "$(tail_cmd xrce_agent.log)"
fi

if has_window core; then
  for inst in "${INSTANCES[@]}"; do
    add_pane core "core:${inst}" "CORE · $(ns_label "${inst}")" "$(robot_color "${inst}")" "$(docker_exec bash)" \
      "$(core_cmd "${inst}")"
  done
fi

if has_window code; then
  if ((MULLET_CMD_COUNT == 0)); then
    add_pane code code:0 "CODE" "${MUTED_COLOR}" "$(docker_exec bash)"
  fi
  for ((i = 0; i < MULLET_CMD_COUNT; i++)); do
    cmd_var="MULLET_CMD_${i}"
    autorun_var="MULLET_CMD_${i}_AUTORUN"
    add_pane code "code:${i}" "CODE · $((i + 1))" "${MUTED_COLOR}" "$(docker_exec bash)" "${!cmd_var}" "${!autorun_var}"
  done
fi

if [[ "${MULLET_RVIZ}" == 1 ]]; then
  rviz_cmd="rviz2"
  if [[ -n "${MULLET_RVIZ_CONFIG}" ]]; then rviz_cmd+=" -d ${MULLET_RVIZ_CONFIG}"; fi
  add_pane rviz rviz "RViz2" "${MUTED_COLOR}" "$(docker_exec bash)" "${rviz_cmd}"
fi

if [[ "${MULLET_FOXGLOVE}" == 1 ]]; then
  add_pane fox fox "Foxglove bridge" "${MUTED_COLOR}" "$(docker_exec bash)" \
    "ros2 launch foxglove_bridge foxglove_bridge_launch.xml"
fi

if [[ "${MULLET_BAG}" == 1 ]]; then
  bag_dir="/ws/bags/${MULLET_SESSION}_$(date +%Y%m%d_%H%M%S)"
  bag_args="${MULLET_BAG_TOPICS}"
  if [[ "${bag_args}" == all ]]; then bag_args="-a"; fi
  add_pane bag bag "rosbag → ${bag_dir}" "#FF5470" "$(docker_exec bash)" \
    "mkdir -p /ws/bags && ros2 bag record -o ${bag_dir} ${bag_args}"
fi

# Type the queued commands once each container shell shows its prompt
# (keys sent earlier are echoed twice by the terminal).
for i in "${!pending_panes[@]}"; do
  id="${pending_panes[$i]}"
  for _ in {1..50}; do
    if [[ "$(tmx capture-pane -p -t "${id}")" == *[\$#] ]]; then break; fi
    sleep 0.2
  done
  if [[ "${pending_enter[$i]}" == 1 ]]; then
    tmx send-keys -t "${id}" "${pending_keys[$i]}" Enter
  else
    tmx send-keys -t "${id}" "${pending_keys[$i]}"
  fi
done

if [[ "${MULLET_QGC}" == 1 ]]; then
  qgc="${QGC_PATH:-$(env_file_value QGC_PATH)}"
  qgc="${qgc:-$(command -v QGroundControl || true)}"
  if pgrep -f QGroundControl >/dev/null 2>&1; then
    echo "[mullet] QGroundControl already running"
  elif [[ -n "${qgc}" && -x "${qgc}" ]]; then
    setsid "${qgc}" >/dev/null 2>&1 </dev/null &
    echo "[mullet] started QGroundControl: ${qgc}"
  else
    echo "[mullet] warning: QGroundControl not found; set QGC_PATH in .env" >&2
  fi
fi

# Session-wide info and bindings that need absolute paths.
tmx set -g @mullet_domain "${ROS_DOMAIN_ID}"
tmx bind r source-file "${script_dir}/${TMUX_CONF_PATH}" \; display "Config reloaded!"
tmx bind S confirm-before -p "Stop the simulation? (y/n)" "run-shell -b '${script_dir}/stop.sh'"
tmx bind -n MouseDown1Status if -F '#{==:#{mouse_status_range},stop}' \
  "confirm-before -p 'Stop the simulation? (y/n)' \"run-shell -b '${script_dir}/stop.sh'\"" \
  'switch-client -t ='

echo "[mullet] tmux session ready: tmux -L ${TMUX_SOCKET} attach -t ${SESSION_NAME}"

if ((ATTACH)); then
  tmx select-window -t "${SESSION_NAME}:=px4" 2>/dev/null || true
  env -u TMUX tmux -L "${TMUX_SOCKET}" attach -t "${SESSION_NAME}" || true
  if ((KEEP)); then
    echo "[mullet] still running. Re-attach: tmux -L ${TMUX_SOCKET} attach -t ${SESSION_NAME} | stop: ./stop.sh"
  else
    ./stop.sh
  fi
fi
