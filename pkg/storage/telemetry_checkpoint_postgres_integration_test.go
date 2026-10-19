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

func TestPostgresTelemetryCheckpointFencesAndOrdering(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("apexload_telemetry_checkpoint_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE tests DROP COLUMN telemetry_checkpoint_json`, `ALTER TABLE tests DROP COLUMN telemetry_checkpoint_revision`, `DELETE FROM schema_migrations WHERE version >= 67`} {
		if _, err := store.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkTelemetryCheckpointFencesAndOrdering(t, store.SQLiteStore)
}
