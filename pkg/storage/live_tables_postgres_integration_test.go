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

func TestPostgresLiveTableJournalCheckpointAndFencing(t *testing.T) {
	checkLiveTableJournalCheckpointAndFencing(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}

func TestPostgresLiveTableWritebackPreservesExcludedRowsAndIsIdempotent(t *testing.T) {
	checkLiveTableWritebackPreservesExcludedRowsAndIsIdempotent(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}

func newPostgresLiveTableTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("apexload_live_tables_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, url, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
