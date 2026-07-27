package srcparse

import (
	"encoding/json"
	"strings"
	"testing"
)

func findingKinds(findings []Finding) []FindingKind {
	kinds := make([]FindingKind, 0, len(findings))
	for _, finding := range findings {
		kinds = append(kinds, finding.Kind)
	}
	return kinds
}

func hasKind(findings []Finding, kind FindingKind) bool {
	for _, finding := range findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}

func TestInspectYAMLMappingKeysReportsStructuralAmbiguity(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want FindingKind
	}{
		{
			name: "merge key",
			yaml: "base: &base\n  timeout: 1\nchild:\n  <<: *base\n  retries: 2\n",
			want: MergeKey,
		},
		{
			name: "non scalar key",
			yaml: "? [a, b]\n: value\n",
			want: NonScalarKey,
		},
		{
			name: "exact duplicate key",
			yaml: "timeout: 1\ntimeout: 2\n",
			want: DuplicateKey,
		},
		{
			name: "case fold duplicate key",
			yaml: "Timeout: 1\ntimeout: 2\n",
			want: DuplicateKey,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := InspectYAMLMappingKeys([]byte(test.yaml), Limits{})
			if err != nil {
				t.Fatalf("InspectYAMLMappingKeys: %v", err)
			}
			if !hasKind(findings, test.want) {
				t.Fatalf("findings = %v, want a %s", findingKinds(findings), test.want)
			}
		})
	}
}

func TestInspectYAMLMappingKeysAcceptsOrdinaryDocuments(t *testing.T) {
	const spec = `openapi: "3.0.3"
info:
  title: Orders
  version: "1"
paths:
  /orders:
    get:
      responses:
        "200":
          description: ok
`
	findings, err := InspectYAMLMappingKeys([]byte(spec), Limits{})
	if err != nil {
		t.Fatalf("InspectYAMLMappingKeys: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none for an ordinary spec", findingKinds(findings))
	}
}

// TestSpecPolicyToleratesMergeKeysButStillReportsThem is the severity split that
// keeps real-world API specs importable. Anchors plus "<<" are idiomatic in
// hand-authored OpenAPI, so rejecting them would break valid uploads — but the
// caller still needs to know, because what the document contains depends on
// anchor resolution.
func TestSpecPolicyToleratesMergeKeysButStillReportsThem(t *testing.T) {
	const spec = "base: &base\n  timeout: 1\nchild:\n  <<: *base\n"
	findings, err := ValidateYAMLMappingKeys([]byte(spec), SpecYAMLPolicy(), Limits{})
	if err != nil {
		t.Fatalf("spec policy rejected a merge key: %v", err)
	}
	if !hasKind(findings, MergeKey) {
		t.Fatalf("findings = %v, want the tolerated merge key reported", findingKinds(findings))
	}
}

func TestStrictPolicyRejectsMergeKeys(t *testing.T) {
	const document = "base: &base\n  timeout: 1\nchild:\n  <<: *base\n"
	if _, err := ValidateYAMLMappingKeys([]byte(document), StrictYAMLPolicy(), Limits{}); err == nil {
		t.Fatal("strict policy accepted a merge key")
	}
}

func TestSpecPolicyRejectsDuplicateAndNonScalarKeys(t *testing.T) {
	for _, document := range []string{
		"timeout: 1\ntimeout: 2\n",
		"? [a, b]\n: value\n",
	} {
		if _, err := ValidateYAMLMappingKeys([]byte(document), SpecYAMLPolicy(), Limits{}); err == nil {
			t.Fatalf("spec policy accepted an ambiguous document: %q", document)
		}
	}
}

func TestValidateJSONRejectsAmbiguity(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate key", body: `{"a":1,"a":2}`},
		{name: "nested duplicate key", body: `{"outer":{"a":1,"a":2}}`},
		{name: "trailing value", body: `{"a":1} {"b":2}`},
		{name: "trailing token", body: `{"a":1}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateJSON([]byte(test.body), Limits{}); err == nil {
				t.Fatalf("ValidateJSON accepted %q", test.body)
			}
		})
	}
}

func TestValidateJSONRejectsExcessiveNesting(t *testing.T) {
	body := strings.Repeat("[", 40) + strings.Repeat("]", 40)
	if err := ValidateJSON([]byte(body), Limits{MaxDepth: 8}); err == nil {
		t.Fatal("ValidateJSON accepted nesting beyond the configured depth")
	}
	if err := ValidateJSON([]byte(body), Limits{}); err != nil {
		t.Fatalf("ValidateJSON rejected nesting inside the default depth: %v", err)
	}
}

// TestDecodeJSONPreservesNumericSpelling is why DecodeJSON exists rather than
// json.Unmarshal: a 64-bit identifier must survive a round trip. float64 would
// silently round it, and an imported request would then send a different id.
func TestDecodeJSONPreservesNumericSpelling(t *testing.T) {
	const body = `{"id":9007199254740993,"rate":1.50}`
	value, err := DecodeJSON([]byte(body), Limits{})
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want an object", value)
	}
	id, ok := object["id"].(json.Number)
	if !ok || id.String() != "9007199254740993" {
		t.Fatalf("id = %#v, want the exact json.Number spelling", object["id"])
	}
	rate, ok := object["rate"].(json.Number)
	if !ok || rate.String() != "1.50" {
		t.Fatalf("rate = %#v, want the authored decimal spelling", object["rate"])
	}
}

func TestDecodeJSONAcceptsOrdinaryDocuments(t *testing.T) {
	value, err := DecodeJSON([]byte(`{"a":[1,2,{"b":"c"}]}`), Limits{})
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if _, ok := value.(map[string]any); !ok {
		t.Fatalf("value = %#v, want an object", value)
	}
}
