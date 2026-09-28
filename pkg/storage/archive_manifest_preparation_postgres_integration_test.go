//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestArchiveManifestPreparationPostgres(t *testing.T) {
	url := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("archive_preparation_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	s, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, url, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testArchiveManifestPreparation(t, s.SQLiteStore)
}
