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
	"sync/atomic"
	"testing"
)

func TestAnalysisCLIHistoricalProtectedProjectionRefusesQueryAndCommitBeforeCapability(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, role := range []string{"timestamp", "value", "name_column", "sample_count_column", "sample_sum_column", "bound_column", "count_columns", "source", "host", "service", "dimension"} {
			for _, action := range []string{"query", "commit"} {
				t.Run(role+"/"+action+string(rune('0'+version)), func(t *testing.T) {
					m := map[string]any{"version": version, "timestamp": []string{"ts"}, "measures": []any{map[string]any{"value": []string{"value"}, "name": "v"}}}
					switch role {
					case "timestamp":
						m["timestamp"] = []string{"password"}
					case "value":
						m["measures"] = []any{map[string]any{"value": []string{"api", "key"}}}
					case "name_column", "sample_count_column", "sample_sum_column":
						m["measures"] = []any{map[string]any{"value": []string{"value"}, role: []string{"client_secret"}}}
					case "bound_column":
						m["measures"] = []any{map[string]any{"value": []string{"value"}, "histogram": map[string]any{role: []string{"client_secret"}}}}
					case "count_columns":
						m["measures"] = []any{map[string]any{"value": []string{"value"}, "histogram": map[string]any{role: [][]string{{"bucket"}, {"api", "key"}}}}}
					case "source", "host", "service":
						m["identity"] = map[string]any{role: []string{"password"}}
					case "dimension":
						m["identity"] = map[string]any{"dimensions": []any{map[string]any{"name": "transaction", "value": []string{"api", "key"}}}}
					}
					var metadata, capabilities, operations atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/v1/analysis/imports/dataset" && r.Method == http.MethodGet {
							metadata.Add(1)
							_ = json.NewEncoder(w).Encode(map[string]any{"id": "dataset", "project_id": "p", "mapping": m})
							return
						}
						if strings.Contains(r.URL.Path, "capabilities") {
							capabilities.Add(1)
							_, _ = io.WriteString(w, `{}`)
							return
						}
						operations.Add(1)
						_, _ = io.WriteString(w, `{}`)
					}))
					defer server.Close()
					args := []string{action, "--id", "dataset", "--api", server.URL}
					if action == "query" {
						file := filepath.Join(t.TempDir(), "query.json")
						if err := os.WriteFile(file, []byte(`{"metric":"v"}`), 0600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--file", file)
					}
					if err := executeWorkflowCommand(context.Background(), "analysis", args, &bytes.Buffer{}); err == nil || metadata.Load() != 1 || capabilities.Load() != 0 || operations.Load() != 0 {
						t.Fatal("protected historical projection reached requested operation", err, metadata.Load(), capabilities.Load(), operations.Load())
					}
				})
			}
		}
	}
}

func TestAnalysisCLIHistoricalProjectionRetainsSafeOmittedLegacyPathsAndExactRequest(t *testing.T) {
	body := `{"metric":"password","aggregation":"avg"}`
	var operations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/imports/dataset" {
			_, _ = io.WriteString(w, `{"id":"dataset","project_id":"p","mapping":{"version":1,"legacy_annotation":"retained","measures":[{"name":"password","legacy_field":true}]}}`)
			return
		}
		if strings.Contains(r.URL.Path, "capabilities") {
			t.Error("safe legacy request required new capability")
			return
		}
		operations.Add(1)
		if r.URL.Path != "/api/v1/analysis/datasets/dataset/series" || r.Method != http.MethodPost {
			t.Error("legacy workflow changed", r.URL, r.Method)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != body {
			t.Error("request bytes changed", err, string(raw))
		}
		_, _ = io.WriteString(w, `{"points":[]}`)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "query.json")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"query", "--id", "dataset", "--file", file, "--api", server.URL}, &bytes.Buffer{}); err != nil || operations.Load() != 1 {
		t.Fatal("valid legacy request refused", err, operations.Load())
	}
}

func TestAnalysisCLIHistoricalProjectionRefusesAmbiguousOrExcessiveMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"Timestamp":["password"]}`,
		`{"version":1,"timestamp":["ts"],"timestamp":["password"]}`,
		`{"version":1,"measures":[{"Value":["password"]}]}`,
		`{"version":1,"measures":[{"value":["v"],"value":["password"]}]}`,
		`{"version":1,"timestamp":["\ud800"]}`,
		`{"version":1,"measures":{}}`,
		`{"version":2,"identity":{"Source":["password"]}}`,
		`{"version":2,"identity":{"dimensions":[{"name":"n","Value":["password"]}]}}`,
		`{"version":1,"timestamp":["` + strings.Repeat("x", 257) + `"]}`,
		`{"version":1,"timestamp":[` + strings.Repeat(`"v",`, 16) + `"v"]}`,
	} {
		if _, err := analysisCLIMappingVersion([]byte(raw), false); err == nil {
			t.Fatal("ambiguous/excessive metadata admitted", raw)
		}
	}
	if _, err := analysisCLIMappingVersion([]byte(`{"version":1,"timestamp":null,"measures":null}`), false); err != nil {
		t.Fatal("historical nil paths unavailable", err)
	}
}

func TestAnalysisCLISafeRemapRepairsProtectedHistoricalMappingWithoutReadingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		metadata     string
		mapping      string
		capability   string
		wantErr      bool
		capabilities int32
	}{
		{"legacy repair", `{"version":1,"timestamp":["password"]}`, analysisCLITestMappingV1, `{}`, false, 0},
		{"identity repair", `{"version":2,"timestamp":["password"],"identity":{"source":["client_secret"]}}`, analysisCLITestMappingV1, `{"imported_identity_version":2}`, false, 1},
		{"identity peer refusal", `{"version":2,"timestamp":["password"]}`, analysisCLITestMappingV1, `{"imported_identity_version":1}`, true, 1},
		{"protected replacement refusal", `{"version":1,"timestamp":["password"]}`, strings.Replace(analysisCLITestMappingV1, `["ts"]`, `["password"]`, 1), `{}`, true, 0},
		{"ambiguous version refusal", `{"version":2,"Version":1,"timestamp":["password"]}`, analysisCLITestMappingV1, `{"imported_identity_version":2}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capabilities, operations atomic.Int32
			body := "{\n  \"revision\": 7, \"mapping\": " + tc.mapping + "\n}\n"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/imports/dataset":
					_, _ = io.WriteString(w, `{"id":"dataset","project_id":"p","state":"mapped","mapping":`+tc.metadata+`}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/capabilities":
					capabilities.Add(1)
					if r.URL.Query().Get("project_id") != "p" {
						t.Error("remapping capability used a different project")
					}
					_, _ = io.WriteString(w, tc.capability)
				default:
					operations.Add(1)
					if r.Method != http.MethodPut || r.URL.Path != "/api/v1/analysis/imports/dataset/mapping" {
						t.Error("remapping attempted to read analytical evidence", r.Method, r.URL)
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil || string(raw) != body {
						t.Error("remapping changed replacement request bytes", err, string(raw))
					}
					_, _ = io.WriteString(w, `{"state":"mapped"}`)
				}
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "mapping.json")
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			err := executeWorkflowCommand(context.Background(), "analysis", []string{"map", "--id", "dataset", "--project", "p", "--file", file, "--api", server.URL}, &bytes.Buffer{})
			wantOperations := int32(1)
			if tc.wantErr {
				wantOperations = 0
			}
			if (err != nil) != tc.wantErr || operations.Load() != wantOperations || capabilities.Load() != tc.capabilities {
				t.Fatal("remapping repair/fence mismatch", err, operations.Load(), capabilities.Load())
			}
		})
	}
}
