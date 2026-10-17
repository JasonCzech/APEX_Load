//go:build windows

package duckdb

// TEMPORARY diagnostic for PR #230: on Windows the worker tests whose child
// calls os.Exit mid-request ("exit", "exit-clean") end with "native worker
// ownership unresolved". This times when the child is reaped and every
// descriptor close (and, for the process, the job's active-process count),
// and lists what is still unjoined, as "DIAG" lines on stderr. It asserts
// nothing.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func workerDiagDescribe(d io.Closer) string {
	switch v := d.(type) {
	case *os.File:
		return "file " + v.Name()
	case *workerWindowsHandleOwner:
		return fmt.Sprintf("handle %#x", uintptr(v.handle))
	case *workerProcessCloseOwner:
		return "process"
	default:
		return fmt.Sprintf("%T", d)
	}
}

func workerDiagActiveProcesses(p *workerOwnedProcess) string {
	if p == nil || p.job == 0 {
		return "no job"
	}
	var accounting workerWindowsJobAccounting
	if err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
		return "query failed: " + err.Error()
	}
	return fmt.Sprintf("active=%d total=%d terminated=%d", accounting.ActiveProcesses, accounting.TotalProcesses, accounting.TotalTerminatedProcesses)
}

func TestWindowsWorkerExitDiagnostic(t *testing.T) {
	for _, mode := range []string{"exit", "panic"} {
		t.Run(mode, func(t *testing.T) {
			// Written to stderr, not t.Log: the CI step runs without -v, and
			// closes go on after the test returns.
			logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
			a, _ := workerRuntimeTestAttempt(t, context.Background())
			launch := workerRuntimeTestLaunch(t, a, mode)
			start := time.Now()
			since := func() string { return time.Since(start).Round(time.Millisecond).String() }
			a.mu.Lock()
			a.closeDescriptorHook = func(d io.Closer) error {
				began := time.Now()
				extra := ""
				if owner, ok := d.(*workerProcessCloseOwner); ok {
					owner.process.mu.Lock()
					extra = fmt.Sprintf(" reaped=%v %s", owner.process.reaped, workerDiagActiveProcesses(owner.process))
					owner.process.mu.Unlock()
				}
				err := d.Close()
				logf("DIAG %s close %s at %s took %s err=%v%s", mode, workerDiagDescribe(d), since(), time.Since(began).Round(time.Millisecond), err, extra)
				return err
			}
			a.mu.Unlock()
			options := workerRunOptions{
				probe:                 func(*workerAttempt, *os.Process) error { return nil },
				testLaunch:            launch,
				testAfterWait:         func() { logf("DIAG %s child reaped at %s", mode, since()) },
				testAfterWaitConsumed: func() { logf("DIAG %s loop consumed the wait at %s", mode, since()) },
			}
			_, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, options)
			logf("DIAG %s runWorkerParent returned at %s: %v", mode, since(), err)
			last := ""
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
				a.mu.Lock()
				var open []string
				for d, owner := range a.descriptors {
					if d == a.root || d == a.directory || owner.joined {
						continue
					}
					state := "closing"
					select {
					case <-owner.done:
						state = fmt.Sprintf("failed %v", owner.err)
					default:
					}
					open = append(open, workerDiagDescribe(d)+" "+state)
				}
				reaped, joined := a.reaped, a.pipesJoined
				a.mu.Unlock()
				sort.Strings(open)
				now := fmt.Sprintf("reaped=%v pipesJoined=%v unjoined=[%s]", reaped, joined, strings.Join(open, "; "))
				if now != last {
					logf("DIAG %s at %s: %s", mode, since(), now)
					last = now
				}
				if reaped && joined && len(open) == 0 {
					break
				}
			}
		})
	}
}
