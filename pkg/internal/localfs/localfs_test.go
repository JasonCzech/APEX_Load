package localfs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The test's temporary directory is on the runner's local disk (APFS, ext4,
// overlay, tmpfs or NTFS); a missing path is an error, never local. A TMPDIR
// on a shared filesystem makes this test fail, not skip: point TMPDIR at a
// local disk.
func TestIsLocalOnTheTemporaryDirectory(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		if local, err := IsLocal(t.TempDir()); local || err == nil {
			t.Fatal("an unprobed platform reported a local filesystem")
		}
		return
	}
	local, err := IsLocal(t.TempDir())
	if err != nil || !local {
		t.Fatalf("temporary directory: local=%v err=%v", local, err)
	}
	if local, err = IsLocal(filepath.Join(t.TempDir(), "missing")); local || !os.IsNotExist(err) {
		t.Fatalf("missing path: local=%v err=%v", local, err)
	}
}

func TestIsUNCRecognizesNetworkShares(t *testing.T) {
	for path, unc := range map[string]bool{
		`\\server\share\analysis`:       true,
		`//server/share/analysis`:       true,
		`\\?\UNC\server\share\analysis`: true,
		`\\?\unc\server\share`:          true,
		`\\?\C:\archive\_analysis`:      false,
		`\\.\C:\archive`:                false,
		`C:\archive\_analysis`:          false,
		`/var/lib/apexload/archive`:     false,
		`\\?\Volume{1234}\archive`:      false,
	} {
		if got := isUNC(path); got != unc {
			t.Errorf("%s: UNC=%v, want %v", path, got, unc)
		}
	}
}

// The Windows volume policy: a fixed NTFS or ReFS drive only.
func TestWindowsVolumeLocalIsFixedNTFSOrReFS(t *testing.T) {
	const removable, remote, ramdisk = 2, 4, 6
	for name, c := range map[string]struct {
		driveType  uint32
		filesystem string
		local      bool
	}{
		"fixed NTFS":          {windowsDriveFixed, "NTFS", true},
		"fixed ReFS":          {windowsDriveFixed, "ReFS", true},
		"fixed ntfs, padded":  {windowsDriveFixed, " ntfs ", true},
		"WinFsp rclone mount": {windowsDriveFixed, "FUSE-rclone", false},
		"WinFsp default":      {windowsDriveFixed, "WinFsp-FUSE", false},
		"fixed exFAT":         {windowsDriveFixed, "exFAT", false},
		"fixed FAT32":         {windowsDriveFixed, "FAT32", false},
		"removable NTFS":      {removable, "NTFS", false},
		"network NTFS":        {remote, "NTFS", false},
		"RAM disk NTFS":       {ramdisk, "NTFS", false},
		"fixed, unnamed":      {windowsDriveFixed, "", false},
	} {
		if got := windowsVolumeLocal(c.driveType, c.filesystem); got != c.local {
			t.Errorf("%s: local=%v, want %v", name, got, c.local)
		}
	}
}

// LOCALFS_EXPECT_SHARED names a directory on a network or host-shared
// filesystem (for example a Docker Desktop bind mount, which is virtiofs or
// FUSE inside the VM). It, and a symbolic link to it from a local directory,
// must never read as local.
func TestIsLocalRefusesASharedFilesystem(t *testing.T) {
	path := os.Getenv("LOCALFS_EXPECT_SHARED")
	if path == "" {
		t.Skip("LOCALFS_EXPECT_SHARED is not set")
	}
	if local, err := IsLocal(path); local || err != nil {
		t.Fatalf("shared filesystem %s: local=%v err=%v", path, local, err)
	}
	link := filepath.Join(t.TempDir(), "_analysis")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if local, err := IsLocal(link); local || err != nil {
		t.Fatalf("link to shared filesystem: local=%v err=%v", local, err)
	}
}
