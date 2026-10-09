package dsl

import (
	"strings"
	"testing"
)

func blockScript(blocks []RunLogicBlock) *Script {
	return &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://shop.test", RunLogic: RunLogicConfig{Blocks: blocks}}, Actions: []Action{
		{Name: "a1", Type: "GET", Target: "/a", Metadata: map[string]any{RunLogicActionMetadataKey: "Browse"}},
		{Name: "a2", Type: "GET", Target: "/b", Metadata: map[string]any{RunLogicActionMetadataKey: "Browse"}},
		{Name: "checkout", Type: "GET", Target: "/c"},
	}}
}

func TestValidateRunLogicBlocks(t *testing.T) {
	valid := blockScript([]RunLogicBlock{{Mode: RunLogicPercentage, Children: []RunLogicChild{{Action: "browse", Weight: 70}, {Action: "Checkout", Weight: 30}}}})
	if issues := ValidateScriptIssues(valid); len(issues) != 0 {
		t.Fatalf("valid blocks: %v", issues)
	}
	cases := map[string]struct {
		blocks []RunLogicBlock
		want   string
	}{
		"unknown action":  {[]RunLogicBlock{{Children: []RunLogicChild{{Action: "Missing"}}}}, `no action belongs to run-logic action "Missing"`},
		"bad mode":        {[]RunLogicBlock{{Mode: "weighted", Children: []RunLogicChild{{Action: "Browse"}}}}, "mode must be sequential, random or percentage"},
		"no children":     {[]RunLogicBlock{{}}, "at least one child"},
		"both":            {[]RunLogicBlock{{Children: []RunLogicChild{{Action: "Browse", Block: &RunLogicBlock{Children: []RunLogicChild{{Action: "Browse"}}}}}}}, "not both"},
		"zero weights":    {[]RunLogicBlock{{Mode: RunLogicPercentage, Children: []RunLogicChild{{Action: "Browse"}}}}, "weights above zero"},
		"negative weight": {[]RunLogicBlock{{Mode: RunLogicPercentage, Children: []RunLogicChild{{Action: "Browse", Weight: -1}, {Action: "Checkout", Weight: 2}}}}, "weight must be zero or greater"},
		"iterations":      {[]RunLogicBlock{{Iterations: -2, Children: []RunLogicChild{{Action: "Browse"}}}}, "iterations must be"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			issues := ValidateScriptIssues(blockScript(tc.blocks))
			if !strings.Contains(strings.Join(issues, "\n"), tc.want) {
				t.Fatalf("issues = %v, want %q", issues, tc.want)
			}
		})
	}
}

func TestRunLogicOrder(t *testing.T) {
	script := blockScript(nil)
	order := RunLogicOrder(script.Actions, []RunLogicBlock{
		{Iterations: 2, Children: []RunLogicChild{{Action: "Browse"}}},
		{Mode: RunLogicRandom, Children: []RunLogicChild{{Action: "Browse"}, {Action: "checkout"}}},
		{Mode: RunLogicPercentage, Children: []RunLogicChild{{Action: "Browse", Weight: 1}, {Action: "checkout", Weight: 3}}},
	}, func(n int) int { return n - 1 })
	// Two passes of Browse, the last random child, and the last weight band.
	want := []int{0, 1, 0, 1, 2, 2}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// Block iterations multiply through nesting, so the steps one iteration
// runs are bounded.
func TestRunLogicBlocksBoundTheStepsOfAnIteration(t *testing.T) {
	nested := []RunLogicBlock{{Iterations: 1000, Children: []RunLogicChild{{Block: &RunLogicBlock{Iterations: 1000, Children: []RunLogicChild{{Action: "Browse"}}}}}}}
	if issues := ValidateScriptIssues(blockScript(nested)); !hasIssue(issues, "more than 100000 steps in one iteration") {
		t.Fatalf("issues = %v", issues)
	}
	if order := RunLogicOrder(blockScript(nested).Actions, nested, func(int) int { return 0 }); len(order) != MaxRunLogicOrderSteps {
		t.Fatalf("order = %d steps, want the cap", len(order))
	}
	fits := []RunLogicBlock{{Iterations: 100, Mode: RunLogicRandom, Children: []RunLogicChild{{Action: "Browse"}, {Action: "Checkout"}}}}
	if issues := ValidateScriptIssues(blockScript(fits)); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
}
