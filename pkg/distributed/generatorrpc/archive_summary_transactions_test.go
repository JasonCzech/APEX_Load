package generatorrpc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestGeneratorArchiveIntervalSummaryPersistsBoundedTransactionBreakdown(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("summary-transactions-generator", "summary-transactions-run", "Summary transactions")
	if err != nil {
		t.Fatalf("newArchiveRecorder: %v", err)
	}
	publishedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	batch := &pb.MetricsBatch{
		TestId:          "summary-transactions-run",
		TimestampUnixMs: publishedAt.UnixMilli(),
		WindowMs:        2_000,
		Stats:           &pb.GeneratorStats{ActiveVusers: 4},
		FleetMetrics:    &pb.MetricAggregate{Count: 10, Errors: 3, AvgMs: 125},
		TransactionMetrics: []*pb.MetricAggregate{
			{Name: "Login", Count: 4, Errors: 1, AvgMs: 100, Tps: 1.5},
			{Name: "Search", Count: 6, Errors: 2, AvgMs: 150, Tps: 2},
		},
	}
	(&Service{}).recordMetricsBatch(batch.GetTestId(), writer, batch)
	record, err := writer.Finalize("COMPLETE", publishedAt, "")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatalf("read interval summaries: %v", err)
	}
	var summary archiveIntervalSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode interval summary: %v", err)
	}
	if login := summary.Transactions["Login"]; login == nil || login.Name != "Login" || login.TPS != 1.5 || login.Count != 4 || login.Errors != 1 || login.AvgMs != 100 {
		t.Fatalf("archived Login metrics = %#v", login)
	}
	if search := summary.Transactions["Search"]; search == nil || search.Name != "Search" || search.TPS != 2 || search.Count != 6 || search.Errors != 2 || search.AvgMs != 150 {
		t.Fatalf("archived Search metrics = %#v", search)
	}
	parquetRows := toParquetSummaries([]archiveIntervalSummary{summary})
	if len(parquetRows) != 1 || !strings.Contains(parquetRows[0].TransactionsJSON, `"Login"`) || !strings.Contains(parquetRows[0].TransactionsJSON, `"Search"`) {
		t.Fatalf("parquet transaction breakdown = %#v", parquetRows)
	}
}

func TestArchiveSummaryTransactionsFromBatchIsDeterministicBoundedAndDisclosureSafe(t *testing.T) {
	build := func(reverse bool) []*pb.MetricAggregate {
		values := make([]*pb.MetricAggregate, maxArchiveIntervalSummaryTransactions+1)
		for offset := range values {
			index := offset
			if reverse {
				index = len(values) - 1 - offset
			}
			values[offset] = &pb.MetricAggregate{
				Name:   fmt.Sprintf("transaction-%03d", index),
				Count:  1,
				Errors: int64(index % 2),
				AvgMs:  float64(index),
				Tps:    1,
			}
		}
		return values
	}

	forward := archiveSummaryTransactionsFromBatch(build(false))
	reverse := archiveSummaryTransactionsFromBatch(build(true))
	forwardJSON, err := json.Marshal(forward)
	if err != nil {
		t.Fatal(err)
	}
	reverseJSON, err := json.Marshal(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if string(forwardJSON) != string(reverseJSON) {
		t.Fatalf("bounded transaction selection depends on input order")
	}
	if len(forward) != maxArchiveIntervalSummaryTransactions || forward["transaction-510"] == nil || forward["transaction-511"] != nil || forward["transaction-512"] != nil {
		t.Fatalf("bounded transaction names = %#v", forward)
	}
	overflow := forward[archiveIntervalSummaryTransactionOverflow]
	if overflow == nil || overflow.TPS != 2 || overflow.Count != 2 || overflow.Errors != 1 || overflow.AvgMs != 511.5 {
		t.Fatalf("overflow metrics = %#v", overflow)
	}

	unsafeName := "checkout token=abc123"
	safe := archiveSummaryTransactionsFromBatch([]*pb.MetricAggregate{{Name: unsafeName, Count: 1}})
	wantName := "checkout token=" + scrub.Redacted
	if safe[wantName] == nil || safe[wantName].Name != wantName {
		t.Fatalf("sanitized transaction map = %#v, want key/name %q", safe, wantName)
	}
}
