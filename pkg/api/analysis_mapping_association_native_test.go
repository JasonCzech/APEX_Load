//go:build analytics_duckdb && cgo

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisNegotiatedAssociationPreservesMeasurementMapping(t *testing.T) {
	for _, kind := range []string{"summary", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
			// This two-row semantic fixture declares its bounded resource cohort.
			// Production defaults and actual admission checks remain unchanged.
			t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "256MiB")
			t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", "128MiB")
			s, store := newTimeslotServer(t)
			query := "SELECT 1700000040+i*60 AS ts, (i+1)::DOUBLE AS value FROM range(2) t(i)"
			in, path, dataset := uploadNativeAnalysisFixture(t, s, query)
			measure := analysis.ParquetMeasure{Value: []string{"value"}, Name: "measure", Kind: kind, Unit: "ms"}
			if kind == "summary" {
				measure.Statistic = "p95"
				measure.WindowSeconds = 60
			} else {
				measure.Histogram = &analysis.ParquetHistogram{Bounds: []string{"+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []analysis.ParquetColumnPath{}}
			}
			mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{measure}}
			current := commitNativeAnalysisMapping(t, s, store, in, path, mapping)
			if err := store.SaveTest(&storage.TestRecord{ID: "mapping-run", ProjectID: "proj-a", Name: "run", Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			// A legacy route table rejects PATCH before its typed metadata writer runs.
			legacyMux := http.NewServeMux()
			legacyMux.HandleFunc("PUT /api/v1/analysis/datasets/{id}/association", s.handleAnalysisAssociation)
			blocked := httptest.NewRecorder()
			legacyMux.ServeHTTP(blocked, httptest.NewRequest(http.MethodPatch, dataset+"/association", strings.NewReader(`{"contract_version":1,"run_id":"mapping-run","revision":1}`)))
			if blocked.Code != http.StatusMethodNotAllowed {
				t.Fatal("legacy method barrier bypassed", blocked.Code)
			}
			request := AnalysisMappingAssociationRequest{ContractVersion: 1, RunID: "mapping-run", Revision: current.Revision}
			original, _ := json.Marshal(current.Mapping)
			for _, version := range []int{0, 2, -1} {
				bad := request
				bad.ContractVersion = version
				response := timeslotRequest(t, s, http.MethodPatch, dataset+"/association", bad)
				if response.Code != http.StatusConflict {
					t.Fatal("unsupported contract", response.Code, response.Body.String())
				}
			}
			bad := request
			bad.Revision--
			if got := timeslotRequest(t, s, http.MethodPatch, dataset+"/association", bad); got.Code != 409 {
				t.Fatal("stale revision", got.Code)
			}
			if err := store.SaveProject(&storage.Project{ID: "proj-b", Name: "Foreign project"}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveTest(&storage.TestRecord{ID: "foreign-run", ProjectID: "proj-b", Name: "foreign", Status: "COMPLETE"}); err != nil {
				t.Fatal(err)
			}
			bad = request
			bad.RunID = "foreign-run"
			if got := timeslotRequest(t, s, http.MethodPatch, dataset+"/association", bad); got.Code != 404 {
				t.Fatal("foreign run", got.Code)
			}
			raw, _ := json.Marshal(request)
			for _, body := range []string{strings.Replace(string(raw), `"contract_version":1`, `"contract_version":1,"contract_version":1`, 1), strings.Replace(string(raw), `"contract_version":1`, `"contract_version":1,"future":true`, 1), string(raw) + " {}"} {
				response := httptest.NewRecorder()
				s.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, dataset+"/association", strings.NewReader(body)))
				if response.Code != 400 {
					t.Fatal("ambiguous contract", response.Code, response.Body.String())
				}
			}
			unchanged, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
			if err != nil || unchanged.Revision != current.Revision || unchanged.RunID != "" {
				t.Fatal("refusal mutated dataset", unchanged, err)
			}
			var response *httptest.ResponseRecorder
			for _, access := range []struct {
				name, project, role, scope string
				allowed                    bool
			}{
				{"viewer", "proj-a", RoleViewer, "write", false},
				{"foreign", "proj-b", RoleEditor, "write", false},
				{"read-scope", "proj-a", RoleEditor, "read", false},
				{"editor", "proj-a", RoleEditor, "write", true},
			} {
				key := "mapping-association-" + access.name
				now := time.Now().UTC()
				if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: key, Name: key, KeyHash: hashAPIKey(key), Active: true, ProjectRoles: map[string][]string{access.project: {access.role}}, Scopes: []string{access.scope}, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPatch, dataset+"/association", strings.NewReader(string(raw)))
				req.SetPathValue("id", in.ID)
				req.Header.Set("X-APEXLoad-API-Key", key)
				response = httptest.NewRecorder()
				authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)(http.HandlerFunc(s.handleAnalysisMappingAssociation)).ServeHTTP(response, req)
				if access.allowed {
					if response.Code != 200 {
						t.Fatal(access.name, response.Code, response.Body.String())
					}
				} else {
					if response.Code != 403 && response.Code != 404 {
						t.Fatal("authorization refusal", access.name, response.Code)
					}
					got, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
					if err != nil || got.Revision != current.Revision || got.RunID != "" {
						t.Fatal("denied request mutated dataset", access.name, got, err)
					}
				}
			}
			var result AnalysisMappingAssociationResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || result.ContractVersion != 1 || result.Import == nil || result.Import.RunID != "mapping-run" || result.Import.Revision != current.Revision+1 {
				t.Fatal(response.Code, response.Body.String())
			}
			after, _ := json.Marshal(result.Import.Mapping)
			if string(original) != string(after) {
				t.Fatal("negotiated association changed mapping", string(original), string(after))
			}
			retained, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
			if err != nil || !reflect.DeepEqual(retained.Mapping, result.Import.Mapping) || !reflect.DeepEqual(retained.Normalized, current.Normalized) || retained.Accepted != current.Accepted {
				t.Fatal("retained evidence changed", retained, err)
			}
			// Legacy clients still receive the historical unwrapped response.
			legacy := timeslotRequest(t, s, http.MethodPut, dataset+"/association", map[string]any{"run_id": "", "revision": retained.Revision})
			var legacyImport storage.AnalysisImport
			if err := json.Unmarshal(legacy.Body.Bytes(), &legacyImport); err != nil || legacy.Code != 200 || legacyImport.ID != in.ID || legacyImport.RunID != "" {
				t.Fatal("legacy contract changed", legacy.Code, legacy.Body.String(), err)
			}
		})
	}
}
