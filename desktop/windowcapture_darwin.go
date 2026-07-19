//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#include <dispatch/dispatch.h>
#include <pthread.h>

typedef struct {
	int width;
	int height;
	int x;
	int y;
	int maximised;
	int valid;
} apexload_window_geometry;

static void apexload_capture_window_geometry_on_main(void *opaque) {
	apexload_window_geometry *result = (apexload_window_geometry *)opaque;
	@autoreleasepool {
		NSWindow *window = [NSApp mainWindow];
		if (window == nil) {
			window = [NSApp keyWindow];
		}
		if (window == nil) {
			return;
		}
		NSScreen *screen = [window screen];
		if (screen == nil) {
			screen = [NSScreen mainScreen];
		}
		if (screen == nil) {
			return;
		}
		NSRect frame = [window frame];
		NSRect visible = [screen visibleFrame];
		result->width = (int)frame.size.width;
		result->height = (int)frame.size.height;
		result->x = (int)(frame.origin.x - visible.origin.x);
		result->y = (int)(visible.size.height - (frame.origin.y - visible.origin.y) - frame.size.height);
		result->maximised = [window isZoomed] ? 1 : 0;
		result->valid = result->width > 0 && result->height > 0;
	}
}

static apexload_window_geometry apexload_capture_window_geometry(void) {
	apexload_window_geometry result = {0};
	if (pthread_main_np() != 0) {
		apexload_capture_window_geometry_on_main(&result);
	} else {
		dispatch_sync_f(dispatch_get_main_queue(), &result, apexload_capture_window_geometry_on_main);
	}
	return result;
}
*/
import "C"

import "context"

func captureNativeWindowGeometry(ctx context.Context) (nativeWindowGeometry, bool) {
	if ctx == nil {
		return nativeWindowGeometry{}, false
	}
	geometry := C.apexload_capture_window_geometry()
	if geometry.valid == 0 {
		return nativeWindowGeometry{}, false
	}
	return nativeWindowGeometry{
		Width:     int(geometry.width),
		Height:    int(geometry.height),
		X:         int(geometry.x),
		Y:         int(geometry.y),
		Maximised: geometry.maximised != 0,
	}, true
}
