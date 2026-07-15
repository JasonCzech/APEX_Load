package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteFolderHierarchyCannotCrossProjects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, project := range []*Project{
		{ID: "folder-project-a", Name: "A", CreatedAt: now},
		{ID: "folder-project-b", Name: "B", CreatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject(%s): %v", project.ID, err)
		}
	}
	parentA := &Folder{ID: "parent-a", ProjectID: "folder-project-a", Name: "A", Category: "general", CreatedAt: now}
	parentB := &Folder{ID: "parent-b", ProjectID: "folder-project-b", Name: "B", Category: "general", CreatedAt: now}
	for _, folder := range []*Folder{parentA, parentB} {
		if err := store.SaveFolder(folder); err != nil {
			t.Fatalf("SaveFolder(%s): %v", folder.ID, err)
		}
	}

	foreignParentID := parentB.ID
	child := &Folder{ID: "child-a", ProjectID: "folder-project-a", ParentID: &foreignParentID, Name: "Child", Category: "general", CreatedAt: now}
	if err := store.SaveFolder(child); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("cross-project SaveFolder error = %v", err)
	}
	if _, err := store.GetFolder(child.ID); err == nil {
		t.Fatal("rejected cross-project child was persisted")
	}

	parentAID := parentA.ID
	child.ParentID = &parentAID
	if err := store.SaveFolder(child); err != nil {
		t.Fatalf("SaveFolder(valid child): %v", err)
	}
	if err := store.MoveFolder(child.ID, &foreignParentID); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("cross-project MoveFolder error = %v", err)
	}
	got, err := store.GetFolder(child.ID)
	if err != nil {
		t.Fatalf("GetFolder(child): %v", err)
	}
	if got.ParentID == nil || *got.ParentID != parentA.ID {
		t.Fatalf("failed move changed parent to %#v", got.ParentID)
	}

	got.ProjectID = "folder-project-b"
	if err := store.SaveFolder(got); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("ownership-changing SaveFolder error = %v", err)
	}
	got, err = store.GetFolder(child.ID)
	if err != nil {
		t.Fatalf("GetFolder(child after ownership attempt): %v", err)
	}
	if got.ProjectID != "folder-project-a" {
		t.Fatalf("folder project changed to %q", got.ProjectID)
	}
}
