package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := windowState{Width: 1400, Height: 900, X: 120, Y: 80, Maximised: false, LastRoute: "#/scripts"}
	if err := saveWindowState(dir, want); err != nil {
		t.Fatalf("saveWindowState: %v", err)
	}
	got, ok := loadWindowState(dir)
	if !ok {
		t.Fatal("loadWindowState ok=false after save")
	}
	if got != want {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadWindowStateMissingFile(t *testing.T) {
	if _, ok := loadWindowState(t.TempDir()); ok {
		t.Error("expected ok=false for missing window-state.json")
	}
	if _, ok := loadWindowState(""); ok {
		t.Error("expected ok=false for empty app data path")
	}
}

func TestLoadWindowStateMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, windowStateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadWindowState(dir); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestSaveWindowStateRequiresPath(t *testing.T) {
	if err := saveWindowState("", windowState{Width: 100, Height: 100}); err == nil {
		t.Error("expected error saving with empty app data path")
	}
}

func TestSanitizeWindowGeometry(t *testing.T) {
	const deskW, deskH = 2560, 1440

	tests := []struct {
		name string
		st   windowState
		want sanitizedGeometry
	}{
		{
			name: "valid on-screen geometry is trusted",
			st:   windowState{Width: 1400, Height: 900, X: 200, Y: 100},
			want: sanitizedGeometry{ApplySize: true, Width: 1400, Height: 900, ApplyPosition: true, X: 200, Y: 100},
		},
		{
			name: "size below minimum is clamped up",
			st:   windowState{Width: 400, Height: 300, X: 10, Y: 10},
			want: sanitizedGeometry{ApplySize: true, Width: minWindowWidth, Height: minWindowHeight, ApplyPosition: true, X: 10, Y: 10},
		},
		{
			name: "off-screen to the right re-centers (no position)",
			st:   windowState{Width: 1000, Height: 700, X: 5000, Y: 100},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "off-screen above re-centers",
			st:   windowState{Width: 1000, Height: 700, X: 100, Y: -1000},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "zero size keeps defaults entirely",
			st:   windowState{Width: 0, Height: 0, X: 0, Y: 0},
			want: sanitizedGeometry{},
		},
		{
			name: "negative offset within margin is kept (secondary monitor to the left)",
			st:   windowState{Width: 1000, Height: 700, X: -200, Y: 50},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700, ApplyPosition: true, X: -200, Y: 50},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeWindowGeometry(tc.st, deskW, deskH)
			if got != tc.want {
				t.Errorf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestSanitizeWindowGeometryUnknownBoundsTrustsPosition(t *testing.T) {
	st := windowState{Width: 1200, Height: 800, X: 9999, Y: 9999}
	got := sanitizeWindowGeometry(st, 0, 0)
	want := sanitizedGeometry{ApplySize: true, Width: 1200, Height: 800, ApplyPosition: true, X: 9999, Y: 9999}
	if got != want {
		t.Errorf("unknown bounds should trust position: got %+v want %+v", got, want)
	}
}
