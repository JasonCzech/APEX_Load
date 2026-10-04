package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

func TestSalesforceAuraOutcomeReportPersistsNativeAndGenericDrafts(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "generic", true: "native"}[native], func(t *testing.T) {
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(salesforceHARImportJSON(t), &payload); err != nil {
				t.Fatal(err)
			}
			// Record an explicit baseline failure while retaining valid request IDs.
			data := []byte(strings.ReplaceAll(payload.Content, "SUCCESS", "ERROR"))
			result, err := har.Convert(data, har.ConvertOptions{ScriptName: "Synthetic Salesforce"})
			if err != nil {
				t.Fatal(err)
			}
			protected := result.ProtectedRequirements.Take()
			defer clearCredentialRequirementValues(protected)
			augmentation := augmentHARImportWithSalesforce(result, data, "", "Synthetic Salesforce", "har", native, false, true)
			outcomes := augmentation.ReviewForResponse.AuraOutcomes
			if outcomes == nil || !outcomes.Complete || outcomes.StateCounts[aura.Error] != 1 || outcomes.MatchedActions != 1 {
				t.Fatalf("captured ERROR hidden: %#v", outcomes)
			}
			if augmentation.ReviewForResponse.NativePreview != nil && augmentation.ReviewForResponse.NativePreview.Script != nil {
				t.Fatal("response review contains executable preview")
			}
			foundWarning := false
			for _, section := range augmentation.ReviewSections {
				if section.Key == "salesforce_review" && section.Severity == "warning" {
					foundWarning = true
				}
			}
			if !foundWarning {
				t.Fatal("failed captured actions must have a warning section")
			}
			encoded, err := json.Marshal(augmentation.Script)
			if err != nil {
				t.Fatal(err)
			}
			var reopened dsl.Script
			if err := json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			preserved, err := json.Marshal(reopened.Metadata.Import.Preserved["salesforce_review"])
			if err != nil {
				t.Fatal(err)
			}
			var review salesforce.Review
			if err := json.Unmarshal(preserved, &review); err != nil {
				t.Fatal(err)
			}
			if review.AuraOutcomes == nil || review.AuraOutcomes.StateCounts[aura.Error] != 1 || review.AuraOutcomes.Batches[0].HAREntryID != 2 || review.AuraOutcomes.Batches[0].ActionPath != "actions[1]" {
				t.Fatalf("structural evidence lost after reopen: %#v", review.AuraOutcomes)
			}
			checks := 0
			for _, action := range reopened.Actions {
				for _, assertion := range action.Assertions {
					if assertion.Type == "aura_actions_success" {
						checks++
					}
				}
			}
			if native && checks != 1 {
				t.Fatal("native response assertion missing after reopen")
			}
			if !native && checks != 0 {
				t.Fatal("generic mode changed executable assertions")
			}
		})
	}
}

func TestSalesforceMalformedNativeDraftKeepsGateAndReviewAfterReopen(t *testing.T) {
	data := []byte(`{"log":{"entries":[{"request":{"method":"POST","url":"https://example.lightning.force.com/aura","postData":{"text":"message=%7B%22actions%22%3A%5B%7B%22descriptor%22%3A%22private_descriptor%22%7D%5D%7D"}},"response":{"status":200,"content":{"text":"{\"actions\":[{\"id\":\"private_id\",\"state\":\"SUCCESS\"}]}"}}}]}}`)
	result, err := har.Convert(data, har.ConvertOptions{ScriptName: "Malformed synthetic Aura"})
	if err != nil {
		t.Fatal(err)
	}
	protected := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protected)
	augmentation := augmentHARImportWithSalesforce(result, data, "", "Malformed synthetic Aura", "har", true, false, true)
	outcomes := augmentation.ReviewForResponse.AuraOutcomes
	if outcomes.Complete || outcomes.InvalidRequests != 1 || outcomes.IssueCounts[aura.InvalidID] != 1 {
		t.Fatalf("malformed import not explicit: %#v", outcomes)
	}
	encoded, err := json.Marshal(augmentation.Script)
	if err != nil {
		t.Fatal(err)
	}
	var reopened dsl.Script
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	findings, err := importreview.FindingsFromScript(&reopened)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Code == "salesforce_aura_request_requires_review" && finding.Gate == importreview.GateExecution {
			found = true
		}
	}
	if !found {
		t.Fatal("malformed native gate lost after reopen")
	}
	encoded, err = json.Marshal(outcomes)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"private_descriptor", "private_id", "example.lightning.force.com"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("new structural report disclosed source value %q", value)
		}
	}
}
