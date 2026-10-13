package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestScenarioDraftLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"drafts-a", "drafts-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}

	draft := &ScenarioDraft{ID: "draft-1", ProjectID: "drafts-a", Name: "Checkout peak", Document: `{"version":3}`, CreatedBy: "Ada"}
	if err := store.CreateScenarioDraft(draft); err != nil {
		t.Fatalf("CreateScenarioDraft: %v", err)
	}
	if draft.Revision != 1 || draft.UpdatedBy != "Ada" {
		t.Fatalf("created draft = %+v", draft)
	}
	if err := store.CreateScenarioDraft(&ScenarioDraft{ID: "draft-x", ProjectID: "missing", Name: "x", Document: "{}"}); !errors.Is(err, ErrScenarioDraftProjectNotFound) {
		t.Fatalf("a draft in a missing project = %v", err)
	}

	// Another project never sees or changes it.
	if _, err := store.GetScenarioDraft("drafts-b", "draft-1"); !errors.Is(err, ErrScenarioDraftNotFound) {
		t.Fatalf("other project read = %v", err)
	}
	if listed, err := store.ListScenarioDrafts("drafts-b"); err != nil || len(listed) != 0 {
		t.Fatalf("other project list = %v, %v", listed, err)
	}

	// An update needs the revision it was read at.
	update := &ScenarioDraft{ID: "draft-1", ProjectID: "drafts-a", ScenarioID: "scenario-9", Name: "Checkout peak hour", Document: `{"version":3,"step":"run"}`, UpdatedBy: "Grace"}
	if ok, err := store.UpdateScenarioDraftIfRevision(update, 1); err != nil || !ok {
		t.Fatalf("update at revision 1 = %v, %v", ok, err)
	}
	if update.Revision != 2 || update.CreatedBy != "Ada" || update.UpdatedBy != "Grace" || update.ScenarioID != "scenario-9" {
		t.Fatalf("updated draft = %+v", update)
	}
	stale := &ScenarioDraft{ID: "draft-1", ProjectID: "drafts-a", Name: "Lost", Document: "{}"}
	if ok, err := store.UpdateScenarioDraftIfRevision(stale, 1); err != nil || ok {
		t.Fatalf("stale update = %v, %v", ok, err)
	}
	if ok, err := store.UpdateScenarioDraftIfRevision(&ScenarioDraft{ID: "draft-1", ProjectID: "drafts-b", Name: "x", Document: "{}"}, 2); err != nil || ok {
		t.Fatalf("other project update = %v, %v", ok, err)
	}

	read, err := store.GetScenarioDraft("drafts-a", "draft-1")
	if err != nil || read.Document != `{"version":3,"step":"run"}` || read.Name != "Checkout peak hour" {
		t.Fatalf("read = %+v, %v", read, err)
	}
	listed, err := store.ListScenarioDrafts("drafts-a")
	if err != nil || len(listed) != 1 || listed[0].Document != "" || listed[0].Revision != 2 {
		t.Fatalf("list = %+v, %v", listed, err)
	}

	if deleted, err := store.DeleteScenarioDraft("drafts-b", "draft-1", 0); err != nil || deleted {
		t.Fatalf("other project delete = %v, %v", deleted, err)
	}
	// A delete at an older revision keeps the work saved since.
	if deleted, err := store.DeleteScenarioDraft("drafts-a", "draft-1", 1); err != nil || deleted {
		t.Fatalf("stale delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteScenarioDraft("drafts-a", "draft-1", 2); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}

	// Deleting the project removes its drafts.
	if err := store.CreateScenarioDraft(&ScenarioDraft{ID: "draft-2", ProjectID: "drafts-b", Name: "Kept until the project goes", Document: "{}"}); err != nil {
		t.Fatalf("CreateScenarioDraft(b): %v", err)
	}
	if err := store.DeleteProject("drafts-b"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scenario_drafts WHERE id = ?`, "draft-2").Scan(&count); err != nil || count != 0 {
		t.Fatalf("drafts left after project deletion = %d, %v", count, err)
	}
}
