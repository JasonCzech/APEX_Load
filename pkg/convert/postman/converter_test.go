package postman

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRawURLPreservesStructuredPortAndHash(t *testing.T) {
	got := rawURL(postmanURL{
		Protocol: "http",
		Host:     []string{"localhost"},
		Port:     "8080",
		Path:     []string{"api", "v1"},
		Hash:     "results",
	})
	if got != "http://localhost:8080/api/v1#results" {
		t.Fatalf("rawURL = %q, want structured port and hash", got)
	}
}

func TestPostmanLiteralAssignmentsApplyAtTheirSourcePosition(t *testing.T) {
	vars := newPostmanScopedVariables(map[string]string{"stage": "initial"}, nil)
	request := requestItem{
		Name: "one",
		Events: []event{
			{Listen: "prerequest", Script: eventScript{Exec: []string{`pm.environment.set("stage", "before")`}}},
			{Listen: "test", Script: eventScript{Exec: []string{`pm.environment.set("stage", "after")`}}},
		},
	}
	var diagnostics diag.Collector
	applyPostmanScopedEventAssignmentsForListen(vars, request, "prerequest", &diagnostics)
	values, _ := vars.effectiveValues()
	if values["stage"] != "before" {
		t.Fatalf("pre-request value = %q", values["stage"])
	}
	applyPostmanScopedEventAssignmentsForListen(vars, request, "test", &diagnostics)
	values, _ = vars.effectiveValues()
	if values["stage"] != "after" {
		t.Fatalf("post-response value = %q", values["stage"])
	}
}

func TestConvertPostmanLiteralAssignmentsMaterializeAtSourcePosition(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Ordered assignments","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"},{"key":"stage","value":"initial"}],
  "item":[
    {"name":"First","event":[
      {"listen":"prerequest","script":{"exec":["pm.environment.set(\"stage\", \"before\")"]}},
      {"listen":"test","script":{"exec":["pm.environment.set(\"stage\", \"after\")"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/one/{{stage}}"}},
    {"name":"Second","request":{"method":"GET","url":"{{baseUrl}}/two/{{stage}}"}}
  ]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/one/before" {
		t.Fatalf("first target = %q, want source-position value", got)
	}
	if got := result.Script.Actions[1].Target; got != "/two/after" {
		t.Fatalf("second target = %q, want post-response value", got)
	}
	for _, parameter := range result.Script.Parameters {
		if parameter.Name == "stage" {
			t.Fatalf("mutable literal was retained as one global parameter: %#v", parameter)
		}
	}
}

func TestConvertPostmanVariableStoresHonorScopePrecedence(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "info":{"name":"Scoped assignments","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"},{"key":"stage","value":"collection"}],
  "item":[
    {"name":"First","event":[
      {"listen":"prerequest","script":{"exec":["pm.collectionVariables.set(\"stage\", \"lower-write\")"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/one/{{stage}}"}},
    {"name":"Second","request":{"method":"GET","url":"{{baseUrl}}/two/{{stage}}"}}
  ]
}`,
		EnvironmentContent: `{"name":"selected","values":[{"key":"stage","value":"environment","enabled":true}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/one/{{var.stage}}" {
		t.Fatalf("first target = %q, want stable higher-scope environment parameter", got)
	}
	if got := result.Script.Actions[1].Target; got != "/two/{{var.stage}}" {
		t.Fatalf("second target = %q, lower-scope write escaped precedence", got)
	}
	if !postmanParameterNames(result, "stage") {
		t.Fatalf("shadowed lower-scope write removed stable environment parameter: %#v", result.Script.Parameters)
	}
}

func TestConvertPostmanLocalVariableDoesNotLeakIntoNextRequest(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Local lifetime","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"},{"key":"stage","value":"collection"}],
  "item":[
    {"name":"First","event":[
      {"listen":"prerequest","script":{"exec":["pm.variables.set(\"stage\", \"request-local\")"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/one/{{stage}}"}},
    {"name":"Second","request":{"method":"GET","url":"{{baseUrl}}/two/{{stage}}"}}
  ]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/one/request-local" {
		t.Fatalf("first target = %q, want request-local value", got)
	}
	if got := result.Script.Actions[1].Target; got != "/two/collection" {
		t.Fatalf("second target = %q, local value leaked past its request", got)
	}
}

func TestConvertPostmanGlobalLiteralPersistsAcrossRequests(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Global lifetime","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"}],
  "item":[
    {"name":"First","event":[
      {"listen":"prerequest","script":{"exec":["pm.globals.set(\"region\", \"global-value\")"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/one/{{region}}"}},
    {"name":"Second","request":{"method":"GET","url":"{{baseUrl}}/two/{{region}}"}}
  ]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/one/global-value" {
		t.Fatalf("first target = %q, want global value", got)
	}
	if got := result.Script.Actions[1].Target; got != "/two/global-value" {
		t.Fatalf("second target = %q, global value did not persist", got)
	}
}

func TestConvertPostmanMixedLiteralAndResponseAssignmentsPreserveChaining(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Mixed assignments","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"},{"key":"stage","value":"initial"}],
  "item":[
    {"name":"Capture","event":[
      {"listen":"prerequest","script":{"exec":["pm.environment.set(\"stage\", \"bootstrap\")"]}},
      {"listen":"test","script":{"exec":["pm.environment.set(\"stage\", pm.response.json().stage)"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/capture/{{stage}}"}},
    {"name":"Consume","request":{"method":"GET","url":"{{baseUrl}}/consume/{{stage}}"}},
    {"name":"Override","event":[
      {"listen":"prerequest","script":{"exec":["pm.variables.set(\"stage\", \"manual\")"]}}
    ],"request":{"method":"GET","url":"{{baseUrl}}/override/{{stage}}"}}
  ]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/capture/bootstrap" {
		t.Fatalf("literal phase target = %q", got)
	}
	if got := result.Script.Actions[1].Target; got != "/consume/{{var.stage}}" {
		t.Fatalf("response-derived phase target = %q, want runtime variable", got)
	}
	if got := result.Script.Actions[2].Target; got != "/override/manual" {
		t.Fatalf("later literal override target = %q", got)
	}
	if len(result.Script.Actions[0].Extractors) != 1 || result.Script.Actions[0].Extractors[0].Name != "stage" {
		t.Fatalf("response extractor was lost: %#v", result.Script.Actions[0].Extractors)
	}
	foundParameter := false
	for _, parameter := range result.Script.Parameters {
		foundParameter = foundParameter || parameter.Name == "stage"
	}
	if !foundParameter {
		t.Fatal("mixed-provenance variable lost its initial parameter fallback")
	}
}

func TestConvertPostmanLiteralAssignmentsMaterializeColonPathVariables(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Colon assignment","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable":[{"key":"baseUrl","value":"https://api.example.test"},{"key":"stage","value":"initial"}],
  "item":[{"name":"Request","event":[{"listen":"prerequest","script":{"exec":["pm.environment.set(\"stage\", \"before\")"]}}],"request":{"method":"GET","url":"{{baseUrl}}/jobs/:stage"}}]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Actions[0].Target; got != "/jobs/before" {
		t.Fatalf("target = %q, want inlined colon path assignment", got)
	}
	for _, parameter := range result.Script.Parameters {
		if parameter.Name == "stage" {
			t.Fatalf("mutable colon path variable retained as global parameter: %#v", parameter)
		}
	}
}

func TestToTargetPreservesSameOriginFragment(t *testing.T) {
	got := toTarget("https://api.example.test/orders?view=full#result", "https://api.example.test", postmanVariableLookup{}, nil, "")
	if got != "/orders?view=full#result" {
		t.Fatalf("target = %q, want query and fragment", got)
	}
}

func TestConvertPostmanCollection(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {
		    "name": "Orders API",
		    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
		  },
		  "variable": [
		    {"key": "baseUrl", "value": "https://api.example.com"},
		    {"key": "order_id", "value": "12345"}
		  ],
		  "item": [
		    {
		      "name": "Orders",
		      "item": [
		        {
		          "name": "Get Order",
		          "request": {
		            "method": "GET",
		            "header": [{"key": "X-Trace", "value": "{{trace_id}}"}],
		            "url": "{{baseUrl}}/orders/{{order_id}}?include=lines"
		          }
		        },
		        {
		          "name": "Create Order",
		          "request": {
		            "method": "POST",
		            "header": [{"key": "Authorization", "value": "Bearer secret-token"}],
		            "url": {
		              "raw": "{{baseUrl}}/orders",
		              "host": ["{{baseUrl}}"],
		              "path": ["orders"]
		            },
		            "body": {
		              "mode": "raw",
		              "raw": "{\"customer\":\"{{customer_id}}\",\"password\":\"hidden\"}"
		            }
		          }
		        }
		      ]
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Name != "Orders_API" {
		t.Fatalf("script name = %q, want Orders_API", result.Script.Name)
	}
	if result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	first := result.Script.Actions[0]
	if first.Name != "Orders_Get_Order" || first.Method != "GET" || first.Target != "/orders/{{var.order_id}}?include=lines" {
		t.Fatalf("first action = %#v", first)
	}
	if len(first.Assertions) != 1 || first.Assertions[0].Type != "status_code_range" || first.Assertions[0].Min != 200 || first.Assertions[0].Max != 299 {
		t.Fatalf("default assertion = %#v", first.Assertions)
	}
	if first.Headers["X-Trace"] != "{{var.trace_id}}" {
		t.Fatalf("trace header = %q", first.Headers["X-Trace"])
	}
	second := result.Script.Actions[1]
	if second.Headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("authorization header = %q, want imported executable value", second.Headers["Authorization"])
	}
	if !strings.Contains(second.Body, "{{var.customer_id}}") ||
		!strings.Contains(second.Body, `"password":"hidden"`) {
		t.Fatalf("second body lost imported request semantics: %s", second.Body)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "order_id" {
		t.Fatalf("parameters = %#v, want order_id only", result.Script.Parameters)
	}
	if len(result.Script.DataSources) != 1 || result.Script.Parameters[0].DataSource != "postman" || !strings.Contains(result.Script.DataSources[0].InlineData, "order_id") {
		t.Fatalf("postman parameter data source = %#v / %#v", result.Script.Parameters, result.Script.DataSources)
	}
	if result.Stats.Folders != 1 || result.Stats.ConvertedActions != 2 || result.Stats.VariablesDetected != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil || result.Script.Metadata.Import.Source != "postman" {
		t.Fatalf("missing postman import metadata: %#v", result.Script.Metadata)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) != 2 {
		t.Fatalf("source review = %#v, want source file and two action mappings", result.SourceReview)
	}
	if strings.Contains(result.SourceReview.SourceFiles[0].Content, "secret-token") {
		t.Fatalf("source review leaked bearer token: %s", result.SourceReview.SourceFiles[0].Content)
	}
}

func TestConvertPostmanFormAndGraphQLBodies(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Mixed Bodies"},
		  "item": [
		    {
		      "name": "Search",
		      "request": {
		        "method": "POST",
		        "url": "https://api.example.com/search",
		        "body": {"mode": "urlencoded", "urlencoded": [{"key": "q", "value": "widgets"}]}
		      }
		    },
		    {
		      "name": "GraphQL Query",
		      "request": {
		        "method": "POST",
		        "url": "https://api.example.com/graphql",
		        "body": {"mode": "graphql", "graphql": {"query": "query Viewer { viewer { id } }", "variables": "{\"limit\": 10}"}}
		      }
		    }
		  ]
		}`,
		Options: ConvertOptions{BaseURL: "https://api.example.com"},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Actions[0].Body != "q=widgets" {
		t.Fatalf("urlencoded body = %q", result.Script.Actions[0].Body)
	}
	if result.Script.Actions[0].Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("urlencoded content type = %q", result.Script.Actions[0].Headers["Content-Type"])
	}
	if !strings.Contains(result.Script.Actions[1].Body, "Viewer") || result.Script.Actions[1].Headers["Content-Type"] != "application/json" {
		t.Fatalf("graphql action = %#v", result.Script.Actions[1])
	}
}

func TestConvertPostmanGraphQLVariablesPreserveRawJSONAndBlockDuplicateKeys(t *testing.T) {
	const variables = `{"role":"first","role":"second","count":1e+02}`
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "GraphQL duplicate variables"},
		  "item": [{
		    "name": "Viewer",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/graphql",
		      "body": {
		        "mode": "graphql",
		        "graphql": {
		          "query": "query Viewer { viewer { id } }",
		          "variables": "{\"role\":\"first\",\"role\":\"second\",\"count\":1e+02}"
		        }
		      }
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	wantBody := `{"query":"query Viewer { viewer { id } }","variables":` + variables + `}`
	if action.Body != wantBody {
		t.Fatalf("GraphQL body = %q, want byte-preserved variables in %q", action.Body, wantBody)
	}
	if action.Metadata["execution_blocker"] != "credential_json_duplicate_key" {
		t.Fatalf("duplicate GraphQL variables were not gated: %#v", action.Metadata)
	}
	if result.finalization == nil || result.finalization.Result == nil ||
		result.finalization.Result.Disposition != requestimport.BlockedDraft {
		t.Fatalf("duplicate GraphQL variables disposition = %#v", result.finalization)
	}
	if !postmanReportHasFinding(result.finalization.Reports[0], "credential_json_duplicate_key") {
		t.Fatalf("duplicate-key finding = %#v", result.finalization.Reports[0].Findings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked GraphQL draft is invalid: %v", issues)
	}
}

func TestConvertPostmanGraphQLMalformedVariablesAreNotStringified(t *testing.T) {
	const malformedVariables = `{"count":1`
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "GraphQL malformed variables"},
		  "item": [{
		    "name": "Viewer",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/graphql",
		      "body": {
		        "mode": "graphql",
		        "graphql": {
		          "query": "query Viewer { viewer { id } }",
		          "variables": "{\"count\":1"
		        }
		      }
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	wantBody := `{"query":"query Viewer { viewer { id } }","variables":` + malformedVariables + `}`
	if action.Body != wantBody {
		t.Fatalf("malformed GraphQL body = %q, want %q", action.Body, wantBody)
	}
	if strings.Contains(action.Body, `"variables":"`) {
		t.Fatalf("malformed GraphQL variables were silently stringified: %q", action.Body)
	}
	if action.Metadata["execution_blocker"] != "credential_json_malformed" ||
		result.finalization.Result.Disposition != requestimport.BlockedDraft {
		t.Fatalf("malformed GraphQL variables were not a blocked draft: %#v / %#v", action.Metadata, result.finalization)
	}
	if !postmanReportHasFinding(result.finalization.Reports[0], "credential_json_malformed") {
		t.Fatalf("malformed-JSON finding = %#v", result.finalization.Reports[0].Findings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked malformed GraphQL draft is invalid: %v", issues)
	}
}

func TestConvertPostmanBlocksMalformedCredentialBearingGraphQLVariables(t *testing.T) {
	const canary = "postman-graphql-malformed-secret-4b9e"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "GraphQL malformed credential variables"},
		  "item": [{
		    "name": "Login",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/graphql",
		      "body": {
		        "mode": "graphql",
		        "graphql": {
		          "query": "mutation Login { login { ok } }",
		          "variables": "{\"password\":\"` + canary + `"
		        }
		      }
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Body, canary) ||
		action.Metadata["execution_blocker"] != "credential_json_malformed" ||
		result.finalization == nil ||
		result.finalization.Result == nil ||
		result.finalization.Result.Disposition != requestimport.BlockedDraft {
		t.Fatalf("malformed credential GraphQL draft = %#v / %#v", action, result.finalization)
	}
	if !postmanReportHasFinding(result.finalization.Reports[0], "credential_json_malformed") {
		t.Fatalf("malformed-JSON finding = %#v", result.finalization.Reports[0].Findings)
	}
	if result.SourceReview == nil ||
		strings.Contains(result.SourceReview.SourceFiles[0].Content, canary) {
		t.Fatalf("source review disclosed malformed credential: %#v", result.SourceReview)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked malformed credential draft is invalid: %v", issues)
	}
}

func TestConvertPostmanAuthDynamicVariablesAndPathVariables(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Auth Collection"},
		  "auth": {
		    "type": "digest",
		    "digest": [
		      {"key": "username", "value": "digest-user"},
		      {"key": "password", "value": "{{digest_password}}"}
		    ]
		  },
		  "item": [
		    {
		      "name": "Get Order",
		      "request": {
		        "method": "GET",
		        "url": {
		          "raw": "https://api.example.com/orders/:orderId?requestId={{$guid}}&ts={{$timestamp}}",
		          "variable": [{"key": "orderId", "value": "A-100"}]
		        },
		        "header": [{"key": "X-Request-Id", "value": "{{$guid}}"}]
		      }
		    },
		    {
		      "name": "Bearer Request",
		      "request": {
		        "method": "POST",
		        "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}"}]},
		        "url": "https://api.example.com/orders",
		        "body": {"mode": "raw", "raw": "{\"id\":\"{{$guid}}\",\"created\":\"{{$isoTimestamp}}\",\"shard\":\"{{$randomInt}}\"}"}
		      }
		    },
		    {
		      "name": "Signed Request",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "awsv4", "awsv4": [{"key": "accessKey", "value": "AKIA"}]},
		        "url": "https://api.example.com/signed"
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.HTTP.Auth.Type != "" {
		t.Fatalf("collection auth should remain action-scoped: %#v", result.Script.Config.HTTP.Auth)
	}
	first := result.Script.Actions[0]
	if first.Target != "/orders/{{var.orderId}}?requestId={{fn.uuid()}}&ts={{fn.timestamp.unix}}" {
		t.Fatalf("target = %q", first.Target)
	}
	if first.Auth == nil || first.Auth.Type != "digest" || first.Auth.Username != "digest-user" || first.Auth.Password != "{{var.digest_password}}" {
		t.Fatalf("digest collection auth = %#v, want native action-scoped digest auth", first.Auth)
	}
	if first.Headers["X-Request-Id"] != "{{fn.uuid()}}" {
		t.Fatalf("dynamic header = %q", first.Headers["X-Request-Id"])
	}
	if !postmanParameterNames(result, "orderId") {
		t.Fatalf("parameters = %#v, want orderId path variable", result.Script.Parameters)
	}
	second := result.Script.Actions[1]
	if second.Auth == nil || second.Auth.Type != "bearer" || second.Auth.Token != "{{var.token}}" {
		t.Fatalf("request auth = %#v", second.Auth)
	}
	if !strings.Contains(second.Body, "{{fn.uuid()}}") || !strings.Contains(second.Body, "{{fn.timestamp.iso8601}}") || !strings.Contains(second.Body, "{{fn.random(0,1000)}}") {
		t.Fatalf("dynamic body = %s", second.Body)
	}
	if !containsPostmanDiagnostic(result, "Postman auth") {
		t.Fatalf("diagnostics = %#v, want unsupported signing auth warning", result.Unsupported)
	}
	if got := result.Script.Actions[2].Metadata["execution_blocker"]; got != "import_review_required" {
		t.Fatalf("unsupported signing auth blocker = %#v", got)
	}
	signed := result.Script.Actions[2]
	if signed.Auth == nil || signed.Auth.Type != "awsv4" {
		t.Fatalf("unsupported signing auth = %#v, want exact source mode retained", signed.Auth)
	}
	fields, ok := signed.Metadata["postman_auth_source_fields"].([]map[string]any)
	if !ok || len(fields) != 1 ||
		fields[0]["name"] != "accessKey" ||
		fields[0]["value"] != "AKIA" {
		t.Fatalf("unsupported signing fields = %#v, want source access key retained", signed.Metadata["postman_auth_source_fields"])
	}
}

func TestConvertPostmanAuthPreservesAuthoredWhitespace(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Whitespace credentials"},
		  "item": [
		    {
		      "name": "Bearer",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "bearer", "bearer": [
		          {"key": "token", "value": "  bearer token  "}
		        ]},
		        "url": "https://api.example.com/bearer"
		      }
		    },
		    {
		      "name": "Basic",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "basic", "basic": [
		          {"key": "username", "value": " operator "},
		          {"key": "password", "value": "  "}
		        ]},
		        "url": "https://api.example.com/basic"
		      }
		    },
		    {
		      "name": "Digest",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "digest", "digest": [
		          {"key": "username", "value": " digest-user "},
		          {"key": "password", "value": " digest-password "}
		        ]},
		        "url": "https://api.example.com/digest"
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Auth; got == nil || got.Token != "  bearer token  " {
		t.Fatalf("bearer auth = %#v, want byte-exact authored token", got)
	}
	if got := result.Script.Actions[1].Auth; got == nil ||
		got.Username != " operator " ||
		got.Password != "  " {
		t.Fatalf("basic auth = %#v, want byte-exact authored fields", got)
	}
	if got := result.Script.Actions[2].Auth; got == nil ||
		got.Username != " digest-user " ||
		got.Password != " digest-password " {
		t.Fatalf("digest auth = %#v, want byte-exact authored fields", got)
	}
}

func TestConvertPostmanNoAuthOverridesCollectionAuth(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Auth Override"},
		  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "collection-secret"}]},
		  "item": [
		    {"name": "Inherit", "request": {"method": "GET", "url": "https://api.example.com/private"}},
		    {"name": "Public", "request": {"method": "GET", "auth": {"type": "noauth"}, "url": "https://api.example.com/public"}}
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.HTTP.Auth.Type != "" {
		t.Fatalf("global auth = %#v, want none", result.Script.Config.HTTP.Auth)
	}
	if got := result.Script.Actions[0].Auth; got == nil || got.Type != "bearer" || got.Token != "collection-secret" {
		t.Fatalf("inherited auth = %#v, want imported bearer action auth", got)
	}
	if got := result.Script.Actions[1].Auth; got == nil || got.Type != "noauth" {
		t.Fatalf("explicit noauth = %#v, want noauth action override", got)
	}
}

func TestConvertPostmanAPIKeyAuth(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "API Keys"},
		  "item": [
		    {
		      "name": "Header Key",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "apikey", "apikey": [
		          {"key": "key", "value": "X-API-Key"},
		          {"key": "value", "value": "super-secret"},
		          {"key": "in", "value": "header"}
		        ]},
		        "url": "https://api.example.com/header"
		      }
		    },
		    {
		      "name": "Query Key",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "apikey", "apikey": [
		          {"key": "key", "value": "api_key"},
		          {"key": "value", "value": "{{apiToken}}"},
		          {"key": "in", "value": "query"}
		        ]},
		        "url": "https://api.example.com/query?existing=1"
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Headers["X-API-Key"]; got != "super-secret" {
		t.Fatalf("header API key = %q, want imported executable value", got)
	}
	if got := result.Script.Actions[1].Target; got != "/query?existing=1&api_key={{var.apiToken}}" {
		t.Fatalf("query API key expression changed: %q", got)
	}
}

func TestConvertPostmanAPIKeyMissingOrUnsupportedPlacementIsBlockedAndRetained(t *testing.T) {
	const (
		missingPlacementCanary     = "postman-apikey-missing-placement-canary"
		unsupportedPlacementCanary = "postman-apikey-unsupported-placement-canary"
	)
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "API Key Review"},
		  "item": [
		    {
		      "name": "Missing Placement",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "apikey", "apikey": [
		          {"key": "key", "value": "X-Missing-Placement"},
		          {"key": "value", "value": "` + missingPlacementCanary + `"}
		        ]},
		        "url": "https://api.example.com/missing-placement"
		      }
		    },
		    {
		      "name": "Unsupported Placement",
		      "request": {
		        "method": "GET",
		        "auth": {"type": "apikey", "apikey": [
		          {"key": "key", "value": "X-Unsupported-Placement"},
		          {"key": "value", "value": "` + unsupportedPlacementCanary + `"},
		          {"key": "in", "value": "body"}
		        ]},
		        "url": "https://api.example.com/unsupported-placement"
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked API-key draft is invalid: %v", issues)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	for index, action := range result.Script.Actions {
		if action.Headers["X-Missing-Placement"] != "" ||
			action.Headers["X-Unsupported-Placement"] != "" {
			t.Fatalf("action %d invented an API-key header: %#v", index, action.Headers)
		}
		wantToken := []string{missingPlacementCanary, unsupportedPlacementCanary}[index]
		if action.Auth == nil ||
			action.Auth.Type != "postman_apikey" ||
			action.Auth.Token != wantToken {
			t.Fatalf("action %d auth = %#v, want exact source credential retained behind gate", index, action.Auth)
		}
		if action.Metadata["execution_blocker"] != "import_review_required" {
			t.Fatalf("action %d blocker = %#v", index, action.Metadata)
		}
		review, ok := action.Metadata["import_review"].(map[string]any)
		if !ok {
			t.Fatalf("action %d import review = %#v", index, action.Metadata["import_review"])
		}
		reasons, ok := review["reason_codes"].([]string)
		if !ok || !containsTestString(reasons, "unsupported_auth") {
			t.Fatalf("action %d review reasons = %#v", index, review["reason_codes"])
		}
	}
	if result.Script.Actions[0].Target != "/missing-placement" ||
		result.Script.Actions[1].Target != "/unsupported-placement" {
		t.Fatalf("API-key review targets changed: %#v", result.Script.Actions)
	}
	protected := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(protected)
	if len(protected) != 0 {
		t.Fatalf("protected requirements = %#v, blocked review must not create reentry bindings", protected)
	}
	destinationJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{missingPlacementCanary, unsupportedPlacementCanary} {
		if !strings.Contains(string(destinationJSON), canary) {
			t.Fatalf("destination blocked draft removed %q: %s", canary, destinationJSON)
		}
	}
}

func TestConvertPostmanUnknownAuthRetainsEverySourceFieldBehindGate(t *testing.T) {
	const password = "unknown-auth-password-canary"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Unknown Auth"},
		  "item": [{
		    "name": "Custom Auth",
		    "request": {
		      "method": "GET",
		      "auth": {"type": "customscheme", "customscheme": [
		        {"key": "account", "value": "operator"},
		        {"key": "password", "value": "` + password + `"},
		        {"key": "realm", "value": "example"}
		      ]},
		      "url": "https://api.example.com/custom"
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Auth == nil || action.Auth.Type != "customscheme" {
		t.Fatalf("auth = %#v, want exact unknown source mode", action.Auth)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("metadata = %#v, want durable review gate", action.Metadata)
	}
	fields, ok := action.Metadata["postman_auth_source_fields"].([]map[string]any)
	if !ok || len(fields) != 3 {
		t.Fatalf("source fields = %#v, want all authored entries", action.Metadata["postman_auth_source_fields"])
	}
	if fields[1]["name"] != "password" || fields[1]["value"] != password {
		t.Fatalf("password field = %#v, want exact source credential", fields[1])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked destination is invalid: %v", issues)
	}
}

func TestConvertPostmanEnvironmentAndParameterDataSource(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Environment Import"},
		  "variable": [
		    {"key": "baseUrl", "value": "https://api.example.com"},
		    {"key": "region", "value": "east"}
		  ],
		  "item": [
		    {"name": "Region", "request": {"method": "GET", "url": "{{baseUrl}}/regions/{{region}}"}}
		  ]
		}`,
		EnvironmentContent: `{
		  "name": "Local",
		  "values": [
		    {"key": "baseUrl", "value": "https://local.example.test", "enabled": true},
		    {"key": "region", "value": "west", "enabled": true},
		    {"key": "ignored", "value": "nope", "enabled": false}
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://local.example.test" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	if result.Stats.EnvironmentValues != 2 {
		t.Fatalf("environment values = %d, want 2", result.Stats.EnvironmentValues)
	}
	if len(result.Script.DataSources) != 1 || !strings.Contains(result.Script.DataSources[0].InlineData, "region") || !strings.Contains(result.Script.DataSources[0].InlineData, "west") {
		t.Fatalf("data source = %#v", result.Script.DataSources)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "region" || result.Script.Parameters[0].DataSource != "postman" {
		t.Fatalf("parameters = %#v", result.Script.Parameters)
	}
}

func TestConvertPostmanResolvesAcyclicVariableDependenciesWithEnvironmentPrecedence(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Dependent Variables"},
		  "variable": [
		    {"key": "baseUrl", "value": "https://api.example.test"},
		    {"key": "region", "value": "collection"},
		    {"key": "tenant", "value": "{{region}}"},
		    {"key": "label", "value": "tenant-{{tenant}}"},
		    {"key": "request_id", "value": "request-{{$guid}}"}
		  ],
		  "item": [{
		    "name": "Resolve",
		    "request": {
		      "method": "GET",
		      "url": "{{baseUrl}}/accounts/{{tenant}}?label={{label}}&request_id={{request_id}}"
		    }
		  }]
		}`,
		EnvironmentContent: `{
		  "name": "Local",
		  "values": [{"key": "region", "value": "environment", "enabled": true}]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Target != "/accounts/{{var.tenant}}?label={{var.label}}&request_id=request-{{fn.uuid()}}" {
		t.Fatalf("resolved target = %q", action.Target)
	}
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("resolved acyclic dependencies were gated: %#v", action.Metadata)
	}
	if got, ok := postmanParameterValue(result, "tenant"); !ok || got != "environment" {
		t.Fatalf("tenant parameter = %q/%t, want resolved environment value", got, ok)
	}
	if got, ok := postmanParameterValue(result, "label"); !ok || got != "tenant-environment" {
		t.Fatalf("label parameter = %q/%t, want transitively resolved value", got, ok)
	}
	if len(result.Script.DataSources) != 1 ||
		!strings.Contains(result.Script.DataSources[0].InlineData, "region") ||
		strings.Contains(result.Script.DataSources[0].InlineData, "tenant") ||
		strings.Contains(result.Script.DataSources[0].InlineData, "label") {
		t.Fatalf("dependent variables changed generated data-source columns: %#v", result.Script.DataSources)
	}
}

func TestConvertPostmanResolvesDependencyReferencesByUniqueNormalizedName(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Normalized Dependencies"},
		  "variable": [
		    {"key": "Region.Name", "value": "east"},
		    {"key": "Tenant-Path", "value": "{{REGION_name}}"}
		  ],
		  "item": [{
		    "name": "Resolve",
		    "request": {
		      "method": "GET",
		      "url": "https://api.example.test/accounts/{{tenant_path}}"
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Target != "/accounts/{{var.Tenant_Path}}" {
		t.Fatalf("normalized target = %q", action.Target)
	}
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("unique normalized dependency was gated: %#v", action.Metadata)
	}
	if got, ok := postmanParameterValue(result, "Tenant_Path"); !ok || got != "east" {
		t.Fatalf("normalized dependent parameter = %q/%t, want east", got, ok)
	}
}

func TestConvertPostmanBlocksMissingCyclicAndShadowedVariableBindings(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Unsafe Bindings"},
		  "variable": [
		    {"key": "missing_root", "value": "{{absent}}"},
		    {"key": "cycle_a", "value": "{{cycle_b}}"},
		    {"key": "cycle_b", "value": "{{cycle_a}}"},
		    {"key": "foo-bar", "value": "shadow-canary"},
		    {"key": "foo_bar", "value": "second"}
		  ],
		  "item": [
		    {"name": "Missing", "request": {"method": "GET", "url": "https://api.example.test/missing/{{missing_root}}"}},
		    {"name": "Cycle", "request": {"method": "GET", "url": "https://api.example.test/cycle/{{cycle_a}}"}},
		    {"name": "Shadow", "request": {"method": "GET", "url": "https://api.example.test/shadow/{{foo-bar}}"}}
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft is not DSL-valid: %v", issues)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(result.Script.Actions))
	}
	wantMissing := []string{"missing_root", "cycle_a", "foo_bar"}
	for index, action := range result.Script.Actions {
		if got := action.Metadata["execution_blocker"]; got != "import_missing_variables" {
			t.Fatalf("action %d blocker = %#v, want import_missing_variables", index, got)
		}
		names, ok := action.Metadata["missing_variables"].([]string)
		if !ok || len(names) != 1 || names[0] != wantMissing[index] {
			t.Fatalf("action %d missing variables = %#v, want %q", index, action.Metadata["missing_variables"], wantMissing[index])
		}
	}
	for _, invalid := range []string{"missing_root", "cycle_a", "cycle_b", "foo_bar"} {
		if postmanParameterNames(result, invalid) {
			t.Fatalf("invalid binding %q remained a runnable parameter: %#v", invalid, result.Script.Parameters)
		}
	}
	for _, diagnostic := range []string{"Postman variable dependency", "Postman variable cycle", "Postman variable shadowing"} {
		if !containsPostmanDiagnostic(result, diagnostic) {
			t.Fatalf("diagnostics = %#v, want %s", result.Unsupported, diagnostic)
		}
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "shadow-canary") {
		t.Fatalf("public blocked draft disclosed an invalid source value: %s", publicJSON)
	}
}

func TestConvertPostmanGraphLimitGatesEveryRequestAndOmitsRawSource(t *testing.T) {
	const canary = "postman-graph-limit-canary"
	longName := strings.Repeat("n", requestimport.DefaultBindingDependencyLimits().MaxNameBytes+1)
	result, err := Convert(ImportRequest{
		Content: fmt.Sprintf(`{
		  "info": {"name": "Graph Limit"},
		  "variable": [{"key": %q, "value": %q}],
		  "item": [
		    {"name": "Constant One", "request": {"method": "GET", "url": "https://api.example.test/one"}},
		    {"name": "Constant Two", "request": {"method": "GET", "url": "https://api.example.test/two"}}
		  ]
		}`, longName, canary),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("graph-limit blocked draft is not DSL-valid: %v", issues)
	}
	for index, action := range result.Script.Actions {
		if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
			t.Fatalf("action %d blocker = %#v", index, got)
		}
		review, ok := action.Metadata["import_review"].(map[string]any)
		if !ok || !containsTestString(review["reason_codes"].([]string), "source_variable_graph_limit") {
			t.Fatalf("action %d review = %#v", index, action.Metadata["import_review"])
		}
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), canary) || strings.Contains(string(publicJSON), longName) {
		t.Fatalf("graph-limit public result disclosed omitted source: %s", publicJSON)
	}
	if !containsPostmanDiagnostic(result, "Postman variable dependency limit") {
		t.Fatalf("graph-limit diagnostics = %#v", result.Unsupported)
	}
}

func TestConvertPostmanStructuredURLPreservesTemplatesAndDisabledQuery(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Structured URL"},
		  "variable": [{"key": "baseUrl", "value": "https://api.example.com"}],
		  "item": [
		    {
		      "name": "Search",
		      "request": {
		        "method": "GET",
		        "url": {
		          "raw": "{{baseUrl}}/search/:account-id?q=old&skip=old",
		          "query": [
		            {"key": "q", "value": "{{term}}"},
		            {"key": "page", "value": "{{page}}"},
		            {"key": "skip", "value": "1", "disabled": true}
		          ],
		          "variable": [{"key": "account-id", "value": "acct-1"}]
		        }
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Target; got != "/search/{{var.account_id}}?q={{var.term}}&page={{var.page}}" {
		t.Fatalf("target = %q", got)
	}
	if !postmanParameterNames(result, "account_id") {
		t.Fatalf("parameters = %#v, want normalized path variable", result.Script.Parameters)
	}
}

func TestConvertPostmanRetainsCrossOriginAbsoluteTargets(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Multiple Origins"},
		  "item": [
		    {
		      "name": "Primary",
		      "request": {
		        "method": "GET",
		        "url": "https://primary.example.test/v1/orders"
		      }
		    },
		    {
		      "name": "Secondary",
		      "request": {
		        "method": "GET",
		        "url": "https://secondary.example.test/v1/orders?state=open"
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://primary.example.test" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	if got := result.Script.Actions[0].Target; got != "/v1/orders" {
		t.Fatalf("same-origin target = %q", got)
	}
	if got := result.Script.Actions[1].Target; got != "https://secondary.example.test/v1/orders?state=open" {
		t.Fatalf("cross-origin target = %q, want absolute target retained", got)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("multi-origin script is invalid: %v", issues)
	}
}

func TestConvertPostmanRejectsEmptyCollection(t *testing.T) {
	if _, err := Convert(ImportRequest{Content: `{"info":{"name":"Empty"},"item":[]}`}); err == nil {
		t.Fatal("Convert returned nil error for empty collection")
	}
}

func TestConvertPostmanReportsEventScriptsAndTranslatesSimpleVariables(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Scripted Collection"},
		  "item": [
		    {
		      "name": "Signed Request",
		      "event": [
		        {
		          "listen": "prerequest",
		          "script": {"type": "text/javascript", "exec": [
		            "pm.environment.set(\"token\", \"abc123\");",
			            "const signature = sign(\"postman-residual-script-canary-a12b\");"
		          ]}
		        },
		        {
		          "listen": "test",
		          "script": {"type": "text/javascript", "exec": [
		            "pm.test(\"ok\", function () { pm.response.to.have.status(200); });"
		          ]}
		        }
		      ],
		      "request": {
		        "method": "GET",
		        "url": "https://api.example.com/orders",
		        "header": [{"key": "Authorization", "value": "Bearer {{token}}"}]
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if postmanParameterNames(result, "token") {
		t.Fatalf("source-position literal was incorrectly retained as one global parameter: %#v", result.Script.Parameters)
	}
	if !containsPostmanDiagnostic(result, "Postman pre-request script") {
		t.Fatalf("diagnostics = %#v, want pre-request warning", result.Unsupported)
	}
	if !containsPostmanDiagnostic(result, "Postman test script") {
		t.Fatalf("diagnostics = %#v, want test script warning", result.Unsupported)
	}
	if !containsPostmanDiagnostic(result, "Postman script variable") {
		t.Fatalf("diagnostics = %#v, want variable translation info", result.Unsupported)
	}
	action := result.Script.Actions[0]
	if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
		t.Fatalf("residual pre-request script blocker = %#v", got)
	}
	if len(action.Assertions) != 1 || action.Assertions[0].Type != "status_code" || action.Assertions[0].Expected != 200 {
		t.Fatalf("translated status assertion = %#v", action.Assertions)
	}
	if !strings.Contains(action.Headers["Authorization"], "abc123") {
		t.Fatalf("destination header lost translated script credential: %q", action.Headers["Authorization"])
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("static script credential requirements = %#v, want no reentry", requirements)
	}
	eventSources, _ := result.Script.Metadata.Import.Preserved["postman_event_sources"].([]map[string]string)
	if len(eventSources) != 1 || !strings.Contains(eventSources[0]["script"], "postman-residual-script-canary-a12b") {
		t.Fatalf("durable destination omitted residual event source: %#v", eventSources)
	}
	reviewJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("marshal source review: %v", err)
	}
	if strings.Contains(string(reviewJSON), "postman-residual-script-canary-a12b") {
		t.Fatalf("public source review disclosed residual script source: %s", reviewJSON)
	}
}

func TestConvertPostmanSafeStatusTestDoesNotRequireReview(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Safe Test"},
		  "item": [{
		    "name": "Created",
		    "event": [{
		      "listen": "test",
		      "script": {"type": "text/javascript", "exec": [
		        "pm.test(\"created\", function () { pm.response.to.have.status(201); });"
		      ]}
		    }],
		    "request": {"method": "POST", "url": "https://api.example.com/orders"}
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("fully translated status test was gated: %#v", action.Metadata)
	}
	if len(action.Assertions) != 1 || action.Assertions[0].Type != "status_code" || action.Assertions[0].Expected != 201 {
		t.Fatalf("translated assertion = %#v", action.Assertions)
	}
}

func postmanParameterNames(result *ConvertResult, name string) bool {
	for _, param := range result.Script.Parameters {
		if param.Name == name {
			return true
		}
	}
	return false
}

func postmanParameterValue(result *ConvertResult, name string) (string, bool) {
	for _, parameter := range result.Script.Parameters {
		if parameter.Name == name && len(parameter.Values) > 0 {
			return parameter.Values[0], true
		}
	}
	return "", false
}

func containsPostmanDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}

func TestAppendQueryParamEncodesLiteralsAndKeepsTemplatesAndFragment(t *testing.T) {
	// Template spans are preserved verbatim so they still resolve at replay.
	if got := appendQueryParam("https://api.test/x", "api_key", "{{env.POSTMAN_API_KEY}}"); got != "https://api.test/x?api_key={{env.POSTMAN_API_KEY}}" {
		t.Fatalf("template value got %q", got)
	}
	// A literal value with reserved characters is percent-encoded, not injected raw.
	got := appendQueryParam("https://api.test/x", "api key", "a b&c=d")
	if got != "https://api.test/x?api+key=a+b%26c%3Dd" {
		t.Fatalf("literal value got %q, want reserved chars encoded", got)
	}
	// An existing fragment is preserved after the appended query param.
	got = appendQueryParam("https://api.test/x#section", "k", "v")
	if got != "https://api.test/x?k=v#section" {
		t.Fatalf("fragment handling got %q", got)
	}
	// Mixed literal + template encodes only the literal portion.
	got = appendQueryParam("https://api.test/x?a=1", "k", "pre {{var.t}} post")
	if got != "https://api.test/x?a=1&k=pre+{{var.t}}+post" {
		t.Fatalf("mixed value got %q", got)
	}
}

func TestConvertPostmanPreservesOrderedDuplicateHeaders(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Ordered Headers"},
		  "item": [{
		    "name": "Duplicates",
		    "request": {
		      "method": "GET",
		      "url": "https://api.example.com/headers",
		      "header": [
		        {"key": "X-Trace", "value": "first"},
		        {"key": "x-trace", "value": "second"},
		        {"key": "X-Dupe", "value": "a=1"},
		        {"key": "X-Dupe", "value": "b=2"}
		      ]
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	got := result.Script.Actions[0].HeaderList
	want := dsl.HeaderList{
		{Name: "X-Trace", Value: "first"},
		{Name: "x-trace", Value: "second"},
		{Name: "X-Dupe", Value: "a=1"},
		{Name: "X-Dupe", Value: "b=2"},
	}
	if len(got) != len(want) {
		t.Fatalf("header list = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("header list[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
	if _, ok := result.Script.Actions[0].Headers["X-Trace"]; ok {
		t.Fatalf("compatibility header map retained superseded casing: %#v", result.Script.Actions[0].Headers)
	}
	if got := result.Script.Actions[0].Headers["x-trace"]; got != "second" {
		t.Fatalf("compatibility x-trace = %q, want last effective occurrence", got)
	}
	if got := result.Script.Actions[0].Headers["X-Dupe"]; got != "b=2" {
		t.Fatalf("compatibility X-Dupe = %q, want last occurrence", got)
	}
}

func TestConvertPostmanRestoresShelteredHeadersAfterMalformedOccurrenceIsRemoved(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Sheltered Header Identity"},
		  "item": [{
		    "name": "Shifted Header",
		    "request": {
		      "method": "GET",
		      "url": "https://api.example.com/headers",
		      "header": [
		        {"key": "Invalid Header", "value": "{{first_dynamic}}"},
		        {"key": "X-Dynamic", "value": "{{second_dynamic}}"}
		      ]
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one blocked draft action", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	want := dsl.HeaderList{{Name: "X-Dynamic", Value: "{{var.second_dynamic}}"}}
	if !reflect.DeepEqual(action.HeaderList, want) {
		t.Fatalf("header list = %#v, want %#v", action.HeaderList, want)
	}
	if got := action.Headers["X-Dynamic"]; got != "{{var.second_dynamic}}" {
		t.Fatalf("compatibility X-Dynamic = %q, want second header template", got)
	}
	if strings.Contains(fmt.Sprintf("%#v", action), "first_dynamic") {
		t.Fatalf("removed malformed header template was transplanted into the retained request: %#v", action)
	}
}

func TestConvertPostmanMultipartPreservesBoundaryBytesOutsideDelimiterLines(t *testing.T) {
	const bodyValue = "prefix--AUTHORED-middle\r\n--AUTHORED-not-a-boundary\r\nsuffix"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Multipart Boundary Bytes"},
		  "item": [{
		    "name": "Upload",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/upload",
		      "header": [{
		        "key": "Content-Type",
		        "value": "multipart/form-data; boundary=AUTHORED"
		      }],
		      "body": {
		        "mode": "formdata",
		        "formdata": [{
		          "key": "description",
		          "value": "prefix--AUTHORED-middle\r\n--AUTHORED-not-a-boundary\r\nsuffix",
		          "type": "text"
		        }]
		      }
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Body, bodyValue) {
		t.Fatalf("multipart body = %q, want source field bytes %q", action.Body, bodyValue)
	}
	if got := action.Headers["Content-Type"]; got != "multipart/form-data; boundary=AUTHORED" {
		t.Fatalf("Content-Type = %q, want authored boundary", got)
	}
}

func TestParsePostmanMultipartAcceptsEmptyMultipart(t *testing.T) {
	multipart, err := parsePostmanMultipart(
		[]byte("--EMPTY--\r\n"),
		"EMPTY",
		requestimport.SourceRef{Format: "postman", Path: "collection.json"},
	)
	if err != nil {
		t.Fatalf("parsePostmanMultipart returned error: %v", err)
	}
	if len(multipart.Parts) != 0 || multipart.Boundary != "EMPTY" {
		t.Fatalf("multipart = %#v, want empty parts with preserved boundary", multipart)
	}
}

func TestConvertPostmanResolvesFolderAuthBeforeRequestProjection(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Folder Auth"},
		  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "collection-secret"}]},
		  "item": [{
		    "name": "Digest Folder",
		    "auth": {"type": "digest", "digest": [
		      {"key": "username", "value": "folder-user"},
		      {"key": "password", "value": "{{digest_password}}"}
		    ]},
		    "item": [
		      {"name": "Inherited", "request": {"method": "GET", "url": "https://api.example.com/private"}},
		      {"name": "Public", "request": {
		        "method": "GET",
		        "auth": {"type": "noauth"},
		        "url": "https://api.example.com/public"
		      }}
		    ]
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Auth; got == nil || got.Type != "digest" || got.Username != "folder-user" || got.Password != "{{var.digest_password}}" {
		t.Fatalf("folder-inherited auth = %#v, want native digest", got)
	}
	if got := result.Script.Actions[1].Auth; got == nil || got.Type != "noauth" {
		t.Fatalf("request noauth override = %#v", got)
	}
	if !containsTestString(result.Stats.AuthSchemes, "digest") || !containsTestString(result.Stats.AuthSchemes, "noauth") {
		t.Fatalf("auth scheme stats = %#v, want folder and request schemes", result.Stats.AuthSchemes)
	}
}

func TestConvertPostmanSerializesMultipartAndFileBindings(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Multipart"},
		  "item": [{
		    "name": "Upload",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/upload",
		      "header": [{"key": "Content-Type", "value": "multipart/form-data; boundary=custom-boundary"}],
		      "body": {"mode": "formdata", "formdata": [
		        {"key": "label", "value": "first"},
		        {"key": "label", "value": "second"},
		        {"key": "document", "type": "file", "src": "/tmp/report.pdf", "contentType": "application/pdf"}
		      ]}
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if got := action.Headers["Content-Type"]; got != "multipart/form-data; boundary=custom-boundary" {
		t.Fatalf("content type = %q", got)
	}
	if strings.Count(action.Body, `name="label"`) != 2 ||
		!strings.Contains(action.Body, "\r\n\r\nfirst\r\n") ||
		!strings.Contains(action.Body, "\r\n\r\nsecond\r\n") {
		t.Fatalf("multipart body did not preserve duplicate ordered fields: %q", action.Body)
	}
	if !strings.Contains(action.Body, "--custom-boundary") ||
		!strings.Contains(action.Body, `filename="report.pdf"`) ||
		!strings.Contains(action.Body, "Content-Type: application/pdf") ||
		!strings.Contains(action.Body, "{{file.postman_1_upload_document}}") {
		t.Fatalf("multipart file part = %q", action.Body)
	}
	if len(result.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one file binding", result.Script.DataSources)
	}
	asset := result.Script.DataSources[0]
	if asset.Name != "postman_1_upload_document" || asset.Type != "file" || asset.FileName != "report.pdf" ||
		asset.ContentType != "application/pdf" || asset.InlineData != postmanFilePlaceholder {
		t.Fatalf("file binding = %#v", asset)
	}
	if got := action.Metadata["execution_blocker"]; got != "import_missing_file_bindings" {
		t.Fatalf("file action execution blocker = %#v", got)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("multipart draft has validation issues: %v", issues)
	}
}

func TestConvertPostmanSanitizesAndGatesInvalidMultipartMetadata(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Multipart metadata"},
		  "item": [{
		    "name": "Submit",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/submit",
		      "body": {"mode": "formdata", "formdata": [{
		        "key": "label\r\nX-Injected: yes",
		        "value": "unchanged",
		        "contentType": "text/plain\r\nX-Other: yes"
		      }]}
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if strings.Contains(action.Body, "\r\nX-Injected: yes") ||
		strings.Contains(action.Body, "\r\nX-Other: yes") {
		t.Fatalf("multipart metadata injected part headers: %q", action.Body)
	}
	if !strings.Contains(action.Body, `name="label%0D%0AX-Injected: yes"`) ||
		!strings.Contains(action.Body, "\r\n\r\nunchanged\r\n") {
		t.Fatalf("sanitized multipart draft = %q", action.Body)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("execution blocker = %#v", action.Metadata["execution_blocker"])
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	reasons, ok := review["reason_codes"].([]string)
	if !ok || !containsTestString(reasons, "invalid_multipart_metadata") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked multipart draft has validation issues: %v", issues)
	}
}

func TestConvertPostmanSynthesizesDeterministicMultipartBoundary(t *testing.T) {
	request := ImportRequest{
		Content: `{
		  "info": {"name": "Multipart"},
		  "item": [{
		    "name": "Upload",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/upload",
		      "body": {"mode": "formdata", "formdata": [{"key": "label", "value": "value"}]}
		    }
		  }]
		}`,
	}
	first, err := Convert(request)
	if err != nil {
		t.Fatalf("first Convert returned error: %v", err)
	}
	second, err := Convert(request)
	if err != nil {
		t.Fatalf("second Convert returned error: %v", err)
	}
	firstAction := first.Script.Actions[0]
	secondAction := second.Script.Actions[0]
	if firstAction.Headers["Content-Type"] == "" || firstAction.Headers["Content-Type"] != secondAction.Headers["Content-Type"] {
		t.Fatalf("generated content types differ: %q / %q", firstAction.Headers["Content-Type"], secondAction.Headers["Content-Type"])
	}
	if firstAction.Body != secondAction.Body {
		t.Fatal("generated multipart bodies are not deterministic")
	}
}

func TestConvertPostmanUsesSharedRequestFinalizer(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Shared Pipeline"},
		  "item": [{
		    "name": "Fetch",
		    "request": {
		      "method": "GET",
		      "url": "https://api.example.com/items",
		      "header": [{"key": "X-Trace", "value": "one"}]
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.finalization == nil || result.finalization.Result == nil {
		t.Fatal("shared finalization sidecar is missing")
	}
	if len(result.finalization.Reports) != 1 {
		t.Fatalf("reports = %d, want one per converted request", len(result.finalization.Reports))
	}
	report := result.finalization.Reports[0]
	if report.ActionPath != "actions[0]" || report.RequestKey == "" {
		t.Fatalf("request report = %#v", report)
	}
	if len(result.finalization.Result.ValidationIssues) != 0 {
		t.Fatalf("shared finalizer returned validation issues: %v", result.finalization.Result.ValidationIssues)
	}
	action := result.Script.Actions[0]
	if action.Name != "Fetch" || action.Method != "GET" || action.Target != "/items" ||
		action.Headers["X-Trace"] != "one" {
		t.Fatalf("request semantics changed across shared projection: %#v", action)
	}
}

func TestConvertPostmanSharedCredentialProjectionPreservesExecutableCanaries(t *testing.T) {
	const (
		queryCanary  = "postman-query-canary-81d5"
		xmlCanary    = "postman-xml-canary-92e6"
		cookieCanary = "postman-cookie-canary-a3f7"
	)
	result, err := Convert(ImportRequest{
		Content: fmt.Sprintf(`{
		  "info": {"name": "Credentials"},
		  "item": [{
		    "name": "Secure",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/login?api_key=%s&mode=exact",
		      "header": [
		        {"key": "Cookie", "value": "session=%s"},
		        {"key": "Content-Type", "value": "application/xml"}
		      ],
		      "body": {"mode": "raw", "raw": "<login><password>%s</password><count>7</count></login>"}
		    }
		  }]
		}`, queryCanary, cookieCanary, xmlCanary),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{queryCanary, xmlCanary, cookieCanary} {
		if !strings.Contains(string(encoded), canary) {
			t.Fatalf("destination conversion lost credential %q: %s", canary, encoded)
		}
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, queryCanary) || !strings.Contains(action.Body, xmlCanary) {
		t.Fatalf("shared projection changed URL or XML credential bytes: %#v", action)
	}
	if action.Headers["Cookie"] != "session="+cookieCanary {
		t.Fatalf("Postman cookie changed: %q", action.Headers["Cookie"])
	}
	if result.finalization == nil || len(result.finalization.Reports) != 1 ||
		len(result.finalization.Reports[0].Requirements) != 0 {
		t.Fatalf("credential requirements = %#v, preserved values must not require reentry", result.finalization)
	}
	if result.SourceReview == nil {
		t.Fatal("source review missing")
	}
	sourceJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{queryCanary, xmlCanary, cookieCanary} {
		if strings.Contains(string(sourceJSON), canary) {
			t.Fatalf("source review disclosed %q: %s", canary, sourceJSON)
		}
	}
}

func TestConvertPostmanMasksNeutralVariablesUsedByCredentialURLAndBodyOnlyInSourceReview(t *testing.T) {
	const (
		queryCanary  = "postman-neutral-query-value-81f3"
		bodyCanary   = "postman-neutral-body-value-42ac"
		shadowCanary = "postman-neutral-duplicate-value-59de"
	)
	result, err := Convert(ImportRequest{
		Content: fmt.Sprintf(`{
		  "info": {"name": "Neutral credential variables"},
		  "variable": [
		    {"key": "lookupLabel", "value": %q},
		    {"key": "payloadLabel", "value": %q},
		    {"key": "shadowLabel", "value": %q}
		  ],
		  "item": [{
		    "name": "Credential flow",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.test/login?api_key={{lookupLabel}}",
		      "header": [{"key": "Content-Type", "value": "application/json"}],
		      "body": {
		        "mode": "raw",
		        "raw": "{\"password\":\"{{payloadLabel}}\",\"password\":\"{{shadowLabel}}\",\"label\":\"ordinary\"}"
		      }
		    }
		  }]
		}`, queryCanary, bodyCanary, shadowCanary),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v, want one request", result.Script)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, "{{var.lookupLabel}}") ||
		!strings.Contains(action.Body, "{{var.payloadLabel}}") ||
		!strings.Contains(action.Body, "{{var.shadowLabel}}") {
		t.Fatalf("durable request lost imported credential bindings: %#v", action)
	}
	durablePayload, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("json.Marshal(durable script): %v", err)
	}
	for _, canary := range []string{queryCanary, bodyCanary, shadowCanary} {
		if !strings.Contains(string(durablePayload), canary) {
			t.Fatalf("durable script lost imported credential %q: %s", canary, durablePayload)
		}
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("preserved imported values require operator reentry: %#v", requirements)
	}
	reviewPayload, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("json.Marshal(source review): %v", err)
	}
	for _, canary := range []string{queryCanary, bodyCanary, shadowCanary} {
		if strings.Contains(string(reviewPayload), canary) {
			t.Fatalf("source review disclosed neutral credential variable %q: %s", canary, reviewPayload)
		}
	}
}

func TestConvertPostmanProtectedProjectionCoversHeaderAuthJSONFormAndMultipart(t *testing.T) {
	canaries := []string{
		"postman-protected-header-139a",
		"postman-protected-auth-20bf",
		"postman-protected-json-31c0",
		"postman-protected-form-42d1",
		"postman-protected-multipart-53e2",
	}
	result, err := Convert(ImportRequest{
		Content: fmt.Sprintf(`{
		  "info": {"name": "Protected credentials"},
		  "item": [
		    {"name": "JSON", "request": {
		      "method": "POST",
		      "url": "https://api.example.com/json",
		      "header": [{"key": "X-Api-Key", "value": "%s"}],
		      "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "%s"}]},
		      "body": {"mode": "raw", "raw": "{\"password\":\"%s\",\"count\":7}"}
		    }},
		    {"name": "Form", "request": {
		      "method": "POST",
		      "url": "https://api.example.com/form",
		      "body": {"mode": "urlencoded", "urlencoded": [
		        {"key": "label", "value": "first"},
		        {"key": "password", "value": "%s"},
		        {"key": "label", "value": "second"}
		      ]}
		    }},
		    {"name": "Multipart", "request": {
		      "method": "POST",
		      "url": "https://api.example.com/multipart",
		      "body": {"mode": "formdata", "formdata": [
		        {"key": "label", "value": "first"},
		        {"key": "password", "value": "%s"},
		        {"key": "label", "value": "second"}
		      ]}
		    }}
		  ]
		}`, canaries[0], canaries[1], canaries[2], canaries[3], canaries[4]),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, canary := range canaries {
		if !strings.Contains(string(encoded), canary) {
			t.Fatalf("destination conversion lost %q: %s", canary, encoded)
		}
	}

	jsonAction := result.Script.Actions[0]
	if jsonAction.Headers["X-Api-Key"] != canaries[0] ||
		jsonAction.Auth == nil || jsonAction.Auth.Token != canaries[1] ||
		!strings.Contains(jsonAction.Body, `"password":"`+canaries[2]+`"`) ||
		!strings.Contains(jsonAction.Body, `"count":7`) {
		t.Fatalf("JSON/header/auth projection = %#v", jsonAction)
	}
	formBody := result.Script.Actions[1].Body
	firstLabel := strings.Index(formBody, "label=first")
	secret := strings.Index(formBody, "password="+canaries[3])
	secondLabel := strings.Index(formBody, "label=second")
	if firstLabel < 0 || secret <= firstLabel || secondLabel <= secret {
		t.Fatalf("ordered duplicate form fields changed: %q", formBody)
	}
	multipartBody := result.Script.Actions[2].Body
	if strings.Count(multipartBody, `name="label"`) != 2 ||
		!strings.Contains(multipartBody, canaries[4]) {
		t.Fatalf("ordered duplicate multipart fields changed: %q", multipartBody)
	}

	protected := result.ProtectedRequirements.Take()
	if len(protected) != 0 {
		t.Fatalf("protected requirements = %#v, preserved credentials must not require reentry", protected)
	}
	sourceJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range canaries {
		if strings.Contains(string(sourceJSON), canary) {
			t.Fatalf("source review disclosed %q: %s", canary, sourceJSON)
		}
	}
}

func TestConvertPostmanGovernsComposedCredentialAsWholeValue(t *testing.T) {
	const canary = "postman-composed-canary-c519"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Composed Credential"},
		  "item": [{
		    "name": "Secure",
		    "request": {
		      "method": "GET",
		      "url": "https://api.example.com/secure",
		      "header": [
		        {"key": "Authorization", "value": "Bearer ` + canary + `-{{token}}"}
		      ]
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	want := "Bearer " + canary + "-{{var.token}}"
	if action.Headers["Authorization"] != want {
		t.Fatalf("composed credential = %q, want imported value %q", action.Headers["Authorization"], want)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), canary) {
		t.Fatalf("destination conversion lost composed credential: %s", encoded)
	}
}

func TestPostmanCredentialTemplatePreservationRequiresResponseProvenance(t *testing.T) {
	const value = "session={{var.session_id}}"
	if postmanCredentialTemplateIsSafeToPreserve(value, nil) {
		t.Fatal("unproven composed credential variable was treated as response-derived")
	}
	if !postmanCredentialTemplateIsSafeToPreserve(value, map[string]struct{}{"session_id": {}}) {
		t.Fatal("proven response-derived cookie variable was not preserved")
	}
	if postmanCredentialTemplateIsSafeToPreserve(
		"session=literal-{{var.session_id}}",
		map[string]struct{}{"session_id": {}},
	) {
		t.Fatal("literal credential prefix was not governed as part of the whole value")
	}
}

func TestConvertPostmanUnsanitizedCompatibilityRemainsExecutable(t *testing.T) {
	sanitize := false
	result, err := Convert(ImportRequest{
		Content: `{
		  "info": {"name": "Unmanaged"},
		  "item": [{
		    "name": "Login",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/login",
		      "header": [
		        {"key": "Authorization", "value": "Bearer raw-header-canary"}
		      ],
		      "body": {"mode": "raw", "raw": "{\"password\":\"raw-body-canary\",\"count\":7}"}
		    }
		  }]
		}`,
		Options: ConvertOptions{SanitizeSensitive: &sanitize},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Headers["Authorization"] != "Bearer raw-header-canary" ||
		action.Body != `{"password":"raw-body-canary","count":7}` {
		t.Fatalf("sanitize_sensitive=false changed source values: %#v", action)
	}
	if blocker := action.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("execution blocker = %#v, preserved credentials must remain runnable", action.Metadata)
	}
	if result.finalization == nil || result.finalization.Result == nil ||
		result.finalization.Result.Disposition == "blocked_draft" {
		t.Fatalf("finalization disposition = %#v", result.finalization)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked compatibility draft is invalid: %v", issues)
	}
}

func TestConvertPostmanProjectsMultipartCredentialsWithoutChangingOtherParts(t *testing.T) {
	const multipartCanary = "postman-multipart-canary-b408"
	result, err := Convert(ImportRequest{
		Content: fmt.Sprintf(`{
		  "info": {"name": "Multipart Credentials"},
		  "item": [{
		    "name": "Submit",
		    "request": {
		      "method": "POST",
		      "url": "https://api.example.com/submit",
		      "body": {"mode": "formdata", "formdata": [
		        {"key": "label", "value": "first"},
		        {"key": "password", "value": "%s"},
		        {"key": "label", "value": "second"}
		      ]}
		    }
		  }]
		}`, multipartCanary),
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Body, multipartCanary) {
		t.Fatalf("multipart credential was not preserved: %q", action.Body)
	}
	if strings.Count(action.Body, `name="label"`) != 2 ||
		!strings.Contains(action.Body, "\r\n\r\nfirst\r\n") ||
		!strings.Contains(action.Body, "\r\n\r\nsecond\r\n") {
		t.Fatalf("nonsensitive multipart occurrences changed: %q", action.Body)
	}
	if len(result.finalization.Reports) != 1 ||
		len(result.finalization.Reports[0].Requirements) != 0 {
		t.Fatalf("multipart credential requirements = %#v, want no reentry", result.finalization.Reports)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), multipartCanary) {
		t.Fatalf("destination conversion lost multipart credential: %s", encoded)
	}
	sourceJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourceJSON), multipartCanary) {
		t.Fatalf("source review disclosed multipart credential: %s", sourceJSON)
	}
}

func TestConvertPostmanSynthesizesCandidateScopedCollisionSafeMultipartBoundaries(t *testing.T) {
	const source = `{
	  "info": {"name": "Multipart boundaries"},
	  "item": [
	    {"name": "First", "request": {
	      "method": "POST", "url": "https://api.example.com/first",
	      "body": {"mode": "formdata", "formdata": [{"key": "label", "value": "same"}]}
	    }},
	    {"name": "Second", "request": {
	      "method": "POST", "url": "https://api.example.com/second",
	      "body": {"mode": "formdata", "formdata": [{"key": "label", "value": "same"}]}
	    }}
	  ]
	}`
	first, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("first Convert returned error: %v", err)
	}
	second, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("second Convert returned error: %v", err)
	}
	firstBoundary := multipartBoundary(first.Script.Actions[0].Headers["Content-Type"])
	secondCandidateBoundary := multipartBoundary(first.Script.Actions[1].Headers["Content-Type"])
	repeatedBoundary := multipartBoundary(second.Script.Actions[0].Headers["Content-Type"])
	if firstBoundary == "" || secondCandidateBoundary == "" {
		t.Fatalf("multipart content types = %#v", first.Script.Actions)
	}
	if firstBoundary == secondCandidateBoundary {
		t.Fatalf("two candidates shared synthesized boundary %q", firstBoundary)
	}
	if repeatedBoundary != firstBoundary {
		t.Fatalf("same candidate boundary = %q, want deterministic %q", repeatedBoundary, firstBoundary)
	}

	collidingBoundary := postmanMultipartBoundaryCandidate("postman_1_Collision", 0)
	collisionResult, err := Convert(ImportRequest{Content: fmt.Sprintf(`{
	  "info": {"name": "Boundary collision"},
	  "item": [{"name": "Collision", "request": {
	    "method": "POST", "url": "https://api.example.com/collision",
	    "body": {"mode": "formdata", "formdata": [{"key": "label", "value": %q}]}
	  }}]
	}`, collidingBoundary)})
	if err != nil {
		t.Fatalf("collision Convert returned error: %v", err)
	}
	chosen := multipartBoundary(collisionResult.Script.Actions[0].Headers["Content-Type"])
	if chosen == collidingBoundary {
		t.Fatalf("synthesized boundary collided with part content: %q", chosen)
	}
	if !strings.Contains(collisionResult.Script.Actions[0].Body, collidingBoundary) {
		t.Fatalf("nonsensitive collision probe was not preserved: %q", collisionResult.Script.Actions[0].Body)
	}
}

func TestConvertPostmanSharedPipelineIsDeterministic(t *testing.T) {
	request := ImportRequest{
		Content: `{
		  "info": {"name": "Deterministic"},
		  "item": [
		    {"name": "One", "request": {
		      "method": "POST",
		      "url": "https://api.example.com/items?api_key=rotate-me",
		      "header": [
		        {"key": "X-Dupe", "value": "one"},
		        {"key": "X-Dupe", "value": "two"}
		      ],
		      "body": {"mode": "raw", "raw": "{\"password\":\"rotate-me\",\"count\":7}"}
		    }},
		    {"name": "Two", "request": {"method": "GET", "url": "https://api.example.com/items/2"}}
		  ]
		}`,
	}
	var expected [32]byte
	for run := 0; run < 25; run++ {
		result, err := Convert(request)
		if err != nil {
			t.Fatalf("Convert run %d returned error: %v", run, err)
		}
		result.Script.CreatedAt = time.Time{}
		payload, err := json.Marshal(struct {
			Script  *dsl.Script
			Stats   ConvertStats
			Summary ReviewSummary
			Reports any
		}{
			Script:  result.Script,
			Stats:   result.Stats,
			Summary: result.Summary,
			Reports: result.finalization.Reports,
		})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(payload)
		if run == 0 {
			expected = hash
			continue
		}
		if hash != expected {
			t.Fatalf("normalized conversion hash changed on run %d: %x != %x", run, hash, expected)
		}
	}
}

func containsTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func postmanReportHasFinding(report requestimport.Report, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
