//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
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

// uploadNativeAnalysisFixture writes query as Parquet and uploads it as a new
// import, returning the import and its import and dataset paths.
func uploadNativeAnalysisFixture(t *testing.T, server *Server, query string) (storage.AnalysisImport, string, string) {
	t.Helper()
	return uploadNativeAnalysisFiles(t, server, query)
}

// uploadNativeAnalysisFiles uploads one Parquet file per query as a new
// import; each file normalizes into its own partitions.
func uploadNativeAnalysisFiles(t *testing.T, server *Server, queries ...string) (storage.AnalysisImport, string, string) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var files []any
	var contents [][]byte
	for i, query := range queries {
		name := fmt.Sprintf("fixture-%d.parquet", i)
		fixture := filepath.Join(t.TempDir(), name)
		if _, err := db.Exec("COPY (" + query + ") TO '" + fixture + "' (FORMAT PARQUET)"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]any{"relative_path": name, "size": len(data)})
		contents = append(contents, data)
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "fixture", "files": files})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID
	for i, file := range in.Files {
		req := httptest.NewRequest("PUT", path+"/files/"+file.ID+"?offset=0", bytes.NewReader(contents[i]))
		req.Header.Set("Content-Type", "application/octet-stream")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("upload %s: %d", file.RelativePath, response.Code)
		}
	}
	return in, path, "/api/v1/analysis/datasets/" + in.ID
}

// commitNativeAnalysisMapping maps and commits an uploaded import and waits
// until it is ready.
func commitNativeAnalysisMapping(t *testing.T, server *Server, store storage.Store, in storage.AnalysisImport, path string, mapping analysis.ParquetMapping) *storage.AnalysisImport {
	t.Helper()
	imports := store.(storage.AnalysisImportStore)
	current, err := imports.GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	if r := timeslotRequest(t, server, "POST", path+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if current, err = imports.GetAnalysisImport(in.ID); err != nil {
			t.Fatal(err)
		}
		if current.State != "normalizing" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if current.State != "ready" {
		t.Fatalf("normalization: %+v", current)
	}
	return current
}

// Merged histogram counts reconcile with an independent sum of the source
// bins, and the manifest accounts for every source row.
func TestAnalysisHistogramImportCommitQueryReconcile(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	server, store := newTimeslotServer(t)
	// Prometheus-style long rows: le-cumulative counters scraped each minute.
	query := "SELECT ts, le, requests FROM (SELECT 1700000040+s*60 AS ts, s FROM range(5) t(s)), (SELECT * FROM (VALUES ('0.05', 1), ('0.25', 2), ('+Inf', 3)) b(le, k)) CROSS JOIN LATERAL (SELECT (s*(k+2)+k)::DOUBLE AS requests)"
	in, path, dataset := uploadNativeAnalysisFixture(t, server, query)
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{
		Value: []string{"requests"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "s",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"0.05", "0.25", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramCumulative, BoundColumn: []string{"le"}},
	}}}
	current := commitNativeAnalysisMapping(t, server, store, in, path, mapping)
	if current.Accepted != 15 || current.PartitionManifest == nil || current.PartitionManifest.Partitions[0].Stats.Kind != analysis.KindHistogram || current.PartitionManifest.Partitions[0].Stats.Rows != 4 {
		t.Fatalf("committed %+v", current)
	}
	// Independent reconciliation: le-cumulative count at scrape s for bound k
	// is s*(k+2)+k, so each one-minute delta per bin is (3, 1, 1) and the
	// whole span holds 4 windows of 5 observations.
	from := time.Unix(1700000040, 0).UTC()
	series := func(aggregation string, bucket, buckets int) ([]analysis.MetricPoint, int, string) {
		r := timeslotRequest(t, server, "POST", dataset+"/series", analytics.SeriesQuery{Metric: "latency", From: from, To: from.Add(time.Duration(bucket*buckets) * time.Second), BucketSeconds: bucket, Aggregation: aggregation})
		var out struct {
			Points []analysis.MetricPoint `json:"points"`
		}
		json.Unmarshal(r.Body.Bytes(), &out)
		return out.Points, r.Code, r.Body.String()
	}
	points, code, body := series("count", 120, 2)
	if code != 200 || len(points) != 2 || points[0].Value == nil || *points[0].Value != 10 || *points[1].Value != 10 {
		t.Fatalf("merged counts: %d %s", code, body)
	}
	// Merged (6,2,2): the median (rank 5) lies in the first bin, 0-50ms.
	points, code, body = series("p50", 120, 2)
	if code != 200 || points[0].Value == nil || math.Abs(*points[0].Value-50*5.0/6) > 1e-9 || points[0].EstimateBin == nil || points[0].EstimateBin.Upper != 50 {
		t.Fatalf("interpolated median: %d %s", code, body)
	}
	// p99 (rank 9.9) is in the overflow bin: no estimate.
	if points, code, body = series("p99", 120, 2); code != 200 || points[0].Value != nil || points[0].MissingReason != analysis.MissingHistogramOverflowBin {
		t.Fatalf("overflow percentile: %d %s", code, body)
	}
	if _, code, body = series("avg", 120, 2); code != 422 || !strings.Contains(body, "interpolated percentile") {
		t.Fatalf("average from a histogram: %d %s", code, body)
	}
}

// An earlier file's histogram partition is read while one of its windows
// crosses the query start, so the first bucket is partial; once its windows
// end at the start, the bucket holds only the later window, whole.
func TestAnalysisHistogramEarlierPartitionCrossingTheQueryStart(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	server, store := newTimeslotServer(t)
	const t0 = 1700000100 // On the five-minute grid.
	// The early file holds windows [t0, t0+300) and [t0+300, t0+600); the
	// late file one window [t0+600, t0+900).
	early := fmt.Sprintf("SELECT %d+i*300 AS ts, 1.0::DOUBLE AS fast, 0.0::DOUBLE AS overflow FROM range(2) t(i)", t0)
	late := fmt.Sprintf("SELECT %d AS ts, 2.0::DOUBLE AS fast, 0.0::DOUBLE AS overflow", t0+600)
	in, path, dataset := uploadNativeAnalysisFiles(t, server, early, late)
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{
		Value: []string{"overflow"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"10", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 300, CountColumns: []analysis.ParquetColumnPath{{"fast"}}},
	}}}
	current := commitNativeAnalysisMapping(t, server, store, in, path, mapping)
	if current.PartitionManifest == nil || len(current.PartitionManifest.Partitions) != 2 {
		t.Fatalf("committed %+v", current)
	}
	count := func(from int64) analysis.MetricPoint {
		start := time.Unix(from, 0).UTC()
		r := timeslotRequest(t, server, "POST", dataset+"/series", analytics.SeriesQuery{Metric: "latency", From: start, To: start.Add(600 * time.Second), BucketSeconds: 600, Aggregation: "count"})
		var out struct {
			Points []analysis.MetricPoint `json:"points"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.Points) != 1 {
			t.Fatalf("series from %d: %d %s", from, r.Code, r.Body.String())
		}
		return out.Points[0]
	}
	if p := count(t0 + 500); p.Value != nil || p.MissingReason != analysis.MissingPartialHistogramWindow {
		t.Fatalf("a window crossing the start from an earlier file: %+v", p)
	}
	if p := count(t0 + 600); p.Value == nil || *p.Value != 2 || p.MissingReason != "" {
		t.Fatalf("an earlier file whose windows end at the start: %+v", p)
	}
}
