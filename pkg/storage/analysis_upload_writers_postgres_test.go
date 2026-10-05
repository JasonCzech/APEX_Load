//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresAnalysisUploadTwoStorePhysicalAuthority(t *testing.T) {
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
	schema := fmt.Sprintf("apexload_upload_writers_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quotePostgresIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quotePostgresIdentifier(schema)+" CASCADE"); err != nil {
			t.Error(err)
			return
		}
		var remaining int
		if err := admin.QueryRowContext(cleanup, `SELECT count(*) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("exact owned PostgreSQL upload fixture schema cleanup was not verified")
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, url, schema)
	first, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	seedMonitorFixture(t, first.SQLiteStore)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 1, MaxIdleConns: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseAnalysisUploadWriterAuthority(t, first.SQLiteStore, second.SQLiteStore)
	exercisePostgresAnalysisUploadInvisibleCommitFence(t, ctx, admin, first.SQLiteStore, second.SQLiteStore)
	exerciseAnalysisUploadMetadataLimit(t, first.SQLiteStore, second.SQLiteStore)
	exerciseAnalysisUploadProjectDeletion(t, first.SQLiteStore)
	if _, err := second.ResolveAnalysisUploadWriter(ctx, "monitor-p", uuid.NewString(), uuid.NewString()); !errors.Is(err, ErrAnalysisUploadNotCommitted) {
		t.Fatal("completed project deletion did not permit fenced absence proof", err)
	}
}

// Hold the actual Begin transaction before commit. PostgreSQL's READ COMMITTED
// journal lookup alone would report missing while that INSERT is still invisible.
// The second store must instead wait on the same scope fence, then resolve the
// committed receipt or return the distinct rollback proof after rollback.
func exercisePostgresAnalysisUploadInvisibleCommitFence(t *testing.T, ctx context.Context, admin *sql.DB, first, second *SQLiteStore) {
	t.Helper()
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("invisible-begin-commit-%t", commit), func(t *testing.T) {
			in := uploadWriterFixture(t, first)
			tx, err := first.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if present, err := analysisUploadLockProject(ctx, tx, in.ProjectID); err != nil || !present {
				t.Fatal("scope lock", present, err)
			}
			if _, err := analysisAttemptLoad(ctx, tx, in.ID, in.ProjectID); err != nil {
				t.Fatal(err)
			}
			now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
			if err != nil {
				t.Fatal(err)
			}
			intent := &AnalysisUploadWriter{ID: uuid.NewString(), ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Bytes: 100, SHA256: strings.Repeat("a", 64), State: "pending", CreatedAt: now}
			intent.ObjectKey = analysisUploadObjectKey(*intent)
			intent.TemporaryKey = analysisAttemptKey(intent.ProjectID, intent.ImportID, "upload-"+intent.ID+".tmp")
			if err := analysisUploadWrite(ctx, tx, intent, true); err != nil {
				t.Fatal(err)
			}
			var resolverPID int
			if err := second.db.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&resolverPID); err != nil {
				t.Fatal(err)
			}
			type result struct {
				w   *AnalysisUploadWriter
				err error
			}
			done := make(chan result, 1)
			resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			go func() {
				w, err := second.ResolveAnalysisUploadWriter(resolveCtx, in.ProjectID, in.ID, intent.ID)
				done <- result{w, err}
			}()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				var blockers int
				if err := admin.QueryRowContext(ctx, `SELECT cardinality(pg_blocking_pids($1))`, resolverPID).Scan(&blockers); err != nil {
					t.Fatal(err)
				}
				if blockers > 0 {
					blocked = true
					break
				}
				select {
				case got := <-done:
					t.Fatal("invisible journal absence bypassed Begin fence", got.w, got.err)
				case <-time.After(time.Millisecond):
				}
			}
			if !blocked {
				t.Fatal("resolver never waited on actual Begin transaction")
			}
			select {
			case got := <-done:
				t.Fatal("fenced resolution returned before actual transaction ended", got.w, got.err)
			default:
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if commit && (got.err != nil || got.w == nil || got.w.ID != intent.ID || got.w.PhysicallyClosed) {
					t.Fatal("committed invisible Begin lost exact owner", got.w, got.err)
				}
				if !commit && (!errors.Is(got.err, ErrAnalysisUploadNotCommitted) || got.w != nil) {
					t.Fatal("rollback did not return fenced absence proof", got.w, got.err)
				}
			case <-resolveCtx.Done():
				t.Fatal("resolution did not resume after actual Begin end", resolveCtx.Err())
			}
		})
	}
}
