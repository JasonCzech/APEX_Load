//go:build !darwin && !linux && !windows

package diskspace

import (
	"fmt"
	"os"
	"runtime"
)

// AvailablePath reports that disk-space inspection is unsupported on this
// platform.
func AvailablePath(string) (int64, error) {
	return 0, fmt.Errorf("disk-space inspection is unsupported on %s", runtime.GOOS)
}

// AvailableFile reports that disk-space inspection is unsupported on this
// platform.
func AvailableFile(*os.File) (int64, error) {
	return 0, fmt.Errorf("disk-space inspection is unsupported on %s", runtime.GOOS)
}
