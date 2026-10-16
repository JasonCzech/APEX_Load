//go:build windows

package winfs

import (
	"encoding/binary"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ID is a file's persistent identity: the volume serial number and the
// 128-bit file ID (FILE_ID_INFO). It survives renames on that volume and is
// never reused while the file exists.
type ID struct {
	Volume uint64
	Low    uint64
	High   uint64
}

// IsZero reports whether the ID is unset.
func (id ID) IsZero() bool { return id == ID{} }

type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// FileID reads an opened file's or directory's identity from its handle.
func FileID(f *os.File) (ID, error) {
	if f == nil {
		return ID{}, errors.New("file handle is required")
	}
	var info fileIDInfo
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return ID{}, err
	}
	id := ID{Volume: info.VolumeSerialNumber, Low: binary.LittleEndian.Uint64(info.FileID[:8]), High: binary.LittleEndian.Uint64(info.FileID[8:])}
	if id.Low == 0 && id.High == 0 {
		return ID{}, errors.New("the filesystem reports no file ID")
	}
	return id, nil
}

// OpenIdentity opens path for identity only: no data access, sharing read,
// write and delete, opening a directory and never following a reparse
// point. The handle conflicts with no other open, so it can stat an object
// other handles hold.
func OpenIdentity(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(ExtendedPath(path))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(pathPtr, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// Lstat stats path through an identity handle. Unlike os.Lstat, whose
// file ID is read lazily (at os.SameFile, from whatever the path names
// then), the result's identity is the object the path named at this call.
func Lstat(path string) (os.FileInfo, error) {
	f, err := OpenIdentity(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	return info, errors.Join(err, f.Close())
}
