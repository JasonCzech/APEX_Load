package importreview

import (
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A script with an attributed warning list builds its preflight from that
// list; a script without one, and the augmented plan-wide warnings, keep the
// draft's Warnings.
func TestImportDraftScriptWarningsScopePreflight(t *testing.T) {
	newScript := func(name string) *dsl.Script {
		return &dsl.Script{Name: name, Version: "1.0.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{{Name: "get", Type: "request", Method: "GET", Target: "/"}}}
	}
	owner, sibling := newScript("owner"), newScript("sibling")
	const own, planWide = "Counter p_A is retained but unused by the messaging actions.", "Plan-level note."
	draft := ImportDraft{
		Source:         "jmeter",
		Scripts:        []*dsl.Script{owner, sibling},
		Warnings:       []string{own, planWide},
		ScriptWarnings: map[*dsl.Script][]string{owner: {own, planWide}, sibling: {planWide}},
	}
	augmented := draft.AugmentWithAgentforceTransform(false)
	warnings := func(script *dsl.Script) []string {
		preflight, _ := script.Metadata.Import.Preserved["replay_preflight"].(*ReplayPreflight)
		if preflight == nil {
			t.Fatalf("%s has no preflight", script.Name)
		}
		return preflight.Warnings
	}
	if got := warnings(owner); !slices.Contains(got, own) || !slices.Contains(got, planWide) {
		t.Errorf("owner preflight = %q", got)
	}
	if got := warnings(sibling); slices.Contains(got, own) || !slices.Contains(got, planWide) {
		t.Errorf("sibling preflight = %q", got)
	}
	if !slices.Contains(augmented.Warnings, own) || !slices.Contains(augmented.Warnings, planWide) {
		t.Errorf("plan-wide warnings = %q", augmented.Warnings)
	}

	fallback := newScript("fallback")
	draft = ImportDraft{Source: "jmeter", Scripts: []*dsl.Script{fallback}, Warnings: []string{own}, ScriptWarnings: map[*dsl.Script][]string{owner: nil}}
	draft.AugmentWithAgentforceTransform(false)
	if got := warnings(fallback); !slices.Contains(got, own) {
		t.Errorf("script without an attributed list did not fall back to Warnings: %q", got)
	}
}
