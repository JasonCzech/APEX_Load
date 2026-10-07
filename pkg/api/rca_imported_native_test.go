//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// importRCACPUDataset imports a CPU dataset sampled every interval over
// [from, to) through the real upload, mapping and normalization pipeline,
// associates it with run, and returns it. cpu gives each sample's value.
func importRCACPUDataset(t *testing.T, server *Server, store any, project, run, host string, from, to time.Time, every time.Duration, cpu func(time.Time) float64) *storage.AnalysisImport {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []string
	for at := from; at.Before(to); at = at.Add(every) {
		rows = append(rows, fmt.Sprintf("(%d, %f, '%s')", at.UnixMilli(), cpu(at), host))
	}
	fixture := filepath.Join(t.TempDir(), "cpu.parquet")
	if _, err := db.Exec("COPY (SELECT * FROM (VALUES " + strings.Join(rows, ",") + ") t(ts, cpu, host)) TO '" + fixture + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	created := request(http.MethodPost, "/api/v1/analysis/imports?project_id="+project, map[string]any{"name": "cpu", "files": []any{map[string]any{"relative_path": "cpu.parquet", "size": len(data)}}})
	var in storage.AnalysisImport
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &in) != nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	upload := httptest.NewRequest(http.MethodPut, "/api/v1/analysis/imports/"+in.ID+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
	upload.Header.Set("Content-Type", "application/octet-stream")
	uploaded := httptest.NewRecorder()
	server.mux.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", uploaded.Code, uploaded.Body.String())
	}
	imports := store.(storage.AnalysisImportStore)
	current, _ := imports.GetAnalysisImport(in.ID)
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC",
		Measures: []analysis.ParquetMeasure{{Value: []string{"cpu"}, Name: "host.cpu.usage", Kind: analysis.KindGauge, Unit: "%"}},
		Identity: &analysis.ParquetIdentityMapping{Host: []string{"host"}}}
	if mapped := request(http.MethodPut, "/api/v1/analysis/imports/"+in.ID+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); mapped.Code != http.StatusOK {
		t.Fatalf("mapping: %d %s", mapped.Code, mapped.Body.String())
	}
	if committed := request(http.MethodPost, "/api/v1/analysis/imports/"+in.ID+"/commit", nil); committed.Code != http.StatusAccepted {
		t.Fatalf("commit: %d %s", committed.Code, committed.Body.String())
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if current, _ = imports.GetAnalysisImport(in.ID); current.State != "normalizing" {
			break
		}
	}
	if current.State != "ready" {
		t.Fatalf("normalization: %+v", current)
	}
	for deadline := time.Now().Add(10 * time.Second); len(analysisPipelineJobs) > 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	if associated := request(http.MethodPut, "/api/v1/analysis/datasets/"+in.ID+"/association", map[string]any{"revision": current.Revision, "run_id": run}); associated.Code != http.StatusOK {
		t.Fatalf("association: %d %s", associated.Code, associated.Body.String())
	}
	current, _ = imports.GetAnalysisImport(in.ID)
	return current
}

// An associated dataset is read through the native worker as imported
// series on the run's grid; an unassociated or foreign dataset never is.
func TestRCAImportedSeriesFromAnAssociatedDataset(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a,proj-b")
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-b", Name: "Project B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1700000000, 0).UTC()
	for _, run := range []struct{ id, project string }{{"rca-imported", "proj-a"}, {"rca-other", "proj-a"}, {"rca-foreign", "proj-b"}} {
		if err := store.SaveTest(&storage.TestRecord{ID: run.id, ProjectID: run.project, Name: run.id, Status: "COMPLETE", StartTime: start}); err != nil {
			t.Fatal(err)
		}
	}
	step := start.Add(60 * time.Second)
	cpu := func(at time.Time) float64 {
		if at.Before(step) {
			return 20
		}
		return 95
	}
	dataset := importRCACPUDataset(t, server, store, "proj-a", "rca-imported", "10.0.0.7", start, start.Add(2*time.Minute), 5*time.Second, cpu)
	importRCACPUDataset(t, server, store, "proj-a", "rca-other", "10.0.0.8", start, start.Add(2*time.Minute), 5*time.Second, cpu)
	importRCACPUDataset(t, server, store, "proj-b", "rca-foreign", "10.0.0.9", start, start.Add(2*time.Minute), 5*time.Second, cpu)

	frozen, truncated, err := loadRCADatasets(store, "proj-a", "rca-imported")
	if err != nil || truncated || len(frozen) != 1 || frozen[0].DatasetID != dataset.ID || frozen[0].Revision != dataset.Revision {
		t.Fatalf("frozen %+v %t %v", frozen, truncated, err)
	}
	var summaries []IntervalSummaryRecord
	for i := 1; i <= 12; i++ {
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * 10 * time.Second)})
	}
	read := func() rca.Series {
		t.Helper()
		series, limitations, err := server.rcaImportedSeries(context.Background(), "proj-a", "rca-imported", frozen, summaries, -1, rca.NewBudgeter(rca.DefaultBudget, nil, nil), 0)
		if err != nil || len(limitations) != 0 || len(series) != 1 {
			t.Fatalf("series %+v limitations %+v err %v", series, limitations, err)
		}
		return series[0]
	}
	s := read()
	if s.Kind != rca.SeriesImported || s.Bucket != 10*time.Second || s.Unit != "percent" || len(s.Identity) != 64 || !strings.HasPrefix(s.ID, "imported/"+dataset.ID+"/host.cpu.usage/") ||
		s.Measures == "" || len(s.Nodes) == 0 || s.Nodes[0] != rca.InfrastructureNodeID("host", "10.0.0.7") {
		t.Fatalf("%+v", s)
	}
	early, late := 0.0, 0.0
	for _, p := range s.Points {
		if !p.Valid {
			continue
		}
		if p.At.Before(step) {
			early = max(early, p.Value)
		} else {
			late = max(late, p.Value)
		}
	}
	if early != 20 || late != 95 {
		t.Fatalf("early %v late %v in %+v", early, late, s.Points)
	}
	// The identity is the same on a new read, and from a restarted controller
	// over the same store.
	if again := read(); again.ID != s.ID || again.Identity != s.Identity {
		t.Fatalf("identity changed: %s vs %s", again.ID, s.ID)
	}
	restarted := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = restarted.Shutdown(ctx)
	})
	if after, _, err := restarted.rcaImportedSeries(context.Background(), "proj-a", "rca-imported", frozen, summaries, -1, rca.NewBudgeter(rca.DefaultBudget, nil, nil), 0); err != nil || len(after) != 1 || after[0].ID != s.ID || after[0].Identity != s.Identity {
		t.Fatalf("identity after restart: %+v %v", after, err)
	}

	// On the run's one-second grid the five-second samples are regridded to
	// their own interval rather than read as mostly gaps; a series on another
	// grid is another measurement series.
	var fine []IntervalSummaryRecord
	for i := 1; i <= 120; i++ {
		fine = append(fine, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * time.Second)})
	}
	if regridded, _, err := server.rcaImportedSeries(context.Background(), "proj-a", "rca-imported", frozen, fine, -1, rca.NewBudgeter(rca.DefaultBudget, nil, nil), 0); err != nil || len(regridded) != 1 || regridded[0].Bucket != 5*time.Second || regridded[0].Points[1].Value != 20 || !regridded[0].Points[1].Valid || regridded[0].Identity == s.Identity {
		t.Fatalf("regridded %+v %v", regridded, err)
	}

	// A dataset that changes after the evidence froze it is not read: a new
	// revision of the same association, then an association with another run.
	reassociate := func(run string) {
		t.Helper()
		current, _ := store.(storage.AnalysisImportStore).GetAnalysisImport(dataset.ID)
		request := httptest.NewRequest(http.MethodPut, "/api/v1/analysis/datasets/"+dataset.ID+"/association", bytes.NewReader([]byte(fmt.Sprintf(`{"revision":%d,"run_id":%q}`, current.Revision, run))))
		request.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		server.mux.ServeHTTP(reply, request)
		if reply.Code != http.StatusOK {
			t.Fatalf("reassociation: %d %s", reply.Code, reply.Body.String())
		}
		series, limitations, err := server.rcaImportedSeries(context.Background(), "proj-a", "rca-imported", frozen, summaries, -1, rca.NewBudgeter(rca.DefaultBudget, nil, nil), 0)
		if err != nil || len(series) != 0 || limitationCodes(limitations) != limitationImportedDatasetChanged {
			t.Fatalf("changed dataset read: %+v %+v %v", series, limitations, err)
		}
	}
	// A dataset that changes while it is being read is dropped too: the
	// series read would describe another revision.
	original := rcaQueryImportedDataset
	t.Cleanup(func() { rcaQueryImportedDataset = original })
	changed := false
	rcaQueryImportedDataset = func(ctx context.Context, in *storage.AnalysisImport, query analytics.SeriesQuery) ([]analysis.MetricPoint, error) {
		points, err := original(ctx, in, query)
		if !changed {
			changed = true
			current, _ := store.(storage.AnalysisImportStore).GetAnalysisImport(dataset.ID)
			request := httptest.NewRequest(http.MethodPut, "/api/v1/analysis/datasets/"+dataset.ID+"/association", bytes.NewReader([]byte(fmt.Sprintf(`{"revision":%d,"run_id":"rca-imported"}`, current.Revision))))
			request.Header.Set("Content-Type", "application/json")
			reply := httptest.NewRecorder()
			server.mux.ServeHTTP(reply, request)
			if reply.Code != http.StatusOK {
				t.Errorf("reassociation during the read: %d %s", reply.Code, reply.Body.String())
			}
		}
		return points, err
	}
	series, limitations, err := server.rcaImportedSeries(context.Background(), "proj-a", "rca-imported", frozen, summaries, -1, rca.NewBudgeter(rca.DefaultBudget, nil, nil), 0)
	if err != nil || len(series) != 0 || !strings.Contains(limitationCodes(limitations), limitationImportedDatasetChanged) {
		t.Fatalf("dataset changed during its read: %+v %+v %v", series, limitations, err)
	}
	rcaQueryImportedDataset = original
	reassociate("rca-imported")
	reassociate("rca-other")
}

// TestRCAEndToEndImportedResourcePressure: a real run whose latency steps up
// while an associated imported dataset shows the target host's CPU rising
// with it. The analysis judges the imported series and attributes the
// latency step to resource pressure. Opt-in with the other end-to-end tests:
// APEXLOAD_RCA_E2E=1 go test -tags analytics_duckdb ./pkg/api -run TestRCAEndToEndImportedResourcePressure -count=1 -timeout 30m
func TestRCAEndToEndImportedResourcePressure(t *testing.T) {
	h := newRCAE2E(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", storage.DefaultProjectID)
	const stepAfter = 60 * time.Second
	var started, stepAt time.Time
	run, _ := h.run(CreateTestRequest{Name: "RCA imported resource pressure", VUsers: 4, Duration: "100s"}, func() {
		started = time.Now()
		stepAt = started.Add(stepAfter)
		h.slowFrom.Store(stepAt.UnixNano())
	})
	// Sampled every two seconds, the level's thirty steady seconds before the
	// step give the detector its ten baseline buckets; the jitter is a real
	// host's noise.
	dataset := importRCACPUDataset(t, h.server, h.store, storage.DefaultProjectID, run.ID, "127.0.0.1", started.Add(-30*time.Second).Truncate(2*time.Second), started.Add(150*time.Second), 2*time.Second, func(at time.Time) float64 {
		jitter := float64(at.Unix()%3) - 1
		if at.Before(stepAt) {
			return 18 + jitter
		}
		return 96 + jitter
	})
	done := h.analyze(run.ID, map[string]any{})
	a := done.Assessment
	logAssessment(t, a)
	imported := ""
	for _, anomaly := range a.Anomalies {
		if strings.HasPrefix(anomaly.Series, "imported/"+dataset.ID+"/") && anomaly.Direction == rca.DirectionIncrease {
			imported = anomaly.ID
		}
	}
	if imported == "" {
		t.Fatal("the imported CPU rise was not detected")
	}
	attributed := false
	for _, hypothesis := range a.Hypotheses {
		if hypothesis.Signature != "resource_pressure" {
			continue
		}
		for _, ref := range hypothesis.Supporting {
			attributed = attributed || ref.Kind == "imported_series" && strings.Contains(ref.Locator, "imported/"+dataset.ID+"/")
		}
	}
	if !attributed {
		t.Fatal("no resource_pressure hypothesis cites the imported CPU rise")
	}
}
