package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
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
			PageURL:    "https://example.test/home",
			NetworkEntries: []protocol.NetworkEntry{
				{URL: "https://example.test/app.js", Method: "GET", StatusCode: 200, ResourceType: "script", DurationMs: 45, BytesReceived: 2048},
				{URL: "https://example.test/styles.css", Method: "GET", StatusCode: 200, ResourceType: "stylesheet", DurationMs: 20, BytesReceived: 1024},
			},
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
				BrowserProcessCPUPct:  20,
				BrowserProcessMemMB:   160,
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
				BrowserProcessCPUPct:  40,
				BrowserProcessMemMB:   240,
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
				BrowserProcessCPUPct:  30,
				BrowserProcessMemMB:   180,
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
	if report.BrowserByPlatform["ios"].AvgBrowserProcessCPU != 30 {
		t.Fatalf("platform AvgBrowserProcessCPU = %v, want 30", report.BrowserByPlatform["ios"].AvgBrowserProcessCPU)
	}
	if report.BrowserByPlatform["ios"].AvgBrowserProcessMem != 200 {
		t.Fatalf("platform AvgBrowserProcessMem = %v, want 200", report.BrowserByPlatform["ios"].AvgBrowserProcessMem)
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
	if len(report.BrowserResources) != 2 {
		t.Fatalf("browser resources = %d, want 2", len(report.BrowserResources))
	}
	if report.BrowserResources[0].URL != "https://example.test/app.js" || report.BrowserResources[0].DurationMs != 45 {
		t.Fatalf("first browser resource = %#v, want app.js sorted by duration", report.BrowserResources[0])
	}
	if report.BrowserResources[0].DeviceProfile != "iphone_se" || report.BrowserResources[0].BrowserEngine != "webkit" {
		t.Fatalf("first browser resource device context = %#v", report.BrowserResources[0])
	}
}

func TestAppendTopBrowserResourceTimingsFromTransactionBoundsResources(t *testing.T) {
	tx := TransactionEvent{
		Timestamp:  time.Date(2026, time.May, 17, 10, 0, 0, 0, time.UTC),
		ActionName: "browser_navigate",
		PageURL:    "https://example.test/home",
		ClientMetrics: &protocol.ClientMetrics{
			DeviceProfile:  "iphone_15_pro",
			DevicePlatform: "ios",
			BrowserEngine:  "webkit",
		},
	}
	for index := 0; index < 60; index++ {
		tx.NetworkEntries = append(tx.NetworkEntries, protocol.NetworkEntry{
			URL:          fmt.Sprintf("https://example.test/resource-%02d.js", index),
			Method:       "GET",
			ResourceType: "script",
			DurationMs:   float64(index),
		})
	}

	resources := appendTopBrowserResourceTimingsFromTransaction(nil, tx, 50)
	if len(resources) != 50 {
		t.Fatalf("resources = %d, want 50", len(resources))
	}
	if resources[0].DurationMs != 59 || resources[0].URL != "https://example.test/resource-59.js" {
		t.Fatalf("first resource = %#v, want slowest resource", resources[0])
	}
	if resources[len(resources)-1].DurationMs != 10 {
		t.Fatalf("last resource duration = %v, want boundary duration 10", resources[len(resources)-1].DurationMs)
	}
	for _, resource := range resources {
		if resource.DurationMs < 10 {
			t.Fatalf("resource below top-50 boundary retained: %#v", resource)
		}
		if resource.DeviceProfile != "iphone_15_pro" || resource.BrowserEngine != "webkit" {
			t.Fatalf("resource lost browser context: %#v", resource)
		}
	}
}

func TestBuildRunArchiveBrowserDiagnosticsExportsEvidence(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-browser-diagnostics",
		TestName:              "Browser Diagnostics",
		CreatedAt:             time.Date(2026, time.May, 16, 9, 30, 0, 0, time.UTC),
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
			DurationMs: 250,
			StatusCode: 200,
			Success:    true,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile: "iphone_15_pro",
				BrowserEngine: "webkit",
				FCPMs:         120,
				LCPMs:         240,
			},
			Snapshot: &protocol.ErrorSnapshot{
				RequestMethod:    "GET",
				RequestURL:       "https://example.test/home",
				ResponseStatus:   200,
				PageURL:          "https://example.test/home",
				PageTitle:        "Home",
				HTML:             "<html><title>Home</title></html>",
				ScreenshotBase64: "c2NyZWVuc2hvdA==",
				ConsoleEntries:   []protocol.ConsoleEntry{{Level: "log", Text: "ready", Source: "console", Timestamp: manifest.CreatedAt}},
				NetworkEntries:   []protocol.NetworkEntry{{URL: "https://example.test/app.js", Method: "GET", StatusCode: 200, ResourceType: "script", DurationMs: 12, BytesReceived: 512}},
				DOMSummary:       &protocol.DOMSummary{Title: "Home", ReadyState: "complete", VisibleText: []string{"Welcome"}},
			},
		},
		{
			Timestamp:  manifest.CreatedAt.Add(time.Second),
			TestID:     manifest.TestID,
			TestName:   manifest.TestName,
			ActionName: "api_call",
			DurationMs: 50,
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

	bundle, err := buildRunArchiveBrowserDiagnostics(&storage.RunArchiveRecord{ArchivePath: tempDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveBrowserDiagnostics: %v", err)
	}
	if bundle.SchemaVersion != browserDiagnosticsSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", bundle.SchemaVersion, browserDiagnosticsSchemaVersion)
	}
	if bundle.Overview.BrowserTransactions != 1 || bundle.Overview.ClientMetricSamples != 1 {
		t.Fatalf("unexpected diagnostics overview %#v", bundle.Overview)
	}
	if bundle.Overview.ScreenshotArtifacts != 1 || bundle.Overview.HTMLArtifacts != 1 {
		t.Fatalf("unexpected artifact counts %#v", bundle.Overview)
	}
	if bundle.Overview.ConsoleEntries != 1 || bundle.Overview.NetworkEntries != 1 || bundle.Overview.DOMSnapshots != 1 {
		t.Fatalf("unexpected evidence counts %#v", bundle.Overview)
	}
	if len(bundle.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(bundle.Events))
	}
	if bundle.TotalEvents != 1 || bundle.RetainedEvents != 1 || bundle.DroppedEvents != 0 || bundle.Truncated {
		t.Fatalf("unexpected retention metadata: %#v", bundle)
	}
	if bundle.Events[0].Snapshot == nil || bundle.Events[0].Snapshot.RequestURL != "https://example.test/home" {
		t.Fatalf("unexpected snapshot %#v", bundle.Events[0].Snapshot)
	}

	var zipBuf bytes.Buffer
	if err := writeRunArchiveBrowserDiagnosticsZip(bundle, &zipBuf); err != nil {
		t.Fatalf("writeRunArchiveBrowserDiagnosticsZip: %v", err)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len()))
	if err != nil {
		t.Fatalf("read diagnostics zip: %v", err)
	}
	files := map[string]string{}
	for _, file := range zipReader.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open zip file %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatalf("read zip file %s: %v", file.Name, err)
		}
		files[file.Name] = string(data)
	}
	if files["browser_diagnostics.json"] == "" {
		t.Fatal("expected browser_diagnostics.json in diagnostics zip")
	}
	if files["screenshots/001-browser_navigate.b64"] != "c2NyZWVuc2hvdA==" {
		t.Fatalf("unexpected screenshot artifact %q", files["screenshots/001-browser_navigate.b64"])
	}
	if files["html/001-browser_navigate.html"] != "<html><title>Home</title></html>" {
		t.Fatalf("unexpected html artifact %q", files["html/001-browser_navigate.html"])
	}
	var indexed BrowserDiagnosticsBundle
	if err := json.Unmarshal([]byte(files["browser_diagnostics.json"]), &indexed); err != nil {
		t.Fatalf("unmarshal diagnostics index: %v", err)
	}
	if indexed.Events[0].Artifacts.ScreenshotFile != "screenshots/001-browser_navigate.b64" {
		t.Fatalf("unexpected screenshot reference %#v", indexed.Events[0].Artifacts)
	}
	if indexed.Events[0].Artifacts.HTMLFile != "html/001-browser_navigate.html" {
		t.Fatalf("unexpected html reference %#v", indexed.Events[0].Artifacts)
	}
}

func TestBuildRunArchiveBrowserDiagnosticsTruncatesAndPrefersFailures(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: "bounded-browser-diagnostics", TestName: "Bounded",
		CreatedAt: time.Now().UTC(), TransactionsFile: "transactions.jsonl",
	}
	manifestPath := filepath.Join(tempDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	for index := 0; index < 5_001; index++ {
		success := index != 5_000
		tx := TransactionEvent{
			Timestamp: manifest.CreatedAt.Add(time.Duration(index) * time.Millisecond),
			TestID:    manifest.TestID, ActionName: "browser_navigate", Success: success,
			ClientMetrics: &protocol.ClientMetrics{FCPMs: float64(index + 1)},
		}
		if !success {
			tx.ErrorMessage = "last failure"
		}
		if err := encoder.Encode(tx); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tempDir, manifest.TransactionsFile), payload.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := buildRunArchiveBrowserDiagnostics(&storage.RunArchiveRecord{ArchivePath: tempDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.TotalEvents != 5_001 || bundle.RetainedEvents != 5_000 || bundle.DroppedEvents != 1 || !bundle.Truncated {
		t.Fatalf("retention metadata = total %d retained %d dropped %d truncated %v", bundle.TotalEvents, bundle.RetainedEvents, bundle.DroppedEvents, bundle.Truncated)
	}
	foundFailure := false
	for _, event := range bundle.Events {
		foundFailure = foundFailure || (!event.Success && event.ErrorMessage == "last failure")
	}
	if !foundFailure {
		t.Fatal("bounded diagnostics did not retain failure evidence")
	}
}

func TestRetainBoundedBrowserDiagnosticEventFindsFittingReplacementAndKeepsOrder(t *testing.T) {
	events := []BrowserDiagnosticsEvent{
		{Index: 1, Success: true, PageURL: "x"},
		{Index: 2, Success: true, PageURL: strings.Repeat("x", 20)},
	}
	payloadBytes := browserDiagnosticEventPayloadBytes(&events[0]) + browserDiagnosticEventPayloadBytes(&events[1])
	failure := BrowserDiagnosticsEvent{Index: 3, Success: false, ErrorMessage: strings.Repeat("e", 15)}
	failureBytes := browserDiagnosticEventPayloadBytes(&failure)

	retained, retainedBytes := retainBoundedBrowserDiagnosticEvent(events, payloadBytes, failure, failureBytes, 2, 25)
	if len(retained) != 2 || retained[0].Index != 1 || retained[1].Index != 3 || retained[1].Success {
		t.Fatalf("retained events = %#v, want small success followed by failure", retained)
	}
	if retainedBytes != 16 {
		t.Fatalf("retained payload bytes = %d, want 16", retainedBytes)
	}
}

func TestRetainBoundedBrowserDiagnosticEventCanEvictMultipleSuccesses(t *testing.T) {
	events := []BrowserDiagnosticsEvent{
		{Index: 1, Success: true, PageURL: "aaaaaa"},
		{Index: 2, Success: true, PageURL: "bbbbbb"},
	}
	payloadBytes := browserDiagnosticEventPayloadBytes(&events[0]) + browserDiagnosticEventPayloadBytes(&events[1])
	failure := BrowserDiagnosticsEvent{Index: 3, Success: false, ErrorMessage: "failure-ten"}
	failureBytes := browserDiagnosticEventPayloadBytes(&failure)

	retained, retainedBytes := retainBoundedBrowserDiagnosticEvent(events, payloadBytes, failure, failureBytes, 2, failureBytes)
	if len(retained) != 1 || retained[0].Index != 3 || retained[0].Success {
		t.Fatalf("retained events = %#v, want only the failure", retained)
	}
	if retainedBytes != failureBytes {
		t.Fatalf("retained payload bytes = %d, want %d", retainedBytes, failureBytes)
	}
}

func TestBrowserDiagnosticEventPayloadBytesCountsNestedVariableData(t *testing.T) {
	event := BrowserDiagnosticsEvent{
		Group:      "group",
		ActionName: "action",
		Snapshot:   &BrowserDiagnosticSnapshot{RequestMethod: "POST"},
		NetworkEntries: []protocol.NetworkEntry{{
			RequestBodyEncoding: "base64",
		}},
		DOMSummary: &protocol.DOMSummary{
			Title:       "title",
			ReadyState:  "complete",
			VisibleText: []string{"one", "two"},
		},
		ClientMetrics: &protocol.ClientMetrics{
			DeviceProfile:  "phone",
			DevicePlatform: "ios",
			DeviceTier:     "high",
			BrowserEngine:  "webkit",
		},
	}
	want := len("groupactionPOSTbase64titlecompleteonetwophoneioshighwebkit")
	if got := browserDiagnosticEventPayloadBytes(&event); got != want {
		t.Fatalf("nested diagnostic payload bytes = %d, want %d", got, want)
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

func TestMergeArchiveSOAPFaultTimelineKeepsDimensionsWritableUntilFinalization(t *testing.T) {
	timestamp := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		first  protocol.ServerTelemetry
		second protocol.ServerTelemetry
	}{
		{
			name:   "category then code",
			first:  protocol.ServerTelemetry{SOAPFaultCategory: "client"},
			second: protocol.ServerTelemetry{SOAPFaultCode: "env:Sender"},
		},
		{
			name:   "code then category",
			first:  protocol.ServerTelemetry{SOAPFaultCode: "env:Receiver"},
			second: protocol.ServerTelemetry{SOAPFaultCategory: "server"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buckets := map[time.Time]*RunArchiveSOAPFaultTimelineBucket{}
			mergeArchiveSOAPFaultTimelineBucket(buckets, TransactionEvent{Timestamp: timestamp, ServerTelemetry: &test.first})
			mergeArchiveSOAPFaultTimelineBucket(buckets, TransactionEvent{Timestamp: timestamp.Add(10 * time.Second), ServerTelemetry: &test.second})
			bucket := buckets[timestamp.Truncate(time.Minute)]
			if bucket == nil || bucket.Total != 2 || len(bucket.ByCode) != 1 || len(bucket.ByCategory) != 1 {
				t.Fatalf("merged bucket = %#v, want both dimensions", bucket)
			}
		})
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

func TestSOAPPayloadTelemetryDoesNotClassifyHTTPPayloadsAsSOAP(t *testing.T) {
	for _, tc := range []struct {
		name string
		tx   TransactionEvent
		want float64
	}{
		{"http", TransactionEvent{ServerTelemetry: &protocol.ServerTelemetry{RequestPayloadKB: 10}}, 0},
		{"soap operation", TransactionEvent{Extractions: map[string]string{"soap_operation": "Checkout"}, ServerTelemetry: &protocol.ServerTelemetry{RequestPayloadKB: 10}}, 10},
		{"soap envelope", TransactionEvent{ServerTelemetry: &protocol.ServerTelemetry{RequestPayloadKB: 10, EnvelopeOverheadKB: 1}}, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := archiveSOAPPayloadTelemetry(tc.tx)
			if got != tc.want {
				t.Fatalf("payload = %v, want %v", got, tc.want)
			}
		})
	}
}
