//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSecureWriteGrantedFilePreservesExistingDACL(t *testing.T) {
	target := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	setWindowsTestDACL(t, target, []windows.EXPLICIT_ACCESS{
		windowsTestAccess(currentUser.User.Sid, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER),
		windowsTestAccess(everyone, windows.GENERIC_READ, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}, currentUser.User.Sid, everyone)
	beforeDescriptor, beforeDACL := windowsTestDACL(t, target)
	assertWindowsTestProtectedDACL(t, beforeDescriptor)
	before := windowsTestACLSignature(t, beforeDACL)

	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil {
		t.Fatal(err)
	} else if string(got) != "replacement" {
		t.Fatalf("saved contents = %q, want replacement", got)
	}
	assertNoWindowsSaveBackups(t, filepath.Dir(target))

	descriptor, dacl := windowsTestDACL(t, target)
	assertWindowsTestProtectedDACL(t, descriptor)
	if got := windowsTestACLSignature(t, dacl); !reflect.DeepEqual(got, before) {
		t.Fatalf("replacement DACL = %#v, want %#v", got, before)
	}
	if dacl.AceCount != 2 {
		t.Fatalf("preserved DACL ACE count = %d, want 2", dacl.AceCount)
	}
	assertWindowsTestACLHasSID(t, dacl, currentUser.User.Sid)
	assertWindowsTestACLHasSID(t, dacl, everyone)
}

func TestSecureWriteGrantedFileCreatesOwnerOnlyFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new.json")
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("sensitive")); err != nil {
		t.Fatal(err)
	}

	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, dacl := windowsTestDACL(t, target)
	assertWindowsTestProtectedDACL(t, descriptor)
	if dacl.AceCount != 1 {
		t.Fatalf("new-file DACL ACE count = %d, want 1", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("new-file ACE type = %d, want access-allowed", ace.Header.AceType)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(currentUser.User.Sid) {
		t.Fatal("new-file DACL grants an identity other than the current user")
	}
}

func TestSecureWriteGrantedFileRollsBackTargetReplacedAtPublication(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(target, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}

	originalOperation := replaceWindowsFileOperation
	defer func() { replaceWindowsFileOperation = originalOperation }()
	operationCalls := 0
	replaceWindowsFileOperation = func(targetPtr, replacementPtr, backupPtr *uint16) error {
		operationCalls++
		if operationCalls == 1 {
			targetPath := windows.UTF16PtrToString(targetPtr)
			if err := os.Rename(targetPath, targetPath+".selected"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(targetPath, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return originalOperation(targetPtr, replacementPtr, backupPtr)
	}

	err = secureWriteGrantedFile(grant, []byte("renderer"))
	if err == nil || !strings.Contains(err.Error(), "replaced") || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("publication race error = %v, want replaced/rolled back", err)
	}
	if operationCalls != 2 {
		t.Fatalf("ReplaceFileW calls = %d, want publish + rollback", operationCalls)
	}
	if contents, err := os.ReadFile(target); err != nil {
		t.Fatal(err)
	} else if string(contents) != "concurrent" {
		t.Fatalf("rolled-back destination = %q, want concurrent file", contents)
	}
	assertNoWindowsSaveBackups(t, directory)
}

func TestSecureWriteGrantedFilePreservesDisplacedTargetWhenRollbackFails(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(target, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}

	originalOperation := replaceWindowsFileOperation
	defer func() { replaceWindowsFileOperation = originalOperation }()
	operationCalls := 0
	injectedRollbackError := errors.New("injected rollback failure")
	replaceWindowsFileOperation = func(targetPtr, replacementPtr, backupPtr *uint16) error {
		operationCalls++
		switch operationCalls {
		case 1:
			targetPath := windows.UTF16PtrToString(targetPtr)
			if err := os.Rename(targetPath, targetPath+".selected"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(targetPath, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			return originalOperation(targetPtr, replacementPtr, backupPtr)
		case 2:
			return injectedRollbackError
		default:
			t.Fatalf("unexpected ReplaceFileW call %d", operationCalls)
			return nil
		}
	}

	err = secureWriteGrantedFile(grant, []byte("renderer"))
	if err == nil || !strings.Contains(err.Error(), "rollback failed") || !errors.Is(err, injectedRollbackError) {
		t.Fatalf("rollback failure error = %v", err)
	}
	if contents, err := os.ReadFile(target); err != nil {
		t.Fatal(err)
	} else if string(contents) != "renderer" {
		t.Fatalf("published destination after failed rollback = %q, want staged replacement", contents)
	}
	backups, err := filepath.Glob(filepath.Join(directory, ".apexload-save-backup-*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("preserved displaced backups = %v, want exactly one", backups)
	}
	if contents, err := os.ReadFile(backups[0]); err != nil {
		t.Fatal(err)
	} else if string(contents) != "concurrent" {
		t.Fatalf("preserved displaced destination = %q, want concurrent file", contents)
	}
}

func TestSecureWriteGrantedFileDoesNotDeleteRecreatedStagingPath(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(target, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}

	originalOperation := replaceWindowsFileOperation
	defer func() { replaceWindowsFileOperation = originalOperation }()
	operationCalls := 0
	stagingPath := ""
	replaceWindowsFileOperation = func(targetPtr, replacementPtr, backupPtr *uint16) error {
		operationCalls++
		switch operationCalls {
		case 1:
			targetPath := windows.UTF16PtrToString(targetPtr)
			stagingPath = windows.UTF16PtrToString(replacementPtr)
			if err := os.Rename(targetPath, targetPath+".selected"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(targetPath, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			return originalOperation(targetPtr, replacementPtr, backupPtr)
		case 2:
			if err := originalOperation(targetPtr, replacementPtr, backupPtr); err != nil {
				return err
			}
			// ReplaceFileW consumed the original staging name. Recreate an
			// unrelated file there before the helper returns to its caller.
			return os.WriteFile(stagingPath, []byte("foreign"), 0o600)
		default:
			t.Fatalf("unexpected ReplaceFileW call %d", operationCalls)
			return nil
		}
	}

	err = secureWriteGrantedFile(grant, []byte("renderer"))
	if err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("publication race error = %v, want replacement refusal", err)
	}
	if stagingPath == "" {
		t.Fatal("test did not capture the staging pathname")
	}
	if contents, err := os.ReadFile(stagingPath); err != nil {
		t.Fatalf("recreated staging file was removed: %v", err)
	} else if string(contents) != "foreign" {
		t.Fatalf("recreated staging contents = %q, want foreign", contents)
	}
}

func TestSecureWriteGrantedFileRejectsExistingStagingPathSwap(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(target, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}

	originalOperation := replaceWindowsFileOperation
	defer func() { replaceWindowsFileOperation = originalOperation }()
	operationCalls := 0
	movedStagingPath := ""
	replaceWindowsFileOperation = func(targetPtr, replacementPtr, backupPtr *uint16) error {
		operationCalls++
		if operationCalls == 1 {
			replacementPath := windows.UTF16PtrToString(replacementPtr)
			movedStagingPath = replacementPath + ".renderer"
			if err := os.Rename(replacementPath, movedStagingPath); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(replacementPath, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return originalOperation(targetPtr, replacementPtr, backupPtr)
	}

	err = secureWriteGrantedFile(grant, []byte("renderer"))
	if err == nil || !strings.Contains(err.Error(), "source or destination changed") || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("staging-path race error = %v, want source-change rollback", err)
	}
	if operationCalls != 2 {
		t.Fatalf("ReplaceFileW calls = %d, want publish + rollback", operationCalls)
	}
	if contents, err := os.ReadFile(target); err != nil {
		t.Fatal(err)
	} else if string(contents) != "selected" {
		t.Fatalf("rolled-back destination = %q, want selected file", contents)
	}
	if contents, err := os.ReadFile(movedStagingPath); err != nil {
		t.Fatalf("original staging file was not retained: %v", err)
	} else if string(contents) != "renderer" {
		t.Fatalf("original staging contents = %q, want renderer", contents)
	}
	backups, err := filepath.Glob(filepath.Join(directory, ".apexload-save-backup-*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("preserved recovery files = %v, want foreign publication backup", backups)
	}
	if contents, err := os.ReadFile(backups[0]); err != nil {
		t.Fatal(err)
	} else if string(contents) != "foreign" {
		t.Fatalf("preserved concurrent file = %q, want foreign", contents)
	}
}

func TestSecureWriteGrantedFileRejectsNewStagingPathSwap(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "new.json")
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}

	originalOperation := moveWindowsFileOperation
	defer func() { moveWindowsFileOperation = originalOperation }()
	movedStagingPath := ""
	moveWindowsFileOperation = func(fromPtr, toPtr *uint16, flags uint32) error {
		stagingPath := windows.UTF16PtrToString(fromPtr)
		movedStagingPath = stagingPath + ".renderer"
		if err := os.Rename(stagingPath, movedStagingPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stagingPath, []byte("foreign"), 0o600); err != nil {
			t.Fatal(err)
		}
		return originalOperation(fromPtr, toPtr, flags)
	}

	err = secureWriteGrantedFile(grant, []byte("renderer"))
	if err == nil || !strings.Contains(err.Error(), "identity does not match") || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("new-file staging race error = %v, want retained identity mismatch", err)
	}
	if contents, err := os.ReadFile(target); err != nil {
		t.Fatal(err)
	} else if string(contents) != "foreign" {
		t.Fatalf("unverified published destination = %q, want preserved foreign file", contents)
	}
	if contents, err := os.ReadFile(movedStagingPath); err != nil {
		t.Fatalf("original staging file was not retained: %v", err)
	} else if string(contents) != "renderer" {
		t.Fatalf("original staging contents = %q, want renderer", contents)
	}
}

func assertNoWindowsSaveBackups(t *testing.T, directory string) {
	t.Helper()
	backups, err := filepath.Glob(filepath.Join(directory, ".apexload-save-backup-*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("save left replacement backups: %v", backups)
	}
}

type windowsTestACEFingerprint struct {
	typeID uint8
	flags  uint8
	mask   windows.ACCESS_MASK
	sid    string
}

func windowsTestACLSignature(t *testing.T, dacl *windows.ACL) []windowsTestACEFingerprint {
	t.Helper()
	result := make([]windowsTestACEFingerprint, 0, dacl.AceCount)
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		result = append(result, windowsTestACEFingerprint{
			typeID: ace.Header.AceType,
			flags:  ace.Header.AceFlags,
			mask:   ace.Mask,
			sid:    (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String(),
		})
	}
	return result
}

func windowsTestAccess(sid *windows.SID, permissions windows.ACCESS_MASK, trusteeType windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func setWindowsTestDACL(t *testing.T, path string, entries []windows.EXPLICIT_ACCESS, sids ...*windows.SID) {
	t.Helper()
	var pinner runtime.Pinner
	for _, sid := range sids {
		pinner.Pin(sid)
	}
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}

func windowsTestDACL(t *testing.T, path string) (*windows.SECURITY_DESCRIPTOR, *windows.ACL) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("file has a nil DACL")
	}
	return descriptor, dacl
}

func assertWindowsTestProtectedDACL(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR) {
	t.Helper()
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("file DACL still inherits permissions")
	}
}

func assertWindowsTestACLHasSID(t *testing.T, dacl *windows.ACL, want *windows.SID) {
	t.Helper()
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		got := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if got.Equals(want) {
			return
		}
	}
	t.Fatalf("DACL does not contain SID %s", want.String())
}
