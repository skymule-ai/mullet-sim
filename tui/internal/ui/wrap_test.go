package ui

import "testing"

func TestOptionIndexAt(t *testing.T) {
	lines := []string{"Theme", "> Dusk", "  HUD", "  Mocha"}
	if optionIndexAt(lines, 1) != 0 {
		t.Fatalf("dusk: %d", optionIndexAt(lines, 1))
	}
	if optionIndexAt(lines, 3) != 2 {
		t.Fatalf("mocha: %d", optionIndexAt(lines, 3))
	}
	if optionIndexAt(lines, 0) != -1 {
		t.Fatalf("title should not count")
	}
}

func TestShineBandSweeps(t *testing.T) {
	if shineBand(0, 0, 20, 20) != 0 {
		t.Fatal("idle frames should be dark")
	}
	if shineBand(7, 0, 5, 20) != 2 {
		t.Fatalf("slash should hit x=7 at frame 5, got %d", shineBand(7, 0, 5, 20))
	}
}
