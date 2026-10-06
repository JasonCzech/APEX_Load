//go:build darwin || linux

package browser

import (
	"fmt"
	"os"
	"syscall"
)

func validateHARPrivateInfo(info os.FileInfo, regular bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("HAR staging ownership or permissions are unsafe")
	}
	if regular && (!info.Mode().IsRegular() || stat.Nlink != 1) {
		return fmt.Errorf("HAR staging file is not a singly linked regular file")
	}
	return nil
}

func persistentHARIdentity(info os.FileInfo) (harPersistentIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return harPersistentIdentity{}, errHAROwnershipUnproven
	}
	return harPersistentIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), UID: stat.Uid}, nil
}
