//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The preview, the committed dataset and the queried series must agree with
// the source summary rows.
func TestAnalysisSummaryImportPreviewCommitQueryReconcile(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	server, store := newTimeslotServer(t)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fixture := filepath.Join(t.TempDir(), "summary.parquet")
	// Six one-minute windows: p50/p95 percentiles, a mean with sample counts.
	if _, err := db.Exec("COPY (SELECT 1700000040+i*60 AS ts, (100+i)::DOUBLE AS p50, (400+i)::DOUBLE AS p95, (200+10*i)::DOUBLE AS mean, (5+i)::DOUBLE AS n FROM range(6) t(i)) TO '" + fixture + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "summaries", "files": []any{map[string]any{"relative_path": "summary.parquet", "size": len(data)}}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path, dataset := "/api/v1/analysis/imports/"+in.ID, "/api/v1/analysis/datasets/"+in.ID
	req := httptest.NewRequest("PUT", path+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("upload: %d", response.Code)
	}
	r = timeslotRequest(t, server, "GET", path+"/files/"+in.Files[0].ID+"/preview", nil)
	if r.Code != 200 {
		t.Fatalf("preview: %d %s", r.Code, r.Body.String())
	}
	var preview struct {
		Samples []map[string]any `json:"samples"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &preview); err != nil || len(preview.Samples) != 6 || preview.Samples[1]["p95"] != 401.0 {
		t.Fatalf("preview samples %s %v", r.Body.String(), err)
	}
	current, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	undeclared := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"p95"}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms"}}}
	if r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": undeclared}); r.Code != 400 || !strings.Contains(r.Body.String(), "statistic") {
		t.Fatalf("undeclared summary mapping: %d %s", r.Code, r.Body.String())
	}
	declare := func(column, statistic string) analysis.ParquetMeasure {
		return analysis.ParquetMeasure{Value: []string{column}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms", Statistic: statistic, WindowSeconds: 60}
	}
	mean := declare("mean", "avg")
	mean.SampleCountColumn = []string{"n"}
	mapping := undeclared
	mapping.Measures = []analysis.ParquetMeasure{declare("p50", "p50"), declare("p95", "p95"), mean}
	if r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	if r = timeslotRequest(t, server, "POST", path+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if current, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID); err != nil {
			t.Fatal(err)
		}
		if current.State != "normalizing" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if current.State != "ready" || current.Accepted != 18 {
		t.Fatalf("normalization: %+v", current)
	}
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	from := time.Unix(1700000040, 0).UTC()
	series := func(aggregation string, bucket int) ([]analysis.MetricPoint, int, string) {
		r := timeslotRequest(t, server, "POST", dataset+"/series", analytics.SeriesQuery{Metric: "latency", From: from, To: from.Add(6 * time.Minute), BucketSeconds: bucket, Aggregation: aggregation})
		var out struct {
			Points []analysis.MetricPoint `json:"points"`
		}
		json.Unmarshal(r.Body.Bytes(), &out)
		return out.Points, r.Code, r.Body.String()
	}
	// Each statistic reconciles with its source column at source resolution.
	for statistic, base := range map[string]float64{"p50": 100, "p95": 400} {
		points, code, body := series(statistic, 60)
		if code != 200 || len(points) != 6 {
			t.Fatalf("%s: %d %s", statistic, code, body)
		}
		for i, point := range points {
			if point.Value == nil || *point.Value != base+float64(i) || point.InputKind != analysis.KindSummary {
				t.Fatalf("%s[%d] %+v", statistic, i, point)
			}
		}
	}
	points, code, body := series("p95", 180)
	if code != 200 || len(points) != 2 || points[0].Value != nil || points[0].MissingReason != analysis.MissingPercentileNotMergeable {
		t.Fatalf("merged percentile: %d %s", code, body)
	}
	points, code, body = series("avg", 180)
	want := (5*200 + 6*210 + 7*220.0) / 18
	if code != 200 || len(points) != 2 || points[0].Value == nil || math.Abs(*points[0].Value-want) > 1e-9 {
		t.Fatalf("weighted mean: %d %s", code, body)
	}
	// Requests the summary cannot answer explain themselves before staging.
	if _, code, body = series("p99", 60); code != 422 || !strings.Contains(body, "p50, p95, avg") {
		t.Fatalf("undeclared statistic: %d %s", code, body)
	}
	if _, code, body = series("p95", 90); code != 422 || !strings.Contains(body, "60-second source window") {
		t.Fatalf("straddling bucket: %d %s", code, body)
	}
	// A dataset comparison may name a summary-only statistic.
	comparison := AnalysisComparisonRequest{Left: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "latency", Aggregation: "p50"}, Right: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "latency", Aggregation: "p95"}, From: from, To: from.Add(6 * time.Minute), BucketSeconds: 60, Mode: "correlation"}
	if r = timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id=proj-a", comparison); r.Code != 200 {
		t.Fatalf("summary correlation: %d %s", r.Code, r.Body.String())
	}
}
