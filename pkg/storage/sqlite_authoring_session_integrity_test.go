package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAuthoringSessionProjectOwnershipAndPagination(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"authoring-project-a", "authoring-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	for i, id := range []string{"authoring-session-1", "authoring-session-2", "authoring-session-3"} {
		session := &AuthoringSession{
			ID: id, ProjectID: "authoring-project-a", Source: "manual", Name: id, Status: "draft",
			Draft: &dsl.Script{ID: "draft-" + id, ProjectID: "authoring-project-a", Name: id, Protocol: "http"},
		}
		if err := store.SaveAuthoringSession(session); err != nil {
			t.Fatalf("SaveAuthoringSession(%s): %v", id, err)
		}
		if i < 2 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	page, err := store.ListAuthoringSessionsPage("authoring-project-a", 1, 1)
	if err != nil {
		t.Fatalf("ListAuthoringSessionsPage: %v", err)
	}
	if len(page) != 1 || page[0].ID != "authoring-session-2" {
		t.Fatalf("page = %#v, want authoring-session-2", page)
	}

	owned, err := store.GetAuthoringSession("authoring-session-1")
	if err != nil {
		t.Fatalf("GetAuthoringSession: %v", err)
	}
	forged := *owned
	forged.ProjectID = "authoring-project-b"
	forged.Draft = &dsl.Script{ID: "draft-forged", ProjectID: "authoring-project-b", Name: "forged", Protocol: "http"}
	if err := store.SaveAuthoringSession(&forged); err == nil {
		t.Fatal("SaveAuthoringSession allowed project ownership rewrite")
	}
	if updated, err := store.CompareAndSwapAuthoringSession(&forged, owned.UpdatedAt, owned.Status); err != nil || updated {
		t.Fatalf("cross-project CAS = (%v, %v), want false, nil", updated, err)
	}
	mismatchedDraft := *owned
	mismatchedDraft.Draft = &dsl.Script{ID: "draft-mismatch", ProjectID: "authoring-project-b", Name: "mismatch", Protocol: "http"}
	if err := store.SaveAuthoringSession(&mismatchedDraft); err == nil {
		t.Fatal("SaveAuthoringSession accepted a foreign-project draft")
	}
	got, err := store.GetAuthoringSession(owned.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession after rejected writes: %v", err)
	}
	if got.ProjectID != "authoring-project-a" || got.Name != owned.Name {
		t.Fatalf("rejected writes changed session: %#v", got)
	}
}
