package paramize

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestApplyDataParameterUsesPairBoundariesForOverlappingKeys(t *testing.T) {
	script := &dsl.Script{
		Name: "pairs", Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "query",
			Type:   "GET",
			Target: "/items?user_id=1&id=1&id=1&id_suffix=1",
			Body:   "user_id=1&id=1&id=1&id_suffix=1",
		}},
	}
	result := Suggest(script, Options{EnableDataParams: true})
	var selected Suggestion
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind == KindDataParam && suggestion.Parameter != nil && suggestion.Parameter.Name == "id" {
			selected = suggestion
			break
		}
	}
	if selected.ID == "" {
		t.Fatalf("suggestions = %#v, want id data parameter", result.Suggestions)
	}
	applied, _, err := ApplySuggestions(script, []Suggestion{selected}, map[string]bool{selected.ID: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{applied.Actions[0].Target, applied.Actions[0].Body} {
		if !strings.Contains(got, "user_id=1") || !strings.Contains(got, "id_suffix=1") {
			t.Fatalf("overlapping key was rewritten: %q", got)
		}
		if count := strings.Count(got, "&id={{var.id}}"); count != 2 {
			t.Fatalf("exact adjacent id pairs rewritten %d times, want 2: %q", count, got)
		}
	}
}
