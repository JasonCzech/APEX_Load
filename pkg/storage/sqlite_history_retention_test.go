package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTrimPersistedTestHistoryKeepsNewestWindow(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SaveTest(&TestRecord{ID: "history-retention", Name: "History", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		timestamp := now.Add(time.Duration(index) * time.Second)
		if _, err := store.db.Exec(`INSERT INTO test_interval_summaries (test_id, schema_version, timestamp) VALUES (?, ?, ?)`, "history-retention", intervalSummarySchemaVersionV1, timestamp); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, "history-retention", timestamp, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"test_interval_summaries", "test_history"} {
		if err := store.trimPersistedTestHistory(table, "history-retention", 3); err != nil {
			t.Fatalf("trim %s: %v", table, err)
		}
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE test_id = ?`, "history-retention").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("%s count = %d, want 3", table, count)
		}
		var deletedOldest int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE test_id = ? AND timestamp < ?`, "history-retention", now.Add(2*time.Second)).Scan(&deletedOldest); err != nil {
			t.Fatal(err)
		}
		if deletedOldest != 0 {
			t.Fatalf("%s retained %d rows older than newest window", table, deletedOldest)
		}
		for index := 0; index < 5; index++ {
			if table == "test_interval_summaries" {
				_, err = store.db.Exec(`INSERT INTO test_interval_summaries (test_id, schema_version, timestamp) VALUES (?, ?, ?)`, "history-retention", intervalSummarySchemaVersionV1, now.Add(10*time.Second))
			} else {
				_, err = store.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, "history-retention", now.Add(10*time.Second), `{}`)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := store.trimPersistedTestHistory(table, "history-retention", 3); err != nil {
			t.Fatalf("trim tied %s timestamps: %v", table, err)
		}
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE test_id = ?`, "history-retention").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("%s tied-timestamp count = %d, want strict cap 3", table, count)
		}
	}
}

func TestTrimPersistedTestHistoryRejectsUnknownTable(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.trimPersistedTestHistory("attacker_controlled", "run", 3); err == nil {
		t.Fatal("unknown history table was accepted")
	}
}
