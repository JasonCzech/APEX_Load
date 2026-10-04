//go:build linux || darwin

package duckdb

import (
	"errors"
	"os"
	"syscall"
)

func workerPlatformProcessAbsent(err error) bool {
	// Used only for the exact child's gopsutil creation/MemoryInfo query, never
	// parent/kernel/quota files. Darwin proc_pidinfo returns ESRCH; Linux /proc
	// statm can disappear before the sole Wait result is consumed.
	return errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrNotExist)
}
