//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisNativeImportLifecycle(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	server, store := newTimeslotServer(t)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fixture := filepath.Join(t.TempDir(), "sample.parquet")
	if _, err := db.Exec("COPY (SELECT 1700000000000+i*1000 AS ts, (i+1)::DOUBLE AS latency, 'secret' AS credential FROM range(10) t(i)) TO '" + fixture + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "fixture", "files": []any{map[string]any{"relative_path": "year=2026/sample.parquet", "size": len(data)}}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID
	dataset := "/api/v1/analysis/datasets/" + in.ID
	if r := timeslotRequest(t, server, "GET", dataset, nil); r.Code != 404 {
		t.Fatal("incomplete dataset visible")
	}
	upload := func(body []byte) int {
		req := httptest.NewRequest("PUT", path+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response.Code
	}
	if code := upload(data); code != 200 {
		t.Fatalf("upload=%d", code)
	}
	if code := upload(data); code != 200 {
		t.Fatalf("retry=%d", code)
	}
	conflict := append([]byte(nil), data...)
	conflict[5] ^= 1
	if code := upload(conflict); code != 409 {
		t.Fatalf("conflicting retry=%d", code)
	}
	r = timeslotRequest(t, server, "GET", path+"/files/"+in.Files[0].ID+"/preview", nil)
	if r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(`"secret"`)) {
		t.Fatalf("preview: %d %s", r.Code, r.Body.String())
	}
	updated, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}
	r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": updated.Revision, "mapping": mapping})
	if r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	r = timeslotRequest(t, server, "POST", path+"/commit", nil)
	if r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		updated, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.State != "normalizing" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if updated.State != "ready" || updated.Accepted != 10 {
		t.Fatalf("normalization: %+v", updated)
	}
	// Wait for the worker to release staging admission after durable publication.
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r = timeslotRequest(t, server, "POST", dataset+"/series", analytics.SeriesQuery{Metric: "latency", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000010000), BucketSeconds: 10, Aggregation: "p95"})
	if r.Code != 200 {
		t.Fatalf("query: %d %s", r.Code, r.Body.String())
	}
	var response struct {
		Points []analysis.MetricPoint `json:"points"`
	}
	json.Unmarshal(r.Body.Bytes(), &response)
	if len(response.Points) != 1 || response.Points[0].Value == nil || *response.Points[0].Value < 9 {
		t.Fatalf("unexpected series: %s", r.Body.String())
	}
	comparison := AnalysisComparisonRequest{Left: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "latency", Aggregation: "avg"}, Right: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "latency", Aggregation: "avg"}, From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000010000), BucketSeconds: 1, Mode: "correlation"}
	r = timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id=proj-a", comparison)
	if r.Code != 200 {
		t.Fatalf("correlation: %d %s", r.Code, r.Body.String())
	}
	var compared struct {
		Correlation []analysis.CorrelationScore `json:"correlation"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &compared); err != nil {
		t.Fatal(err)
	}
	if len(compared.Correlation) != 1 || compared.Correlation[0].Pairs != 10 || compared.Correlation[0].Coefficient == nil || *compared.Correlation[0].Coefficient < .999 {
		t.Fatalf("correlation: %s", r.Body.String())
	}
	// A native panel reads the real normalized dataset only after association.
	start := time.UnixMilli(1700000000000).UTC()
	if err := store.SaveTest(&storage.TestRecord{ID: "overlay-run", ProjectID: "proj-a", Name: "overlay", Status: "COMPLETE", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	q := nativeEvidenceQuery("archive")
	q.EndSeconds = 10
	q.BucketSeconds = 1
	q.OverlaySelection = &storage.NativeOverlaySelection{DatasetID: in.ID, Metric: "latency", Aggregation: "avg"}
	out := nativeEvidenceEnvelope(start, q)
	series := NativeEvidenceSeries{Name: "Checkout", Unit: "ms"}
	for i := 0; i < 10; i++ {
		value := float64(i + 1)
		series.Points = append(series.Points, NativeEvidencePoint{From: start.Add(time.Duration(i) * time.Second), To: start.Add(time.Duration(i+1) * time.Second), Count: 1, Value: &value})
	}
	out.Series = []NativeEvidenceSeries{series}
	if err := server.attachNativeOverlay(context.Background(), "proj-a", "overlay-run", q, &out); err == nil {
		t.Fatal("unassociated overlay accepted")
	}
	if reply := timeslotRequest(t, server, "PUT", dataset+"/association", map[string]any{"revision": updated.Revision, "run_id": "overlay-run"}); reply.Code != 200 {
		t.Fatalf("association: %d %s", reply.Code, reply.Body.String())
	}
	updated, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.attachNativeOverlay(context.Background(), "proj-a", "overlay-run", q, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Overlay) != 10 || len(out.Correlation["Checkout"]) != 1 || out.Correlation["Checkout"][0].Pairs != 10 || out.Correlation["Checkout"][0].Coefficient == nil || *out.Correlation["Checkout"][0].Coefficient < .999 {
		t.Fatalf("native dataset overlay: %+v", out)
	}
	// A caller cannot claim an association just by including a run in the query.
	if r = timeslotRequest(t, server, "PUT", dataset+"/association", map[string]any{"revision": updated.Revision, "run_id": "unknown"}); r.Code != 404 {
		t.Fatal("associated unowned run")
	}
	// The durable upload30 journal now carries actual body/artifact closure,
	// so the complete native lifecycle must delete all source/normalized bytes.
	sourceKeys := make([]string, 0, len(updated.Files[0].Chunks)+len(updated.Normalized))
	for _, chunk := range updated.Files[0].Chunks {
		key, keyErr := storage.AnalysisUploadChunkObjectKey(updated.ProjectID, updated.ID, updated.Files[0], chunk)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		sourceKeys = append(sourceKeys, key)
	}
	sourceKeys = append(sourceKeys, updated.Normalized...)
	ready, err := store.(interface {
		AnalysisDatasetCleanupReady(context.Context, string, string) (bool, error)
	}).AnalysisDatasetCleanupReady(context.Background(), in.ProjectID, in.ID)
	if err != nil || ready {
		t.Fatalf("live dataset unexpectedly grants namespace deletion: %v %v", ready, err)
	}
	for range 2 {
		r = timeslotRequest(t, server, "DELETE", path, nil)
		if r.Code != 204 {
			t.Fatalf("durable upload deletion: %d %s", r.Code, r.Body.String())
		}
	}
	deleted, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil || deleted == nil || deleted.State != "deleted" {
		t.Fatalf("physical deletion lost terminal metadata: %+v %v", deleted, err)
	}
	ready, err = store.(interface {
		AnalysisDatasetCleanupReady(context.Context, string, string) (bool, error)
	}).AnalysisDatasetCleanupReady(context.Background(), in.ProjectID, in.ID)
	if err != nil || !ready {
		t.Fatalf("terminal joined upload failed cleanup readiness: %v %v", ready, err)
	}
	for _, key := range sourceKeys {
		var retained bytes.Buffer
		if _, readErr := analysisObjectRead(context.Background(), key, &retained, 1); !errors.Is(readErr, os.ErrNotExist) || retained.Len() != 0 {
			t.Fatalf("204 retained analytical evidence: %v", readErr)
		}
	}
	if r := timeslotRequest(t, server, "GET", dataset, nil); r.Code != 404 {
		t.Fatal("deleted dataset visible")
	}
}

func TestAnalysisRejectionDownloadIsDurableAndBounded(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	server, durable := newTimeslotServer(t)
	store := durable.(storage.AnalysisImportStore)
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000001", ProjectID: "proj-a", Name: "rejections", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "f", RelativePath: "part.parquet", Size: 16, Uploaded: 16}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("protected-rejection-fixture")
	var key string
	in.AllowRejected = true
	in = publishAnalysisAPIFixture(t, store, in, func(claimed *storage.AnalysisImport, attempt *storage.AnalysisAttempt) {
		claimed.Rejected = 1
		key = attempt.Rejections[0]
		digest, writeErr := analysisObjectWrite(context.Background(), key, bytes.NewReader(body))
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		claimed.RejectionArtifacts = []string{key}
		claimed.RejectionSHA256 = map[string]string{key: digest}
	})
	path := "/api/v1/analysis/datasets/" + in.ID + "/rejections/0"
	r := timeslotRequest(t, server, "GET", path, nil)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), body) || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %s", r.Code, r.Body.String())
	}
	if r = timeslotRequest(t, server, "GET", path+"bad", nil); r.Code != 404 {
		t.Fatal("invalid index accepted")
	}
	if _, err = analysisObjectWrite(context.Background(), key, bytes.NewBufferString("modified")); err != nil {
		t.Fatal(err)
	}
	if r = timeslotRequest(t, server, "GET", path, nil); r.Code != 503 || bytes.Contains(r.Body.Bytes(), []byte("modified")) {
		t.Fatal("released corrupt artifact")
	}
}
