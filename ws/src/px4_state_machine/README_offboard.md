# Offboard VTOL Controller (`scripts/offboard.py`)

## Purpose
This node controls a PX4 VTOL with one offboard state machine:
- MC takeoff and hover are position-controlled.
- MC -> FW transition is command-driven (`request_fw_topic`) and gated by yaw alignment and optional airspeed.
- FW setpoints are PX4 v1.16.1-safe: finite `position=[x,y,z]` and finite `velocity=[vx,vy,0]`.
- Horizontal guidance is pure pursuit toward a lookahead target:
  - straight-line lookahead when `loiter=false`
  - analytic circular lookahead when `loiter=true` and state is `FW_EXECUTE`
- `FW_SET` updates heading/speed/altitude at runtime.
- FW -> MC transition uses `request_mc_topic`.
- Landing is triggered by `land_topic`.

## PX4 v1.16.1 Setpoint Rules
For fixed-wing offboard in PX4 v1.16.1, partial-NaN position vectors are not handled as per-axis disables in `fw_pos_control`.
This controller therefore publishes finite XYZ position in FW phases so TECS gets a valid altitude target.
Velocity is used as feedforward/path intent, not as standalone altitude control.

## State Flow
`WARMUP_SETPOINTS -> REQUEST_OFFBOARD_ARM -> MC_TAKEOFF -> MC_HOVER`

From `MC_HOVER`:
- `request_fw_topic` -> `ALIGN_YAW_FOR_FW`

From `ALIGN_YAW_FOR_FW`:
- Holds MC position, rotates yaw to `fw_heading_deg`.
- If aligned:
  - `fw_min_arsp_gate_mps > 0` -> `PRE_TRANSITION_ACCEL`
  - else -> `AWAITING_FW_TRANSITION`

From `PRE_TRANSITION_ACCEL`:
- Still in MC, publishes forward FW-style setpoint to build airspeed.
- On TAS gate reached -> `AWAITING_FW_TRANSITION`

From `AWAITING_FW_TRANSITION`:
- Holds neutral MC hover setpoint.
- Sends VTOL FW transition command once.
- On FW confirmation -> `FW_EXECUTE`

From `FW_EXECUTE`:
- Publishes FW pure-pursuit setpoints continuously.
- `request_mc_topic` -> `REQUEST_VTOL_MC`

From `REQUEST_VTOL_MC`:
- Keeps FW setpoint alive and commands back-transition.
- On MC confirmation -> `MC_HOVER`

From any state:
- `land_topic` -> `LANDING`

## Topic Interface

### PX4 topics
- Publishes:
  - `<fmu_prefix>/fmu/in/offboard_control_mode`
  - `<fmu_prefix>/fmu/in/trajectory_setpoint`
  - `<fmu_prefix>/fmu/in/vehicle_command`
- Subscribes:
  - `<fmu_prefix>/fmu/out/vehicle_local_position`
  - `<fmu_prefix>/fmu/out/vehicle_status_v1`
  - `<fmu_prefix>/fmu/out/vtol_vehicle_status`
  - `<fmu_prefix>/fmu/out/airspeed_validated`

### Control topics
- `command_topic` (`std_msgs/String`)
  - `FW_SET HDG=<deg> SPD=<mps> ALT=<m>`
- `request_fw_topic` (`std_msgs/Empty`): MC -> FW request
- `request_mc_topic` (`std_msgs/Empty`): FW -> MC request
- `land_topic` (`std_msgs/Empty`): land request

## Parameters
- Core/system:
  - `fmu_prefix` (string, default `""`)
  - `target_system` (int, default `1`)
  - `target_component` (int, default `1`)
- Topic names:
  - `command_topic` (string, default `"command"`)
  - `request_fw_topic` (string, default `"request_fw"`)
  - `request_mc_topic` (string, default `"request_mc"`)
  - `land_topic` (string, default `"land_request"`)
- MC:
  - `takeoff_height_m` (float)
  - `hover_yaw_rad` (float)
  - `setpoint_rate_hz` (float)
  - `offboard_warmup_cycles` (int)
  - `takeoff_reached_tol_m` (float)
  - `yaw_align_tol_deg` (float)
- FW:
  - `fw_heading_deg` (float)
  - `fw_speed_mps` (float)
  - `fw_alt_m` (float)
  - `fw_lookahead_m` (float)
  - `fw_min_arsp_gate_mps` (float)
- Loiter:
  - `loiter` (bool)
  - `loiter_radius_m` (float)
  - `loiter_lookahead_m` (float, arc-length lookahead on circle)
- Debug:
  - `debug_enabled` (bool)
  - `debug_topic` (string)

## FW Guidance Details
- Straight mode (`loiter=false`): target XY comes from line projection + `fw_lookahead_m`.
- Loiter mode (`loiter=true`, `FW_EXECUTE`): target XY is analytic circle lookahead:
  - `theta = atan2(y - cy, x - cx)`
  - `delta = loiter_lookahead_m / loiter_radius_m`
  - direction sign from `loiter_direction` (`1=CW`, `-1=CCW`)
- Both modes pass target XY through the same pure-pursuit helper to compute `vx`, `vy`, and `yaw`.

## Run
```bash
ros2 launch px4_state_machine px4_state_machine.launch.py
```

## Command Examples
```bash
ros2 topic pub /request_fw std_msgs/msg/Empty "{}"
```

```bash
ros2 topic pub /command std_msgs/msg/String "{data: 'FW_SET HDG=90 SPD=17 ALT=60'}"
```

```bash
ros2 topic pub /request_mc std_msgs/msg/Empty "{}"
```

```bash
ros2 topic pub /land_request std_msgs/msg/Empty "{}"
```

## Expected Logs
- `State: ... -> MC_HOVER`
- `State: MC_HOVER -> ALIGN_YAW_FOR_FW`
- `State: ALIGN_YAW_FOR_FW -> PRE_TRANSITION_ACCEL` (if airspeed gate enabled)
- `State: ... -> AWAITING_FW_TRANSITION`
- `FW transition command sent, awaiting completion...`
- `FW transition complete!`
- `State: ... -> FW_EXECUTE`

## Troubleshooting
- If FW request is ignored, verify the vehicle is in `MC_HOVER`.
- If transition stalls, verify `airspeed_validated` and `fw_min_arsp_gate_mps`.
- If FW altitude drifts, inspect `/fmu/in/trajectory_setpoint` and confirm finite XYZ position.
- If loiter entry is oscillatory, increase `loiter_lookahead_m` relative to `fw_speed_mps` and `loiter_radius_m`.
- For diagnostics, set `debug_enabled=true` and inspect `debug_topic`.
