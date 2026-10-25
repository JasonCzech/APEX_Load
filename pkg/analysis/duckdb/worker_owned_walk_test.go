package duckdb

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	began := time.Now()
	if workerScratchQuotas(a) == nil {
		t.Fatal("unbounded private directory depth accepted")
	}
	// The depth bound fails the walk outright, without a vanish recheck.
	if waited := time.Since(began); waited >= workerWalkVanishWait/2 {
		t.Fatalf("a depth breach waited %s for its directory to vanish", waited)
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

// Windows keeps a deleted directory delete-pending, refusing every open and
// stat, until its last handle closes. A nested directory the quota walk fails
// on and that then disappears is an ephemeral entry; one that stays, or a
// callback refusal inside it, still fails the walk.
func TestWorkerQuotaWalkExcusesANestedFailureOnlyWhenTheEntryVanishes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quota    bool
		vanishes bool
		ok       bool
	}{
		{"strict", false, true, false},
		{"quota vanishes", true, true, true},
		{"quota stays", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := workerRuntimeTestAttempt(t, context.Background())
			if err := a.root.Mkdir("pending", 0700); err != nil {
				t.Fatal(err)
			}
			walk := workerOwnedWalk
			if tc.quota {
				walk = workerOwnedQuotaWalk
			}
			removed := make(chan struct{})
			err := walk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
				if path != "pending" {
					return err
				}
				// The directory turns into something the walk refuses, and is
				// removed a moment later, as a delete-pending one would be.
				if err := a.root.Remove("pending"); err != nil {
					return err
				}
				if err := a.root.WriteFile("pending", nil, 0600); err != nil {
					return err
				}
				if !tc.vanishes {
					close(removed)
					return nil
				}
				go func() {
					defer close(removed)
					time.Sleep(100 * time.Millisecond)
					_ = a.root.Remove("pending")
				}()
				return nil
			})
			<-removed
			if (err == nil) != tc.ok {
				t.Fatalf("walk = %v, want ok %v", err, tc.ok)
			}
			_ = a.root.Remove("pending")
			workerWaitPhysicalRelease(t, a)
		})
	}

	// A refusal from the callback inside a directory that then vanishes is
	// still a refusal.
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	if err := a.root.MkdirAll("gone/inner", 0700); err != nil {
		t.Fatal(err)
	}
	err := workerOwnedQuotaWalk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
		if path == filepath.Join("gone", "inner") {
			_ = a.root.RemoveAll("gone")
			return errWorkerResourceLimit
		}
		return err
	})
	if err == nil {
		t.Fatal("a callback refusal inside a vanished directory was excused")
	}
	workerWaitPhysicalRelease(t, a)
}

// A nested failure that a recheck cannot resolve while the walk still holds
// its parent open goes to the parent's recheck, made once that handle closes,
// as a Windows directory deleted under an open handle finishes vanishing then.
func TestWorkerQuotaWalkHandsAnUnresolvedFailureToTheParentRecheck(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "quota"}[quota], func(t *testing.T) {
			a, _ := workerRuntimeTestAttempt(t, context.Background())
			if err := a.root.MkdirAll(filepath.Join("parent", "child"), 0700); err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			a.closeDescriptorHook = func(owner io.Closer) error {
				err := owner.Close()
				if file, ok := owner.(*os.File); ok && filepath.Base(file.Name()) == "parent" {
					_ = a.root.Remove("parent")
				}
				return err
			}
			a.mu.Unlock()
			walk := workerOwnedWalk
			if quota {
				walk = workerOwnedQuotaWalk
			}
			began := time.Now()
			err := walk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
				if path != filepath.Join("parent", "child") {
					return err
				}
				// os.RemoveAll deletes as release does: Windows leaves the open
				// parent delete-pending, so names beneath it cannot be statted.
				// Elsewhere a file in its place fails the same way, with "not a
				// directory".
				if err := os.RemoveAll(filepath.Join(a.path, "parent")); err != nil {
					return err
				}
				if runtime.GOOS != "windows" {
					return a.root.WriteFile("parent", nil, 0600)
				}
				return nil
			})
			if (err == nil) != quota {
				t.Fatalf("walk = %v, want ok %v", err, quota)
			}
			// Nothing beneath a parent that is no longer itself can resolve
			// while the walk holds it, so no wait is spent there.
			if waited := time.Since(began); waited >= workerWalkVanishWait/2 {
				t.Fatalf("the walk waited %s beneath a parent only its ancestor can recheck", waited)
			}
			workerWaitPhysicalRelease(t, a)
		})
	}
}

// One walk's rechecks share a single wait, so entries that each vanish just
// before their own wait would end cannot stall the walk one after another.
func TestWorkerQuotaWalkSharesOneVanishWait(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	for _, name := range []string{"first", "second"} {
		if err := a.root.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// The first entry visited vanishes halfway through the wait and is
	// excused; the second would vanish within a wait of its own, but only the
	// rest of the shared one is left.
	var removals sync.WaitGroup
	visited := 0
	err := workerOwnedQuotaWalk(a.root, a, func(path string, entry fs.DirEntry, err error) error {
		if path != "first" && path != "second" {
			return err
		}
		visited++
		vanishAfter := workerWalkVanishWait / 2
		if visited == 2 {
			vanishAfter = workerWalkVanishWait * 7 / 10
		}
		if err := a.root.Remove(path); err != nil {
			return err
		}
		if err := a.root.WriteFile(path, nil, 0600); err != nil {
			return err
		}
		removals.Add(1)
		go func() {
			defer removals.Done()
			time.Sleep(vanishAfter)
			_ = a.root.Remove(path)
		}()
		return nil
	})
	removals.Wait()
	if visited != 2 || err == nil {
		t.Fatalf("visited %d, walk = %v: the second late vanish got a wait of its own", visited, err)
	}
	workerWaitPhysicalRelease(t, a)
}
