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

func TestPostgresTerminalArchiveState(t *testing.T) {
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
	schema := fmt.Sprintf("apexload_terminal_archive_%d", time.Now().UnixNano())
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
	defer store.Close()
	testTerminalArchiveState(t, store.SQLiteStore)
}
