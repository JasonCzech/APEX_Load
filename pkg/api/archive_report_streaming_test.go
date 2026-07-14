package api

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBuildRunArchiveReportStreamsBeyondLegacyRecordLimit(t *testing.T) {
	const transactionCount = maxArchiveAnalysisRecords + 1
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "large-streamed-report",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      transactionCount,
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	txFile, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(txFile, 1<<20)
	for index := 0; index < transactionCount; index++ {
		if _, err := writer.WriteString("{\"success\":true}\n"); err != nil {
			txFile.Close()
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		txFile.Close()
		t.Fatal(err)
	}
	if err := txFile.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Overview.Transactions != transactionCount {
		t.Fatalf("transactions = %d, want %d", report.Overview.Transactions, transactionCount)
	}
	if report.ByAction[""].Transactions != transactionCount {
		t.Fatalf("empty-action transactions = %d, want %d", report.ByAction[""].Transactions, transactionCount)
	}
}

func TestArchiveQuantileAccumulatorPromotesToBoundedDigest(t *testing.T) {
	accumulator := &archiveQuantileAccumulator{}
	for index := 0; index < archiveExactQuantileLimit+100; index++ {
		accumulator.Add(float64(index))
	}
	if accumulator.digest == nil || len(accumulator.exact) != 0 {
		t.Fatalf("quantile accumulator did not promote to digest: %#v", accumulator)
	}
	if accumulator.Count() != archiveExactQuantileLimit+100 {
		t.Fatalf("count = %d, want %d", accumulator.Count(), archiveExactQuantileLimit+100)
	}
}
