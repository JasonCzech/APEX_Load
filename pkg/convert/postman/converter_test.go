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
	if first.Headers["X-Trace"] != "{{var.trace_id}}" {
		t.Fatalf("trace header = %q", first.Headers["X-Trace"])
	}
	second := result.Script.Actions[1]
	if second.Headers["Authorization"] != "{{var.Authorization}}" {
		t.Fatalf("authorization header = %q", second.Headers["Authorization"])
	}
	if !strings.Contains(second.Body, "{{var.customer_id}}") || !strings.Contains(second.Body, "{{var.password}}") {
		t.Fatalf("second body was not parameterized/sanitized: %s", second.Body)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "order_id" {
		t.Fatalf("parameters = %#v, want order_id only", result.Script.Parameters)
	}
	if result.Stats.Folders != 1 || result.Stats.ConvertedActions != 2 || result.Stats.VariablesDetected != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil || result.Script.Metadata.Import.Source != "postman" {
		t.Fatalf("missing postman import metadata: %#v", result.Script.Metadata)
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

func TestConvertPostmanRejectsEmptyCollection(t *testing.T) {
	if _, err := Convert(ImportRequest{Content: `{"info":{"name":"Empty"},"item":[]}`}); err == nil {
		t.Fatal("Convert returned nil error for empty collection")
	}
}
