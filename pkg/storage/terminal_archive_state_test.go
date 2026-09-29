package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The same lifecycle entry points are exercised against SQLite and PostgreSQL.
func testTerminalArchiveState(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, method := range []string{"plain", "active", "lease", "materialization", "materialization lease"} {
		for _, status := range []string{"COMPLETE", "FAILED", "ABORTED"} {
			t.Run(method+"/"+status, func(t *testing.T) {
				id := "terminal-" + method + status
				start := time.Now().UTC().Add(-time.Minute)
				end := start.Add(time.Minute)
				if err := s.SaveTest(&TestRecord{ID: id, Name: id, StartTime: start, StartedAt: &start, Status: "RUNNING", MaterializationKey: id}); err != nil {
					t.Fatal(err)
				}
				if held, err := s.TryAcquireSchedulerLease(id, "owner", time.Now(), time.Minute); err != nil || !held {
					t.Fatalf("lease: %v %v", held, err)
				}
				lease, err := s.GetSchedulerLease(id)
				if err != nil {
					t.Fatal(err)
				}
				transition := func(owner, key string) error {
					var result TestStatusTransitionResult
					var err error
					switch method {
					case "plain":
						return s.UpdateTestStatus(id, status, &end, "")
					case "active":
						result, err = s.UpdateTestStatusIfActive(id, status, &end, "")
					case "lease":
						result, err = s.UpdateTestStatusIfActiveAndLeaseHeld(id, status, &end, "", id, owner)
					case "materialization":
						result, err = s.UpdateTestStatusIfActiveAndMaterializationKey(id, key, status, &end, "")
					case "materialization lease":
						result, err = s.UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld(id, key, status, &end, "", id, owner, lease.Generation)
					}
					if err == nil && result != TestStatusTransitionUpdated && result != TestStatusTransitionUnchanged {
						return errors.New("fenced")
					}
					return err
				}
				if method == "lease" || method == "materialization" || method == "materialization lease" {
					if err := transition("stale", "stale"); err == nil {
						t.Fatal("stale producer accepted")
					}
					if _, err := s.GetRunArchiveFailure(id); !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("stale producer created marker: %v", err)
					}
				}
				if err := transition("owner", id); err != nil {
					t.Fatal(err)
				}
				f, err := s.GetRunArchiveFailure(id)
				if err != nil || f.Reason != RunArchiveFailureUnconfirmed || f.Status != status {
					t.Fatalf("terminal marker: %+v %v", f, err)
				}
				stored, err := s.GetTest(id)
				if err != nil {
					t.Fatal(err)
				}
				if !f.StartTime.Equal(stored.StartTime) || !f.EndTime.Equal(*stored.EndTime) {
					t.Fatal("marker identity differs from native persisted precision")
				}
				if err := transition("owner", id); err != nil {
					t.Fatal(err)
				}
				again, err := s.GetRunArchiveFailure(id)
				if err != nil || !again.RecordedAt.Equal(f.RecordedAt) {
					t.Fatalf("retry rewrote marker: %+v %v", again, err)
				}
				f.Reason = RunArchiveFailureRemoteCollection
				if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || !saved {
					t.Fatalf("observed failure: %v %v", saved, err)
				}
				if err := transition("owner", id); err != nil {
					t.Fatal(err)
				}
				f, err = s.GetRunArchiveFailure(id)
				if err != nil || f.Reason != RunArchiveFailureRemoteCollection {
					t.Fatalf("placeholder overwrote observed failure: %+v %v", f, err)
				}
				if err := s.SaveArchiveFinalizationJob(&ArchiveFinalizationJob{TestID: id, ProjectID: DefaultProjectID, ArchiveRecord: RunArchiveRecord{TestID: id, CreatedAt: f.StartTime, CompletedAt: f.EndTime}}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.GetRunArchiveFailure(id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("outbox did not supersede marker: %v", err)
				}
			})
		}
	}
}

func TestTerminalArchiveStateAtomicAndFenced(t *testing.T) {
	testTerminalArchiveState(t, newHAFencingStore(t))
}

func TestTerminalArchiveMarkerFailureRollsBackStatus(t *testing.T) {
	s := newHAFencingStore(t)
	for _, status := range []string{"RUNNING", "FAILED"} {
		id := "rollback-" + status
		start := time.Now()
		if err := s.SaveTest(&TestRecord{ID: id, Name: id, StartTime: start, StartedAt: &start, Status: "RUNNING"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_marker BEFORE INSERT ON run_archive_failures BEGIN SELECT RAISE(ABORT, 'injected marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	end := time.Now()
	if _, err := s.UpdateTestStatusIfActive("rollback-FAILED", "FAILED", &end, ""); err == nil {
		t.Fatal("terminality committed without marker")
	}
	got, err := s.GetTest("rollback-FAILED")
	if err != nil || got.Status != "RUNNING" || got.EndTime != nil {
		t.Fatalf("not atomic: %+v %v", got, err)
	}
	if _, err := s.UpdateTestStatusIfActive("rollback-RUNNING", "PAUSED", nil, ""); err != nil {
		t.Fatalf("nonterminal transition touched markers: %v", err)
	}
}

func TestTerminalArchiveMarkerSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	start, end := time.Now().Add(-time.Minute), time.Now()
	if err := s.SaveTest(&TestRecord{ID: "crash", Name: "crash", StartTime: start, StartedAt: &start, Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTestStatusIfActive("crash", "ABORTED", &end, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, err := s.GetRunArchiveFailure("crash")
	if err != nil || f.Reason != RunArchiveFailureUnconfirmed {
		t.Fatalf("lost crash marker: %+v %v", f, err)
	}
}
