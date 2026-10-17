//go:build windows

package recordbridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
	"golang.org/x/sys/windows"
)

// ensurePrivateReplayHARRoot makes the managed root with a private DACL
// (this user and SYSTEM, inherited below) when missing, and proves the
// directory there private: owned by this token, granting no one else, and
// not a reparse point. A root made by another user, or before this check
// existed (with the temporary directory's inherited ACL), is not adopted.
func ensurePrivateReplayHARRoot(root string) error {
	parent, err := os.Open(filepath.Dir(root))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(root)
	dir, err := winfs.CreatePrivateDirAt(parent, name)
	if errors.Is(err, os.ErrExist) {
		dir, err = winfs.OpenAt(parent, name, windows.READ_CONTROL|windows.FILE_LIST_DIRECTORY, true)
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := winfs.CheckPrivate(dir, false); err != nil {
		return errors.Join(errors.New("the managed replay HAR root is not a private directory"), err)
	}
	return nil
}

// privateReplayHARTempDir is a fresh private directory under the system
// temporary directory, for a store that cannot use the managed root.
func privateReplayHARTempDir() (string, error) {
	parentPath := os.TempDir()
	parent, err := os.Open(parentPath)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	for range 8 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := "apexload-replay-har-" + hex.EncodeToString(random[:])
		dir, err := winfs.CreatePrivateDirAt(parent, name)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return filepath.Join(parentPath, name), dir.Close()
	}
	return "", errors.New("no private replay HAR directory name was free")
}
