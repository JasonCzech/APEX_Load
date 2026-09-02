package api

import (
	"strings"
	"testing"
)

func TestValidateGoalDrivenWorkloadShapeConfig(t *testing.T) {
	target := 30.0
	valid := &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{
		"group_0": {TargetTPS: 10, ART: &GoalDrivenARTConfig{Basis: "manual", ARTMs: 400}},
		"group_1": {TargetTPS: 20, ART: &GoalDrivenARTConfig{Basis: "baseline", ARTMs: 800, BaselineTestID: "run-1", BaselineGroup: "Search"}},
	}}}
	tests := []struct {
		name   string
		config *WorkloadShapeConfig
		mode   string
		target *float64
		want   string
	}{
		{name: "valid", config: valid, mode: "goal_oriented", target: &target},
		{name: "forced mode", config: valid, mode: "vuser_driven", target: &target, want: "requires scenario_mode goal_oriented"},
		{name: "aggregate mismatch", config: valid, mode: "goal_oriented", target: func() *float64 { value := 31.0; return &value }(), want: "must equal the sum"},
		{name: "missing baseline evidence", config: &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{"group_0": {TargetTPS: 30, ART: &GoalDrivenARTConfig{Basis: "baseline", ARTMs: 400}}}}}, mode: "goal_oriented", target: &target, want: "baseline_test_id"},
	}
	groups := []CreateGroupEntry{{Name: "Checkout"}, {Name: "Search"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useGroups := groups
			if test.name == "missing baseline evidence" {
				useGroups = groups[:1]
			}
			err := validateWorkloadShapeConfig(test.config, test.mode, useGroups, test.target)
			if test.want == "" && err != nil {
				t.Fatalf("validateWorkloadShapeConfig() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDefaultGoalTargetsByGroupUsesPersistedGoalDrivenTargets(t *testing.T) {
	total := 100.0
	testRun := &ManagedTest{
		Groups: []TestGroup{{Name: "Checkout", VUsers: 90}, {Name: "Search", VUsers: 10}},
		GoalConfig: &GoalConfig{TargetAverageTPS: &total, WorkloadShapeConfig: &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{
			"group_0": {TargetTPS: 25},
			"group_1": {TargetTPS: 75},
		}}}},
	}
	targets := defaultGoalTargetsByGroup(testRun)
	if targets["Checkout"] != 25 || targets["Search"] != 75 {
		t.Fatalf("targets = %#v, want persisted 25/75 split", targets)
	}
}

func TestCloneGoalConfigDeepCopiesGoalDrivenIntent(t *testing.T) {
	original := &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{
		"group_0": {TargetTPS: 25, ART: &GoalDrivenARTConfig{Basis: "manual", ARTMs: 400}},
	}}}}
	clone := cloneGoalConfig(original)
	original.WorkloadShapeConfig.GoalDriven.Groups["group_0"] = GoalDrivenGroupDefinition{TargetTPS: 50, ART: &GoalDrivenARTConfig{Basis: "manual", ARTMs: 900}}
	definition := clone.WorkloadShapeConfig.GoalDriven.Groups["group_0"]
	if definition.TargetTPS != 25 || definition.ART == nil || definition.ART.ARTMs != 400 {
		t.Fatalf("clone was aliased: %#v", definition)
	}
}
