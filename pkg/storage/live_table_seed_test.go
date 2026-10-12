package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func seedLiveDataPool(t *testing.T, s *SQLiteStore) *LiveDataPoolSeed {
	t.Helper()
	pool := &SharedDataPool{ID: "live-seed", ProjectID: DefaultProjectID, Name: "Jobs", Schema: map[string]string{"id": "string", "empty": "string"}}
	if err := s.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}
	for _, row := range []*SharedDataPoolRow{{ID: "active-job", PoolID: pool.ID, Data: map[string]string{"id": "one", "empty": ""}}, {ID: "disabled-job", PoolID: pool.ID, Status: "disabled", Data: map[string]string{"id": "old"}}, {ID: "tagged-job", PoolID: pool.ID, Status: "ready", Data: map[string]string{"id": "two"}}} {
		if err := s.SaveDataPoolRow(row); err != nil {
			t.Fatal(err)
		}
	}
	seed, err := s.ReadLiveDataPoolSeed(context.Background(), pool.ID, DefaultProjectID, 100, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Rows) != 2 || seed.ExcludedRows != 1 || seed.Identity.RowsRevision != 3 || seed.Identity.Digest == "" {
		t.Fatalf("seed=%+v", seed)
	}
	if value, present := seed.Rows[0].Data["empty"]; !present || value != "" {
		t.Fatal("empty field was lost")
	}
	if _, present := seed.Rows[1].Data["empty"]; present {
		t.Fatal("absent field became present")
	}
	return seed
}

func liveFinalRowsFromSeed(seed *LiveDataPoolSeed) []*SharedDataPoolRow {
	rows := make([]*SharedDataPoolRow, 0, len(seed.Rows))
	for _, row := range seed.Rows {
		copy := *row
		copy.Status = "available"
		rows = append(rows, &copy)
	}
	return rows
}

func closedLiveTableForSeed(t *testing.T, s *SQLiteStore, seed *LiveDataPoolSeed, id string) (LiveTableRecord, LiveTableExecutionTerm) {
	t.Helper()
	record, term := liveTableTestFixture(t, s)
	record.ID = id
	record.Alias = id
	record.SeedDigest = seed.Identity.Digest
	record.Durability = "memory"
	if _, err := s.InitializeLiveTable(context.Background(), record, []byte(`{"initial":true}`), term); err != nil {
		t.Fatal(err)
	}
	// Memory mode has no per-operation SQL writes. A final checkpoint advances
	// its stored sequence exactly once before the immutable close boundary.
	if err := s.PublishLiveTableCheckpoint(context.Background(), id, 7, []byte(`{"final":7,"receipts":[1,2,3]}`), term); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseLiveTable(context.Background(), id, 7, term); err != nil {
		t.Fatal(err)
	}
	return record, term
}

func TestLiveTableWritebackPreservesExcludedRowsAndIsIdempotent(t *testing.T) {
	checkLiveTableWritebackPreservesExcludedRowsAndIsIdempotent(t, newHAFencingStore(t))
}

func checkLiveTableWritebackPreservesExcludedRowsAndIsIdempotent(t *testing.T, s *SQLiteStore) {
	t.Helper()
	seed := seedLiveDataPool(t, s)
	r, term := closedLiveTableForSeed(t, s, seed, "output")
	disabledBefore, err := s.GetDataPoolRowFromPool(seed.Identity.PoolID, "disabled-job")
	if err != nil {
		t.Fatal(err)
	}
	in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "write_back", Seed: seed.Identity, Rows: []*SharedDataPoolRow{{ID: "active-job", Data: map[string]string{"id": "changed", "empty": ""}}, {ID: "new-job", Data: map[string]string{"id": "new"}}}}
	result, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || result.State != "complete" {
		t.Fatalf("finalization=%+v %v", result, err)
	}
	again, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatalf("retry=%+v %v", again, err)
	}
	disabledAfter, err := s.GetDataPoolRowFromPool(seed.Identity.PoolID, "disabled-job")
	if err != nil || !reflect.DeepEqual(disabledBefore, disabledAfter) {
		t.Fatalf("excluded row changed=%+v %v", disabledAfter, err)
	}
	rows, err := s.ListDataPoolRows(seed.Identity.PoolID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%v %v", rows, err)
	}
	updated, err := s.GetDataPoolRowFromPool(seed.Identity.PoolID, "active-job")
	if err != nil || updated.Version != 2 || updated.Data["id"] != "changed" {
		t.Fatalf("active row=%+v %v", updated, err)
	}
	in.Rows[0].Data["id"] = "different"
	if _, err := s.FinalizeLiveTable(context.Background(), in, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("changed finalization retry=%v", err)
	}
}

func TestLiveTableConcurrentSourceEditsRefuseWriteback(t *testing.T) {
	for _, change := range []string{"row", "metadata", "archived", "incarnation"} {
		t.Run(change, func(t *testing.T) {
			s := newHAFencingStore(t)
			seed := seedLiveDataPool(t, s)
			r, term := closedLiveTableForSeed(t, s, seed, "conflict")
			switch change {
			case "row":
				row, err := s.GetDataPoolRowFromPool(seed.Identity.PoolID, "disabled-job")
				if err != nil {
					t.Fatal(err)
				}
				row.Data["id"] = "edited"
				if err := s.SaveDataPoolRow(row); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if _, err := s.db.Exec(`UPDATE shared_data_pools SET updated_at=? WHERE id=?`, time.Now().Add(time.Second), seed.Identity.PoolID); err != nil {
					t.Fatal(err)
				}
			case "archived":
				if _, err := s.db.Exec(`UPDATE shared_data_pools SET archived_at=? WHERE id=?`, time.Now(), seed.Identity.PoolID); err != nil {
					t.Fatal(err)
				}
			case "incarnation":
				if _, err := s.db.Exec(`UPDATE shared_data_pools SET created_at=? WHERE id=?`, time.Now().Add(time.Second), seed.Identity.PoolID); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.ListDataPoolRows(seed.Identity.PoolID)
			if err != nil {
				t.Fatal(err)
			}
			in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "write_back", Seed: seed.Identity}
			result, err := s.FinalizeLiveTable(context.Background(), in, term)
			if err != nil || result.State != "conflict" {
				t.Fatalf("finalization=%+v %v", result, err)
			}
			after, err := s.ListDataPoolRows(seed.Identity.PoolID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("conflicting writeback changed source")
			}
		})
	}
}

func TestLiveTableSaveAsNewUsesOneOutputIdentity(t *testing.T) {
	s := newHAFencingStore(t)
	seed := seedLiveDataPool(t, s)
	r, term := closedLiveTableForSeed(t, s, seed, "new-output")
	in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "save_as_new", OutputPoolName: "Final jobs", Seed: seed.Identity, Rows: liveFinalRowsFromSeed(seed), Schema: seed.Identity.Schema}
	first, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("save-as-new retry=%+v %v", second, err)
	}
	if first.OutputPoolID == seed.Identity.PoolID {
		t.Fatal("new pool replaced source")
	}
	rows, err := s.ListDataPoolRows(first.OutputPoolID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("new rows=%+v %v", rows, err)
	}
	for _, row := range rows {
		if row.ID == seed.Rows[0].ID || row.ID == seed.Rows[1].ID {
			t.Fatal("new output reused source globally unique row ID")
		}
	}
	source, err := s.ListDataPoolRows(seed.Identity.PoolID)
	if err != nil || len(source) != 3 {
		t.Fatal("save as new modified source")
	}
}

func TestLiveTableFinalizationKeepsQuarantinedOutputDisabled(t *testing.T) {
	s := newHAFencingStore(t)
	seed := seedLiveDataPool(t, s)
	r, term := closedLiveTableForSeed(t, s, seed, "quarantined-output")
	in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "write_back", Seed: seed.Identity, Rows: []*SharedDataPoolRow{{ID: "active-job", Status: "disabled", Data: map[string]string{"id": "poison"}}}}
	result, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || result.State != "complete" {
		t.Fatalf("quarantined finalization = %+v %v", result, err)
	}
	row, err := s.GetDataPoolRowFromPool(seed.Identity.PoolID, "active-job")
	if err != nil || row.Status != "disabled" {
		t.Fatalf("quarantined row was reactivated = %+v %v", row, err)
	}
	next, err := s.ReadLiveDataPoolSeed(context.Background(), seed.Identity.PoolID, r.ProjectID, 100, 1<<20)
	if err != nil || len(next.Rows) != 0 || next.ExcludedRows != 2 {
		t.Fatalf("disabled output became eligible seed = %+v %v", next, err)
	}
}

func TestLiveTableWritebackConflictCanBeSavedAsNew(t *testing.T) {
	s := newHAFencingStore(t)
	seed := seedLiveDataPool(t, s)
	r, term := closedLiveTableForSeed(t, s, seed, "recover-output")
	if _, err := s.GetLiveTableFinalization(context.Background(), r.ID, r.ProjectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing finalization = %v", err)
	}
	if err := s.SaveDataPoolRow(&SharedDataPoolRow{ID: "concurrent-job", PoolID: seed.Identity.PoolID, Data: map[string]string{"id": "concurrent"}}); err != nil {
		t.Fatal(err)
	}
	in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "write_back", Seed: seed.Identity, Rows: liveFinalRowsFromSeed(seed), Schema: seed.Identity.Schema}
	conflict, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || conflict.State != "conflict" {
		t.Fatalf("conflicting writeback = %+v %v", conflict, err)
	}
	read, err := s.GetLiveTableFinalization(context.Background(), r.ID, r.ProjectID)
	if err != nil || !reflect.DeepEqual(read, conflict) {
		t.Fatalf("read conflict = %+v %v", read, err)
	}
	if _, err := s.GetLiveTableFinalization(context.Background(), r.ID, "another-project"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project finalization read = %v", err)
	}
	in.Mode, in.OutputPoolName = "save_as_new", "Recovered jobs"
	in.Revision++
	if _, err := s.FinalizeLiveTable(context.Background(), in, term); err == nil {
		t.Fatal("conflict recovery accepted a different closed revision")
	}
	in.Revision--
	first, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || first.State != "complete" || first.Mode != "save_as_new" {
		t.Fatalf("recovery = %+v %v", first, err)
	}
	second, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("recovery retry = %+v %v", second, err)
	}
	rows, err := s.ListDataPoolRows(first.OutputPoolID)
	if err != nil || len(rows) != len(seed.Rows) {
		t.Fatalf("recovered rows = %+v %v", rows, err)
	}
	sourceRows, err := s.ListDataPoolRows(seed.Identity.PoolID)
	if err != nil || len(sourceRows) != 4 {
		t.Fatalf("conflict recovery changed source = %+v %v", sourceRows, err)
	}
	in.OutputPoolName = "Changed retry"
	if _, err := s.FinalizeLiveTable(context.Background(), in, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("changed completed recovery = %v", err)
	}
}

func TestLiveTableSeedLimitsAndEmptyProducerBinding(t *testing.T) {
	s := newScenarioRevisionTestStore(t)
	original := seedScenarioRevision(t, s)
	bindings := []ScenarioDataPoolBinding{{ID: "empty-live", Alias: "jobs", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{Profile: "native_queue", SeedPolicy: "empty", Columns: []string{"job"}}}}
	if err := s.SaveScenarioDataPoolBindings(original.Scenario.ID, bindings); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetScenarioAuthoringSnapshot(original.Scenario.ID)
	if err != nil || len(got.DataPools) != 1 || got.DataPools[0].PoolID != "" {
		t.Fatalf("empty live binding=%+v %v", got, err)
	}
	bindings[0].AccessMode = "read_only"
	if err := s.SaveScenarioDataPoolBindings(original.Scenario.ID, bindings); err == nil {
		t.Fatal("read-only binding omitted required pool")
	}
	seed := seedLiveDataPool(t, s)
	if _, err := s.ReadLiveDataPoolSeed(context.Background(), seed.Identity.PoolID, DefaultProjectID, 1, 1<<20); !errors.Is(err, ErrLiveTableLimit) {
		t.Fatalf("row bound=%v", err)
	}
	if _, err := s.ReadLiveDataPoolSeed(context.Background(), seed.Identity.PoolID, DefaultProjectID, 100, 1); !errors.Is(err, ErrLiveTableLimit) {
		t.Fatalf("byte bound=%v", err)
	}
}
