package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryDropAvailabilitySurvivesRestartAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	// Removing the new column simulates an existing database. Reopening must
	// migrate it without inventing availability for its historical rows.
	if _, err := store.db.Exec(`ALTER TABLE test_interval_summaries DROP COLUMN dropped_results_available`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaRelease().Version); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, telemetry := range []map[string]any{
		{"collected_at": now},
		{"dropped_results_available": true},
		{"dropped_results": 3},
	} {
		at := now.Add(time.Duration(i) * time.Second)
		if err := store.SaveHistory("run", at, map[string]any{"timestamp": at, "telemetry": telemetry}); err != nil {
			t.Fatal(err)
		}
	}
	// An older row has a NULL presence flag, not a measured zero.
	if _, err := store.db.Exec(`UPDATE test_interval_summaries SET dropped_results_available = NULL WHERE timestamp = ?`, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 3 {
		t.Fatalf("history %+v: %v", history, err)
	}
	for i, v := range history {
		row := v.(IntervalSummaryRecord)
		if row.DroppedResultsAvailable != (i == 1) || row.DroppedResults != []int64{0, 0, 3}[i] {
			t.Fatalf("row %d availability or counter changed: %+v", i, row)
		}
	}
}
