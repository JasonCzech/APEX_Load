package duckdb

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Unlike fs.WalkDir/ReadDir, every directory descriptor is registered before
// its first operation and its actual Close result participates in ownership.
// Directory depth, entries and each ReadDir allocation are explicitly bounded.
func workerOwnedWalk(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error) error {
	return workerOwnedWalkMode(root, owner, visit, false)
}

// Quota observation permits only disappearance of previously enumerated nested
// entries in this exact owned scratch tree. It never supplies descriptor/process
// closure, never ignores replacement/symlink/I/O errors, and refuses a missing root.
func workerOwnedQuotaWalk(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error) error {
	return workerOwnedWalkMode(root, owner, visit, true)
}

func workerOwnedWalkMode(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error, allowGone bool) error {
	var walk func(string, int) error
	count := 0
	walk = func(path string, depth int) (retErr error) {
		if depth > 32 {
			return errWorkerResourceLimit
		}
		before, err := root.Lstat(path)
		if allowGone && path != "." && os.IsNotExist(err) {
			return nil
		}
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return errWorkerResourceLimit
		}
		file, err := root.Open(path)
		if allowGone && path != "." && os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return errWorkerResourceLimit
		}
		owner.TrackOwner(file)
		defer func() {
			closeErr := owner.CloseOwner(file)
			retErr = errors.Join(retErr, closeErr)
			if closeErr == nil {
				if ended, ok := owner.(interface{ forgetJoinedOwner(io.Closer) }); ok {
					ended.forgetJoinedOwner(file)
				}
			}
		}()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(before, actual) {
			return errWorkerResourceLimit
		}
		if path == "." {
			count++
			if err = visit(path, fs.FileInfoToDirEntry(actual), nil); err != nil {
				return err
			}
		}
		for {
			entries, readErr := file.ReadDir(128)
			for _, entry := range entries {
				count++
				if count > 16384 {
					return errWorkerResourceLimit
				}
				name := entry.Name()
				if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
					return errWorkerResourceLimit
				}
				child := filepath.Join(path, name)
				if err = visit(child, entry, nil); err != nil {
					return err
				}
				if entry.IsDir() {
					if err = walk(child, depth+1); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				if allowGone && path != "." && os.IsNotExist(readErr) {
					if _, missingErr := root.Lstat(path); os.IsNotExist(missingErr) {
						return nil
					}
				}
				return errWorkerResourceLimit
			}
		}
		after, err := root.Lstat(path)
		if allowGone && path != "." && os.IsNotExist(err) {
			return nil
		}
		if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink != 0 {
			return errWorkerResourceLimit
		}
		return nil
	}
	return walk(".", 0)
}
