# mullet TUI

Go TUI for mullet-sim. Module: `github.com/skymule-ai/mullet-sim/tui`.

```
cmd/mullet          CLI (tui / ctl / mirror)
internal/theme      palettes, icons, lipgloss / huh / tmux styles
internal/layout     dashboard split tree
internal/session    fleet YAML via spawn_fleet.py
internal/host       Docker probe, XDG settings/layouts
internal/tmux       tmux socket, panes, mirrors, ctl, launcher
internal/ui         Bubble Tea screens
```

```sh
tui/build.sh              # go build -trimpath -o ../mullet ./cmd/mullet
tui/build.sh test ./...
tui/build.sh vet ./...
```
