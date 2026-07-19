//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replaceWindowsFileOperation is replaceable by Windows-only regression tests
// so they can move a different inode onto the selected pathname at the exact
// publication boundary. Production always calls ReplaceFileW.
var replaceWindowsFileOperation = replaceWindowsFile

// moveWindowsFileOperation is replaceable by Windows-only regression tests so
// they can substitute the staging pathname at the exact new-file publication
// boundary. Production always calls MoveFileExW.
var moveWindowsFileOperation = windows.MoveFileEx

func secureWriteGrantedFile(grant writePathGrant, data []byte) error {
	return secureWriteGrantedFileFrom(grant, bytes.NewReader(data), int64(len(data)))
}

func secureWriteGrantedFileFrom(grant writePathGrant, src io.Reader, maxBytes int64) error {
	if src == nil {
		return fmt.Errorf("saved file source is required")
	}
	parent, err := lockWindowsSaveParent(grant)
	if err != nil {
		return err
	}
	defer parent.Close()
	temp, err := createPrivateWindowsSaveTemp(grant.canonicalParent)
	if err != nil {
		return fmt.Errorf("create atomic save file: %w", err)
	}
	tempName := temp.Name()
	tempClosed := false
	discardStaging := true
	defer func() {
		if discardStaging && !tempClosed {
			// Delete the exact file still referenced by the handle. Never remove the
			// staging pathname: another process may have replaced that directory
			// entry while the save was in progress.
			_ = temp.Truncate(0)
			_ = temp.Sync()
			_ = markWindowsFileForDeletion(temp)
		}
		if !tempClosed {
			_ = temp.Close()
		}
	}()
	// ReplaceFileW opens its replacement with no sharing mode, so the writable
	// staging handle cannot remain open during publication. Retain a zero-access,
	// share-all handle instead: it pins the exact file identity without conflicting
	// with ReplaceFileW's internal read/write/delete open.
	stagingIdentityHandle, err := openWindowsSaveIdentityHandle(tempName)
	if err != nil {
		return fmt.Errorf("pin atomic save file identity: %w", err)
	}
	defer stagingIdentityHandle.Close()
	reader := src
	if maxBytes > 0 && maxBytes < math.MaxInt64 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	written, err := io.Copy(temp, reader)
	if err != nil {
		return fmt.Errorf("write saved file: %w", err)
	}
	if maxBytes > 0 && written > maxBytes {
		return fmt.Errorf("saved file exceeds the %d-byte limit", maxBytes)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync saved file: %w", err)
	}
	replacementIdentity, err := stagingIdentityHandle.Stat()
	if err != nil {
		return fmt.Errorf("identify saved file staging inode: %w", err)
	}
	writableIdentity, err := temp.Stat()
	if err != nil || !os.SameFile(replacementIdentity, writableIdentity) {
		if err != nil {
			return fmt.Errorf("verify saved file staging inode: %w", err)
		}
		return fmt.Errorf("saved file staging identity changed before publication")
	}
	target := filepath.Join(grant.canonicalParent, grant.name)
	info, statErr := os.Lstat(target)
	if grant.targetExisted {
		if statErr != nil {
			return fmt.Errorf("verify existing save destination: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(grant.targetIdentity, info) {
			return fmt.Errorf("refusing to overwrite because the selected save destination was replaced")
		}
	} else if statErr == nil {
		return fmt.Errorf("refusing to overwrite a save destination created after selection")
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("verify new save destination: %w", statErr)
	}
	// Re-verify through the still-open directory descriptor. The handle was
	// opened without FILE_SHARE_DELETE, preventing a junction/directory rename
	// from replacing the selected parent while the pathname-based Win32 atomic
	// publication runs.
	parentInfo, err := parent.Stat()
	if err != nil || !parentInfo.IsDir() || !os.SameFile(grant.parentIdentity, parentInfo) {
		if err != nil {
			return fmt.Errorf("verify granted save directory: %w", err)
		}
		return fmt.Errorf("refusing to write because the selected save directory was replaced")
	}
	// Once a pathname-based publication primitive is called, the staging inode's
	// location can no longer be inferred safely from the return value alone. Keep
	// every unverified result instead of deleting a possibly published file.
	discardStaging = false
	if err := temp.Close(); err != nil {
		tempClosed = true
		return fmt.Errorf("close writable saved file handle: %w", err)
	}
	tempClosed = true
	if grant.targetExisted {
		_, publishErr := publishExistingWindowsSave(grant, target, tempName, replacementIdentity)
		if publishErr != nil {
			return fmt.Errorf("publish saved file: %w", publishErr)
		}
	} else {
		from, err := windows.UTF16PtrFromString(tempName)
		if err != nil {
			return err
		}
		to, err := windows.UTF16PtrFromString(target)
		if err != nil {
			return err
		}
		if err := moveWindowsFileOperation(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
			return fmt.Errorf("publish saved file: %w", err)
		}
		publishedIdentity, err := os.Lstat(target)
		if err != nil || publishedIdentity.Mode()&os.ModeSymlink != 0 || !publishedIdentity.Mode().IsRegular() || !os.SameFile(replacementIdentity, publishedIdentity) {
			if err != nil {
				return fmt.Errorf("published file could not be verified; all files were retained for recovery: %w", err)
			}
			return fmt.Errorf("published file identity does not match the staged save; all files were retained for recovery")
		}
	}
	return nil
}

// publishExistingWindowsSave uses ReplaceFileW's backup rename as an atomic
// compare-after-swap operation. The pathname can change after the preliminary
// Lstat above; validating the displaced file, rather than checking the pathname
// one more time, closes that final check/replace race.
func publishExistingWindowsSave(grant writePathGrant, target, replacement string, replacementIdentity os.FileInfo) (consumed bool, err error) {
	backup, err := reserveWindowsReplacementBackupPath(grant.canonicalParent)
	if err != nil {
		return false, fmt.Errorf("reserve displaced destination backup: %w", err)
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return false, err
	}
	replacementPtr, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return false, err
	}
	backupPtr, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return false, err
	}

	// ReplaceFile preserves the destination's DACL (along with encryption,
	// compression, and other metadata). Do not pass either of the ignore-ACL
	// flags: failure to carry the existing protection forward must fail the save
	// rather than silently widening access. Supplying the random backup also
	// ensures a displaced destination is never destroyed by the operation.
	if err := replaceWindowsFileOperation(targetPtr, replacementPtr, backupPtr); err != nil {
		return false, fmt.Errorf("replace destination (a displaced file, if any, was preserved at %q): %w", backup, err)
	}

	displacedIdentity, displacedErr := os.Lstat(backup)
	displacedMatches := displacedErr == nil && displacedIdentity.Mode()&os.ModeSymlink == 0 && displacedIdentity.Mode().IsRegular() && os.SameFile(grant.targetIdentity, displacedIdentity)
	publishedIdentity, publishedErr := os.Lstat(target)
	publishedMatches := publishedErr == nil && publishedIdentity.Mode()&os.ModeSymlink == 0 && publishedIdentity.Mode().IsRegular() && os.SameFile(replacementIdentity, publishedIdentity)
	if displacedMatches && publishedMatches {
		if err := removeWindowsFileIfSame(backup, grant.targetIdentity); err != nil {
			return true, fmt.Errorf("saved file was published but its verified prior version was preserved at %q: %w", backup, err)
		}
		return true, nil
	}

	rollbackBackup, reserveErr := reserveWindowsReplacementBackupPath(grant.canonicalParent)
	if reserveErr != nil {
		return true, fmt.Errorf("selected save destination was replaced; the displaced file was preserved at %q but rollback staging failed: %w", backup, reserveErr)
	}
	rollbackBackupPtr, err := windows.UTF16PtrFromString(rollbackBackup)
	if err != nil {
		return true, fmt.Errorf("selected save destination was replaced; the displaced file was preserved at %q but rollback staging failed: %w", backup, err)
	}
	// Atomically put the displaced destination back. A second backup is required
	// so even a target changed yet again during validation is preserved rather
	// than deleted by rollback.
	if err := replaceWindowsFileOperation(targetPtr, backupPtr, rollbackBackupPtr); err != nil {
		return true, fmt.Errorf("selected save destination was replaced; rollback failed and the displaced file was preserved at %q: %w", backup, err)
	}

	restoredIdentity, restoredErr := os.Lstat(target)
	restoredMatches := displacedErr == nil && restoredErr == nil &&
		displacedIdentity.Mode()&os.ModeSymlink == 0 && displacedIdentity.Mode().IsRegular() &&
		restoredIdentity.Mode()&os.ModeSymlink == 0 && restoredIdentity.Mode().IsRegular() &&
		os.SameFile(displacedIdentity, restoredIdentity)
	rolledBackIdentity, rollbackStatErr := os.Lstat(rollbackBackup)
	rolledBackStaging := rollbackStatErr == nil && rolledBackIdentity.Mode()&os.ModeSymlink == 0 && rolledBackIdentity.Mode().IsRegular() && os.SameFile(replacementIdentity, rolledBackIdentity)
	if rolledBackStaging {
		if err := removeWindowsFileIfSame(rollbackBackup, replacementIdentity); err != nil {
			return true, fmt.Errorf("save source or destination changed during publication; the staged replacement was preserved at %q after rollback: %w", rollbackBackup, err)
		}
	}
	if !restoredMatches {
		return true, fmt.Errorf("save source or destination changed during publication; rollback completed but the displaced destination could not be verified (destination=%v, source=%v, restored=%v); recovery files were retained", displacedErr, publishedErr, restoredErr)
	}
	if !rolledBackStaging {
		if rollbackStatErr != nil {
			return true, fmt.Errorf("save source or destination changed during publication and was rolled back; the rollback backup could not be verified at %q: %w", rollbackBackup, rollbackStatErr)
		}
		return true, fmt.Errorf("save source or destination changed during publication and was rolled back; a concurrently displaced file was preserved at %q", rollbackBackup)
	}
	return true, fmt.Errorf("save source or destination changed during publication; the replacement was rolled back")
}

func createPrivateWindowsSaveTemp(parent string) (*os.File, error) {
	return createPrivateWindowsSaveFile(
		parent,
		".apexload-save-",
		".tmp",
		0,
		// A zero-access identity handle is opened immediately after creation and
		// retained through publication. Share all categories so that handle can be
		// opened and so ReplaceFileW can later acquire its required access.
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
	)
}

func openWindowsSaveIdentityHandle(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("invalid descriptor")
	}
	return file, nil
}

// reserveWindowsReplacementBackupPath proves the random backup name was absent
// by creating it with CREATE_NEW and no sharing. DELETE_ON_CLOSE removes that
// reservation atomically when the exclusive handle closes, leaving a fresh,
// unpredictable pathname for ReplaceFileW.
func reserveWindowsReplacementBackupPath(parent string) (string, error) {
	reservation, err := createPrivateWindowsSaveFile(parent, ".apexload-save-backup-", ".bak", windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		return "", err
	}
	name := reservation.Name()
	if err := reservation.Close(); err != nil {
		return "", err
	}
	return name, nil
}

func createPrivateWindowsSaveFile(parent, prefix, suffix string, extraFlags, shareMode uint32) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	var pinner runtime.Pinner
	pinner.Pin(user.User.Sid)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	if err := descriptor.SetDACL(acl, true, false); err != nil {
		return nil, err
	}
	if err := descriptor.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return nil, err
	}
	security := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}

	for attempt := 0; attempt < 128; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(parent, prefix+hex.EncodeToString(random[:])+suffix)
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(
			path,
			windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE,
			shareMode,
			security,
			windows.CREATE_NEW,
			windows.FILE_ATTRIBUTE_NORMAL|extraFlags,
			0,
		)
		runtime.KeepAlive(descriptor)
		runtime.KeepAlive(acl)
		if err == windows.ERROR_FILE_EXISTS || err == windows.ERROR_ALREADY_EXISTS {
			continue
		}
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(handle), name)
		if file == nil {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("invalid descriptor")
		}
		return file, nil
	}
	return nil, fmt.Errorf("too many temporary filename collisions")
}

func markWindowsFileForDeletion(file *os.File) error {
	if file == nil {
		return fmt.Errorf("file handle is required")
	}
	deleteFile := byte(1)
	return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfo, &deleteFile, 1)
}

func replaceWindowsFile(target, replacement, backup *uint16) error {
	backupAddress := uintptr(0)
	if backup != nil {
		backupAddress = uintptr(unsafe.Pointer(backup))
	}
	result, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(unsafe.Pointer(replacement)),
		backupAddress,
		0,
		0,
		0,
	)
	runtime.KeepAlive(target)
	runtime.KeepAlive(replacement)
	runtime.KeepAlive(backup)
	if result != 0 {
		return nil
	}
	if errno, ok := callErr.(windows.Errno); ok && errno == 0 {
		return fmt.Errorf("ReplaceFileW returned zero without an error")
	}
	return callErr
}

// removeWindowsFileIfSame performs the identity check and deletion through one
// handle opened without FILE_SHARE_DELETE. Once the handle is open, no rename
// can substitute a different file between verification and delete-on-close.
func removeWindowsFileIfSame(path string, identity os.FileInfo) error {
	if identity == nil {
		return fmt.Errorf("expected file identity is unavailable")
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return fmt.Errorf("invalid descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(identity, info) {
		_ = file.Close()
		return fmt.Errorf("file identity changed before cleanup")
	}
	deleteFile := byte(1)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, 1); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func lockWindowsSaveParent(grant writePathGrant) (*os.File, error) {
	path, err := windows.UTF16PtrFromString(grant.canonicalParent)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		path,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("lock granted save directory: %w", err)
	}
	parent := os.NewFile(uintptr(handle), grant.canonicalParent)
	if parent == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("lock granted save directory: invalid descriptor")
	}
	info, err := parent.Stat()
	if err != nil {
		_ = parent.Close()
		return nil, fmt.Errorf("verify granted save directory: %w", err)
	}
	if !info.IsDir() || !os.SameFile(grant.parentIdentity, info) {
		_ = parent.Close()
		return nil, fmt.Errorf("refusing to write because the selected save directory was replaced")
	}
	return parent, nil
}
