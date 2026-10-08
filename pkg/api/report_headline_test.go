package api

import (
	"strings"
	"testing"
)

// The archive overview mixes actions with their enclosing transactions and
// takes its peak from successful transactions only; the dashboard KPI
// scorecard shows the default population instead. Exports must match it.
func headlineTestReport() *RunArchiveReport {
	report := samplePDFRunArchiveReport()
	report.Overview.P95Ms = 1538
	report.Overview.PeakTPS = 56.0
	report.Overview.ErrorPct = 10.2
	report.Overview.AvgResponseMs = 240.4
	report.MetricPopulations = map[string]ArchiveMetricPopulation{
		"action": {RunArchiveActionStats: RunArchiveActionStats{
			Transactions: 2991, Errors: 30, ErrorPct: 1.0, AvgResponseMs: 1006, P95Ms: 1533, PeakTPS: 61.2, MinResponseMs: 12, MaxResponseMs: 4100,
		}},
		"transaction": {RunArchiveActionStats: RunArchiveActionStats{Transactions: 610, Errors: 5, ErrorPct: 0.8, P95Ms: 1538, PeakTPS: 19.0}},
	}
	return report
}

func TestReportHeadlineUsesDashboardDefaultPopulation(t *testing.T) {
	headline := reportHeadline(headlineTestReport())
	if headline.Population != "action" || headline.P95Ms != 1533 || headline.PeakTPS != 61.2 || headline.ErrorPct != 1.0 {
		t.Fatalf("headline = %+v, want the requests/actions population", headline)
	}
	if headline.Label != "Requests / actions · whole run" {
		t.Fatalf("label = %q", headline.Label)
	}

	legacy := headlineTestReport()
	legacy.MetricPopulations = nil
	if got := reportHeadline(legacy); got.P95Ms != 1538 || !strings.Contains(got.Label, "unclassified") {
		t.Fatalf("legacy headline = %+v, want the mixed overview, labelled", got)
	}

	onlyTransactions := headlineTestReport()
	delete(onlyTransactions.MetricPopulations, "action")
	if got := reportHeadline(onlyTransactions); got.Population != "transaction" || got.PeakTPS != 19.0 {
		t.Fatalf("transaction-only headline = %+v", got)
	}
}

func TestExportHeadlinesMatchOnScreenKPIs(t *testing.T) {
	report := headlineTestReport()
	for _, profileID := range []string{"executive", "engineer"} {
		t.Run(profileID, func(t *testing.T) {
			html, err := renderRunArchiveHTMLReportWithProfile(report, profileID)
			if err != nil {
				t.Fatal(err)
			}
			body := string(html)
			for _, want := range []string{"Requests / actions · whole run", "61.2", "1.00%"} {
				if !strings.Contains(body, want) {
					t.Errorf("%s export is missing headline %q", profileID, want)
				}
			}
			for _, stale := range []string{"Peak successful transaction TPS", "Archive record p95", "Archive record error rate", "56.0 TPS", "10.20% archive-record errors"} {
				if strings.Contains(body, stale) {
					t.Errorf("%s export still shows the mixed archive headline %q", profileID, stale)
				}
			}
		})
	}
	if html, err := renderRunArchiveHTMLReportWithProfile(report, "executive"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(html), "1,533 ms") {
		t.Error("executive KPI p95 should read 1,533 ms, as on screen")
	}
}

func TestFormatReportMsMatchesDashboard(t *testing.T) {
	for value, want := range map[float64]string{0: "0.0 ms", 42.24: "42.2 ms", 100: "100 ms", 1533.4: "1,533 ms", 1234567: "1,234,567 ms"} {
		if got := formatReportMs(value); got != want {
			t.Errorf("formatReportMs(%v) = %q, want %q", value, got, want)
		}
	}
}
