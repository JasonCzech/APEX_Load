//go:build windows

package winfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// ErrUnsupportedVolume refuses a path on a remote, removable or otherwise
// unqualified volume, or on a filesystem the caller does not accept.
var ErrUnsupportedVolume = errors.New("unsupported Windows volume")

// VolumeOf reports the drive type and filesystem name (upper case) of the
// volume holding path.
func VolumeOf(path string) (driveType uint32, filesystem string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, "", err
	}
	pathPtr, err := windows.UTF16PtrFromString(ExtendedPath(abs))
	if err != nil {
		return 0, "", err
	}
	// A volume can be mounted below a long directory path, so the result
	// buffer is not MAX_PATH-sized.
	volumePath := make([]uint16, 32768)
	if err := windows.GetVolumePathName(pathPtr, &volumePath[0], uint32(len(volumePath))); err != nil {
		return 0, "", fmt.Errorf("resolve volume of %s: %w", path, err)
	}
	volumePtr, err := windows.UTF16PtrFromString(windows.UTF16ToString(volumePath))
	if err != nil {
		return 0, "", err
	}
	name := make([]uint16, 32)
	if err := windows.GetVolumeInformation(volumePtr, nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil {
		return 0, "", fmt.Errorf("inspect volume of %s: %w", path, err)
	}
	return windows.GetDriveType(volumePtr), strings.ToUpper(strings.TrimSpace(windows.UTF16ToString(name))), nil
}

// ValidateVolume accepts only a local fixed drive whose filesystem is one of
// filesystems (NTFS when none are named).
func ValidateVolume(driveType uint32, filesystem string, filesystems ...string) error {
	if len(filesystems) == 0 {
		filesystems = []string{"NTFS"}
	}
	if driveType != windows.DRIVE_FIXED {
		return fmt.Errorf("%w: the volume is not a local fixed drive (drive type %d)", ErrUnsupportedVolume, driveType)
	}
	name := strings.ToUpper(strings.TrimSpace(filesystem))
	if !slices.ContainsFunc(filesystems, func(allowed string) bool { return strings.EqualFold(allowed, name) }) {
		return fmt.Errorf("%w: the filesystem is %s, not %s", ErrUnsupportedVolume, name, strings.Join(filesystems, " or "))
	}
	return nil
}

// ValidateLocalVolume accepts path only on a local fixed volume whose
// filesystem is one of filesystems (NTFS when none are named). UNC and
// network paths are refused before the volume is inspected.
func ValidateLocalVolume(path string, filesystems ...string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(abs, `\\`) {
		// Only the extended form of a drive-letter path is local; UNC,
		// \\?\UNC\ and device paths are not.
		rest := strings.TrimPrefix(abs, `\\?\`)
		if rest == abs || len(rest) < 2 || rest[1] != ':' {
			return fmt.Errorf("%w: %s is a network or device path", ErrUnsupportedVolume, path)
		}
	}
	driveType, filesystem, err := VolumeOf(abs)
	if err != nil {
		return err
	}
	return ValidateVolume(driveType, filesystem, filesystems...)
}
