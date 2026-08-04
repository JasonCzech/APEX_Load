package openapi

import (
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
