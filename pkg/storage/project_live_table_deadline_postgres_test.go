//go:build postgres_integration

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresProjectLiveTableProjectLockHonorsDeadline(t *testing.T) {
	s := newPostgresLiveTableTestStore(t).SQLiteStore
	// Keep the blocker and mutation on separate connections; this exercises a
	// PostgreSQL row lock, not a canceled wait for a connection-pool slot.
	s.db.db.SetMaxOpenConns(4)
	input, term := projectTableFixture(t, s)
	if _, err := s.CreateProjectLiveTable(context.Background(), input, term); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "append", "detach", "prune", "rendezvous"} {
		t.Run(operation, func(t *testing.T) {
			blocker, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err = blocker.Exec(`UPDATE projects SET updated_at=updated_at WHERE id=?`, DefaultProjectID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "create":
					next := input
					next.Definition.ID, next.Definition.CurrentInstanceID = "second-table", "second-instance"
					next.Instance.ID, next.Instance.TableID = "second-instance", "second-table"
					_, err = s.CreateProjectLiveTable(ctx, next, term)
				case "append":
					err = s.AppendProjectLiveTableEntry(ctx, ProjectLiveTableEntry{LiveTableEntry: LiveTableEntry{TableID: input.Instance.ID, Revision: 1, At: time.Now(), Payload: []byte(`{"encrypted":"blocked"}`)}}, term)
				case "detach":
					err = s.DetachProjectLiveTableAttachments(ctx, DefaultProjectID, "live-run", time.Now().Add(time.Minute), term)
				case "prune":
					_, err = s.PruneProjectLiveTableInstances(ctx, DefaultProjectID, term)
				case "rendezvous":
					err = s.InitializeRendezvous(ctx, RendezvousRecord{RunID: "live-run", Group: "group", ProjectID: DefaultProjectID, Snapshot: []byte(`{"encrypted":"barrier"}`)}, term)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("project row lock did not preserve timeout: %v", err)
				}
			case <-time.After(2 * time.Second):
				// A failing regression still releases and joins its blocked worker.
				_ = blocker.Rollback()
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("store worker did not exit after unlock")
				}
				t.Fatal("project row lock ignored operation deadline")
			}
		})
	}
	definitions, err := s.ListProjectLiveTables(context.Background(), DefaultProjectID)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("canceled create published: %+v %v", definitions, err)
	}
	instance, err := s.GetProjectLiveTableInstance(context.Background(), DefaultProjectID, input.Instance.ID)
	if err != nil || instance.Revision != 0 {
		t.Fatalf("canceled journal advanced revision: %+v %v", instance, err)
	}
}
