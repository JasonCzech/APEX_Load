//go:build unix

package hostnet

import "syscall"

func openFileLimit() OpenFileLimit {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return OpenFileLimit{}
	}
	return OpenFileLimit{Available: true, Soft: widenRlimit(limit.Cur), Hard: widenRlimit(limit.Max)}
}

// widenRlimit reads an Rlimit field, which is an int64 on some Unix systems,
// such as FreeBSD, and a uint64 on Linux and Darwin.
func widenRlimit[T ~int64 | ~uint64](value T) uint64 { return uint64(value) }
