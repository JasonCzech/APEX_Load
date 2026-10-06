package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisCLIForwardsExactImportedIdentityMappingAndBothComparisonSides(t *testing.T) {
	mapping := `{"version":2,"timestamp":["ts"],"timestamp_encoding":"unix_s","timezone":"UTC","measures":[{"value":["value"],"name":"latency","kind":"observation","unit":"ms"}],"identity":{"source":["nested","source"],"host":["host"],"service":["service"],"dimensions":[{"name":"a/b~c","value":["label"]}]}}`
	selection := `{"source_id":" agent 😀 ","host":"host-a","service":"api","dimensions":{"__proto__":"","a/b~\"c":"x' quoted"},"metric":"latency","from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"aggregation":"p95"}`
	comparison := `{"left":{"dataset_id":"a","source_id":"agent-a","host":"host-a","service":"api-a","dimensions":{"a/b~c":""},"metric":"latency","aggregation":"p95"},"right":{"dataset_id":"b","source_id":"agent-b","host":"host-b","service":"api-b","dimensions":{"__proto__":"","group":"two"},"metric":"latency","aggregation":"p95"},"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"max_lag_seconds":0,"mode":"overlay"}`
	for _, tc := range []struct {
		action, method, path, body string
		args                       []string
	}{
		{"map", "PUT", "/api/v1/analysis/imports/dataset-a/mapping", `{"revision":7,"mapping":` + mapping + `}`, []string{"--id", "dataset-a"}},
		{"query", "POST", "/api/v1/analysis/datasets/dataset-a/series", selection, []string{"--id", "dataset-a"}},
		{"compare", "POST", "/api/v1/analysis/compare", comparison, []string{"--project", "project & one"}},
		{"save-mapping", "POST", "/api/v1/analysis/mappings", `{"name":"identity mapping","definition":` + mapping + `}`, []string{"--project", "project & one"}},
		{"save-comparison", "POST", "/api/v1/analysis/comparisons", `{"name":"both sources","definition":` + comparison + `}`, []string{"--project", "project & one"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/analysis/imports/") {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/imports/"), "project_id": "project & one", "mapping": map[string]any{"version": 2}})
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/capabilities" {
					if r.URL.Query().Get("project_id") != "project & one" {
						t.Error("capability project changed")
					}
					_, _ = w.Write([]byte(`{"imported_identity_version":2}`))
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != tc.method || r.URL.Path != tc.path || !bytes.Equal(body, []byte(tc.body)) {
					t.Errorf("CLI altered approved identity request: %s %s body=%s err=%v", r.Method, r.URL, body, err)
				}
				if tc.action != "map" && tc.action != "query" && r.URL.Query().Get("project_id") != "project & one" {
					t.Error("project identity changed while encoding query")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"state":"ready"}`))
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{tc.action}, tc.args...)
			args = append(args, "--file", file, "--api", server.URL)
			if err := executeWorkflowCommand(context.Background(), "analysis", args, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
