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
