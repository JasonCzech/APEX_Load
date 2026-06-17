package storage

import (
	"path/filepath"
	"testing"
)

func TestObjectRepositoryRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	// Absent repo returns sql.ErrNoRows from Get.
	if _, err := store.GetObjectRepository("missing"); err == nil {
		t.Fatal("expected error for absent repository")
	}

	repo := &ObjectRepository{
		ProjectID: "proj-1",
		Name:      "Checkout objects",
		Source:    "script:s1",
		Objects: []RepositoryObject{
			{LogicalName: "search-box", CSS: "[data-testid=search]", BestConfidence: 95,
				Candidates: []RepositoryObjectCandidate{{Kind: "css", Value: "[data-testid=search]", Confidence: 95}}},
			{LogicalName: "submit", Role: "button", Attributes: map[string]string{"aria-label": "Go"}},
		},
	}
	repo.ID = "object-repo-1"
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("SaveObjectRepository: %v", err)
	}
	if repo.CreatedAt.IsZero() || repo.UpdatedAt.IsZero() {
		t.Error("timestamps not stamped")
	}

	loaded, err := store.GetObjectRepository("object-repo-1")
	if err != nil {
		t.Fatalf("GetObjectRepository: %v", err)
	}
	if loaded == nil || len(loaded.Objects) != 2 {
		t.Fatalf("expected 2 objects, got %#v", loaded)
	}
	if loaded.Objects[0].LogicalName != "search-box" || loaded.Objects[0].Candidates[0].Confidence != 95 {
		t.Errorf("object not round-tripped: %#v", loaded.Objects[0])
	}
	if loaded.Objects[1].Attributes["aria-label"] != "Go" {
		t.Errorf("attributes not round-tripped: %#v", loaded.Objects[1])
	}

	// List is project-scoped.
	repos, err := store.ListObjectRepositories("proj-1", 100, 0)
	if err != nil || len(repos) != 1 {
		t.Fatalf("ListObjectRepositories proj-1 = %d (%v)", len(repos), err)
	}
	if other, _ := store.ListObjectRepositories("proj-2", 100, 0); len(other) != 0 {
		t.Errorf("expected no repos for proj-2, got %d", len(other))
	}

	// Upsert replaces objects.
	repo.Objects = repo.Objects[:1]
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	loaded, _ = store.GetObjectRepository("object-repo-1")
	if len(loaded.Objects) != 1 {
		t.Errorf("upsert did not replace objects, got %d", len(loaded.Objects))
	}

	if err := store.DeleteObjectRepository("object-repo-1"); err != nil {
		t.Fatalf("DeleteObjectRepository: %v", err)
	}
	if repos, _ := store.ListObjectRepositories("proj-1", 100, 0); len(repos) != 0 {
		t.Errorf("expected 0 repos after delete, got %d", len(repos))
	}
}
