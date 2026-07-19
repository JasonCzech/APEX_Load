//go:build !windows

package recorder

import (
	"fmt"
	"os"
	"path/filepath"
)

// replacePersistentCAFile atomically publishes one fully-synced file and then
// syncs its directory so the rename survives a sudden power loss on local Unix
// filesystems.
func replacePersistentCAFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(to))
	if err != nil {
		return fmt.Errorf("open recorder CA directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync recorder CA directory: %w", err)
	}
	return nil
}
