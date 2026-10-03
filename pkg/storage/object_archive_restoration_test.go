package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestArchiveRestorationScheduleBoundary(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkArchiveRestorationScheduleBoundary(t, s)
}

func checkArchiveRestorationScheduleBoundary(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, kind := range []string{"script", "scenario"} {
		id := "restoration-boundary-" + kind
		if kind == "script" {
			if err := s.SaveScript(&dsl.Script{ID: id, Name: "Source", Protocol: "http", Version: "1", Actions: []dsl.Action{}}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := s.SaveScenario(&Scenario{ID: id, Name: "Scenario", Duration: "1m"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		readVersion := func() time.Time {
			t.Helper()
			if kind == "script" {
				source, err := s.GetScript(id)
				if err != nil {
					t.Fatal(err)
				}
				return source.UpdatedAt
			}
			source, _, err := s.GetScenario(id)
			if err != nil {
				t.Fatal(err)
			}
			return source.UpdatedAt
		}
		plannedAt := time.Now().UTC()
		for _, archived := range []bool{true, false} {
			if _, err := s.SetObjectArchived(kind, id, readVersion(), archived); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			planned time.Time
			blocked bool
		}{{plannedAt, true}, {readVersion().Add(time.Second), false}} {
			blocked, err := s.ObjectRestoredAfter(kind, id, tc.planned.In(time.FixedZone("west", -4*60*60)))
			if err != nil || blocked != tc.blocked {
				t.Fatalf("%s planned %s boundary = %v %v", kind, tc.planned, blocked, err)
			}
		}
		if _, err := s.SetObjectArchived(kind, id, readVersion(), true); err != nil {
			t.Fatal(err)
		}
		if blocked, err := s.ObjectRestoredAfter(kind, id, plannedAt); err != nil || !blocked {
			t.Fatalf("%s rearchive lost restoration boundary: %v %v", kind, blocked, err)
		}
		if kind == "script" {
			if err := s.DeleteScript(id); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveScript(&dsl.Script{ID: id, Name: "New source", Protocol: "http", Version: "1", Actions: []dsl.Action{}}); err != nil {
				t.Fatal(err)
			}
			if blocked, err := s.ObjectRestoredAfter(kind, id, plannedAt); err != nil || blocked {
				t.Fatalf("old incarnation affected new source: %v %v", blocked, err)
			}
		}
	}
}

func TestArchiveRestorationBoundaryUpgradesExistingMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{ID: "existing", Name: "Existing", Protocol: "http", Version: "1", Actions: []dsl.Action{}}
	if err := s.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetObjectArchived("script", script.ID, script.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE object_archive_states DROP COLUMN restored_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=28`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stored, err := s.GetScript(script.ID)
	if err != nil || stored.ArchivedAt == nil {
		t.Fatalf("migration lost archive metadata: %#v %v", stored, err)
	}
	if _, err := s.SetObjectArchived("script", script.ID, stored.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.ObjectRestoredAfter("script", script.ID, script.CreatedAt); err != nil || !blocked {
		t.Fatalf("migrated restoration boundary: %v %v", blocked, err)
	}
}
