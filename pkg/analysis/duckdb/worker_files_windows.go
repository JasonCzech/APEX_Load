//go:build windows

package duckdb

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func workerCurrentSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid == nil {
		return "", errWorkerOwnership
	}
	return user.User.Sid.String(), nil
}
func workerCreateScratchDirectory(parent, prefix string, owner workerScratchOwner) (string, error) {
	sid, err := workerCurrentSID()
	if err != nil {
		return "", err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return "", errWorkerOwnership
	}
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	var random [16]byte
	for attempt := 0; attempt < 8; attempt++ {
		if _, err := rand.Read(random[:]); err != nil {
			return "", errWorkerProtocol
		}
		path := filepath.Join(parent, prefix+hex.EncodeToString(random[:]))
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return "", errWorkerProtocol
		}
		if err = windows.CreateDirectory(name, &security); err == windows.ERROR_ALREADY_EXISTS {
			continue
		} else if err != nil {
			return "", errWorkerOwnership
		}
		directory, err := os.Open(path)
		if err != nil {
			return path, errWorkerOwnership
		}
		owner.TrackOwner(directory)
		privateErr := workerPrivateDescriptor(directory)
		closeErr := owner.CloseOwner(directory)
		if privateErr != nil || closeErr != nil {
			return path, errWorkerOwnership
		}
		return path, nil
	}
	return "", errWorkerOwnership
}
func workerPrivateDescriptor(file *os.File) error {
	sid, err := workerCurrentSID()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errWorkerOwnership
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		return errWorkerOwnership
	}
	// Only the current process account and LOCAL_SYSTEM may read private inputs.
	// Windows mode bits are not an ACL and cannot establish this boundary.
	seenUser := false
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, index, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < 20 {
			return errWorkerOwnership
		}
		identity := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if identity != sid && identity != "S-1-5-18" {
			return errWorkerOwnership
		}
		if identity == sid {
			seenUser = true
		}
	}
	if !seenUser {
		return errWorkerOwnership
	}
	return nil
}
