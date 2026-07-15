package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFolderAndScriptWritesRequireDurableProjectParents(t *testing.T) {
	store := newProjectEntityCASStore(t)
	if err := store.SaveFolder(&Folder{ID: "missing-project-folder", ProjectID: "missing-project", Name: "missing", Category: "general"}); err == nil {
		t.Fatal("SaveFolder accepted a missing project")
	}
	if err := store.SaveScript(&dsl.Script{ID: "missing-project-script", ProjectID: "missing-project", Name: "missing", Protocol: "http"}); err == nil {
		t.Fatal("SaveScript accepted a missing project")
	}
	if err := store.SaveFolder(&Folder{ID: "project-a-folder", ProjectID: "project-a", Name: "A", Category: "general"}); err != nil {
		t.Fatalf("SaveFolder(project-a): %v", err)
	}
	foreignFolder := "project-a-folder"
	if err := store.SaveScript(&dsl.Script{ID: "foreign-folder-script", ProjectID: "project-b", FolderID: &foreignFolder, Name: "foreign", Protocol: "http"}); err == nil {
		t.Fatal("SaveScript accepted a folder from another project")
	}
}

func TestConcurrentOpposingFolderMovesCannotCreateCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folder-cycle-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "folder-race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	for _, folder := range []*Folder{
		{ID: "folder-a", ProjectID: "folder-race-project", Name: "A", Category: "general"},
		{ID: "folder-b", ProjectID: "folder-race-project", Name: "B", Category: "general"},
	} {
		if err := first.SaveFolder(folder); err != nil {
			t.Fatalf("SaveFolder(%s): %v", folder.ID, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		parent := "folder-b"
		errs <- first.MoveFolder("folder-a", &parent)
	}()
	go func() {
		defer wg.Done()
		<-start
		parent := "folder-a"
		errs <- second.MoveFolder("folder-b", &parent)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "descendant") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("opposing MoveFolder error: %v", err)
		}
	}

	for _, startID := range []string{"folder-a", "folder-b"} {
		seen := map[string]struct{}{}
		current := startID
		for current != "" {
			if _, ok := seen[current]; ok {
				t.Fatalf("folder hierarchy contains a cycle from %s: %#v", startID, seen)
			}
			seen[current] = struct{}{}
			var parent sql.NullString
			if err := first.db.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, current).Scan(&parent); err != nil {
				t.Fatalf("load folder %s parent: %v", current, err)
			}
			if !parent.Valid {
				break
			}
			current = parent.String
		}
	}
}

func TestConcurrentScriptCreateAndFolderDeleteCannotLeaveOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folder-script-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "script-race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := first.SaveFolder(&Folder{ID: "script-race-folder", ProjectID: "script-race-project", Name: "Race", Category: "general"}); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	folderID := "script-race-folder"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.SaveScript(&dsl.Script{ID: "script-race", ProjectID: "script-race-project", FolderID: &folderID, Name: "Race", Protocol: "http", CreatedAt: now})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.DeleteFolder(folderID)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "not found") && !strings.Contains(message, "not empty") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("concurrent script/folder mutation error: %v", err)
		}
	}

	script, scriptErr := first.GetScript("script-race")
	folder, folderErr := first.GetFolder(folderID)
	if scriptErr == nil && script != nil && (folderErr != nil || folder == nil) {
		t.Fatalf("orphan script survived concurrent create/delete: script=%#v folder=%#v folderErr=%v", script, folder, folderErr)
	}
}
