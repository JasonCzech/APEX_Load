package api

import (
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
	*storage.SQLiteStore
	entered chan struct{}
	release chan struct{}
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
	runner := NewTestRunner(1, NewHub(), store)
	now := time.Now().UTC()
	test := &ManagedTest{ID: "publication-visibility", Name: "publication visibility", ProjectID: storage.DefaultProjectID, Status: "RUNNING", CreatedAt: now, remoteFinalizing: true, LiveMetrics: &LiveMetrics{Timestamp: now, ActiveVUsers: 0}}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, Status: test.Status, StartTime: now}); err != nil {
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
			if _, err := store.GetRunArchive(test.ID); err != nil {
				t.Errorf("publication did not commit catalog: %v", err)
			}
			if _, err := store.GetArchiveFinalizationJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
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
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "COMPLETE" {
		t.Fatalf("terminal outcome not durable: %+v %v", durable, err)
	}
	if _, err := store.GetArchiveFinalizationJob(test.ID); err != nil {
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
	if _, err := store.GetRunArchive(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("catalog became visible before publication: %v", err)
	}
}
