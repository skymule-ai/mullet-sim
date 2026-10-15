package ui

import (
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/skymule-ai/mullet-sim/tui/internal/host"
	"github.com/skymule-ai/mullet-sim/tui/internal/layout"
	"github.com/skymule-ai/mullet-sim/tui/internal/theme"
	"github.com/skymule-ai/mullet-sim/tui/internal/tmux"
)

// --- a tiny cell canvas for the layout editor and the maps ---

type cell struct {
	r      rune
	fg, bg string
	bold   bool
}

type canvas struct {
	w, h int
	c    [][]cell
}

func newCanvas(w, h int) *canvas {
	cv := &canvas{w: max(w, 1), h: max(h, 1)}
	for y := 0; y < cv.h; y++ {
		row := make([]cell, cv.w)
		for x := range row {
			row[x] = cell{r: ' '}
		}
		cv.c = append(cv.c, row)
	}
	return cv
}

func (cv *canvas) set(x, y int, r rune, fg, bg string, bold bool) {
	if x >= 0 && y >= 0 && x < cv.w && y < cv.h {
		if bg == "" {
			bg = cv.c[y][x].bg
		}
		cv.c[y][x] = cell{r, fg, bg, bold}
	}
}

func (cv *canvas) text(x, y int, s, fg string, bold bool) {
	for i, r := range []rune(s) {
		cv.set(x+i, y, r, fg, "", bold)
	}
}

func (cv *canvas) fill(r Rect, bg string) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if x >= 0 && y >= 0 && x < cv.w && y < cv.h {
				cv.c[y][x].bg = bg
			}
		}
	}
}

func (cv *canvas) box(r Rect, fg string, heavy bool) {
	h, v, tl, tr, bl, br := '─', '│', '╭', '╮', '╰', '╯'
	if heavy {
		h, v, tl, tr, bl, br = '━', '┃', '┏', '┓', '┗', '┛'
	}
	x2, y2 := r.X+r.W-1, r.Y+r.H-1
	for x := r.X + 1; x < x2; x++ {
		cv.set(x, r.Y, h, fg, "", false)
		cv.set(x, y2, h, fg, "", false)
	}
	for y := r.Y + 1; y < y2; y++ {
		cv.set(r.X, y, v, fg, "", false)
		cv.set(x2, y, v, fg, "", false)
	}
	cv.set(r.X, r.Y, tl, fg, "", false)
	cv.set(x2, r.Y, tr, fg, "", false)
	cv.set(r.X, y2, bl, fg, "", false)
	cv.set(x2, y2, br, fg, "", false)
}

func (cv *canvas) String() string {
	var b strings.Builder
	for y, row := range cv.c {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < len(row); {
			start := row[x]
			var run []rune
			for x < len(row) && row[x].fg == start.fg && row[x].bg == start.bg && row[x].bold == start.bold {
				run = append(run, row[x].r)
				x++
			}
			st := lipgloss.NewStyle().Bold(start.bold)
			if start.fg != "" {
				st = st.Foreground(lipgloss.Color(start.fg))
			}
			if start.bg != "" {
				st = st.Background(lipgloss.Color(start.bg))
			}
			b.WriteString(st.Render(string(run)))
		}
	}
	return b.String()
}

type mapPoint struct {
	x, y         float64
	label, color string
}

// fleetMap plots points (x right, y up) inside a bordered box.
func fleetMap(pts []mapPoint, w, h int) string {
	cv := newCanvas(w, h)
	cv.box(Rect{X: 0, Y: 0, W: w, H: h}, theme.Current.P.Border, false)
	if len(pts) == 0 || w < 5 || h < 4 {
		return cv.String()
	}
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	// Keep at least 1 m of span, centered, so aligned robots don't stick to an edge.
	if d := 1 - (maxX - minX); d > 0 {
		minX, maxX = minX-d/2, maxX+d/2
	}
	if d := 1 - (maxY - minY); d > 0 {
		minY, maxY = minY-d/2, maxY+d/2
	}
	spanX, spanY := maxX-minX, maxY-minY
	iw, ih := float64(w-5), float64(h-3)
	for _, p := range pts {
		px := 2 + int(math.Round((p.x-minX)/spanX*iw))
		py := h - 2 - int(math.Round((p.y-minY)/spanY*ih))
		cv.text(px, py, p.label, p.color, true)
	}
	cv.text(2, h-1, fmt.Sprintf(" %.0f×%.0f m ", spanX, spanY), theme.Current.P.Muted, false)
	return cv.String()
}

// joinColumns puts right next to left, which is padded/cropped to leftW cells.
func joinColumns(left string, leftW int, right string) string {
	l, r := strings.Split(left, "\n"), strings.Split(right, "\n")
	var out []string
	for i := 0; i < max(len(l), len(r)); i++ {
		line := ""
		if i < len(l) {
			line = padTo(ansi.Truncate(l[i], leftW, "…"), leftW)
		} else {
			line = strings.Repeat(" ", leftW)
		}
		if i < len(r) {
			line += "  " + r[i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// --- layout editor ---

type editorState struct {
	prev   Layout
	back   screen
	sel    string
	drag   string
	div    *Divider
	hover  string // region, or edge:<left|right|top|bottom>
	zone   string
	zoomed bool
}

func (a *app) enterLayout() tea.Cmd {
	a.ed = editorState{prev: a.layout.Clone(), back: a.screen, sel: "tui"}
	if a.ed.back == scrLayout || a.ed.back == scrForm {
		a.ed.back = scrHome
	}
	a.screen = scrLayout
	// Zoom the TUI pane so the editor gets the whole window.
	if tui, ok := tmux.TuiPane(tmux.Panes()); ok {
		if z, _ := tmux.Tmx("display", "-p", "-t", tui.ID, "#{window_zoomed_flag}"); z != "1" {
			if _, err := tmux.Tmx("resize-pane", "-Z", "-t", tui.ID); err == nil {
				a.ed.zoomed = true
			}
		}
	}
	return nil
}

func (a *app) exitLayout(apply bool) tea.Cmd {
	if !apply {
		a.layout = a.ed.prev
	}
	if a.ed.zoomed {
		if tui, ok := tmux.TuiPane(tmux.Panes()); ok {
			tmux.Tmx("resize-pane", "-Z", "-t", tui.ID)
		}
	}
	a.screen = a.ed.back
	_ = host.SaveLayout(a.layout)
	a.layoutMtime = host.LayoutMtime()
	if apply {
		a.notify("Layout applied", false)
	}
	return a.installCmd(true)
}

func (a *app) canvasRect() Rect { return Rect{X: 0, Y: 3, W: a.w, H: max(4, a.h-4-4)} }

func (a *app) hiddenRegions() []string {
	var out []string
	for _, r := range layout.RegionNames {
		if !a.layout.Root.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

func regionTitle(r string) string {
	switch r {
	case "tui":
		return theme.Current.I.Brand + " TUI"
	case "px4":
		return theme.Current.I.Multicopter + " PX4 logs"
	case "core":
		return theme.Current.I.Dash + " mullet-core"
	case "xrce":
		return "⇄ XRCE agent"
	}
	return "❯ code"
}

func (a *app) viewLayout() (string, string) {
	cr := a.canvasRect()
	cv := newCanvas(cr.W, cr.H)
	rects := map[string]Rect{}
	a.layout.Root.Rects(Rect{X: 0, Y: 0, W: cr.W, H: cr.H}, rects)
	counts := map[string]int{}
	for r, g := range tmux.RegionPanes(tmux.Panes()) {
		counts[r] = len(g)
	}

	for _, r := range a.layout.Root.Leaves() {
		rr := rects[r]
		color := theme.Current.P.RegionColor(r)
		if r == a.ed.sel {
			cv.fill(Rect{X: rr.X + 1, Y: rr.Y + 1, W: rr.W - 2, H: rr.H - 2}, theme.Current.P.Surface)
		}
		cv.box(rr, color, r == a.ed.sel)
		title := regionTitle(r)
		cy := rr.Y + rr.H/2 - 1
		cv.text(rr.X+max(1, (rr.W-len([]rune(title)))/2), cy, title, color, true)
		sub := "this TUI"
		if r != "tui" {
			sub = fmt.Sprintf("%d panes", counts[r])
		}
		cv.text(rr.X+max(1, (rr.W-len([]rune(sub)))/2), cy+1, sub, theme.Current.P.Muted, false)
	}

	// Drop preview.
	if a.ed.drag != "" && a.ed.hover != "" {
		var z Rect
		if edge, ok := strings.CutPrefix(a.ed.hover, "edge:"); ok {
			z = edgeRect(Rect{X: 0, Y: 0, W: cr.W, H: cr.H}, edge, .35)
		} else if a.ed.hover != a.ed.drag {
			t := rects[a.ed.hover]
			if a.ed.zone == "center" {
				z = Rect{X: t.X + 1, Y: t.Y + 1, W: t.W - 2, H: t.H - 2}
			} else {
				z = edgeRect(t, a.ed.zone, .5)
			}
		}
		if z.W > 0 {
			cv.fill(z, theme.Current.P.Border)
			label := "drop " + strings.ToUpper(a.ed.drag) + " here"
			if a.ed.zone == "center" && !strings.HasPrefix(a.ed.hover, "edge:") {
				label = "swap with " + strings.ToUpper(a.ed.hover)
			}
			cv.text(z.X+max(0, (z.W-len(label))/2), z.Y+z.H/2, label, theme.Current.P.Accent, true)
		}
	}

	head := "  " + theme.Current.Primary.Render(theme.Current.I.Layout+" LAYOUT") + "  " + theme.Current.Bold.Render(orDefault(a.layout.Name, "custom")) +
		theme.Current.Muted.Render("   drag a region onto another (edges split, center swaps) or to a window edge · drag borders to resize")
	hidden := a.hiddenRegions()
	hid := theme.Current.Muted.Render("hidden: ")
	if len(hidden) == 0 {
		hid += theme.Current.Muted.Render("none")
	}
	for _, r := range hidden {
		hid += theme.Current.Accent.Render(strings.ToUpper(r)) + " "
	}
	presets := theme.Current.Muted.Render("presets: 1 classic · 2 quad · 3 logs · 4 focus")
	footLines := tmux.PackWidth([]string{strings.TrimSpace(hid), presets}, theme.Current.Muted.Render("   "), max(1, a.w-2), 2, nil)
	for i := range footLines {
		footLines[i] = "  " + footLines[i]
	}
	body := strings.Join(append([]string{"", head, cv.String()}, footLines...), "\n")
	return body, "tab select · ←↑↓→ snap to edge · +/- resize · x hide · a add · s save · o open · enter apply · esc cancel"
}

func edgeRect(r Rect, edge string, f float64) Rect {
	w, h := int(float64(r.W)*f), int(float64(r.H)*f)
	switch edge {
	case "left":
		return Rect{X: r.X, Y: r.Y, W: w, H: r.H}
	case "right":
		return Rect{X: r.X + r.W - w, Y: r.Y, W: w, H: r.H}
	case "top":
		return Rect{X: r.X, Y: r.Y, W: r.W, H: h}
	}
	return Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}
}

func (a *app) updateLayout(msg tea.Msg) tea.Cmd {
	cr := a.canvasRect()
	local := func(m tea.Mouse) (int, int) { return m.X - cr.X, m.Y - cr.Y }
	rects := map[string]Rect{}
	a.layout.Root.Rects(Rect{X: 0, Y: 0, W: cr.W, H: cr.H}, rects)
	regionAt := func(x, y int) string {
		for r, rr := range rects {
			if rr.Contains(x, y) {
				return r
			}
		}
		return ""
	}
	l := &a.layout
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		x, y := local(m.Mouse())
		var divs []Divider
		l.Root.Dividers(Rect{X: 0, Y: 0, W: cr.W, H: cr.H}, &divs)
		for i := len(divs) - 1; i >= 0; i-- {
			if divs[i].At(x, y) {
				a.ed.div = &divs[i]
				return nil
			}
		}
		if r := regionAt(x, y); r != "" {
			a.ed.sel, a.ed.drag = r, r
		}
	case tea.MouseMotionMsg:
		x, y := local(m.Mouse())
		switch {
		case a.ed.div != nil:
			a.ed.div.Drag(x, y)
			l.Changed()
		case a.ed.drag != "":
			a.ed.hover, a.ed.zone = "", ""
			switch {
			case x <= 0:
				a.ed.hover = "edge:left"
			case x >= cr.W-1:
				a.ed.hover = "edge:right"
			case y <= 0:
				a.ed.hover = "edge:top"
			case y >= cr.H-1:
				a.ed.hover = "edge:bottom"
			default:
				if r := regionAt(x, y); r != "" {
					a.ed.hover, a.ed.zone = r, layout.ZoneAt(rects[r], x, y)
				}
			}
		}
	case tea.MouseReleaseMsg:
		if a.ed.drag != "" && a.ed.hover != "" {
			if edge, ok := strings.CutPrefix(a.ed.hover, "edge:"); ok {
				l.SnapEdge(a.ed.drag, edge)
			} else {
				l.SnapTo(a.ed.drag, a.ed.hover, a.ed.zone)
			}
			l.Name = ""
		}
		a.ed.drag, a.ed.hover, a.ed.zone, a.ed.div = "", "", "", nil
	case tea.KeyPressMsg:
		leaves := l.Root.Leaves()
		idx := 0
		for i, r := range leaves {
			if r == a.ed.sel {
				idx = i
			}
		}
		switch key := m.String(); key {
		case "tab":
			a.ed.sel = leaves[(idx+1)%len(leaves)]
		case "shift+tab":
			a.ed.sel = leaves[(idx+len(leaves)-1)%len(leaves)]
		case "left", "right", "up", "down", "h", "l", "k", "j":
			edge := map[string]string{"left": "left", "h": "left", "right": "right", "l": "right", "up": "top", "k": "top", "down": "bottom", "j": "bottom"}[key]
			l.SnapEdge(a.ed.sel, edge)
			l.Name = ""
		case "+", "=", "-":
			if p, inA := l.Root.ParentOf(a.ed.sel); p != nil {
				d := .05
				if (key == "-") == inA {
					d = -d
				}
				p.Ratio = math.Min(.85, math.Max(.15, math.Round((p.Ratio+d)*20)/20))
				l.Changed()
			}
		case "x":
			l.Hide(a.ed.sel)
			a.ed.sel = "tui"
		case "a":
			if hidden := a.hiddenRegions(); len(hidden) > 0 {
				l.SnapEdge(hidden[0], "right")
				a.ed.sel = hidden[0]
			}
		case "1", "2", "3", "4":
			*l = layout.Preset(layout.PresetNames[key[0]-'1'])
		case "s":
			name := orDefault(l.Name, "")
			f := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Layout name").Value(&name).Validate(matches(`[A-Za-z0-9_.-]+`))).Title("Save layout"))
			return a.openForm(f, scrLayout, func() tea.Cmd {
				l.Name = name
				if err := host.SaveNamedLayout(name, *l); err != nil {
					a.notify(err.Error(), true)
				} else {
					a.notify("Saved layout "+name, false)
				}
				return nil
			})
		case "o":
			names := append(append([]string{}, layout.PresetNames...), host.NamedLayouts()...)
			choice := names[0]
			f := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Open layout").Options(huh.NewOptions(names...)...).Value(&choice)))
			return a.openForm(f, scrLayout, func() tea.Cmd {
				if nl, err := host.LoadNamedLayout(choice); err == nil && nl.Root != nil {
					*l = nl
				} else {
					*l = layout.Preset(choice)
				}
				return nil
			})
		case "enter":
			return a.exitLayout(true)
		case "esc", "q":
			return a.exitLayout(false)
		}
	}
	return nil
}
