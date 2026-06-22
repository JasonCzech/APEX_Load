package openapi

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	if create.Headers["Cookie"] != "session_id={{data.openapi.session_id}}" {
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
	if !strings.Contains(session.Body, "password={{data.openapi.password}}") ||
		!strings.Contains(session.Body, "username={{data.openapi.username}}") {
		t.Fatalf("form body = %q, want raw data placeholders", session.Body)
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

func openAPISupportMatrixContains(entries []SupportMatrixEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Area != "" && entry.Status != "" {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}
