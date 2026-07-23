//go:build !windows

package javasidecar

import (
	"errors"
	"syscall"
)

func testProcessExists(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
