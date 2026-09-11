package openapi

import (
	"strings"
	"testing"
)

func TestConvertPreservesEscapedCredentialExamplesInSourceReview(t *testing.T) {
	tests := []struct {
		name   string
		source string
		canary string
	}{
		{
			name: "json unicode escape",
			source: `{
  "openapi": "3.0.3",
  "info": {"title": "escaped", "version": "1"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {"/orders": {"get": {
    "parameters": [{"name": "X-Api-Key", "in": "header", "schema": {"type": "string"}, "example": "openapi\u002djson"}],
    "responses": {"200": {"description": "ok"}}
  }}}
}`,
			canary: `openapi\u002djson`,
		},
		{
			name: "yaml unicode escape",
			source: `openapi: 3.0.3
info:
  title: escaped
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /orders:
    get:
      parameters:
        - name: X-Api-Key
          in: header
          schema:
            type: string
          example: "openapi\u002dyaml"
      responses:
        "200":
          description: ok`,
			canary: `openapi\u002dyaml`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: test.source})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
				t.Fatalf("source review = %#v", result.SourceReview)
			}
			file := result.SourceReview.SourceFiles[0]
			if file.Redacted || !strings.Contains(file.Content, test.canary) {
				t.Fatalf("source review changed escaped credential: %#v", file)
			}
		})
	}
}

func TestConvertPreservesCredentialYAMLBlockScalarInSourceReview(t *testing.T) {
	const source = `openapi: 3.0.3
info:
  title: block scalar
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /orders:
    get:
      parameters:
        - name: api_key
          in: query
          schema:
            type: string
          example: |-
            openapi-literal
            secret
      responses:
        "200":
          description: ok`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v", result.SourceReview)
	}
	file := result.SourceReview.SourceFiles[0]
	if file.Redacted || file.Content != source {
		t.Fatalf("source review changed block scalar: %#v", file)
	}
}
