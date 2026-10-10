package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The native analytics reference benchmark's results (NA-6, B8). The
// benchmark itself needs the DuckDB build (analysis_reference_benchmark_
// native_test.go); its results and F4 acceptance requests are written here
// so the default build tests them.

const analyticsReferenceP95TargetMs = 5000

// The reference fixture writes one file a day, and a series query spans at
// most 366 days (analytics.SeriesQuery.Validate), so a fixture is at most 366
// files, about 6.5 GB at its ZSTD compression.
const (
	analyticsReferenceMaxDays  = 366
	analyticsReferenceMaxBytes = int64(6) << 30
)

type analyticsReferenceHost struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	CPUs        int    `json:"cpus"`
	MemoryBytes int64  `json:"memory_bytes,omitempty"`
	// Reference reports whether the host is the NA-6 reference shape:
	// 4 vCPUs and 16 GiB of memory.
	Reference bool `json:"reference"`
}

type analyticsReferenceFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

type analyticsReferenceTiming struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type analyticsReferenceResults struct {
	Schema      string                   `json:"schema"`
	GeneratedAt time.Time                `json:"generated_at"`
	Host        analyticsReferenceHost   `json:"host"`
	Files       []analyticsReferenceFile `json:"files"`
	FixtureSQL  string                   `json:"fixture_sql"`
	// TargetBytes is the fixture size requested; the F4 profile names it, so
	// runs of one workload share a subject whatever the files compress to.
	TargetBytes int64 `json:"target_bytes"`
	TotalBytes  int64 `json:"total_bytes"`
	TotalRows   int64 `json:"total_rows"`
	// Import: upload and normalization wall time, and what normalization
	// accepted and rejected.
	UploadMs    float64 `json:"upload_ms"`
	NormalizeMs float64 `json:"normalize_ms"`
	Accepted    int64   `json:"accepted"`
	Rejected    int64   `json:"rejected"`
	// The selected-window query: one hour of one partition at 60-second
	// buckets, first (cold) and repeated.
	ColdQueryMs     float64                  `json:"cold_query_ms"`
	RepeatedQueries analyticsReferenceTiming `json:"repeated_queries"`
	FullRangeMs     float64                  `json:"full_range_ms"`
	FullRangeStatus int                      `json:"full_range_status"`
	// Pruning of the selected-window query.
	TotalObjects   int     `json:"total_objects"`
	PrunedObjects  int     `json:"pruned_objects"`
	AvoidedBytes   int64   `json:"avoided_bytes"`
	StagedBytes    int64   `json:"staged_bytes"`
	CancelReturnMs float64 `json:"cancel_return_ms"`
	// Peak RSS sampled during the import and the queries: of the controller
	// process, of its native worker processes, and of the two together at
	// one instant. Spill is bounded by SpillLimit but not measured.
	PeakControllerRSSBytes int64    `json:"peak_controller_rss_bytes"`
	PeakWorkerRSSBytes     int64    `json:"peak_worker_rss_bytes"`
	PeakCombinedRSSBytes   int64    `json:"peak_combined_rss_bytes"`
	MemoryLimit            string   `json:"memory_limit"`
	SpillLimit             string   `json:"spill_limit"`
	NotMeasured            []string `json:"not_measured"`
	Failures               []string `json:"failures"`
}

// writeAnalyticsReferenceAcceptance writes the results and, given the
// measured commit, one F4 feature acceptance request. A request is pending
// review when the run had no failure and met the five-second P95 target on
// the selected-window query, and failed otherwise; it never claims a pass by
// itself, and a host that is not the reference shape says so.
func writeAnalyticsReferenceAcceptance(t *testing.T, dir string, results analyticsReferenceResults) {
	t.Helper()
	body, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "analytics-reference.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	t.Logf("analytics reference results written to %s (sha256 %x)", path, sum)
	commit := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_COMMIT_SHA")))
	if !regexp.MustCompile(`^[0-9a-f]{7,64}$`).MatchString(commit) {
		t.Logf("APEXLOAD_COMMIT_SHA is not a commit; no acceptance requests written")
		return
	}
	status := "pending"
	if len(results.Failures) > 0 || results.RepeatedQueries.Samples == 0 || results.RepeatedQueries.P95Ms > analyticsReferenceP95TargetMs {
		status = "failed"
	}
	host := "not the 4 vCPU/16 GiB reference host"
	if results.Host.Reference {
		host = "reference host shape"
	}
	// A value the run did not measure reads n/m, never zero: one listed as
	// not measured, or never taken (a run that stopped before it, or a
	// cancellation that never returned).
	measured := func(name string, taken bool, value string) string {
		if !taken {
			return "n/m"
		}
		for _, item := range results.NotMeasured {
			if strings.HasPrefix(item, name) {
				return "n/m"
			}
		}
		return value
	}
	notes := fmt.Sprintf("%.2f GiB, %d files, %d rows: normalize %s; query cold %s, P95 %s; pruned %s; cancel %s; peak RSS %s; %s.",
		float64(results.TotalBytes)/(1<<30), len(results.Files), results.TotalRows,
		measured("normalization", results.NormalizeMs > 0, fmt.Sprintf("%.0f s", results.NormalizeMs/1000)),
		measured("cold query", results.ColdQueryMs > 0, fmt.Sprintf("%.0f ms", results.ColdQueryMs)),
		measured("repeated queries", results.RepeatedQueries.Samples > 0, fmt.Sprintf("%.0f ms", results.RepeatedQueries.P95Ms)),
		measured("pruning", results.TotalObjects > 0, fmt.Sprintf("%d/%d", results.PrunedObjects, results.TotalObjects)),
		measured("cancellation", results.CancelReturnMs > 0, fmt.Sprintf("%.0f ms", results.CancelReturnMs)), measured("peak RSS", results.PeakCombinedRSSBytes > 0, fmt.Sprintf("%d MiB", results.PeakCombinedRSSBytes>>20)), host)
	if len(notes) > 256 {
		t.Errorf("acceptance notes exceed the 256 bytes a record accepts: %q", notes)
	}
	requests := []map[string]any{{
		"requirement_id": "NA-6", "feature": "analytics-reference", "profile": fmt.Sprintf("%dmib-partitioned", results.TargetBytes>>20),
		"platform": results.Host.OS + "-" + results.Host.Arch, "commit_sha": commit, "suite": "pkg/api TestNativeAnalyticsReferenceBenchmark", "result": status,
		"artifacts": []map[string]string{{"name": "analytics-reference.json", "sha256": hex.EncodeToString(sum[:])}},
		"notes":     notes,
	}}
	records, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "analytics-reference-acceptance-requests.json"), records, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The reference benchmark's acceptance request names the measured commit, is
// pending only without failures and within the P95 target, and carries the
// F4 shape.
func TestAnalyticsReferenceAcceptanceRequestsArePendingOrFailed(t *testing.T) {
	results := analyticsReferenceResults{Schema: "apexload.analytics_reference.v1", Host: analyticsReferenceHost{OS: "darwin", Arch: "arm64", CPUs: 12},
		Files: []analyticsReferenceFile{{Name: "day-00.parquet", Bytes: 1 << 30, Rows: 1e8}}, TargetBytes: 1 << 30, TotalBytes: 1 << 30, TotalRows: 1e8,
		RepeatedQueries: analyticsReferenceTiming{Samples: 20, P50Ms: 900, P95Ms: 1200, MaxMs: 1300}, ColdQueryMs: 9999, NormalizeMs: 999999, CancelReturnMs: 99,
		TotalObjects: 9999, PrunedObjects: 9998, PeakControllerRSSBytes: 99999 << 20, PeakWorkerRSSBytes: 99999 << 20, PeakCombinedRSSBytes: 199998 << 20}
	read := func(dir string) (status, notes string) {
		raw, err := os.ReadFile(filepath.Join(dir, "analytics-reference-acceptance-requests.json"))
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
		data, err := os.ReadFile(filepath.Join(dir, "analytics-reference.json"))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		request := written[0]
		if request.RequirementID != "NA-6" || len(request.Notes) > 256 || len(request.Artifacts) != 1 || request.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(request.Profile) {
			t.Fatalf("request %+v", request)
		}
		return request.Result, request.Notes
	}
	dir := t.TempDir()
	t.Setenv("APEXLOAD_COMMIT_SHA", "")
	writeAnalyticsReferenceAcceptance(t, dir, results)
	if _, err := os.Stat(filepath.Join(dir, "analytics-reference-acceptance-requests.json")); !os.IsNotExist(err) {
		t.Fatalf("a request was written without a commit: %v", err)
	}
	t.Setenv("APEXLOAD_COMMIT_SHA", strings.Repeat("c", 40))
	writeAnalyticsReferenceAcceptance(t, dir, results)
	if status, notes := read(dir); status != "pending" || !strings.Contains(notes, "not the 4 vCPU/16 GiB reference host") || !strings.Contains(notes, "cancel 99 ms") {
		t.Fatalf("a clean run within the target: %s %q", status, notes)
	}
	// A value the run did not measure is never written as zero.
	unmeasured := results
	unmeasured.CancelReturnMs, unmeasured.PeakCombinedRSSBytes = 0, 0
	unmeasured.NotMeasured = []string{"cancellation (the heavy query finished before it was cancelled)", "peak RSS (not measured on this platform)"}
	writeAnalyticsReferenceAcceptance(t, dir, unmeasured)
	if _, notes := read(dir); !strings.Contains(notes, "cancel n/m") || !strings.Contains(notes, "peak RSS n/m") {
		t.Fatalf("unmeasured values in the notes: %q", notes)
	}
	// A run that failed before it took them (no not_measured entry) says so too.
	stopped := results
	stopped.CancelReturnMs, stopped.PeakCombinedRSSBytes = 0, 0
	stopped.Failures = []string{"a cancelled heavy query did not return within 30 s"}
	writeAnalyticsReferenceAcceptance(t, dir, stopped)
	if status, notes := read(dir); status != "failed" || !strings.Contains(notes, "cancel n/m") || !strings.Contains(notes, "peak RSS n/m") {
		t.Fatalf("values never taken in the notes: %s %q", status, notes)
	}
	// A run that stopped before its import or queries says so for each.
	early := analyticsReferenceResults{Schema: results.Schema, Host: results.Host, TargetBytes: 1 << 30, Failures: []string{"the run stopped before it was measured"}}
	writeAnalyticsReferenceAcceptance(t, dir, early)
	if status, notes := read(dir); status != "failed" || !strings.Contains(notes, "normalize n/m; query cold n/m, P95 n/m; pruned n/m") {
		t.Fatalf("a run that stopped early: %s %q", status, notes)
	}
	slow := results
	slow.RepeatedQueries.P95Ms = analyticsReferenceP95TargetMs + 1
	writeAnalyticsReferenceAcceptance(t, dir, slow)
	if status, _ := read(dir); status != "failed" {
		t.Fatalf("a P95 above the target was %s", status)
	}
	broken := results
	broken.Failures = []string{"controller stopped answering"}
	writeAnalyticsReferenceAcceptance(t, dir, broken)
	if status, _ := read(dir); status != "failed" {
		t.Fatalf("a run with a failure was %s", status)
	}
}
