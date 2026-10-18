package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSampleImportResponseNegotiationRetainsUsableDraft(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	for _, format := range []string{"", "canonical-v1", "future-version"} {
		t.Run(format, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample?project_id="+storage.DefaultProjectID+"&response_format="+format,
				strings.NewReader(`{"name":"Negotiated import","format":"curl","content":"curl https://example.test/orders"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			var scripts []dsl.Script
			if err := json.Unmarshal(wire["scripts"], &scripts); err != nil || len(scripts) != 1 {
				t.Fatalf("scripts=%s error=%v", wire["scripts"], err)
			}
			if len(scripts[0].Actions) != 1 || scripts[0].Actions[0].Method != http.MethodGet {
				t.Fatalf("lost imported request: %+v", scripts[0].Actions)
			}
			if format == "canonical-v1" {
				if string(wire["response_format"]) != `"canonical-v1"` || string(wire["primary_script_index"]) != "0" || wire["script"] != nil {
					t.Fatal("canonical draft was duplicated or primary selection was lost")
				}
				var facts map[string]json.RawMessage
				if err := json.Unmarshal(wire["result"], &facts); err != nil {
					t.Fatal(err)
				}
				if facts["script"] != nil || facts["scripts"] != nil {
					t.Fatal("converter repeated the canonical draft")
				}
			} else if wire["script"] == nil || wire["response_format"] != nil {
				t.Fatal("legacy or unknown-format client lost the legacy response")
			}
		})
	}
}

func TestEnterpriseArchiveCanonicalResponseReferencesNativeSalesforcePreview(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	for _, format := range []string{"", "canonical-v1"} {
		t.Run(format, func(t *testing.T) {
			script := &dsl.Script{Name: "Native archive", ProjectID: storage.DefaultProjectID, Protocol: "http",
				GeneratedBy: "apexload-salesforce-builder", GeneratedFrom: "enterprise_script_archive+har",
				Actions: []dsl.Action{{Type: "request", Method: http.MethodGet, Target: "https://acme.my.salesforce.com/services/data/v60.0/query"}},
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "salesforce", Preserved: map[string]any{
					"salesforce_review": &salesforce.Review{Detected: true, NativePreview: &salesforce.NativePreview{}},
				}}},
			}
			converted := &enterprisearchive.ConvertResult{Script: script}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?response_format="+format, nil)
			response := server.newEnterpriseArchiveImportResponse(request, converted, enterprisearchive.RuntimeSettingMigrationPolicy{}, false)
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Scripts []dsl.Script `json:"scripts"`
				Summary struct {
					SalesforceReview *salesforce.Review `json:"salesforce_review"`
				} `json:"summary"`
			}
			if err = json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Scripts) != 1 || wire.Scripts[0].GeneratedFrom != "enterprise_script_archive+har" || wire.Summary.SalesforceReview == nil || wire.Summary.SalesforceReview.NativePreview == nil {
				t.Fatalf("archive native draft or review lost: %s", encoded)
			}
			copies := strings.Count(string(encoded), `"generated_by":"apexload-salesforce-builder"`)
			if format == "canonical-v1" {
				if response["salesforce_preview_script_index"] != 0 || wire.Summary.SalesforceReview.NativePreview.Script != nil || copies != 1 {
					t.Fatalf("canonical archive preview duplicated or lost: copies=%d %s", copies, encoded)
				}
			} else if wire.Summary.SalesforceReview.NativePreview.Script == nil || copies < 2 {
				t.Fatal("legacy archive preview contract changed")
			}
		})
	}
}
