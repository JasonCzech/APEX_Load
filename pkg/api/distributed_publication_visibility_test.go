package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type gatedPublicationStore struct {
	SQLiteStore *storage.SQLiteStore
	entered     chan struct{}
	release     chan struct{}
}

func (s *gatedPublicationStore) CompleteArchiveFinalization(id, token, refresh string, record *storage.RunArchiveRecord) (bool, error) {
	close(s.entered)
	<-s.release
	return s.SQLiteStore.CompleteArchiveFinalization(id, token, refresh, record)
}

func (s *gatedPublicationStore) CompleteArchiveFinalizationForRun(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity) (bool, error) {
	close(s.entered)
	<-s.release
	return s.SQLiteStore.CompleteArchiveFinalizationForRun(id, token, refresh, record, identity)
}

func TestDistributedCompletionVisibleWhilePublicationPending(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &gatedPublicationStore{SQLiteStore: sqlite, entered: make(chan struct{}), release: make(chan struct{})}
	runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
	now := time.Now().UTC()
	test := &ManagedTest{ID: "publication-visibility", Name: "publication visibility", ProjectID: storage.DefaultProjectID, Status: "RUNNING", CreatedAt: now, remoteFinalizing: true, LiveMetrics: &LiveMetrics{Timestamp: now, ActiveVUsers: 0}}
	if err := store.SQLiteStore.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, Status: test.Status, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	artifacts := map[string]string{}
	for _, name := range []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"} {
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		artifacts[name] = path
	}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: test.ID, TestName: test.Name, Status: "COMPLETE", CreatedAt: now, CompletedAt: now, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
	artifacts[runArchiveManifestFileName] = filepath.Join(source, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(artifacts[runArchiveManifestFileName], manifest); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: artifacts}
	test.remoteClientsByTarget = map[string]generatorClient{"generator": client}
	runner.tests[test.ID] = test
	backend := newDistributedExecutionBackend([]string{"generator"}, time.Second, nil)
	done := make(chan struct{})
	go func() { defer close(done); backend.completeDistributedTestRun(runner, test, "COMPLETE", "") }()
	defer func() {
		close(store.release)
		select {
		case <-done:
			if _, err := store.SQLiteStore.GetRunArchive(test.ID); err != nil {
				t.Errorf("publication did not commit catalog: %v", err)
			}
			if _, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Errorf("publication intent did not retire: %v", err)
			}
			if snapshot, ok := runner.GetTestSummary(test.ID); !ok || snapshot.Archive == nil || snapshot.Status != "COMPLETE" {
				t.Error("publication was not attached to the completed run")
			}
		case <-time.After(5 * time.Second):
			t.Error("publication did not finish")
		}
	}()
	select {
	case <-store.entered:
	case <-done:
		t.Fatal("publication gate was not reached")
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not start")
	}
	durable, err := store.SQLiteStore.GetTest(test.ID)
	if err != nil || durable.Status != "COMPLETE" {
		t.Fatalf("terminal outcome not durable: %+v %v", durable, err)
	}
	if _, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := runner.GetTestSummary(test.ID)
	if !ok || snapshot.Status != "COMPLETE" || snapshot.EndedAt == nil || snapshot.LiveMetrics == nil || snapshot.LiveMetrics.ObservationState != "historical" {
		t.Fatalf("durably completed run still presented as active: found=%v snapshot=%+v", ok, snapshot)
	}
	foundCompletion := false
readEvents:
	for {
		select {
		case message := <-runner.hub.broadcast:
			var event struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(message.payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "test_complete" {
				foundCompletion = true
			}
		default:
			break readEvents
		}
	}
	if !foundCompletion {
		t.Fatal("completion event remained blocked behind archive publication")
	}
	if snapshot.Archive != nil {
		t.Fatal("uncommitted archive exposed")
	}
	if _, err := store.SQLiteStore.GetRunArchive(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("catalog became visible before publication: %v", err)
	}
}

// failFirstPublicationStore refuses the first archive publication commit.
type failFirstPublicationStore struct {
	SQLiteStore *storage.SQLiteStore
	failed      bool
}

func (s *failFirstPublicationStore) CompleteArchiveFinalizationForRun(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity) (bool, error) {
	if !s.failed {
		s.failed = true
		return false, errors.New("catalog unavailable")
	}
	return s.SQLiteStore.CompleteArchiveFinalizationForRun(id, token, refresh, record, identity)
}

func (s *failFirstPublicationStore) CompleteArchiveFinalization(id, token, refresh string, record *storage.RunArchiveRecord) (bool, error) {
	if !s.failed {
		s.failed = true
		return false, errors.New("catalog unavailable")
	}
	return s.SQLiteStore.CompleteArchiveFinalization(id, token, refresh, record)
}

// A staged archive whose publication fails is retried from the outbox: the
// joint batches held for its sidecar (B5) stay held for that retry, and its
// commit releases them.
func TestDistributedPublicationRetryKeepsHeldJointBatches(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &failFirstPublicationStore{SQLiteStore: sqlite}
	runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
	// The refused commit queues a background publication retry; stop it and
	// wait for it before the store closes and the archive root is restored.
	defer func() {
		runner.mu.Lock()
		runner.shuttingDown = true
		runner.mu.Unlock()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			runner.terminalRetryMu.Lock()
			active := len(runner.terminalRetrying)
			runner.terminalRetryMu.Unlock()
			if active == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("the publication retry worker did not stop")
	}()
	now := time.Now().UTC()
	test := &ManagedTest{ID: "publication-retry-joint", Name: "publication retry", ProjectID: storage.DefaultProjectID, Status: "RUNNING", CreatedAt: now, remoteFinalizing: true, LiveMetrics: &LiveMetrics{Timestamp: now, ActiveVUsers: 0},
		pendingJointPublications: []*storage.NativePublicationEvidence{{SourceID: "generator", Sequence: 5}}, pendingJointBytes: 256}
	if err := store.SQLiteStore.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, Status: test.Status, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	artifacts := map[string]string{}
	for _, name := range []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"} {
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		artifacts[name] = path
	}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: test.ID, TestName: test.Name, Status: "COMPLETE", CreatedAt: now, CompletedAt: now, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
	artifacts[runArchiveManifestFileName] = filepath.Join(source, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(artifacts[runArchiveManifestFileName], manifest); err != nil {
		t.Fatal(err)
	}
	test.remoteClientsByTarget = map[string]generatorClient{"generator": &archiveStreamingFakeClient{artifacts: artifacts}}
	runner.tests[test.ID] = test
	newDistributedExecutionBackend([]string{"generator"}, time.Second, nil).completeDistributedTestRun(runner, test, "COMPLETE", "")
	if !store.failed {
		t.Fatal("the first publication was not attempted")
	}
	runner.mu.RLock()
	held, dropped, attached := len(test.pendingJointPublications), test.jointPublicationsDropped, test.Archive != nil
	runner.mu.RUnlock()
	if attached || held != 1 || dropped != 0 {
		t.Fatalf("after a failed publication: attached %v, %d held, %d dropped", attached, held, dropped)
	}
	job, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatalf("no publication retry was queued: %v", err)
	}
	if err := runner.processArchiveFinalizationJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	held, dropped, attached = len(test.pendingJointPublications), test.jointPublicationsDropped, test.Archive != nil
	runner.mu.RUnlock()
	if !attached || held != 0 || dropped != 1 {
		t.Fatalf("after the retried publication: attached %v, %d held, %d dropped", attached, held, dropped)
	}
}
