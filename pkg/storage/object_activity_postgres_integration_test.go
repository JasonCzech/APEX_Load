//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPostgresObjectActivitySummaryIndex(t *testing.T) {
	store, dsn := managementPostgresFixture(t)
	assertIndex := func(s *SQLiteStore) {
		t.Helper()
		var columns string
		if err := s.db.QueryRow(`SELECT COALESCE(string_agg(a.attname, ',' ORDER BY k.ordinality), '')
			FROM pg_index i
			JOIN pg_class idx ON idx.oid=i.indexrelid
			JOIN pg_namespace ns ON ns.oid=idx.relnamespace
			CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ordinality)
			JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum
			WHERE ns.nspname=current_schema() AND idx.relname='idx_object_activity_summary'
			AND i.indrelid='object_activity_events'::regclass AND i.indisvalid`).Scan(&columns); err != nil {
			t.Fatalf("read activity summary index: %v", err)
		}
		if want := "project_id,resource_type,resource_id,action,status,created_at"; columns != want {
			t.Fatalf("activity summary index columns = %q, want %q", columns, want)
		}
	}
	assertIndex(store.SQLiteStore)
	now := time.Now().UTC()
	for i, action := range []string{"create", "modify", "archive"} {
		if err := store.SaveObjectActivity(&ObjectActivityEvent{
			ID: "index-" + action, ProjectID: DefaultProjectID, ResourceType: "script", ResourceID: "index-resource",
			Action: action, ActorSubject: "index-user", Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	want, err := store.ObjectActivitySummaries(DefaultProjectID, "script", []string{"index-resource"})
	if err != nil {
		t.Fatal(err)
	}
	if v := want["index-resource"]; v.Creator == nil || v.LastModifier == nil || v.LastArchive == nil {
		t.Fatalf("seeded activity summary is incomplete: %#v", v)
	}
	if _, err := store.db.Exec(`DROP INDEX idx_object_activity_summary`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version>=64`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The first reopen upgrades an existing database; the second must preserve
	// the index and retained summaries without changing its migration record.
	var appliedAt time.Time
	for i := 0; i < 2; i++ {
		reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		assertIndex(reopened.SQLiteStore)
		got, err := reopened.ObjectActivitySummaries(DefaultProjectID, "script", []string{"index-resource"})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("reopen %d changed activity summary: got %#v, want %#v, err %v", i, got, want, err)
		}
		status, err := reopened.SchemaStatus()
		if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 {
			t.Fatalf("reopen %d schema status = %#v, err %v", i, status, err)
		}
		latest := status.Applied[len(status.Applied)-1]
		if i == 0 {
			appliedAt = latest.AppliedAt
		} else if !latest.AppliedAt.Equal(appliedAt) {
			t.Fatalf("idempotent reopen changed migration timestamp: %v, want %v", latest.AppliedAt, appliedAt)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObjectArchivePostgres(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("apexload_object_activity_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkObjectArchiveLifecycle(t, store.SQLiteStore)
	checkArchivedSourceAdmission(t, store.SQLiteStore)
	checkObjectArchiveConcurrentTransitions(t, store.SQLiteStore)
	checkObjectActivityFiltersAndSummaries(t, store.SQLiteStore)
	checkArchiveRestorationScheduleBoundary(t, store.SQLiteStore)
	checkArchiveStateAcrossPostgresTimezones(t, store.SQLiteStore)
}

func checkArchiveStateAcrossPostgresTimezones(t *testing.T, s *SQLiteStore) {
	t.Helper()
	// Keep SET TIME ZONE and subsequent store calls on the same connection.
	s.db.db.SetMaxOpenConns(1)
	for _, kind := range []string{"script", "scenario"} {
		t.Run("timezone-"+kind, func(t *testing.T) {
			if _, err := s.db.Exec(`SET TIME ZONE 'UTC'`); err != nil {
				t.Fatal(err)
			}
			id := "timezone-" + kind
			if kind == "script" {
				if err := s.SaveScript(&dsl.Script{ID: id, Name: "Timezone source", Protocol: "http", Version: "1", Actions: []dsl.Action{}}); err != nil {
					t.Fatal(err)
				}
			} else if err := s.SaveScenario(&Scenario{ID: id, Name: "Timezone scenario", Duration: "1m"}, nil); err != nil {
				t.Fatal(err)
			}
			read := func(wantArchived bool) time.Time {
				t.Helper()
				if kind == "script" {
					v, err := s.GetScript(id)
					if err != nil || (v.ArchivedAt != nil) != wantArchived {
						t.Fatalf("script archive state = %#v %v", v, err)
					}
					return v.UpdatedAt
				}
				v, _, err := s.GetScenario(id)
				if err != nil || (v.ArchivedAt != nil) != wantArchived {
					t.Fatalf("scenario archive state = %#v %v", v, err)
				}
				return v.UpdatedAt
			}
			plannedAt := time.Now().UTC()
			version := read(false)
			if _, err := s.SetObjectArchived(kind, id, version, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`SET TIME ZONE 'Asia/Tokyo'`); err != nil {
				t.Fatal(err)
			}
			archivedVersion := read(true)
			if changed, err := s.SetObjectArchived(kind, id, version, true); err != nil || changed {
				t.Fatalf("repeated archive changed with timezone: %v %v", changed, err)
			}
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			admissionErr := ensureObjectActiveTx(tx, kind, id)
			_ = tx.Rollback()
			if !errors.Is(admissionErr, ErrObjectArchived) {
				t.Fatalf("archive admission changed with timezone: %v", admissionErr)
			}
			if kind == "script" {
				list, err := s.ListScripts("", nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range list {
					if item.ID == id && item.ArchivedAt == nil {
						t.Fatal("script list lost archive state")
					}
				}
			} else {
				list, err := s.ListScenarios()
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range list {
					if item.ID == id && item.ArchivedAt == nil {
						t.Fatal("scenario list lost archive state")
					}
				}
			}
			if changed, err := s.SetObjectArchived(kind, id, archivedVersion, false); err != nil || !changed {
				t.Fatalf("restore changed with timezone: %v %v", changed, err)
			}
			if _, err := s.db.Exec(`SET TIME ZONE 'America/New_York'`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetObjectArchived(kind, id, read(false), true); err != nil {
				t.Fatal(err)
			}
			if blocked, err := s.ObjectRestoredAfter(kind, id, plannedAt); err != nil || !blocked {
				t.Fatalf("timezone change lost restoration boundary: %v %v", blocked, err)
			}
		})
	}
}
