package postman

import (
	"strings"
	"testing"
)

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
	if second.Headers["Authorization"] != "{{env.POSTMAN_AUTHORIZATION}}" {
		t.Fatalf("authorization header = %q", second.Headers["Authorization"])
	}
	if !strings.Contains(second.Body, "{{var.customer_id}}") || !strings.Contains(second.Body, "{{var.password}}") {
		t.Fatalf("second body was not parameterized/sanitized: %s", second.Body)
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
		t.Fatalf("collection auth should not emit replay-blocking digest auth: %#v", result.Script.Config.HTTP.Auth)
	}
	first := result.Script.Actions[0]
	if first.Target != "/orders/{{var.orderId}}?requestId={{fn.uuid()}}&ts={{fn.timestamp.unix}}" {
		t.Fatalf("target = %q", first.Target)
	}
	if first.Auth != nil {
		t.Fatalf("digest collection auth should be review-only, got action auth %#v", first.Auth)
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
	if got := result.Script.Actions[0].Auth; got == nil || got.Type != "bearer" || got.Token != "{{env.POSTMAN_BEARER_TOKEN}}" {
		t.Fatalf("inherited auth = %#v, want sanitized bearer action auth", got)
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
	if got := result.Script.Actions[0].Headers["X-API-Key"]; got != "{{env.POSTMAN_API_KEY}}" {
		t.Fatalf("header API key = %q", got)
	}
	if got := result.Script.Actions[1].Target; got != "/query?existing=1&api_key={{var.apiToken}}" {
		t.Fatalf("query API key target = %q", got)
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
		            "const signature = sign(pm.request.url);"
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
	if !postmanParameterNames(result, "token") {
		t.Fatalf("parameters = %#v, want translated token variable", result.Script.Parameters)
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
}

func postmanParameterNames(result *ConvertResult, name string) bool {
	for _, param := range result.Script.Parameters {
		if param.Name == name {
			return true
		}
	}
	return false
}

func containsPostmanDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}
