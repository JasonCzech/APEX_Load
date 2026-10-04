//go:build !windows

package collectoragent

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a regular-file path swap to a FIFO from holding
// agent construction. Descriptor/type checks reject the swapped object.
func openKafkaManagementTrustFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
