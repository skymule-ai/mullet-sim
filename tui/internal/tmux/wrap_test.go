package tmux

import (
	"strings"
	"testing"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
)

func TestPackWidthTwoLines(t *testing.T) {
	parts := []string{"PX4 1 2", "CORE 1 2", "XRCE ●", "CODE ✚"}
	got := PackWidth(parts, "   ", 20, 2, func(s string) int { return len(s) })
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d %q", len(got), got)
	}
	if got[0] != "PX4 1 2   CORE 1 2" {
		t.Fatalf("line 0: %q", got[0])
	}
	if got[1] != "XRCE ●   CODE ✚" {
		t.Fatalf("line 1: %q", got[1])
	}
}

func TestNavRegionXRCEHasColon(t *testing.T) {
	l := layout.Layout{Root: layout.Leaf("xrce")}
	s := NavRegion(&l, theme.Palettes[0], "xrce", []Pane{{Role: "xrce:0"}}, false)
	if !strings.Contains(s, "range=user|xrce:0") {
		t.Fatalf("expected clickable xrce:0 range, got %s", s)
	}
}

func TestPackWidthFitsOneLine(t *testing.T) {
	got := PackWidth([]string{"a", "b"}, " ", 10, 2, func(s string) int { return len(s) })
	if len(got) != 1 || got[0] != "a b" {
		t.Fatalf("got %q", got)
	}
}

func TestTmuxWidthStripsStyles(t *testing.T) {
	s := "#[fg=#fff,bold]PX4#[nobold] 1"
	if TmuxPlain(s) != "PX4 1" {
		t.Fatalf("plain: %q", TmuxPlain(s))
	}
	if TmuxWidth(s) != 5 {
		t.Fatalf("width %d", TmuxWidth(s))
	}
}
