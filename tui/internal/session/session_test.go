package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The Go writer and the Python parser (spawn_fleet.py) must agree on every field.
func TestSessionRoundTripThroughPythonParser(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	repo, _ := filepath.Abs("../../..")
	s := New("ghcr.io/skymule-ai/px4-sitl:v1.17.0")
	s.Name, s.DomainID, s.SpeedFactor = "roundtrip", 42, 2.5
	s.PX4Params = []string{"COM_ARM_WO_GPS=1"}
	s.RViz, s.RVizConfig, s.Bag, s.BagTopics = true, "/ws/x.rviz", true, []string{"/uav_1/fmu/out/vehicle_odometry", "/tf"}
	s.GPU, s.Windows = "off", []string{"px4", "core"}
	s.Commands = []Command{{Run: "ros2 launch pkg a.launch.py px4_instances:={instances} | tee /tmp/x", Autorun: true}}
	s.Fleet = append(s.Fleet, Robot{ID: "7", Instance: 2, Model: "gz_standard_vtol", Pose: [6]float64{3.5, -1, 0.1, 0, 0, 1.57}, Params: []string{"MPC_XY_VEL_MAX=5"}})

	path := filepath.Join(t.TempDir(), "roundtrip.yaml")
	if err := os.WriteFile(path, []byte(s.YAML()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, problems, err := Load(repo, path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if HasErrors(problems) {
		t.Fatalf("valid session rejected: %v", problems)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip mismatch\nwant %+v\n got %+v\nyaml:\n%s", s, got, s.YAML())
	}
}

func TestValidateUsesSpawnFleetRules(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	repo, _ := filepath.Abs("../../..")
	s := New("")
	s.Fleet = append(s.Fleet, Robot{ID: "b", Instance: 1, Model: "gz_x500", Params: []string{"MAV_SYS_ID=3"}})
	problems := strings.Join(s.Validate(repo), "\n")
	for _, want := range []string{"must be unique", "MAV_SYS_ID is derived"} {
		if !strings.Contains(problems, want) {
			t.Errorf("expected %q in problems:\n%s", want, problems)
		}
	}
	s.Commands = []Command{{Run: "echo # nope"}}
	if !strings.Contains(strings.Join(s.Validate(repo), "\n"), "must not contain '#'") {
		t.Error("a '#' in a command must be rejected before writing YAML")
	}
}
