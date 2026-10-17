//go:build darwin || linux

package browser

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// validateHARPrivate proves an opened HAR staging object private: this
// effective user owns it, no group or other permission bit is set, and a
// regular file has exactly one link.
func validateHARPrivate(f *os.File, regular bool) error {
	if f == nil {
		return errHAROwnershipUnproven
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("HAR staging ownership or permissions are unsafe")
	}
	if regular && (!info.Mode().IsRegular() || stat.Nlink != 1) {
		return fmt.Errorf("HAR staging file is not a singly linked regular file")
	}
	if !regular && !info.IsDir() {
		return fmt.Errorf("HAR staging directory is not a directory")
	}
	return nil
}

// persistentHARIdentityAt is the identity a retirement record binds: the
// device, inode and owner of name in root, which info describes.
func persistentHARIdentityAt(_ *os.Root, _ string, info os.FileInfo) (harPersistentIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return harPersistentIdentity{}, errHAROwnershipUnproven
	}
	return harPersistentIdentity{Device: widenStatDevice(stat.Dev), Inode: stat.Ino, UID: stat.Uid}, nil
}

// validHARPersistentIdentity accepts only a Unix-shaped identity owned by
// this effective user.
func validHARPersistentIdentity(id harPersistentIdentity) bool {
	return id.Inode != 0 && id.UID == uint32(os.Geteuid()) && id.FileIDHigh == 0 && id.Owner == ""
}

// widenStatDevice reads Stat_t.Dev, which is an int32 on Darwin and a uint64
// on Linux.
func widenStatDevice[T ~int32 | ~uint64](device T) uint64 { return uint64(device) }

// pinHARPathIdentity returns info, whose identity os.Lstat already read at
// the call.
func pinHARPathIdentity(_ string, info os.FileInfo) (os.FileInfo, error) { return info, nil }

func validateHARVolume(string) error { return nil }

// createHARPrivateDir creates name in parent at 0700 (the umask only clears
// bits).
func createHARPrivateDir(parent *os.Root, name string) error { return parent.Mkdir(name, 0o700) }

// syncHARRoot makes root's directory entries durable.
func syncHARRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// renameHARNoReplace renames oldName to newName in root, refusing an
// existing newName. Callers hold the namespace lease, so nothing else
// creates newName between the check and the rename.
func renameHARNoReplace(root *os.Root, oldName, newName string) error {
	if _, err := root.Lstat(newName); !os.IsNotExist(err) {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	return root.Rename(oldName, newName)
}

// removeHAR removes name from root.
func removeHAR(root *os.Root, name string) error { return root.Remove(name) }

// replaceHAR renames oldName over newName in root.
func replaceHAR(root *os.Root, oldName, newName string) error { return root.Rename(oldName, newName) }
