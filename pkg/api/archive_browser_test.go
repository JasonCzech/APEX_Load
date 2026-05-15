package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestBuildRunArchiveReportAggregatesBrowserMetrics(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-1",
		TestName:              "Browser Metrics",
		CreatedAt:             time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestPath := filepath.Join(tempDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	transaction := TransactionEvent{
		Timestamp:  manifest.CreatedAt,
		TestID:     manifest.TestID,
		TestName:   manifest.TestName,
		ActionName: "browser_navigate",
		DurationMs: 150,
		Success:    true,
		ClientMetrics: &protocol.ClientMetrics{
			DeviceProfile:         "iphone_se",
			DevicePlatform:        "ios",
			DeviceTier:            "low",
			BrowserEngine:         "webkit",
			FCPMs:                 120,
			LCPMs:                 240,
			CLSScore:              0.03,
			INPMs:                 55,
			BlockingResourceCount: 2,
			LongTaskCount:         1,
			JSHeapUsedMB:          18,
		},
	}
	txBytes, err := json.Marshal(transaction)
	if err != nil {
		t.Fatalf("marshal transaction: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, manifest.TransactionsFile), append(txBytes, '\n'), 0o644); err != nil {
		t.Fatalf("write transactions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, manifest.ResourceSamplesFile), nil, 0o644); err != nil {
		t.Fatalf("write resources: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, manifest.IntervalSummariesFile), nil, 0o644); err != nil {
		t.Fatalf("write summaries: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: tempDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport: %v", err)
	}
	if report.Overview.Browser == nil || report.Overview.Browser.Samples != 1 {
		t.Fatalf("expected browser overview summary, got %#v", report.Overview.Browser)
	}
	if report.BrowserByDevice == nil || report.BrowserByDevice["iphone_se"] == nil {
		t.Fatalf("expected browser-by-device summary, got %#v", report.BrowserByDevice)
	}
	if report.BrowserByDevice["iphone_se"].DeviceTier != "low" {
		t.Fatalf("device tier = %q, want low", report.BrowserByDevice["iphone_se"].DeviceTier)
	}
	if report.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("browser engine = %q, want webkit", report.BrowserByDevice["iphone_se"].BrowserEngine)
	}
	if report.BrowserByDevice["iphone_se"].DevicePlatform != "ios" {
		t.Fatalf("device platform = %q, want ios", report.BrowserByDevice["iphone_se"].DevicePlatform)
	}
}