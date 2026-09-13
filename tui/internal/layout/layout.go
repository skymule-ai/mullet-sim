// Package layout is the dashboard split tree: regions, presets, and geometry.
package layout

import (
	"math"
	"slices"
	"strings"
)

// RegionNames are the dashboard window regions: the TUI itself plus one per kind of simulation pane.
var RegionNames = []string{"tui", "px4", "core", "xrce", "code"}

// Node is a binary split tree; leaves name a region.
type Node struct {
	Split  string  `json:"split,omitempty"` // "h": A left of B, "v": A above B
	Ratio  float64 `json:"ratio,omitempty"` // A's share of the space
	A      *Node   `json:"a,omitempty"`
	B      *Node   `json:"b,omitempty"`
	Region string  `json:"region,omitempty"`
}

type Layout struct {
	Name string   `json:"name,omitempty"`
	Root *Node    `json:"root"`
	Off  []string `json:"off,omitempty"` // pane roles hidden from the dash; new panes show by default
	// Exact tmux geometry (your border drags); only valid for the same panes in the same order.
	TmuxLayout string   `json:"tmux_layout,omitempty"`
	PaneRoles  []string `json:"pane_roles,omitempty"`
}

var PresetNames = []string{"classic", "quad", "logs", "focus"}

func Leaf(r string) *Node { return &Node{Region: r} }

func Split(dir string, ratio float64, a, b *Node) *Node {
	return &Node{Split: dir, Ratio: ratio, A: a, B: b}
}

// Preset returns a named dashboard layout (classic, quad, logs, focus).
func Preset(name string) Layout {
	var root *Node
	switch name {
	case "quad":
		root = Split("v", .5, Split("h", .5, Leaf("tui"), Leaf("px4")), Split("h", .5, Leaf("core"), Leaf("code")))
	case "logs":
		root = Split("h", .28, Leaf("tui"), Split("v", .6, Leaf("px4"), Split("h", .5, Leaf("core"), Leaf("xrce"))))
	case "focus":
		root = Split("h", .25, Leaf("tui"), Leaf("px4"))
	default:
		name = "classic"
		root = Split("h", .35, Leaf("tui"), Split("v", .5, Leaf("px4"), Leaf("core")))
	}
	return Layout{Name: name, Root: root}
}

func (n *Node) Leaves() []string {
	if n == nil {
		return nil
	}
	if n.Region != "" {
		return []string{n.Region}
	}
	return append(n.A.Leaves(), n.B.Leaves()...)
}

func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	c.A, c.B = n.A.Clone(), n.B.Clone()
	return &c
}

// Clone returns a deep copy of the layout (tree, hidden roles, saved geometry).
func (l Layout) Clone() Layout {
	c := l
	c.Root = l.Root.Clone()
	c.Off = append([]string(nil), l.Off...)
	c.PaneRoles = append([]string(nil), l.PaneRoles...)
	return c
}

func (n *Node) Has(r string) bool {
	for _, l := range n.Leaves() {
		if l == r {
			return true
		}
	}
	return false
}

// Without returns a copy of the tree without region r; its sibling takes the parent's place.
func (n *Node) Without(r string) *Node {
	if n == nil || n.Region == r {
		return nil
	}
	if n.Region != "" {
		return n.Clone()
	}
	a, b := n.A.Without(r), n.B.Without(r)
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return Split(n.Split, n.Ratio, a, b)
}

func (n *Node) Replace(r string, with *Node) *Node {
	if n == nil {
		return nil
	}
	if n.Region == r {
		return with
	}
	if n.Region != "" {
		return n.Clone()
	}
	return Split(n.Split, n.Ratio, n.A.Replace(r, with), n.B.Replace(r, with))
}

// Keep returns the tree pruned to regions in present (the TUI is always present).
func (n *Node) Keep(present map[string]bool) *Node {
	out := n.Clone()
	for _, r := range n.Leaves() {
		if r != "tui" && !present[r] {
			out = out.Without(r)
		}
	}
	return out
}

// ParentOf returns the split that contains region r and whether r is in A.
func (n *Node) ParentOf(r string) (*Node, bool) {
	if n == nil || n.Region != "" {
		return nil, false
	}
	if n.A.Region == r {
		return n, true
	}
	if n.B.Region == r {
		return n, false
	}
	if p, inA := n.A.ParentOf(r); p != nil {
		return p, inA
	}
	return n.B.ParentOf(r)
}

// Changed drops the saved exact geometry: it belongs to the previous tree.
func (l *Layout) Changed() { l.TmuxLayout, l.PaneRoles = "", nil }

// SnapEdge moves (or adds) region r to an edge of the whole window.
func (l *Layout) SnapEdge(r, edge string) {
	rest := l.Root.Without(r)
	switch {
	case rest == nil:
		l.Root = Leaf(r)
	case edge == "left":
		l.Root = Split("h", .35, Leaf(r), rest)
	case edge == "right":
		l.Root = Split("h", .65, rest, Leaf(r))
	case edge == "top":
		l.Root = Split("v", .4, Leaf(r), rest)
	default:
		l.Root = Split("v", .6, rest, Leaf(r))
	}
	l.Changed()
}

// SnapTo drops region r onto target: an edge zone splits the target, center swaps them.
func (l *Layout) SnapTo(r, target, zone string) {
	if r == target || !l.Root.Has(target) {
		return
	}
	if zone == "center" {
		if !l.Root.Has(r) {
			l.Root = l.Root.Replace(target, Leaf(r))
		} else {
			l.Root = l.Root.Replace(r, Leaf("\x00")).Replace(target, Leaf(r)).Replace("\x00", Leaf(target))
		}
		l.Changed()
		return
	}
	root := l.Root.Without(r)
	var s *Node
	switch zone {
	case "left":
		s = Split("h", .5, Leaf(r), Leaf(target))
	case "right":
		s = Split("h", .5, Leaf(target), Leaf(r))
	case "top":
		s = Split("v", .5, Leaf(r), Leaf(target))
	default:
		s = Split("v", .5, Leaf(target), Leaf(r))
	}
	l.Root = root.Replace(target, s)
	l.Changed()
}

// Hide removes a region from the dashboard (its panes stay in their own tmux window).
func (l *Layout) Hide(r string) {
	if r == "tui" {
		return
	}
	if root := l.Root.Without(r); root != nil {
		l.Root = root
		l.Changed()
	}
}

// Shown reports whether a pane role is mirrored on the dash.
func (l *Layout) Shown(role string) bool {
	k, _, _ := strings.Cut(role, ":")
	return l.Root.Has(k) && !slices.Contains(l.Off, role)
}

// SetShown shows or hides roles of region r on the dash; all lists every role of r.
// Showing a pane of a region that isn't on the dash adds the region with only that pane.
// Hiding everything keeps the region in the tree, so it comes back in the same place.
func (l *Layout) SetShown(r string, all, roles []string, on bool) {
	if on && !l.Root.Has(r) {
		l.SnapEdge(r, "right")
		l.Off = append(l.Off, all...)
	}
	l.Off = slices.DeleteFunc(l.Off, func(s string) bool { return slices.Contains(roles, s) })
	if !on {
		l.Off = append(l.Off, roles...)
	}
	slices.Sort(l.Off)
	l.Off = slices.Compact(l.Off)
}

// --- geometry (layout editor) ---

type Rect struct{ X, Y, W, H int }

func (r Rect) Contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

func (n *Node) Share(total int) int { return int(math.Round(float64(total) * n.Ratio)) }

func (n *Node) Rects(r Rect, out map[string]Rect) {
	switch {
	case n == nil:
	case n.Region != "":
		out[n.Region] = r
	case n.Split == "h":
		wa := n.Share(r.W)
		n.A.Rects(Rect{r.X, r.Y, wa, r.H}, out)
		n.B.Rects(Rect{r.X + wa, r.Y, r.W - wa, r.H}, out)
	default:
		ha := n.Share(r.H)
		n.A.Rects(Rect{r.X, r.Y, r.W, ha}, out)
		n.B.Rects(Rect{r.X, r.Y + ha, r.W, r.H - ha}, out)
	}
}

// Divider is the boundary between the two halves of a split, for mouse resizing.
type Divider struct {
	Node   *Node
	Parent Rect
}

// At reports whether (x, y) is on the divider line (the first column/row of B).
func (d Divider) At(x, y int) bool {
	if !d.Parent.Contains(x, y) {
		return false
	}
	if d.Node.Split == "h" {
		pos := d.Parent.X + d.Node.Share(d.Parent.W)
		return x == pos || x == pos-1
	}
	pos := d.Parent.Y + d.Node.Share(d.Parent.H)
	return y == pos || y == pos-1
}

// Drag sets the split ratio from the mouse position, snapped to 5% steps.
func (d Divider) Drag(x, y int) {
	var f float64
	if d.Node.Split == "h" {
		f = float64(x-d.Parent.X) / float64(max(d.Parent.W, 1))
	} else {
		f = float64(y-d.Parent.Y) / float64(max(d.Parent.H, 1))
	}
	d.Node.Ratio = math.Min(.85, math.Max(.15, math.Round(f*20)/20))
}

func (n *Node) Dividers(r Rect, out *[]Divider) {
	if n == nil || n.Region != "" {
		return
	}
	*out = append(*out, Divider{n, r})
	if n.Split == "h" {
		wa := n.Share(r.W)
		n.A.Dividers(Rect{r.X, r.Y, wa, r.H}, out)
		n.B.Dividers(Rect{r.X + wa, r.Y, r.W - wa, r.H}, out)
	} else {
		ha := n.Share(r.H)
		n.A.Dividers(Rect{r.X, r.Y, r.W, ha}, out)
		n.B.Dividers(Rect{r.X, r.Y + ha, r.W, r.H - ha}, out)
	}
}

// ZoneAt picks the drop zone of a target rect: its middle swaps, otherwise the nearest edge.
func ZoneAt(r Rect, x, y int) string {
	fx := (float64(x-r.X) + .5) / float64(max(r.W, 1))
	fy := (float64(y-r.Y) + .5) / float64(max(r.H, 1))
	if fx > .3 && fx < .7 && fy > .3 && fy < .7 {
		return "center"
	}
	zone, best := "left", fx
	for _, c := range []struct {
		z string
		d float64
	}{{"right", 1 - fx}, {"top", fy}, {"bottom", 1 - fy}} {
		if c.d < best {
			zone, best = c.z, c.d
		}
	}
	return zone
}
