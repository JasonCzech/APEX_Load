package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

type fakeObjectRepoReader struct {
	repos []*storage.ObjectRepository
	calls int
}

func (f *fakeObjectRepoReader) ListObjectRepositories(projectID string, limit, offset int) ([]*storage.ObjectRepository, error) {
	f.calls++
	var out []*storage.ObjectRepository
	for _, r := range f.repos {
		if projectID == "" || r.ProjectID == projectID {
			out = append(out, r)
		}
	}
	return out, nil
}

func clickAction(name, objectRef string) dsl.Action {
	return dsl.Action{Name: name, Type: "browser_click", ObjectRef: objectRef}
}

func metaCandidates(t *testing.T, action dsl.Action, key string) []selector.Candidate {
	t.Helper()
	if action.Metadata == nil {
		return nil
	}
	return selector.FromMetadataValue(action.Metadata[key])
}

func TestResolveObjectRefs_BareReference(t *testing.T) {
	reader := &fakeObjectRepoReader{repos: []*storage.ObjectRepository{{
		ID:        "object-repo-a",
		ProjectID: "unassigned",
		Objects: []storage.RepositoryObject{{
			LogicalName:    "login",
			BestConfidence: 88,
			Candidates: []storage.RepositoryObjectCandidate{
				{Kind: "css", Value: "#login", Confidence: 88},
				{Kind: "xpath", Value: "//a[@id='login']", Confidence: 58},
			},
		}},
	}}}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{clickAction("Login", "login")}}

	resolved, warnings, issues := resolveObjectRefs(reader, script)
	if len(issues) != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected warnings=%v issues=%v", warnings, issues)
	}
	if resolved == script {
		t.Fatal("expected a deep clone, got same pointer")
	}
	got := resolved.Actions[0]
	if got.Selector != "#login" {
		t.Fatalf("primary selector = %q, want #login", got.Selector)
	}
	cands := metaCandidates(t, got, selector.MetadataCandidatesKey)
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	fallbacks := metaCandidates(t, got, selector.MetadataFallbacksKey)
	if len(fallbacks) != 1 || fallbacks[0].Kind != "xpath" {
		t.Fatalf("fallbacks = %#v, want one xpath", fallbacks)
	}
	if got.Metadata[objectRefRepositoryMetadataKey] != "object-repo-a" {
		t.Fatalf("repo stamp = %v, want object-repo-a", got.Metadata[objectRefRepositoryMetadataKey])
	}
	// The stored script must be untouched.
	if script.Actions[0].Selector != "" || script.Actions[0].Metadata != nil {
		t.Fatalf("input script mutated: %#v", script.Actions[0])
	}
}

func TestResolveObjectRefs_QualifiedPinsRepository(t *testing.T) {
	reader := &fakeObjectRepoReader{repos: []*storage.ObjectRepository{
		{ID: "object-repo-a", ProjectID: "unassigned", Objects: []storage.RepositoryObject{{LogicalName: "go", CSS: "#a-go", BestConfidence: 88}}},
		{ID: "object-repo-b", ProjectID: "unassigned", Objects: []storage.RepositoryObject{{LogicalName: "go", CSS: "#b-go", BestConfidence: 88}}},
	}}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{clickAction("Go", "object-repo-b:go")}}

	resolved, _, issues := resolveObjectRefs(reader, script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	if got := resolved.Actions[0].Selector; got != "#b-go" {
		t.Fatalf("qualified ref selector = %q, want #b-go", got)
	}
	if resolved.Actions[0].Metadata[objectRefRepositoryMetadataKey] != "object-repo-b" {
		t.Fatalf("repo stamp wrong: %v", resolved.Actions[0].Metadata[objectRefRepositoryMetadataKey])
	}
}

func TestResolveObjectRefs_TieBreakHighestConfidence(t *testing.T) {
	reader := &fakeObjectRepoReader{repos: []*storage.ObjectRepository{
		{ID: "object-repo-a", ProjectID: "unassigned", Objects: []storage.RepositoryObject{{LogicalName: "go", CSS: "#weak", BestConfidence: 60}}},
		{ID: "object-repo-b", ProjectID: "unassigned", Objects: []storage.RepositoryObject{{LogicalName: "go", CSS: "#strong", BestConfidence: 90}}},
	}}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{clickAction("Go", "go")}}

	resolved, _, _ := resolveObjectRefs(reader, script)
	if got := resolved.Actions[0].Selector; got != "#strong" {
		t.Fatalf("tie-break selector = %q, want #strong (higher confidence)", got)
	}
}

func TestResolveObjectRefs_DegradeToInlineSelector(t *testing.T) {
	reader := &fakeObjectRepoReader{}
	action := clickAction("Go", "missing")
	action.Selector = "#fallback"
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{action}}

	resolved, warnings, issues := resolveObjectRefs(reader, script)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %v", warnings)
	}
	if got := resolved.Actions[0].Selector; got != "#fallback" {
		t.Fatalf("selector = %q, want inline #fallback preserved", got)
	}
}

func TestResolveObjectRefs_HardFailNoFallback(t *testing.T) {
	reader := &fakeObjectRepoReader{}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{clickAction("Go", "missing")}}

	_, warnings, issues := resolveObjectRefs(reader, script)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(issues) != 1 {
		t.Fatalf("expected one blocking issue, got %v", issues)
	}
}

func TestResolveObjectRefs_FastPathNoRefs(t *testing.T) {
	reader := &fakeObjectRepoReader{}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{{Name: "Go", Type: "browser_click", Selector: "#x"}}}

	resolved, warnings, issues := resolveObjectRefs(reader, script)
	if resolved != script {
		t.Fatal("no-ref script should be returned unchanged (same pointer)")
	}
	if warnings != nil || issues != nil {
		t.Fatalf("expected nil warnings/issues, got %v / %v", warnings, issues)
	}
	if reader.calls != 0 {
		t.Fatalf("fast path must not hit the store, calls = %d", reader.calls)
	}
}

func TestResolveObjectRefs_SynthesizesCandidatesFromDescriptor(t *testing.T) {
	reader := &fakeObjectRepoReader{repos: []*storage.ObjectRepository{{
		ID:        "object-repo-a",
		ProjectID: "unassigned",
		Objects:   []storage.RepositoryObject{{LogicalName: "go", CSS: "#go", XPath: "//button"}},
	}}}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{clickAction("Go", "go")}}

	resolved, _, issues := resolveObjectRefs(reader, script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	got := resolved.Actions[0]
	if got.Selector != "#go" {
		t.Fatalf("primary = %q, want #go", got.Selector)
	}
	fallbacks := metaCandidates(t, got, selector.MetadataFallbacksKey)
	if len(fallbacks) != 1 || fallbacks[0].Kind != "xpath" {
		t.Fatalf("synthesized fallbacks = %#v, want one xpath", fallbacks)
	}
}

func TestResolveObjectRefs_ResolvesInsideControlSteps(t *testing.T) {
	reader := &fakeObjectRepoReader{repos: []*storage.ObjectRepository{{
		ID:        "object-repo-a",
		ProjectID: "unassigned",
		Objects:   []storage.RepositoryObject{{LogicalName: "go", CSS: "#go", BestConfidence: 88}},
	}}}
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{{
		Name: "loop", Type: "control_loop",
		Control: &dsl.ControlFlow{Steps: []dsl.Action{clickAction("Go", "go")}},
	}}}

	resolved, _, issues := resolveObjectRefs(reader, script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	if got := resolved.Actions[0].Control.Steps[0].Selector; got != "#go" {
		t.Fatalf("nested selector = %q, want #go", got)
	}
}
