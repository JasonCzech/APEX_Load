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

func TestSQLiteStoreApplicationsReleasesScenariosAndContext(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	app := &Application{ID: "app-1", Name: "Payments", CreatedAt: time.Now()}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &Release{ID: "rel-1", ApplicationID: app.ID, Name: "2026.05", CreatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	script := &dsl.Script{
		ID:       "script-context",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	scenario := &Scenario{
		ID:           "scenario-1",
		Name:         "Checkout Smoke",
		Duration:     "2m",
		ScenarioMode: "vuser_driven",
		CreatedAt:    time.Now(),
	}
	groups := []ScenarioScript{{Order: 0, ScriptID: script.ID, VUsers: 25, Initialization: "at_runtime"}}
	if err := store.SaveScenario(scenario, groups); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	roundTrip, scripts, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario returned error: %v", err)
	}
	if roundTrip.Name != scenario.Name || len(scripts) != 1 || scripts[0].ScriptID != script.ID {
		t.Fatalf("unexpected scenario round trip: %#v scripts=%#v", roundTrip, scripts)
	}

	record := &TestRecord{
		ID:            "test-1",
		Name:          scenario.Name,
		Status:        "COMPLETE",
		ApplicationID: app.ID,
		ReleaseID:     release.ID,
		ScenarioID:    scenario.ID,
		TotalVUsers:   25,
		Duration:      int64(2 * time.Minute),
		StartTime:     time.Now(),
		Groups:        []map[string]any{{"script_id": script.ID, "vusers": 25}},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	filtered, err := store.ListTestsByContext(app.ID, release.ID, scenario.ID)
	if err != nil {
		t.Fatalf("ListTestsByContext returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ReleaseID != release.ID || filtered[0].ScenarioID != scenario.ID {
		t.Fatalf("unexpected filtered tests: %#v", filtered)
	}

	if err := store.DeleteRelease(release.ID); err == nil {
		t.Fatal("expected DeleteRelease to fail while results exist")
	}
	if err := store.ArchiveRelease(release.ID); err == nil {
		t.Fatal("expected ArchiveRelease to fail while results exist")
	}
}

func TestSQLiteStoreSharedDataPoolsAndScenarioBindings(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-data-pool",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &Scenario{ID: "scenario-data-pool", Name: "Checkout Flow", Duration: "5m", CreatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []ScenarioScript{{Order: 0, ScriptID: script.ID, VUsers: 10}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	pool := &SharedDataPool{
		ID:          "pool-1",
		Name:        "Customers",
		Description: "Reusable customer identities",
		Schema:      map[string]string{"customer_id": "string", "zip": "string"},
		CreatedAt:   time.Now(),
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-100", "zip": "10001"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-101", "zip": "10002"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow update returned error: %v", err)
	}

	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Data["customer_id"] != "C-101" || rows[0].Version != 2 {
		t.Fatalf("unexpected rows after update: %#v", rows)
	}

	bindings := []ScenarioDataPoolBinding{{
		ID:                "binding-1",
		PoolID:            pool.ID,
		Alias:             "customers",
		AccessMode:        "read_write",
		SelectionStrategy: "unique",
	}}
	if err := store.SaveScenarioDataPoolBindings(scenario.ID, bindings); err != nil {
		t.Fatalf("SaveScenarioDataPoolBindings returned error: %v", err)
	}
	roundTrip, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings returned error: %v", err)
	}
	if len(roundTrip) != 1 || roundTrip[0].PoolName != pool.Name || roundTrip[0].Alias != "customers" || roundTrip[0].SelectionStrategy != "unique" {
		t.Fatalf("unexpected scenario data pool bindings: %#v", roundTrip)
	}
	if err := store.DeleteDataPool(pool.ID); err == nil {
		t.Fatal("expected DeleteDataPool to fail while scenario binding exists")
	}
	if err := store.DeleteScenarioDataPoolBinding("binding-1"); err != nil {
		t.Fatalf("DeleteScenarioDataPoolBinding returned error: %v", err)
	}
	if err := store.DeleteDataPool(pool.ID); err != nil {
		t.Fatalf("DeleteDataPool returned error after unbinding: %v", err)
	}
}
