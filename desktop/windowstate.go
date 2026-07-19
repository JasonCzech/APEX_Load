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
	return writePrivateFileAtomic(windowStatePath(appDataPath), data)
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
	MinX, MinY int
	// MaxX/MaxY are the right/bottom edges of the usable virtual desktop. They
	// are tracked separately from Width/Height (which clamp the window SIZE)
	// because a multi-monitor arrangement is not symmetric: a secondary monitor
	// may extend to the right/below the primary, so the position bound is not
	// simply MinX+Width. Zero means "derive from MinX+Width / MinY+Height".
	MaxX, MaxY    int
	Width, Height int
}

// windowsPhysicalWindowMetrics describes the Win32 monitor work origin and DPI
// used to convert a persisted logical offset into an absolute screen position.
type windowsPhysicalWindowMetrics struct {
	MonitorLeft, MonitorTop int
	WorkLeft, WorkTop       int
	DPI                     uint32
}

func windowsLogicalPlacement(normalLeft, normalTop, normalRight, normalBottom, monitorLeft, monitorTop int, dpi uint32, maximised bool) (nativeWindowGeometry, bool) {
	width := normalRight - normalLeft
	height := normalBottom - normalTop
	if width <= 0 || height <= 0 {
		return nativeWindowGeometry{}, false
	}
	return nativeWindowGeometry{
		Width:            windowsPhysicalToLogical(width, dpi),
		Height:           windowsPhysicalToLogical(height, dpi),
		X:                windowsPhysicalToLogical(normalLeft-monitorLeft, dpi),
		Y:                windowsPhysicalToLogical(normalTop-monitorTop, dpi),
		Maximised:        maximised,
		HasRestoreBounds: true,
	}, true
}

func windowsPlacementIsMaximised(showCommand, flags uint32, isZoomed bool) bool {
	const (
		showMinimised      = 2
		showMaximised      = 3
		restoreToMaximised = 0x0002
	)
	return showCommand == showMaximised ||
		(showCommand == showMinimised && (isZoomed || flags&restoreToMaximised != 0))
}

func windowsPhysicalRestorePosition(logicalX, logicalY, workLeft, workTop int, dpi uint32) (int, int) {
	return workLeft + windowsLogicalToPhysical(logicalX, dpi),
		workTop + windowsLogicalToPhysical(logicalY, dpi)
}

func windowsPhysicalToLogical(value int, dpi uint32) int {
	if dpi == 0 {
		dpi = 96
	}
	return roundedScale(value, 96, int64(dpi))
}

func windowsLogicalToPhysical(value int, dpi uint32) int {
	if dpi == 0 {
		dpi = 96
	}
	return roundedScale(value, int64(dpi), 96)
}

func roundedScale(value int, numerator, denominator int64) int {
	scaled := int64(value) * numerator
	if scaled >= 0 {
		return int((scaled + denominator/2) / denominator)
	}
	return int((scaled - denominator/2) / denominator)
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
// deskW/deskH of 0 mean "unknown" (no screen info). The saved size remains
// useful, but the position is re-centered: native window managers do not
// consistently clamp unreachable coordinates on every supported platform.
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

	// Without known desktop bounds, keep the size and ask the caller to center.
	if desk.Width <= 0 || desk.Height <= 0 {
		return g
	}

	// Re-center if the window would be (almost) entirely off the usable desktop.
	maxX := desk.MaxX
	if maxX == 0 {
		maxX = desk.MinX + desk.Width
	}
	maxY := desk.MaxY
	if maxY == 0 {
		maxY = desk.MinY + desk.Height
	}
	offRight := st.X > maxX-windowEdgeMargin
	offBottom := st.Y > maxY-windowEdgeMargin
	offLeft := st.X+w < desk.MinX+windowEdgeMargin
	// A bottom strip does not make a window above the desktop recoverable: its
	// title bar is still unreachable, so the user cannot drag it back. Require
	// the restored top edge itself to remain on the usable desktop.
	offTop := st.Y < desk.MinY
	if offRight || offBottom || offLeft || offTop {
		return g // ApplyPosition stays false → caller centers
	}

	g.ApplyPosition = true
	g.X, g.Y = st.X, st.Y
	return g
}
