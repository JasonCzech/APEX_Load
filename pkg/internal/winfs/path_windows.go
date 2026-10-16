//go:build windows

package winfs

import (
	"path/filepath"
	"strings"
)

// ExtendedPath returns the \\?\ (or \\?\UNC\) form of an absolute path, so
// Win32 calls accept paths longer than MAX_PATH. A path that cannot be made
// absolute is returned unchanged.
func ExtendedPath(value string) string {
	abs, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	if strings.HasPrefix(abs, `\\?\`) {
		return abs
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(abs, `\\`)
	}
	return `\\?\` + abs
}
