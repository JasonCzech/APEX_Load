package storage

import (
	"path/filepath"
	"testing"
	"time"
)

// exerciseDeletedTestIdentities checks that deleting a test, alone or with its
// project, keeps the project its ID belonged to, and that a same-project
// re-creation deleted again refreshes the record.
func exerciseDeletedTestIdentities(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	for _, projectID := range []string{"tombstone-a", "tombstone-b"} {
		if err := s.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	save := func(id, projectID string) {
		t.Helper()
		if err := s.SaveTest(&TestRecord{ID: id, Name: id, ProjectID: projectID, Status: "COMPLETE", StartTime: now}); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(id, wantProject string, wantDeleted bool) {
		t.Helper()
		projectID, deleted, err := s.DeletedTestProject(id)
		if err != nil || deleted != wantDeleted || projectID != wantProject {
			t.Fatalf("%s: deleted=%v project=%q err=%v, want %v %q", id, deleted, projectID, err, wantDeleted, wantProject)
		}
	}
	save("tombstone-single", "tombstone-a")
	expect("tombstone-single", "", false)
	if err := s.DeleteTest("tombstone-single"); err != nil {
		t.Fatal(err)
	}
	expect("tombstone-single", "tombstone-a", true)
	// Deleting an ID that was never stored records nothing.
	if err := s.DeleteTest("tombstone-never-stored"); err != nil {
		t.Fatal(err)
	}
	expect("tombstone-never-stored", "", false)
	// A same-project re-creation deleted again keeps one refreshed record.
	save("tombstone-single", "tombstone-a")
	if err := s.DeleteTest("tombstone-single"); err != nil {
		t.Fatal(err)
	}
	expect("tombstone-single", "tombstone-a", true)
	save("tombstone-project-run", "tombstone-b")
	if err := s.DeleteProject("tombstone-b"); err != nil {
		t.Fatal(err)
	}
	expect("tombstone-project-run", "tombstone-b", true)
}

func TestDeletedTestIdentities(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "tombstones.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exerciseDeletedTestIdentities(t, s)
}
