package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedRecordingFixtureFunctions(t *testing.T, source string) map[string][]*ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package api\n"+source, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string][]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions[fn.Name.Name] = append(functions[fn.Name.Name], fn)
		}
	}
	return functions
}

func TestSharedRecordingInferenceSkipsUnrelatedFixtures(t *testing.T) {
	for _, source := range []string{
		`func (s *Server) handle(w W, r R) { writeJSON(w, 200, "ok") }`,
		`func (s *Server) handle(w W, r R) { s.otherService().Handle(w, r) }`,
		`func (s *Server) handle(w W, r R) { s.recordingHTTPService().Handle(w, r); audit() }`,
	} {
		functions := sharedRecordingFixtureFunctions(t, source)
		original := functions["handle"][0]
		if err := addSharedRecordingHandlerDefinitions(filepath.Join(t.TempDir(), "api"), functions, map[string]ast.Expr{}, map[*ast.FuncDecl]map[int]bool{}); err != nil {
			t.Fatalf("unrelated fixture requires recording runtime: %v", err)
		}
		if functions["handle"][0] != original {
			t.Fatal("unrelated handler was replaced")
		}
	}
}

func TestSharedRecordingInferenceRequiresForwardedImplementation(t *testing.T) {
	for _, tc := range []struct{ name, shared, wantError string }{
		{"missing directory", "", "parse shared recording handlers"},
		{"wrong package", "package other", "recordbridge package is missing"},
		{"missing method", "package recordbridge", "expected one Service.Handle implementation"},
		{"wrong receiver", "package recordbridge\nfunc (s *Other) Handle(w W, r R) {}", "expected one Service.Handle implementation"},
		{"service method", "package recordbridge\ntype Payload struct { Value string }\nfunc (s *Service) Handle(w W, r R) { WriteJSON(w, 201, Payload{}) }", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.shared != "" {
				if err := os.Mkdir(filepath.Join(root, "recordbridge"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "recordbridge", "service.go"), []byte(tc.shared), 0600); err != nil {
					t.Fatal(err)
				}
			}
			functions := sharedRecordingFixtureFunctions(t, `func (s *Server) handle(w W, r R) { s.recordingHTTPService().Handle(w, r) }`)
			original := functions["handle"][0]
			types := map[string]ast.Expr{}
			err := addSharedRecordingHandlerDefinitions(filepath.Join(root, "api"), functions, types, map[*ast.FuncDecl]map[int]bool{})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if functions["handle"][0] == original || functions["handle"][0].Name.Name != "Handle" || types["Payload"] == nil {
				t.Fatal("shared handler body or response type was not followed")
			}
		})
	}
}

func TestSharedRecordingInferencePreservesAdapterAnnotations(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "recordbridge"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recordbridge", "service.go"), []byte("package recordbridge\n// OpenAPI-Responses: 409\nfunc (s *Service) Handle(w W, r R) {}"), 0600); err != nil {
		t.Fatal(err)
	}
	functions := sharedRecordingFixtureFunctions(t, `func (s *Server) handle(w W, r R) { s.recordingHTTPService().Handle(w, r) }`)
	statuses := map[*ast.FuncDecl]map[int]bool{functions["handle"][0]: {500: true, 503: true}}
	if err := addSharedRecordingHandlerDefinitions(filepath.Join(root, "api"), functions, map[string]ast.Expr{}, statuses); err != nil {
		t.Fatal(err)
	}
	forwarded := statuses[functions["handle"][0]]
	if !forwarded[409] || !forwarded[500] || !forwarded[503] {
		t.Fatalf("adapter callback statuses lost: %v", forwarded)
	}
	if statuses[functions["Handle"][0]][500] {
		t.Fatal("adapter annotation contaminated another shared call site")
	}
}

func TestSharedRecordingOpenAPIKeepsMarkerValidationAndCallbackContracts(t *testing.T) {
	routes, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := inferRouteContracts("server.go", routes)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"/api/v1/browser-recorder/sessions/{id}", "/api/v1/recorder/{id}"} {
		for _, suffix := range []string{"/notes", "/transactions/start", "/transactions/end"} {
			contract := contracts["POST "+prefix+suffix]
			if contract.RequestBody == nil {
				t.Fatalf("marker request disappeared for %s%s", prefix, suffix)
			}
			schema := contract.RequestBody.Content["application/json"].Schema
			if schema.Properties["name"].Type != "string" || schema.Properties["text"].Type != "string" {
				t.Fatalf("marker request became opaque for %s%s", prefix, suffix)
			}
			if strings.Contains(prefix, "browser-recorder") && schema.Properties["at_step_seq"].Type != "integer" {
				t.Fatal("browser marker step position disappeared")
			}
			for _, status := range []string{"200", "400", "404", "413"} {
				if _, ok := contract.Responses[status]; !ok {
					t.Fatalf("marker status %s disappeared for %s%s", status, prefix, suffix)
				}
			}
		}
	}
	for _, path := range []string{"/api/v1/scripts/{id}/validate", "/api/v1/scripts/validate"} {
		schema := contracts["POST "+path].Responses["200"].Content["application/json"].Schema
		if schema.Properties["valid"].Type != "boolean" || schema.Properties["authoring_health"].Properties["inputs"].Items == nil || schema.Properties["preflight"].Properties["browser_required"].Type != "boolean" {
			t.Fatalf("shared validation response became opaque for %s", path)
		}
	}
	for suffix, expected := range map[string][]string{"convert": {"422"}, "verify": {"500", "503"}} {
		contract := contracts["POST /api/v1/browser-recorder/sessions/{id}/"+suffix]
		for _, status := range expected {
			if _, ok := contract.Responses[status]; !ok {
				t.Fatalf("callback response %s disappeared for %s", status, suffix)
			}
		}
	}
}
