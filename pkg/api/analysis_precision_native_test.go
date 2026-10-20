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
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisPrecisionReviewCommitAndHistoricalQuery(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "256MiB")
	t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", "128MiB")
	server, rawStore := newTimeslotServer(t)
	store := rawStore.(storage.AnalysisImportStore)
	initial, path, dataset := uploadNativeAnalysisFixture(t, server, "SELECT 1700000040::BIGINT AS ts, 12345678901234567.89::DECIMAL(38,2) AS amount")
	load := func() *storage.AnalysisImport {
		t.Helper()
		in, err := store.GetAnalysisImport(initial.ID)
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	wait := func(want string) *storage.AnalysisImport {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			in := load()
			if in.State != "normalizing" && len(analysisPipelineJobs) == 0 {
				if in.State != want {
					t.Fatalf("state %s, want %s: %s", in.State, want, in.Error)
				}
				return in
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("normalization did not settle")
		return nil
	}
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"amount"}, Name: "amount", Kind: analysis.KindGauge, Unit: "count"}}}
	current := load()
	sourceFiles := current.Files
	if err := rawStore.SaveProject(&storage.Project{ID: "foreign", Name: "Foreign project"}); err != nil {
		t.Fatal(err)
	}
	for _, access := range []struct {
		name, project, role, scope, suffix string
		allowed                            bool
	}{
		{"viewer read", "proj-a", RoleViewer, "read", "/precision-review", true},
		{"foreign project", "foreign", RoleViewer, "read", "/precision-review", false},
		{"viewer cannot save", "proj-a", RoleViewer, "write", "/mapping", false},
	} {
		t.Run(access.name, func(t *testing.T) {
			key := "precision-" + access.name
			now := time.Now().UTC()
			if err := rawStore.SaveServiceAccount(&storage.ServiceAccount{ID: key, Name: key, KeyHash: hashAPIKey(key), Active: true, ProjectRoles: map[string][]string{access.project: {access.role}}, Scopes: []string{access.scope}, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"revision": current.Revision, "mapping": mapping})
			method := http.MethodPost
			handler := server.handleAnalysisPrecisionReview
			if access.suffix == "/mapping" {
				method = http.MethodPut
				handler = server.handleAnalysisMapping
			}
			req := httptest.NewRequest(method, path+access.suffix, strings.NewReader(string(body)))
			req.SetPathValue("id", initial.ID)
			req.Header.Set("X-APEXLoad-API-Key", key)
			response := httptest.NewRecorder()
			authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, rawStore, nil)(http.HandlerFunc(handler)).ServeHTTP(response, req)
			if access.allowed {
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
			} else if response.Code != 403 && response.Code != 404 {
				t.Fatal("authorization refusal", response.Code, response.Body.String())
			}
			if load().Revision != current.Revision {
				t.Fatal("read or refusal mutated import")
			}
		})
	}
	response := timeslotRequest(t, server, http.MethodPost, path+"/precision-review", map[string]any{"revision": current.Revision, "mapping": mapping})
	var legacyReview AnalysisPrecisionReview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &legacyReview) != nil || legacyReview.Version != 1 || legacyReview.Review == nil || len(legacyReview.Review.Risks) != 1 {
		t.Fatalf("preview %d %s", response.Code, response.Body.String())
	}
	if after := load(); after.Revision != current.Revision || after.Mapping != nil {
		t.Fatal("read-only review changed import")
	}
	response = timeslotRequest(t, server, http.MethodPut, path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response = timeslotRequest(t, server, http.MethodPost, path+"/commit", nil); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	current = wait("failed")
	if len(current.NormalizedSHA256) != 0 || current.PartitionManifest != nil || current.Accepted != 0 {
		t.Fatal("unconsented conversion published output")
	}
	mapping.Version = 3
	mapping.PrecisionPolicy = analysis.PrecisionAllowFloat64Rounding
	response = timeslotRequest(t, server, http.MethodPost, path+"/precision-review", map[string]any{"revision": current.Revision, "mapping": mapping})
	var review AnalysisPrecisionReview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.Review == nil || len(review.Review.Risks) != 1 || review.ImportID != initial.ID || review.Revision != current.Revision {
		t.Fatalf("policy review %d %s", response.Code, response.Body.String())
	}
	for _, tc := range []struct {
		name    string
		mapping analysis.ParquetMapping
		ack     string
	}{
		{"foreign acknowledgement", mapping, strings.Repeat("a", 64)},
		{"earlier mapping and revision", mapping, legacyReview.AcknowledgementSHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := timeslotRequest(t, server, http.MethodPut, path+"/mapping", map[string]any{"revision": current.Revision, "mapping": tc.mapping, "precision_review_sha256": review.Review.SHA256, "precision_acknowledgement_sha256": tc.ack})
			if r.Code != 409 || load().Revision != current.Revision {
				t.Fatal("stale acknowledgement mutated import", r.Code, r.Body.String())
			}
		})
	}
	response = timeslotRequest(t, server, http.MethodPut, path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping, "precision_review_sha256": review.Review.SHA256, "precision_acknowledgement_sha256": review.AcknowledgementSHA256})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	saved := load()
	if saved.PrecisionConsentRevision != current.Revision || saved.PrecisionReviewSHA256 != review.Review.SHA256 || saved.PrecisionAcknowledgementSHA256 != review.AcknowledgementSHA256 {
		t.Fatal("consent did not survive durable reload")
	}
	if r := timeslotRequest(t, server, http.MethodPost, path+"/precision-review", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 409 {
		t.Fatal("stale preview accepted", r.Code)
	}
	if response = timeslotRequest(t, server, http.MethodPost, path+"/commit", nil); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	ready := wait("ready")
	if ready.Accepted != 1 || ready.Mapping.Version != 3 || ready.PartitionManifest == nil || ready.PartitionManifest.MappingVersion != 3 || ready.PartitionManifest.Partitions[0].Stats.IdentityVersion != 2 {
		t.Fatalf("mapping/identity proof %+v", ready)
	}
	sealedFiles := append([]storage.AnalysisImportFile(nil), ready.Files...)
	for i := range sealedFiles {
		if sealedFiles[i].SHA256 != review.Review.Risks[0].FileSHA256 {
			t.Fatal("sealed source digest differs from reviewed immutable file")
		}
		sealedFiles[i].SHA256 = sourceFiles[i].SHA256
	}
	if !reflect.DeepEqual(sourceFiles, sealedFiles) {
		t.Fatal("precision normalization changed immutable source metadata")
	}
	response = timeslotRequest(t, server, http.MethodPost, dataset+"/series", analytics.SeriesQuery{Metric: "amount", IdentitySchemaVersion: 2, From: time.Unix(1700000040, 0), To: time.Unix(1700000100, 0), BucketSeconds: 60, Aggregation: "max"})
	var result struct {
		Points []analysis.MetricPoint `json:"points"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != float64(12345678901234567.89) {
		t.Fatalf("consented query %d %s", response.Code, response.Body.String())
	}
	changed := *ready
	changed.PrecisionReviewSHA256 = strings.Repeat("b", 64)
	if _, err := store.SaveAnalysisImport(changed, ready.Revision); err == nil {
		t.Fatal("ready consent was mutable")
	}
	if err := rawStore.SaveTest(&storage.TestRecord{ID: "precision-run", ProjectID: "proj-a", Name: "Precision association", Status: "COMPLETE"}); err != nil {
		t.Fatal(err)
	}
	response = timeslotRequest(t, server, http.MethodPatch, dataset+"/association", AnalysisMappingAssociationRequest{ContractVersion: 1, RunID: "precision-run", Revision: ready.Revision})
	var association AnalysisMappingAssociationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &association) != nil || association.Import == nil {
		t.Fatal("precision reassociation failed", response.Code, response.Body.String())
	}
	associated := association.Import
	if !reflect.DeepEqual(associated.Mapping, ready.Mapping) || associated.PrecisionReviewSHA256 != ready.PrecisionReviewSHA256 || associated.PrecisionAcknowledgementSHA256 != ready.PrecisionAcknowledgementSHA256 || associated.PrecisionConsentRevision != ready.PrecisionConsentRevision {
		t.Fatal("reassociation changed precision evidence")
	}

}
