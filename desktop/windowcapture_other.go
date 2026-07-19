//go:build windows

package main

import (
	"context"
	goruntime "runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	apexloadWindowTitle             = "APEX Load"
	apexloadGWOwner                 = 4
	apexloadMonitorDefaultToNearest = 2
	apexloadSWPNoSize               = 0x0001
	apexloadSWPNoZOrder             = 0x0004
	apexloadSWPNoActivate           = 0x0010
)

type apexloadWindowsRect struct {
	Left, Top, Right, Bottom int32
}

type apexloadWindowsMonitorInfo struct {
	Size    uint32
	Monitor apexloadWindowsRect
	Work    apexloadWindowsRect
	Flags   uint32
}

type apexloadWindowsPoint struct {
	X, Y int32
}

type apexloadWindowsPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCommand    uint32
	MinPosition    apexloadWindowsPoint
	MaxPosition    apexloadWindowsPoint
	NormalPosition apexloadWindowsRect
}

type apexloadWindowSearch struct {
	processID uint32
	handle    uintptr
	fallback  uintptr
}

var (
	apexloadUser32                   = windows.NewLazySystemDLL("user32.dll")
	apexloadEnumWindows              = apexloadUser32.NewProc("EnumWindows")
	apexloadGetWindowThreadProcessID = apexloadUser32.NewProc("GetWindowThreadProcessId")
	apexloadGetWindow                = apexloadUser32.NewProc("GetWindow")
	apexloadIsWindowVisible          = apexloadUser32.NewProc("IsWindowVisible")
	apexloadGetWindowTextW           = apexloadUser32.NewProc("GetWindowTextW")
	apexloadGetWindowRect            = apexloadUser32.NewProc("GetWindowRect")
	apexloadGetWindowPlacement       = apexloadUser32.NewProc("GetWindowPlacement")
	apexloadMonitorFromWindow        = apexloadUser32.NewProc("MonitorFromWindow")
	apexloadGetMonitorInfoW          = apexloadUser32.NewProc("GetMonitorInfoW")
	apexloadGetDPIForWindow          = apexloadUser32.NewProc("GetDpiForWindow")
	apexloadIsZoomed                 = apexloadUser32.NewProc("IsZoomed")
	apexloadSetWindowPos             = apexloadUser32.NewProc("SetWindowPos")
	apexloadEnumWindowsCallback      = syscall.NewCallback(apexloadVisitTopLevelWindow)
)

func apexloadVisitTopLevelWindow(hwnd, opaque uintptr) uintptr {
	search := (*apexloadWindowSearch)(unsafe.Pointer(opaque))
	var processID uint32
	apexloadGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&processID)))
	if processID != search.processID {
		return 1
	}
	owner, _, _ := apexloadGetWindow.Call(hwnd, apexloadGWOwner)
	if owner != 0 {
		return 1
	}
	var rect apexloadWindowsRect
	ok, _, _ := apexloadGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 || rect.Right <= rect.Left || rect.Bottom <= rect.Top {
		return 1
	}

	if search.fallback == 0 {
		search.fallback = hwnd
	}
	visible, _, _ := apexloadIsWindowVisible.Call(hwnd)
	if visible != 0 {
		search.fallback = hwnd
	}
	var title [256]uint16
	length, _, _ := apexloadGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	if length > 0 && windows.UTF16ToString(title[:length]) == apexloadWindowTitle {
		search.handle = hwnd
		return 0
	}
	return 1
}

func apexloadMainWindowHandle() uintptr {
	search := apexloadWindowSearch{processID: windows.GetCurrentProcessId()}
	apexloadEnumWindows.Call(apexloadEnumWindowsCallback, uintptr(unsafe.Pointer(&search)))
	goruntime.KeepAlive(&search)
	if search.handle != 0 {
		return search.handle
	}
	return search.fallback
}

func apexloadWindowMetrics(hwnd uintptr) (windowsPhysicalWindowMetrics, bool) {
	monitor, _, _ := apexloadMonitorFromWindow.Call(hwnd, apexloadMonitorDefaultToNearest)
	if monitor == 0 {
		return windowsPhysicalWindowMetrics{}, false
	}
	info := apexloadWindowsMonitorInfo{Size: uint32(unsafe.Sizeof(apexloadWindowsMonitorInfo{}))}
	ok, _, _ := apexloadGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return windowsPhysicalWindowMetrics{}, false
	}
	return windowsPhysicalWindowMetrics{
		MonitorLeft: int(info.Monitor.Left), MonitorTop: int(info.Monitor.Top),
		WorkLeft: int(info.Work.Left), WorkTop: int(info.Work.Top),
		DPI: apexloadWindowDPI(hwnd),
	}, true
}

func apexloadWindowDPI(hwnd uintptr) uint32 {
	if err := apexloadGetDPIForWindow.Find(); err != nil {
		return 96
	}
	dpi, _, _ := apexloadGetDPIForWindow.Call(hwnd)
	if dpi == 0 {
		return 96
	}
	return uint32(dpi)
}

func captureNativeWindowGeometry(ctx context.Context) (nativeWindowGeometry, bool) {
	if ctx == nil {
		return nativeWindowGeometry{}, false
	}
	hwnd := apexloadMainWindowHandle()
	if hwnd == 0 {
		return nativeWindowGeometry{}, false
	}
	placement := apexloadWindowsPlacement{Length: uint32(unsafe.Sizeof(apexloadWindowsPlacement{}))}
	placementOK, _, _ := apexloadGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&placement)))
	if placementOK == 0 {
		return nativeWindowGeometry{}, false
	}
	zoomed, _, _ := apexloadIsZoomed.Call(hwnd)
	normal := placement.NormalPosition
	metrics, metricsOK := apexloadWindowMetrics(hwnd)
	if !metricsOK {
		return nativeWindowGeometry{}, false
	}
	return windowsLogicalPlacement(
		int(normal.Left), int(normal.Top), int(normal.Right), int(normal.Bottom),
		metrics.MonitorLeft, metrics.MonitorTop, metrics.DPI,
		windowsPlacementIsMaximised(placement.ShowCommand, placement.Flags, zoomed != 0),
	)
}

// setNativeWindowPosition bypasses Wails' Windows SetPos implementation, which
// adds the monitor work-area origin to coordinates that its Pos getter reports
// as absolute physical pixels. Persisted positions are work-area-relative
// 96-DPI logical units; convert them once and set the absolute Win32 position.
func setNativeWindowPosition(ctx context.Context, logicalX, logicalY int) {
	if ctx == nil {
		return
	}
	hwnd := apexloadMainWindowHandle()
	if hwnd == 0 {
		return
	}
	metrics, ok := apexloadWindowMetrics(hwnd)
	if !ok {
		return
	}
	x, y := windowsPhysicalRestorePosition(logicalX, logicalY, metrics.WorkLeft, metrics.WorkTop, metrics.DPI)
	apexloadSetWindowPos.Call(
		hwnd,
		0,
		uintptr(x),
		uintptr(y),
		0,
		0,
		apexloadSWPNoSize|apexloadSWPNoZOrder|apexloadSWPNoActivate,
	)
}
