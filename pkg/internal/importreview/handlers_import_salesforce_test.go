package importreview

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// tokenHAR is a two-request capture whose second request replays a token from
// the first response. The Salesforce variant posts to Aura on Salesforce hosts.
func tokenHAR(tokenURL, useURL string) string {
	return `{
  "log": {
    "entries": [
      {
        "request": {"method": "GET", "url": "` + tokenURL + `", "headers": []},
        "response": {"status": 200, "headers": [], "content": {"mimeType": "application/json", "text": "{\"access_token\":\"tok-abcdef123456\"}"}}
      },
      {
        "request": {"method": "POST", "url": "` + useURL + `", "headers": [], "postData": {"mimeType": "application/json", "text": "{\"token\":\"tok-abcdef123456\"}"}},
        "response": {"status": 200, "headers": [], "content": {"mimeType": "application/json", "text": "{\"ok\":true}"}}
      }
    ]
  }
}`
}

var (
	salesforceTokenHAR = tokenHAR("https://acme.my.salesforce.com/login", "https://acme.lightning.force.com/aura")
	genericTokenHAR    = tokenHAR("https://shop.example.test/session", "https://shop.example.test/orders")
)

func convertTokenHAR(t *testing.T, data string) *har.ConvertResult {
	t.Helper()
	result, err := har.Convert([]byte(data), har.ConvertOptions{ScriptName: "Synthetic capture"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || len(result.Script.Actions) == 0 {
		t.Fatal("HAR conversion produced no actions")
	}
	return result
}

type analysisCall struct {
	BuildNative           bool
	BuildNativeIfDetected bool
	RequireDetected       bool
	SkipUndetectedReview  bool
}

// recordAnalyses wraps the real analyzer for one call without changing shared
// package state. The shared parsed document must reach that same analysis.
func recordAnalyses(t *testing.T, result *har.ConvertResult) (func(*dsl.Script, salesforce.AnalyzeOptions) (*salesforce.Review, error), *[]analysisCall) {
	t.Helper()
	calls := &[]analysisCall{}
	analyze := func(script *dsl.Script, opts salesforce.AnalyzeOptions) (*salesforce.Review, error) {
		*calls = append(*calls, analysisCall{BuildNative: opts.BuildNative, BuildNativeIfDetected: opts.BuildNativeIfDetected, RequireDetected: opts.RequireDetected, SkipUndetectedReview: opts.SkipUndetectedReview})
		if opts.Document == nil || opts.Document != result.Document || opts.Source != "har" {
			t.Fatal("analysis lost the shared document or source provenance")
		}
		return salesforce.Analyze(script, opts)
	}
	return analyze, calls
}

func countWarnings(warnings []string, want string) int {
	count := 0
	for _, warning := range warnings {
		if warning == want {
			count++
		}
	}
	return count
}

// Detection and optional native preparation share one analysis for all import
// paths, including explicit Salesforce imports and native preparation failures.
func TestAugmentHARImportWithSalesforceAnalyzesOnceWhenDetectionIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name            string
		data            string
		buildNative     bool
		requireDetected bool
		want            []analysisCall
		detected        bool
	}{
		{"generic native", genericTokenHAR, true, true, []analysisCall{{BuildNativeIfDetected: true, RequireDetected: true, SkipUndetectedReview: true}}, false},
		{"generic", genericTokenHAR, false, true, []analysisCall{{RequireDetected: true, SkipUndetectedReview: true}}, false},
		{"salesforce native", salesforceTokenHAR, true, true, []analysisCall{{BuildNativeIfDetected: true, RequireDetected: true, SkipUndetectedReview: true}}, true},
		{"salesforce", salesforceTokenHAR, false, true, []analysisCall{{RequireDetected: true, SkipUndetectedReview: true}}, true},
		{"explicit salesforce import", salesforceTokenHAR, true, false, []analysisCall{{BuildNativeIfDetected: true}}, true},
		{"explicit generic native", genericTokenHAR, true, false, []analysisCall{{BuildNativeIfDetected: true}}, false},
		{"explicit salesforce review", salesforceTokenHAR, false, false, []analysisCall{{}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := convertTokenHAR(t, tc.data)
			analyze, calls := recordAnalyses(t, result)
			augmentation := augmentHARImportWithSalesforce(result, []byte(tc.data), "", "Synthetic capture", "har", tc.buildNative, false, tc.requireDetected, analyze)
			if !reflect.DeepEqual(*calls, tc.want) {
				t.Fatalf("analyses = %+v, want %+v", *calls, tc.want)
			}
			if augmentation.Detected != tc.detected {
				t.Fatalf("detected = %v, want %v", augmentation.Detected, tc.detected)
			}
		})
	}
}

// A malformed capture fails once and does not replace the existing HAR draft.
func TestAugmentHARImportWithSalesforceDoesNotRetryReviewFailures(t *testing.T) {
	result := convertTokenHAR(t, salesforceTokenHAR)
	analyze, calls := recordAnalyses(t, result)
	augmentation := augmentHARImportWithSalesforce(result, []byte("{"), "", "Synthetic capture", "har", true, false, true, analyze)
	if want := []analysisCall{{BuildNativeIfDetected: true, RequireDetected: true, SkipUndetectedReview: true}}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("analyses = %+v, want %+v", *calls, want)
	}
	if augmentation.Detected || augmentation.Script != result.Script {
		t.Fatalf("failed review changed the draft: detected %v, HAR draft kept %v", augmentation.Detected, augmentation.Script == result.Script)
	}
}

func TestAugmentHARImportWithSalesforceBuildsNativeDraft(t *testing.T) {
	result := convertTokenHAR(t, salesforceTokenHAR)
	converterWarnings := slices.Clone(result.Warnings)
	augmentation := AugmentHARImportWithSalesforce(result, []byte(salesforceTokenHAR), "", "Synthetic capture", "har", true, false, true)
	if !augmentation.Detected || augmentation.Source != "salesforce_har" || augmentation.Script == result.Script {
		t.Fatalf("native draft not selected: detected %v, source %q, HAR draft kept %v", augmentation.Detected, augmentation.Source, augmentation.Script == result.Script)
	}
	if augmentation.Summary["generation_mode"] != "salesforce_native_http" {
		t.Fatalf("generation mode = %v, want salesforce_native_http", augmentation.Summary["generation_mode"])
	}
	if !slices.Equal(result.Warnings, converterWarnings) {
		t.Fatalf("warnings = %v, want only the converter's %v", result.Warnings, converterWarnings)
	}
}

// A detected capture whose native build fails must keep the HAR draft, the full
// review including attempted-native diagnostics, and one native-failure warning.
func TestAugmentHARImportWithSalesforceKeepsReviewWhenNativeBuildFails(t *testing.T) {
	result := convertTokenHAR(t, salesforceTokenHAR)
	// The native builder clones the script through JSON, which rejects NaN;
	// detection never marshals the script.
	if result.Script.Actions[0].Metadata == nil {
		result.Script.Actions[0].Metadata = map[string]any{}
	}
	result.Script.Actions[0].Metadata["unencodable"] = math.NaN()
	want, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{HARData: []byte(salesforceTokenHAR)})
	if err != nil {
		t.Fatalf("detection review: %v", err)
	}
	// This synthetic token request is deliberately not a valid Aura envelope.
	// Attempting native preparation adds its blocker before cloning fails; a
	// second nonnative analysis would lose that diagnostic. Preserve every other
	// review field from the independent nonnative baseline below.
	const nativeRequestBlocker = "Imported Aura requests could not be validated; native replay is gated until those request envelopes are repaired."
	if want.AuraOutcomes == nil || want.AuraOutcomes.InvalidRequests != 1 || slices.Contains(want.Blockers, nativeRequestBlocker) {
		t.Fatalf("fixture does not isolate attempted-native diagnostics: %+v", want)
	}
	want.Blockers = append(want.Blockers, nativeRequestBlocker)
	_, nativeErr := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{BuildNative: true, HARData: []byte(salesforceTokenHAR)})
	if nativeErr == nil {
		t.Fatal("native build accepted an unencodable script")
	}

	analyze, calls := recordAnalyses(t, result)
	augmentation := augmentHARImportWithSalesforce(result, []byte(salesforceTokenHAR), "", "Synthetic capture", "har", true, false, true, analyze)
	if wantCalls := []analysisCall{{BuildNativeIfDetected: true, RequireDetected: true, SkipUndetectedReview: true}}; !reflect.DeepEqual(*calls, wantCalls) {
		t.Fatalf("analyses = %+v, want %+v", *calls, wantCalls)
	}
	if !augmentation.Detected || augmentation.Source != "har" || augmentation.Script != result.Script {
		t.Fatalf("HAR draft not kept: detected %v, source %q, HAR draft kept %v", augmentation.Detected, augmentation.Source, augmentation.Script == result.Script)
	}
	if mode := augmentation.Summary["generation_mode"]; mode == "salesforce_native_http" {
		t.Fatalf("generation mode %v advertised without a native script", mode)
	}
	warning := "Salesforce native preview failed: " + nativeErr.Error()
	if count := countWarnings(result.Warnings, warning); count != 1 {
		t.Fatalf("warnings = %v, want %q once", result.Warnings, warning)
	}
	got, err := json.Marshal(augmentation.ReviewForResponse)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(SalesforceReviewForResponse(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expected) {
		t.Fatalf("review = %s, want the full attempted-native review %s", got, expected)
	}
}
