package storage

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBusinessKeyProjectKeyIsCreatedOnceAndDeletedWithItsProject(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &Project{ID: "keys", Name: "Keys", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	// Concurrent first callers all get the one stored key.
	keys := make([]string, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i], _ = store.BusinessKeyProjectKey(project.ID, func() (string, error) { return "wrapped-" + string(rune('a'+i)), nil })
		}(i)
	}
	wg.Wait()
	for _, key := range keys {
		if key == "" || key != keys[0] {
			t.Fatalf("keys differ: %v", keys)
		}
	}
	if again, err := store.BusinessKeyProjectKey(project.ID, func() (string, error) { return "", errors.New("must not create") }); err != nil || again != keys[0] {
		t.Fatalf("reread: %q %v", again, err)
	}
	if _, err := store.BusinessKeyProjectKey(project.ID+"-missing", func() (string, error) { return "", errors.New("wrapper unavailable") }); err == nil {
		t.Fatal("a failed create was accepted")
	}
	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	created := false
	if _, err := store.BusinessKeyProjectKey(DefaultProjectID, func() (string, error) { created = true; return "wrapped-default", nil }); err != nil || !created {
		t.Fatalf("default project: %v", err)
	}
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM business_key_project_keys WHERE project_id = ?`, project.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("deleted project's key remains: %d %v", n, err)
	}
}
