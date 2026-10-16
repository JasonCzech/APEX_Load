//go:build windows

package winfs

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrNotPrivate refuses an object whose owner, DACL or attributes do not
// prove it private to this process's user.
var ErrNotPrivate = errors.New("the object is not private to this user")

// systemSID is LOCAL_SYSTEM, the only other principal a private DACL grants.
const systemSID = "S-1-5-18"

type tokenIdentity struct {
	user, owner string
	err         error
}

var (
	tokenOnce sync.Once
	tokenSelf tokenIdentity
)

// currentToken returns this process token's user SID and its default owner
// SID (TokenOwner: the user, or Administrators in an elevated token).
func currentToken() (user, owner string, err error) {
	tokenOnce.Do(func() {
		token := windows.GetCurrentProcessToken()
		u, err := token.GetTokenUser()
		if err != nil || u.User.Sid == nil {
			tokenSelf.err = errors.Join(err, errors.New("token user is unavailable"))
			return
		}
		var needed uint32
		_ = windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &needed)
		if needed < uint32(unsafe.Sizeof(uintptr(0))) {
			tokenSelf.err = errors.New("token owner is unavailable")
			return
		}
		buffer := make([]byte, needed)
		if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], needed, &needed); err != nil {
			tokenSelf.err = err
			return
		}
		o := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0]))
		if o.Owner == nil {
			tokenSelf.err = errors.New("token owner is unavailable")
			return
		}
		tokenSelf = tokenIdentity{user: u.User.Sid.String(), owner: o.Owner.String()}
	})
	return tokenSelf.user, tokenSelf.owner, tokenSelf.err
}

// CurrentUserSID is this process token's user SID in string form.
func CurrentUserSID() (string, error) {
	user, _, err := currentToken()
	return user, err
}

// PrivateDirectorySDDL is the protected DACL a private directory is created
// with: full access for SYSTEM and user, inherited by every file and
// directory below it.
func PrivateDirectorySDDL(user string) string {
	return "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user + ")"
}

func validComponent(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "\\/:\x00") {
		return fmt.Errorf("invalid path component %q", name)
	}
	return nil
}

// CreatePrivateDirAt atomically creates name in the directory parent with
// the private DACL (PrivateDirectorySDDL for this user) and returns a
// handle to it that can list, traverse and read its security. The DACL is
// part of the create, never applied afterwards, and a name that already
// exists (including a reparse point) fails with fs.ErrExist.
func CreatePrivateDirAt(parent *os.File, name string) (*os.File, error) {
	if parent == nil {
		return nil, errors.New("parent directory is required")
	}
	if err := validComponent(name); err != nil {
		return nil, err
	}
	user, err := CurrentUserSID()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(PrivateDirectorySDDL(user))
	if err != nil {
		return nil, err
	}
	handle, err := ntCreateAt(parent, name, sd,
		windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_CREATE, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT)
	if err != nil {
		return nil, &os.PathError{Op: "mkdirat", Path: name, Err: ntErrno(err)}
	}
	return os.NewFile(uintptr(handle), name), nil
}

// OpenAt opens name in the directory parent without following a reparse
// point, sharing read, write and delete. access is the NT access mask;
// directory selects FILE_DIRECTORY_FILE.
func OpenAt(parent *os.File, name string, access uint32, directory bool) (*os.File, error) {
	f, err := openAt(parent, name, access, directory)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: ntErrno(err)}
	}
	return f, nil
}

// openAt is OpenAt returning the NTSTATUS it failed with.
func openAt(parent *os.File, name string, access uint32, directory bool) (*os.File, error) {
	if parent == nil {
		return nil, errors.New("parent directory is required")
	}
	if err := validComponent(name); err != nil {
		return nil, err
	}
	options := uint32(windows.FILE_OPEN_REPARSE_POINT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	}
	handle, err := ntCreateAt(parent, name, nil, access|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, options)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}

func ntCreateAt(parent *os.File, name string, sd *windows.SECURITY_DESCRIPTOR, access, disposition, options uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: sd,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access, attributes, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition, options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return handle, nil
}

// ntErrno converts an NTSTATUS into the Win32 error os and errors.Is
// understand (STATUS_OBJECT_NAME_COLLISION becomes ERROR_ALREADY_EXISTS,
// so fs.ErrExist matches).
func ntErrno(err error) error {
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status.Errno()
	}
	return err
}

// CheckPrivate proves an opened object private to this process's user: its
// owner is the token user or the token's default owner; its DACL is present
// and holds only access-allowed entries for the user and SYSTEM, with one
// that grants the user access to the object itself; it is not a reparse
// point; and a regular file is a non-directory with exactly one link. The
// handle must carry READ_CONTROL (every os.Root and os open does).
func CheckPrivate(f *os.File, regular bool) error {
	if f == nil {
		return fmt.Errorf("%w: no handle", ErrNotPrivate)
	}
	user, owner, err := currentToken()
	if err != nil {
		return errors.Join(ErrNotPrivate, err)
	}
	handle := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return errors.Join(ErrNotPrivate, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: it is a reparse point", ErrNotPrivate)
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if regular && (isDir || info.NumberOfLinks != 1) {
		return fmt.Errorf("%w: it is not a singly linked regular file", ErrNotPrivate)
	}
	if !regular && !isDir {
		return fmt.Errorf("%w: it is not a directory", ErrNotPrivate)
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.Join(ErrNotPrivate, err)
	}
	return checkDescriptor(sd, user, owner)
}

func checkDescriptor(sd *windows.SECURITY_DESCRIPTOR, user, tokenOwner string) error {
	objectOwner, _, err := sd.Owner()
	if err != nil || objectOwner == nil {
		return errors.Join(fmt.Errorf("%w: no owner", ErrNotPrivate), err)
	}
	if o := objectOwner.String(); o != user && o != tokenOwner {
		return fmt.Errorf("%w: it is owned by %s", ErrNotPrivate, o)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		// A missing DACL (SE_DACL_PRESENT unset) or a NULL DACL grants
		// everyone everything.
		return errors.Join(fmt.Errorf("%w: it has no DACL", ErrNotPrivate), err)
	}
	userGranted := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return errors.Join(fmt.Errorf("%w: unreadable access entry", ErrNotPrivate), err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < uint16(unsafe.Offsetof(ace.SidStart)+8) {
			return fmt.Errorf("%w: access entry %d is not a plain allow entry", ErrNotPrivate, index)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return fmt.Errorf("%w: access entry %d has no valid SID", ErrNotPrivate, index)
		}
		switch sid.String() {
		case user:
			if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 {
				userGranted = true
			}
		case systemSID:
		default:
			return fmt.Errorf("%w: access entry %d grants %s", ErrNotPrivate, index, sid.String())
		}
	}
	if !userGranted {
		return fmt.Errorf("%w: its DACL does not grant this user access", ErrNotPrivate)
	}
	return nil
}

// Owner is an opened object's owner SID. The handle must carry READ_CONTROL.
func Owner(f *os.File) (string, error) {
	if f == nil {
		return "", errors.New("file handle is required")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return "", errors.Join(errors.New("the object has no owner"), err)
	}
	return owner.String(), nil
}

// OwnedByCurrentToken reports whether sid is this process token's user or
// its default owner, the owners CheckPrivate accepts.
func OwnedByCurrentToken(sid string) bool {
	user, owner, err := currentToken()
	return err == nil && sid != "" && (sid == user || sid == owner)
}
