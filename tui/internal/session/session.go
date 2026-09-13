// Package session loads and writes fleet YAML through spawn_fleet.py.
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Robot is one fleet entry. Autostart 0 means "derive from the model" (done in the container).
type Robot struct {
	ID        string
	Instance  int
	Model     string
	Autostart int
	Pose      [6]float64
	Params    []string
}

// Command is one pane of the "code" window.
type Command struct {
	Run     string
	Autorun bool
}

// Session mirrors a fleet/session YAML (see config/sessions/example_uav.yaml).
type Session struct {
	Name string // file name without extension, under config/sessions/

	World         string
	Headless      bool
	DomainID      int
	NSPrefix      string
	SpeedFactor   float64
	PX4Params     []string
	XRCETransport string
	XRCEPort      int

	Image       string
	RMW         string
	RViz        bool
	RVizConfig  string
	QGC         bool
	Foxglove    bool
	Bag         bool
	BagTopics   []string
	Build       bool
	GPU         string
	ResetParams bool
	Windows     []string

	Commands []Command
	Fleet    []Robot
}

var AllWindows = []string{"px4", "xrce", "core", "code"}

// New returns a session with default simulation values and one x500 robot.
func New(image string) *Session {
	return &Session{
		Name: "session", World: "default", NSPrefix: "uav", SpeedFactor: 1,
		XRCETransport: "udp4", XRCEPort: 8888, Image: image, BagTopics: []string{"all"},
		Build: true, GPU: "auto", Windows: append([]string(nil), AllWindows...),
		Fleet: []Robot{{ID: "uav_1", Instance: 1, Model: "gz_x500", Pose: [6]float64{0, 0, 0.2}}},
	}
}

// Path is the session file path relative to the repo root.
func (s *Session) Path() string { return filepath.Join("config", "sessions", s.Name+".yaml") }

// Namespace is the ROS namespace of a robot (instance 0 has none).
func (s *Session) Namespace(inst int) string {
	if inst == 0 {
		return "/"
	}
	prefix := s.NSPrefix
	if prefix == "" {
		prefix = "px4"
	}
	return fmt.Sprintf("/%s_%d", prefix, inst)
}

// NextInstance is the lowest free instance >= 1.
func (s *Session) NextInstance() int {
	used := map[int]bool{}
	for _, r := range s.Fleet {
		used[r.Instance] = true
	}
	for i := 1; ; i++ {
		if !used[i] {
			return i
		}
	}
}

// --- load: reuse the Python parser so both paths read files identically ---

// spawnFleet runs scripts/spawn_fleet.py with args and returns stdout and its problem lines.
func spawnFleet(repo string, args ...string) (string, []string, error) {
	cmd := exec.Command("python3", append([]string{filepath.Join(repo, "scripts", "spawn_fleet.py")}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var problems []string
	for _, line := range strings.Split(strings.TrimSpace(errb.String()), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			problems = append(problems, line)
		}
	}
	return out.String(), problems, err
}

// Load reads a session YAML via spawn_fleet.py --print-json.
// Load parses a fleet YAML through spawn_fleet.py --print-json.
func Load(repo, path string) (*Session, []string, error) {
	out, problems, err := spawnFleet(repo, "--config", path, "--print-json")
	if out == "" {
		if err == nil {
			err = fmt.Errorf("empty output")
		}
		return nil, problems, fmt.Errorf("%s: %v %s", path, err, strings.Join(problems, "; "))
	}
	var raw struct {
		Simulation map[string]any   `json:"simulation"`
		Session    map[string]any   `json:"session"`
		Commands   []map[string]any `json:"commands"`
		Fleet      []map[string]any `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, problems, err
	}
	sim, ses := raw.Simulation, raw.Session
	xrce, _ := sim["xrce_agent"].(map[string]any)
	s := &Session{
		Name:          strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		World:         str(sim, "world", "default"),
		Headless:      boolean(sim, "headless", false),
		DomainID:      int(num(sim, "ros_domain_id", 0)),
		NSPrefix:      str(sim, "namespace_prefix", ""),
		SpeedFactor:   num(sim, "speed_factor", 1),
		PX4Params:     strs(sim, "px4_params"),
		XRCETransport: str(xrce, "transport", "udp4"),
		XRCEPort:      int(num(xrce, "port", 8888)),
		Image:         str(ses, "image", ""),
		RMW:           str(ses, "rmw", ""),
		RViz:          boolean(ses, "rviz", false),
		RVizConfig:    str(ses, "rviz_config", ""),
		QGC:           boolean(ses, "qgc", false),
		Foxglove:      boolean(ses, "foxglove", false),
		Bag:           boolean(ses, "bag", false),
		BagTopics:     strs(ses, "bag_topics"),
		Build:         boolean(ses, "build", true),
		GPU:           str(ses, "gpu", "auto"),
		ResetParams:   boolean(ses, "reset_params", false),
		Windows:       strs(ses, "windows"),
	}
	if len(s.BagTopics) == 0 {
		s.BagTopics = []string{"all"}
	}
	if len(s.Windows) == 0 {
		s.Windows = append([]string(nil), AllWindows...)
	}
	for _, c := range raw.Commands {
		s.Commands = append(s.Commands, Command{Run: str(c, "run", ""), Autorun: boolean(c, "autorun", false)})
	}
	for _, v := range raw.Fleet {
		r := Robot{
			ID:        str(v, "id", ""),
			Instance:  int(num(v, "instance", 0)),
			Model:     str(v, "model", str(v, "px4_sim_model", "")),
			Autostart: int(num(v, "px4_sys_autostart", 0)),
			Params:    strs(v, "params"),
		}
		for i, p := range list(v["pose"]) {
			if f, ok := p.(float64); ok && i < 6 {
				r.Pose[i] = f
			}
		}
		s.Fleet = append(s.Fleet, r)
	}
	return s, problems, nil
}

func str(m map[string]any, k, def string) string {
	if v, ok := m[k]; ok && v != nil {
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return fmt.Sprint(v)
	}
	return def
}

func num(m map[string]any, k string, def float64) float64 {
	if f, ok := m[k].(float64); ok {
		return f
	}
	return def
}

func boolean(m map[string]any, k string, def bool) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
	}
	return def
}

func list(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	default:
		return []any{t}
	}
}

func strs(m map[string]any, k string) []string {
	var out []string
	for _, v := range list(m[k]) {
		if s := str(map[string]any{"v": v}, "v", ""); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// --- save: write the YAML subset spawn_fleet.py parses (no anchors, no '#', no multi-line) ---

// scalar writes a string so the parser reads it back as the same string.
func scalar(s string) string {
	if s == "" {
		return `""`
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil || s == "true" || s == "false" {
		return `"` + s + `"`
	}
	return s
}

func inlineList(items []string) string {
	return "[" + strings.Join(items, ", ") + "]"
}

func fnum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func (s *Session) YAML() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Written by the mullet TUI. Hand edits are fine: keep to the simple YAML subset")
	w("# (no anchors, no multi-line values, no hash signs inside values).")
	w("# Without the TUI: ./run.sh --config %s", s.Path())
	w("simulation:")
	w("  world: %s", scalar(s.World))
	w("  headless: %t", s.Headless)
	w("  ros_domain_id: %d", s.DomainID)
	if s.NSPrefix != "" {
		w("  namespace_prefix: %s", s.NSPrefix)
	}
	w("  speed_factor: %s", fnum(s.SpeedFactor))
	if len(s.PX4Params) > 0 {
		w("  px4_params: %s", inlineList(s.PX4Params))
	}
	w("  xrce_agent:")
	w("    transport: %s", scalar(s.XRCETransport))
	w("    port: %d", s.XRCEPort)
	w("")
	w("session:")
	if s.Image != "" {
		w("  image: %s", s.Image)
	}
	if s.RMW != "" {
		w("  rmw: %s", s.RMW)
	}
	w("  rviz: %t", s.RViz)
	w("  rviz_config: %s", scalar(s.RVizConfig))
	w("  qgc: %t", s.QGC)
	w("  foxglove: %t", s.Foxglove)
	w("  bag: %t", s.Bag)
	w("  bag_topics: %s", inlineList(s.BagTopics))
	w("  build: %t", s.Build)
	w("  gpu: %s", s.GPU)
	w("  reset_params: %t", s.ResetParams)
	w("  windows: %s", inlineList(s.Windows))
	if len(s.Commands) > 0 {
		w("")
		w("commands:")
		for _, c := range s.Commands {
			w("  - run: %s", c.Run)
			w("    autorun: %t", c.Autorun)
		}
	}
	w("")
	w("fleet:")
	for _, r := range s.Fleet {
		w("  - id: %s", scalar(r.ID))
		w("    instance: %d", r.Instance)
		w("    model: %s", r.Model)
		if r.Autostart != 0 {
			w("    px4_sys_autostart: %d", r.Autostart)
		}
		pose := make([]string, 6)
		for i, p := range r.Pose {
			pose[i] = strconv.FormatFloat(p, 'f', 2, 64)
		}
		w("    pose: %s", inlineList(pose))
		if len(r.Params) > 0 {
			w("    params: %s", inlineList(r.Params))
		}
	}
	return b.String()
}

// textProblems catches what the YAML subset cannot represent.
func (s *Session) textProblems() []string {
	var p []string
	check := func(what, v string, inList bool) {
		if strings.Contains(v, "#") {
			p = append(p, fmt.Sprintf("Config error: %s must not contain '#'", what))
		}
		if strings.ContainsAny(v, "\n\r") {
			p = append(p, fmt.Sprintf("Config error: %s must be a single line", what))
		}
		if inList && strings.ContainsAny(v, ",[]") {
			p = append(p, fmt.Sprintf("Config error: %s must not contain ',' '[' or ']'", what))
		}
	}
	if s.Name == "" || strings.ContainsAny(s.Name, "/ \\") {
		p = append(p, "Config error: session name must be non-empty, without spaces or slashes")
	}
	check("world", s.World, false)
	check("rviz_config", s.RVizConfig, false)
	for _, v := range append(append(append([]string{}, s.PX4Params...), s.BagTopics...), s.Windows...) {
		check("list item "+strconv.Quote(v), v, true)
	}
	for i, c := range s.Commands {
		check(fmt.Sprintf("command %d", i+1), c.Run, false)
	}
	for _, r := range s.Fleet {
		check("robot id "+strconv.Quote(r.ID), r.ID, false)
		for _, v := range r.Params {
			check("param "+strconv.Quote(v), v, true)
		}
	}
	return p
}

// Validate runs the same checks as run.sh (spawn_fleet.py) on the unsaved session.
func (s *Session) Validate(repo string) []string {
	if p := s.textProblems(); len(p) > 0 {
		return p
	}
	f, err := os.CreateTemp("", "mullet-*.yaml")
	if err != nil {
		return []string{err.Error()}
	}
	defer os.Remove(f.Name())
	f.WriteString(s.YAML())
	f.Close()
	_, problems, _ := spawnFleet(repo, "--config", f.Name(), "--print-json")
	return problems
}

func (s *Session) Save(repo string) error {
	path := filepath.Join(repo, s.Path())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s.YAML()), 0o644)
}

// hasErrors reports whether any problem line is an error (not a warning).
func HasErrors(problems []string) bool {
	for _, p := range problems {
		if !strings.Contains(p, "warning") {
			return true
		}
	}
	return false
}
