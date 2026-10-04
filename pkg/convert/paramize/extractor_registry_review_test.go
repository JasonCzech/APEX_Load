package paramize

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNestedExtractorNamesRemainStableAcrossImportJSON(t *testing.T) {
	extractor := dsl.Extractor{Name: "recordid", Type: "jsonpath", Expression: "$.id"}
	for _, tc := range []struct {
		name       string
		extractors any
	}{
		{"typed slice", []dsl.Extractor{extractor}},
		{"map slice", []map[string]any{{"name": "recordid"}}},
		{"JSON slice", []any{map[string]any{"name": "recordid"}}},
		{"mixed typed JSON slice", []any{extractor, nil, "ignored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{Name: "nested imported request", Version: "1.0", Protocol: "http", Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []any{
				map[string]any{"name": "bootstrap", "type": "GET", "target": "/bootstrap", "extractors": tc.extractors},
				map[string]any{"name": "save", "type": "POST", "target": "/aura", "body": syntheticAuraBody(map[string]any{"recordId": "synthetic-record"})},
			}}}}}
			before := auraOnlyAdvice(script)
			if len(before.Suggestions) != 1 || before.Suggestions[0].VarName != "recordid_2" {
				t.Fatalf("suggestions = %+v, want recordid_2", before.Suggestions)
			}
			encoded, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			var decoded dsl.Script
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			after := auraOnlyAdvice(&decoded)
			if len(after.Suggestions) != 1 || after.Suggestions[0].ID != before.Suggestions[0].ID || after.Suggestions[0].VarName != before.Suggestions[0].VarName {
				t.Fatalf("import advice changed across JSON: before=%+v after=%+v", before.Suggestions, after.Suggestions)
			}
			applied, _, err := ApplySuggestions(script, before.Suggestions, map[string]bool{before.Suggestions[0].ID: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(applied.Parameters) != 1 || applied.Parameters[0].Name != "recordid_2" || newNameRegistry(applied).claim("recordid") != "recordid_3" {
				t.Fatalf("existing extractor and applied binding were not both reserved: %+v", applied.Parameters)
			}
		})
	}
}

func TestNestedExtractorRegistryIgnoresEmptyOrMalformedEntries(t *testing.T) {
	for _, value := range []any{nil, []dsl.Extractor{}, []any{nil, "ignored", map[string]any{"name": 123}}, []map[string]any{{"expression": "ignored"}}} {
		script := &dsl.Script{Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"type": "GET", "target": "/", "extractors": value}}}}}}
		if got := newNameRegistry(script).claim("recordid"); got != "recordid" {
			t.Fatalf("malformed entries reserved %q", got)
		}
	}
}
