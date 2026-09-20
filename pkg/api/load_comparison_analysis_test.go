package api

import (
	"context"
	"math"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadComparisonQueryValidation(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"", true}, {"population=action&start_seconds=10&end_seconds=40", true},
		{"population=transaction", true}, {"population=unknown", true},
		{"population=other", false}, {"start_seconds=-1", false}, {"end_seconds=NaN", false},
		{"end_seconds=Inf", false}, {"start_seconds=20&end_seconds=10", false}, {"start_seconds=31536001", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			v, _ := url.ParseQuery(tc.query)
			_, err := parseLoadComparisonQuery(v)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestLoadComparisonDistributionsSeparateOutcomesAndMerge(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	runs := []loadComparisonRun{}
	for i, duration := range []float64{10, 1000} {
		id := []string{"a", "b"}[i]
		run := loadComparisonRunFixture(id, "release", "Release", base, []TransactionEvent{
			{Timestamp: base, ActionName: "Checkout", DurationMs: duration, Success: i == 0, Population: "action"},
		}, []IntervalSummaryRecord{loadComparisonSummary(base, 10, nil)})
		runs = append(runs, run)
	}
	result := buildLoadComparison("app", "transaction", "Checkout", runs)
	point := result.Releases[0].Points[0]
	if point.Latency == nil || point.Latency.Samples != 2 || point.Latency.P95Ms < 900 {
		t.Fatalf("pooled distribution = %+v", point.Latency)
	}
	if point.SuccessfulLatency == nil || point.SuccessfulLatency.AverageMs != 10 || point.FailedLatency == nil || point.FailedLatency.AverageMs != 1000 {
		t.Fatalf("outcomes = %+v", point)
	}
	if point.SuccessfulRate == nil || *point.SuccessfulRate != 0.5 || point.TPS != 1 || point.ObservedSeconds != 2 {
		t.Fatalf("rates = %+v", point)
	}
	if len(result.RunSeries) != 2 || len(point.RunIDs) != 2 {
		t.Fatalf("repeat provenance missing: %+v", result)
	}
}

func TestLoadComparisonIdleIntervalsAndWindow(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	run := loadComparisonRunFixture("a", "release", "Release", base, []TransactionEvent{
		{Timestamp: base.Add(time.Second), ActionName: "Checkout", DurationMs: 100, Success: true, Population: "action"},
		{Timestamp: base.Add(3 * time.Second), ActionName: "Checkout", DurationMs: 200, Success: false, Population: "action"},
		{Timestamp: base.Add(3 * time.Second), ActionName: "Checkout", DurationMs: 9999, Success: true, Population: "transaction"},
	}, []IntervalSummaryRecord{loadComparisonSummary(base.Add(time.Second), 10, nil), loadComparisonSummary(base.Add(2*time.Second), 10, nil), loadComparisonSummary(base.Add(3*time.Second), 10, nil)})
	run.Started = base
	result := buildLoadComparison("app", "transaction", "Checkout", []loadComparisonRun{run}, loadComparisonQuery{Population: "action"})
	point := result.Releases[0].Points[0]
	if point.Transactions != 2 || math.Abs(point.TPS-2.0/3) > 0.00001 || point.ObservedSeconds != 3 {
		t.Fatalf("idle intervals lost: %+v", point)
	}
	end := 3.0
	result = buildLoadComparison("app", "transaction", "Checkout", []loadComparisonRun{run}, loadComparisonQuery{Population: "action", StartSeconds: 1, EndSeconds: &end})
	point = result.Releases[0].Points[0]
	if point.Transactions != 1 || point.AvgResponseMs != 200 || point.ObservedSeconds != 2 {
		t.Fatalf("window = %+v", point)
	}
}

func TestLoadComparisonMissingGroupDenominator(t *testing.T) {
	base := time.Now()
	run := loadComparisonRunFixture("a", "r", "R", base, nil, []IntervalSummaryRecord{loadComparisonSummary(base, 10, map[string]*GroupLiveMetrics{"G": {Name: "G", VUsers: 10, Transactions: 0, Errors: 77, AvgResponseMs: 17}})})
	point := buildLoadComparison("app", "group", "G", []loadComparisonRun{run}).Releases[0].Points[0]
	if point.EvidenceStatus != "inconsistent_counts" || point.Latency != nil || point.SuccessfulRate != nil || point.AvgResponseMs != 0 {
		t.Fatalf("invented evidence: %+v", point)
	}
}

func TestLoadComparisonDistributionBudgetIsAtomic(t *testing.T) {
	interval := &transactionIntervalAccumulator{Distribution: &loadComparisonDistribution{}}
	interval.Distribution.add(TransactionEvent{DurationMs: 10, Success: true})
	aggregate := map[string]*loadComparisonTransactionAggregate{"A": {Intervals: map[transactionIntervalLoadKey]*transactionIntervalAccumulator{{Index: 0, Load: 10}: interval}}}
	budget := newLoadComparisonAggregateBudgetWithLimits(20, 20, loadComparisonDistributionBytes-1)
	if err := budget.reserveRun("transaction", nil, aggregate); err == nil {
		t.Fatal("distribution allocation was not charged")
	}
	if budget.cells != 0 || budget.distributionCells != 0 || len(budget.dimensions) != 0 {
		t.Fatal("failed reservation mutated request budget")
	}
}

func TestLoadComparisonAnalysisQueryContract(t *testing.T) {
	contracts, err := inferRouteContracts("server.go", []discoveredRoute{{Method: "GET", Path: "/api/v1/applications/{id}/load-comparison", Handler: "handleGetApplicationLoadComparison"}})
	if err != nil {
		t.Fatal(err)
	}
	queries := contracts["GET /api/v1/applications/{id}/load-comparison"].QueryParameters
	found := map[string]bool{}
	for _, query := range queries {
		found[query.Name] = true
	}
	for _, name := range []string{"population", "start_seconds", "end_seconds"} {
		if !found[name] {
			t.Errorf("missing query parameter %s", name)
		}
	}
}

func TestLoadComparisonLongRunSharesDistributionsAcrossIntervals(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	record := writeLoadComparisonArchiveFixture(t, "release", "long", "Long run", base)
	count := maxLoadComparisonDistributionCells + 10
	summaries := make([]IntervalSummaryRecord, count)
	events := make([]any, count)
	for i := range summaries {
		timestamp := base.Add(time.Duration(i+1) * time.Second)
		summaries[i] = loadComparisonSummary(timestamp, 10, nil)
		events[i] = TransactionEvent{Timestamp: timestamp, ActionName: "Checkout", DurationMs: 100, Success: true, Population: "action"}
	}
	writeJSONL(t, filepath.Join(record.ArchivePath, "transactions.jsonl"), events...)
	aggregates, err := streamLoadComparisonTransactionAggregatesWithQuery(context.Background(), record, summaries, 20, count*2, loadComparisonQuery{Population: "action"})
	if err != nil {
		t.Fatal(err)
	}
	budget := newLoadComparisonAggregateBudgetWithLimits(20, count*3, 10<<20)
	if err := budget.reserveRun("transaction", summaries, aggregates); err != nil {
		t.Fatal(err)
	}
	if budget.distributionCells != 1 {
		t.Fatalf("retained %d distributions for one transaction/load", budget.distributionCells)
	}
	run := loadComparisonRunFixture("long", "release", "Release", base, nil, summaries)
	run.Started = base
	run.TransactionStats = aggregates
	point := buildLoadComparison("app", "transaction", "Checkout", []loadComparisonRun{run}).Releases[0].Points[0]
	if point.Latency == nil || point.Latency.Samples != int64(count) || point.Latency.P95Ms != 100 || point.TPS != 1 {
		t.Fatalf("shared distribution double counted: %+v", point)
	}
	writeJSONL(t, filepath.Join(record.ArchivePath, "transactions.jsonl"), TransactionEvent{Timestamp: base.Add(time.Second), ActionName: "Checkout", DurationMs: -1})
	if _, err := streamLoadComparisonTransactionAggregates(context.Background(), record, summaries); err == nil {
		t.Fatal("corrupt negative duration accepted")
	}
}
