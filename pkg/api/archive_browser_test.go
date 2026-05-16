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

func TestBuildRunArchiveReportAggregatesSOAPFaults(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-1",
		TestName:              "SOAP Fault Metrics",
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
			ActionName: "SOAPRequest",
			DurationMs: 180,
			Success:    false,
			ServerTelemetry: &protocol.ServerTelemetry{
				SOAPFaultCode:     "env:Sender",
				SOAPFaultCategory: "client",
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(20 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "SOAPRequest",
			DurationMs: 220,
			Success:    false,
			ServerTelemetry: &protocol.ServerTelemetry{
				SOAPFaultCode:     "env:Sender",
				SOAPFaultCategory: "client",
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(65 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "SOAPRequest",
			DurationMs: 260,
			Success:    false,
			ServerTelemetry: &protocol.ServerTelemetry{
				SOAPFaultCode:     "env:Receiver",
				SOAPFaultCategory: "server",
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(15 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "SOAPRequest",
			DurationMs: 140,
			Success:    true,
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
	if report.BySOAPFaultCode["env:Sender"] != 2 || report.BySOAPFaultCode["env:Receiver"] != 1 {
		t.Fatalf("unexpected SOAP fault code counts %#v", report.BySOAPFaultCode)
	}
	if report.BySOAPFaultCategory["client"] != 2 || report.BySOAPFaultCategory["server"] != 1 {
		t.Fatalf("unexpected SOAP fault category counts %#v", report.BySOAPFaultCategory)
	}
	if len(report.SOAPFaultTimeline) != 2 {
		t.Fatalf("unexpected SOAP fault timeline bucket count %d", len(report.SOAPFaultTimeline))
	}
	if report.SOAPFaultTimeline[0].Timestamp != manifest.CreatedAt.Truncate(time.Minute) {
		t.Fatalf("unexpected first SOAP fault timeline bucket time %s", report.SOAPFaultTimeline[0].Timestamp)
	}
	if report.SOAPFaultTimeline[0].Total != 2 || report.SOAPFaultTimeline[0].ByCode["env:Sender"] != 2 || report.SOAPFaultTimeline[0].ByCategory["client"] != 2 {
		t.Fatalf("unexpected first SOAP fault timeline bucket %#v", report.SOAPFaultTimeline[0])
	}
	if report.SOAPFaultTimeline[1].Timestamp != manifest.CreatedAt.Add(time.Minute).Truncate(time.Minute) {
		t.Fatalf("unexpected second SOAP fault timeline bucket time %s", report.SOAPFaultTimeline[1].Timestamp)
	}
	if report.SOAPFaultTimeline[1].Total != 1 || report.SOAPFaultTimeline[1].ByCode["env:Receiver"] != 1 || report.SOAPFaultTimeline[1].ByCategory["server"] != 1 {
		t.Fatalf("unexpected second SOAP fault timeline bucket %#v", report.SOAPFaultTimeline[1])
	}
}

func TestBuildRunArchiveReportAggregatesSOAPPayloadTimeline(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-1",
		TestName:              "SOAP Payload Metrics",
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
			ActionName: "SOAPRequest",
			DurationMs: 180,
			Success:    true,
			ServerTelemetry: &protocol.ServerTelemetry{
				RequestPayloadKB:   1.2,
				ResponsePayloadKB:  4.0,
				EnvelopeOverheadKB: 0.7,
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(20 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "SOAPRequest",
			DurationMs: 220,
			Success:    true,
			ServerTelemetry: &protocol.ServerTelemetry{
				RequestPayloadKB:   1.8,
				ResponsePayloadKB:  6.0,
				EnvelopeOverheadKB: 0.9,
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(65 * time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "SOAPRequest",
			DurationMs: 260,
			Success:    false,
			ServerTelemetry: &protocol.ServerTelemetry{
				RequestPayloadKB:   2.0,
				ResponsePayloadKB:  8.0,
				EnvelopeOverheadKB: 1.5,
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
	if len(report.SOAPPayloadTimeline) != 2 {
		t.Fatalf("unexpected SOAP payload timeline bucket count %d", len(report.SOAPPayloadTimeline))
	}
	firstBucket := report.SOAPPayloadTimeline[0]
	if firstBucket.Timestamp != manifest.CreatedAt.Truncate(time.Minute) {
		t.Fatalf("unexpected first SOAP payload timeline bucket time %s", firstBucket.Timestamp)
	}
	if firstBucket.Samples != 2 || firstBucket.AvgRequestPayloadKB != 1.5 || firstBucket.AvgResponsePayloadKB != 5.0 || firstBucket.AvgEnvelopeOverheadKB != 0.8 || firstBucket.MaxEnvelopeOverheadKB != 0.9 {
		t.Fatalf("unexpected first SOAP payload timeline bucket %#v", firstBucket)
	}
	secondBucket := report.SOAPPayloadTimeline[1]
	if secondBucket.Timestamp != manifest.CreatedAt.Add(time.Minute).Truncate(time.Minute) {
		t.Fatalf("unexpected second SOAP payload timeline bucket time %s", secondBucket.Timestamp)
	}
	if secondBucket.Samples != 1 || secondBucket.AvgRequestPayloadKB != 2.0 || secondBucket.AvgResponsePayloadKB != 8.0 || secondBucket.AvgEnvelopeOverheadKB != 1.5 || secondBucket.MaxEnvelopeOverheadKB != 1.5 {
		t.Fatalf("unexpected second SOAP payload timeline bucket %#v", secondBucket)
	}
}
