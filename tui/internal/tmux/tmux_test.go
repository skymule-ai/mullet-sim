package tmux

import (
	"strings"
	"testing"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
)

func TestNextCodeNum(t *testing.T) {
	if got := NextCodeNum(nil); got != 0 {
		t.Fatalf("empty: got %d want 0", got)
	}
	if got := NextCodeNum([]Pane{{Role: "px4:1"}, {Role: "code:0"}, {Role: "code:2"}}); got != 3 {
		t.Fatalf("code:0,2: got %d want 3", got)
	}
}

func TestNavRegion(t *testing.T) {
	p := theme.Palettes[0]
	l := &layout.Layout{Root: layout.Split("h", .5, layout.Leaf("tui"), layout.Leaf("px4")), Off: []string{"px4:2"}}
	s := NavRegion(l, p, "px4", []Pane{{Role: "px4:1"}, {Role: "px4:2"}}, false)
	for _, want := range []string{"menu:px4", "px4:1", "px4:2", "view:px4"} {
		if !strings.Contains(s, "range=user|"+want+"]") {
			t.Errorf("px4: missing %s in %s", want, s)
		}
	}
	if !strings.Contains(s, "range=user|px4:1]#[fg="+p.Base+",bg=") || strings.Contains(s, "range=user|px4:2]#[fg="+p.Base) {
		t.Errorf("only px4:1 should be highlighted: %s", s)
	}
	if !strings.Contains(s, "⊞") || !strings.Contains(NavRegion(l, p, "px4", nil, true), "▢") {
		t.Errorf("view icon should follow the window zoom")
	}

	c := NavRegion(&layout.Layout{Root: layout.Leaf("tui")}, p, "code", []Pane{{Role: "code:0"}}, false)
	for _, want := range []string{"menu:code", "split:h", "split:v", "close:code"} {
		if !strings.Contains(c, "range=user|"+want) {
			t.Errorf("code: missing %s in %s", want, c)
		}
	}
	if e := NavRegion(l, p, "code", nil, false); !strings.Contains(e, "range=user|show:code") || strings.Contains(e, "split:") {
		t.Errorf("no code panes: want only show:code, got %s", e)
	}
	if x := NavRegion(l, p, "xrce", []Pane{{Role: "xrce:0"}}, false); strings.Contains(x, "menu:") {
		t.Errorf("xrce has no dropdown: %s", x)
	}
}

func TestMirrorFrame(t *testing.T) {
	got := MirrorFrame("a\nb\nc\n\n  \n", 2)
	want := "\x1b[Hb\x1b[0m\x1b[K\r\nc\x1b[0m\x1b[K\x1b[J"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := MirrorFrame("\n\n", 5); got != "\x1b[H\x1b[0m\x1b[K\x1b[J" {
		t.Fatalf("blank screen: got %q", got)
	}
}
