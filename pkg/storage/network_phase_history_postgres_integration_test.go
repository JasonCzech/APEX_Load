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

func TestPostgresNetworkPhaseHistoryRoundTrip(t *testing.T) {
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
	schema := fmt.Sprintf("apexload_phase_history_%d", time.Now().UnixNano())
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
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.SaveTest(&TestRecord{ID: "old", Name: "old", Status: "COMPLETED", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("old", now, networkPhaseHistorySample(now, 1)); err != nil {
		t.Fatal(err)
	}
	// Reopen a database from before the column existed: it must gain the
	// column, and its rows read back as unknown.
	if _, err := store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN network_phases_json"); err != nil {
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
	legacy, err := store.GetHistory("old")
	if err != nil || len(legacy) != 1 || legacy[0].(IntervalSummaryRecord).NetworkPhases != nil {
		t.Fatalf("migrated postgres legacy row: %+v, err=%v", legacy, err)
	}
	exerciseNetworkPhaseHistory(t, store.SQLiteStore, now)
}
