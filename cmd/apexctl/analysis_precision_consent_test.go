package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnalysisCLIPrecisionConsentNegotiationAndExactForwarding(t *testing.T) {
	mapping := strings.Replace(analysisCLITestMappingV1, `"version":1`, `"version":3,"precision_policy":"allow_float64_rounding"`, 1)
	acknowledgement := `,"precision_review_sha256":"` + strings.Repeat("a", 64) + `","precision_acknowledgement_sha256":"` + strings.Repeat("b", 64) + `"`
	for _, capability := range []string{``, `,"precision_consent_version":0`, `,"precision_consent_version":null`, `,"precision_consent_version":"1"`, `,"precision_consent_version":2`, `,"precision_consent_version":1,"Precision_Consent_Version":0`, `,"precision_consent_version":1`} {
		for _, tc := range []struct{ action, method, path, body string }{
			{"precision-review", "POST", "/api/v1/analysis/imports/a/precision-review", `{"revision":7,"mapping":` + mapping + `}`},
			{"map", "PUT", "/api/v1/analysis/imports/a/mapping", `{"revision":7,"mapping":` + mapping + acknowledgement + `}`},
			{"commit", "POST", "/api/v1/analysis/imports/a/commit", ""},
			{"save-mapping", "POST", "/api/v1/analysis/mappings", `{"name":"mapping","definition":` + mapping + `}`},
		} {
			t.Run(tc.action+capability, func(t *testing.T) {
				var operations atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" && r.URL.Path == "/api/v1/analysis/imports/a" {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "a", "project_id": "project", "mapping": json.RawMessage(mapping)})
						return
					}
					if r.Method == "GET" && r.URL.Path == "/api/v1/analysis/capabilities" {
						_, _ = io.WriteString(w, `{"imported_identity_version":2`+capability+`}`)
						return
					}
					operations.Add(1)
					body, err := io.ReadAll(r.Body)
					if err != nil || r.Method != tc.method || r.URL.Path != tc.path || string(body) != tc.body {
						t.Errorf("request changed: %s %s %q %v", r.Method, r.URL, body, err)
					}
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				defer server.Close()
				args := []string{tc.action, "--api", server.URL, "--project", "project"}
				if tc.action != "save-mapping" {
					args = append(args, "--id", "a")
				}
				if tc.body != "" {
					file := filepath.Join(t.TempDir(), "request.json")
					if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--file", file)
				}
				var out bytes.Buffer
				err := executeWorkflowCommand(context.Background(), "analysis", args, &out)
				supported := capability == `,"precision_consent_version":1`
				if supported {
					if err != nil || operations.Load() != 1 {
						t.Fatal("supported precision request refused", err, operations.Load())
					}
				} else if !errors.Is(err, errAnalysisCLIPrecisionUnavailable) || operations.Load() != 0 || out.Len() != 0 {
					t.Fatal("unsupported precision request escaped preflight", err, operations.Load(), out.String())
				}
			})
		}
	}
}

func TestAnalysisCLIHistoricalPrecisionMappingIdentity(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  uint32
		valid bool
	}{
		{`{"version":3,"precision_policy":"allow_float64_rounding"}`, 2, true},
		{`{"version":3,"precision_policy":"reject_potential_loss"}`, 2, true},
		{`{"version":3}`, 0, false},
		{`{"version":3,"precision_policy":"unknown"}`, 0, false},
		{`{"version":3,"precision_policy":"reject_potential_loss","Precision_Policy":"allow_float64_rounding"}`, 0, false},
		{`{"version":4}`, 4, true},
		{`{"version":2}`, 2, true},
		{`{"version":1}`, 0, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := analysisCLIHistoricalMappingVersion([]byte(tc.raw), true)
			if (err == nil) != tc.valid || tc.valid && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
