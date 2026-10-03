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

	"github.com/jasonczech/apexload/pkg/dsl"
)

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
