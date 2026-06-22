package salesforce

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
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
	if poll.Control.Condition == nil || !strings.Contains(poll.Control.Condition.Expr, "JobComplete") || !strings.Contains(poll.Control.Condition.Expr, "has(var.bulk_job_state)") {
		t.Fatalf("loop condition = %#v, want a null-safe JobComplete predicate", poll.Control.Condition)
	}
	if len(poll.Control.Steps) != 1 {
		t.Fatalf("loop steps = %d, want 1", len(poll.Control.Steps))
	}
	step := poll.Control.Steps[0]
	var stateExt *dsl.Extractor
	for i := range step.Extractors {
		if step.Extractors[i].Name == "bulk_job_state" {
			stateExt = &step.Extractors[i]
		}
	}
	if stateExt == nil || stateExt.Expression != "$.state" {
		t.Fatalf("loop step extractors = %#v, want a bulk_job_state $.state extractor", step.Extractors)
	}
	if !step.ThinkTimeAfter || step.ThinkTime.Type == "" {
		t.Fatalf("loop step = %#v, want a poll-interval think time (think_time_after)", step)
	}
	if strings.Contains(step.Target, jobID) {
		t.Fatalf("loop step target = %q, still carries the recorded job id literal", step.Target)
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
	// The folded connect must carry a long-poll-safe timeout (> the ~110s server
	// hold), or the default client timeout would make every poll error.
	if connectStep.TimeoutMs <= 110000 {
		t.Fatalf("connect loop step timeout_ms = %d, want > 110000 for the long-poll hold", connectStep.TimeoutMs)
	}
}

// TestIsCometdConnectActionExcludesBatchedMessages guards that a batched Bayeux
// message (e.g. subscribe+connect in one POST) is NOT treated as a foldable
// connect — folding it would repeat the subscribe.
func TestIsCometdConnectActionExcludesBatchedMessages(t *testing.T) {
	cometdMeta := map[string]any{"salesforce": map[string]any{"classification": "cometd"}}
	pureConnect := &dsl.Action{Metadata: cometdMeta, Body: `message=[{"channel":"/meta/connect","clientId":"x"}]`}
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
			Body: `{"actions":[{"params":{"recordId":"001D000000IqhSLIAZ","contactId":"003D000000Zx1AAAS"}}]}`,
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
	// Columns rec_3, rec, rec with distinct SF-ID values previously caused a
	// non-terminating disambiguation loop (param_rec collided with param_rec_3).
	refs := []actionRef{
		{Sequence: 1, Action: &dsl.Action{Body: `{"actions":[{"params":{"rec 3":"001D000000IqhSLIAZ","rec":"5003000000D8cuIAAR"}}]}`}},
		{Sequence: 2, Action: &dsl.Action{Body: `{"actions":[{"params":{"rec":"00Q3000001AbCdEFGH"}}]}`}},
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
	body := `{"actions":[{"params":{"recordId":"001D000000IqhSLIAZ","redirect":"https://x?a=1&message=2"}}]}`
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
