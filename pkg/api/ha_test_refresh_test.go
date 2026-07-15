package api

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

type countingListTestStore struct {
	storage.Store
	listCalls atomic.Int32
}

func (s *countingListTestStore) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	s.listCalls.Add(1)
	return s.Store.ListTests(projectID, folderID)
}

func TestFollowerReadBurstCollapsesDurableTestRefresh(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := &countingListTestStore{Store: base}
	follower := newExecutionLeadership(store, "test-follower")
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(follower))
	store.listCalls.Store(0) // Ignore constructor hydration.

	const readers = 32
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			_ = runner.ListTests()
		}()
	}
	wait.Wait()

	if got := store.listCalls.Load(); got != 1 {
		t.Fatalf("follower read burst performed %d durable test scans, want 1", got)
	}
	// Leadership recovery is a forced reconciliation and must not inherit the
	// follower-read throttle.
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	if got := store.listCalls.Load(); got != 2 {
		t.Fatalf("forced reload left durable test scans at %d, want 2", got)
	}
}
