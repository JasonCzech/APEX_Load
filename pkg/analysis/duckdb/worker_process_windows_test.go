//go:build windows

package duckdb

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The worker runs detached from any console, so no console host
// (conhost.exe) joins its job: the job's only process is the worker, and none
// is active once the worker is reaped. A child that exits mid-request is then
// reaped and joined like one that crashes slowly, instead of leaving its
// ownership unresolved.
func TestWorkerWindowsJobHoldsOnlyTheWorker(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	launch := workerRuntimeTestLaunch(t, a, "exit")
	var mu sync.Mutex
	var accounting workerWindowsJobAccounting
	accounted := false
	a.mu.Lock()
	a.closeDescriptorHook = func(d io.Closer) error {
		if owner, ok := d.(*workerProcessCloseOwner); ok {
			owner.process.mu.Lock()
			mu.Lock()
			accounted = windows.QueryInformationJobObject(owner.process.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil) == nil
			mu.Unlock()
			owner.process.mu.Unlock()
		}
		return d.Close()
	}
	a.mu.Unlock()
	_, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
	if err == nil || errors.Is(err, errWorkerOwnership) {
		t.Fatalf("a child that exited mid-request: %v", err)
	}
	if !a.reaped || !a.pipesJoined || admission.releases.Load() != 0 {
		t.Fatal("the exited child was not reaped and joined, or its lease was released")
	}
	mu.Lock()
	defer mu.Unlock()
	if !accounted || accounting.TotalProcesses != 1 || accounting.ActiveProcesses != 0 {
		t.Fatalf("the worker's job held %+v (accounted %v)", accounting, accounted)
	}
}

func TestWorkerWindowsEnvironmentStrictSortedPrivateBlock(t *testing.T) {
	for _, tc := range []struct {
		name            string
		input, expected []string
		bad             bool
	}{
		{name: "sorted", input: []string{"SystemRoot=C:\\Windows", "LANG=C.UTF-8", "TEMP=C:\\private", "APEXLOAD_NATIVE_ROOT_HANDLE=13"}, expected: []string{"APEXLOAD_NATIVE_ROOT_HANDLE=13", "LANG=C.UTF-8", "SystemRoot=C:\\Windows", "TEMP=C:\\private"}},
		{name: "empty", input: nil, expected: nil},
		{name: "alias", input: []string{"TEMP=a", "Temp=b"}, bad: true},
		{name: "duplicate", input: []string{"TEMP=a", "TEMP=b"}, bad: true},
		{name: "emptyname", input: []string{"=value"}, bad: true},
		{name: "non-ascii-name", input: []string{"TÉMP=a"}, bad: true},
		{name: "embeddednul", input: []string{"TEMP=a\x00b"}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := workerWindowsEnvironmentBlock(tc.input)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid environment admitted")
				}
				return
			}
			if err != nil || len(output) < 2 || output[len(output)-1] != 0 || output[len(output)-2] != 0 {
				t.Fatal("invalid termination", err)
			}
			text := string(utf16.Decode(output))
			var actual []string
			if trimmed := strings.TrimSuffix(text, "\x00\x00"); trimmed != "" {
				actual = strings.Split(trimmed, "\x00")
			}
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Fatalf("ordering differs: %#v", actual)
			}
		})
	}
}
