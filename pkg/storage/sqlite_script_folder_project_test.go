package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSQLiteScriptsCannotReferenceFoldersFromAnotherProject(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, projectID := range []string{"script-project-a", "script-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	folderA := &Folder{ID: "script-folder-a", ProjectID: "script-project-a", Name: "A", Category: "scripts", CreatedAt: now}
	folderB := &Folder{ID: "script-folder-b", ProjectID: "script-project-b", Name: "B", Category: "scripts", CreatedAt: now}
	for _, folder := range []*Folder{folderA, folderB} {
		if err := store.SaveFolder(folder); err != nil {
			t.Fatalf("SaveFolder(%s): %v", folder.ID, err)
		}
	}

	foreignFolderID := folderB.ID
	foreignCreate := &dsl.Script{ID: "foreign-folder-create", Name: "foreign", Protocol: "http", ProjectID: folderA.ProjectID, FolderID: &foreignFolderID}
	if err := store.SaveScript(foreignCreate); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("SaveScript foreign folder error = %v", err)
	}
	if created, err := store.CreateScriptIfAbsent(&dsl.Script{ID: "foreign-folder-create-only", Name: "foreign", Protocol: "http", ProjectID: folderA.ProjectID, FolderID: &foreignFolderID}); err == nil || created || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("CreateScriptIfAbsent foreign folder = (%v, %v)", created, err)
	}

	localFolderID := folderA.ID
	script := &dsl.Script{ID: "folder-owned-script", Name: "local", Protocol: "http", ProjectID: folderA.ProjectID, FolderID: &localFolderID}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript(local): %v", err)
	}
	loaded, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	loaded.FolderID = &foreignFolderID
	if updated, err := store.UpdateScriptIfUnchanged(loaded, loaded.UpdatedAt); err == nil || updated || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("UpdateScriptIfUnchanged foreign folder = (%v, %v)", updated, err)
	}
	if err := store.MoveScript(script.ID, &foreignFolderID); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("MoveScript foreign folder error = %v", err)
	}
	unchanged, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript unchanged: %v", err)
	}
	if unchanged.ProjectID != folderA.ProjectID || unchanged.FolderID == nil || *unchanged.FolderID != folderA.ID {
		t.Fatalf("rejected folder mutations changed script: %#v", unchanged)
	}

	ownershipRewrite := *unchanged
	ownershipRewrite.ProjectID = folderB.ProjectID
	ownershipRewrite.FolderID = &foreignFolderID
	if err := store.SaveScript(&ownershipRewrite); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("SaveScript ownership rewrite error = %v", err)
	}
	persisted, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript after ownership rewrite: %v", err)
	}
	if persisted.ProjectID != folderA.ProjectID {
		t.Fatalf("script project changed to %q", persisted.ProjectID)
	}
}
