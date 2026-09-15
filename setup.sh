#!/usr/bin/env bash
# Check host tools, pull the public PX4 SITL image, copy .env, and try org submodules.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

DEFAULT_IMAGE="${PX4_SITL_IMAGE:-ghcr.io/skymule-ai/px4-sitl:v1.17.0}"
# The image's PX4 line (1.17.x, as on the drones' Pixhawks); mullet_core builds against it.
PX4_MSGS_BRANCH="release/1.17"
need=()

need_cmd() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    need+=("${name}")
  fi
}

if [[ "$(uname -s)" != Linux ]]; then
  echo "mullet SITL is Linux-only (PX4 multi-vehicle Gazebo)." >&2
  exit 1
fi

need_cmd docker
need_cmd tmux
need_cmd python3
need_cmd git

if ! docker compose version >/dev/null 2>&1; then
  need+=("docker compose")
fi

if ((${#need[@]})); then
  echo "Missing required tools: ${need[*]}" >&2
  echo "Install Docker Engine + Compose plugin, tmux (>= 3.4 for TUI tabs), python3, and git." >&2
  exit 1
fi

echo "[mullet] pulling ${DEFAULT_IMAGE}"
docker pull "${DEFAULT_IMAGE}"

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "[mullet] wrote .env from .env.example — set DISPLAY (and QGC_PATH if you use QGroundControl)"
else
  echo "[mullet] keeping existing .env"
fi

if GIT_TERMINAL_PROMPT=0 git submodule update --init; then
  echo "[mullet] submodules ready (mullet-core, mullet-interfaces)"
else
  cat >&2 <<'EOF'
[mullet] could not init private submodules (SkyMule org SSH access required).
PX4 + Gazebo + MicroXRCEAgent still work. In your session YAML:

session:
  build: false
  windows: [px4, xrce, code]

CORE panes and the heartbeat dashboard need the submodules and colcon build.
EOF
fi

if [[ ! -f ws/src/px4_msgs/package.xml ]]; then
  echo "[mullet] cloning px4_msgs ${PX4_MSGS_BRANCH} into ws/src/px4_msgs"
  rmdir ws/src/px4_msgs 2>/dev/null || true
  git clone --depth 1 --branch "${PX4_MSGS_BRANCH}" https://github.com/PX4/px4_msgs.git ws/src/px4_msgs
fi

echo "[mullet] building TUI → ./mullet (optional)"
if ! tui/build.sh; then
  echo "[mullet] TUI build skipped; ./run.sh still works without it" >&2
fi

echo
echo "[mullet] ready. Next:"
echo "  ./mullet"
echo "  # or: ./run.sh --config config/sessions/example_uav.yaml"
