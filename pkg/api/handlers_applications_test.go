package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestApplicationReleaseAndScenarioHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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
	createScenario := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewBufferString(`{"name":"Smoke","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":5}]}`))
	createScenario.Header.Set("Content-Type", "application/json")
	scenarioResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scenarioResp, createScenario)
	if scenarioResp.Code != http.StatusCreated {
		t.Fatalf("create scenario status = %d body=%s", scenarioResp.Code, scenarioResp.Body.String())
	}
}
