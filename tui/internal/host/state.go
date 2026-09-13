package host

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/layout"
)

// Settings persist across TUI sessions in ~/.config/mullet-sitl/settings.json.
type Settings struct {
	Theme       string `json:"theme"`        // dusk | hud | mocha
	Icons       string `json:"icons"`        // unicode | ascii
	NavPosition string `json:"nav_position"` // bottom | top
	ConfirmStop bool   `json:"confirm_stop"`
	ImageMatch  string `json:"image_match"` // substring of image repositories to list
	LastSession string `json:"last_session"`
}

func configDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "mullet-sitl")
}

func cacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "mullet-sitl")
}

func LoadSettings() Settings {
	s := Settings{Theme: "dusk", Icons: "unicode", NavPosition: "bottom", ConfirmStop: true, ImageMatch: "px4"}
	_ = ReadJSON(filepath.Join(configDir(), "settings.json"), &s)
	return s
}

func (s Settings) Save() error { return WriteJSON(filepath.Join(configDir(), "settings.json"), s) }

func layoutsDir() string { return filepath.Join(configDir(), "layouts") }

// LoadLayout returns the current layout (last.json), or the Classic preset.
func LoadLayout() layout.Layout {
	var l layout.Layout
	if ReadJSON(filepath.Join(layoutsDir(), "last.json"), &l) != nil || l.Root == nil {
		l = layout.Preset("classic")
	}
	return l
}

func SaveLayout(l layout.Layout) error { return WriteJSON(filepath.Join(layoutsDir(), "last.json"), l) }

func LayoutMtime() int64 {
	st, err := os.Stat(filepath.Join(layoutsDir(), "last.json"))
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}

func NamedLayouts() []string {
	matches, _ := filepath.Glob(filepath.Join(layoutsDir(), "*.json"))
	var names []string
	for _, m := range matches {
		if n := strings.TrimSuffix(filepath.Base(m), ".json"); n != "last" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func SaveNamedLayout(name string, l layout.Layout) error {
	l.Name = name
	return WriteJSON(filepath.Join(layoutsDir(), name+".json"), l)
}

func LoadNamedLayout(name string) (layout.Layout, error) {
	var l layout.Layout
	err := ReadJSON(filepath.Join(layoutsDir(), name+".json"), &l)
	return l, err
}
