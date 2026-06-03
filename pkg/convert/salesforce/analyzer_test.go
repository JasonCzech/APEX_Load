package salesforce

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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
			Body:   `{"descriptor":"apex://AccountController/ACTION$get","token":"tok-abcdef123456"}`,
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
	if !strings.Contains(preview.Actions[1].Body, "{{var.corr_access_token}}") {
		t.Fatalf("aura body = %q, want correlated token variable", preview.Actions[1].Body)
	}
	if len(preview.Actions[1].Assertions) != 2 {
		t.Fatalf("aura assertions = %#v, want Aura state assertions", preview.Actions[1].Assertions)
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
				"loadrunner_transaction": "T_AccountLookup",
			},
		}, {
			Name:   "Work Order Lookup",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://WorkOrderController/ACTION$getWorkOrder","params":{"record-id":"a0WSANITIZED123"}}`,
			Metadata: map[string]any{
				"loadrunner_transaction": "T_WorkOrderLookup",
			},
		}, {
			Name:   "Service Address Search",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://BWC_ServiceAddressController/ACTION$getSearchResults","params":{"query":"100 Main"}}`,
			Metadata: map[string]any{
				"loadrunner_transaction": "T_ServiceAddress",
			},
		}, {
			Name:   "Record UI",
			Type:   "GET",
			Method: "GET",
			Target: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001SANITIZED",
			Metadata: map[string]any{
				"loadrunner_transaction": "T_RecordUI",
			},
		}, {
			Name:   "Use Nested Record ID",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"params":{"record-id":"a0WSANITIZED123"}}`,
			Metadata: map[string]any{
				"loadrunner_transaction": "T_WorkOrderLookup",
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
	foundJMES := false
	for _, extractor := range preview.Actions[1].Extractors {
		if extractor.Type == "jmespath" && extractor.Expression == `actions[0].returnValue."record-id"` {
			foundJMES = true
			break
		}
	}
	if !foundJMES {
		t.Fatalf("work order extractors = %#v, want nested JMESPath extractor", preview.Actions[1].Extractors)
	}
	if !strings.Contains(preview.Actions[4].Body, "{{var.corr_record_id}}") {
		t.Fatalf("correlated body = %q, want nested record id variable", preview.Actions[4].Body)
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
			Body:   `{"descriptor":"apex://AccountController/ACTION$get"}`,
		}, {
			Name:   "Aura Two",
			Type:   "POST",
			Method: "POST",
			Target: "https://acme.lightning.force.com/aura",
			Body:   `{"descriptor":"apex://WorkOrderController/ACTION$get"}`,
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
	if review.NativePreview.Script.Actions[1].Metadata["salesforce"].(map[string]any)["har_match_id"] == 0 {
		t.Fatalf("first duplicate Aura request was not matched: %#v", review.NativePreview.Script.Actions[1].Metadata)
	}
	if review.NativePreview.Script.Actions[2].Metadata["salesforce"].(map[string]any)["har_match_id"] == 0 {
		t.Fatalf("second duplicate Aura request was not matched: %#v", review.NativePreview.Script.Actions[2].Metadata)
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
