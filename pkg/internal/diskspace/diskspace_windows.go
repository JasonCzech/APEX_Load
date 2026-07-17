//go:build windows

package diskspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	maxFinalPathUTF16Length = 1 << 20
	volumeNameDOS           = 0x0
	volumeNameGUID          = 0x1
)

// AvailablePath returns the bytes available to the current process on the
// filesystem containing path.
func AvailablePath(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("disk-space path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("resolve disk-space path %q: %w", path, err)
	}
	return availableDirectory(absolute)
}

// AvailableFile returns the bytes available to the current process on the
// filesystem containing an already-open file. Resolving the volume or directory
// from the open handle keeps the check bound to the file even if its original
// path is replaced with a reparse point while the download is being staged.
func AvailableFile(file *os.File) (int64, error) {
	if file == nil {
		return 0, fmt.Errorf("disk-space file is required")
	}
	guidPath, guidErr := finalPathForFile(file, volumeNameGUID)
	guidFailure := guidErr
	if guidErr == nil {
		if volumeRoot := windowsVolumeRoot(guidPath); volumeRoot != "" {
			available, err := availableDirectory(volumeRoot)
			if err == nil {
				return available, nil
			}
			guidFailure = err
		} else {
			guidFailure = errors.New("volume GUID path has an unexpected format")
		}
	}
	finalPath, err := finalPathForFile(file, volumeNameDOS)
	if err != nil {
		return 0, fmt.Errorf("resolve final path for file %q using volume GUID (%v) or DOS path: %w", file.Name(), guidFailure, err)
	}
	available, err := availableDirectory(filepath.Dir(finalPath))
	if err != nil {
		return 0, fmt.Errorf("query free space for file %q using volume GUID (%v) or DOS path: %w", file.Name(), guidFailure, err)
	}
	return available, nil
}

func availableDirectory(path string) (int64, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("encode disk-space path %q: %w", path, err)
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(pathPointer, &available, nil, nil); err != nil {
		return 0, fmt.Errorf("query free space for %q: %w", path, err)
	}
	return clampAvailableBytes(available), nil
}

func finalPathForFile(file *os.File, flags uint32) (string, error) {
	handle := windows.Handle(file.Fd())
	if handle == windows.InvalidHandle {
		return "", fmt.Errorf("resolve final path for file %q: invalid handle", file.Name())
	}
	buffer := make([]uint16, 256)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if err != nil {
			return "", fmt.Errorf("resolve final path for file %q: %w", file.Name(), err)
		}
		if length < uint32(len(buffer)) {
			path := windows.UTF16ToString(buffer[:length])
			if path == "" {
				return "", fmt.Errorf("resolve final path for file %q: empty path", file.Name())
			}
			return path, nil
		}
		if length > maxFinalPathUTF16Length {
			return "", fmt.Errorf("resolve final path for file %q: path length %d exceeds limit", file.Name(), length)
		}
		buffer = make([]uint16, length)
	}
}

func windowsVolumeRoot(path string) string {
	const prefix = `\\?\Volume{`
	if len(path) < len(prefix) || !strings.EqualFold(path[:len(prefix)], prefix) {
		return ""
	}
	end := strings.Index(path[len(prefix):], `}\`)
	if end < 0 {
		return ""
	}
	return path[:len(prefix)+end+2]
}
