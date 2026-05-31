package openapi

import (
	"strings"
	"testing"
)

func TestConvertOpenAPIJSONBuildsExecutableHTTPScript(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Catalog API",
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Catalog", "version": "1.2.0"},
  "servers": [{"url": "https://api.example.test"}],
  "components": {
    "securitySchemes": {
      "bearerAuth": {"type": "http", "scheme": "bearer"}
    }
  },
  "paths": {
    "/orders/{orderId}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [
          {"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "includeItems", "in": "query", "schema": {"type": "boolean"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "customerId": {"type": "string"},
                  "quantity": {"type": "integer"}
                }
              }
            }
          }
        },
        "responses": {"201": {"description": "created"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("Protocol = %q, want http", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("BaseURL = %q", result.Script.Config.BaseURL)
	}
	if result.Script.Config.HTTP.Auth.Type != "bearer" {
		t.Fatalf("Auth.Type = %q, want bearer", result.Script.Config.HTTP.Auth.Type)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Target != "/orders" {
		t.Fatalf("first action target = %q, want /orders", result.Script.Actions[0].Target)
	}
	if !strings.Contains(result.Script.Actions[0].Body, "{{data.openapi.customerid}}") {
		t.Fatalf("post body did not include data placeholder: %s", result.Script.Actions[0].Body)
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{data.openapi.orderid}}") {
		t.Fatalf("get target did not include path placeholder: %s", result.Script.Actions[1].Target)
	}
	if len(result.Script.DataSources) != 1 {
		t.Fatalf("data sources = %d, want 1", len(result.Script.DataSources))
	}
	if result.Stats.ConvertedActions != 2 || result.Stats.Assertions != 2 {
		t.Fatalf("stats = %+v, want 2 actions and 2 assertions", result.Stats)
	}
}

func TestConvertOpenAPIYAMLFiltersOperations(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
openapi: 3.0.0
info:
  title: YAML API
  version: 1
servers:
  - url: https://yaml.example.test
paths:
  /health:
    get:
      operationId: getHealth
      tags: [ops]
      responses:
        "200": {}
  /internal:
    get:
      operationId: getInternal
      tags: [internal]
      responses:
        "200": {}
`,
		Options: ConvertOptions{Tags: []string{"ops"}},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Name != "getHealth" {
		t.Fatalf("action name = %q, want getHealth", result.Script.Actions[0].Name)
	}
	if len(result.Stats.SkippedOperations) != 1 {
		t.Fatalf("skipped operations = %v, want one filtered operation", result.Stats.SkippedOperations)
	}
	if len(result.Unsupported) == 0 {
		t.Fatalf("unsupported diagnostics = %#v, want skipped operation diagnostic", result.Unsupported)
	}
}

func TestConvertOpenAPIResolvesRefsAndReportsUnsupportedConstructs(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "components": {
    "schemas": {
      "OrderInput": {
        "type": "object",
        "properties": {
          "customerId": {"type": "string"},
          "quantity": {"type": "integer"}
        }
      }
    }
  },
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "parameters": [
          {"name": "X-Tenant", "in": "header", "schema": {"type": "string"}},
          {"name": "session_id", "in": "cookie", "schema": {"type": "string"}}
        ],
        "requestBody": {
          "content": {
            "application/json": {"schema": {"$ref": "#/components/schemas/OrderInput"}}
          }
        },
        "responses": {"201": {}}
      }
    },
    "/uploads": {
      "post": {
        "operationId": "uploadDocument",
        "requestBody": {
          "content": {
            "multipart/form-data": {
              "schema": {
                "type": "object",
                "properties": {
                  "file": {"type": "string", "format": "binary"},
                  "description": {"type": "string"}
                }
              }
            }
          }
        },
        "responses": {"200": {}}
      }
    },
    "/composed": {
      "post": {
        "operationId": "createComposed",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "allOf": [
                  {"type": "object", "properties": {"baseId": {"type": "string"}}},
                  {"type": "object", "properties": {"lineCount": {"type": "integer"}}}
                ]
              }
            }
          }
        },
        "responses": {"200": {}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	create := openAPIAction(result, "createOrder")
	if !strings.Contains(create.Body, "{{data.openapi.customerid}}") || !strings.Contains(create.Body, "{{data.openapi.quantity}}") {
		t.Fatalf("ref body was not expanded: %s", create.Body)
	}
	if create.Headers["X-Tenant"] != "{{data.openapi.x_tenant}}" {
		t.Fatalf("header parameter = %q", create.Headers["X-Tenant"])
	}
	if create.Headers["Cookie"] != "session_id={{data.openapi.session_id}}" {
		t.Fatalf("cookie parameter = %q", create.Headers["Cookie"])
	}
	upload := openAPIAction(result, "uploadDocument")
	if !strings.Contains(upload.Body, "{{file.file}}") || !strings.Contains(upload.Headers["Content-Type"], "boundary=----apexload-openapi-boundary") {
		t.Fatalf("multipart upload action = %#v", upload)
	}
	composed := openAPIAction(result, "createComposed")
	if !strings.Contains(composed.Body, "{{data.openapi.baseid}}") || !strings.Contains(composed.Body, "{{data.openapi.linecount}}") {
		t.Fatalf("allOf body was not merged: %s", composed.Body)
	}
	if !containsOpenAPIDiagnostic(result, "OpenAPI multipart file") {
		t.Fatalf("diagnostics = %#v, want multipart file warning", result.Unsupported)
	}
	if !containsOpenAPIDiagnostic(result, "OpenAPI allOf") {
		t.Fatalf("diagnostics = %#v, want allOf warning", result.Unsupported)
	}
}

func TestConvertSwagger2BodyAndFormParameters(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "swagger": "2.0",
  "info": {"title": "Legacy Orders", "version": "1.0"},
  "host": "api.example.test",
  "schemes": ["https"],
  "basePath": "/v1",
  "consumes": ["application/json"],
  "definitions": {
    "OrderInput": {
      "type": "object",
      "properties": {
        "customerId": {"type": "string"},
        "quantity": {"type": "integer"}
      }
    }
  },
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createLegacyOrder",
        "parameters": [
          {"name": "body", "in": "body", "schema": {"$ref": "#/definitions/OrderInput"}}
        ],
        "responses": {"201": {}}
      }
    },
    "/uploads": {
      "post": {
        "operationId": "uploadLegacyFile",
        "consumes": ["multipart/form-data"],
        "parameters": [
          {"name": "file", "in": "formData", "type": "string", "format": "binary"},
          {"name": "description", "in": "formData", "type": "string"}
        ],
        "responses": {"200": {}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	legacy := openAPIAction(result, "createLegacyOrder")
	if !strings.Contains(legacy.Body, "{{data.openapi.customerid}}") || !strings.Contains(legacy.Body, "{{data.openapi.quantity}}") {
		t.Fatalf("Swagger 2 body parameter was not expanded: %s", legacy.Body)
	}
	upload := openAPIAction(result, "uploadLegacyFile")
	if !strings.Contains(upload.Body, "{{file.file}}") || !strings.Contains(upload.Headers["Content-Type"], "multipart/form-data") {
		t.Fatalf("Swagger 2 formData action = %#v", upload)
	}
	if !containsOpenAPIDiagnostic(result, "Swagger 2 formData parameters") {
		t.Fatalf("diagnostics = %#v, want formData modeling info", result.Unsupported)
	}
}

func TestConvertSwagger2ResolvesTopLevelParameterRefs(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "swagger": "2.0",
  "info": {"title": "Legacy Orders", "version": "1.0"},
  "host": "api.example.test",
  "schemes": ["https"],
  "basePath": "/v1",
  "consumes": ["application/json"],
  "definitions": {
    "OrderInput": {
      "type": "object",
      "properties": {
        "customerId": {"type": "string"},
        "quantity": {"type": "integer"}
      }
    }
  },
  "parameters": {
    "TenantHeader": {"name": "X-Tenant", "in": "header", "type": "string"},
    "SearchQuery": {"name": "q", "in": "query", "type": "string"},
    "OrderBody": {"name": "body", "in": "body", "schema": {"$ref": "#/definitions/OrderInput"}}
  },
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createLegacyOrderFromRefs",
        "parameters": [
          {"$ref": "#/parameters/TenantHeader"},
          {"$ref": "#/parameters/SearchQuery"},
          {"$ref": "#/parameters/OrderBody"}
        ],
        "responses": {"201": {}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	action := openAPIAction(result, "createLegacyOrderFromRefs")
	if action.Headers["X-Tenant"] != "{{data.openapi.x_tenant}}" {
		t.Fatalf("top-level header ref = %q", action.Headers["X-Tenant"])
	}
	if !strings.Contains(action.Target, "q={{data.openapi.q}}") {
		t.Fatalf("top-level query ref target = %q", action.Target)
	}
	if !strings.Contains(action.Body, "{{data.openapi.customerid}}") || !strings.Contains(action.Body, "{{data.openapi.quantity}}") {
		t.Fatalf("top-level body ref was not expanded: %s", action.Body)
	}
	if containsOpenAPIDiagnostic(result, "OpenAPI parameter $ref") {
		t.Fatalf("diagnostics = %#v, did not expect unresolved parameter ref", result.Unsupported)
	}
}

func openAPIAction(result *ConvertResult, name string) dslActionForTest {
	for _, action := range result.Script.Actions {
		if action.Name == name {
			return dslActionForTest{Body: action.Body, Headers: action.Headers, Target: action.Target}
		}
	}
	return dslActionForTest{}
}

type dslActionForTest struct {
	Body    string
	Headers map[string]string
	Target  string
}

func containsOpenAPIDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}
