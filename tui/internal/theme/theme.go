// Package theme holds palettes, icons, and lipgloss styles for the TUI and tmux session.
package theme

import (
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type Palette struct {
	Name, Label                                                         string
	Base, Surface, Border, Text, Muted, Primary, Accent, OK, Warn, Stop string
	Robots                                                              [8]string
}

var Palettes = []Palette{
	{Name: "dusk", Label: "Mullet Dusk",
		Base: "#0E1116", Surface: "#161A22", Border: "#2A2F3C", Text: "#E6E9EF", Muted: "#8B93A7",
		Primary: "#4CC2FF", Accent: "#FFB547", OK: "#3DDC97", Warn: "#F5C04E", Stop: "#FF5470",
		Robots: [8]string{"#4CC2FF", "#FFB547", "#B18CFF", "#3DDC97", "#FF7AB6", "#FF8F5A", "#6C9CFF", "#C6E86B"}},
	{Name: "hud", Label: "Aviation HUD",
		Base: "#0A0C0A", Surface: "#111511", Border: "#243024", Text: "#D8F5D0", Muted: "#6E8A6A",
		Primary: "#7CFF6B", Accent: "#FF8A1F", OK: "#7CFF6B", Warn: "#FFD23F", Stop: "#FF3B30",
		Robots: [8]string{"#7CFF6B", "#FF8A1F", "#3FD0FF", "#FFD23F", "#FF5DA2", "#B98CFF", "#4DFFC3", "#F2F5A8"}},
	{Name: "mocha", Label: "Catppuccin Mocha",
		Base: "#1E1E2E", Surface: "#313244", Border: "#45475A", Text: "#CDD6F4", Muted: "#A6ADC8",
		Primary: "#89B4FA", Accent: "#F5C2E7", OK: "#A6E3A1", Warn: "#F9E2AF", Stop: "#F38BA8",
		Robots: [8]string{"#89B4FA", "#FAB387", "#CBA6F7", "#A6E3A1", "#F5C2E7", "#F9E2AF", "#94E2D5", "#EBA0AC"}},
}

func PaletteByName(name string) Palette {
	for _, p := range Palettes {
		if p.Name == name {
			return p
		}
	}
	return Palettes[0]
}

// RobotColor matches run.sh: instance 1 gets the first color.
func (p Palette) RobotColor(inst int) string { return p.Robots[((inst+7)%8+8)%8] }

func (p Palette) RegionColor(r string) string {
	switch r {
	case "tui":
		return p.Primary
	case "px4":
		return p.Accent
	case "core":
		return p.Robots[2]
	case "code":
		return p.OK
	}
	return p.Muted
}

type Icons struct {
	Multicopter, VTOL, FixedWing, Rover, Other string
	Ready, Booting, Failed, Stopped            string
	Brand, Launch, Stop, Multi, Single         string
	Add, Open, Layout, Settings, Resume, Dash  string
}

var iconSets = map[string]Icons{
	"unicode": {"✢", "⇅", "✈", "⛟", "◆", "●", "◐", "✕", "○", "✦", "▶", "■", "⊞", "▢", "✚", "◫", "▤", "⚙", "↻", "◉"},
	"ascii":   {"X", "V", ">", "R", "*", "*", "~", "x", "o", "*", ">", "#", "+", "-", "+", "o", "=", "%", "<", "@"},
}

func RobotType(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "vtol"), strings.Contains(m, "tailsitter"), strings.Contains(m, "tiltrotor"):
		return "VTOL"
	case strings.Contains(m, "plane"), strings.Contains(m, "cessna"):
		return "Fixed-wing"
	case strings.Contains(m, "rover"), strings.Contains(m, "lawnmower"):
		return "Rover"
	case strings.Contains(m, "spacecraft"):
		return "Other"
	}
	return "Multicopter"
}

func (i Icons) Robot(model string) string {
	switch RobotType(model) {
	case "VTOL":
		return i.VTOL
	case "Fixed-wing":
		return i.FixedWing
	case "Rover":
		return i.Rover
	case "Other":
		return i.Other
	}
	return i.Multicopter
}

// Theme bundles the active palette, icons and lipgloss styles.
type Theme struct {
	P                                                  Palette
	I                                                  Icons
	Text, Muted, Primary, Accent, OK, Warn, Stop, Bold lipgloss.Style
	Header, Title, Key, Selected, StopButton, Button   lipgloss.Style
	Box                                                lipgloss.Style
}

// Current is the active theme, set by Apply.
var Current Theme

// Apply sets Current from a palette name (dusk, hud, mocha) and icon set (unicode, ascii).
func Apply(themeName, icons string) {
	p := PaletteByName(themeName)
	iconSet, ok := iconSets[icons]
	if !ok {
		iconSet = iconSets["unicode"]
	}
	c := lipgloss.Color
	fg := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(c(hex)) }
	Current = Theme{
		P: p, I: iconSet,
		Text: fg(p.Text), Muted: fg(p.Muted), Primary: fg(p.Primary).Bold(true), Accent: fg(p.Accent),
		OK: fg(p.OK), Warn: fg(p.Warn), Stop: fg(p.Stop), Bold: fg(p.Text).Bold(true),
		Header:     lipgloss.NewStyle().Background(c(p.Surface)).Foreground(c(p.Text)),
		Title:      lipgloss.NewStyle().Foreground(c(p.Base)).Background(c(p.Primary)).Bold(true).Padding(0, 1),
		Key:        fg(p.Accent).Bold(true),
		Selected:   lipgloss.NewStyle().Background(c(p.Border)).Foreground(c(p.Text)).Bold(true),
		StopButton: lipgloss.NewStyle().Background(c(p.Stop)).Foreground(c(p.Base)).Bold(true).Padding(0, 1),
		Button:     lipgloss.NewStyle().Background(c(p.Primary)).Foreground(c(p.Base)).Bold(true).Padding(0, 1),
		Box:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c(p.Border)).Padding(0, 1),
	}
}

func (t Theme) Robot(inst int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.P.RobotColor(inst))).Bold(true)
}

// HuhTheme styles the forms with the active palette.
func HuhTheme() huh.Theme {
	p := Current.P
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(true)
		c := lipgloss.Color
		for _, f := range []*huh.FieldStyles{&s.Focused, &s.Blurred} {
			f.Title = f.Title.Foreground(c(p.Muted))
			f.Description = f.Description.Foreground(c(p.Muted))
			f.Option = f.Option.Foreground(c(p.Text))
			f.SelectSelector = f.SelectSelector.Foreground(c(p.Accent))
			f.MultiSelectSelector = f.MultiSelectSelector.Foreground(c(p.Accent))
			f.SelectedOption = f.SelectedOption.Foreground(c(p.OK))
			f.SelectedPrefix = f.SelectedPrefix.Foreground(c(p.OK))
			f.UnselectedOption = f.UnselectedOption.Foreground(c(p.Text))
			f.ErrorIndicator = f.ErrorIndicator.Foreground(c(p.Stop))
			f.ErrorMessage = f.ErrorMessage.Foreground(c(p.Stop))
			f.TextInput.Prompt = f.TextInput.Prompt.Foreground(c(p.Accent))
			f.TextInput.Cursor = f.TextInput.Cursor.Foreground(c(p.Primary))
			f.TextInput.Placeholder = f.TextInput.Placeholder.Foreground(c(p.Border))
			f.TextInput.Text = f.TextInput.Text.Foreground(c(p.Text))
			f.FocusedButton = f.FocusedButton.Background(c(p.Primary)).Foreground(c(p.Base)).Bold(true)
			f.BlurredButton = f.BlurredButton.Background(c(p.Surface)).Foreground(c(p.Muted))
		}
		s.Focused.Base = s.Focused.Base.BorderForeground(c(p.Primary))
		s.Focused.Title = s.Focused.Title.Foreground(c(p.Primary)).Bold(true)
		s.Group.Title = s.Group.Title.Foreground(c(p.Accent)).Bold(true)
		s.Group.Description = s.Group.Description.Foreground(c(p.Muted))
		s.Help.ShortKey = s.Help.ShortKey.Foreground(c(p.Accent))
		s.Help.ShortDesc = s.Help.ShortDesc.Foreground(c(p.Muted))
		return s
	})
}

func paneBorderFormat(p Palette) string {
	return fmt.Sprintf(
		" #[fg=#{?@mullet_color,#{@mullet_color},%s},bold]#{?@mullet_label,#{@mullet_label},#{?@mullet_tui,mullet,#{pane_current_command}}}#[default]",
		p.Muted)
}

// TmuxTheme returns the tmux commands that give the session the palette (same tokens as the TUI).
func TmuxTheme(p Palette, navPosition string) [][]string {
	if navPosition != "top" {
		navPosition = "bottom"
	}
	return [][]string{
		{"set", "-g", "window-style", "bg=" + p.Base + ",fg=" + p.Text},
		{"set", "-g", "window-active-style", "bg=" + p.Base + ",fg=" + p.Text},
		{"set", "-g", "pane-border-style", "fg=" + p.Border + ",bg=" + p.Base},
		{"set", "-g", "pane-active-border-style", "fg=" + p.Primary + ",bg=" + p.Base},
		{"set", "-g", "pane-border-format", paneBorderFormat(p)},
		{"set", "-g", "status-style", "bg=" + p.Surface + ",fg=" + p.Text},
		{"set", "-g", "message-style", "bg=" + p.Border + ",fg=" + p.Text},
		{"set", "-g", "mode-style", "bg=" + p.Border + ",fg=" + p.Text},
		{"set", "-g", "status-position", navPosition},
		{"set", "-g", "status-left", fmt.Sprintf("#[fg=%s,bg=%s,bold] ✦ mullet #[default] ", p.Base, p.Primary)},
		{"set", "-g", "status-left-length", "40"},
		{"set", "-g", "status-right", fmt.Sprintf("#{?@mullet_domain,#[fg=%s]DOMAIN #[fg=%s#,bold]#{@mullet_domain}#[default]  ,}#[fg=%s]%%H:%%M #[fg=%s,bg=%s,bold]#[range=user|stop] ■ STOP #[norange]#[default]", p.Muted, p.Accent, p.Muted, p.Base, p.Stop)},
		{"set", "-g", "status-right-length", "60"},
		{"set", "-g", "window-status-current-format", fmt.Sprintf("#[fg=%s,bg=%s,bold] #I #W ", p.Base, p.Accent)},
		{"set", "-g", "window-status-format", fmt.Sprintf("#[fg=%s] #I #W ", p.Muted)},
	}
}
