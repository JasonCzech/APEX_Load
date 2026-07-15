package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestResolveReleaseApplicationIDConvergesConcurrentImplicitCreates(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	project := &storage.Project{ID: "project-concurrent-release", Name: "Concurrent releases"}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	const callers = 8
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := server.resolveReleaseApplicationID(project.ID, "")
			ids <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("resolveReleaseApplicationID: %v", err)
		}
	}
	want := ""
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("implicit application ids diverged: got %q and %q", want, id)
		}
	}
	applications, err := store.ListApplications()
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	count := 0
	for _, application := range applications {
		if application.ProjectID == project.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("project applications = %d, want exactly one implicit application", count)
	}
}

func TestApplicationReleaseAndScenarioHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createApp := httptest.NewRequest(http.MethodPost, "/api/v1/applications", bytes.NewBufferString(`{"name":"Payments"}`))
	createApp.Header.Set("Content-Type", "application/json")
	appResp := httptest.NewRecorder()
	server.mux.ServeHTTP(appResp, createApp)
	if appResp.Code != http.StatusCreated {
		t.Fatalf("create application status = %d body=%s", appResp.Code, appResp.Body.String())
	}
	var app storage.Application
	if err := json.Unmarshal(appResp.Body.Bytes(), &app); err != nil {
		t.Fatalf("unmarshal application: %v", err)
	}

	createRelease := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+app.ID+"/releases", bytes.NewBufferString(`{"name":"2026.05"}`))
	createRelease.Header.Set("Content-Type", "application/json")
	releaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(releaseResp, createRelease)
	if releaseResp.Code != http.StatusCreated {
		t.Fatalf("create release status = %d body=%s", releaseResp.Code, releaseResp.Body.String())
	}

	script := &dsl.Script{
		ID:       "script-api",
		Name:     "API Script",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	server.runner.StoreScript(script)
	createScenario := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewBufferString(`{
		"name":"Smoke",
		"duration":"1m",
		"groups":[{"script_id":"`+script.ID+`","vusers":5}],
		"runtime_settings":{
			"iterations":4,
			"pacing":{"type":"fixed_delay","delay_ms":1500},
			"think_time":{"type":"fixed","mean_ms":750},
			"browser":{"headless":false,"device_profile":"iphone_se","touch_mode":true,"orientation":"portrait"},
			"log_level":"extended",
			"auto_transactions":"per_iteration",
			"error_handling":{"continue_on_error":true,"snapshot_on_error":true}
		}
	}`))
	createScenario.Header.Set("Content-Type", "application/json")
	scenarioResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scenarioResp, createScenario)
	if scenarioResp.Code != http.StatusCreated {
		t.Fatalf("create scenario status = %d body=%s", scenarioResp.Code, scenarioResp.Body.String())
	}
	var scenario StoredScenario
	if err := json.Unmarshal(scenarioResp.Body.Bytes(), &scenario); err != nil {
		t.Fatalf("unmarshal scenario: %v", err)
	}
	if scenario.RuntimeSettings == nil {
		t.Fatal("expected runtime settings on stored scenario")
	}
	if scenario.RuntimeSettings.Iterations == nil || *scenario.RuntimeSettings.Iterations != 4 {
		t.Fatalf("scenario iterations = %#v, want 4", scenario.RuntimeSettings.Iterations)
	}
	if scenario.RuntimeSettings.Pacing == nil || scenario.RuntimeSettings.Pacing.Type != "fixed_delay" || scenario.RuntimeSettings.Pacing.DelayMs != 1500 {
		t.Fatalf("scenario pacing = %#v, want fixed_delay 1500ms", scenario.RuntimeSettings.Pacing)
	}
	if scenario.RuntimeSettings.ThinkTime == nil || scenario.RuntimeSettings.ThinkTime.Type != "fixed" || scenario.RuntimeSettings.ThinkTime.MeanMs != 750 {
		t.Fatalf("scenario think_time = %#v, want fixed 750ms", scenario.RuntimeSettings.ThinkTime)
	}
	if scenario.RuntimeSettings.Browser == nil || scenario.RuntimeSettings.Browser.Headless == nil || *scenario.RuntimeSettings.Browser.Headless != false {
		t.Fatalf("scenario browser override = %#v, want headless false", scenario.RuntimeSettings.Browser)
	}
	if scenario.RuntimeSettings.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("scenario browser device_profile = %q, want iphone_se", scenario.RuntimeSettings.Browser.DeviceProfile)
	}
	if scenario.RuntimeSettings.Browser.TouchMode == nil || *scenario.RuntimeSettings.Browser.TouchMode != true {
		t.Fatalf("scenario browser touch_mode = %#v, want true", scenario.RuntimeSettings.Browser.TouchMode)
	}
	if scenario.RuntimeSettings.Browser.Orientation != "portrait" {
		t.Fatalf("scenario browser orientation = %q, want portrait", scenario.RuntimeSettings.Browser.Orientation)
	}
	if scenario.RuntimeSettings.LogLevel != "extended" {
		t.Fatalf("scenario log level = %q, want extended", scenario.RuntimeSettings.LogLevel)
	}
	if scenario.RuntimeSettings.AutoTransactions != "per_iteration" {
		t.Fatalf("scenario auto transactions = %q, want per_iteration", scenario.RuntimeSettings.AutoTransactions)
	}
	if scenario.RuntimeSettings.ErrorHandling == nil || !scenario.RuntimeSettings.ErrorHandling.ContinueOnError || !scenario.RuntimeSettings.ErrorHandling.SnapshotOnError {
		t.Fatalf("scenario error handling = %#v, want continue_on_error and snapshot_on_error", scenario.RuntimeSettings.ErrorHandling)
	}
}

func TestScenarioCreateRejectsIncompatibleBrowserRuntimeOverride(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	script := &dsl.Script{
		ID:       "script-browser",
		Name:     "Browser Script",
		Protocol: "browser",
		Version:  "1.0",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{Engine: "chromium"},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	}
	server.runner.StoreScript(script)

	createScenario := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewBufferString(`{
		"name":"Smoke",
		"duration":"1m",
		"groups":[{"script_id":"`+script.ID+`","vusers":5}],
		"runtime_settings":{
			"browser":{"device_profile":"iphone_15_pro"}
		}
	}`))
	createScenario.Header.Set("Content-Type", "application/json")
	scenarioResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scenarioResp, createScenario)

	if scenarioResp.Code != http.StatusBadRequest {
		t.Fatalf("create scenario status = %d, want %d body=%s", scenarioResp.Code, http.StatusBadRequest, scenarioResp.Body.String())
	}
	if !strings.Contains(scenarioResp.Body.String(), "incompatible with device_profile") || !strings.Contains(scenarioResp.Body.String(), "requires webkit") {
		t.Fatalf("create scenario body = %s, want incompatible browser override issue", scenarioResp.Body.String())
	}
}

func TestArchiveReleaseRequiresNoAssociatedResults(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	app := &storage.Application{ID: "app-1", Name: "Payments"}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &storage.Release{ID: "rel-1", ApplicationID: app.ID, Name: "2026.05"}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "test-1", Name: "run", Status: "COMPLETE", ApplicationID: app.ID, ReleaseID: release.ID}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/archive", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("archive release status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("associated results")) {
		t.Fatalf("archive release body = %s, want associated results warning", resp.Body.String())
	}
}
