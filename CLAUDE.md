# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repo Is

Multi-vehicle PX4 SITL + Gazebo simulator using Docker. One container runs Gazebo + N PX4 instances + a single `MicroXRCEAgent`. The `ws/` directory is a ROS 2 workspace mounted into the container.

## Common Commands

```sh
# Start simulation (attaches to tmux inside container; exiting tmux brings compose down)
./run.sh

# Stop simulation
./stop.sh

# Build ROS 2 workspace (run inside container or with ROS 2 sourced)
cd ws && colcon build

# Launch the VTOL offboard state machine
ros2 launch px4_state_machine px4_state_machine.launch.py

# Launch HEDAC coverage node
ros2 launch hedac_ros2 hedac_px4.launch.py

# Run HEDAC standalone simulation (from ws/src/hedac/)
uv run python examples/run_hedac.py --config configs/sitl_px4_params.yaml --no-plot

# Lint HEDAC Python code
uv run ruff check .
```

### One-time setup
```sh
cp .env.example .env   # set ROS_DOMAIN_ID, DISPLAY, etc.
```

### ROS 2 deps (not tracked, clone into ws/src/)
```sh
git clone https://github.com/PX4/px4_msgs.git ws/src/px4_msgs
git clone https://github.com/PX4/px4_ros_com.git ws/src/px4_ros_com
```

## Architecture

### Container layer (`docker-compose.yml`)
- **`px4_sitl`** (`mullet_px4_sitl`): runs Gazebo + PX4 + `MicroXRCEAgent`. Entrypoint is `scripts/entrypoint.sh` → `scripts/spawn_fleet.py`. Uses `network_mode: host` for QGC auto-connect.
- **`px4_no_sitl`** (`mullet_px4_no_sitl`): same image, no SITL. Used to run custom ROS 2 nodes against a running `px4_sitl` container.
- Both mount `./ws` → `/ws`, `./config` → `/config`, `./scripts` → `/scripts`, and `./.venv` → `/ws/venv`.

### Fleet launcher (`scripts/spawn_fleet.py`)
- Parses `config/drones.yaml` with a stdlib-only YAML parser (no PyYAML—container doesn't have it). **Avoid YAML anchors and multi-line blocks in drones.yaml.**
- Validates autostart/model pairs using `KNOWN_AUTOSTART_TO_MODEL` and fails fast on mismatch.
- Starts `MicroXRCEAgent` first, then PX4 instances in order with a 0.8 s stagger. Instance 0 starts Gazebo; instances 1+ set `PX4_GZ_STANDALONE=1`.
- Logs per-instance to `/tmp/runtime/px4_<N>.log` and `/tmp/runtime/xrce_agent.log`.

### ROS 2 workspace (`ws/src/`)
Two tracked packages:

**`px4_state_machine`** — VTOL offboard controller
- Main node: `scripts/offboard.py` (`vtol_sm` node). Config: `config/offboard_params.yaml`.
- State machine: `WARMUP_SETPOINTS → REQUEST_OFFBOARD_ARM → MC_TAKEOFF → MC_HOVER → ALIGN_YAW_FOR_FW → [PRE_TRANSITION_ACCEL →] AWAITING_FW_TRANSITION → FW_EXECUTE`.
- FW guidance uses pure pursuit with straight-line or analytic circular lookahead (loiter mode).
- Key PX4 constraint: FW offboard setpoints must have finite XYZ position (no partial-NaN vectors).
- Control interface via ROS topics: `command` (Float32MultiArray), `request_fw`, `request_mc`, `land_request` (all Empty).

**`hedac`** — HEDAC (Heat Equation Driven Area Coverage) ergodic controller
- Pure Python library under `ws/src/hedac/src/`: `core/hedac.py` (main algo), `models/agents.py`, `utils/`.
- ROS 2 bridge: `ws/src/hedac/hedac_ros2/` — subscribes to `/fmu/out/vehicle_local_position`, runs HEDAC, publishes `Float32MultiArray [vx, vy, 1]` to `command`.
- Config: YAML files under `ws/src/hedac/configs/`. Load via `HEDACParams.from_yaml(...)`.
- Has its own `uv`-managed venv (separate `pyproject.toml` at root for host-side deps).

### `scripts/sourcefile.sh`
Sources ROS 2 and `/ws/install/setup.bash` in every shell. Appended to `~/.bashrc` inside the container so all tmux panes inherit the environment.

## Configuration Rules

- `config/drones.yaml`: fleet instances must be **unique, start at 0, and be contiguous**. Do not add YAML anchors or multi-line blocks (custom parser limitation).
- `fmu_prefix` in `offboard_params.yaml`: for multi-vehicle, set to `/px4_<N>` to match uXRCE topic namespacing.
- `simulation.gz_ip`: use `127.0.0.1` for single-container setup (matches PX4's default `make px4_sitl` behavior).

## Coding Style

- **Bash scripts**: keep `set -euo pipefail`.
- **Python (scripts/)**: stdlib-only; no third-party imports unless the container image is updated.
- **Python (hedac/)**: managed by `uv`; add deps to `ws/src/hedac/pyproject.toml`. Use `ruff` for lint.
- **ROS 2 packages**: keep `package.xml` and `CMakeLists.txt` in sync; colcon ignores `ws/venv/` via `COLCON_IGNORE`.

## Testing

No automated tests are wired in. ROS 2 tests integrate via `cd ws && colcon test`. HEDAC tests (when added): `uv run pytest` from `ws/src/hedac/`.
