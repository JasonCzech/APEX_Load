package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestArchiveRecoveredPublicationRefreshIsAtomic(t *testing.T) {
	checkArchiveRecoveredPublicationRefreshIsAtomic(t, newHAFencingStore(t))
}
func checkArchiveRecoveredPublicationRefreshIsAtomic(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, mode := range []string{"new dirty", "existing dirty", "newer dirty", "changed identity", "stale owner"} {
		t.Run("recovered refresh "+mode, func(t *testing.T) {
			now := time.Now().UTC()
			id := fmt.Sprintf("recovered-refresh-%d", now.UnixNano())
			row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: &now, Status: "COMPLETE"}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			record := RunArchiveRecord{TestID: id, CreatedAt: row.StartTime, CompletedAt: now}
			job := &ArchiveFinalizationJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: record}
			var existing *ArchiveRefreshJob
			if mode == "existing dirty" || mode == "newer dirty" {
				var err error
				existing, err = s.BumpArchiveRefreshJob(id)
				if err != nil {
					t.Fatal(err)
				}
				job.RefreshToken = existing.IntentToken
				if mode == "newer dirty" {
					existing, err = s.BumpArchiveRefreshJob(id)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.SaveArchiveFinalizationJob(job); err != nil {
				t.Fatal(err)
			}
			identity := ArchiveFinalizationRunIdentity{ProjectID: row.ProjectID, StartTime: row.StartTime, EndTime: now, Status: row.Status, RequireAdjunctRefresh: true}
			if mode == "changed identity" {
				identity.StartTime = identity.StartTime.Add(time.Second)
			}
			if mode == "stale owner" {
				identity.LeaseName = id
				identity.LeaseOwner = "stale"
			}
			committed, err := s.CompleteArchiveFinalizationForRun(id, job.IntentToken, job.RefreshToken, &record, identity)
			want := mode != "changed identity" && mode != "stale owner"
			if err != nil || committed != want {
				t.Fatal("recovered completion", committed, err)
			}
			dirty, err := s.GetArchiveRefreshJob(id)
			if want {
				if err != nil || dirty == nil {
					t.Fatal("recovery lost adjunct reconciliation", err)
				}
				if existing != nil && (dirty.IntentToken != existing.IntentToken || dirty.Generation != existing.Generation) {
					t.Fatal("recovery replaced existing dirty work")
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("rejected recovery changed dirty state", err)
			}
		})
	}
}
