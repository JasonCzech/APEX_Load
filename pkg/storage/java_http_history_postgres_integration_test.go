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

func TestPostgresJavaHTTPHistoryMigrationAndRoundTrip(t *testing.T) {
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
	schema := fmt.Sprintf("apexload_java_http_history_%d", time.Now().UnixNano())
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
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", now.Add(-time.Second), map[string]any{"timestamp": now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"java_http_json"} {
		if _, err := store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
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
	assertJavaHTTPHistory(t, store.SQLiteStore, now)
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 2 {
		t.Fatalf("history %+v: %v", history, err)
	}
	legacy := history[0].(IntervalSummaryRecord)
	if legacy.JavaHTTP != nil {
		t.Fatal("migrated postgres absence invented observations")
	}
}
