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

// TestPostgresIntegerColumnsWidenToBigint proves both halves of the int4 fix
// against a real catalog: a fresh schema declares no int4 columns, and a
// database created by an older release (int4 columns) is widened on open so a
// realistic test duration can be saved. Run it with:
//
//	APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration \
//		./pkg/storage -run TestPostgresIntegerColumnsWidenToBigint
func TestPostgresIntegerColumnsWidenToBigint(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	defer adminDB.Close()

	schemaName := fmt.Sprintf("apexload_int_widening_%d", time.Now().UnixNano())
	quotedSchema := quotePostgresIdentifier(schemaName)
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
	}()
	scopedURL := postgresIntegrationURLWithSearchPath(t, databaseURL, schemaName)

	int4Columns := func(store *PostgresStore) []postgresIntegerColumn {
		t.Helper()
		columns, err := queryPostgresInt4Columns(store.db, postgresOwnedTableNames())
		if err != nil {
			t.Fatalf("query int4 columns: %v", err)
		}
		return columns
	}

	store, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatalf("initialize fresh PostgreSQL store: %v", err)
	}
	if columns := int4Columns(store); len(columns) != 0 {
		store.Close()
		t.Fatalf("fresh schema declared int4 columns: %v", columns)
	}

	// Recreate the legacy shape an older release produced, with a user table
	// in the same schema that must not be touched.
	for _, statement := range []string{
		`ALTER TABLE tests ALTER COLUMN duration_ns TYPE INTEGER, ALTER COLUMN cumulative_pause_ns TYPE INTEGER`,
		`ALTER TABLE scheduler_leases ALTER COLUMN generation TYPE INTEGER`,
		`CREATE TABLE foreign_app_counters (n INTEGER)`,
	} {
		if _, err := store.db.db.Exec(statement); err != nil {
			store.Close()
			t.Fatalf("simulate legacy schema (%s): %v", statement, err)
		}
	}
	if columns := int4Columns(store); len(columns) != 3 {
		store.Close()
		t.Fatalf("legacy simulation produced %d int4 columns, want 3: %v", len(columns), columns)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	migrated, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatalf("reopen legacy PostgreSQL store: %v", err)
	}
	defer migrated.Close()
	if columns := int4Columns(migrated); len(columns) != 0 {
		t.Fatalf("owned int4 columns survived the migration: %v", columns)
	}
	var foreignType string
	if err := migrated.db.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'foreign_app_counters' AND column_name = 'n'`).Scan(&foreignType); err != nil {
		t.Fatalf("read foreign table column type: %v", err)
	}
	if foreignType != "integer" {
		t.Fatalf("migration altered a table it does not own: foreign_app_counters.n = %q", foreignType)
	}

	const duration = 150 * time.Second
	record := &TestRecord{
		ID:              "int-widening-test",
		Name:            "int widening",
		Status:          "QUEUED",
		Duration:        int64(duration),
		CumulativePause: int64(90 * time.Minute),
		StartTime:       time.Now().UTC(),
	}
	if err := migrated.SaveTest(record); err != nil {
		t.Fatalf("save test with a %s duration: %v", duration, err)
	}
	loaded, err := migrated.GetTest(record.ID)
	if err != nil {
		t.Fatalf("load saved test: %v", err)
	}
	if loaded.Duration != int64(duration) || loaded.CumulativePause != record.CumulativePause {
		t.Fatalf("round-trip duration=%d pause=%d, want %d/%d", loaded.Duration, loaded.CumulativePause, int64(duration), record.CumulativePause)
	}
}
