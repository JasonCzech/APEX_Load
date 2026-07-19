package dsl

import (
	"math"
	"strings"
	"testing"
)

func TestValidateWorkloadSettingsIssues(t *testing.T) {
	tests := []struct {
		name     string
		runLogic RunLogicConfig
		pacing   PacingConfig
		want     string
	}{
		{
			name:     "canonicalized uppercase open TPS",
			runLogic: RunLogicConfig{WorkloadModel: " OPEN "},
			pacing:   PacingConfig{Type: " TPS ", TPS: 25},
		},
		{
			name:     "missing TPS",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{Type: "tps"},
			want:     "finite and greater than zero",
		},
		{
			name:     "missing TPS pacing type",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{TPS: 25},
			want:     "config.pacing.type to be tps",
		},
		{
			name:     "negative TPS",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{Type: "tps", TPS: -1},
			want:     "finite and greater than zero",
		},
		{
			name:     "NaN TPS",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{Type: "tps", TPS: math.NaN()},
			want:     "finite and greater than zero",
		},
		{
			name:     "infinite TPS",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{Type: "tps", TPS: math.Inf(1)},
			want:     "finite and greater than zero",
		},
		{
			name:     "misspelled workload model",
			runLogic: RunLogicConfig{WorkloadModel: "opne"},
			pacing:   PacingConfig{Type: "tps", TPS: 25},
			want:     "workload_model must be closed or open",
		},
		{
			name:     "misspelled pacing type",
			runLogic: RunLogicConfig{WorkloadModel: "open"},
			pacing:   PacingConfig{Type: "tsp", TPS: 25},
			want:     "config.pacing.type must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateWorkloadSettingsIssues(tt.runLogic, tt.pacing)
			if tt.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %v, want none", issues)
				}
				return
			}
			if joined := strings.Join(issues, "\n"); !strings.Contains(joined, tt.want) {
				t.Fatalf("issues = %v, want %q", issues, tt.want)
			}
		})
	}
}

func TestCanonicalWorkloadAndPacingEnumsApplyDocumentedDefaults(t *testing.T) {
	if got := CanonicalWorkloadModel(""); got != "closed" {
		t.Fatalf("empty workload model = %q, want closed", got)
	}
	if got := CanonicalPacingType(""); got != "none" {
		t.Fatalf("empty pacing type = %q, want none", got)
	}
	if got := CanonicalWorkloadModel(" OPEN "); got != "open" {
		t.Fatalf("canonical workload model = %q, want open", got)
	}
	if got := CanonicalPacingType(" TPS "); got != "tps" {
		t.Fatalf("canonical pacing type = %q, want tps", got)
	}
}
