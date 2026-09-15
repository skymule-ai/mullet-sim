# AGENTS.md (Repo Guide for Coding Agents)

Multi-vehicle PX4 SITL + Gazebo launcher (Docker) with a mounted ROS 2 workspace
in `ws/` and an optional Go TUI in `tui/`. License: MIT.

One container runs Gazebo + N PX4 instances + a single `MicroXRCEAgent`. The TUI
(`./mullet`) builds sessions and arranges tmux panes; everything also works with
`run.sh` alone.

The PX4 image `ghcr.io/skymule-ai/px4-sitl:v1.16.1` is public. Submodules
`ws/src/mullet-core` and `ws/src/mullet-interfaces` are private (SSH). Without
org access, run with `session.build: false` and `windows: [px4, xrce, code]`.

## Common commands

```sh
./setup.sh                                          # tools check, docker pull, .env, submodules, TUI
./run.sh --config config/sessions/example_uav.yaml  # [--no-attach] [--keep]
./stop.sh
python3 scripts/spawn_fleet.py --config config/sessions/example_uav.yaml --print-env
tui/build.sh && tui/build.sh test ./... && ./mullet
```

Inside the container:

```sh
cd /ws && colcon build
ros2 run mullet_core mullet_core_node --ros-args -r __ns:=/px4_1 \
  --params-file "$(ros2 pkg prefix mullet_core)/share/mullet_core/config/mullet_core_params.yaml" \
  -p vehicle_instance:=1 -p px4_namespace_prefix:=px4
```

`mullet_core.launch.py` takes no arguments (one YAML per drone), so `run.sh` starts
each simulated instance this way.

One-time without `setup.sh`: `cp .env.example .env` and (org SSH)
`git submodule update --init`.

Host tmux uses socket `mullet`. Detaching from `run.sh` brings compose down
unless `--keep`.

## Repo layout

- `docker-compose.yml`: `px4_sitl` / `px4_no_sitl` (host networking). `docker-compose.gpu.yml` is NVIDIA. Local mounts: gitignored `docker-compose.override.yml` (see the `.example`).
- `config/`: fleet/session YAML (stdlib subset), `tmux.conf`, DDS QoS XML.
- `scripts/`: container entrypoint + `spawn_fleet.py` (stdlib-only) + heartbeat helper.
- `tui/`: Go TUI. Binary `./mullet` at the repo root (`tui/build.sh`).
- `ws/`: ROS 2 workspace mounted at `/ws`.
  - `ws/src/mullet-core`, `ws/src/mullet-interfaces`: private submodules
  - `ws/src/px4_state_machine`: in-tree package
  - `ws/build/`, `ws/install/`, `ws/log/`: colcon output (do not commit)

## Architecture

### Launch flow

`./mullet` (optional) writes `config/sessions/<name>.yaml` and calls
`run.sh --no-attach`. `run.sh` → `spawn_fleet.py --print-env` (validation +
`MULLET_*` vars) → `--check-ports` → `docker compose [-f docker-compose.gpu.yml]
[-f docker-compose.override.yml] up -d --force-recreate px4_sitl` → wait for
`/tmp/runtime/ready` → `colcon build` **if** `session.build` is true → host tmux
panes `docker exec -it mullet_px4_sitl …`, tagged `@mullet_role`
(`px4:N`, `core:N`, `xrce`, `code:I`, `rviz`, `bag`, `fox`).

GPU overlay: `session.gpu` is `auto|on|off`. `auto` adds the GPU compose file
when `nvidia-smi` works.

### Container (`docker-compose.yml`)

- **`px4_sitl`** (`mullet_px4_sitl`): Gazebo + PX4 + `MicroXRCEAgent`. Entrypoint
  `scripts/entrypoint.sh` → `scripts/spawn_fleet.py`. `network_mode: host`. Image
  from `${PX4_SITL_IMAGE}` (`session.image`).
- **`px4_no_sitl`**: same image, idle, for extra ROS 2 nodes.

### Fleet launcher (`scripts/spawn_fleet.py`)

- Stdlib YAML subset: lists of mappings (`fleet:`, `commands:`) and one nested
  mapping level. No PyYAML, no anchors, no multi-line values, no `#` inside
  values, no commas inside inline-list items.
- Valid models come from the image's airframes (`ROMFS/.../airframes/<id>_gz_<model>`).
- Isolation: unique instances in `[0, 254]`, reserved params (`MAV_SYS_ID`,
  `UXRCE_DDS_KEY`, `UXRCE_DDS_DOM_ID`), free host UDP ports; warn for instances
  > 9 (shared offboard port).
- `PX4_UXRCE_DDS_NS=<prefix>_<N>` (N ≠ 0) when `simulation.namespace_prefix` is set.
- Starts `MicroXRCEAgent`, then PX4 in ascending instance order (0.8 s stagger).
  The **lowest** instance starts Gazebo; others set `PX4_GZ_STANDALONE=1`.
- Logs: `/tmp/runtime/px4_<N>.log`, `/tmp/runtime/xrce_agent.log`.

### TUI (`tui/`)

Go, Bubble Tea v2 + Lip Gloss + Huh.

```
cmd/mullet          CLI (tui / ctl / mirror)
internal/theme      palettes, icons, lipgloss / huh / tmux styles
internal/layout     dashboard split tree
internal/session    fleet YAML via spawn_fleet.py
internal/host       Docker probe, XDG settings/layouts
internal/tmux       tmux socket, panes, mirrors, ctl, launcher
internal/ui         Bubble Tea screens (FIGlet ANSI Shadow wordmark)
```

`session` loads configs through `spawn_fleet.py --print-json` and writes the same
YAML subset. `host` probes images (cached per image ID). `tmux` keeps real panes
in their own windows and fills `dash` with read-only mirrors (`tail -F` for
PX4/XRCE logs, `mullet mirror <pane>` otherwise), draws the clickable nav bar,
and handles `mullet ctl …` from tmux bindings.

State: `~/.config/mullet-sitl/settings.json`, `~/.config/mullet-sitl/layouts/*.json`,
cache in `~/.cache/mullet-sitl/`. `MULLET_DEBUG=1 ./mullet ctl …` traces tmux
commands. `scripts/heartbeat_json.py` streams `/mullet/heartbeat` as JSON (needs
`mullet_interfaces` in the container).

### ROS 2 workspace

- `mullet_core`: per-robot offboard supervisor; namespace `/<prefix>_<instance>`.
- `mullet_interfaces`: actions, `DroneHeartbeat` and the mission API
  (`MissionCommand`, `MissionStatus`, `LoadMission`).
- `px4_state_machine`: older standalone controllers. Mission frameworks (e.g. the
  ergodic MPPI) live in their own repos and talk to `mullet_core` over the mission API.

### `scripts/sourcefile.sh`

Sources ROS 2 and `/ws/install/setup.bash`. Appended to `~/.bashrc` in the
container; the image's `.bashrc` returns early for non-interactive shells, so
`bash -c` commands must source it explicitly.

## Configuration rules

Configs must stay parseable by `spawn_fleet.py`: no YAML anchors, no multi-line
values, no `#` inside values, no commas inside inline-list items.

- `fleet[].instance` unique in `[0, 254]`. Lowest instance starts Gazebo.
- Gaps are allowed (warning). Starting at `1` is recommended (`/uav_1`, …).
- Instance `0` has no ROS namespace (`/fmu/...`). Without `namespace_prefix`,
  instance `N` is `/px4_N`.
- `simulation.gz_ip`: `127.0.0.1` for the single-container setup.

## Coding style

- Prefer small, reviewable diffs. Do not commit `ws/build/`, `ws/install/`,
  `ws/log/`, `.env`, `docker-compose.override.yml`, `/mullet`.
- Fail fast on config errors.
- **Bash**: `set -euo pipefail`; quote variables.
- **Python (`scripts/`)**: stdlib-only unless the image is updated. Google-style
  docstrings on non-trivial functions. `heartbeat_json.py` may use `rclpy`
  (runs in the container).
- **Go (`tui/`)**: `gofmt`; `tui/build.sh vet ./...`. Package comments + exported
  godoc.
- **ROS 2**: keep `package.xml` and `CMakeLists.txt` in sync; colcon ignores
  `ws/venv/` via `COLCON_IGNORE`.

Python imports: stdlib, third-party, local. No wildcards. `snake_case` functions,
`CapWords` classes, `UPPER_SNAKE_CASE` constants. Type-hint public functions
where practical.

## Testing

```sh
tui/build.sh test ./...
python3 scripts/spawn_fleet.py --config config/sessions/example_uav.yaml --print-env
cd ws && colcon test && colcon test-result --verbose
```

CI (`.github/workflows/ci.yml`) runs the TUI tests and the spawn_fleet parse.
It does not pull the PX4 image or check out private submodules.

## Pointers

- Operator docs: `README.md`
- Vehicles/worlds: `docs/vehicles.md`
- Offboard VTOL topic contract: `ws/src/px4_state_machine/README_offboard.md`
