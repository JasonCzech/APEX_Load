package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func auraEvent(transaction string, actions ...map[string]any) TransactionEvent {
	list := make([]any, 0, len(actions))
	for _, action := range actions {
		list = append(list, action)
	}
	return TransactionEvent{SourceTransaction: transaction, ProtocolEvidence: map[string]any{"aura_actions": map[string]any{"requests": 1, "actions": list}}}
}

// The run report rolls a run's Aura actions up by transaction, most time
// first, counting errors; names past the run's limit are reported together.
func TestArchiveAuraActionRollup(t *testing.T) {
	rollup := newArchiveAuraActionAccumulator()
	find := map[string]any{"name": "AccountSearch.find", "apex": true, "duration_ms": 100.0, "state": "SUCCESS"}
	findFailed := map[string]any{"name": "AccountSearch.find", "apex": true, "duration_ms": 120.0, "state": "ERROR"}
	record := map[string]any{"name": "serviceComponent://RecordGvpController/ACTION$getRecord", "duration_ms": 50.0}
	rollup.merge(auraEvent("Search", find, record))
	rollup.merge(auraEvent("Search", findFailed))
	rollup.merge(auraEvent("", record))
	got := rollup.result()
	want := []AuraActionRollup{
		{Transaction: "", Name: "serviceComponent://RecordGvpController/ACTION$getRecord", Count: 1, TotalMs: 50},
		{Transaction: "Search", Name: "AccountSearch.find", Apex: true, Count: 2, TotalMs: 220, Errors: 1},
		{Transaction: "Search", Name: "serviceComponent://RecordGvpController/ACTION$getRecord", Count: 1, TotalMs: 50},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rollup = %+v\nwant %+v", got, want)
	}

	many := newArchiveAuraActionAccumulator()
	for i := 0; i <= maxAuraActionNames; i++ {
		many.merge(auraEvent("Load", map[string]any{"name": fmt.Sprintf("Ctrl.m%03d", i), "duration_ms": 1.0}))
	}
	if len(many.names) != maxAuraActionNames || many.rows[[2]string{"Load", otherAuraActions}] == nil {
		t.Fatalf("names = %d, other row = %v", len(many.names), many.rows[[2]string{"Load", otherAuraActions}])
	}
	if rows := many.result(); len(rows) != maxAuraActionsPerTransaction {
		t.Fatalf("a transaction lists %d actions", len(rows))
	}
}

// A run with Aura actions but no page metrics still reports them, in HTML and
// CSV.
func TestArchiveReportRendersAuraActions(t *testing.T) {
	report := consistencyMatrixReport()
	report.Overview.Browser = nil
	report.AuraActions = []AuraActionRollup{
		{Transaction: "Search", Name: "AccountSearch.find", Apex: true, Count: 2, TotalMs: 220, Errors: 1},
	}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Salesforce Aura actions by transaction") || !strings.Contains(string(html), "AccountSearch.find") {
		t.Fatal("the HTML report does not list the Aura actions")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "aura_actions:Search") || !strings.Contains(csv.String(), "calls=2;total_ms=220") {
		t.Fatalf("CSV:\n%s", csv.String())
	}
}
