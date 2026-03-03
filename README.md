# mullet_sitl_sim

Multi-vehicle PX4 SITL + Gazebo (GZ) launcher built around `ghcr.io/skymule-ai/px4-sitl:v1.16.1`.

This repo focuses on:
- One container that starts Gazebo and N PX4 instances (multi-vehicle).
- One `MicroXRCEAgent` handling all PX4 uXRCE-DDS clients.
- Forwarded MAVLink UDP ports so QGroundControl can connect from the host.

## Requirements

- Linux host (PX4 multi-vehicle Gazebo support is Linux-only)
- Docker + Docker Compose
- `tmux`
- Optional: X11 (`xhost`) if you want Gazebo GUI on the host
- NVIDIA GPU (recommended): NVIDIA drivers + NVIDIA Container Toolkit for hardware-accelerated Gazebo

### NVIDIA Container Toolkit

If you have an NVIDIA GPU, install the NVIDIA Container Toolkit so Gazebo can use the GPU from inside the container.

- Install: `sudo apt-get install -y nvidia-container-toolkit`
- Restart Docker: `sudo systemctl restart docker`
- Verify:

```sh
docker run --rm --gpus all nvidia/cuda:12.2.0-base-ubuntu22.04 nvidia-smi
```

If Gazebo is still laggy, double-check:
- You are running an X11 session (not Wayland), or your X11 forwarding is working.
- `nvidia-smi` works inside the container: `docker exec -it mullet_px4_sitl nvidia-smi`

## Quick Start

1) Create env file (once):

```sh
cp .env.example .env
```

2) Edit the fleet config:

- `config/drones.yaml`

Container paths:
- `./config` -> `/config`
- `./scripts` -> `/scripts`
- `./src` -> `/ws/src`
- Runtime logs -> `/tmp/runtime`

3) Run:

```sh
./run.sh
```

Stop:

```sh
./stop.sh
```

## Custom code
To add custom code (e.g. ROS 2 nodes, Gazebo plugins, etc.):
1) Add your code to `src/` (or mount an external volume).
2) Clone the following repo to `src/` to get PX4 messages:
```sh
git clone https://github.com/PX4/px4_msgs.git
git clone https://github.com/PX4/px4_ros_com.git
```

## Configuration

Fleet config is `config/drones.yaml`.

- `simulation.world` sets `PX4_GZ_WORLD` (e.g. `default`, `windy`, `moving_platform`, `baylands`).
- `simulation.gz_ip` sets `GZ_IP` (Gazebo transport IP). Default is `127.0.0.1`, matching `make px4_sitl ...`.
- `simulation.headless: true` sets `HEADLESS=1` for PX4 (no Gazebo GUI).
- `simulation.ros_domain_id` sets `ROS_DOMAIN_ID` (DDS domain for ROS 2 and uXRCE-DDS agent).
- `simulation.xrce_agent.transport` and `simulation.xrce_agent.port` configure the MicroXRCEAgent command (default `udp4`, port `8888`).
- `fleet[].instance` must be a unique integer `>= 0`. Any starting offset is allowed (e.g. `1,2,3` or `10,11`). Gaps are allowed but a warning is printed. The launcher always launches in ascending instance order, and the **lowest** instance starts the Gazebo server.
- `fleet[].id` is an optional human-readable identifier (metadata only).
- `fleet[].px4_sys_autostart` is the PX4 airframe autostart id.
- `fleet[].px4_sim_model` is the Gazebo model name (PX4 uses `PX4_SIM_MODEL`). Example: `gz_x500`.
- `fleet[].pose` maps to `PX4_GZ_MODEL_POSE` (`x,y,z,roll,pitch,yaw` in meters/radians).
- For known PX4 SITL vehicles, `px4_sys_autostart` and `px4_sim_model` must match their standard pair (launcher validates this and fails fast on mismatch).

### Instance numbering and ROS 2 topic prefixes

PX4 maps instance numbers directly to ROS 2 topic namespaces via the uXRCE-DDS agent:

| Instance | ROS 2 prefix |
|----------|-------------|
| `0` | *(no prefix)* — topics at `/fmu/...` |
| `1` | `/px4_1/fmu/...` |
| `N` | `/px4_N/fmu/...` |

For multi-robot work, **starting your fleet at instance `1`** is recommended so that every vehicle gets a named prefix (`/px4_1/...`, `/px4_2/...`) and no vehicle occupies the ambiguous unprefixed namespace.

## Supported Vehicle Models

PX4 Gazebo simulation supports a variety of vehicle types. Reference the full list from [PX4 Gazebo Documentation](https://docs.px4.io/main/en/sim_gazebo_gz/).

### Multicopters

| Vehicle | Model Name | Autostart ID |
|---------|-----------|--------------|
| Quadrotor (X500) | `gz_x500` | 4001 |
| X500 with Depth Camera (Front) | `gz_x500_depth` | 4002 |
| X500 with Vision Odometry | `gz_x500_vision` | 4005 |
| X500 with 1D LIDAR (Down-facing) | `gz_x500_lidar_down` | 4016 |
| X500 with 2D LIDAR | `gz_x500_lidar_2d` | 4013 |
| X500 with 1D LIDAR (Front-facing) | `gz_x500_lidar_front` | 4017 |
| X500 with Gimbal (Front-facing) | `gz_x500_gimbal` | 4019 |

### Fixed-Wing Aircraft

| Vehicle | Model Name | Autostart ID |
|---------|-----------|--------------|
| Plane (RC Cessna) | `gz_rc_cessna` | 4003 |
| Advanced Plane | `gz_advanced_plane` | 4008 |

### VTOL (Vertical Takeoff & Landing)

| Vehicle | Model Name | Autostart ID |
|---------|-----------|--------------|
| Standard VTOL | `gz_standard_vtol` | 4004 |
| Quad Tailsitter VTOL | `gz_quadtailsitter` | 4018 |
| Tiltrotor VTOL | `gz_tiltrotor` | 4020 |

### Rovers

| Vehicle | Model Name | Autostart ID |
|---------|-----------|--------------|
| Differential Rover | `gz_rover_differential` | 50000 |
| Ackermann Rover | `gz_rover_ackermann` | 51000 |
| Mecanum Rover | `gz_rover_mecanum` | 52000 |

### Available Simulation Worlds

The simulation supports multiple worlds. Specify a world using the `simulation.world` config or by appending `_<world>` to model names:

| World | Description |
|-------|------------|
| `default` | Empty world (grey plane) |
| `aruco` | Empty world with ArUco marker for precision landing testing |
| `baylands` | Baylands world surrounded by water |
| `lawn` | Lawn world for testing rovers |
| `rover` | Rover world (optimized for rover testing) |
| `walls` | Wall world for collision prevention testing |
| `windy` | Empty world with wind enabled |
| `moving_platform` | World with moving takeoff/landing platform |

**Note:** Do not explicitly specify `_default` on model names as this will prevent PX4 from launching.

### Example Configuration

Single VTOL baseline — instance 0 (matches `make px4_sitl gz_standard_vtol` behavior, unprefixed ROS topics):

```yaml
simulation:
  world: default
  gz_ip: 127.0.0.1
  headless: false

fleet:
  - instance: 0
    px4_sys_autostart: 4004
    px4_sim_model: gz_standard_vtol
    pose: [0, 0, 0.1, 0, 0, 0]
```

Single VTOL baseline — instance 1 (ROS 2 topics under `/px4_1/...`):

```yaml
simulation:
  world: default
  gz_ip: 127.0.0.1
  headless: false

fleet:
  - instance: 1
    px4_sys_autostart: 4004
    px4_sim_model: gz_standard_vtol
    pose: [0, 0, 0.1, 0, 0, 0]
```

Multi-vehicle example starting at instance 1 (topics: `/px4_1/...`, `/px4_2/...`):

```yaml
simulation:
  world: windy
  gz_ip: 127.0.0.1
  headless: false

fleet:
  - instance: 1
    px4_sys_autostart: 4001
    px4_sim_model: gz_x500
    pose: [0, 0, 0, 0, 0, 0]
  - instance: 2
    px4_sys_autostart: 4004
    px4_sim_model: gz_standard_vtol
    pose: [5, 0, 0, 0, 0, 0]
```

## MAVLink Ports (Host)

This setup uses `network_mode: host` (Linux) so PX4's default SITL MAVLink
configuration (bound to localhost) works with QGroundControl on the host.

QGroundControl should auto-connect without any manual comm link.

Note: PX4 still prints `MAVLink only on localhost ...` in this configuration, but
that is fine with host networking because `127.0.0.1` is shared with the host.

## ROS 2 / DDS

- Set `ROS_DOMAIN_ID` in `.env`.
- External ROS 2 containers need the same `ROS_DOMAIN_ID` and must be able to reach the SITL container network.

Notes:
- The uXRCE agent is started inside the SITL container (`MicroXRCEAgent udp4 -p 8888`).
- We export `XRCE_DOMAIN_ID_OVERRIDE` to keep the agent's DDS domain aligned with `ROS_DOMAIN_ID` when supported.

In PX4 multi-vehicle ROS 2 setups you should see topics like:
- `/px4_1/...` (instance 1)
- `/px4_2/...` (instance 2)

If you use instance 0, that vehicle's topics appear unprefixed at the root (e.g. `/fmu/out/vehicle_local_position`).

## Environment Variables (Launcher)

These are consumed or set by `scripts/spawn_fleet.py`.

### Launcher-level

- `MULLET_RUNTIME_DIR` (default `/tmp/runtime`): log directory for `px4_<instance>.log` and `xrce_agent.log`.
- `ROS_DOMAIN_ID`: DDS domain for ROS 2 and the uXRCE-DDS agent (overridden by `simulation.ros_domain_id` when set).
- `GZ_IP`: Gazebo transport IP. Precedence: existing `GZ_IP` > `simulation.gz_ip` > `127.0.0.1`.
- `XRCE_DOMAIN_ID_OVERRIDE`: defaults to `ROS_DOMAIN_ID` to keep the agent domain aligned when supported.
- `PX4_BIN` (default `px4`): PX4 binary to execute.
- `PX4_GZ_SIM_RENDER_ENGINE`: optional renderer override (pass-through if set).

### Per-vehicle PX4/Gazebo variables

- `PX4_SYS_AUTOSTART` from `fleet[].px4_sys_autostart`.
- `PX4_SIM_MODEL` from `fleet[].px4_sim_model`.
- `PX4_GZ_WORLD` from `simulation.world`.
- `PX4_GZ_MODEL_POSE` from `fleet[].pose`.
- `HEADLESS=1` when `simulation.headless: true`.
- `PX4_GZ_STANDALONE=1` for all but the first-launched instance (lowest instance starts `gz-server`).

### PX4 parameter overrides (enforced defaults)

These parameters are set via environment variables for predictable SITL behavior. Update `scripts/spawn_fleet.py` if you need different values.

- `PX4_PARAM_COM_RC_IN_MODE=4`
- `PX4_PARAM_NAV_RCL_ACT=2`
- `PX4_PARAM_NAV_DLL_ACT=0`
- `PX4_PARAM_COM_DL_LOSS_T=300`
- `PX4_PARAM_COM_RC_LOSS_T=300`

## Future Work

- MAVLink router / forwarding rules to match real-drone networking.
- Replace the minimal YAML parser with PyYAML (or embed config in JSON) once the container has the dependency.
- Add a `tmuxinator` config generator (currently we use plain tmux).
