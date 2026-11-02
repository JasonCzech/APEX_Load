package requestimport

import (
	"strings"
	"testing"
)

func replaceLiterals(value string, vars map[string]string) string {
	for literal, template := range vars {
		value = strings.ReplaceAll(value, literal, template)
	}
	return value
}

// Correlating query metadata copies it only when a value changes, and never
// mutates an entry another action may share.
func TestCorrelateHARQueryStringValuesCopiesOnlyOnChange(t *testing.T) {
	shared := map[string]any{"name": "sid", "value": "Rec0rdedSid"}
	other := map[string]any{"name": "page", "value": "home"}
	items := []any{shared, other, "not an entry"}
	metadata := map[string]any{HARQueryStringMetadataKey: items}

	calls := 0
	counting := func(value string, vars map[string]string) string {
		calls++
		return replaceLiterals(value, vars)
	}
	CorrelateHARQueryStringValues(metadata, map[string]string{"Unrelated": "{{corr_x}}"}, counting)
	if got, ok := metadata[HARQueryStringMetadataKey].([]any); !ok || &got[0] != &items[0] {
		t.Fatalf("unchanged query metadata was reallocated: %#v", metadata[HARQueryStringMetadataKey])
	}
	before := calls
	CorrelateHARQueryStringValues(metadata, nil, counting)
	if calls != before || before == 0 {
		t.Fatalf("apply calls = %d then %d, want some with vars and none without", before, calls)
	}

	CorrelateHARQueryStringValues(metadata, map[string]string{"Rec0rdedSid": "{{corr_sid}}"}, counting)
	got := metadata[HARQueryStringMetadataKey].([]any)
	if &got[0] == &items[0] {
		t.Fatal("changed query metadata was updated in place")
	}
	if entry := got[0].(map[string]any); entry["value"] != "{{corr_sid}}" || entry["name"] != "sid" {
		t.Fatalf("correlated entry = %#v", entry)
	}
	if shared["value"] != "Rec0rdedSid" {
		t.Fatalf("shared entry was mutated: %#v", shared)
	}
	if got[2] != "not an entry" {
		t.Fatalf("non-entry item = %#v", got[2])
	}

	typed := []map[string]any{{"name": "a", "value": "keep"}}
	typedMetadata := map[string]any{HARQueryStringMetadataKey: typed}
	CorrelateHARQueryStringValues(typedMetadata, map[string]string{"Other": "{{corr_o}}"}, replaceLiterals)
	if got := typedMetadata[HARQueryStringMetadataKey].([]map[string]any); &got[0] != &typed[0] {
		t.Fatal("unchanged typed query metadata was reallocated")
	}
}
