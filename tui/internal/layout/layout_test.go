package layout

import (
	"reflect"
	"sort"
	"testing"
)

func sortedLeaves(n *Node) []string {
	l := n.Leaves()
	sort.Strings(l)
	return l
}

// Every snap keeps the same set of regions and a tree whose rects tile the whole window.
func TestSnapMutationsKeepTreeValid(t *testing.T) {
	win := Rect{0, 0, 120, 40}
	for _, name := range PresetNames {
		base := Preset(name)
		want := sortedLeaves(base.Root)
		for _, r := range want {
			for _, edge := range []string{"left", "right", "top", "bottom"} {
				l := base.Clone()
				l.SnapEdge(r, edge)
				checkTree(t, l.Root, want, win)
				for _, target := range want {
					for _, zone := range []string{"left", "right", "top", "bottom", "center"} {
						m := l.Clone()
						m.SnapTo(r, target, zone)
						checkTree(t, m.Root, want, win)
					}
				}
			}
		}
	}
}

func checkTree(t *testing.T, root *Node, want []string, win Rect) {
	t.Helper()
	if got := sortedLeaves(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("regions changed: got %v want %v", got, want)
	}
	rects := map[string]Rect{}
	root.Rects(win, rects)
	area := 0
	for _, r := range rects {
		area += r.W * r.H
	}
	if area != win.W*win.H {
		t.Fatalf("rects cover %d cells, window has %d", area, win.W*win.H)
	}
}

func TestSnapEdgeAndHide(t *testing.T) {
	l := Preset("classic") // tui | (px4 / core)
	l.SnapEdge("core", "left")
	if l.Root.Split != "h" || l.Root.A.Region != "core" {
		t.Fatalf("core should be the left half: %+v", l.Root)
	}
	l.Hide("tui")
	if !l.Root.Has("tui") {
		t.Fatal("the TUI region can't be hidden")
	}
	l.Hide("px4")
	if l.Root.Has("px4") || !l.Root.Has("core") {
		t.Fatal("hide should remove only px4")
	}
	l.SnapEdge("xrce", "bottom") // adding a hidden region
	if !l.Root.Has("xrce") {
		t.Fatal("xrce should be added")
	}
}

func TestSetShown(t *testing.T) {
	l := Preset("classic") // tui | (px4 / core)
	px4 := []string{"px4:1", "px4:2"}
	if !l.Shown("px4:1") || l.Shown("code:0") {
		t.Fatal("panes of regions on the dash show by default, others don't")
	}
	l.SetShown("px4", px4, []string{"px4:1"}, false)
	if l.Shown("px4:1") || !l.Shown("px4:2") {
		t.Fatalf("toggle px4:1 off: off=%v", l.Off)
	}
	l.SetShown("px4", px4, px4, false)
	if l.Shown("px4:2") || !l.Root.Has("px4") {
		t.Fatalf("deselect all hides every pane but keeps the region placed: off=%v", l.Off)
	}
	l.SetShown("px4", px4, px4, true)
	if !l.Shown("px4:1") || !l.Shown("px4:2") || len(l.Off) != 0 {
		t.Fatalf("select all: off=%v", l.Off)
	}
	code := []string{"code:0", "code:1"}
	l.SetShown("code", code, []string{"code:1"}, true)
	if !l.Root.Has("code") || l.Shown("code:0") || !l.Shown("code:1") {
		t.Fatalf("showing one pane of a hidden region adds only that pane: off=%v", l.Off)
	}
}

func TestZoneAt(t *testing.T) {
	r := Rect{10, 10, 20, 10}
	for _, c := range []struct {
		x, y int
		want string
	}{{20, 15, "center"}, {10, 15, "left"}, {29, 15, "right"}, {20, 10, "top"}, {20, 19, "bottom"}} {
		if got := ZoneAt(r, c.x, c.y); got != c.want {
			t.Errorf("ZoneAt(%d,%d) = %s, want %s", c.x, c.y, got, c.want)
		}
	}
}

func TestKeepPrunesMissingRegions(t *testing.T) {
	l := Preset("quad")
	got := sortedLeaves(l.Root.Keep(map[string]bool{"px4": true}))
	if !reflect.DeepEqual(got, []string{"px4", "tui"}) {
		t.Fatalf("got %v", got)
	}
}
