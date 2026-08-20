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

	"golang.org/x/sys/windows"
)

const durableTombstoneDirectory = ".durable-delete"

func validateDurableFilesystem(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootPtr, err := windows.UTF16PtrFromString(extendedWindowsPath(abs))
	if err != nil {
		return err
	}
	// A volume can be mounted below a long directory path. The traditional
	// MAX_PATH-sized result buffer would reject that otherwise supported local
	// NTFS/ReFS layout even though all subsequent operations use the extended
	// namespace.
	volumePath := make([]uint16, 32768)
	if err := windows.GetVolumePathName(rootPtr, &volumePath[0], uint32(len(volumePath))); err != nil {
		return fmt.Errorf("resolve protected import artifact volume: %w", err)
	}
	volumeRoot := windows.UTF16ToString(volumePath)
	volumePtr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return err
	}
	driveType := windows.GetDriveType(volumePtr)
	filesystem := make([]uint16, 32)
	if err := windows.GetVolumeInformation(volumePtr, nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return fmt.Errorf("inspect protected import artifact volume: %w", err)
	}
	name := strings.ToUpper(strings.TrimSpace(windows.UTF16ToString(filesystem)))
	return validateWindowsVolume(driveType, name)
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
		err = moveFileWriteThrough(temporary, destination, false)
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
	return filepath.Join(filepath.Dir(destination), ".apexload-durable-dir-"+hex.EncodeToString(digest[:12]))
}

func validateWindowsVolume(driveType uint32, filesystem string) error {
	if driveType != windows.DRIVE_FIXED {
		return fmt.Errorf("%w: Windows protected imports require a local fixed NTFS or ReFS volume", ErrUnsupportedFilesystem)
	}
	name := strings.ToUpper(strings.TrimSpace(filesystem))
	if name != "NTFS" && name != "REFS" {
		return fmt.Errorf("%w: Windows protected imports require NTFS or ReFS (found %s)", ErrUnsupportedFilesystem, name)
	}
	return nil
}

func publishCreateOnly(existingPath, targetPath string) error {
	return moveFileWriteThrough(existingPath, targetPath, false)
}

func renameWriteThrough(oldPath, newPath string) error {
	return moveFileWriteThrough(oldPath, newPath, true)
}

func moveFileWriteThrough(oldPath, newPath string, replace bool) error {
	oldPtr, err := windows.UTF16PtrFromString(extendedWindowsPath(oldPath))
	if err != nil {
		return err
	}
	newPtr, err := windows.UTF16PtrFromString(extendedWindowsPath(newPath))
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if err := windows.MoveFileEx(oldPtr, newPtr, flags); err != nil {
		switch {
		case errors.Is(err, windows.ERROR_FILE_EXISTS), errors.Is(err, windows.ERROR_ALREADY_EXISTS):
			return fmt.Errorf("%w: %v", fs.ErrExist, err)
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			return fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		default:
			return err
		}
	}
	return nil
}

func removeWriteThrough(root, targetPath string) error {
	if _, err := os.Lstat(targetPath); err != nil {
		return err
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
	if err := moveFileWriteThrough(targetPath, tombstone, false); err != nil {
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

func extendedWindowsPath(value string) string {
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
