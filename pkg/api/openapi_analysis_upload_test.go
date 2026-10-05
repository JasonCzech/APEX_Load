package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestOpenAPIRawUploadInferenceRecognizesOwnedBodyReader(t *testing.T) {
	for _, test := range []struct {
		name string
		call string
		raw  bool
	}{
		{"direct body read", "io.ReadAll(r.Body)", true},
		{"owned body read", "readAnalysisUploadBody(r.Context(), owner, r.Body)", true},
		{"owned non-request input", "readAnalysisUploadBody(r.Context(), owner, file)", false},
		{"body in another argument", "readAnalysisUploadBody(r.Body, owner, file)", false},
		{"unrecognized helper", "readOtherBody(r.Context(), owner, r.Body)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "handler.go", "package fixture\nfunc handleUpload() { "+test.call+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			functions := map[string]*ast.FuncDecl{"handleUpload": file.Decls[0].(*ast.FuncDecl)}
			analysis := newHandlerContractAnalysis()
			inspectHandlerContract("handleUpload", functions, nil, nil, nil, analysis, map[string]bool{}, 0)
			if analysis.rawRequest != test.raw || analysis.jsonRequest || analysis.multipartRequest {
				t.Fatalf("raw=%v JSON=%v multipart=%v, want only raw=%v", analysis.rawRequest, analysis.jsonRequest, analysis.multipartRequest, test.raw)
			}
			body := inferredRequestBody(discoveredRoute{Method: http.MethodPut}, analysis)
			if !test.raw {
				if body != nil {
					t.Fatalf("non-request read inferred a request body: %#v", body)
				}
				return
			}
			if body == nil || !body.Required || len(body.Content) != 1 || body.Content["application/octet-stream"].Schema.Format != "binary" {
				t.Fatalf("raw request must remain a required binary body: %#v", body)
			}
			if inferredRequestBody(discoveredRoute{Method: http.MethodGet}, analysis) != nil {
				t.Fatal("shared read handler inherited a mutation request body")
			}
		})
	}
}

func TestGeneratedOpenAPIAnalysisUploadRetainsBinaryChunkContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc.Paths["/api/v1/analysis/imports/{id}/files/{file_id}"]["put"]
	if operation.RequestBody == nil || !operation.RequestBody.Required || len(operation.RequestBody.Content) != 1 {
		t.Fatalf("chunk upload lost its required raw-body contract: %#v", operation.RequestBody)
	}
	schema := operation.RequestBody.Content["application/octet-stream"].Schema
	if schema.Type != "string" || schema.Format != "binary" {
		t.Fatalf("chunk body must preserve binary bytes, got %#v", schema)
	}
	for _, parameter := range operation.Parameters {
		if parameter.In == "query" && parameter.Name == "offset" {
			if parameter.Schema.Type != "integer" {
				t.Fatalf("upload offset type=%q, want integer", parameter.Schema.Type)
			}
			return
		}
	}
	t.Fatal("chunk upload lost its durable cursor query parameter")
}
