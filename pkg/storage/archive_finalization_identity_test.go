package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestArchiveFinalizationTerminalIdentity(t *testing.T) {
	checkArchiveFinalizationTerminalIdentity(t, newHAFencingStore(t))
}
func checkArchiveFinalizationTerminalIdentity(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, mode := range []string{"valid", "offset roundtrip", "new refresh", "start changed", "end changed", "status changed", "active", "project mismatch", "job project mismatch", "stale owner", "released lease", "expired lease", "deleted", "new token", "cleanup"} {
		t.Run("completion "+mode, func(t *testing.T) {
			id := fmt.Sprintf("completion-%d", time.Now().UnixNano())
			now := time.Now().UTC()
			if mode == "offset roundtrip" {
				now = now.In(time.FixedZone("EDT", -4*60*60))
			}
			start := now.Add(-time.Minute)
			row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, StartTime: start, EndTime: &now, Status: "COMPLETE"}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			refresh, err := s.BumpArchiveRefreshJob(id)
			if err != nil {
				t.Fatal(err)
			}
			record := RunArchiveRecord{TestID: id, CreatedAt: start.Add(time.Second), CompletedAt: now, ArchivePath: "/archive/" + id, ManifestPath: "/archive/" + id + "/manifest.json"}
			job := &ArchiveFinalizationJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: record, RefreshToken: refresh.IntentToken}
			if err := s.SaveArchiveFinalizationJob(job); err != nil {
				t.Fatal(err)
			}
			token := job.IntentToken
			identity := ArchiveFinalizationRunIdentity{ProjectID: row.ProjectID, StartTime: start, EndTime: now, Status: row.Status, LeaseName: id, LeaseOwner: "owner"}
			if ok, err := s.TryAcquireSchedulerLease(id, identity.LeaseOwner, time.Now(), time.Minute); err != nil || !ok {
				t.Fatal(err)
			}
			switch mode {
			case "offset roundtrip":
				identity.StartTime = identity.StartTime.UTC()
				identity.EndTime = identity.EndTime.UTC()
			case "new refresh":
				if _, err := s.BumpArchiveRefreshJob(id); err != nil {
					t.Fatal(err)
				}
			case "start changed":
				row.StartTime = row.StartTime.Add(time.Second)
				if err := s.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "end changed":
				end := now.Add(time.Second)
				row.EndTime = &end
				if err := s.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "status changed", "active":
				row.Status = "FAILED"
				if mode == "active" {
					row.Status = "RUNNING"
				}
				if err := s.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "project mismatch":
				identity.ProjectID = "different-project"
			case "job project mismatch":
				project := &Project{ID: id + "-project", Name: id}
				if err := s.SaveProject(project); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE archive_finalization_jobs SET project_id = ? WHERE test_id = ?`, project.ID, id); err != nil {
					t.Fatal(err)
				}
			case "stale owner":
				identity.LeaseOwner = "stale"
			case "released lease":
				if err := s.ReleaseSchedulerLease(id, identity.LeaseOwner); err != nil {
					t.Fatal(err)
				}
			case "expired lease":
				if _, err := s.db.Exec(`UPDATE scheduler_leases SET expires_at = ? WHERE name = ?`, time.Now().Add(-time.Second), id); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := s.DeleteTest(id); err != nil {
					t.Fatal(err)
				}
			case "new token":
				if err := s.SaveArchiveFinalizationJob(job); err != nil {
					t.Fatal(err)
				}
			case "cleanup":
				if err := s.SaveArchiveCleanupJob(&ArchiveCleanupJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: record}); err != nil {
					t.Fatal(err)
				}
			}
			committed, err := s.CompleteArchiveFinalizationForRun(id, token, refresh.IntentToken, &record, identity)
			want := mode == "valid" || mode == "offset roundtrip" || mode == "new refresh"
			if err != nil || committed != want {
				t.Fatalf("commit %s=%v: %v", mode, committed, err)
			}
			catalog, err := s.GetRunArchive(id)
			if want {
				if err != nil || catalog == nil {
					t.Fatal("catalog missing", err)
				}
				created := record.CreatedAt
				if s.db.dialect == storeDialectPostgres {
					created = created.Truncate(time.Microsecond)
				}
				if !catalog.CreatedAt.Equal(created) {
					t.Fatal("generator creation timestamp replaced with request time")
				}
				if _, err := s.GetArchiveFinalizationJob(id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("completed outbox retained", err)
				}
				_, err = s.GetArchiveRefreshJob(id)
				if mode == "new refresh" && err != nil {
					t.Fatal("newer refresh lost", err)
				}
				if mode == "valid" && !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("completed refresh not acknowledged", err)
				}
			} else {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("rejected completion published", err)
				}
				if mode != "deleted" && mode != "cleanup" {
					if pending, err := s.GetArchiveFinalizationJob(id); err != nil || pending == nil {
						t.Fatal("rejected completion erased pending work", err)
					}
				}
			}
		})
	}

	t.Run("completion single winner", func(t *testing.T) {
		now := time.Now().UTC()
		id := fmt.Sprintf("completion-concurrent-%d", now.UnixNano())
		row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: &now, Status: "FAILED"}
		if err := s.SaveTest(row); err != nil {
			t.Fatal(err)
		}
		record := RunArchiveRecord{TestID: id, CreatedAt: row.StartTime.Add(time.Second), CompletedAt: now}
		job := &ArchiveFinalizationJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: record}
		if err := s.SaveArchiveFinalizationJob(job); err != nil {
			t.Fatal(err)
		}
		identity := ArchiveFinalizationRunIdentity{ProjectID: row.ProjectID, StartTime: row.StartTime, EndTime: now, Status: row.Status}
		var workers sync.WaitGroup
		results := make(chan bool, 8)
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			workers.Go(func() {
				copy := record
				ok, err := s.CompleteArchiveFinalizationForRun(id, job.IntentToken, "", &copy, identity)
				results <- ok
				failures <- err
			})
		}
		workers.Wait()
		close(results)
		close(failures)
		wins := 0
		for ok := range results {
			if ok {
				wins++
			}
		}
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		if wins != 1 {
			t.Fatalf("completion winners=%d", wins)
		}
	})
}

func TestArchiveFinalizationRejectsIncompleteIdentity(t *testing.T) {
	s := newHAFencingStore(t)
	now := time.Now().UTC()
	record := &RunArchiveRecord{TestID: "test", CompletedAt: now}
	valid := ArchiveFinalizationRunIdentity{ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: now, Status: "COMPLETE"}
	for _, mode := range []string{"start", "end", "status", "lease", "owner", "record end", "nil record"} {
		t.Run(mode, func(t *testing.T) {
			identity := valid
			copy := *record
			input := &copy
			switch mode {
			case "start":
				identity.StartTime = time.Time{}
			case "end":
				identity.EndTime = time.Time{}
			case "status":
				identity.Status = "RUNNING"
			case "lease":
				identity.LeaseName = "lease"
			case "owner":
				identity.LeaseOwner = "owner"
			case "record end":
				copy.CompletedAt = now.Add(time.Second)
			case "nil record":
				input = nil
			}
			if ok, err := s.CompleteArchiveFinalizationForRun("test", "token", "", input, identity); err == nil || ok {
				t.Fatal("invalid completion accepted")
			}
		})
	}
}
