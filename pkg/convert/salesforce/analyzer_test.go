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
