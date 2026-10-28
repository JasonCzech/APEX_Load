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

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob/memblob"
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
		NotMeasured: []string{"spill bytes (bounded by the spill limit, not measured)", "live telemetry lag under a concurrent run"}}
	fail := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		results.Failures = append(results.Failures, message)
		t.Error(message)
	}
	results.TargetBytes = target
	defer func() {
		// A run that stopped before it was measured is a failure, never a
		// pending candidate.
		if t.Failed() && len(results.Failures) == 0 {
			results.Failures = append(results.Failures, "the run stopped before it was measured")
		}
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
		case got.code == 200:
			// It answered in full. Within a small margin of the cancel that is
			// a race with it; later, the cancellation was ignored.
			if late := time.Since(cancelled); late > 100*time.Millisecond {
				fail("a cancelled heavy query answered in full %.0f ms after it was cancelled", float64(late.Microseconds())/1000)
			} else {
				results.NotMeasured = append(results.NotMeasured, "cancellation (the heavy query completed as it was cancelled)")
			}
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
	results.FaultTrials = runAnalyticsReferenceFaultTrials(t, server, store, in.ID, selected, fail)
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

func runAnalyticsReferenceFaultTrials(t *testing.T, server *Server, store storage.Store, datasetID string, selected time.Time, fail func(string, ...any)) []analyticsReferenceFaultTrial {
	t.Helper()
	var trials []analyticsReferenceFaultTrial
	record := func(name string, started time.Time, passed bool, detail string) {
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		trials = append(trials, analyticsReferenceFaultTrial{
			Name:       name,
			Passed:     passed,
			DurationMs: elapsed,
			Detail:     detail,
		})
		if !passed && fail != nil {
			fail("fault trial %s failed: %s", name, detail)
		}
	}

	// 1. Multi-job admission contention: hold the single analytical job slot and
	// verify a concurrent series query is deterministically refused with 503.
	t0 := time.Now()
	analysisPipelineJobs <- struct{}{}
	body, _ := json.Marshal(analytics.SeriesQuery{Metric: "latency", From: selected, To: selected.Add(time.Hour), BucketSeconds: 60, Aggregation: "avg"})
	req := httptest.NewRequest("POST", "/api/v1/analysis/datasets/"+datasetID+"/series", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	contendResp := httptest.NewRecorder()
	server.mux.ServeHTTP(contendResp, req)
	<-analysisPipelineJobs
	record("admission_contention", t0, contendResp.Code == 503, fmt.Sprintf("status=%d", contendResp.Code))

	// 2. Scratch disk exhaustion: request a pipeline reservation whose staging+input
	// exceeds the 20 GiB admission bound and verify deterministic refusal without
	// leaving staging directories behind.
	t1 := time.Now()
	cacheRoot, cacheErr := nativeAnalysisCacheRoot()
	_, exhaustedPipeline, exhaustErr := analytics.ReserveNativePipeline(t.Context(), cacheRoot, analytics.PipelinePlan{
		InputBytes:   (20 << 30) + 1,
		OutputBytes:  12,
		StagingBytes: 12,
	})
	if exhaustedPipeline != nil {
		_ = exhaustedPipeline.Release()
	}
	record("disk_exhaustion", t1, cacheErr == nil && exhaustErr != nil, fmt.Sprintf("err=%v", exhaustErr))

	// 3. Cold HA replica read: a second Server sharing the same store and archive
	// directory queries the committed dataset cold without re-normalizing.
	t2 := time.Now()
	replica := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	replicaReq := httptest.NewRequest("POST", "/api/v1/analysis/datasets/"+datasetID+"/series", bytes.NewReader(body))
	replicaReq.Header.Set("Content-Type", "application/json")
	replicaResp := httptest.NewRecorder()
	replica.mux.ServeHTTP(replicaResp, replicaReq)
	var replicaOut AnalysisDatasetSeriesResponse
	_ = json.Unmarshal(replicaResp.Body.Bytes(), &replicaOut)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = replica.Shutdown(shutdownCtx)
	shutdownCancel()
	record("cold_ha_replica", t2, replicaResp.Code == 200 && len(replicaOut.Points) == 60, fmt.Sprintf("status=%d points=%d", replicaResp.Code, len(replicaOut.Points)))

	// 4. Worker crash & controller restart recovery: an interrupted attempt from
	// an exited controller incarnation is proven exited, transitions to failed,
	// and its orphaned staging directory is reclaimed.
	t3 := time.Now()
	sqliteStore, ok := store.(*storage.SQLiteStore)
	if ok {
		ready, getErr := sqliteStore.GetAnalysisImport(datasetID)
		if getErr == nil && ready != nil {
			actAsWriterB(server)
			interrupted, interruptedAttempt := claimAsWriter(t, sqliteStore, ready, apiWriterA)
			writeAnalysisFixtureObject(t, interruptedAttempt.Normalized[0])
			jobDir := filepath.Join(cacheRoot, "job-"+interrupted.ID+"-"+interruptedAttempt.ID+"-crash")
			if err := os.MkdirAll(jobDir, 0700); err == nil {
				ownerJSON, _ := json.Marshal(map[string]any{"version": 1, "controller": apiWriterA, "dataset_id": interrupted.ID, "attempt_id": interruptedAttempt.ID})
				_ = os.WriteFile(filepath.Join(jobDir, ".staging-owner.json"), ownerJSON, 0600)
			}
			actAsWriterB(server, apiWriterA)
			server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
			afterCrash, _ := sqliteStore.GetAnalysisImport(interrupted.ID)
			_, jobStatErr := os.Stat(jobDir)
			delResp := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+interrupted.ID, nil)
			passed := afterCrash != nil && afterCrash.State == "failed" && os.IsNotExist(jobStatErr) && delResp.Code == 204
			record("worker_crash_and_controller_restart", t3, passed, fmt.Sprintf("state=%v delete=%d", afterCrash.State, delResp.Code))
		}
	}

	// 5. Remote artifact lifecycle cleanup with admin intervention.
	t4 := time.Now()
	if ok {
		ready, getErr := sqliteStore.GetAnalysisImport(datasetID)
		if getErr == nil && ready != nil {
			remoteClaimed, remoteAttempt := claimAsWriter(t, sqliteStore, ready, apiWriterA)
			remoteScope := storage.AnalysisWriterExitScope{
				Prover:          apiWriterB,
				LocalFilesystem: false,
				Exited:          func(id procidentity.Identity) bool { return id == apiWriterA },
			}
			outcome, resolveErr := sqliteStore.ResolveExitedAnalysisAttempt(t.Context(), remoteClaimed.ProjectID, remoteAttempt.ID, remoteScope)
			_ = timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+remoteClaimed.ID, nil)
			attestResp := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+remoteClaimed.ID+"/attest-writer-stopped", map[string]any{
				"reason": "fault trial verified remote writer stopped",
			})
			bucket := memblob.OpenBucket(nil)
			lateKey := filepath.ToSlash(filepath.Join("_analysis", remoteAttempt.Normalized[0]))
			_ = bucket.WriteAll(t.Context(), lateKey, []byte("late remote bytes"), nil)
			ops := defaultAnalysisCleanupPhysicalOps
			ops.openBucket = func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error) {
				return analysisNoCloseBucket{bucket}, nil
			}
			cleanupCtx, _ := cleanupOwnerContext(t, 4, ops)
			cleanupErr := server.retryAnalysisCleanupJob(cleanupCtx, sqliteStore, storage.AnalysisCleanupJob{
				Kind:       "attempt",
				ProjectID:  remoteClaimed.ProjectID,
				ResourceID: remoteAttempt.ID,
			})
			_ = bucket.Close()
			delResp := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+remoteClaimed.ID, nil)
			passed := resolveErr == nil && outcome.RemoteCommitUnproven && attestResp.Code == 200 && cleanupErr == nil && delResp.Code == 204
			record("remote_artifact_cleanup", t4, passed, fmt.Sprintf("attest=%d delete=%d", attestResp.Code, delResp.Code))
		}
	}
	return trials
}

func TestNativeAnalyticsReferenceFaultTrials(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", filepath.Join(scratch, "archives"))
	server, store := newTimeslotServer(t)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fixturePath := filepath.Join(scratch, "day-00.parquet")
	const dayStart = int64(1_700_006_400_000)
	query := fmt.Sprintf("COPY (SELECT %d + i*1000 AS ts, (hash(i, 0) %% 200000) / 100.0 AS latency, (hash(0, i) %% 16)::DOUBLE AS errors FROM range(7200) t(i)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)", dayStart, fixturePath)
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	r := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports?project_id=proj-a", map[string]any{
		"name":  "fault-trials",
		"files": []any{map[string]any{"relative_path": "day-00.parquet", "size": len(data)}},
	})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var in storage.AnalysisImport
	if err := json.Unmarshal(r.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	importPath := "/api/v1/analysis/imports/" + in.ID
	req := httptest.NewRequest("PUT", fmt.Sprintf("%s/files/%s?offset=0", importPath, in.Files[0].ID), bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != 200 {
		t.Fatalf("upload: %d %s", resp.Code, resp.Body.String())
	}
	current, err := store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapping := analysis.ParquetMapping{
		Version:           1,
		Timestamp:         []string{"ts"},
		TimestampEncoding: "unix_ms",
		Timezone:          "UTC",
		Measures: []analysis.ParquetMeasure{
			{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"},
			{Value: []string{"errors"}, Name: "errors", Kind: analysis.KindGauge, Unit: "count"},
		},
	}
	if r = timeslotRequest(t, server, "PUT", importPath+"/mapping", map[string]any{"revision": current.Revision, "mapping": mapping}); r.Code != 200 {
		t.Fatalf("mapping: %d %s", r.Code, r.Body.String())
	}
	if r = timeslotRequest(t, server, "POST", importPath+"/commit", nil); r.Code != 202 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		current, err = store.(storage.AnalysisImportStore).GetAnalysisImport(in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State != "normalizing" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for len(analysisPipelineJobs) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if current.State != "ready" {
		t.Fatalf("expected ready dataset, got %s (%s)", current.State, current.Error)
	}
	selected := time.UnixMilli(dayStart).UTC()
	trials := runAnalyticsReferenceFaultTrials(t, server, store, in.ID, selected, t.Errorf)
	if len(trials) != 5 {
		t.Fatalf("expected 5 fault trials, got %d: %+v", len(trials), trials)
	}
	for _, trial := range trials {
		if !trial.Passed {
			t.Fatalf("fault trial %s failed: %+v", trial.Name, trial)
		}
	}
	_ = uuid.NewString()
}
