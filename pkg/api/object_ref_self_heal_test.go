package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func healEvent(actionName, healed, kind string, confidence int) engine.ReplayActionEvent {
	return engine.ReplayActionEvent{
		ActionName: actionName,
		ProtocolEvidence: map[string]any{
			"selector_self_heal": map[string]any{
				"action":          actionName,
				"healed_selector": healed,
				"candidate_kind":  kind,
				"confidence":      confidence,
				"reason":          "fallback matched",
			},
		},
	}
}

func TestCollectSelfHeals(t *testing.T) {
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEvent("Login", "#healed", "css", 80),
		{ActionName: "NoHeal"}, // no evidence → skipped
	}}
	heals := collectSelfHeals(report)
	if len(heals) != 1 {
		t.Fatalf("heals = %d, want 1", len(heals))
	}
	if heals[0].actionName != "Login" || heals[0].healed != "#healed" || heals[0].kind != "css" || heals[0].confidence != 80 {
		t.Fatalf("unexpected heal record: %#v", heals[0])
	}
}

func TestObjectRefBindingsByActionName(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-a:login",
			Metadata: map[string]any{objectRefRepositoryMetadataKey: "object-repo-a"}},
		{Name: "Plain", Type: "browser_click", Selector: "#x"}, // unbound
	}}
	bindings := objectRefBindingsByActionName(script)
	if len(bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(bindings))
	}
	b := bindings["Login"]
	if b.logicalName != "login" || b.repoID != "object-repo-a" {
		t.Fatalf("binding = %#v", b)
	}
}

func TestApplyHealToRepositoryObject(t *testing.T) {
	t.Run("appends a drifted locator", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#b", kind: "css", confidence: 80}, false)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(repo.Objects[0].Candidates) != 2 {
			t.Fatalf("candidates = %d, want 2", len(repo.Objects[0].Candidates))
		}
		if repo.Objects[0].BestConfidence != 80 {
			t.Fatalf("best confidence = %d, want 80", repo.Objects[0].BestConfidence)
		}
	})

	t.Run("raises confidence of an existing candidate", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#a", kind: "css", confidence: 88}, false)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(repo.Objects[0].Candidates) != 1 || repo.Objects[0].Candidates[0].Confidence != 88 {
			t.Fatalf("candidate not strengthened: %#v", repo.Objects[0].Candidates)
		}
	})

	t.Run("does not lower confidence", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#a", Confidence: 70}},
		}}}
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "#a", kind: "css", confidence: 50}, false)
		if changed {
			t.Fatal("expected changed=false for a weaker observation")
		}
		if repo.Objects[0].Candidates[0].Confidence != 70 {
			t.Fatalf("confidence lowered to %d", repo.Objects[0].Candidates[0].Confidence)
		}
	})

	t.Run("matches an xpath candidate by engine form", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login",
			Candidates:  []storage.RepositoryObjectCandidate{{Kind: "xpath", Value: "//a", Confidence: 58}},
		}}}
		// self-heal records the engine form ("xpath=//a"), not the raw value.
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "xpath=//a", kind: "xpath", confidence: 58}, false)
		if changed {
			t.Fatal("engine-form match should not append a duplicate")
		}
		if len(repo.Objects[0].Candidates) != 1 {
			t.Fatalf("candidates = %d, want 1 (no duplicate)", len(repo.Objects[0].Candidates))
		}
	})

	t.Run("autoPromote pushes the healed candidate above the primary", func(t *testing.T) {
		repo := &storage.ObjectRepository{Objects: []storage.RepositoryObject{{
			LogicalName: "login", BestConfidence: 88,
			Candidates: []storage.RepositoryObjectCandidate{
				{Kind: "css", Value: "#login", Confidence: 88},
				{Kind: "xpath", Value: "//a", Confidence: 58},
			},
		}}}
		// The healed xpath (engine form) starts below the css primary; with
		// autoPromote it should become strictly the highest-confidence candidate.
		changed := applyHealToRepositoryObject(repo, "login", selfHealRecord{healed: "xpath=//a", kind: "xpath", confidence: 58}, true)
		if !changed {
			t.Fatal("expected changed=true")
		}
		healed := repo.Objects[0].Candidates[1]
		if healed.Confidence != 89 || repo.Objects[0].BestConfidence != 89 {
			t.Fatalf("healed not promoted: confidence=%d best=%d", healed.Confidence, repo.Objects[0].BestConfidence)
		}
		if healed.Reason != "promoted by replay self-heal" {
			t.Fatalf("reason = %q", healed.Reason)
		}
	})
}

func TestPersistSelfHealToRepository_Integration(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID:        "object-repo-int",
		ProjectID: "unassigned",
		Name:      "Checkout",
		Objects: []storage.RepositoryObject{{
			LogicalName:    "Login",
			BestConfidence: 70,
			Candidates:     []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save repo: %v", err)
	}

	script := &dsl.Script{ProjectID: "unassigned", Protocol: "browser", Actions: []dsl.Action{{
		Name: "Login", Type: "browser_click", ObjectRef: "object-repo-int:Login",
		Metadata: map[string]any{objectRefRepositoryMetadataKey: "object-repo-int"},
	}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{
		healEvent("Login", "#login-healed", "css", 80),
	}}

	server.persistSelfHealToRepository("unassigned", script, report)

	updated, err := server.store.GetObjectRepository("object-repo-int")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	found := false
	for _, c := range updated.Objects[0].Candidates {
		if c.Value == "#login-healed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("healed candidate not written back: %#v", updated.Objects[0].Candidates)
	}
}

func TestPersistSelfHealToRepository_IgnoresUnboundActions(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID: "object-repo-u", ProjectID: "unassigned", Name: "x",
		Objects: []storage.RepositoryObject{{LogicalName: "Login", Candidates: []storage.RepositoryObjectCandidate{{Kind: "css", Value: "#login", Confidence: 70}}}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Action heals but is NOT bound to a repository (no object_ref).
	script := &dsl.Script{ProjectID: "unassigned", Actions: []dsl.Action{{Name: "Login", Type: "browser_click", Selector: "#login"}}}
	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{healEvent("Login", "#login-healed", "css", 80)}}

	server.persistSelfHealToRepository("unassigned", script, report)

	updated, _ := server.store.GetObjectRepository("object-repo-u")
	if len(updated.Objects[0].Candidates) != 1 {
		t.Fatalf("unbound heal must not touch the repository, candidates = %d", len(updated.Objects[0].Candidates))
	}
}
