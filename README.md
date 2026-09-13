<p align="center">
  <img src="docs/assets/mullet.svg" width="720" alt="MULLET">
</p>

<p align="center">
  <strong>Multi-robot PX4 SITL · Gazebo · ROS 2</strong><br>
  One container. N vehicles. A tmux dashboard — or just <code>./run.sh</code>.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-4CC2FF?style=flat-square" alt="MIT"></a>
  <img src="https://img.shields.io/badge/PX4-v1.16.1-FFB547?style=flat-square" alt="PX4">
  <img src="https://img.shields.io/badge/ROS%202-Humble-3DDC97?style=flat-square" alt="ROS 2">
  <img src="https://img.shields.io/badge/tmux-3.4+-8B93A7?style=flat-square" alt="tmux">
</p>

The wordmark is **FIGlet ANSI Shadow** (the same boxed letters as the TUI), without the terminal plate or shine.

---

## What this is

MULLET launches a multi-vehicle [PX4](https://px4.io/) SITL + Gazebo (GZ) fleet from a YAML file.

- One Docker container starts Gazebo, N PX4 instances, and a single `MicroXRCEAgent`.
- The host runs tmux on socket `mullet` (`tmux -L mullet`). Detaching stops the sim unless you pass `--keep`.
- An optional Go TUI (`./mullet`) builds the fleet, themes tmux, and lays out a live dashboard.
- Networking is `network_mode: host` (Linux). QGroundControl auto-connects on localhost — nothing is published as forwarded UDP ports.

PX4 image (public): `ghcr.io/skymule-ai/px4-sitl:v1.16.1`.

ROS packages `mullet-core` and `mullet-interfaces` are **private SkyMule submodules**. You can still run PX4 + Gazebo + XRCE without them.

## Contents

- [Requirements](#requirements)
- [Install](#install)
- [Quick start](#quick-start)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [TUI](#tui)
- [GPU](#gpu)
- [ROS 2](#ros-2)
- [Troubleshooting](#troubleshooting)

## Requirements

| Required | Optional |
|----------|----------|
| Linux host | NVIDIA GPU + [NVIDIA Container Toolkit](#gpu) |
| [Docker Engine](https://docs.docker.com/engine/install/) + Compose plugin | X11 (`xhost`) for Gazebo GUI |
| `tmux` ≥ 3.4 (clickable TUI tabs) | Go 1.25, or Docker, to build the TUI |
| `python3` (stdlib only on the host) | QGroundControl (`QGC_PATH` in `.env`) |
| `git` | SkyMule org SSH (private ROS submodules) |

## Install

```sh
git clone https://github.com/skymule-ai/mullet_sitl_sim.git
cd mullet_sitl_sim
chmod +x setup.sh run.sh stop.sh
./setup.sh
```

`setup.sh` checks the tools, `docker pull`s the PX4 image, copies `.env.example` → `.env`, tries `git submodule update --init`, and builds `./mullet` when it can.

Set `DISPLAY` in `.env` to your X11 display (`echo $DISPLAY`). If you use QGroundControl, set `QGC_PATH`.

### Without org access

Submodule init will fail. PX4 + Gazebo still work. Use a session like:

```yaml
session:
  build: false
  windows: [px4, xrce, code]
```

`core` panes and the heartbeat dashboard need the private submodules and `colcon build`.

### Manual steps (same as setup.sh)

```sh
docker pull ghcr.io/skymule-ai/px4-sitl:v1.16.1
cp .env.example .env
git submodule update --init          # SkyMule SSH
tui/build.sh                         # optional → ./mullet
```

## Quick start

### TUI

```sh
tui/build.sh
./mullet
```

Build the fleet (models and worlds are discovered from local PX4 images), press `L` to launch. Sessions are written to `config/sessions/<name>.yaml`.

### CLI

```sh
./run.sh --config config/sessions/example_uav.yaml
./stop.sh
```

`run.sh --keep` leaves the simulation running after you detach. `prefix+S` or the `■ STOP` status button also stops it.

Validate a config on the host:

```sh
python3 scripts/spawn_fleet.py --config config/sessions/example_uav.yaml --print-env
```

Container mounts: `./config` → `/config`, `./scripts` → `/scripts`, `./ws` → `/ws`. Logs live in `/tmp/runtime`. Extra mounts: copy [`docker-compose.override.yml.example`](docker-compose.override.yml.example) to `docker-compose.override.yml` (gitignored).

Interactive diagram: [`docs/sitl_architecture.html`](docs/sitl_architecture.html).

## Architecture

```
host:  ./mullet | ./run.sh
          │
          ├─ spawn_fleet.py --print-env / --check-ports
          ├─ docker compose up px4_sitl     (optional gpu overlay)
          ├─ wait /tmp/runtime/ready
          ├─ colcon build                   (if session.build)
          └─ tmux -L mullet                 (@mullet_role panes)

container: entrypoint.sh → spawn_fleet.py
             MicroXRCEAgent
             PX4 instance 1  ← starts Gazebo
             PX4 instance 2  ← PX4_GZ_STANDALONE=1
             …
```

## Configuration

Parsed by `scripts/spawn_fleet.py` (stdlib YAML subset: **no anchors, no multi-line values, no `#` inside values, no commas in inline-list items**). Canonical example: [`config/sessions/example_uav.yaml`](config/sessions/example_uav.yaml).

| Key | Role |
|-----|------|
| `simulation.world` | `PX4_GZ_WORLD` (`default`, `windy`, `baylands`, …) |
| `simulation.gz_ip` | Gazebo transport. Use `127.0.0.1` here |
| `simulation.headless` | `HEADLESS=1` (no Gazebo GUI) |
| `simulation.ros_domain_id` | DDS domain everywhere (config > shell > `.env` > 0) |
| `simulation.namespace_prefix` | instance N → `/<prefix>_N` (else `/px4_N`) |
| `simulation.speed_factor` | `PX4_SIM_SPEED_FACTOR` |
| `simulation.px4_params` / `fleet[].params` | `NAME=VALUE` (not `MAV_SYS_ID` / DDS keys) |
| `fleet[].instance` | unique integer in `[0, 254]`; **lowest** starts Gazebo |
| `fleet[].model` | `gz_x500`, `gz_standard_vtol`, … (from the image) |
| `fleet[].pose` | `x,y,z,roll,pitch,yaw` |
| `session.image` | PX4 image ref |
| `session.gpu` | `auto` \| `on` \| `off` |
| `session.build` | run `colcon build` after the fleet is ready |
| `session.windows` | `px4`, `xrce`, `core`, `code` |
| `commands[]` | code-window shells; `{instances}` and `{ns_prefix}` expand |

Vehicle and world tables: [`docs/vehicles.md`](docs/vehicles.md).

### Isolation (MAVLink / DDS)

`MAV_SYS_ID = UXRCE_DDS_KEY = instance + 1`. Each instance binds UDP `18570+i` (GCS), `14580+i` (offboard), `14280+i` (payload), `13030+i` (gimbal). QGC sees every robot on 14550 and tells them apart by system id.

Instances above 9 share PX4's offboard remote port (14549). `run.sh` refuses to start if those UDP ports are already taken on the host.

### ROS 2 namespaces

| Instance | Prefix |
|----------|--------|
| `0` | none — `/fmu/...` |
| `N` without `namespace_prefix` | `/px4_N/fmu/...` |
| `N` with `namespace_prefix: uav` | `/uav_N/...` |

Start fleets at instance `1` so no vehicle occupies the unprefixed namespace.

### Custom ROS 2 code

Add packages under `ws/src/` (or an override mount). Optional PX4 ROS deps (gitignored):

```sh
git clone https://github.com/PX4/px4_msgs.git ws/src/px4_msgs
git clone https://github.com/PX4/px4_ros_com.git ws/src/px4_ros_com
```

Persisted PX4 params can mask airframe defaults: `MULLET_RESET_PARAMS=1 ./run.sh`. Force a PX4 rebuild with `MULLET_PX4_REBUILD=1`.

## TUI

```
tui/build.sh              # ./mullet (local Go, or golang Docker image)
tui/build.sh test ./...
./mullet
```

Sessions: `config/sessions/<name>.yaml`. Settings and layouts: `~/.config/mullet-sitl/`. Package map: [`tui/README.md`](tui/README.md).

Every block (PX4, CORE, XRCE, CODE, …) keeps its panes in its own tmux window. The dashboard next to the TUI shows read-only mirrors of the ones you pick. Status bar: `PX4 ▾ 1 2 3 ⊞` — `▾` picker, chips toggle mirrors, `⊞` / `M-v` single vs multi, `M-1..9` jump to robot N, `■ STOP` always visible. Layout editor: `l`. Themes: Mullet Dusk, Aviation HUD, Catppuccin Mocha.

## GPU

`run.sh` adds [`docker-compose.gpu.yml`](docker-compose.gpu.yml) when `session.gpu` is `on`, or `auto` and `nvidia-smi` works.

```sh
sudo apt-get install -y nvidia-container-toolkit
sudo systemctl restart docker
docker run --rm --gpus all nvidia/cuda:12.2.0-base-ubuntu22.04 nvidia-smi
```

If Gazebo is still laggy: prefer an X11 session (not Wayland), and check `docker exec -it mullet_px4_sitl nvidia-smi`.

## ROS 2

Set `ROS_DOMAIN_ID` in `.env` (overridden by `simulation.ros_domain_id`). The agent is `MicroXRCEAgent udp4 -p 8888` inside the SITL container. `XRCE_DOMAIN_ID_OVERRIDE` tracks the same domain.

Non-interactive `bash -c` in the container must `source /scripts/sourcefile.sh` — the image `.bashrc` returns early for non-interactive shells.

## Troubleshooting

| Symptom | What to try |
|---------|-------------|
| `docker pull` denied | The PX4 image should be public. Check the tag in `session.image`. |
| Gazebo / RViz blank | `DISPLAY` in `.env` matches `echo $DISPLAY`; `xhost +local:docker` |
| GPU overlay missing | `nvidia-smi` on the host; `session.gpu: on` |
| `colcon` / CORE missing | Private submodules: org SSH, or `build: false` and drop `core` from `windows` |
| Port error on start | Another SITL/agent still bound; `./stop.sh`, then retry |
| TUI tabs not clickable | tmux ≥ 3.4 |
| Detach killed the sim | `./run.sh --keep`, then `tmux -L mullet attach -t mullet` |

## License

[MIT](LICENSE)
