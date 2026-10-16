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
	return harPersistentIdentity{Device: widenStatDevice(stat.Dev), Inode: stat.Ino, UID: stat.Uid}, nil
}

// widenStatDevice reads Stat_t.Dev, which is an int32 on Darwin and a uint64
// on Linux.
func widenStatDevice[T ~int32 | ~uint64](device T) uint64 { return uint64(device) }
