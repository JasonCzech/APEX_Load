//go:build analytics_duckdb && cgo

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestNativeAnalyticsReferenceBenchmark is the NA-6 reference workload
// (B8), opt-in under APEXLOAD_ANALYTICS_BENCHMARK=1. It generates a
// reproducible multi-file, day-partitioned, ZSTD-compressed Parquet fixture
// (APEXLOAD_ANALYTICS_BENCHMARK_BYTES, default 1 GiB), imports it through the
// API (upload, mapping, commit, normalization), then times a selected-window
// query (one hour of one day at 60-second buckets: cold, then repeated), a
// full-range query, and the cancellation of a heavy query, recording the
// pruning, peak controller and worker RSS and every failure. With
// APEXLOAD_ANALYTICS_BENCHMARK_OUT it writes the results there, and an F4
// acceptance request when APEXLOAD_COMMIT_SHA names the measured commit.
func TestNativeAnalyticsReferenceBenchmark(t *testing.T) {
	if os.Getenv("APEXLOAD_ANALYTICS_BENCHMARK") != "1" {
		t.Skip("set APEXLOAD_ANALYTICS_BENCHMARK=1 to run the native analytics reference benchmark")
	}
	target := int64(1 << 30)
	if raw := strings.TrimSpace(os.Getenv("APEXLOAD_ANALYTICS_BENCHMARK_BYTES")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1<<20 || parsed > analyticsReferenceMaxBytes {
			t.Fatalf("APEXLOAD_ANALYTICS_BENCHMARK_BYTES must be between 1 MiB and 6 GiB, got %q", raw)
		}
		target = parsed
	}
	scratch := t.TempDir()
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", filepath.Join(scratch, "archives"))
	server, store := newTimeslotServer(t)
	profile, err := analytics.ApproveNativeResourceProfile(os.Getenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT"), os.Getenv("APEXLOAD_ANALYSIS_SPILL_LIMIT"))
	if err != nil {
		t.Fatal(err)
	}
	limits, err := profile.Limits()
	if err != nil {
		t.Fatal(err)
	}
	results := analyticsReferenceResults{Schema: "apexload.analytics_reference.v1", GeneratedAt: time.Now().UTC(), Host: analyticsReferenceHostInfo(),
		MemoryLimit: fmt.Sprintf("%d MiB", limits.MemoryBytes>>20), SpillLimit: fmt.Sprintf("%d MiB", limits.SpillBytes>>20),
		Failures:    []string{},
		NotMeasured: []string{"spill bytes (bounded by the spill limit, not measured)", "live telemetry lag under a concurrent run", "a cold replica, a worker crash, disk exhaustion and contention"}}
	fail := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		results.Failures = append(results.Failures, message)
		t.Error(message)
	}
	defer func() {
		if dir := strings.TrimSpace(os.Getenv("APEXLOAD_ANALYTICS_BENCHMARK_OUT")); dir != "" {
			writeAnalyticsReferenceAcceptance(t, dir, results)
		}
	}()

	// The fixture: one file per day, a row every 25 ms, ZSTD-compressed, with
	// values derived from a hash of the row (no random seed), until the files
	// reach the target size.
	const rowsPerDay = 3_456_000 // 25 ms spacing over a day
	const dayStart = int64(1_700_006_400_000)
	results.FixtureSQL = "SELECT day_start_ms + i*25 AS ts, (hash(i, day) % 200000) / 100.0 AS latency, (hash(day, i) % 16)::DOUBLE AS errors FROM range(3456000) t(i), ZSTD"
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(scratch, "fixture")
	if err := os.MkdirAll(fixtureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two measures per file, at most 1,024 normalized partitions, and no more
	// days than one series query may span.
	for day := 0; results.TotalBytes < target && day < analyticsReferenceMaxDays; day++ {
		path := filepath.Join(fixtureDir, fmt.Sprintf("day-%02d.parquet", day))
		start := dayStart + int64(day)*86_400_000
		query := fmt.Sprintf("COPY (SELECT %d + i*25 AS ts, (hash(i, %d) %% 200000) / 100.0 AS latency, (hash(%d, i) %% 16)::DOUBLE AS errors FROM range(%d) t(i)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)", start, day, day, rowsPerDay, path)
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		results.Files = append(results.Files, analyticsReferenceFile{Name: filepath.Base(path), Bytes: int64(len(data)), Rows: rowsPerDay, SHA256: hex.EncodeToString(digest[:])})
		results.TotalBytes += int64(len(data))
		results.TotalRows += rowsPerDay
	}
	// The fixture writer's memory is not the controller's: close it before
	// the import, and sample RSS only from here on.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: %d files, %d bytes, %d rows", len(results.Files), results.TotalBytes, results.TotalRows)
	if results.TotalBytes < target {
		fail("the fixture reached %d of the %d bytes requested", results.TotalBytes, target)
		return
	}
	sampler := startReferenceRSSSampler(250 * time.Millisecond)
	sampled := false
	stopSampling := func() {
		if sampled {
			return
		}
		sampled = true
		self, workers, combined, ok := sampler.stop()
		if !ok {
			results.NotMeasured = append(results.NotMeasured, "peak RSS (not measured on this platform)")
			return
		}
		results.PeakControllerRSSBytes, results.PeakWorkerRSSBytes, results.PeakCombinedRSSBytes = self, workers, combined
	}
	defer stopSampling()

	// Import through the API.
	declared := []any{}
	for _, file := range results.Files {
		declared = append(declared, map[string]any{"relative_path": file.Name, "size": file.Bytes})
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{"name": "reference", "files": declared})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	importPath, datasetPath := "/api/v1/analysis/imports/"+in.ID, "/api/v1/analysis/datasets/"+in.ID
	uploadStart := time.Now()
	for index, file := range results.Files {
		source, err := os.Open(filepath.Join(fixtureDir, file.Name))
		if err != nil {
			t.Fatal(err)
		}
		chunk := make([]byte, analysisUploadChunk)
		for offset := int64(0); offset < file.Bytes; {
			n, err := io.ReadFull(source, chunk)
			if err != nil && err != io.ErrUnexpectedEOF {
				t.Fatal(err)
			}
			req := httptest.NewRequest("PUT", fmt.Sprintf("%s/files/%s?offset=%d", importPath, in.Files[index].ID, offset), bytes.NewReader(chunk[:n]))
			req.Header.Set("Content-Type", "application/octet-stream")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("upload %s at %d: %d %s", file.Name, offset, response.Code, response.Body.String())
			}
			offset += int64(n)
		}
		source.Close()
	}
	results.UploadMs = float64(time.Since(uploadStart).Microseconds()) / 1000
	current, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []analysis.ParquetMeasure{
		{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"},
		{Value: []string{"errors"}, Name: "errors", Kind: analysis.KindGauge, Unit: "count"},
	}}
	if r = timeslotRequest(t, server, "PUT", importPath+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	normalizeStart := time.Now()
	if r = timeslotRequest(t, server, "POST", importPath+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if current, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID); err != nil {
			t.Fatal(err)
		}
		if current.State != "normalizing" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	results.NormalizeMs = float64(time.Since(normalizeStart).Microseconds()) / 1000
	results.Accepted, results.Rejected = current.Accepted, current.Rejected
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if current.State != "ready" || current.Accepted != 2*results.TotalRows || current.Rejected != 0 {
		fail("normalization ended %s with %d accepted and %d rejected of %d measurements: %s", current.State, current.Accepted, current.Rejected, 2*results.TotalRows, current.Error)
		return
	}
	t.Logf("import: upload %.1f s, normalize %.1f s", results.UploadMs/1000, results.NormalizeMs/1000)

	query := func(ctx context.Context, from time.Time, span time.Duration, bucket int) (AnalysisDatasetSeriesResponse, float64, int) {
		body, _ := json.Marshal(analytics.SeriesQuery{Metric: "latency", From: from, To: from.Add(span), BucketSeconds: bucket, Aggregation: "avg"})
		req := httptest.NewRequest("POST", datasetPath+"/series", bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		started := time.Now()
		server.mux.ServeHTTP(response, req)
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		var out AnalysisDatasetSeriesResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, elapsed, response.Code
	}
	// The selected window: one hour inside the last day.
	selected := time.UnixMilli(dayStart + int64(len(results.Files)-1)*86_400_000 + 3_600_000).UTC()
	cold, coldMs, code := query(context.Background(), selected, time.Hour, 60)
	results.ColdQueryMs = coldMs
	if code != 200 || len(cold.Points) != 60 {
		fail("selected-window query: %d with %d points", code, len(cold.Points))
	}
	for _, selection := range cold.ArtifactSelections {
		results.TotalObjects += selection.TotalObjects
		results.PrunedObjects += selection.PrunedObjects
		results.AvoidedBytes += selection.AvoidedArtifactBytes
		results.StagedBytes += selection.StagedArtifactBytes
	}
	var repeated []float64
	for i := 0; i < 20; i++ {
		_, elapsed, code := query(context.Background(), selected, time.Hour, 60)
		if code != 200 {
			fail("repeated query %d: %d", i, code)
		}
		repeated = append(repeated, elapsed)
	}
	results.RepeatedQueries = analyticsReferenceTimingOf(repeated)
	// The full range at hourly buckets: recorded, not a target (the series
	// endpoint bounds a query at one minute).
	full, fullMs, code := query(context.Background(), time.UnixMilli(dayStart).UTC(), time.Duration(len(results.Files))*24*time.Hour, 3600)
	results.FullRangeMs, results.FullRangeStatus = fullMs, code
	if code == 200 && len(full.Points) != 24*len(results.Files) {
		fail("full-range query answered %d points, want %d", len(full.Points), 24*len(results.Files))
	}
	// Cancel the same heavy query after 500 ms; it must return promptly and
	// the controller keep answering.
	ctx, cancel := context.WithCancel(context.Background())
	type answer struct {
		elapsed float64
		code    int
	}
	done := make(chan answer, 1)
	go func() {
		_, elapsed, code := query(ctx, time.UnixMilli(dayStart).UTC(), time.Duration(len(results.Files))*24*time.Hour, 3600)
		done <- answer{elapsed, code}
	}()
	time.Sleep(500 * time.Millisecond)
	cancelled := time.Now()
	cancel()
	select {
	case got := <-done:
		switch {
		case got.elapsed < 500 && got.code != 200:
			results.NotMeasured = append(results.NotMeasured, fmt.Sprintf("cancellation (the heavy query was refused with %d before it could be cancelled)", got.code))
		case got.elapsed < 500:
			results.NotMeasured = append(results.NotMeasured, "cancellation (the heavy query finished before it was cancelled)")
		default:
			results.CancelReturnMs = float64(time.Since(cancelled).Microseconds()) / 1000
		}
	case <-time.After(30 * time.Second):
		fail("a cancelled heavy query did not return within 30 s")
	}
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, code := query(context.Background(), selected, time.Hour, 60); code != 200 {
		fail("the controller did not answer after the cancellation: %d", code)
	}
	stopSampling()
	t.Logf("queries: cold %.0f ms, repeated p50 %.0f / p95 %.0f / max %.0f ms, full range %.0f ms; pruned %d of %d objects (%d bytes avoided); cancel %.0f ms; peak RSS controller %d MiB, workers %d MiB, together %d MiB",
		results.ColdQueryMs, results.RepeatedQueries.P50Ms, results.RepeatedQueries.P95Ms, results.RepeatedQueries.MaxMs, results.FullRangeMs,
		results.PrunedObjects, results.TotalObjects, results.AvoidedBytes, results.CancelReturnMs, results.PeakControllerRSSBytes>>20, results.PeakWorkerRSSBytes>>20, results.PeakCombinedRSSBytes>>20)
	if results.RepeatedQueries.P95Ms > analyticsReferenceP95TargetMs {
		t.Logf("the selected-window P95 %.0f ms is above the %d ms target", results.RepeatedQueries.P95Ms, analyticsReferenceP95TargetMs)
	}
}

func analyticsReferenceTimingOf(samples []float64) analyticsReferenceTiming {
	if len(samples) == 0 {
		return analyticsReferenceTiming{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	rank := func(q float64) float64 {
		index := int(q*float64(len(sorted))+0.999999) - 1
		return sorted[max(0, min(len(sorted)-1, index))]
	}
	return analyticsReferenceTiming{Samples: len(sorted), P50Ms: rank(0.5), P95Ms: rank(0.95), MaxMs: sorted[len(sorted)-1]}
}

func analyticsReferenceHostInfo() analyticsReferenceHost {
	host := analyticsReferenceHost{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			host.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		}
	case "linux":
		if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemTotal:" {
					kib, _ := strconv.ParseInt(fields[1], 10, 64)
					host.MemoryBytes = kib * 1024
				}
			}
		}
	}
	gib := float64(host.MemoryBytes) / (1 << 30)
	host.Reference = host.CPUs == 4 && gib >= 15 && gib <= 17
	return host
}
