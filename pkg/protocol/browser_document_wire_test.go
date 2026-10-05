package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func browserWireObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		t.Fatal("fixture decode")
	}
	return object
}
func browserWireNested(object map[string]any, path string) map[string]any {
	var value any = object
	if path != "" {
		for _, part := range strings.Split(path, ".") {
			if part == "0" {
				value = value.([]any)[0]
			} else {
				value = value.(map[string]any)[part]
			}
		}
	}
	return value.(map[string]any)
}
func TestBrowserDocumentWireRequiresExplicitFieldsAndTypes(t *testing.T) {
	o := browserTestObservation()
	evidence := &BrowserDocumentEvidence{Version: 2, SourceGeneration: o.SourceGeneration, Observations: []BrowserDocumentObservation{o}}
	summary := SummarizeBrowserDocumentObservations([]BrowserDocumentObservation{o}, 0, false)
	for _, fixture := range []struct {
		name   string
		value  any
		decode func([]byte) bool
		groups map[string]string
	}{
		{"evidence", evidence, func(raw []byte) bool { v, e := DecodeBrowserDocumentEvidence(raw); return e == nil && v != nil }, map[string]string{
			"":                      "version source_generation observations dropped_observations incomplete issued_requests in_flight_requests dropped_request_identities",
			"observations.0":        "version source_generation document_id lifecycle_id frame_id navigation_id measurement_id revision metric algorithm algorithm_sha256 unit engine device_profile frame_scope lifecycle_phase state boundary window_start window_end captured_at available qualified",
			"observations.0.cohort": "version browser_version platform viewport_width viewport_height device_scale_factor cpu_throttle_rate network_latency_ms network_bandwidth_kbps shaping_applied new_user_policy new_user_policy_applied",
		}},
		{"summary", summary, func(raw []byte) bool { v, e := DecodeBrowserDocumentSummary(raw); return e == nil && v != nil }, map[string]string{
			"":                       "version metrics dropped_observations incomplete",
			"metrics.0":              "metric algorithm algorithm_sha256 unit engine device_profile frame_scope lifecycle_phase eligible_final unqualified_final unavailable_final censored provisional distribution",
			"metrics.0.distribution": "count sum min max buckets",
			"metrics.0.cohort":       "version browser_version platform viewport_width viewport_height device_scale_factor cpu_throttle_rate network_latency_ms network_bandwidth_kbps shaping_applied new_user_policy new_user_policy_applied",
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw, _ := json.Marshal(fixture.value)
			if !fixture.decode(raw) {
				t.Fatal("canonical complete fixture refused")
			}
			for path, fields := range fixture.groups {
				for _, field := range strings.Fields(fields) {
					t.Run(path+"/"+field, func(t *testing.T) {
						object := browserWireObject(t, fixture.value)
						parent := browserWireNested(object, path)
						delete(parent, field)
						missing, _ := json.Marshal(object)
						if fixture.decode(missing) {
							t.Fatal("missing evidence became an observed zero/false")
						}
						object = browserWireObject(t, fixture.value)
						parent = browserWireNested(object, path)
						if _, collection := parent[field].([]any); !collection {
							parent[field] = nil
							bad, _ := json.Marshal(object)
							if fixture.decode(bad) {
								t.Fatal("null required scalar/struct became observed evidence")
							}
						}
					})
				}
			}
			for _, suffix := range []string{`,"incomplete":false}`, `,"\u0069ncomplete":false}`, `,"Incomplete":false}`, `,"unknown":false}`} {
				duplicate := strings.TrimSuffix(string(raw), "}") + suffix
				if fixture.decode([]byte(duplicate)) {
					t.Fatal("ambiguous or unknown wire key accepted")
				}
			}
		})
	}
}
func TestBrowserDocumentWireExplicitEmptyCollectionsRemainCompatible(t *testing.T) {
	gen := "0123456789abcdef0123456789abcdef"
	for _, observations := range [][]BrowserDocumentObservation{nil, {}} {
		raw, _ := json.Marshal(BrowserDocumentEvidence{Version: 2, SourceGeneration: gen, Observations: observations})
		if got, err := DecodeBrowserDocumentEvidence(raw); err != nil || got == nil || got.Incomplete || got.IssuedRequests != 0 {
			t.Fatal("explicit empty producer population refused")
		}
	}
	for _, metrics := range [][]BrowserDocumentMetricSummary{nil, {}} {
		raw, _ := json.Marshal(BrowserDocumentSummary{Version: 2, Metrics: metrics})
		if got, err := DecodeBrowserDocumentSummary(raw); err != nil || got == nil || got.Incomplete {
			t.Fatal("explicit empty producer summary refused")
		}
	}
	for _, raw := range []string{`{"version":2}`, `{"version":2,"source_generation":"` + gen + `"}`, `{"version":2,"metrics":null,"dropped_observations":0}`, `null`} {
		if got, err := DecodeBrowserDocumentEvidence([]byte(raw)); err == nil || got != nil {
			t.Fatal("incomplete envelope admitted")
		}
		if got, err := DecodeBrowserDocumentSummary([]byte(raw)); err == nil || got != nil {
			t.Fatal("incomplete summary admitted")
		}
	}
}
func TestBrowserDocumentWireRefusesWrongTypesNestedDuplicatesAndMalformedBuckets(t *testing.T) {
	o := browserTestObservation()
	summary := SummarizeBrowserDocumentObservations([]BrowserDocumentObservation{o}, 0, false)
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["incomplete"] = "false" },
		func(m map[string]any) { m["dropped_observations"] = false },
		func(m map[string]any) { browserWireNested(m, "metrics.0")["eligible_final"] = "1" },
		func(m map[string]any) { browserWireNested(m, "metrics.0.distribution")["buckets"] = []any{} },
		func(m map[string]any) { browserWireNested(m, "metrics.0.distribution")["buckets"] = nil },
		func(m map[string]any) { browserWireNested(m, "metrics.0.cohort")["headless"] = "true" },
	} {
		m := browserWireObject(t, summary)
		change(m)
		raw, _ := json.Marshal(m)
		if got, err := DecodeBrowserDocumentSummary(raw); err == nil || got != nil {
			t.Fatal("wrong type or bucket shape admitted")
		}
	}
	raw, _ := json.Marshal(summary)
	invalidUTF8 := strings.Replace(string(raw), `"device_profile":""`, "\"device_profile\":\""+string([]byte{0xff})+"\"", 1)
	if got, err := DecodeBrowserDocumentSummary([]byte(invalidUTF8)); err == nil || got != nil {
		t.Fatal("corrupt raw UTF-8 identity silently rewritten")
	}
	duplicated := strings.Replace(string(raw), `"count":1`, `"count":1,"count":1`, 1)
	if got, err := DecodeBrowserDocumentSummary([]byte(duplicated)); err == nil || got != nil {
		t.Fatal("nested duplicate accepted")
	}
	if got, err := DecodeBrowserDocumentSummary(append(raw, []byte(`{}`)...)); err == nil || got != nil {
		t.Fatal("trailing value accepted")
	}
	if got, err := DecodeBrowserDocumentSummary([]byte(strings.Repeat(" ", MaxBrowserDocumentSummaryBytes+1))); err == nil || got != nil {
		t.Fatal("oversize summary accepted")
	}
}
