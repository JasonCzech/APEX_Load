package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestCompactIntervalTransactionsIsDeterministicAndPreservesOverflowTotals(t *testing.T) {
	exactLimit := make(map[string]*IntervalTransactionMetrics, maxIntervalSummaryTransactions)
	for index := 0; index < maxIntervalSummaryTransactions; index++ {
		name := fmt.Sprintf("transaction-%03d", index)
		exactLimit[name] = &IntervalTransactionMetrics{Name: name, Count: 1}
	}
	exactLimit = compactIntervalTransactions(exactLimit)
	if len(exactLimit) != maxIntervalSummaryTransactions || exactLimit["transaction-511"] == nil || exactLimit[intervalSummaryTransactionOverflow] != nil {
		t.Fatalf("exact-limit transaction map was unnecessarily compacted: len=%d tail=%#v overflow=%#v", len(exactLimit), exactLimit["transaction-511"], exactLimit[intervalSummaryTransactionOverflow])
	}

	build := func(reverse bool) map[string]*IntervalTransactionMetrics {
		metrics := make(map[string]*IntervalTransactionMetrics, maxIntervalSummaryTransactions+1)
		for offset := 0; offset <= maxIntervalSummaryTransactions; offset++ {
			index := offset
			if reverse {
				index = maxIntervalSummaryTransactions - offset
			}
			name := fmt.Sprintf("transaction-%03d", index)
			metrics[name] = &IntervalTransactionMetrics{Name: name, TPS: 1, Count: 1, Errors: int64(index % 2), AvgMs: float64(index)}
		}
		return compactIntervalTransactions(metrics)
	}

	forward := build(false)
	reverse := build(true)
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatal("compacted transaction selection depends on insertion order")
	}
	if len(forward) != maxIntervalSummaryTransactions {
		t.Fatalf("transaction count = %d, want bounded count %d", len(forward), maxIntervalSummaryTransactions)
	}
	if forward["transaction-510"] == nil || forward["transaction-511"] != nil || forward["transaction-512"] != nil {
		t.Fatalf("retained transaction names are not the deterministic lexical prefix: %#v", forward)
	}
	overflow := forward[intervalSummaryTransactionOverflow]
	if overflow == nil || overflow.TPS != 2 || overflow.Count != 2 || overflow.Errors != 1 || overflow.AvgMs != 511.5 {
		t.Fatalf("overflow = %#v, want aggregate of the two omitted transactions", overflow)
	}
}

func TestMergeIntervalTransactionsUsesAttemptWeightedLatencyAndSuccessTPS(t *testing.T) {
	merged := mergeIntervalTransactions(nil, map[string]*IntervalTransactionMetrics{
		"Search": {Name: "Search", TPS: 3, Count: 4, Errors: 1, AvgMs: 100},
	})
	merged = mergeIntervalTransactions(merged, map[string]*IntervalTransactionMetrics{
		"Search": {Name: "Search", TPS: 2, Count: 6, Errors: 2, AvgMs: 200},
	})
	search := merged["Search"]
	if search == nil || search.TPS != 5 || search.Count != 10 || search.Errors != 3 || search.AvgMs != 160 {
		t.Fatalf("merged Search metric = %#v", search)
	}
}

func TestIntervalTransactionsFromAccumulatorsPrefersAdditiveSourceRates(t *testing.T) {
	metrics := intervalTransactionsFromAccumulators(map[string]*intervalTransactionAccumulator{
		"Search": {
			count: 3, totalDurationMs: 300, sourceTPS: 7, sourceWindows: 2,
		},
		"ErrorsOnly": {
			count: 1, errors: 1, totalDurationMs: 50, sourceWindows: 1,
		},
	}, 0.5)
	if search := metrics["Search"]; search == nil || search.TPS != 7 || search.Count != 3 || search.AvgMs != 100 {
		t.Fatalf("Search metric = %#v, want its additive source-window rate", search)
	}
	if errorsOnly := metrics["ErrorsOnly"]; errorsOnly == nil || errorsOnly.TPS != 0 || errorsOnly.Count != 1 || errorsOnly.Errors != 1 {
		t.Fatalf("ErrorsOnly metric = %#v, want an evidenced zero success rate", errorsOnly)
	}
}

func TestRunArchiveWriterScrubsIntervalTransactionNames(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "summary-name-scrub", Name: "Summary scrub"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("newRunArchiveWriter: %v", err)
	}
	unsafeName := "checkout token=abc123"
	if err := writer.RecordIntervalSummary(&LiveMetrics{
		Timestamp: time.Now().UTC(),
		Transactions: map[string]*TxMetrics{
			unsafeName: {Name: unsafeName, TPS: 1, Count: 1, AvgMs: 10},
		},
	}); err != nil {
		t.Fatalf("RecordIntervalSummary: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(payload), "abc123") {
		t.Fatalf("summary JSONL retained structured credential value: %s", payload)
	}
	var summary IntervalSummaryRecord
	if err := json.Unmarshal(payload, &summary); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantName := "checkout token=" + scrub.Redacted
	metric := summary.Transactions[wantName]
	if metric == nil || metric.Name != wantName {
		t.Fatalf("scrubbed summary transaction map = %#v, want key/name %q", summary.Transactions, wantName)
	}
}
