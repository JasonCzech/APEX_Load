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
	transactions := []TransactionEvent{
		{
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
		},
		{
			Timestamp:  manifest.CreatedAt.Add(5 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "browser_navigate",
			DurationMs: 170,
			Success:    true,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "iphone_15_pro",
				DevicePlatform:        "ios",
				DeviceTier:            "high",
				BrowserEngine:         "webkit",
				FCPMs:                 140,
				LCPMs:                 300,
				CLSScore:              0.05,
				INPMs:                 65,
				BlockingResourceCount: 3,
				LongTaskCount:         2,
				JSHeapUsedMB:          20,
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(10 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "browser_navigate",
			DurationMs: 140,
			Success:    true,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "pixel_8",
				DevicePlatform:        "android",
				DeviceTier:            "high",
				BrowserEngine:         "chromium",
				FCPMs:                 100,
				LCPMs:                 180,
				CLSScore:              0.02,
				INPMs:                 45,
				BlockingResourceCount: 1,
				LongTaskCount:         1,
				JSHeapUsedMB:          16,
			},
		},
	}
	var txPayload []byte
	for _, transaction := range transactions {
		txBytes, err := json.Marshal(transaction)
		if err != nil {
			t.Fatalf("marshal transaction: %v", err)
		}
		txPayload = append(txPayload, txBytes...)
		txPayload = append(txPayload, '\n')
	}
	if err := os.WriteFile(filepath.Join(tempDir, manifest.TransactionsFile), txPayload, 0o644); err != nil {
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
	if report.Overview.Browser == nil || report.Overview.Browser.Samples != 3 {
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
	if report.BrowserByPlatform == nil || report.BrowserByPlatform["ios"] == nil {
		t.Fatalf("expected browser-by-platform summary, got %#v", report.BrowserByPlatform)
	}
	if report.BrowserByPlatform["ios"].BrowserEngine != "webkit" {
		t.Fatalf("platform browser engine = %q, want webkit", report.BrowserByPlatform["ios"].BrowserEngine)
	}
	if report.BrowserByPlatform["ios"].Samples != 2 {
		t.Fatalf("platform samples = %d, want 2", report.BrowserByPlatform["ios"].Samples)
	}
	if report.BrowserByPlatform["ios"].AvgLCPMs != 270 {
		t.Fatalf("platform AvgLCPMs = %v, want 270", report.BrowserByPlatform["ios"].AvgLCPMs)
	}
	if report.BrowserByEngine == nil || report.BrowserByEngine["webkit"] == nil {
		t.Fatalf("expected browser-by-engine summary, got %#v", report.BrowserByEngine)
	}
	if report.BrowserByEngine["webkit"].Samples != 2 {
		t.Fatalf("webkit samples = %d, want 2", report.BrowserByEngine["webkit"].Samples)
	}
	if report.BrowserByEngine["webkit"].AvgLCPMs != 270 {
		t.Fatalf("webkit AvgLCPMs = %v, want 270", report.BrowserByEngine["webkit"].AvgLCPMs)
	}
	if report.BrowserByEngine["chromium"] == nil || report.BrowserByEngine["chromium"].AvgLCPMs != 180 {
		t.Fatalf("chromium summary = %#v, want AvgLCPMs 180", report.BrowserByEngine["chromium"])
	}
}