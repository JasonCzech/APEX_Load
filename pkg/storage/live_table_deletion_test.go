package storage

import (
	"context"
	"testing"
	"time"
)

func TestLiveTableEncryptedStateFollowsRunAndProjectDeletion(t *testing.T) {
	for _, target := range []string{"run", "project"} {
		t.Run(target, func(t *testing.T) {
			s := newHAFencingStore(t)
			r, term := liveTableTestFixture(t, s)
			if target == "project" {
				r.ProjectID = "live-project"
				if err := s.SaveProject(&Project{ID: r.ProjectID, Name: "Live project"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE tests SET project_id=? WHERE id=?`, r.ProjectID, r.TestID); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			r.SeedMetadata = "encrypted private seed"
			if _, err := s.InitializeLiveTable(ctx, r, []byte("encrypted checkpoint"), term); err != nil {
				t.Fatal(err)
			}
			if err := s.AppendLiveTableEntry(ctx, LiveTableEntry{TableID: r.ID, Revision: 1, PreviousRevision: 0, At: time.Now(), Payload: []byte("encrypted journal")}, term); err != nil {
				t.Fatal(err)
			}
			closed := r
			closed.ID, closed.Alias = "closed-table", "closed"
			if _, err := s.InitializeLiveTable(ctx, closed, []byte("encrypted final checkpoint"), term); err != nil {
				t.Fatal(err)
			}
			if err := s.CloseLiveTable(ctx, closed.ID, 0, term); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FinalizeLiveTable(ctx, LiveTableFinalizationRequest{TableID: closed.ID, ProjectID: closed.ProjectID, Mode: "discard"}, term); err != nil {
				t.Fatal(err)
			}
			var err error
			if target == "project" {
				err = s.DeleteProject(r.ProjectID)
			} else {
				err = s.DeleteTest(r.TestID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"live_tables", "live_table_journal", "live_table_finalizations"} {
				var count int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s retained after %s deletion: %d %v", table, target, count, err)
				}
			}
		})
	}
}
