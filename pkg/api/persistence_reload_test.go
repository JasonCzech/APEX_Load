package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// reloadRaceStore is a minimal testPersistence + historyPersistence stub. Its
// GetHistory hook fires inside reloadStoredTests' check-then-act window (after
// the RLock liveness read, before the final write Lock), letting the test
// deterministically simulate a run starting concurrently with the reload.
type reloadRaceStore struct {
	records []*storage.TestRecord
	inject  func()
}

func (s *reloadRaceStore) SaveTest(*storage.TestRecord) error            { return nil }
func (s *reloadRaceStore) GetTest(string) (*storage.TestRecord, error)   { return nil, nil }
func (s *reloadRaceStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return s.records, nil
}
func (s *reloadRaceStore) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (s *reloadRaceStore) UpdateTestStatus(string, string, *time.Time, string) error { return nil }
func (s *reloadRaceStore) DeleteTest(string) error                                   { return nil }
func (s *reloadRaceStore) MoveTest(string, *string) error                            { return nil }
func (s *reloadRaceStore) SaveHistory(string, time.Time, any) error { return nil }
func (s *reloadRaceStore) GetHistory(string) ([]any, error) {
	if s.inject != nil {
		s.inject()
	}
	return nil, nil
}

// TestReloadStoredTestsPreservesConcurrentlyStartedRun reproduces the HA failover
// race: reloadStoredTests must not overwrite a test that became live (cancel set)
// in the window between its liveness read and its write, or Stop/Abort would lose
// the cancel func and could never stop the run.
func TestReloadStoredTestsPreservesConcurrentlyStartedRun(t *testing.T) {
	const id = "t1"
	live := &ManagedTest{ID: id, Status: "RUNNING", cancel: func() {}}

	tr := &TestRunner{
		tests:    map[string]*ManagedTest{},
		archives: map[string]*storage.RunArchiveRecord{},
	}
	store := &reloadRaceStore{
		records: []*storage.TestRecord{{ID: id, Status: "COMPLETED"}},
		// Simulate startManagedTestRun landing in the check-then-act window.
		inject: func() {
			tr.mu.Lock()
			tr.tests[id] = live
			tr.mu.Unlock()
		},
	}
	tr.testStore = store
	tr.historyStore = store

	tr.reloadStoredTests()

	got := tr.tests[id]
	if got != live {
		t.Fatalf("reload clobbered the concurrently-started run (got %p, want live %p)", got, live)
	}
	if got.cancel == nil {
		t.Fatal("live run lost its cancel func; Stop/Abort could no longer stop it")
	}
}
