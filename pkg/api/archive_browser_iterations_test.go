package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func iterationEvent(transaction, temperature string, durationMs float64, success bool) TransactionEvent {
	event := TransactionEvent{ActionName: transaction, Group: "Observers", BusinessTransaction: true, DurationMs: durationMs, Success: success}
	if temperature != "" {
		event.PropertyDimensions = map[string]string{protocol.BrowserIterationDimension: temperature}
	}
	return event
}

func writeTransactionEvents(t *testing.T, events ...TransactionEvent) string {
	t.Helper()
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

// A transaction SLA rule judges warm browser iterations by default, every
// iteration with "all", and cold ones with "cold"; an untagged session (one
// that never runs warm) is judged by all but "cold".
func TestTransactionSLARulesJudgeWarmIterationsByDefault(t *testing.T) {
	root := writeTransactionEvents(t,
		iterationEvent("Open account", protocol.IterationCold, 5000, true),
		iterationEvent("Open account", protocol.IterationWarm, 900, true),
		iterationEvent("Open account", protocol.IterationWarm, 1100, true),
		iterationEvent("Open account", "", 1000, true),
	)
	manifest := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{
		{Transaction: "Open account", Metric: "max_duration_ms", Comparator: "lte", Threshold: 2000},
		{Transaction: "Open account", Metric: "max_duration_ms", Comparator: "lte", Threshold: 2000, Iterations: "warm"},
		{Transaction: "Open account", Metric: "max_duration_ms", Comparator: "lte", Threshold: 2000, Iterations: "all"},
		{Transaction: "Open account", Metric: "minimum_count", Comparator: "eq", Threshold: 1, Iterations: "cold"},
	}}}}
	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatal(err)
	}
	for index, want := range []struct {
		status  string
		matched int64
	}{{"pass", 3}, {"pass", 3}, {"fail", 4}, {"pass", 1}} {
		got := manifest.TransactionRuleResults[index]
		if got.Status != want.status || got.MatchedCount != want.matched {
			t.Errorf("rule %d = %s matched %d (%s), want %s matched %d", index, got.Status, got.MatchedCount, got.Message, want.status, want.matched)
		}
	}
	if err := ValidateTransactionSLARules([]TransactionSLARule{{Transaction: "x", Metric: "tps", Comparator: "gt", Iterations: "first"}}); err == nil || !strings.Contains(err.Error(), "iterations") {
		t.Fatalf("an unknown iterations value validated: %v", err)
	}
}

// The response-time SLA judges warm iterations: a cold one never counts.
func TestTransactionResponseTimeSLASkipsColdIterations(t *testing.T) {
	root := writeTransactionEvents(t,
		iterationEvent("Open account", protocol.IterationCold, 9000, true),
		iterationEvent("Open account", protocol.IterationWarm, 900, true),
		iterationEvent("Open account", "", 950, true),
	)
	manifest := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{
		GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(3000)},
		Groups:     []TestGroup{{Name: "Observers"}},
	}}
	if err := applyTransactionResponseTimeSLAs(root, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.TransactionResponseTimeSLAResults) != 1 {
		t.Fatalf("results = %#v", manifest.TransactionResponseTimeSLAResults)
	}
	if got := manifest.TransactionResponseTimeSLAResults[0]; got.Status != "pass" || got.TotalCount != 2 || got.MissedCount != 0 {
		t.Fatalf("result = %#v, want the warm and untagged samples judged", got)
	}
}

// The run report lists each transaction's cold and warm times side by side,
// in HTML, PDF text and CSV.
func TestArchiveReportShowsColdAndWarmIterations(t *testing.T) {
	rollup := newArchiveBrowserIterationAccumulator()
	for _, event := range []TransactionEvent{
		iterationEvent("Open account", protocol.IterationCold, 5000, true),
		iterationEvent("Open account", protocol.IterationCold, 7000, false),
		iterationEvent("Open account", protocol.IterationWarm, 900, true),
		iterationEvent("Open account", protocol.IterationWarm, 1100, true),
		iterationEvent("Open account", "", 1000, true),
		{ActionName: "click", Population: "action", PropertyDimensions: map[string]string{protocol.BrowserIterationDimension: protocol.IterationWarm}, Success: true},
	} {
		rollup.merge(event)
	}
	rows := rollup.result()
	if len(rows) != 1 || rows[0].Cold == nil || rows[0].Cold.Count != 1 || rows[0].Cold.Errors != 1 || rows[0].Cold.AvgMs != 5000 || rows[0].Warm == nil || rows[0].Warm.Count != 2 || rows[0].Warm.AvgMs != 1000 {
		t.Fatalf("rows = %+v (cold %+v, warm %+v)", rows, rows[0].Cold, rows[0].Warm)
	}

	report := consistencyMatrixReport()
	report.Overview.Browser = nil
	report.BrowserIterations = rows
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Cold and warm browser iterations") || !strings.Contains(string(html), "5000.0 ms") {
		t.Fatal("the HTML report does not show the cold and warm times")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "browser_iteration:cold") || !strings.Contains(csv.String(), "count=2;errors=0;avg_ms=1000") {
		t.Fatalf("CSV:\n%s", csv.String())
	}
	lines := []string{}
	buildReportSectionRegistry()["browser"].PDF(&runArchiveHTMLReportData{Report: report}, &lines)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "Open account: cold 1 passed, avg 5000.0 ms") {
		t.Fatalf("PDF lines:\n%s", text)
	}
}
