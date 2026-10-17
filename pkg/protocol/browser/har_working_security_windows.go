//go:build windows

package browser

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
	"golang.org/x/sys/windows"
)

// validateHARPrivate proves an opened HAR staging object private: owned by
// this token's user or default owner, a DACL granting only that user and
// SYSTEM, no reparse point, and a regular file with exactly one link.
func validateHARPrivate(f *os.File, regular bool) error {
	if f == nil {
		return errHAROwnershipUnproven
	}
	if err := winfs.CheckPrivate(f, regular); err != nil {
		return fmt.Errorf("HAR staging ownership or permissions are unsafe: %w", err)
	}
	return nil
}

// persistentHARIdentityAt is the identity a retirement record binds, read
// from a handle to name in root (which info must describe): the volume
// serial number, the 128-bit file ID and the owner SID.
func persistentHARIdentityAt(root *os.Root, name string, info os.FileInfo) (harPersistentIdentity, error) {
	if root == nil || info == nil || !validHARComponent(name) {
		return harPersistentIdentity{}, errHAROwnershipUnproven
	}
	dir, err := root.Open(".")
	if err != nil {
		return harPersistentIdentity{}, err
	}
	defer dir.Close()
	object, err := winfs.OpenAt(dir, name, windows.READ_CONTROL, false)
	if err != nil {
		return harPersistentIdentity{}, err
	}
	defer object.Close()
	opened, err := object.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return harPersistentIdentity{}, errors.Join(err, errHAROwnershipUnproven)
	}
	id, err := winfs.FileID(object)
	if err != nil {
		return harPersistentIdentity{}, errors.Join(err, errHAROwnershipUnproven)
	}
	owner, err := winfs.Owner(object)
	if err != nil || !winfs.OwnedByCurrentToken(owner) {
		return harPersistentIdentity{}, errors.Join(err, errHAROwnershipUnproven)
	}
	return harPersistentIdentity{Device: id.Volume, Inode: id.Low, FileIDHigh: id.High, Owner: owner}, nil
}

// validHARPersistentIdentity accepts only a Windows-shaped identity owned
// by this token.
func validHARPersistentIdentity(id harPersistentIdentity) bool {
	return (id.Inode != 0 || id.FileIDHigh != 0) && id.UID == 0 && winfs.OwnedByCurrentToken(id.Owner)
}

// pinHARPathIdentity re-stats path through a handle, so the identity is the
// object the path names now: os.Lstat reads the file ID lazily, at
// os.SameFile, from whatever the path names by then.
func pinHARPathIdentity(path string, _ os.FileInfo) (os.FileInfo, error) { return winfs.Lstat(path) }

// validateHARVolume admits only a local fixed NTFS volume.
func validateHARVolume(path string) error { return winfs.ValidateLocalVolume(path) }

// createHARPrivateDir creates name in parent with the private DACL as part
// of the create; everything below it inherits that DACL.
func createHARPrivateDir(parent *os.Root, name string) error {
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	created, err := winfs.CreatePrivateDirAt(dir, name)
	if err == nil {
		err = created.Close()
	}
	return errors.Join(err, dir.Close())
}

// syncHARRoot flushes root's directory entries.
func syncHARRoot(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(winfs.SyncDir(dir), dir.Close())
}

// retryHAR retries a namespace mutation while another handle (an antivirus
// or indexing scan, a closing handle) holds the object, for at most
// runtimeHARCloseWait; past that the mutation fails closed. also names
// further errors that such a handle causes.
func retryHAR(op func() error, also ...error) error {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	defer cancel()
	err := winfs.Retry(ctx, op, also...)
	if err != nil && ctx.Err() != nil {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	return err
}

// renameHARNoReplace renames oldName to newName in root through one handle
// to the object, never replacing an existing newName. A directory cannot be
// renamed while any handle to a child is open, which Windows reports as
// ERROR_ACCESS_DENIED: a scan holding one of the generation's files for a
// moment is waited out like a sharing violation.
func renameHARNoReplace(root *os.Root, oldName, newName string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	err = retryHAR(func() error { return winfs.RenameAt(dir, oldName, newName) }, windows.ERROR_ACCESS_DENIED)
	if os.IsExist(err) {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	return err
}

// removeHAR removes name from root with POSIX semantics (the name is gone
// at once); a name another handle left delete-pending counts as removed,
// and a directory still holding such an entry is retried until it empties.
func removeHAR(root *os.Root, name string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	// Delete-pending children leave a directory non-empty until their last
	// handle closes.
	return retryHAR(func() error { return winfs.RemoveAt(dir, name) }, windows.ERROR_DIR_NOT_EMPTY)
}

// replaceHAR renames oldName over newName in root.
func replaceHAR(root *os.Root, oldName, newName string) error {
	return retryHAR(func() error { return root.Rename(oldName, newName) })
}
