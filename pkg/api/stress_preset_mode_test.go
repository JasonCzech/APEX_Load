package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The dashboard's Stress preset authors three ramp/hold escalation levels and
// historically paired them with scenario_mode goal_oriented, max_vusers and p95
// and error thresholds, but no target TPS. These tests pin what the runtime does
// with that combination so the preset can be judged against real behaviour.
func stressEscalationStages() []storage.LoadStage {
	return []storage.LoadStage{
		{Name: "Ramp 1", Type: "ramp_up", Duration: "3m", TargetVUsers: 99},
		{Name: "Hold 1", Type: "hold", Duration: "4m", TargetVUsers: 99},
		{Name: "Ramp 2", Type: "ramp_up", Duration: "3m", TargetVUsers: 198},
		{Name: "Hold 2", Type: "hold", Duration: "4m", TargetVUsers: 198},
		{Name: "Ramp 3", Type: "ramp_up", Duration: "3m", TargetVUsers: 300},
		{Name: "Hold 3", Type: "hold", Duration: "3m", TargetVUsers: 300},
	}
}

func TestStressEscalationStagesOnlyScheduleInVUserDrivenMode(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-stress",
		Name:     "Stress Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}},
	})
	entry := CreateGroupEntry{ScriptID: "script-stress", Stages: stressEscalationStages()}

	// goal_oriented: the stages are kept on the group but are not a schedule.
	// They collapse to one linear ramp (the summed ramp_up durations) to the
	// staged peak, so the hold levels never run as distinct load steps.
	goalGroups, goalEngine, _, err := runner.compileManagedTestGroupsForModeWithDataSourcesLocked([]CreateGroupEntry{entry}, nil, nil, "goal_oriented")
	if err != nil {
		t.Fatalf("compile goal_oriented stress group: %v", err)
	}
	if goalGroups[0].stageDriven {
		t.Fatal("goal_oriented stress group is stage driven; startStageSchedules only runs vuser_driven groups")
	}
	if len(goalGroups[0].Stages) != 6 {
		t.Fatalf("goal_oriented stages = %d, want the authored stages preserved", len(goalGroups[0].Stages))
	}
	if goalEngine[0].VUsers != 300 || goalEngine[0].RampUp != 9*time.Minute {
		t.Fatalf("goal_oriented engine group = vusers %d ramp %s, want a single 9m ramp straight to 300", goalEngine[0].VUsers, goalEngine[0].RampUp)
	}

	// vuser_driven: the same stages drive the engine from zero, level by level.
	vuserGroups, vuserEngine, _, err := runner.compileManagedTestGroupsForModeWithDataSourcesLocked([]CreateGroupEntry{entry}, nil, nil, "vuser_driven")
	if err != nil {
		t.Fatalf("compile vuser_driven stress group: %v", err)
	}
	if !vuserGroups[0].stageDriven {
		t.Fatal("vuser_driven stress group is not stage driven")
	}
	if vuserEngine[0].VUsers != 0 || vuserEngine[0].RampUp != 0 || !vuserEngine[0].AllowZeroVUsers {
		t.Fatalf("vuser_driven engine group = vusers %d ramp %s allowZero %v, want stage scheduler ownership from zero",
			vuserEngine[0].VUsers, vuserEngine[0].RampUp, vuserEngine[0].AllowZeroVUsers)
	}
}

func TestGoalControllerWithoutTargetTPSOnlyReportsStressGuardrails(t *testing.T) {
	maxVUsers := 300
	p95 := 1000.0
	maxErrorPct := 2.0
	config := &GoalConfig{MaxVUsers: &maxVUsers, P95LatencyMs: &p95, MaxErrorPct: &maxErrorPct}

	if _, err := validateScenarioRequest(CreateTestRequest{ScenarioMode: "goal_oriented", Duration: "20m", GoalConfig: config}); err != nil {
		t.Fatalf("goal_oriented without target TPS was rejected: %v", err)
	}
	if targets := defaultGoalTargetsByGroup(&ManagedTest{Groups: []TestGroup{{Name: "stress", VUsers: 300}}, GoalConfig: config}); targets != nil {
		t.Fatalf("default goal targets = %#v, want none without target_average_tps", targets)
	}

	// Throughput far below anything, latency and errors far over their limits:
	// the controller reports breaches but neither scales nor stops, even after a
	// long breach, because there is no TPS target and stop_on_sla_breach is unset.
	state := &goalControllerState{}
	start := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	live := &LiveMetrics{TPS: 1, P95Ms: 5000, ErrorPct: 50}
	for _, at := range []time.Time{start, start.Add(10 * time.Minute)} {
		update := evaluateGoalController(at, 99, live, config, state)
		if update.ShouldScale || update.ShouldStop {
			t.Fatalf("update at %s = scale:%v stop:%v, want neither", at.Sub(start), update.ShouldScale, update.ShouldStop)
		}
		if !update.Status.LatencyBreached || !update.Status.ErrorBreached {
			t.Fatalf("status = %#v, want latency and error breaches reported", update.Status)
		}
	}

	// The same thresholds only end the run when the operator opts in.
	stopping := *config
	stopping.StopOnSLABreach = true
	stopping.SLABreachWindow = "1m"
	stopState := &goalControllerState{}
	evaluateGoalController(start, 99, live, &stopping, stopState)
	if update := evaluateGoalController(start.Add(2*time.Minute), 99, live, &stopping, stopState); !update.ShouldStop {
		t.Fatal("stop_on_sla_breach did not stop a sustained breach")
	}
}
