//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openBridgeReadFile makes the descriptor nonblocking before any type or
// identity check. A selected/replaced FIFO must never be able to stall the
// native bridge. O_NOFOLLOW also keeps the canonical-path check and open bound
// to the same final directory entry.
func openBridgeReadFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open bridge file: invalid descriptor")
	}
	return file, nil
}
