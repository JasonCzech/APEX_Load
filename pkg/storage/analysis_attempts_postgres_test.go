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

func TestPostgresNativeAttemptTwoStoreAuthorityAndPublicationFences(t *testing.T) {
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
	schema := fmt.Sprintf("apexload_native_attempts_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quotePostgresIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quotePostgresIdentifier(schema)+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, url, schema)
	first, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	seedMonitorFixture(t, first.SQLiteStore)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseAnalysisNativeAttemptAuthority(t, first.SQLiteStore, second.SQLiteStore)
	exerciseAnalysisNativePublicationSQLFence(t, first.SQLiteStore)
	t.Run("precision consent durable authority", func(t *testing.T) { exerciseAnalysisPrecisionConsent(t, first.SQLiteStore) })
}
