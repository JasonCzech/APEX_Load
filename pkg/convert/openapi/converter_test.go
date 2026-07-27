package openapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestOpenAPIEffectiveBodyDecodesBase64DSLRepresentation(t *testing.T) {
	wireBody := []byte(`{"password":"source-value","note":"unchanged"}`)
	body, err := openAPIEffectiveBody(dsl.Action{
		Body:         base64.StdEncoding.EncodeToString(wireBody),
		BodyEncoding: "base64",
		Headers:      map[string]string{"Content-Type": "application/json"},
	}, credentialpolicy.SourceRef{Format: "openapi", Path: "fixture.yaml"})
	if err != nil {
		t.Fatalf("openAPIEffectiveBody: %v", err)
	}
	if string(body.Payload) != string(wireBody) ||
		body.Encoding != requestimport.BodyEncodingBase64 ||
		body.Kind != requestimport.BodyJSON {
		t.Fatalf("effective body = %#v, want decoded JSON wire bytes", body)
	}
}

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
	if result.Script.Config.HTTP.Auth.Type != "" {
		t.Fatalf("Auth.Type = %q, want no auth when scheme is defined but unused", result.Script.Config.HTTP.Auth.Type)
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
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) != 2 {
		t.Fatalf("source review = %#v, want source file and two action mappings", result.SourceReview)
	}
	if result.SourceReview.Mappings[0].TargetPath != "actions[0]" || result.SourceReview.Mappings[0].Status != "converted" {
		t.Fatalf("first source mapping = %#v", result.SourceReview.Mappings[0])
	}
}

func TestConvertOpenAPIServerVariablesStyleExplodeAndOperationSecurity(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Search", "version": "1.0"},
  "servers": [{
    "url": "https://{env}.api.example.test/{basePath}",
    "variables": {
      "env": {"default": "prod", "enum": ["prod", "stage"]},
      "basePath": {"default": "v2"}
    }
  }],
  "components": {
    "securitySchemes": {
      "basicAuth": {"type": "http", "scheme": "basic"},
      "bearerAuth": {"type": "http", "scheme": "bearer"}
    }
  },
  "paths": {
    "/search": {
      "get": {
        "operationId": "searchOrders",
        "security": [{"basicAuth": []}],
        "parameters": [
          {"name": "tags", "in": "query", "style": "pipeDelimited", "explode": false, "schema": {"type": "array", "items": {"type": "string"}}},
          {"name": "filter", "in": "query", "style": "deepObject", "explode": true, "schema": {"type": "object", "properties": {"status": {"type": "string"}, "owner": {"type": "string"}}}}
        ],
        "responses": {"200": {}}
      }
    },
    "/public": {
      "get": {
        "operationId": "publicStatus",
        "security": [],
        "responses": {"200": {}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://prod.api.example.test/v2" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "server variable env used default prod") {
		t.Fatalf("warnings = %#v, want server variable enum note", result.Warnings)
	}
	search := openAPIActionFull(result, "searchOrders")
	if search.Auth == nil || search.Auth.Type != "basic" {
		t.Fatalf("operation auth = %#v, want basic override", search.Auth)
	}
	for _, want := range []string{
		"tags={{data.openapi.tags}}",
		"filter[owner]={{data.openapi.filter_owner}}",
		"filter[status]={{data.openapi.filter_status}}",
	} {
		if !strings.Contains(search.Target, want) {
			t.Fatalf("target = %q, want %q", search.Target, want)
		}
	}
	public := openAPIActionFull(result, "publicStatus")
	if public.Auth == nil || public.Auth.Type != "none" {
		t.Fatalf("public auth = %#v, want explicit anonymous override (type none) for security:[]", public.Auth)
	}
	if !strings.Contains(result.Script.DataSources[0].InlineData, "filter_owner") || !strings.Contains(result.Script.DataSources[0].InlineData, "filter_status") {
		t.Fatalf("data source = %q", result.Script.DataSources[0].InlineData)
	}
}

func TestConvertOpenAPICRUDChainExtractsAndPrunesDataSeeds(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "requestBody": {
          "content": {"application/json": {"schema": {
            "type": "object",
            "properties": {
              "customerId": {"type": "string"},
              "quantity": {"type": "integer"}
            }
          }}}
        },
        "responses": {"201": {
          "description": "created",
          "content": {"application/json": {"schema": {
            "type": "object",
            "properties": {
              "id": {"type": "string"},
              "href": {"type": "string", "format": "uri"},
              "status": {"type": "string"}
            }
          }}}
        }}
      }
    },
    "/orders/{orderId}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      },
      "put": {
        "operationId": "updateOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {"content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"status": {"type": "string"}}
        }}}},
        "responses": {"200": {"description": "ok"}}
      },
      "delete": {
        "operationId": "deleteOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"204": {"description": "deleted"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	create := openAPIActionFull(result, "createOrder")
	if !strings.Contains(create.Body, `"quantity": {{data.openapi.quantity}}`) {
		t.Fatalf("create body = %s, want unquoted numeric placeholder", create.Body)
	}
	orderIDExtractor := openAPIExtractorByName(create.Extractors, "order_id")
	if orderIDExtractor == nil || orderIDExtractor.Source != "body" || orderIDExtractor.Type != "jsonpath" || orderIDExtractor.Expression != "$.id" || orderIDExtractor.OnMiss != "empty" {
		t.Fatalf("create extractors = %#v, want order_id jsonpath extractor", create.Extractors)
	}
	for _, name := range []string{"getOrder", "updateOrder", "deleteOrder"} {
		action := openAPIActionFull(result, name)
		if !strings.Contains(action.Target, "{{var.order_id}}") {
			t.Fatalf("%s target = %q, want chained order_id var", name, action.Target)
		}
		if strings.Contains(action.Target, "{{data.openapi.orderid}}") {
			t.Fatalf("%s target retained data seed after chaining: %q", name, action.Target)
		}
	}
	if len(result.Correlations) != 1 || result.Correlations[0].Name != "order_id" || len(result.Correlations[0].UsedInSteps) != 3 {
		t.Fatalf("correlations = %#v, want one order_id correlation used by three consumers", result.Correlations)
	}
	if result.Stats.Extractors < 2 || result.Stats.Correlations != 1 {
		t.Fatalf("stats = %+v, want generated extractors and one correlation", result.Stats)
	}
	if len(result.Script.DataSources) != 1 || strings.Contains(result.Script.DataSources[0].InlineData, "orderid") {
		t.Fatalf("data source = %#v, want dead orderId CSV seed pruned", result.Script.DataSources)
	}
	if !strings.Contains(result.Script.DataSources[0].InlineData, "quantity") || !strings.Contains(result.Script.DataSources[0].InlineData, "42") {
		t.Fatalf("data source = %q, want schema-aware numeric sample", result.Script.DataSources[0].InlineData)
	}
}

func TestConvertOpenAPILocationHeaderExtractor(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "responses": {"201": {"description": "created"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createOrder")
	if len(action.Extractors) != 1 || action.Extractors[0].Source != "header" || action.Extractors[0].Type != "header" || action.Extractors[0].Expression != "Location" || action.Extractors[0].Name != "order_location" {
		t.Fatalf("extractors = %#v, want Location header extractor", action.Extractors)
	}
}

// TestConvertOpenAPIAnonymousSecurityOverrideSuppressesInheritedAuth guards
// operation-level OpenAPI security semantics: document-level security is applied
// to inherited operations only, while security: [] remains explicitly anonymous.
func TestConvertOpenAPIAnonymousSecurityOverrideSuppressesInheritedAuth(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Mixed", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer"}}},
  "security": [{"bearerAuth": []}],
  "paths": {
    "/secured": {"get": {"operationId": "securedPing", "responses": {"200": {}}}},
    "/public": {"get": {"operationId": "publicPing", "security": [], "responses": {"200": {}}}}
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	if result.Script.Config.HTTP.Auth.Type != "" {
		t.Fatalf("global auth = %#v, want imported auth to remain per-action", result.Script.Config.HTTP.Auth)
	}
	secured := openAPIActionFull(result, "securedPing")
	if secured.Auth == nil || secured.Auth.Type != "bearer" || secured.Auth.Token != "{{secret.OPENAPI_BEARERAUTH_BEARER_TOKEN}}" {
		t.Fatalf("securedPing auth = %#v, want inherited bearer", secured.Auth)
	}
	public := openAPIActionFull(result, "publicPing")
	if public.Auth == nil || public.Auth.Type != "none" {
		t.Fatalf("publicPing auth = %#v, want anonymous override (type none) to suppress global auth", public.Auth)
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
	if create.Headers["Cookie"] != "session_id={{secret.OPENAPI_SESSION_ID}}" {
		t.Fatalf("cookie parameter = %q", create.Headers["Cookie"])
	}
	upload := openAPIAction(result, "uploadDocument")
	if !strings.Contains(upload.Body, "{{file.file}}") || !strings.Contains(upload.Headers["Content-Type"], "boundary=----apexload-openapi-boundary") {
		t.Fatalf("multipart upload action = %#v", upload)
	}
	if !openAPIHasFileDataSource(result, "file") {
		t.Fatalf("data sources = %#v, want generated file asset placeholder", result.Script.DataSources)
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

func TestConvertOpenAPIFormURLEncodedAndVariantSchemas(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Variant API", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/sessions": {
      "post": {
        "operationId": "createSession",
        "requestBody": {
          "content": {
            "application/x-www-form-urlencoded": {
              "schema": {
                "type": "object",
                "properties": {
                  "username": {"type": "string"},
                  "password": {"type": "string"}
                }
              }
            }
          }
        },
        "responses": {"200": {}}
      }
    },
    "/variants": {
      "post": {
        "operationId": "createVariant",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "discriminator": {"propertyName": "kind"},
                "oneOf": [
                  {"type": "object", "properties": {"kind": {"type": "string"}, "cardId": {"type": "string"}}},
                  {"type": "object", "properties": {"kind": {"type": "string"}, "bankId": {"type": "string"}}}
                ]
              }
            }
          }
        },
        "responses": {"202": {}}
      }
    },
    "/fallbacks": {
      "post": {
        "operationId": "createFallback",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "anyOf": [
                  {"type": "object", "properties": {"email": {"type": "string"}}},
                  {"type": "object", "properties": {"phone": {"type": "string"}}}
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

	session := openAPIAction(result, "createSession")
	if session.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("form content type = %q", session.Headers["Content-Type"])
	}
	if !strings.Contains(session.Body, "password={{secret.OPENAPI_PASSWORD}}") ||
		!strings.Contains(session.Body, "username={{data.openapi.username}}") {
		t.Fatalf("form body = %q, want governed password and ordinary data placeholders", session.Body)
	}

	variant := openAPIAction(result, "createVariant")
	if !strings.Contains(variant.Body, "{{data.openapi.cardid}}") || strings.Contains(variant.Body, "bankId") {
		t.Fatalf("oneOf body = %s, want first variant only", variant.Body)
	}
	fallback := openAPIAction(result, "createFallback")
	if !strings.Contains(fallback.Body, "{{data.openapi.email}}") || strings.Contains(fallback.Body, "phone") {
		t.Fatalf("anyOf body = %s, want first variant only", fallback.Body)
	}
	for _, element := range []string{"OpenAPI oneOf", "OpenAPI anyOf", "OpenAPI discriminator"} {
		if !containsOpenAPIDiagnostic(result, element) {
			t.Fatalf("diagnostics = %#v, want %s", result.Unsupported, element)
		}
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

func openAPIActionFull(result *ConvertResult, name string) dsl.Action {
	for _, action := range result.Script.Actions {
		if action.Name == name {
			return action
		}
	}
	return dsl.Action{}
}

type dslActionForTest struct {
	Body    string
	Headers map[string]string
	Target  string
}

func openAPIHasFileDataSource(result *ConvertResult, name string) bool {
	for _, source := range result.Script.DataSources {
		if source.Name == name && source.Type == "file" && strings.Contains(source.InlineData, "APEXLOAD_OPENAPI_FILE_PLACEHOLDER") {
			return true
		}
	}
	return false
}

func openAPIExtractorByName(extractors []dsl.Extractor, name string) *dsl.Extractor {
	for i := range extractors {
		if extractors[i].Name == name {
			return &extractors[i]
		}
	}
	return nil
}

func containsOpenAPIDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}

func TestConvertOpenAPIResponseSchemaBodyAssertions(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "components": {
    "schemas": {
      "Order": {
        "type": "object",
        "required": ["id", "status", "total", "internal_note"],
        "properties": {
          "id": {"type": "string"},
          "status": {"type": "string"},
          "total": {"type": "number"},
          "created_at": {"type": "string"}
        }
      }
    }
  },
  "paths": {
    "/orders/{orderId}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {
            "description": "ok",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Order"}}}
          }
        }
      }
    },
    "/health": {
      "get": {
        "operationId": "health",
        "responses": {"200": {"description": "no schema declared"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	var orderAction, healthAction *dsl.Action
	for i := range result.Script.Actions {
		switch result.Script.Actions[i].Name {
		case "getOrder":
			orderAction = &result.Script.Actions[i]
		case "health":
			healthAction = &result.Script.Actions[i]
		}
	}
	if orderAction == nil || healthAction == nil {
		t.Fatalf("expected getOrder and health actions, got %+v", result.Script.Actions)
	}

	// Status assertion plus body_contains for required+declared properties,
	// capped at 3 and excluding required names without a declared property.
	if len(orderAction.Assertions) != 4 {
		t.Fatalf("getOrder assertions = %+v, want status + 3 body_contains", orderAction.Assertions)
	}
	if orderAction.Assertions[0].Type != "status_code" {
		t.Fatalf("first assertion = %+v, want status_code", orderAction.Assertions[0])
	}
	wantValues := map[string]bool{`"id"`: true, `"status"`: true, `"total"`: true}
	for _, assertion := range orderAction.Assertions[1:] {
		if assertion.Type != "body_contains" {
			t.Fatalf("assertion type = %q, want body_contains", assertion.Type)
		}
		if !wantValues[assertion.Value] {
			t.Fatalf("unexpected body_contains value %q (internal_note must be skipped: no declared property)", assertion.Value)
		}
		delete(wantValues, assertion.Value)
	}

	// No declared schema means status assertion only.
	if len(healthAction.Assertions) != 1 || healthAction.Assertions[0].Type != "status_code" {
		t.Fatalf("health assertions = %+v, want status_code only", healthAction.Assertions)
	}
}

func TestConvertOpenAPIQueryAndDocLevelSecurity(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Keyed", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "security": [{"queryKey": []}],
  "components": {
    "securitySchemes": {
      "queryKey": {"type": "apiKey", "in": "query", "name": "api_key"}
    }
  },
  "paths": {
    "/things": {
      "get": {
        "operationId": "listThings",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	target := result.Script.Actions[0].Target
	if !strings.Contains(target, "api_key={{secret.OPENAPI_QUERYKEY_API_KEY}}") {
		t.Fatalf("target = %q, want doc-level query api key appended", target)
	}
}

func TestConvertOpenAPIOAuthClientCredentialsGeneratesTokenInit(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "OAuth API", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "security": [{"oauth2": ["orders:read"]}],
  "components": {
    "securitySchemes": {
      "oauth2": {
        "type": "oauth2",
        "flows": {
          "clientCredentials": {
            "tokenUrl": "/oauth/token",
            "scopes": {"orders:read": "Read orders", "orders:write": "Write orders"}
          }
        }
      }
    }
  },
  "paths": {
    "/orders": {
      "get": {
        "operationId": "listOrders",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Init) != 1 {
		t.Fatalf("init actions = %#v, want OAuth token init", result.Script.Init)
	}
	init := result.Script.Init[0]
	if init.Type != "http_oauth2_token" || init.Target != "https://api.example.test/oauth/token" || init.Params["grant_type"] != "client_credentials" || init.Params["save_as"] != "access_token" {
		t.Fatalf("init action = %#v, want client-credentials token init", init)
	}
	action := openAPIActionFull(result, "listOrders")
	if action.Auth == nil || action.Auth.Type != "bearer" || action.Auth.Token != "{{var.access_token}}" {
		t.Fatalf("listOrders auth = %#v, want token init variable", action.Auth)
	}
}

func TestConvertOpenAPIOAuthTokenInitUnionsScopesAcrossOperations(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"OAuth scope union","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{"oauth2":{
    "type":"oauth2",
    "flows":{"clientCredentials":{
      "tokenUrl":"/oauth/token",
      "scopes":{"orders:read":"Read orders","orders:write":"Write orders"}
    }}
  }}},
  "paths":{
    "/orders/read":{"get":{
      "operationId":"readOrders",
      "security":[{"oauth2":["orders:read"]}],
      "responses":{"200":{"description":"ok"}}
    }},
    "/orders/write":{"post":{
      "operationId":"writeOrders",
      "security":[{"oauth2":["orders:write"]}],
      "responses":{"204":{"description":"ok"}}
    }}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Init) != 1 {
		t.Fatalf("init actions = %#v, want one shared OAuth token init", result.Script.Init)
	}
	if got := result.Script.Init[0].Params["scope"]; got != "orders:read orders:write" {
		t.Fatalf("OAuth scope = %#v, want union of every consuming operation", got)
	}
	for _, name := range []string{"readOrders", "writeOrders"} {
		action := openAPIActionFull(result, name)
		if action.Auth == nil || action.Auth.Token != "{{var.access_token}}" {
			t.Fatalf("%s auth = %#v, want shared token variable", name, action.Auth)
		}
	}
}

func TestConvertOpenAPIOAuthPasswordGrantIsOptIn(t *testing.T) {
	content := `{
  "openapi": "3.0.3",
  "info": {"title": "OAuth API", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "security": [{"oauth2": ["orders:read"]}],
  "components": {
    "securitySchemes": {
      "oauth2": {
        "type": "oauth2",
        "flows": {
          "password": {
            "tokenUrl": "/oauth/token",
            "scopes": {"orders:read": "Read orders"}
          }
        }
      }
    }
  },
  "paths": {
    "/orders": {
      "get": {
        "operationId": "listOrders",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`
	defaultResult, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert default returned error: %v", err)
	}
	if len(defaultResult.Script.Init) != 0 {
		t.Fatalf("default init actions = %#v, want password grant disabled by default", defaultResult.Script.Init)
	}
	defaultAction := openAPIActionFull(defaultResult, "listOrders")
	if defaultAction.Auth == nil || defaultAction.Auth.Token != "{{secret.OPENAPI_OAUTH2_BEARER_TOKEN}}" {
		t.Fatalf("default action auth = %#v, want bearer token placeholder", defaultAction.Auth)
	}

	enabledResult, err := Convert(ImportRequest{
		Content: content,
		Options: ConvertOptions{
			SynthesizeOAuthPasswordGrant: boolPtr(true),
		},
	})
	if err != nil {
		t.Fatalf("Convert enabled returned error: %v", err)
	}
	if len(enabledResult.Script.Init) != 1 {
		t.Fatalf("enabled init actions = %#v, want password token init", enabledResult.Script.Init)
	}
	init := enabledResult.Script.Init[0]
	if init.Type != "http_oauth2_token" || init.Params["grant_type"] != "password" || init.Params["username"] != "{{env.OPENAPI_OAUTH2_USERNAME}}" || init.Params["password"] != "{{secret.OPENAPI_OAUTH2_PASSWORD}}" {
		t.Fatalf("password init action = %#v", init)
	}
	action := openAPIActionFull(enabledResult, "listOrders")
	if action.Auth == nil || action.Auth.Token != "{{var.access_token}}" {
		t.Fatalf("enabled action auth = %#v, want token init variable", action.Auth)
	}
}

func TestConvertOpenAPIHeadAndOptions(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "HTTP Methods", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "head": {"operationId": "headOrders", "responses": {"200": {}}},
      "options": {"operationId": "optionsOrders", "responses": {"204": {}}},
      "trace": {"operationId": "traceOrders", "responses": {"200": {}}}
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want HEAD and OPTIONS only", result.Script.Actions)
	}
	if options := openAPIActionFull(result, "optionsOrders"); options.Method != "OPTIONS" {
		t.Fatalf("options action = %#v", options)
	}
	if head := openAPIActionFull(result, "headOrders"); head.Method != "HEAD" {
		t.Fatalf("head action = %#v", head)
	}
	if len(result.Stats.SkippedOperations) != 1 || result.Stats.SkippedOperations[0] != "TRACE /orders" {
		t.Fatalf("skipped = %#v, want TRACE unsupported", result.Stats.SkippedOperations)
	}
}

func TestOpenAPISupportMatrixDocumentsReplayReadiness(t *testing.T) {
	matrix := GetSupportMatrix()
	for _, id := range []string{
		"openapi-chaining",
		"openapi-extractors",
		"openapi-security",
		"openapi-bodies-xml",
		"openapi-composition",
		"openapi-refs",
		"openapi-replay-readiness",
	} {
		if !openAPISupportMatrixContains(matrix, id) {
			t.Fatalf("support matrix missing %s: %#v", id, matrix)
		}
	}
	if len(GetSupportMatrixNotes()) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestConvertOpenAPISecuritySelectsFirstFullySupportedORAndAppliesWholeAND(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Security alternatives","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{
    "legacy":{"type":"http","scheme":"ntlm"},
    "basic":{"type":"http","scheme":"basic"},
    "headerKey":{"type":"apiKey","in":"header","name":"X-API-Key"},
    "queryKey":{"type":"apiKey","in":"query","name":"api_key"}
  }},
  "paths":{"/orders":{"get":{
    "operationId":"listOrders",
    "security":[{"legacy":[]},{"queryKey":[],"basic":[],"headerKey":[]}],
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "listOrders")
	if action.Auth == nil || action.Auth.Type != "basic" {
		t.Fatalf("auth = %#v, want basic from second supported OR alternative", action.Auth)
	}
	if action.Headers["X-API-Key"] != "{{secret.OPENAPI_HEADERKEY_API_KEY}}" {
		t.Fatalf("headers = %#v, want header API key from selected AND requirement", action.Headers)
	}
	if !strings.Contains(action.Target, "api_key={{secret.OPENAPI_QUERYKEY_API_KEY}}") {
		t.Fatalf("target = %q, want query API key from selected AND requirement", action.Target)
	}
	if action.Metadata["execution_blocker"] != nil {
		t.Fatalf("metadata = %#v, supported alternative must not be gated", action.Metadata)
	}
}

func TestConvertOpenAPISecurityConflictCreatesDurableBlockedDraft(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Conflict","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{
    "basic":{"type":"http","scheme":"basic"},
    "bearer":{"type":"http","scheme":"bearer"}
  }},
  "paths":{"/orders":{"get":{
    "operationId":"listOrders",
    "security":[{"bearer":[],"basic":[]}],
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "listOrders")
	if action.Auth != nil {
		t.Fatalf("auth = %#v, conflicting schemes must not be partially applied", action.Auth)
	}
	if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
		t.Fatalf("execution blocker = %#v", got)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok || review["source"] != "openapi" || review["resolution"] != "replace_behavior_and_remove_gate" {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok || !containsString(codes, "conflicting_security_schemes") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertOpenAPIProjectsAuthoredCredentialExamplesWithoutTypeCorruption(t *testing.T) {
	const canary = "openapi-canary-secret-9B4F"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Examples","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createSession",
    "parameters":[
      {"name":"X-API-Key","in":"header","schema":{"type":"string","example":"` + canary + `"}},
      {"name":"access_token","in":"query","schema":{"type":"string","default":"` + canary + `"}},
      {"name":"page","in":"query","schema":{"type":"integer","default":7}}
    ],
    "requestBody":{"content":{"application/json":{"example":{
      "username":"alice",
      "password":"` + canary + `",
      "client_secret":"` + canary + `",
      "count":3
    }}}},
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createSession")
	if action.Headers["X-API-Key"] != "{{secret.OPENAPI_X_API_KEY}}" {
		t.Fatalf("credential header = %q", action.Headers["X-API-Key"])
	}
	if !strings.Contains(action.Target, "access_token={{secret.OPENAPI_ACCESS_TOKEN}}") {
		t.Fatalf("target = %q, want governed query credential", action.Target)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
	}
	if body["password"] != "{{secret.OPENAPI_PASSWORD}}" || body["client_secret"] != "{{secret.OPENAPI_CLIENT_SECRET}}" {
		t.Fatalf("credential body projection = %#v", body)
	}
	if count, ok := body["count"].(float64); !ok || count != 3 {
		t.Fatalf("ordinary count changed: %#v", body["count"])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("credential canary leaked in conversion result: %s", encoded)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("validation issues = %v", issues)
	}
	if len(result.Script.DataSources) == 0 || !strings.Contains(result.Script.DataSources[0].InlineData, "\n7\n") {
		t.Fatalf("nonsensitive authored default was not preserved in data source: %#v", result.Script.DataSources)
	}
}

func TestConvertOpenAPIFormatPasswordIsGovernedWithoutSensitiveFieldName(t *testing.T) {
	const canary = "openapi-format-password-canary-74B2"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Schema credential annotation","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createPinSession",
    "requestBody":{"content":{"application/json":{
      "schema":{"type":"object","properties":{
        "pin":{"type":"string","format":"password"},
        "note":{"type":"string"}
      }},
      "example":{"pin":"` + canary + `","note":"unchanged"}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
	action := openAPIActionFull(result, "createPinSession")
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
	}
	if body["pin"] != "{{secret.OPENAPI_PIN}}" || body["note"] != "unchanged" {
		t.Fatalf("body = %#v, want governed pin and unchanged note", body)
	}
	if len(requirements) != 1 || requirements[0].Value != canary {
		t.Fatalf("protected requirements = %#v, want authored format-password value", requirements)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), canary) {
		t.Fatal("format-password canary leaked in the public conversion result")
	}
	if result.SourceReview == nil {
		t.Fatal("source review is missing")
	}
	sourceJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourceJSON), canary) {
		t.Fatal("format-password canary leaked in source review")
	}
}

func TestConvertOpenAPIAuthoredMultipartExampleUsesWireEncodingAndCredentialProjection(t *testing.T) {
	const canary = "openapi-multipart-canary-89C1"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Authored multipart","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createMultipartSession",
    "requestBody":{"content":{"multipart/form-data":{
      "schema":{"type":"object","properties":{
        "pin":{"type":"string","format":"password"},
        "note":{"type":"string"},
        "metadata":{"type":"object"}
      }},
      "example":{
        "pin":"` + canary + `",
        "note":"unchanged",
        "metadata":{"count":1}
      }
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
	action := openAPIActionFull(result, "createMultipartSession")
	if !strings.Contains(action.Headers["Content-Type"], "multipart/form-data; boundary=----apexload-openapi-boundary") ||
		!strings.Contains(action.Body, "Content-Disposition: form-data; name=\"pin\"") ||
		!strings.Contains(action.Body, "{{secret.OPENAPI_PIN}}") ||
		!strings.Contains(action.Body, "{\"count\":1}") ||
		!strings.Contains(action.Body, "\r\n\r\nunchanged\r\n") {
		t.Fatalf("authored multipart action = %#v", action)
	}
	if strings.Contains(action.Body, canary) {
		t.Fatal("authored multipart credential remained in the script")
	}
	if len(requirements) != 1 || requirements[0].Value != canary {
		t.Fatalf("protected requirements = %#v", requirements)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v, want complex multipart encoding gate", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok || !containsString(codes, "unsupported_multipart_complex_encoding") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("validation issues = %v", issues)
	}
}

func TestConvertOpenAPIMediaTypeExamplesSelectFirstSourceEntry(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "json",
			content: `{
  "openapi":"3.0.3",
  "info":{"title":"Ordered JSON examples","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/orders":{"post":{
    "operationId":"createOrder",
    "requestBody":{"content":{"application/json":{
      "examples":{
        "z_first":{"summary":"authored first","value":{"selection":"first"}},
        "a_second":{"value":{"selection":"second"}}
      }
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`,
		},
		{
			name: "yaml",
			content: `openapi: 3.0.3
info:
  title: Ordered YAML examples
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /orders:
    post:
      operationId: createOrder
      requestBody:
        content:
          application/json:
            examples:
              z_first:
                summary: authored first
                value:
                  selection: first
              a_second:
                value:
                  selection: second
      responses:
        "204":
          description: ok
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: test.content})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			action := openAPIActionFull(result, "createOrder")
			var body map[string]any
			if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
				t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
			}
			if body["selection"] != "first" {
				t.Fatalf("body = %#v, want first source-ordered example", body)
			}
			if action.Metadata["execution_blocker"] != nil {
				t.Fatalf("metadata = %#v, source-ordered local value should be executable", action.Metadata)
			}
		})
	}
}

func TestConvertOpenAPISingularMediaTypeExampleRetainsPrecedence(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Singular example precedence","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/orders":{"post":{
    "operationId":"createOrder",
    "requestBody":{"content":{"application/json":{
      "example":{"selection":"singular"},
      "examples":{"first":{"value":{"selection":"plural"}}}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createOrder")
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
	}
	if body["selection"] != "singular" {
		t.Fatalf("body = %#v, want existing singular example behavior", body)
	}
}

func TestConvertOpenAPIMediaTypeExamplesProjectSelectedCredentialsAndScrubAllAlternatives(t *testing.T) {
	const selectedCanary = "openapi-plural-selected-canary-71A9"
	const unselectedCanary = "openapi-plural-unselected-canary-84D2"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Plural credential examples","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createSession",
    "requestBody":{"content":{"application/json":{
      "examples":{
        "selected":{"value":{"username":"alice","password":"` + selectedCanary + `"}},
        "unselected":{"value":{"username":"bob","password":"` + unselectedCanary + `"}}
      }
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
	action := openAPIActionFull(result, "createSession")
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
	}
	if body["username"] != "alice" || body["password"] != "{{secret.OPENAPI_PASSWORD}}" {
		t.Fatalf("body = %#v, want selected example with governed credential", body)
	}
	if len(requirements) != 1 || requirements[0].Value != selectedCanary {
		t.Fatalf("protected requirements = %#v, want only the selected example credential", requirements)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), selectedCanary) || strings.Contains(string(publicJSON), unselectedCanary) {
		t.Fatal("plural example credential leaked in the public conversion result or source review")
	}
}

func TestConvertOpenAPIExternalMediaTypeExampleCreatesBlockedDraft(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"External example","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/orders":{"post":{
    "operationId":"createOrder",
    "requestBody":{"content":{"application/json":{
      "examples":{"remote":{"externalValue":"https://examples.invalid/order.json"}}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createOrder")
	if action.Body != "" {
		t.Fatalf("body = %q, external example must not be treated as an inline value", action.Body)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v, want durable external-example gate", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok || !containsString(codes, "unsupported_media_example_external_value") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertOpenAPIAuthoredMultipartBinaryUsesFileBindingAndBlocksReplay(t *testing.T) {
	const authoredFileValue = "this-is-not-a-safe-file-binding"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Authored binary multipart","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/documents":{"post":{
    "operationId":"createDocument",
    "requestBody":{"content":{"multipart/form-data":{
      "schema":{"type":"object","properties":{
        "document":{"type":"string","format":"binary"}
      }},
      "example":{"document":"` + authoredFileValue + `"}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createDocument")
	if strings.Contains(action.Body, authoredFileValue) ||
		!strings.Contains(action.Body, `filename="document.bin"`) ||
		!strings.Contains(action.Body, "{{file.document}}") {
		t.Fatalf("authored binary multipart body = %q", action.Body)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v, want binary binding gate", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok || !containsString(codes, "multipart_binary_binding_required") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	missing, ok := action.Metadata["missing_file_bindings"].([]string)
	if !ok || len(missing) != 1 || missing[0] != "document" {
		t.Fatalf("missing file bindings = %#v", action.Metadata["missing_file_bindings"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertOpenAPISchemaMultipartBinaryAndComplexValuesRequireReview(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Schema multipart semantics","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/documents":{"post":{
    "operationId":"createDocument",
    "requestBody":{"content":{"multipart/form-data":{
      "schema":{"type":"object","properties":{
        "document":{"type":"string","format":"binary"},
        "metadata":{"type":"object","properties":{"revision":{"type":"integer"}}}
      }}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createDocument")
	if !strings.Contains(action.Body, "{{file.document}}") ||
		!strings.Contains(action.Body, "{{data.openapi.metadata}}") {
		t.Fatalf("schema multipart body = %q", action.Body)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v, want binary/complex gates", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok ||
		!containsString(codes, "multipart_binary_binding_required") ||
		!containsString(codes, "unsupported_multipart_complex_encoding") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertOpenAPISchemaMultipartSanitizesAndGatesInvalidFieldName(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Unsafe multipart schema","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/documents":{"post":{
    "operationId":"createDocument",
    "requestBody":{"content":{"multipart/form-data":{
      "schema":{"type":"object","properties":{
        "label\r\nX-Injected: yes":{"type":"string"}
      }}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createDocument")
	if strings.Contains(action.Body, "\r\nX-Injected: yes") {
		t.Fatalf("schema property injected a multipart header: %q", action.Body)
	}
	if !strings.Contains(action.Body, `Content-Disposition: form-data; name="blocked_field_1"`) {
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
	if !ok || !containsString(reasons, "invalid_multipart_schema_field_name") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked multipart draft has validation issues: %v", issues)
	}
}

func TestConvertOpenAPIMultipartEncodingRequiresReview(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Multipart encoding","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/documents":{"post":{
    "operationId":"createEncodedDocument",
    "requestBody":{"content":{"multipart/form-data":{
      "schema":{"type":"object","properties":{"document":{"type":"object"}}},
      "encoding":{"document":{
        "contentType":"application/vnd.example.document+json",
        "headers":{"X-Part-ID":{"schema":{"type":"string"}}}
      }}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createEncodedDocument")
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("metadata = %#v, want durable review gate", action.Metadata)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	codes, ok := review["reason_codes"].([]string)
	if !ok || !containsString(codes, "unsupported_multipart_encoding") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertOpenAPIComposedCookieDoesNotDisableBodyCredentialProjection(t *testing.T) {
	const canary = "openapi-independent-body-canary-81D4"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Composed cookie credential","version":"1"},
  "components":{"securitySchemes":{
    "cookieAuth":{"type":"apiKey","in":"cookie","name":"session_signature"}
  }},
  "paths":{"/sessions":{"post":{
    "operationId":"createComposedCookieSession",
    "security":[{"cookieAuth":[]}],
    "parameters":[
      {"name":"locale","in":"cookie","schema":{"type":"string"}}
    ],
    "requestBody":{"content":{"application/json":{"example":{
      "username":"alice",
      "password":"` + canary + `"
    }}}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatal(err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)

	action := openAPIActionFull(result, "createComposedCookieSession")
	wantCookie := "locale={{data.openapi.locale}}; session_signature={{secret.OPENAPI_COOKIEAUTH_API_KEY}}"
	if action.Headers["Cookie"] != wantCookie {
		t.Fatalf("composed Cookie = %q, want %q", action.Headers["Cookie"], wantCookie)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, action.Body)
	}
	if body["password"] != "{{secret.OPENAPI_PASSWORD}}" {
		t.Fatalf("independent body credential was not projected: %#v", body)
	}
	if len(requirements) != 1 || requirements[0].Value != canary {
		t.Fatalf("protected requirements = %#v, want body credential only", requirements)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("missing-base-url review gate was not preserved: %#v", action.Metadata)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatal("conversion result disclosed independent body credential")
	}
}

func TestConvertOpenAPIDoesNotTrustAuthoredComposedCredentialHeader(t *testing.T) {
	const canary = "openapi-authored-header-canary-4E92"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Authored composed credential","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/session":{"get":{
    "operationId":"authoredComposedCredential",
    "parameters":[{
      "name":"Authorization",
      "in":"header",
      "schema":{"type":"string","example":"Bearer ` + canary + `-{{secret.EXTRA}}"}
    }],
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatal(err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)

	action := openAPIActionFull(result, "authoredComposedCredential")
	if strings.Contains(action.Headers["Authorization"], canary) ||
		!strings.Contains(action.Headers["Authorization"], "{{secret.") {
		t.Fatalf("authored composed Authorization was not governed as one value: %q", action.Headers["Authorization"])
	}
	if len(requirements) != 1 || !strings.Contains(requirements[0].Value, canary) {
		t.Fatalf("protected requirements = %#v, want authored header credential", requirements)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatal("conversion result disclosed authored composed credential")
	}
}

func TestConvertOpenAPIBlocksSensitiveNonStringExampleAndPreservesJSONType(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Typed secret","version":"1"},
  "paths":{"/typed":{"post":{
    "operationId":"typedSecret",
    "requestBody":{"content":{"application/json":{"example":{
      "password":12345,
      "ordinary":12345
    }}}},
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "typedSecret")
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if _, ok := body["password"].(float64); !ok || body["password"] != float64(0) {
		t.Fatalf("sensitive number = %#v, want disclosure-safe numeric zero", body["password"])
	}
	if body["ordinary"] != float64(12345) {
		t.Fatalf("ordinary numeric value changed: %#v", body["ordinary"])
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("metadata = %#v, want durable import review gate", action.Metadata)
	}
}

func TestConvertOpenAPISharedFinalizerIsDeterministicAndDisclosureSafe(t *testing.T) {
	const canary = "openapi-shared-finalizer-canary-7F3C"
	content := `{
  "openapi":"3.0.3",
  "info":{"title":"Shared finalizer","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createSession",
    "parameters":[
      {"name":"X-API-Key","in":"header","schema":{"type":"string","example":"` + canary + `"}},
      {"name":"access_token","in":"query","schema":{"type":"string","default":"` + canary + `"}}
    ],
    "requestBody":{"content":{"application/json":{"example":{
      "username":"alice",
      "password":"` + canary + `"
    }}}},
    "responses":{"201":{"description":"created"}}
  }}}
}`

	var expected [sha256.Size]byte
	for run := 0; run < 25; run++ {
		result, err := Convert(ImportRequest{Content: content})
		if err != nil {
			t.Fatalf("run %d: Convert returned error: %v", run, err)
		}
		if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
			t.Fatalf("run %d: validation issues = %v", run, issues)
		}
		action := openAPIActionFull(result, "createSession")
		if len(action.HeaderList) != len(action.Headers) {
			t.Fatalf("run %d: header_list = %#v, headers = %#v", run, action.HeaderList, action.Headers)
		}
		headerNames := make([]string, 0, len(action.HeaderList))
		for _, header := range action.HeaderList {
			headerNames = append(headerNames, header.Name)
			if action.Headers[header.Name] != header.Value {
				t.Fatalf("run %d: header occurrence %#v does not match compatibility map", run, header)
			}
		}
		if got := strings.Join(headerNames, ","); got != "Accept,Content-Type,X-API-Key" {
			t.Fatalf("run %d: header order = %q, want deterministic synthesized order", run, got)
		}
		if !containsOpenAPIDiagnostic(result, "source_order_unavailable") {
			t.Fatalf("run %d: diagnostics = %#v, want source_order_unavailable", run, result.Unsupported)
		}
		if !strings.Contains(action.Target, "access_token={{secret.OPENAPI_ACCESS_TOKEN}}") {
			t.Fatalf("run %d: target = %q", run, action.Target)
		}
		result.Script.CreatedAt = time.Time{}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("run %d: marshal result: %v", run, err)
		}
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("run %d: credential canary leaked in conversion result", run)
		}
		digest := sha256.Sum256(encoded)
		if run == 0 {
			expected = digest
		} else if digest != expected {
			t.Fatalf("run %d: normalized conversion digest changed: %x != %x", run, digest, expected)
		}
	}
}

func TestConvertOpenAPISharedFinalizerProjectsFormAndXMLExamples(t *testing.T) {
	const canary = "openapi-structured-canary-D219"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Structured credentials","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{
    "/form":{"post":{
      "operationId":"formLogin",
      "requestBody":{"content":{"application/x-www-form-urlencoded":{"example":{
        "password":"` + canary + `",
        "username":"alice"
      }}}},
      "responses":{"204":{"description":"ok"}}
    }},
    "/xml":{"post":{
      "operationId":"xmlLogin",
      "requestBody":{"content":{"application/xml":{"example":
        "<login xmlns=\"urn:test\"><password>` + canary + `</password><username>alice</username></login>"
      }}},
      "responses":{"204":{"description":"ok"}}
    }}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	form := openAPIActionFull(result, "formLogin")
	if form.Body != "password={{secret.OPENAPI_PASSWORD}}&username=alice" {
		t.Fatalf("form body = %q", form.Body)
	}
	xml := openAPIActionFull(result, "xmlLogin")
	const xmlPrefix = `<login xmlns="urn:test"><password>{{secret.OPENAPI_PASSWORD`
	const xmlSuffix = `}}</password><username>alice</username></login>`
	if !strings.HasPrefix(xml.Body, xmlPrefix) || !strings.HasSuffix(xml.Body, xmlSuffix) {
		t.Fatalf("XML body = %q, want byte-preserving envelope with a governed password", xml.Body)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("structured credential canary leaked in conversion result")
	}
}

func openAPISupportMatrixContains(entries []SupportMatrixEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Area != "" && entry.Status != "" {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}
