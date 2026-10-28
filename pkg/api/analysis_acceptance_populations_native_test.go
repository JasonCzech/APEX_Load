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

// Analytical acceptance (NA-A1): API-level golden verification covering:
//   - multi-chunk duplicate content uploaded with different chunk boundaries
//     (2 chunks vs. 3 chunks vs. single chunk) failing at commit (422) and
//     naming both source files;
//   - long, wide, and nested schemas;
//   - intra-file DST transitions (never inferring a file's time grid from its
//     first window);
//   - DECIMAL beyond float64 precision consent;
//   - conflicting partition columns vs. constants; and
//   - client capability-version gating for summary/histogram reassociation.
func TestAnalysisAcceptanceAPIGoldensAndChunkDuplicateProvenance(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newTimeslotServer(t)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	writeParquet := func(name, query string) []byte {
		t.Helper()
		path := filepath.Join(dir, name)
		if _, err := db.Exec("COPY (" + query + ") TO '" + path + "' (FORMAT PARQUET)"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	// 1. Multi-chunk duplicate content across 2-chunk, 3-chunk, and single-chunk boundaries.
	t.Run("multi-chunk-duplicate-provenance", func(t *testing.T) {
		data := writeParquet("dup.parquet", "SELECT 1700000040+i AS ts, (i*1.5)::DOUBLE AS latency FROM range(200) t(i)")
		uploadChunks := func(importID, fileID string, splits []int) {
			offset := 0
			for _, n := range splits {
				req := httptest.NewRequest("PUT", "/api/v1/analysis/imports/"+importID+"/files/"+fileID+"?offset="+strconv.Itoa(offset), bytes.NewReader(data[offset:offset+n]))
				req.Header.Set("Content-Type", "application/octet-stream")
				res := httptest.NewRecorder()
				server.mux.ServeHTTP(res, req)
				if res.Code != 200 {
					t.Fatalf("upload offset %d size %d: %d %s", offset, n, res.Code, res.Body.String())
				}
				offset += n
			}
		}
		mapping := analysis.ParquetMapping{
			Version:           1,
			Timestamp:         []string{"ts"},
			TimestampEncoding: "unix_s",
			Timezone:          "UTC",
			Measures:          []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}},
		}
		half := len(data) / 2
		third := len(data) / 3
		for _, tc := range []struct {
			first, second string
			aSplits       []int
			bSplits       []int
		}{
			{"two-chunk.parquet", "three-chunk.parquet", []int{half, len(data) - half}, []int{third, third, len(data) - 2*third}},
			{"single-chunk.parquet", "three-chunk.parquet", []int{len(data)}, []int{third, third, len(data) - 2*third}},
		} {
			created := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{
				"name": "dup-" + tc.first,
				"files": []any{
					map[string]any{"relative_path": tc.first, "size": len(data)},
					map[string]any{"relative_path": tc.second, "size": len(data)},
				},
			})
			var in storage.AnalysisImport
			if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &in) != nil {
				t.Fatalf("create: %d %s", created.Code, created.Body.String())
			}
			uploadChunks(in.ID, in.Files[0].ID, tc.aSplits)
			uploadChunks(in.ID, in.Files[1].ID, tc.bSplits)
			cur, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
			if err != nil {
				t.Fatal(err)
			}
			if r := timeslotRequest(t, server, "PUT", "/api/v1/analysis/imports/"+in.ID+"/mapping", map[string]any{"revision": cur.Revision, "mapping": mapping}); r.Code != 200 {
				t.Fatalf("map: %d %s", r.Code, r.Body.String())
			}
			committed := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+in.ID+"/commit", nil)
			want := `files "` + tc.first + `" and "` + tc.second + `" have the same content; create a new import without one of them`
			var errResp map[string]string
			_ = json.Unmarshal(committed.Body.Bytes(), &errResp)
			if committed.Code != 422 || errResp["error"] != want {
				t.Fatalf("commit %s vs %s: %d %s, want 422 with %q", tc.first, tc.second, committed.Code, committed.Body.String(), want)
			}
		}
	})

	// 2. Long, wide, and nested schemas + intra-file DST transition + reassociation capability gate.
	t.Run("long-wide-nested-dst-and-reassociation-gate", func(t *testing.T) {
		// America/New_York spring-forward on 2026-03-08:
		// 01:59:00 EST (2026-03-08T06:59:00Z = 1772953140) -> 03:00:00 EDT (2026-03-08T07:00:00Z = 1772953200).
		// Local wall timestamps inside a single file cross the spring-forward boundary, proving
		// the normalizer never infers the file's time grid from its first window.
		nestedAndDST := writeParquet("nested_dst.parquet", `
			SELECT * FROM (VALUES
				('2026-03-08 01:58:10'::TIMESTAMP, {'http': {'p95_ms': 12.0::DOUBLE, 'ok_ms': 10.0::DOUBLE}}, 40.0::DOUBLE, 'cpu_pct', 'us-east'),
				('2026-03-08 01:59:20'::TIMESTAMP, {'http': {'p95_ms': 18.0::DOUBLE, 'ok_ms': 14.0::DOUBLE}}, 44.0::DOUBLE, 'cpu_pct', 'us-east'),
				('2026-03-08 03:00:15'::TIMESTAMP, {'http': {'p95_ms': 30.0::DOUBLE, 'ok_ms': 22.0::DOUBLE}}, 52.0::DOUBLE, 'cpu_pct', 'us-east'),
				('2026-03-08 03:01:25'::TIMESTAMP, {'http': {'p95_ms': 24.0::DOUBLE, 'ok_ms': 18.0::DOUBLE}}, 48.0::DOUBLE, 'cpu_pct', 'us-east')
			) AS v(wall_ts, service, metric_val, metric_name, region)
		`)
		mapping := analysis.ParquetMapping{
			Version:           2,
			Timestamp:         []string{"wall_ts"},
			TimestampEncoding: "timestamp",
			Timezone:          "America/New_York",
			Identity: &analysis.ParquetIdentityMapping{
				Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: []string{"region"}}},
			},
			Measures: []analysis.ParquetMeasure{
				// Wide + nested struct paths (summary + observation) and long schema (metric name from column):
				{Value: []string{"service", "http", "p95_ms"}, Name: "checkout_p95", Kind: analysis.KindSummary, Statistic: "p95", WindowSeconds: 60, Unit: "ms"},
				{Value: []string{"service", "http", "ok_ms"}, Name: "checkout_ok", Kind: analysis.KindObservation, Unit: "ms"},
				{Value: []string{"metric_val"}, NameColumn: []string{"metric_name"}, Kind: analysis.KindObservation, Unit: "%"},
			},
		}
		noopPreview := func(storage.AnalysisImport) {}
		code, body, in := acceptanceImport(t, server, store, []acceptanceImportFile{{"nested_dst.parquet", nestedAndDST}}, mapping, noopPreview)
		if code != 202 || in.State != "ready" || in.Accepted != 12 || in.Rejected != 0 {
			t.Fatalf("commit nested_dst: %d %s (state=%s accepted=%d rejected=%d err=%q)", code, body, in.State, in.Accepted, in.Rejected, in.Error)
		}

		// Verify intra-file DST transition placed rows into the exact 4 consecutive UTC minutes:
		// 06:58Z, 06:59Z, 07:00Z, 07:01Z.
		from := time.Date(2026, 3, 8, 6, 58, 0, 0, time.UTC)
		seriesRes := timeslotRequest(t, server, "POST", "/api/v1/analysis/datasets/"+in.ID+"/series", analytics.SeriesQuery{
			Metric: "cpu_pct", From: from, To: from.Add(4 * time.Minute), BucketSeconds: 60, Aggregation: "avg",
		})
		var seriesOut AnalysisDatasetSeriesResponse
		if seriesRes.Code != 200 || json.Unmarshal(seriesRes.Body.Bytes(), &seriesOut) != nil || len(seriesOut.Points) != 4 {
			t.Fatalf("dst series: %d %s", seriesRes.Code, seriesRes.Body.String())
		}
		for i, want := range []float64{40, 44, 52, 48} {
			if seriesOut.Points[i].Value == nil || *seriesOut.Points[i].Value != want {
				t.Fatalf("dst bucket %d = %+v, want %v", i, seriesOut.Points[i], want)
			}
		}

		// Summary/histogram reassociation capability-version gating:
		// PATCH /association requires ContractVersion == 1 (rejecting 0, 2, -1 with 409).
		if err := store.SaveTest(&storage.TestRecord{ID: "goldens-run", ProjectID: "proj-a", Name: "run", Status: "COMPLETE", StartTime: from}); err != nil {
			t.Fatal(err)
		}
		for _, badVer := range []int{0, 2, -1} {
			badVerAssoc := timeslotRequest(t, server, "PATCH", "/api/v1/analysis/datasets/"+in.ID+"/association", AnalysisMappingAssociationRequest{
				ContractVersion: badVer, RunID: "goldens-run", Revision: in.Revision,
			})
			if badVerAssoc.Code != 409 {
				t.Fatalf("PATCH association with unsupported contract_version=%d = %d %s, want 409", badVer, badVerAssoc.Code, badVerAssoc.Body.String())
			}
		}
		goodAssoc := timeslotRequest(t, server, "PATCH", "/api/v1/analysis/datasets/"+in.ID+"/association", AnalysisMappingAssociationRequest{
			ContractVersion: 1, RunID: "goldens-run", Revision: in.Revision,
		})
		if goodAssoc.Code != 200 {
			t.Fatalf("PATCH association with contract_version=1 = %d %s, want 200", goodAssoc.Code, goodAssoc.Body.String())
		}
	})

	// 3. DECIMAL beyond float64 precision requires explicit precision review and acknowledgement consent.
	t.Run("decimal-precision-consent", func(t *testing.T) {
		decParquet := writeParquet("decimal.parquet", "SELECT 1700000040+i AS ts, (12345678901234567.89::DECIMAL(38,2)) AS amount FROM range(4) t(i)")
		mapping := analysis.ParquetMapping{
			Version:           2,
			Timestamp:         []string{"ts"},
			TimestampEncoding: "unix_s",
			Timezone:          "UTC",
			Measures:          []analysis.ParquetMeasure{{Value: []string{"amount"}, Name: "amount", Kind: analysis.KindGauge, Unit: "count"}},
		}
		// Unconsented commit fails closed during normalization.
		noopPreview := func(storage.AnalysisImport) {}
		_, _, failedIn := acceptanceImport(t, server, store, []acceptanceImportFile{{"decimal.parquet", decParquet}}, mapping, noopPreview)
		if failedIn.State != "failed" || failedIn.Accepted != 0 {
			t.Fatalf("unconsented decimal commit = %+v, want failed with 0 accepted", failedIn)
		}
		// Reviewing and acknowledging under Version 3 + PrecisionAllowFloat64Rounding succeeds.
		mapping.Version = 3
		mapping.PrecisionPolicy = analysis.PrecisionAllowFloat64Rounding
		revRes := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+failedIn.ID+"/precision-review", map[string]any{
			"revision": failedIn.Revision, "mapping": mapping,
		})
		var review AnalysisPrecisionReview
		if revRes.Code != 200 || json.Unmarshal(revRes.Body.Bytes(), &review) != nil || review.Review == nil || len(review.Review.Risks) != 1 {
			t.Fatalf("precision-review = %d %s", revRes.Code, revRes.Body.String())
		}
		mapRes := timeslotRequest(t, server, "PUT", "/api/v1/analysis/imports/"+failedIn.ID+"/mapping", map[string]any{
			"revision":                         failedIn.Revision,
			"mapping":                          mapping,
			"precision_review_sha256":          review.Review.SHA256,
			"precision_acknowledgement_sha256": review.AcknowledgementSHA256,
		})
		if mapRes.Code != 200 {
			t.Fatalf("consented mapping = %d %s", mapRes.Code, mapRes.Body.String())
		}
		commitRes := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+failedIn.ID+"/commit", nil)
		if commitRes.Code != 202 {
			t.Fatalf("consented commit = %d %s", commitRes.Code, commitRes.Body.String())
		}
		deadline := time.Now().Add(15 * time.Second)
		var readyIn *storage.AnalysisImport
		for time.Now().Before(deadline) {
			cur, err := store.(storage.AnalysisImportStore).GetAnalysisImport(failedIn.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cur.State != "normalizing" && len(analysisPipelineJobs) == 0 {
				readyIn = cur
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if readyIn == nil || readyIn.State != "ready" || readyIn.Accepted != 4 {
			t.Fatalf("consented decimal import = %+v", readyIn)
		}
	})

	// 4. Conflicting partition columns vs. constants: directory partition labels cannot override
	// physical columns, and specifying both a constant Name and NameColumn on a measure is rejected.
	t.Run("conflicting-partition-column-vs-constant", func(t *testing.T) {
		partParquet := writeParquet("part_conflict.parquet", "SELECT 1700000040+i AS ts, 10.0::DOUBLE AS latency, 'physical_metric'::VARCHAR AS metric_col, 'us-west'::VARCHAR AS region FROM range(5) t(i)")
		ambiguousMapping := analysis.ParquetMapping{
			Version:           2,
			Timestamp:         []string{"ts"},
			TimestampEncoding: "unix_s",
			Timezone:          "UTC",
			Identity: &analysis.ParquetIdentityMapping{
				Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: []string{"region"}}},
			},
			Measures: []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "constant_metric", NameColumn: []string{"metric_col"}, Kind: analysis.KindObservation, Unit: "ms"}},
		}
		created := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{
			"name":  "partition-conflict",
			"files": []any{map[string]any{"relative_path": "region=us-east/metric_col=injected/part_conflict.parquet", "size": len(partParquet)}},
		})
		var in storage.AnalysisImport
		if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &in) != nil {
			t.Fatalf("create: %d %s", created.Code, created.Body.String())
		}
		req := httptest.NewRequest("PUT", "/api/v1/analysis/imports/"+in.ID+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(partParquet))
		req.Header.Set("Content-Type", "application/octet-stream")
		res := httptest.NewRecorder()
		server.mux.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("upload: %d %s", res.Code, res.Body.String())
		}
		cur, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
		if err != nil {
			t.Fatal(err)
		}
		refused := timeslotRequest(t, server, "PUT", "/api/v1/analysis/imports/"+in.ID+"/mapping", map[string]any{
			"revision": cur.Revision, "mapping": ambiguousMapping,
		})
		if refused.Code != 400 {
			t.Fatalf("ambiguous constant+column mapping = %d %s, want 400", refused.Code, refused.Body.String())
		}

		// When mapped cleanly, physical column ('us-west') wins over directory partition label ('region=us-east').
		cleanMapping := ambiguousMapping
		cleanMapping.Measures = []analysis.ParquetMeasure{{Value: []string{"latency"}, NameColumn: []string{"metric_col"}, Kind: analysis.KindObservation, Unit: "ms"}}
		noopPreview := func(storage.AnalysisImport) {}
		code, body, readyIn := acceptanceImport(t, server, store, []acceptanceImportFile{{"region=us-east/part_conflict.parquet", partParquet}}, cleanMapping, noopPreview)
		if code != 202 || readyIn.State != "ready" || readyIn.Accepted != 5 {
			t.Fatalf("clean partition import: %d %s (%+v)", code, body, readyIn)
		}
		from := time.Unix(1700000040, 0).UTC()
		westRes := timeslotRequest(t, server, "POST", "/api/v1/analysis/datasets/"+readyIn.ID+"/series", analytics.SeriesQuery{
			Metric: "physical_metric", IdentitySchemaVersion: 2, Dimensions: map[string]string{"region": "us-west"}, From: from, To: from.Add(time.Minute), BucketSeconds: 60, Aggregation: "count",
		})
		var westOut AnalysisDatasetSeriesResponse
		if westRes.Code != 200 || json.Unmarshal(westRes.Body.Bytes(), &westOut) != nil || len(westOut.Points) != 1 || westOut.Points[0].Value == nil || *westOut.Points[0].Value != 5 {
			t.Fatalf("physical column query (us-west): %d %s", westRes.Code, westRes.Body.String())
		}
	})
}
