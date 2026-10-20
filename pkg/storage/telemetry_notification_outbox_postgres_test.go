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

func TestPostgresTelemetryNotificationOutbox(t *testing.T) {
	url := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal("open PostgreSQL fixture")
	}
	defer admin.Close()
	schema := fmt.Sprintf("telemetry_outbox_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("create owned schema")
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(clean, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("remove owned schema")
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, url, schema)
	first, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	t.Run("contract", func(t *testing.T) { checkTelemetryNotificationOutboxContract(t, first.SQLiteStore) })
	// Independent runs and tables share the same real schema. Remove the previous
	// fixture's recreated event so each bounded scan has its stated population.
	if err := first.DeleteTest("outbox-contract"); err != nil {
		t.Fatal(err)
	}
	t.Run("bounds", func(t *testing.T) { checkTelemetryNotificationOutboxAdmissionAndBounds(t, first.SQLiteStore) })
	for _, id := range []string{"outbox-bounds", "outbox-legacy", "outbox-active"} {
		if err := first.DeleteTest(id); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("exhaustion", func(t *testing.T) { checkTelemetryNotificationOutboxExhaustionAndRevocation(t, first.SQLiteStore) })
	if err := first.DeleteTest("outbox-exhaustion"); err != nil {
		t.Fatal(err)
	}
	t.Run("two stores", func(t *testing.T) {
		checkTelemetryNotificationOutboxConcurrent(t, first.SQLiteStore, second.SQLiteStore)
	})
	// A deletion that has not committed must exclude enqueue, then cascade any
	// existing destinations. This exercises PostgreSQL's actual row-lock behavior.
	t.Run("deletion race", func(t *testing.T) {
		in, now := telemetryOutboxFixture(t, first.SQLiteStore, "outbox-delete-race")
		tx, err := second.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM tests WHERE id=?`, in.TestID); err != nil {
			t.Fatal(err)
		}
		type result struct {
			state string
			err   error
		}
		done := make(chan result, 1)
		go func() { _, state, err := first.EnqueueTelemetryNotification(ctx, in, now); done <- result{state, err} }()
		select {
		case r := <-done:
			t.Fatalf("enqueue escaped deletion lock: %+v", r)
		case <-time.After(100 * time.Millisecond):
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-done:
			if r.err != nil || r.state != "fenced" {
				t.Fatalf("deleted enqueue=%+v", r)
			}
		case <-ctx.Done():
			t.Fatal("enqueue did not finish")
		}
	})
	// Reopen a third store while one delivery is leased; pending/leased outcomes
	// survive process replacement without a process-local claim registry.
	t.Run("reopen", func(t *testing.T) {
		reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 2}, false)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		due, err := reopened.ListDueTelemetryNotificationDeliveries(ctx, time.Now().Add(2*time.Minute), 100)
		if err != nil || len(due) != 2 {
			t.Fatalf("reopened due=%+v %v", due, err)
		}
		var leased *TelemetryNotificationDelivery
		for _, candidate := range due {
			if candidate.Attempts == 1 {
				leased = &candidate
				break
			}
		}
		if leased == nil {
			t.Fatal("leased delivery disappeared")
		}
		claim, err := reopened.ClaimTelemetryNotificationDelivery(ctx, *leased, time.Now().Add(2*time.Minute), time.Minute)
		if err != nil || claim == nil || claim.Attempts != 2 {
			t.Fatalf("reopened lease=%+v %v", claim, err)
		}
	})
}
