package salesforce

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAuraTokenEikoocnekotConfirmationAndRedirectScope(t *testing.T) {
	for _, cookieName := range []string{
		"__Host-ERIC_PROD-2876452450001777317",
		"__Host-ERIC_PROD1049330172993496433",
	} {
		t.Run(cookieName, func(t *testing.T) {
			script := auraTokenScript(auraTokenFixture)
			rawHAR := auraTokenHAR(t, auraTokenFixture, "acme.lightning.force.com", "")
			var parsed map[string]any
			if err := json.Unmarshal(rawHAR, &parsed); err != nil {
				t.Fatal(err)
			}
			entries := parsed["log"].(map[string]any)["entries"].([]any)
			entries[0].(map[string]any)["response"].(map[string]any)["content"].(map[string]any)["text"] =
				`<html><script>var auraConfig = {"eikoocnekot":"` + cookieName + `"};</script></html>`
			var err error
			rawHAR, err = json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}

			review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: rawHAR})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if review.NativePreview == nil || review.NativePreview.Script == nil {
				t.Fatal("expected NativePreview.Script")
			}
			var foundReason bool
			for _, corr := range review.NativePreview.Correlations {
				if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
					for _, reason := range corr.Reasons {
						if strings.Contains(reason, cookieName) {
							foundReason = true
						}
					}
				}
			}
			if !foundReason {
				t.Fatalf("expected eikoocnekot confirmation for %s in correlation reasons: %#v", cookieName, review.NativePreview.Correlations)
			}
			var hasRedirectScope bool
			for _, ext := range review.NativePreview.Script.Actions[0].Extractors {
				if ext.Name == "corr_salesforce_aura_token" && ext.Scope != nil && ext.Scope.Redirects == "include" {
					hasRedirectScope = true
				}
			}
			if !hasRedirectScope {
				t.Fatalf("expected Scope.Redirects=include on corr_salesforce_aura_token: %#v", review.NativePreview.Script.Actions[0].Extractors)
			}
		})
	}
}

func TestNativePreviewDeduplicatesBodyNotExportedGateAndEnrichesAuraActionNames(t *testing.T) {
	validMsg := `{"actions":[{"id":"1;a","descriptor":"aura://ApexActionController/ACTION$execute","callingDescriptor":"UNKNOWN","params":{"namespace":"","classname":"CustomerSearchController","method":"findAccount","params":{}}},{"id":"2;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","callingDescriptor":"UNKNOWN","params":{}}]}`
	validBody := "message=" + url.QueryEscape(validMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=null"

	script := &dsl.Script{
		Name:     "Aura Gate Dedup",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.sandbox.lightning.force.com"},
		Actions: []dsl.Action{
			{
				Name:   "POST /aura",
				Type:   "http_request",
				Method: "POST",
				Target: "/aura?r=1",
				Body:   validBody,
				Metadata: map[string]any{
					"har.entry_index": 1,
				},
			},
			{
				Name:   "POST /aura",
				Type:   "http_request",
				Method: "POST",
				Target: "/aura?r=2",
				Body:   "",
				Metadata: map[string]any{
					"har.entry_index":                     2,
					"har.request.recorded_content_length": int64(96719),
					"execution_blockers":                  []string{sample.HARRequestBodyNotExportedCode},
				},
			},
		},
	}

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	native := review.NativePreview.Script
	if native.Actions[0].Name != "POST /aura — CustomerSearchController.findAccount (+1)" {
		t.Fatalf("enriched Aura action name = %q, want Apex class+method (+1)", native.Actions[0].Name)
	}
	findings, err := importreview.FindingsFromScript(&dsl.Script{Actions: []dsl.Action{native.Actions[1]}})
	if err != nil {
		t.Fatalf("FindingsFromScript: %v", err)
	}
	for _, f := range findings {
		if f.Code == "salesforce_aura_request_requires_review" {
			t.Fatalf("unexported body action received duplicate salesforce_aura_request_requires_review finding: %#v", findings)
		}
	}
}

func TestAnalyzeMatchesTemplatedHAREntriesRewritesQueryMetadataAndBoostsDynamicDiffIDs(t *testing.T) {
	const (
		rawSID       = "00Dxx0000001gEREAY!AQEAQSyntheticSession987654"
		escapedSID   = "00Dxx0000001gEREAY%21AQEAQSyntheticSession987654"
		dynamicRecID = "a3Cir000000bcUXEAY"
		staticAppID  = "06mir0000008XYZAA2"
	)

	createResp := `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":{"id":"` + dynamicRecID + `","appId":"` + staticAppID + `"}}]}`
	viewMsg := `{"actions":[{"id":"2;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","callingDescriptor":"UNKNOWN","params":{"recordId":"` + dynamicRecID + `","appId":"` + staticAppID + `"}}]}`
	viewBody := "message=" + url.QueryEscape(viewMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=null"

	harPayload := map[string]any{
		"log": map[string]any{
			"entries": []any{
				map[string]any{
					"request": map[string]any{"method": "POST", "url": "https://acme.sandbox.my.salesforce.com/", "headers": []any{}},
					"response": map[string]any{
						"status":  302,
						"headers": []map[string]string{{"name": "Location", "value": "https://acme.sandbox.my.salesforce.com/secur/frontdoor.jsp?sid=" + escapedSID}},
						"content": map[string]any{"text": ""},
					},
				},
				map[string]any{
					"request":  map[string]any{"method": "GET", "url": "https://acme.sandbox.my.salesforce.com/secur/frontdoor.jsp?sid=" + escapedSID, "queryString": []map[string]string{{"name": "sid", "value": rawSID}}, "headers": []any{}},
					"response": map[string]any{"status": 200, "headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": createResp}},
				},
				map[string]any{
					"request": map[string]any{
						"method":   "POST",
						"url":      "https://acme.sandbox.lightning.force.com/aura?r=1",
						"headers":  []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": viewBody},
					},
					"response": map[string]any{"status": 200, "headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"2;a","state":"SUCCESS"}]}`}},
				},
			},
		},
	}
	rawHAR, err := json.Marshal(harPayload)
	if err != nil {
		t.Fatal(err)
	}

	converted, err := sample.Convert(sample.ImportRequest{
		Name:    "Paired Diff Native",
		Format:  "har",
		Content: string(rawHAR),
		Options: sample.ConvertOptions{HighConfidenceOnly: true},
	})
	if err != nil {
		t.Fatalf("sample.Convert: %v", err)
	}
	script := converted.Script

	review, err := Analyze(script, AnalyzeOptions{
		BuildNative: true,
		HARData:     rawHAR,
		DiffEvidence: []DiffCorrelationEvidence{
			{
				Source:         "body",
				Expression:     "$.actions[0].returnValue.id",
				BaselineValue:  dynamicRecID,
				BaselineEntry:  2,
				Classification: "dynamic",
			},
			{
				Source:         "body",
				Expression:     "$.actions[0].returnValue.appId",
				BaselineValue:  staticAppID,
				BaselineEntry:  2,
				Classification: "static",
			},
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if review.HARMatchCoverage.MatchedRequests != 3 || review.HARMatchCoverage.CoveragePct != 100 {
		t.Fatalf("HARMatchCoverage = %+v, want 3/3 (100%%)", review.HARMatchCoverage)
	}
	totalDomainCount := 0
	for _, d := range review.Domains {
		totalDomainCount += d.Count
	}
	if totalDomainCount != 3 {
		t.Fatalf("domainObservations double-counted matched entries: total=%d, domains=%+v", totalDomainCount, review.Domains)
	}

	native := review.NativePreview.Script
	// Producer 0 must reuse corr_salesforce_frontdoor_sid without creating a _2 or secondary duplicate extractor.
	var extNames []string
	for _, ext := range native.Actions[0].Extractors {
		extNames = append(extNames, ext.Name)
	}
	if len(extNames) != 1 || extNames[0] != "corr_salesforce_frontdoor_sid" {
		t.Fatalf("unexpected duplicate extractors on action 0: %v", extNames)
	}
	// Query metadata must carry templated {{var.corr_salesforce_frontdoor_sid}} and no raw sid.
	qRaw, _ := json.Marshal(native.Actions[1].Metadata["har.request.query_string"])
	if !strings.Contains(string(qRaw), `"value":"{{var.corr_salesforce_frontdoor_sid}}"`) || strings.Contains(string(qRaw), rawSID) || strings.Contains(string(qRaw), escapedSID) {
		t.Fatalf("har.request.query_string = %s, want templated sid", string(qRaw))
	}
	// Also verify rewriteSalesforceActionQueryMetadata directly rewrites decoded ! values against percent-encoded vars.
	directAction := &dsl.Action{
		Metadata: map[string]any{
			"har.request.query_string": []any{map[string]any{"name": "sid", "value": rawSID}},
		},
	}
	rewriteSalesforceActionQueryMetadata(directAction, map[string]string{escapedSID: "{{var.corr_salesforce_frontdoor_sid}}"})
	qDirect, _ := json.Marshal(directAction.Metadata["har.request.query_string"])
	if !strings.Contains(string(qDirect), `"value":"{{var.corr_salesforce_frontdoor_sid}}"`) {
		t.Fatalf("direct rewriteSalesforceActionQueryMetadata = %s, want templated sid", string(qDirect))
	}
	// Dynamic 18-char record ID must be auto-applied, while static 18-char appId stays literal!
	if strings.Contains(native.Actions[2].Body, dynamicRecID) {
		t.Fatalf("dynamic record ID %s remained literal in body: %s", dynamicRecID, native.Actions[2].Body)
	}
	if !strings.Contains(native.Actions[2].Body, staticAppID) {
		t.Fatalf("static appId %s was unexpectedly correlated in body: %s", staticAppID, native.Actions[2].Body)
	}
}
