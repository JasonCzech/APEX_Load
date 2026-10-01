package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisSavedMappingsProjectIsolationAndRevision(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a,proj-b")
	path := "/api/v1/analysis/mappings?project_id=proj-a"
	m := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}
	r := timeslotRequest(t, s, "POST", path, map[string]any{"name": "latency mapping", "definition": m})
	if r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	var saved storage.AnalysisDefinition
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	item := "/api/v1/analysis/mappings/" + saved.ID
	if r = timeslotRequest(t, s, "GET", item+"?project_id=proj-b", nil); r.Code != 404 {
		t.Fatalf("cross-project: %d", r.Code)
	}
	if r = timeslotRequest(t, s, "DELETE", item+"?project_id=proj-a&revision=9", nil); r.Code != 409 {
		t.Fatal("stale delete accepted")
	}
	if r = timeslotRequest(t, s, "DELETE", item+"?project_id=proj-a&revision=1", nil); r.Code != 200 {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	if r = timeslotRequest(t, s, "GET", item+"?project_id=proj-a", nil); r.Code != 404 {
		t.Fatal("deleted mapping visible")
	}
	if r = timeslotRequest(t, s, "GET", item+"?project_id=proj-a&revision=1", nil); r.Code != 200 {
		t.Fatal("lost historical mapping revision")
	}
}

func TestAnalysisComparisonBoundsAndForeignRun(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := AnalysisComparisonRequest{Left: AnalysisSeriesSelection{RunID: "unknown", SourceID: "s", Metric: "cpu", Aggregation: "avg"}, Right: AnalysisSeriesSelection{DatasetID: "unknown", Metric: "cpu", Aggregation: "avg"}, From: from, To: from.Add(time.Minute), BucketSeconds: 1, Mode: "correlation"}
	r := timeslotRequest(t, s, "POST", "/api/v1/analysis/compare?project_id=proj-a", q)
	if r.Code != 422 {
		t.Fatalf("foreign run: %d %s", r.Code, r.Body.String())
	}
	q.MaxLagSeconds = 3601
	if r = timeslotRequest(t, s, "POST", "/api/v1/analysis/compare?project_id=proj-a", q); r.Code != 400 {
		t.Fatal("unbounded lag accepted")
	}
}

func TestAnalysisStagingQuotaAndObjectPaths(t *testing.T) {
	remaining := int64(2)
	var out bytes.Buffer
	w := analysisStagingWriter{out: &out, dir: t.TempDir(), remaining: &remaining}
	if _, err := w.Write([]byte("abc")); err == nil {
		t.Fatal("staging quota ignored")
	}
	for _, name := range []string{".", "..", "../outside", ""} {
		if _, err := analysisObjectWrite(context.Background(), analysisObjectKey("p", "00000000-0000-0000-0000-000000000000", name), bytes.NewReader(nil)); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
