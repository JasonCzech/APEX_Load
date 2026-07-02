package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleScenarioScheduleCRUD(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
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

	createBody := bytes.NewBufferString(`{"name":"Nightly Smoke","cron_expr":"*/10 * * * *","run_name_template":"Nightly Checkout","approval_policy":"production","blackout_windows":[{"name":"blackout","days_of_week":[0],"start_time":"00:00","end_time":"06:00"}],"maintenance_windows":[{"name":"shared-maintenance","days_of_week":[6],"start_time":"22:00","end_time":"23:00"}],"availability_windows":[{"name":"business-hours","days_of_week":[1,2,3,4,5],"start_time":"09:00","end_time":"17:00"}]}`)
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
	if len(created.BlackoutWindows) != 1 || len(created.MaintenanceWindows) != 1 || len(created.AvailabilityWindows) != 1 {
		t.Fatalf("created schedule windows = blackout %#v maintenance %#v availability %#v, want persisted schedule windows", created.BlackoutWindows, created.MaintenanceWindows, created.AvailabilityWindows)
	}
	if created.PendingApprovalID == "" || created.NextRequiredAction != "approval_required" {
		t.Fatalf("created approval state = id %q action %q, want pending approval", created.PendingApprovalID, created.NextRequiredAction)
	}
	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID+"/approvals/"+created.PendingApprovalID+"/approve", bytes.NewBufferString(`{"reason":"release window approved"}`))
	approveReq.Header.Set("Content-Type", "application/json")
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	if approveResp.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d body=%s", approveResp.Code, http.StatusOK, approveResp.Body.String())
	}
	var approvalPayload struct {
		Approval storage.ScenarioScheduleApproval `json:"approval"`
		Schedule storage.ScenarioSchedule         `json:"schedule"`
	}
	if err := json.Unmarshal(approveResp.Body.Bytes(), &approvalPayload); err != nil {
		t.Fatalf("json.Unmarshal approve returned error: %v", err)
	}
	if approvalPayload.Approval.Status != "approved" || approvalPayload.Schedule.NextRequiredAction != "" {
		t.Fatalf("approval payload = %#v, want approved schedule", approvalPayload)
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
	plannedAt := time.Date(2026, time.May, 18, 9, 0, 0, 0, time.UTC)
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID:               "occurrence-1",
		ScheduleID:       created.ID,
		ScenarioID:       scenario.ID,
		ProjectID:        "project-schedule",
		ScheduleRevision: created.Revision,
		PlannedAt:        plannedAt,
		Status:           "materialized",
		ReasonCode:       "started",
		TestID:           "test-scheduled",
		CreatedAt:        plannedAt,
		UpdatedAt:        plannedAt,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence returned error: %v", err)
	}
	occurrencesReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID+"/occurrences", nil)
	occurrencesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(occurrencesResp, occurrencesReq)
	if occurrencesResp.Code != http.StatusOK {
		t.Fatalf("occurrences status = %d, want %d body=%s", occurrencesResp.Code, http.StatusOK, occurrencesResp.Body.String())
	}
	var occurrencePayload struct {
		Occurrences []*storage.ScenarioScheduleOccurrence `json:"occurrences"`
		Count       int                                   `json:"count"`
	}
	if err := json.Unmarshal(occurrencesResp.Body.Bytes(), &occurrencePayload); err != nil {
		t.Fatalf("json.Unmarshal occurrences returned error: %v", err)
	}
	if occurrencePayload.Count != 1 || occurrencePayload.Occurrences[0].Status != "materialized" {
		t.Fatalf("occurrence payload = %#v, want one materialized occurrence", occurrencePayload)
	}

	approvalsReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID+"/approvals", nil)
	approvalsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approvalsResp, approvalsReq)
	if approvalsResp.Code != http.StatusOK {
		t.Fatalf("approvals status = %d, want %d body=%s", approvalsResp.Code, http.StatusOK, approvalsResp.Body.String())
	}
	var approvalsPayload struct {
		Approvals []*storage.ScenarioScheduleApproval `json:"approvals"`
		Count     int                                 `json:"count"`
	}
	if err := json.Unmarshal(approvalsResp.Body.Bytes(), &approvalsPayload); err != nil {
		t.Fatalf("json.Unmarshal approvals returned error: %v", err)
	}
	if approvalsPayload.Count != 1 || approvalsPayload.Approvals[0].ID != created.PendingApprovalID || approvalsPayload.Approvals[0].Status != "approved" {
		t.Fatalf("approvals payload = %#v, want one approved approval %q", approvalsPayload, created.PendingApprovalID)
	}

	approvalsMissingReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/schedules/does-not-exist/approvals", nil)
	approvalsMissingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approvalsMissingResp, approvalsMissingReq)
	if approvalsMissingResp.Code != http.StatusNotFound {
		t.Fatalf("approvals (missing schedule) status = %d, want 404", approvalsMissingResp.Code)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/schedules/health", nil)
	healthResp := httptest.NewRecorder()
	server.mux.ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d body=%s", healthResp.Code, http.StatusOK, healthResp.Body.String())
	}
	var health ScenarioScheduleHealth
	if err := json.Unmarshal(healthResp.Body.Bytes(), &health); err != nil {
		t.Fatalf("json.Unmarshal health returned error: %v", err)
	}
	if health.Total != 1 || health.StatusCounts["materialized"] != 1 || health.Materialized != 1 {
		t.Fatalf("health = %#v, want one schedule and one materialized occurrence", health)
	}

	deleteOccID := occurrencePayload.Occurrences[0].ID
	delOccReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID+"/occurrences/"+deleteOccID, nil)
	delOccResp := httptest.NewRecorder()
	server.mux.ServeHTTP(delOccResp, delOccReq)
	if delOccResp.Code != http.StatusOK {
		t.Fatalf("delete occurrence status = %d body=%s, want 200", delOccResp.Code, delOccResp.Body.String())
	}
	delOccAgain := httptest.NewRequest(http.MethodDelete, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID+"/occurrences/"+deleteOccID, nil)
	delOccAgainResp := httptest.NewRecorder()
	server.mux.ServeHTTP(delOccAgainResp, delOccAgain)
	if delOccAgainResp.Code != http.StatusNotFound {
		t.Fatalf("re-delete occurrence status = %d, want 404", delOccAgainResp.Code)
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
	if len(events) != 5 {
		t.Fatalf("audit event count = %d, want 5", len(events))
	}
	if events[0].Action != "scenario.schedule.delete" || events[1].Action != "scenario.schedule.update" || events[2].Action != "scenario.schedule.occurrence.delete" || events[3].Action != "scenario.schedule.approval.approve" || events[4].Action != "scenario.schedule.create" {
		t.Fatalf("audit actions = [%s %s %s %s %s], want delete/update/occurrence.delete/approve/create", events[0].Action, events[1].Action, events[2].Action, events[3].Action, events[4].Action)
	}
	if events[4].Details["governance"] == nil || events[4].Details["owner"] == nil || events[4].Details["blackout_windows"] == nil || events[4].Details["maintenance_windows"] == nil || events[4].Details["availability_windows"] == nil {
		t.Fatalf("create audit details = %#v, want governance and owner metadata", events[4].Details)
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
	if payload.StatusCounts["executed"] != len(payload.SupportMatrix) || payload.StatusCounts["partial"] != 0 {
		t.Fatalf("status_counts = %#v, want all load-model shapes executed", payload.StatusCounts)
	}
	if !containsLoadModelSupportEntry(payload.SupportMatrix, "open-arrival-rate", "executed") {
		t.Fatalf("support_matrix = %#v, want executed open arrival-rate entry", payload.SupportMatrix)
	}
	if !containsLoadModelSupportEntry(payload.SupportMatrix, "chaos-resilience", "executed") {
		t.Fatalf("support_matrix = %#v, want executed chaos-resilience entry", payload.SupportMatrix)
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

func TestLoadModelTemplatesEndpointReturnsChaosShakeoutTemplate(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/load-models/templates?shape_id=chaos-resilience", nil)
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
	if payload.Count != 1 || payload.Templates[0].ShapeID != "chaos-resilience" {
		t.Fatalf("payload = %#v, want one chaos-resilience template", payload)
	}
	template := payload.Templates[0]
	if !template.Scenario.ScriptShakeout || !template.Scenario.ResourceBaseline {
		t.Fatalf("scenario = %#v, want shakeout and resource baseline enabled", template.Scenario)
	}
	if template.Scenario.RuntimeSettings == nil || template.Scenario.RuntimeSettings.ErrorHandling == nil || !template.Scenario.RuntimeSettings.ErrorHandling.SnapshotOnError {
		t.Fatalf("runtime_settings = %#v, want snapshot-on-error evidence setting", template.Scenario.RuntimeSettings)
	}
	if len(template.Scenario.Groups) != 1 || len(template.Scenario.Groups[0].Stages) != 3 {
		t.Fatalf("groups = %#v, want staged chaos template", template.Scenario.Groups)
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
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
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

// F16: an open-ended pause (disabled_from set, disabled_until omitted) must save as
// a paused schedule (next_run_at nil) instead of exhausting the cron search horizon
// and returning a misleading "impossible day/month" error.
func TestScenarioScheduleOpenEndedPauseSaves(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID: "script-pause", Name: "Pause Script", ProjectID: "project-pause", Protocol: "http",
		Version: "1.0.0", Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Pause Scenario", Duration: "2m",
		Groups: []CreateGroupEntry{{ScriptID: "script-pause", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	disabledFrom := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := bytes.NewBufferString(`{"name":"Paused","cron_expr":"*/10 * * * *","disabled_from":"` + disabledFrom + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("open-ended pause create status = %d, want 201 body=%s", resp.Code, resp.Body.String())
	}
	var created storage.ScenarioSchedule
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.NextRunAt != nil {
		t.Fatalf("open-ended paused schedule next_run_at = %v, want nil", created.NextRunAt)
	}

	// Un-pause: clear disabled_from with every other schedule field unchanged. The
	// next run must be recomputed (non-nil), not left at the stale paused nil — a
	// resumed schedule stuck at nil would be skipped by the sweep forever.
	unpauseBody := bytes.NewBufferString(`{"name":"Paused","cron_expr":"*/10 * * * *"}`)
	unpauseReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+created.ID, unpauseBody)
	unpauseReq.Header.Set("Content-Type", "application/json")
	unpauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unpauseResp, unpauseReq)
	if unpauseResp.Code != http.StatusOK {
		t.Fatalf("un-pause update status = %d, want 200 body=%s", unpauseResp.Code, unpauseResp.Body.String())
	}
	var resumed storage.ScenarioSchedule
	if err := json.Unmarshal(unpauseResp.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("json.Unmarshal un-pause returned error: %v", err)
	}
	if resumed.NextRunAt == nil {
		t.Fatalf("resumed schedule next_run_at = nil, want a recomputed next run after clearing the pause")
	}
}
