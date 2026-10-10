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

// rcaPostgresFixture is rcaFixture on PostgreSQL, in a schema of its own
// that is dropped afterwards.
func rcaPostgresFixture(t *testing.T) (*SQLiteStore, time.Time) {
	t.Helper()
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
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("apexload_rca_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quotePostgresIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quotePostgresIdentifier(schema)+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, url, schema), StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store.SQLiteStore, seedRCAFixture(t, store.SQLiteStore)
}

// The claim order, a request's promotion of a queued automatic analysis and
// the batch read of runs' newest analyses run as written on PostgreSQL.
func TestPostgresRCAClaimPriority(t *testing.T) {
	s, now := rcaPostgresFixture(t)
	exerciseRCAClaimPriority(t, s, now)
}

func TestPostgresLatestRCAAnalyses(t *testing.T) {
	s, now := rcaPostgresFixture(t)
	exerciseLatestRCAAnalyses(t, s, now)
}
