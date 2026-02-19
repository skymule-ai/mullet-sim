# Offboard VTOL Controller (`scripts/offboard.py`)

## Purpose
This node controls a PX4 VTOL in offboard mode with a simplified behavior:
- Default: multicopter hover at a latched position.
- On FW command: transition to fixed-wing and execute the command.
- If FW commands stop: immediately transition back to multicopter and relatch hover.
- Landing is triggered by a separate empty-message topic.

## State Flow
`WARMUP_SETPOINTS -> REQUEST_OFFBOARD_ARM -> MC_TAKEOFF -> MC_HOVER`

From `MC_HOVER`:
- valid FW command -> `REQUEST_VTOL_FW -> FW_EXECUTE`

From `FW_EXECUTE`:
- fresh FW command this cycle -> keep executing FW target
- no fresh FW command this cycle -> `REQUEST_VTOL_MC -> MC_HOVER`

From any state:
- land topic message -> `LANDING`

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

### Command topics
- `command_topic` (`std_msgs/String`)
  - `FW_SET HDG=<deg> SPD=<mps> ALT=<m>`
  - `FW_HOLD` (optional, keeps current FW target for one cycle)
- `land_topic` (`std_msgs/Empty`)
  - any message triggers landing

## Parameters
- `fmu_prefix` (string, default: `""`)
- `takeoff_height_m` (float, default: `5.0`)
- `hover_yaw_rad` (float, default: `0.0`)
- `setpoint_rate_hz` (float, default: `10.0`)
- `offboard_warmup_cycles` (int, default: `10`)
- `takeoff_reached_tol_m` (float, default: `0.3`)
- `target_system` (int, default: `1`)
- `target_component` (int, default: `1`)
- `command_topic` (string, default: `"command"`)
- `land_topic` (string, default: `"land_request"`)

## Run
```bash
ros2 launch px4_state_machine px4_state_machine.launch.py
```

## Command Examples
```bash
ros2 topic pub /command std_msgs/msg/String "{data: 'FW_SET HDG=90 SPD=17 ALT=60'}"
```

```bash
ros2 topic pub /command std_msgs/msg/String "{data: 'FW_HOLD'}"
```

```bash
ros2 topic pub /land_request std_msgs/msg/Empty "{}"
```

## Expected Logs
- `State: ... -> MC_HOVER` after takeoff.
- `FW target set hdg=... spd=... alt=...` when FW command is received.
- `State: REQUEST_VTOL_FW -> FW_EXECUTE` when transition succeeds.
- `State: FW_EXECUTE -> REQUEST_VTOL_MC` when commands stop.
- `Sent land command` when land topic is triggered.

## Troubleshooting
- If transition does not happen, verify VTOL status topic is active and command is valid.
- If offboard is rejected, verify heartbeat is continuous and warmup cycles are sufficient.
- If vehicle does not move in FW, verify command format exactly (`FW_SET HDG=... SPD=... ALT=...`).
