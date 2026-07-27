//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package brunoartifact

import (
	"errors"
	"os"
)

// syncDirectory persists directory-entry changes made before this call. It is
// kept behind an OS-specific boundary because Windows does not expose a
// portable directory fsync through os.File.Sync.
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
