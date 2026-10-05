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

func TestPostgresAnalysisPartitionReceiptLifecycleReopen(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal("cannot open owned PostgreSQL fixture")
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_partition_receipt_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("cannot create private receipt fixture schema")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("cannot remove exact owned receipt fixture schema")
			return
		}
		var remaining int
		if err := admin.QueryRowContext(cleanup, `SELECT count(*) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("owned receipt fixture schema cleanup was not verified")
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	first, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal("cannot initialize receipt fixture store")
	}
	defer func() {
		if first != nil {
			first.Close()
		}
	}()
	seedMonitorFixture(t, first.SQLiteStore)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal("cannot open independent receipt fixture store")
	}
	defer func() {
		if second != nil {
			second.Close()
		}
	}()
	result := exerciseAnalysisPartitionReceiptLifecycle(t, first.SQLiteStore, second.SQLiteStore)
	exerciseAnalysisPartitionCancellationAndRetry(t, first.SQLiteStore, second.SQLiteStore)
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	second = nil
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	first = nil
	reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal("cannot reopen receipt fixture store")
	}
	defer reopened.Close()
	requireAnalysisPartitionReceiptLifecycleReopen(t, reopened.SQLiteStore, result)
}
