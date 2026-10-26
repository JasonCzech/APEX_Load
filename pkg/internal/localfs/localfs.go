// Package localfs decides whether a directory lives on a filesystem whose
// writes are synchronous system calls of the writing process on this host.
//
// Only then does a writer process's exit prove that nothing more will be
// written there. A network or user-space filesystem (NFS, SMB/CIFS, EFS,
// Azure Files, Ceph, GlusterFS, 9p, virtiofs, FUSE such as rclone or
// blobfuse2) can complete a write after the process that issued it has been
// reaped, so it is never local. The decision is an allowlist: an unknown
// type is not local.
//
// The directory is opened and judged through that handle, following
// symbolic links as the writers' own os.OpenRoot does, so a link or mount
// point at the directory is judged by where it actually leads. Only the
// directory itself is judged: a filesystem mounted below it is not seen.
package localfs

import (
	"errors"
	"strings"
)

// ErrUnknown reports a platform without a filesystem-type probe.
var ErrUnknown = errors.New("filesystem locality is unknown on this platform")

// IsLocal reports whether the directory at path is on an allowlisted local
// filesystem. A missing path is an error, never local.
func IsLocal(path string) (bool, error) { return isLocal(path) }

// windowsDriveFixed is GetDriveType's DRIVE_FIXED.
const windowsDriveFixed = 3

// windowsVolumeLocal reports whether a Windows volume is local by the same
// policy as winfs.ValidateVolume(driveType, filesystem, "NTFS", "REFS"): a
// fixed drive formatted NTFS or ReFS. A user-space filesystem (WinFsp, as
// rclone or sshfs-win mount by default) reports a fixed drive with its own
// filesystem name, and FAT/exFAT volumes are refused too.
func windowsVolumeLocal(driveType uint32, filesystem string) bool {
	name := strings.ToUpper(strings.TrimSpace(filesystem))
	return driveType == windowsDriveFixed && (name == "NTFS" || name == "REFS")
}

// isUNC reports whether a Windows path names a network share: \\server\share
// or the extended \\?\UNC\server\share. Device paths (\\?\C:\, \\.\C:\) are
// local drive paths, not shares.
func isUNC(path string) bool {
	path = strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(strings.ToUpper(path), `\\?\UNC\`) {
		return true
	}
	return strings.HasPrefix(path, `\\`) && !strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(path, `\\.\`)
}
