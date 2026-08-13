package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeRemoteArchiveIntervalSummariesRetainsPersistedTransactionsWithoutExactRecompute(t *testing.T) {
	root := t.TempDir()
	timestamp := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	writeSummary := func(name string, transaction *IntervalTransactionMetrics) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(IntervalSummaryRecord{
			Timestamp:    timestamp,
			TestID:       "distributed-summary-fallback",
			Transactions: map[string]*IntervalTransactionMetrics{"Search": transaction},
		}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	destinationPath := filepath.Join(root, "final.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destinationPath, []remoteArchiveBundle{
		{
			target:        "generator-a",
			manifest:      RunArchiveManifest{IntervalSummaryCount: 1},
			summariesPath: writeSummary("a.jsonl", &IntervalTransactionMetrics{Name: "Search", TPS: 3, Count: 4, Errors: 1, AvgMs: 100}),
			// An unavailable raw-transaction path forces the bounded exact pass to
			// decline recomputation, as it does for an archive beyond its raw-data
			// budget. The durable interval breakdown must remain authoritative.
			transactionsPath: "",
		},
		{
			target:           "generator-b",
			manifest:         RunArchiveManifest{IntervalSummaryCount: 1},
			summariesPath:    writeSummary("b.jsonl", &IntervalTransactionMetrics{Name: "Search", TPS: 2, Count: 6, Errors: 2, AvgMs: 200}),
			transactionsPath: "",
		},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("summary count = %d, want 1", count)
	}
	final, err := os.Open(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	var summary IntervalSummaryRecord
	if err := json.NewDecoder(final).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	search := summary.Transactions["Search"]
	if search == nil || search.Name != "Search" || search.TPS != 5 || search.Count != 10 || search.Errors != 3 || search.AvgMs != 160 {
		t.Fatalf("fallback transaction metrics = %#v, want summed TPS/count/errors and attempt-weighted latency", search)
	}
}
