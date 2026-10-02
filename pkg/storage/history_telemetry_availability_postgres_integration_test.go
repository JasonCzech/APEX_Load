//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresHistoryDropAvailabilityMigrationAndRoundTrip(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_history_telemetry_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	scoped := postgresIntegrationURLWithSearchPath(t, url, schema)
	store, err := NewPostgresStore(scoped, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`ALTER TABLE test_interval_summaries DROP COLUMN dropped_results_available`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaRelease().Version); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(scoped, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	for i, available := range []bool{false, true} {
		at := now.Add(time.Duration(i) * time.Second)
		if err := store.SaveHistory("run", at, map[string]any{"timestamp": at, "telemetry": map[string]any{"dropped_results_available": available, "collected_at": now}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE test_interval_summaries SET dropped_results_available = NULL WHERE timestamp = ?`, now); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 2 {
		t.Fatalf("history %+v: %v", history, err)
	}
	for i, v := range history {
		row := v.(IntervalSummaryRecord)
		if row.DroppedResultsAvailable != (i == 1) || row.DroppedResults != 0 {
			t.Fatalf("row %d lost measured/legacy zero availability: %+v", i, row)
		}
	}
}
