//go:build linux

package http

import "syscall"

func limitPACWorkerMemory() error {
	// DATA bounds both the data segment and anonymous heap mappings on supported
	// hosts. Failure refuses evaluation rather than running an unbounded worker.
	var current syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_DATA, &current); err != nil {
		return err
	}
	const limit = 256 << 20
	if current.Cur > limit {
		current.Cur = limit
	}
	return syscall.Setrlimit(syscall.RLIMIT_DATA, &current)
}
