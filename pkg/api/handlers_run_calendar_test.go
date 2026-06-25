package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleRunCalendarReturnsAdHocScheduledAndProjectedRuns(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-calendar-checkout",
		Name:      "Checkout API",
		ProjectID: "project-calendar",
		Protocol:  "http",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://checkout.example.test"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/checkout"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Scenario",
		Duration: "30m",
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: "script-calendar-checkout",
			VUsers:   25,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	schedule, err := server.runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name:             "Hourly Checkout",
		CronExpr:         "0 * * * *",
		RunNameTemplate:  "Hourly Checkout {{time}}",
		OwnerSubject:     "scheduler-owner",
		OwnerDisplayName: "Scheduler Owner",
	})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}

	now := time.Now().UTC()
	plannedAt := now.Truncate(time.Hour).Add(time.Hour)
	scheduledTest, err := server.runner.CreateTest(CreateTestRequest{
		Name:       "Materialized Checkout",
		ScenarioID: scenario.ID,
	})
	if err != nil {
		t.Fatalf("CreateTest(scheduled) returned error: %v", err)
	}
	scheduledStart := plannedAt.Add(2 * time.Minute)
	scheduledEnd := plannedAt.Add(22 * time.Minute)
	scheduledTest.Status = "COMPLETE"
	scheduledTest.StartedAt = &scheduledStart
	scheduledTest.EndedAt = &scheduledEnd
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID:               "occurrence-calendar-materialized",
		ScheduleID:       schedule.ID,
		ScenarioID:       scenario.ID,
		ProjectID:        "project-calendar",
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt,
		MaterializedAt:   &scheduledStart,
		Status:           "materialized",
		ReasonCode:       "started",
		TestID:           scheduledTest.ID,
		CreatedAt:        plannedAt,
		UpdatedAt:        scheduledStart,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence returned error: %v", err)
	}

	adHocTest, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Investigate Checkout Spike",
		Duration: "15m",
		ScriptID: "script-calendar-checkout",
		VUsers:   7,
	})
	if err != nil {
		t.Fatalf("CreateTest(ad hoc) returned error: %v", err)
	}
	adHocStart := now.Add(-20 * time.Minute)
	adHocTest.Status = "RUNNING"
	adHocTest.StartedAt = &adHocStart
	adHocTest.StartedBySubject = "dana@example.com"
	adHocTest.StartedByDisplayName = "Dana Runner"
	adHocTest.GeneratorShards = []GeneratorShardSummary{{Group: "default", Target: "generator-a", VUsers: 7, ShardStrategy: "capacity_weighted"}}

	values := url.Values{}
	values.Set("from", now.Add(-2*time.Hour).Format(time.RFC3339))
	values.Set("to", plannedAt.Add(3*time.Hour).Format(time.RFC3339))
	values.Set("project_id", "project-calendar")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/run-calendar?"+values.Encode(), nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload RunCalendarResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Count < 3 || payload.SourceCounts["ad_hoc"] != 1 || payload.SourceCounts["cron"] < 2 {
		t.Fatalf("payload counts = count %d sources %#v, want ad-hoc plus materialized/projected cron events", payload.Count, payload.SourceCounts)
	}

	adHoc := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.TestID == adHocTest.ID
	})
	if adHoc == nil {
		t.Fatalf("events = %#v, want ad-hoc test event", payload.Events)
	}
	if adHoc.Source != "ad_hoc" || adHoc.User.DisplayName != "Dana Runner" || adHoc.Topology.Source != "live" || len(adHoc.Topology.GeneratorShards) != 1 {
		t.Fatalf("ad-hoc event = %#v, want ad-hoc live topology and runner attribution", adHoc)
	}

	materialized := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.ScheduleID == schedule.ID && event.TestID == scheduledTest.ID
	})
	if materialized == nil {
		t.Fatalf("events = %#v, want materialized schedule occurrence", payload.Events)
	}
	if materialized.Source != "cron" || materialized.Projected || materialized.Status != "COMPLETE" || materialized.User.DisplayName != "Scheduler Owner" {
		t.Fatalf("materialized event = %#v, want cron materialized event with schedule owner", materialized)
	}

	projected := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.ScheduleID == schedule.ID && event.Projected
	})
	if projected == nil {
		t.Fatalf("events = %#v, want projected future schedule event", payload.Events)
	}
	if projected.Source != "cron" || projected.Status != "scheduled" || projected.Workload.TotalVUsers != 25 || projected.Topology.Source != "planned" {
		t.Fatalf("projected event = %#v, want scheduled projection with scenario workload", projected)
	}
}

func TestHandleRunCalendarProjectsBlockedScheduleAsBlocked(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-blocked-calendar",
		Name:      "Blocked Checkout",
		ProjectID: "project-calendar",
		Protocol:  "http",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://checkout.example.test"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/checkout"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Blocked Scenario",
		Duration: "10m",
		Groups:   []CreateGroupEntry{{Name: "buyers", ScriptID: "script-blocked-calendar", VUsers: 5}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	// A non-auto approval policy leaves NextRequiredAction == "approval_required",
	// so the schedule will not materialize as-is. It must still be projected on
	// the calendar, tagged blocked, rather than disappearing.
	schedule, err := server.runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name:           "Approval-Gated Hourly",
		CronExpr:       "0 * * * *",
		ApprovalPolicy: "manual",
	})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}

	now := time.Now().UTC()
	values := url.Values{}
	values.Set("from", now.Add(-time.Hour).Format(time.RFC3339))
	values.Set("to", now.Add(6*time.Hour).Format(time.RFC3339))
	values.Set("project_id", "project-calendar")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/run-calendar?"+values.Encode(), nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload RunCalendarResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	blocked := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.ScheduleID == schedule.ID && event.Projected
	})
	if blocked == nil {
		t.Fatalf("events = %#v, want blocked schedule still projected", payload.Events)
	}
	if blocked.Status != "blocked" || blocked.OccurrenceStatus != "blocked" || blocked.ReasonCode != "approval_required" {
		t.Fatalf("blocked event = %#v, want status/occurrence blocked with approval_required reason", blocked)
	}
}

func TestHandleRunCalendarClampsRetentionAndFutureRange(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.ArchiveRetention.Enabled = true
	settings.ArchiveRetention.DefaultRetentionDays = 7
	settings.ArchiveRetention.Policies = []ArchiveRetentionPolicy{{
		Name:          "regulated-project",
		ProjectID:     "project-calendar",
		RetentionDays: 30,
	}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	now := time.Now().UTC()
	values := url.Values{}
	values.Set("from", now.Add(-90*24*time.Hour).Format(time.RFC3339))
	values.Set("to", now.Add(400*24*time.Hour).Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/run-calendar?"+values.Encode(), nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload RunCalendarResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !payload.Range.RetentionEnforced || !payload.Range.HistoryClamped || !payload.Range.FutureClamped || payload.Range.HistoryFloor == nil {
		t.Fatalf("range = %#v, want retention and future clamping", payload.Range)
	}
	if payload.Range.From.Before(*payload.Range.HistoryFloor) {
		t.Fatalf("range from = %s before history floor %s", payload.Range.From, payload.Range.HistoryFloor)
	}
	if payload.Range.To.After(payload.Range.FutureCeiling) {
		t.Fatalf("range to = %s after future ceiling %s", payload.Range.To, payload.Range.FutureCeiling)
	}
}

func findRunCalendarEvent(events []RunCalendarEvent, match func(RunCalendarEvent) bool) *RunCalendarEvent {
	for i := range events {
		if match(events[i]) {
			return &events[i]
		}
	}
	return nil
}
