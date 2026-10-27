package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// The legacy HAR default applies every generic correlation, but the native
// Salesforce rewrite must keep its historical high-confidence threshold unless
// the caller explicitly asked for every correlation.
func TestAugmentSalesforceUsesNativeHighConfidenceThreshold(t *testing.T) {
	tests := []struct {
		name            string
		correlationMode string
		wantHighConf    bool
	}{
		{name: "legacy default", wantHighConf: true},
		{name: "explicit all", correlationMode: "all", wantHighConf: false},
		{name: "explicit high_confidence", correlationMode: "high_confidence", wantHighConf: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := har.Convert([]byte(salesforceTokenHAR), har.ConvertOptions{
				ScriptName:      "Synthetic capture",
				CorrelationMode: tc.correlationMode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.EffectiveSettings == nil {
				t.Fatal("HAR conversion returned no effective settings")
			}
			var got *salesforce.AnalyzeOptions
			analyze := func(script *dsl.Script, opts salesforce.AnalyzeOptions) (*salesforce.Review, error) {
				captured := opts
				got = &captured
				return salesforce.Analyze(script, opts)
			}
			augmentHARImportWithSalesforceProfile(result, []byte(salesforceTokenHAR), "", "Synthetic capture", "har", true, false, true, nil, analyze)
			if got == nil {
				t.Fatal("Salesforce analysis did not run")
			}
			if got.HighConfidenceOnly == nil {
				t.Fatal("HighConfidenceOnly was not forwarded to Salesforce analysis")
			}
			if *got.HighConfidenceOnly != tc.wantHighConf {
				t.Fatalf("HighConfidenceOnly = %v, want %v (generic threshold %v)", *got.HighConfidenceOnly, tc.wantHighConf, result.EffectiveSettings.HighConfidenceOnly)
			}
		})
	}
}
