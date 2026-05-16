package sample

import (
	"strings"
	"testing"
)

func TestConvertRESTJSON(t *testing.T) {
	includeAssertions := true
	inferExtractors := true
	result, err := Convert(ImportRequest{
		Name:   "Order Flow",
		Format: "rest_json",
		Content: `{
			"requests": [
				{
					"request": {
						"method": "POST",
						"url": "https://api.example.com/v1/login",
						"headers": {"Content-Type": "application/json", "Authorization": "Bearer top-secret"},
						"body": {"username": "alice", "password": "secret"}
					},
					"response": {
						"status": 200,
						"headers": {"Content-Type": "application/json"},
						"body": {"token": "abc123token", "userId": 91234}
					}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://api.example.com/v1/users/91234/orders",
						"headers": {"Authorization": "Bearer abc123token"}
					},
					"response": {
						"status": 200,
						"body": {"orders": []}
					}
				}
			]
		}`,
		Options: ConvertOptions{
			IncludeAssertions: &includeAssertions,
			InferExtractors:   &inferExtractors,
		},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q, want https://api.example.com", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; !strings.Contains(got, "{{secret.authorization}}") {
		t.Fatalf("expected authorization header to be sanitized, got %q", got)
	}
	if got := result.Script.Actions[0].Body; !strings.Contains(got, "{{secret.redacted}}") {
		t.Fatalf("expected password to be sanitized in body, got %q", got)
	}
	if len(result.Script.Actions[0].Assertions) != 1 {
		t.Fatalf("first action assertions = %d, want 1", len(result.Script.Actions[0].Assertions))
	}
	if len(result.Script.Actions[0].Extractors) == 0 {
		t.Fatal("expected first action to include inferred extractors")
	}
	if got := result.Script.Actions[1].Target; !strings.Contains(got, "{{var.corr_userid}}") {
		t.Fatalf("expected correlated user ID in second target, got %q", got)
	}
	if result.Stats.CorrelationsDetected == 0 {
		t.Fatal("expected correlation stats to be populated")
	}
}

func TestConvertCurl(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -X POST https://api.example.com/v1/orders -H 'Content-Type: application/json' -d '{"item":"widget","qty":5}'`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Method != "POST" {
		t.Fatalf("method = %q, want POST", result.Script.Actions[0].Method)
	}
	if result.Script.Actions[0].Target != "/v1/orders" {
		t.Fatalf("target = %q, want /v1/orders", result.Script.Actions[0].Target)
	}
	if got := result.Script.Actions[0].Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if !strings.Contains(result.Script.Actions[0].Body, `"qty":5`) {
		t.Fatalf("body = %q, want request payload preserved", result.Script.Actions[0].Body)
	}
	if result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q, want https://api.example.com", result.Script.Config.BaseURL)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected missing correlation warning for single request sample")
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
	}
}

func TestConvertCurlSOAP(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl -X POST https://example.com/services/quotes -H 'Content-Type: text/xml; charset=utf-8' -H 'SOAPAction: urn:GetQuote' -d '<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><GetQuote xmlns="urn:quotes"><Symbol>STORM</Symbol></GetQuote></soapenv:Body></soapenv:Envelope>'`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", result.Script.Protocol)
	}
	if result.Script.Config.SOAP.Version != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", result.Script.Config.SOAP.Version)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.SOAP == nil {
		t.Fatal("expected SOAP action metadata")
	}
	if action.SOAP.Action != "urn:GetQuote" {
		t.Fatalf("soap action = %q, want urn:GetQuote", action.SOAP.Action)
	}
	if action.SOAP.BodyMode != "raw_xml" {
		t.Fatalf("body mode = %q, want raw_xml", action.SOAP.BodyMode)
	}
	if action.Target != "/services/quotes" {
		t.Fatalf("target = %q, want /services/quotes", action.Target)
	}
}

func TestConvertBatchRawHTTP(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "raw_http",
		Content: strings.Join([]string{
			"POST /v1/session HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\n\n{\"username\":\"demo\",\"password\":\"secret\"}",
			"GET /v1/session/session-12345/orders HTTP/1.1\nHost: api.example.com\nAccept: application/json",
		}, rawHTTPRequestDelimiter),
		ResponseContent: strings.Join([]string{
			"HTTP/1.1 201 Created\nContent-Type: application/json\n\n{\"sessionId\":\"session-12345\"}",
			"HTTP/1.1 200 OK\nContent-Type: application/json\n\n{\"orders\":[]}",
		}, rawHTTPResponseDelimiter),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[1].Target != "/v1/session/{{var.corr_sessionid}}/orders" {
		t.Fatalf("second target = %q, want correlated target", result.Script.Actions[1].Target)
	}
	if result.Stats.Assertions != 2 {
		t.Fatalf("assertions = %d, want 2", result.Stats.Assertions)
	}
	if result.Stats.CorrelationsDetected != 1 {
		t.Fatalf("correlations = %d, want 1", result.Stats.CorrelationsDetected)
	}
}

func TestConvertHARWithThinkTime(t *testing.T) {
	result, err := Convert(ImportRequest{
		Format: "har",
		Content: `{
			"log": {
				"entries": [
					{
						"startedDateTime": "2026-05-17T10:00:00.000Z",
						"time": 200,
						"request": {
							"method": "GET",
							"url": "https://api.example.com/v1/session",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					},
					{
						"startedDateTime": "2026-05-17T10:00:03.700Z",
						"time": 150,
						"request": {
							"method": "GET",
							"url": "https://api.example.com/v1/orders",
							"headers": []
						},
						"response": {
							"status": 200,
							"headers": [],
							"content": {"text": "{}"}
						}
					}
				]
			}
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	first := result.Script.Actions[0]
	if !first.ThinkTimeAfter {
		t.Fatal("expected first HAR action to preserve think time after the request")
	}
	if first.ThinkTime.Type != "fixed" || first.ThinkTime.MeanMs != 3500 {
		t.Fatalf("first action think time = %#v, want fixed 3500ms", first.ThinkTime)
	}
	if result.Script.Actions[1].ThinkTimeAfter {
		t.Fatal("expected final HAR action to omit trailing think time")
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "think time") {
		t.Fatalf("warnings = %#v, want think time guidance", result.Warnings)
	}
}
