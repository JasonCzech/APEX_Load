//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNativeAnalysisIdentityProtectedNewAndPersistedMappingsRefuseBeforeArtifactAdmission(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	s, raw := newTimeslotServer(t)
	if err := ensureAnalysisBackend(raw); err != nil {
		t.Fatal(err)
	}
	store := raw.(storage.AnalysisImportStore)
	const secret = "protected-source-value-not-authorized-for-series"
	original := []byte("PAR1" + secret + "PAR1")
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000091", ProjectID: "proj-a", Name: "historical protected identity", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "00000000-0000-0000-0000-000000000092", RelativePath: "protected.parquet", Size: int64(len(original)), Uploaded: int64(len(original))}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	originalKey := analysisObjectKey(in.ProjectID, in.ID, "original-"+in.Files[0].ID+".parquet")
	originalHash, err := analysisObjectWrite(t.Context(), originalKey, bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: []string{"password"}}}
	w := timeslotRequest(t, s, http.MethodPut, "/api/v1/analysis/imports/"+in.ID+"/mapping", map[string]any{"revision": in.Revision, "mapping": mapping})
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), secret) {
		t.Fatal("new protected mapping reached publication/disclosed value", w.Code, w.Body.String())
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current == nil || current.State != "uploading" || current.Revision != in.Revision || current.Mapping != nil {
		t.Fatal("refused mapping mutated upload", current, err)
	}
	// Persist a historical mapping through the real existing storage protocol.
	// Opaque bytes are deliberate: protected-path admission must refuse before
	// attempting to inspect or read any normalized artifact, valid or otherwise.
	in.Mapping = &mapping
	in.State = "mapped"
	in.Files[0].SHA256 = originalHash
	in, err = store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	attempts := raw.(storage.AnalysisAttemptStore)
	in, a, err := attempts.ClaimAnalysisImportAttempt(context.Background(), in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	in.NormalizedSHA256 = map[string]string{}
	in.Accepted = 1
	for _, key := range in.Normalized {
		digest, err := analysisObjectWrite(t.Context(), key, bytes.NewReader(original))
		if err != nil {
			t.Fatal(err)
		}
		in.NormalizedSHA256[key] = digest
	}
	if err := attempts.ConfirmAnalysisAttemptWritersQuiesced(t.Context(), in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	digest, err := storage.AnalysisAttemptResultDigest(*in)
	if err != nil {
		t.Fatal(err)
	}
	in, err = attempts.PublishAnalysisImportAttempt(t.Context(), storage.AnalysisAttemptPublication{Import: *in, AttemptID: a.ID, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	from, to := time.Unix(1700000000, 0).UTC(), time.Unix(1700000001, 0).UTC()
	before := analytics.NativeBusy()
	requests := []struct {
		path string
		body any
	}{
		{"/api/v1/analysis/datasets/" + in.ID + "/series", map[string]any{"metric": "v", "from": from, "to": to, "bucket_seconds": 1, "aggregation": "avg"}},
		{"/api/v1/analysis/compare?project_id=proj-a", AnalysisComparisonRequest{Left: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "v", Aggregation: "avg"}, Right: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "v", Aggregation: "avg"}, From: from, To: to, BucketSeconds: 1, Mode: "overlay"}},
	}
	for _, request := range requests {
		w = timeslotRequest(t, s, http.MethodPost, request.path, request.body)
		if w.Code != http.StatusUnprocessableEntity || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "\"points\"") || analytics.NativeBusy() != before || len(analysisPipelineJobs) != 0 {
			t.Fatal("historical protected mapping entered native or produced outward evidence", request.path, w.Code, w.Body.String())
		}
	}
	for _, key := range append([]string{originalKey}, in.Normalized...) {
		var out bytes.Buffer
		if _, err := analysisObjectRead(t.Context(), key, &out, int64(len(original)+1)); err != nil || !bytes.Equal(out.Bytes(), original) {
			t.Fatal("refusal modified protected original/normalized bytes", key, err)
		}
	}
	current, err = store.GetAnalysisImport(in.ID)
	if err != nil || current == nil || current.State != "ready" || current.Revision != in.Revision {
		t.Fatal("refusal changed historical dataset ownership", current, err)
	}
}
