package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/host"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/session"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/tmux"
)

// --- launch: run ./run.sh --no-attach and show its progress ---

// Each step is marked done when run.sh prints its marker (later markers imply earlier steps).
var launchSteps = []struct{ label, marker string }{
	{"Validate session", "[mullet] config:"},
	{"Host UDP ports free", "[mullet] instances:"},
	{"Start container", "Container " + host.ContainerName + " Started"},
	{"PX4 fleet up", "[mullet] fleet started"},
	{"colcon build", "Summary:"},
	{"tmux panes", "[mullet] tmux session ready"},
	{"Dashboard layout", "\x00"},
}

const (
	stepPending = iota
	stepDone
	stepFailed
)

type launchEvent struct {
	line string
	done bool
	err  error
}

type launchState struct {
	steps    []int
	log      []string
	ch       chan launchEvent
	running  bool
	err      error
	finished time.Time
}

type (
	launchLineMsg string
	launchDoneMsg struct{ err error }
	launchedMsg   struct{ err error }
)

func waitLaunch(ch chan launchEvent) tea.Cmd {
	return func() tea.Msg {
		ev := <-ch
		if ev.done {
			return launchDoneMsg{ev.err}
		}
		return launchLineMsg(ev.line)
	}
}

func (a *app) startLaunch() tea.Cmd {
	a.validate()
	if session.HasErrors(a.problems) {
		a.notify("Fix the errors in the fleet before launching", true)
		return nil
	}
	if a.host.Running && a.screen != scrLaunch {
		a.confirm("A simulation is running", "Launching restarts it with "+a.sess.Name+".",
			[]string{"Restart", "Cancel"}, func(c int) tea.Cmd {
				if c == 0 {
					a.host.Running = false
					a.screen = scrLaunch
					return a.startLaunch()
				}
				return nil
			})
		return nil
	}
	if !a.save() {
		return nil
	}
	a.dash.stopHeartbeat()
	a.dash = dashState{}
	ch := make(chan launchEvent, 256)
	a.launch = launchState{steps: make([]int, len(launchSteps)), ch: ch, running: true}
	a.screen = scrLaunch
	repo, path := a.repo, a.sess.Path()
	go func() {
		cmd := exec.Command(filepath.Join(repo, "run.sh"), "--config", path, "--no-attach")
		cmd.Dir = repo
		out, err := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			ch <- launchEvent{done: true, err: err}
			return
		}
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			ch <- launchEvent{line: ansi.Strip(sc.Text())}
		}
		ch <- launchEvent{done: true, err: cmd.Wait()}
	}()
	return waitLaunch(ch)
}

func (l *launchState) mark(upTo int) {
	for i := 0; i <= upTo && i < len(l.steps); i++ {
		l.steps[i] = stepDone
	}
}

func (l *launchState) fail() {
	for i, s := range l.steps {
		if s == stepPending {
			l.steps[i] = stepFailed
			return
		}
	}
}

func (a *app) updateLaunch(msg tea.Msg) tea.Cmd {
	l := &a.launch
	switch m := msg.(type) {
	case launchLineMsg:
		line := string(m)
		if l.log = append(l.log, line); len(l.log) > 400 {
			l.log = l.log[len(l.log)-400:]
		}
		for i, s := range launchSteps {
			if strings.Contains(line, s.marker) {
				l.mark(i)
			}
		}
		return waitLaunch(l.ch)
	case launchDoneMsg:
		l.running = false
		if m.err != nil {
			l.err = m.err
			l.fail()
			a.notify("Launch failed: see the log (r to retry)", true)
			return nil
		}
		l.mark(len(launchSteps) - 2)
		a.host.Running = true
		self, set, lay := a.self, a.set, a.layout
		return func() tea.Msg {
			tmux.Install(self, set, &lay)
			return launchedMsg{tmux.ApplyLayout(&lay)}
		}
	case launchedMsg:
		l.finished = time.Now()
		if m.err != nil {
			l.steps[len(l.steps)-1] = stepFailed
			a.notify("Layout: "+m.err.Error(), true)
		} else {
			l.mark(len(launchSteps) - 1)
			a.notify(theme.Current.I.Launch+" "+a.sess.Name+" is running", false)
		}
		return a.enterDash()
	case tea.KeyPressMsg:
		switch m.String() {
		case "esc", "q":
			if l.running {
				a.notify("Launch in progress (ctrl+x stops the simulation)", false)
			} else {
				a.screen = scrFleet
			}
		case "r":
			if !l.running && l.err != nil {
				return a.startLaunch()
			}
		}
	}
	return nil
}

func (a *app) viewLaunch() (string, string) {
	l := a.launch
	lines := []string{"", "  " + theme.Current.Primary.Render(theme.Current.I.Launch+" LAUNCH") + "  " + theme.Current.Bold.Render(a.sess.Name), ""}
	current := true
	for i, s := range launchSteps {
		var icon string
		switch l.steps[i] {
		case stepDone:
			icon = theme.Current.OK.Render(theme.Current.I.Ready)
		case stepFailed:
			icon = theme.Current.Stop.Render(theme.Current.I.Failed)
		default:
			icon = theme.Current.Muted.Render(theme.Current.I.Stopped)
			if current && l.running {
				icon = theme.Current.Accent.Render(spinner(a.frame))
				current = false
			}
		}
		lines = append(lines, "  "+icon+" "+theme.Current.Text.Render(s.label))
	}
	lines = append(lines, "", "  "+theme.Current.Muted.Render("─ run.sh "+strings.Repeat("─", max(0, a.w-14))))
	n := max(3, a.h-4-len(lines)-1)
	start := max(0, len(l.log)-n)
	for _, line := range l.log[start:] {
		lines = append(lines, "  "+theme.Current.Muted.Render(line))
	}
	hints := "esc back"
	if l.err != nil {
		hints = "r retry · esc back"
	}
	return strings.Join(lines, "\n"), hints
}

// --- dashboard: live fleet status (TUI region while the simulation runs) ---

type Heartbeat struct {
	Instance int     `json:"vehicle_instance"`
	State    string  `json:"state_label"`
	Armed    bool    `json:"armed"`
	Offboard bool    `json:"offboard"`
	Landed   bool    `json:"landed"`
	Nav      string  `json:"px4_nav_state_label"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	at       time.Time
}

type hbMsg Heartbeat

type dashState struct {
	hb    map[int]Heartbeat
	ready map[int]bool
	dds   map[int]bool
	ddsAt time.Time
	hbCmd *exec.Cmd
	hbCh  chan Heartbeat
}

func waitHB(ch chan Heartbeat) tea.Cmd {
	return func() tea.Msg {
		h, ok := <-ch
		if !ok {
			return nil
		}
		return hbMsg(h)
	}
}

func (d *dashState) onHeartbeat(m hbMsg) tea.Cmd {
	if d.hb == nil {
		d.hb = map[int]Heartbeat{}
	}
	h := Heartbeat(m)
	h.at = time.Now()
	d.hb[h.Instance] = h
	if d.hbCh == nil {
		return nil
	}
	return waitHB(d.hbCh)
}

func (d *dashState) stopHeartbeat() {
	if d.hbCmd != nil && d.hbCmd.Process != nil {
		_ = d.hbCmd.Process.Kill()
	}
	d.hbCmd, d.hbCh = nil, nil
}

func (a *app) enterDash() tea.Cmd {
	a.screen = scrDash
	if a.sess == nil || a.dash.hbCmd != nil || !strings.Contains(strings.Join(a.sess.Windows, " "), "core") {
		return a.pollCmd()
	}
	ch := make(chan Heartbeat, 64)
	cmd := exec.Command("docker", "exec", "-e", "ROS_DOMAIN_ID="+strconv.Itoa(a.sess.DomainID), host.ContainerName,
		"bash", "-c", "source /scripts/sourcefile.sh >/dev/null 2>&1; exec python3 /scripts/heartbeat_json.py")
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		a.notify("heartbeat: "+err.Error(), true)
		return a.pollCmd()
	}
	a.dash.hbCmd, a.dash.hbCh = cmd, ch
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var h Heartbeat
			if json.Unmarshal(sc.Bytes(), &h) == nil {
				select {
				case ch <- h:
				default: // drop when the UI is behind; the next heartbeat is 100 ms away
				}
			}
		}
		_ = cmd.Wait()
		close(ch)
	}()
	return tea.Batch(waitHB(ch), a.pollCmd())
}

type statusMsg struct {
	started    time.Time
	running    bool
	ready, dds map[int]bool
	layout     *Layout
	mtime      int64
}

func px4Ready() map[int]bool {
	out, _ := host.Output("docker", "exec", host.ContainerName, "sh", "-c", "grep -l 'Startup script returned successfully' /tmp/runtime/px4_*.log")
	ready := map[int]bool{}
	for _, f := range strings.Fields(out) {
		var n int
		if _, err := fmt.Sscanf(filepath.Base(f), "px4_%d.log", &n); err == nil {
			ready[n] = true
		}
	}
	return ready
}

// ddsCheck confirms each robot's PX4 topics are visible on the session's ROS_DOMAIN_ID.
func ddsCheck(domain int, ns map[int]string) map[int]bool {
	out, err := host.Output("docker", "exec", "-e", "ROS_DOMAIN_ID="+strconv.Itoa(domain), host.ContainerName,
		"bash", "-c", "source /scripts/sourcefile.sh >/dev/null 2>&1; ros2 topic list")
	if err != nil {
		return nil
	}
	res := map[int]bool{}
	for inst, n := range ns {
		prefix := strings.TrimSuffix(n, "/") + "/fmu/out/"
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, prefix) {
				res[inst] = true
				break
			}
		}
	}
	return res
}

func (a *app) pollCmd() tea.Cmd {
	inDash, mtime := a.screen == scrDash, a.layoutMtime
	var ns map[int]string
	domain, checkDDS := 0, false
	if a.sess != nil && inDash {
		domain, ns = a.sess.DomainID, map[int]string{}
		for _, r := range a.sess.Fleet {
			ns[r.Instance] = a.sess.Namespace(r.Instance)
			if !a.dash.dds[r.Instance] {
				checkDDS = true
			}
		}
		if checkDDS && time.Since(a.dash.ddsAt) > 6*time.Second {
			a.dash.ddsAt = time.Now()
		} else {
			checkDDS = false
		}
	}
	started := time.Now()
	return func() tea.Msg {
		st := statusMsg{started: started, running: host.SimRunning()}
		// The file is the source of truth: `mullet ctl` (tmux clicks) writes it too.
		l := host.LoadLayout()
		if st.running && inDash {
			st.ready = px4Ready()
			if checkDDS {
				st.dds = ddsCheck(domain, ns)
			}
			if tmux.RememberGeometry(&l) { // border drags: store only the geometry on the latest tree
				_ = host.SaveLayout(l)
			}
			tmux.RefreshNav(&l)
		}
		if mt := host.LayoutMtime(); mt != mtime {
			st.layout, st.mtime = &l, mt
		}
		return st
	}
}

func (a *app) onStatus(st statusMsg) tea.Cmd {
	if a.launch.running || st.started.Before(a.launch.finished) {
		return nil // the container is being recreated; this poll is stale
	}
	wasRunning := a.host.Running
	a.host.Running = st.running
	if st.layout != nil && a.screen != scrLayout { // never clobber a layout being edited
		a.layout, a.layoutMtime = *st.layout, st.mtime
	}
	if st.ready != nil {
		a.dash.ready = st.ready
	}
	if st.dds != nil {
		a.dash.dds = st.dds
	}
	if wasRunning && !st.running && a.busy == "" {
		// Stopped from elsewhere: the status-bar STOP, ./stop.sh, a crash.
		a.dash.stopHeartbeat()
		a.dash = dashState{}
		a.notify("Simulation stopped", false)
		if a.screen == scrDash {
			a.screen = scrHome
			if a.sess != nil {
				a.screen = scrFleet
			}
		}
	}
	return nil
}

func yesNo(ok bool, yes string) string {
	if ok {
		return theme.Current.OK.Render(yes)
	}
	return theme.Current.Muted.Render("·")
}

func (a *app) viewDash() (string, string) {
	s := a.sess
	if s == nil {
		return "\n  " + theme.Current.Muted.Render("No session loaded."), "esc home"
	}
	img, _ := a.image()
	lines := []string{"", "  " + theme.Current.Primary.Render(theme.Current.I.Dash+" LIVE") + "  " + theme.Current.Bold.Render(s.Name) +
		theme.Current.Muted.Render(fmt.Sprintf("  PX4 %s · %s · DOMAIN %d · /%s_<N>", img.Version(), s.World, s.DomainID, orDefault(s.NSPrefix, "px4"))), ""}
	wide := a.w >= 96
	if wide {
		lines = append(lines, theme.Current.Muted.Render(fmt.Sprintf("    %-12s %-9s %-4s %-26s %-16s %-20s %s", "ROBOT", "PX4", "DDS", "STATE", "ARM OFFB LAND", "POSITION x y z", "HB")))
	} else {
		lines = append(lines, theme.Current.Muted.Render(fmt.Sprintf("    %-10s %-7s %-14s %s", "ROBOT", "PX4", "STATE", "z")))
	}
	fleet := append([]Robot(nil), s.Fleet...)
	sort.Slice(fleet, func(i, j int) bool { return fleet[i].Instance < fleet[j].Instance })
	var pts []mapPoint
	for _, r := range fleet {
		px4 := theme.Current.Accent.Render(spinner(a.frame) + " boot ")
		if a.dash.ready[r.Instance] {
			px4 = theme.Current.OK.Render(theme.Current.I.Ready + " ready")
		}
		h, ok := a.dash.hb[r.Instance]
		state, pos, age, z := theme.Current.Muted.Render("—"), theme.Current.Muted.Render("—"), theme.Current.Muted.Render("—"), theme.Current.Muted.Render("—")
		east, north := r.Pose[0], r.Pose[1]
		if ok {
			state = theme.Current.Bold.Render(h.State) + theme.Current.Muted.Render(" "+h.Nav)
			pos = fmt.Sprintf("%6.1f %6.1f %6.1f", h.X, h.Y, h.Z)
			z = fmt.Sprintf("%.1f", -h.Z)
			dt := time.Since(h.at)
			age = theme.Current.OK.Render(fmt.Sprintf("%.1fs", dt.Seconds()))
			if dt > 2*time.Second {
				age = theme.Current.Warn.Render(fmt.Sprintf("%.0fs", dt.Seconds()))
			}
			// PX4 local NED (x north, y east) relative to the spawn pose (Gazebo x east, y north).
			east, north = r.Pose[0]+h.Y, r.Pose[1]+h.X
		}
		pts = append(pts, mapPoint{east, north, strconv.Itoa(r.Instance), theme.Current.P.RobotColor(r.Instance)})
		robot := theme.Current.Robot(r.Instance).Render(theme.Current.I.Robot(r.Model) + " " + fmt.Sprintf("%-9s", s.Namespace(r.Instance)))
		if wide {
			dds := theme.Current.Muted.Render("…")
			if v, done := a.dash.dds[r.Instance]; done && v {
				dds = theme.Current.OK.Render("✓")
			}
			flags := fmt.Sprintf("%s   %s    %s", yesNo(ok && h.Armed, "●"), yesNo(ok && h.Offboard, "●"), yesNo(ok && h.Landed, "●"))
			lines = append(lines, fmt.Sprintf("    %s   %s  %s    %s %s %s %s", robot, px4, dds,
				padTo(state, 26), padTo(flags, 16), padTo(theme.Current.Text.Render(pos), 20), age))
		} else {
			lines = append(lines, fmt.Sprintf("    %s %s %s %s", robot, px4, padTo(state, 14), z))
		}
	}
	if len(a.dash.hb) == 0 && strings.Contains(strings.Join(s.Windows, " "), "core") {
		lines = append(lines, "", "  "+theme.Current.Muted.Render("waiting for mullet_core heartbeats on /mullet/heartbeat…"))
	}
	if mh := a.h - 4 - len(lines) - 4; mh >= 8 {
		mw := min(a.w-6, 60)
		lines = append(lines, "", "  "+theme.Current.Muted.Render("fleet map (east → , north ↑)"))
		for _, l := range strings.Split(fleetMap(pts, mw, min(mh, 16)), "\n") {
			lines = append(lines, "  "+l)
		}
	}
	return strings.Join(lines, "\n"), "l layout editor · 1-4 presets · a re-apply layout · d detach · esc home"
}

func padTo(s string, w int) string {
	if pad := w - ansi.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func (a *app) updateDash(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key := k.String(); key {
	case "l":
		return a.enterLayout()
	case "1", "2", "3", "4":
		i, _ := strconv.Atoi(key)
		a.layout = layout.Preset(layout.PresetNames[i-1])
		_ = host.SaveLayout(a.layout)
		a.layoutMtime = host.LayoutMtime()
		a.notify("Layout: "+a.layout.Name, false)
		return a.installCmd(true)
	case "a":
		return a.installCmd(true)
	case "d":
		tmux.Tmx("detach-client", "-s", tmux.SessionName)
	case "esc", "q":
		a.screen = scrHome
	}
	return nil
}
