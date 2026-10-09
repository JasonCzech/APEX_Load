package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The TEL-4 reference workload (C8), opt-in under
// APEXLOAD_TELEMETRY_REFERENCE=1: two declared generators (real generator
// services in this process, reached over in-memory gRPC) each run half of
// APEXLOAD_TELEMETRY_REFERENCE_RATE (default 2,000) accepted observations a
// second against a local target for APEXLOAD_TELEMETRY_REFERENCE_SECONDS
// (default 60), publishing every second. While it runs, a foreground
// dashboard (headless Chromium on the run's live page, which reports its
// render evidence) watches it and archive analyses of an earlier run are
// read concurrently. It records each telemetry stage's P50/P95/P99, coverage,
// loss accounting against the target's own count of served requests, process
// CPU and RSS, and every failure, and with APEXLOAD_TELEMETRY_REFERENCE_OUT
// writes the results and an F4 acceptance request.

const telemetryReferenceAgeTargetMs = 2000

type telemetryReferenceResults struct {
	Schema      string    `json:"schema"`
	GeneratedAt time.Time `json:"generated_at"`
	Host        struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
		CPUs int    `json:"cpus"`
	} `json:"host"`
	Generators     int     `json:"generators"`
	DurationS      float64 `json:"duration_s"`
	TargetRate     int     `json:"target_rate"`
	ServedRequests int64   `json:"served_requests"`
	// AcceptedRecords is the archive's transaction count; AcceptedRate is
	// it over the run's duration.
	AcceptedRecords int64   `json:"accepted_records"`
	AcceptedRate    float64 `json:"accepted_rate"`
	// UnreportedLoss is served requests the archive does not hold and no
	// loss counter reports.
	UnreportedLoss int64                        `json:"unreported_loss"`
	Loss           *TelemetryLossAccounting     `json:"loss,omitempty"`
	Stages         []TelemetryStageDistribution `json:"stages"`
	Coverage       *TelemetryCoverageRecord     `json:"coverage,omitempty"`
	// CaptureToRenderMaxMs bounds capture → visible render: the sum of the
	// sample-age and browser-render maxima (stage percentiles are never
	// added).
	CaptureToRenderMaxMs *float64  `json:"capture_to_render_max_ms,omitempty"`
	ClockUncertaintyMs   []float64 `json:"clock_uncertainty_ms,omitempty"`
	AnalysisReads        int       `json:"analysis_reads"`
	AnalysisFailures     int       `json:"analysis_failures"`
	CPUSeconds           float64   `json:"cpu_seconds"`
	PeakRSSBytes         int64     `json:"peak_rss_bytes"`
	PeakChildRSSBytes    int64     `json:"peak_child_rss_bytes"`
	NotMeasured          []string  `json:"not_measured"`
	Failures             []string  `json:"failures"`
}

func (r *telemetryReferenceResults) stage(name string) *TelemetryStageDistribution {
	for index := range r.Stages {
		if r.Stages[index].Stage == name {
			return &r.Stages[index]
		}
	}
	return nil
}

func TestTelemetryReferenceWorkload(t *testing.T) {
	if os.Getenv("APEXLOAD_TELEMETRY_REFERENCE") != "1" {
		t.Skip("set APEXLOAD_TELEMETRY_REFERENCE=1 to run the telemetry reference workload")
	}
	duration, rate := 60*time.Second, 2000
	if raw := os.Getenv("APEXLOAD_TELEMETRY_REFERENCE_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 10 || seconds > 3600 {
			t.Fatalf("APEXLOAD_TELEMETRY_REFERENCE_SECONDS must be 10 to 3600, got %q", raw)
		}
		duration = time.Duration(seconds) * time.Second
	}
	if raw := os.Getenv("APEXLOAD_TELEMETRY_REFERENCE_RATE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 20 || parsed > 100000 {
			t.Fatalf("APEXLOAD_TELEMETRY_REFERENCE_RATE must be 20 to 100000, got %q", raw)
		}
		rate = parsed
	}
	results := telemetryReferenceResults{Schema: "apexload.telemetry_reference.v1", GeneratedAt: time.Now().UTC(), Generators: 2, DurationS: duration.Seconds(), TargetRate: rate,
		Failures: []string{}, NotMeasured: []string{"collector traffic", "an overload trial", "native (DuckDB) analytics: the concurrent analyses are archive reads"}}
	results.Host.OS, results.Host.Arch, results.Host.CPUs = runtime.GOOS, runtime.GOARCH, runtime.NumCPU()
	fail := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		results.Failures = append(results.Failures, message)
		t.Error(message)
	}
	defer func() {
		if dir := strings.TrimSpace(os.Getenv("APEXLOAD_TELEMETRY_REFERENCE_OUT")); dir != "" {
			writeTelemetryReferenceAcceptance(t, dir, results)
		}
	}()

	t.Setenv("APEXLOAD_ARCHIVE_DIR", filepath.Join(t.TempDir(), "archives"))
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reference.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4})
	// Served through httptest rather than Start, so start the WebSocket hub
	// that Start would.
	server.launchBackground(server.hub.Run)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	var served atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	// Two generator services, reached over in-memory gRPC.
	listeners := map[string]*bufconn.Listener{}
	for _, id := range []string{"gen-a", "gen-b"} {
		listener := bufconn.Listen(4 << 20)
		grpcServer := grpc.NewServer()
		service := generatorrpc.NewService(id, 500, generatorrpc.WithExecution())
		pb.RegisterGeneratorControlServer(grpcServer, service)
		go func() { _ = grpcServer.Serve(listener) }()
		listeners[id] = listener
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = service.Shutdown(ctx)
			grpcServer.Stop()
			_ = listener.Close()
		})
	}
	server.runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, func(ctx context.Context, target string) (generatorClient, error) {
		listener := listeners[target]
		return generatorrpc.Dial(ctx, target, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	}))
	api := httptest.NewServer(server.mux)
	t.Cleanup(api.Close)
	call := func(method, path string, body any) (int, []byte) {
		var reader *bytes.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, api.URL+path, reader)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, []byte(err.Error())
		}
		defer response.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(response.Body)
		return response.StatusCode, out.Bytes()
	}
	if code, body := call("PUT", "/api/v1/projects/"+storage.DefaultProjectID+"/telemetry-policy", map[string]any{"client_render_evidence": true}); code != 200 {
		t.Fatalf("render evidence opt-in: %d %s", code, body)
	}

	// Each group (one per generator) is paced at half the workload's rate;
	// TPS pacing is a group's total rate, shared by its VUsers.
	vusersPerGenerator := max(1, rate/2/100)
	server.runner.StoreScript(&dsl.Script{ID: "reference", Name: "Reference", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: target.URL, Pacing: dsl.PacingConfig{Type: "tps", TPS: float64(rate) / 2}},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Method: "GET", Target: "/"}}})
	groups := func() []CreateGroupEntry {
		var out []CreateGroupEntry
		for _, generator := range []string{"gen-a", "gen-b"} {
			out = append(out, CreateGroupEntry{Name: "load " + generator, ScriptID: "reference", VUsers: vusersPerGenerator, RampUp: "1s",
				Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{generator}}})
		}
		return out
	}
	runOnce := func(name string, length time.Duration) *ManagedTest {
		run, err := server.runner.CreateTest(CreateTestRequest{Name: name, Duration: length.String(), ProjectID: storage.DefaultProjectID, Groups: groups()})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := server.runner.StartTest(run.ID); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		return run
	}
	waitArchived := func(id string, limit time.Duration) *storage.RunArchiveRecord {
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if record, ok := server.runner.GetRunArchive(id); ok && record != nil && record.ManifestSHA256 != "" {
				return record
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("run %s was not archived within %v", id, limit)
		return nil
	}

	// An earlier run whose archive the concurrent analyses read.
	warm := runOnce("Reference warm-up", 5*time.Second)
	waitArchived(warm.ID, 2*time.Minute)
	servedBefore := served.Load()

	var before syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &before)
	run := runOnce("Telemetry reference workload", duration)

	// The foreground dashboard.
	stopDashboard := telemetryReferenceDashboard(t, api.URL+"/#/test/"+run.ID+"?tab=diagnostics&projectId="+storage.DefaultProjectID, &results)
	// Concurrent archive analyses of the earlier run.
	analysisDone := make(chan struct{})
	var analysisWait sync.WaitGroup
	analysisWait.Add(1)
	go func() {
		defer analysisWait.Done()
		for {
			select {
			case <-analysisDone:
				return
			case <-time.After(time.Second):
			}
			code, _ := call("GET", "/api/v1/tests/"+warm.ID+"/archive/report?profile=engineer", nil)
			results.AnalysisReads++
			if code != 200 {
				results.AnalysisFailures++
			}
		}
	}()
	record := waitArchived(run.ID, duration+5*time.Minute)
	close(analysisDone)
	analysisWait.Wait()
	stopDashboard()
	var after syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &after)
	results.CPUSeconds = time.Duration(after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano()).Seconds()
	scale := int64(1024)
	if runtime.GOOS == "darwin" {
		scale = 1
	}
	var children syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children)
	results.PeakRSSBytes, results.PeakChildRSSBytes = int64(after.Maxrss)*scale, int64(children.Maxrss)*scale

	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	results.Stages, results.Coverage, results.Loss = manifest.TelemetryStageDistributions, manifest.TelemetryCoverage, manifest.TelemetryLoss
	// Accepted records are the archived samples of the script's request
	// (the archive also holds each iteration's transaction row).
	results.AcceptedRecords = telemetryReferenceArchivedRequests(t, record, manifest, "Ping")
	if manifest.Provenance.StartedAt != nil && manifest.CompletedAt.After(*manifest.Provenance.StartedAt) {
		results.AcceptedRate = float64(results.AcceptedRecords) / manifest.CompletedAt.Sub(*manifest.Provenance.StartedAt).Seconds()
	}
	results.ServedRequests = served.Load() - servedBefore
	reported := int64(0)
	if results.Loss != nil && results.Loss.DroppedResults != nil {
		reported += results.Loss.DroppedResults.Value
	}
	results.UnreportedLoss = max(0, results.ServedRequests-results.AcceptedRecords-reported)
	if raw, err := json.Marshal(manifest); err == nil {
		for _, match := range regexp.MustCompile(`"uncertainty_ms":([0-9.]+)`).FindAllSubmatch(raw, 64) {
			if value, err := strconv.ParseFloat(string(match[1]), 64); err == nil {
				results.ClockUncertaintyMs = append(results.ClockUncertaintyMs, value)
			}
		}
	}
	age, render := results.stage(telemetryStageSampleAge), results.stage(telemetryStageBrowserRender)
	if age != nil && render != nil && age.MaxMs != nil && render.MaxMs != nil {
		bound := *age.MaxMs + *render.MaxMs
		results.CaptureToRenderMaxMs = &bound
	}
	switch {
	case results.UnreportedLoss > 0:
		fail("%d served requests are neither archived nor reported lost", results.UnreportedLoss)
	case age == nil || age.P99Ms == nil:
		fail("the sample-age stage has no P99")
	case *age.P99Ms > telemetryReferenceAgeTargetMs:
		fail("sample-age P99 %.0f ms is above the %d ms target", *age.P99Ms, telemetryReferenceAgeTargetMs)
	}
	if results.AnalysisFailures > 0 {
		fail("%d of %d concurrent archive analyses failed", results.AnalysisFailures, results.AnalysisReads)
	}
	if results.AcceptedRate < 0.9*float64(rate) {
		fail("the accepted rate %.0f/s is below 90%% of the %d/s workload", results.AcceptedRate, rate)
	}
	for _, stage := range results.Stages {
		if stage.P99Ms != nil {
			t.Logf("stage %s: %d samples, p50 %.1f p95 %.1f p99 %.1f max %.1f ms", stage.Stage, stage.Samples, *stage.P50Ms, *stage.P95Ms, *stage.P99Ms, *stage.MaxMs)
		} else {
			t.Logf("stage %s: %s %s %v", stage.Stage, stage.Availability, stage.Reason, stage.Excluded)
		}
	}
	t.Logf("served %d, archived %d (%.0f/s), unreported loss %d; analyses %d/%d failed; CPU %.1f s, RSS %d MiB (+%d MiB children); clock uncertainty %v",
		results.ServedRequests, results.AcceptedRecords, results.AcceptedRate, results.UnreportedLoss, results.AnalysisFailures, results.AnalysisReads,
		results.CPUSeconds, results.PeakRSSBytes>>20, results.PeakChildRSSBytes>>20, results.ClockUncertaintyMs)
}

// telemetryReferenceArchivedRequests counts the archive's samples of one
// action (not transaction rows).
func telemetryReferenceArchivedRequests(t *testing.T, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, action string) int64 {
	t.Helper()
	lease, err := acquireArchiveMaterialized(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	name, parquet := manifest.TransactionsFile, false
	if name == "" {
		name, parquet = manifest.TransactionsParquetFile, true
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var count int64
	visit := func(raw []byte) error {
		var event TransactionEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if !event.BusinessTransaction && event.ActionName == action {
			count++
		}
		return nil
	}
	if parquet {
		err = visitBrowserResourceParquetRows(context.Background(), file, visit)
	} else {
		err = visitArchiveJSONLRows(context.Background(), file, visit)
	}
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// telemetryReferenceDashboard opens the run's live page in headless
// Chromium, which reports its render evidence while it is in the
// foreground. Without an installed Chromium the browser-render stage is not
// measured (a failure under APEXLOAD_BROWSER_TESTS=require).
func telemetryReferenceDashboard(t *testing.T, url string, results *telemetryReferenceResults) func() {
	t.Helper()
	unavailable := func(err error) func() {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Errorf("the foreground dashboard could not start: %v", err)
			results.Failures = append(results.Failures, "the foreground dashboard could not start")
		}
		results.NotMeasured = append(results.NotMeasured, "browser render (no Chromium for the foreground dashboard)")
		return func() {}
	}
	pw, err := playwright.Run()
	if err != nil {
		return unavailable(err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		_ = pw.Stop()
		return unavailable(err)
	}
	page, err := browser.NewPage()
	if err == nil {
		if _, err = page.Goto(url); err == nil {
			_, err = page.Reload()
		}
	}
	if err != nil {
		_ = browser.Close()
		_ = pw.Stop()
		return unavailable(err)
	}
	return func() {
		_ = browser.Close()
		_ = pw.Stop()
	}
}

// writeTelemetryReferenceAcceptance writes the results and, given the
// measured commit, one F4 feature acceptance request: pending review when
// the run had no failure (no unreported loss, the sample-age P99 within the
// target, the workload's rate sustained), failed otherwise; never passed by
// itself, and always naming the in-process generators.
func writeTelemetryReferenceAcceptance(t *testing.T, dir string, results telemetryReferenceResults) {
	t.Helper()
	body, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "telemetry-reference.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	t.Logf("telemetry reference results written to %s (sha256 %x)", path, sum)
	commit := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_COMMIT_SHA")))
	if !regexp.MustCompile(`^[0-9a-f]{7,64}$`).MatchString(commit) {
		t.Logf("APEXLOAD_COMMIT_SHA is not a commit; no acceptance requests written")
		return
	}
	status := "pending"
	if len(results.Failures) > 0 {
		status = "failed"
	}
	p99 := func(name string) string {
		if stage := results.stage(name); stage != nil && stage.P99Ms != nil {
			return fmt.Sprintf("%.0f", *stage.P99Ms)
		}
		return "n/a"
	}
	notes := fmt.Sprintf("%d gens in-process, %.0f/s for %.0f s; P99 ms age %s gen %s transport %s fanout %s render %s; unreported loss %d; %d failures.",
		results.Generators, results.AcceptedRate, results.DurationS, p99(telemetryStageSampleAge), p99(telemetryStageGeneratorProcessing), p99(telemetryStageTransport),
		p99(telemetryStageControllerFanout), p99(telemetryStageBrowserRender), results.UnreportedLoss, len(results.Failures))
	if len(notes) > 256 {
		t.Errorf("acceptance notes exceed the 256 bytes a record accepts: %q", notes)
	}
	requests := []map[string]any{{
		"requirement_id": "TEL-4", "feature": "telemetry-reference", "profile": fmt.Sprintf("%dps-%dgen", results.TargetRate, results.Generators),
		"platform": results.Host.OS + "-" + results.Host.Arch, "commit_sha": commit, "suite": "pkg/api TestTelemetryReferenceWorkload", "result": status,
		"artifacts": []map[string]string{{"name": "telemetry-reference.json", "sha256": hex.EncodeToString(sum[:])}},
		"notes":     notes,
	}}
	records, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "telemetry-reference-acceptance-requests.json"), records, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The reference workload's acceptance request names the measured commit, is
// pending only without failures, and carries the F4 shape.
func TestTelemetryReferenceAcceptanceRequestsArePendingOrFailed(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	results := telemetryReferenceResults{Schema: "apexload.telemetry_reference.v1", Generators: 2, DurationS: 99999, TargetRate: 2000, AcceptedRate: 99999, UnreportedLoss: 99999, Failures: []string{},
		Stages: []TelemetryStageDistribution{
			{Stage: telemetryStageSampleAge, P99Ms: p(99999)}, {Stage: telemetryStageGeneratorProcessing, P99Ms: p(99999)}, {Stage: telemetryStageTransport, P99Ms: p(99999)},
			{Stage: telemetryStageControllerFanout, P99Ms: p(99999)}, {Stage: telemetryStageBrowserRender, P99Ms: p(99999)}}}
	results.Host.OS, results.Host.Arch = "darwin", "arm64"
	read := func(dir string) (string, string) {
		raw, err := os.ReadFile(filepath.Join(dir, "telemetry-reference-acceptance-requests.json"))
		if err != nil {
			t.Fatal(err)
		}
		var written []struct {
			RequirementID string `json:"requirement_id"`
			Profile       string `json:"profile"`
			Result        string `json:"result"`
			Notes         string `json:"notes"`
			Artifacts     []struct {
				SHA256 string `json:"sha256"`
			} `json:"artifacts"`
		}
		if err := json.Unmarshal(raw, &written); err != nil || len(written) != 1 {
			t.Fatalf("requests %s %v", raw, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "telemetry-reference.json"))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		request := written[0]
		if request.RequirementID != "TEL-4" || len(request.Notes) > 256 || request.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(request.Profile) {
			t.Fatalf("request %+v", request)
		}
		return request.Result, request.Notes
	}
	dir := t.TempDir()
	t.Setenv("APEXLOAD_COMMIT_SHA", "")
	writeTelemetryReferenceAcceptance(t, dir, results)
	if _, err := os.Stat(filepath.Join(dir, "telemetry-reference-acceptance-requests.json")); !os.IsNotExist(err) {
		t.Fatalf("a request was written without a commit: %v", err)
	}
	t.Setenv("APEXLOAD_COMMIT_SHA", strings.Repeat("d", 40))
	writeTelemetryReferenceAcceptance(t, dir, results)
	if status, notes := read(dir); status != "pending" || !strings.Contains(notes, "in-process") {
		t.Fatalf("a clean run: %s %q", status, notes)
	}
	results.Failures = []string{"sample-age P99 above the target"}
	writeTelemetryReferenceAcceptance(t, dir, results)
	if status, _ := read(dir); status != "failed" {
		t.Fatalf("a run with a failure was %s", status)
	}
}
