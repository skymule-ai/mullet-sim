package tmux

import "charm.land/lipgloss/v2"

// packWidth joins parts with sep into at most maxLines lines, each at most width
// visible cells. vis measures a string; nil means lipgloss.Width.
func PackWidth(parts []string, sep string, width, maxLines int, vis func(string) int) []string {
	if len(parts) == 0 || maxLines < 1 {
		return nil
	}
	if vis == nil {
		vis = lipgloss.Width
	}
	sepW := vis(sep)
	var lines []string
	var cur string
	flush := func() {
		if cur == "" {
			return
		}
		lines = append(lines, cur)
		cur = ""
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		need := vis(p)
		if cur != "" {
			need += sepW
		}
		if cur != "" && vis(cur)+need > width && len(lines) < maxLines-1 {
			flush()
			need = vis(p)
		}
		if cur == "" {
			cur = p
		} else {
			cur += sep + p
		}
	}
	flush()
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}
