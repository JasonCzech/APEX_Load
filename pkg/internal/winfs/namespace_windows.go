//go:build windows

package winfs

import (
	"context"
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrIdentityChanged refuses to remove a name that no longer names the
// expected object.
var ErrIdentityChanged = errors.New("the name no longer names the expected object")

const (
	fileRenameInformationEx = 65
	statusInvalidInfoClass  = windows.NTStatus(0xC0000003)
)

type fileRenameInformation struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

type fileDispositionInformationEx struct{ Flags uint32 }

// RenameAt renames oldName in dir to newName in the same directory through
// one handle to the object, never replacing an existing newName (which
// fails with fs.ErrExist) and never following a reparse point.
func RenameAt(dir *os.File, oldName, newName string) error {
	if err := validComponent(newName); err != nil {
		return err
	}
	object, err := OpenAt(dir, oldName, windows.DELETE, false)
	if err != nil {
		return err
	}
	defer object.Close()
	name, err := windows.UTF16FromString(newName)
	if err != nil {
		return err
	}
	length := (len(name) - 1) * 2
	buffer := make([]byte, int(unsafe.Offsetof(fileRenameInformation{}.FileName))+len(name)*2)
	info := (*fileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.Flags = windows.FILE_RENAME_POSIX_SEMANTICS
	info.RootDirectory = windows.Handle(dir.Fd())
	info.FileNameLength = uint32(length)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(windows.Handle(object.Fd()), &iosb, &buffer[0], uint32(len(buffer)), fileRenameInformationEx)
	if errors.Is(err, statusInvalidInfoClass) || errors.Is(err, windows.STATUS_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_NOT_SUPPORTED) {
		// A filesystem without the extended class: the classic form, whose
		// first field is the BOOLEAN ReplaceIfExists (false).
		info.Flags = 0
		err = windows.NtSetInformationFile(windows.Handle(object.Fd()), &iosb, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	}
	if err != nil {
		return &os.LinkError{Op: "renameat", Old: oldName, New: newName, Err: ntErrno(err)}
	}
	return nil
}

// RemoveIfSame removes name from dir only while it still names the object
// whose identity is want, through one handle with POSIX delete semantics
// (the name is gone at once even while other handles stay open). A name
// already delete-pending counts as removed; a missing one fails with
// fs.ErrNotExist.
func RemoveIfSame(dir *os.File, name string, want ID) error {
	if want.IsZero() {
		return errors.New("expected identity is required")
	}
	object, err := openAt(dir, name, windows.DELETE, false)
	if errors.Is(err, windows.STATUS_DELETE_PENDING) {
		return nil
	}
	if err != nil {
		return &os.PathError{Op: "remove", Path: name, Err: ntErrno(err)}
	}
	defer object.Close()
	id, err := FileID(object)
	if err != nil {
		return err
	}
	if id != want {
		return &os.PathError{Op: "remove", Path: name, Err: ErrIdentityChanged}
	}
	handle := windows.Handle(object.Fd())
	disposition := fileDispositionInformationEx{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE}
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(handle, &iosb, (*byte)(unsafe.Pointer(&disposition)), uint32(unsafe.Sizeof(disposition)), windows.FileDispositionInformationEx)
	if errors.Is(err, statusInvalidInfoClass) || errors.Is(err, windows.STATUS_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_NOT_SUPPORTED) {
		deleteFile := byte(1)
		return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, 1)
	}
	if err != nil {
		return &os.PathError{Op: "remove", Path: name, Err: ntErrno(err)}
	}
	return nil
}

// Transient reports whether err is a sharing, lock or delete-pending
// conflict that another handle (an antivirus or indexing scan, a closing
// handle) can cause and that clears by itself.
func Transient(err error) bool {
	for _, transient := range []error{windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_DELETE_PENDING, windows.STATUS_SHARING_VIOLATION, windows.STATUS_DELETE_PENDING} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}

// RetryInterval is the backoff between attempts of Retry.
const RetryInterval = 25 * time.Millisecond

// Retry runs op until it succeeds, fails with an error that is not
// Transient, or ctx ends; then it returns op's last error (joined with the
// context's when ctx ended first).
func Retry(ctx context.Context, op func() error) error {
	for {
		err := op()
		if err == nil || !Transient(err) {
			return err
		}
		timer := time.NewTimer(RetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}
