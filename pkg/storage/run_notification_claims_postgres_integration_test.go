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

// On PostgreSQL a run's notification is claimed once per kind, and never for
// a run that no longer exists.
func TestPostgresRunNotificationClaims(t *testing.T) {
	url := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("notification_claims_%d", time.Now().UnixNano())
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
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := s.SaveTest(&TestRecord{ID: "run-1", ProjectID: DefaultProjectID, Name: "run-1", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		run  string
		want bool
	}{{"run-1", true}, {"run-1", false}, {"deleted-run", false}} {
		claimed, err := s.ClaimRunNotification(tc.run, "telemetry_acceptance:fail", now)
		if err != nil || claimed != tc.want {
			t.Fatalf("claim %s = %v %v, want %v", tc.run, claimed, err, tc.want)
		}
	}
	// A claim racing a deletion that has removed the run but not committed
	// waits for it, then finds no run: no claim is left behind.
	if err := s.SaveTest(&TestRecord{ID: "run-2", ProjectID: DefaultProjectID, Name: "run-2", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	deleter, err := sql.Open("pgx", postgresIntegrationURLWithSearchPath(t, url, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer deleter.Close()
	tx, err := deleter.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tests WHERE id = $1`, "run-2"); err != nil {
		t.Fatal(err)
	}
	type claim struct {
		claimed bool
		err     error
	}
	result := make(chan claim, 1)
	go func() {
		claimed, err := s.ClaimRunNotification("run-2", "telemetry_acceptance:fail", now)
		result <- claim{claimed, err}
	}()
	select {
	case got := <-result:
		t.Fatalf("the claim did not wait for the deletion: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got.err != nil || got.claimed {
		t.Fatalf("claim after the deletion = %+v", got)
	}
}
