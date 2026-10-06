//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func resourcePostgresFixture(t *testing.T) (*PostgresStore, string) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set; genuine PostgreSQL acceptance was not executed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal("cannot open owned PostgreSQL resource fixture")
	}
	schema := fmt.Sprintf("apexload_browser_resources_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal("cannot create owned resource schema")
	}
	t.Cleanup(func() {
		defer admin.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("cannot drop exact owned resource schema")
			return
		}
		var remaining int
		if err := admin.QueryRowContext(cleanup, `SELECT count(*) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("owned resource schema cleanup was not verified")
			return
		}
		t.Logf("verified cleanup of exact owned PostgreSQL resource schema %s", schema)
	})
	dsn := postgresIntegrationURLWithSearchPath(t, raw, schema)
	store, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 1, MaxIdleConns: 1}, false)
	if err != nil {
		t.Fatal("cannot initialize owned PostgreSQL resource store")
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dsn
}
func TestPostgresBrowserResourceExactIdentityFreshnessAndCancellation(t *testing.T) {
	store, dsn := resourcePostgresFixture(t)
	checkBrowserResourceArchiveExactOwner(t, store.SQLiteStore)
	checkBrowserResourceArchiveFreshnessAndCancellation(t, store.SQLiteStore)
	reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 1}, false)
	if err != nil {
		t.Fatal("cannot reopen owned resource store")
	}
	defer reopened.Close()
	identity, err := reopened.ReadBrowserResourceArchiveIdentityContext(context.Background(), "run-1")
	if err != nil || identity.Archive.ManifestSHA256 != "replacement-sha" || identity.MaterializationKey != "incarnation-run-1" {
		t.Fatal("fresh durable archive identity did not survive independent reopen")
	}
	if _, err := reopened.ReadBrowserResourceArchiveIdentityContext(context.Background(), "collision"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("tombstoned exact owner was read after reopen")
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := store.ReadBrowserResourceArchiveIdentityContext(ctx, "run-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("occupied PostgreSQL identity read did not cancel")
	}
	if won, err := store.CompareAndSwapSettingContext(ctx, "occupied-resource-key", "", "not-written"); won || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancelled PostgreSQL CAS wrote a key")
	}
}
func TestPostgresBrowserResourceCursorKeyCASOneWinnerAndReopen(t *testing.T) {
	first, dsn := resourcePostgresFixture(t)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 1}, false)
	if err != nil {
		t.Fatal("cannot open second owned cursor store")
	}
	defer second.Close()
	const key = "security.browser_resource_cursor_hmac_key.v1"
	candidates := []string{base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32))), base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	var joined sync.WaitGroup
	for index, store := range []*PostgresStore{first, second} {
		joined.Add(1)
		go func(index int, store *PostgresStore) {
			defer joined.Done()
			won, err := store.CompareAndSwapSettingContext(ctx, key, "", candidates[index])
			results <- won
			failures <- err
		}(index, store)
	}
	joined.Wait()
	close(results)
	close(failures)
	wins := 0
	for won := range results {
		if won {
			wins++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent owned cursor initialization failed")
		}
	}
	if wins != 1 {
		t.Fatal("PostgreSQL cursor initialization did not retain exactly one winner")
	}
	retained, err := first.GetSettingContext(context.Background(), key)
	if err != nil || retained != candidates[0] && retained != candidates[1] {
		t.Fatal("cursor winner was not durable")
	}
	if won, err := second.CompareAndSwapSettingContext(context.Background(), key, "wrong", candidates[0]); won || err != nil {
		t.Fatal("stale expected cursor key replaced durable authority")
	}
	reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 1}, false)
	if err != nil {
		t.Fatal("cannot reopen cursor fixture")
	}
	defer reopened.Close()
	read, err := reopened.GetSettingContext(context.Background(), key)
	if err != nil || read != retained {
		t.Fatal("cursor key differed across independently reopened stores")
	}
}
func TestPostgresBrowserResourceCurrentAuthProjectionAndOccupiedCancellation(t *testing.T) {
	store, _ := resourcePostgresFixture(t)
	checkBrowserResourceAuthSQLProjection(t, store.SQLiteStore)
}
func TestPostgresBrowserResourceRoleOverflowIsExplicitRefusal(t *testing.T) {
	store, _ := resourcePostgresFixture(t)
	checkBrowserResourceAuthBindingOverflow(t, store.SQLiteStore)
}
