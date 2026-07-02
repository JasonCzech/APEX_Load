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

func TestHandleRunCalendarProjectsExecutionPlanSchedules(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-plan-calendar",
		Name:      "Plan Checkout",
		ProjectID: "project-calendar",
		Protocol:  "http",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://checkout.example.test"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/checkout"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Plan Scenario",
		Duration: "20m",
		Groups:   []CreateGroupEntry{{Name: "buyers", ScriptID: "script-plan-calendar", VUsers: 12}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	plan := &storage.ExecutionPlan{
		ID:        "exec-plan-calendar",
		ProjectID: "project-calendar",
		Name:      "Nightly Plan",
		RunMode:   "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan returned error: %v", err)
	}
	schedule, err := server.runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{
		Name:     "Nightly Plan Schedule",
		CronExpr: "0 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule returned error: %v", err)
	}

	now := time.Now().UTC()
	// A materialized occurrence in the recent past (top of a prior hour, within range).
	plannedAt := now.Truncate(time.Hour).Add(-2 * time.Hour)
	materializedAt := plannedAt.Add(time.Minute)
	if err := store.SaveExecutionPlanScheduleOccurrence(&storage.ExecutionPlanScheduleOccurrence{
		ID:             "plan-occ-calendar",
		ScheduleID:     schedule.ID,
		PlanID:         plan.ID,
		ProjectID:      "project-calendar",
		PlannedAt:      plannedAt,
		MaterializedAt: &materializedAt,
		Status:         "materialized",
		ReasonCode:     "started",
		RunID:          "epr-calendar",
		CreatedAt:      plannedAt,
		UpdatedAt:      materializedAt,
	}); err != nil {
		t.Fatalf("SaveExecutionPlanScheduleOccurrence returned error: %v", err)
	}

	values := url.Values{}
	values.Set("from", now.Add(-3*time.Hour).Format(time.RFC3339))
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
	if payload.SourceCounts["execution_plan"] < 2 {
		t.Fatalf("source counts = %#v, want >=2 execution_plan events (materialized + projected)", payload.SourceCounts)
	}

	occurrence := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.PlanID == plan.ID && !event.Projected
	})
	if occurrence == nil {
		t.Fatalf("events = %#v, want materialized plan occurrence", payload.Events)
	}
	if occurrence.Source != "execution_plan" || occurrence.Status != "materialized" || occurrence.ScheduleID != schedule.ID {
		t.Fatalf("plan occurrence event = %#v, want materialized execution_plan event", occurrence)
	}

	projected := findRunCalendarEvent(payload.Events, func(event RunCalendarEvent) bool {
		return event.PlanID == plan.ID && event.Projected
	})
	if projected == nil {
		t.Fatalf("events = %#v, want projected plan run", payload.Events)
	}
	if projected.Source != "execution_plan" || projected.Status != "scheduled" || projected.CronExpr != "0 * * * *" || projected.Workload.TotalVUsers != 12 || projected.Topology.Source != "planned" {
		t.Fatalf("projected plan event = %#v, want scheduled projection with aggregate workload", projected)
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

func TestBuildRunCalendarCapacityOverlay(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	resStart := day.Add(9 * time.Hour)
	resEnd := day.Add(17 * time.Hour)
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "proj-a", ReservedGenerators: 9, StartsAt: &resStart, EndsAt: &resEnd},
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	capacity := server.runner.buildRunCalendarCapacity(day.Add(-24*time.Hour), day.Add(48*time.Hour), "proj-a", "UTC")
	if capacity == nil || len(capacity.Days) == 0 {
		t.Fatal("expected capacity days")
	}
	byDate := map[string]RunCalendarCapacityDay{}
	for _, d := range capacity.Days {
		byDate[d.Date] = d
	}
	target, ok := byDate["2026-08-10"]
	if !ok {
		t.Fatalf("missing 2026-08-10 day in %+v", capacity.Days)
	}
	if target.PeakReservedGenerators != 9 {
		t.Fatalf("peak reserved = %d, want 9", target.PeakReservedGenerators)
	}
	if target.Pressure != "tight" {
		t.Fatalf("pressure = %q (pct %.2f), want tight", target.Pressure, target.PressurePct)
	}
	if clearDay, ok := byDate["2026-08-09"]; !ok || clearDay.Pressure != "clear" || clearDay.PeakReservedGenerators != 0 {
		t.Fatalf("2026-08-09 = %+v, want clear with 0 reserved", clearDay)
	}

	// The overlay is opt-in: absent without ?capacity=true, present with it.
	base := httptest.NewRequest(http.MethodGet, "/api/v1/run-calendar", nil)
	baseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(baseResp, base)
	var without RunCalendarResponse
	if err := json.Unmarshal(baseResp.Body.Bytes(), &without); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if without.Capacity != nil {
		t.Fatal("capacity should be omitted without ?capacity=true")
	}
	withReq := httptest.NewRequest(http.MethodGet, "/api/v1/run-calendar?capacity=true", nil)
	withResp := httptest.NewRecorder()
	server.mux.ServeHTTP(withResp, withReq)
	var with RunCalendarResponse
	if err := json.Unmarshal(withResp.Body.Bytes(), &with); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if with.Capacity == nil {
		t.Fatal("capacity should be present with ?capacity=true")
	}
}

func TestBuildRunCalendarCapacityMarksZeroCapacityReservationsOver(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	resStart := day.Add(9 * time.Hour)
	resEnd := day.Add(17 * time.Hour)
	server.runner.mu.Lock()
	server.runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 0
	server.runner.settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "proj-a", ReservedGenerators: 8, StartsAt: &resStart, EndsAt: &resEnd},
	}
	server.runner.mu.Unlock()
	capacity := server.runner.buildRunCalendarCapacity(day, day.Add(24*time.Hour), "proj-a", "UTC")
	if capacity == nil || len(capacity.Days) != 1 {
		t.Fatalf("capacity days = %+v, want one day", capacity)
	}
	got := capacity.Days[0]
	if got.PeakReservedGenerators != 8 || got.Pressure != "over" || got.PressurePct <= 0 {
		t.Fatalf("capacity day = %+v, want over pressure for 8 reserved / 0 capacity", got)
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
