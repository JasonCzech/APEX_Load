package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestReplayMissingVariablesSaveAllDerivedKeys(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			{
				Name: "list", Type: "http_request", Target: "/items",
				Extractors: []dsl.Extractor{
					{Name: "items", Source: "body", Type: "regex", Expression: `id=(\d+)`, Mode: "all"},
				},
			},
			{
				Name: "use-first", Type: "http_request", Target: "/items/{{var.items_1}}",
			},
			{
				Name: "use-count", Type: "http_request", Target: "/count/{{var.items_count}}",
			},
			{
				Name: "bare-name", Type: "http_request", Target: "/bare/{{var.items}}",
			},
			{
				Name: "truly-missing", Type: "http_request", Target: "/x/{{var.unrelated}}",
			},
		},
	}

	missing := replayMissingVariables(script, nil)

	for _, ref := range missing {
		if ref == "items_1" || ref == "items_count" {
			t.Errorf("save-all derived key %q must count as satisfied", ref)
		}
	}
	var sawBare, sawUnrelated bool
	for _, ref := range missing {
		if ref == "items" {
			sawBare = true
		}
		if ref == "unrelated" {
			sawUnrelated = true
		}
	}
	if !sawBare {
		t.Error("bare name of a save-all extractor is never set and must be reported missing")
	}
	if !sawUnrelated {
		t.Error("genuinely missing variables must still be reported")
	}
}

func TestReplayMissingVariablesSequenceChildExtractors(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			{
				Name: "seq", Type: "http_sequence", Method: "SEQUENCE", Target: "/login",
				Params: map[string]any{"requests": []any{
					map[string]any{
						"name": "login", "type": "POST", "method": "POST", "target": "/login",
						"extractors": []any{map[string]any{
							"name": "token", "source": "body", "type": "jsonpath", "expression": "$.token",
						}},
					},
					map[string]any{
						"name": "list", "type": "GET", "method": "GET", "target": "/list?t={{var.token}}",
						"extractors": []any{map[string]any{
							"name": "items", "source": "body", "type": "regex", "expression": "id=(\\d+)", "mode": "all",
						}},
					},
				}},
			},
			{Name: "use", Type: "http_request", Target: "/items/{{var.items_1}}/of/{{var.items_count}}"},
		},
	}

	missing := replayMissingVariables(script, nil)
	if len(missing) != 0 {
		t.Errorf("sequence-child extractors must satisfy refs (intra-sequence and downstream); missing = %v", missing)
	}
}
