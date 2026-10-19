package storage

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestObjectActivitySummaryIndex(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "fresh"
		if upgrade {
			name = "upgrade"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "activity-index.db")
			s, err := NewSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			if upgrade {
				// Model an installation from before this index was introduced.
				if _, err := s.db.Exec(`DROP INDEX idx_object_activity_summary`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>=64`); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			for i, action := range []string{"create", "modify"} {
				if err := s.SaveObjectActivity(&ObjectActivityEvent{
					ID: action, ProjectID: DefaultProjectID, ResourceType: "script", ResourceID: "indexed-script",
					Action: action, Status: "success", ActorSubject: "user:alice", CreatedAt: now.Add(time.Duration(i) * time.Second),
				}); err != nil {
					t.Fatal(err)
				}
			}
			// Check the initial install/upgrade and an additional idempotent reopen.
			for pass := 0; pass < 2; pass++ {
				if upgrade || pass > 0 {
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					reopened, err := NewSQLiteStore(path)
					if err != nil {
						t.Fatal(err)
					}
					s = reopened
				}
				rows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_object_activity_summary') ORDER BY seqno`)
				if err != nil {
					t.Fatal(err)
				}
				var columns []string
				for rows.Next() {
					var column string
					if err := rows.Scan(&column); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					columns = append(columns, column)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"project_id", "resource_type", "resource_id", "action", "status", "created_at"}
				if !reflect.DeepEqual(columns, want) {
					t.Fatalf("summary index columns = %v, want %v", columns, want)
				}
				checkObjectActivitySummaryIndexPlan(t, s)
				summaries, err := s.ObjectActivitySummaries(DefaultProjectID, "script", []string{"indexed-script"})
				if err != nil {
					t.Fatal(err)
				}
				summary := summaries["indexed-script"]
				if summary.Creator == nil || summary.Creator.ID != "create" || summary.LastModifier == nil || summary.LastModifier.ID != "modify" {
					t.Fatalf("summary history changed: %#v", summary)
				}
				status, err := s.SchemaStatus()
				if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 {
					t.Fatalf("schema status = %#v, %v", status, err)
				}
			}
		})
	}
}

func checkObjectActivitySummaryIndexPlan(t *testing.T, s *SQLiteStore) {
	t.Helper()
	// Exercise the correlated incarnation boundary used by ObjectActivitySummaries.
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT payload FROM object_activity_events
		WHERE project_id=? AND resource_type=? AND resource_id=?
		AND created_at>=COALESCE((SELECT MAX(c.created_at) FROM object_activity_events c
		WHERE c.project_id=object_activity_events.project_id AND c.resource_type=object_activity_events.resource_type
		AND c.resource_id=object_activity_events.resource_id AND c.action='create' AND c.status='success'),created_at)`, DefaultProjectID, "script", "indexed-script")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
		indexed = indexed || strings.Contains(detail, "SEARCH c USING COVERING INDEX idx_object_activity_summary")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatalf("latest successful creation lookup did not use covering index: %v", details)
	}
}
