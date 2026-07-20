package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type stubSchedulingService struct {
	preview           *ScenarioSchedulePreview
	previewScenarioID string
	previewRequest    CreateScenarioScheduleRequest
	previewCount      int
	previewCalls      int
	listed            []*storage.ScenarioSchedule
	listOK            bool
	listScenarioID    string
	listCalls         int
	health            ScenarioScheduleHealth
	healthNow         time.Time
	healthCalls       int
	archiveRetention  ArchiveRetentionSettings
	calendar          RunCalendarResponse
	calendarCapacity  *RunCalendarCapacity
}

func (service *stubSchedulingService) PreviewScenarioScheduleRuns(scenarioID string, request CreateScenarioScheduleRequest, count int) (*ScenarioSchedulePreview, error) {
	service.previewCalls++
	service.previewScenarioID = scenarioID
	service.previewRequest = request
	service.previewCount = count
	return service.preview, nil
}

func (*stubSchedulingService) CreateScenarioSchedule(string, CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	return nil, nil
}

func (service *stubSchedulingService) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool) {
	service.listCalls++
	service.listScenarioID = scenarioID
	return service.listed, service.listOK
}

func (*stubSchedulingService) UpdateScenarioSchedule(string, string, CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error) {
	return nil, false, nil
}

func (*stubSchedulingService) DeleteScenarioSchedule(string, string) (bool, error) {
	return false, nil
}

func (*stubSchedulingService) ListScenarioScheduleOccurrencesPage(string, string, int, int) ([]*storage.ScenarioScheduleOccurrence, bool, error) {
	return nil, false, nil
}

func (*stubSchedulingService) ResolveScenarioScheduleOccurrence(string, string, string, ScheduleOccurrenceResolutionRequest) (*storage.ScenarioScheduleOccurrence, *storage.ScenarioSchedule, bool, error) {
	return nil, nil, false, nil
}

func (*stubSchedulingService) DeleteScenarioScheduleOccurrence(string, string, string) (bool, error) {
	return false, nil
}

func (*stubSchedulingService) ListScenarioScheduleApprovalsPage(string, string, int, int) ([]*storage.ScenarioScheduleApproval, bool, error) {
	return nil, false, nil
}

func (*stubSchedulingService) ResolveScenarioScheduleApproval(string, string, string, bool, string, string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error) {
	return nil, nil, false, nil
}

func (*stubSchedulingService) ScenarioProjectID(string) string { return "" }

func (service *stubSchedulingService) Health(now time.Time) ScenarioScheduleHealth {
	service.healthCalls++
	service.healthNow = now
	return service.health
}

func (service *stubSchedulingService) ArchiveRetentionSettings() ArchiveRetentionSettings {
	return service.archiveRetention
}

func (service *stubSchedulingService) BuildRunCalendar(time.Time, time.Time, string, time.Time, SystemInventoryResponse, RunCalendarRange, int) RunCalendarResponse {
	return service.calendar
}

func (service *stubSchedulingService) BuildRunCalendarCapacity(time.Time, time.Time, string, string) *RunCalendarCapacity {
	return service.calendarCapacity
}

func TestScenarioSchedulingHandlersDelegateToDomainService(t *testing.T) {
	service := &stubSchedulingService{
		preview: &ScenarioSchedulePreview{CronExpr: "*/5 * * * *", Timezone: "UTC", Count: 1},
		listed:  []*storage.ScenarioSchedule{{ID: "schedule-1", ScenarioID: "scenario-1"}},
		listOK:  true,
		health:  ScenarioScheduleHealth{Total: 3, Enabled: 2},
	}
	server := &Server{schedulingService: service}

	previewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/scenario-1/schedules/preview?count=7", bytes.NewBufferString(`{"name":"preview","cron_expr":"*/5 * * * *"}`))
	previewRequest.SetPathValue("id", "scenario-1")
	previewResponse := httptest.NewRecorder()
	server.handleScenarioSchedulePreview(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK || service.previewCalls != 1 {
		t.Fatalf("preview response = %d, calls = %d, body=%s; want 200 and one service call", previewResponse.Code, service.previewCalls, previewResponse.Body.String())
	}
	if service.previewScenarioID != "scenario-1" || service.previewCount != 7 || service.previewRequest.Name != "preview" {
		t.Fatalf("preview delegation = scenario:%q count:%d request:%#v", service.previewScenarioID, service.previewCount, service.previewRequest)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/scenario-1/schedules", nil)
	listRequest.SetPathValue("id", "scenario-1")
	listResponse := httptest.NewRecorder()
	server.handleListScenarioSchedules(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || service.listCalls != 1 || service.listScenarioID != "scenario-1" {
		t.Fatalf("list response = %d, calls = %d, scenario = %q, body=%s", listResponse.Code, service.listCalls, service.listScenarioID, listResponse.Body.String())
	}
	var listed struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Count != 1 {
		t.Fatalf("list count = %d, want 1", listed.Count)
	}

	healthResponse := httptest.NewRecorder()
	server.handleScenarioScheduleHealth(healthResponse, httptest.NewRequest(http.MethodGet, "/api/v1/schedules/health", nil))
	if healthResponse.Code != http.StatusOK || service.healthCalls != 1 || service.healthNow.IsZero() {
		t.Fatalf("health response = %d, calls = %d, now = %v, body=%s", healthResponse.Code, service.healthCalls, service.healthNow, healthResponse.Body.String())
	}
	var health ScenarioScheduleHealth
	if err := json.Unmarshal(healthResponse.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health.Total != 3 || health.Enabled != 2 {
		t.Fatalf("health response = %#v, want delegated totals", health)
	}
}

func TestSchedulingServiceKeepsServerConstructionCompatible(t *testing.T) {
	constructed := NewServer(ServerConfig{MaxConcurrent: 2})
	adapter, ok := constructed.schedulingService.(*testRunnerSchedulingService)
	if !ok || adapter.runner != constructed.runner || adapter.scheduler != constructed.scheduler {
		t.Fatalf("NewServer scheduling service = %#v, want runner/scheduler compatibility adapter", constructed.schedulingService)
	}

	replacementRunner := NewTestRunner(3, NewHub(), nil)
	replacementScheduler := newScenarioScheduler(replacementRunner)
	constructed.runner = replacementRunner
	constructed.scheduler = replacementScheduler
	replacement, ok := constructed.schedulingDomainService().(*testRunnerSchedulingService)
	if !ok || replacement.runner != replacementRunner || replacement.scheduler != replacementScheduler {
		t.Fatalf("replacement scheduling service = %#v, want current runner/scheduler", replacement)
	}

	legacy := &Server{runner: replacementRunner, scheduler: replacementScheduler}
	compatibility, ok := legacy.schedulingDomainService().(*testRunnerSchedulingService)
	if !ok || compatibility.runner != replacementRunner || compatibility.scheduler != replacementScheduler {
		t.Fatalf("legacy Server literal scheduling service = %#v", compatibility)
	}
}

var _ schedulingService = (*stubSchedulingService)(nil)
