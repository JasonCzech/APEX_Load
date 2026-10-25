package duckdb

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Unlike fs.WalkDir/ReadDir, every directory descriptor is registered before
// its first operation and its actual Close result participates in ownership.
// Directory depth, entries and each ReadDir allocation are explicitly bounded.
func workerOwnedWalk(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error) error {
	return workerOwnedWalkMode(root, owner, visit, false)
}

// Quota observation permits only disappearance of previously enumerated nested
// entries in this exact owned scratch tree. It never supplies descriptor/process
// closure, excuses a replacement/symlink/I/O error only once the entry is then
// not found, and refuses a missing root.
//
// Windows deletes a directory only once its last handle closes; until then it
// is delete-pending and every open or stat of it, or of a name beneath it,
// fails with access denied, not "not found". An attempt's release removes its
// scratch inside the staging tree the pipeline monitor walks, so a nested
// directory the walk fails on is rechecked once the walk's own handle on it
// has closed: if it is gone, it disappeared like any other enumerated entry.
// When the directory the walk is in cannot be statted as itself either, only
// an ancestor can recheck, after the walk's handle on this one closes, so the
// failure goes up without waiting here. A directory that provably still exists, a refusal
// from the visit callback, a failed Close or a broken depth or entry bound
// fails the walk. One walk's rechecks share a single workerWalkVanishWait of
// polling, so the scratch's writer cannot stall the walk with entries that
// each vanish late.
func workerOwnedQuotaWalk(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error) error {
	return workerOwnedWalkMode(root, owner, visit, true)
}

// workerWalkVanishWait bounds how long one quota walk spends, in all, waiting
// for the nested directories it failed on to finish disappearing.
const workerWalkVanishWait = 500 * time.Millisecond

func workerOwnedWalkMode(root *os.Root, owner workerScratchOwner, visit func(string, fs.DirEntry, error) error, allowGone bool) error {
	var walk func(string, int) error
	count := 0
	// A settled failure fails the whole walk: no ancestor rechecks it.
	settled := false
	vanishWait := workerWalkVanishWait // what this walk's rechecks may still poll
	walk = func(path string, depth int) (retErr error) {
		if depth > 32 {
			settled = true
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
			if closeErr != nil {
				settled = true
			} else if ended, ok := owner.(interface{ forgetJoinedOwner(io.Closer) }); ok {
				ended.forgetJoinedOwner(file)
			}
		}()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(before, actual) {
			return errWorkerResourceLimit
		}
		if path == "." {
			count++
			if err = visit(path, fs.FileInfoToDirEntry(actual), nil); err != nil {
				settled = true
				return err
			}
		}
		for {
			entries, readErr := file.ReadDir(128)
			for _, entry := range entries {
				count++
				if count > 16384 {
					settled = true
					return errWorkerResourceLimit
				}
				name := entry.Name()
				if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
					settled = true
					return errWorkerResourceLimit
				}
				child := filepath.Join(path, name)
				if err = visit(child, entry, nil); err != nil {
					settled = true
					return err
				}
				if entry.IsDir() {
					if err = walk(child, depth+1); err != nil {
						if allowGone && !settled {
							if path != "." {
								if current, err := root.Lstat(path); err != nil || !os.SameFile(before, current) {
									return errWorkerResourceLimit
								}
							}
							gone, present, waited := workerWalkEntryVanished(root, child, vanishWait)
							vanishWait -= waited
							if gone {
								continue
							}
							settled = present
						}
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

// workerWalkEntryVanished polls path until it stops existing or wait is spent,
// checking at least once, and returns how long it waited. Only "not found" is
// gone: a delete-pending directory's access denied, or a replacement, is not.
// present reports that path could still be statted when the wait ran out.
func workerWalkEntryVanished(root *os.Root, path string, wait time.Duration) (gone, present bool, waited time.Duration) {
	began := time.Now()
	for {
		_, err := root.Lstat(path)
		if os.IsNotExist(err) {
			return true, false, time.Since(began)
		}
		remaining := wait - time.Since(began)
		if remaining <= 0 {
			return false, err == nil, time.Since(began)
		}
		time.Sleep(min(25*time.Millisecond, remaining))
	}
}
