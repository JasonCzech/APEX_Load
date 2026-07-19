//go:build windows

package recorder

import "golang.org/x/sys/windows"

// MOVEFILE_WRITE_THROUGH asks Windows to flush the atomic replacement before
// returning. The staging file lives in the destination directory, so this never
// falls back to a cross-volume copy.
func replacePersistentCAFile(from, to string) error {
	fromUTF16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toUTF16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromUTF16, toUTF16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
