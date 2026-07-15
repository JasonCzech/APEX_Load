package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestUpdateScriptIfUnchangedRejectsStaleWriter(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scripts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	script := &dsl.Script{ID: "script", Name: "initial", Protocol: "http", ProjectID: DefaultProjectID, RefKey: "DEV-SCR-1-v1", CreatedAt: time.Now().UTC()}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	observed, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := *observed
	first.Name = "first"
	updated, err := store.UpdateScriptIfUnchanged(&first, observed.UpdatedAt)
	if err != nil || !updated {
		t.Fatalf("first conditional update = %t, %v", updated, err)
	}
	stale := *observed
	stale.Name = "stale"
	updated, err = store.UpdateScriptIfUnchanged(&stale, observed.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale conditional update unexpectedly succeeded")
	}
	current, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != "first" {
		t.Fatalf("current name = %q, want first", current.Name)
	}
}
