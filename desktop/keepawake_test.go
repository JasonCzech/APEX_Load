package main

import "testing"

func TestKeepAwakeSetIsIdempotent(t *testing.T) {
	var starts, stops int
	k := &keepAwake{starter: func() func() {
		starts++
		return func() { stops++ }
	}}

	k.set(true)
	k.set(true) // already active → no second start
	if starts != 1 {
		t.Fatalf("starts = %d, want 1", starts)
	}

	k.set(false)
	k.set(false) // already inactive → no second stop
	if stops != 1 {
		t.Fatalf("stops = %d, want 1", stops)
	}

	// Re-activates after a stop.
	k.set(true)
	if starts != 2 {
		t.Fatalf("starts after re-activate = %d, want 2", starts)
	}
	k.set(false)
}

func TestInhibitorCommand(t *testing.T) {
	cases := []struct {
		goos     string
		wantName string
	}{
		{"darwin", "caffeinate"},
		{"linux", "systemd-inhibit"},
		{"windows", ""}, // uses SetThreadExecutionState, not a subprocess
		{"plan9", ""},
	}
	for _, tc := range cases {
		name, args := inhibitorCommand(tc.goos, 4321)
		if name != tc.wantName {
			t.Errorf("inhibitorCommand(%q) name = %q, want %q", tc.goos, name, tc.wantName)
		}
		if tc.goos == "darwin" && (len(args) != 3 || args[0] != "-i" || args[1] != "-w" || args[2] != "4321") {
			t.Errorf("darwin caffeinate args = %v, want [-i -w 4321]", args)
		}
		if tc.goos == "linux" && (len(args) == 0 || args[len(args)-1] != "infinity") {
			t.Errorf("linux systemd-inhibit args = %v, want to end with 'infinity'", args)
		}
	}
}

func TestSetKeepAwakeBound(t *testing.T) {
	// Drive the bound method through a fake starter so no real inhibitor spawns.
	app := NewApp()
	var active bool
	app.keepAwake.starter = func() func() {
		active = true
		return func() { active = false }
	}
	app.SetKeepAwake(true)
	if !active {
		t.Error("SetKeepAwake(true) should engage the inhibitor")
	}
	app.SetKeepAwake(false)
	if active {
		t.Error("SetKeepAwake(false) should release the inhibitor")
	}
}
