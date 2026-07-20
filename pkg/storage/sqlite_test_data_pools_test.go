package storage

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTestRecordDataPoolSnapshotSurvivesAllSavePathsAndReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data-pool-snapshot.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	bindings := []ScenarioDataPoolBinding{{
		ID:                "binding-1",
		ScenarioID:        "scenario-source",
		PoolID:            "pool-1",
		PoolName:          "Customers",
		Alias:             "customers",
		AccessMode:        "consume",
		SelectionStrategy: "unique",
		CreatedAt:         now.Add(-time.Minute),
		UpdatedAt:         now,
	}}
	newRecord := func(id string) *TestRecord {
		return &TestRecord{
			ID:          id,
			Name:        "Data-pool snapshot",
			Status:      "QUEUED",
			ProjectID:   DefaultProjectID,
			TotalVUsers: 1,
			Duration:    int64(time.Minute),
			StartTime:   now,
			Groups:      []any{},
			DataPools:   append([]ScenarioDataPoolBinding(nil), bindings...),
		}
	}

	if err := store.SaveTest(newRecord("upsert-path")); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	created, err := store.CreateTestIfAbsent(newRecord("create-only-path"))
	if err != nil || !created {
		t.Fatalf("CreateTestIfAbsent = %t, %v", created, err)
	}

	assertSnapshot := func(label string, record *TestRecord, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if record == nil || !reflect.DeepEqual(record.DataPools, bindings) {
			t.Fatalf("%s data pools = %+v, want %+v", label, record, bindings)
		}
	}
	upserted, err := store.GetTest("upsert-path")
	assertSnapshot("GetTest(upsert)", upserted, err)
	createOnly, err := store.GetTest("create-only-path")
	assertSnapshot("GetTest(create-only)", createOnly, err)
	listed, err := store.ListTests(DefaultProjectID, nil)
	if err != nil {
		t.Fatalf("ListTests: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListTests count = %d, want 2", len(listed))
	}
	for _, record := range listed {
		if !reflect.DeepEqual(record.DataPools, bindings) {
			t.Fatalf("ListTests(%s) data pools = %+v, want %+v", record.ID, record.DataPools, bindings)
		}
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen): %v", err)
	}
	defer reopened.Close()
	reloaded, err := reopened.GetTest("upsert-path")
	assertSnapshot("GetTest(reopen)", reloaded, err)
}
