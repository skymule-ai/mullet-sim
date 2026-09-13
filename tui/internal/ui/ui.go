// Package ui is the Bubble Tea TUI. FIGlet ANSI Shadow wordmark + diagonal specular shine;
// compact mark is Unicode block elements.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/host"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/session"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/tmux"
)

type (
	Settings   = host.Settings
	HostStatus = host.HostStatus
	ImageInfo  = host.ImageInfo
	Airframe   = host.Airframe
	Session    = session.Session
	Robot      = session.Robot
	Command    = session.Command
	Layout     = layout.Layout
	Rect       = layout.Rect
	Divider    = layout.Divider
	Node       = layout.Node
)

type screen int

const (
	scrHome screen = iota
	scrOpen
	scrFleet
	scrCommands
	scrForm
	scrLaunch
	scrDash
	scrLayout
)

// hit is a clickable area registered while rendering.
type hit struct {
	r      Rect
	action string
}

type dialog struct {
	title, body string
	options     []string
	sel         int
	on          func(choice int) tea.Cmd // -1 = cancelled
}

type app struct {
	repo, self string
	set        Settings
	host       HostStatus
	images     []ImageInfo
	sess       *Session
	problems   []string
	screen     screen
	w, h       int
	frame      int
	hits       []hit

	menu   int // home menu / open list cursor
	files  []string
	sel    int // fleet row
	cmdSel int // commands row

	form     *huh.Form
	formBack screen
	onSubmit func() tea.Cmd

	launch      launchState
	dash        dashState
	ed          editorState
	layout      Layout
	layoutMtime int64

	dialog   *dialog
	toast    string
	toastErr bool
	toastAt  time.Time
	busy     string
	quitKill bool
}

type (
	pollMsg   struct{}
	frameMsg  struct{}
	hostMsg   HostStatus
	imagesMsg []ImageInfo
	toastMsg  struct {
		text string
		err  bool
	}
	stoppedMsg struct{ err error }
	installMsg struct{ err error }
)

// Run starts the Bubble Tea TUI.
func Run(repo string) error {
	self, _ := os.Executable()
	set := host.LoadSettings()
	theme.Apply(set.Theme, set.Icons)
	a := &app{repo: repo, self: self, set: set, layout: host.LoadLayout(), layoutMtime: host.LayoutMtime()}
	if set.LastSession != "" {
		if s, p, err := session.Load(repo, filepath.Join(repo, set.LastSession)); err == nil {
			a.sess, a.problems = s, p
		}
	}
	a.host.Running = host.SimRunning()
	if a.host.Running && a.sess != nil {
		a.screen = scrDash
	}
	_, err := tea.NewProgram(a).Run()
	a.dash.stopHeartbeat()
	if a.quitKill {
		tmux.Tmx("kill-session", "-t", tmux.SessionName)
	}
	return err
}

func pollEvery() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollMsg{} })
}
func frameEvery() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return frameMsg{} })
}

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{pollEvery(), frameEvery(), a.installCmd(a.host.Running), a.discoverCmd()}
	if a.screen == scrDash {
		cmds = append(cmds, a.enterDash())
	}
	return tea.Batch(cmds...)
}

func (a *app) discoverCmd() tea.Cmd {
	match := a.set.ImageMatch
	return tea.Batch(
		func() tea.Msg { return hostMsg(host.Status()) },
		func() tea.Msg {
			imgs, _ := host.ListImages(match)
			for i := range imgs {
				imgs[i] = host.ProbeImage(imgs[i])
			}
			return imagesMsg(imgs)
		})
}

// installCmd themes the tmux session, installs the bindings and (optionally) re-applies the layout.
func (a *app) installCmd(apply bool) tea.Cmd {
	self, set, l := a.self, a.set, a.layout
	return func() tea.Msg {
		tmux.Install(self, set, &l)
		if apply {
			return installMsg{tmux.ApplyLayout(&l)}
		}
		return installMsg{}
	}
}

func (a *app) notify(text string, isErr bool) {
	a.toast, a.toastErr, a.toastAt = text, isErr, time.Now()
}

// image returns the probed info of the session image (or the first usable image).
func (a *app) image() (ImageInfo, bool) {
	for _, img := range a.images {
		if a.sess != nil && img.Ref == a.sess.Image {
			return img, true
		}
	}
	for _, img := range a.images {
		if img.HasPX4 && (a.sess == nil || a.sess.Image == "") {
			return img, true
		}
	}
	return ImageInfo{}, false
}

func (a *app) validate() {
	if a.sess == nil {
		return
	}
	a.problems = a.sess.Validate(a.repo)
	if img, ok := a.image(); ok && len(img.Airframes) > 0 {
		for _, r := range a.sess.Fleet {
			if !img.HasModel(r.Model) {
				a.problems = append(a.problems, fmt.Sprintf("Config error: %s: model %s is not in %s", r.ID, r.Model, img.Ref))
			}
		}
	}
}

// --- dialogs, stop, quit ---

func (a *app) confirm(title, body string, options []string, on func(int) tea.Cmd) {
	a.dialog = &dialog{title: title, body: body, options: options, on: on}
}

func (a *app) askStop() tea.Cmd {
	if !a.host.Running {
		a.notify("No simulation running", false)
		return nil
	}
	if !a.set.ConfirmStop {
		return a.doStop()
	}
	a.confirm(theme.Current.I.Stop+" Stop the simulation?", "Containers and simulation panes will be closed.",
		[]string{"Stop", "Cancel"}, func(c int) tea.Cmd {
			if c == 0 {
				return a.doStop()
			}
			return nil
		})
	return nil
}

func (a *app) doStop() tea.Cmd {
	a.busy = "Stopping the simulation…"
	a.dash.stopHeartbeat()
	repo := a.repo
	return func() tea.Msg {
		cmd := exec.Command(filepath.Join(repo, "stop.sh"))
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%v: %s", err, lastLine(string(out)))
		}
		return stoppedMsg{err}
	}
}

func (a *app) askQuit() tea.Cmd {
	if !a.host.Running {
		a.quitKill = true
		return tea.Quit
	}
	a.confirm("Quit mullet", "The simulation is still running.",
		[]string{"Detach (keep running)", "Stop & quit", "Cancel"}, func(c int) tea.Cmd {
			switch c {
			case 0:
				tmux.Tmx("detach-client", "-s", tmux.SessionName)
			case 1:
				a.quitKill = true
				return tea.Sequence(a.doStop(), tea.Quit)
			}
			return nil
		})
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// --- update ---

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		if a.form != nil {
			a.form = a.form.WithWidth(a.formWidth())
		}
	case frameMsg:
		a.frame++
		return a, frameEvery()
	case pollMsg:
		return a, tea.Batch(a.pollCmd(), pollEvery())
	case statusMsg:
		return a, a.onStatus(m)
	case hostMsg:
		running := a.host.Running
		a.host = HostStatus(m)
		a.host.Running = running || a.host.Running
		return a, nil
	case imagesMsg:
		a.images = m
		if a.sess != nil && a.sess.Image == "" {
			if img, ok := a.image(); ok {
				a.sess.Image = img.Ref
			}
		}
		a.validate()
		return a, nil
	case toastMsg:
		a.notify(m.text, m.err)
		return a, nil
	case installMsg:
		if m.err != nil {
			a.notify("Layout: "+m.err.Error(), true)
		}
		return a, nil
	case stoppedMsg:
		a.busy = ""
		a.host.Running = false
		a.dash = dashState{}
		if m.err != nil {
			a.notify("Stop failed: "+m.err.Error(), true)
		} else {
			a.notify(theme.Current.I.Stop+" Simulation stopped", false)
		}
		if a.screen == scrDash || a.screen == scrLaunch {
			a.screen = scrFleet
			if a.sess == nil {
				a.screen = scrHome
			}
		}
		return a, nil
	case launchLineMsg, launchDoneMsg, launchedMsg:
		return a, a.updateLaunch(msg)
	case hbMsg:
		return a, a.dash.onHeartbeat(m)
	}

	if a.dialog != nil {
		return a, a.updateDialog(msg)
	}
	if a.screen == scrForm && a.form != nil {
		return a, a.updateForm(msg)
	}

	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "ctrl+x":
			return a, a.askStop()
		case "ctrl+c":
			return a, a.askQuit()
		}
	case tea.MouseClickMsg:
		if a.hitAt(m.X, m.Y) == "stop" {
			return a, a.askStop()
		}
	}

	switch a.screen {
	case scrHome:
		return a, a.updateHome(msg)
	case scrOpen:
		return a, a.updateOpen(msg)
	case scrFleet:
		return a, a.updateFleet(msg)
	case scrCommands:
		return a, a.updateCommands(msg)
	case scrLaunch:
		return a, a.updateLaunch(msg)
	case scrDash:
		return a, a.updateDash(msg)
	case scrLayout:
		return a, a.updateLayout(msg)
	}
	return a, nil
}

func (a *app) hitAt(x, y int) string {
	for i := len(a.hits) - 1; i >= 0; i-- {
		if a.hits[i].r.Contains(x, y) {
			return a.hits[i].action
		}
	}
	return ""
}

func (a *app) updateDialog(msg tea.Msg) tea.Cmd {
	d := a.dialog
	choose := func(c int) tea.Cmd {
		a.dialog = nil
		return d.on(c)
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "left", "shift+tab", "h":
			d.sel = (d.sel + len(d.options) - 1) % len(d.options)
		case "right", "tab", "l":
			d.sel = (d.sel + 1) % len(d.options)
		case "enter", "space":
			return choose(d.sel)
		case "esc", "q", "ctrl+c":
			return choose(-1)
		}
	case tea.MouseClickMsg:
		var c int
		if _, err := fmt.Sscanf(a.hitAt(m.X, m.Y), "dlg:%d", &c); err == nil {
			return choose(c)
		}
	}
	return nil
}

// openForm shows a huh form; onSubmit runs when it completes, esc returns to back.
func (a *app) openForm(f *huh.Form, back screen, onSubmit func() tea.Cmd) tea.Cmd {
	a.form = f.WithTheme(theme.HuhTheme()).WithKeyMap(formKeys()).WithWidth(a.formWidth()).
		WithHeight(max(8, a.h-6)).WithShowHelp(true)
	a.formBack, a.onSubmit, a.screen = back, onSubmit, scrForm
	return a.form.Init()
}

func formKeys() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Input.Prev.SetKeys("shift+tab", "up")
	km.Input.Next.SetKeys("enter", "tab", "down")
	km.Text.Prev.SetKeys("shift+tab", "up")
	km.Text.Next.SetKeys("tab", "enter", "down")
	km.Confirm.Prev.SetKeys("shift+tab", "up")
	km.Confirm.Next.SetKeys("enter", "tab", "down")
	km.Note.Prev.SetKeys("shift+tab", "up")
	km.Note.Next.SetKeys("enter", "tab", "down")
	km.Select.Left.SetEnabled(true)
	km.Select.Right.SetEnabled(true)
	return km
}

func (a *app) formWidth() int { return max(30, min(a.w-4, 96)) }

func (a *app) feedForm(msg tea.Msg) tea.Cmd {
	m, cmd := a.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		a.form = f
	}
	switch a.form.State {
	case huh.StateCompleted:
		a.form, a.screen = nil, a.formBack
		return tea.Batch(cmd, a.onSubmit())
	case huh.StateAborted:
		a.form, a.screen = nil, a.formBack
	}
	return cmd
}

func (a *app) updateForm(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		a.form, a.screen = nil, a.formBack
		return nil
	}
	switch m := msg.(type) {
	case tea.MouseWheelMsg:
		key := "down"
		if m.Button == tea.MouseWheelUp {
			key = "up"
		}
		msg = tea.KeyPressMsg{Text: key}
	case tea.MouseClickMsg:
		return a.formClick(m)
	}
	return a.feedForm(msg)
}

func (a *app) formClick(m tea.MouseClickMsg) tea.Cmd {
	y := m.Mouse().Y - 1
	if y < 0 || a.form == nil {
		return nil
	}
	switch a.form.GetFocusedField().(type) {
	case *huh.Confirm:
		return a.feedForm(tea.KeyPressMsg{Text: "l"})
	case *huh.Select[string], *huh.MultiSelect[string]:
		n := optionIndexAt(strings.Split(ansi.Strip(a.form.View()), "\n"), y)
		if n < 0 {
			return nil
		}
		keys := []string{"g"}
		for i := 0; i < n; i++ {
			keys = append(keys, "down")
		}
		var cmd tea.Cmd
		for _, k := range keys {
			cmd = a.feedForm(tea.KeyPressMsg{Text: k})
		}
		return cmd
	}
	return nil
}

func optionIndexAt(lines []string, y int) int {
	if y < 0 || y >= len(lines) || !isOptionLine(lines[y]) {
		return -1
	}
	start := y
	for start > 0 && isOptionLine(lines[start-1]) {
		start--
	}
	return y - start
}

func isOptionLine(s string) bool {
	t := strings.TrimSpace(ansi.Strip(s))
	if t == "" {
		return false
	}
	plain := ansi.Strip(s)
	trim := strings.TrimLeft(plain, " ")
	return strings.HasPrefix(trim, ">") || strings.HasPrefix(t, "•") || strings.HasPrefix(t, "[") ||
		(strings.HasPrefix(plain, "  ") && !strings.Contains(t, ":") && len(t) > 1)
}

// --- view ---

func (a *app) View() tea.View {
	a.hits = a.hits[:0]
	if a.w == 0 {
		return tea.NewView("")
	}
	var body, hints string
	switch a.screen {
	case scrHome:
		body, hints = a.viewHome()
	case scrOpen:
		body, hints = a.viewOpen()
	case scrFleet:
		body, hints = a.viewFleet()
	case scrCommands:
		body, hints = a.viewCommands()
	case scrForm:
		body, hints = a.form.View(), "↑↓ move · tab next · enter confirm · esc cancel"
	case scrLaunch:
		body, hints = a.viewLaunch()
	case scrDash:
		body, hints = a.viewDash()
	case scrLayout:
		body, hints = a.viewLayout()
	}
	foot := a.footer(hints)
	footH := strings.Count(foot, "\n") + 1
	bodyH := max(1, a.h-3-footH)
	if a.dialog != nil {
		body = a.viewDialog(bodyH)
	}
	status := ""
	if a.busy != "" {
		status = theme.Current.Accent.Render(spinner(a.frame) + " " + a.busy)
	} else if a.toast != "" && time.Since(a.toastAt) < 5*time.Second {
		style := theme.Current.OK
		if a.toastErr {
			style = theme.Current.Stop
		}
		status = style.Render(a.toast)
	}
	content := strings.Join([]string{a.header(), fit(body, a.w, bodyH), " " + status, foot}, "\n")
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "mullet"
	return v
}

// fit crops/pads a block to exactly w x h cells.
func fit(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func spinner(frame int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if a := theme.Current.I.Brand; a == "*" {
		frames = []string{"|", "/", "-", "\\"}
	}
	return frames[frame%len(frames)]
}

func (a *app) header() string {
	left := theme.Current.Title.Render(theme.Current.I.Brand + " mullet sitl")
	var info []string
	if a.sess != nil {
		info = append(info, theme.Current.Bold.Render(a.sess.Name))
		if img, ok := a.image(); ok {
			info = append(info, theme.Current.Muted.Render("PX4 ")+theme.Current.Accent.Render(img.Version()))
		}
		info = append(info, theme.Current.Muted.Render("DOMAIN ")+theme.Current.Accent.Render(fmt.Sprint(a.sess.DomainID)))
	}
	if a.host.Running {
		info = append(info, theme.Current.OK.Render(theme.Current.I.Ready+" running"))
	} else {
		info = append(info, theme.Current.Muted.Render(theme.Current.I.Stopped+" idle"))
	}
	line := left + " " + strings.Join(info, theme.Current.Muted.Render(" │ "))
	stop := theme.Current.StopButton.Render(theme.Current.I.Stop + " STOP")
	if !a.host.Running {
		stop = theme.Current.Button.Background(lipgloss.Color(theme.Current.P.Border)).Foreground(lipgloss.Color(theme.Current.P.Muted)).Render(theme.Current.I.Stop + " STOP")
	}
	sw := lipgloss.Width(stop)
	line = ansi.Truncate(line, max(0, a.w-sw-1), "…")
	gap := max(1, a.w-lipgloss.Width(line)-sw)
	a.hits = append(a.hits, hit{Rect{X: a.w - sw, Y: 0, W: sw, H: 1}, "stop"})
	return line + strings.Repeat(" ", gap) + stop
}

func (a *app) footer(hints string) string {
	var parts []string
	for _, h := range strings.Split(hints+" · ctrl+x stop · ctrl+c quit", " · ") {
		if k, d, ok := strings.Cut(h, " "); ok {
			parts = append(parts, theme.Current.Key.Render(k)+" "+theme.Current.Muted.Render(d))
		}
	}
	sep := theme.Current.Muted.Render(" · ")
	lines := tmux.PackWidth(parts, sep, max(1, a.w-1), 2, nil)
	if len(lines) == 0 {
		return ""
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(" "+l, a.w, "…")
	}
	return strings.Join(lines, "\n")
}

func (a *app) viewDialog(h int) string {
	d := a.dialog
	var buttons []string
	for i, o := range d.options {
		style := theme.Current.Button.Background(lipgloss.Color(theme.Current.P.Surface)).Foreground(lipgloss.Color(theme.Current.P.Text))
		if i == d.sel {
			style = theme.Current.Button
			if strings.Contains(o, "Stop") {
				style = theme.Current.StopButton
			}
		}
		buttons = append(buttons, style.Render(o))
	}
	row := strings.Join(buttons, "  ")
	box := theme.Current.Box.BorderForeground(lipgloss.Color(theme.Current.P.Primary)).Padding(1, 3).Render(
		theme.Current.Bold.Render(d.title) + "\n\n" + theme.Current.Muted.Render(d.body) + "\n\n" + row)
	bw, bh := lipgloss.Size(box)
	x0, y0 := max(0, (a.w-bw)/2), max(0, (h-bh)/2)
	// Buttons sit on the box's last inner row; register them for clicks.
	bx := x0 + 4
	for i, b := range buttons {
		w := lipgloss.Width(b)
		a.hits = append(a.hits, hit{Rect{X: bx, Y: 2 + y0 + bh - 3, W: w, H: 1}, fmt.Sprintf("dlg:%d", i)})
		bx += w + 2
	}
	return lipgloss.Place(a.w, h, lipgloss.Center, lipgloss.Center, box)
}
