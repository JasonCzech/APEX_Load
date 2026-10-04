package aurapayload

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func contractFixtureBody() string {
	encode := func(value string) string {
		return strings.ReplaceAll(url.QueryEscape(value), url.QueryEscape("{{var.shared}}"), "{{var.shared}}")
	}
	return "message=" + encode(`{"actions":[{"id":"a","descriptor":"fixture","params":{"input":"{\"recordId\":\"business\",\"echo\":\"{{var.shared}}\"}"}}]}`) + "&aura.context=" + encode(`{"fwuid":"{{var.shared}}"}`) + "&aura.token={{var.shared}}&other=prefix{{var.shared}}suffix"
}

func TestAuraTemplateContractPreservesOnlyPriorFormWireOccurrences(t *testing.T) {
	body := contractFixtureBody()
	contract, _, err := PromoteTemplateContractWithWorkAccounting(body, "", "", nil, MaxWorkBytes)
	if err != nil || len(contract.FormWireOccurrences) != 4 {
		t.Fatalf("prior wire occurrence count=%d err=%v", len(contract.FormWireOccurrences), err)
	}
	inspection, _ := Inspect(body, "")
	var edit Edit
	for _, leaf := range inspection.Leaves {
		if leaf.Key == "recordId" {
			edit = Edit{Route: leaf.Route, Shape: inspection.Shape, Expected: leaf.Value, Replacement: "{{var.shared}}"}
		}
	}
	changed, err := Replace(body, "", []Edit{edit})
	if err != nil {
		t.Fatal(err)
	}
	var wire, logical int
	actual, _, _, err := ResolveContextualWithWorkAccounting(changed, "", contract, func(value string, context TemplateContext) (string, string) {
		if strings.Contains(value, "{{var.shared}}") {
			if context.IsFormWireToken(0) {
				wire++
			} else {
				logical++
			}
		}
		return value, value
	}, MaxWorkBytes)
	if err != nil || wire != 4 || logical != 1 || actual != changed {
		t.Fatalf("occurrence semantics wire=%d logical=%d changed=%t err=%v", wire, logical, actual != changed, err)
	}
	retained, _, err := PromoteTemplateContractWithWorkAccounting(changed, "", TemplateValueEncodingJSONStrings, contract, MaxWorkBytes)
	if err != nil || len(retained.FormWireOccurrences) != 4 {
		t.Fatalf("successive apply changed prior semantics: %v", err)
	}
	for _, policy := range []string{TemplateValueEncodingQueryComponent, TemplateValueEncodingJSONStrings} {
		logical, _, err := PromoteTemplateContractWithWorkAccounting(body, "", policy, nil, MaxWorkBytes)
		if err != nil || len(logical.FormWireOccurrences) != 0 {
			t.Fatalf("logical policy %s marked old wire templates: %v", policy, err)
		}
	}
}

func TestAuraTemplateContractAdmissionBeforeCallbacks(t *testing.T) {
	body := contractFixtureBody()
	good, _, err := PromoteTemplateContractWithWorkAccounting(body, "", "", nil, MaxWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(good)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown root", func(v map[string]any) { v["extra"] = 1 }},
		{"unknown occurrence", func(v map[string]any) { v["form_wire_occurrences"].([]any)[0].(map[string]any)["extra"] = 1 }},
		{"wrong version", func(v map[string]any) { v["version"] = 2 }},
		{"fractional ordinal", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["occurrence_index"] = 0.5
		}},
		{"negative ordinal", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["occurrence_index"] = -1
		}},
		{"large ordinal", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["occurrence_index"] = MaxTemplateOccurrences
		}},
		{"duplicate", func(v map[string]any) {
			rows := v["form_wire_occurrences"].([]any)
			v["form_wire_occurrences"] = append(rows, rows[0])
		}},
		{"conflicting policies", func(v map[string]any) { v["literal_occurrences"] = []any{v["form_wire_occurrences"].([]any)[0]} }},
		{"oversize", func(v map[string]any) {
			rows := make([]any, MaxTemplateOccurrences+1)
			v["form_wire_occurrences"] = rows
		}},
		{"combined cardinality", func(v map[string]any) {
			rows := make([]any, MaxTemplateOccurrences)
			for i := range rows {
				rows[i] = map[string]any{"context_id": fmt.Sprintf("%064x", i), "occurrence_index": 0, "expression_sha256": strings.Repeat("a", 64)}
			}
			v["form_wire_occurrences"] = rows
			v["literal_occurrences"] = []any{map[string]any{"context_id": strings.Repeat("b", 64), "occurrence_index": 0, "expression_sha256": strings.Repeat("a", 64)}}
		}},
		{"stale context", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["context_id"] = strings.Repeat("a", 64)
		}},
		{"stale token", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["expression_sha256"] = strings.Repeat("a", 64)
		}},
		{"bad hash", func(v map[string]any) {
			v["form_wire_occurrences"].([]any)[0].(map[string]any)["expression_sha256"] = "bad"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed map[string]any
			_ = json.Unmarshal(encoded, &changed)
			tc.edit(changed)
			parsed, parseErr := ReadTemplateContract(changed)
			calls := 0
			if parseErr == nil {
				_, _, _, parseErr = ResolveContextualWithWorkAccounting(body, "", parsed, func(value string, _ TemplateContext) (string, string) { calls++; return value, value }, MaxWorkBytes)
			}
			if parseErr == nil || calls != 0 || strings.Contains(parseErr.Error(), "shared") {
				t.Fatalf("malformed contract admitted: calls=%d error=%v", calls, parseErr)
			}
		})
	}
	if _, _, _, err := ResolveContextualWithWorkAccounting(body, "windows-1252", good, func(value string, _ TemplateContext) (string, string) {
		t.Fatal("unsupported encoding resolved")
		return value, value
	}, MaxWorkBytes); err == nil {
		t.Fatal("unsupported source encoding admitted")
	}
	if _, _, _, err := ResolveContextualWithWorkAccounting(strings.Replace(body, "other=", "renamed=", 1), "", good, func(value string, _ TemplateContext) (string, string) {
		t.Fatal("changed form consumer resolved")
		return value, value
	}, MaxWorkBytes); err == nil {
		t.Fatal("same-ordinal changed form key admitted")
	}
}
