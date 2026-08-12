//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPostgresSchedulerLeaseGenerationMigration exercises the real PostgreSQL
// catalog and ALTER TABLE path against an isolated schema. Run it with:
//
//	APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration \
//		./pkg/storage -run TestPostgresSchedulerLeaseGenerationMigration
func TestPostgresSchedulerLeaseGenerationMigration(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	defer adminDB.Close()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL integration database: %v", err)
	}

	schemaName := fmt.Sprintf("apexload_scheduler_generation_%d", time.Now().UnixNano())
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
	legacyDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open isolated legacy PostgreSQL schema: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `CREATE TABLE scheduler_leases (
		name TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL,
		generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
		acquired_at TIMESTAMPTZ NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL
	)`); err != nil {
		legacyDB.Close()
		t.Fatalf("create legacy scheduler_leases table: %v", err)
	}
	legacyGeneration := int64(math.MaxInt32)
	now := time.Now().UTC()
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO scheduler_leases
		(name, owner_id, generation, acquired_at, expires_at, updated_at)
		VALUES ('execution', 'controller-a', $1, $2, $3, $2)`, legacyGeneration, now, now.Add(time.Minute)); err != nil {
		legacyDB.Close()
		t.Fatalf("seed legacy scheduler lease: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy PostgreSQL connection: %v", err)
	}

	store, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatalf("initialize PostgreSQL store over legacy scheduler lease schema: %v", err)
	}

	var dataType string
	if err := store.db.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'scheduler_leases' AND column_name = 'generation'`).Scan(&dataType); err != nil {
		store.Close()
		t.Fatalf("read migrated scheduler generation type: %v", err)
	}
	if dataType != "bigint" {
		store.Close()
		t.Fatalf("migrated scheduler generation type = %q, want bigint", dataType)
	}
	var preserved int64
	if err := store.db.QueryRow(`SELECT generation FROM scheduler_leases WHERE name = 'execution'`).Scan(&preserved); err != nil {
		store.Close()
		t.Fatalf("read preserved scheduler generation: %v", err)
	}
	if preserved != legacyGeneration {
		store.Close()
		t.Fatalf("preserved scheduler generation = %d, want %d", preserved, legacyGeneration)
	}
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET generation = ? WHERE name = 'execution'`, int64(math.MaxInt64)); err != nil {
		store.Close()
		t.Fatalf("store int64 scheduler generation after migration: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated PostgreSQL store: %v", err)
	}

	// A second full initialization proves the guarded migration is restart-safe.
	reopened, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatalf("reopen migrated PostgreSQL store: %v", err)
	}
	defer reopened.Close()
	var maximum int64
	if err := reopened.db.QueryRow(`SELECT generation FROM scheduler_leases WHERE name = 'execution'`).Scan(&maximum); err != nil {
		t.Fatalf("read int64 scheduler generation after reopen: %v", err)
	}
	if maximum != math.MaxInt64 {
		t.Fatalf("scheduler generation after reopen = %d, want %d", maximum, int64(math.MaxInt64))
	}
}

func postgresIntegrationURLWithSearchPath(t *testing.T, databaseURL, schemaName string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse APEXLOAD_TEST_POSTGRES_URL: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("APEXLOAD_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
