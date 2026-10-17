//go:build windows

package importartifact

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
)

const durableTombstoneDirectory = ".durable-delete"

func validateDurableFilesystem(root string) error {
	err := winfs.ValidateLocalVolume(root, "NTFS", "REFS")
	if errors.Is(err, winfs.ErrUnsupportedVolume) {
		return unsupportedImportVolume(err)
	}
	if err != nil {
		return fmt.Errorf("inspect protected import artifact volume: %w", err)
	}
	return nil
}

// mkdirAllDurably publishes each missing directory with a same-volume,
// write-through rename. Creating the final name directly with CreateDirectoryW
// would leave the namespace mutation outside the durability contract used for
// protected artifacts.
func mkdirAllDurably(target string, mode os.FileMode) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	missing := make([]string, 0, 8)
	current := filepath.Clean(abs)
	for {
		info, statErr := os.Stat(current)
		if statErr == nil {
			if !info.IsDir() {
				return fmt.Errorf("protected import artifact path %q is not a directory", current)
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return statErr
		}
		current = parent
	}
	if err := validateDurableFilesystem(current); err != nil {
		return err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		destination := missing[index]
		// A deterministic sibling lets the next controller finish an interrupted
		// directory publication instead of accumulating source-named debris.
		temporary := durableDirectoryPendingPath(destination)
		err := os.Mkdir(temporary, mode)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err == nil {
			if err := os.Chmod(temporary, mode); err != nil {
				if directoryExists(destination) {
					continue
				}
				_ = os.Remove(temporary)
				return err
			}
		} else {
			info, statErr := os.Stat(temporary)
			if statErr != nil || !info.IsDir() {
				if directoryExists(destination) {
					continue
				}
				return errors.Join(err, statErr)
			}
		}
		entries, readErr := os.ReadDir(temporary)
		if readErr != nil {
			if directoryExists(destination) {
				continue
			}
			return readErr
		}
		if len(entries) != 0 {
			return fmt.Errorf("protected import durable directory staging %q is not empty", temporary)
		}
		err = winfs.MoveFileWriteThrough(temporary, destination, false)
		if err == nil {
			continue
		}
		info, statErr := os.Stat(destination)
		if statErr == nil && info.IsDir() {
			_ = os.Remove(temporary)
			continue
		}
		_ = os.Remove(temporary)
		if statErr == nil && !info.IsDir() {
			return fmt.Errorf("protected import artifact path %q is not a directory", destination)
		}
		return errors.Join(err, statErr)
	}
	return nil
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func durableDirectoryPendingPath(destination string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(destination))))
	return filepath.Join(filepath.Dir(destination), durableDirectoryPendingPrefix+hex.EncodeToString(digest[:12]))
}

func validateWindowsVolume(driveType uint32, filesystem string) error {
	return unsupportedImportVolume(winfs.ValidateVolume(driveType, filesystem, "NTFS", "REFS"))
}

func unsupportedImportVolume(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: Windows protected imports require a local fixed NTFS or ReFS volume: %v", ErrUnsupportedFilesystem, err)
}

func publishCreateOnly(existingPath, targetPath string) error {
	return winfs.MoveFileWriteThrough(existingPath, targetPath, false)
}

func renameWriteThrough(oldPath, newPath string) error {
	return winfs.MoveFileWriteThrough(oldPath, newPath, true)
}

func removeWriteThrough(root, targetPath string) error {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	// Callers prune directories expecting rmdir semantics: a directory that
	// still holds entries (another project's sessions, sibling objects) must
	// stay. A tombstone move would take those entries with it, so directories
	// use RemoveDirectoryW, which refuses a non-empty directory atomically. An
	// empty directory carries no payload whose absence must be write-through.
	if info.IsDir() {
		return os.Remove(targetPath)
	}
	tombstoneRoot := filepath.Join(root, durableTombstoneDirectory)
	if err := mkdirAllDurably(tombstoneRoot, 0o700); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tombstone := filepath.Join(tombstoneRoot, hex.EncodeToString(nonce[:]))
	if err := winfs.MoveFileWriteThrough(targetPath, tombstone, false); err != nil {
		return err
	}
	// The write-through move is the durability boundary. Cleanup is reclaimable
	// housekeeping and is retried when the store next opens.
	_ = os.RemoveAll(tombstone)
	return nil
}

func cleanupDurableTombstones(root string) error {
	directory := filepath.Join(root, durableTombstoneDirectory)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Publication already made the original name durably absent. Antivirus,
		// indexing, or another transient handle may delay physical reclamation;
		// leave that tombstone for a later startup rather than taking imports
		// offline for reclaim-only housekeeping.
		_ = os.RemoveAll(filepath.Join(directory, entry.Name()))
	}
	return nil
}
