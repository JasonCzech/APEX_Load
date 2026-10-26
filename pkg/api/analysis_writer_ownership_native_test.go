//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/storage"
)

// writerCaptureStore records the generations the real handlers mint.
type writerCaptureStore struct {
	SQLiteStore *storage.SQLiteStore
	mu          sync.Mutex
	attempts    []*storage.AnalysisAttempt
	writers     []*storage.AnalysisUploadWriter
}

func (c *writerCaptureStore) ClaimAnalysisImportAttempt(ctx context.Context, id, project string, revision int64, lease time.Duration) (*storage.AnalysisImport, *storage.AnalysisAttempt, error) {
	in, a, err := c.SQLiteStore.ClaimAnalysisImportAttempt(ctx, id, project, revision, lease)
	if a != nil {
		c.mu.Lock()
		c.attempts = append(c.attempts, a)
		c.mu.Unlock()
	}
	return in, a, err
}

func (c *writerCaptureStore) BeginAnalysisUploadWriter(ctx context.Context, req storage.AnalysisUploadRequest) (*storage.AnalysisUploadWriter, error) {
	w, err := c.SQLiteStore.BeginAnalysisUploadWriter(ctx, req)
	if w != nil {
		c.mu.Lock()
		c.writers = append(c.writers, w)
		c.mu.Unlock()
	}
	return w, err
}

// The real upload and normalization pipeline records this controller process
// as the writer of every generation before its artifact I/O; after
// publication no exit judgement can change the published dataset, and this
// process never judges its own generations.
func TestAnalysisWriterOwnershipNativePipelineRecordsItsIncarnation(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	self, err := procidentity.Self()
	if err != nil {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			t.Fatalf("process identity unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Skipf("process identity unavailable: %v", err)
	}
	capture := &writerCaptureStore{}
	server, store, _ := nativeQueryAuthorityFixture(t, func(s *storage.SQLiteStore) storage.Store {
		capture.SQLiteStore = s
		return sqliteOverlay(capture)
	})
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fixture := filepath.Join(t.TempDir(), "sample.parquet")
	if _, err = db.Exec("COPY (SELECT 1700000000000+i*1000 AS ts, (i+1)::DOUBLE AS latency FROM range(10) t(i)) TO '" + fixture + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "owned", "files": []any{map[string]any{"relative_path": "sample.parquet", "size": len(data)}}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err = json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID
	req := httptest.NewRequest("PUT", path+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}
	if r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	if r = timeslotRequest(t, server, "POST", path+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(15 * time.Second)
	for current.State != "ready" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if current, err = store.GetAnalysisImport(in.ID); err != nil {
			t.Fatal(err)
		}
	}
	if current.State != "ready" {
		t.Fatalf("normalization: %+v", current)
	}
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	capture.mu.Lock()
	attempts, writers := append([]*storage.AnalysisAttempt{}, capture.attempts...), append([]*storage.AnalysisUploadWriter{}, capture.writers...)
	capture.mu.Unlock()
	if len(attempts) != 1 || attempts[0].Incarnation == nil || *attempts[0].Incarnation != self {
		t.Fatal("claimed attempt did not record this controller", attempts)
	}
	if len(writers) != 1 || writers[0].Incarnation == nil || *writers[0].Incarnation != self {
		t.Fatal("upload writer did not record this controller", writers)
	}
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	everything := storage.AnalysisWriterExitScope{Prover: procidentity.Identity{Machine: "m@h", Boot: "b", Namespace: "n", PID: 1, Start: "1"}, LocalFilesystem: true, Exited: func(procidentity.Identity) bool { return true }}
	outcome, err := store.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, attempts[0].ID, everything)
	if err != nil || outcome.Recorded || !outcome.Quiesced {
		t.Fatal("published attempt was re-judged", outcome, err)
	}
	after, err := store.GetAnalysisImport(in.ID)
	if err != nil || after.State != "ready" || after.Revision != current.Revision {
		t.Fatal("published dataset changed", after, err)
	}
	if r = timeslotRequest(t, server, "DELETE", path, nil); r.Code != 204 {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
}
