package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveAnalysisStreamsGraphAndRetainsOnlyRequestedPage(t *testing.T) {
	const transactionCount = archiveExactQuantileLimit + 500
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:    archiveSchemaVersion,
		TestID:           "streamed-analysis",
		TransactionsFile: "transactions.jsonl",
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	start := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	// Write newest-first to prove the bounded page path preserves the analysis
	// endpoint's historical timestamp ordering rather than archive file order.
	for index := 0; index < transactionCount; index++ {
		tx := TransactionEvent{
			Timestamp:        start.Add(time.Duration(transactionCount-index) * time.Second),
			ActionName:       "checkout",
			DependencyType:   "http",
			DependencyTarget: "shop.example",
			DurationMs:       float64(index + 1),
			Success:          true,
		}
		if err := encoder.Encode(tx); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	page := archivePage{Limit: 3, Offset: 7}
	run, err := archiveAnalysisRunFromRecord(
		manifest.TestID,
		"Streamed Analysis",
		&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath},
		transactionEventFilter{},
		&page,
	)
	if err != nil {
		t.Fatalf("archiveAnalysisRunFromRecord returned error: %v", err)
	}
	if run.Total != transactionCount || len(run.Transactions) != page.Limit {
		t.Fatalf("total/page = %d/%d, want %d/%d", run.Total, len(run.Transactions), transactionCount, page.Limit)
	}
	if want := start.Add(8 * time.Second); !run.Transactions[0].Timestamp.Equal(want) {
		t.Fatalf("first page timestamp = %s, want %s", run.Transactions[0].Timestamp, want)
	}

	actionID := archiveAnalysisNodeID("action", "checkout")
	acc := run.nodeAcc[actionID]
	if acc == nil || acc.Transactions != transactionCount {
		t.Fatalf("action accumulator = %#v, want %d transactions", acc, transactionCount)
	}
	if acc.Quantiles == nil || acc.Quantiles.digest == nil || len(acc.Quantiles.exact) != 0 {
		t.Fatalf("large percentile state was not promoted to a bounded digest: %#v", acc.Quantiles)
	}

	response := buildRunArchiveAnalysis(run, nil, transactionEventFilter{}, page)
	if response.Total != transactionCount || response.Count != page.Limit {
		t.Fatalf("response total/count = %d/%d, want %d/%d", response.Total, response.Count, transactionCount, page.Limit)
	}
	node := findArchiveAnalysisNode(response.Graph.Nodes, "action", "checkout")
	if node == nil || len(node.Runs) != 1 || node.Runs[0].Transactions != transactionCount {
		t.Fatalf("streamed graph action node = %#v", node)
	}
}
