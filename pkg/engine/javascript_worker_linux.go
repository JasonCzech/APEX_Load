//go:build linux

package engine

import "syscall"

func nativeJSContainmentAvailable() bool {
	var memory syscall.Rlimit
	return syscall.Getrlimit(syscall.RLIMIT_DATA, &memory) == nil && (memory.Cur == ^uint64(0) || memory.Cur >= 128<<20)
}
func nativeJSApplyLimits() error {
	for _, bound := range []struct {
		resource int
		limit    uint64
	}{{syscall.RLIMIT_DATA, 256 << 20}, {syscall.RLIMIT_CPU, 2}, {syscall.RLIMIT_CORE, 0}} {
		var current syscall.Rlimit
		if err := syscall.Getrlimit(bound.resource, &current); err != nil {
			return err
		}
		if current.Cur > bound.limit {
			current.Cur = bound.limit
		}
		if current.Max > bound.limit {
			current.Max = bound.limit
		}
		if err := syscall.Setrlimit(bound.resource, &current); err != nil {
			return err
		}
	}
	return nil
}
