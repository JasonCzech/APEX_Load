//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#import <Cocoa/Cocoa.h>

static void setDockBadgeLabel(const char *label) {
  NSString *s = label ? [NSString stringWithUTF8String:label] : @"";
  // Dock tile mutations must run on the main thread.
  dispatch_async(dispatch_get_main_queue(), ^{
    @autoreleasepool {
      [[NSApp dockTile] setBadgeLabel:(s.length ? s : nil)];
    }
  });
}
*/
import "C"

import "unsafe"

// setDockBadge sets the macOS dock badge label ("" clears it).
func setDockBadge(label string) {
	c := C.CString(label)
	defer C.free(unsafe.Pointer(c))
	C.setDockBadgeLabel(c)
}
