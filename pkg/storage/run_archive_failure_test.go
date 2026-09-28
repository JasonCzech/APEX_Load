package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func seedArchiveFailureRun(t *testing.T, s *SQLiteStore) *RunArchiveFailure {
	t.Helper()
	start := time.Now().UTC().Add(-time.Minute)
	end := start.Add(time.Minute)
	f := &RunArchiveFailure{TestID: "archive-failure-run", ProjectID: DefaultProjectID, StartTime: start, EndTime: end, Status: "FAILED", Reason: RunArchiveFailureRemoteCollection}
	if err := s.SaveTest(&TestRecord{ID: f.TestID, Name: "Failure", ProjectID: f.ProjectID, StartTime: start, EndTime: &end, Status: f.Status}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunArchiveFailurePersistenceAndDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failure.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	f := seedArchiveFailureRun(t, s)
	if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || !saved {
		t.Fatalf("save: %v %v", saved, err)
	}
	first, err := s.GetRunArchiveFailure(f.TestID)
	if err != nil || first.RecordedAt.IsZero() {
		t.Fatalf("read: %+v %v", first, err)
	}
	f.Reason = RunArchiveFailureRemoteUnavailable
	if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || !saved {
		t.Fatalf("repeat: %v %v", saved, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetRunArchiveFailure(f.TestID)
	if err != nil || got.Reason != first.Reason || !got.RecordedAt.Equal(first.RecordedAt) || !got.StartTime.Equal(f.StartTime) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	if len(public) != 2 || public["reason"] != first.Reason || public["recorded_at"] == nil {
		t.Fatalf("public data: %s", encoded)
	}
	if err := s.DeleteTest(f.TestID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM run_archive_failures").Scan(&count); err != nil || count != 0 {
		t.Fatalf("delete did not cascade: %d %v", count, err)
	}
	replacement := seedArchiveFailureRun(t, s)
	if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || saved {
		t.Fatalf("old incarnation saved: %v %v", saved, err)
	}
	if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replacement inherited failure: %v", err)
	}
	if saved, err := s.SaveRunArchiveFailure(replacement, "", ""); err != nil || !saved {
		t.Fatalf("replacement: %v %v", saved, err)
	}
}

func TestRunArchiveFailureRejectsInvalidIdentity(t *testing.T) {
	s := newHAFencingStore(t)
	f := seedArchiveFailureRun(t, s)
	for _, tc := range []struct {
		name    string
		mutate  func(*RunArchiveFailure)
		wantErr bool
	}{
		{"empty ID", func(f *RunArchiveFailure) { f.TestID = "" }, true},
		{"missing start", func(f *RunArchiveFailure) { f.StartTime = time.Time{} }, true},
		{"missing end", func(f *RunArchiveFailure) { f.EndTime = time.Time{} }, true},
		{"active status", func(f *RunArchiveFailure) { f.Status = "RUNNING" }, true},
		{"arbitrary reason", func(f *RunArchiveFailure) { f.Reason = "secret transport error" }, true},
		{"project", func(f *RunArchiveFailure) { f.ProjectID = "other" }, false},
		{"creation", func(f *RunArchiveFailure) { f.StartTime = f.StartTime.Add(time.Nanosecond) }, false},
		{"completion", func(f *RunArchiveFailure) { f.EndTime = f.EndTime.Add(time.Nanosecond) }, false},
		{"outcome", func(f *RunArchiveFailure) { f.Status = "ABORTED" }, false},
		{"missing row", func(f *RunArchiveFailure) { f.TestID = "missing" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *f
			tc.mutate(&copy)
			saved, err := s.SaveRunArchiveFailure(&copy, "", "")
			if saved || (err != nil) != tc.wantErr {
				t.Fatalf("save=%v err=%v", saved, err)
			}
		})
	}
	if saved, err := s.SaveRunArchiveFailure(nil, "", ""); saved || err == nil {
		t.Fatal("nil accepted")
	}
}

func TestRunArchiveFailureLeaseAndPublicationPrecedence(t *testing.T) {
	for _, publication := range []string{"catalog", "outbox", "orphan"} {
		t.Run(publication, func(t *testing.T) {
			s := newHAFencingStore(t)
			f := seedArchiveFailureRun(t, s)
			if acquired, err := s.TryAcquireSchedulerLease(testExecutionLeaseName, "owner", time.Now(), time.Minute); err != nil || !acquired {
				t.Fatalf("lease: %v %v", acquired, err)
			}
			for _, pair := range [][2]string{{testExecutionLeaseName, "stale"}, {testExecutionLeaseName, ""}, {"", "owner"}} {
				saved, err := s.SaveRunArchiveFailure(f, pair[0], pair[1])
				if saved || ((pair[0] == "" || pair[1] == "") && err == nil) {
					t.Fatalf("invalid lease %v: %v %v", pair, saved, err)
				}
			}
			if saved, err := s.SaveRunArchiveFailure(f, testExecutionLeaseName, "owner"); err != nil || !saved {
				t.Fatalf("owner: %v %v", saved, err)
			}
			record := RunArchiveRecord{TestID: f.TestID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: f.StartTime, CompletedAt: f.EndTime}
			var err error
			if publication == "catalog" {
				err = s.SaveRunArchive(&record)
			} else if publication == "orphan" {
				var saved bool
				saved, err = s.SaveArchiveFinalizationJobIfOrphan(&ArchiveFinalizationJob{TestID: f.TestID, ProjectID: f.ProjectID, ArchiveRecord: record})
				if !saved && err == nil {
					t.Fatal("orphan intent not saved")
				}
			} else {
				err = s.SaveArchiveFinalizationJob(&ArchiveFinalizationJob{TestID: f.TestID, ProjectID: f.ProjectID, ArchiveRecord: record})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("superseded failure visible: %v", err)
			}
			if saved, err := s.SaveRunArchiveFailure(f, testExecutionLeaseName, "owner"); err != nil || saved {
				t.Fatalf("late failure: %v %v", saved, err)
			}
			// Retention/cancellation removes publication metadata later; an old
			// collection failure must not reappear as the current explanation.
			if _, err := s.db.Exec("DELETE FROM run_archives WHERE test_id = ?", f.TestID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteArchiveFinalizationJob(f.TestID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("resolved failure resurfaced after retention: %v", err)
			}
		})
	}
}

func TestRunArchiveFailureUpgradeAndProjectDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Model the pre-failure release 16 ledger, then reopen through the
	// normal convergence path rather than creating the new table in the test.
	if _, err := s.db.Exec("DROP TABLE run_archive_failures"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM schema_migrations WHERE version >= 17"); err != nil {
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
	f := seedArchiveFailureRun(t, s)
	f.TestID = "project-owned-failure"
	f.ProjectID = "failure-project"
	if err := s.SaveProject(&Project{ID: f.ProjectID, Name: "Failure project", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTest(&TestRecord{ID: f.TestID, Name: "Failure", ProjectID: f.ProjectID, StartTime: f.StartTime, EndTime: &f.EndTime, Status: f.Status}); err != nil {
		t.Fatal(err)
	}
	if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || !saved {
		t.Fatalf("upgraded save: %v %v", saved, err)
	}
	if err := s.DeleteProject(f.ProjectID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM run_archive_failures").Scan(&count); err != nil || count != 0 {
		t.Fatalf("project deletion: %d %v", count, err)
	}
}

func TestRunArchiveFailureFullRowReplacementInvalidatesOldRecord(t *testing.T) {
	s := newHAFencingStore(t)
	old := seedArchiveFailureRun(t, s)
	if saved, err := s.SaveRunArchiveFailure(old, "", ""); err != nil || !saved {
		t.Fatalf("save: %v %v", saved, err)
	}
	replacement := seedArchiveFailureRun(t, s)
	if _, err := s.GetRunArchiveFailure(old.TestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale record survived incarnation change: %v", err)
	}
	if saved, err := s.SaveRunArchiveFailure(old, "", ""); err != nil || saved {
		t.Fatalf("late old write: %v %v", saved, err)
	}
	if saved, err := s.SaveRunArchiveFailure(replacement, "", ""); err != nil || !saved {
		t.Fatalf("replacement write: %v %v", saved, err)
	}
	got, err := s.GetRunArchiveFailure(old.TestID)
	if err != nil || !got.StartTime.Equal(replacement.StartTime) {
		t.Fatalf("replacement read: %+v %v", got, err)
	}
}

func TestRunArchiveFailureSupersessionIsAtomic(t *testing.T) {
	for _, publication := range []string{"catalog", "outbox", "orphan"} {
		t.Run(publication, func(t *testing.T) {
			s := newHAFencingStore(t)
			f := seedArchiveFailureRun(t, s)
			if saved, err := s.SaveRunArchiveFailure(f, "", ""); err != nil || !saved {
				t.Fatalf("save: %v %v", saved, err)
			}
			if _, err := s.db.Exec(`CREATE TRIGGER reject_failure_resolution BEFORE DELETE ON run_archive_failures BEGIN SELECT RAISE(ABORT, 'injected resolution failure'); END`); err != nil {
				t.Fatal(err)
			}
			record := RunArchiveRecord{TestID: f.TestID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: f.StartTime, CompletedAt: f.EndTime}
			publish := func() error {
				if publication == "catalog" {
					return s.SaveRunArchive(&record)
				}
				job := &ArchiveFinalizationJob{TestID: f.TestID, ProjectID: f.ProjectID, ArchiveRecord: record}
				if publication == "orphan" {
					_, err := s.SaveArchiveFinalizationJobIfOrphan(job)
					return err
				}
				return s.SaveArchiveFinalizationJob(job)
			}
			if err := publish(); err == nil {
				t.Fatal("publication committed without resolving failure")
			}
			if _, err := s.GetRunArchiveFailure(f.TestID); err != nil {
				t.Fatalf("failure lost after rollback: %v", err)
			}
			if _, err := s.GetRunArchive(f.TestID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("catalog survived rollback: %v", err)
			}
			if _, err := s.GetArchiveFinalizationJob(f.TestID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("intent survived rollback: %v", err)
			}
			if _, err := s.db.Exec("DROP TRIGGER reject_failure_resolution"); err != nil {
				t.Fatal(err)
			}
			if err := publish(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("retry did not resolve failure: %v", err)
			}
		})
	}
}

func TestRunArchiveFailureCompletionClearsPreviouslyCoexistingRecord(t *testing.T) {
	s := newHAFencingStore(t)
	f := seedArchiveFailureRun(t, s)
	record := RunArchiveRecord{TestID: f.TestID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: f.StartTime, CompletedAt: f.EndTime}
	job := &ArchiveFinalizationJob{TestID: f.TestID, ProjectID: f.ProjectID, ArchiveRecord: record}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	// An older writer could leave a hidden failure beside the job. Completing
	// that pending job must also resolve the old record, not just hide it.
	if _, err := s.db.Exec(`INSERT INTO run_archive_failures (test_id, project_id, test_start_time, test_end_time, test_status, reason, recorded_at)
 SELECT id, project_id, start_time, end_time, status, ?, ? FROM tests WHERE id = ?`, f.Reason, time.Now().UTC(), f.TestID); err != nil {
		t.Fatal(err)
	}
	if done, err := s.CompleteArchiveFinalization(f.TestID, job.IntentToken, "", &record); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	if _, err := s.db.Exec("DELETE FROM run_archives WHERE test_id = ?", f.TestID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completion left old failure: %v", err)
	}
}
