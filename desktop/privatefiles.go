package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
)

func ensurePrivateDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("directory path is required")
	}
	if err := os.MkdirAll(path, privateDirectoryMode); err != nil {
		return err
	}
	// MkdirAll honors the process umask and does not update existing legacy
	// permissions. The platform helper uses mode bits on Unix and a protected,
	// owner-only DACL on Windows.
	return setOwnerOnlyPermissions(path, true)
}

func ensurePrivateFile(path string) error {
	if path == "" {
		return fmt.Errorf("file path is required")
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return setOwnerOnlyPermissions(path, false)
}

func hardenPrivateStateFiles(appDataPath string) error {
	for _, name := range []string{windowStateFileName, recentsFileName} {
		if err := ensurePrivateFile(filepath.Join(appDataPath, name)); err != nil {
			return err
		}
	}
	return nil
}

// writePrivateFileAtomic prevents partially-written state after a crash and
// ensures both new files and migrated replacements are owner-only.
func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDirectory(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".apexload-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := setOwnerOnlyPermissions(tmpName, false); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replacePrivateFile(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return ensurePrivateFile(path)
}
