package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestApplyRestoredWindowStateUsesOneOrderedNativeUIDispatch(t *testing.T) {
	previousDispatch := dispatchWindowStateMutation
	previousSetSize := windowSetSize
	previousSetPosition := windowSetPosition
	previousCenter := windowCenter
	previousMaximise := windowMaximise
	t.Cleanup(func() {
		dispatchWindowStateMutation = previousDispatch
		windowSetSize = previousSetSize
		windowSetPosition = previousSetPosition
		windowCenter = previousCenter
		windowMaximise = previousMaximise
	})

	var events []string
	dispatchWindowStateMutation = func(operation func()) bool {
		events = append(events, "dispatch-begin")
		operation()
		events = append(events, "dispatch-end")
		return true
	}
	windowSetSize = func(_ context.Context, width, height int) {
		events = append(events, fmt.Sprintf("size:%dx%d", width, height))
	}
	windowSetPosition = func(_ context.Context, x, y int) {
		events = append(events, fmt.Sprintf("position:%d,%d", x, y))
	}
	windowCenter = func(context.Context) {
		events = append(events, "center")
	}
	windowMaximise = func(context.Context) {
		events = append(events, "maximise")
	}

	g := sanitizedGeometry{
		ApplySize: true, Width: 1440, Height: 900,
		ApplyPosition: true, X: 125, Y: 80,
	}
	if !applyRestoredWindowState(context.Background(), g, true) {
		t.Fatal("applyRestoredWindowState() = false, want successful dispatch")
	}
	want := "[dispatch-begin size:1440x900 position:125,80 maximise dispatch-end]"
	if got := fmt.Sprint(events); got != want {
		t.Fatalf("restore events = %s, want %s", got, want)
	}

	events = nil
	if !applyRestoredWindowState(context.Background(), sanitizedGeometry{
		ApplySize: true, Width: 1000, Height: 700,
	}, false) {
		t.Fatal("centered applyRestoredWindowState() = false, want successful dispatch")
	}
	want = "[dispatch-begin size:1000x700 center dispatch-end]"
	if got := fmt.Sprint(events); got != want {
		t.Fatalf("centered restore events = %s, want %s", got, want)
	}

	events = nil
	dispatchWindowStateMutation = func(func()) bool { return false }
	if applyRestoredWindowState(context.Background(), g, true) {
		t.Fatal("applyRestoredWindowState() = true when dispatcher refused the operation")
	}
	if len(events) != 0 {
		t.Fatalf("native mutation ran after dispatcher refusal: %v", events)
	}
}

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

func TestWindowsLogicalGeometryIsWorkAreaRelativeAndDPINormalized(t *testing.T) {
	// WINDOWPLACEMENT reports workspace coordinates. On a monitor at 1920,1080
	// with a 60x90 left/top appbar, normal 2040,1260 is a 120x180 workspace
	// offset. Restoring it against work origin 1980,1170 reaches 2100,1350.
	got, ok := windowsLogicalPlacement(2040, 1260, 3960, 2340, 1920, 1080, 144, false)
	want := nativeWindowGeometry{
		Width: 1280, Height: 720, X: 80, Y: 120,
		HasRestoreBounds: true,
	}
	if !ok || got != want {
		t.Fatalf("logical Windows geometry = %+v, %v; want %+v, true", got, ok, want)
	}

	physicalX, physicalY := windowsPhysicalRestorePosition(got.X, got.Y, 1980, 1170, 144)
	if physicalX != 2100 || physicalY != 1350 {
		t.Fatalf("restored physical position = %d,%d; want 2100,1350", physicalX, physicalY)
	}
}

func TestWindowsPlacementUsesNormalBoundsForMaximisedAndMinimisedWindows(t *testing.T) {
	got, ok := windowsLogicalPlacement(-1770, -60, -270, 840, -1920, 0, 144, true)
	want := nativeWindowGeometry{
		Width: 1000, Height: 600, X: 100, Y: -40,
		Maximised: true, HasRestoreBounds: true,
	}
	if !ok || got != want {
		t.Fatalf("normal Windows placement = %+v, %v; want %+v, true", got, ok, want)
	}
	if !windowsPlacementIsMaximised(3, 0, false) {
		t.Fatal("maximized show state was not retained")
	}
	if windowsPlacementIsMaximised(2, 0, false) {
		t.Fatal("ordinary minimized window was restored maximized")
	}
	if !windowsPlacementIsMaximised(2, 0, true) || !windowsPlacementIsMaximised(2, 0x0002, false) {
		t.Fatal("minimized-from-maximized window lost its restore state")
	}
	if x, y := windowsPhysicalRestorePosition(25, 40, 10, 20, 0); x != 35 || y != 60 {
		t.Fatalf("96-DPI fallback position = %d,%d; want 35,60", x, y)
	}
	if _, ok := windowsLogicalPlacement(0, 0, 0, 0, 0, 0, 96, false); ok {
		t.Fatal("zero-sized Windows geometry was accepted")
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
			name: "visible bottom strip with unreachable title bar re-centers",
			st:   windowState{Width: 1000, Height: 700, X: 100, Y: -620},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "off-screen below re-centers",
			st:   windowState{Width: 1000, Height: 700, X: 100, Y: 5000},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "off-screen far left re-centers",
			st:   windowState{Width: 1000, Height: 700, X: -2000, Y: 100},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "width/height exceeding the desktop is clamped down",
			st:   windowState{Width: 5000, Height: 3000, X: 0, Y: 0},
			want: sanitizedGeometry{ApplySize: true, Width: deskW, Height: deskH, ApplyPosition: true, X: 0, Y: 0},
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

func TestSanitizeWindowGeometryUnknownBoundsRecenters(t *testing.T) {
	st := windowState{Width: 1200, Height: 800, X: 9999, Y: 9999}
	got := sanitizeWindowGeometry(st, 0, 0)
	want := sanitizedGeometry{ApplySize: true, Width: 1200, Height: 800}
	if got != want {
		t.Errorf("unknown bounds should re-center: got %+v want %+v", got, want)
	}
}

func TestSanitizeWindowGeometryAllowsNegativeVirtualDesktopOrigins(t *testing.T) {
	tests := []struct {
		name string
		st   windowState
		desk virtualDesktopBounds
		want sanitizedGeometry
	}{
		{
			name: "window on monitor left of primary is trusted",
			st:   windowState{Width: 1000, Height: 700, X: -1600, Y: 100},
			desk: virtualDesktopBounds{MinX: -1920, Width: 4480, Height: 1440},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700, ApplyPosition: true, X: -1600, Y: 100},
		},
		{
			name: "window on monitor above primary is trusted",
			st:   windowState{Width: 1000, Height: 700, X: 200, Y: -900},
			desk: virtualDesktopBounds{MinY: -1080, Width: 2560, Height: 2520},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700, ApplyPosition: true, X: 200, Y: -900},
		},
		{
			name: "window beyond virtual desktop still recenters",
			st:   windowState{Width: 1000, Height: 700, X: -4000, Y: 100},
			desk: virtualDesktopBounds{MinX: -1920, Width: 4480, Height: 1440},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeWindowGeometryInBounds(tc.st, tc.desk)
			if got != tc.want {
				t.Errorf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestDesktopBoundsFromScreensKeepsRestoresOnPrimaryDisplay(t *testing.T) {
	screens := []runtime.Screen{
		wailsScreen(true, 1920, 1080),
		wailsScreen(false, 1920, 1080),
	}
	desk := desktopBoundsFromScreens(screens)
	if want := (virtualDesktopBounds{Width: 1920, Height: 1080}); desk != want {
		t.Fatalf("desktopBoundsFromScreens() = %+v, want %+v", desk, want)
	}

	tests := []struct {
		name string
		st   windowState
		want sanitizedGeometry
	}{
		{
			name: "saved vertical-secondary position recenters after side-by-side layout",
			st:   windowState{Width: 1000, Height: 700, X: 100, Y: 1500},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "saved horizontal-secondary position recenters after vertical layout",
			st:   windowState{Width: 1000, Height: 700, X: 2500, Y: 100},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700},
		},
		{
			name: "primary-display position remains trusted",
			st:   windowState{Width: 1000, Height: 700, X: 200, Y: 100},
			want: sanitizedGeometry{ApplySize: true, Width: 1000, Height: 700, ApplyPosition: true, X: 200, Y: 100},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeWindowGeometryInBounds(tc.st, desk); got != tc.want {
				t.Fatalf("sanitizeWindowGeometryInBounds() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDesktopBoundsFromScreenResultUsesValidPartialWindowsResult(t *testing.T) {
	screens := []runtime.Screen{
		wailsScreen(true, 1920, 1080),
		{}, // Failed secondary enumeration remains as a partial zero-sized entry.
	}
	got := desktopBoundsFromScreenResult(screens, errors.New("secondary monitor DPI lookup failed"))
	want := virtualDesktopBounds{Width: 1920, Height: 1080}
	if got != want {
		t.Fatalf("desktopBoundsFromScreenResult() = %+v, want %+v", got, want)
	}
	if unknown := desktopBoundsFromScreenResult(nil, errors.New("screen enumeration failed")); unknown != (virtualDesktopBounds{}) {
		t.Fatalf("empty failed screen result = %+v, want unknown bounds", unknown)
	}
}

// wailsScreen builds a screen the way Wails reports one, with its logical Size.
func wailsScreen(primary bool, width, height int) runtime.Screen {
	screen := runtime.Screen{IsPrimary: primary}
	screen.Size.Width, screen.Size.Height = width, height
	return screen
}
