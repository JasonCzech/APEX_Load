package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestBuildRunArchiveReportClustersErrorPatterns(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-1",
		TestName:              "Checkout",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	timestamp := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	txFile, err := os.Create(filepath.Join(archiveDir, "transactions.jsonl"))
	if err != nil {
		t.Fatalf("Create transactions.jsonl returned error: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range []TransactionEvent{
		{Timestamp: timestamp, TestID: "test-1", TestName: "Checkout", ActionName: "CreateOrder", Success: false, ErrorMessage: "Request validation failed for order 1001"},
		{Timestamp: timestamp.Add(20 * time.Second), TestID: "test-1", TestName: "Checkout", ActionName: "CreateOrder", Success: false, ErrorMessage: "Request validation failed for order 1002"},
		{Timestamp: timestamp.Add(40 * time.Second), TestID: "test-1", TestName: "Checkout", ActionName: "HealthCheck", Success: true},
	} {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("Encode transaction returned error: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions.jsonl returned error: %v", err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if len(report.ErrorPatterns) != 1 {
		t.Fatalf("expected 1 clustered error pattern, got %#v", report.ErrorPatterns)
	}
	pattern := report.ErrorPatterns[0]
	if pattern.Count != 2 {
		t.Fatalf("pattern count = %d, want 2", pattern.Count)
	}
	if pattern.Signature != "request validation failed for order #" {
		t.Fatalf("pattern signature = %q, want normalized order signature", pattern.Signature)
	}
	if len(pattern.Actions) != 1 || pattern.Actions[0] != "CreateOrder" {
		t.Fatalf("pattern actions = %#v, want CreateOrder", pattern.Actions)
	}
	if pattern.FirstSeen != timestamp || pattern.LatestSeen != timestamp.Add(20*time.Second) {
		t.Fatalf("pattern timing = %#v, want first=%s latest=%s", pattern, timestamp, timestamp.Add(20*time.Second))
	}
}

func TestTraceparentIDsExtractsAPMIdentifiers(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	traceID, spanID := traceparentIDs(traceparent)

	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q, want W3C trace id", traceID)
	}
	if spanID != "00f067aa0ba902b7" {
		t.Fatalf("span id = %q, want W3C span id", spanID)
	}
	if traceID, spanID := traceparentIDs("corr-archive-1"); traceID != "" || spanID != "" {
		t.Fatalf("non-traceparent ids = (%q, %q), want empty", traceID, spanID)
	}
	if traceID, spanID := traceparentIDs("00-00000000000000000000000000000000-0000000000000000-01"); traceID != "" || spanID != "" {
		t.Fatalf("zero traceparent ids = (%q, %q), want empty", traceID, spanID)
	}
}
