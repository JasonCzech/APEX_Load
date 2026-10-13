package dsl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func liveTableSpec(t *testing.T) ActionSpec {
	t.Helper()
	for _, spec := range AllActionSpecs() {
		if spec.Type == "live_table" {
			return spec
		}
	}
	t.Fatal("live_table is not in the catalog")
	return ActionSpec{}
}

// A valid value for every live_table field, by params key.
var liveTableSamples = map[string]any{
	"alias": "orders", "result_prefix": "row", "record_id": "r1", "position": 1,
	"columns": []any{"status"}, "values": map[string]any{"status": "new"}, "expected": map[string]any{"status": "new"},
	"expected_version": "1", "delta": "1", "lease_ref": "lease1", "lease_ms": 1000, "wait_ms": 100,
	"limit": 5, "cursor": "c1", "on_empty": "skip", "on_condition": "skip",
}

func paramKey(field string) string { return strings.TrimPrefix(field, "params.") }

func withFields(base map[string]any, fields ...string) map[string]any {
	next := make(map[string]any, len(base)+len(fields))
	for key, value := range base {
		next[key] = value
	}
	for _, field := range fields {
		next[paramKey(field)] = liveTableSamples[paramKey(field)]
	}
	return next
}

func without(base map[string]any, field string) map[string]any {
	next := make(map[string]any, len(base))
	for key, value := range base {
		if key != paramKey(field) {
			next[key] = value
		}
	}
	return next
}

// TestLiveTableVariantsMatchTheValidator: an editor built from the catalog's
// variants offers only what the validator accepts. For every operation, its
// required fields (with any one choice) pass; dropping a required field or a
// choice, giving two choices, adding a field it does not allow, or giving a
// counted field the wrong count fails; each optional field and the defaults
// pass.
func TestLiveTableVariantsMatchTheValidator(t *testing.T) {
	spec := liveTableSpec(t)
	if spec.Discriminator != "params.operation" {
		t.Fatalf("discriminator = %q", spec.Discriminator)
	}
	var operations []string
	var specRequired []string
	allFields := map[string]bool{}
	for _, field := range spec.Fields {
		allFields[field.Name] = true
		if field.Name == spec.Discriminator {
			operations = append([]string(nil), field.Enum...)
		} else if field.Required {
			specRequired = append(specRequired, field.Name)
		}
		if _, ok := liveTableSamples[paramKey(field.Name)]; !ok && field.Name != spec.Discriminator {
			t.Fatalf("no sample value for %s", field.Name)
		}
	}
	values := make([]string, 0, len(spec.Variants))
	for _, variant := range spec.Variants {
		values = append(values, variant.Value)
	}
	if !slices.Equal(slices.Sorted(slices.Values(values)), slices.Sorted(slices.Values(operations))) {
		t.Fatalf("variants %v do not cover the operations %v", values, operations)
	}
	accepts := func(params map[string]any) error {
		_, err := ParseLiveTableAction(params)
		return err
	}
	for _, variant := range spec.Variants {
		t.Run(variant.Value, func(t *testing.T) {
			if variant.Label == "" || variant.Group == "" || variant.Summary == "" {
				t.Fatalf("variant %q needs a label, group and summary", variant.Value)
			}
			allowed := map[string]bool{}
			for _, field := range variant.Allowed {
				if !allFields[field] {
					t.Fatalf("allowed field %s is not a catalog field", field)
				}
				allowed[field] = true
			}
			named := map[string]bool{spec.Discriminator: true}
			for _, field := range append(append([]string(nil), specRequired...), variant.Required...) {
				named[field] = true
				if !allowed[field] {
					t.Fatalf("required field %s is not allowed", field)
				}
			}
			for _, choice := range variant.Choices {
				for _, field := range choice {
					named[field] = true
					if !allowed[field] {
						t.Fatalf("choice field %s is not allowed", field)
					}
				}
			}
			base := withFields(map[string]any{"operation": variant.Value}, append(append([]string(nil), specRequired...), variant.Required...)...)
			minimal := []map[string]any{base}
			if len(variant.Choices) > 0 {
				minimal = minimal[:0]
				for _, choice := range variant.Choices {
					minimal = append(minimal, withFields(base, choice...))
				}
			}
			for _, params := range minimal {
				if err := accepts(params); err != nil {
					t.Fatalf("minimal %v rejected: %v", params, err)
				}
			}
			first := minimal[0]
			for _, field := range append(append([]string(nil), specRequired...), variant.Required...) {
				if err := accepts(without(first, field)); err == nil {
					t.Errorf("accepted without required %s", field)
				}
			}
			for index, choice := range variant.Choices {
				for _, field := range choice {
					if err := accepts(without(minimal[index], field)); err == nil {
						t.Errorf("accepted choice %v without %s", choice, field)
					}
				}
				if index > 0 {
					if err := accepts(withFields(minimal[0], choice...)); err == nil {
						t.Errorf("accepted two choices %v and %v together", variant.Choices[0], choice)
					}
				}
			}
			for field := range allowed {
				if named[field] {
					continue
				}
				if err := accepts(withFields(first, field)); err != nil {
					t.Errorf("rejected optional %s: %v", field, err)
				}
			}
			for field := range allFields {
				if allowed[field] || field == spec.Discriminator {
					continue
				}
				if err := accepts(withFields(first, field)); err == nil {
					t.Errorf("accepted %s, which the variant does not allow", field)
				}
			}
			for field, count := range variant.Counts {
				extra := withFields(first)
				items := make([]any, count+1)
				for index := range items {
					items[index] = "col" + string(rune('a'+index))
				}
				extra[paramKey(field)] = items
				if err := accepts(extra); err == nil {
					t.Errorf("accepted %d items in %s, which takes %d", count+1, field, count)
				}
			}
			if len(variant.Defaults) > 0 {
				params := withFields(map[string]any{"operation": variant.Value}, append(append([]string(nil), specRequired...), variant.Required...)...)
				for key, value := range variant.Defaults {
					params[key] = value
				}
				if len(variant.Choices) > 0 && !defaultsMakeAChoice(variant, params) {
					params = withFields(params, variant.Choices[0]...)
				}
				if err := accepts(params); err != nil {
					t.Errorf("defaults %v rejected: %v", variant.Defaults, err)
				}
			}
		})
	}
}

func defaultsMakeAChoice(variant ActionVariant, params map[string]any) bool {
	for _, choice := range variant.Choices {
		whole := true
		for _, field := range choice {
			if _, ok := params[paramKey(field)]; !ok {
				whole = false
			}
		}
		if whole {
			return true
		}
	}
	return false
}

// TestLiveTableDeltaTakesADecimalString: an increment past 2^53 survives as a
// decimal string; a non-canonical or out-of-range string is rejected.
func TestLiveTableDeltaTakesADecimalString(t *testing.T) {
	base := map[string]any{"alias": "orders", "operation": "increment", "position": 1, "columns": []any{"count"}}
	for _, delta := range []any{"9007199254740993", "-9223372036854775808", "0", 5, json.Number("7")} {
		params := withFields(base)
		params["delta"] = delta
		parsed, err := ParseLiveTableAction(params)
		if err != nil {
			t.Fatalf("delta %v rejected: %v", delta, err)
		}
		if delta == "9007199254740993" && int64(parsed.Delta) != 9007199254740993 {
			t.Fatalf("delta parsed as %d", parsed.Delta)
		}
	}
	for _, delta := range []any{"-0", "01", "+1", "1.5", "", "9223372036854775808", "{{var.n}}", 1.5} {
		params := withFields(base)
		params["delta"] = delta
		if _, err := ParseLiveTableAction(params); err == nil {
			t.Errorf("delta %#v accepted", delta)
		}
	}
}

// TestLiveTableCatalogFixtureIsFresh keeps the dashboard's live_table catalog
// fixture (its form tests) equal to the catalog the controller serves. Set
// APEXLOAD_UPDATE_LIVE_TABLE_FIXTURE=1 to rewrite it.
func TestLiveTableCatalogFixtureIsFresh(t *testing.T) {
	encoded, err := json.MarshalIndent(liveTableSpec(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "dashboard", "src", "components", "catalogActionFormFixture.json")
	if os.Getenv("APEXLOAD_UPDATE_LIVE_TABLE_FIXTURE") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (set APEXLOAD_UPDATE_LIVE_TABLE_FIXTURE=1 to write it)", path, err)
	}
	if !bytes.Equal(current, encoded) {
		t.Fatalf("%s is stale; set APEXLOAD_UPDATE_LIVE_TABLE_FIXTURE=1 and rerun", path)
	}
}
