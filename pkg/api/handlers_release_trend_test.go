package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestReleaseTrendExportReturnsArchivedRunCSV(t *testing.T) {
	server := NewServer(ServerConfig{})
	releaseID := "release-trend-1"
	started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	completedEarly := started.Add(3 * time.Minute)
	completedLate := started.Add(8 * time.Minute)

	lateArchive := writeReleaseTrendArchiveFixture(t, releaseID, "run-late", "Late Run", started.Add(5*time.Minute), completedLate, false)
	earlyArchive := writeReleaseTrendArchiveFixture(t, releaseID, "run-early", "Early Run", started, completedEarly, true)

	server.runner.mu.Lock()
	server.runner.tests["run-late"] = &ManagedTest{
		ID:        "run-late",
		Name:      "Late Run",
		Status:    "COMPLETE",
		ReleaseID: releaseID,
		CreatedAt: started.Add(5 * time.Minute),
		StartedAt: ptrTime(started.Add(5 * time.Minute)),
		EndedAt:   ptrTime(completedLate),
		Archive:   lateArchive,
	}
	server.runner.tests["run-early"] = &ManagedTest{
		ID:        "run-early",
		Name:      "Early Run",
		Status:    "COMPLETE",
		ReleaseID: releaseID,
		CreatedAt: started,
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(completedEarly),
		Archive:   earlyArchive,
	}
	server.runner.archives["run-late"] = lateArchive
	server.runner.archives["run-early"] = earlyArchive
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+releaseID+"/trend/export", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want csv", got)
	}
	rows, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows len = %d, want header + two runs: %#v", len(rows), rows)
	}
	if rows[1][1] != "run-early" || rows[2][1] != "run-late" {
		t.Fatalf("run order = %q then %q, want completion-time order", rows[1][1], rows[2][1])
	}
	index := releaseTrendHeaderIndex(rows[0])
	if rows[1][index["sla_breached"]] != "true" {
		t.Fatalf("early sla_breached = %q, want true", rows[1][index["sla_breached"]])
	}
	if rows[1][index["completeness_status"]] == "" {
		t.Fatalf("expected completeness status in early row: %#v", rows[1])
	}
	if rows[1][index["cpu_peak_pct"]] != "72.5" || rows[1][index["memory_peak_mb"]] != "512" {
		t.Fatalf("resource peaks = cpu %q mem %q", rows[1][index["cpu_peak_pct"]], rows[1][index["memory_peak_mb"]])
	}
	if rows[1][index["telemetry_status"]] != "breached" || rows[1][index["telemetry_breached_intervals"]] != "1" {
		t.Fatalf("telemetry columns = status %q breached %q", rows[1][index["telemetry_status"]], rows[1][index["telemetry_breached_intervals"]])
	}
}

func writeReleaseTrendArchiveFixture(t *testing.T, releaseID string, testID string, testName string, startedAt time.Time, completedAt time.Time, slaBreached bool) *storage.RunArchiveRecord {
	t.Helper()
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:           archiveSchemaVersion,
		TestID:                  testID,
		TestName:                testName,
		CreatedAt:               startedAt,
		CompletedAt:             completedAt,
		Status:                  "COMPLETE",
		Provenance:              RunArchiveProvenance{ReleaseID: releaseID, StartedAt: ptrTime(startedAt)},
		TransactionsFile:        "transactions.jsonl",
		ResourceSamplesFile:     "resources.jsonl",
		IntervalSummariesFile:   "interval_summaries.jsonl",
		TransactionCount:        2,
		ResourceSampleCount:     1,
		IntervalSummaryCount:    1,
		SLABreached:             slaBreached,
		TransactionsParquetFile: "",
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile),
		TransactionEvent{Timestamp: startedAt.Add(time.Second), TestID: testID, TestName: testName, ActionName: "search", DurationMs: 120, Success: true},
		TransactionEvent{Timestamp: startedAt.Add(2 * time.Second), TestID: testID, TestName: testName, ActionName: "checkout", DurationMs: 300, Success: false, ErrorMessage: "boom"},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.ResourceSamplesFile),
		ResourceSample{Timestamp: startedAt.Add(time.Second), TestID: testID, TestName: testName, ActiveVUsers: 15, CPUProcessPct: 72.5, MemProcessMB: 512},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile),
		IntervalSummaryRecord{
			Timestamp:       startedAt.Add(time.Second),
			TestID:          testID,
			TestName:        testName,
			ActiveVUsers:    15,
			TPS:             18,
			AvgResponseMs:   210,
			ErrorPct:        50,
			TotalTx:         2,
			TotalErrors:     1,
			TelemetryStatus: &TelemetryRunHealth{Status: "breached", Severity: "danger", SustainedBreach: true},
		},
	)
	return &storage.RunArchiveRecord{
		TestID:               testID,
		SchemaVersion:        archiveSchemaVersion,
		ArchivePath:          archiveDir,
		ManifestPath:         manifestPath,
		TransactionCount:     manifest.TransactionCount,
		ResourceSampleCount:  manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            startedAt,
		CompletedAt:          completedAt,
	}
}

func writeJSONL(t *testing.T, path string, values ...any) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	encoder := json.NewEncoder(file)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			t.Fatalf("encode %s: %v", path, err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func releaseTrendHeaderIndex(headers []string) map[string]int {
	index := make(map[string]int, len(headers))
	for i, header := range headers {
		index[header] = i
	}
	return index
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
