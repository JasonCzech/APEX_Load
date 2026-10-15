package api

import (
	"bytes"
	"strings"
	"testing"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

func eptEvent(step string, eptMs, durationMs float64, success bool) TransactionEvent {
	event := TransactionEvent{ActionName: step, Population: "action", DurationMs: durationMs, Success: success}
	if eptMs != 0 {
		event.ProtocolEvidence = map[string]any{browserprotocol.SalesforceEPTEvidenceKey: map[string]any{"ms": eptMs}}
	}
	return event
}

// The run report sets each step's EPT readings beside the same steps' own
// times, in HTML, PDF text and CSV; failed steps, steps without a reading,
// transactions and unbelievable readings are left out.
func TestArchiveReportShowsSalesforceEPTCalibration(t *testing.T) {
	rollup := newArchiveSalesforceEPTAccumulator()
	open := eptEvent("Open account", 1000, 1400, true)
	transaction := eptEvent("Open account", 5000, 9000, true)
	transaction.Population, transaction.BusinessTransaction = "transaction", true
	for _, event := range []TransactionEvent{
		open,
		eptEvent("Open account", 2000, 2600, true),
		eptEvent("Open account", 3000, 3100, false),
		eptEvent("Open account", 0, 700, true),
		eptEvent("Open account", browserprotocol.MaxSalesforceEPTMs+1, 700, true),
		transaction,
		eptEvent("Search", 800, 750, true),
	} {
		rollup.merge(event)
	}
	rows := rollup.result()
	if len(rows) != 2 || rows[0].Step != "Open account" || rows[0].Samples != 2 || rows[0].AvgEPTMs != 1500 || rows[0].AvgStepMs != 2000 || rows[0].AvgDifferenceMs != 500 ||
		rows[1].Step != "Search" || rows[1].AvgDifferenceMs != -50 {
		t.Fatalf("rows = %+v", rows)
	}

	report := consistencyMatrixReport()
	report.Overview.Browser = nil
	report.SalesforceEPT = rows
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Salesforce EPT calibration") || !strings.Contains(string(html), "1500.0 ms") {
		t.Fatal("the HTML report does not show the EPT calibration")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "salesforce_ept") || !strings.Contains(csv.String(), "samples=2;avg_ept_ms=1500") {
		t.Fatalf("CSV:\n%s", csv.String())
	}
	lines := []string{}
	buildReportSectionRegistry()["browser"].PDF(&runArchiveHTMLReportData{Report: report}, &lines)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "Open account: 2 readings, EPT avg 1500.0 ms, step avg 2000.0 ms, difference +500.0 ms") {
		t.Fatalf("PDF lines:\n%s", text)
	}
}
