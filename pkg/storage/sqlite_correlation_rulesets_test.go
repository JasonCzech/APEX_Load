package storage

import (
	"path/filepath"
	"testing"
)

func TestCorrelationRulesetRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ruleset := &CorrelationRuleset{
		ID:        "correlation-ruleset-test",
		ProjectID: "proj-a",
		Name:      "Checkout correlations",
		Source:    "script:s1",
		Rules: []CorrelationRule{
			{Name: "csrf_token", RuleID: "form_hidden", Source: "response_body", ExtractType: "boundary", Expression: `name="csrf" value="(.+?)"`, Confidence: 0.9},
		},
	}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Checkout correlations" || len(got.Rules) != 1 || got.Rules[0].RuleID != "form_hidden" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	if got.ProjectID != "proj-a" {
		t.Fatalf("project = %q", got.ProjectID)
	}

	list, err := store.ListCorrelationRulesets("proj-a", 0, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (%v)", len(list), err)
	}
	// Project scoping: a different project sees none.
	if other, _ := store.ListCorrelationRulesets("proj-b", 0, 0); len(other) != 0 {
		t.Fatalf("project scoping leaked: %d", len(other))
	}

	if err := store.DeleteCorrelationRuleset(ruleset.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if remaining, _ := store.ListCorrelationRulesets("proj-a", 0, 0); len(remaining) != 0 {
		t.Fatalf("delete left %d", len(remaining))
	}
}
