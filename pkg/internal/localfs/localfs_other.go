//go:build !darwin && !linux && !windows

package localfs

import "os"

func isLocal(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, err
	}
	return false, ErrUnknown
}
