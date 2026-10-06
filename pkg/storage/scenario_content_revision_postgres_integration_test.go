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
)

func newPostgresScenarioRevisionFixture(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Fatal("APEXLOAD_TEST_POSTGRES_URL is required for actual PostgreSQL scenario-revision acceptance")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("apexload_scenario_revision_%d", time.Now().UnixNano())
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

func TestPostgresScenarioContentRevisionIntegration(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *SQLiteStore)
	}{
		{"root_group_pool_triggers", exerciseScenarioRevisionTriggers},
		{"opaque_cas_receipts", exerciseScenarioAuthoringCAS},
		{"placement_monitor", exerciseScenarioAuthoringPlacementAndMonitors},
		{"reference_owner_source_boundary", exerciseScenarioAuthoringReferenceAndSourceBoundary},
		{"reparent_rollback", exerciseScenarioRevisionReparent},
		{"active_dependencies_context", exerciseScenarioAuthoringLifecycle},
		{"monitor_corruption", exerciseScenarioAuthoringMonitorCorruption},
		{"blank_source_repair", exerciseScenarioRevisionBlankIDRepair},
	} {
		t.Run(tc.name, func(t *testing.T) { store, _ := newPostgresScenarioRevisionFixture(t); tc.run(t, store) })
	}
	t.Run("repeatable_read_and_single_creation", func(t *testing.T) {
		first, url := newPostgresScenarioRevisionFixture(t)
		second, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		exerciseScenarioAuthoringCoherentRead(t, first, second.SQLiteStore)
		var wg sync.WaitGroup
		outcomes := make(chan *ScenarioAuthoringMutationResult, 2)
		failures := make(chan error, 2)
		for _, store := range []*SQLiteStore{first, second.SQLiteStore} {
			wg.Add(1)
			go func(store *SQLiteStore) {
				defer wg.Done()
				result, err := store.CreateScenarioAuthoringIfAbsent(&Scenario{ID: "pg-concurrent", Name: "Concurrent", Duration: "1m"}, nil, nil)
				outcomes <- result
				failures <- err
			}(store)
		}
		wg.Wait()
		close(outcomes)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		applied := 0
		nonce := ""
		for result := range outcomes {
			if result.Applied {
				applied++
			}
			if nonce != "" && nonce != result.Snapshot.ContentRevision {
				t.Fatal("different creation receipts")
			}
			nonce = result.Snapshot.ContentRevision
		}
		if applied != 1 {
			t.Fatal("create not single winner")
		}
	})
	t.Run("migration38_backfill_verified_functions", testPostgresScenarioRevisionMigration)
	t.Run("project_first_legacy_and_conditioned_writers", testPostgresScenarioRevisionWriterLockOrder)
	t.Run("child_commit_fences_old_nonce", testPostgresScenarioRevisionChildCommit)
	t.Run("policy_enable_race", testPostgresScenarioRevisionPolicyRace)
}

func testPostgresScenarioRevisionMigration(t *testing.T) {
	store, url := newPostgresScenarioRevisionFixture(t)
	defer func() { store.Close() }()
	old := seedScenarioRevision(t, store)
	before, err := store.SchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err := removeScenarioRevisionTrackingForTest(store); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scenarios DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=38`} {
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
	if err != nil || after.AppliedVersion != currentSchemaRelease().Version || !reflect.DeepEqual(before.Applied[:37], after.Applied[:37]) {
		t.Fatalf("released ledger changed %v", err)
	}
	backfill := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if backfill.ContentRevision == old.ContentRevision || !reflect.DeepEqual(backfill.Scripts, old.Scripts) {
		t.Fatal("backfill changed groups or failed nonce")
	}
	if _, err := store.db.Exec(`ALTER TABLE scenario_scripts DISABLE TRIGGER apexload_scenario_content_group`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE OR REPLACE FUNCTION apexload_bump_scenario_group_revision() RETURNS TRIGGER AS $body$BEGIN RETURN NEW; END;$body$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	store = opened.SQLiteStore
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != backfill.ContentRevision {
		t.Fatal("reopen reminted valid nonce")
	}
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET vusers=2 WHERE scenario_id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision == backfill.ContentRevision {
		t.Fatal("disabled/wrong function not restored")
	}
	if _, err := store.db.Exec(`UPDATE scenarios SET content_revision='' WHERE id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	store.Close()
	opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	store = opened.SQLiteStore
	missing := requireScenarioAuthoring(t, store, old.Scenario.ID)
	store.Close()
	opened, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	store = opened.SQLiteStore
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != missing.ContentRevision {
		t.Fatal("missing-only backfill repeated")
	}
	if _, err := store.db.Exec(`UPDATE scenarios SET content_revision='invalid' WHERE id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatal("malformed nonce qualified")
	}
	store.Close()
	if rejected, err := NewPostgresStore(url, StoreConnectionOptions{}, false); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		if rejected != nil {
			rejected.Close()
		}
		t.Fatalf("malformed nonce silently repaired: %v", err)
	}
}

// Wait until the real PostgreSQL session reports its hierarchy lock wait; an
// arbitrary sleep is not a proof that a writer has reached admission.
func waitScenarioProjectLock(t *testing.T, ctx context.Context, store *SQLiteStore, appName string, done <-chan error, joined *bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=? AND wait_event_type='Lock' AND query LIKE 'UPDATE projects SET updated_at%')`, appName).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			*joined = true
			t.Fatalf("writer bypassed hierarchy or failed before admission: %v", err)
		case <-ctx.Done():
			t.Fatal("actual project lock wait absent")
		case <-ticker.C:
		}
	}
}
func testPostgresScenarioRevisionWriterLockOrder(t *testing.T) {
	for _, operation := range []string{"legacy_save", "opaque_save", "replace_pools", "delete_binding", "delete_scenario", "legacy_timestamp_save"} {
		t.Run(operation, func(t *testing.T) {
			store, url := newPostgresScenarioRevisionFixture(t)
			old := seedScenarioRevision(t, store)
			parsed, _ := neturl.Parse(url)
			appName := parsed.Query().Get("application_name")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			hold, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := hold.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, old.ProjectID); err != nil {
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
						t.Error("owned scenario writer did not drain")
					}
				}
			}()
			candidate := *old.Scenario
			candidate.Name = "After hierarchy"
			go func() {
				var err error
				switch operation {
				case "legacy_save":
					err = store.SaveScenario(&candidate, old.Scripts)
				case "opaque_save":
					_, err = store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
				case "replace_pools":
					err = store.SaveScenarioDataPoolBindings(old.Scenario.ID, old.DataPools)
				case "delete_binding":
					err = store.DeleteScenarioDataPoolBinding(old.DataPools[0].ID)
				case "delete_scenario":
					err = store.DeleteScenario(old.Scenario.ID)
				case "legacy_timestamp_save":
					_, err = store.UpdateScenarioWithDataPoolBindingsIfUnchanged(&candidate, old.Scripts, old.DataPools, old.Scenario.UpdatedAt)
				}
				done <- err
			}()
			waitScenarioProjectLock(t, ctx, store, appName, done, &joined)
			probe, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, identity := range []struct{ table, id string }{{"scenarios", old.Scenario.ID}, {"scripts", "scn-script"}, {"shared_data_pools", "scn-pool"}} {
				var id string
				err := probe.QueryRowContext(ctx, `SELECT id FROM `+identity.table+` WHERE id=? FOR UPDATE NOWAIT`, identity.id).Scan(&id)
				if err != nil {
					probe.Rollback()
					t.Fatalf("%s locked %s before project: %v", operation, identity.table, err)
				}
			}
			probe.Rollback()
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
				t.Fatal("writer did not complete after project release")
			}
		})
	}
}
func testPostgresScenarioRevisionChildCommit(t *testing.T) {
	for _, relation := range []string{"root", "group", "pool", "monitor"} {
		t.Run(relation, func(t *testing.T) {
			store, _ := newPostgresScenarioRevisionFixture(t)
			old := seedScenarioRevision(t, store)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Rollback()
			query := ""
			args := []any{old.Scenario.ID}
			switch relation {
			case "root":
				query = `UPDATE scenarios SET name='Winning root' WHERE id=?`
			case "group":
				query = `UPDATE scenario_scripts SET vusers=23 WHERE scenario_id=?`
			case "pool":
				query = `UPDATE scenario_data_pool_bindings SET alias='accounts' WHERE scenario_id=?`
			case "monitor":
				query = `INSERT INTO monitor_bindings(scenario_id,project_id,revision,payload) VALUES(?,?,1,?)`
				args = []any{old.Scenario.ID, old.ProjectID, `{"scenario_id":"scn-parent","project_id":"scn-project","revision":1,"sources":[{"source_id":"monitor","required":false}]}`}
			}
			if _, err := child.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
			inside, err := readScenarioAuthoringSnapshotTx(child, old.Scenario.ID)
			if err != nil || inside.ContentRevision == old.ContentRevision {
				t.Fatal("mutation nonce was deferred")
			}
			contender, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var id string
			lockErr := contender.QueryRowContext(ctx, `SELECT id FROM scenarios WHERE id=? FOR UPDATE NOWAIT`, old.Scenario.ID).Scan(&id)
			contender.Rollback()
			var pgErr *pgconn.PgError
			if !errors.As(lockErr, &pgErr) || pgErr.Code != "55P03" {
				t.Fatalf("real mutation parent lock not owned: %v", lockErr)
			}
			done := make(chan error, 1)
			joined := false
			defer func() {
				child.Rollback()
				if !joined {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("owned stale writer not drained")
					}
				}
			}()
			candidate := *old.Scenario
			candidate.Name = "Must refuse"
			go func() {
				_, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
				done <- err
			}()
			if err := child.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, ErrScenarioAuthoringConflict) {
					t.Fatalf("old nonce bypassed %s commit: %v", relation, err)
				}
			case <-ctx.Done():
				t.Fatal("stale writer did not drain")
			}
			if current := requireScenarioAuthoring(t, store, old.Scenario.ID); !reflect.DeepEqual(current, inside) {
				t.Fatalf("stale writer overwrote committed %s", relation)
			}
		})
	}
}
func testPostgresScenarioRevisionPolicyRace(t *testing.T) {
	store, url := newPostgresScenarioRevisionFixture(t)
	old := seedScenarioRevision(t, store)
	parsed, _ := neturl.Parse(url)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	policy, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ExecContext(ctx, `UPDATE projects SET settings_json=? WHERE id=?`, `{"authoring":{"require_scenario_save_if_match":true}}`, old.ProjectID); err != nil {
		policy.Rollback()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	joined := false
	defer func() {
		policy.Rollback()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("owned legacy writer not drained")
			}
		}
	}()
	candidate := *old.Scenario
	candidate.Name = "Missing observed pair"
	expected := scenarioAuthoringExpected(old)
	expected.LegacyOrdinarySave = true
	go func() {
		_, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, expected)
		done <- err
	}()
	waitScenarioProjectLock(t, ctx, store, parsed.Query().Get("application_name"), done, &joined)
	if err := policy.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, ErrScenarioAuthoringPreconditionRequired) {
			t.Fatalf("policy-enable race admitted compatibility save: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("policy-admitted writer did not drain")
	}
	if !reflect.DeepEqual(old, requireScenarioAuthoring(t, store, old.Scenario.ID)) {
		t.Fatal("refused policy race mutated content")
	}
}
