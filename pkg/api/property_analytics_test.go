package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPropertyReportUsesActionPopulationAndArchiveEvidence(t *testing.T) {
	root := t.TempDir()
	manifest := RunArchiveManifest{TestID: "properties", TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
	body, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i, duration := range []float64{10, 30, 1000} {
		event := TransactionEvent{Timestamp: time.Now(), TestID: "properties", ActionName: "request", DurationMs: duration, Success: true, PropertyDimensions: map[string]string{"kind": "checkout"}, BusinessTransaction: i == 2}
		event.Population = "action"
		if i == 2 {
			event.Population = "transaction"
		}
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: "properties", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")})
	if err != nil {
		t.Fatal(err)
	}
	got := report.ByProperty["kind=checkout"]
	if got.Transactions != 2 || got.AvgResponseMs != 20 {
		t.Fatalf("action population doubled: %+v", got)
	}
}
func TestMessagingGateReadsArchivedAvailability(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, lag := range []int64{0, 12} {
		if err := encoder.Encode(TransactionEvent{ActionName: "Consume", BusinessTransaction: true, Success: true, DurationMs: 1, ServerTelemetry: &protocol.ServerTelemetry{ConsumerOffsetLagObserved: true, ConsumerOffsetLag: lag}}); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	manifest := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{{Transaction: "Consume", Metric: "max_consumer_offset_lag", Comparator: "lte", Threshold: 5}}}}}
	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.TransactionRuleResults) != 1 || manifest.TransactionRuleResults[0].Status != "fail" {
		t.Fatalf("gate ignored backlog: %+v", manifest.TransactionRuleResults)
	}
}
