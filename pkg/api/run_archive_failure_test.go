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

func archiveFailureFixture(t *testing.T) (*storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	start := time.Now().UTC().Add(-time.Minute)
	end := start.Add(time.Minute)
	run := &ManagedTest{ID: "collection-failure", Name: "Failure", ProjectID: storage.DefaultProjectID, Status: "FAILED", CreatedAt: start, StartedAt: &start, EndedAt: &end}
	if err := s.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: start, StartedAt: &start, EndTime: &end, Status: run.Status}); err != nil {
		t.Fatal(err)
	}
	return s, run
}

func TestRunArchiveFailureReadbackAndScopedEvent(t *testing.T) {
	s, run := archiveFailureFixture(t)
	hub := NewHub()
	runner := NewTestRunner(1, hub, s)
	runner.tests[run.ID] = run
	// Joint batches held for the run's sidecar (B5): no archive was collected,
	// so recording the failure releases and counts them.
	run.pendingJointPublications, run.pendingJointBytes = []*storage.NativePublicationEvidence{{SourceID: "generator-a", Sequence: 3}}, 512
	runner.recordRemoteArchiveFailure(run, storage.RunArchiveFailureRemoteCollection)
	runner.mu.RLock()
	held, dropped := len(run.pendingJointPublications), run.jointPublicationsDropped
	runner.mu.RUnlock()
	if held != 0 || dropped != 1 {
		t.Fatalf("held joint batches after a remote archive failure: %d held, %d dropped", held, dropped)
	}
	select {
	case message := <-hub.broadcast:
		var event struct {
			Type      string         `json:"type"`
			TestID    string         `json:"test_id"`
			ProjectID string         `json:"project_id"`
			Failure   map[string]any `json:"archive_failure"`
		}
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "test_archive_failed" || event.TestID != run.ID || event.ProjectID != run.ProjectID || len(event.Failure) != 2 || event.Failure["reason"] != storage.RunArchiveFailureRemoteCollection {
			t.Fatalf("event: %s", message.payload)
		}
	default:
		t.Fatal("missing failure event")
	}
	// A newly constructed runner models a restarted controller/follower and must
	// hydrate failure state without relying on the producer's in-memory pointer.
	reopened := NewTestRunner(1, nil, s)
	for _, read := range []func() (*ManagedTest, bool){func() (*ManagedTest, bool) { return reopened.GetTest(run.ID) }, func() (*ManagedTest, bool) { return reopened.GetTestSummary(run.ID) }, func() (*ManagedTest, bool) { return reopened.GetTestMetricsWindow(run.ID, nil, 10) }} {
		got, ok := read()
		if !ok || got.ArchiveFailure == nil || got.ArchiveFailure.Reason != storage.RunArchiveFailureRemoteCollection || got.Status != "FAILED" {
			t.Fatalf("read: %+v %v", got, ok)
		}
		got.ArchiveFailure.Reason = "mutated projection"
	}
	if _, err := s.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failure created publication job: %v", err)
	}
	if err := s.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: storage.RunArchiveRecord{TestID: run.ID, CreatedAt: run.CreatedAt, CompletedAt: *run.EndedAt}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.GetTestSummary(run.ID); got.ArchiveFailure != nil {
		t.Fatal("publication intent did not supersede failure")
	}
}

type interceptedArchiveFailureStore struct {
	SQLiteStore *storage.SQLiteStore
	rejected    bool
	substitute  *storage.RunArchiveFailure
	reads       int
}

func (s *interceptedArchiveFailureStore) SaveRunArchiveFailure(f *storage.RunArchiveFailure, name, owner string) (bool, error) {
	if s.rejected {
		return false, nil
	}
	return s.SQLiteStore.SaveRunArchiveFailure(f, name, owner)
}
func (s *interceptedArchiveFailureStore) GetRunArchiveFailure(id string) (*storage.RunArchiveFailure, error) {
	s.reads++
	if s.substitute != nil {
		return s.substitute, nil
	}
	return s.SQLiteStore.GetRunArchiveFailure(id)
}

func TestRunArchiveFailureRejectsFencedAndReusedReadback(t *testing.T) {
	for _, mode := range []string{"fenced", "new incarnation", "other project", "other outcome", "other completion"} {
		t.Run(mode, func(t *testing.T) {
			s, run := archiveFailureFixture(t)
			wrapped := &interceptedArchiveFailureStore{SQLiteStore: s, rejected: mode == "fenced"}
			if !wrapped.rejected {
				f := &storage.RunArchiveFailure{TestID: run.ID, ProjectID: run.ProjectID, StartTime: run.CreatedAt, EndTime: *run.EndedAt, Status: run.Status, Reason: storage.RunArchiveFailureRemoteCollection}
				switch mode {
				case "new incarnation":
					f.StartTime = f.StartTime.Add(time.Second)
				case "other project":
					f.ProjectID = "other"
				case "other outcome":
					f.Status = "ABORTED"
				case "other completion":
					f.EndTime = f.EndTime.Add(time.Second)
				}
				wrapped.substitute = f
			}
			hub := NewHub()
			runner := NewTestRunner(1, hub, sqliteOverlay(wrapped))
			runner.tests[run.ID] = run
			runner.recordRemoteArchiveFailure(run, storage.RunArchiveFailureRemoteCollection)
			if wrapped.rejected && wrapped.reads != 0 {
				t.Fatal("fenced producer performed readback")
			}
			select {
			case message := <-hub.broadcast:
				t.Fatalf("stale producer emitted: %s", message.payload)
			default:
			}
			if !wrapped.rejected {
				snapshot := cloneManagedTestFieldsLocked(run)
				runner.attachRunArchiveFailure(snapshot)
				if snapshot.ArchiveFailure != nil {
					t.Fatal("stale identity attached on read")
				}
			}
		})
	}
}

func TestDistributedCompletionRecordsArchiveFailure(t *testing.T) {
	for _, tc := range []struct {
		status, reason string
		brokenRoot     bool
	}{
		{"FAILED", storage.RunArchiveFailureRemoteUnavailable, false},
		{"ABORTED", storage.RunArchiveFailureRemoteUnavailable, false},
		{"FAILED", storage.RunArchiveFailureRemoteCollection, true},
		{"COMPLETE", storage.RunArchiveFailureRemoteCollection, false},
	} {
		t.Run(tc.status+"/"+tc.reason, func(t *testing.T) {
			s, run := archiveFailureFixture(t)
			runner := NewTestRunner(1, nil, s)
			root := t.TempDir()
			if tc.brokenRoot {
				root = filepath.Join(root, "file")
				if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			run.Status = "RUNNING"
			run.EndedAt = nil
			if err := s.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, Status: run.Status}); err != nil {
				t.Fatal(err)
			}
			run.remoteTarget = "gen-a"
			run.remoteClient = &startupLifecycleFakeClient{accept: true}
			// Joint batches held for the sidecar (B5): no archive is collected,
			// so each of these completions releases and counts them.
			run.pendingJointPublications, run.pendingJointBytes = []*storage.NativePublicationEvidence{{SourceID: "gen-a", Sequence: 2}}, 256
			runner.tests[run.ID] = run
			backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
			backend.completeDistributedTestRun(runner, run, tc.status, "")
			runner.mu.RLock()
			held, dropped := len(run.pendingJointPublications), run.jointPublicationsDropped
			runner.mu.RUnlock()
			if held != 0 || dropped != 1 {
				t.Fatalf("held joint batches after a %s completion without an archive: %d held, %d dropped", tc.status, held, dropped)
			}
			wantStatus := tc.status
			if tc.status == "COMPLETE" {
				wantStatus = "FAILED"
			} // existing fail-closed archive semantics
			stored, err := s.GetTest(run.ID)
			if err != nil || stored.Status != wantStatus {
				t.Fatalf("terminal status: %+v %v", stored, err)
			}
			failure, err := s.GetRunArchiveFailure(run.ID)
			if err != nil || failure.Reason != tc.reason || failure.Status != wantStatus {
				t.Fatalf("failure: %+v %v", failure, err)
			}
		})
	}
}

func TestTerminalUnconfirmedArchiveReadbackAfterRestart(t *testing.T) {
	s, run := archiveFailureFixture(t)
	// Model the last durable write before process loss. No archive collection or
	// in-memory failure notification has happened, and no retry job is invented.
	runner := NewTestRunner(1, nil, s)
	if err := s.UpdateTestStatus(run.ID, "RUNNING", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := runner.persistStoredTestStatus(run.ID, "", "ABORTED", run.EndedAt, ""); err != nil {
		t.Fatal(err)
	}
	restarted := NewTestRunner(1, nil, s)
	got, ok := restarted.GetTestSummary(run.ID)
	if !ok || got.Status != "ABORTED" || got.ArchiveFailure == nil || got.ArchiveFailure.Reason != storage.RunArchiveFailureUnconfirmed {
		t.Fatalf("crash window was invisible: %+v", got)
	}
	if _, err := s.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("marker masquerades as retry job: %v", err)
	}
}
