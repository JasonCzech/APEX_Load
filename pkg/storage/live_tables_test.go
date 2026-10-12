package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func liveTableTestFixture(t *testing.T, s *SQLiteStore) (LiveTableRecord, LiveTableExecutionTerm) {
	t.Helper()
	if err := s.SaveTest(&TestRecord{ID: "live-run", Name: "Live run", ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	held, lease, err := s.TryAcquireSchedulerLeaseTerm("live-execution", "owner-one", time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatalf("lease %v %v", held, err)
	}
	return LiveTableRecord{ID: "live-table", TestID: "live-run", ProjectID: DefaultProjectID, RunIncarnation: "incarnation-one", Alias: "jobs", Profile: "native", Durability: "durable", SeedDigest: "seed-digest"}, LiveTableExecutionTerm{LeaseName: lease.Name, OwnerID: lease.OwnerID, Generation: lease.Generation}
}

func TestLiveTableJournalCheckpointAndFencing(t *testing.T) {
	checkLiveTableJournalCheckpointAndFencing(t, newHAFencingStore(t))
}
func checkLiveTableJournalCheckpointAndFencing(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	record, term := liveTableTestFixture(t, s)
	initial := []byte(`{"rows":["first","second"],"receipts":[]}`)
	first, err := s.InitializeLiveTable(ctx, record, initial, term)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.InitializeLiveTable(ctx, record, initial, term)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("initialize retry %v %v", second, err)
	}
	heldOther, other, err := s.TryAcquireSchedulerLeaseTerm("unrelated-scheduler", term.OwnerID, time.Now(), time.Minute)
	if err != nil || !heldOther {
		t.Fatalf("other lease = %v %v", heldOther, err)
	}
	otherTerm := LiveTableExecutionTerm{LeaseName: other.Name, OwnerID: other.OwnerID, Generation: other.Generation}
	if err := s.ClaimLiveTable(ctx, record.ID, otherTerm); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("cross-lease ownership bypass = %v", err)
	}
	if err := s.CloseLiveTable(ctx, record.ID, 0, otherTerm); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("cross-lease mutation bypass = %v", err)
	}
	changed := record
	changed.SeedDigest = "other"
	if _, err := s.InitializeLiveTable(ctx, changed, initial, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("reseed=%v", err)
	}
	entry := LiveTableEntry{TableID: record.ID, Revision: 1, PreviousRevision: 0, At: time.Now().UTC(), Payload: []byte(`{"pop":"first","receipt":{"client":"one","sequence":1}}`)}
	if err := s.AppendLiveTableEntry(ctx, entry, term); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendLiveTableEntry(ctx, entry, term); err != nil {
		t.Fatalf("lost-reply retry=%v", err)
	}
	altered := entry
	altered.Payload = []byte(`{"pop":"second"}`)
	if err := s.AppendLiveTableEntry(ctx, altered, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("changed duplicate=%v", err)
	}
	future := entry
	future.Revision = 3
	future.PreviousRevision = 2
	if err := s.AppendLiveTableEntry(ctx, future, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("sequence gap=%v", err)
	}
	checkpoint := []byte(`{"rows":["second"],"receipts":[{"client":"one","sequence":1,"result":"first"}],"retired":{}}`)
	if err := s.PublishLiveTableCheckpoint(ctx, record.ID, 1, checkpoint, term); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishLiveTableCheckpoint(ctx, record.ID, 1, []byte(`{}`), term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("checkpoint overwrite=%v", err)
	}
	entry2 := entry
	entry2.Revision = 2
	entry2.PreviousRevision = 1
	entry2.Payload = []byte(`{"push":"third","receipt":{"client":"one","sequence":2}}`)
	if err := s.AppendLiveTableEntry(ctx, entry2, term); err != nil {
		t.Fatal(err)
	}
	recovery, err := s.ReadLiveTableRecovery(ctx, record.ID, record.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if string(recovery.Checkpoint) != string(checkpoint) || recovery.Table.CheckpointRevision != 1 || recovery.Table.Revision != 2 || len(recovery.Entries) != 1 || string(recovery.Entries[0].Payload) != string(entry2.Payload) {
		t.Fatalf("checkpoint/tail lost state: %#v", recovery)
	}
	if _, err := s.ReadLiveTableRecovery(ctx, record.ID, "foreign"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project leak=%v", err)
	}
	// Expiry fences the old owner before another owner has claimed the table.
	if _, err := s.db.Exec(`UPDATE scheduler_leases SET expires_at=? WHERE name=?`, time.Now().Add(-time.Minute), term.LeaseName); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishLiveTableCheckpoint(ctx, record.ID, 2, []byte(`{"expired":true}`), term); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("expired checkpoint=%v", err)
	}
	held, newLease, err := s.TryAcquireSchedulerLeaseTerm(term.LeaseName, "owner-two", time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatal(err)
	}
	next := LiveTableExecutionTerm{LeaseName: newLease.Name, OwnerID: newLease.OwnerID, Generation: newLease.Generation}
	if err := s.ClaimLiveTable(ctx, record.ID, next); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendLiveTableEntry(ctx, future, term); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("old leader=%v", err)
	}
	if err := s.AppendLiveTableEntry(ctx, future, next); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseLiveTable(ctx, record.ID, 3, next); err != nil {
		t.Fatal(err)
	}
	last := future
	last.PreviousRevision = 3
	last.Revision = 4
	if err := s.AppendLiveTableEntry(ctx, last, next); !errors.Is(err, ErrLiveTableClosed) {
		t.Fatalf("write after close=%v", err)
	}
}

func TestLiveTableRecoveryRejectsCorruptionAndMissingTail(t *testing.T) {
	for _, kind := range []string{"checkpoint", "payload", "gap"} {
		t.Run(kind, func(t *testing.T) {
			s := newHAFencingStore(t)
			ctx := context.Background()
			r, term := liveTableTestFixture(t, s)
			if _, err := s.InitializeLiveTable(ctx, r, []byte(`{"checkpoint":1}`), term); err != nil {
				t.Fatal(err)
			}
			if err := s.AppendLiveTableEntry(ctx, LiveTableEntry{TableID: r.ID, Revision: 1, At: time.Now(), Payload: []byte(`{"entry":1}`)}, term); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE live_tables SET checkpoint_payload='corrupt' WHERE id=?`
			if kind == "payload" {
				query = `UPDATE live_table_journal SET payload='corrupt' WHERE table_id=?`
			}
			if kind == "gap" {
				query = `DELETE FROM live_table_journal WHERE table_id=?`
			}
			if _, err := s.db.Exec(query, r.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReadLiveTableRecovery(ctx, r.ID, r.ProjectID); !errors.Is(err, ErrLiveTableCorrupt) {
				t.Fatalf("corruption=%v", err)
			}
		})
	}
}

func TestLiveTableLimitsAndMemoryCannotBecomeAdoptable(t *testing.T) {
	s := newHAFencingStore(t)
	ctx := context.Background()
	r, term := liveTableTestFixture(t, s)
	if _, err := s.InitializeLiveTable(ctx, r, []byte(`{}`), LiveTableExecutionTerm{}); err == nil {
		t.Fatal("durable initialization without lease")
	}
	r.Durability = "memory"
	if _, err := s.InitializeLiveTable(ctx, r, []byte(`{}`), LiveTableExecutionTerm{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimLiveTable(ctx, r.ID, term); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("adopted memory=%v", err)
	}
	if err := s.AppendLiveTableEntry(ctx, LiveTableEntry{TableID: r.ID, Revision: 1, At: time.Now(), Payload: make([]byte, MaxLiveTableEntryBytes+1)}, LiveTableExecutionTerm{}); !errors.Is(err, ErrLiveTableLimit) {
		t.Fatalf("entry bytes=%v", err)
	}
	if _, err := s.db.Exec(`UPDATE live_tables SET journal_count=? WHERE id=?`, MaxLiveTableJournalEntries, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendLiveTableEntry(ctx, LiveTableEntry{TableID: r.ID, Revision: 1, At: time.Now(), Payload: []byte(`{}`)}, LiveTableExecutionTerm{}); !errors.Is(err, ErrLiveTableLimit) {
		t.Fatalf("retention admission=%v", err)
	}
	if err := s.CloseLiveTable(ctx, r.ID, 0, LiveTableExecutionTerm{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimLiveTable(ctx, r.ID, term); err != nil {
		t.Fatalf("closed memory finalization claim = %v", err)
	}
	if err := s.PublishLiveTableCheckpoint(ctx, r.ID, 1, []byte(`{"mutated_closed":true}`), term); !errors.Is(err, ErrLiveTableClosed) {
		t.Fatalf("closed memory checkpoint changed = %v", err)
	}
	if err := s.AppendLiveTableEntry(ctx, LiveTableEntry{TableID: r.ID, Revision: 1, At: time.Now(), Payload: []byte(`{}`)}, term); !errors.Is(err, ErrLiveTableClosed) {
		t.Fatalf("closed memory claim reopened serving = %v", err)
	}
}

func TestLiveTableOptionsPersistAndAdvanceAuthoredRevision(t *testing.T) {
	s := newScenarioRevisionTestStore(t)
	original := seedScenarioRevision(t, s)
	bindings, err := s.ListScenarioDataPoolBindings(original.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bindings[0].LiveOptions != nil {
		t.Fatal("legacy read_only binding acquired live configuration")
	}
	bindings[0].AccessMode = "live"
	bindings[0].LiveOptions = &LiveTableOptions{Profile: "native_queue", Columns: []string{"job"}, SeedPolicy: "current_at_start", Durability: "memory", EndMode: "discard", LeaseTTLMS: 1000}
	if err := s.SaveScenarioDataPoolBindings(original.Scenario.ID, bindings); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetScenarioAuthoringSnapshot(original.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ContentRevision == original.ContentRevision || !reflect.DeepEqual(updated.DataPools[0].LiveOptions, bindings[0].LiveOptions) {
		t.Fatal("live options missing from authored snapshot or revision")
	}
	before := updated.ContentRevision
	if _, err := s.db.Exec(`UPDATE scenario_data_pool_bindings SET live_options_json=? WHERE id=?`, `{"lease_ttl_ms":2000}`, bindings[0].ID); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, s, original.Scenario.ID) == before {
		t.Fatal("direct live-option update did not advance revision")
	}
}

func TestLiveTableBindingMigrationRetainsLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-live-bindings.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	original := seedScenarioRevision(t, s)
	bindings, err := s.ListScenarioDataPoolBindings(original.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-live binding shape, including its NOT NULL seed foreign
	// key and populated static binding. Opening upgrades it without data loss.
	if _, err := s.db.Exec(`DROP TABLE scenario_data_pool_bindings`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE scenario_data_pool_bindings(id TEXT PRIMARY KEY,scenario_id TEXT NOT NULL,pool_id TEXT NOT NULL,alias TEXT NOT NULL,access_mode TEXT NOT NULL,selection_strategy TEXT NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,UNIQUE(scenario_id,alias),FOREIGN KEY(scenario_id) REFERENCES scenarios(id),FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id))`); err != nil {
		t.Fatal(err)
	}
	b := bindings[0]
	if _, err := s.db.Exec(`INSERT INTO scenario_data_pool_bindings(id,scenario_id,pool_id,alias,access_mode,selection_strategy,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, b.ID, b.ScenarioID, b.PoolID, b.Alias, b.AccessMode, b.SelectionStrategy, b.CreatedAt, b.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version=57`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.ListScenarioDataPoolBindings(original.Scenario.ID)
	if err != nil || !reflect.DeepEqual(bindings, after) {
		t.Fatalf("legacy binding changed %#v %v", after, err)
	}
	after = append(after, ScenarioDataPoolBinding{ID: "producer", Alias: "producer", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{Profile: "native_queue", SeedPolicy: "empty", Columns: []string{"id"}}})
	if err := reopened.SaveScenarioDataPoolBindings(original.Scenario.ID, after); err != nil {
		t.Fatal(err)
	}
	current, err := reopened.GetScenarioAuthoringSnapshot(original.Scenario.ID)
	if err != nil || len(current.DataPools) != 2 {
		t.Fatalf("migrated nullable binding unavailable %#v %v", current, err)
	}
}
