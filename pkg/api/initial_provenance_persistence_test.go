package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPersistInitialArchiveProvenanceDoesNotOverwriteLifecycleState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.SaveTest(&storage.TestRecord{
		ID: "run-initial-provenance", Name: "Run", Status: "STOPPING",
		ProjectID: storage.DefaultProjectID, StartTime: now,
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.testStore = store
	test := &ManagedTest{
		ID: "run-initial-provenance", Name: "Run", Status: "INITIALIZING",
		ProjectID: storage.DefaultProjectID, CreatedAt: now,
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	provenance := &RunArchiveProvenance{ProjectID: storage.DefaultProjectID, TotalVUsers: 3, Duration: "3m"}
	if err := runner.persistInitialArchiveProvenance(test, provenance); err != nil {
		t.Fatalf("persistInitialArchiveProvenance: %v", err)
	}
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "STOPPING" {
		t.Fatalf("stored status = %q, want concurrent STOPPING state preserved", stored.Status)
	}
	decoded := decodeStoredInitialProvenance(stored.InitialProvenance)
	if decoded == nil || decoded.TotalVUsers != 3 {
		t.Fatalf("stored initial provenance = %#v, want targeted snapshot", decoded)
	}
}

func TestReloadStoredTestsHydratesInitialArchiveProvenanceIntoExistingIdentity(t *testing.T) {
	now := time.Now().UTC()
	const testID = "run-reload-initial-provenance"
	current := &ManagedTest{
		ID:        testID,
		Name:      "Run",
		Status:    "QUEUED",
		ProjectID: storage.DefaultProjectID,
		CreatedAt: now,
	}
	store := &reloadRaceStore{records: []*storage.TestRecord{{
		ID:        testID,
		Name:      "Run",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
		StartTime: now,
		InitialProvenance: map[string]any{
			"project_id":   storage.DefaultProjectID,
			"total_vusers": 7,
			"duration":     "7m",
		},
	}}}
	runner := &TestRunner{
		tests:        map[string]*ManagedTest{testID: current},
		archives:     map[string]*storage.RunArchiveRecord{},
		testStore:    store,
		historyStore: store,
	}

	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	if runner.tests[testID] != current {
		t.Fatal("same durable identity should be refreshed in place")
	}
	if got := current.initialArchiveProvenance; got == nil || got.TotalVUsers != 7 || got.Duration != "7m" {
		t.Fatalf("reloaded initial provenance = %#v, want immutable durable snapshot", got)
	}
}
