package generatorrpc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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

func TestToParquetTransactionsCarriesBusinessTransactionProvenance(t *testing.T) {
	rows := toParquetTransactions([]archiveTransactionEvent{{BusinessTransaction: true, Population: "transaction"}})
	if len(rows) != 1 || !rows[0].BusinessTransaction || rows[0].Population != "transaction" {
		t.Fatalf("parquet transaction provenance = %#v", rows)
	}
}

func TestArchiveRecorderCarriesBusinessTransactionProvenance(t *testing.T) {
	var output bytes.Buffer
	recorder := &archiveRecorder{
		manifest:  archiveManifest{TestID: "run-1", TestName: "Run 1"},
		txEncoder: json.NewEncoder(&output),
	}
	if err := recorder.RecordResult(&protocol.Result{
		ActionName:          "Checkout",
		BusinessTransaction: true,
		Success:             true,
	}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	var event archiveTransactionEvent
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	if !event.BusinessTransaction || event.ActionName != "Checkout" || event.Population != "transaction" {
		t.Fatalf("archived transaction provenance = %#v", event)
	}
}

func TestGeneratorAdaptiveMessageDetailOneItemBudgetRetainsHighPriority(t *testing.T) {
	var output bytes.Buffer
	recorder := &archiveRecorder{
		manifest:  archiveManifest{TestID: "run-adaptive", TestName: "Adaptive"},
		txEncoder: json.NewEncoder(&output),
	}
	result := &protocol.Result{
		Success: true, MessageDetailRequested: true, MessageDetailMode: "adaptive", MessageDetailPriority: 3,
		MessageDetailMaxItems: 1, MessageDetailMaxItemBytes: 256, MessageDetailMaxTotalBytes: 256,
		Snapshot: &protocol.ErrorSnapshot{ResponseBody: "outlier"},
	}
	if err := recorder.RecordResult(result); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if !result.MessageDetailRetained || recorder.manifest.MessageDetailCapture == nil || recorder.manifest.MessageDetailCapture.RetainedItems != 1 {
		t.Fatalf("one-item generator capture = result %#v stats %#v", result, recorder.manifest.MessageDetailCapture)
	}
}

func TestCombinedSinkArchivesSyntheticTransactionWithoutDuplicatingActionResult(t *testing.T) {
	var output bytes.Buffer
	recorder := &archiveRecorder{
		manifest:  archiveManifest{TestID: "run-synthetic", TestName: "Synthetic"},
		txEncoder: json.NewEncoder(&output),
	}
	transactions := &transactionBuffer{}
	sink := combineSinks(transactions, recorder)
	ordinary := &protocol.Result{ActionName: "HTTP request", Success: true, StartTime: time.Now()}
	synthetic := &protocol.Result{ActionName: "Checkout flow", BusinessTransaction: true, Success: true, StartTime: time.Now()}
	if err := sink.RecordResult(ordinary); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	transactionSink, ok := sink.(interface{ RecordTransaction(*protocol.Result) error })
	if !ok {
		t.Fatalf("combined sink %T does not forward synthetic transactions", sink)
	}
	if err := transactionSink.RecordTransaction(synthetic); err != nil {
		t.Fatalf("RecordTransaction: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var events []archiveTransactionEvent
	for decoder.More() {
		var event archiveTransactionEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode transaction: %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 2 || recorder.manifest.TransactionCount != 2 {
		t.Fatalf("archive events/count = %#v/%d, want one action plus one logical transaction", events, recorder.manifest.TransactionCount)
	}
	if events[0].ActionName != "HTTP request" || events[0].BusinessTransaction {
		t.Fatalf("ordinary action event = %#v", events[0])
	}
	if events[1].ActionName != "Checkout flow" || !events[1].BusinessTransaction {
		t.Fatalf("synthetic transaction event = %#v", events[1])
	}
	if drained := transactions.Drain(); len(drained) != 1 || drained[0].Name != "Checkout flow" {
		t.Fatalf("live transaction buffer = %#v, want one synthetic transaction", drained)
	}
}
