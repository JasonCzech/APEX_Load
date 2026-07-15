package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newProjectEntityCASStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, project := range []*Project{
		{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now},
		{ID: "project-b", Name: "B", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject %s: %v", project.ID, err)
		}
	}
	return store
}

func TestApplicationOptimisticUpdateAndImmutableProject(t *testing.T) {
	store := newProjectEntityCASStore(t)
	application := &Application{ID: "application-cas", ProjectID: "project-a", Name: "original"}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	first, err := store.GetApplication(application.ID)
	if err != nil {
		t.Fatalf("GetApplication first: %v", err)
	}
	stale, err := store.GetApplication(application.ID)
	if err != nil {
		t.Fatalf("GetApplication stale: %v", err)
	}
	first.Name = "first writer"
	if updated, err := store.UpdateApplicationIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateApplicationIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	if err := store.SaveApplication(&Application{ID: application.ID, ProjectID: "project-b", Name: "legacy update"}); err != nil {
		t.Fatalf("SaveApplication legacy update: %v", err)
	}
	fresh, err := store.GetApplication(application.ID)
	if err != nil || fresh.ProjectID != "project-a" {
		t.Fatalf("application ownership after update = %#v, err=%v", fresh, err)
	}
}

func TestReleaseOptimisticUpdateAndImmutablePlacement(t *testing.T) {
	store := newProjectEntityCASStore(t)
	for _, application := range []*Application{
		{ID: "application-a", ProjectID: "project-a", Name: "A"},
		{ID: "application-b", ProjectID: "project-b", Name: "B"},
	} {
		if err := store.SaveApplication(application); err != nil {
			t.Fatalf("SaveApplication %s: %v", application.ID, err)
		}
	}
	release := &Release{ID: "release-cas", ApplicationID: "application-a", ProjectID: "project-a", Name: "original"}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	first, err := store.GetRelease(release.ID)
	if err != nil {
		t.Fatalf("GetRelease first: %v", err)
	}
	stale, err := store.GetRelease(release.ID)
	if err != nil {
		t.Fatalf("GetRelease stale: %v", err)
	}
	first.Name = "first writer"
	if updated, err := store.UpdateReleaseIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateReleaseIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	if err := store.SaveRelease(&Release{ID: release.ID, ApplicationID: "application-b", ProjectID: "project-b", Name: "legacy update"}); err != nil {
		t.Fatalf("SaveRelease legacy update: %v", err)
	}
	fresh, err := store.GetRelease(release.ID)
	if err != nil || fresh.ApplicationID != "application-a" || fresh.ProjectID != "project-a" {
		t.Fatalf("release placement after update = %#v, err=%v", fresh, err)
	}
	if err := store.SaveRelease(&Release{ID: "release-mismatch", ApplicationID: "application-a", ProjectID: "project-b", Name: "mismatch"}); err == nil {
		t.Fatal("SaveRelease accepted a cross-project application relationship")
	}
}

func TestProjectOptimisticUpdateRejectsStaleSettings(t *testing.T) {
	store := newProjectEntityCASStore(t)
	first, err := store.GetProject("project-a")
	if err != nil {
		t.Fatalf("GetProject first: %v", err)
	}
	stale, err := store.GetProject("project-a")
	if err != nil {
		t.Fatalf("GetProject stale: %v", err)
	}
	first.Settings.MaxVUsers = 100
	if updated, err := store.UpdateProjectIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Settings.MaxVUsers = 1
	if updated, err := store.UpdateProjectIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetProject("project-a")
	if err != nil || fresh.Settings.MaxVUsers != 100 {
		t.Fatalf("project settings after stale update = %#v, err=%v", fresh, err)
	}
}

func TestFolderOptimisticUpdateRejectsStaleMove(t *testing.T) {
	store := newProjectEntityCASStore(t)
	if err := store.SaveFolder(&Folder{ID: "folder-parent", ProjectID: "project-a", Name: "parent", Category: "general"}); err != nil {
		t.Fatalf("SaveFolder parent: %v", err)
	}
	if err := store.SaveFolder(&Folder{ID: "folder-cas", ProjectID: "project-a", Name: "original", Category: "general"}); err != nil {
		t.Fatalf("SaveFolder child: %v", err)
	}
	first, err := store.GetFolder("folder-cas")
	if err != nil {
		t.Fatalf("GetFolder first: %v", err)
	}
	stale, err := store.GetFolder("folder-cas")
	if err != nil {
		t.Fatalf("GetFolder stale: %v", err)
	}
	parentID := "folder-parent"
	first.ParentID = &parentID
	if updated, err := store.UpdateFolderIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateFolderIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetFolder("folder-cas")
	if err != nil || fresh.ParentID == nil || *fresh.ParentID != parentID || fresh.Name != "original" {
		t.Fatalf("folder after stale update = %#v, err=%v", fresh, err)
	}
}
