package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
	"time"
)

func TestArchiveMetricPopulationsDoNotDoubleCount(t *testing.T) {
	groups := map[string]*archivePopulationAccumulator{"action": {}, "transaction": {}, "unknown": {}}
	start := time.Now()
	for _, event := range []TransactionEvent{
		{Population: "action", Timestamp: start, DurationMs: 10, Success: true},
		{Population: "action", Timestamp: start.Add(10 * time.Millisecond), DurationMs: 30, Success: false},
		{BusinessTransaction: true, Timestamp: start, DurationMs: 40, Success: false},
		{ActionName: "legacy without classification", HasDetail: false, Timestamp: start, DurationMs: 100, Success: true},
	} {
		groups[archiveEventPopulation(event)].add(event)
	}
	actions, transactions, unknown := groups["action"].finish(time.Second), groups["transaction"].finish(time.Second), groups["unknown"].finish(time.Second)
	if actions.Transactions != 2 || actions.Errors != 1 || actions.ErrorPct != 50 || actions.AvgResponseMs != 20 {
		t.Fatal(actions)
	}
	if transactions.Transactions != 1 || transactions.AvgResponseMs != 40 || unknown.Transactions != 1 {
		t.Fatal(transactions, unknown)
	}
	if actions.AverageRate == nil || *actions.AverageRate <= 0 || actions.P95Ms <= actions.P50Ms {
		t.Fatal(actions)
	}
}

func TestNamedTransactionSLAIgnoresRequestPopulation(t *testing.T) {
	report := &RunArchiveReport{
		Manifest:          &RunArchiveManifest{TerminationReason: "workload_completed", Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{P95LatencyMs: ptr(50.0)}}},
		Completeness:      RunArchiveCompletenessScore{Status: "complete", ScorePct: 100},
		MetricPopulations: map[string]ArchiveMetricPopulation{"action": {}},
		ByAction:          map[string]RunArchiveActionStats{"request": {Transactions: 10, P95Ms: 10}},
		ByTransaction:     map[string]RunArchiveActionStats{"checkout": {Transactions: 5, P95Ms: 100}},
	}
	if got := deriveReportVerdict(report); got.Status != reportVerdictFail {
		t.Fatalf("enclosing transaction breach was concealed: %+v", got)
	}
	report.ByTransaction = nil
	if got := deriveReportVerdict(report); got.Status != reportVerdictConditional {
		t.Fatalf("request success cannot establish transaction acceptance: %+v", got)
	}
}

func TestArchiveTerminationDoesNotGuessCompletion(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	for _, tc := range []struct{ id, status, reason, actor, want string }{
		{"unknown", "COMPLETE", "", "", "unknown"},
		{"natural", "COMPLETE", "workload_completed", "", "workload_completed"},
		{"stop", "COMPLETE", "workload_completed", "operator", "operator_stopped"},
		{"abort", "ABORTED", "", "", "aborted"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			writer, err := newRunArchiveWriter(&ManagedTest{ID: tc.id, Name: tc.id}, RunArchiveProvenance{StoppedBySubject: tc.actor})
			if err != nil {
				t.Fatal(err)
			}
			if tc.reason != "" {
				writer.SetTerminationReason(tc.reason)
			}
			if _, err := writer.Finalize(tc.status, time.Now(), "", false, false, nil); err != nil {
				t.Fatal(err)
			}
			if writer.manifest.TerminationReason != tc.want {
				t.Fatalf("termination=%s", writer.manifest.TerminationReason)
			}
		})
	}
}

func TestArchivePopulationReconciliationSurvivesReportRendering(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	start := time.Now()
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "populations", Name: "Checkout"}, RunArchiveProvenance{StartedAt: &start, Duration: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{ActionName: "same name", StartTime: start, Duration: 10 * time.Millisecond, Success: true, DependencyType: "http"},
		{ActionName: "same name", StartTime: start.Add(time.Second), Duration: 30 * time.Millisecond, Success: false, DependencyType: "http"},
		{ActionName: "same name", StartTime: start, Duration: 40 * time.Millisecond, Success: false, BusinessTransaction: true},
	} {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	writer.SetTerminationReason("workload_completed")
	record, err := writer.Finalize("COMPLETE", start.Add(2*time.Second), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overview.Transactions != 3 || report.ByRequest["same name"].Transactions != 2 || report.ByTransaction["same name"].Transactions != 1 {
		t.Fatalf("populations: %+v / %+v", report.ByRequest, report.ByTransaction)
	}
	for population, want := range map[string]float64{"action": 1, "transaction": 0.5, "unknown": 0} {
		rate := report.MetricPopulations[population].AverageRate
		if rate == nil || *rate != want {
			t.Fatalf("%s rate=%v, want %v over the shared two-second run", population, rate, want)
		}
	}
	report.ReleaseTrend = &RunArchiveReleaseTrendSummary{ReleaseID: "release-id", ReleaseName: "Checkout release", RunCount: 1}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Not assessed", "Requests / actions", "Named transactions", "Not provided", "Checkout release", `Execution <span class="mono">Finished</span>`} {
		if !strings.Contains(string(html), required) {
			t.Errorf("missing %s", required)
		}
	}
	if strings.Contains(string(html), "met its SLA targets") || strings.Contains(string(html), "Release Manager · Date") || strings.Contains(string(html), "Performance Engineer</") {
		t.Fatal("invented acceptance")
	}
}

func TestArchivePopulationRatesUseSharedElapsedWindow(t *testing.T) {
	start := time.Now()
	for _, tc := range []struct {
		name  string
		begin *time.Time
		end   time.Time
		valid bool
	}{
		{"paced run", &start, start.Add(30 * time.Second), true},
		{"missing start", nil, start.Add(30 * time.Second), false},
		{"missing end", &start, time.Time{}, false},
		{"zero window", &start, start, false},
		{"reversed window", &start, start.Add(-time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window := archivePopulationWindow(&RunArchiveManifest{Provenance: RunArchiveProvenance{StartedAt: tc.begin}, CompletedAt: tc.end})
			for _, offset := range []time.Duration{0, 10 * time.Second, 29 * time.Second} {
				acc := archivePopulationAccumulator{}
				acc.add(TransactionEvent{Timestamp: start.Add(offset), DurationMs: 80, Success: true})
				got := acc.finish(window)
				if !tc.valid {
					if got.AverageRate != nil {
						t.Fatalf("invented legacy rate: %v", *got.AverageRate)
					}
					continue
				}
				if got.AverageRate == nil || *got.AverageRate != 1.0/30 {
					t.Fatalf("rate=%v", got.AverageRate)
				}
			}
			empty := (&archivePopulationAccumulator{}).finish(window)
			if tc.valid && (empty.AverageRate == nil || *empty.AverageRate != 0) {
				t.Fatal("valid empty population must have zero rate")
			}
		})
	}
}
