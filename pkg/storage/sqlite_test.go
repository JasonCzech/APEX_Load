package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestSQLiteStoreSaveScriptPreservesMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Imported Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
		Actions: []dsl.Action{{
			Name:   "Homepage",
			Type:   "GET",
			Target: "/",
		}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source:           "loadrunner",
				SourceScriptName: "Demo",
				Preserved: map[string]any{
					"runtime_files": map[string]any{
						"default.cfg": map[string]any{
							"HTTP": map[string]any{
								"ConnectTimeout": "45",
							},
						},
					},
				},
			},
		},
	}

	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	roundTrip, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.Metadata == nil || roundTrip.Metadata.Import == nil {
		t.Fatal("expected metadata to round-trip through sqlite")
	}
	payload, err := json.Marshal(roundTrip.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"loadrunner\"", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
}

func TestSQLiteStoreProjectsFoldersAndFiltering(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects returned error: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != DefaultProjectID {
		t.Fatalf("expected default project, got %#v", projects)
	}

	project := &Project{ID: "project-1", Name: "Payments", CreatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &Folder{ID: "folder-1", ProjectID: project.ID, Name: "Checkout", Category: "scripts", CreatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	script := &dsl.Script{
		ID:        "script-1",
		Name:      "Checkout API",
		Protocol:  "http",
		Version:   "1.0",
		ProjectID: project.ID,
		FolderID:  &folder.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Cart", Type: "request", Method: "GET", Target: "/cart"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	filtered, err := store.ListScripts(project.ID, &folder.ID)
	if err != nil {
		t.Fatalf("ListScripts returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ProjectID != project.ID || filtered[0].FolderID == nil || *filtered[0].FolderID != folder.ID {
		t.Fatalf("unexpected filtered scripts: %#v", filtered)
	}

	if err := store.DeleteFolder(folder.ID); err == nil {
		t.Fatal("expected non-empty folder delete to fail")
	}
	if err := store.MoveScript(script.ID, nil); err != nil {
		t.Fatalf("MoveScript returned error: %v", err)
	}
	if err := store.DeleteFolder(folder.ID); err != nil {
		t.Fatalf("DeleteFolder returned error after moving script: %v", err)
	}
}

func TestSQLiteStoreBackfillsDefaultProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stormrunner.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	script := &dsl.Script{
		ID:       "legacy-script",
		Name:     "Legacy",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	store.Close()

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore returned error: %v", err)
	}
	defer reopened.Close()
	roundTrip, err := reopened.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("project id = %q, want %q", roundTrip.ProjectID, DefaultProjectID)
	}
}
