package dsl

import (
	"math"
	"strings"
	"testing"
)

func TestValidateScriptIssuesRejectsExecutionResourceOverflows(t *testing.T) {
	script := &Script{
		Name:     "resource-bounds",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			Pacing:  PacingConfig{Type: "tps", TPS: math.NaN()},
			RunLogic: RunLogicConfig{
				Iterations:       MaxRunIterations + 1,
				GlobalIterations: MaxRunIterations + 1,
				MaxInFlight:      MaxOpenWorkloadInFlight + 1,
			},
		},
		Parameters: []Parameter{{
			Name:          "oversized",
			Type:          "random_string",
			Length:        MaxGeneratedParameterBytes + 1,
			Charset:       strings.Repeat("x", MaxParameterCharsetBytes+1),
			StartRow:      MaxParameterRowIndex + 1,
			BlockSize:     MaxParameterRowIndex + 1,
			OffsetSeconds: MaxParameterOffsetSeconds + 1,
			PadWidth:      MaxGeneratedParameterBytes + 1,
		}},
		Actions: []Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &ControlFlow{
				Count: MaxControlLoopIterations + 1,
				Steps: []Action{{
					Name: "nested-loop",
					Type: "control_loop",
					Control: &ControlFlow{
						MaxIterations: MaxControlLoopIterations + 1,
						Steps:         []Action{{Name: "request", Type: "request", Target: "/"}},
					},
				}},
			},
		}},
	}

	joined := strings.Join(ValidateScriptIssues(script), "\n")
	for _, want := range []string{
		"iterations must not exceed",
		"global_iterations must not exceed",
		"max_in_flight must not exceed",
		"tps must be finite",
		"length must not exceed",
		"charset must not exceed",
		"start_row must be between",
		"block_size must be between",
		"offset_seconds must be between",
		"pad_width must be between",
		"control count must not exceed",
		"control max_iterations must not exceed",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in validation issues:\n%s", want, joined)
		}
	}
}

func TestSpecializedProtocolValidationStillEnforcesResourceLimits(t *testing.T) {
	for _, protocolName := range []string{"jms-generic", "kafka", "eventhubs"} {
		t.Run(protocolName, func(t *testing.T) {
			script := &Script{
				Name:       "specialized",
				Protocol:   protocolName,
				Config:     ScriptConfig{RunLogic: RunLogicConfig{Iterations: MaxRunIterations + 1}},
				Parameters: []Parameter{{Name: "huge", Type: "random_string", Length: MaxGeneratedParameterBytes + 1}},
			}
			joined := strings.Join(ValidateScriptIssues(script), "\n")
			if !strings.Contains(joined, "iterations must not exceed") || !strings.Contains(joined, "length must not exceed") {
				t.Fatalf("specialized validation bypassed limits:\n%s", joined)
			}
		})
	}
}
