# ergodic_mppi_ros2

Self-contained ROS 2 package for centralized multi-robot ergodic MPPI control in PX4 SITL.

## Scope

- Contains MPPI core, Stein objective, model dynamics, and config loader directly in this package.
- Runs one centralized coordinator node: `ergodic_mppi_coordinator_node`.
- Subscribes to PX4 local position for each instance and publishes commands on `/px4_N/command`.
- Uses X500-compatible command format:
  - `Float32MultiArray([vx, vy, 1])`

## Package modules

- `ergodic_mppi_ros2/mppi_core.py`
- `ergodic_mppi_ros2/stein.py`
- `ergodic_mppi_ros2/double_integrator.py`
- `ergodic_mppi_ros2/params_loader.py`
- `ergodic_mppi_ros2/frame_utils.py`
- `ergodic_mppi_ros2/runtime.py`

## SITL scale defaults

Default MPPI config is `config/mppi_sitl_100x100.yaml`:
- map limits: `[0, 100] x [0, 100]`
- multi-modal target density scaled for 100 m environment
- runtime-oriented MPPI settings (`K=256`, `T=45`, `delta_t=0.2`)

## Prerequisites

This package imports `jax`/`jaxlib` at runtime. In this repo, dependencies are
installed into the root workspace virtualenv (`./.venv`), which is mounted
inside the SITL container as `/ws/venv`.

From repo root:

```bash
uv sync
```

Inside the container shell:

```bash
source /scripts/sourcefile.sh
cd /ws && colcon build --symlink-install
```

## Launch

```bash
ros2 launch ergodic_mppi_ros2 ergodic_mppi_px4.launch.py px4_instances:=1,2,3,4
```

Override MPPI config if needed:

```bash
ros2 launch ergodic_mppi_ros2 ergodic_mppi_px4.launch.py \
  px4_instances:=1,2,3,4 \
  mppi_config_path:=/ws/src/ergodic_mppi_ros2/config/mppi_sitl_100x100.yaml
```

## Visualization

Launch RViz visualization (starts RViz by default):

```bash
ros2 launch ergodic_mppi_ros2 ergodic_mppi_viz.launch.py px4_instances:=1,2,3,4
```

Published topics:
- `/ergodic_mppi_viz/robots` (`visualization_msgs/MarkerArray`)
- `/ergodic_mppi_viz/trails` (`visualization_msgs/MarkerArray`)
- `/ergodic_mppi_viz/obstacles` (`visualization_msgs/MarkerArray`)
- `/ergodic_mppi_viz/target_density` (`nav_msgs/OccupancyGrid`)

## Troubleshooting

- `ModuleNotFoundError: No module named 'jax'`:
  - Ensure dependencies were synced in repo root (`uv sync`) so `./.venv`
    includes `jax` and `jaxlib`.
  - Re-source environment (`source /scripts/sourcefile.sh`) before launch.
- `An NVIDIA GPU may be present ... CUDA-enabled jaxlib is not installed`:
  - Informational only; the visualizer falls back to CPU and can still run.
- `No mesh visual found ...` from `ergodic_mppi_rviz_visualizer`:
  - Verify `model_dir` / `model_sdf` point to a valid PX4 Gazebo model.
  - If your model layout is custom, set `mesh_resource` explicitly in
    `ergodic_mppi_rviz_visualizer.yaml`.
