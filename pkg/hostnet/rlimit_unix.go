//go:build unix

package hostnet

import "syscall"

func openFileLimit() OpenFileLimit {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return OpenFileLimit{}
	}
	return OpenFileLimit{Available: true, Soft: uint64(limit.Cur), Hard: uint64(limit.Max)}
}
