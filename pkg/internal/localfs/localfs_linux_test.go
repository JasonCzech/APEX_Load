//go:build linux

package localfs

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestLocalMagicTypesAreAnAllowlist(t *testing.T) {
	for name, c := range map[string]struct {
		magic int64
		local bool
	}{
		"ext4":      {unix.EXT4_SUPER_MAGIC, true},
		"xfs":       {unix.XFS_SUPER_MAGIC, true},
		"btrfs":     {unix.BTRFS_SUPER_MAGIC, true},
		"tmpfs":     {unix.TMPFS_MAGIC, true},
		"overlayfs": {unix.OVERLAYFS_SUPER_MAGIC, true},
		"zfs":       {zfsSuperMagic, true},
		"nfs":       {unix.NFS_SUPER_MAGIC, false},
		"smb":       {unix.SMB_SUPER_MAGIC, false},
		"smb2":      {unix.SMB2_SUPER_MAGIC, false},
		"cifs":      {unix.CIFS_SUPER_MAGIC, false},
		"fuse":      {unix.FUSE_SUPER_MAGIC, false},
		"9p":        {unix.V9FS_MAGIC, false},
		"ceph":      {unix.CEPH_SUPER_MAGIC, false},
		"virtiofs":  {0x6a656a63, false},
		"unknown":   {0x12345678, false},
	} {
		if got := localMagicType(c.magic); got != c.local {
			t.Errorf("%s: local=%v, want %v", name, got, c.local)
		}
	}
	// 32-bit Linux reports f_type as int32: Btrfs, F2FS and bcachefs arrive
	// negative and must not sign-extend.
	for name, magic := range map[string]int64{"btrfs": unix.BTRFS_SUPER_MAGIC, "f2fs": unix.F2FS_SUPER_MAGIC, "bcachefs": unix.BCACHEFS_SUPER_MAGIC} {
		if got := magicOf(int32(uint32(magic))); got != magic || !localMagicType(got) {
			t.Errorf("%s as a 32-bit f_type: %#x", name, got)
		}
	}
	if magicOf(uint32(unix.BTRFS_SUPER_MAGIC)) != unix.BTRFS_SUPER_MAGIC {
		t.Error("an unsigned 32-bit f_type (s390x) changed")
	}
	if magicOf(int64(unix.EXT4_SUPER_MAGIC)) != unix.EXT4_SUPER_MAGIC {
		t.Error("64-bit f_type changed")
	}
}
