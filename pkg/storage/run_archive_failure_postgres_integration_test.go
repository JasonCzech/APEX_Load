//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRunArchiveFailurePostgresPrecisionAndDeletion(t *testing.T) {
	url := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("archive_failure_%d", time.Now().UnixNano())
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
	f := seedArchiveFailureRun(t, s.SQLiteStore)
	if ok, err := s.TryAcquireSchedulerLease(testExecutionLeaseName, "owner", time.Now(), time.Minute); err != nil || !ok {
		t.Fatalf("lease: %v %v", ok, err)
	}
	if saved, err := s.SaveRunArchiveFailure(f, testExecutionLeaseName, "stale"); err != nil || saved {
		t.Fatalf("stale owner: %v %v", saved, err)
	}
	if saved, err := s.SaveRunArchiveFailure(f, testExecutionLeaseName, "owner"); err != nil || !saved {
		t.Fatalf("nanosecond input: %v %v", saved, err)
	}
	got, err := s.GetRunArchiveFailure(f.TestID)
	if err != nil || !got.StartTime.Equal(f.StartTime.Truncate(time.Microsecond)) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	f.StartTime = f.StartTime.Add(time.Microsecond)
	if saved, err := s.SaveRunArchiveFailure(f, testExecutionLeaseName, "owner"); err != nil || saved {
		t.Fatalf("different incarnation: %v %v", saved, err)
	}
	if err := s.DeleteTest(f.TestID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRunArchiveFailure(f.TestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted failure: %v", err)
	}
}
