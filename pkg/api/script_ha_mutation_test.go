package api

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestStaleReplicaScriptMutationsCannotTouchCrossProjectReplacement(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	seedScriptMutationProjects(t, store)

	owner := NewTestRunner(2, NewHub(), store)
	original, err := owner.StoreScriptDurable(&dsl.Script{
		ID: "reused-script-id", Name: "Original", Protocol: "http", ProjectID: "project-a",
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable(original): %v", err)
	}
	stale := NewTestRunner(2, NewHub(), store)
	if cached, ok := stale.GetScript(original.ID); !ok || cached.ProjectID != "project-a" {
		t.Fatalf("stale replica did not cache original: %#v", cached)
	}

	if deleted, err := owner.DeleteScriptDurableInProject(original.ID, "project-a"); err != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject(original) = %v, %v", deleted, err)
	}
	replacement, err := owner.StoreScriptDurable(&dsl.Script{
		ID: original.ID, Name: "Replacement", Protocol: "http", ProjectID: "project-b",
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable(replacement): %v", err)
	}

	if updated, ok, err := stale.UpdateScriptDurableInProject(original.ID, &dsl.Script{Name: "Forged", Protocol: "http"}, "project-a"); !errors.Is(err, errScriptConcurrentChange) || ok || updated != nil {
		t.Fatalf("stale update = %#v, %v, %v; want ownership conflict", updated, ok, err)
	}
	if moved, err := stale.MoveScriptDurableInProject(original.ID, ptrString("foreign-folder"), "project-a"); !errors.Is(err, errScriptConcurrentChange) || moved {
		t.Fatalf("stale move = %v, %v; want ownership conflict", moved, err)
	}
	if deleted, err := stale.DeleteScriptDurableInProject(original.ID, "project-a"); !errors.Is(err, errScriptConcurrentChange) || deleted {
		t.Fatalf("stale delete = %v, %v; want ownership conflict", deleted, err)
	}

	durable, err := store.GetScript(original.ID)
	if err != nil {
		t.Fatalf("GetScript(replacement): %v", err)
	}
	if durable.ProjectID != replacement.ProjectID || durable.Name != replacement.Name || durable.FolderID != nil {
		t.Fatalf("replacement changed by stale replica: %#v", durable)
	}
}

func TestStoreScriptDurableRejectsStaleCrossProjectReplica(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	seedScriptMutationProjects(t, store)

	owner := NewTestRunner(2, NewHub(), store)
	original, err := owner.StoreScriptDurable(&dsl.Script{
		ID: "store-reused-id", Name: "Original", Protocol: "http", ProjectID: "project-a",
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable(original): %v", err)
	}
	stale := NewTestRunner(2, NewHub(), store)
	if deleted, err := owner.DeleteScriptDurableInProject(original.ID, "project-a"); err != nil || !deleted {
		t.Fatalf("delete original = %v, %v", deleted, err)
	}
	if _, err := owner.StoreScriptDurable(&dsl.Script{ID: original.ID, Name: "Replacement", Protocol: "http", ProjectID: "project-b"}); err != nil {
		t.Fatalf("store replacement: %v", err)
	}

	_, err = stale.StoreScriptDurable(&dsl.Script{ID: original.ID, Name: "Stale overwrite", Protocol: "http", ProjectID: "project-a"})
	if !errors.Is(err, errScriptConcurrentChange) {
		t.Fatalf("stale StoreScriptDurable error = %v, want concurrent change", err)
	}
	durable, err := store.GetScript(original.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if durable.ProjectID != "project-b" || durable.Name != "Replacement" {
		t.Fatalf("stale store overwrote replacement: %#v", durable)
	}
}

func seedScriptMutationProjects(t *testing.T, store *storage.SQLiteStore) {
	t.Helper()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatalf("SaveProject(%s): %v", id, err)
		}
	}
}
