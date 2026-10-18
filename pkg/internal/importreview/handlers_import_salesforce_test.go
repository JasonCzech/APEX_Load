package importreview

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
)

// salesforceTokenHAR is a two-request Salesforce capture whose Aura request
// replays a token from the login response.
const salesforceTokenHAR = `{
  "log": {
    "entries": [
      {
        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/login", "headers": []},
        "response": {"status": 200, "headers": [], "content": {"mimeType": "application/json", "text": "{\"access_token\":\"tok-abcdef123456\"}"}}
      },
      {
        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"mimeType": "application/json", "text": "{\"token\":\"tok-abcdef123456\"}"}},
        "response": {"status": 200, "headers": [], "content": {"mimeType": "application/json", "text": "{\"ok\":true}"}}
      }
    ]
  }
}`

func convertSalesforceTokenHAR(t *testing.T) *har.ConvertResult {
	t.Helper()
	result, err := har.Convert([]byte(salesforceTokenHAR), har.ConvertOptions{ScriptName: "Synthetic Salesforce"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || len(result.Script.Actions) == 0 {
		t.Fatal("HAR conversion produced no actions")
	}
	return result
}

func hasWarningPrefix(warnings []string, prefix string) bool {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}

func TestAugmentHARImportWithSalesforceBuildsNativeDraft(t *testing.T) {
	result := convertSalesforceTokenHAR(t)
	augmentation := AugmentHARImportWithSalesforce(result, []byte(salesforceTokenHAR), "", "Synthetic Salesforce", "har", true, false, true)
	if !augmentation.Detected || augmentation.Source != "salesforce_har" || augmentation.Script == result.Script {
		t.Fatalf("native draft not selected: detected %v, source %q", augmentation.Detected, augmentation.Source)
	}
	if augmentation.Summary["generation_mode"] != "salesforce_native_http" {
		t.Fatalf("generation mode = %v, want salesforce_native_http", augmentation.Summary["generation_mode"])
	}
	if hasWarningPrefix(result.Warnings, "Salesforce native preview failed: ") {
		t.Fatalf("unexpected native failure warning: %v", result.Warnings)
	}
}

// A detected capture whose native build fails must keep the HAR draft, the full
// detection review, and a native-failure warning.
func TestAugmentHARImportWithSalesforceKeepsReviewWhenNativeBuildFails(t *testing.T) {
	result := convertSalesforceTokenHAR(t)
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
	if _, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{BuildNative: true, HARData: []byte(salesforceTokenHAR)}); err == nil {
		t.Fatal("native build accepted an unencodable script")
	}

	augmentation := AugmentHARImportWithSalesforce(result, []byte(salesforceTokenHAR), "", "Synthetic Salesforce", "har", true, false, true)
	if !augmentation.Detected || augmentation.Source != "har" || augmentation.Script != result.Script {
		t.Fatalf("HAR draft not kept: detected %v, source %q", augmentation.Detected, augmentation.Source)
	}
	if mode := augmentation.Summary["generation_mode"]; mode == "salesforce_native_http" {
		t.Fatalf("generation mode %v advertised without a native script", mode)
	}
	if !hasWarningPrefix(result.Warnings, "Salesforce native preview failed: ") {
		t.Fatalf("native failure not reported: %v", result.Warnings)
	}
	got, err := json.Marshal(augmentation.ReviewForResponse)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(SalesforceReviewForResponse(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expected) || want.CorrelationConfidence == "" {
		t.Fatalf("review = %s, want the detection review %s", got, expected)
	}
}
