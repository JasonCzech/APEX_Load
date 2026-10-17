//go:build windows

package browser

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has neither ps nor /proc. A Toolhelp snapshot lists every process
// with the PID of the process that created it, and each process in the tree
// is then read through one handle.
var procK32GetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS (its sizes are SIZE_T).
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// listProcessTreeSamples lists rootPID and its descendants with their
// working sets (Windows' resident memory) and CPU time. A root that is not
// running gives no samples.
func listProcessTreeSamples(ctx context.Context, rootPID int) ([]processSample, error) {
	if rootPID <= 0 {
		return nil, nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	children := map[int][]int{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parent := int(entry.ParentProcessID)
		children[parent] = append(children[parent], int(entry.ProcessID))
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return walkProcessTree(ctx, rootPID, children, readWindowsProcess)
}

// readWindowsProcess reads one running process through a single handle. It
// reports false when the process cannot be opened or read, or has exited (an
// exited process still opens while another holder keeps a handle to it).
func readWindowsProcess(pid int) (processTreeEntry, bool) {
	if pid <= 0 {
		return processTreeEntry{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return processTreeEntry{}, false
	}
	defer windows.CloseHandle(handle)
	if event, err := windows.WaitForSingleObject(handle, 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		return processTreeEntry{}, false
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return processTreeEntry{}, false
	}
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	if ok, _, _ := procK32GetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb)); ok == 0 {
		return processTreeEntry{}, false
	}
	return processTreeEntry{
		sample:  processSample{PID: pid, RSSKB: float64(counters.workingSetSize) / 1024, CPUTimeSeconds: float64(filetimeTicks(kernel)+filetimeTicks(user)) / 1e7},
		created: filetimeTicks(creation),
	}, true
}

// filetimeTicks is a FILETIME as one count of 100 ns ticks.
func filetimeTicks(t windows.Filetime) uint64 {
	return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime)
}
