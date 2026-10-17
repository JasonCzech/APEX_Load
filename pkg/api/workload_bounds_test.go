package api

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func workloadBoundsRunner(t *testing.T) *TestRunner {
	t.Helper()
	runner := NewTestRunner(2, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "workload-bounds-script",
		Name:     "Workload bounds",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	return runner
}

func TestCompileManagedTestGroupsEnforcesHardWorkloadBounds(t *testing.T) {
	runner := workloadBoundsRunner(t)
	base := CreateGroupEntry{ScriptID: "workload-bounds-script", VUsers: 1}

	tests := []struct {
		name   string
		groups []CreateGroupEntry
		want   string
	}{
		{
			name:   "too many groups",
			groups: make([]CreateGroupEntry, maxManagedTestGroups+1),
			want:   "groups; limit",
		},
		{
			name:   "negative vusers",
			groups: []CreateGroupEntry{{ScriptID: base.ScriptID, VUsers: -1}},
			want:   "zero or greater",
		},
		{
			name:   "per-group vusers",
			groups: []CreateGroupEntry{{ScriptID: base.ScriptID, VUsers: maxManagedTestVUsers + 1}},
			want:   "cannot exceed",
		},
		{
			name: "aggregate vusers",
			groups: []CreateGroupEntry{
				{ScriptID: base.ScriptID, VUsers: maxManagedTestVUsers/2 + 1},
				{ScriptID: base.ScriptID, VUsers: maxManagedTestVUsers/2 + 1},
			},
			want: "hard workload limit",
		},
		{
			name: "too many stages",
			groups: []CreateGroupEntry{{
				ScriptID: base.ScriptID,
				VUsers:   1,
				Stages:   make([]storage.LoadStage, maxManagedLoadStagesPerGroup+1),
			}},
			want: "stages; limit",
		},
		{
			name: "stage target",
			groups: []CreateGroupEntry{{
				ScriptID: base.ScriptID,
				VUsers:   1,
				Stages: []storage.LoadStage{{
					Type: "hold", Duration: "1s", TargetVUsers: maxManagedTestVUsers + 1,
				}},
			}},
			want: "cannot exceed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := runner.compileManagedTestGroupsLocked(tt.groups, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("compile error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadStageCompatibilityRejectsDurationOverflow(t *testing.T) {
	_, _, _, err := loadStageCompatibility([]storage.LoadStage{
		{Type: "ramp_up", Duration: time.Duration(math.MaxInt64).String(), TargetVUsers: 1},
		{Type: "ramp_up", Duration: "1ns", TargetVUsers: 1},
	}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "cumulative ramp_up duration") {
		t.Fatalf("loadStageCompatibility error = %v, want cumulative duration overflow", err)
	}
}

func TestValidateScenarioRequestEnforcesGoalBoundsAndNewSLAThresholds(t *testing.T) {
	tooMany := maxManagedTestVUsers + 1
	notFinite := math.Inf(1)
	negative := -1.0
	for _, config := range []*GoalConfig{
		{MaxVUsers: &tooMany},
		{TargetAverageTPS: &notFinite},
		{MaxEndToEndLatencyMs: &negative},
		{MaxPartitionSkewPct: &notFinite},
	} {
		if _, err := validateScenarioRequest(CreateTestRequest{Duration: "1s", ScenarioMode: "goal_oriented", GoalConfig: config}); err == nil {
			t.Fatalf("validateScenarioRequest(%+v) unexpectedly succeeded", config)
		}
	}

	lcp := 2500.0
	if _, err := validateScenarioRequest(CreateTestRequest{
		Duration:     "1s",
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{MaxLCPMs: &lcp, StopOnSLABreach: true},
	}); err != nil {
		t.Fatalf("newer SLA-only goal rejected: %v", err)
	}
}

func TestScaleRampRejectsHardVUserLimitBeforeStartingWork(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	_, err := runner.startVUserScaleRamp(context.Background(), "missing", "group", maxManagedTestVUsers+1, time.Second)
	if err == nil || !strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("startVUserScaleRamp error = %v, want hard-limit rejection", err)
	}
}

func TestValidateManagedTestWorkloadBoundsRejectsLegacyOversizeState(t *testing.T) {
	test := &ManagedTest{
		TotalVUsers: maxManagedTestVUsers + 1,
		Groups:      []TestGroup{{Name: "load", VUsers: maxManagedTestVUsers + 1}},
	}
	if err := validateManagedTestWorkloadBounds(test); err == nil {
		t.Fatal("validateManagedTestWorkloadBounds unexpectedly accepted oversized persisted state")
	}
}
