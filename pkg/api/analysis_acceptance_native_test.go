//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Analytical acceptance through the import API (NA-6, B8; NA-A): what an
// import of several files does with duplicate content and with a file whose
// schema drifted from the mapping.

type acceptanceImportFile struct {
	name string
	data []byte
}

func acceptanceParquet(t *testing.T, query string) []byte {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(t.TempDir(), "fixture.parquet")
	if _, err := db.Exec("COPY (" + query + ") TO '" + path + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// acceptanceImport creates an import of files, uploads them, runs any
// beforeMapping check (a preview, say), sets mapping and commits; it returns
// the commit response code and body and the import as it stands once
// normalization ends.
func acceptanceImport(t *testing.T, server *Server, store storage.Store, files []acceptanceImportFile, mapping analysis.ParquetMapping, beforeMapping ...func(storage.AnalysisImport)) (int, string, storage.AnalysisImport) {
	t.Helper()
	declared := []any{}
	for _, file := range files {
		declared = append(declared, map[string]any{"relative_path": file.name, "size": len(file.data)})
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "acceptance", "files": declared})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID
	for index, file := range files {
		req := httptest.NewRequest("PUT", path+"/files/"+in.Files[index].ID+"?offset=0", bytes.NewReader(file.data))
		req.Header.Set("Content-Type", "application/octet-stream")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("upload %s: %d %s", file.name, response.Code, response.Body.String())
		}
	}
	for _, check := range beforeMapping {
		check(in)
	}
	current, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	r = timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping})
	if r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	r = timeslotRequest(t, server, "POST", path+"/commit", nil)
	code, body := r.Code, r.Body.String()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if current, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID); err != nil {
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
	return code, body, *current
}

func TestAnalysisAcceptanceDuplicateAndDriftedFiles(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newTimeslotServer(t)
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"}}}
	first := acceptanceParquet(t, "SELECT 1700000040+i AS ts, (100+i)::DOUBLE AS latency FROM range(60) t(i)")

	// Two distinct files import every row of both.
	code, _, in := acceptanceImport(t, server, store, []acceptanceImportFile{{"a.parquet", first}, {"b.parquet", acceptanceParquet(t, "SELECT 1700000100+i AS ts, (200+i)::DOUBLE AS latency FROM range(60) t(i)")}}, mapping)
	if code != 202 || in.State != "ready" || in.Accepted != 120 {
		t.Fatalf("two files: commit %d, %s with %d accepted", code, in.State, in.Accepted)
	}

	// The same content twice is refused before normalization, naming both
	// files; the import is left as it was (mapped), to be replaced.
	code, body, in := acceptanceImport(t, server, store, []acceptanceImportFile{{"a.parquet", first}, {"copy of a.parquet", first}}, mapping)
	if code != 422 || !strings.Contains(body, `\"a.parquet\" and \"copy of a.parquet\" have the same content; create a new import without one of them`) || in.State != "mapped" || in.Accepted != 0 {
		t.Fatalf("duplicate content: commit %d %s, state %s", code, body, in.State)
	}

	// A file whose schema drifted from the mapping fails the import closed:
	// nothing is accepted, and the stored failure names the file and the
	// mapped column it lacks or holds as a list, with no engine detail.
	for _, drifted := range []struct{ query, want string }{
		{"SELECT 1700000100+i AS ts, (200+i)::DOUBLE AS latency_ms FROM range(60) t(i)", `File "b.parquet" does not match the mapping: it has no column "latency".`},
		{"SELECT 1700000100+i AS ts, [(200+i)::DOUBLE] AS latency FROM range(60) t(i)", `File "b.parquet" does not match the mapping: column "latency" is a list or repeated field, which cannot be mapped to a scalar measurement.`},
		// Rows whose value is not a number are rejected; unaccepted, they
		// fail the import naming the file, the measure and the first rows.
		{"SELECT 1700000100+i AS ts, CASE WHEN i % 10 = 3 THEN 'n/a' ELSE (200+i)::VARCHAR END AS latency FROM range(60) t(i)", `File "b.parquet" has 6 rows whose measure "latency" was rejected (first source rows, counting from 0: 3, 13, 23, 33, 43). Accept rejected measurements to import the rest, or correct the file.`},
	} {
		code, _, in = acceptanceImport(t, server, store, []acceptanceImportFile{{"a.parquet", first}, {"b.parquet", acceptanceParquet(t, drifted.query)}}, mapping)
		if code != 202 || in.State != "failed" || in.Accepted != 0 || in.PartitionManifest != nil || in.Error != drifted.want {
			t.Fatalf("drifted file: commit %d, %s with %d accepted: %q", code, in.State, in.Accepted, in.Error)
		}
	}

	// A mapped column other than the value (here a long table's name
	// column, the third column the mapping reads) is named the same way.
	long := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, NameColumn: []string{"metric"}, Kind: analysis.KindGauge, Unit: "ms"}}}
	named := acceptanceParquet(t, "SELECT 1700000040+i AS ts, 'latency' AS metric, (100+i)::DOUBLE AS value FROM range(60) t(i)")
	unnamed := acceptanceParquet(t, "SELECT 1700000100+i AS ts, (200+i)::DOUBLE AS value FROM range(60) t(i)")
	code, _, in = acceptanceImport(t, server, store, []acceptanceImportFile{{"a.parquet", named}, {"b.parquet", unnamed}}, long)
	if want := `File "b.parquet" does not match the mapping: it has no column "metric".`; code != 202 || in.State != "failed" || in.Accepted != 0 || in.Error != want {
		t.Fatalf("missing name column: commit %d, %s with %d accepted: %q", code, in.State, in.Accepted, in.Error)
	}
}
