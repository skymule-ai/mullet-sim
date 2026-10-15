// Package host is machine status, Docker image probing, and XDG settings/layouts.
package host

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const ContainerName = "mullet_px4_sitl"

type Airframe struct {
	Autostart int    `json:"autostart"`
	Model     string `json:"model"`
}

// ImageInfo describes a local PX4 SITL image; probed once per image ID and cached.
type ImageInfo struct {
	Ref       string     `json:"ref"`
	ID        string     `json:"id"`
	HasPX4    bool       `json:"has_px4"`
	Airframes []Airframe `json:"airframes"`
	Worlds    []string   `json:"worlds"`
	Gz        string     `json:"gz"`
	RMWs      []string   `json:"rmws"`
	RViz      bool       `json:"rviz"`
	Foxglove  bool       `json:"foxglove"`
}

func (i ImageInfo) Version() string {
	if k := strings.LastIndex(i.Ref, ":"); k >= 0 {
		return i.Ref[k+1:]
	}
	return i.Ref
}

func (i ImageInfo) Models() []string {
	var out []string
	for _, a := range i.Airframes {
		out = append(out, a.Model)
	}
	return out
}

func (i ImageInfo) HasModel(model string) bool {
	for _, a := range i.Airframes {
		if a.Model == model {
			return true
		}
	}
	return false
}

func isRepo(dir string) bool {
	for _, f := range []string{"run.sh", "docker-compose.yml", "scripts/spawn_fleet.py"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	return true
}

// FindRepo locates the mullet-sim repo: $MULLET_REPO, the binary's directory
// (build.sh puts ./mullet at the repo root), then the current directory and its parents.
func FindRepo() (string, error) {
	if d := os.Getenv("MULLET_REPO"); d != "" && isRepo(d) {
		return filepath.Abs(d)
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil && isRepo(filepath.Dir(exe)) {
			return filepath.Dir(exe), nil
		}
	}
	dir, _ := os.Getwd()
	for {
		if isRepo(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("mullet-sim repo not found: run from inside it or set MULLET_REPO")
		}
		dir = parent
	}
}

func Output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// listImages returns local images whose repository contains match, newest first.
func ListImages(match string) ([]ImageInfo, error) {
	out, err := Output("docker", "images", "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}")
	if err != nil {
		return nil, err
	}
	var images []ImageInfo
	for _, line := range strings.Split(out, "\n") {
		ref, id, ok := strings.Cut(line, "\t")
		if !ok || strings.Contains(ref, "<none>") || !strings.Contains(ref[:strings.LastIndex(ref, ":")], match) {
			continue
		}
		images = append(images, ImageInfo{Ref: ref, ID: id})
	}
	return images, nil
}

const probeScript = `
echo @px4; test -x /opt/PX4-Autopilot/build/px4_sitl_default/bin/px4 && echo yes
echo @airframes; ls /opt/PX4-Autopilot/ROMFS/px4fmu_common/init.d-posix/airframes 2>/dev/null
echo @worlds; ls /opt/PX4-Autopilot/Tools/simulation/gz/worlds 2>/dev/null
echo @gz; gz sim --versions 2>/dev/null
echo @ros; ls -d /opt/ros/*/share/rmw_*_cpp /opt/ros/*/share/rviz2 /opt/ros/*/share/foxglove_bridge 2>/dev/null
true`

var airframeRe = regexp.MustCompile(`^(\d+)_(gz_\w+)$`)

// probeImage inspects an image (~1-2 s) and caches the result by image ID.
func ProbeImage(img ImageInfo) ImageInfo {
	cache := filepath.Join(cacheDir(), "images", img.ID+".json")
	var cached ImageInfo
	if ReadJSON(cache, &cached) == nil {
		cached.Ref = img.Ref
		return cached
	}
	out, err := Output("docker", "run", "--rm", "--entrypoint", "bash", img.Ref, "-c", probeScript)
	if err != nil {
		return img
	}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "@") {
			section = line[1:]
			continue
		}
		switch section {
		case "px4":
			img.HasPX4 = line == "yes"
		case "airframes":
			if m := airframeRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				img.Airframes = append(img.Airframes, Airframe{n, m[2]})
			}
		case "worlds":
			if w, ok := strings.CutSuffix(line, ".sdf"); ok {
				img.Worlds = append(img.Worlds, w)
			}
		case "gz":
			if line != "" {
				img.Gz = line
			}
		case "ros":
			switch base := filepath.Base(line); {
			case strings.HasPrefix(base, "rmw_"):
				img.RMWs = append(img.RMWs, base)
			case base == "rviz2":
				img.RViz = true
			case base == "foxglove_bridge":
				img.Foxglove = true
			}
		}
	}
	sort.Slice(img.Airframes, func(a, b int) bool { return img.Airframes[a].Model < img.Airframes[b].Model })
	_ = WriteJSON(cache, img)
	return img
}

// launchFiles lists `ros2 launch <pkg> <file>` commands found in ws/src.
func LaunchFiles(repo string) []string {
	var cmds []string
	for _, pattern := range []string{"ws/src/*/launch/*.launch.*", "ws/src/*/*/launch/*.launch.*"} {
		matches, _ := filepath.Glob(filepath.Join(repo, pattern))
		for _, m := range matches {
			pkgDir := filepath.Dir(filepath.Dir(m))
			xml, err := os.ReadFile(filepath.Join(pkgDir, "package.xml"))
			if err != nil {
				continue
			}
			name := regexp.MustCompile(`<name>\s*([^<\s]+)\s*</name>`).FindSubmatch(xml)
			if name != nil {
				cmds = append(cmds, "ros2 launch "+string(name[1])+" "+filepath.Base(m))
			}
		}
	}
	sort.Strings(cmds)
	return cmds
}

// HostStatus is what the home screen's status strip shows.
type HostStatus struct {
	Docker  bool
	Tmux    string
	GPU     bool
	Running bool
}

func Status() HostStatus {
	_, dockerErr := Output("docker", "info", "--format", "{{.ServerVersion}}")
	tmuxV, _ := Output("tmux", "-V")
	_, gpuErr := Output("nvidia-smi", "-L")
	return HostStatus{Docker: dockerErr == nil, Tmux: strings.TrimPrefix(tmuxV, "tmux "), GPU: gpuErr == nil, Running: SimRunning()}
}

func SimRunning() bool {
	out, _ := Output("docker", "inspect", "-f", "{{.State.Running}}", ContainerName)
	return out == "true"
}

func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes atomically so concurrent `mullet ctl` calls never read half a file.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
