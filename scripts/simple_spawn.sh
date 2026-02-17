#!/usr/bin/env bash
# Usage: ./tmux_spawn.sh N

NUM=$1
MODEL_NAME=${2:-x500}
PX4_DIR="${PX4_DIR:-~/PX4-Autopilot}"
MODEL="gz_${MODEL_NAME}"
TMUX_CONF_PATH="${TMUX_CONF_PATH:-config/tmux.conf}"

if [ -z "$NUM" ]; then
    echo "Usage: $0 <number_of_robots>"
    exit 1
fi

echo "Number of robots: $NUM"
echo "Selected model: $MODEL"

SESSION="robots"

if [ -f "$TMUX_CONF_PATH" ]; then
    tmux source-file "$TMUX_CONF_PATH" >/dev/null 2>&1 || true
fi

# Kill session if it already exists
tmux has-session -t $SESSION 2>/dev/null
if [ $? -eq 0 ]; then
    tmux kill-session -t $SESSION
fi

# Create session with pane 0
tmux new-session -d -s $SESSION

# Create N additional panes (in a tiled layout later)
for ((i=2; i<=NUM; i++)); do
    tmux split-window -t $SESSION
    tmux select-layout -t $SESSION tiled
done

# Spawn first robot and launch Gazebo
GZCMD="PX4_SYS_AUTOSTART=4001 PX4_SIM_MODEL=$MODEL $PX4_DIR/build/px4_sitl_default/bin/px4 -i 1"
tmux send-keys -t ${SESSION}:0.0 "$GZCMD" C-m

# Send command to each pane
for ((i=2; i<=NUM; i++)); do
    # UAV i gets a namespace "px4_i"
    CMD="PX4_GZ_STANDALONE=1 PX4_SYS_AUTOSTART=4001 PX4_GZ_MODEL_POSE=\"$((i-1)),0\" PX4_SIM_MODEL=$MODEL $PX4_DIR/build/px4_sitl_default/bin/px4 -i $i"
    tmux send-keys -t ${SESSION}:0.$((i-1)) "$CMD" C-m
done

### MicroXRCE Window ###
WIN_ID=$(tmux new-window -t $SESSION -n MicroXRCE -P)
MICROXRCECMD="MicroXRCEAgent udp4 -p 8888"
tmux send-keys -t "$WIN_ID" "$MICROXRCECMD" C-m

# Attach to the session
tmux select-layout -t $SESSION tiled
tmux attach -t $SESSION