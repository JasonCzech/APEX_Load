//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresConcurrentInitialization(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_concurrent_init_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	scoped := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	for _, phase := range []string{"empty", "initialized"} {
		if phase == "initialized" {
			if _, err := admin.ExecContext(ctx, "INSERT INTO "+quoted+".app_settings (key, value, updated_at) VALUES ('initialization-sentinel', 'preserved', NOW())"); err != nil {
				t.Fatal(err)
			}
		}
		t.Run(phase, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan error, 8)
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					<-start
					store, err := NewPostgresStore(scoped, StoreConnectionOptions{MaxOpenConns: 1, MaxIdleConns: 1, ConnMaxLifetime: time.Nanosecond}, false)
					if err != nil {
						results <- err
						return
					}
					defer store.Close()
					status, err := store.SchemaStatus()
					if err == nil && (status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 || len(status.Applied) != len(schemaReleases)) {
						err = fmt.Errorf("incomplete schema: applied=%d pending=%d", status.AppliedVersion, len(status.Pending))
					}
					if err == nil && phase == "initialized" {
						value, readErr := store.GetSetting("initialization-sentinel")
						if readErr != nil {
							err = readErr
						} else if value != "preserved" {
							err = fmt.Errorf("sentinel changed: %q", value)
						}
					}
					results <- err
				})
			}
			close(start)
			workers.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// The callback intentionally skips application DDL: this tests session ownership
// and release independently of migration success or schema contents.
func TestPostgresInitializationLockLifecycle(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := newSQLStore(db, storeDialectPostgres, StoreBackendInfo{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sentinel := errors.New("initializer failed")
	var firstPID int
	err = store.withPostgresInitializationLock(ctx, func() error {
		if err := store.db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&firstPID); err != nil {
			return err
		}
		if _, err := store.db.ExecContext(ctx, "CREATE TEMP TABLE initialization_session_marker(value TEXT)"); err != nil {
			return err
		}
		if _, err := store.db.ExecContext(ctx, "INSERT INTO initialization_session_marker VALUES (?)", "preserved"); err != nil {
			return err
		}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var pid int
		var value string
		if err := tx.QueryRow("SELECT pg_backend_pid(), value FROM initialization_session_marker").Scan(&pid, &value); err != nil {
			return err
		}
		if pid != firstPID || value != "preserved" {
			return fmt.Errorf("initialization changed session or value")
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("initializer error = %v", err)
	}
	if store.db.initializationConn != nil {
		t.Fatal("initialization session remains attached")
	}
	err = store.withPostgresInitializationLock(ctx, func() error {
		var pid int
		var absent bool
		if err := store.db.QueryRowContext(ctx, "SELECT pg_backend_pid(), to_regclass('pg_temp.initialization_session_marker') IS NULL").Scan(&pid, &absent); err != nil {
			return err
		}
		if pid == firstPID || !absent {
			return errors.New("failed initialization session was reused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A canceled waiter must not run initialization or return a locked session
	// to the pool. An independent session holds the lock for this check.
	holderDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer holderDB.Close()
	holder, err := holderDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_lock($1)", postgresInitializationLock); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", postgresInitializationLock)
	waiting, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	called := false
	err = store.withPostgresInitializationLock(waiting, func() error { called = true; return nil })
	stop()
	if err == nil || called || store.db.initializationConn != nil {
		t.Fatalf("canceled initialization: err=%v called=%v", err, called)
	}
	if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", postgresInitializationLock); err != nil {
		t.Fatal(err)
	}
	if err := store.withPostgresInitializationLock(ctx, func() error { return nil }); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	// Losing the owning backend must fail initialization, never transparently
	// reconnect without its lock and continue applying schema changes.
	err = store.withPostgresInitializationLock(ctx, func() error {
		var pid int
		if err := store.db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		var terminated bool
		if err := holder.QueryRowContext(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&terminated); err != nil {
			return err
		}
		if !terminated {
			return errors.New("test backend was not terminated")
		}
		if _, err := store.db.ExecContext(ctx, "SELECT 1"); err == nil {
			return errors.New("initialization reconnected without its lock")
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost session: %v", err)
	}
	if err := store.withPostgresInitializationLock(ctx, func() error { return nil }); err != nil {
		t.Fatalf("lost session retained lock: %v", err)
	}

}
