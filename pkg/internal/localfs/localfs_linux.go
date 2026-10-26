//go:build linux

package localfs

import (
	"os"

	"golang.org/x/sys/unix"
)

// zfsSuperMagic is not among x/sys's constants.
const zfsSuperMagic = 0x2fc12fc1

var localMagic = map[int64]bool{
	unix.EXT4_SUPER_MAGIC:      true, // also ext2 and ext3
	unix.XFS_SUPER_MAGIC:       true,
	unix.BTRFS_SUPER_MAGIC:     true,
	unix.F2FS_SUPER_MAGIC:      true,
	unix.BCACHEFS_SUPER_MAGIC:  true,
	unix.TMPFS_MAGIC:           true,
	unix.RAMFS_MAGIC:           true,
	unix.OVERLAYFS_SUPER_MAGIC: true, // container root filesystems
	zfsSuperMagic:              true,
}

// localMagicType reports whether a statfs f_type is an allowlisted local type.
func localMagicType(fsType int64) bool { return localMagic[fsType] }

// magicOf reads statfs's f_type, which 32-bit Linux declares int32 (and
// s390x uint32): a magic above 0x7fffffff (Btrfs, F2FS, bcachefs) must not
// sign-extend.
func magicOf[T ~int32 | ~int64 | ~uint32](fsType T) int64 { return int64(uint32(fsType)) }

func isLocal(path string) (bool, error) {
	dir, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return false, err
	}
	return localMagicType(magicOf(stat.Type)), nil
}
