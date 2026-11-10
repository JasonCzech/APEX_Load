package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The network-phase count leaves business-transaction rows out (they repeat
// their requests' time), so the rendered copy must say it covers requests.
func TestRenderedNetworkPhaseCopyCountsRequestsOnly(t *testing.T) {
	timestamp := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	record := writeArchiveTransactionsForTest(t, []TransactionEvent{
		{Timestamp: timestamp, TestID: "test-query", ActionName: "Checkout", Population: "action", DNSMs: 1, ConnectMs: 2, TLSMs: 3, TTFBMs: 40, Success: true},
		{Timestamp: timestamp.Add(time.Second), TestID: "test-query", ActionName: "Checkout", Population: "action", TTFBMs: 60, Success: true},
		{Timestamp: timestamp.Add(2 * time.Second), TestID: "test-query", ActionName: "Checkout", Population: "transaction", BusinessTransaction: true, DNSMs: 1, ConnectMs: 2, TLSMs: 3, TTFBMs: 100, Success: true},
	})
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(record.ArchivePath, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.NetworkPhases.Samples != 2 {
		t.Fatalf("network phase samples = %d, want the two request rows", report.NetworkPhases.Samples)
	}
	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReport returned error: %v", err)
	}
	text := string(html)
	if !strings.Contains(text, "measured requests (business-transaction rows are left out") {
		t.Fatal("network phase copy should say it counts request rows")
	}
	if strings.Contains(text, "measured requests and transactions") {
		t.Fatal("network phase copy still claims to count transactions")
	}
}
