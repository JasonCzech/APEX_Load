//go:build postgres_integration

package storage

import (
	"context"
	"fmt"
	neturl "net/url"
	"testing"
	"time"
)

func TestPostgresLibraryImportPublicationPrimitive(t *testing.T) {
	store, url := newPostgresScenarioRevisionFixture(t)
	second, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Share the exact same semantic and byte-presence assertions with SQLite.
	absent := requirePublication(t, store)
	if absent.State.Exists {
		t.Fatal("fixture unexpectedly has a claims row")
	}
	if applied, err := second.CompareAndSwapLibraryImportClaims("", "[]"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	before := requirePublication(t, store)
	if applied, err := store.CompareAndSwapLibraryImportClaims("[]", "[ ]"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if after := requirePublication(t, second.SQLiteStore); before.State == after.State {
		t.Fatal("PostgreSQL normalized raw JSON identity")
	}
	if applied, err := second.CompareAndSwapLibraryImportClaims("[]", "null"); err != nil || applied {
		t.Fatal("stale PostgreSQL CAS won", err)
	}
	if err := store.SaveSetting(LibraryImportClaimsSettingKey, ""); err != nil {
		t.Fatal(err)
	}
	empty := requirePublication(t, store)
	if !empty.State.Exists || empty.State.SHA256 != absent.State.SHA256 || empty.State == absent.State {
		t.Fatal("PostgreSQL collapsed row presence")
	}
	if applied, err := second.CompareAndSwapLibraryImportClaims("", "[]"); err != nil || applied {
		t.Fatal("PostgreSQL present-empty created", err)
	}
}

func TestPostgresLibraryImportPublicationLockOrder(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, operation := range []string{"read", "claims_cas", "blank_id_repair"} {
			t.Run(fmt.Sprintf("existing_%v_%s", existing, operation), func(t *testing.T) {
				first, url := newPostgresScenarioRevisionFixture(t)
				first.db.db.SetMaxOpenConns(4)
				scenario := seedScenarioRevision(t, first)
				sourceID := scenario.Scripts[0].ScriptID
				if existing {
					if err := first.SaveSetting(LibraryImportClaimsSettingKey, "[]"); err != nil {
						t.Fatal(err)
					}
				}
				parsed, err := neturl.Parse(url)
				if err != nil {
					t.Fatal(err)
				}
				values := parsed.Query()
				appName := values.Get("application_name") + "_pub"
				values.Set("application_name", appName)
				parsed.RawQuery = values.Encode()
				second, err := NewPostgresStore(parsed.String(), StoreConnectionOptions{MaxOpenConns: 2}, false)
				if err != nil {
					t.Fatal(err)
				}
				defer second.Close()
				tx, err := first.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
					t.Fatal(err)
				}
				before, err := readLibraryImportPublicationTx(tx)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "read":
						var snapshot *LibraryImportPublicationSnapshot
						snapshot, err = second.GetLibraryImportPublicationSnapshot()
						if err == nil && snapshot.State != before.State {
							err = fmt.Errorf("reader changed unchanged publication authority")
						}
					case "claims_cas":
						var applied bool
						applied, err = second.CompareAndSwapLibraryImportClaims(before.Raw, "[ ]")
						if err == nil && !applied {
							err = fmt.Errorf("waiting claims CAS did not apply")
						}
					case "blank_id_repair":
						err = second.backfillBlankScriptIDs()
					}
					done <- err
				}()
				joined := false
				defer func() {
					tx.Rollback()
					if !joined {
						select {
						case err := <-done:
							if err != nil {
								t.Error(err)
							}
						case <-time.After(10 * time.Second):
							t.Error("publication waiter did not join")
						}
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting int
					if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.application_name=? AND l.locktype='advisory' AND NOT l.granted AND l.classid::bigint=? AND l.objid::bigint=?`, appName, libraryImportPublicationAdvisoryLock>>32, libraryImportPublicationAdvisoryLock&0xffffffff).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting == 1 {
						break
					}
					select {
					case err := <-done:
						joined = true
						t.Fatalf("writer skipped shared fence: %v", err)
					case <-ctx.Done():
						t.Fatal("did not observe actual PostgreSQL publication lock wait")
					case <-ticker.C:
					}
				}
				// The waiter holds no project/source tuple lock while waiting for
				// publication. NOWAIT probes independently prove the required order.
				probe, err := first.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, identity := range []struct{ table, id string }{{"projects", scenario.ProjectID}, {"scripts", sourceID}} {
					var id string
					if err := probe.QueryRowContext(ctx, `SELECT id FROM `+identity.table+` WHERE id=? FOR UPDATE NOWAIT`, identity.id).Scan(&id); err != nil {
						probe.Rollback()
						t.Fatalf("publication waiter locked %s before its fence: %v", identity.table, err)
					}
				}
				if err := probe.Rollback(); err != nil {
					t.Fatal(err)
				}
				if _, err := lockProjectForOwnedMutation(tx, scenario.ProjectID); err != nil {
					t.Fatal(err)
				}
				var id string
				if err := tx.QueryRow(`SELECT id FROM scripts WHERE id=? FOR UPDATE`, sourceID).Scan(&id); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					joined = true
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("waiter did not finish after fence release")
				}
			})
		}
	}
}
