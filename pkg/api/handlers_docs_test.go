package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestAPIDocsHandlersServeEmbeddedGeneratedAssets(t *testing.T) {
	s := &Server{}
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		path        string
		contentType string
		assertBody  func(t *testing.T, body string)
	}{
		{
			name:        "swagger yaml",
			handler:     s.handleGetAPIDocsSwagger,
			path:        "/api/v1/docs/swagger.yaml",
			contentType: "application/yaml; charset=utf-8",
			assertBody: func(t *testing.T, body string) {
				t.Helper()
				var spec struct {
					OpenAPI string `yaml:"openapi"`
					Paths   map[string]struct {
						Put struct {
							RequestBody struct {
								Content map[string]struct {
									Schema struct {
										Type       string         `yaml:"type"`
										Properties map[string]any `yaml:"properties"`
									} `yaml:"schema"`
								} `yaml:"content"`
							} `yaml:"requestBody"`
						} `yaml:"put"`
					} `yaml:"paths"`
				}
				if err := yaml.Unmarshal([]byte(body), &spec); err != nil {
					t.Fatalf("swagger body is not valid yaml: %v", err)
				}
				if spec.OpenAPI != "3.0.3" {
					t.Fatalf("openapi = %q, want 3.0.3", spec.OpenAPI)
				}
				if len(spec.Paths) == 0 {
					t.Fatal("swagger body has no paths")
				}
				annotationPath, ok := spec.Paths["/api/v1/tests/{id}/report/annotations"]
				if !ok {
					t.Fatal("swagger body has no report annotations path")
				}
				jsonBody, ok := annotationPath.Put.RequestBody.Content["application/json"]
				if !ok {
					t.Fatalf("report annotations request content = %#v, want application/json", annotationPath.Put.RequestBody.Content)
				}
				if _, binary := annotationPath.Put.RequestBody.Content["application/octet-stream"]; binary {
					t.Fatal("report annotations request was incorrectly documented as a binary body")
				}
				if jsonBody.Schema.Type != "object" || jsonBody.Schema.Properties["section_comments"] == nil {
					t.Fatalf("report annotations request schema = %#v, want structured object fields", jsonBody.Schema)
				}
			},
		},
		{
			name:        "endpoint catalog json",
			handler:     s.handleGetAPIDocsEndpointCatalog,
			path:        "/api/v1/docs/endpoint-catalog",
			contentType: "application/json; charset=utf-8",
			assertBody: func(t *testing.T, body string) {
				t.Helper()
				var catalog endpointCatalog
				if err := json.Unmarshal([]byte(body), &catalog); err != nil {
					t.Fatalf("catalog body is not valid json: %v", err)
				}
				if catalog.Version != 1 {
					t.Fatalf("catalog version = %d, want 1", catalog.Version)
				}
				if len(catalog.Routes) == 0 {
					t.Fatal("catalog body has no routes")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			tt.handler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			body := strings.TrimSpace(rec.Body.String())
			if body == "" {
				t.Fatal("body is empty")
			}
			tt.assertBody(t, body)
		})
	}
}
