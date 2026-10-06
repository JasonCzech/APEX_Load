//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func newPostgresScriptRevisionFixture(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Fatal("APEXLOAD_TEST_POSTGRES_URL is required for actual PostgreSQL script-revision acceptance")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("apexload_script_revision_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	url := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	values.Set("application_name", schema)
	parsed.RawQuery = values.Encode()
	url = parsed.String()
	store, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var version string
	if err := store.db.QueryRow(`SHOW server_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("Actual PostgreSQL server_version=%s owned schema=%s", version, schema)
	return store.SQLiteStore, url
}

func TestPostgresScriptContentRevisionIntegration(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *SQLiteStore)
	}{
		{"parent_triggers", exerciseScriptContentParentTriggers},
		{"opaque_cas", exerciseScriptAuthoringCAS},
		{"reference_lookup", exerciseScriptAuthoringReferenceLookup},
		{"authored_placement", exerciseScriptAuthoringPlacement},
		{"locked_policy_ruleset", exerciseScriptAuthoringPolicyAndRuleset},
		{"imports_rollback_receipts", exerciseScriptRevisionImports},
		{"blank_id_incarnation_and_placement", exerciseScriptRevisionBlankIDRepair},
	} {
		t.Run(tc.name, func(t *testing.T) { store, _ := newPostgresScriptRevisionFixture(t); tc.run(t, store) })
	}
	t.Run("coherent_read", func(t *testing.T) {
		first, url := newPostgresScriptRevisionFixture(t)
		second, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		exerciseScriptAuthoringCoherentRead(t, first, second.SQLiteStore)
	})
	t.Run("migration_reopen_and_shape_repair", func(t *testing.T) {
		store, url := newPostgresScriptRevisionFixture(t)
		defer func() { store.Close() }()
		script := &dsl.Script{ID: "pg-migration-revision", Name: "Original", ProjectID: DefaultProjectID, GoalConfig: scriptGoalsFixture()}
		if err := store.SaveScript(script); err != nil {
			t.Fatal(err)
		}
		before, err := store.SchemaStatus()
		if err != nil {
			t.Fatal(err)
		}
		if err := removeScriptRevisionTrackingForTest(store); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=37`} {
			if _, err := store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
		store.Close()
		opened, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		store = opened.SQLiteStore
		after, err := store.SchemaStatus()
		if err != nil || after.AppliedVersion != currentSchemaRelease().Version || !reflect.DeepEqual(before.Applied[:36], after.Applied[:36]) {
			t.Fatalf("published ledger %#v %v", after, err)
		}
		snapshot := requireScriptAuthoring(t, store, script.ID)
		if !reflect.DeepEqual(snapshot.Script.GoalConfig, scriptGoalsFixture()) {
			t.Fatal("release36 goals changed")
		}
		if _, err := store.db.Exec(`ALTER TABLE scripts DISABLE TRIGGER apexload_script_content_parent`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE OR REPLACE FUNCTION apexload_mint_script_content_revision() RETURNS TRIGGER AS $body$BEGIN RETURN NEW; END;$body$ LANGUAGE plpgsql`); err != nil {
			t.Fatal(err)
		}
		store.Close()
		opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		store = opened.SQLiteStore
		if requireScriptAuthoring(t, store, script.ID).ContentRevision != snapshot.ContentRevision {
			t.Fatal("reopen reminted valid nonce")
		}
		if _, err := store.db.Exec(`UPDATE scripts SET name='Restored trigger' WHERE id=?`, script.ID); err != nil {
			t.Fatal(err)
		}
		if requireScriptAuthoring(t, store, script.ID).ContentRevision == snapshot.ContentRevision {
			t.Fatal("disabled/wrong-function trigger not repaired")
		}
		if _, err := store.db.Exec(`UPDATE scripts SET content_revision='' WHERE id=?`, script.ID); err != nil {
			t.Fatal(err)
		}
		store.Close()
		opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		store = opened.SQLiteStore
		backfilled := requireScriptAuthoring(t, store, script.ID)
		store.Close()
		opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		store = opened.SQLiteStore
		if requireScriptAuthoring(t, store, script.ID).ContentRevision != backfilled.ContentRevision {
			t.Fatal("missing-only backfill repeated")
		}
		if _, err := store.db.Exec(`UPDATE scripts SET content_revision='bad' WHERE id=?`, script.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetScriptAuthoringSnapshot(script.ID); !errors.Is(err, ErrScriptAuthoringUnavailable) {
			t.Fatalf("invalid persisted nonce accepted%v", err)
		}
		store.Close()
		if rejected, err := NewPostgresStore(url, StoreConnectionOptions{}, false); !errors.Is(err, ErrScriptAuthoringUnavailable) {
			if rejected != nil {
				rejected.Close()
			}
			t.Fatalf("invalid nonce silently repaired%v", err)
		}
	})
	t.Run("concurrent_initialization", func(t *testing.T) {
		store, url := newPostgresScriptRevisionFixture(t)
		if err := store.SaveScript(&dsl.Script{ID: "pg-parallel-init", Name: "Original", ProjectID: DefaultProjectID}); err != nil {
			t.Fatal(err)
		}
		if err := removeScriptRevisionTrackingForTest(store); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=37`} {
			if _, err := store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
		store.Close()
		outcomes := make(chan *PostgresStore, 2)
		failures := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				store, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
				outcomes <- store
				failures <- err
			}()
		}
		wg.Wait()
		close(outcomes)
		close(failures)
		var opened []*PostgresStore
		for reopened := range outcomes {
			if reopened != nil {
				opened = append(opened, reopened)
				t.Cleanup(func() { reopened.Close() })
			}
		}
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		var nonce string
		for _, reopened := range opened {
			snapshot := requireScriptAuthoring(t, reopened.SQLiteStore, "pg-parallel-init")
			if nonce != "" && snapshot.ContentRevision != nonce {
				t.Fatal("concurrent initialization reminted nonce")
			}
			nonce = snapshot.ContentRevision
			status, err := reopened.SchemaStatus()
			if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 {
				t.Fatalf("concurrent ledger%#v %v", status, err)
			}
			reopened.Close()
		}
	})
	t.Run("legacy_writer_hierarchy_lock_order", func(t *testing.T) {
		store, url := newPostgresScriptRevisionFixture(t)
		parsed, err := neturl.Parse(url)
		if err != nil {
			t.Fatal(err)
		}
		applicationName := parsed.Query().Get("application_name")
		script := &dsl.Script{ID: "pg-legacy-lock-order", ProjectID: DefaultProjectID, Name: "Original"}
		if err := store.SaveScript(script); err != nil {
			t.Fatal(err)
		}
		folder := "pg-lock-order-folder"
		if err := store.SaveFolder(&Folder{ID: folder, ProjectID: DefaultProjectID, Name: "Folder", Category: "scripts"}); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"save", "move"} {
			t.Run(operation, func(t *testing.T) {
				current := requireScriptAuthoring(t, store, script.ID)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				hold, err := store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				var project string
				if err := hold.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=? FOR UPDATE`, DefaultProjectID).Scan(&project); err != nil {
					hold.Rollback()
					t.Fatal(err)
				}
				done := make(chan error, 1)
				joined := false
				defer func() {
					hold.Rollback()
					if !joined {
						select {
						case <-done:
						case <-time.After(3 * time.Second):
							t.Error("owned legacy writer did not drain")
						}
					}
				}()
				go func() {
					if operation == "save" {
						candidate := *current.Script
						candidate.Name = "Legacy save after hierarchy admission"
						done <- store.SaveScript(&candidate)
					} else {
						done <- store.MoveScript(script.ID, &folder)
					}
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting bool
					if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=? AND wait_event_type='Lock' AND query LIKE 'UPDATE projects SET updated_at%')`, applicationName).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case err := <-done:
						joined = true
						t.Fatalf("writer bypassed held hierarchy or failed early: %v", err)
					case <-ctx.Done():
						t.Fatal("actual hierarchy wait was not observed")
					case <-ticker.C:
					}
				}
				// A writer blocked on hierarchy must not already own the script tuple.
				// Parent-first legacy code makes this NOWAIT probe fail with55P03.
				probe, err := store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				var id string
				lockErr := probe.QueryRowContext(ctx, `SELECT id FROM scripts WHERE id=? FOR UPDATE NOWAIT`, script.ID).Scan(&id)
				probe.Rollback()
				if lockErr != nil || id != script.ID {
					t.Fatalf("legacy %s locked parent before hierarchy: %v", operation, lockErr)
				}
				if err := hold.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					joined = true
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("legacy writer did not finish after hierarchy release")
				}
			})
		}
	})
	t.Run("child_write_fences_parent_before_commit", func(t *testing.T) {
		store, _ := newPostgresScriptRevisionFixture(t)
		seedLibraryAssignments(t, store)
		app := "la-app"
		if _, _, err := store.ReplaceLibraryAssignment("script", "la-script", "la-project", &app, nil, 0); err != nil {
			t.Fatal(err)
		}
		old := requireScriptAuthoring(t, store, "la-script")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		child, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer child.Rollback()
		if _, err := child.ExecContext(ctx, `INSERT INTO script_library_assignments_releases(item_id,release_id) VALUES ('la-script','la-r1')`); err != nil {
			t.Fatal(err)
		}
		inside, err := readScriptAuthoringSnapshotTx(child, "la-script")
		if err != nil {
			t.Fatal(err)
		}
		if inside.ContentRevision == old.ContentRevision {
			t.Fatal("child trigger was deferred")
		}
		// Independent NOWAIT proves the child already owns the parent tuple lock.
		contender, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		lockErr := contender.QueryRowContext(ctx, `SELECT id FROM scripts WHERE id='la-script' FOR UPDATE NOWAIT`).Scan(&id)
		contender.Rollback()
		var pgError *pgconn.PgError
		if !errors.As(lockErr, &pgError) || pgError.Code != "55P03" {
			t.Fatalf("expected real parent lock refusal, got %v", lockErr)
		}
		done := make(chan error, 1)
		candidate := *old.Script
		candidate.Name = "Must not overwrite child"
		go func() {
			_, err := store.UpdateScriptAuthoringIfCurrent(&candidate, scriptAuthoringExpected(old))
			done <- err
		}()
		if err := child.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrScriptAuthoringConflict) {
				t.Fatalf("stale parent slipped past child%v", err)
			}
		case <-ctx.Done():
			t.Fatal("stale parent CAS did not drain")
		}
		current := requireScriptAuthoring(t, store, "la-script")
		if current.Script.Name != old.Script.Name || len(current.LibraryAssignment.Releases) != 1 {
			t.Fatal("stale parent mutation changed authored content")
		}
	})
}
