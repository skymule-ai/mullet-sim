// Package tmux drives the mullet tmux socket: panes, mirrors, ctl callbacks, and the launcher.
package tmux

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/host"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
)

// Same socket/session as run.sh and stop.sh.
const (
	Socket      = "mullet"
	SessionName = "mullet"
)

// kindFmt is a pane's dashboard kind source: the TUI, the role a mirror copies, or its own role.
const kindFmt = "#{?@mullet_tui,tui,#{?@mullet_mirror,#{@mullet_mirror},#{@mullet_role}}}"

// codeIf matches real code panes and their dash mirrors.
const codeIf = "#{m:code:*,#{@mullet_role}#{@mullet_mirror}}"

func Tmx(args ...string) (string, error) {
	if os.Getenv("MULLET_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "tmux", strings.Join(args, " "))
	}
	out, err := exec.Command("tmux", append([]string{"-L", Socket}, args...)...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
	}
	// Only the final newline: trailing tabs are empty fields of -F formats.
	return strings.TrimRight(string(out), "\n"), err
}

// Pane is a tmux pane; Role is set by run.sh (px4:N, core:N, xrce:0, code:I, rviz, bag, fox).
// Mirror is set on the dash's read-only copies instead: the role they show.
type Pane struct {
	ID, Role, WindowID, WindowName, Label, Color, Mirror string
	TUI                                                  bool
}

func (p Pane) Kind() string { k, _, _ := strings.Cut(p.Role, ":"); return k }

func (p Pane) Num() int {
	_, n, _ := strings.Cut(p.Role, ":")
	i, _ := strconv.Atoi(n)
	return i
}

func NextCodeNum(ps []Pane) int {
	n := -1
	for _, p := range ps {
		if p.Kind() == "code" && p.Num() > n {
			n = p.Num()
		}
	}
	return n + 1
}

func Panes() []Pane {
	out, err := Tmx("list-panes", "-s", "-t", SessionName, "-F",
		"#{pane_id}\t#{@mullet_role}\t#{window_id}\t#{window_name}\t#{@mullet_tui}\t#{@mullet_label}\t#{@mullet_color}\t#{@mullet_mirror}")
	if err != nil {
		return nil
	}
	var ps []Pane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 8 {
			ps = append(ps, Pane{ID: f[0], Role: f[1], WindowID: f[2], WindowName: f[3], TUI: f[4] == "1",
				Label: f[5], Color: f[6], Mirror: f[7]})
		}
	}
	return ps
}

// regionPanes groups simulation panes by dashboard region, ordered by robot/command number.
// Mirrors have no role, so only real panes count.
func RegionPanes(ps []Pane) map[string][]Pane {
	groups := map[string][]Pane{}
	for _, p := range ps {
		if k := p.Kind(); k != "" && k != "tui" {
			groups[k] = append(groups[k], p)
		}
	}
	for _, g := range groups {
		sort.Slice(g, func(a, b int) bool { return g[a].Num() < g[b].Num() })
	}
	return groups
}

func TuiPane(ps []Pane) (Pane, bool) {
	for _, p := range ps {
		if p.TUI {
			return p, true
		}
	}
	return Pane{}, false
}

func pct(f float64) string {
	return strconv.Itoa(int(math.Min(95, math.Max(5, math.Round(f*100))))) + "%"
}

// stash moves a pane back to the window named after its kind (created by run.sh).
func stash(p Pane) error {
	k := p.Kind()
	if _, err := Tmx("list-panes", "-t", SessionName+":="+k); err == nil {
		if _, err := Tmx("join-pane", "-d", "-s", p.ID, "-t", SessionName+":="+k); err != nil {
			return err
		}
		_, err = Tmx("select-layout", "-t", SessionName+":="+k, "tiled")
		return err
	}
	_, err := Tmx("break-pane", "-d", "-s", p.ID, "-n", k)
	return err
}

// paneKinds is the layout signature: pane kinds of the dash window in pane order.
func paneKinds(window string) []string {
	out, err := Tmx("list-panes", "-t", window, "-F", kindFmt)
	if err != nil {
		return nil
	}
	var kinds []string
	for _, r := range strings.Split(out, "\n") {
		k, _, _ := strings.Cut(r, ":")
		kinds = append(kinds, k)
	}
	return kinds
}

// applyLayout rebuilds the dash window (the TUI's window): read-only mirrors of the panes the
// layout shows, arranged by its tree. Real panes always stay in their own windows.
func ApplyLayout(l *layout.Layout) error {
	tui, ok := TuiPane(Panes())
	if !ok {
		return errors.New("TUI pane not found")
	}
	Tmx("set", "-g", "@mullet_applying", "1")
	defer Tmx("set", "-gu", "@mullet_applying")

	for _, p := range Panes() {
		switch {
		case p.Mirror != "": // rebuilt below; also clears an old dash left by a TUI restart
			Tmx("kill-pane", "-t", p.ID)
		case p.Role != "" && (p.WindowID == tui.WindowID || p.WindowName != p.Kind()):
			// real panes that older versions moved into the dash go back to their window
			if err := stash(p); err != nil {
				return err
			}
		}
	}

	members := map[string][]string{"tui": {tui.ID}}
	present := map[string]bool{}
	for r, g := range RegionPanes(Panes()) {
		for _, p := range g {
			if !l.Shown(p.Role) {
				continue
			}
			id, err := mirror(p)
			if err != nil {
				return err
			}
			members[r] = append(members[r], id)
			present[r] = true
		}
	}

	if os.Getenv("MULLET_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "off=%v members=%v tree=%v\n", l.Off, members, l.Root.Keep(present).Leaves())
	}
	err := build(l.Root.Keep(present), tui.ID, "tui", members)
	if l.TmuxLayout != "" && strings.Join(paneKinds(tui.WindowID), " ") == strings.Join(l.PaneRoles, " ") {
		Tmx("select-layout", "-t", tui.WindowID, l.TmuxLayout)
	}
	Tmx("select-pane", "-t", tui.ID)
	return err
}

// mirror starts a read-only copy of p in a detached window; build then joins it into the dash.
func mirror(p Pane) (string, error) {
	var cmd string
	switch p.Kind() {
	// ponytail: log paths duplicate run.sh's RUNTIME_DIR_IN_CONTAINER/tail_cmd; share them if they ever move.
	case "px4":
		cmd = containerCmd(fmt.Sprintf("tail -n 200 -F /tmp/runtime/px4_%d.log", p.Num()))
	case "xrce":
		cmd = containerCmd("tail -n 200 -F /tmp/runtime/xrce_agent.log")
	default:
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		cmd = fmt.Sprintf("'%s' mirror %s", self, p.ID)
	}
	id, err := Tmx("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", SessionName+":", "-n", "mirror", cmd)
	if err != nil {
		return "", err
	}
	for k, v := range map[string]string{"@mullet_mirror": p.Role, "@mullet_label": p.Label, "@mullet_color": p.Color, "remain-on-exit": "off"} {
		if v != "" {
			Tmx("set", "-p", "-t", id, k, v)
		}
	}
	Tmx("select-pane", "-d", "-t", id) // read-only: typing happens in the real pane's window
	return id, nil
}

// mirrorPane redraws pane id's screen into this terminal until that pane is gone (`mullet mirror`).
func MirrorPane(id string) error {
	os.Stdout.WriteString("\x1b[?25l\x1b[?7l") // no cursor; clip long lines instead of wrapping
	last := ""
	for {
		out, err := Tmx("capture-pane", "-p", "-e", "-t", id)
		if err != nil {
			return nil // the source closed: so does its mirror
		}
		ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
		if err != nil {
			return err
		}
		frame := MirrorFrame(out, int(ws.Row))
		if key := fmt.Sprint(ws.Row, ws.Col, frame); key != last {
			os.Stdout.WriteString(frame)
			last = key
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// mirrorFrame draws the last rows non-blank lines of a `capture-pane -e` dump from the top left.
func MirrorFrame(capture string, rows int) string {
	lines := strings.Split(capture, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = lines[max(0, len(lines)-rows):]
	return "\x1b[H" + strings.Join(lines, "\x1b[0m\x1b[K\r\n") + "\x1b[0m\x1b[K\x1b[J"
}

// build lays out n inside the space held by anchor, a pane of anchorRegion (a leaf of n).
func build(n *layout.Node, anchor, anchorRegion string, members map[string][]string) error {
	if n == nil {
		return nil
	}
	if n.Region != "" {
		return grid(members[n.Region])
	}
	anchorInA := n.A.Has(anchorRegion)
	other := n.B
	if !anchorInA {
		other = n.A
	}
	first := other.Leaves()[0]
	p := members[first][0]
	args := []string{"join-pane", "-d", "-h", "-s", p, "-t", anchor}
	if n.Split == "v" {
		args[2] = "-v"
	}
	if anchorInA {
		args = append(args, "-l", pct(1-n.Ratio))
	} else {
		args = append(args, "-b", "-l", pct(n.Ratio))
	}
	if _, err := Tmx(args...); err != nil {
		return err
	}
	if anchorInA {
		return errors.Join(build(n.A, anchor, anchorRegion, members), build(n.B, p, first, members))
	}
	return errors.Join(build(n.A, p, first, members), build(n.B, anchor, anchorRegion, members))
}

// grid tiles a region's panes; ids[0] already holds the region's space.
func grid(ids []string) error {
	n := len(ids)
	if n <= 1 {
		return nil
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	rows := (n + cols - 1) / cols
	heads := []string{ids[0]}
	for r := 1; r < rows; r++ {
		head := ids[r*cols]
		if _, err := Tmx("join-pane", "-d", "-v", "-s", head, "-t", heads[r-1], "-l", pct(float64(rows-r)/float64(rows-r+1))); err != nil {
			return err
		}
		heads = append(heads, head)
	}
	for r := 0; r < rows; r++ {
		row := ids[r*cols : min(n, r*cols+cols)]
		for c := 1; c < len(row); c++ {
			k := len(row)
			if _, err := Tmx("join-pane", "-d", "-h", "-s", row[c], "-t", row[c-1], "-l", pct(float64(k-c)/float64(k-c+1))); err != nil {
				return err
			}
		}
	}
	return nil
}

// rememberGeometry stores the dash window's exact geometry when it changed (e.g. border drags).
func RememberGeometry(l *layout.Layout) bool {
	if v, _ := Tmx("show", "-gv", "@mullet_applying"); v == "1" {
		return false
	}
	tui, ok := TuiPane(Panes())
	if !ok {
		return false
	}
	geom, err := Tmx("display", "-p", "-t", tui.ID, "#{window_layout}")
	if err != nil || geom == l.TmuxLayout {
		return false
	}
	l.TmuxLayout, l.PaneRoles = geom, paneKinds(tui.WindowID)
	return true
}

var tmuxStyle = regexp.MustCompile(`#\[.*?\]`)

func TmuxPlain(s string) string { return tmuxStyle.ReplaceAllString(s, "") }

func TmuxWidth(s string) int { return len([]rune(TmuxPlain(s))) }

func clientWidth() int {
	out, _ := Tmx("display", "-p", "#{client_width}")
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	if n <= 0 {
		return 120
	}
	return n
}

// navRegion is one block of the nav bar: its label (▾ opens the dash picker), a chip per pane
// (highlighted when on the dash; click toggles), the single/multi icon of the block's own
// window and, for CODE, the split/close buttons.
func NavRegion(l *layout.Layout, p theme.Palette, r string, g []Pane, zoomed bool) string {
	var b strings.Builder
	label := strings.ToUpper(r)
	if r == "code" && len(g) == 0 {
		fmt.Fprintf(&b, "#[range=user|show:code]#[fg=%s]%s ✚#[norange]", p.Border, label)
		return b.String()
	}
	if r == "xrce" {
		fmt.Fprintf(&b, "#[fg=%s,bold]%s#[nobold] ", p.RegionColor(r), label)
	} else {
		fmt.Fprintf(&b, "#[range=user|menu:%s]#[fg=%s,bold]%s ▾#[nobold]#[norange] ", r, p.RegionColor(r), label)
	}
	for _, pn := range g {
		color, text := p.RobotColor(pn.Num()), strconv.Itoa(pn.Num())
		if r == "code" {
			color, text = p.OK, strconv.Itoa(pn.Num()+1)
		} else if r != "px4" && r != "core" {
			color, text = p.Muted, "●"
		}
		if l.Shown(pn.Role) {
			fmt.Fprintf(&b, "#[range=user|%s]#[fg=%s,bg=%s,bold] %s #[nobold,bg=%s]#[norange]", pn.Role, p.Base, color, text, p.Surface)
		} else {
			fmt.Fprintf(&b, "#[range=user|%s]#[fg=%s] %s #[norange]", pn.Role, p.Border, text)
		}
	}
	icon := "⊞"
	if zoomed {
		icon = "▢"
	}
	fmt.Fprintf(&b, "#[range=user|view:%s]#[fg=%s] %s#[norange]", r, p.Muted, icon)
	if r == "code" {
		fmt.Fprintf(&b, "#[range=user|split:h] [|]#[norange]#[range=user|split:v] [-]#[norange]"+
			"#[range=user|close:code]#[fg=%s] [✕]#[norange]", p.Stop)
	}
	return b.String()
}

func navLines(l *layout.Layout, p theme.Palette, width int) []string {
	prefix := fmt.Sprintf("#[bg=%s,fg=%s] ", p.Surface, p.Muted)
	groups := RegionPanes(Panes())
	if len(groups) == 0 {
		return []string{prefix + "no simulation running"}
	}
	zoomed := map[string]bool{}
	if out, err := Tmx("list-windows", "-t", SessionName, "-F", "#{window_name}\t#{window_zoomed_flag}"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			name, z, _ := strings.Cut(line, "\t")
			zoomed[name] = z == "1"
		}
	}
	var chunks []string
	for _, r := range layout.RegionNames[1:] {
		if g := groups[r]; len(g) > 0 || r == "code" { // CODE ✚ stays to bring code panes back
			chunks = append(chunks, NavRegion(l, p, r, g, zoomed[r]))
		}
	}
	sep, hint := "   ", fmt.Sprintf("#[fg=%s]block ▾ pick · chip show/hide · M-1..9 robot · M-v single/multi · C-a | - x code", p.Border)
	inner := max(8, width-1)
	lines := PackWidth(chunks, sep, inner, 2, TmuxWidth)
	if len(lines) == 0 {
		return []string{prefix + "no simulation running"}
	}
	last := lines[len(lines)-1]
	if TmuxWidth(last)+3+TmuxWidth(hint) <= inner {
		lines[len(lines)-1] = last + sep + hint
	}
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return lines
}

func RefreshNav(l *layout.Layout) {
	lines := navLines(l, theme.Current.P, clientWidth())
	Tmx("set", "-g", "status", strconv.Itoa(1+len(lines)))
	for i, line := range lines {
		Tmx("set", "-g", fmt.Sprintf("status-format[%d]", i+1), line)
	}
}

// installTmux applies the theme and the mouse/key bindings that call back into `mullet ctl`.
func Install(self string, set host.Settings, l *layout.Layout) {
	for _, c := range theme.TmuxTheme(theme.Current.P, set.NavPosition) {
		Tmx(c...)
	}
	for _, p := range Panes() {
		if k := p.Kind(); k == "px4" || k == "core" {
			Tmx("set", "-p", "-t", p.ID, "@mullet_color", theme.Current.P.RobotColor(p.Num()))
		}
	}
	RefreshNav(l)

	ctl := "'" + self + "' ctl "
	stop := fmt.Sprintf(`confirm-before -p "Stop the simulation? (y/n)" "run-shell -b \"%sstop\""`, ctl)
	Tmx("bind", "-n", "MouseDown1Status", "if", "-F", "#{==:#{mouse_status_range},stop}", stop,
		fmt.Sprintf(`if -F "#{m:*:*,#{mouse_status_range}}" "run-shell -b \"%sclick #{mouse_status_range} #{client_name} #{mouse_x}\"" "switch-client -t ="`, ctl))
	installCopyBinds()
	// Flat argv only: tmux doesn't parse { } blocks passed as separate arguments.
	Tmx(append([]string{"bind", "-n", "MouseDown3Pane"}, paneMenu(ctl, stop)...)...)
	Tmx("bind", "|", "if", "-F", codeIf, "run-shell -b \""+ctl+"split #{pane_id} h\"", "split-window -h -c '#{pane_current_path}'")
	Tmx("bind", "-", "if", "-F", codeIf, "run-shell -b \""+ctl+"split #{pane_id} v\"", "split-window -v -c '#{pane_current_path}'")
	Tmx("bind", "x", "if", "-F", codeIf, "run-shell -b \""+ctl+"close #{pane_id}\"", "confirm-before -p 'Kill pane? (y/n)' kill-pane")
	for i := 1; i <= 9; i++ {
		Tmx("bind", "-n", fmt.Sprintf("M-%d", i), "run-shell", "-b", fmt.Sprintf("%srobot %d #{pane_id}", ctl, i))
	}
	Tmx("bind", "-n", "M-v", "resize-pane", "-Z") // single/multi: zoom the active pane of a block window
	Tmx("bind", "-n", "M-s", stop)
	Tmx("bind", "S", stop)
}

// paneMenu is the right-click menu. Items whose label expands to empty are hidden, so the code
// items show only on code panes and the layout items only in the dash.
// Pane title bars can't host buttons: tmux 3.6 drops clicks there or reports no column.
func paneMenu(ctl, stop string) []string {
	code := func(label string) string { return "#{?" + codeIf + "," + label + ",}" }
	dash := func(label string) string { return "#{?#{==:#{window_name},dash}," + label + ",}" }
	return []string{"display-menu", "-O", "-t", "=", "-x", "M", "-y", "M",
		"-T", "#[align=centre] #{?@mullet_label,#{@mullet_label},mullet} ",
		"Zoom", "z", "resize-pane -Z -t #{pane_id}",
		code("Split right"), "|", "run-shell -b \"" + ctl + "split #{pane_id} h\"",
		code("Split down"), "-", "run-shell -b \"" + ctl + "split #{pane_id} v\"",
		code("Close pane"), "X", "run-shell -b \"" + ctl + "close #{pane_id}\"",
		dash("Snap left"), "h", "run-shell -b \"" + ctl + "snap #{pane_id} left\"",
		dash("Snap right"), "l", "run-shell -b \"" + ctl + "snap #{pane_id} right\"",
		dash("Snap top"), "k", "run-shell -b \"" + ctl + "snap #{pane_id} top\"",
		dash("Snap bottom"), "j", "run-shell -b \"" + ctl + "snap #{pane_id} bottom\"",
		dash("Hide region"), "x", "run-shell -b \"" + ctl + "hide #{pane_id}\"",
		"",
		"Save layout as…", "s", `command-prompt -p "Layout name:" "run-shell -b \"` + ctl + `save %%\""`,
		"Stop simulation", "S", stop,
	}
}

// pickMenu is the dash dropdown of block r: Select all / Deselect all and a toggle per pane.
// Each item runs `ctl pick`, which applies the choice and opens the menu again.
func pickMenu(l *layout.Layout, r, client, x string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	pick := func(what string) string {
		return fmt.Sprintf(`run-shell -b "'%s' ctl pick %s %s %s %s"`, self, r, what, client, x)
	}
	args := []string{"display-menu", "-c", client, "-x", x, "-y", "S",
		"-T", "#[align=centre] " + strings.ToUpper(r) + " on the dash ",
		"Select all", "a", pick("all"),
		"Deselect all", "n", pick("none"),
		""}
	for i, p := range RegionPanes(Panes())[r] {
		mark, key := "  ", ""
		if l.Shown(p.Role) {
			mark = "✓ "
		}
		if i < 9 {
			key = strconv.Itoa(i + 1)
		}
		args = append(args, mark+orDefault(p.Label, p.Role), key, pick(p.Role))
	}
	_, err = Tmx(args...)
	return err
}

// containerCmd runs cmd in the simulation container on the session's ROS domain.
func containerCmd(cmd string) string {
	domain, _ := Tmx("show", "-gv", "@mullet_domain")
	if domain == "" {
		domain = "0"
	}
	return fmt.Sprintf("docker exec -it -e ROS_DOMAIN_ID=%s %s %s", domain, host.ContainerName, cmd)
}

// codePane is the real code pane an action applies to: the given or active pane (a dash mirror
// stands for its source), else the active pane of the code window.
func codePane(id string) (Pane, bool) {
	ps := Panes()
	if id == "" {
		id, _ = Tmx("display", "-p", "#{pane_id}")
	}
	role := ""
	for _, p := range ps {
		if p.ID == id {
			role = p.Role + p.Mirror
		}
	}
	if !strings.HasPrefix(role, "code:") {
		role, _ = Tmx("display", "-p", "-t", SessionName+":=code", "#{@mullet_role}")
	}
	for _, p := range ps {
		if p.Kind() == "code" && p.Role == role {
			return p, true
		}
	}
	Tmx("display-message", "No code pane")
	return Pane{}, false
}

// newCodePane runs a container shell through a split-window/new-window command, tags it as the
// next code pane and returns its role.
func newCodePane(args ...string) (string, error) {
	n := NextCodeNum(Panes())
	id, err := Tmx(append(args, "-P", "-F", "#{pane_id}", containerCmd("bash"))...)
	if err != nil {
		return "", err
	}
	role := fmt.Sprintf("code:%d", n)
	Tmx("set", "-p", "-t", id, "@mullet_role", role)
	Tmx("set", "-p", "-t", id, "@mullet_label", fmt.Sprintf("CODE · %d", n+1))
	Tmx("set", "-p", "-t", id, "remain-on-exit", "off")
	return role, nil
}

// splitCode splits a code pane in the code window; when CODE is on the dash the new pane's
// mirror joins it.
func splitCode(l *layout.Layout, pane, mode string) error {
	if mode != "h" && mode != "v" {
		return fmt.Errorf("split mode %q: want h or v", mode)
	}
	src, ok := codePane(pane)
	if !ok {
		return nil
	}
	role, err := newCodePane("split-window", "-"+mode, "-t", src.ID)
	if err != nil || !l.Root.Has("code") {
		return err
	}
	l.SetShown("code", []string{role}, []string{role}, true)
	host.SaveLayout(*l)
	return ApplyLayout(l)
}

// closeCode kills a code pane. Its mirror exits by itself, tmux drops an emptied code window,
// and the nav bar then offers CODE ✚.
func closeCode(pane string) error {
	src, ok := codePane(pane)
	if !ok {
		return nil
	}
	_, err := Tmx("kill-pane", "-t", src.ID)
	return err
}

// Ctl handles the callbacks of the tmux bindings (runs as a short-lived process).
func Ctl(repo string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mullet ctl click|pick|snap|hide|robot|save|stop|split|close ...")
	}
	set := host.LoadSettings()
	theme.Apply(set.Theme, set.Icons)
	l := host.LoadLayout()
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	regionOf := func(pane string) string {
		out, _ := Tmx("display", "-p", "-t", pane, kindFmt)
		k, _, _ := strings.Cut(out, ":")
		return k
	}
	roles := func(r string) []string {
		var out []string
		for _, p := range RegionPanes(Panes())[r] {
			out = append(out, p.Role)
		}
		return out
	}

	switch args[0] {
	case "stop":
		cmd := exec.Command(filepath.Join(repo, "stop.sh"))
		cmd.Dir = repo
		return cmd.Run()
	case "save":
		if err := host.SaveNamedLayout(arg(1), l); err != nil {
			return err
		}
		Tmx("display-message", "Layout saved: "+arg(1))
		return nil
	case "split":
		if err := splitCode(&l, arg(1), arg(2)); err != nil {
			return err
		}
		RefreshNav(&l)
		return nil
	case "close":
		if err := closeCode(arg(1)); err != nil {
			return err
		}
		RefreshNav(&l)
		return nil
	case "click": // click <range> <client> <x>
		r := arg(1)
		switch {
		case strings.HasPrefix(r, "menu:"):
			return pickMenu(&l, r[5:], arg(2), arg(3))
		case strings.HasPrefix(r, "split:"):
			if err := splitCode(&l, "", r[6:]); err != nil {
				return err
			}
			RefreshNav(&l)
			return nil
		case strings.HasPrefix(r, "close:"):
			if err := closeCode(""); err != nil {
				return err
			}
			RefreshNav(&l)
			return nil
		case strings.HasPrefix(r, "view:"): // single/multi of the block's own window
			Tmx("resize-pane", "-Z", "-t", SessionName+":="+r[5:])
			RefreshNav(&l)
			return nil
		case r == "show:code": // all code panes closed: start a fresh one
			role, err := newCodePane("new-window", "-d", "-t", SessionName+":", "-n", "code")
			if err != nil {
				return err
			}
			l.SetShown("code", []string{role}, []string{role}, true)
		default: // a pane chip: show/hide it on the dash
			k, _, _ := strings.Cut(r, ":")
			l.SetShown(k, roles(k), []string{r}, !l.Shown(r))
		}
	case "pick": // pick <region> <role|all|none> <client> <x>: a dropdown choice
		r, all := arg(1), roles(arg(1))
		switch arg(2) {
		case "all":
			l.SetShown(r, all, all, true)
		case "none":
			l.SetShown(r, all, all, false)
		default:
			l.SetShown(r, all, []string{arg(2)}, !l.Shown(arg(2)))
		}
	case "robot": // M-1..9: robot N's real pane in its own window, keeping single view
		k := regionOf(arg(2))
		n, _ := strconv.Atoi(arg(1))
		if g := RegionPanes(Panes())[k]; n >= 1 && n <= len(g) {
			Tmx("select-window", "-t", g[n-1].ID)
			_, err := Tmx("select-pane", "-Z", "-t", g[n-1].ID)
			return err
		}
		return nil
	case "snap":
		l.SnapEdge(regionOf(arg(1)), arg(2))
	case "hide":
		l.Hide(regionOf(arg(1)))
	default:
		return fmt.Errorf("unknown ctl command %q", args[0])
	}
	Tmx("set", "-g", "@mullet_applying", "1") // the TUI's poll must not record geometry mid-change
	host.SaveLayout(l)
	err := ApplyLayout(&l)
	RefreshNav(&l)
	if err == nil && args[0] == "pick" {
		return pickMenu(&l, arg(1), arg(3), arg(4)) // stay open for more picks
	}
	return err
}

// Launch starts (or re-attaches to) the tmux session that hosts the TUI.
func Launch(repo, self string) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is required")
	}
	ps := Panes()
	if _, ok := TuiPane(ps); !ok {
		cmdline := "'" + self + "' tui"
		// Windows created before the client attaches would otherwise be 80x24, too small for the layout.
		cols, rows := 200, 50
		if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 0 {
			cols, rows = int(ws.Col), int(ws.Row)
		}
		var id string
		if len(ps) == 0 {
			_, _ = Tmx("kill-session", "-t", SessionName) // stale empty session
			id, err = Tmx("-f", filepath.Join(repo, "config", "tmux.conf"), "new-session", "-d", "-P", "-F", "#{pane_id}",
				"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "-s", SessionName, "-n", "dash", "-e", "MULLET_REPO="+repo, cmdline)
		} else { // simulation still running from an earlier TUI
			Tmx("set", "-g", "default-size", fmt.Sprintf("%dx%d", cols, rows))
			id, err = Tmx("new-window", "-P", "-F", "#{pane_id}", "-t", SessionName+":", "-n", "dash", "-e", "MULLET_REPO="+repo, cmdline)
		}
		if err != nil {
			return err
		}
		Tmx("set", "-p", "-t", id, "@mullet_tui", "1")
	}
	env := os.Environ()
	for i := 0; i < len(env); i++ {
		if strings.HasPrefix(env[i], "TMUX=") {
			env = append(env[:i], env[i+1:]...)
			i--
		}
	}
	return execve(tmuxBin, []string{"tmux", "-L", Socket, "attach", "-t", SessionName}, env)
}

func clipboardPipe() string {
	for _, c := range []string{"wl-copy", "xclip -selection clipboard", "xsel --clipboard --input"} {
		name, _, _ := strings.Cut(c, " ")
		if _, err := exec.LookPath(name); err == nil {
			return c
		}
	}
	return ""
}

func installCopyBinds() {
	Tmx("set", "-g", "set-clipboard", "on")
	copy := []string{"send-keys", "-X", "copy-selection-and-cancel"}
	if pipe := clipboardPipe(); pipe != "" {
		copy = []string{"send-keys", "-X", "copy-pipe-and-cancel", pipe}
	}
	for _, table := range []string{"copy-mode-vi", "copy-mode"} {
		Tmx(append([]string{"bind", "-T", table, "MouseDragEnd1Pane"}, copy...)...)
	}
	Tmx("bind", "-n", "MouseDown2Pane", "paste-buffer")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func execve(bin string, argv, env []string) error { return syscall.Exec(bin, argv, env) }
