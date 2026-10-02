package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRAMLZipRootSelectionAndLibraryRequestFidelity(t *testing.T) {
	entries := map[string]string{
		"api.raml": "#%RAML 1.0 Library\ntypes: {Other: string}\n",
		"project/main.raml": `#%RAML 1.0
title: Actual API
uses:
  common: libraries/common.raml
/orders:
  is: [common.headers]
  post:
    queryParameters:
      status: {example: READY}
    body:
      application/json:
        type: common.Order
`,
		"project/libraries/common.raml": `#%RAML 1.0 Library
traits:
  headers:
    headers:
      client_id: {example: dummy-id}
      client_secret: {example: DO-NOT-RETURN-THIS}
      source: {example: CRM}
types:
  Order:
    example: !include ../examples/order.json
`,
		"project/examples/order.json": `{"id":"ORDER-1"}`,
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml?filename=export.zip", bytes.NewReader(archive.Bytes()))
	req.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "DO-NOT-RETURN-THIS") {
		t.Fatal("source credential example disclosed")
	}
	var result struct {
		Script             dsl.Script                 `json:"script"`
		SecretRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Script.Name != "main" || len(result.Script.Actions) != 1 {
		t.Fatal("wrong API root selected")
	}
	action := result.Script.Actions[0]
	if action.Target != "/orders?status=READY" || action.Headers["source"] != "CRM" || action.Headers["client_secret"] != "{{secret.RAML_CLIENT_SECRET}}" || action.Body != `{"id":"ORDER-1"}` {
		t.Fatal("archive request fidelity lost through API")
	}
	if len(result.SecretRequirements) != 1 {
		t.Fatal("missing credential binding review")
	}
}

func TestCustomerRAMLArchivesThroughImportAPI(t *testing.T) {
	dir := os.Getenv("APEXLOAD_RAML_FIDELITY_ARCHIVES")
	if dir == "" {
		t.Skip("set APEXLOAD_RAML_FIDELITY_ARCHIVES to evaluate private source archives")
	}
	cases := []struct {
		name    string
		actions int
	}{
		{"customer-crm-billing-eapi-1.0.32-raml.zip", 9},
		{"cc-mule-orders-api-1.0.0-raml.zip", 1},
		{"customer-profile-crm-eapi-1.0.75-raml.zip", 13},
	}
	server := NewServer(ServerConfig{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml?filename="+tc.name, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/octet-stream")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("HTTP status=%d", response.Code)
			}
			var result struct {
				Script dsl.Script `json:"script"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Script.Actions) != tc.actions {
				t.Fatalf("actions=%d want=%d", len(result.Script.Actions), tc.actions)
			}
			t.Logf("HTTP 200, preserved %d operations", len(result.Script.Actions))
		})
	}
}
