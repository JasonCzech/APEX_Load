package dslwalk

import (
	"reflect"
	"testing"
)

// TestMetadataBoolAcceptsBothWireForms covers why this helper exists: the same
// logical flag reaches the DSL as a real bool from Go importer code and as the
// string "true" after a JSON round trip.
func TestMetadataBoolAcceptsBothWireForms(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     bool
	}{
		{name: "native bool", metadata: map[string]any{"review_required": true}, want: true},
		{name: "string true", metadata: map[string]any{"review_required": "true"}, want: true},
		{name: "string mixed case", metadata: map[string]any{"review_required": " TRUE "}, want: true},
		{name: "string false", metadata: map[string]any{"review_required": "false"}},
		{name: "native false", metadata: map[string]any{"review_required": false}},
		{name: "wrong type", metadata: map[string]any{"review_required": 1}},
		{name: "missing", metadata: map[string]any{}},
		{name: "nil map", metadata: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MetadataBool(test.metadata, "review_required"); got != test.want {
				t.Fatalf("MetadataBool = %v, want %v", got, test.want)
			}
		})
	}
}

// TestMetadataStringDoesNotNormalizeCase is the load-bearing property of the
// split between extraction and normalization.
//
// pkg/api lowercases gate names because it compares them to fixed literals;
// pkg/convert/importreview preserves case because findings are displayed. If
// this helper folded case in, importreview would silently start lowercasing
// review output — which is exactly the kind of behavior change a "remove the
// duplication" refactor is prone to introducing.
func TestMetadataStringDoesNotNormalizeCase(t *testing.T) {
	metadata := map[string]any{"review_gate": "  Bruno_JavaScript  "}
	if got := MetadataString(metadata, "review_gate"); got != "Bruno_JavaScript" {
		t.Fatalf("MetadataString = %q, want the trimmed value with case preserved", got)
	}
	if got := MetadataString(map[string]any{"review_gate": 7}, "review_gate"); got != "" {
		t.Fatalf("MetadataString on a non-string = %q, want empty", got)
	}
	if got := MetadataString(nil, "review_gate"); got != "" {
		t.Fatalf("MetadataString on a nil map = %q, want empty", got)
	}
}

// TestMetadataStringsPreservesSourceOrder is the companion property: ordering
// and deduplication are the caller's choice. pkg/api keeps source order so a
// blocker message reads the way the importer wrote it; importreview sorts for
// stable review output.
func TestMetadataStringsPreservesSourceOrder(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     []string
	}{
		{
			name:     "string slice",
			metadata: map[string]any{"missing_variables": []string{"zeta", "alpha", "zeta"}},
			want:     []string{"zeta", "alpha", "zeta"},
		},
		{
			name:     "any slice from JSON",
			metadata: map[string]any{"missing_variables": []any{"zeta", "alpha"}},
			want:     []string{"zeta", "alpha"},
		},
		{
			name:     "drops empties and non-strings",
			metadata: map[string]any{"missing_variables": []any{"zeta", "", "  ", 7, "alpha"}},
			want:     []string{"zeta", "alpha"},
		},
		{name: "missing", metadata: map[string]any{}},
		{name: "nil map", metadata: nil},
		{name: "wrong type", metadata: map[string]any{"missing_variables": "zeta"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MetadataStrings(test.metadata, "missing_variables")
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("MetadataStrings = %#v, want %#v", got, test.want)
			}
		})
	}
}
