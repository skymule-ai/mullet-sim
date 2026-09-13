#!/usr/bin/env bash
# Tear down the SITL container and simulation tmux panes (socket `mullet`).
# rosbag panes get Ctrl-C first so the bag can finalize.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

TMUX_SOCKET="mullet"
SESSION_NAME="mullet"
tmx() { tmux -L "${TMUX_SOCKET}" "$@"; }
sim_panes() { tmx list-panes -s -t "${SESSION_NAME}" -F '#{pane_id} #{@mullet_role}' 2>/dev/null || true; }

# Let rosbag finalize its metadata before the container goes away.
bag_panes="$(sim_panes | awk '$2 == "bag" {print $1}')"
for id in ${bag_panes}; do
  tmx send-keys -t "${id}" C-c
done
if [[ -n "${bag_panes}" ]]; then
  sleep 3
fi

docker compose down

# Close the simulation panes; panes without a role (the TUI) stay.
sim_panes | awk '$2 != "" {print $1}' | while read -r id; do
  tmx kill-pane -t "${id}" || true
done
