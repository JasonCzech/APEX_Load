package importreview

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCanonicalImportResponseCarriesDraftOnceAndPreservesConversionFacts(t *testing.T) {
	body := "unique-import-body-" + strings.Repeat("x", 32<<10)
	script := &dsl.Script{Name: "Imported", Protocol: "http", Actions: []dsl.Action{{Name: "Request", Type: "request", Method: "POST", Target: "/", Body: body}}}
	diagnostics := diag.Diagnostics{
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: diag.Info, Location: "entry 1"},
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: diag.Info, Location: "entry 2"},
	}
	type conversion struct {
		Script      *dsl.Script      `json:"script"`
		Scripts     []*dsl.Script    `json:"scripts,omitempty"`
		Stats       map[string]int   `json:"stats"`
		Unsupported diag.Diagnostics `json:"unsupported,omitempty"`
		Format      string           `json:"format"`
		Private     string           `json:"-"`
	}
	converted := conversion{Script: script, Scripts: []*dsl.Script{script}, Stats: map[string]int{"actions": 1}, Unsupported: diagnostics, Format: "har", Private: "do-not-serialize"}
	opts := ScriptImportResponseOptions{Result: converted, Script: script, Scripts: converted.Scripts, Stats: converted.Stats, Diagnostics: diagnostics, UnsupportedElements: diagnostics.Strings()}
	legacy, legacyPrivate := NewScriptImportResponseWithProjection(opts)
	opts.CanonicalResponse = true
	compact, compactPrivate := NewScriptImportResponseWithProjection(opts)
	if !reflect.DeepEqual(legacyPrivate, compactPrivate) {
		t.Fatal("wire negotiation changed private projections")
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	compactJSON, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(legacyJSON), body) != 4 || strings.Count(string(compactJSON), body) != 1 {
		t.Fatal("draft aliases are missing from legacy or duplicated in canonical response")
	}
	if len(compactJSON)*2 >= len(legacyJSON) {
		t.Fatalf("wire payload did not shrink: legacy=%d compact=%d", len(legacyJSON), len(compactJSON))
	}
	if strings.Contains(string(compactJSON), "do-not-serialize") {
		t.Fatal("serialized private converter state")
	}
	facts := compact["result"].(map[string]any)
	if facts["format"] != "har" || len(facts) != 1 {
		t.Fatalf("unexpected conversion facts: %#v", facts)
	}
	if compact["primary_script_index"] != 0 || compact["unsupported_from_diagnostics"] != true {
		t.Fatalf("missing aliases: %#v", compact)
	}
	if grouped := compact["diagnostics"].(diag.Diagnostics); len(grouped) != 1 || grouped[0].Count != 2 {
		t.Fatalf("diagnostics not grouped: %+v", grouped)
	}
	if script.Actions[0].Body != body || converted.Script != script {
		t.Fatal("mutated converter")
	}
}

func TestCanonicalImportPrimaryAndDistinctFacts(t *testing.T) {
	a := &dsl.Script{Name: "first", Protocol: "http"}
	b := &dsl.Script{Name: "second", Protocol: "http"}
	for _, tc := range []struct {
		name      string
		scripts   []*dsl.Script
		primary   *dsl.Script
		wantIndex any
		count     int
	}{
		{"second", []*dsl.Script{a, b}, b, 1, 2},
		{"primary-only", nil, b, 0, 1},
		{"missing-member", []*dsl.Script{a}, b, 1, 2},
		{"no-primary", []*dsl.Script{a}, nil, nil, 1},
		{"empty", nil, nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := ScriptImportResponseOptions{CanonicalResponse: true, Scripts: tc.scripts, Script: tc.primary,
				Stats: map[string]int{"count": 2}, Result: map[string]any{"stats": map[string]int{"count": 1}, "conversion_only": true},
				Diagnostics: diag.Diagnostics{{Element: "note", Severity: diag.Info}}, UnsupportedElements: []string{"different actionable warning"}}
			got, _ := NewScriptImportResponseWithProjection(opts)
			if got["primary_script_index"] != tc.wantIndex || len(got["scripts"].([]any)) != tc.count {
				t.Fatalf("unexpected primary: %#v", got)
			}
			if got["result"].(map[string]any)["stats"].(map[string]any)["count"] != float64(1) {
				t.Fatal("lost distinct conversion stats")
			}
			if !reflect.DeepEqual(got["unsupported_elements"], opts.UnsupportedElements) {
				t.Fatal("lost distinct unsupported notes")
			}
		})
	}
}

func TestCanonicalResultFieldsRetainUnrecognizedShapes(t *testing.T) {
	for _, input := range []any{nil, (*dsl.Script)(nil), []string{"review"}, struct{ *dsl.Script }{&dsl.Script{Name: "embedded"}}} {
		if _, ok := importResultFields(input); ok {
			t.Fatalf("guessed unknown JSON contract: %#v", input)
		}
	}
}

func TestCanonicalSalesforcePreviewUsesExistingDraftReference(t *testing.T) {
	body := "native-preview-body-" + strings.Repeat("x", 32<<10)
	script := &dsl.Script{Name: "Native", Protocol: "http", Actions: []dsl.Action{{Type: "request", Method: "POST", Target: "/", Body: body}}}
	review := &salesforce.Review{Detected: true, NativePreview: &salesforce.NativePreview{Script: script, NativePathDomain: "final_native"}}
	type archiveSummary struct {
		SalesforceReview *salesforce.Review `json:"salesforce_review,omitempty"`
		Empty            string             `json:"empty,omitempty"`
	}
	for _, summary := range []any{archiveSummary{SalesforceReview: review}, &archiveSummary{SalesforceReview: review}, map[string]any{"salesforce_review": review, "source": "har"}} {
		for _, collection := range []bool{false, true} {
			opts := ScriptImportResponseOptions{Summary: summary, Script: script, Result: map[string]any{"summary": summary}}
			if collection {
				opts.Scripts = []*dsl.Script{{Name: "Other"}, script}
			}
			legacy, _ := NewScriptImportResponseWithProjection(opts)
			opts.CanonicalResponse = true
			compact, _ := NewScriptImportResponseWithProjection(opts)
			legacyJSON, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			compactJSON, err := json.Marshal(compact)
			if err != nil {
				t.Fatal(err)
			}
			index := 0
			if collection {
				index = 1
			}
			if compact["salesforce_preview_script_index"] != index || strings.Count(string(compactJSON), body) != 1 || strings.Count(string(legacyJSON), body) != 4 {
				t.Fatalf("preview not deduplicated: index=%v canonical copies=%d legacy copies=%d", compact["salesforce_preview_script_index"], strings.Count(string(compactJSON), body), strings.Count(string(legacyJSON), body))
			}
			if strings.Contains(string(compactJSON), `"empty"`) || compact["result_aliases"].(map[string]string)["summary"] != "summary" {
				t.Fatal("summary JSON semantics or converter alias lost")
			}
			if review.NativePreview.Script != script {
				t.Fatal("canonical projection mutated the original preview")
			}
		}
	}
}

func TestCanonicalSalesforcePreviewKeepsDistinctDraft(t *testing.T) {
	primary := &dsl.Script{Name: "Primary", Protocol: "http"}
	preview := &dsl.Script{Name: "Different preview", Protocol: "http"}
	review := &salesforce.Review{Detected: true, NativePreview: &salesforce.NativePreview{Script: preview}}
	opts := ScriptImportResponseOptions{CanonicalResponse: true, Script: primary, Summary: map[string]any{"salesforce_review": review}}
	response, _ := NewScriptImportResponseWithProjection(opts)
	if _, exists := response["salesforce_preview_script_index"]; exists || response["summary"].(map[string]any)["salesforce_review"] != review || review.NativePreview.Script != preview {
		t.Fatal("a distinct preview was removed or relabeled as the primary")
	}
	// An independently allocated but identical draft can use the same reference.
	clone := *primary
	review.NativePreview.Script = &clone
	response, _ = NewScriptImportResponseWithProjection(opts)
	if response["salesforce_preview_script_index"] != 0 {
		t.Fatal("identical preview clone was repeated")
	}
}

func TestCanonicalDirectSalesforceHARUsesAugmentedDraftOnce(t *testing.T) {
	data := []byte(`{"log":{"entries":[{"request":{"method":"GET","url":"https://acme.my.salesforce.com/services/data/v60.0/query"},"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"records\":[],\"done\":true}"}}}]}}`)
	for _, source := range []string{"har", "salesforce_har", "browser_recording_har"} {
		t.Run(source, func(t *testing.T) {
			converted, err := har.Convert(data, har.ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			augmented := AugmentHARImportWithSalesforce(converted, data, "project-a", "capture", source, true, false, true)
			opts := ScriptImportResponseOptions{CanonicalResponse: true, Script: augmented.Script, Summary: augmented.Summary, Result: converted}
			response, _ := NewScriptImportResponseWithProjection(opts)
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(encoded), `"generated_by":"apexload-salesforce-builder"`) != 1 || response["primary_script_index"] != 0 {
				t.Fatalf("direct capture native draft repeated or lost: %s", encoded)
			}
			if augmented.ReviewForResponse.NativePreview.Script != nil {
				t.Fatal("shared augmentation unexpectedly retained a duplicate preview")
			}
		})
	}
}
