package api

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

const (
	swaggerOutputPath      = "../../docs/swagger.yaml"
	swaggerEmbedOutputPath = "apidocs/swagger.yaml"
	catalogOutputPath      = "../../docs/api-endpoint-catalog.json"
	catalogEmbedOutputPath = "apidocs/api-endpoint-catalog.json"
)

// TestSwaggerYAMLIsFresh enforces that docs/swagger.yaml is the exact output of the
// generator over the current routing layer. If routes change, run:
//
//	go generate ./pkg/api
func TestSwaggerYAMLIsFresh(t *testing.T) {
	got, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	for _, path := range []string{swaggerOutputPath, swaggerEmbedOutputPath} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; run `go generate ./pkg/api` to regenerate it", path)
		}
	}
}

// TestEndpointCatalogJSONIsFresh enforces that docs/api-endpoint-catalog.json is
// regenerated with the OpenAPI spec after route changes.
func TestEndpointCatalogJSONIsFresh(t *testing.T) {
	got, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatalf("GenerateEndpointCatalogJSON: %v", err)
	}
	for _, path := range []string{catalogOutputPath, catalogEmbedOutputPath} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; run `go generate ./pkg/api` to regenerate it", path)
		}
	}
}

// TestGeneratedSwaggerIsValid sanity-checks the generated spec: it parses as YAML,
// declares OpenAPI 3.0.x, has a non-empty paths object, and every operation carries
// an operationId and at least one response.
func TestGeneratedSwaggerIsValid(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}

	var spec struct {
		OpenAPI string `yaml:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string                 `yaml:"operationId"`
			Responses   map[string]interface{} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}
	if spec.OpenAPI != "3.0.3" {
		t.Fatalf("openapi version = %q, want 3.0.3", spec.OpenAPI)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("generated spec has no paths")
	}

	seenOpIDs := map[string]string{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if op.OperationID == "" {
				t.Errorf("%s %s: missing operationId", method, path)
			}
			if len(op.Responses) == 0 {
				t.Errorf("%s %s: missing responses", method, path)
			}
			if prev, dup := seenOpIDs[op.OperationID]; dup {
				t.Errorf("duplicate operationId %q on %s %s and %s", op.OperationID, method, path, prev)
			}
			seenOpIDs[op.OperationID] = method + " " + path
		}
	}
}

func TestGeneratedSwaggerMarksScriptDebugAliasesDeprecated(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}

	var spec struct {
		Paths map[string]map[string]struct {
			Deprecated bool `yaml:"deprecated"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	if !spec.Paths["/api/v1/script-debug/sessions/{session_id}"]["get"].Deprecated {
		t.Fatal("script-debug session alias is not marked deprecated")
	}
	if !spec.Paths["/api/v1/script-debug/sessions/{session_id}/step"]["post"].Deprecated {
		t.Fatal("script-debug step alias is not marked deprecated")
	}
	if spec.Paths["/api/v1/replay/sessions/{session_id}"]["get"].Deprecated {
		t.Fatal("canonical replay session route should not be deprecated")
	}
}

func TestGeneratedEndpointCatalogIsValid(t *testing.T) {
	data, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatalf("GenerateEndpointCatalogJSON: %v", err)
	}

	var catalog endpointCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("generated endpoint catalog is not valid JSON: %v", err)
	}
	if catalog.Version != 1 {
		t.Fatalf("catalog version = %d, want 1", catalog.Version)
	}
	if catalog.Source != "server.go" {
		t.Fatalf("catalog source = %q, want server.go", catalog.Source)
	}
	if len(catalog.Routes) == 0 {
		t.Fatal("generated endpoint catalog has no routes")
	}

	seen := map[string]bool{}
	for _, route := range catalog.Routes {
		key := route.Method + " " + route.Path
		if route.Method == "" {
			t.Errorf("%s: missing method", key)
		}
		if route.Path == "" {
			t.Errorf("%s: missing path", key)
		}
		if route.OperationID == "" {
			t.Errorf("%s: missing operationId", key)
		}
		if route.Summary == "" {
			t.Errorf("%s: missing summary", key)
		}
		if route.Category == "" {
			t.Errorf("%s: missing category", key)
		}
		if route.Access == "" {
			t.Errorf("%s: missing access text", key)
		}
		if route.Public == route.Authenticated {
			t.Errorf("%s: public/authenticated flags should be inverse, got public=%t authenticated=%t", key, route.Public, route.Authenticated)
		}
		if seen[key] {
			t.Errorf("duplicate catalog route %s", key)
		}
		seen[key] = true
	}

	for _, required := range []string{
		"GET /api/v1/execution-plans",
		"GET /api/v1/capacity/timeslots",
		"POST /api/v1/auth/users/onboard",
		"POST /api/v1/auth/users/{id}/revoke-sessions",
		"GET /api/v1/downloads/recording-agent/latest",
	} {
		if !seen[required] {
			t.Errorf("catalog missing required route %s", required)
		}
	}
}
