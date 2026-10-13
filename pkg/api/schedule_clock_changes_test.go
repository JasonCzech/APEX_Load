package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

var clockChangesFrom = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func clockChangesWithin(t *testing.T, cronExpr, timezone string, allows func(time.Time) (bool, error)) []ScheduleClockChange {
	t.Helper()
	return scheduleClockChanges(cronExpr, timezone, clockChangesFrom, clockChangesFrom.Add(scheduleClockChangeMinHorizon), allows)
}

// A 02:30 on the spring-forward day does not exist and is skipped, which
// is why such a yearly schedule next runs a year later.
func TestScheduleClockChangesNamesSkippedSpringForwardTimes(t *testing.T) {
	changes := clockChangesWithin(t, "30 2 14 3 *", "America/Chicago", nil)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	change := changes[0]
	if change.Kind != "skipped" || change.Date != "2027-03-14" || !reflect.DeepEqual(change.LocalTimes, []string{"02:30"}) || change.Matches != 1 || change.RunsAt != nil {
		t.Fatalf("change = %+v, want 2:30 AM on 2027-03-14 skipped", change)
	}
	if want := time.Date(2027, 3, 14, 8, 0, 0, 0, time.UTC); !change.At.Equal(want) {
		t.Fatalf("change at %s, want %s", change.At, want)
	}
}

// A 01:30 on the fall-back day happens twice and runs once, at the first
// (CDT) one.
func TestScheduleClockChangesNamesRepeatedFallBackTimes(t *testing.T) {
	changes := clockChangesWithin(t, "30 1 * * *", "America/Chicago", nil)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the November fall-back only", changes)
	}
	change := changes[0]
	if change.Kind != "repeated" || change.Date != "2026-11-01" || !reflect.DeepEqual(change.LocalTimes, []string{"01:30"}) || change.Matches != 1 {
		t.Fatalf("change = %+v, want 1:30 AM on 2026-11-01 repeated", change)
	}
	if want := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC); change.RunsAt == nil || !change.RunsAt.Equal(want) {
		t.Fatalf("runs at %v, want %s (1:30 AM CDT)", change.RunsAt, want)
	}
}

// A schedule with several times in the skipped hour counts them all and
// names the first few; one whose times avoid the changes, or a zone without
// them, names none.
func TestScheduleClockChangesCountsAndBounds(t *testing.T) {
	changes := clockChangesWithin(t, "*/10 2 * * *", "America/Chicago", nil)
	if len(changes) != 1 || changes[0].Kind != "skipped" || changes[0].Matches != 6 || !reflect.DeepEqual(changes[0].LocalTimes, []string{"02:00", "02:10", "02:20"}) {
		t.Fatalf("changes = %+v, want six skipped times, the first three named", changes)
	}
	for _, schedule := range []struct{ cron, zone string }{{"0 9 * * 1-5", "America/Chicago"}, {"30 2 * * *", "UTC"}, {"30 2 * * *", "Asia/Kolkata"}, {"not a cron", "America/Chicago"}} {
		if changes := clockChangesWithin(t, schedule.cron, schedule.zone, nil); len(changes) != 0 {
			t.Fatalf("%s in %s: changes = %+v, want none", schedule.cron, schedule.zone, changes)
		}
	}
	// Times the schedule would not run anyway (a pause) are not named.
	paused := func(time.Time) (bool, error) { return false, nil }
	if changes := clockChangesWithin(t, "30 1 * * *", "America/Chicago", paused); len(changes) != 0 {
		t.Fatalf("paused changes = %+v, want none", changes)
	}
}

// The horizon reaches the schedule's next run when that is more than a
// year away, so the skipped date that pushed it there is named.
func TestScheduleClockChangeHorizonReachesNextRun(t *testing.T) {
	from := time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC)
	next := time.Date(2029, 3, 14, 7, 30, 0, 0, time.UTC)
	if got := scheduleClockChangeHorizon(from, &next); !got.Equal(next) {
		t.Fatalf("horizon = %s, want the next run %s", got, next)
	}
	if got := scheduleClockChangeHorizon(from, nil); !got.Equal(from.Add(scheduleClockChangeMinHorizon)) {
		t.Fatalf("horizon = %s, want a year", got)
	}
	changes := scenarioScheduleClockChanges([]*storage.ScenarioSchedule{
		{ID: "yearly", CronExpr: "30 2 14 3 *", Timezone: "America/Chicago", NextRunAt: &next},
		{ID: "weekday", CronExpr: "0 9 * * 1-5", Timezone: "America/Chicago"},
	}, from)
	// 14 March is the spring-forward day in 2027 and 2032; from 15 March
	// 2027 to the 2029 run there is none, so nothing is named.
	if len(changes) != 0 {
		t.Fatalf("changes = %+v, want none before the 2029 run", changes)
	}
	changes = scenarioScheduleClockChanges([]*storage.ScenarioSchedule{{ID: "yearly", CronExpr: "30 2 14 3 *", Timezone: "America/Chicago"}}, clockChangesFrom)
	if len(changes["yearly"]) != 1 || changes["yearly"][0].Date != "2027-03-14" {
		t.Fatalf("changes = %+v, want the 2027 skip for the yearly schedule", changes)
	}
	planChanges := executionPlanScheduleClockChanges([]*storage.ExecutionPlanSchedule{{ID: "nightly", CronExpr: "30 1 * * *", Timezone: "America/Chicago"}}, clockChangesFrom)
	if len(planChanges["nightly"]) != 1 || planChanges["nightly"][0].Kind != "repeated" {
		t.Fatalf("plan changes = %+v, want the repeated 1:30 AM", planChanges)
	}
}

// Both schedule lists carry each schedule's clock changes by schedule ID:
// within any year a Chicago 01:30 schedule meets a fall-back night on which
// 01:30 happens twice.
func TestScheduleListsCarryClockChanges(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveExecutionPlan(&storage.ExecutionPlan{ID: "plan-clock", ProjectID: "proj-a", Name: "Plan", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/plan-clock/schedules", map[string]any{"name": "Nightly", "cron_expr": "30 1 * * *", "timezone": "America/Chicago"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create plan schedule = %d body=%s", create.Code, create.Body.String())
	}
	var planSchedule storage.ExecutionPlanSchedule
	_ = json.Unmarshal(create.Body.Bytes(), &planSchedule)
	planList := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/plan-clock/schedules", nil)
	var planListed struct {
		ClockChanges map[string][]ScheduleClockChange `json:"clock_changes"`
	}
	if err := json.Unmarshal(planList.Body.Bytes(), &planListed); err != nil || planList.Code != http.StatusOK {
		t.Fatalf("plan list = %d %s (%v)", planList.Code, planList.Body.String(), err)
	}
	// A year can hold two fall-back nights when they are 364 days apart.
	if changes := planListed.ClockChanges[planSchedule.ID]; len(changes) == 0 || changes[0].Kind != "repeated" || changes[0].RunsAt == nil {
		t.Fatalf("plan clock changes = %+v, want the repeated 01:30", planListed.ClockChanges)
	}

	if _, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "script-clock", Name: "Clock", ProjectID: "proj-a", Protocol: "http", Version: "1.0.0", Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}}}); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Clock", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-clock", VUsers: 1}}})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	scenarioCreate := timeslotRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", map[string]any{"name": "Nightly", "cron_expr": "30 1 * * *", "timezone": "America/Chicago"})
	if scenarioCreate.Code != http.StatusCreated {
		t.Fatalf("create scenario schedule = %d body=%s", scenarioCreate.Code, scenarioCreate.Body.String())
	}
	var scenarioSchedule storage.ScenarioSchedule
	_ = json.Unmarshal(scenarioCreate.Body.Bytes(), &scenarioSchedule)
	scenarioList := timeslotRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/schedules", nil)
	var scenarioListed struct {
		ClockChanges map[string][]ScheduleClockChange `json:"clock_changes"`
	}
	if err := json.Unmarshal(scenarioList.Body.Bytes(), &scenarioListed); err != nil || scenarioList.Code != http.StatusOK {
		t.Fatalf("scenario list = %d %s (%v)", scenarioList.Code, scenarioList.Body.String(), err)
	}
	if changes := scenarioListed.ClockChanges[scenarioSchedule.ID]; len(changes) == 0 || changes[0].Kind != "repeated" {
		t.Fatalf("scenario clock changes = %+v, want the repeated 01:30", scenarioListed.ClockChanges)
	}
}
