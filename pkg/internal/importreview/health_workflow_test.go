package importreview

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSharedStoredAuthoringHealthIncludesEveryValidationInput(t *testing.T) {
	for _, tc := range []struct {
		name, status, key, inputStatus string
		change                         func(*dsl.Script)
	}{
		{name: "ready", status: "ready", key: "validation_issues", inputStatus: "pass"},
		{name: "structural issue", status: "blocked", key: "validation_issues", inputStatus: "blocker", change: func(s *dsl.Script) { s.Actions = nil }},
		{name: "missing data source", status: "blocked", key: "data_source_fit", inputStatus: "blocker", change: func(s *dsl.Script) {
			s.Parameters = []dsl.Parameter{{Name: "user", Type: "file", DataSource: "missing", Column: "username"}}
		}},
		{name: "AI review", status: "blocked", key: "ai_guardrails", inputStatus: "blocker", change: func(s *dsl.Script) {
			s.GeneratedBy, s.GeneratedFrom = "ai", "ai_generated"
		}},
		{name: "runtime parity", status: "needs_review", key: "runtime_parity", inputStatus: "warning", change: func(s *dsl.Script) {
			s.Config.WebRuntime.AdditionalAttributes = map[string]string{"BusinessProcess": "Checkout"}
		}},
		{name: "replay gate", status: "blocked", key: "replay_readiness", inputStatus: "blocker", change: func(s *dsl.Script) {
			s.Actions[0].Metadata = map[string]any{"execution_blocker": "unsupported_source_behavior"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{ID: "recorded", Name: "Recorded request", Version: "1", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}}}
			if tc.change != nil {
				tc.change(script)
			}
			before, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			health := BuildStoredScriptAuthoringHealth(script)
			if health == nil || health.Status != tc.status {
				t.Fatalf("health=%#v, want %s", health, tc.status)
			}
			if len(health.Inputs) != 7 {
				t.Fatalf("lost health dimensions: %#v", health.Inputs)
			}
			found := false
			for _, input := range health.Inputs {
				if input.Key == tc.key {
					found = true
					if input.Status != tc.inputStatus {
						t.Fatalf("%s=%#v", tc.key, input)
					}
				}
			}
			if !found {
				t.Fatalf("missing health input %q", tc.key)
			}
			full := BuildScriptValidationResponse(script.ID, script)
			if !reflect.DeepEqual(*health, full.AuthoringHealth) {
				t.Fatal("stored and validation health differ")
			}
			after, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("health calculation changed the stored draft")
			}
		})
	}
	if got := BuildStoredScriptAuthoringHealth(nil); got != nil {
		t.Fatalf("nil draft has health: %#v", got)
	}
}

func TestSharedAuthoringHealthPreservesWarningsAndWireShape(t *testing.T) {
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{DownloadResources: "recorded"}}}
	warnings := ValidationWarnings(script)
	found := false
	for _, warning := range warnings {
		if warning == RecordedResourceMetadataWarning() && warning != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recorded metadata warning missing: %#v", warnings)
	}
	body, err := json.Marshal(BuildAuthoringHealth(nil, nil, nil, nil, nil, false))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 3 || wire["score"] == nil || wire["status"] == nil || wire["inputs"] == nil {
		t.Fatalf("health wire changed: %s", body)
	}
}
