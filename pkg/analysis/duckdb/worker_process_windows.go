//go:build windows

package duckdb

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// All native handles remain parent-owned. The unnamed job is never inherited;
// only explicit duplicated bootstrap/root/command/stdout/stderr handles are.
// CreateProcess returns the primary thread handle, avoiding PID-based thread
// enumeration and ensuring assignment/limits precede the first instruction.
type workerOwnedProcess struct {
	process                  windows.Handle
	owner                    *workerAttempt
	processCloser, jobCloser *workerWindowsHandleOwner
	job                      windows.Handle
	cmd                      *exec.Cmd
	mu                       sync.Mutex
	reaped                   bool
	assigned                 bool
	waitOnce                 sync.Once
	waitErr                  error
}

func workerStartOwned(a *workerAttempt, cmd *exec.Cmd, bootstrap, root, commands *os.File, profile workerProcessProfile) (*workerOwnedProcess, error) {
	if cmd == nil || bootstrap == nil || root == nil || commands == nil || !filepath.IsAbs(cmd.Path) {
		return nil, errWorkerProtocol
	}
	stdout, ok := cmd.Stdout.(*os.File)
	if !ok {
		return nil, errWorkerProtocol
	}
	stderr, ok := cmd.Stderr.(*os.File)
	if !ok {
		return nil, errWorkerProtocol
	}
	current := windows.CurrentProcess()
	handles := make([]windows.Handle, 5)
	closers := make([]*workerWindowsHandleOwner, 5)
	defer func() {
		for _, closer := range closers {
			if closer != nil {
				a.requestDescriptorClose(closer)
			}
		}
	}()
	for i, f := range []*os.File{bootstrap, root, commands, stdout, stderr} {
		if windows.DuplicateHandle(current, windows.Handle(f.Fd()), current, &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS) != nil {
			return nil, errWorkerProtocol
		}
		closers[i] = &workerWindowsHandleOwner{handle: handles[i]}
		a.trackDescriptor(closers[i])
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, errWorkerProtocol
	}
	defer attrs.Delete()
	if attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])) != nil {
		return nil, errWorkerProtocol
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, errWorkerOwnership
	}
	jobCloser := &workerWindowsHandleOwner{handle: job}
	a.trackDescriptor(jobCloser)
	transferred := false
	defer func() {
		if !transferred {
			a.requestDescriptorClose(jobCloser)
		}
	}()
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = 1 // child executor has no shell/helper path
	limits.JobMemoryLimit = uintptr(profile.memoryBytes + (512 << 20))
	if profile.memoryBytes <= 0 || uint64(limits.JobMemoryLimit) != uint64(profile.memoryBytes+(512<<20)) {
		return nil, errWorkerResourceLimit
	}
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, errWorkerOwnership
	}
	env := append([]string{}, cmd.Env...)
	for i, key := range []string{"APEXLOAD_NATIVE_BOOTSTRAP_HANDLE", "APEXLOAD_NATIVE_ROOT_HANDLE", "APEXLOAD_NATIVE_COMMAND_HANDLE"} {
		env = append(env, key+"="+strconv.FormatUint(uint64(handles[i]), 10))
	}
	environment, err := workerWindowsEnvironmentBlock(env)
	if err != nil {
		return nil, errWorkerProtocol
	}
	executable, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, errWorkerProtocol
	}
	commandLine := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		commandLine[i] = syscall.EscapeArg(arg)
	}
	arguments, err := windows.UTF16PtrFromString(strings.Join(commandLine, " "))
	if err != nil {
		return nil, errWorkerProtocol
	}
	directory, err := windows.UTF16PtrFromString(cmd.Dir)
	if err != nil {
		return nil, errWorkerProtocol
	}
	info := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE, StdInput: handles[2], StdOutput: handles[3], StdErr: handles[4]}, ProcThreadAttributeList: attrs.List()}
	var created windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW)
	if windows.CreateProcess(executable, arguments, nil, nil, true, flags, &environment[0], directory, &info.StartupInfo, &created) != nil {
		return nil, errWorkerProtocol
	}
	processCloser := &workerWindowsHandleOwner{handle: created.Process}
	a.trackDescriptor(processCloser)
	threadCloser := &workerWindowsHandleOwner{handle: created.Thread}
	a.trackDescriptor(threadCloser)
	transferred = true
	p := &workerOwnedProcess{process: created.Process, job: job, cmd: cmd, owner: a, processCloser: processCloser, jobCloser: jobCloser}
	// Any error after CreateProcess transfers exact-handle ownership to the common
	// supervisor. It must Wait even if assignment or resume fails; no detached
	// startup cleanup is allowed to release scratch/admission.
	defer a.requestDescriptorClose(threadCloser)
	cmd.Process, err = os.FindProcess(int(created.ProcessId))
	if err != nil {
		return p, errWorkerOwnership
	}
	if windows.AssignProcessToJobObject(job, created.Process) != nil {
		return p, errWorkerOwnership
	}
	p.assigned = true
	if _, err := windows.ResumeThread(created.Thread); err != nil {
		return p, errWorkerOwnership
	}
	return p, nil
}
func (p *workerOwnedProcess) wait() error {
	p.waitOnce.Do(func() {
		status, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			p.waitErr = errWorkerOwnership
			return
		}
		var code uint32
		if windows.GetExitCodeProcess(p.process, &code) != nil {
			p.waitErr = errWorkerOwnership
			return
		}
		p.mu.Lock()
		p.reaped = true
		p.mu.Unlock()
		if code != 0 {
			p.waitErr = errWorkerProtocol
		}
	})
	return p.waitErr
}
func (p *workerOwnedProcess) terminate() error { return p.kill() } // typed cancellation was the graceful step
func (p *workerOwnedProcess) kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return nil
	}
	if p.assigned {
		if windows.TerminateJobObject(p.job, 78) != nil {
			return errWorkerOwnership
		}
	} else if windows.TerminateProcess(p.process, 78) != nil {
		return errWorkerOwnership
	}
	return nil
}

type workerWindowsJobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func (p *workerOwnedProcess) closeAfterWait() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reaped {
		return errWorkerOwnership
	}
	if p.assigned {
		var accounting workerWindowsJobAccounting
		if windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil) != nil || accounting.ActiveProcesses != 0 {
			return errWorkerOwnership
		}
	}
	if p.cmd.Process != nil && p.cmd.Process.Release() != nil {
		return errWorkerOwnership
	}
	if p.owner.awaitDescriptorClose(p.processCloser) != nil {
		return errWorkerOwnership
	}
	p.process = 0
	if p.owner.awaitDescriptorClose(p.jobCloser) != nil {
		return errWorkerOwnership
	}
	p.job = 0
	return nil
}
func workerWindowsEnvironmentBlock(env []string) ([]uint16, error) {
	entries := append([]string{}, env...)
	names := map[string]bool{}
	for _, item := range entries {
		split := strings.IndexByte(item, '=')
		if split <= 0 || strings.IndexByte(item, 0) >= 0 {
			return nil, errWorkerProtocol
		}
		for _, c := range item[:split] {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return nil, errWorkerProtocol
			}
		}
		name := strings.ToUpper(item[:split])
		if names[name] {
			return nil, errWorkerProtocol
		}
		names[name] = true
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToUpper(entries[i][:strings.IndexByte(entries[i], '=')]) < strings.ToUpper(entries[j][:strings.IndexByte(entries[j], '=')])
	})
	var joined strings.Builder
	for _, item := range entries {
		joined.WriteString(item)
		joined.WriteByte(0)
	}
	joined.WriteByte(0)
	if len(entries) == 0 {
		joined.WriteByte(0)
	}
	return utf16.Encode([]rune(joined.String())), nil
}

func workerInheritedChannels() (*os.File, *os.File, *os.File, error) {
	var files [3]*os.File
	failed := true
	defer func() {
		if failed {
			for _, f := range files {
				if f != nil {
					_ = f.Close()
				}
			}
		}
	}()
	for i, key := range []string{"APEXLOAD_NATIVE_BOOTSTRAP_HANDLE", "APEXLOAD_NATIVE_ROOT_HANDLE", "APEXLOAD_NATIVE_COMMAND_HANDLE"} {
		value := os.Getenv(key)
		_ = os.Unsetenv(key)
		h, err := strconv.ParseUint(value, 10, 64)
		if err != nil || h == 0 || uint64(uintptr(h)) != h {
			return nil, nil, nil, errWorkerProtocol
		}
		for j := 0; j < i; j++ {
			if files[j].Fd() == uintptr(h) {
				return nil, nil, nil, errWorkerProtocol
			}
		}
		files[i] = os.NewFile(uintptr(h), key)
		if files[i] == nil {
			return nil, nil, nil, errWorkerProtocol
		}
		if i != 1 {
			kind, err := windows.GetFileType(windows.Handle(h))
			if err != nil || kind != windows.FILE_TYPE_PIPE {
				return nil, nil, nil, errWorkerProtocol
			}
		}
	}
	info, err := files[1].Stat()
	if err != nil || !info.IsDir() || workerPrivateDescriptor(files[1]) != nil {
		return nil, nil, nil, errWorkerProtocol
	}
	failed = false
	return files[0], files[1], files[2], nil
}
func workerInheritedRootPath(root *os.File) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(root.Fd()), &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		return "", errWorkerProtocol
	}
	path := windows.UTF16ToString(buffer[:n])
	if !filepath.IsAbs(path) {
		return "", errWorkerProtocol
	}
	before, err := root.Stat()
	after, statErr := os.Stat(path)
	if err != nil || statErr != nil || !after.IsDir() || !os.SameFile(before, after) {
		return "", errWorkerProtocol
	}
	return path, nil
}
func workerPlatformEnvironment(root string) []string {
	systemRoot, err := windows.GetSystemWindowsDirectory()
	if err != nil || !filepath.IsAbs(systemRoot) {
		return nil
	}
	return []string{"SystemRoot=" + systemRoot, "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "TEMP=" + root, "TMP=" + root}
}

type workerWindowsHandleOwner struct{ handle windows.Handle }

func (owner *workerWindowsHandleOwner) Close() error {
	if windows.CloseHandle(owner.handle) != nil {
		return errWorkerOwnership
	}
	return nil
}
