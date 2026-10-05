//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func nativePartitionImportFixture(t *testing.T) (*Server, *storage.AnalysisImport, time.Time) {
	t.Helper()
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, raw := newTimeslotServer(t)
	store := raw.(storage.AnalysisImportStore)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	from := time.Unix(1700000000, 0).UTC()
	bodies := [][]byte{}
	files := []any{}
	for i, side := range []string{"left", "right", "outside"} {
		timestamp := int64(1700000000)
		factor := 1
		if side == "right" {
			factor = 10
		}
		host := side
		if side == "outside" {
			timestamp += 300
			host = "left"
		}
		path := filepath.Join(t.TempDir(), side+".parquet")
		// Controlled fixture literals are fixed scalar values and a test-owned path.
		query := `COPY (SELECT ` + sqlFixtureInt(timestamp) + `+i AS ts, CASE WHEN i=2 THEN NULL ELSE (i+1)*` + sqlFixtureInt(int64(factor)) + ` END::DOUBLE AS value, 'source' AS source, '` + host + `' AS host, 'svc' AS service, '` + host + `' AS region FROM range(4) t(i)) TO '` + strings.ReplaceAll(path, "'", "''") + `' (FORMAT PARQUET)`
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		files = append(files, map[string]any{"relative_path": "side=" + side + "/part-" + sqlFixtureInt(int64(i)) + ".parquet", "size": len(body)})
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "partition proof", "files": files})
	if r.Code != 201 {
		t.Fatal("create", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID
	for i, file := range in.Files {
		req := httptest.NewRequest("PUT", path+"/files/"+file.ID+"?offset=0", bytes.NewReader(bodies[i]))
		req.Header.Set("Content-Type", "application/octet-stream")
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal("upload", w.Code, w.Body.String())
		}
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Source: []string{"source"}, Host: []string{"host"}, Service: []string{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: []string{"region"}}}}, Measures: []analysis.ParquetMeasure{
		{Name: "gauge", Value: []string{"value"}, Kind: analysis.KindGauge, Unit: "ms"},
		{Name: "latency", Value: []string{"value"}, Kind: analysis.KindObservation, Unit: "ms"},
		{Name: "counter", Value: []string{"value"}, Kind: analysis.KindCounter, Unit: "count"},
	}}
	r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping})
	if r.Code != 200 {
		t.Fatal("mapping", r.Code, r.Body.String())
	}
	r = timeslotRequest(t, server, "POST", path+"/commit", nil)
	if r.Code != 202 {
		t.Fatal("commit", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		current, err = store.GetAnalysisImport(in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State != "normalizing" && len(analysisPipelineJobs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("normalization did not join", current.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if current.State != "ready" || current.Accepted != 36 || current.PartitionManifestVersion != 1 || validateAnalysisQueryManifest(current) != nil {
		t.Fatalf("missing durable producer proof: %+v", current)
	}
	return server, current, from
}

func sqlFixtureInt(value int64) string { return fmt.Sprintf("%d", value) }

func TestNativePartitionQueriesPreserveFullScanQualityLineageAndIndependentComparisonPlans(t *testing.T) {
	server, in, from := nativePartitionImportFixture(t)
	legacy := *in
	legacy.PartitionManifestVersion = 0
	legacy.PartitionManifest = nil
	query := analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: "gauge", IdentitySchemaVersion: 2, SourceID: "source", Host: "left", Service: "svc", Dimensions: map[string]string{"region": "left"}, From: from, To: from.Add(4 * time.Second), BucketSeconds: 1, Aggregation: "avg"}
	for _, tc := range []struct{ metric, aggregation string }{{"gauge", "avg"}, {"gauge", "count"}, {"gauge", "min"}, {"gauge", "max"}, {"gauge", "sum"}, {"latency", "p95"}, {"counter", "rate"}} {
		t.Run(tc.metric+"-"+tc.aggregation, func(t *testing.T) {
			q := query
			q.Metric, q.Aggregation = tc.metric, tc.aggregation
			pruned, err := queryAnalysisDataset(t.Context(), in, q)
			if err != nil {
				t.Fatal(err)
			}
			full, err := queryAnalysisDataset(t.Context(), &legacy, q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pruned, full) {
				t.Fatalf("partition pruning changed values/quality/lineage\npruned=%+v\nfull=%+v", pruned, full)
			}
		})
	}
	comparison := AnalysisComparisonRequest{Left: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "gauge", Host: "left", Aggregation: "avg"}, Right: AnalysisSeriesSelection{DatasetID: in.ID, Metric: "gauge", Host: "right", Aggregation: "avg"}, From: from, To: from.Add(4 * time.Second), BucketSeconds: 1, Mode: "overlay"}
	reply := timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+in.ProjectID, comparison)
	if reply.Code != 200 {
		t.Fatal("distinct comparison", reply.Code, reply.Body.String())
	}
	var got struct {
		Left, Right        []analysis.MetricPoint
		ArtifactSelections []AnalysisArtifactSelection `json:"artifact_selections"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Left) != 4 || len(got.Right) != 4 || got.Left[0].Value == nil || got.Right[0].Value == nil || *got.Left[0].Value != 1 || *got.Right[0].Value != 10 {
		t.Fatal("comparison borrowed another side's filtered plan", reply.Body.String())
	}
	if len(got.ArtifactSelections) != 2 || got.ArtifactSelections[0].Mode != "producer_manifest" || got.ArtifactSelections[0].SelectedObjects != 1 || got.ArtifactSelections[1].SelectedObjects != 1 || got.ArtifactSelections[0].AvoidedArtifactBytes <= 0 {
		t.Fatal("comparison did not retain bounded artifact selection provenance", reply.Body.String())
	}
	// The public requested window is retained, while both actual populations
	// and correlation use the same reported TIMESTAMPTZ microsecond bounds.
	submicro := comparison
	submicro.From, submicro.To = from.Add(time.Nanosecond), from.Add(4*time.Second+time.Nanosecond)
	submicro.Mode = "correlation"
	subreply := timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+in.ProjectID, submicro)
	if subreply.Code != 200 {
		t.Fatal("submicro correlation rejected or erased", subreply.Code, subreply.Body.String())
	}
	var subgot AnalysisComparisonResponse
	if err := json.Unmarshal(subreply.Body.Bytes(), &subgot); err != nil {
		t.Fatal(err)
	}
	if subgot.TimeWindow == nil || !subgot.Request.From.Equal(submicro.From) || !subgot.TimeWindow.From.Equal(from) || len(subgot.Left) != 4 || subgot.Left[0].Value == nil || *subgot.Left[0].Value != 1 || len(subgot.Correlation) == 0 {
		t.Fatal("requested/effective window or evidence lost", subreply.Body.String())
	}
	subquery := query
	subquery.From, subquery.To = submicro.From, submicro.To
	subquery.IdentitySchemaVersion = 0 // Omission binds the immutable v2 mapping before validating filters.
	seriesreply := timeslotRequest(t, server, "POST", "/api/v1/analysis/datasets/"+in.ID+"/series", subquery)
	if seriesreply.Code != 200 {
		t.Fatal(seriesreply.Code, seriesreply.Body.String())
	}
	var seriesgot AnalysisDatasetSeriesResponse
	if err := json.Unmarshal(seriesreply.Body.Bytes(), &seriesgot); err != nil {
		t.Fatal(err)
	}
	if seriesgot.TimeWindow == nil || !seriesgot.TimeWindow.RequestedFrom.Equal(submicro.From) || !seriesgot.TimeWindow.From.Equal(from) || len(seriesgot.Points) != 4 || seriesgot.Points[0].Value == nil {
		t.Fatal("series precision hidden or evidence erased", seriesreply.Body.String())
	}
	// Removing excluded immutable objects proves the production query performs
	// no Stat/Attributes/Read on them. Old no-proof datasets still scan all files.
	selected, _, err := selectAnalysisDatasetParts(t.Context(), in, query)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	for _, part := range selected {
		keep[part.key] = true
	}
	root := currentArchiveBackend().LocalRoot
	for _, key := range in.Normalized {
		if !keep[key] {
			if err := os.Remove(filepath.Join(root, "_analysis", key)); err != nil {
				t.Fatal(err)
			}
		}
	}
	pruned, err := queryAnalysisDataset(t.Context(), in, query)
	if err != nil || len(pruned) != 4 {
		t.Fatal("excluded object accessed", err)
	}
	if _, err := queryAnalysisDataset(t.Context(), &legacy, query); err == nil {
		t.Fatal("legacy metadata inferred ordinal proof and skipped missing object")
	}
	comparison.Right = comparison.Left
	reply = timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+in.ProjectID, comparison)
	if reply.Code != 200 {
		t.Fatal("same selected geometry", reply.Code, reply.Body.String())
	}
	comparison.Right.Host = "right"
	reply = timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+in.ProjectID, comparison)
	if reply.Code != 422 {
		t.Fatal("right reused left's smaller admitted geometry", reply.Code, reply.Body.String())
	}
	// Selected byte/hash integrity stays mandatory even when all excluded files
	// can be skipped. A successful plan never certifies mutated physical bytes.
	selectedPath := filepath.Join(root, "_analysis", selected[0].key)
	body, err := os.ReadFile(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 1
	if err := os.WriteFile(selectedPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	if points, err := queryAnalysisDataset(t.Context(), in, query); err == nil || len(points) != 0 {
		t.Fatal("selected corrupt artifact served", err)
	}
}
