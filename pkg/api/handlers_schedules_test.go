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

func TestHandleScenarioScheduleCRUD(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-schedule-api",
		Name:      "Scenario API Script",
		ProjectID: "project-schedule",
		Protocol:  "http",
		Version:   "1.0.0",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Scenario",
		Duration: "2m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-schedule-api",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	createBody := bytes.NewBufferString(`{"name":"Nightly Smoke","cron_expr":"*/10 * * * *","run_name_template":"Nightly Checkout"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.ScenarioSchedule
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.ID == "" || created.NextRunAt == nil {
		t.Fatalf("created schedule = %#v, want id and next_run_at", created)
	}
	if created.ProjectID != "project-schedule" || created.OwnerSubject != "dev-mode" || created.Governance == nil {
		t.Fatalf("created schedule governance = %#v, want project ownership and governance review", created)
	}
	if created.Governance.RBACReview.Decision != "allowed" || created.Governance.QuotaReview.Decision != "allowed" || created.Governance.QuotaReview.EstimatedPeakVUsers != 1 {
		t.Fatalf("created governance = %#v, want allowed RBAC/quota review for one VUser", created.Governance)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/schedules", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var listed struct {
		Schedules []*storage.ScenarioSchedule `json:"schedules"`
		Count     int                         `json:"count"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("json.Unmarshal list returned error: %v", err)
	}
	if listed.Count != 1 || len(listed.Schedules) != 1 {
		t.Fatalf("list payload = %#v, want exactly one schedule", listed)
	}

	updateBody := bytes.NewBufferString(`{"name":"Nightly Smoke","cron_expr":"0 * * * *","run_name_template":"Hourly Checkout","enabled":false}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID, updateBody)
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}
	var updated storage.ScenarioSchedule
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("json.Unmarshal update returned error: %v", err)
	}
	if updated.CronExpr != "0 * * * *" || updated.RunNameTemplate != "Hourly Checkout" || updated.Enabled {
		t.Fatalf("updated schedule = %#v, want hourly disabled schedule", updated)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}
	if schedules, ok := server.runner.ListScenarioSchedules(scenario.ID); !ok || len(schedules) != 0 {
		t.Fatalf("remaining schedules = %#v, want empty", schedules)
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: "project-schedule"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("audit event count = %d, want 3", len(events))
	}
	if events[0].Action != "scenario.schedule.delete" || events[1].Action != "scenario.schedule.update" || events[2].Action != "scenario.schedule.create" {
		t.Fatalf("audit actions = [%s %s %s], want delete/update/create", events[0].Action, events[1].Action, events[2].Action)
	}
	if events[2].Details["governance"] == nil || events[2].Details["owner"] == nil {
		t.Fatalf("create audit details = %#v, want governance and owner metadata", events[2].Details)
	}
}

func TestLoadModelSupportMatrixEndpoint(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/load-models/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		SupportMatrix []LoadModelSupportEntry `json:"support_matrix"`
		StatusCounts  map[string]int          `json:"status_counts"`
		Notes         []string                `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(payload.SupportMatrix) < 8 {
		t.Fatalf("support_matrix = %d, want core workload shapes", len(payload.SupportMatrix))
	}
	if payload.StatusCounts["executed"] == 0 || payload.StatusCounts["partial"] == 0 {
		t.Fatalf("status_counts = %#v, want executed and partial coverage", payload.StatusCounts)
	}
	if !containsLoadModelSupportEntry(payload.SupportMatrix, "open-arrival-rate", "executed") {
		t.Fatalf("support_matrix = %#v, want executed open arrival-rate entry", payload.SupportMatrix)
	}
}

func TestLoadModelTemplatesEndpointFiltersByShape(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/load-models/templates?shape_id=open-arrival-rate", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Templates []LoadModelTemplate `json:"templates"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Count != 1 || payload.Templates[0].ShapeID != "open-arrival-rate" {
		t.Fatalf("payload = %#v, want one open-arrival-rate template", payload)
	}
	if payload.Templates[0].Scenario.GoalConfig == nil || payload.Templates[0].Scenario.GoalConfig.TargetAverageTPS == nil {
		t.Fatalf("scenario = %#v, want throughput goal hints", payload.Templates[0].Scenario)
	}
}

func containsLoadModelSupportEntry(entries []LoadModelSupportEntry, id string, status string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status == status {
			return true
		}
	}
	return false
}

func TestHandleScenarioCRUDWritesAuditEvents(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-scenario-api",
		Name:      "Scenario CRUD Script",
		ProjectID: "project-scenario",
		Protocol:  "http",
		Version:   "1.0.0",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	createBody := bytes.NewBufferString(`{"name":"Checkout Scenario","duration":"2m","groups":[{"script_id":"script-scenario-api","vusers":1}]}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var created StoredScenario
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected scenario id in create payload")
	}

	updateBody := bytes.NewBufferString(`{"name":"Checkout Scenario Updated","duration":"3m","groups":[{"script_id":"script-scenario-api","vusers":2}]}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+created.ID, updateBody)
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scenarios/"+created.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: "project-scenario"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("audit event count = %d, want 3", len(events))
	}
	if events[0].Action != "scenario.delete" || events[1].Action != "scenario.update" || events[2].Action != "scenario.create" {
		t.Fatalf("audit actions = [%s %s %s], want delete/update/create", events[0].Action, events[1].Action, events[2].Action)
	}
}
