//go:build windows

package localfs

import (
	"strings"

	"golang.org/x/sys/windows"
)

// volumeNameDOS asks GetFinalPathNameByHandle for a drive-letter (or
// \\?\UNC\) path; x/sys does not name it.
const volumeNameDOS = 0x0

// isLocal opens the directory and asks Windows for its final path, so a
// directory symbolic link or junction to a share is judged by its target.
func isLocal(path string) (bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), volumeNameDOS)
	if err != nil {
		return false, err
	}
	final := windows.UTF16ToString(buffer[:n])
	if isUNC(final) {
		return false, nil
	}
	final = strings.TrimPrefix(final, `\\?\`)
	target, err := windows.UTF16PtrFromString(final)
	if err != nil {
		return false, err
	}
	volume := make([]uint16, windows.MAX_LONG_PATH)
	if err = windows.GetVolumePathName(target, &volume[0], uint32(len(volume))); err != nil {
		return false, err
	}
	if isUNC(windows.UTF16ToString(volume)) {
		return false, nil
	}
	// The filesystem name comes from the same open handle.
	filesystem := make([]uint16, windows.MAX_PATH+1)
	if err = windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return false, err
	}
	return windowsVolumeLocal(windows.GetDriveType(&volume[0]), windows.UTF16ToString(filesystem)), nil
}
