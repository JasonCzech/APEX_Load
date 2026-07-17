//go:build darwin || linux

package diskspace

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// AvailablePath returns the bytes available to the current process on the
// filesystem containing path.
func AvailablePath(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("disk-space path is required")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("stat filesystem for %q: %w", path, err)
	}
	return availableBytesFromStat(stat.Bavail, stat.Bsize)
}

// AvailableFile returns the bytes available to the current process on the
// filesystem containing an already-open file.
func AvailableFile(file *os.File) (int64, error) {
	if file == nil {
		return 0, fmt.Errorf("disk-space file is required")
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return 0, fmt.Errorf("stat filesystem for file %q: %w", file.Name(), err)
	}
	return availableBytesFromStat(stat.Bavail, stat.Bsize)
}

func availableBytesFromStat[T ~uint32 | ~int64](availableBlocks uint64, blockSize T) (int64, error) {
	if blockSize <= 0 {
		return 0, fmt.Errorf("filesystem reported an invalid block size")
	}
	return availableBytesFromBlocks(availableBlocks, uint64(blockSize)), nil
}
