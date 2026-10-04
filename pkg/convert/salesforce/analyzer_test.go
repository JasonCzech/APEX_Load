package salesforce

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jmespath/go-jmespath"
)

func TestAnalyzeBuildsSalesforceNativePreviewFromHAR(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Import",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://attone--perfhyper2.sandbox.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "GET",
			Method: "GET",
			Target: "/login",
		}, {
			Name:   "Aura",
			Type:   "POST",
			Method: "POST",
			Target: "https://attone--perfhyper2.sandbox.lightning.force.com/aura",
			Body:   `{"actions":[{"id":"1;a","descriptor":"apex://AccountController/ACTION$get"}],"token":"tok-abcdef123456"}`,
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://attone--perfhyper2.sandbox.my.salesforce.com/login", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"access_token\":\"tok-abcdef123456\"}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://attone--perfhyper2.sandbox.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"token\":\"tok-abcdef123456\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !review.Detected {
		t.Fatal("Salesforce traffic was not detected")
	}
	if review.HARMatchCoverage.MatchedRequests != 2 || review.HARMatchCoverage.ImportedRequests != 2 {
		t.Fatalf("coverage = %#v, want 2/2 matches", review.HARMatchCoverage)
	}
	if len(review.Domains) != 2 {
		t.Fatalf("domains = %#v, want my.salesforce and lightning domains", review.Domains)
	}
	if len(review.AuraEndpoints) != 1 {
		t.Fatalf("aura endpoints = %#v, want one endpoint", review.AuraEndpoints)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("native preview script missing")
	}
	preview := review.NativePreview.Script
	if preview.GeneratedBy != "apexload-salesforce-builder" {
		t.Fatalf("generated_by = %q, want salesforce builder", preview.GeneratedBy)
	}
	if len(preview.Actions[0].Extractors) != 1 || preview.Actions[0].Extractors[0].Name != "corr_access_token" {
		t.Fatalf("login extractors = %#v, want access token correlation", preview.Actions[0].Extractors)
	}
	if preview.Actions[0].Extractors[0].OnMiss != "fail" {
		t.Fatalf("login correlation on_miss = %q, want fail so a later iteration cannot reuse a stale token", preview.Actions[0].Extractors[0].OnMiss)
	}
	if !strings.Contains(preview.Actions[1].Body, "{{var.corr_access_token}}") {
		t.Fatalf("aura body = %q, want correlated token variable", preview.Actions[1].Body)
	}
	if len(preview.Actions[1].Assertions) != 1 || preview.Actions[1].Assertions[0].Type != "aura_actions_success" {
		t.Fatalf("aura assertions = %#v, want request-aware Aura assertion", preview.Actions[1].Assertions)
	}
}

// Regression for the review finding that a standard 2000-record SOQL page in
// the HAR made Analyze return a nil review: the review (detection, coverage,
// endpoints) must survive, with correlation analysis bounded internally.
func TestAnalyzeSurvivesLargeSOQLRecordPage(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Query",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Query",
			Type:   "GET",
			Method: "GET",
			Target: "/services/data/v60.0/query?q=SELECT+Id+FROM+Account",
		}},
	}
	var records strings.Builder
	for index := 0; index < 2000; index++ {
		if index > 0 {
			records.WriteByte(',')
		}
		fmt.Fprintf(&records, `{\"Id\":\"001xx%011d\"}`, index)
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/services/data/v60.0/query?q=SELECT+Id+FROM+Account", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"totalSize\":2000,\"done\":true,\"records\":[` + records.String() + `]}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review == nil {
		t.Fatal("Analyze returned a nil review for a standard SOQL page")
	}
	if !review.Detected {
		t.Fatal("Salesforce traffic was not detected")
	}
}

func TestNativeCorrelationCollisionsFlagPartialOverlapWithMergedOccurrence(t *testing.T) {
	plans := []nativeCorrelationPlan{
		{correlation: correlate.Correlation{Name: "corr_a", FoundInID: 1, Expression: "$.a"}, automatic: true, usage: correlationUse{raw: true}, value: "abcdef"},
		{correlation: correlate.Correlation{Name: "corr_b", FoundInID: 2, Expression: "$.b"}, automatic: true, usage: correlationUse{raw: true}, value: "defghi"},
	}
	// The suffix/prefix merge "abcdefghi" really occurs on a rewrite-target
	// surface, so single-pass replacement would corrupt the merged region.
	surfaces := "https://example.test/use?v=abcdefghi\x00"
	ambiguousLiterals, ambiguousPlans := nativeCorrelationReplacementCollisions(plans, surfaces)
	if !ambiguousLiterals["abcdef"] || !ambiguousLiterals["defghi"] {
		t.Fatalf("ambiguous literals = %#v, want both partially-overlapping literals flagged", ambiguousLiterals)
	}
	if len(ambiguousPlans) != 2 {
		t.Fatalf("ambiguous plans = %#v, want both plans flagged", ambiguousPlans)
	}
}

func TestNativeCorrelationCollisionsKeepDisjointOverlapCandidatesAutomatic(t *testing.T) {
	plans := []nativeCorrelationPlan{
		{correlation: correlate.Correlation{Name: "corr_a", FoundInID: 1, Expression: "$.a"}, automatic: true, usage: correlationUse{raw: true}, value: "abcdef"},
		{correlation: correlate.Correlation{Name: "corr_b", FoundInID: 2, Expression: "$.b"}, automatic: true, usage: correlationUse{raw: true}, value: "defghi"},
	}
	// The literals share suffix/prefix characters but only occur disjointly, so
	// single-pass replacement rewrites both correctly and must stay automatic.
	surfaces := "https://example.test/use?a=abcdef&b=defghi\x00"
	ambiguousLiterals, ambiguousPlans := nativeCorrelationReplacementCollisions(plans, surfaces)
	if len(ambiguousLiterals) != 0 || len(ambiguousPlans) != 0 {
		t.Fatalf("disjoint usage flagged as ambiguous: literals %#v plans %#v", ambiguousLiterals, ambiguousPlans)
	}
}

func TestDetectCorrelationsCheckedDegradesWorkLimitToWarning(t *testing.T) {
	entries := make([]harEntry, 4097)
	for index := range entries {
		entries[index].ID = index + 1
	}
	correlations, warnings, err := detectCorrelationsChecked(entries)
	if err != nil {
		t.Fatalf("detectCorrelationsChecked must degrade the work limit, got error %v", err)
	}
	if len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want none for pathological input", correlations)
	}
	if len(warnings) == 0 {
		t.Fatal("degraded analysis must record a warning")
	}
}

func TestAnalyzeReportsNativePreviewBlockerWithoutHAR(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Import",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://attone--perfhyper2.sandbox.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Aura",
			Type:   "POST",
			Method: "POST",
			Target: "https://attone--perfhyper2.sandbox.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://AccountController/ACTION$get"}`,
		}},
	}

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !review.Detected {
		t.Fatal("Salesforce traffic was not detected")
	}
	if len(review.Blockers) == 0 {
		t.Fatalf("blockers = %#v, want HAR-required blocker", review.Blockers)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("native preview should still be returned for review")
	}
}

func TestAnalyzeClassifiesSalesforceObjectFlowsAndNestedJMESPath(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Object Flow",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Account Lookup",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://AccountController/ACTION$getAccount","params":{"recordId":"001SANITIZED"}}`,
			Metadata: map[string]any{
				"source_transaction": "T_AccountLookup",
			},
		}, {
			Name:   "Work Order Lookup",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://WorkOrderController/ACTION$getWorkOrder","params":{"record-id":"a0WSANITIZED123"}}`,
			Metadata: map[string]any{
				"source_transaction": "T_WorkOrderLookup",
			},
		}, {
			Name:   "Service Address Search",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://BWC_ServiceAddressController/ACTION$getSearchResults","params":{"query":"100 Main"}}`,
			Metadata: map[string]any{
				"source_transaction": "T_ServiceAddress",
			},
		}, {
			Name:   "Record UI",
			Type:   "GET",
			Method: "GET",
			Target: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001SANITIZED",
			Metadata: map[string]any{
				"source_transaction": "T_RecordUI",
			},
		}, {
			Name:   "Use Nested Record ID",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"params":{"record-id":"a0WSANITIZED123"}}`,
			Metadata: map[string]any{
				"source_transaction": "T_WorkOrderLookup",
			},
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"descriptor\":\"apex://AccountController/ACTION$getAccount\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\",\"returnValue\":{\"accountId\":\"001SANITIZED\"}}]}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"descriptor\":\"apex://WorkOrderController/ACTION$getWorkOrder\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\",\"returnValue\":{\"record-id\":\"a0WSANITIZED123\"}}]}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"descriptor\":\"apex://BWC_ServiceAddressController/ACTION$getSearchResults\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      },
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001SANITIZED", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"apiName\":\"Account\"}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"params\":{\"record-id\":\"a0WSANITIZED123\"}}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	flowsByTransaction := map[string][]string{}
	for _, transaction := range review.Transactions {
		flowsByTransaction[transaction.Name] = transaction.FlowKinds
	}
	for transaction, wantFlow := range map[string]string{
		"T_AccountLookup":   "account",
		"T_WorkOrderLookup": "work_order",
		"T_ServiceAddress":  "service",
		"T_RecordUI":        "record",
	} {
		if !containsString(flowsByTransaction[transaction], wantFlow) {
			t.Fatalf("transaction %s flow kinds = %#v, want %s", transaction, flowsByTransaction[transaction], wantFlow)
		}
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("native preview script missing")
	}
	preview := review.NativePreview.Script
	workOrderMetadata, _ := preview.Actions[1].Metadata["salesforce"].(map[string]any)
	if workOrderMetadata["flow_kind"] != "work_order" || workOrderMetadata["object"] != "WorkOrder" {
		t.Fatalf("work order metadata = %#v, want work_order WorkOrder", workOrderMetadata)
	}
	foundJMESCandidate := false
	for _, candidate := range review.NativePreview.Correlations {
		if candidate.Expression == `actions[0].returnValue."record-id"` {
			foundJMESCandidate = true
			if candidate.Confidence >= 0.8 {
				t.Fatalf("nested record-id candidate confidence = %.2f, want review-only confidence", candidate.Confidence)
			}
			break
		}
	}
	if !foundJMESCandidate {
		t.Fatalf("correlations = %#v, want nested JMESPath review candidate", review.NativePreview.Correlations)
	}
	if len(preview.Actions[1].Extractors) != 0 {
		t.Fatalf("work order extractors = %#v, want low-confidence candidate left unwired", preview.Actions[1].Extractors)
	}
	if !strings.Contains(preview.Actions[4].Body, "a0WSANITIZED123") || strings.Contains(preview.Actions[4].Body, "{{var.") {
		t.Fatalf("record-id body = %q, want low-confidence literal preserved for review", preview.Actions[4].Body)
	}
}

func TestAnalyzeFlattensHTTPSequenceChildren(t *testing.T) {
	script := &dsl.Script{
		Name:     "Grouped SF Import",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Address Search",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "https://acme.lightning.force.com/aura",
			Metadata: map[string]any{
				"source_transaction": "S01_AddressSearch",
			},
			Params: map[string]any{
				"requests": []map[string]any{{
					"name":   "Address Search Aura",
					"type":   "POST",
					"method": "POST",
					"target": "https://acme.lightning.force.com/aura",
					"body":   `{"descriptor":"apex://BWC_ServiceAddressController/ACTION$getSearchResults","params":{"query":"100 Main"}}`,
				}, {
					"name":   "Record UI",
					"type":   "GET",
					"method": "GET",
					"target": "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001SANITIZED",
				}},
			},
		}},
	}

	review, err := Analyze(script, AnalyzeOptions{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !review.Detected {
		t.Fatal("Salesforce traffic was not detected")
	}
	if len(review.Transactions) != 1 {
		t.Fatalf("transactions = %#v, want one grouped transaction", review.Transactions)
	}
	transaction := review.Transactions[0]
	if transaction.Name != "S01_AddressSearch" || transaction.Source != "source_transaction" {
		t.Fatalf("transaction = %#v, want grouped LoadRunner transaction", transaction)
	}
	if transaction.RequestCount != 2 || transaction.AuraActions != 1 || transaction.UIAPICalls != 1 {
		t.Fatalf("transaction counts = %#v, want two child requests with Aura and UI API counts", transaction)
	}
	if !containsString(transaction.FlowKinds, "service") || !containsString(transaction.FlowKinds, "record") {
		t.Fatalf("flow kinds = %#v, want service and record child classifications", transaction.FlowKinds)
	}
}

func TestBuildNativePreviewSkipsCorrelationWithUnmatchedSource(t *testing.T) {
	const token = "source-only-token-123456"
	script := &dsl.Script{
		Name:     "SF unmatched source",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Use token",
			Type:   "POST",
			Method: "POST",
			Target: "/services/data/v60.0/composite",
			Body:   `{"token":"` + token + `"}`,
		}},
	}
	refs := flattenRequestActions(script)
	preview, err := buildNativePreview(
		script,
		refs,
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap", ResponseBody: `{"access_token":"` + token + `"}`},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/composite", RequestBody: `{"token":"` + token + `"}`},
		},
		map[int]int{0: 2},
		[]correlate.Correlation{{
			Name:        "corr_access_token",
			Source:      "body",
			Expression:  "$.access_token",
			ExtractType: "jsonpath",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       token,
			Confidence:  0.9,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[0].Body; got != `{"token":"`+token+`"}` {
		t.Fatalf("body = %q, want unmatched-source literal left intact", got)
	}
	if len(preview.Script.Actions[0].Extractors) != 0 {
		t.Fatalf("use-action extractors = %#v, want none", preview.Script.Actions[0].Extractors)
	}
	if len(preview.Correlations) != 0 {
		t.Fatalf("correlation summaries = %#v, want unmatched source omitted", preview.Correlations)
	}
}

func TestBuildNativePreviewDoesNotRewriteBeforeMappedExtractorSource(t *testing.T) {
	const token = "source-ordered-salesforce-token-123456"
	script := &dsl.Script{
		Name:     "SF source order",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Before", Type: "GET", Method: "GET", Target: "/before?echo=" + token},
			{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
			{Name: "Use token", Type: "POST", Method: "POST", Target: "/services/data/v60.0/composite", Body: `{"token":"` + token + `"}`},
		},
	}
	refs := flattenRequestActions(script)
	preview, err := buildNativePreview(
		script,
		refs,
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/before?echo=" + token},
			{ID: 2, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap", ResponseBody: `{"access_token":"` + token + `"}`},
			{ID: 3, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/composite", RequestBody: `{"token":"` + token + `"}`},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{{
			Name:        "corr_access_token",
			Source:      "body",
			Expression:  "$.access_token",
			ExtractType: "jsonpath",
			FoundInID:   2,
			UsedInIDs:   []int{3},
			Value:       token,
			Confidence:  0.9,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[0].Target; got != "/before?echo="+token {
		t.Fatalf("pre-source target = %q, want literal preserved", got)
	}
	if len(preview.Script.Actions[1].Extractors) != 1 {
		t.Fatalf("source extractors = %#v, want one", preview.Script.Actions[1].Extractors)
	}
	if got := preview.Script.Actions[2].Body; got != `{"token":"{{var.corr_access_token}}"}` {
		t.Fatalf("post-source body = %q, want correlation variable", got)
	}
}

func TestBuildNativePreviewPersistsHTTPSequenceCorrelationMutations(t *testing.T) {
	const token = "sequence-token-123456"
	script := &dsl.Script{
		Name:     "SF sequence correlation",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Grouped requests",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Params: map[string]any{
				"requests": []map[string]any{
					{
						"name":   "Bootstrap",
						"type":   "GET",
						"method": "GET",
						"target": "https://acme.my.salesforce.com/bootstrap",
					},
					{
						"name":   "Use token",
						"type":   "POST",
						"method": "POST",
						"target": "https://acme.my.salesforce.com/services/data/v60.0/composite",
						"body":   `{"token":"` + token + `"}`,
					},
				},
			},
		}},
	}
	refs := flattenRequestActions(script)
	preview, err := buildNativePreview(
		script,
		refs,
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap", ResponseBody: `{"access_token":"` + token + `"}`},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/composite", RequestBody: `{"token":"` + token + `"}`},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name:        "corr_access_token",
			Source:      "body",
			Expression:  "$.access_token",
			ExtractType: "jsonpath",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       token,
			Confidence:  0.9,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	children := salesforceSequenceChildren(preview.Script.Actions[0])
	if len(children) != 2 {
		t.Fatalf("sequence children = %#v, want two", children)
	}
	if len(children[0].Extractors) != 1 || children[0].Extractors[0].Name != "corr_access_token" {
		t.Fatalf("bootstrap extractors = %#v, want persisted token extractor", children[0].Extractors)
	}
	if got := children[1].Body; strings.Contains(got, token) || !strings.Contains(got, "{{var.corr_access_token}}") {
		t.Fatalf("use-token body = %q, want persisted correlation rewrite", got)
	}
	for index, child := range children {
		salesforceMetadata, ok := child.Metadata["salesforce"].(map[string]any)
		if !ok || salesforceMetadata["har_match_id"] != index+1 {
			t.Fatalf("child %d metadata = %#v, want persisted HAR match %d", index, child.Metadata, index+1)
		}
	}
}

func TestBuildNativePreviewDetectsAndRewritesEncodedHeaderListCorrelation(t *testing.T) {
	const (
		token        = "tok+/=="
		encodedToken = "tok%2B%2F%3D%3D"
		headerName   = "X-" + encodedToken
	)
	script := &dsl.Script{
		Name:     "SF header-list correlation",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
			{
				Name:       "Use token",
				Type:       "GET",
				Method:     "GET",
				Target:     "/services/data/v60.0/limits",
				HeaderList: dsl.HeaderList{{Name: headerName, Value: encodedToken}},
			},
		},
	}
	refs := flattenRequestActions(script)
	preview, err := buildNativePreview(
		script,
		refs,
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
			{ID: 2, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/limits"},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name:        "corr_access_token",
			Source:      "body",
			Expression:  "$.access_token",
			ExtractType: "jsonpath",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       token,
			Confidence:  0.9,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	bootstrap := preview.Script.Actions[0]
	if len(bootstrap.Extractors) != 1 || bootstrap.Extractors[0].Convert != "url_encode" {
		t.Fatalf("bootstrap extractors = %#v, want encoded-only HeaderList usage to select url_encode", bootstrap.Extractors)
	}
	got := preview.Script.Actions[1].HeaderList
	if len(got) != 1 || strings.Contains(got[0].Value, encodedToken) || got[0].Value != "{{var.corr_access_token}}" {
		t.Fatalf("rewritten HeaderList = %#v, want encoded literal replaced", got)
	}
	if got[0].Name != headerName {
		t.Fatalf("rewritten HeaderList name = %q, want original %q preserved byte-for-byte", got[0].Name, headerName)
	}
}

func TestBuildNativePreviewLeavesUnsupportedEncodedSpellingsForReview(t *testing.T) {
	const rawValue = "tok +/== recorded"
	exact := url.QueryEscape(rawValue)
	tests := []struct {
		name     string
		spelling string
	}{
		{name: "path escape", spelling: url.PathEscape(rawValue)},
		{name: "percent-20 space", spelling: strings.ReplaceAll(exact, "+", "%20")},
		{name: "unescaped literal plus", spelling: strings.ReplaceAll(exact, "%2B", "+")},
		{name: "lowercase percent hex", spelling: strings.NewReplacer("%2B", "%2b", "%2F", "%2f", "%3D", "%3d").Replace(exact)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/callback?access_token=" + tt.spelling
			script := &dsl.Script{
				Name:     "SF unsupported encoding",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
				Actions: []dsl.Action{
					{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
					{Name: "Use token", Type: "GET", Method: "GET", Target: target},
				},
			}
			preview, err := buildNativePreview(
				script,
				flattenRequestActions(script),
				[]harEntry{
					{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
					{ID: 2, Method: "GET", URL: "https://acme.my.salesforce.com" + target},
				},
				map[int]int{0: 1, 1: 2},
				[]correlate.Correlation{{
					Name:        "corr_access_token",
					Source:      "body",
					Expression:  "$.access_token",
					ExtractType: "jsonpath",
					FoundInID:   1,
					UsedInIDs:   []int{2},
					Value:       rawValue,
					Confidence:  0.9,
				}},
			)
			if err != nil {
				t.Fatalf("buildNativePreview returned error: %v", err)
			}
			if got := preview.Script.Actions[1].Target; got != target {
				t.Fatalf("target = %q, want unsupported spelling preserved as %q", got, target)
			}
			for _, extractor := range preview.Script.Actions[0].Extractors {
				if extractor.Convert == "url_encode" {
					t.Fatalf("source extractors = %#v, unsupported spelling must not select url_encode", preview.Script.Actions[0].Extractors)
				}
			}
			if len(preview.Script.Actions[0].Extractors) != 0 {
				t.Fatalf("source extractors = %#v, unsupported-only consumer must not add an unused extractor", preview.Script.Actions[0].Extractors)
			}
			if len(preview.Correlations) != 1 {
				t.Fatalf("correlation review rows = %#v, want the unsupported occurrence retained for review", preview.Correlations)
			}
			if preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "manual_value_replacement_required" {
				t.Fatalf("correlation review row = %#v, want manual non-pre-applied candidate", preview.Correlations[0])
			}
		})
	}
}

func TestBuildNativePreviewEncodedSourceDoesNotDoubleEncodeReuse(t *testing.T) {
	const (
		token        = "tok&branch/123456"
		encodedToken = "tok%26branch%2F123456"
	)
	script := &dsl.Script{
		Name:     "SF encoded source correlation",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
			{Name: "Use token", Type: "POST", Method: "POST", Target: "/aura", Body: "aura.token=" + encodedToken},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap", ResponseBody: "aura.token=" + encodedToken},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/aura", RequestBody: "aura.token=" + encodedToken},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name:        "corr_salesforce_aura_token",
			Source:      "body",
			Expression:  `(?i)(?:^|&)aura\.token=([^&]*)`,
			ExtractType: "regex",
			Convert:     "url_decode",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       token,
			Confidence:  0.95,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	extractors := preview.Script.Actions[0].Extractors
	if len(extractors) != 1 || extractors[0].Convert != "" {
		t.Fatalf("bootstrap extractors = %#v, want encoded source captured without a second encoding pass", extractors)
	}
	if got := preview.Script.Actions[1].Body; got != "aura.token={{var.corr_salesforce_aura_token}}" {
		t.Fatalf("rewritten body = %q, want encoded variable", got)
	}
	if len(preview.Correlations) != 1 || !preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "" {
		t.Fatalf("correlations = %#v, want executable correlation classified pre-applied", preview.Correlations)
	}
}

func TestBuildNativePreviewEncodedSourceRequiresExactConsumerSpelling(t *testing.T) {
	const (
		token          = "tok branch/123456"
		sourceSpelling = "tok%20branch%2F123456"
		consumer       = "tok+branch%2F123456"
	)
	script := &dsl.Script{
		Name:     "SF encoded source spelling mismatch",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
			{Name: "Use token", Type: "POST", Method: "POST", Target: "/aura", Body: "aura.token=" + consumer},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap", ResponseBody: "aura.token=" + sourceSpelling},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/aura", RequestBody: "aura.token=" + consumer},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name:        "corr_salesforce_aura_token",
			Source:      "body",
			Expression:  `(?i)(?:^|&)aura\.token=([^&]*)`,
			ExtractType: "regex",
			Convert:     "url_decode",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       token,
			Confidence:  0.95,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[1].Body; got != "aura.token="+consumer {
		t.Fatalf("body = %q, want consumer spelling preserved for manual replacement", got)
	}
	if got := preview.Script.Actions[0].Extractors; len(got) != 0 {
		t.Fatalf("source extractors = %#v, want none because %%20 source cannot supply '+' consumer", got)
	}
	if len(preview.Correlations) != 1 || preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("correlations = %#v, want manual non-pre-applied candidate", preview.Correlations)
	}
}

func TestBuildNativePreviewRejectsEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	script := &dsl.Script{
		Name:     "SF correlation literal collision",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Source A", Type: "GET", Method: "GET", Target: "/source-a"},
			{Name: "Source B", Type: "GET", Method: "GET", Target: "/source-b"},
			{Name: "Use", Type: "GET", Method: "GET", Target: "/use?token=" + encodedValue},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, URL: "https://acme.my.salesforce.com/source-a"},
			{ID: 2, URL: "https://acme.my.salesforce.com/source-b"},
			{ID: 3, URL: "https://acme.my.salesforce.com/use?token=" + encodedValue},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{
			{
				Name:        "corr_a",
				Source:      "body",
				ExtractType: "regex",
				Expression:  `(opaque token/123456)`,
				Value:       rawValue,
				FoundInID:   1,
				UsedInIDs:   []int{3},
				Confidence:  0.9,
			},
			{
				Name:        "corr_b",
				Source:      "body",
				ExtractType: "regex",
				Expression:  `(opaque\+token%2F123456)`,
				Value:       encodedValue,
				FoundInID:   2,
				UsedInIDs:   []int{3},
				Confidence:  0.9,
			},
		},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[2].Target; got != "/use?token="+encodedValue {
		t.Fatalf("collision target = %q, want ambiguous literal preserved", got)
	}
	for index := 0; index < 2; index++ {
		if len(preview.Script.Actions[index].Extractors) != 0 {
			t.Fatalf("source %d extractors = %#v, want none for ambiguous collision", index, preview.Script.Actions[index].Extractors)
		}
	}
	if len(preview.Correlations) != 2 {
		t.Fatalf("correlation summaries = %#v, want both owners retained for review", preview.Correlations)
	}
	for _, correlation := range preview.Correlations {
		if correlation.PreApplied || correlation.ReviewAction != "manual_value_replacement_required" {
			t.Fatalf("collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestBuildNativePreviewRejectsSubstringEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	containingRawValue := "prefix-" + encodedValue + "-suffix"
	script := &dsl.Script{
		Name:     "SF substring correlation literal collision",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Source A", Type: "GET", Method: "GET", Target: "/source-a"},
			{Name: "Source B", Type: "GET", Method: "GET", Target: "/source-b"},
			{Name: "Use", Type: "GET", Method: "GET", Target: "/use?token=" + containingRawValue},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, URL: "https://acme.my.salesforce.com/source-a"},
			{ID: 2, URL: "https://acme.my.salesforce.com/source-b"},
			{ID: 3, URL: "https://acme.my.salesforce.com/use?token=" + containingRawValue},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{
			{
				Name:        "corr_a",
				Source:      "body",
				ExtractType: "regex",
				Expression:  `(opaque token/123456)`,
				Value:       rawValue,
				FoundInID:   1,
				UsedInIDs:   []int{3},
				Confidence:  0.9,
			},
			{
				Name:        "corr_b",
				Source:      "body",
				ExtractType: "regex",
				Expression:  `(prefix-opaque\+token%2F123456-suffix)`,
				Value:       containingRawValue,
				FoundInID:   2,
				UsedInIDs:   []int{3},
				Confidence:  0.9,
			},
		},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[2].Target; got != "/use?token="+containingRawValue {
		t.Fatalf("substring collision target = %q, want ambiguous literal preserved", got)
	}
	for index := 0; index < 2; index++ {
		if len(preview.Script.Actions[index].Extractors) != 0 {
			t.Fatalf("source %d extractors = %#v, want none for ambiguous substring collision", index, preview.Script.Actions[index].Extractors)
		}
	}
	if len(preview.Correlations) != 2 {
		t.Fatalf("correlation summaries = %#v, want both substring owners retained for review", preview.Correlations)
	}
	for _, correlation := range preview.Correlations {
		if correlation.PreApplied || correlation.ReviewAction != "manual_value_replacement_required" {
			t.Fatalf("substring collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestBuildNativePreviewIncludesReviewOnlyOwnersInCollisionCheck(t *testing.T) {
	const shortValue = "token-123456"
	const longValue = "token-123456-extra"
	script := &dsl.Script{
		Name: "SF mixed-confidence collision", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Source A", Type: "GET", Method: "GET", Target: "/source-a"},
			{Name: "Source B", Type: "GET", Method: "GET", Target: "/source-b"},
			{Name: "Use", Type: "GET", Method: "GET", Target: "/use?token=" + longValue},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, URL: "https://acme.my.salesforce.com/source-a"},
			{ID: 2, URL: "https://acme.my.salesforce.com/source-b"},
			{ID: 3, URL: "https://acme.my.salesforce.com/use?token=" + longValue},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{
			{Name: "corr_short", Source: "body", ExtractType: "regex", Expression: `(token-123456)`, Value: shortValue, FoundInID: 1, UsedInIDs: []int{3}, Confidence: 0.9},
			{Name: "corr_long", Source: "body", ExtractType: "regex", Expression: `(token-123456-extra)`, Value: longValue, FoundInID: 2, UsedInIDs: []int{3}, Confidence: 0.6},
		},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if got := preview.Script.Actions[2].Target; got != "/use?token="+longValue {
		t.Fatalf("mixed-confidence target = %q, want review-only owner preserved", got)
	}
	for index := 0; index < 2; index++ {
		if len(preview.Script.Actions[index].Extractors) != 0 {
			t.Fatalf("source %d extractors = %#v, want no partial automatic owner", index, preview.Script.Actions[index].Extractors)
		}
	}
	for _, correlation := range preview.Correlations {
		if correlation.PreApplied || correlation.ReviewAction != "manual_value_replacement_required" {
			t.Fatalf("mixed-confidence correlation = %#v, want manual value replacement", correlation)
		}
	}
}

func TestPrepareCorrelationNamesAvoidsExistingAndEncodedSiblingCollisions(t *testing.T) {
	refs := []actionRef{{Action: &dsl.Action{Extractors: []dsl.Extractor{{Name: "corr_token"}}}}}
	named, registry := prepareCorrelationNames([]correlate.Correlation{
		{Name: "corr_token"},
		{Name: "corr_token_2_encoded"},
		{Name: "corr_token"},
	}, refs)

	seen := map[string]bool{"corr_token": true}
	for _, corr := range named {
		if seen[corr.Name] {
			t.Fatalf("correlation name %q collides: %#v", corr.Name, named)
		}
		seen[corr.Name] = true
	}
	encodedSibling := registry.claim(named[0].Name + "_encoded")
	if seen[encodedSibling] {
		t.Fatalf("synthesized encoded name %q collides with existing names %#v", encodedSibling, seen)
	}
	if encodedSibling != "corr_token_2_encoded_2" {
		t.Fatalf("encoded sibling = %q, want deterministic collision suffix", encodedSibling)
	}
}

func TestBuildNativePreviewReservesCorrelationExtractorCapacityBeforeRewriting(t *testing.T) {
	const value = "capacity token/123456"
	encoded := url.QueryEscape(value)
	tests := []struct {
		name               string
		existingExtractors int
		consumerTarget     string
		consumerBody       string
	}{
		{
			name:               "single raw extractor at full source",
			existingExtractors: dsl.MaxActionExtractors,
			consumerTarget:     "/use",
			consumerBody:       "token=" + value,
		},
		{
			name:               "mixed raw and encoded needs two free slots",
			existingExtractors: dsl.MaxActionExtractors - 1,
			consumerTarget:     "/use?encoded=" + encoded,
			consumerBody:       "raw=" + value,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			existing := make([]dsl.Extractor, tc.existingExtractors)
			for index := range existing {
				existing[index] = dsl.Extractor{
					Name:       fmt.Sprintf("existing_%03d", index),
					Source:     "body",
					Type:       "regex",
					Expression: `(existing)`,
				}
			}
			script := &dsl.Script{
				Name:     "capacity preview",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
				Actions: []dsl.Action{
					{Name: "Source", Type: "GET", Method: "GET", Target: "/source", Extractors: existing},
					{Name: "Consumer", Type: "POST", Method: "POST", Target: tc.consumerTarget, Body: tc.consumerBody},
				},
			}
			preview, err := buildNativePreview(
				script,
				flattenRequestActions(script),
				[]harEntry{
					{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/source", ResponseBody: "token=" + value},
					{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com" + tc.consumerTarget, RequestBody: tc.consumerBody},
				},
				map[int]int{0: 1, 1: 2},
				[]correlate.Correlation{{
					Name:        "corr_capacity_token",
					Source:      "body",
					Expression:  `(capacity token/123456)`,
					ExtractType: "regex",
					FoundInID:   1,
					UsedInIDs:   []int{2},
					Value:       value,
					Confidence:  0.95,
				}},
			)
			if err != nil {
				t.Fatalf("buildNativePreview returned error: %v", err)
			}
			if got := len(preview.Script.Actions[0].Extractors); got != tc.existingExtractors {
				t.Fatalf("source extractors = %d, want unchanged %d", got, tc.existingExtractors)
			}
			consumer := preview.Script.Actions[1]
			if consumer.Target != tc.consumerTarget || consumer.Body != tc.consumerBody {
				t.Fatalf("capacity-rejected consumer was partially rewritten: target=%q body=%q", consumer.Target, consumer.Body)
			}
			if len(preview.Correlations) != 1 || preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "manual_value_replacement_required" {
				t.Fatalf("correlation status = %#v, want manual and not pre-applied", preview.Correlations)
			}
		})
	}
}

func TestBuildNativePreviewLeavesLowConfidenceCorrelationForReviewOnly(t *testing.T) {
	const value = "static-product-123456"
	script := &dsl.Script{
		Name:     "SF low-confidence candidate",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Product", Type: "GET", Method: "GET", Target: "/services/data/v60.0/product"},
			{Name: "Use product", Type: "POST", Method: "POST", Target: "/services/data/v60.0/composite", Body: `{"product_id":"` + value + `"}`},
		},
	}
	refs := flattenRequestActions(script)
	preview, err := buildNativePreview(
		script,
		refs,
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/product"},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/composite", RequestBody: `{"product_id":"` + value + `"}`},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name:        "corr_product_id",
			Source:      "body",
			Expression:  "product_id",
			ExtractType: "jmespath",
			FoundInID:   1,
			UsedInIDs:   []int{2},
			Value:       value,
			RuleID:      correlate.RuleJSONDynamicID,
			Confidence:  0.62,
		}},
	)
	if err != nil {
		t.Fatalf("buildNativePreview returned error: %v", err)
	}
	if len(preview.Correlations) != 1 || preview.Correlations[0].Confidence != 0.62 {
		t.Fatalf("correlation summaries = %#v, want low-confidence review candidate", preview.Correlations)
	}
	if len(preview.Script.Actions[0].Extractors) != 0 {
		t.Fatalf("source extractors = %#v, want no low-confidence auto-wiring", preview.Script.Actions[0].Extractors)
	}
	if got := preview.Script.Actions[1].Body; !strings.Contains(got, value) || strings.Contains(got, "{{var.") {
		t.Fatalf("use-product body = %q, want recorded literal left for review", got)
	}
}

func TestCollapseCometdConnectRunDoesNotMoveLaterExtractorEarlier(t *testing.T) {
	cometdMetadata := func() map[string]any {
		return map[string]any{"salesforce": map[string]any{"classification": "cometd"}}
	}
	actions := []dsl.Action{
		{
			Name:     "Connect 1",
			Type:     "POST",
			Method:   "POST",
			Target:   "https://acme.my.salesforce.com/cometd/60.0",
			Body:     `message=[{"id":"1","channel":"/meta/connect","clientId":"abc"}]`,
			Metadata: cometdMetadata(),
		},
		{
			Name:     "Connect 2",
			Type:     "POST",
			Method:   "POST",
			Target:   "https://acme.my.salesforce.com/cometd/60.0",
			Body:     `message=[{"id":"2","channel":"/meta/connect","clientId":"abc"}]`,
			Metadata: cometdMetadata(),
			Extractors: []dsl.Extractor{{
				Name:       "corr_late_connect_token",
				Source:     "body",
				Type:       "jsonpath",
				Expression: "$.token",
				OnMiss:     "fail",
			}},
		},
	}

	collapsed := collapseCometdConnectRun(actions)
	if len(collapsed) != 2 {
		t.Fatalf("collapsed actions = %#v, want later-only extractor timing kept as two actions", collapsed)
	}
	if len(collapsed[0].Extractors) != 0 {
		t.Fatalf("first connect extractors = %#v, want later extractor kept off the first response", collapsed[0].Extractors)
	}
	if len(collapsed[1].Extractors) != 1 || collapsed[1].Extractors[0].Name != "corr_late_connect_token" || collapsed[1].Extractors[0].OnMiss != "fail" {
		t.Fatalf("second connect extractors = %#v, want fail-closed later response extractor preserved", collapsed[1].Extractors)
	}
}

func TestCollapseCometdConnectRunDoesNotUnionExtractorsPastDSLMaximum(t *testing.T) {
	cometdMetadata := func() map[string]any {
		return map[string]any{"salesforce": map[string]any{"classification": "cometd"}}
	}
	extractors := func(prefix string, count int) []dsl.Extractor {
		out := make([]dsl.Extractor, 0, count)
		for index := 0; index < count; index++ {
			out = append(out, dsl.Extractor{
				Name:       fmt.Sprintf("%s_%d", prefix, index),
				Source:     "body",
				Type:       "jsonpath",
				Expression: fmt.Sprintf("$.%s_%d", prefix, index),
				OnMiss:     "fail",
			})
		}
		return out
	}
	actions := []dsl.Action{
		{
			Name:       "Connect 1",
			Type:       "POST",
			Method:     "POST",
			Target:     "https://acme.my.salesforce.com/cometd/60.0",
			Body:       `message=[{"id":"1","channel":"/meta/connect","clientId":"abc"}]`,
			Metadata:   cometdMetadata(),
			Extractors: extractors("first", dsl.MaxActionExtractors-1),
		},
		{
			Name:       "Connect 2",
			Type:       "POST",
			Method:     "POST",
			Target:     "https://acme.my.salesforce.com/cometd/60.0",
			Body:       `message=[{"id":"2","channel":"/meta/connect","clientId":"abc"}]`,
			Metadata:   cometdMetadata(),
			Extractors: extractors("later", 2),
		},
	}

	collapsed := collapseCometdConnectRun(actions)
	if len(collapsed) != 2 {
		t.Fatalf("collapsed actions = %d, want unsafe extractor union left discrete", len(collapsed))
	}
	for index := range collapsed {
		if len(collapsed[index].Extractors) > dsl.MaxActionExtractors {
			t.Fatalf("action %d has %d extractors, max %d", index, len(collapsed[index].Extractors), dsl.MaxActionExtractors)
		}
	}
}

func TestCorrelationSummariesUseMappedNativeSequenceIDs(t *testing.T) {
	refs := map[int]*actionRef{
		4:  {Sequence: 1, Action: &dsl.Action{}},
		9:  {Sequence: 2, Action: &dsl.Action{}},
		12: {Sequence: 2, Action: &dsl.Action{}},
	}
	summaries := correlationSummaries([]correlate.Correlation{{
		Name:      "corr_token",
		FoundInID: 4,
		UsedInIDs: []int{9, 12, 99},
	}}, refs, nil)
	if len(summaries) != 1 {
		t.Fatalf("summaries = %#v, want one", summaries)
	}
	if summaries[0].FoundInSeq != 1 || len(summaries[0].UsedInSeqs) != 1 || summaries[0].UsedInSeqs[0] != 2 {
		t.Fatalf("summary IDs = %#v, want mapped native sequences 1 -> 2", summaries[0])
	}
}

func TestAnalyzePreservesRepeatedSetCookieHeadersForCorrelation(t *testing.T) {
	const cookieValue = "abcd1234efgh5678"
	script := &dsl.Script{
		Name:     "SF repeated cookies",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Login", Type: "GET", Method: "GET", Target: "/login"},
			{Name: "Home", Type: "GET", Method: "GET", Target: "/home", Headers: map[string]string{"Cookie": "JSESSIONID=" + cookieValue}},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/login", "headers": []},
	        "response": {"headers": [
	          {"name":"Set-Cookie","value":"JSESSIONID=` + cookieValue + `; Path=/; HttpOnly"},
	          {"name":"set-cookie","value":"theme=darkmode; Path=/"}
	        ], "content": {"text": "ok"}}
	      },
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/home", "headers": [{"name":"Cookie","value":"JSESSIONID=` + cookieValue + `"}]},
	        "response": {"headers": [], "content": {"text": "ok"}}
	      }
	    ]
	  }
	}`)

	entries, err := parseHAR(har)
	if err != nil {
		t.Fatalf("parseHAR returned error: %v", err)
	}
	combined := entries[0].ResponseHeaders["Set-Cookie"]
	if !strings.Contains(combined, "JSESSIONID="+cookieValue) || !strings.Contains(combined, "theme=darkmode") {
		t.Fatalf("combined Set-Cookie = %q, want both repeated header values", combined)
	}

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	var cookieCorrelation *CorrelationCandidate
	for index := range review.NativePreview.Correlations {
		if review.NativePreview.Correlations[index].RuleID == correlate.RuleSetCookieSession {
			cookieCorrelation = &review.NativePreview.Correlations[index]
			break
		}
	}
	if cookieCorrelation == nil {
		t.Fatalf("correlations = %#v, want session-cookie correlation", review.NativePreview.Correlations)
	}
	if len(review.NativePreview.Script.Actions[0].Extractors) == 0 {
		t.Fatal("login action is missing the Set-Cookie extractor")
	}
	if got := review.NativePreview.Script.Actions[1].Headers["Cookie"]; strings.Contains(got, cookieValue) || !strings.Contains(got, "{{var."+cookieCorrelation.Name+"}}") {
		t.Fatalf("Cookie header = %q, want repeated Set-Cookie value correlated", got)
	}
}

func TestAnalyzeHandlesHAREdgeCases(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF HAR Edges",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "GET",
			Method: "GET",
			Target: "/login",
		}, {
			Name:   "Aura One",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"actions":[{"id":"one;a","descriptor":"apex://AccountController/ACTION$get"}]}`,
		}, {
			Name:   "Aura Two",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"actions":[{"id":"two;a","descriptor":"apex://WorkOrderController/ACTION$get"}]}`,
		}, {
			Name:   "Missing From HAR",
			Type:   "GET",
			Method: "GET",
			Target: "/services/data/v60.0/ui-api/records/001SANITIZED",
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      },
	      {
	        "request": {"method": "GET", "url": "https://static.example.test/app.js", "headers": []},
	        "response": {"headers": [], "content": {"text": "console.log('static');"}}
	      },
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/login", "headers": []},
	        "response": {"headers": [], "content": {"text": "<html>login</html>"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"descriptor\":\"apex://WorkOrderController/ACTION$get\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.HARMatchCoverage.ImportedRequests != 4 || review.HARMatchCoverage.MatchedRequests != 3 {
		t.Fatalf("coverage = %#v, want 3 of 4 imported requests matched", review.HARMatchCoverage)
	}
	if review.HARMatchCoverage.CoveragePct != 75 {
		t.Fatalf("coverage pct = %.1f, want 75.0", review.HARMatchCoverage.CoveragePct)
	}
	if len(review.Blockers) == 0 || !strings.Contains(review.Blockers[0], "below 80") {
		t.Fatalf("blockers = %#v, want partial HAR coverage blocker", review.Blockers)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("native preview should still be returned for partial HAR review")
	}
	if got := review.NativePreview.Script.Actions[1].Metadata["salesforce"].(map[string]any)["har_match_id"]; got != 1 {
		t.Fatalf("first duplicate Aura request matched HAR %v, want fallback entry 1: %#v", got, review.NativePreview.Script.Actions[1].Metadata)
	}
	if got := review.NativePreview.Script.Actions[2].Metadata["salesforce"].(map[string]any)["har_match_id"]; got != 4 {
		t.Fatalf("second duplicate Aura request matched HAR %v, want body-matched entry 4: %#v", got, review.NativePreview.Script.Actions[2].Metadata)
	}
}

func TestAnalyzeNativePreviewReplacesEncodedAuraCorrelation(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Encoded Aura",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "GET",
			Method: "GET",
			Target: "https://acme.my.salesforce.com/login",
		}, {
			Name:   "Aura",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Headers: map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
			},
			Body: `aura.token=tok%2B%2F%3D%3D&message=%7B%22actions%22%3A%5B%7B%22descriptor%22%3A%22apex%3A%2F%2FAccountController%2FACTION%24get%22%7D%5D%7D`,
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/login", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"access_token\":\"tok+/==\"}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [{"name":"content-type","value":"application/x-www-form-urlencoded"}], "postData": {"text": "aura.token=tok%2B%2F%3D%3D&message=%7B%22actions%22%3A%5B%7B%22descriptor%22%3A%22apex%3A%2F%2FAccountController%2FACTION%24get%22%7D%5D%7D"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}
	        }
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	login := review.NativePreview.Script.Actions[0]
	if len(login.Extractors) == 0 || login.Extractors[0].Convert != "url_encode" {
		t.Fatalf("login extractors = %#v, want url_encode conversion for encoded Aura reuse", login.Extractors)
	}
	auraBody := review.NativePreview.Script.Actions[1].Body
	if strings.Contains(auraBody, "tok%2B%2F%3D%3D") || !strings.Contains(auraBody, "{{var.corr_access_token}}") {
		t.Fatalf("aura body = %q, want encoded token replaced with variable", auraBody)
	}
}

// TestAnalyzeNativePreviewHandlesDualRawAndEncodedReuse guards the A2 fix for a
// correlated value reused BOTH raw (a JSON body) and percent-encoded (a form
// /aura body). A single variable cannot resolve both ways, so the builder must
// emit a raw extractor plus a url_encode sibling and route each occurrence to the
// matching variable, leaving neither occurrence stale.
func TestAnalyzeNativePreviewHandlesDualRawAndEncodedReuse(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Dual Reuse",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "GET",
			Method: "GET",
			Target: "https://acme.my.salesforce.com/login",
		}, {
			Name:    "Aura",
			Type:    "POST",
			Method:  "POST",
			Target:  "https://acme.lightning.force.com/aura",
			Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
			Body:    `aura.token=tok%2B%2F%3D%3D&message=%7B%22actions%22%3A%5B%5D%7D`,
		}, {
			Name:    "Composite",
			Type:    "POST",
			Method:  "POST",
			Target:  "https://acme.my.salesforce.com/services/data/v59.0/sobjects/Account",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"token":"tok+/=="}`,
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/login", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"access_token\":\"tok+/==\"}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.lightning.force.com/aura", "headers": [{"name":"content-type","value":"application/x-www-form-urlencoded"}], "postData": {"text": "aura.token=tok%2B%2F%3D%3D&message=%7B%22actions%22%3A%5B%5D%7D"}},
	        "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}
	      },
	      {
	        "request": {"method": "POST", "url": "https://acme.my.salesforce.com/services/data/v59.0/sobjects/Account", "headers": [{"name":"content-type","value":"application/json"}], "postData": {"text": "{\"token\":\"tok+/==\"}"}},
	        "response": {"headers": [], "content": {"text": "{\"success\":true}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	login := review.NativePreview.Script.Actions[0]
	var rawConvert, encodedConvert string
	var sawRaw, sawEncoded bool
	for _, ext := range login.Extractors {
		switch ext.Name {
		case "corr_access_token":
			sawRaw = true
			rawConvert = ext.Convert
		case "corr_access_token_encoded":
			sawEncoded = true
			encodedConvert = ext.Convert
		}
	}
	if !sawRaw || rawConvert != "" {
		t.Fatalf("login extractors = %#v, want a raw corr_access_token (no convert)", login.Extractors)
	}
	if !sawEncoded || encodedConvert != "url_encode" {
		t.Fatalf("login extractors = %#v, want a url_encode corr_access_token_encoded sibling", login.Extractors)
	}
	auraBody := review.NativePreview.Script.Actions[1].Body
	if strings.Contains(auraBody, "tok%2B%2F%3D%3D") || !strings.Contains(auraBody, "{{var.corr_access_token_encoded}}") {
		t.Fatalf("aura body = %q, want encoded occurrence routed to the url_encode sibling variable", auraBody)
	}
	compositeBody := review.NativePreview.Script.Actions[2].Body
	if strings.Contains(compositeBody, `"tok+/=="`) || !strings.Contains(compositeBody, "{{var.corr_access_token}}") {
		t.Fatalf("composite body = %q, want raw occurrence routed to the raw variable", compositeBody)
	}
}

func TestAnalyzeNativePreviewCorrelatesCSHCHandoff(t *testing.T) {
	const cshc = "cshc-abcdef1234567890"
	sessionURL := "https://attone--perfhyper2.sandbox.lightning.force.com/visualforce/session?url=https%3A%2F%2Fattone--perfhyper2--c.sandbox.vf.force.com%2Fapex%2FVFLoginFlow&cshc=" + cshc + "&inst=dq"
	script := &dsl.Script{
		Name:     "SF CSHC Handoff",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://attone--perfhyper2.sandbox.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "POST",
			Method: "POST",
			Target: "https://attone--perfhyper2.sandbox.my.salesforce.com/",
			Body:   "username=user@example.test&password=secret",
		}, {
			Name:   "Visualforce Session",
			Type:   "POST",
			Method: "POST",
			Target: sessionURL,
			Body:   "cshc=" + cshc + "&retURL=%2Fhome%2Fhome.jsp",
		}},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "POST", "url": "https://attone--perfhyper2.sandbox.my.salesforce.com/", "headers": [], "postData": {"text": "username=user@example.test&password=secret"}},
	        "response": {"headers": [], "content": {"text": "<html><a href=\"/visualforce/session?cshc=` + cshc + `&display=page\">continue</a></html>"}}
	      },
	      {
	        "request": {"method": "POST", "url": "` + sessionURL + `", "headers": [], "postData": {"text": "cshc=` + cshc + `&retURL=%2Fhome%2Fhome.jsp"}},
	        "response": {"headers": [], "content": {"text": "<html>ok</html>"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	preview := review.NativePreview.Script
	login := preview.Actions[0]
	var cshcExtractor *dsl.Extractor
	for i := range login.Extractors {
		if login.Extractors[i].Name == "corr_salesforce_cshc" {
			cshcExtractor = &login.Extractors[i]
			break
		}
	}
	if cshcExtractor == nil {
		t.Fatalf("login extractors = %#v, want cshc correlation extractor", login.Extractors)
	}
	if cshcExtractor.Type != "regex" || cshcExtractor.Source != "body" || !strings.Contains(cshcExtractor.Expression, "cshc") {
		t.Fatalf("cshc extractor = %#v, want body regex extractor targeting cshc", cshcExtractor)
	}
	session := preview.Actions[1]
	if strings.Contains(session.Target, cshc) || strings.Contains(session.Body, cshc) {
		t.Fatalf("session action still carries recorded cshc literal: target=%q body=%q", session.Target, session.Body)
	}
	if !strings.Contains(session.Target, "{{var.corr_salesforce_cshc}}") || !strings.Contains(session.Body, "{{var.corr_salesforce_cshc}}") {
		t.Fatalf("session action missing cshc variable substitution: target=%q body=%q", session.Target, session.Body)
	}
	foundSummary := false
	for _, corr := range review.NativePreview.Correlations {
		if corr.RuleID == correlate.RuleSalesforceCSHC {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatalf("native preview correlations = %#v, want Salesforce cshc summary", review.NativePreview.Correlations)
	}
}

// TestAnalyzeNativePreviewCorrelatesSOQLPagination proves the A5 SOQL-pagination
// rule flows end-to-end through the native preview: the first query gets a
// $.nextRecordsUrl extractor and the follow-up query's URL is rewritten to reuse
// the extracted cursor variable instead of the recorded literal.
func TestAnalyzeNativePreviewCorrelatesSOQLPagination(t *testing.T) {
	const nextURL = "/services/data/v59.0/query/01gRT0000004ABC-2000"
	script := &dsl.Script{
		Name:     "SF SOQL Pagination",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Query", Type: "GET", Method: "GET", Target: "https://acme.my.salesforce.com/services/data/v59.0/query?q=SELECT+Id+FROM+Account"},
			{Name: "Query More", Type: "GET", Method: "GET", Target: "https://acme.my.salesforce.com" + nextURL},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com/services/data/v59.0/query?q=SELECT+Id+FROM+Account", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"totalSize\":4000,\"done\":false,\"nextRecordsUrl\":\"` + nextURL + `\",\"records\":[{\"Id\":\"001RT0000012345AAA\"}]}"}}
	      },
	      {
	        "request": {"method": "GET", "url": "https://acme.my.salesforce.com` + nextURL + `", "headers": []},
	        "response": {"headers": [], "content": {"text": "{\"totalSize\":4000,\"done\":true,\"records\":[]}"}}
	      }
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	query := review.NativePreview.Script.Actions[0]
	var cursor *dsl.Extractor
	for i := range query.Extractors {
		if query.Extractors[i].Expression == "$.nextRecordsUrl" {
			cursor = &query.Extractors[i]
		}
	}
	if cursor == nil {
		t.Fatalf("query extractors = %#v, want a $.nextRecordsUrl extractor", query.Extractors)
	}
	if cursor.Type != "jsonpath" || cursor.Convert != "" {
		t.Fatalf("cursor extractor = %#v, want jsonpath with no convert", cursor)
	}
	more := review.NativePreview.Script.Actions[1].Target
	if strings.Contains(more, "01gRT0000004ABC-2000") {
		t.Fatalf("query-more target = %q, still carries the recorded cursor literal", more)
	}
	if !strings.Contains(more, "{{var."+cursor.Name+"}}") {
		t.Fatalf("query-more target = %q, want it to reuse {{var.%s}}", more, cursor.Name)
	}
}

func TestDetectBulkQueryLocatorCorrelationFromHeader(t *testing.T) {
	observations := []correlate.Observation{
		{
			ID:              1,
			RequestURL:      "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750ABC/results",
			ResponseHeaders: map[string]string{"Sforce-Locator": "locator-page-2"},
		},
		{
			ID:         2,
			RequestURL: "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750ABC/results?locator=locator-page-2&maxRecords=2000",
		},
		{
			ID:              3,
			RequestURL:      "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750ABC/results?locator=locator-page-2&maxRecords=2000",
			ResponseHeaders: map[string]string{"Sforce-Locator": "null"},
		},
	}

	correlations, err := detectBulkQueryLocatorCorrelations(observations, map[string]bool{}, newSalesforceCorrelationBudget())
	if err != nil {
		t.Fatalf("detectBulkQueryLocatorCorrelations returned error: %v", err)
	}
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want one non-terminal locator", correlations)
	}
	got := correlations[0]
	if got.RuleID != correlate.RuleSalesforceBulkQueryLocator || got.Source != "header" || got.ExtractType != "header" || got.Expression != "Sforce-Locator" {
		t.Fatalf("locator correlation = %#v, want Sforce-Locator header extractor", got)
	}
	if got.Value != "locator-page-2" || len(got.UsedInIDs) != 2 || got.UsedInIDs[0] != 2 || got.UsedInIDs[1] != 3 {
		t.Fatalf("locator reuse = value %q ids %#v, want page-2 reused in requests 2 and 3", got.Value, got.UsedInIDs)
	}
}

func TestBulkQueryLocatorCorrelationFailsClosedOnSearchBudget(t *testing.T) {
	observations := []correlate.Observation{
		{
			ID:              1,
			RequestURL:      "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750ABC/results",
			ResponseHeaders: map[string]string{"Sforce-Locator": "locator-page-2"},
		},
		{
			ID:         2,
			RequestURL: "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750ABC/results?locator=locator-page-2",
		},
	}
	budget := newSalesforceCorrelationBudget()
	budget.maxSearchBytes = 1
	// The phase reports the work limit and returns the (empty) partial result;
	// the caller records the truncation warning.
	if correlations, err := detectBulkQueryLocatorCorrelations(observations, map[string]bool{}, budget); !errors.Is(err, correlate.ErrCorrelationWorkLimit) || len(correlations) != 0 {
		t.Fatalf("budgeted bulk correlations = %#v, error %v; want empty partial with ErrCorrelationWorkLimit", correlations, err)
	}
}

func TestAnalyzeNativePreviewCorrelatesBulkQueryLocatorAndPollLoop(t *testing.T) {
	const (
		base    = "https://acme.my.salesforce.com"
		jobID   = "7508c00000QRYA6AAL"
		locator = "2WeNtJk4NTE4MDAwMDAwMDAw"
	)
	script := &dsl.Script{
		Name:     "SF Bulk Query",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Create Query Job", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/jobs/query", Body: `{"operation":"query","query":"SELECT Id FROM Account"}`},
			{Name: "Query Job Status", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/jobs/query/" + jobID},
			{Name: "Query Results Page 1", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/jobs/query/" + jobID + "/results?maxRecords=2"},
			{Name: "Query Results Page 2", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/jobs/query/" + jobID + "/results?locator=" + locator + "&maxRecords=2"},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/jobs/query", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + jobID + `\",\"operation\":\"query\",\"state\":\"UploadComplete\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/jobs/query/` + jobID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + jobID + `\",\"state\":\"JobComplete\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/jobs/query/` + jobID + `/results?maxRecords=2", "headers": []},
	       "response": {"headers": [{"name":"Sforce-Locator","value":"` + locator + `"}], "content": {"text": "Id\n001A\n001B"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/jobs/query/` + jobID + `/results?locator=` + locator + `&maxRecords=2", "headers": []},
	       "response": {"headers": [{"name":"Sforce-Locator","value":"null"}], "content": {"text": "Id\n001C"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	actions := review.NativePreview.Script.Actions

	poll := actions[1]
	if poll.Type != "control_loop" || poll.Control == nil {
		t.Fatalf("query status action = %#v, want a control_loop", poll)
	}
	step := poll.Control.Steps[0]
	if !strings.Contains(step.Target, jobID) || strings.Contains(step.Target, "{{var.corr_id}}") {
		t.Fatalf("poll step target = %q, want low-confidence query job id left for review", step.Target)
	}
	foundJobIDReview := false
	for _, candidate := range review.NativePreview.Correlations {
		if candidate.Name == "corr_id" && candidate.Confidence < 0.8 {
			foundJobIDReview = true
			break
		}
	}
	if !foundJobIDReview {
		t.Fatalf("correlations = %#v, want low-confidence query job id review candidate", review.NativePreview.Correlations)
	}

	results := actions[2]
	var locatorExt *dsl.Extractor
	for i := range results.Extractors {
		if results.Extractors[i].Name == "corr_salesforce_bulk_query_locator" {
			locatorExt = &results.Extractors[i]
		}
	}
	if locatorExt == nil || locatorExt.Source != "header" || locatorExt.Type != "header" || locatorExt.Expression != "Sforce-Locator" {
		t.Fatalf("results extractors = %#v, want Sforce-Locator header extractor", results.Extractors)
	}
	page2 := actions[3].Target
	if strings.Contains(page2, locator) {
		t.Fatalf("page 2 target = %q, still carries recorded locator", page2)
	}
	if !strings.Contains(page2, "{{var."+locatorExt.Name+"}}") {
		t.Fatalf("page 2 target = %q, want locator variable %s", page2, locatorExt.Name)
	}
	if actions[2].Type == "control_loop" || actions[3].Type == "control_loop" {
		t.Fatalf("results actions must not be wrapped as status polls: %#v %#v", actions[2], actions[3])
	}
}

// TestAnalyzeNativePreviewBulkIngestAndQueryUseDistinctPollState guards B7: when
// a recording contains BOTH a Bulk ingest job and a Bulk query job, each
// synthesized poll loop must use its own state variable. A shared
// "bulk_job_state" lets the first loop leave a terminal value that makes the
// second loop's condition false on its first pass, so the second job is never
// polled.
func TestAnalyzeNativePreviewBulkIngestAndQueryUseDistinctPollState(t *testing.T) {
	const (
		base     = "https://acme.my.salesforce.com"
		ingestID = "7508c00000INGEST0AAL"
		queryID  = "7508c00000QUERY00AAL"
	)
	script := &dsl.Script{
		Name:     "SF Bulk Ingest + Query",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Create Ingest Job", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/jobs/ingest", Body: `{"object":"Account","operation":"insert"}`},
			{Name: "Ingest Job Status", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/jobs/ingest/" + ingestID},
			{Name: "Create Query Job", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/jobs/query", Body: `{"operation":"query","query":"SELECT Id FROM Account"}`},
			{Name: "Query Job Status", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/jobs/query/" + queryID},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/jobs/ingest", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + ingestID + `\",\"state\":\"Open\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/jobs/ingest/` + ingestID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + ingestID + `\",\"state\":\"JobComplete\"}"}}},
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/jobs/query", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + queryID + `\",\"state\":\"UploadComplete\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/jobs/query/` + queryID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + queryID + `\",\"state\":\"JobComplete\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	actions := review.NativePreview.Script.Actions

	stateVars := map[string]bool{}
	loopCount := 0
	for _, a := range actions {
		if a.Type != "control_loop" || a.Control == nil || len(a.Control.Steps) == 0 {
			continue
		}
		loopCount++
		var stateVar string
		for _, ext := range a.Control.Steps[0].Extractors {
			if strings.HasPrefix(ext.Name, "bulk_job_state") {
				stateVar = ext.Name
			}
		}
		if stateVar == "" {
			t.Fatalf("bulk poll loop %q has no bulk_job_state extractor: %#v", a.Name, a.Control.Steps[0].Extractors)
		}
		if stateVars[stateVar] {
			t.Fatalf("two bulk poll loops share state var %q (ingest/query collision)", stateVar)
		}
		stateVars[stateVar] = true
		// The loop condition must reference the same per-kind state var.
		if a.Control.Condition == nil || !strings.Contains(a.Control.Condition.Expr, "var."+stateVar) {
			t.Fatalf("loop %q condition %q does not gate on its state var %q", a.Name, a.Control.Condition.Expr, stateVar)
		}
		// do_while makes the status GET re-prime the state var on every VUser
		// iteration; without it the loop is skipped from iteration 2 under load.
		if !a.Control.DoWhile {
			t.Fatalf("loop %q must set DoWhile so it re-executes across iterations", a.Name)
		}
	}
	if loopCount != 2 {
		t.Fatalf("synthesized %d bulk poll loops, want 2 (ingest + query)", loopCount)
	}
	if !stateVars["bulk_job_state_ingest"] || !stateVars["bulk_job_state_query"] {
		t.Fatalf("poll loops did not use per-kind state vars: %#v", stateVars)
	}
}

// TestAnalyzeNativePreviewSynthesizesBulkPollLoop proves the A5 Bulk API 2.0
// lifecycle: the recorded job-status GET is rewritten into a control_loop that
// polls until the job reaches a terminal state, the CSV-upload target reuses the
// extracted contentUrl, and the result download is left untouched.
func TestAnalyzeNativePreviewSynthesizesBulkPollLoop(t *testing.T) {
	const (
		base       = "https://acme.my.salesforce.com"
		jobID      = "7508c00000ARgV6AAL"
		contentURL = "services/data/v59.0/jobs/ingest/7508c00000ARgV6AAL/batches"
	)
	script := &dsl.Script{
		Name:     "SF Bulk Ingest",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Create Job", Type: "POST", Method: "POST", Target: base + "/services/data/v59.0/jobs/ingest", Body: `{"object":"Account","operation":"insert","contentType":"CSV"}`},
			{Name: "Upload Batch", Type: "PUT", Method: "PUT", Target: base + "/" + contentURL, Body: "Name\nAcme"},
			{Name: "Close Job", Type: "PATCH", Method: "PATCH", Target: base + "/services/data/v59.0/jobs/ingest/" + jobID, Body: `{"state":"UploadComplete"}`},
			{Name: "Job Status", Type: "GET", Method: "GET", Target: base + "/services/data/v59.0/jobs/ingest/" + jobID},
			{Name: "Results", Type: "GET", Method: "GET", Target: base + "/services/data/v59.0/jobs/ingest/" + jobID + "/successfulResults"},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v59.0/jobs/ingest", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + jobID + `\",\"state\":\"Open\",\"object\":\"Account\",\"contentUrl\":\"` + contentURL + `\"}"}}},
	      {"request": {"method": "PUT", "url": "` + base + `/` + contentURL + `", "headers": []},
	       "response": {"headers": [], "content": {"text": ""}}},
	      {"request": {"method": "PATCH", "url": "` + base + `/services/data/v59.0/jobs/ingest/` + jobID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + jobID + `\",\"state\":\"UploadComplete\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v59.0/jobs/ingest/` + jobID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"id\":\"` + jobID + `\",\"state\":\"JobComplete\",\"numberRecordsProcessed\":1}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v59.0/jobs/ingest/` + jobID + `/successfulResults", "headers": []},
	       "response": {"headers": [], "content": {"text": "sf__Id,Name\n001,Acme"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	actions := review.NativePreview.Script.Actions

	// The CSV-upload target reuses the extracted contentUrl variable.
	if strings.Contains(actions[1].Target, "/batches") && !strings.Contains(actions[1].Target, "{{var.corr_salesforce_bulk_content_url}}") {
		t.Fatalf("upload target = %q, want it to reuse the contentUrl variable", actions[1].Target)
	}

	// The job-status GET is rewritten into a poll loop.
	poll := actions[3]
	if poll.Type != "control_loop" || poll.Control == nil {
		t.Fatalf("job-status action = %#v, want a control_loop", poll)
	}
	if poll.Control.MaxIterations != 60 {
		t.Fatalf("loop max_iterations = %d, want 60", poll.Control.MaxIterations)
	}
	if poll.Control.Condition == nil || !strings.Contains(poll.Control.Condition.Expr, "JobComplete") || !strings.Contains(poll.Control.Condition.Expr, "has(var.bulk_job_state_ingest)") {
		t.Fatalf("loop condition = %#v, want a null-safe JobComplete predicate", poll.Control.Condition)
	}
	if len(poll.Control.Steps) != 1 {
		t.Fatalf("loop steps = %d, want 1", len(poll.Control.Steps))
	}
	step := poll.Control.Steps[0]
	var stateExt *dsl.Extractor
	for i := range step.Extractors {
		if step.Extractors[i].Name == "bulk_job_state_ingest" {
			stateExt = &step.Extractors[i]
		}
	}
	if stateExt == nil || stateExt.Expression != "$.state" {
		t.Fatalf("loop step extractors = %#v, want a bulk_job_state $.state extractor", step.Extractors)
	}
	if !step.ThinkTimeAfter || step.ThinkTime.Type == "" {
		t.Fatalf("loop step = %#v, want a poll-interval think time (think_time_after)", step)
	}
	if !strings.Contains(step.Target, jobID) || strings.Contains(step.Target, "{{var.corr_id}}") {
		t.Fatalf("loop step target = %q, want low-confidence job id left for review", step.Target)
	}
	foundJobIDReview := false
	for _, candidate := range review.NativePreview.Correlations {
		if candidate.Name == "corr_id" && candidate.Confidence < 0.8 {
			foundJobIDReview = true
			break
		}
	}
	if !foundJobIDReview {
		t.Fatalf("correlations = %#v, want low-confidence ingest job id review candidate", review.NativePreview.Correlations)
	}

	// The result download is NOT wrapped in a loop.
	if actions[4].Type == "control_loop" {
		t.Fatalf("result-download action was wrongly wrapped in a loop: %#v", actions[4])
	}

	// The synthesized control_loop must be valid DSL.
	for _, issue := range dsl.ValidateScriptIssues(review.NativePreview.Script) {
		if strings.Contains(strings.ToLower(issue), "control") || strings.Contains(strings.ToLower(issue), "condition") {
			t.Fatalf("synthesized loop failed validation: %s", issue)
		}
	}
	assertNativeReferencePath(t, review.NativePreview, 4, "actions[3]", "actions[3].control.steps[0]")
}

// TestCompositeCandidatesParsesReferenceIds covers the A5 composite parser: one
// candidate per subrequest that returns a record id, addressed by referenceId,
// skipping error subrequests and subrequests without a body id.
func TestCompositeCandidatesParsesReferenceIds(t *testing.T) {
	body := `{"compositeResponse":[
	  {"referenceId":"NewAccount","httpStatusCode":201,"body":{"id":"001AAA000111","success":true}},
	  {"referenceId":"NewContact","httpStatusCode":201,"body":{"id":"003BBB000222"}},
	  {"referenceId":"FailedThing","httpStatusCode":400,"body":[{"errorCode":"REQUIRED_FIELD_MISSING"}]},
	  {"referenceId":"QueryOnly","httpStatusCode":200,"body":{"totalSize":1,"records":[]}}
	]}`
	cands := compositeCandidates(body)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2 (NewAccount, NewContact): %#v", len(cands), cands)
	}
	expr := map[string]string{}
	typ := map[string]string{}
	for _, c := range cands {
		expr[c.Value] = c.Expression
		typ[c.Value] = c.ExtractType
	}
	if typ["001AAA000111"] != "jmespath" || expr["001AAA000111"] != "compositeResponse[?referenceId=='NewAccount'].body.id | [0]" {
		t.Fatalf("NewAccount candidate = %s %q, want referenceId-scoped jmespath", typ["001AAA000111"], expr["001AAA000111"])
	}
	if expr["003BBB000222"] != "compositeResponse[?referenceId=='NewContact'].body.id | [0]" {
		t.Fatalf("NewContact expr = %q", expr["003BBB000222"])
	}
}

func TestCompositeCandidatesSuppressDuplicateRuntimeExpressions(t *testing.T) {
	body := `{"compositeResponse":[
	  {"referenceId":"Duplicate","httpStatusCode":201,"body":{"id":"001AAA000111"}},
	  {"referenceId":"Duplicate","httpStatusCode":201,"body":{"id":"001BBB000222"}}
	]}`
	candidates := compositeCandidates(body)
	if len(candidates) != 1 || candidates[0].Value != "001AAA000111" {
		t.Fatalf("duplicate-reference candidates = %#v, want only the value selected by JMESPath [0]", candidates)
	}
}

func TestCompositeCandidatesCheckedEnforcesPreAppendBudgets(t *testing.T) {
	body := `{"compositeResponse":[
	  {"referenceId":"First","httpStatusCode":201,"body":{"id":"001AAA000111"}},
	  {"referenceId":"Second","httpStatusCode":201,"body":{"id":"001BBB000222"}}
	]}`
	budget := newSalesforceCorrelationBudget()
	budget.maxCandidates = 1
	if candidates, err := compositeCandidatesChecked(body, budget); !errors.Is(err, correlate.ErrCorrelationWorkLimit) || candidates != nil {
		t.Fatalf("candidate-limited composite = %#v, error %v; want nil ErrCorrelationWorkLimit", candidates, err)
	}

	budget = newSalesforceCorrelationBudget()
	budget.maxConstructionBytes = 16
	if candidates, err := compositeCandidatesChecked(body, budget); !errors.Is(err, correlate.ErrCorrelationWorkLimit) || candidates != nil {
		t.Fatalf("construction-limited composite = %#v, error %v; want nil ErrCorrelationWorkLimit", candidates, err)
	}
}

func TestNestedJSONCandidatesCheckedBoundsTraversalAndPreservesNumbers(t *testing.T) {
	const largeInteger = "9007199254740993"
	candidates, err := nestedJSONCandidatesChecked(`{"outer":{"recordId":`+largeInteger+`}}`, newSalesforceCorrelationBudget())
	if err != nil {
		t.Fatalf("nestedJSONCandidatesChecked returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != largeInteger || candidates[0].Expression != "outer.recordId" {
		t.Fatalf("numeric candidates = %#v, want lexical integer %s", candidates, largeInteger)
	}

	budget := newSalesforceCorrelationBudget()
	budget.maxCandidates = 1
	if candidates, err := nestedJSONCandidatesChecked(`{"firstId":"first-value-123456","secondId":"second-value-654321"}`, budget); !errors.Is(err, correlate.ErrCorrelationWorkLimit) || candidates != nil {
		t.Fatalf("candidate-limited nested JSON = %#v, error %v; want nil ErrCorrelationWorkLimit", candidates, err)
	}

	// Beyond the structural caps the response's shape pass is skipped (not an
	// analysis failure): no candidates, no error.
	deep := strings.Repeat(`{"x":`, maxSalesforceJSONDepth+1) + `"leaf"` + strings.Repeat(`}`, maxSalesforceJSONDepth+1)
	if candidates, err := nestedJSONCandidatesChecked(deep, newSalesforceCorrelationBudget()); err != nil || candidates != nil {
		t.Fatalf("deep nested JSON = %#v, error %v; want skipped response", candidates, err)
	}
}

func TestNestedJSONCorrelationDedupChoosesDeterministicPath(t *testing.T) {
	const reusedValue = "same-value-123456"
	observations := []correlate.Observation{
		{ID: 1, ResponseBody: `{"zId":"` + reusedValue + `","aId":"` + reusedValue + `"}`},
		{ID: 2, RequestURL: "https://example.test/use?id=" + reusedValue},
	}

	for run := 0; run < 100; run++ {
		correlations, err := detectNestedJSONCorrelations(observations, map[string]bool{}, newSalesforceCorrelationBudget())
		if err != nil {
			t.Fatalf("run %d: detectNestedJSONCorrelations returned error: %v", run, err)
		}
		if len(correlations) != 1 {
			t.Fatalf("run %d: correlations = %#v, want one same-value correlation", run, correlations)
		}
		if got := correlations[0].Expression; got != "aId" {
			t.Fatalf("run %d: extractor expression = %q, want deterministic lexicographically-first path aId", run, got)
		}
	}
}

func TestNestedJSONCandidatesQuoteControlCharacterKeysAsJSON(t *testing.T) {
	const body = `{"outer":{"token\u0007":"token-value-123456"}}`
	candidates, err := nestedJSONCandidatesChecked(body, newSalesforceCorrelationBudget())
	if err != nil {
		t.Fatalf("nestedJSONCandidatesChecked returned error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one control-character key candidate", candidates)
	}
	if got, want := candidates[0].Expression, `outer."token\u0007"`; got != want {
		t.Fatalf("JMESPath expression = %q, want JSON-safe %q", got, want)
	}
	compiled, err := jmespath.Compile(candidates[0].Expression)
	if err != nil {
		t.Fatalf("compile generated JMESPath: %v", err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode test JSON: %v", err)
	}
	got, err := compiled.Search(decoded)
	if err != nil {
		t.Fatalf("evaluate generated JMESPath: %v", err)
	}
	if got != "token-value-123456" {
		t.Fatalf("generated JMESPath returned %#v, want token-value-123456", got)
	}
}

// TestCompositeCandidatesDisambiguatesSluggedReferenceIds guards against the
// silent id cross-wiring that occurs when two referenceIds slug to the same name
// (e.g. "Acct-1" and "Acct_1"): each must get a distinct extractor name so its
// own record id is substituted, not the first subrequest's.
func TestCompositeCandidatesDisambiguatesSluggedReferenceIds(t *testing.T) {
	body := `{"compositeResponse":[
	  {"referenceId":"Acct-1","httpStatusCode":201,"body":{"id":"001AAA000111"}},
	  {"referenceId":"Acct_1","httpStatusCode":201,"body":{"id":"001BBB000222"}}
	]}`
	cands := compositeCandidates(body)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}
	if cands[0].Name == cands[1].Name {
		t.Fatalf("collision: both referenceIds produced the same extractor name %q", cands[0].Name)
	}
	// Each candidate must still target its OWN referenceId and carry its OWN id.
	byVal := map[string]string{}
	for _, c := range cands {
		byVal[c.Value] = c.Expression
	}
	if byVal["001AAA000111"] != "compositeResponse[?referenceId=='Acct-1'].body.id | [0]" {
		t.Fatalf("Acct-1 expr = %q", byVal["001AAA000111"])
	}
	if byVal["001BBB000222"] != "compositeResponse[?referenceId=='Acct_1'].body.id | [0]" {
		t.Fatalf("Acct_1 expr = %q", byVal["001BBB000222"])
	}
}

func TestSalesforceIDNormalization(t *testing.T) {
	const (
		id15 = "001A0000009z3ZI"
		id18 = "001A0000009z3ZIIAY"
	)
	got15, ok := salesforceID15(id18)
	if !ok || got15 != id15 {
		t.Fatalf("salesforceID15(%q) = %q, %v; want %q, true", id18, got15, ok, id15)
	}
	if got15From15, ok := salesforceID15(id15); !ok || got15From15 != id15 {
		t.Fatalf("salesforceID15(%q) = %q, %v; want unchanged 15-char id", id15, got15From15, ok)
	}
	for _, invalid := range []string{"", "not-an-id", "001A0000009z3Z!", "001A0000009z3Z"} {
		if got, ok := salesforceID15(invalid); ok {
			t.Fatalf("salesforceID15(%q) = %q, true; want false", invalid, got)
		}
	}
}

func TestCompositeCandidatesParsesTreeReferenceIds(t *testing.T) {
	body := `{"hasErrors":false,"results":[
	  {"referenceId":"AccountRoot","id":"001AAA000111","errors":[]},
	  {"referenceId":"ContactChild","id":"003BBB000222","errors":[]},
	  {"referenceId":"FailedChild","errors":[{"statusCode":"REQUIRED_FIELD_MISSING"}]}
	]}`
	cands := compositeCandidates(body)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2 tree ids: %#v", len(cands), cands)
	}
	expr := map[string]string{}
	for _, c := range cands {
		expr[c.Value] = c.Expression
		if !strings.Contains(c.Name, "salesforce_composite_tree") {
			t.Fatalf("candidate name = %q, want tree-specific name", c.Name)
		}
	}
	if expr["001AAA000111"] != "results[?referenceId=='AccountRoot'].id | [0]" {
		t.Fatalf("AccountRoot expr = %q", expr["001AAA000111"])
	}
	if expr["003BBB000222"] != "results[?referenceId=='ContactChild'].id | [0]" {
		t.Fatalf("ContactChild expr = %q", expr["003BBB000222"])
	}
}

func TestCompositeCandidatesParsesGraphReferenceIds(t *testing.T) {
	body := `{"graphs":[
	  {"graphId":"G1","graphResponse":{"compositeResponse":[
	    {"referenceId":"SharedRef","httpStatusCode":201,"body":{"id":"001AAA000111"}},
	    {"referenceId":"NoId","httpStatusCode":200,"body":{"totalSize":0}}
	  ]}},
	  {"graphId":"G2","graphResponse":{"compositeResponse":[
	    {"referenceId":"SharedRef","httpStatusCode":201,"body":{"id":"001BBB000222"}},
	    {"referenceId":"FailedRef","httpStatusCode":400,"body":[{"errorCode":"INVALID_FIELD"}]}
	  ]}}
	]}`
	cands := compositeCandidates(body)
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2 graph ids: %#v", len(cands), cands)
	}
	if cands[0].Name == cands[1].Name {
		t.Fatalf("graph candidates reused name %q for repeated referenceId", cands[0].Name)
	}
	expr := map[string]string{}
	for _, c := range cands {
		expr[c.Value] = c.Expression
	}
	wantG1 := "graphs[?graphId=='G1'].graphResponse.compositeResponse[] | [?referenceId=='SharedRef'].body.id | [0]"
	if expr["001AAA000111"] != wantG1 {
		t.Fatalf("G1 expr = %q, want %q", expr["001AAA000111"], wantG1)
	}
	wantG2 := "graphs[?graphId=='G2'].graphResponse.compositeResponse[] | [?referenceId=='SharedRef'].body.id | [0]"
	if expr["001BBB000222"] != wantG2 {
		t.Fatalf("G2 expr = %q, want %q", expr["001BBB000222"], wantG2)
	}
}

// TestAnalyzeNativePreviewCorrelatesCompositeReferenceId proves cross-request
// chaining: a record id created by a /composite subrequest is extracted with a
// referenceId-scoped JMESPath and reused in a later request's URL.
func TestAnalyzeNativePreviewCorrelatesCompositeReferenceId(t *testing.T) {
	const (
		base  = "https://acme.my.salesforce.com"
		newID = "001ABC0000111222AAA"
	)
	script := &dsl.Script{
		Name:     "SF Composite",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Composite Create", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/composite", Body: `{"compositeRequest":[{"referenceId":"NewAccount","method":"POST","url":"/services/data/v60.0/sobjects/Account","body":{"Name":"Acme"}}]}`},
			{Name: "Get Account", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + newID},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/composite", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"compositeResponse\":[{\"referenceId\":\"NewAccount\",\"httpStatusCode\":201,\"body\":{\"id\":\"` + newID + `\",\"success\":true}}]}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + newID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + newID + `\",\"Name\":\"Acme\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	composite := review.NativePreview.Script.Actions[0]
	var ext *dsl.Extractor
	for i := range composite.Extractors {
		if composite.Extractors[i].Type == "jmespath" && strings.Contains(composite.Extractors[i].Expression, "referenceId=='NewAccount'") {
			ext = &composite.Extractors[i]
		}
	}
	if ext == nil {
		t.Fatalf("composite extractors = %#v, want a referenceId-scoped jmespath extractor", composite.Extractors)
	}
	getTarget := review.NativePreview.Script.Actions[1].Target
	if strings.Contains(getTarget, newID) {
		t.Fatalf("get-account target = %q, still carries the recorded record id literal", getTarget)
	}
	if !strings.Contains(getTarget, "{{var."+ext.Name+"}}") {
		t.Fatalf("get-account target = %q, want it to reuse {{var.%s}}", getTarget, ext.Name)
	}
}

func TestAnalyzeNativePreviewCorrelatesCompositeID18ResponseTo15Request(t *testing.T) {
	const (
		base = "https://acme.my.salesforce.com"
		id15 = "001A0000009z3ZI"
		id18 = "001A0000009z3ZIIAY"
	)
	script := &dsl.Script{
		Name:     "SF Composite 15 ID",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Composite Create", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/composite", Body: `{"compositeRequest":[{"referenceId":"NewAccount","method":"POST","url":"/services/data/v60.0/sobjects/Account","body":{"Name":"Acme"}}]}`},
			{Name: "Get Account 15", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + id15},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/composite", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"compositeResponse\":[{\"referenceId\":\"NewAccount\",\"httpStatusCode\":201,\"body\":{\"id\":\"` + id18 + `\",\"success\":true}}]}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + id15 + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + id18 + `\",\"Name\":\"Acme\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	composite := review.NativePreview.Script.Actions[0]
	var ext *dsl.Extractor
	for i := range composite.Extractors {
		if composite.Extractors[i].Expression == "compositeResponse[?referenceId=='NewAccount'].body.id | [0]" {
			ext = &composite.Extractors[i]
		}
	}
	if ext == nil || ext.SaveLength != 15 {
		t.Fatalf("composite extractors = %#v, want SaveLength 15 extractor", composite.Extractors)
	}
	getTarget := review.NativePreview.Script.Actions[1].Target
	if strings.Contains(getTarget, id15) || strings.Contains(getTarget, id18) {
		t.Fatalf("get-account target = %q, still carries a recorded Salesforce id", getTarget)
	}
	if !strings.Contains(getTarget, "{{var."+ext.Name+"}}") {
		t.Fatalf("get-account target = %q, want 15-char id variable %s", getTarget, ext.Name)
	}
}

func TestAnalyzeNativePreviewCorrelatesCompositeIDUsedAs18And15(t *testing.T) {
	const (
		base = "https://acme.my.salesforce.com"
		id15 = "001A0000009z3ZI"
		id18 = "001A0000009z3ZIIAY"
	)
	script := &dsl.Script{
		Name:     "SF Composite Dual ID",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Composite Create", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/composite", Body: `{"compositeRequest":[{"referenceId":"NewAccount","method":"POST","url":"/services/data/v60.0/sobjects/Account","body":{"Name":"Acme"}}]}`},
			{Name: "Get Account 18", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + id18},
			{Name: "Get Account 15", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + id15},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/composite", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"compositeResponse\":[{\"referenceId\":\"NewAccount\",\"httpStatusCode\":201,\"body\":{\"id\":\"` + id18 + `\",\"success\":true}}]}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + id18 + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + id18 + `\",\"Name\":\"Acme\"}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + id15 + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + id18 + `\",\"Name\":\"Acme\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	composite := review.NativePreview.Script.Actions[0]
	rawName := "corr_salesforce_composite_newaccount"
	trimName := rawName + "_15"
	seen := map[string]dsl.Extractor{}
	for _, ext := range composite.Extractors {
		seen[ext.Name] = ext
	}
	if ext, ok := seen[rawName]; !ok || ext.SaveLength != 0 {
		t.Fatalf("raw extractor = %#v ok=%v, want untrimmed %s", ext, ok, rawName)
	}
	if ext, ok := seen[trimName]; !ok || ext.SaveLength != 15 {
		t.Fatalf("trimmed extractor = %#v ok=%v, want SaveLength 15 %s", ext, ok, trimName)
	}
	if got := review.NativePreview.Script.Actions[1].Target; strings.Contains(got, id18) || !strings.Contains(got, "{{var."+rawName+"}}") {
		t.Fatalf("18-char target = %q, want raw variable", got)
	}
	if got := review.NativePreview.Script.Actions[2].Target; strings.Contains(got, id15) || !strings.Contains(got, "{{var."+trimName+"}}") {
		t.Fatalf("15-char target = %q, want trimmed variable", got)
	}
}

func TestAnalyzeNativePreviewCorrelatesCompositeTreeReferenceId(t *testing.T) {
	const (
		base  = "https://acme.my.salesforce.com"
		newID = "001TREE000111222AAA"
	)
	script := &dsl.Script{
		Name:     "SF Composite Tree",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Tree Create", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/composite/sobjects/tree/Account", Body: `{"records":[{"attributes":{"type":"Account","referenceId":"RootAccount"},"Name":"Acme"}]}`},
			{Name: "Get Account", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + newID},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/composite/sobjects/tree/Account", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"hasErrors\":false,\"results\":[{\"referenceId\":\"RootAccount\",\"id\":\"` + newID + `\",\"errors\":[]}]}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + newID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + newID + `\",\"Name\":\"Acme\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	treeCreate := review.NativePreview.Script.Actions[0]
	var ext *dsl.Extractor
	for i := range treeCreate.Extractors {
		if treeCreate.Extractors[i].Expression == "results[?referenceId=='RootAccount'].id | [0]" {
			ext = &treeCreate.Extractors[i]
		}
	}
	if ext == nil {
		t.Fatalf("tree extractors = %#v, want referenceId-scoped tree extractor", treeCreate.Extractors)
	}
	getTarget := review.NativePreview.Script.Actions[1].Target
	if strings.Contains(getTarget, newID) || !strings.Contains(getTarget, "{{var."+ext.Name+"}}") {
		t.Fatalf("get-account target = %q, want substitution with %s", getTarget, ext.Name)
	}
}

func TestAnalyzeNativePreviewCorrelatesCompositeGraphReferenceId(t *testing.T) {
	const (
		base  = "https://acme.my.salesforce.com"
		newID = "001GRAPH00111222AAA"
	)
	script := &dsl.Script{
		Name:     "SF Composite Graph",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Graph Create", Type: "POST", Method: "POST", Target: base + "/services/data/v60.0/composite/graph", Body: `{"graphs":[{"graphId":"G1","compositeRequest":[{"referenceId":"RootAccount","method":"POST","url":"/services/data/v60.0/sobjects/Account","body":{"Name":"Acme"}}]}]}`},
			{Name: "Get Account", Type: "GET", Method: "GET", Target: base + "/services/data/v60.0/sobjects/Account/" + newID},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + base + `/services/data/v60.0/composite/graph", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"graphs\":[{\"graphId\":\"G1\",\"graphResponse\":{\"compositeResponse\":[{\"referenceId\":\"RootAccount\",\"httpStatusCode\":201,\"body\":{\"id\":\"` + newID + `\",\"success\":true}}]}}]}"}}},
	      {"request": {"method": "GET", "url": "` + base + `/services/data/v60.0/sobjects/Account/` + newID + `", "headers": []},
	       "response": {"headers": [], "content": {"text": "{\"Id\":\"` + newID + `\",\"Name\":\"Acme\"}"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	graphCreate := review.NativePreview.Script.Actions[0]
	wantExpr := "graphs[?graphId=='G1'].graphResponse.compositeResponse[] | [?referenceId=='RootAccount'].body.id | [0]"
	var ext *dsl.Extractor
	for i := range graphCreate.Extractors {
		if graphCreate.Extractors[i].Expression == wantExpr {
			ext = &graphCreate.Extractors[i]
		}
	}
	if ext == nil {
		t.Fatalf("graph extractors = %#v, want graph/referenceId-scoped extractor", graphCreate.Extractors)
	}
	getTarget := review.NativePreview.Script.Actions[1].Target
	if strings.Contains(getTarget, newID) || !strings.Contains(getTarget, "{{var."+ext.Name+"}}") {
		t.Fatalf("get-account target = %q, want substitution with %s", getTarget, ext.Name)
	}
}

// TestClassifySalesforceCometd guards the A5 CometD classification: Bayeux
// traffic is recognized as "cometd"/"streaming_subscribe" by path or channel,
// without stealing /services/data REST traffic.
func TestClassifySalesforceCometd(t *testing.T) {
	connect := &dsl.Action{Method: "POST", Target: "https://acme.my.salesforce.com/cometd/57.0", Body: `message=[{"channel":"/meta/connect","clientId":"abc"}]`}
	if got := classifySalesforceAction(connect, connect.Target); got != "cometd" {
		t.Fatalf("classifySalesforceAction = %q, want cometd", got)
	}
	if got := classifySalesforceFlow(salesforceActionEvidence(connect, connect.Target), ""); got != "streaming_subscribe" {
		t.Fatalf("classifySalesforceFlow = %q, want streaming_subscribe", got)
	}
	rest := &dsl.Action{Method: "GET", Target: "https://acme.my.salesforce.com/services/data/v59.0/sobjects/Account/001"}
	if got := classifySalesforceAction(rest, rest.Target); got == "cometd" {
		t.Fatalf("REST traffic misclassified as cometd: %q", got)
	}
}

// TestCometdCandidatesParsesHandshakeArray covers the CometD clientId parser:
// it extracts the clientId from the /meta/handshake message in a Bayeux response
// array, and no-ops on non-array bodies and arrays without a handshake clientId.
func TestCometdCandidatesParsesHandshakeArray(t *testing.T) {
	ok := cometdCandidates(`[{"channel":"/meta/handshake","successful":true,"clientId":"abc123"}]`)
	if len(ok) != 1 || ok[0].Value != "abc123" || ok[0].ExtractType != "jmespath" {
		t.Fatalf("handshake parse = %#v, want one jmespath clientId candidate", ok)
	}
	if ok[0].Expression != "[?channel=='/meta/handshake'].clientId | [0]" {
		t.Fatalf("expression = %q", ok[0].Expression)
	}
	// Not an array (object root) -> nil.
	if c := cometdCandidates(`{"channel":"/meta/handshake","clientId":"x"}`); c != nil {
		t.Fatalf("object-root body should yield no candidates: %#v", c)
	}
	// Array without a handshake clientId (e.g. a connect response) -> nil.
	if c := cometdCandidates(`[{"channel":"/meta/connect","successful":true}]`); c != nil {
		t.Fatalf("non-handshake array should yield no candidates: %#v", c)
	}
}

// TestAnalyzeNativePreviewCometdConnectLoop proves the A5 CometD lifecycle: the
// Bayeux clientId is correlated with an array-root JMESPath and reused in later
// requests, and the contiguous /meta/connect long-poll run is collapsed into a
// single control_loop (handshake and subscribe left as discrete steps).
func TestAnalyzeNativePreviewCometdConnectLoop(t *testing.T) {
	const (
		base     = "https://acme.my.salesforce.com"
		cometd   = base + "/cometd/57.0"
		clientID = "3f8k2j9xqz0011aa"
	)
	connectBody := func(id string) string {
		return `message=[{"id":"` + id + `","channel":"/meta/connect","clientId":"` + clientID + `","connectionType":"long-polling"}]`
	}
	script := &dsl.Script{
		Name:     "SF Streaming",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: base},
		Actions: []dsl.Action{
			{Name: "Handshake", Type: "POST", Method: "POST", Target: cometd, Body: `message=[{"id":"1","channel":"/meta/handshake","supportedConnectionTypes":["long-polling"],"version":"1.0"}]`},
			{Name: "Subscribe", Type: "POST", Method: "POST", Target: cometd, Body: `message=[{"id":"2","channel":"/meta/subscribe","clientId":"` + clientID + `","subscription":"/event/Order__e"}]`},
			{Name: "Connect 1", Type: "POST", Method: "POST", Target: cometd, Body: connectBody("3")},
			{Name: "Connect 2", Type: "POST", Method: "POST", Target: cometd, Body: connectBody("4")},
		},
	}
	har := []byte(`{
	  "log": {
	    "entries": [
	      {"request": {"method": "POST", "url": "` + cometd + `", "headers": [], "postData": {"text": "message=[{\"id\":\"1\",\"channel\":\"/meta/handshake\"}]"}},
	       "response": {"headers": [], "content": {"text": "[{\"id\":\"1\",\"channel\":\"/meta/handshake\",\"successful\":true,\"clientId\":\"` + clientID + `\"}]"}}},
	      {"request": {"method": "POST", "url": "` + cometd + `", "headers": [], "postData": {"text": "message=[{\"id\":\"2\",\"channel\":\"/meta/subscribe\",\"clientId\":\"` + clientID + `\",\"subscription\":\"/event/Order__e\"}]"}},
	       "response": {"headers": [], "content": {"text": "[{\"id\":\"2\",\"channel\":\"/meta/subscribe\",\"successful\":true}]"}}},
	      {"request": {"method": "POST", "url": "` + cometd + `", "headers": [], "postData": {"text": "message=[{\"id\":\"3\",\"channel\":\"/meta/connect\",\"clientId\":\"` + clientID + `\"}]"}},
	       "response": {"headers": [], "content": {"text": "[{\"id\":\"3\",\"channel\":\"/meta/connect\",\"successful\":true}]"}}},
	      {"request": {"method": "POST", "url": "` + cometd + `", "headers": [], "postData": {"text": "message=[{\"id\":\"4\",\"channel\":\"/meta/connect\",\"clientId\":\"` + clientID + `\"}]"}},
	       "response": {"headers": [], "content": {"text": "[{\"id\":\"4\",\"channel\":\"/meta/connect\",\"successful\":true}]"}}}
	    ]
	  }
	}`)

	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil, want script")
	}
	actions := review.NativePreview.Script.Actions
	// handshake, subscribe, and ONE collapsed connect loop (the 2 connects fold).
	if len(actions) != 3 {
		t.Fatalf("preview has %d actions, want 3 (handshake, subscribe, connect loop): %#v", len(actions), actions)
	}

	// clientId extractor on the handshake action, array-root JMESPath.
	handshake := actions[0]
	var ext *dsl.Extractor
	for i := range handshake.Extractors {
		if handshake.Extractors[i].Type == "jmespath" && strings.Contains(handshake.Extractors[i].Expression, "/meta/handshake") {
			ext = &handshake.Extractors[i]
		}
	}
	if ext == nil {
		t.Fatalf("handshake extractors = %#v, want an array-root jmespath clientId extractor", handshake.Extractors)
	}

	// The connect run is collapsed into a control_loop whose body reuses the var.
	var loop *dsl.Action
	for i := range actions {
		if actions[i].Type == "control_loop" {
			loop = &actions[i]
		}
	}
	if loop == nil || loop.Control == nil {
		t.Fatalf("preview actions = %#v, want a collapsed connect control_loop", actions)
	}
	if loop.Control.Count != 2 || len(loop.Control.Steps) != 1 {
		t.Fatalf("connect loop = %#v, want Count=2 with one step", loop.Control)
	}
	connectStep := loop.Control.Steps[0]
	connectBodyOut := connectStep.Body
	if strings.Contains(connectBodyOut, clientID) || !strings.Contains(connectBodyOut, "{{var."+ext.Name+"}}") {
		t.Fatalf("connect loop body = %q, want clientId reused via {{var.%s}}", connectBodyOut, ext.Name)
	}
	if !strings.Contains(connectBodyOut, cometdLoopIndexTemplate) {
		t.Fatalf("connect loop body = %q, want Bayeux id rewritten to %s", connectBodyOut, cometdLoopIndexTemplate)
	}
	// The folded connect must carry a long-poll-safe timeout (> the ~110s server
	// hold), or the default client timeout would make every poll error.
	if connectStep.TimeoutMs <= 110000 {
		t.Fatalf("connect loop step timeout_ms = %d, want > 110000 for the long-poll hold", connectStep.TimeoutMs)
	}

	assertNativeReferencePath(t, review.NativePreview, 3, "actions[2]", "actions[2].control.steps[0]")
	assertNativeReferencePath(t, review.NativePreview, 4, "actions[3]", "")
}

func TestWrapCometdConnectLoopRewritesRawMessageID(t *testing.T) {
	body := `[{"id":"7","channel":"/meta/connect","clientId":"{{var.corr_salesforce_cometd_client_id}}","connectionType":"long-polling"}]`
	loop := wrapCometdConnectLoop(dsl.Action{Name: "Connect", Body: body}, 3)
	gotBody := loop.Control.Steps[0].Body

	var messages []map[string]any
	if err := json.Unmarshal([]byte(gotBody), &messages); err != nil {
		t.Fatalf("rewritten raw body is not JSON: %v\n%s", err, gotBody)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one connect message", messages)
	}
	if messages[0]["id"] != cometdLoopIndexTemplate {
		t.Fatalf("message id = %#v, want %s", messages[0]["id"], cometdLoopIndexTemplate)
	}
	if messages[0]["clientId"] != "{{var.corr_salesforce_cometd_client_id}}" {
		t.Fatalf("clientId = %#v, want existing variable untouched", messages[0]["clientId"])
	}
}

func TestWrapCometdConnectLoopRewritesFormEncodedMessageID(t *testing.T) {
	message := `[{"id":"8","channel":"/meta/connect","clientId":"{{var.corr_salesforce_cometd_client_id}}","connectionType":"long-polling"}]`
	body := "message=" + url.QueryEscape(message) + "&timeout=110000"
	loop := wrapCometdConnectLoop(dsl.Action{Name: "Connect", Body: body}, 2)
	gotBody := loop.Control.Steps[0].Body
	if !strings.Contains(gotBody, cometdLoopIndexTemplate) {
		t.Fatalf("form body = %q, want unescaped loop-index template", gotBody)
	}
	if !strings.Contains(gotBody, "{{var.corr_salesforce_cometd_client_id}}") {
		t.Fatalf("form body = %q, want existing clientId template preserved", gotBody)
	}

	values, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("rewritten form body does not parse: %v\n%s", err, gotBody)
	}
	var messages []map[string]any
	if err := json.Unmarshal([]byte(values.Get("message")), &messages); err != nil {
		t.Fatalf("rewritten message is not JSON: %v\n%s", err, values.Get("message"))
	}
	if messages[0]["id"] != cometdLoopIndexTemplate {
		t.Fatalf("message id = %#v, want %s", messages[0]["id"], cometdLoopIndexTemplate)
	}
	if messages[0]["clientId"] != "{{var.corr_salesforce_cometd_client_id}}" {
		t.Fatalf("clientId = %#v, want existing variable untouched", messages[0]["clientId"])
	}
	if values.Get("timeout") != "110000" {
		t.Fatalf("timeout param = %q, want preserved", values.Get("timeout"))
	}
}

func TestWrapCometdConnectLoopLeavesBatchedMessageIDAlone(t *testing.T) {
	body := `message=[{"id":"7","channel":"/meta/connect","clientId":"x"},{"id":"8","channel":"/meta/connect","clientId":"x"}]`
	loop := wrapCometdConnectLoop(dsl.Action{Name: "Connect", Body: body}, 2)
	if got := loop.Control.Steps[0].Body; got != body {
		t.Fatalf("batched connect body was rewritten:\n got: %s\nwant: %s", got, body)
	}
}

// TestIsCometdConnectActionExcludesBatchedMessages guards that a batched Bayeux
// message (e.g. subscribe+connect in one POST) is NOT treated as a foldable
// connect — folding it would repeat the subscribe.
func TestIsCometdConnectActionExcludesBatchedMessages(t *testing.T) {
	cometdMeta := map[string]any{"salesforce": map[string]any{"classification": "cometd"}}
	pureConnect := &dsl.Action{Metadata: cometdMeta, Body: `message=[{"id":"1","channel":"/meta/connect","clientId":"x"}]`}
	if !isCometdConnectAction(pureConnect) {
		t.Fatal("a pure /meta/connect request should be foldable")
	}
	batched := &dsl.Action{Metadata: cometdMeta, Body: `message=[{"channel":"/meta/subscribe","subscription":"/event/X"},{"channel":"/meta/connect","clientId":"x"}]`}
	if isCometdConnectAction(batched) {
		t.Fatal("a batched subscribe+connect request must not be folded as a connect")
	}
}

func TestAnalyzeClassifiesBroadenedSalesforceSurface(t *testing.T) {
	script := &dsl.Script{
		Name:     "SF Broad Surface",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme--dev.sandbox.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Apex REST", Type: "GET", Method: "GET", Target: "https://acme.my.salesforce.com/services/apexrest/MyApi/v1/resource"},
			{Name: "Composite", Type: "POST", Method: "POST", Target: "https://acme.my.salesforce.com/services/data/v60.0/composite"},
			{Name: "Composite Graph", Type: "POST", Method: "POST", Target: "https://acme.my.salesforce.com/services/data/v60.0/composite/graph"},
			{Name: "GraphQL", Type: "POST", Method: "POST", Target: "https://acme.my.salesforce.com/services/data/v60.0/graphql"},
			{Name: "Bulk Ingest", Type: "POST", Method: "POST", Target: "https://acme--dev.sandbox.my.salesforce.com/services/data/v60.0/jobs/ingest"},
			{Name: "Bulk Query", Type: "GET", Method: "GET", Target: "https://acme.my.salesforce.com/services/data/v60.0/jobs/query/750xx"},
			{Name: "Agentforce Session", Type: "POST", Method: "POST", Target: "https://api.salesforce.com/einstein/ai-agent/v1/agents/0Xxxx/sessions"},
			{Name: "File Download", Type: "GET", Method: "GET", Target: "https://acme.file.force.com/sfc/servlet.shepherd/version/download/068xx"},
		},
	}

	review, err := Analyze(script, AnalyzeOptions{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !review.Detected {
		t.Fatal("Salesforce traffic was not detected")
	}

	for _, check := range []struct {
		name string
		got  []EndpointObservation
	}{
		{"apex_rest", review.ApexRestEndpoints},
		{"composite", review.CompositeEndpoints},
		{"graphql", review.GraphQLEndpoints},
		{"bulk", review.BulkEndpoints},
		{"agentforce", review.AgentforceEndpoints},
	} {
		if len(check.got) == 0 {
			t.Fatalf("%s endpoints not detected; review = %#v", check.name, review)
		}
	}
	if len(review.CompositeEndpoints) < 2 {
		t.Fatalf("composite endpoints = %#v, want composite + composite/graph", review.CompositeEndpoints)
	}

	domainKinds := map[string]string{}
	for _, domain := range review.Domains {
		domainKinds[domain.Host] = domain.Kind
	}
	for host, wantKind := range map[string]string{
		"acme--dev.sandbox.my.salesforce.com": "sandbox",
		"acme.file.force.com":                 "content",
		"api.salesforce.com":                  "agentforce",
	} {
		if domainKinds[host] != wantKind {
			t.Fatalf("domain %s kind = %q, want %q (%#v)", host, domainKinds[host], wantKind, review.Domains)
		}
	}

	flowKinds := map[string]bool{}
	for _, transaction := range review.Transactions {
		for _, flow := range transaction.FlowKinds {
			flowKinds[flow] = true
		}
	}
	for _, want := range []string{"apex_custom_api", "composite", "bulk_data", "agent_conversation"} {
		if !flowKinds[want] {
			t.Fatalf("flow kinds = %#v, want %s", flowKinds, want)
		}
	}
}

func TestAnalyzeProposesAuraMessageParameterization(t *testing.T) {
	formMessage := `{"actions":[{"id":"123;a","descriptor":"apex://AccountController/ACTION$getAccount","params":{"recordId":"001D000000IqhSLIAZ"}}]}`
	form := url.Values{}
	form.Set("message", formMessage)
	form.Set("aura.token", "tok-123456")
	form.Set("aura.context", `{"fwuid":"abc"}`)

	script := &dsl.Script{
		Name:     "SF Aura Params",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Form-encoded Aura",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   form.Encode(),
		}, {
			Name:   "Raw JSON Aura",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"actions":[{"id":"1;a","descriptor":"apex://AcctController/ACTION$get","params":{"entity":{"accountId":"5003000000D8cuIAAR"}}}]}`,
		}},
	}

	review, err := Analyze(script, AnalyzeOptions{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	byKey := map[string]ParameterizationCandidate{}
	for _, candidate := range review.Parameterizations {
		byKey[candidate.ParamKey] = candidate
	}
	if len(review.Parameterizations) != 2 {
		t.Fatalf("parameterizations = %#v, want recordId + accountId", review.Parameterizations)
	}
	recordID, ok := byKey["recordId"]
	if !ok || recordID.Expression != "actions[0].params.recordId" || recordID.Kind != "record_id" {
		t.Fatalf("recordId candidate = %#v, want form-encoded record id", recordID)
	}
	if !strings.HasPrefix(recordID.Template, "{{data.salesforce_records.") || recordID.Column == "" {
		t.Fatalf("recordId template = %q / column = %q, want data-source template", recordID.Template, recordID.Column)
	}
	accountID, ok := byKey["accountId"]
	if !ok || accountID.Expression != "actions[0].params.entity.accountId" {
		t.Fatalf("accountId candidate = %#v, want nested raw-JSON path", accountID)
	}
}

func TestAuraMessageParameterizationsSkipsCorrelatedValues(t *testing.T) {
	refs := []actionRef{{
		Sequence: 7,
		Action: &dsl.Action{
			Body: `{"actions":[{"id":"synthetic-id","descriptor":"synthetic","params":{"recordId":"001D000000IqhSLIAZ","contactId":"003D000000Zx1AAAS"}}]}`,
		},
	}}
	// recordId is already produced by an earlier response (correlated); only the
	// non-correlated contactId should be proposed for parameterization.
	correlations := []correlate.Correlation{{Name: "corr_record_id", Value: "001D000000IqhSLIAZ"}}

	got := auraMessageParameterizations(refs, correlations)
	if len(got) != 1 {
		t.Fatalf("candidates = %#v, want only the non-correlated contactId", got)
	}
	if got[0].ParamKey != "contactId" {
		t.Fatalf("candidate = %#v, want contactId", got[0])
	}
	if got[0].ActionSeq != 7 {
		t.Fatalf("action seq = %d, want 7", got[0].ActionSeq)
	}
}

func TestClassifySalesforceActionAnchorsLogin(t *testing.T) {
	cases := map[string]string{
		"https://acme.my.salesforce.com/services/apexrest/LoginController/recent":        "apex_rest",
		"https://acme.my.salesforce.com/services/data/v60.0/sobjects/Login_Event__c/a01": "rest_api",
		"https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/LoginHistory": "ui_api",
		"https://login.salesforce.com/services/oauth2/token":                             "login",
		"https://acme.my.salesforce.com/secur/login":                                     "login",
		"https://test.salesforce.com/":                                                   "login",
	}
	for rawURL, want := range cases {
		if got := classifySalesforceAction(&dsl.Action{}, rawURL); got != want {
			t.Errorf("classifySalesforceAction(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

func TestClassifySalesforceFlowAnchoredShapesBeatObjectNames(t *testing.T) {
	cases := map[string]string{
		`{"compositeRequest":[{"url":"/services/data/v60.0/composite/sobjects/Account"}]}`: "composite",
		`/services/data/v60.0/jobs/ingest {"object":"Account"}`:                            "bulk_data",
		`/services/apexrest/AccountService {"contact":"x"}`:                                "apex_custom_api",
		`contentversion title billing-statement.pdf for account`:                           "file_content",
		`apex://AccountController/ACTION$getAccount`:                                       "account",
	}
	for evidence, want := range cases {
		if got := classifySalesforceFlow(evidence, ""); got != want {
			t.Errorf("classifySalesforceFlow(%q) = %q, want %q", evidence, got, want)
		}
	}
}

func TestAuraMessageParameterizationsTerminatesOnNameCollision(t *testing.T) {
	// Case variants share the same column slug; distinct occurrences must receive
	// deterministic unique names and independently bindable columns.
	refs := []actionRef{
		{Sequence: 1, Action: &dsl.Action{Body: `{"actions":[{"id":"synthetic-id","descriptor":"synthetic","params":{"RecordId":"001D000000IqhSLIAZ","recordId":"5003000000D8cuIAAR"}}]}`}},
		{Sequence: 2, Action: &dsl.Action{Body: `{"actions":[{"id":"synthetic-id","descriptor":"synthetic","params":{"recordId":"00Q3000001AbCdEFGH"}}]}`}},
	}
	got := auraMessageParameterizations(refs, nil)
	if len(got) != 3 {
		t.Fatalf("candidates = %#v, want 3 uniquely-named candidates", got)
	}
	names := map[string]bool{}
	for _, c := range got {
		if names[c.Name] {
			t.Fatalf("duplicate candidate name %q in %#v", c.Name, got)
		}
		names[c.Name] = true
	}
}

func TestAuraMessageBodyHandlesRawJSONWithEmbeddedQueryString(t *testing.T) {
	// A raw-JSON Aura body whose string value contains "&message=" must not be
	// mis-split by url.ParseQuery; the recordId candidate must still surface.
	body := `{"actions":[{"id":"synthetic-id","descriptor":"synthetic","params":{"recordId":"001D000000IqhSLIAZ","redirect":"https://x?a=1&message=2"}}]}`
	got := auraMessageParameterizations([]actionRef{{Sequence: 1, Action: &dsl.Action{Body: body}}}, nil)
	if len(got) != 1 || got[0].ParamKey != "recordId" {
		t.Fatalf("candidates = %#v, want a single recordId candidate from raw JSON", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAnalyzeHandlesMalformedTargetURLs ensures imported actions whose Target
// contains characters that make url.Parse return (nil, err) do not panic.
func TestAnalyzeHandlesMalformedTargetURLs(t *testing.T) {
	for _, target := range []string{
		"https://example.my.salesforce.com/services/data\x7f/v59.0",
		"https://example.my.salesforce.com/%zz",
		"ht tp://bad host/login",
	} {
		script := &dsl.Script{
			Name:     "SF Import",
			Protocol: "http",
			Config:   dsl.ScriptConfig{BaseURL: "https://example.my.salesforce.com"},
			Actions:  []dsl.Action{{Name: "req", Type: "POST", Method: "POST", Target: target}},
		}
		if _, err := Analyze(script, AnalyzeOptions{BuildNative: true}); err != nil {
			t.Fatalf("Analyze(%q) returned error: %v", target, err)
		}
	}
}
