package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquireCreatesTheLockFileAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.lock")
	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file was not created: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// A released lock must be re-acquirable in the same process.
	secondRelease, err := Acquire(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := secondRelease(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

// TestAcquireSerializesConcurrentHolders is the property the durable stores
// depend on: publication and finalization must not interleave, or a finalizer
// can delete a content-addressed payload another writer is publishing.
func TestAcquireSerializesConcurrentHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.lock")

	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	acquired := make(chan struct{})
	var waiter sync.WaitGroup
	waiter.Add(1)
	go func() {
		defer waiter.Done()
		secondRelease, err := Acquire(path)
		if err != nil {
			t.Errorf("second Acquire: %v", err)
			return
		}
		close(acquired)
		_ = secondRelease()
	}()

	select {
	case <-acquired:
		t.Fatal("second holder acquired the lock while the first still held it")
	case <-time.After(50 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second holder did not acquire the lock after it was released")
	}
	waiter.Wait()
}

func TestAcquireRejectsAnUncreatablePath(t *testing.T) {
	// A path whose parent is a regular file cannot be opened.
	base := t.TempDir()
	blocker := filepath.Join(base, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Acquire(filepath.Join(blocker, "guard.lock")); err == nil {
		t.Fatal("Acquire accepted a path under a regular file")
	}
}
