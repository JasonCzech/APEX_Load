//go:build windows

package browser

// TEMPORARY Phase 0 spike (WP1): observes Windows filesystem, ACL, lock and
// process behavior that the native HAR ownership design depends on. It runs
// only with APEXLOAD_HAR_WINDOWS_SPIKE=1 and prints "SPIKE <key>: <value>"
// lines; findings are recorded in docs/qa and this file is removed.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	playwright "github.com/mxschmitt/playwright-go"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const spikeFileRenameInformationEx = 65

type spikeRenameInformation struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

type spikeDispositionInformationEx struct{ Flags uint32 }

type spikeFileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func spikeEnabled(t *testing.T) {
	if os.Getenv("APEXLOAD_HAR_WINDOWS_SPIKE") != "1" {
		t.Skip("Windows HAR spike runs only with APEXLOAD_HAR_WINDOWS_SPIKE=1")
	}
}

func spikef(t *testing.T, key string, format string, args ...any) {
	t.Helper()
	t.Logf("SPIKE %s: %s", key, fmt.Sprintf(format, args...))
}

func spikeErr(err error) string {
	if err == nil {
		return "ok"
	}
	var errno windows.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("%v (errno %d)", err, uint32(errno))
	}
	var status windows.NTStatus
	if errors.As(err, &status) {
		return fmt.Sprintf("%v (ntstatus 0x%08X)", err, uint32(status))
	}
	return err.Error()
}

func spikeTokenSIDs(t *testing.T) (user, owner string, elevated bool) {
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var needed uint32
	_ = windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &needed)
	buffer := make([]byte, needed)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], needed, &needed); err != nil {
		t.Fatal(err)
	}
	o := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0]))
	return u.User.Sid.String(), o.Owner.String(), token.IsElevated()
}

func spikeDescriptor(handle windows.Handle) (owner string, sddl string, err error) {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", "", err
	}
	o, _, err := sd.Owner()
	if err != nil {
		return "", "", err
	}
	return o.String(), sd.String(), nil
}

func spikeOpenAttributes(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func spikeFileID(handle windows.Handle) (spikeFileIDInfo, error) {
	var info spikeFileIDInfo
	err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info, err
}

func spikeCreatePrivateDirAt(parent windows.Handle, name, sid string) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return 0, err
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: objectName, SecurityDescriptor: sd}
	oa.Length = uint32(unsafe.Sizeof(*oa))
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return handle, err
}

func spikeOpenDir(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func spikeRenameAt(handle, root windows.Handle, name string, flags uint32) error {
	name16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	length := (len(name16) - 1) * 2
	var dummy spikeRenameInformation
	buffer := make([]byte, int(unsafe.Offsetof(dummy.FileName))+length+2)
	info := (*spikeRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.Flags, info.RootDirectory, info.FileNameLength = flags, root, uint32(length)
	copy(unsafe.Slice(&info.FileName[0], len(name16)), name16)
	var iosb windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(handle, &iosb, &buffer[0], uint32(len(buffer)), spikeFileRenameInformationEx)
}

func spikeOpenForDelete(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func spikePosixDelete(handle windows.Handle) error {
	info := spikeDispositionInformationEx{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS}
	var iosb windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(handle, &iosb, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), windows.FileDispositionInformationEx)
}

func TestWindowsHARSpikeEnvironment(t *testing.T) {
	spikeEnabled(t)
	user, owner, elevated := spikeTokenSIDs(t)
	spikef(t, "token", "user=%s token_owner=%s elevated=%v", user, owner, elevated)
	for _, dir := range []string{os.TempDir(), os.Getenv("RUNNER_TEMP"), t.TempDir()} {
		if dir == "" {
			continue
		}
		volume := make([]uint16, 32768)
		p, _ := windows.UTF16PtrFromString(dir)
		if err := windows.GetVolumePathName(p, &volume[0], uint32(len(volume))); err != nil {
			spikef(t, "volume", "%s: %s", dir, spikeErr(err))
			continue
		}
		v := windows.UTF16ToString(volume)
		vp, _ := windows.UTF16PtrFromString(v)
		fs := make([]uint16, 32)
		err := windows.GetVolumeInformation(vp, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs)))
		spikef(t, "volume", "%s: root=%s type=%d fs=%s err=%s", dir, v, windows.GetDriveType(vp), windows.UTF16ToString(fs), spikeErr(err))
		h, err := spikeOpenAttributes(dir)
		if err == nil {
			o, sddl, err := spikeDescriptor(h)
			spikef(t, "parent-acl", "%s: owner=%s sddl=%s err=%s", dir, o, sddl, spikeErr(err))
			_ = windows.CloseHandle(h)
		}
	}
	// D6 host identity sources.
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		guid, _, err := k.GetStringValue("MachineGuid")
		spikef(t, "machine-guid", "present=%v err=%s", guid != "", spikeErr(err))
		_ = k.Close()
	} else {
		spikef(t, "machine-guid", "open: %s", spikeErr(err))
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		boot, kind, err := k.GetIntegerValue("BootId")
		spikef(t, "boot-id", "value=%d type=%d err=%s", boot, kind, spikeErr(err))
		_ = k.Close()
	} else {
		spikef(t, "boot-id", "open: %s", spikeErr(err))
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		_, _, err := k.GetIntegerValue("ContainerType")
		spikef(t, "container-type", "lookup=%s", spikeErr(err))
		_ = k.Close()
	}
	host := make([]uint16, 256)
	size := uint32(len(host))
	err := windows.GetComputerNameEx(windows.ComputerNamePhysicalDnsHostname, &host[0], &size)
	spikef(t, "dns-host", "present=%v err=%s", size > 0, spikeErr(err))
	// D6 process identity: self, a dead child, PID 4 (System), an unused PID.
	self, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(os.Getpid()))
	if err == nil {
		var creation, exit, kernel, userTime windows.Filetime
		err = windows.GetProcessTimes(self, &creation, &exit, &kernel, &userTime)
		wait, _ := windows.WaitForSingleObject(self, 0)
		spikef(t, "process-self", "creation=%d wait=%d err=%s", uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), wait, spikeErr(err))
		_ = windows.CloseHandle(self)
	} else {
		spikef(t, "process-self", "open: %s", spikeErr(err))
	}
	child := exec.Command("cmd", "/c", "exit", "0")
	if err := child.Start(); err == nil {
		pid := child.Process.Pid
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
		_ = child.Wait()
		if openErr == nil {
			wait, _ := windows.WaitForSingleObject(h, 0)
			spikef(t, "process-exited-held", "wait=%d (WAIT_OBJECT_0=%d)", wait, windows.WAIT_OBJECT_0)
			_ = windows.CloseHandle(h)
		}
		time.Sleep(200 * time.Millisecond)
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
		spikef(t, "process-exited-reopen", "pid=%d err=%s", pid, spikeErr(err))
		if err == nil {
			wait, _ := windows.WaitForSingleObject(h, 0)
			spikef(t, "process-exited-reopen-wait", "wait=%d", wait)
			_ = windows.CloseHandle(h)
		}
	}
	_, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, 4)
	spikef(t, "process-system", "pid=4 err=%s", spikeErr(err))
	_, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, 0x7ffffff0)
	spikef(t, "process-unused", "err=%s", spikeErr(err))
}

// Q1: are os.Root.Lstat/Stat file IDs loaded at the call, while os.Lstat
// loads them lazily at SameFile?
func TestWindowsHARSpikeFileIdentity(t *testing.T) {
	spikeEnabled(t)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	handleInfo, _ := f.Stat()
	_ = f.Close()
	pathInfo, _ := os.Lstat(filepath.Join(dir, "a"))
	pathStat, _ := os.Stat(filepath.Join(dir, "a"))
	rootLstat, _ := root.Lstat("a")
	rootStat, _ := root.Stat("a")
	if err := os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, _ := os.Lstat(filepath.Join(dir, "b"))
	replacement, _ := os.Lstat(filepath.Join(dir, "a"))
	spikef(t, "q1-os-lstat", "same-as-original-handle=%v same-as-moved=%v same-as-replacement=%v", os.SameFile(pathInfo, handleInfo), os.SameFile(pathInfo, moved), os.SameFile(pathInfo, replacement))
	spikef(t, "q1-os-stat", "same-as-original-handle=%v same-as-replacement=%v", os.SameFile(pathStat, handleInfo), os.SameFile(pathStat, replacement))
	spikef(t, "q1-root-lstat", "same-as-original-handle=%v same-as-moved=%v same-as-replacement=%v", os.SameFile(rootLstat, handleInfo), os.SameFile(rootLstat, moved), os.SameFile(rootLstat, replacement))
	spikef(t, "q1-root-stat", "same-as-original-handle=%v same-as-replacement=%v", os.SameFile(rootStat, handleInfo), os.SameFile(rootStat, replacement))
	dirInfo, _ := root.Stat(".")
	dirHandle, _ := root.Open(".")
	dirHandleInfo, _ := dirHandle.Stat()
	_ = dirHandle.Close()
	spikef(t, "q1-root-dot", "root.Stat(.) same as opened dir=%v sys=%T", os.SameFile(dirInfo, dirHandleInfo), dirInfo.Sys())
	// The 128-bit FILE_ID_INFO next to the 64-bit index.
	h, err := spikeOpenAttributes(filepath.Join(dir, "b"))
	if err == nil {
		id, err := spikeFileID(h)
		var by windows.ByHandleFileInformation
		byErr := windows.GetFileInformationByHandle(h, &by)
		spikef(t, "q1-file-id-info", "id=%x vol=%x err=%s index=%08x%08x links=%d byErr=%s", id.FileID, id.VolumeSerialNumber, spikeErr(err), by.FileIndexHigh, by.FileIndexLow, by.NumberOfLinks, spikeErr(byErr))
		_ = windows.CloseHandle(h)
	}
}

// Q2: does os.Root.Link work on Windows? Q3: do os.Root/os handles block
// rename and remove of an object they hold open?
func TestWindowsHARSpikeLinkAndSharing(t *testing.T) {
	spikeEnabled(t)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "source"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	spikef(t, "q2-root-link", "%s", spikeErr(root.Link("source", "linked")))
	spikef(t, "q2-root-link-existing", "%s", spikeErr(root.Link("source", "linked")))
	spikef(t, "q2-os-link", "%s", spikeErr(os.Link(filepath.Join(dir, "source"), filepath.Join(dir, "linked2"))))

	for _, c := range []struct {
		name string
		open func() (*os.File, error)
	}{
		{"root.Open", func() (*os.File, error) { return root.Open("held") }},
		{"root.OpenFile-rw", func() (*os.File, error) { return root.OpenFile("held", os.O_RDWR, 0) }},
		{"os.Open", func() (*os.File, error) { return os.Open(filepath.Join(dir, "held")) }},
	} {
		if err := os.WriteFile(filepath.Join(dir, "held"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := c.open()
		if err != nil {
			t.Fatal(err)
		}
		renameErr := root.Rename("held", "held-renamed")
		if renameErr == nil {
			_ = root.Rename("held-renamed", "held")
		}
		removeErr := root.Remove("held")
		_ = f.Close()
		_ = os.Remove(filepath.Join(dir, "held"))
		_ = os.Remove(filepath.Join(dir, "held-renamed"))
		spikef(t, "q3-file-"+c.name, "rename=%s remove=%s", spikeErr(renameErr), spikeErr(removeErr))
	}
	// A directory held by an os.Root (OpenRoot) or a root.Open(".") handle.
	for _, c := range []struct {
		name string
		hold func() (func() error, error)
	}{
		{"OpenRoot", func() (func() error, error) {
			r, err := root.OpenRoot("dir")
			if err != nil {
				return nil, err
			}
			return r.Close, nil
		}},
		{"OpenRoot+Open(.)", func() (func() error, error) {
			r, err := root.OpenRoot("dir")
			if err != nil {
				return nil, err
			}
			d, err := r.Open(".")
			if err != nil {
				_ = r.Close()
				return nil, err
			}
			return func() error { return errors.Join(d.Close(), r.Close()) }, nil
		}},
		{"OpenRoot+child-file", func() (func() error, error) {
			r, err := root.OpenRoot("dir")
			if err != nil {
				return nil, err
			}
			f, err := r.OpenFile("child", os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				_ = r.Close()
				return nil, err
			}
			return func() error { return errors.Join(f.Close(), r.Close()) }, nil
		}},
	} {
		if err := root.Mkdir("dir", 0o700); err != nil {
			t.Fatal(err)
		}
		release, err := c.hold()
		if err != nil {
			t.Fatal(err)
		}
		renameErr := root.Rename("dir", "dir-renamed")
		if renameErr == nil {
			renameErr = errors.Join(errors.New("renamed"), root.Rename("dir-renamed", "dir"))
			if renameErr.Error() == "renamed" {
				renameErr = nil
			}
		}
		removeErr := root.Remove("dir/child")
		removeDirErr := root.Remove("dir")
		_ = release()
		_ = os.RemoveAll(filepath.Join(dir, "dir"))
		_ = os.RemoveAll(filepath.Join(dir, "dir-renamed"))
		spikef(t, "q3-dir-"+c.name, "rename=%s remove-child=%s remove-dir=%s", spikeErr(renameErr), spikeErr(removeErr), spikeErr(removeDirErr))
	}
	// Handle-relative rename (FILE_RENAME_INFORMATION_EX) and POSIX delete
	// while another share-all handle stays open.
	if err := os.Mkdir(filepath.Join(dir, "rel"), 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := spikeOpenDir(filepath.Join(dir, "rel"), windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA|windows.SYNCHRONIZE)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	if err := os.WriteFile(filepath.Join(dir, "rel", "one"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rel", "taken"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := spikeOpenForDelete(filepath.Join(dir, "rel", "one"))
	if err != nil {
		t.Fatal(err)
	}
	spikef(t, "q3-rename-ex-onto-existing", "%s", spikeErr(spikeRenameAt(h, parent, "taken", windows.FILE_RENAME_POSIX_SEMANTICS)))
	spikef(t, "q3-rename-ex-relative", "%s", spikeErr(spikeRenameAt(h, parent, "two", windows.FILE_RENAME_POSIX_SEMANTICS)))
	_, statErr := os.Lstat(filepath.Join(dir, "rel", "two"))
	spikef(t, "q3-rename-ex-relative-result", "two exists: %s", spikeErr(statErr))
	reader, _ := os.Open(filepath.Join(dir, "rel", "two"))
	spikef(t, "q3-posix-delete-with-reader-open", "%s", spikeErr(spikePosixDelete(h)))
	_ = windows.CloseHandle(h)
	_, statErr = os.Lstat(filepath.Join(dir, "rel", "two"))
	spikef(t, "q3-posix-delete-name-gone-while-reader-open", "lstat=%s", spikeErr(statErr))
	createErr := os.WriteFile(filepath.Join(dir, "rel", "two"), []byte("y"), 0o600)
	spikef(t, "q3-posix-delete-name-reusable", "%s", spikeErr(createErr))
	if reader != nil {
		_ = reader.Close()
	}
	// A legacy (non-POSIX) delete with another handle open leaves the name
	// delete-pending.
	if err := os.WriteFile(filepath.Join(dir, "rel", "legacy"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := spikeOpenForDelete(filepath.Join(dir, "rel", "legacy"))
	if err == nil {
		removeErr := os.Remove(filepath.Join(dir, "rel", "legacy"))
		_, lstatErr := os.Lstat(filepath.Join(dir, "rel", "legacy"))
		createErr := os.WriteFile(filepath.Join(dir, "rel", "legacy"), []byte("y"), 0o600)
		spikef(t, "q3-legacy-delete-pending", "remove=%s lstat=%s recreate=%s", spikeErr(removeErr), spikeErr(lstatErr), spikeErr(createErr))
		_ = windows.CloseHandle(holder)
	}
}

// Q4: does File.Sync() on a directory fail, and does FlushFileBuffers on a
// directory handle opened for write work?
func TestWindowsHARSpikeDirectorySync(t *testing.T) {
	spikeEnabled(t)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	d, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	spikef(t, "q4-root-open-dot-sync", "%s", spikeErr(d.Sync()))
	_ = d.Close()
	d, err = os.Open(dir)
	if err == nil {
		spikef(t, "q4-os-open-dir-sync", "%s", spikeErr(d.Sync()))
		_ = d.Close()
	}
	for _, access := range []struct {
		name   string
		access uint32
	}{
		{"GENERIC_WRITE", windows.GENERIC_WRITE},
		{"FILE_ADD_FILE|FILE_ADD_SUBDIRECTORY", windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA},
		{"FILE_WRITE_ATTRIBUTES", windows.FILE_WRITE_ATTRIBUTES},
		{"GENERIC_READ", windows.GENERIC_READ},
	} {
		h, err := spikeOpenDir(dir, access.access)
		if err != nil {
			spikef(t, "q4-flush-"+access.name, "open: %s", spikeErr(err))
			continue
		}
		spikef(t, "q4-flush-"+access.name, "%s", spikeErr(windows.FlushFileBuffers(h)))
		_ = windows.CloseHandle(h)
	}
}

// D2 + Q5: a private directory created by NtCreateFile with a protected
// DACL; what its children inherit; and what the Node driver's flush of
// working.har leaves.
func TestWindowsHARSpikePrivateCreationAndDriverFlush(t *testing.T) {
	spikeEnabled(t)
	user, _, _ := spikeTokenSIDs(t)
	dir := t.TempDir()
	parent, err := spikeOpenDir(dir, windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.FILE_APPEND_DATA|windows.SYNCHRONIZE)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	h, err := spikeCreatePrivateDirAt(parent, ".apexload-har-work", user)
	spikef(t, "d2-ntcreatefile", "%s", spikeErr(err))
	if err != nil {
		return
	}
	owner, sddl, err := spikeDescriptor(h)
	spikef(t, "d2-private-dir", "owner=%s sddl=%s err=%s", owner, sddl, spikeErr(err))
	_, err = spikeCreatePrivateDirAt(parent, ".apexload-har-work", user)
	spikef(t, "d2-ntcreatefile-existing", "%s", spikeErr(err))
	_ = windows.CloseHandle(h)
	work := filepath.Join(dir, ".apexload-har-work")
	root, err := os.OpenRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("gen", 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := root.OpenFile("gen/working.har", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for _, name := range []string{"gen", "gen/working.har"} {
		h, err := spikeOpenAttributes(filepath.Join(work, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		owner, sddl, err := spikeDescriptor(h)
		spikef(t, "d2-inherited-"+name, "owner=%s sddl=%s err=%s", owner, sddl, spikeErr(err))
		_ = windows.CloseHandle(h)
	}
	beforeHandle, _ := spikeOpenAttributes(filepath.Join(work, "gen", "working.har"))
	beforeID, _ := spikeFileID(beforeHandle)
	_ = windows.CloseHandle(beforeHandle)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>spike</title><p>ok</p>`))
	}))
	defer server.Close()
	pwBrowser, err := getBrowserInstance("chromium", true)
	if err != nil {
		spikef(t, "q5-browser", "unavailable: %s", spikeErr(err))
		return
	}
	harPath := filepath.Join(work, "gen", "working.har")
	ctx, err := pwBrowser.NewContext(playwright.BrowserNewContextOptions{RecordHarPath: playwright.String(harPath), RecordHarContent: playwright.HarContentPolicyEmbed})
	if err != nil {
		spikef(t, "q5-context", "%s", spikeErr(err))
		return
	}
	page, err := ctx.NewPage()
	if err == nil {
		_, err = page.Goto(server.URL)
	}
	spikef(t, "q5-navigate", "%s", spikeErr(err))
	spikef(t, "q5-context-close", "%s", spikeErr(ctx.Close()))
	after, err := os.Lstat(harPath)
	if err != nil {
		spikef(t, "q5-after", "lstat: %s", spikeErr(err))
		return
	}
	afterHandle, err := spikeOpenAttributes(harPath)
	if err != nil {
		spikef(t, "q5-after", "open: %s", spikeErr(err))
		return
	}
	defer windows.CloseHandle(afterHandle)
	afterID, _ := spikeFileID(afterHandle)
	owner, sddl, err = spikeDescriptor(afterHandle)
	var by windows.ByHandleFileInformation
	_ = windows.GetFileInformationByHandle(afterHandle, &by)
	spikef(t, "q5-working-har", "size=%d same-file-id=%v links=%d owner=%s sddl=%s err=%s", after.Size(), beforeID == afterID, by.NumberOfLinks, owner, sddl, spikeErr(err))
	entries, _ := os.ReadDir(filepath.Join(work, "gen"))
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	spikef(t, "q5-generation-entries", "%s", strings.Join(names, ","))
}

// Q6: LockFileEx on a zero-length file across handles and processes.
func TestWindowsHARSpikeZeroLengthLock(t *testing.T) {
	spikeEnabled(t)
	if path := os.Getenv("APEXLOAD_HAR_WINDOWS_SPIKE_LOCK_CHILD"); path != "" {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			fmt.Println("CHILD open", spikeErr(err))
			os.Exit(3)
		}
		var overlapped windows.Overlapped
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		fmt.Println("CHILD lock", spikeErr(err))
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			os.Exit(2)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "namespace.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := os.OpenFile(path, os.O_RDWR, 0)
	b, _ := os.OpenFile(path, os.O_RDWR, 0)
	defer a.Close()
	defer b.Close()
	var oa, ob windows.Overlapped
	spikef(t, "q6-first-handle", "%s", spikeErr(windows.LockFileEx(windows.Handle(a.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &oa)))
	spikef(t, "q6-second-handle-same-process", "%s", spikeErr(windows.LockFileEx(windows.Handle(b.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ob)))
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsHARSpikeZeroLengthLock$", "-test.v")
	cmd.Env = append(os.Environ(), "APEXLOAD_HAR_WINDOWS_SPIKE_LOCK_CHILD="+path)
	out, err := cmd.CombinedOutput()
	spikef(t, "q6-other-process-while-held", "exit=%s out=%q", spikeErr(err), strings.TrimSpace(lastLine(string(out), "CHILD")))
	_ = windows.UnlockFileEx(windows.Handle(a.Fd()), 0, 1, 0, &oa)
	cmd = exec.Command(os.Args[0], "-test.run=^TestWindowsHARSpikeZeroLengthLock$", "-test.v")
	cmd.Env = append(os.Environ(), "APEXLOAD_HAR_WINDOWS_SPIKE_LOCK_CHILD="+path)
	out, err = cmd.CombinedOutput()
	spikef(t, "q6-other-process-after-release", "exit=%s out=%q", spikeErr(err), strings.TrimSpace(lastLine(string(out), "CHILD")))
	info, _ := os.Stat(path)
	spikef(t, "q6-file-still-zero-length", "size=%d", info.Size())
	// A lock held by a closed handle is released on close.
	c, _ := os.OpenFile(path, os.O_RDWR, 0)
	var oc windows.Overlapped
	_ = windows.LockFileEx(windows.Handle(c.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &oc)
	_ = c.Close()
	var od windows.Overlapped
	spikef(t, "q6-after-holder-closed", "%s", spikeErr(windows.LockFileEx(windows.Handle(b.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &od)))
}

func lastLine(out, prefix string) string {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			line = l
		}
	}
	return line
}

// Q7: how often do create/write/rename/remove cycles meet sharing, lock or
// delete-pending errors (antivirus, indexing) on this runner?
func TestWindowsHARSpikeTransientErrors(t *testing.T) {
	spikeEnabled(t)
	dir := t.TempDir()
	counts := map[string]int{}
	note := func(op string, err error) {
		if err == nil {
			return
		}
		counts[op+": "+spikeErr(err)]++
	}
	deadline := time.Now().Add(90 * time.Second)
	cycles := 0
	for cycles < 2000 && time.Now().Before(deadline) {
		generation := filepath.Join(dir, fmt.Sprintf("g%04d", cycles))
		note("mkdir", os.Mkdir(generation, 0o700))
		for _, name := range []string{"owner.lock", "manifest.json", "working.har"} {
			note("write "+name, os.WriteFile(filepath.Join(generation, name), []byte(strings.Repeat("{}", 512)), 0o600))
		}
		note("rename working->part", os.Rename(filepath.Join(generation, "working.har"), filepath.Join(generation, "scrubbed.part")))
		note("rename dir->retired", os.Rename(generation, generation+".retired"))
		for _, name := range []string{"owner.lock", "manifest.json", "scrubbed.part"} {
			note("remove "+name, os.Remove(filepath.Join(generation+".retired", name)))
		}
		note("remove dir", os.Remove(generation+".retired"))
		cycles++
	}
	spikef(t, "q7-cycles", "%d in %s", cycles, dir)
	if len(counts) == 0 {
		spikef(t, "q7-errors", "none")
	}
	for key, count := range counts {
		spikef(t, "q7-errors", "%d x %s", count, key)
	}
}
