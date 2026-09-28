//go:build !windows

package javasidecar

import (
	"os"
	"syscall"
)

// Stat alone cannot prevent a path swap to a FIFO before Open. Nonblocking
// opens plus the caller's descriptor/type check keep that race fail-closed.
func openPooledManifestFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
