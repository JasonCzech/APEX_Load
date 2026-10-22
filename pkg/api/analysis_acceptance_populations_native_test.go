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
