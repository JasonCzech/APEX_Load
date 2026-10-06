//go:build postgres_integration

package storage

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type privateSourceOutcome struct {
	result *ScenarioAuthoringMutationResult
	err    error
}

func sourcePeer(t *testing.T, url, suffix string) (*SQLiteStore, string) {
	t.Helper()
	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	name := values.Get("application_name")
	if len(name) > 45 {
		name = name[:45]
	}
	name += "_" + suffix
	values.Set("application_name", name)
	parsed.RawQuery = values.Encode()
	peer, err := NewPostgresStore(parsed.String(), StoreConnectionOptions{MaxOpenConns: 3}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	return peer.SQLiteStore, name
}
func waitPrivateSourceQuery(t *testing.T, ctx context.Context, store *SQLiteStore, name, query string, done <-chan privateSourceOutcome, joined *bool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=? AND wait_event_type='Lock' AND query LIKE ?)`, name, query).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case o := <-done:
			*joined = true
			t.Fatalf("private writer bypassed boundary: %#v/%v", o.result, o.err)
		case <-ctx.Done():
			t.Fatal("observable private writer wait absent")
		case <-ticker.C:
		}
	}
}
func probePrivateSourceTuple(t *testing.T, ctx context.Context, store *SQLiteStore, table, id string, locked bool) {
	t.Helper()
	probe, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	var got string
	err = probe.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE id=? FOR UPDATE NOWAIT`, id).Scan(&got)
	var pg *pgconn.PgError
	if locked {
		if !errors.As(err, &pg) || pg.Code != "55P03" {
			t.Fatalf("expected owned %s tuple lock: %v", table, err)
		}
	} else if err != nil {
		t.Fatalf("locked %s before boundary: %v", table, err)
	}
}
func drainPrivateSource(t *testing.T, hold *storeTx, done <-chan privateSourceOutcome, joined *bool) {
	t.Helper()
	hold.Rollback()
	if !*joined {
		select {
		case <-done:
			*joined = true
		case <-time.After(15 * time.Second):
			t.Error("owned private writer did not join")
		}
	}
}

func TestPostgresScenarioSourceSemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *SQLiteStore)
	}{{"happy", exerciseScenarioSourceAdmissionHappy}, {"publication", exerciseScenarioSourcePublication}, {"content_incarnation_owner", exerciseScenarioSourceChanged}, {"lifecycle", exerciseScenarioSourceLifecycle}, {"delete_recreate", exerciseScenarioSourceRecreation}} {
		t.Run(tc.name, func(t *testing.T) { store, _ := newPostgresScenarioRevisionFixture(t); tc.run(t, store) })
	}
}

func TestPostgresScenarioSourceAdmissionLockOrder(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, boundary := range []string{"publication", "project", "scenario", "sorted_source", "pool"} {
			if create && boundary == "scenario" {
				continue
			}
			t.Run(fmt.Sprintf("create_%v_%s", create, boundary), func(t *testing.T) {
				first, url := newPostgresScenarioRevisionFixture(t)
				first.db.db.SetMaxOpenConns(5)
				old := seedScenarioRevision(t, first)
				if err := first.SaveDataPool(&SharedDataPool{ID: "scn-pool-two", Name: "Second pool", ProjectID: old.ProjectID}); err != nil {
					t.Fatal(err)
				}
				pools := append([]ScenarioDataPoolBinding(nil), old.DataPools...)
				pools = append(pools, ScenarioDataPoolBinding{ID: "source-second-binding", PoolID: "scn-pool-two", Alias: "second", AccessMode: "read_only"})
				pools[0], pools[1] = pools[1], pools[0]
				poolOwner := *old
				poolOwner.DataPools = pools
				candidate, groups := sourceCandidate(old, create)
				groups = append(groups, ScenarioScript{ScriptID: "scn-script-two", Name: "Second", VUsers: 1})
				// Deliberately reverse the request to prove source locks use sorted identities.
				groups[0], groups[1] = groups[1], groups[0]
				evidence := requireScenarioSources(t, first, groups).Admission
				peer, name := sourcePeer(t, url, "cas")
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				hold, err := first.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Rollback()
				query := ""
				switch boundary {
				case "publication":
					if err := acquireLibraryImportPublicationFenceTx(hold); err != nil {
						t.Fatal(err)
					}
					query = "%pg_advisory_xact_lock%"
				case "project":
					if _, err := lockProjectForOwnedMutation(hold, old.ProjectID); err != nil {
						t.Fatal(err)
					}
					query = "UPDATE projects SET updated_at%"
				case "scenario":
					var id string
					if err := hold.QueryRow(`SELECT id FROM scenarios WHERE id=? FOR UPDATE`, old.Scenario.ID).Scan(&id); err != nil {
						t.Fatal(err)
					}
					query = "SELECT created_at%FROM scenarios%FOR UPDATE"
				case "sorted_source":
					var id string
					if err := hold.QueryRow(`SELECT id FROM scripts WHERE id='scn-script-two' FOR UPDATE`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					query = "%scripts%"
				case "pool":
					var id string
					if err := hold.QueryRow(`SELECT id FROM shared_data_pools WHERE id='scn-pool-two' FOR UPDATE`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					query = "UPDATE shared_data_pools%"
				}
				done := make(chan privateSourceOutcome, 1)
				joined := false
				defer drainPrivateSource(t, hold, done, &joined)
				go func() {
					var result *ScenarioAuthoringMutationResult
					var err error
					if create {
						result, err = peer.CreateScenarioAuthoringWithSourcesIfAbsent(candidate, groups, sourceCreatePools(&poolOwner), evidence)
					} else {
						result, err = peer.UpdateScenarioAuthoringWithSourcesIfCurrent(candidate, groups, pools, scenarioAuthoringExpected(old), evidence)
					}
					done <- privateSourceOutcome{result, err}
				}()
				waitPrivateSourceQuery(t, ctx, first, name, query, done, &joined)
				if boundary == "publication" {
					var waiting int
					if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.application_name=? AND l.locktype='advisory' AND NOT l.granted AND l.classid::bigint=? AND l.objid::bigint=?`, name, libraryImportPublicationAdvisoryLock>>32, libraryImportPublicationAdvisoryLock&0xffffffff).Scan(&waiting); err != nil || waiting != 1 {
						t.Fatal("specific publication fence wait absent", waiting, err)
					}
					probePrivateSourceTuple(t, ctx, first, "projects", old.ProjectID, false)
				}
				if boundary == "publication" || boundary == "project" {
					probePrivateSourceTuple(t, ctx, first, "scenarios", old.Scenario.ID, false)
				}
				if boundary == "publication" || boundary == "project" || boundary == "scenario" {
					for _, id := range []string{"scn-script", "scn-script-two"} {
						probePrivateSourceTuple(t, ctx, first, "scripts", id, false)
					}
				}
				if boundary == "sorted_source" {
					probePrivateSourceTuple(t, ctx, first, "scripts", "scn-script", true)
				}
				if boundary != "pool" {
					for _, id := range []string{"scn-pool", "scn-pool-two"} {
						probePrivateSourceTuple(t, ctx, first, "shared_data_pools", id, false)
					}
				} else {
					probePrivateSourceTuple(t, ctx, first, "shared_data_pools", "scn-pool", true)
				}
				if boundary == "pool" {
					for _, id := range []string{"scn-script", "scn-script-two"} {
						probePrivateSourceTuple(t, ctx, first, "scripts", id, true)
					}
				}
				if err := hold.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case o := <-done:
					joined = true
					if o.err != nil || o.result == nil || !o.result.Applied {
						t.Fatalf("admission failed after release %#v/%v", o.result, o.err)
					}
				case <-ctx.Done():
					t.Fatal("private writer did not complete")
				}
			})
		}
	}
}

func TestPostgresScenarioSourcePublicationBetweenSnapshots(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(fmt.Sprintf("create_%v", create), func(t *testing.T) {
			first, url := newPostgresScenarioRevisionFixture(t)
			first.db.db.SetMaxOpenConns(5)
			old := seedScenarioRevision(t, first)
			initial := requireScenarioSources(t, first, old.Scripts).Admission
			peer, name := sourcePeer(t, url, "pubrace")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			hold, err := first.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback()
			if err := acquireLibraryImportPublicationFenceTx(hold); err != nil {
				t.Fatal(err)
			}
			done := make(chan privateSourceOutcome, 1)
			joined := false
			defer drainPrivateSource(t, hold, done, &joined)
			go func() { r, err := trySourceMutation(peer, old, create, initial); done <- privateSourceOutcome{r, err} }()
			waitPrivateSourceQuery(t, ctx, first, name, "%pg_advisory_xact_lock%", done, &joined)
			raw := `[{"kind":"script","prepared":true,"local_item_id":"scn-script","remote_id":"direct-import:survivor"}]`
			if _, err := hold.Exec(`INSERT INTO app_settings(key,value) VALUES(?,?)`, LibraryImportClaimsSettingKey, raw); err != nil {
				t.Fatal(err)
			}
			if err := hold.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case o := <-done:
				joined = true
				if o.result != nil || !errors.Is(o.err, ErrScenarioSourceAdmissionConflict) {
					t.Fatalf("prepared surviving source accepted %#v/%v", o.result, o.err)
				}
			case <-ctx.Done():
				t.Fatal("publication waiter did not join")
			}
			if !reflect.DeepEqual(old, requireScenarioAuthoring(t, first, old.Scenario.ID)) {
				t.Fatal("publication refusal changed parent")
			}
			if _, err := first.GetScenarioAuthoringSnapshot("source-new"); !errors.Is(err, ErrScenarioAuthoringNotFound) {
				t.Fatal("publication refusal installed create", err)
			}
		})
	}
}

func TestPostgresScenarioSourceRawOwnerInversion(t *testing.T) {
	first, url := newPostgresScenarioRevisionFixture(t)
	first.db.db.SetMaxOpenConns(5)
	old := seedScenarioRevision(t, first)
	initial := requireScenarioSources(t, first, old.Scripts).Admission
	peer, name := sourcePeer(t, url, "owner")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	admin, err := first.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Rollback()
	var id string
	if err := admin.QueryRow(`SELECT id FROM scripts WHERE id='scn-script' FOR UPDATE`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	done := make(chan privateSourceOutcome, 1)
	joined := false
	defer drainPrivateSource(t, admin, done, &joined)
	go func() { r, err := trySourceMutation(peer, old, false, initial); done <- privateSourceOutcome{r, err} }()
	waitPrivateSourceQuery(t, ctx, first, name, "UPDATE scripts SET updated_at%", done, &joined)
	probePrivateSourceTuple(t, ctx, first, "scenarios", old.Scenario.ID, true)
	// Raw administrative owner repair deliberately takes source before scenario.
	// Keep the real trigger; PostgreSQL must resolve the actual inverse-order cycle.
	_, adminErr := admin.ExecContext(ctx, `UPDATE scripts SET project_id='scn-foreign' WHERE id='scn-script'`)
	var pg *pgconn.PgError
	rawWon := adminErr == nil
	if rawWon {
		if err := admin.Commit(); err != nil {
			t.Fatal(err)
		}
	} else {
		if !errors.As(adminErr, &pg) || pg.Code != "40P01" {
			t.Fatalf("unexpected inversion outcome %v", adminErr)
		}
		admin.Rollback()
		t.Log("raw administrative repair was actual PostgreSQL deadlock victim (40P01)")
	}
	var outcome privateSourceOutcome
	select {
	case outcome = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("inversion waiter did not join")
	}
	if rawWon {
		if outcome.result != nil || !errors.Is(outcome.err, ErrScenarioSourceAdmissionUnavailable) {
			t.Fatalf("raw owner winner bypassed fail-closed CAS %#v/%v", outcome.result, outcome.err)
		}
		var name string
		if err := first.db.QueryRow(`SELECT name FROM scenarios WHERE id=?`, old.Scenario.ID).Scan(&name); err != nil || name != old.Scenario.Name {
			t.Fatal("deadlock victim installed candidate", err)
		}
		// Owner corruption intentionally makes the qualified scenario reader refuse.
		if _, err := first.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
			t.Fatal("foreign owner corruption qualified", err)
		}
	} else {
		if outcome.err != nil || outcome.result == nil || !outcome.result.Applied {
			t.Fatalf("rolled-back raw victim invalidated unchanged evidence %#v/%v", outcome.result, outcome.err)
		}
		source, err := first.GetScriptAuthoringSnapshot("scn-script")
		if err != nil || source.ContentRevision != initial.Sources[0].ContentRevision || source.Script.ProjectID != old.ProjectID {
			t.Fatal("raw victim retained source mutation", err)
		}
	}
	// Neither outcome may lose or rewrite the source/pool child identities.
	var sourceID, poolID string
	if err := first.db.QueryRow(`SELECT script_id FROM scenario_scripts WHERE scenario_id=?`, old.Scenario.ID).Scan(&sourceID); err != nil || sourceID != old.Scripts[0].ScriptID {
		t.Fatal("inversion changed source children", err)
	}
	if err := first.db.QueryRow(`SELECT pool_id FROM scenario_data_pool_bindings WHERE scenario_id=?`, old.Scenario.ID).Scan(&poolID); err != nil || poolID != old.DataPools[0].PoolID {
		t.Fatal("inversion changed pool children", err)
	}
}

func TestPostgresScenarioSourceWriterWaitsThroughCommit(t *testing.T) {
	for _, operation := range []string{"editor", "archive"} {
		t.Run(operation, func(t *testing.T) {
			first, url := newPostgresScenarioRevisionFixture(t)
			first.db.db.SetMaxOpenConns(5)
			old := seedScenarioRevision(t, first)
			initial := requireScenarioSources(t, first, old.Scripts).Admission
			peer, casName := sourcePeer(t, url, "commit")
			outside, outsideName := sourcePeer(t, url, operation)
			source, err := first.GetScript(old.Scripts[0].ScriptID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			hold, err := first.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback()
			var id string
			if err := hold.QueryRow(`SELECT id FROM shared_data_pools WHERE id=? FOR UPDATE`, old.DataPools[0].PoolID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			done := make(chan privateSourceOutcome, 1)
			joined := false
			defer drainPrivateSource(t, hold, done, &joined)
			go func() { r, err := trySourceMutation(peer, old, false, initial); done <- privateSourceOutcome{r, err} }()
			waitPrivateSourceQuery(t, ctx, first, casName, "UPDATE shared_data_pools%", done, &joined)
			probePrivateSourceTuple(t, ctx, first, "scripts", source.ID, true)
			outsideDone := make(chan privateSourceOutcome, 1)
			outsideJoined := false
			defer drainPrivateSource(t, hold, outsideDone, &outsideJoined)
			go func() {
				var err error
				if operation == "editor" {
					source.Name = "Postcommit edit"
					err = outside.SaveScript(source)
				} else {
					var applied bool
					applied, err = outside.SetObjectArchived("script", source.ID, source.UpdatedAt, true)
					if err == nil && !applied {
						err = fmt.Errorf("archive did not apply")
					}
				}
				outsideDone <- privateSourceOutcome{err: err}
			}()
			pattern := "UPDATE projects SET updated_at%"
			if operation == "archive" {
				pattern = "UPDATE scripts SET updated_at%"
			}
			waitPrivateSourceQuery(t, ctx, first, outsideName, pattern, outsideDone, &outsideJoined)
			if err := hold.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case o := <-done:
				joined = true
				if o.err != nil || o.result == nil || !o.result.Applied {
					t.Fatal("final source admission failed", o.err)
				}
			case <-ctx.Done():
				t.Fatal("CAS did not commit")
			}
			select {
			case o := <-outsideDone:
				outsideJoined = true
				if o.err != nil {
					t.Fatal("postcommit source writer failed", o.err)
				}
			case <-ctx.Done():
				t.Fatal("source writer did not join")
			}
			parent := requireScenarioAuthoring(t, first, old.Scenario.ID)
			if parent.Scenario.Name != "Attempted candidate" {
				t.Fatal("source writer overwrote parent")
			}
		})
	}
}
