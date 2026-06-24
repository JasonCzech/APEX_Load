package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteTestNotesRoundTripAndOrdering(t *testing.T) {
	store := newTestNotesStore(t)
	saveTestNotesRun(t, store, "notes-run", DefaultProjectID)

	first := &TestNote{ID: "note-1", TestID: "notes-run", Body: "first note", CreatedBy: "operator"}
	if err := store.CreateTestNote(first); err != nil {
		t.Fatalf("CreateTestNote first returned error: %v", err)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Fatalf("create did not stamp timestamps: %#v", first)
	}
	if !first.CreatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("created_at = %s, updated_at = %s, want equal on create", first.CreatedAt, first.UpdatedAt)
	}

	time.Sleep(2 * time.Millisecond)
	second := &TestNote{ID: "note-2", TestID: "notes-run", Body: "# second note"}
	if err := store.CreateTestNote(second); err != nil {
		t.Fatalf("CreateTestNote second returned error: %v", err)
	}

	notes, err := store.ListTestNotes("notes-run")
	if err != nil {
		t.Fatalf("ListTestNotes returned error: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(notes))
	}
	if notes[0].ID != "note-2" || notes[1].ID != "note-1" {
		t.Fatalf("note order = %s, %s; want updated_at desc", notes[0].ID, notes[1].ID)
	}

	got, err := store.GetTestNote("note-1")
	if err != nil {
		t.Fatalf("GetTestNote returned error: %v", err)
	}
	if got.Body != "first note" || got.CreatedBy != "operator" {
		t.Fatalf("round trip note = %#v", got)
	}

	createdAt := got.CreatedAt
	time.Sleep(2 * time.Millisecond)
	got.Body = "first note edited"
	got.UpdatedBy = "reviewer"
	if err := store.UpdateTestNote(got); err != nil {
		t.Fatalf("UpdateTestNote returned error: %v", err)
	}
	updated, err := store.GetTestNote("note-1")
	if err != nil {
		t.Fatalf("GetTestNote updated returned error: %v", err)
	}
	if !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at changed from %s to %s", createdAt, updated.CreatedAt)
	}
	if !updated.UpdatedAt.After(createdAt) {
		t.Fatalf("updated_at = %s, want after %s", updated.UpdatedAt, createdAt)
	}
	if updated.UpdatedBy != "reviewer" || updated.Body != "first note edited" {
		t.Fatalf("updated note = %#v", updated)
	}

	if err := store.DeleteTestNote("note-2"); err != nil {
		t.Fatalf("DeleteTestNote returned error: %v", err)
	}
	notes, err = store.ListTestNotes("notes-run")
	if err != nil {
		t.Fatalf("ListTestNotes after delete returned error: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != "note-1" {
		t.Fatalf("remaining notes = %#v", notes)
	}
}

func TestSQLiteTestNotesUnknownUpdateDelete(t *testing.T) {
	store := newTestNotesStore(t)
	if err := store.UpdateTestNote(&TestNote{ID: "missing", Body: "nope"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateTestNote missing err = %v, want sql.ErrNoRows", err)
	}
	if err := store.DeleteTestNote("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteTestNote missing err = %v, want sql.ErrNoRows", err)
	}
}

func TestSQLiteTestNotesCleanupWithTestAndProject(t *testing.T) {
	store := newTestNotesStore(t)
	project := &Project{ID: "notes-project", Name: "Notes Project"}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	saveTestNotesRun(t, store, "delete-test-run", DefaultProjectID)
	saveTestNotesRun(t, store, "delete-project-run", project.ID)

	if err := store.CreateTestNote(&TestNote{ID: "delete-test-note", TestID: "delete-test-run", Body: "remove with test"}); err != nil {
		t.Fatalf("CreateTestNote delete-test returned error: %v", err)
	}
	if err := store.CreateTestNote(&TestNote{ID: "delete-project-note", TestID: "delete-project-run", Body: "remove with project"}); err != nil {
		t.Fatalf("CreateTestNote delete-project returned error: %v", err)
	}

	if err := store.DeleteTest("delete-test-run"); err != nil {
		t.Fatalf("DeleteTest returned error: %v", err)
	}
	if notes, err := store.ListTestNotes("delete-test-run"); err != nil || len(notes) != 0 {
		t.Fatalf("notes after DeleteTest = %#v, err=%v; want empty", notes, err)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatalf("DeleteProject returned error: %v", err)
	}
	if notes, err := store.ListTestNotes("delete-project-run"); err != nil || len(notes) != 0 {
		t.Fatalf("notes after DeleteProject = %#v, err=%v; want empty", notes, err)
	}
}

func newTestNotesStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func saveTestNotesRun(t *testing.T, store *SQLiteStore, id string, projectID string) {
	t.Helper()
	if err := store.SaveTest(&TestRecord{
		ID:        id,
		Name:      id,
		Status:    "COMPLETE",
		ProjectID: projectID,
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest %s returned error: %v", id, err)
	}
}
