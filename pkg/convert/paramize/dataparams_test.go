package paramize

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func dataParamScript(action dsl.Action) *dsl.Script {
	return &dsl.Script{
		Name: "test", Version: "1.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{action},
	}
}

// applyDataParamKey selects only the suggestion for key and returns the applied
// action, mirroring a reviewer who accepts a single short-key suggestion.
func applyDataParamKey(t *testing.T, script *dsl.Script, key string) dsl.Action {
	t.Helper()
	result := Suggest(script, Options{EnableCorrelation: true, EnableDataParams: true, EnableSecrets: true})
	selected := map[string]bool{}
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind == KindDataParam && suggestion.VarName == key {
			selected[suggestion.ID] = true
		}
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, selected)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	return applied.Actions[0]
}

// A short key that is a suffix of a longer one must never rebind the longer
// parameter. The pair at offset 0 of a form body has no left delimiter to anchor
// on, so its literal also matches inside `user_id=1`.
func TestDataParamShortKeyAtBodyStartLeavesLongerKeyAlone(t *testing.T) {
	script := dataParamScript(dsl.Action{
		Name: "create", Type: "http_request", Method: "POST", Target: "/orders",
		Body: "id=1&user_id=1&order_id=9",
	})
	action := applyDataParamKey(t, script, "id")
	if !strings.Contains(action.Body, "user_id=1") {
		t.Errorf("body = %q, want user_id left bound to its recorded literal", action.Body)
	}
	if !strings.Contains(action.Body, "order_id=9") {
		t.Errorf("body = %q, want order_id untouched", action.Body)
	}
}

// Whitespace and line breaks separate pairs too (queryPairPattern accepts them),
// so a pair harvested after a space needs the same anchoring as one after '&'.
func TestDataParamWhitespaceSeparatedPairsAnchorOnTheSeparator(t *testing.T) {
	script := dataParamScript(dsl.Action{
		Name: "create", Type: "http_request", Method: "POST", Target: "/orders",
		Body: "user_id=1 id=1",
	})
	action := applyDataParamKey(t, script, "id")
	if action.Body != "user_id=1 id={{var.id}}" {
		t.Errorf("body = %q, want only the standalone id pair replaced", action.Body)
	}
}

// The unanchored-pair guard must only fire on a genuinely ambiguous literal: an
// unambiguous first pair is still parameterized.
func TestDataParamUnambiguousPairAtBodyStartStillApplies(t *testing.T) {
	script := dataParamScript(dsl.Action{
		Name: "create", Type: "http_request", Method: "POST", Target: "/orders",
		Body: "id=1&sku=abc",
	})
	action := applyDataParamKey(t, script, "id")
	if action.Body != "id={{var.id}}&sku=abc" {
		t.Errorf("body = %q, want the leading id pair parameterized", action.Body)
	}
}

// Anchoring on whitespace must not make the two pairs look like overlapping
// literals to the multi-select validator.
func TestDataParamWhitespaceSeparatedPairsApplyTogether(t *testing.T) {
	script := dataParamScript(dsl.Action{
		Name: "create", Type: "http_request", Method: "POST", Target: "/orders",
		Body: "user_id=1 id=1",
	})
	result := Suggest(script, Options{EnableCorrelation: true, EnableDataParams: true, EnableSecrets: true})
	selected := map[string]bool{}
	for _, suggestion := range result.Suggestions {
		selected[suggestion.ID] = true
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, selected)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if applied.Actions[0].Body != "user_id={{var.user_id}} id={{var.id}}" {
		t.Errorf("body = %q, want both pairs bound to their own columns", applied.Actions[0].Body)
	}
}

// Query strings already anchor on '?' and '&'; keep that covered so the
// delimiter handling cannot regress into the same suffix match.
func TestDataParamShortQueryKeyLeavesLongerKeyAlone(t *testing.T) {
	script := dataParamScript(dsl.Action{
		Name: "list", Type: "http_request", Method: "GET", Target: "/orders?user_id=7&id=7",
	})
	action := applyDataParamKey(t, script, "id")
	if action.Target != "/orders?user_id=7&id={{var.id}}" {
		t.Errorf("target = %q, want only the standalone id pair replaced", action.Target)
	}
}
