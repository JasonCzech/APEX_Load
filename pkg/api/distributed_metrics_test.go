package api

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

func successTransactions(group string, n int) []generatorrpc.TransactionMetric {
	txs := make([]generatorrpc.TransactionMetric, 0, n)
	for i := 0; i < n; i++ {
		txs = append(txs, generatorrpc.TransactionMetric{Name: "tx", Group: group, DurationMs: 100, Success: true})
	}
	return txs
}

func transactionsWithErrors(group string, successes, errors int) []generatorrpc.TransactionMetric {
	transactions := successTransactions(group, successes)
	for index := 0; index < errors; index++ {
		transactions = append(transactions, generatorrpc.TransactionMetric{
			Name:         "tx",
			Group:        group,
			DurationMs:   100,
			Success:      false,
			StatusCode:   500,
			ErrorMessage: "failed",
		})
	}
	return transactions
}

func requireTransactionAggregate(t *testing.T, batch generatorrpc.MetricsBatch, name string) generatorrpc.MetricAggregate {
	t.Helper()
	for _, aggregate := range batch.TransactionMetrics {
		if aggregate.Name == name {
			return aggregate
		}
	}
	t.Fatalf("transaction aggregate %q not found in %#v", name, batch.TransactionMetrics)
	return generatorrpc.MetricAggregate{}
}

func requireGroupAggregate(t *testing.T, batch generatorrpc.MetricsBatch, name string) generatorrpc.MetricAggregate {
	t.Helper()
	for _, group := range batch.Groups {
		if group.Name == name && group.Metrics != nil {
			return *group.Metrics
		}
	}
	t.Fatalf("group aggregate %q not found in %#v", name, batch.Groups)
	return generatorrpc.MetricAggregate{}
}

// TPS is a per-second rate that is additive across generators: each generator's
// success count is divided by its OWN window before summing.
func TestAggregateDistributedMetricsBatchesTPSIsSumOfRates(t *testing.T) {
	tests := []struct {
		name      string
		batches   map[string]generatorrpc.MetricsBatch
		wantFleet float64
		wantGroup float64
	}{
		{
			name: "heterogeneous windows sum as rates",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 10)},
				"gen-b": {TestID: "t", WindowMs: 2000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 20)},
				"gen-c": {TestID: "t", WindowMs: 500, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 30)},
			},
			// 10/1 + 20/2 + 30/0.5 = 10 + 10 + 60
			wantFleet: 80,
			wantGroup: 80,
		},
		{
			name: "window_ms=0 falls back to 1s (mixed-version fleet, no div-by-zero)",
			batches: map[string]generatorrpc.MetricsBatch{
				"old": {TestID: "t", WindowMs: 0, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 5)},
				"new": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 10)},
			},
			wantFleet: 15,
			wantGroup: 15,
		},
		{
			name: "negative window also falls back to 1s",
			batches: map[string]generatorrpc.MetricsBatch{
				"old": {TestID: "t", WindowMs: -500, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 7)},
			},
			wantFleet: 7,
			wantGroup: 7,
		},
		{
			name: "coalesced 2x window halves the rate vs a naive count",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen": {TestID: "t", WindowMs: 2000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 20)},
			},
			wantFleet: 10,
			wantGroup: 10,
		},
		{
			name: "per-group rate is additive across generators",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 6)},
				"gen-b": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 4)},
			},
			wantFleet: 10,
			wantGroup: 10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", tc.batches)
			if fleetTPS != tc.wantFleet {
				t.Fatalf("fleet TPS = %v, want %v", fleetTPS, tc.wantFleet)
			}
			if combined.FleetMetrics == nil || combined.FleetMetrics.TPS != tc.wantFleet {
				t.Fatalf("fleet aggregate = %#v, want TPS %v", combined.FleetMetrics, tc.wantFleet)
			}
			if aggregate := requireTransactionAggregate(t, combined, "tx"); aggregate.TPS != tc.wantFleet {
				t.Fatalf("transaction TPS = %v, want %v", aggregate.TPS, tc.wantFleet)
			}
			if groupTPS["A"] != tc.wantGroup {
				t.Fatalf("group A TPS = %v, want %v", groupTPS["A"], tc.wantGroup)
			}
		})
	}
}

// buildLiveMetricsFromDistributedBatch must surface the windowed fleet rate on
// live.TPS and resolve per-group rates onto configured group names.
func TestBuildLiveMetricsFromDistributedBatchTPSIsRate(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 2}}}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 6)},
		"gen-b": {TestID: "t", WindowMs: 2000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 8)},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.TPS != 10 {
		t.Fatalf("live TPS = %v, want 10 (6/1s + 8/2s)", live.TPS)
	}
	if live.TotalTx != 14 {
		t.Fatalf("live TotalTx = %d, want 14", live.TotalTx)
	}
	group := live.GroupMetrics["A"]
	if group == nil || group.TPS != 10 {
		t.Fatalf("group A TPS = %#v, want 10", group)
	}
	if tx := live.Transactions["tx"]; tx == nil || tx.TPS != 10 {
		t.Fatalf("transaction tx TPS = %#v, want 10", tx)
	}
}

func TestAggregateDistributedMetricsBatchesMergesAggregateAndRawTransactionTPS(t *testing.T) {
	// The new generator retains six bounded raw exemplars in addition to its
	// authoritative aggregate. Only the old generator's raw records should be
	// used as fallback; including the new exemplars would add another 3 TPS.
	batches := map[string]generatorrpc.MetricsBatch{
		"new": {
			TestID:       "t",
			WindowMs:     2000,
			Transactions: successTransactions("A", 6),
			FleetMetrics: &generatorrpc.MetricAggregate{Name: "fleet", Count: 20, Errors: 2, TPS: 9},
			TransactionMetrics: []generatorrpc.MetricAggregate{{
				Name: "tx", Count: 20, Errors: 2, AvgMs: 100, TPS: 9,
			}},
			Groups: []generatorrpc.GroupMetric{{
				Name:         "A",
				ActiveVUsers: 2,
				Metrics:      &generatorrpc.MetricAggregate{Name: "A", Count: 20, Errors: 2, AvgMs: 100, TPS: 9},
			}},
		},
		"old": {
			TestID:       "t",
			WindowMs:     1000,
			Transactions: transactionsWithErrors("A", 3, 1),
			Groups:       []generatorrpc.GroupMetric{{Name: "A", ActiveVUsers: 1}},
		},
	}

	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	aggregate := requireTransactionAggregate(t, combined, "tx")
	if aggregate.TPS != 12 {
		t.Fatalf("combined transaction TPS = %v, want 12 (9 aggregate + 3 raw fallback)", aggregate.TPS)
	}
	if aggregate.Count != 24 || aggregate.Errors != 3 {
		t.Fatalf("combined transaction counts = %d/%d, want 24/3", aggregate.Count, aggregate.Errors)
	}
	if combined.FleetMetrics == nil || combined.FleetMetrics.Count != 24 || combined.FleetMetrics.Errors != 3 || combined.FleetMetrics.TPS != 12 {
		t.Fatalf("combined fleet aggregate = %#v, want count/errors/TPS 24/3/12", combined.FleetMetrics)
	}
	groupAggregate := requireGroupAggregate(t, combined, "A")
	if groupAggregate.Count != 24 || groupAggregate.Errors != 3 || groupAggregate.TPS != 12 {
		t.Fatalf("combined group aggregate = %#v, want count/errors/TPS 24/3/12", groupAggregate)
	}
	if fleetTPS != 12 || groupTPS["A"] != 12 {
		t.Fatalf("fleet/group TPS = %v/%v, want 12/12", fleetTPS, groupTPS["A"])
	}

	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 2}}}, combined, fleetTPS, groupTPS)
	if live.TotalTx != 21 || live.TotalErrors != 3 || live.TPS != 12 || live.ErrorPct != 12.5 {
		t.Fatalf("live fleet metrics = %#v, want successes/errors/TPS/errorPct 21/3/12/12.5", live)
	}
	if tx := live.Transactions["tx"]; tx == nil || tx.Count != 24 || tx.Errors != 3 || tx.TPS != 12 {
		t.Fatalf("live transaction metrics = %#v, want count/errors/TPS 24/3/12", tx)
	}
	if group := live.GroupMetrics["A"]; group == nil || group.Transactions != 21 || group.Errors != 3 || group.TPS != 12 {
		t.Fatalf("live group metrics = %#v, want successes/errors/TPS 21/3/12", group)
	}
}

func TestAggregateDistributedMetricsBatchesFallsBackPerMissingMetricFamily(t *testing.T) {
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"transitional": {
			TestID:   "t",
			WindowMs: 2000,
			Transactions: []generatorrpc.TransactionMetric{{
				Name:             "Checkout",
				Group:            "buyers",
				DependencyType:   "database",
				DependencyTarget: "orders-db",
				SOAPOperation:    "SubmitOrder",
				DurationMs:       120,
				Success:          true,
			}},
			FleetMetrics: &generatorrpc.MetricAggregate{Name: "fleet", Count: 8, AvgMs: 120, TPS: 4},
			TransactionMetrics: []generatorrpc.MetricAggregate{{
				Name: "Checkout", Count: 8, AvgMs: 120, TPS: 4,
			}},
			// Older mixed-capability senders can publish transaction aggregates and
			// group gauges without the later dependency/SOAP/group aggregates.
			Groups: []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 2}},
		},
	})

	if fleetTPS != 4 || combined.FleetMetrics == nil || combined.FleetMetrics.Count != 8 {
		t.Fatalf("fleet aggregate/TPS = %#v/%v, want authoritative count/TPS 8/4", combined.FleetMetrics, fleetTPS)
	}
	transaction := requireTransactionAggregate(t, combined, "Checkout")
	if transaction.Count != 8 || transaction.TPS != 4 {
		t.Fatalf("transaction aggregate = %#v, raw exemplar must not double count authoritative transaction metrics", transaction)
	}
	if len(combined.DependencyMetrics) != 1 || combined.DependencyMetrics[0].Name != "orders-db" || combined.DependencyMetrics[0].Count != 1 || combined.DependencyMetrics[0].TPS != 0.5 {
		t.Fatalf("dependency fallback = %#v, want one reconstructed orders-db sample", combined.DependencyMetrics)
	}
	if len(combined.SOAPMetrics) != 1 || combined.SOAPMetrics[0].Name != "SubmitOrder" || combined.SOAPMetrics[0].Count != 1 || combined.SOAPMetrics[0].TPS != 0.5 {
		t.Fatalf("SOAP fallback = %#v, want one reconstructed SubmitOrder sample", combined.SOAPMetrics)
	}
	group := requireGroupAggregate(t, combined, "buyers")
	if group.Count != 1 || group.TPS != 0.5 || groupTPS["buyers"] != 0.5 {
		t.Fatalf("group fallback/TPS = %#v/%v, want one reconstructed sample at 0.5 TPS", group, groupTPS["buyers"])
	}
}

func TestAggregateDistributedMetricsBatchesKeepsModernOverflowAggregateAuthoritative(t *testing.T) {
	const (
		exactSeries = 255
		totalSeries = 300
	)
	raw := make([]generatorrpc.TransactionMetric, 0, totalSeries)
	aggregates := make([]generatorrpc.MetricAggregate, 0, exactSeries+1)
	for index := 0; index < totalSeries; index++ {
		name := fmt.Sprintf("tx-%03d", index)
		raw = append(raw, generatorrpc.TransactionMetric{Name: name, Group: "A", DurationMs: 100, Success: true})
		if index < exactSeries {
			aggregates = append(aggregates, generatorrpc.MetricAggregate{Name: name, Count: 1, AvgMs: 100, TPS: 1})
		}
	}
	aggregates = append(aggregates, generatorrpc.MetricAggregate{
		Name: "[other]", Count: totalSeries - exactSeries, AvgMs: 100, TPS: totalSeries - exactSeries,
	})

	combined, fleetTPS, _ := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"modern": {
			TestID:             "t",
			WindowMs:           1000,
			Transactions:       raw,
			FleetMetrics:       &generatorrpc.MetricAggregate{Name: "fleet", Count: totalSeries, AvgMs: 100, TPS: totalSeries},
			TransactionMetrics: aggregates,
		},
	})

	if fleetTPS != totalSeries || len(combined.TransactionMetrics) != exactSeries+1 {
		t.Fatalf("fleet TPS/transaction series = %v/%d, want %d/%d", fleetTPS, len(combined.TransactionMetrics), totalSeries, exactSeries+1)
	}
	var totalCount int64
	totalTPS := 0.0
	for _, aggregate := range combined.TransactionMetrics {
		totalCount += aggregate.Count
		totalTPS += aggregate.TPS
		if aggregate.Name == "tx-255" {
			t.Fatal("raw name folded into [other] was incorrectly restored as a second series")
		}
	}
	if totalCount != totalSeries || totalTPS != totalSeries {
		t.Fatalf("transaction totals = count %d TPS %v, want %d/%d", totalCount, totalTPS, totalSeries, totalSeries)
	}
	other := requireTransactionAggregate(t, combined, "[other]")
	if other.Count != totalSeries-exactSeries || other.TPS != totalSeries-exactSeries {
		t.Fatalf("overflow aggregate = %#v, want count/TPS %d", other, totalSeries-exactSeries)
	}
}

func TestAggregateDistributedMetricsBatchesBoundsLegacyRawTransactionSeries(t *testing.T) {
	const totalSeries = 300
	raw := make([]generatorrpc.TransactionMetric, 0, totalSeries)
	for index := 0; index < totalSeries; index++ {
		raw = append(raw, generatorrpc.TransactionMetric{
			Name:       fmt.Sprintf("legacy-%03d", index),
			Group:      "A",
			DurationMs: 100,
			Success:    true,
		})
	}

	combined, fleetTPS, _ := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"legacy": {TestID: "t", WindowMs: 1000, Transactions: raw},
	})
	if len(combined.TransactionMetrics) != maxDistributedMetricSeries {
		t.Fatalf("legacy transaction series = %d, want bound %d", len(combined.TransactionMetrics), maxDistributedMetricSeries)
	}
	var count int64
	totalTPS := 0.0
	for _, aggregate := range combined.TransactionMetrics {
		count += aggregate.Count
		totalTPS += aggregate.TPS
	}
	if count != totalSeries || totalTPS != totalSeries || fleetTPS != totalSeries {
		t.Fatalf("legacy totals = count/TPS/fleetTPS %d/%v/%v, want %d/%d/%d", count, totalTPS, fleetTPS, totalSeries, totalSeries, totalSeries)
	}
	overflow := requireTransactionAggregate(t, combined, distributedOverflowSeries)
	if overflow.Count != totalSeries-(maxDistributedMetricSeries-1) || overflow.TPS != totalSeries-(maxDistributedMetricSeries-1) {
		t.Fatalf("legacy overflow aggregate = %#v", overflow)
	}
}

func TestAggregateDistributedMetricsBatchesBoundsDisjointModernGeneratorSeries(t *testing.T) {
	makeAggregate := func(name string, count int64, avgMs float64) generatorrpc.MetricAggregate {
		digest := engine.NewTDigest(200)
		for sample := int64(0); sample < count; sample++ {
			digest.Add(avgMs * float64(time.Millisecond))
		}
		snapshot := digest.Snapshot()
		return generatorrpc.MetricAggregate{
			Name:           name,
			Count:          count,
			AvgMs:          avgMs,
			AvgDNSMs:       avgMs / 10,
			BytesSent:      count * 2,
			BytesReceived:  count * 3,
			TPS:            float64(count),
			DurationDigest: &snapshot,
		}
	}
	buildSeries := func(start, end int, overflowCount int64, overflowAvgMs float64) []generatorrpc.MetricAggregate {
		series := make([]generatorrpc.MetricAggregate, 0, end-start+2)
		for index := start; index <= end; index++ {
			series = append(series, makeAggregate(fmt.Sprintf("series-%03d", index), 1, float64(index+1)))
		}
		if overflowCount > 0 {
			series = append(series, makeAggregate(distributedOverflowSeries, overflowCount, overflowAvgMs))
		}
		return series
	}

	first := buildSeries(0, 199, 5, 1_000)
	second := buildSeries(200, 399, 7, 2_000)
	all := append(append([]generatorrpc.MetricAggregate(nil), first...), second...)
	combined, _, _ := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"generator-a": {
			TestID:                "t",
			WindowMs:              1_000,
			FleetMetrics:          &generatorrpc.MetricAggregate{Name: "fleet", Count: 205, TPS: 205},
			TransactionMetrics:    first,
			DependencyMetrics:     first,
			SOAPMetrics:           first,
			NetworkProfileMetrics: first,
		},
		"generator-b": {
			TestID:                "t",
			WindowMs:              1_000,
			FleetMetrics:          &generatorrpc.MetricAggregate{Name: "fleet", Count: 207, TPS: 207},
			TransactionMetrics:    second,
			DependencyMetrics:     second,
			SOAPMetrics:           second,
			NetworkProfileMetrics: second,
		},
	})

	assertBounded := func(label string, got []generatorrpc.MetricAggregate) {
		t.Helper()
		if len(got) != maxDistributedMetricSeries {
			t.Fatalf("%s series = %d, want global bound %d", label, len(got), maxDistributedMetricSeries)
		}
		byName := make(map[string]generatorrpc.MetricAggregate, len(got))
		var gotCount, gotBytesSent, gotBytesReceived, gotDigestCount int64
		var gotTPS, gotWeightedAvg float64
		for index, aggregate := range got {
			if index > 0 && got[index-1].Name > aggregate.Name {
				t.Fatalf("%s series order is not deterministic: %q before %q", label, got[index-1].Name, aggregate.Name)
			}
			byName[aggregate.Name] = aggregate
			gotCount += aggregate.Count
			gotTPS += aggregate.TPS
			gotWeightedAvg += aggregate.AvgMs * float64(aggregate.Count)
			gotBytesSent += aggregate.BytesSent
			gotBytesReceived += aggregate.BytesReceived
			if aggregate.DurationDigest == nil {
				t.Fatalf("%s aggregate %q lost its complete duration digest", label, aggregate.Name)
			}
			gotDigestCount += aggregate.DurationDigest.Count
		}

		var wantCount, wantBytesSent, wantBytesReceived, wantDigestCount int64
		var wantTPS, wantWeightedAvg float64
		for _, aggregate := range all {
			wantCount += aggregate.Count
			wantTPS += aggregate.TPS
			wantWeightedAvg += aggregate.AvgMs * float64(aggregate.Count)
			wantBytesSent += aggregate.BytesSent
			wantBytesReceived += aggregate.BytesReceived
			wantDigestCount += aggregate.DurationDigest.Count
		}
		if gotCount != wantCount || gotTPS != wantTPS || gotBytesSent != wantBytesSent || gotBytesReceived != wantBytesReceived || gotDigestCount != wantDigestCount || math.Abs(gotWeightedAvg-wantWeightedAvg) > 1e-9 {
			t.Fatalf("%s totals = count %d TPS %v weighted latency %v bytes %d/%d digest %d; want %d/%v/%v/%d/%d/%d", label, gotCount, gotTPS, gotWeightedAvg, gotBytesSent, gotBytesReceived, gotDigestCount, wantCount, wantTPS, wantWeightedAvg, wantBytesSent, wantBytesReceived, wantDigestCount)
		}
		if byName["series-000"].Count != 1 || byName["series-254"].Count != 1 {
			t.Fatalf("%s did not retain the deterministic lexical prefix", label)
		}
		if _, exists := byName["series-255"]; exists {
			t.Fatalf("%s retained series-255 instead of folding it into overflow", label)
		}
		overflow := byName[distributedOverflowSeries]
		if overflow.Count != 157 || overflow.TPS != 157 || overflow.DurationDigest == nil || overflow.DurationDigest.Count != 157 {
			t.Fatalf("%s overflow = %#v, want existing 12 plus 145 omitted series exactly once", label, overflow)
		}
	}

	assertBounded("transactions", combined.TransactionMetrics)
	assertBounded("dependencies", combined.DependencyMetrics)
	assertBounded("SOAP", combined.SOAPMetrics)
	assertBounded("network profiles", combined.NetworkProfileMetrics)

	boundaryFirst := buildSeries(0, 127, 0, 0)
	boundarySecond := buildSeries(128, 255, 0, 0)
	boundary, _, _ := aggregateDistributedMetricsBatches("boundary", map[string]generatorrpc.MetricsBatch{
		"generator-a": {TestID: "boundary", TransactionMetrics: boundaryFirst},
		"generator-b": {TestID: "boundary", TransactionMetrics: boundarySecond},
	})
	if len(boundary.TransactionMetrics) != maxDistributedMetricSeries || boundary.TransactionMetrics[0].Name != distributedOverflowSeries || boundary.TransactionMetrics[0].Count != 1 {
		t.Fatalf("exact-boundary series = %#v, want 255 exact names plus one overflow sample", boundary.TransactionMetrics)
	}
	for _, aggregate := range boundary.TransactionMetrics {
		if aggregate.Name == "series-255" {
			t.Fatal("exact-boundary lexical tail was not folded into overflow")
		}
	}
}

func TestAggregateDistributedMetricsBatchesAllErrorTPSAndCountsAreClamped(t *testing.T) {
	tests := []struct {
		name       string
		count      int64
		errors     int64
		inputTPS   float64
		wantErrors int64
	}{
		{name: "all errors remains zero TPS", count: 5, errors: 5, inputTPS: 0, wantErrors: 5},
		{name: "errors above attempts cannot create negative successes", count: 2, errors: 5, inputTPS: 99, wantErrors: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
				"generator": {
					TestID:   "t",
					WindowMs: 1000,
					FleetMetrics: &generatorrpc.MetricAggregate{
						Name: "fleet", Count: tc.count, Errors: tc.errors, TPS: tc.inputTPS,
					},
					TransactionMetrics: []generatorrpc.MetricAggregate{{
						Name: "tx", Count: tc.count, Errors: tc.errors, TPS: tc.inputTPS,
					}},
				},
			})
			if fleetTPS != 0 || combined.FleetMetrics == nil || combined.FleetMetrics.TPS != 0 || combined.FleetMetrics.Errors != tc.wantErrors {
				t.Fatalf("fleet aggregate/TPS = %#v/%v, want errors %d and zero TPS", combined.FleetMetrics, fleetTPS, tc.wantErrors)
			}
			transaction := requireTransactionAggregate(t, combined, "tx")
			if transaction.TPS != 0 || transaction.Errors != tc.wantErrors {
				t.Fatalf("transaction aggregate = %#v, want errors %d and zero TPS", transaction, tc.wantErrors)
			}
			live := buildLiveMetricsFromDistributedBatch(&ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 1}}}, combined, fleetTPS, groupTPS)
			if live.TotalTx != 0 || live.TotalErrors != tc.wantErrors || live.TPS != 0 {
				t.Fatalf("live metrics = %#v, want successes/errors/TPS 0/%d/0", live, tc.wantErrors)
			}
		})
	}
}

func TestAggregateDistributedMetricsBatchesKeepsExactRawFallbackPercentiles(t *testing.T) {
	combined, _, _ := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"legacy": {
			TestID:   "t",
			WindowMs: 1000,
			Transactions: []generatorrpc.TransactionMetric{
				{Name: "tx", DurationMs: 100, Success: true},
				{Name: "tx", DurationMs: 200, Success: true},
				{Name: "tx", DurationMs: 300, Success: false},
			},
		},
	})

	if combined.FleetMetrics == nil || combined.FleetMetrics.P50Ms != 200 || combined.FleetMetrics.P95Ms != 290 {
		t.Fatalf("raw fleet percentiles = %#v, want exact interpolated p50/p95 200/290", combined.FleetMetrics)
	}
	transaction := requireTransactionAggregate(t, combined, "tx")
	if transaction.P50Ms != 200 || transaction.P95Ms != 290 {
		t.Fatalf("raw transaction percentiles = %#v, want exact interpolated p50/p95 200/290", transaction)
	}
}

func TestMergeGeneratorMetricAggregatesRequiresCompleteDigestCoverage(t *testing.T) {
	digest := engine.NewTDigest(200)
	digest.Add(100 * float64(time.Millisecond))
	digest.Add(100 * float64(time.Millisecond))
	digestSnapshot := digest.Snapshot()

	merged := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{
		{
			Name: "tx", Count: 2, AvgMs: 100,
			P50Ms: 100, P90Ms: 100, P95Ms: 100, P99Ms: 100,
			DurationDigest: &digestSnapshot,
		},
		{
			Name: "tx", Count: 8, AvgMs: 1000,
			P50Ms: 1000, P90Ms: 1000, P95Ms: 1000, P99Ms: 1000,
		},
	})

	if merged == nil || merged.Count != 10 {
		t.Fatalf("merged aggregate = %#v, want count 10", merged)
	}
	// With incomplete distribution evidence, use the existing all-input scalar
	// fallback. Computing from the two digest-bearing samples alone would return
	// 100ms and silently discard 80% of the fleet.
	if merged.P50Ms != 820 || merged.P90Ms != 820 || merged.P95Ms != 820 || merged.P99Ms != 820 {
		t.Fatalf("merged percentiles = %#v, want all-input weighted fallback at 820ms", merged)
	}
	if merged.DurationDigest != nil {
		t.Fatalf("incomplete merged digest was retained: %#v", merged.DurationDigest)
	}
}

func TestAggregateDistributedMetricsBatchesFallsBackWhenFleetAggregateLacksTransactions(t *testing.T) {
	batches := map[string]generatorrpc.MetricsBatch{
		"old": {
			TestID:       "t",
			WindowMs:     2000,
			Transactions: successTransactions("A", 8),
			// Transitional generators can publish fleet aggregates without the
			// subsequently-added per-transaction aggregate field.
			FleetMetrics: &generatorrpc.MetricAggregate{Name: "fleet", Count: 8, TPS: 4},
		},
	}

	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	aggregate := requireTransactionAggregate(t, combined, "tx")
	if aggregate.Count != 8 || aggregate.TPS != 4 {
		t.Fatalf("raw fallback aggregate = %#v, want count 8 and TPS 4", aggregate)
	}

	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 1}}}, combined, fleetTPS, groupTPS)
	if tx := live.Transactions["tx"]; tx == nil || tx.Count != 8 || tx.TPS != 4 {
		t.Fatalf("live raw fallback transaction = %#v, want count 8 and TPS 4", tx)
	}
}

func TestBuildLiveMetricsFromDistributedBatchUsesUncappedAggregateCounts(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 20}}}
	digest := engine.NewTDigest(200)
	for i := 0; i < 2000; i++ {
		digest.Add(100 * float64(time.Millisecond))
	}
	digestSnapshot := digest.Snapshot()
	batches := map[string]generatorrpc.MetricsBatch{
		"gen": {
			TestID:   "t",
			WindowMs: 1000,
			Stats:    generatorrpc.GeneratorStats{ActiveVUsers: 20},
			// Raw transactions stay bounded exemplars; aggregate counts are authoritative.
			Transactions: successTransactions("A", 1024),
			FleetMetrics: &generatorrpc.MetricAggregate{
				Name:           "fleet",
				Count:          2000,
				AvgMs:          100,
				P95Ms:          100,
				P99Ms:          100,
				TPS:            2000,
				DurationDigest: &digestSnapshot,
			},
			Groups: []generatorrpc.GroupMetric{{
				Name:         "A",
				ActiveVUsers: 20,
				Metrics: &generatorrpc.MetricAggregate{
					Name:           "A",
					Count:          2000,
					AvgMs:          100,
					P95Ms:          100,
					P99Ms:          100,
					TPS:            2000,
					DurationDigest: &digestSnapshot,
				},
			}},
		},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.TotalTx != 2000 {
		t.Fatalf("live TotalTx = %d, want uncapped 2000", live.TotalTx)
	}
	if live.TPS != 2000 {
		t.Fatalf("live TPS = %v, want uncapped 2000", live.TPS)
	}
	if live.P95Ms != 100 || live.P99Ms != 100 {
		t.Fatalf("percentiles = p95 %v p99 %v, want digest values 100/100", live.P95Ms, live.P99Ms)
	}
	if group := live.GroupMetrics["A"]; group == nil || group.Transactions != 2000 || group.TPS != 2000 {
		t.Fatalf("group metrics = %#v, want uncapped tx/tps", group)
	}
}

func TestAggregateDistributedMetricsBatchesSumsDroppedSubscriberBatches(t *testing.T) {
	combined, _, _ := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"gen-a": {
			TestID:    "t",
			Telemetry: &generatorrpc.MetricsTelemetry{DroppedSubscriberBatches: 3, MissingMetricWindows: 2},
		},
		"gen-b": {
			TestID:    "t",
			Telemetry: &generatorrpc.MetricsTelemetry{DroppedSubscriberBatches: 5},
		},
	})
	if combined.Telemetry == nil {
		t.Fatal("combined telemetry is nil")
	}
	if combined.Telemetry.DroppedSubscriberBatches != 8 {
		t.Fatalf("dropped subscriber batches = %d, want sum 8", combined.Telemetry.DroppedSubscriberBatches)
	}
	if combined.Telemetry.MissingMetricWindows != 2 {
		t.Fatalf("missing metric windows = %d, want 2", combined.Telemetry.MissingMetricWindows)
	}
}

func TestBuildLiveMetricsFromDistributedBatchCarriesNetworkAndBrowserAggregates(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 1}}}
	batch := generatorrpc.MetricsBatch{
		TestID:   "t",
		WindowMs: 1000,
		Stats:    generatorrpc.GeneratorStats{ActiveVUsers: 1},
		FleetMetrics: &generatorrpc.MetricAggregate{
			Name:  "fleet",
			Count: 1,
			AvgMs: 120,
			TPS:   1,
		},
		NetworkProfileMetrics: []generatorrpc.MetricAggregate{{
			Name:  "preset:dsl",
			Count: 1,
			AvgMs: 120,
			P95Ms: 120,
		}},
		RegionMetrics: []generatorrpc.MetricAggregate{{
			Name:  "us-east",
			Count: 1,
			AvgMs: 120,
			P95Ms: 120,
			TPS:   1,
		}},
		Browser: &generatorrpc.BrowserMetrics{
			Samples:  1,
			AvgLCPMs: 240,
		},
		BrowserByDevice: []generatorrpc.BrowserDeviceMetrics{{
			DeviceProfile:  "iphone_se",
			DevicePlatform: "ios",
			DeviceTier:     "low",
			BrowserEngine:  "webkit",
			Metrics:        generatorrpc.BrowserMetrics{Samples: 1, AvgLCPMs: 240},
		}},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{"gen": batch})
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.NetworkProfiles["preset:dsl"] == nil || live.NetworkProfiles["preset:dsl"].AvgMs != 120 {
		t.Fatalf("network profiles = %#v, want preset:dsl aggregate", live.NetworkProfiles)
	}
	if live.ByRegion["us-east"] == nil || live.ByRegion["us-east"].AvgMs != 120 {
		t.Fatalf("regional metrics = %#v, want us-east aggregate", live.ByRegion)
	}
	if live.Browser == nil || live.Browser.AvgLCPMs != 240 {
		t.Fatalf("browser metrics = %#v, want LCP 240", live.Browser)
	}
	if live.BrowserByDevice["iphone_se"] == nil || live.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("browser by device = %#v, want iphone_se webkit", live.BrowserByDevice)
	}
}

// Empty group names fall back to the sole configured group for both bucketing
// and rate resolution.
func TestBuildLiveMetricsFromDistributedBatchResolvesEmptyGroupTPS(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "only", VUsers: 3}}}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 3}, Transactions: successTransactions("", 8)},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.TPS != 8 {
		t.Fatalf("live TPS = %v, want 8", live.TPS)
	}
	group := live.GroupMetrics["only"]
	if group == nil || group.TPS != 8 {
		t.Fatalf("group only TPS = %#v, want 8", group)
	}
	if tx := live.Transactions["tx"]; tx == nil || tx.TPS != 8 {
		t.Fatalf("transaction tx TPS = %#v, want 8", tx)
	}
}

func TestTxMetricsFromAggregateCarriesTPS(t *testing.T) {
	metrics := txMetricsFromAggregate(generatorrpc.MetricAggregate{
		Name:  "checkout",
		Count: 10,
		TPS:   4.5,
	})
	if metrics == nil || metrics.TPS != 4.5 {
		t.Fatalf("transaction aggregate TPS = %#v, want 4.5", metrics)
	}
}

func TestBuildLiveMetricsFromDistributedBatchNetworkPhasesAndErrorBreakdown(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A"}}}
	phase := generatorrpc.TransactionMetric{Name: "tx", Group: "A", DurationMs: 150, DNSMs: 12, ConnectMs: 24, TLSMs: 36, TTFBMs: 90}
	success := phase
	success.Success = true
	httpErr := phase
	httpErr.Success = false
	httpErr.StatusCode = 500
	httpErr.ErrorMessage = "boom"
	timeoutErr := phase
	timeoutErr.Success = false
	timeoutErr.ErrorMessage = "context deadline exceeded"

	batches := map[string]generatorrpc.MetricsBatch{
		"gen": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: []generatorrpc.TransactionMetric{success, httpErr, timeoutErr}},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	// Phase averages are over all 3 attempts; identical per-tx values keep them exact.
	if live.AvgDNSMs != 12 || live.AvgConnectMs != 24 || live.AvgTLSMs != 36 || live.AvgTTFBMs != 90 {
		t.Fatalf("phase averages = dns %v connect %v tls %v ttfb %v", live.AvgDNSMs, live.AvgConnectMs, live.AvgTLSMs, live.AvgTTFBMs)
	}
	if live.AvgWaitMs != 60 { // 150 - 90
		t.Fatalf("avg wait = %v, want 60", live.AvgWaitMs)
	}
	if live.ErrorBreakdown["http_5xx"] != 1 || live.ErrorBreakdown["timeout"] != 1 {
		t.Fatalf("error breakdown = %#v, want one http_5xx and one timeout", live.ErrorBreakdown)
	}
	if live.TPS != 1 { // one success over a 1s window
		t.Fatalf("live TPS = %v, want 1", live.TPS)
	}
}

func TestAggregateDistributedGeneratorStats(t *testing.T) {
	t.Run("CPU averages over active pods, max preserved, GC pause maxed", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 5, CPUPercent: 90, CPUFreePct: 10, MemoryBytes: 100, MemFreeMB: 1000, NetSentKbps: 10, NetRecvKbps: 20, DiskReadKbps: 30, DiskWriteKbps: 40, ContainerCPUPeriodsDelta: 5, GCPauseMs: 120},
			{ActiveVUsers: 5, CPUPercent: 60, CPUFreePct: 40, MemoryBytes: 200, MemFreeMB: 2000, NetSentKbps: 15, NetRecvKbps: 25, DiskReadKbps: 35, DiskWriteKbps: 45, ContainerCPUPeriodsDelta: 7, GCPauseMs: 40},
			{ActiveVUsers: 5, CPUPercent: 30, CPUFreePct: 70, MemoryBytes: 300, MemFreeMB: 3000, NetSentKbps: 25, NetRecvKbps: 35, DiskReadKbps: 45, DiskWriteKbps: 55, ContainerCPUPeriodsDelta: 9, GCPauseMs: 0},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 60 {
			t.Fatalf("CPUPercent = %v, want 60 (avg of 90,60,30)", agg.CPUPercent)
		}
		if agg.CPUFreePct != 40 {
			t.Fatalf("CPUFreePct = %v, want 40 (avg of 10,40,70)", agg.CPUFreePct)
		}
		if agg.CPUProcessPctMax != 90 {
			t.Fatalf("CPUProcessPctMax = %v, want 90", agg.CPUProcessPctMax)
		}
		if agg.GCPauseMs != 120 {
			t.Fatalf("GCPauseMs = %v, want 120 (max)", agg.GCPauseMs)
		}
		if agg.MemoryBytes != 600 {
			t.Fatalf("MemoryBytes = %d, want 600 (sum)", agg.MemoryBytes)
		}
		if agg.MemFreeMB != 6000 {
			t.Fatalf("MemFreeMB = %v, want 6000 (sum)", agg.MemFreeMB)
		}
		if agg.NetSentKbps != 50 || agg.NetRecvKbps != 80 || agg.DiskReadKbps != 110 || agg.DiskWriteKbps != 140 {
			t.Fatalf("I/O rates = net(%v,%v) disk(%v,%v), want net(50,80) disk(110,140)", agg.NetSentKbps, agg.NetRecvKbps, agg.DiskReadKbps, agg.DiskWriteKbps)
		}
		if agg.ContainerCPUPeriodsDelta != 21 {
			t.Fatalf("ContainerCPUPeriodsDelta = %d, want 21 (sum)", agg.ContainerCPUPeriodsDelta)
		}
		if agg.ActiveVUsers != 15 {
			t.Fatalf("ActiveVUsers = %d, want 15 (sum)", agg.ActiveVUsers)
		}
		resources := resourcesFromGeneratorStats(agg)
		if resources.CPUFreePct != 40 || resources.MemFreeMb != 6000 ||
			resources.NetSentKbps != 50 || resources.NetRecvKbps != 80 ||
			resources.DiskReadKbps != 110 || resources.DiskWriteKbps != 140 ||
			resources.ContainerCPUPeriodsDelta != 21 {
			t.Fatalf("resourcesFromGeneratorStats = %#v, want CPUFreePct/MemFreeMb/Net/Disk/ContainerCPUPeriodsDelta mapped", resources)
		}
	})

	t.Run("idle pods are excluded from the CPU average", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 10, CPUPercent: 90, CPUFreePct: 10},
			{ActiveVUsers: 0, CPUPercent: 10, CPUFreePct: 90},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 90 {
			t.Fatalf("CPUPercent = %v, want 90 (idle pod excluded)", agg.CPUPercent)
		}
		if agg.CPUFreePct != 10 {
			t.Fatalf("CPUFreePct = %v, want 10 (idle pod excluded)", agg.CPUFreePct)
		}
		if agg.CPUProcessPctMax != 90 {
			t.Fatalf("CPUProcessPctMax = %v, want 90", agg.CPUProcessPctMax)
		}
	})

	t.Run("all-idle falls back to averaging every pod", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 0, CPUPercent: 40, CPUFreePct: 60},
			{ActiveVUsers: 0, CPUPercent: 20, CPUFreePct: 80},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 30 {
			t.Fatalf("CPUPercent = %v, want 30 (fallback avg of all)", agg.CPUPercent)
		}
		if agg.CPUFreePct != 70 {
			t.Fatalf("CPUFreePct = %v, want 70 (fallback avg of all)", agg.CPUFreePct)
		}
	})
}
