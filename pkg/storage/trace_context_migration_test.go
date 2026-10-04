package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTestTraceContextRoundTripsAndUpgradesFromV29(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main-v29.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	requireProjectFixtures(t, store, "trace-p")
	now := time.Now().UTC()
	policy := map[string]any{"enabled": true, "origins": []any{"https://checkout.example.test"}}
	if err := store.SaveTest(&TestRecord{ID: "trace-run", ProjectID: "trace-p", Name: "run", Status: "QUEUED", StartTime: now, TraceContext: policy}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: "plain-run", ProjectID: "trace-p", Name: "run", Status: "QUEUED", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetTest("trace-run")
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := loaded.TraceContext.(map[string]any)
	if !ok || decoded["enabled"] != true {
		t.Fatalf("trace context = %#v, want the saved policy", loaded.TraceContext)
	}

	// A database published at v29 has neither the column nor the release.
	for _, statement := range []string{
		`ALTER TABLE tests DROP COLUMN trace_context_json`,
		`DELETE FROM schema_migrations WHERE version>=30`,
	} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 {
		t.Fatalf("upgraded schema = %+v, %v", status, err)
	}
	upgraded, err := store.GetTest("plain-run")
	if err != nil || upgraded.TraceContext != nil {
		t.Fatalf("upgraded row trace context = %#v, %v; want none", upgraded, err)
	}
}
