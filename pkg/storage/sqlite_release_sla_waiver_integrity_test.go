package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newReleaseSLAWaiverIntegrityStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "release-sla-waiver.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, projectID := range []string{"waiver-project-a", "waiver-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
		applicationID := "application-" + projectID
		if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: projectID, Name: applicationID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication(%s): %v", applicationID, err)
		}
		for index := 1; index <= 2; index++ {
			releaseID := projectID + "-release-" + string(rune('0'+index))
			if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: applicationID, ProjectID: projectID, Name: releaseID, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("SaveRelease(%s): %v", releaseID, err)
			}
		}
	}
	return store
}

func TestSaveReleaseSLAWaiverValidatesAndPreservesOwnership(t *testing.T) {
	store := newReleaseSLAWaiverIntegrityStore(t)
	now := time.Now().UTC().Add(-time.Minute)
	waiver := &ReleaseSLAWaiver{
		ID: "waiver-fixed-id", ReleaseID: "waiver-project-a-release-1", ProjectID: "waiver-project-a",
		Scope: "release", Reason: "initial reason", ApprovedBy: "approver", Status: "active", CreatedAt: now,
	}
	if err := store.SaveReleaseSLAWaiver(waiver); err != nil {
		t.Fatalf("SaveReleaseSLAWaiver(create): %v", err)
	}
	createdAt := waiver.CreatedAt
	waiver.Reason = "updated reason"
	if err := store.SaveReleaseSLAWaiver(waiver); err != nil {
		t.Fatalf("SaveReleaseSLAWaiver(update): %v", err)
	}

	for _, tc := range []struct {
		name      string
		releaseID string
		projectID string
	}{
		{name: "same project different release", releaseID: "waiver-project-a-release-2", projectID: "waiver-project-a"},
		{name: "different project", releaseID: "waiver-project-b-release-1", projectID: "waiver-project-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			moved := *waiver
			moved.ReleaseID = tc.releaseID
			moved.ProjectID = tc.projectID
			moved.Reason = "relocated"
			if err := store.SaveReleaseSLAWaiver(&moved); err == nil || !strings.Contains(err.Error(), "already belongs") {
				t.Fatalf("ownership relocation error = %v, want immutable-owner rejection", err)
			}
		})
	}

	persisted, err := store.ListReleaseSLAWaivers(waiver.ReleaseID)
	if err != nil {
		t.Fatalf("ListReleaseSLAWaivers: %v", err)
	}
	if len(persisted) != 1 || persisted[0].Reason != "updated reason" {
		t.Fatalf("persisted waiver changed after rejected relocation: %+v", persisted)
	}
	if !persisted[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at changed on update: got %v want %v", persisted[0].CreatedAt, createdAt)
	}
}

func TestSaveReleaseSLAWaiverRejectsInvalidOrArchivedParent(t *testing.T) {
	store := newReleaseSLAWaiverIntegrityStore(t)
	base := ReleaseSLAWaiver{
		ID: "waiver-parent-check", ReleaseID: "waiver-project-a-release-1", ProjectID: "waiver-project-a",
		Scope: "release", Reason: "risk accepted", Status: "active",
	}

	missing := base
	missing.ID = "waiver-missing"
	missing.ReleaseID = "missing-release"
	if err := store.SaveReleaseSLAWaiver(&missing); err == nil || !strings.Contains(err.Error(), "release not found") {
		t.Fatalf("missing release error = %v", err)
	}

	mismatched := base
	mismatched.ID = "waiver-mismatch"
	mismatched.ProjectID = "waiver-project-b"
	if err := store.SaveReleaseSLAWaiver(&mismatched); err == nil || !strings.Contains(err.Error(), "does not match release project") {
		t.Fatalf("project mismatch error = %v", err)
	}

	if _, err := store.db.Exec(`UPDATE releases SET project_id = ? WHERE id = ?`, "waiver-project-b", "waiver-project-a-release-2"); err != nil {
		t.Fatalf("corrupt release ownership fixture: %v", err)
	}
	corrupt := base
	corrupt.ID = "waiver-corrupt-parent"
	corrupt.ReleaseID = "waiver-project-a-release-2"
	corrupt.ProjectID = "waiver-project-b"
	if err := store.SaveReleaseSLAWaiver(&corrupt); err == nil || !strings.Contains(err.Error(), "does not match application project") {
		t.Fatalf("corrupt parent error = %v", err)
	}

	if err := store.ArchiveRelease(base.ReleaseID); err != nil {
		t.Fatalf("ArchiveRelease: %v", err)
	}
	archived := base
	archived.ID = "waiver-archived"
	if err := store.SaveReleaseSLAWaiver(&archived); err == nil || !strings.Contains(err.Error(), "archived release") {
		t.Fatalf("archived release error = %v", err)
	}
}
