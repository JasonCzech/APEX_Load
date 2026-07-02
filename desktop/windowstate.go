package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const windowStateFileName = "window-state.json"

// Minimum window size — keeps the sidebar + topbar usable. Also passed to the
// Wails options so the OS enforces it during manual resize.
const (
	minWindowWidth  = 960
	minWindowHeight = 640
	// windowEdgeMargin is how many pixels of the window must remain on the
	// usable desktop for a restored position to be trusted; otherwise the window
	// is re-centered (guards the "monitor unplugged" case).
	windowEdgeMargin = 80
)

// windowState is the persisted window geometry restored across launches.
type windowState struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Maximised bool   `json:"maximised"`
	LastRoute string `json:"last_route,omitempty"`
}

func windowStatePath(appDataPath string) string {
	return filepath.Join(appDataPath, windowStateFileName)
}

// loadWindowState reads persisted geometry. A missing or malformed file is not
// an error (first launch / corruption): it returns ok=false so the caller keeps
// the built-in defaults.
func loadWindowState(appDataPath string) (windowState, bool) {
	if appDataPath == "" {
		return windowState{}, false
	}
	data, err := os.ReadFile(windowStatePath(appDataPath))
	if err != nil {
		return windowState{}, false
	}
	var st windowState
	if err := json.Unmarshal(data, &st); err != nil {
		return windowState{}, false
	}
	return st, true
}

// saveWindowState writes geometry as indented JSON.
func saveWindowState(appDataPath string, st windowState) error {
	if appDataPath == "" {
		return fmt.Errorf("app data path is not available")
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(windowStatePath(appDataPath), data, 0o644)
}

// sanitizedGeometry is the outcome of validating a restored windowState against
// the usable desktop bounds.
type sanitizedGeometry struct {
	ApplySize     bool
	Width, Height int
	ApplyPosition bool
	X, Y          int
}

type virtualDesktopBounds struct {
	MinX, MinY    int
	Width, Height int
}

// sanitizeWindowGeometry clamps a restored geometry against a usable desktop
// with origin at 0,0. It is kept as a convenience wrapper for tests and callers
// that only know desktop size.
func sanitizeWindowGeometry(st windowState, deskW, deskH int) sanitizedGeometry {
	return sanitizeWindowGeometryInBounds(st, virtualDesktopBounds{Width: deskW, Height: deskH})
}

// sanitizeWindowGeometryInBounds clamps a restored geometry against the usable
// desktop bounds. It guarantees the window is at least min-size and that enough
// of it stays on-screen to be reachable; otherwise it asks the caller to
// re-center.
//
// deskW/deskH of 0 mean "unknown" (no screen info) — the saved position is then
// trusted as-is (the OS will usually clamp an off-screen window anyway).
//
// This is a pure function so it is unit-testable without a live Wails window.
func sanitizeWindowGeometryInBounds(st windowState, desk virtualDesktopBounds) sanitizedGeometry {
	// No usable size persisted → keep the built-in option defaults entirely.
	if st.Width <= 0 || st.Height <= 0 {
		return sanitizedGeometry{}
	}

	w, h := st.Width, st.Height
	if w < minWindowWidth {
		w = minWindowWidth
	}
	if h < minWindowHeight {
		h = minWindowHeight
	}
	if desk.Width > 0 && w > desk.Width {
		w = desk.Width
	}
	if desk.Height > 0 && h > desk.Height {
		h = desk.Height
	}

	g := sanitizedGeometry{ApplySize: true, Width: w, Height: h}

	// Without known desktop bounds, trust the saved position.
	if desk.Width <= 0 || desk.Height <= 0 {
		g.ApplyPosition = true
		g.X, g.Y = st.X, st.Y
		return g
	}

	// Re-center if the window would be (almost) entirely off the usable desktop.
	maxX := desk.MinX + desk.Width
	maxY := desk.MinY + desk.Height
	offRight := st.X > maxX-windowEdgeMargin
	offBottom := st.Y > maxY-windowEdgeMargin
	offLeft := st.X+w < desk.MinX+windowEdgeMargin
	offTop := st.Y+h < desk.MinY+windowEdgeMargin
	if offRight || offBottom || offLeft || offTop {
		return g // ApplyPosition stays false → caller centers
	}

	g.ApplyPosition = true
	g.X, g.Y = st.X, st.Y
	return g
}
