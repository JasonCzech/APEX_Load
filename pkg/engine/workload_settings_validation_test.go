package engine

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestUppercaseOpenTPSValidationExecutesAsOpenWorkload(t *testing.T) {
	script := &dsl.Script{
		Name:     "canonical-open-workload",
		Protocol: "slow-stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 5, WorkloadModel: " OPEN "},
			Pacing:   dsl.PacingConfig{Type: " TPS ", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	if issues := dsl.ValidateWorkloadSettingsIssues(script.Config.RunLogic, script.Config.Pacing); len(issues) != 0 {
		t.Fatalf("validation issues = %v, want none", issues)
	}
	if !isOpenWorkload(script) {
		t.Fatal("validated uppercase TPS settings were not classified as open workload")
	}

	eng, err := New(Config{Script: script, VUsers: 1, Duration: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	started := time.Now()
	result, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 5 {
		t.Fatalf("transactions = %d, want 5", result.TotalTransactions)
	}
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("uppercase TPS settings fell back to closed execution; elapsed = %v", elapsed)
	}
}

func TestEngineAdmissionRejectsInvalidWorkloadSettings(t *testing.T) {
	tests := []struct {
		name     string
		runLogic dsl.RunLogicConfig
		pacing   dsl.PacingConfig
	}{
		{name: "missing TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps"}},
		{name: "zero TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps", TPS: 0}},
		{name: "negative TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps", TPS: -1}},
		{name: "NaN TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps", TPS: math.NaN()}},
		{name: "positive infinite TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps", TPS: math.Inf(1)}},
		{name: "negative infinite TPS", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tps", TPS: math.Inf(-1)}},
		{name: "misspelled workload model", runLogic: dsl.RunLogicConfig{WorkloadModel: "opne"}, pacing: dsl.PacingConfig{Type: "tps", TPS: 10}},
		{name: "misspelled pacing type", runLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, pacing: dsl.PacingConfig{Type: "tsp", TPS: 10}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := &dsl.Script{
				Name:     "invalid-workload",
				Protocol: "stub",
				Config:   dsl.ScriptConfig{RunLogic: tt.runLogic, Pacing: tt.pacing},
				Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			}
			if issues := dsl.ValidateWorkloadSettingsIssues(tt.runLogic, tt.pacing); len(issues) == 0 {
				t.Fatal("DSL validation accepted invalid workload settings")
			}
			if _, err := New(Config{Script: script, VUsers: 1, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "invalid workload settings") {
				t.Fatalf("New error = %v, want invalid workload settings", err)
			}
			if _, err := NewMultiGroup(MultiGroupConfig{
				Groups:   []GroupConfig{{Name: "group", Script: script, VUsers: 1}},
				Duration: time.Second,
			}); err == nil || !strings.Contains(err.Error(), "invalid workload settings") {
				t.Fatalf("NewMultiGroup error = %v, want invalid workload settings", err)
			}
		})
	}
}
