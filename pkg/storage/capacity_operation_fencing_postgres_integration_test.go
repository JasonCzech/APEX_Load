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

// The fenced capacity commit runs inside a lease-term transaction and commits
// the warm pool size with the request, so its dialect translation needs the
// same proof on PostgreSQL as on SQLite.
func TestPostgresCapacityOperationFencing(t *testing.T) {
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
	for name, check := range map[string]func(*testing.T, *SQLiteStore){
		"fences attempt and lease": checkCapacityOperationFencesAttemptAndLease,
		"rolls back invalid warm":  checkCapacityOperationRollsBackInvalidWarmPublication,
	} {
		t.Run(name, func(t *testing.T) {
			schema := fmt.Sprintf("apexload_capacity_fencing_%d", time.Now().UnixNano())
			quoted := quotePostgresIdentifier(schema)
			if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
					t.Error(err)
				}
			})
			store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			check(t, store.SQLiteStore)
		})
	}
}
