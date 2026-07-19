//go:build !windows && !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package main

import "fmt"

func acquireDesktopProcessLock(string) (desktopProcessLock, error) {
	return nil, fmt.Errorf("desktop process locking is unsupported on this platform")
}
