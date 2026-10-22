//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Analytical acceptance (NA-A): raw observations with nulls and a counter that
// resets between two files and again inside one reconcile across preview,
// commit and query with DuckDB's own SQL over the source files (independent
// of the normalization and series code; percentiles use DuckDB's linear
// interpolation, the definition the engine uses), and the stored originals
// keep the sources' bytes. A null measurement is a missing sample, so its
// whole bucket reads as missing.
func TestAnalysisAcceptanceRawAndCounterPopulationsReconcile(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newTimeslotServer(t)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const start = 1700000040 // a minute boundary
	dir := t.TempDir()
	write := func(name, query string) (string, []byte) {
		path := filepath.Join(dir, name)
		if _, err := db.Exec("COPY (" + query + ") TO '" + path + "' (FORMAT PARQUET)"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return path, data
	}
	// File a: two minutes, with latency nulls in the second minute only and
	// requests counting up by irregular steps, so a minute's opening and
	// closing readings differ by different amounts. File b: five minutes;
	// requests restart near zero (a reset between files) and again in its
	// fourth minute (a reset inside one file).
	const steps = "sum(((i*7) % 11)+1) OVER (ORDER BY i)"
	aPath, a := write("a.parquet", "SELECT "+strconv.Itoa(start)+"+i AS ts, CASE WHEN i >= 60 AND i % 10 = 9 THEN NULL ELSE ((i*37) % 100)::DOUBLE END AS latency, (1000+"+steps+")::DOUBLE AS requests FROM range(120) t(i) ORDER BY ts")
	bPath, b := write("b.parquet", "SELECT "+strconv.Itoa(start+120)+"+i AS ts, ((i*53) % 100)::DOUBLE AS latency, (3+sum(((i*7) % 11)+1) OVER (PARTITION BY i >= 200 ORDER BY i))::DOUBLE AS requests FROM range(300) t(i) ORDER BY ts")
	sources := "read_parquet(['" + aPath + "', '" + bPath + "'])"
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{
		{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"},
		{Value: []string{"requests"}, Name: "requests", Kind: analysis.KindCounter, Unit: "count"},
	}}

	// Preview: each file's schema and first samples match the source.
	previewed := func(in storage.AnalysisImport) {
		for index, rows := range []int{120, 300} {
			r := timeslotRequest(t, server, "GET", "/api/v1/analysis/imports/"+in.ID+"/files/"+in.Files[index].ID+"/preview", nil)
			var preview analytics.Preview
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &preview) != nil || preview.Schema.Rows != int64(rows) || len(preview.Samples) == 0 {
				t.Fatalf("preview %d: %d %s", index, r.Code, r.Body.String())
			}
			if first := preview.Samples[0]["requests"]; index == 0 && first != 1001.0 || index == 1 && first != 4.0 {
				t.Fatalf("preview %d first requests = %v", index, first)
			}
		}
	}
	code, body, in := acceptanceImport(t, server, store, []acceptanceImportFile{{"a.parquet", a}, {"b.parquet", b}}, mapping, previewed)
	if code != 202 || in.State != "ready" || in.Rejected != 0 {
		t.Fatalf("commit %d %s: %s, %d accepted, %d rejected", code, body, in.State, in.Accepted, in.Rejected)
	}
	var measurements int64
	if err := db.QueryRow("SELECT 2*count(*) FROM " + sources).Scan(&measurements); err != nil || in.Accepted != measurements {
		t.Fatalf("accepted %d of %d measurements: %v", in.Accepted, measurements, err)
	}
	// The upload arrived intact, and after commit the stored originals still
	// hold the sources' exact bytes.
	for index, data := range [][]byte{a, b} {
		digest := sha256.Sum256(data)
		if in.Files[index].SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("file %d digest %q, source %x", index, in.Files[index].SHA256, digest)
		}
		var stored bytes.Buffer
		key := analysisObjectKey(in.ProjectID, in.ID, "original-"+in.Files[index].ID+".parquet")
		if _, err := analysisObjectRead(context.Background(), key, &stored, int64(len(data))+1); err != nil || !bytes.Equal(stored.Bytes(), data) {
			t.Fatalf("file %d stored original differs from the source: %d bytes, %v", index, stored.Len(), err)
		}
	}

	from := time.Unix(start, 0).UTC()
	const minutes = 7
	query := func(metric, aggregation string, derivation *analysis.QueryDerivation) AnalysisDatasetSeriesResponse {
		t.Helper()
		r := timeslotRequest(t, server, "POST", "/api/v1/analysis/datasets/"+in.ID+"/series", analytics.SeriesQuery{Metric: metric, From: from, To: from.Add(minutes * time.Minute), BucketSeconds: 60, Aggregation: aggregation, Derivation: derivation})
		var out AnalysisDatasetSeriesResponse
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.Points) != minutes {
			t.Fatalf("%s %s: %d %s", metric, aggregation, r.Code, r.Body.String())
		}
		return out
	}

	// Each minute's raw observations match the sources; the minute holding
	// nulls is missing for every aggregation, never fewer samples or zero.
	rows, err := db.Query("SELECT count(latency), sum(latency), min(latency), max(latency), avg(latency), quantile_cont(latency, 0.5), quantile_cont(latency, 0.95), count(*) - count(latency) FROM " + sources + " GROUP BY (ts-" + strconv.Itoa(start) + ") // 60 ORDER BY (ts-" + strconv.Itoa(start) + ") // 60")
	if err != nil {
		t.Fatal(err)
	}
	const nulls = 7
	var oracle [][8]float64
	for rows.Next() {
		var row [8]float64
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5], &row[6], &row[7]); err != nil {
			t.Fatal(err)
		}
		oracle = append(oracle, row)
	}
	rows.Close()
	if len(oracle) != minutes || oracle[1][nulls] == 0 {
		t.Fatalf("oracle buckets = %v", oracle)
	}
	for column, aggregation := range []string{"count", "sum", "min", "max", "avg", "p50", "p95"} {
		for bucket, point := range query("latency", aggregation, nil).Points {
			if oracle[bucket][nulls] > 0 {
				if point.Value != nil || point.Quality != analysis.QualityMissing {
					t.Fatalf("latency %s bucket %d with nulls = %+v, want missing", aggregation, bucket, point)
				}
				continue
			}
			want := oracle[bucket][column]
			if point.Value == nil || math.Abs(*point.Value-want) > 1e-9*math.Max(1, math.Abs(want)) {
				t.Fatalf("latency %s bucket %d = %+v, the sources give %v", aggregation, bucket, point, want)
			}
		}
	}

	// The counter's delta per minute: the first minute has no prior sample, a
	// reading that falls is a reset, the minute after a reset lacks its prior
	// reading (missing_counter_sample), and every other delta is the sources'
	// difference of closing (last) readings.
	var closing [minutes]float64
	for bucket := range closing {
		if err := db.QueryRow("SELECT arg_max(requests, ts) FROM "+sources+" WHERE (ts-"+strconv.Itoa(start)+") // 60 = ?", bucket).Scan(&closing[bucket]); err != nil {
			t.Fatal(err)
		}
	}
	derived := query("requests", "max", &analysis.QueryDerivation{Operation: "delta"}).Derived
	if derived == nil || len(derived.Buckets) != minutes {
		t.Fatalf("delta = %+v", derived)
	}
	want := []struct {
		value  float64
		reason string
	}{{0, "no_prior_sample"}, {closing[1] - closing[0], ""}, {0, analysis.MissingCounterReset}, {0, "missing_counter_sample"}, {closing[4] - closing[3], ""}, {0, analysis.MissingCounterReset}, {0, "missing_counter_sample"}}
	for bucket, expected := range want {
		got := derived.Buckets[bucket]
		if got.Reason != expected.reason || expected.reason == "" && (got.Value == nil || *got.Value != expected.value) || expected.reason != "" && got.Value != nil {
			t.Fatalf("delta bucket %d = %+v, want %+v", bucket, got, expected)
		}
	}
}

// Analytical acceptance (NA-A): an import that accepts rejected measurements
// keeps only the readable ones, and its rejected count, its downloadable
// rejection rows and its queried counts and sums reconcile with DuckDB's own
// SQL over the source.
func TestAnalysisAcceptanceAcceptedRejectionsReconcile(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newTimeslotServer(t)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const start = 1700000040
	source := filepath.Join(t.TempDir(), "words.parquet")
	if _, err := db.Exec("COPY (SELECT " + strconv.Itoa(start) + "+i AS ts, CASE WHEN i % 13 = 4 THEN 'n/a' ELSE ((i*31) % 100)::VARCHAR END AS latency FROM range(180) t(i)) TO '" + source + "' (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the source's exact bytes.
	created := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "rejections", "files": []any{map[string]any{"relative_path": "words.parquet", "size": len(data)}}})
	var in storage.AnalysisImport
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &in) != nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	path, dataset := "/api/v1/analysis/imports/"+in.ID, "/api/v1/analysis/datasets/"+in.ID
	upload := httptest.NewRequest("PUT", path+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
	upload.Header.Set("Content-Type", "application/octet-stream")
	uploaded := httptest.NewRecorder()
	server.mux.ServeHTTP(uploaded, upload)
	if uploaded.Code != 200 {
		t.Fatalf("upload: %d", uploaded.Code)
	}
	// Preview: the file's row count and first timestamp, before any rows are
	// judged (the text column is redacted there).
	previewed := timeslotRequest(t, server, "GET", path+"/files/"+in.Files[0].ID+"/preview", nil)
	var preview analytics.Preview
	if previewed.Code != 200 || json.Unmarshal(previewed.Body.Bytes(), &preview) != nil || preview.Schema.Rows != 180 || len(preview.Samples) == 0 || preview.Samples[0]["ts"] != float64(start) {
		t.Fatalf("preview: %d %s", previewed.Code, previewed.Body.String())
	}
	current, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}
	if r := timeslotRequest(t, server, "PUT", path+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping, "allow_rejected": true}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	if r := timeslotRequest(t, server, "POST", path+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
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
	// The engine's contract: a finite number is a measurement, a null is an
	// accepted missing sample, and anything else is rejected.
	const readablePredicate = "coalesce(isfinite(try_cast(latency AS DOUBLE)), false)"
	const rejectedPredicate = "latency IS NOT NULL AND NOT " + readablePredicate
	var accepted, unreadable int64
	if err := db.QueryRow("SELECT count(*) FILTER (WHERE latency IS NULL OR "+readablePredicate+"), count(*) FILTER (WHERE "+rejectedPredicate+") FROM read_parquet('"+source+"')").Scan(&accepted, &unreadable); err != nil {
		t.Fatal(err)
	}
	if current.State != "ready" || current.Accepted != accepted || current.Rejected != unreadable || unreadable == 0 {
		t.Fatalf("commit: %s, %d accepted (source %d), %d rejected (source %d): %q", current.State, current.Accepted, accepted, current.Rejected, unreadable, current.Error)
	}
	if digest := sha256.Sum256(data); current.Files[0].SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("source digest %q", current.Files[0].SHA256)
	}

	// The rejection download names exactly the unreadable source rows.
	r := timeslotRequest(t, server, "GET", dataset+"/rejections/0", nil)
	if r.Code != 200 {
		t.Fatalf("rejections: %d %s", r.Code, r.Body.String())
	}
	rejections := filepath.Join(t.TempDir(), "rejections.parquet")
	if err := os.WriteFile(rejections, r.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	rejectedRows := "SELECT file_row_number FROM read_parquet('" + source + "', file_row_number=true) WHERE " + rejectedPredicate
	var mismatched int64
	if err := db.QueryRow("SELECT count(*) FROM (SELECT source_row FROM read_parquet('" + rejections + "') EXCEPT " + rejectedRows + " UNION ALL SELECT file_row_number FROM (" + rejectedRows + " EXCEPT SELECT source_row FROM read_parquet('" + rejections + "')))").Scan(&mismatched); err != nil || mismatched != 0 {
		t.Fatalf("rejection rows differ from the source's unreadable rows by %d: %v", mismatched, err)
	}
	// Each rejected row appears once, attributed to the one file and measure,
	// with the engine's fixed reason.
	var listed, distinct int64
	var attributed bool
	if err := db.QueryRow("SELECT count(*), count(DISTINCT source_row), coalesce(bool_and(source_file = 0 AND mapped_measure = 0 AND reason = 'invalid_timestamp_name_or_value'), false) FROM read_parquet('"+rejections+"')").Scan(&listed, &distinct, &attributed); err != nil || listed != unreadable || distinct != unreadable || !attributed {
		t.Fatalf("rejection artifact: %d rows, %d distinct, attributed %t (want %d): %v", listed, distinct, attributed, unreadable, err)
	}

	// Queried minutes hold only the readable measurements, and every series
	// response reports the rejected count.
	from := time.Unix(start, 0).UTC()
	for _, aggregation := range []string{"count", "sum"} {
		r := timeslotRequest(t, server, "POST", dataset+"/series", analytics.SeriesQuery{Metric: "latency", From: from, To: from.Add(3 * time.Minute), BucketSeconds: 60, Aggregation: aggregation})
		var out AnalysisDatasetSeriesResponse
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.Points) != 3 || out.RejectedRows != unreadable {
			t.Fatalf("%s: %d %s", aggregation, r.Code, r.Body.String())
		}
		for bucket, point := range out.Points {
			var want float64
			if err := db.QueryRow("SELECT "+map[string]string{"count": "count", "sum": "sum"}[aggregation]+"(try_cast(latency AS DOUBLE)) FILTER (WHERE "+readablePredicate+") FROM read_parquet('"+source+"') WHERE (ts-"+strconv.Itoa(start)+") // 60 = ?", bucket).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if point.Value == nil || *point.Value != want {
				t.Fatalf("%s bucket %d = %+v, the readable source rows give %v", aggregation, bucket, point, want)
			}
		}
	}
}
