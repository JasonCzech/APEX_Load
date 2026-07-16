package generatorrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamJSONLToParquetAcceptsLargeSnapshotRecord(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "transactions.jsonl")
	outputPath := filepath.Join(dir, "transactions.parquet")
	input, err := os.Create(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	event := archiveTransactionEvent{
		Timestamp:    time.Now(),
		TestID:       "run-1",
		ErrorMessage: strings.Repeat("x", 3*1024*1024),
	}
	if err := json.NewEncoder(input).Encode(event); err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}

	err = streamJSONLToParquet(inputPath, outputPath, func(item archiveTransactionEvent) parquetTransactionEvent {
		return toParquetTransactions([]archiveTransactionEvent{item})[0]
	})
	if err != nil {
		t.Fatalf("streamJSONLToParquet rejected valid >2MiB record: %v", err)
	}
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		t.Fatalf("parquet output missing or empty: info=%v err=%v", info, err)
	}
}

func TestStreamJSONLToParquetRejectsOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "transactions.jsonl")
	outputPath := filepath.Join(dir, "transactions.parquet")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(inputPath, int64(maxGeneratorArchiveJSONLRecordBytes)+1); err != nil {
		t.Fatal(err)
	}

	if err := streamJSONLToParquet(inputPath, outputPath, func(item archiveTransactionEvent) parquetTransactionEvent {
		return toParquetTransactions([]archiveTransactionEvent{item})[0]
	}); err == nil {
		t.Fatal("streamJSONLToParquet accepted an archive record above the memory-safety ceiling")
	}
}
