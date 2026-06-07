package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func storePreflightScript(runner *TestRunner, id string, projectID string) {
	runner.StoreScript(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
}

func TestPreflightLaunchAllowsImmediateStartWhenCapacityAvailable(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-start", "")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Start Now",
			Duration: "5m",
			ScriptID: "script-preflight-start",
			VUsers:   10,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.QueueAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want start-now decision", preflight)
	}
}

func TestPreflightLaunchQueuesWhenCurrentControllerCapacityUnavailable(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-queue", "")

	active, err := runner.CreateTest(CreateTestRequest{Name: "Active", Duration: "30m", ScriptID: "script-preflight-queue", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(active) returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Queued",
			Duration: "5m",
			ScriptID: "script-preflight-queue",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || !preflight.QueueAllowed || preflight.Decision != "queue" {
		t.Fatalf("preflight = %#v, want queue decision", preflight)
	}
	if len(preflight.BlockingConflicts) != 0 || len(preflight.Warnings) == 0 {
		t.Fatalf("preflight conflicts = blocking %#v warnings %#v, want queue warning only", preflight.BlockingConflicts, preflight.Warnings)
	}
}

func TestPreflightLaunchBlocksFutureCronCapacityOverlap(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-cron", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	settings.CapacityPlanning.HeadroomPct = 0
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Cron Scenario",
		Duration: "10m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-preflight-cron", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	if _, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "Every Minute", CronExpr: "* * * * *"}); err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Overlapping Ad Hoc",
			Duration: "10m",
			ScriptID: "script-preflight-cron",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || preflight.QueueAllowed || len(preflight.BlockingConflicts) == 0 {
		t.Fatalf("preflight = %#v, want blocking cron capacity conflict", preflight)
	}
	if preflight.BlockingConflicts[0].Code != "generator_capacity_window" {
		t.Fatalf("conflict = %#v, want generator_capacity_window", preflight.BlockingConflicts[0])
	}
}

func TestPreflightLaunchBlocksReservationStartingMidRun(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-reservation", "")
	now := time.Now().UTC()
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	settings.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID:          "project-b",
		ReservedGenerators: 2,
		StartsAt:           ptr(now.Add(2 * time.Minute)),
		EndsAt:             ptr(now.Add(30 * time.Minute)),
	}}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:      "Reservation Conflict",
			Duration:  "10m",
			ProjectID: "project-a",
			ScriptID:  "script-preflight-reservation",
			VUsers:    1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "reservation_window" {
		t.Fatalf("preflight conflicts = %#v, want reservation_window", preflight.BlockingConflicts)
	}
}

func TestPreflightLaunchBlocksMaintenanceWindow(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-preflight-maintenance", storage.DefaultProjectID)
	now := time.Now().UTC()
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Maintenance Scenario",
		Duration: "5m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-preflight-maintenance", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	schedule := &storage.ScenarioSchedule{
		ID:                 "schedule-preflight-maintenance",
		ScenarioID:         scenario.ID,
		ProjectID:          storage.DefaultProjectID,
		Name:               "Maintenance",
		CronExpr:           "* * * * *",
		Timezone:           "UTC",
		Enabled:            true,
		MaintenanceWindows: []storage.ScenarioScheduleWindow{{Name: "shared", DaysOfWeek: []int{int(now.Weekday())}, StartTime: now.Add(-time.Minute).Format("15:04"), EndTime: now.Add(20 * time.Minute).Format("15:04"), Timezone: "UTC"}},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = append(runner.scenarios[scenario.ID].Schedules, schedule)
	runner.mu.Unlock()

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:       "Maintenance Conflict",
			Duration:   "5m",
			ScenarioID: scenario.ID,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "maintenance_window" {
		t.Fatalf("preflight conflicts = %#v, want maintenance_window", preflight.BlockingConflicts)
	}
}

func TestCreateTestWithScheduledStartPersistsScheduledStatus(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-preflight-scheduled", "")
	scheduledAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:             "Future Run",
		Duration:         "5m",
		ScriptID:         "script-preflight-scheduled",
		VUsers:           1,
		ScheduledStartAt: &scheduledAt,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if testRun.Status != "SCHEDULED" || testRun.ScheduledStartAt == nil || !testRun.ScheduledStartAt.Equal(scheduledAt) {
		t.Fatalf("testRun = %#v, want scheduled run", testRun)
	}
	if len(store.savedTests) == 0 || store.savedTests[len(store.savedTests)-1].ScheduledStartAt == nil {
		t.Fatalf("saved tests = %#v, want scheduled_start_at persisted", store.savedTests)
	}
}

func TestScenarioSchedulerStartsDueOneTimeScheduledRun(t *testing.T) {
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner := NewTestRunner(10, NewHub(), nil)
	runner.execution = backend
	storePreflightScript(runner, "script-preflight-due", "")
	now := time.Now().UTC()
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:             "Due Run",
		Duration:         "1ms",
		ScriptID:         "script-preflight-due",
		VUsers:           1,
		ScheduledStartAt: ptr(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[testRun.ID].Status = "SCHEDULED"
	runner.tests[testRun.ID].ScheduledStartAt = ptr(now.Add(-time.Minute))
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case startedID := <-backend.started:
		if startedID != testRun.ID {
			t.Fatalf("started id = %s, want %s", startedID, testRun.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for due scheduled run to start")
	}
}
