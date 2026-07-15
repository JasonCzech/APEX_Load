package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newReusableBlockTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestReusableBlockCRUD(t *testing.T) {
	store := newReusableBlockTestStore(t)
	for _, id := range []string{"proj-a", "proj-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatalf("SaveProject(%s): %v", id, err)
		}
	}

	block := &ReusableBlock{
		ID:        "block-1",
		ProjectID: "proj-a",
		Name:      "Login Flow",
		Kind:      "auth_flow",
		Component: `{"id":"block-1","name":"Login Flow","kind":"auth_flow"}`,
	}
	if err := store.SaveReusableBlock(block); err != nil {
		t.Fatalf("SaveReusableBlock: %v", err)
	}
	if block.CreatedAt.IsZero() || block.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set: %+v", block)
	}

	got, err := store.GetReusableBlock("block-1")
	if err != nil {
		t.Fatalf("GetReusableBlock: %v", err)
	}
	if got.Name != "Login Flow" || got.Kind != "auth_flow" || got.Component == "" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Project scoping: a block in another project must not leak into proj-a's list.
	if err := store.SaveReusableBlock(&ReusableBlock{ID: "block-2", ProjectID: "proj-b", Name: "Other", Kind: "teardown", Component: "{}"}); err != nil {
		t.Fatalf("SaveReusableBlock(proj-b): %v", err)
	}
	listA, err := store.ListReusableBlocks("proj-a")
	if err != nil {
		t.Fatalf("ListReusableBlocks: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != "block-1" {
		t.Errorf("ListReusableBlocks(proj-a) = %+v, want only block-1", listA)
	}

	if err := store.DeleteReusableBlock("block-1"); err != nil {
		t.Fatalf("DeleteReusableBlock: %v", err)
	}
	listA, err = store.ListReusableBlocks("proj-a")
	if err != nil {
		t.Fatalf("ListReusableBlocks after delete: %v", err)
	}
	if len(listA) != 0 {
		t.Errorf("expected proj-a empty after delete, got %+v", listA)
	}
}

func TestReusableBlockRejectsMissingProjectAndOwnershipRelocation(t *testing.T) {
	store := newReusableBlockTestStore(t)
	if err := store.SaveReusableBlock(&ReusableBlock{ID: "missing-project-block", ProjectID: "missing-project", Name: "bad", Component: "{}"}); err == nil {
		t.Fatal("SaveReusableBlock accepted a missing project")
	}
	for _, id := range []string{"block-project-a", "block-project-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id}); err != nil {
			t.Fatalf("SaveProject(%s): %v", id, err)
		}
	}
	block := &ReusableBlock{ID: "owned-block", ProjectID: "block-project-a", Name: "owned", Component: "{}"}
	if err := store.SaveReusableBlock(block); err != nil {
		t.Fatalf("initial SaveReusableBlock: %v", err)
	}
	block.ProjectID = "block-project-b"
	if err := store.SaveReusableBlock(block); err == nil {
		t.Fatal("SaveReusableBlock relocated project ownership")
	}
	persisted, err := store.GetReusableBlock(block.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ProjectID != "block-project-a" {
		t.Fatalf("persisted project = %q, want block-project-a", persisted.ProjectID)
	}
}
