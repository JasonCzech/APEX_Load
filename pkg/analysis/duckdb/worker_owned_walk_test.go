package duckdb

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkerQuotaWalkRetainsFailedDirectoryCloseAndCharge(t *testing.T) {
	shortenWorkerReleaseWait(t)
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	var failed atomic.Pointer[os.File]
	a.mu.Lock()
	a.closeDescriptorHook = func(owner io.Closer) error {
		if file, ok := owner.(*os.File); ok && file != a.directory {
			if info, err := file.Stat(); err == nil && info.IsDir() && failed.CompareAndSwap(nil, file) {
				return errWorkerOwnership
			}
		}
		return owner.Close()
	}
	a.mu.Unlock()
	if workerScratchQuotas(a) == nil {
		t.Fatal("quota walk dropped a directory close failure")
	}
	file := failed.Load()
	if file == nil {
		t.Fatal("directory ownership fixture not entered")
	}
	a.mu.Lock()
	owner := a.descriptors[file]
	a.mu.Unlock()
	if owner == nil || owner.descriptor != file || owner.joined {
		t.Fatal("internal directory close lost exact physical owner")
	}
	if !errors.Is(a.release(), errWorkerOwnership) || admission.releases.Load() != 0 {
		t.Fatal("failed directory released scratch admission")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	owner.err = nil
	owner.joined = true
	a.notifyPhysicalLocked()
	a.mu.Unlock()
	workerWaitPhysicalRelease(t, a)
}
func TestWorkerOwnedWalkReclaimsOnlySuccessfullyEndedRecordsAndBoundsDepth(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	a.mu.Lock()
	before := len(a.descriptors)
	a.mu.Unlock()
	for i := 0; i < 100; i++ {
		if err := workerScratchQuotas(a); err != nil {
			t.Fatal(err)
		}
	}
	a.mu.Lock()
	after := len(a.descriptors)
	a.mu.Unlock()
	if after != before {
		t.Fatal("ended quota descriptors accumulated", before, after)
	}
	path := strings.TrimSuffix(strings.Repeat("d/", 33), "/")
	if err := a.root.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if workerScratchQuotas(a) == nil {
		t.Fatal("unbounded private directory depth accepted")
	}
	workerWaitPhysicalRelease(t, a)
}

func TestWorkerQuotaWalkAllowsOnlyEnumeratedNestedRemovalAndStillJoins(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "quota"}[quota], func(t *testing.T) {
			a, _ := workerRuntimeTestAttempt(t, context.Background())
			if err := a.root.Mkdir("ephemeral", 0700); err != nil {
				t.Fatal(err)
			}
			walk := workerOwnedWalk
			if quota {
				walk = workerOwnedQuotaWalk
			}
			err := walk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
				if path == "ephemeral" {
					return a.root.Remove("ephemeral")
				}
				return err
			})
			if (err == nil) != quota {
				t.Fatal("strict identity/ephemeral quota distinction lost", err)
			}
			a.mu.Lock()
			for descriptor, owner := range a.descriptors {
				if descriptor != a.root && descriptor != a.directory && !owner.joined {
					t.Error("removed nested entry lost exact walker Close proof")
				}
			}
			a.mu.Unlock()
			workerWaitPhysicalRelease(t, a)
		})
	}
}

func TestWorkerQuotaWalkRejectsSymlinkReplacementAndCallbackBreach(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	if err := a.root.Mkdir("owned", 0700); err != nil {
		t.Fatal(err)
	}
	err := workerOwnedQuotaWalk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
		if path == "owned" {
			if err := a.root.Remove("owned"); err != nil {
				return err
			}
			return os.Symlink(t.TempDir(), filepath.Join(a.path, "owned"))
		}
		return err
	})
	if err == nil {
		t.Fatal("quota disappearance exception accepted a replacement symlink")
	}
	if err := workerOwnedQuotaWalk(a.root, a, func(string, fs.DirEntry, error) error { return errWorkerResourceLimit }); err == nil {
		t.Fatal("quota callback breach was hidden")
	}
	workerWaitPhysicalRelease(t, a)
}
