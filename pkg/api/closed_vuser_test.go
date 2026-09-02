package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestValidateClosedVUserWorkloadShapeConfig(t *testing.T) {
	valid := ClosedVUserDefinition{PeakVUsers: 5, RampUp: "2m", Sustain: "65m", RampDown: "2m"}
	tests := []struct {
		name         string
		config       *WorkloadShapeConfig
		scenarioMode string
		groups       []CreateGroupEntry
		wantErr      string
	}{
		{name: "omitted"},
		{name: "scenario", scenarioMode: "vuser_driven", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &valid}}},
		{name: "group", scenarioMode: "vuser_driven", groups: []CreateGroupEntry{{}, {}}, config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "group", Groups: map[string]ClosedVUserDefinition{"group_0": valid, "group_1": valid}}}},
		{name: "goal mode", scenarioMode: "goal_oriented", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &valid}}, wantErr: "requires scenario_mode vuser_driven"},
		{name: "conflicting shapes", scenarioMode: "vuser_driven", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &valid}, SteppedRamp: &SteppedRampConfig{Scope: "scenario"}}, wantErr: "cannot contain both"},
		{name: "missing group definition", scenarioMode: "vuser_driven", groups: []CreateGroupEntry{{}, {}}, config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "group", Groups: map[string]ClosedVUserDefinition{"group_0": valid}}}, wantErr: "exactly one definition"},
		{name: "wrong group key", scenarioMode: "vuser_driven", groups: []CreateGroupEntry{{}}, config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "group", Groups: map[string]ClosedVUserDefinition{"checkout": valid}}}, wantErr: "groups.group_0 is required"},
		{name: "missing scenario definition", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario"}}, wantErr: "scenario is required"},
		{name: "missing group definitions", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "group"}}, wantErr: "groups is required"},
		{name: "zero peak", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{RampUp: "2m", Sustain: "65m"}}}, wantErr: "peak_vusers"},
		{name: "zero ramp up", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 5, RampUp: "0s", Sustain: "65m"}}}, wantErr: "ramp_up must be a positive duration"},
		{name: "zero sustain", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 5, RampUp: "2m", Sustain: "0s"}}}, wantErr: "sustain must be a positive duration"},
		{name: "enabled ramp down requires duration", config: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 5, RampUp: "2m", Sustain: "65m", RampDownEnabled: true}}}, wantErr: "ramp_down must be a positive duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.scenarioMode
			if mode == "" {
				mode = "vuser_driven"
			}
			err := validateWorkloadShapeConfig(tt.config, mode, tt.groups)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateWorkloadShapeConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateWorkloadShapeConfig() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestClosedVUserWithoutRampDownStopsGroupAfterSustain(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 4)}
	testRun := &ManagedTest{
		ID: "closed-stop", Status: "RUNNING", executionBackend: backend,
		GoalConfig: &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{
			Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 5, RampUp: "1ms", Sustain: "1ms"},
		}}},
		Groups: []TestGroup{{Name: "checkout", VUsers: 5}},
	}
	runner.tests[testRun.ID] = testRun
	runner.runStageSchedule(context.Background(), testRun.ID, "checkout", []storage.LoadStage{{Type: "hold", Duration: "1ms", TargetVUsers: 5}}, 0, nil)

	var calls []scaleBackendCall
	for len(backend.calls) > 0 {
		calls = append(calls, <-backend.calls)
	}
	if len(calls) < 1 || calls[len(calls)-1].target != 0 {
		t.Fatalf("scale calls = %+v, want final target 0", calls)
	}
	if testRun.Groups[0].ActiveStage != nil {
		t.Fatalf("Closed VUser phase leaked into discrete load levels: %+v", testRun.Groups[0].ActiveStage)
	}
}

func TestClosedVUserEarlyStopReasonUsesActualGroupLoad(t *testing.T) {
	reason := earlyStopReason(&LiveMetrics{ErrorPct: 6, GroupMetrics: map[string]*GroupLiveMetrics{
		"checkout": {Name: "checkout", VUsers: 3},
		"search":   {Name: "search", VUsers: 2},
	}}, EarlyStopConfig{ErrorRatePct: 5, BreachWindow: "1m"})
	if !strings.Contains(reason, "checkout (3 VUsers), search (2 VUsers)") {
		t.Fatalf("early stop reason = %q", reason)
	}
}

func TestCloneGoalConfigDeepCopiesClosedVUserIntent(t *testing.T) {
	original := &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{
		Scope: "group", Groups: map[string]ClosedVUserDefinition{"group_0": {PeakVUsers: 5, RampUp: "2m", Sustain: "65m", RampDown: "2m"}},
	}}}
	clone := cloneGoalConfig(original)
	definition := clone.WorkloadShapeConfig.ClosedVUser.Groups["group_0"]
	definition.PeakVUsers = 20
	clone.WorkloadShapeConfig.ClosedVUser.Groups["group_0"] = definition
	if original.WorkloadShapeConfig.ClosedVUser.Groups["group_0"].PeakVUsers != 5 {
		t.Fatal("cloneGoalConfig aliased Closed VUser group definitions")
	}
}

func TestClosedVUserRampDownPhaseDoesNotPublishMeasurementLevel(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	testRun := &ManagedTest{ID: "closed-levels", Status: "RUNNING", GoalConfig: &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 5, RampUp: "2m", Sustain: "65m", RampDownEnabled: true, RampDown: "2m"}}}}, Groups: []TestGroup{{Name: "checkout"}}}
	runner.tests[testRun.ID] = testRun
	runner.setActiveLoadStage(testRun.ID, "checkout", 2, storage.LoadStage{Type: "ramp_down", Duration: "2m", TargetVUsers: 0})
	if testRun.Groups[0].ActiveStage != nil {
		t.Fatal("continuous ramp-down was published as a discrete measurement level")
	}
}
