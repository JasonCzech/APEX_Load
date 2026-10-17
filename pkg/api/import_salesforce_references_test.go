package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNativeSelectedSourceReviewDomainIsCopiedAndDurable(t *testing.T) {
	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(salesforceHARImportJSON(t), &payload); err != nil {
		t.Fatal(err)
	}
	for _, native := range []bool{false, true} {
		result, err := har.Convert([]byte(payload.Content), har.ConvertOptions{ScriptName: "synthetic"})
		if err != nil {
			t.Fatal(err)
		}
		requirements := result.ProtectedRequirements.Take()
		defer clearCredentialRequirementValues(requirements)
		original := result.SourceReview
		augmentation := augmentHARImportWithSalesforce(result, []byte(payload.Content), "", "synthetic", "har", native, false, true)
		if original.TargetDomain != "" {
			t.Fatal("native selection changed the original generic source review")
		}
		want := ""
		if native {
			want = sourceview.OriginalImportTargetDomain
		}
		if result.SourceReview.TargetDomain != want || (native && result.SourceReview == original) {
			t.Fatal("native review domain was not attached to a private selected copy")
		}
		draft := importDraft{Script: augmentation.Script, Source: augmentation.Source, SourceReview: result.SourceReview}
		draft.AugmentWithAgentforceTransform(false)
		wire, err := json.Marshal(augmentation.Script)
		if err != nil {
			t.Fatal(err)
		}
		var reopened dsl.Script
		if err := json.Unmarshal(wire, &reopened); err != nil {
			t.Fatal(err)
		}
		review := sourceReviewValue(reopened.Metadata.Import.Preserved["source_review"])
		if review == nil || review.TargetDomain != want {
			t.Fatal("source domain lost after metadata/save JSON roundtrip")
		}
		if native {
			preserved := reopened.Metadata.Import.Preserved["salesforce_review"].(map[string]any)
			preview := preserved["native_preview"].(map[string]any)
			if preview["native_path_domain"] != salesforce.FinalNativePathDomain || reopened.Metadata.Import.Source != "salesforce" {
				t.Fatal("final native domain/provenance lost after reopen")
			}
		}
	}
}

func TestNativeReferencesBindAfterLastAgentforceTransform(t *testing.T) {
	script, data := syntheticAgentforceReferenceSource()
	review, err := salesforce.Analyze(script, salesforce.AnalyzeOptions{BuildNative: true, HARData: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.NativePreview.Correlations) == 0 {
		t.Fatal("synthetic captured correlation missing")
	}
	native := review.NativePreview.Script
	responseReview := salesforceReviewForResponse(review)
	source := originalImportSourceReview(&sourceview.Review{Mappings: []sourceview.Mapping{{ID: "bootstrap", SourceFile: "synthetic.har", TargetPath: "init[0]"}, {ID: "start", SourceFile: "synthetic.har", TargetPath: "actions[0]"}}})
	ensureScriptImportPreserved(native, "salesforce")["salesforce_review"] = responseReview
	oldCorrelations := responseReview.NativePreview.Correlations
	draft := importDraft{Script: native, Source: "salesforce_har", SourceReview: source}
	draft.AugmentWithAgentforceTransform(true)
	if len(native.Init) != 2 {
		t.Fatal("real Agentforce transform did not prepend its token action")
	}
	found := false
	for _, row := range responseReview.NativePreview.Correlations {
		if row.ProducerReference.HAREntryID == 1 {
			found = true
			if row.ProducerReference.OriginalActionPath != "init[0]" || row.ProducerReference.NativeActionPath != "init[1]" || row.ProducerReference.SourceMappingID != "bootstrap" {
				t.Fatal("last transform left a guessed/stale native path or lost original source linkage")
			}
		}
	}
	if !found || responseReview.NativePreview.NativePathDomain != salesforce.FinalNativePathDomain {
		t.Fatal("last-transform reference report not finalized")
	}
	// Response envelope must not retain the earlier correlation slice captured
	// before augmentation rebuilt its exact paths.
	envelope := newScriptImportResponse(scriptImportResponseOptions{Script: native, Correlations: oldCorrelations})
	rows, ok := envelope["correlations"].([]salesforce.CorrelationCandidate)
	if !ok || len(rows) == 0 || rows[0].ProducerReference.NativeActionPath != "init[1]" {
		t.Fatal("response advertised intermediate native paths")
	}
}

func syntheticAgentforceReferenceSource() (*dsl.Script, []byte) {
	const base = "https://synthetic.my.salesforce.com"
	// A record-id shape: correlation detection skips digit-free word values
	// as names, not server state.
	const recordID = "a0B5g00000SyNtHQAA"
	script := &dsl.Script{Name: "synthetic", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: base}, Init: []dsl.Action{{Type: "GET", Target: base + "/bootstrap"}}, Actions: []dsl.Action{{Type: "POST", Target: base + "/einstein/ai-agent/v1/agents/SYNTHETIC/sessions", Headers: map[string]string{"Authorization": "Bearer synthetic-recorded-token", "Content-Type": "application/json"}, Body: `{"externalSessionKey":"` + recordID + `"}`}}}
	capture := map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": base + "/bootstrap"}, "response": map[string]any{"content": map[string]any{"text": `{"recordId":"` + recordID + `"}`}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": script.Actions[0].Target, "postData": map[string]any{"text": script.Actions[0].Body}}, "response": map[string]any{"content": map[string]any{"text": "{}"}}},
	}}}
	data, _ := json.Marshal(capture)
	return script, data
}

func TestEnterpriseArchiveSummaryUsesFinalNativeReferencesAfterAgentforceTransform(t *testing.T) {
	script, data := syntheticAgentforceReferenceSource()
	originalSource := &sourceview.Review{Mappings: []sourceview.Mapping{{ID: "bootstrap", SourceFile: "synthetic.har", TargetPath: "init[0]"}, {ID: "start", SourceFile: "synthetic.har", TargetPath: "actions[0]"}}}
	result := &enterprisearchive.ConvertResult{Script: script, SourceReview: originalSource}
	attachSalesforceReviewToEnterpriseArchiveResult(result, &enterpriseArchiveImportRequest{SalesforceNative: true, HARData: data})
	if originalSource.TargetDomain != "" || result.Script.GeneratedBy != "apexload-salesforce-builder" {
		t.Fatal("synthetic archive did not select a native draft without changing original review")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/validate?agentforce_transform=true", nil)
	response := NewServer(ServerConfig{}).newEnterpriseArchiveImportResponse(request, result, enterprisearchive.RuntimeSettingMigrationPolicy{}, false)
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatal("archive summary and persisted review formed a script metadata cycle")
	}
	if strings.Contains(string(wire), "referenceCatalog") || strings.Contains(string(wire), "producerWeights") {
		t.Fatal("archive response exported its private rebind catalog")
	}
	var reopened struct {
		Script  dsl.Script `json:"script"`
		Summary struct {
			SalesforceReview *salesforce.Review `json:"salesforce_review"`
			SourceReview     *sourceview.Review `json:"source_review"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(wire, &reopened); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Script.Init) != 2 || reopened.Summary.SourceReview == nil || reopened.Summary.SourceReview.TargetDomain != sourceview.OriginalImportTargetDomain {
		t.Fatal("archive late transform or durable original-source domain missing")
	}
	persisted := salesforceReviewValue(reopened.Script.Metadata.Import.Preserved["salesforce_review"])
	public := reopened.Summary.SalesforceReview
	if public == nil || public.NativePreview == nil || public.NativePreview.Script == nil || persisted == nil || persisted.NativePreview == nil || persisted.NativePreview.Script != nil {
		t.Fatal("archive summary preview or cycle-free persisted review contract changed")
	}
	for _, review := range []*salesforce.Review{public, persisted} {
		if review.NativePreview.NativePathDomain != salesforce.FinalNativePathDomain || len(review.NativePreview.Correlations) == 0 {
			t.Fatal("archive summary or reopened metadata retained a detached pre-transform review")
		}
		found := false
		for _, row := range review.NativePreview.Correlations {
			if row.ProducerReference != nil && row.ProducerReference.HAREntryID == 1 {
				found = true
				if row.ProducerReference.OriginalActionPath != "init[0]" || row.ProducerReference.NativeActionPath != "init[1]" || row.ProducerReference.SourceMappingID != "bootstrap" {
					t.Fatal("archive final response or saved metadata advertised a stale native path")
				}
			}
		}
		if !found {
			t.Fatal("archive summary or persisted producer reference missing")
		}
	}
	if !reflect.DeepEqual(public.NativePreview.Correlations, persisted.NativePreview.Correlations) || !reflect.DeepEqual(public.NativePreview.CorrelationCoverage, persisted.NativePreview.CorrelationCoverage) {
		t.Fatal("archive summary and reopened metadata have inconsistent final reference details/counts")
	}
}
