package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBuildRunArchiveReportBreaksPopulationsDownByGroup(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		TestID:                "test-group-breakdown",
		TestName:              "Group Breakdown Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	event := func(group, name, population string, durationMs float64, success bool) TransactionEvent {
		return TransactionEvent{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, Group: group, ActionName: name, Population: population, DurationMs: durationMs, Success: success}
	}
	transactions := []TransactionEvent{
		event("Browse", "Login", "transaction", 100, true),
		event("Browse", "Login", "transaction", 300, false),
		event("Browse", "Search", "transaction", 50, true),
		event("Buy", "Login", "transaction", 200, true),
		event("Buy", "GET /cart", "action", 20, true),
	}
	txFile, err := os.Create(filepath.Join(tempDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("create transactions: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range transactions {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("encode transaction: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("close transactions: %v", err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(tempDir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: tempDir, ManifestPath: filepath.Join(tempDir, "manifest.json")})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if len(report.GroupBreakdownTruncated) != 0 {
		t.Fatalf("breakdown truncated for %v, want complete", report.GroupBreakdownTruncated)
	}
	named := report.GroupBreakdown["transaction"]
	browse := named["Browse"]
	if browse.Totals.Transactions != 3 || browse.Totals.Errors != 1 {
		t.Fatalf("Browse totals = %#v, want three transactions, one error", browse.Totals)
	}
	login := browse.Items["Login"]
	if login.Transactions != 2 || login.Errors != 1 || login.AvgResponseMs != 200 || login.ErrorPct != 50 {
		t.Fatalf("Browse/Login = %#v, want two transactions, one error, avg 200", login)
	}
	// The same transaction in another group is its own row.
	if buy := named["Buy"]; buy.Totals.Transactions != 1 || buy.Items["Login"].AvgResponseMs != 200 || len(buy.Items) != 1 {
		t.Fatalf("Buy = %#v, want only its own Login", buy)
	}
	// Requests stay in their own population.
	if requests := report.GroupBreakdown["action"]["Buy"]; requests.Items["GET /cart"].Transactions != 1 {
		t.Fatalf("action breakdown = %#v, want the Buy request", report.GroupBreakdown["action"])
	}
}

func TestArchiveGroupBreakdownTruncatesInsteadOfFailing(t *testing.T) {
	acc := newArchiveGroupBreakdownAccumulator(3)
	acc.add("transaction", TransactionEvent{Group: "A", ActionName: "one", DurationMs: 1, Success: true})
	acc.add("transaction", TransactionEvent{Group: "A", ActionName: "two", DurationMs: 1, Success: true})
	// The budget is spent: a new group needs two cells, a new name one.
	acc.add("transaction", TransactionEvent{Group: "B", ActionName: "one", DurationMs: 1, Success: true})
	acc.add("transaction", TransactionEvent{Group: "A", ActionName: "three", DurationMs: 9, Success: false})
	// Known pairs still count.
	acc.add("transaction", TransactionEvent{Group: "A", ActionName: "one", DurationMs: 3, Success: true})
	// Each population has its own budget.
	acc.add("action", TransactionEvent{Group: "B", ActionName: "GET /", DurationMs: 1, Success: true})
	result, truncated := acc.result(0)
	if len(truncated) != 1 || truncated[0] != "transaction" {
		t.Fatalf("truncated = %v, want [transaction]", truncated)
	}
	if _, ok := result["transaction"]["B"]; ok {
		t.Fatal("group B kept past the limit")
	}
	a := result["transaction"]["A"]
	if _, ok := a.Items["three"]; ok {
		t.Fatal("name three kept past the limit")
	}
	// A dropped name still counts in its group's totals.
	if a.Totals.Transactions != 4 || a.Totals.Errors != 1 || a.Items["one"].Transactions != 2 {
		t.Fatalf("group A = %#v, want four totals with one error and two for one", a)
	}
	if result["action"]["B"].Items["GET /"].Transactions != 1 {
		t.Fatalf("action breakdown = %#v, want its own budget", result["action"])
	}
}
