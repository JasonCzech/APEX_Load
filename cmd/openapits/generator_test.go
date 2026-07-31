package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateTransportTypesCoversSchemasAndOperations(t *testing.T) {
	t.Parallel()
	input := []byte(`openapi: 3.0.3
components:
  schemas:
    Choice:
      nullable: true
      oneOf:
        - type: string
        - type: integer
    Dynamic: {}
    Labels:
      type: object
      additionalProperties:
        type: string
    State:
      type: string
      enum: [ready, complete]
    Widget:
      type: object
      properties:
        archived_at:
          type: string
          format: date-time
          nullable: true
        id:
          type: string
        metadata: {}
      required: [id]
paths:
  /widgets/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
    get:
      operationId: getWidget
      parameters:
        - name: verbose
          in: query
          schema:
            type: boolean
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Widget'
        "204":
          description: No content
  /widgets:
    post:
      operationId: createWidget
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Widget'
      responses:
        default:
          content:
            application/json:
              schema: {}
`)

	first, err := generateTransportTypes(input)
	if err != nil {
		t.Fatalf("generate transport types: %v", err)
	}
	second, err := generateTransportTypes(input)
	if err != nil {
		t.Fatalf("generate transport types again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generator output is not deterministic")
	}

	output := string(first)
	for _, expected := range []string{
		`"Choice": ((string | number)) | null;`,
		`"Dynamic": unknown;`,
		`"Labels": Record<string, string>;`,
		`"State": "ready" | "complete";`,
		`"archived_at"?: (string) | null;`,
		`"id": string;`,
		`"metadata"?: unknown;`,
		`"get": operations["getWidget"];`,
		`"post": operations["createWidget"];`,
		`"verbose"?: boolean;`,
		`"application/json": components["schemas"]["Widget"];`,
		`content?: never;`,
		`export type JsonRequestBody<OperationId extends ApiOperationId> =`,
		`export type JsonSuccessResponseBody<`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("generated output does not contain %q", expected)
		}
	}
	if regexp.MustCompile(`\bany\b`).MatchString(output) {
		t.Fatal("generated contract must use unknown rather than any")
	}
}

func TestCollectOperationsRejectsIncompleteOperationCatalog(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		input     string
		wantError string
	}{
		{
			name: "missing operation id",
			input: `openapi: 3.0.3
paths:
  /widgets:
    get:
      responses:
        "200": {description: OK}
`,
			wantError: "does not declare operationId",
		},
		{
			name: "duplicate operation id",
			input: `openapi: 3.0.3
paths:
  /widgets:
    get:
      operationId: list
      responses:
        "200": {description: OK}
  /gadgets:
    get:
      operationId: list
      responses:
        "200": {description: OK}
`,
			wantError: "is shared by",
		},
		{
			name: "missing responses",
			input: `openapi: 3.0.3
paths:
  /widgets:
    get:
      operationId: list
`,
			wantError: "does not declare any responses",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var document openAPIDocument
			if err := yaml.Unmarshal([]byte(testCase.input), &document); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			_, err := collectOperations(&document)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("collectOperations() error = %v, want substring %q", err, testCase.wantError)
			}
		})
	}
}

func TestCommittedTransportTypesAreFreshAndComplete(t *testing.T) {
	root := repositoryRoot(t)
	input, err := os.ReadFile(filepath.Join(root, "docs", "swagger.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	generated, err := generateTransportTypes(input)
	if err != nil {
		t.Fatalf("generate transport types: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, "dashboard", "src", "types", "generated", "openapi.ts"))
	if err != nil {
		t.Fatalf("read committed transport types: %v", err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatal("dashboard OpenAPI transport types are stale; run `npm run contracts:generate` from dashboard/")
	}

	var document openAPIDocument
	if err := yaml.Unmarshal(input, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}
	operations, err := collectOperations(&document)
	if err != nil {
		t.Fatalf("collect OpenAPI operations: %v", err)
	}
	output := string(committed)
	for _, located := range operations {
		pathBinding := quote(located.Method) + ": operations[" + quote(located.Operation.OperationID) + "];"
		if !strings.Contains(output, pathBinding) {
			t.Errorf("generated contract is missing %s %s (%s)", strings.ToUpper(located.Method), located.Path, located.Operation.OperationID)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve generator test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
