package requestimport

import (
	"errors"
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRequestOrigin(t *testing.T) {
	for _, tc := range []struct{ base, target, want string }{
		{"https://App.Example.test", "/a", "https://app.example.test"},
		{"https://app.example.test:443", "/a", "https://app.example.test"},
		{"https://app.example.test:8443/base", "b", "https://app.example.test:8443"},
		{"https://app.example.test", "http://other.example.test:80/x", "http://other.example.test"},
		{"https://app.example.test", "https://[::1]:443/x", "https://[::1]"},
		{"https://{{var.host}}", "/a", ""},
		{"https://app.example.test", "{{var.url}}", ""},
		{"", "/relative", ""},
		{"https://app.example.test", "ftp://files.example.test/x", ""},
		// The same cases as dashboard/src/components/importTransportModel.test.ts.
		{"https://app.example.test", "HTTPS://Other.example.test/x", "https://app.example.test"},
		{"https://app.example.test/api", "//cdn.example.test/x", "https://app.example.test"},
		{"https://user:secret@App.example.test", "/x", "https://app.example.test"},
		{"https://Bücher.example", "/x", "https://bücher.example"},
		{"https://app.example.test", "{{var.base}}/x", ""},
		{"http://app.example.test:80", "/x", "http://app.example.test"},
	} {
		if got := RequestOrigin(tc.base, tc.target); got != tc.want {
			t.Errorf("RequestOrigin(%q, %q) = %q, want %q", tc.base, tc.target, got, tc.want)
		}
	}
}

// transportScript has a top-level request, an http_sequence with a typed and
// a JSON-shaped child, a control branch, and a request on another origin.
func transportScript(t *testing.T) *dsl.Script {
	t.Helper()
	script := validBaseScript()
	h2, _ := dsl.NewHTTPVersionOverride("2")
	script.Actions = []dsl.Action{
		{Name: "home", Type: "GET", Method: "GET", Target: "/", HTTPVersion: h2, Metadata: map[string]any{"http_version": "2"}},
		{Name: "batch", Type: "http_sequence", Params: map[string]any{"requests": []any{
			map[string]any{"name": "child-a", "type": "GET", "method": "GET", "target": "/a"},
			map[string]any{"name": "child-b", "type": "GET", "method": "GET", "target": "https://cdn.example.test/b"},
		}}},
		{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "1", Op: "eq", Right: "1"}, Steps: []dsl.Action{
			{Name: "nested", Type: "GET", Method: "GET", Target: "/c"},
		}}},
	}
	return script
}

func TestApplyTransportEditsReachesNestedRequests(t *testing.T) {
	script := transportScript(t)
	edits := []TransportEdit{
		{Path: "actions[0]", Origin: "https://example.test", From: "2", To: "auto+h3"},
		{Path: "actions[1].params.requests[0]", Origin: "https://example.test", From: "", To: "auto+h3"},
		{Path: "actions[2].control.steps[0]", Origin: "https://example.test", From: "", To: "auto+h3"},
	}
	changes, err := ApplyTransportEdits(script, edits)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	if script.Actions[0].HTTPVersion.Value() != "auto+h3" || script.Actions[0].Metadata["http_version"] != "auto+h3" {
		t.Fatalf("top-level: %+v %v", script.Actions[0].HTTPVersion, script.Actions[0].Metadata)
	}
	provenance, _ := script.Actions[0].Metadata[TransportEditMetadataKey].(map[string]any)
	if provenance["previous_policy"] != "2" || provenance["policy"] != "auto+h3" || provenance["previous_declared"] != "2" {
		t.Fatalf("provenance = %v", provenance)
	}
	children := script.Actions[1].Params["requests"].([]any)
	if child := children[0].(map[string]any); child["http_version"] != "auto+h3" {
		t.Fatalf("sequence child = %v", child)
	}
	// The other origin's child is untouched.
	if child := children[1].(map[string]any); child["http_version"] != nil {
		t.Fatalf("another origin changed: %v", child)
	}
	if nested := script.Actions[2].Control.Steps[0]; nested.HTTPVersion.Value() != "auto+h3" {
		t.Fatalf("control branch: %+v", nested.HTTPVersion)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("invalid after edit: %v", issues)
	}
	// Back to inherit removes the leaf and the declared version.
	if _, err := ApplyTransportEdits(script, []TransportEdit{{Path: "actions[0]", Origin: "https://example.test", From: "auto+h3", To: ""}}); err != nil {
		t.Fatal(err)
	}
	if script.Actions[0].HTTPVersion.IsPresent() || script.Actions[0].Metadata["http_version"] != nil {
		t.Fatalf("inherit kept a policy: %+v %v", script.Actions[0].HTTPVersion, script.Actions[0].Metadata)
	}
}

func TestApplyTransportEditsRefusesStaleOrInvalidEdits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edits []TransportEdit
		stale bool
	}{
		{"wrong from", []TransportEdit{{Path: "actions[0]", Origin: "https://example.test", From: "", To: "3"}}, true},
		{"wrong origin", []TransportEdit{{Path: "actions[0]", Origin: "https://other.test", From: "2", To: "3"}}, true},
		{"missing path", []TransportEdit{{Path: "actions[9]", Origin: "https://example.test", From: "", To: "3"}}, true},
		{"not a request", []TransportEdit{{Path: "actions[1]", Origin: "https://example.test", From: "", To: "3"}}, true},
		{"unknown policy", []TransportEdit{{Path: "actions[0]", Origin: "https://example.test", From: "2", To: "h3-29"}}, false},
		{"exact 3 on cleartext", []TransportEdit{{Path: "actions[0]", Origin: "http://example.test", From: "2", To: "3"}}, false},
		{"duplicate", []TransportEdit{{Path: "actions[0]", Origin: "https://example.test", From: "2", To: "3"}, {Path: "actions[0]", Origin: "https://example.test", From: "2", To: "1.1"}}, false},
	} {
		script := transportScript(t)
		_, err := ApplyTransportEdits(script, tc.edits)
		if err == nil || errors.Is(err, ErrStaleTransportEdit) != tc.stale {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		// Nothing changed: the batch is all or nothing.
		if script.Actions[0].HTTPVersion.Value() != "2" {
			t.Fatalf("%s: a refused batch changed the draft", tc.name)
		}
	}
}

func TestTransportEditResolvesOnlyVersionGates(t *testing.T) {
	script := validBaseScript()
	action := dsl.Action{Name: "race", Type: "GET", Method: "GET", Target: "/orders", Metadata: map[string]any{
		curlReviewReasonsKey:            []string{"curl_http3_race_not_modeled", "unsupported_curl_option"},
		"http_version_rewrite_proposal": map[string]any{"version": 1, "policy": "auto+h3"},
	}}
	importreview.ApplyToAction(&action, []importreview.Finding{
		{Code: "curl_http3_race_not_modeled", Severity: "blocker", Gate: importreview.GateExecution},
		{Code: "unsupported_curl_option", Severity: "blocker", Gate: importreview.GateExecution},
	})
	draft := dsl.Action{Name: "draft", Type: "GET", Method: "GET", Target: "/draft"}
	ApplyHTTPVersionCapture(&draft, ReviewHTTPVersionCapture("h3-29", true, nil, false, nil, false, false), SourceRef{Format: "har"})
	script.Actions = []dsl.Action{action, draft}
	changes, err := ApplyTransportEdits(script, []TransportEdit{
		{Path: "actions[0]", Origin: "https://example.test", From: "", To: "auto+h3"},
		{Path: "actions[1]", Origin: "https://example.test", From: "", To: "auto+h3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	race := script.Actions[0].Metadata
	if !slices.Equal(changes[0].Resolved, []string{"curl_http3_race_not_modeled"}) || race["execution_blocker"] != "unsupported_curl_option" ||
		race["http_version_rewrite_proposal"] != nil || !slices.Equal(race[curlReviewReasonsKey].([]string), []string{"unsupported_curl_option"}) {
		t.Fatalf("race gate: changes=%+v metadata=%v", changes[0], race)
	}
	modernized := script.Actions[1].Metadata
	if !slices.Equal(changes[1].Resolved, []string{ReasonHTTP3DraftToken}) || modernized["review_reasons"] != nil || modernized["review_gate"] != nil || modernized["promotion_blocker"] != nil {
		t.Fatalf("draft gate: changes=%+v metadata=%v", changes[1], modernized)
	}
	// The captured observation survives the policy change.
	if captured, _ := modernized["http_version_capture"].(map[string]any); captured["reason_code"] != ReasonHTTP3DraftToken {
		t.Fatalf("observation lost: %v", modernized)
	}
	// Choosing inherit is not an explicit version choice: gates stay.
	script.Actions = []dsl.Action{action}
	script.Actions[0].Metadata = map[string]any{"execution_blocker": "curl_http3_race_not_modeled", "execution_blockers": []string{"curl_http3_race_not_modeled"}}
	script.Actions[0].HTTPVersion, _ = dsl.NewHTTPVersionOverride("2")
	if _, err := ApplyTransportEdits(script, []TransportEdit{{Path: "actions[0]", Origin: "https://example.test", From: "2", To: ""}}); err != nil {
		t.Fatal(err)
	}
	if script.Actions[0].Metadata["execution_blocker"] != "curl_http3_race_not_modeled" {
		t.Fatalf("inherit cleared a gate: %v", script.Actions[0].Metadata)
	}
}
