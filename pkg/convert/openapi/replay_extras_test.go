package openapi

import (
	"strings"
	"testing"
)

// TestConvertOpenAPIRawJSONScalarsAndAdditionalProperties covers the
// numeric/boolean "raw template" unquoting, nullable / 3.1 type-array handling,
// and additionalProperties synthesis in JSON request bodies.
func TestConvertOpenAPIRawJSONScalarsAndAdditionalProperties(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Widgets",
		Content: `{
  "openapi": "3.1.0",
  "info": {"title": "Widgets", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "createWidget",
        "requestBody": {"content": {"application/json": {"schema": {
          "type": "object",
          "properties": {
            "name": {"type": ["string", "null"]},
            "qty": {"type": "integer"},
            "active": {"type": "boolean"},
            "meta": {"type": "object", "additionalProperties": {"type": "string"}}
          }
        }}}},
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
	body := result.Script.Actions[0].Body

	// Integer/boolean placeholders must be emitted UNQUOTED so the JSON stays valid.
	if !strings.Contains(body, "{{data.openapi.qty}}") || strings.Contains(body, `"{{data.openapi.qty}}"`) {
		t.Fatalf("integer placeholder should be unquoted: %s", body)
	}
	if !strings.Contains(body, "{{data.openapi.active}}") || strings.Contains(body, `"{{data.openapi.active}}"`) {
		t.Fatalf("boolean placeholder should be unquoted: %s", body)
	}
	// A nullable / type-array string stays a quoted string placeholder.
	if !strings.Contains(body, `"{{data.openapi.name}}"`) {
		t.Fatalf("nullable string placeholder should be quoted: %s", body)
	}
	// additionalProperties produces a representative templated entry.
	if !strings.Contains(body, "additionalProperty") || !strings.Contains(body, "{{data.openapi.additional_value}}") {
		t.Fatalf("additionalProperties not represented: %s", body)
	}
}

// TestConvertOpenAPIXMLRequestBodyFromSchema covers schema-derived XML body
// generation including attribute-vs-element handling and data placeholders.
func TestConvertOpenAPIXMLRequestBodyFromSchema(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "XmlApi",
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "XmlApi", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "createWidgetXml",
        "requestBody": {"content": {"application/xml": {"schema": {
          "type": "object",
          "xml": {"name": "Widget"},
          "properties": {
            "id": {"type": "string"},
            "size": {"type": "integer", "xml": {"attribute": true}}
          }
        }}}},
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if ct := action.Headers["Content-Type"]; !strings.Contains(ct, "xml") {
		t.Fatalf("content type = %q, want xml", ct)
	}
	body := action.Body
	if !strings.Contains(body, "<Widget") {
		t.Fatalf("XML body missing named root element: %s", body)
	}
	if !strings.Contains(body, "<id>{{data.openapi.id}}</id>") {
		t.Fatalf("XML body missing id element placeholder: %s", body)
	}
	// `size` is declared as an XML attribute, so it must render on the element, not as a child.
	if !strings.Contains(body, `size="{{data.openapi.size}}"`) {
		t.Fatalf("XML body missing size attribute placeholder: %s", body)
	}
	if strings.Contains(body, "<size>") {
		t.Fatalf("size should be an attribute, not an element: %s", body)
	}
}
