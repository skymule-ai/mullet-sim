# Vehicles and worlds

Models and autostart IDs below match `ghcr.io/skymule-ai/px4-sitl:v1.17.0`. The
launcher and the TUI read valid models from the airframes of the image you run,
so other PX4 versions may add or rename vehicles. Full PX4 list:
[Gazebo simulation](https://docs.px4.io/main/en/sim_gazebo_gz/).

Do not put `_default` on model names; that prevents PX4 from launching.

## Multicopters

| Vehicle | Model | Autostart |
|---------|-------|-----------|
| Quadrotor (X500) | `gz_x500` | 4001 |
| X500 with Depth Camera (Front) | `gz_x500_depth` | 4002 |
| X500 with Vision Odometry | `gz_x500_vision` | 4005 |
| X500 with 1D LIDAR (Down-facing) | `gz_x500_lidar_down` | 4016 |
| X500 with 2D LIDAR | `gz_x500_lidar_2d` | 4013 |
| X500 with 1D LIDAR (Front-facing) | `gz_x500_lidar_front` | 4017 |
| X500 with Gimbal (Front-facing) | `gz_x500_gimbal` | 4019 |

## Fixed-wing

| Vehicle | Model | Autostart |
|---------|-------|-----------|
| Plane (RC Cessna) | `gz_rc_cessna` | 4003 |
| Advanced Plane | `gz_advanced_plane` | 4008 |

## VTOL

| Vehicle | Model | Autostart |
|---------|-------|-----------|
| Standard VTOL | `gz_standard_vtol` | 4004 |
| Quad Tailsitter VTOL | `gz_quadtailsitter` | 4018 |
| Tiltrotor VTOL | `gz_tiltrotor` | 4020 |

## Rovers

| Vehicle | Model | Autostart |
|---------|-------|-----------|
| R1 Rover | `gz_r1_rover` | 4009 |
| Ackermann Rover | `gz_rover_ackermann` | 4012 |
| R1 Mecanum Rover | `gz_r1_rover_mecanum` | 4015 |
| Zero Turn Lawnmower | `gz_lawnmower` | 4011 |

## Worlds

Set `simulation.world` (or append `_<world>` to a model name, except `_default`).

| World | Description |
|-------|-------------|
| `default` | Empty world (grey plane) |
| `aruco` | ArUco marker for precision landing |
| `baylands` | Water around the field |
| `lawn` | Lawn, useful for rovers |
| `rover` | Rover-oriented world |
| `walls` | Collision-prevention walls |
| `windy` | Empty world with wind |
| `moving_platform` | Moving takeoff/landing platform |
