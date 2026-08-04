package openapi

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestStructuredXMLExampleSerializesAsXML(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `openapi: 3.0.3
info: {title: XML API, version: "1"}
servers: [{url: https://example.test}]
paths:
  /orders:
    post:
      requestBody:
        content:
          application/xml:
            schema:
              type: object
              xml: {name: order, namespace: "urn:orders", prefix: ord}
            example:
              id: "A&B"
              lines:
                - sku: one
                - sku: two
      responses:
        "201": {description: created}
`})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	for _, want := range []string{`<ord:order xmlns:ord="urn:orders">`, `<id>A&amp;B</id>`, `<lines><sku>one</sku></lines>`, `<lines><sku>two</sku></lines>`, `</ord:order>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("XML body %q does not contain %q", body, want)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Fatalf("structured XML example was serialized as JSON: %s", body)
	}
}

func TestStructuredXMLExampleResolvesSchemaMetadata(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `openapi: 3.0.3
info: {title: XML Metadata API, version: "1"}
servers: [{url: https://example.test}]
paths:
  /orders:
    post:
      requestBody:
        content:
          application/xml:
            schema:
              $ref: '#/components/schemas/Order'
      responses:
        "201": {description: created}
components:
  schemas:
    Order:
      type: object
      xml: {name: order, namespace: "urn:orders", prefix: ord}
      example:
        id: A-1
        customer: Alice
        lines:
          - {sku: one}
          - {sku: two}
      properties:
        id:
          type: string
          xml: {attribute: true}
        customer:
          type: string
          xml: {name: buyer, namespace: "urn:customers", prefix: c}
        lines:
          type: array
          xml: {name: lines, wrapped: true}
          items:
            type: object
            xml: {name: line}
            properties:
              sku: {type: string}
`})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	for _, want := range []string{
		`<ord:order xmlns:ord="urn:orders" id="A-1">`,
		`<c:buyer xmlns:c="urn:customers">Alice</c:buyer>`,
		`<lines><line><sku>one</sku></line><line><sku>two</sku></line></lines>`,
		`</ord:order>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("XML body %q does not contain %q", body, want)
		}
	}
	if result.Script.Actions[0].Metadata["execution_blocker"] == "import_review_required" {
		t.Fatalf("supported XML metadata was unnecessarily review-gated: %#v", result.Script.Actions[0].Metadata)
	}
}

func TestStructuredXMLExampleTopLevelArrayUsesSingleWrapper(t *testing.T) {
	root := schema{
		Type: "array",
		XML:  &xmlObject{Name: "orders", Wrapped: true},
		Items: &schema{
			Type: "object",
			XML:  &xmlObject{Name: "order"},
			Properties: map[string]schema{
				"id": {Type: "string"},
			},
		},
	}
	body, review := renderStructuredXMLExample([]any{
		map[string]any{"id": "A-1"},
		map[string]any{"id": "A-2"},
	}, root)
	if len(review) != 0 {
		t.Fatalf("wrapped root array review codes = %#v", review)
	}
	want := `<orders><order><id>A-1</id></order><order><id>A-2</id></order></orders>`
	if body != want {
		t.Fatalf("wrapped root array = %q, want %q", body, want)
	}

	root.XML.Wrapped = false
	if body, review = renderStructuredXMLExample([]any{"one", "two"}, root); body != "" || len(review) == 0 {
		t.Fatalf("unwrapped root array body=%q review=%#v, want review-only", body, review)
	}
}

func TestStructuredXMLExampleRequiresAuthoredTopLevelElementNames(t *testing.T) {
	t.Run("object root", func(t *testing.T) {
		body, review := renderStructuredXMLExample(
			map[string]any{"id": "A-1"},
			schema{Type: "object", Properties: map[string]schema{"id": {Type: "string"}}},
		)
		if body != "" || !stringSliceContains(review, "unsupported_structured_xml_example") {
			t.Fatalf("unnamed root body=%q review=%#v, want review-only", body, review)
		}
	})

	t.Run("wrapped array item", func(t *testing.T) {
		body, review := renderStructuredXMLExample([]any{map[string]any{"id": "A-1"}}, schema{
			Type: "array",
			XML:  &xmlObject{Name: "orders", Wrapped: true},
			Items: &schema{
				Type:       "object",
				Properties: map[string]schema{"id": {Type: "string"}},
			},
		})
		if body != "" || !stringSliceContains(review, "unsupported_structured_xml_example") {
			t.Fatalf("unnamed array item body=%q review=%#v, want review-only", body, review)
		}
	})
}

func TestStructuredXMLExampleWithoutRootNameGatesImportedAction(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `openapi: 3.0.3
info: {title: Unnamed XML API, version: "1"}
servers: [{url: https://example.test}]
paths:
  /orders:
    post:
      requestBody:
        content:
          application/xml:
            schema:
              type: object
              properties:
                id: {type: string}
            example: {id: A-1}
      responses:
        "201": {description: created}
`})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if action.Body != "" || action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unnamed XML action = body:%q metadata:%#v", action.Body, action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	reasons, _ := review["reason_codes"].([]string)
	if !stringSliceContains(reasons, "unsupported_structured_xml_example") {
		t.Fatalf("review reasons = %#v", review["reason_codes"])
	}
}

func stringSliceContains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func TestStructuredXMLExampleDeduplicatesAttributeNamespaceDeclarations(t *testing.T) {
	root := schema{
		Type: "object",
		XML:  &xmlObject{Name: "order", Namespace: "urn:orders", Prefix: "ord"},
		Properties: map[string]schema{
			"id":   {Type: "string", XML: &xmlObject{Attribute: true, Namespace: "urn:orders", Prefix: "ord"}},
			"kind": {Type: "string", XML: &xmlObject{Attribute: true, Namespace: "urn:orders", Prefix: "ord"}},
		},
	}
	body, review := renderStructuredXMLExample(map[string]any{"id": "A-1", "kind": "retail"}, root)
	if len(review) != 0 {
		t.Fatalf("attribute namespace review codes = %#v", review)
	}
	if strings.Count(body, `xmlns:ord="urn:orders"`) != 1 {
		t.Fatalf("namespace declaration was duplicated: %q", body)
	}
	var document struct{}
	if err := xml.NewDecoder(strings.NewReader(body)).Decode(&document); err != nil {
		t.Fatalf("rendered XML is not well formed: %v; body=%q", err, body)
	}
	if !strings.Contains(body, `ord:id="A-1"`) || !strings.Contains(body, `ord:kind="retail"`) {
		t.Fatalf("namespaced attributes missing from %q", body)
	}
}

func TestStructuredXMLExampleValidatesNamespaceContextAndAttributeNames(t *testing.T) {
	t.Run("inherits bound prefix", func(t *testing.T) {
		root := schema{
			Type: "object",
			XML:  &xmlObject{Name: "root", Namespace: "urn:root", Prefix: "p"},
			Properties: map[string]schema{
				"child": {Type: "string", XML: &xmlObject{Name: "child", Prefix: "p"}},
			},
		}
		body, review := renderStructuredXMLExample(map[string]any{"child": "value"}, root)
		if len(review) != 0 {
			t.Fatalf("inherited prefix review codes = %#v", review)
		}
		if strings.Count(body, `xmlns:p="urn:root"`) != 1 || !strings.Contains(body, `<p:child>value</p:child>`) {
			t.Fatalf("inherited namespace output = %q", body)
		}
		var document struct{}
		if err := xml.NewDecoder(strings.NewReader(body)).Decode(&document); err != nil {
			t.Fatalf("rendered inherited-prefix XML is invalid: %v; body=%q", err, body)
		}
	})

	tests := []struct {
		name  string
		root  schema
		value map[string]any
	}{
		{
			name:  "unbound root prefix",
			root:  schema{Type: "object", XML: &xmlObject{Name: "root", Prefix: "p"}},
			value: map[string]any{},
		},
		{
			name: "conflicting child prefix",
			root: schema{
				Type: "object", XML: &xmlObject{Name: "root", Namespace: "urn:root", Prefix: "p"},
				Properties: map[string]schema{
					"child": {Type: "string", XML: &xmlObject{Name: "child", Namespace: "urn:other", Prefix: "p"}},
				},
			},
			value: map[string]any{"child": "value"},
		},
		{
			name: "duplicate expanded attribute name",
			root: schema{
				Type: "object",
				XML:  &xmlObject{Name: "root"},
				Properties: map[string]schema{
					"first":  {Type: "string", XML: &xmlObject{Name: "id", Attribute: true, Namespace: "urn:id", Prefix: "a"}},
					"second": {Type: "string", XML: &xmlObject{Name: "id", Attribute: true, Namespace: "urn:id", Prefix: "b"}},
				},
			},
			value: map[string]any{"first": "one", "second": "two"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, review := renderStructuredXMLExample(test.value, test.root)
			if body != "" || len(review) == 0 {
				t.Fatalf("body=%q review=%#v, want review-only", body, review)
			}
		})
	}
}
