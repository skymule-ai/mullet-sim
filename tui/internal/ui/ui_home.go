package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/skymule-ai/mullet-sim/tui/internal/session"
	"github.com/skymule-ai/mullet-sim/tui/internal/theme"
)

var wordmark = []string{
	"█▀▄▀█ █ █ █   █   █▀▀ ▀█▀",
	"█ ▀ █ █▄█ █▄▄ █▄▄ ██▄  █ ",
}

var wordmarkBig = []string{
	` ███╗   ███╗ ██╗   ██╗ ██╗     ██╗     ███████╗ ████████╗`,
	` ████╗ ████║ ██║   ██║ ██║     ██║     ██╔════╝ ╚══██╔══╝`,
	` ██╔████╔██║ ██║   ██║ ██║     ██║     █████╗      ██║   `,
	` ██║╚██╔╝██║ ██║   ██║ ██║     ██║     ██╔══╝      ██║   `,
	` ██║ ╚═╝ ██║ ╚██████╔╝ ███████╗███████╗███████╗    ██║   `,
	` ╚═╝     ╚═╝  ╚═════╝  ╚══════╝╚══════╝╚══════╝    ╚═╝   `,
}

// shineBand: 0 idle, 1 halo, 2 hot slash. cycle ~4.8s at 120ms/frame.
func shineBand(x, y, frame, w int) int {
	cycle := frame % 40
	if cycle > 12 {
		return 0
	}
	slash := cycle*(w+10)/12 - 5
	d := x - slash + y
	switch {
	case d >= 0 && d <= 1:
		return 2
	case d >= -1 && d <= 3:
		return 1
	}
	return 0
}

func (a *app) renderWordmark() []string {
	mark := wordmark
	if a.w >= 62 && theme.Current.I.Brand != "*" {
		mark = wordmarkBig
	}
	w := 0
	rows := make([][]rune, len(mark))
	for i, l := range mark {
		rows[i] = []rune(l)
		if n := len(rows[i]); n > w {
			w = n
		}
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Current.P.Border)).Background(lipgloss.Color(theme.Current.P.Surface))
	shadow := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Current.P.Border))
	base := theme.Current.Primary.Background(lipgloss.Color(theme.Current.P.Surface))
	mid := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Current.P.Accent)).Background(lipgloss.Color(theme.Current.P.Surface)).Bold(true)
	hot := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color(theme.Current.P.Surface)).Bold(true)
	shine := theme.Current.I.Brand != "*"
	out := make([]string, 0, len(rows)+1)
	for y, row := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for x, r := range row {
			if r == ' ' {
				b.WriteString(dim.Render("░"))
				continue
			}
			st := base
			if shine {
				switch shineBand(x, y, a.frame, w) {
				case 2:
					st = hot
				case 1:
					st = mid
				}
			}
			b.WriteString(st.Render(string(r)))
		}
		b.WriteString(shadow.Render("▓"))
		out = append(out, b.String())
	}
	var sh strings.Builder
	sh.WriteString("   ")
	for range w {
		sh.WriteString(shadow.Render("░"))
	}
	return append(out, sh.String())
}

type menuItem struct{ icon, label, action, key string }

func (a *app) homeMenu() []menuItem {
	var items []menuItem
	if a.host.Running && a.sess != nil {
		items = append(items, menuItem{theme.Current.I.Dash, "Dashboard", "dash", "d"})
	}
	if a.sess != nil {
		items = append(items, menuItem{theme.Current.I.Resume, fmt.Sprintf("Resume  %s  (%d robots)", a.sess.Name, len(a.sess.Fleet)), "resume", "r"})
	}
	items = append(items,
		menuItem{theme.Current.I.Add, "New session", "new", "n"},
		menuItem{theme.Current.I.Open, "Open session…", "open", "o"},
		menuItem{theme.Current.I.Layout, "Layout editor", "layout", "l"},
		menuItem{theme.Current.I.Settings, "Settings", "settings", "s"},
	)
	if a.host.Running {
		items = append(items, menuItem{theme.Current.I.Stop, "Stop simulation", "stop", "x"})
	}
	return append(items, menuItem{theme.Current.I.Failed, "Quit", "quit", "q"})
}

func check(ok bool, label string) string {
	if ok {
		return theme.Current.OK.Render(theme.Current.I.Ready) + " " + theme.Current.Text.Render(label)
	}
	return theme.Current.Stop.Render(theme.Current.I.Failed) + " " + theme.Current.Muted.Render(label)
}

func (a *app) viewHome() (string, string) {
	lines := []string{""}
	lines = append(lines, a.renderWordmark()...)
	lines = append(lines, "  "+theme.Current.Muted.Render("multi-robot PX4 SITL · Gazebo · ROS 2"), "")
	items := a.homeMenu()
	a.menu = min(a.menu, len(items)-1)
	for i, it := range items {
		row := fmt.Sprintf("  %s  %-40s %s", it.icon, it.label, theme.Current.Muted.Render(it.key))
		if i == a.menu {
			row = theme.Current.Accent.Render("▸ ") + theme.Current.Bold.Render(fmt.Sprintf("%s  %-40s", it.icon, it.label)) + " " + theme.Current.Key.Render(it.key)
		}
		a.hits = append(a.hits, hit{Rect{X: 0, Y: 1 + len(lines), W: a.w, H: 1}, fmt.Sprintf("menu:%d", i)})
		lines = append(lines, row)
	}
	usable := 0
	for _, img := range a.images {
		if img.HasPX4 {
			usable++
		}
	}
	tmuxOK := a.host.Tmux >= "3.4" || len(a.host.Tmux) > 3 && a.host.Tmux[:2] != "3." && a.host.Tmux[:2] != "2."
	lines = append(lines, "",
		"  "+strings.Join([]string{
			check(a.host.Docker, "docker"),
			check(tmuxOK, "tmux "+a.host.Tmux),
			check(a.host.GPU, "nvidia gpu"),
			check(usable > 0, fmt.Sprintf("%d PX4 image(s)", usable)),
		}, theme.Current.Muted.Render("   ")))
	return strings.Join(lines, "\n"), "↑↓ move · enter select"
}

func (a *app) updateHome(msg tea.Msg) tea.Cmd {
	items := a.homeMenu()
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch k := m.String(); k {
		case "up", "k":
			a.menu = (a.menu + len(items) - 1) % len(items)
		case "down", "j":
			a.menu = (a.menu + 1) % len(items)
		case "enter", "space":
			return a.homeAction(items[a.menu].action)
		case "esc":
			return nil
		default:
			for _, it := range items {
				if it.key == k {
					return a.homeAction(it.action)
				}
			}
		}
	case tea.MouseClickMsg:
		var i int
		if _, err := fmt.Sscanf(a.hitAt(m.X, m.Y), "menu:%d", &i); err == nil && i < len(items) {
			a.menu = i
			return a.homeAction(items[i].action)
		}
	}
	return nil
}

func (a *app) homeAction(action string) tea.Cmd {
	switch action {
	case "dash":
		a.screen = scrDash
		return a.enterDash()
	case "resume":
		a.screen = scrFleet
		a.validate()
	case "new":
		img, _ := a.image()
		a.sess = session.New(img.Ref)
		a.sess.Name = "session_" + time.Now().Format("20060102_1504")
		a.sel = 0
		a.validate()
		a.screen = scrFleet
	case "open":
		a.files = nil
		for _, pattern := range []string{"config/sessions/*.yaml", "config/*.yaml"} {
			matches, _ := filepath.Glob(filepath.Join(a.repo, pattern))
			for _, m := range matches {
				rel, _ := filepath.Rel(a.repo, m)
				a.files = append(a.files, rel)
			}
		}
		a.menu, a.screen = 0, scrOpen
	case "layout":
		return a.enterLayout()
	case "settings":
		return a.openSettings()
	case "stop":
		return a.askStop()
	case "quit":
		return a.askQuit()
	}
	return nil
}

func (a *app) viewOpen() (string, string) {
	lines := []string{"", "  " + theme.Current.Primary.Render(theme.Current.I.Open+" Open session"), ""}
	if len(a.files) == 0 {
		lines = append(lines, "  "+theme.Current.Muted.Render("No session files in config/ yet."))
	}
	for i, f := range a.files {
		row := "    " + theme.Current.Text.Render(f)
		if i == a.menu {
			row = "  " + theme.Current.Accent.Render("▸ ") + theme.Current.Bold.Render(f)
		}
		a.hits = append(a.hits, hit{Rect{X: 0, Y: 1 + len(lines), W: a.w, H: 1}, fmt.Sprintf("open:%d", i)})
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n"), "↑↓ move · enter open · esc back"
}

func (a *app) updateOpen(msg tea.Msg) tea.Cmd {
	n := max(1, len(a.files))
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "up", "k":
			a.menu = (a.menu + n - 1) % n
		case "down", "j":
			a.menu = (a.menu + 1) % n
		case "enter", "space":
			return a.openFile()
		case "esc", "q":
			a.menu, a.screen = 0, scrHome
		}
	case tea.MouseClickMsg:
		var i int
		if _, err := fmt.Sscanf(a.hitAt(m.X, m.Y), "open:%d", &i); err == nil {
			a.menu = i
			return a.openFile()
		}
	}
	return nil
}

func (a *app) openFile() tea.Cmd {
	if a.menu >= len(a.files) {
		return nil
	}
	rel := a.files[a.menu]
	s, problems, err := session.Load(a.repo, filepath.Join(a.repo, rel))
	if err != nil {
		a.notify(err.Error(), true)
		return nil
	}
	a.sess, a.problems, a.sel = s, problems, 0
	a.set.LastSession = rel
	_ = a.set.Save()
	a.validate()
	a.menu, a.screen = 0, scrFleet
	if !strings.HasPrefix(rel, "config/sessions/") {
		a.notify("Opened "+rel+" — saving writes "+s.Path()+" (the original stays untouched)", false)
	}
	return nil
}

func (a *app) openSettings() tea.Cmd {
	themeName, icons, nav, confirm, match := a.set.Theme, a.set.Icons, a.set.NavPosition, a.set.ConfirmStop, a.set.ImageMatch
	var themes []huh.Option[string]
	for _, p := range theme.Palettes {
		themes = append(themes, huh.NewOption(p.Label, p.Name))
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Theme").Options(themes...).Value(&themeName).Height(min(6, max(2, len(themes)))),
		huh.NewSelect[string]().Title("Icons").Options(
			huh.NewOption("Unicode   ✢ ⇅ ✈ ⛟ ●", "unicode"),
			huh.NewOption("ASCII     X V > R *", "ascii")).Value(&icons).Height(2),
		huh.NewSelect[string]().Title("Navigation bar").Description("tmux tabs + STOP line").Options(
			huh.NewOption("Bottom", "bottom"), huh.NewOption("Top", "top")).Value(&nav).Height(2),
		huh.NewConfirm().Title("Confirm before STOP").Value(&confirm),
		huh.NewInput().Title("Image filter").Description("List local Docker images whose repository contains this text").Value(&match),
	).Title(theme.Current.I.Settings + " Settings"))
	back := a.screen
	return a.openForm(f, back, func() tea.Cmd {
		rediscover := match != a.set.ImageMatch
		a.set.Theme, a.set.Icons, a.set.NavPosition, a.set.ConfirmStop, a.set.ImageMatch = themeName, icons, nav, confirm, match
		if err := a.set.Save(); err != nil {
			a.notify(err.Error(), true)
		} else {
			a.notify("Settings saved", false)
		}
		theme.Apply(a.set.Theme, a.set.Icons)
		cmds := []tea.Cmd{a.installCmd(false)}
		if rediscover {
			cmds = append(cmds, a.discoverCmd())
		}
		return tea.Batch(cmds...)
	})
}
