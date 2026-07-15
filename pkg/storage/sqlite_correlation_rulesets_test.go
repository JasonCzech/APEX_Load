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
	requireProjectFixtures(t, store, "proj-a")

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

func TestCorrelationRulesetOptimisticUpdateRejectsStaleAndOwnershipChanges(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a", "project-b")

	ruleset := &CorrelationRuleset{ID: "ruleset-cas", ProjectID: "project-a", Name: "original"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("SaveCorrelationRuleset: %v", err)
	}
	first, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset first: %v", err)
	}
	stale, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset stale: %v", err)
	}
	first.Name = "first writer"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset fresh: %v", err)
	}
	if fresh.Name != "first writer" || fresh.ProjectID != "project-a" {
		t.Fatalf("fresh ruleset = %#v, want first writer in project-a", fresh)
	}

	expected := fresh.UpdatedAt
	fresh.ProjectID = "project-b"
	if updated, err := store.UpdateCorrelationRulesetIfUnchanged(fresh, expected); err != nil || updated {
		t.Fatalf("ownership-changing update = (%v, %v), want rejection", updated, err)
	}
	if err := store.SaveCorrelationRuleset(&CorrelationRuleset{ID: ruleset.ID, ProjectID: "project-b", Name: "legacy update"}); err == nil {
		t.Fatal("SaveCorrelationRuleset accepted a cross-project update")
	}
	fresh, err = store.GetCorrelationRuleset(ruleset.ID)
	if err != nil || fresh.ProjectID != "project-a" || fresh.Name != "first writer" {
		t.Fatalf("ruleset ownership after upsert = %#v, err=%v", fresh, err)
	}
	if err := store.SaveCorrelationRuleset(&CorrelationRuleset{ID: "missing-owner", ProjectID: "missing-project", Name: "bad"}); err == nil {
		t.Fatal("SaveCorrelationRuleset accepted a missing owner project")
	}
}
