// mullet is an optional TUI for mullet_sitl_sim: build a fleet session, launch it through
// ./run.sh and arrange the simulation panes in a tmux dashboard.
package main

import (
	"fmt"
	"os"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/host"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/tmux"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/ui"
)

const usage = `Usage:
  mullet          open the TUI (starts or re-attaches the "mullet" tmux session)
  mullet tui      run the TUI in the current terminal (used inside tmux)
  mullet ctl ...  callbacks for the tmux bindings (click, pick, snap, hide, robot, save, stop, split, close)
  mullet mirror PANE  read-only copy of a tmux pane (the dash's CORE/CODE views)

Without the TUI: ./run.sh --config config/sessions/<name>.yaml and ./stop.sh
`

func main() {
	repo, err := host.FindRepo()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mullet:", err)
		os.Exit(1)
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mullet:", err)
		os.Exit(1)
	}

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "":
		err = tmux.Launch(repo, self)
	case "tui":
		err = ui.Run(repo)
	case "ctl":
		err = tmux.Ctl(repo, os.Args[2:])
		if err != nil {
			tmux.Tmx("display-message", "mullet: "+err.Error())
		}
	case "mirror":
		if len(os.Args) != 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		err = tmux.MirrorPane(os.Args[2])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mullet:", err)
		os.Exit(1)
	}
}
