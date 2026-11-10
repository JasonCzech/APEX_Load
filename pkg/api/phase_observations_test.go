package api

import (
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestMergeGeneratorMetricAggregatesPhaseObservations(t *testing.T) {
	current := generatorrpc.MetricAggregate{Name: "checkout", Count: 4, PhaseObserved: &engine.PhaseObservations{DNS: 1, Connect: 2, TLS: 2}}
	peer := generatorrpc.MetricAggregate{Name: "checkout", Count: 6, PhaseObserved: &engine.PhaseObservations{Connect: 1, TLS: 1}}
	merged := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{current, peer})
	if merged.PhaseObserved == nil || *merged.PhaseObserved != (engine.PhaseObservations{DNS: 1, Connect: 3, TLS: 3}) {
		t.Fatalf("merged = %+v", merged.PhaseObserved)
	}
	single := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{current})
	single.PhaseObserved.DNS = 99
	if current.PhaseObserved.DNS != 1 {
		t.Fatal("single-part merge must not alias the input observations")
	}
	older := generatorrpc.MetricAggregate{Name: "checkout", Count: 3}
	if got := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{current, older}).PhaseObserved; got != nil {
		t.Fatalf("mixed-version merge should be unknown, got %+v", got)
	}
	if tx := txMetricsFromAggregate(*merged); tx.PhaseObserved == nil || tx.PhaseObserved.Connect != 3 {
		t.Fatalf("live transaction metrics lost observations: %+v", tx.PhaseObserved)
	}
}

// Four samples: a new TLS connection, a reused one, a connect-then-timeout
// with no first byte and a protocol that records no TTFB.
func mixedTimeoutTransactions() []generatorrpc.TransactionMetric {
	return []generatorrpc.TransactionMetric{
		{Name: "checkout", Success: true, DurationMs: 185, DNSMs: 5, ConnectMs: 10, TLSMs: 20, TTFBMs: 135},
		{Name: "checkout", Success: true, DurationMs: 80, TTFBMs: 60},
		{Name: "checkout", Success: false, DurationMs: 30000, ConnectMs: 30, ErrorMessage: "timeout"},
		{Name: "checkout", Success: true, DurationMs: 5000},
	}
}

func requireLivePhaseStack(t *testing.T, live *LiveMetrics) {
	t.Helper()
	if live.AvgServerMs == nil || live.AvgNoResponseMs == nil {
		t.Fatalf("known observations must expose the split: server=%v no_response=%v", live.AvgServerMs, live.AvgNoResponseMs)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-6 }
	if !near(*live.AvgServerMs, 160.0/4) || !near(live.AvgWaitMs, 70.0/4) || !near(*live.AvgNoResponseMs, 34970.0/4) {
		t.Fatalf("server=%v download=%v no_response=%v", *live.AvgServerMs, live.AvgWaitMs, *live.AvgNoResponseMs)
	}
	stack := live.AvgDNSMs + live.AvgConnectMs + live.AvgTLSMs + *live.AvgServerMs + live.AvgWaitMs + *live.AvgNoResponseMs
	if !near(stack, live.AvgResponseMs) {
		t.Fatalf("stack %v != avg response %v", stack, live.AvgResponseMs)
	}
}

func TestRawDistributedLiveMetricsSplitTimeoutsOutOfDownload(t *testing.T) {
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{Transactions: mixedTimeoutTransactions()}, 0, nil)
	requireLivePhaseStack(t, live)
	if tx := live.Transactions["checkout"]; tx == nil || tx.AvgServerMs == nil || math.Abs(*tx.AvgServerMs-40) > 1e-6 || tx.AvgNoResponseMs == nil {
		t.Fatalf("transaction split = %+v", tx)
	}
}

func TestAggregateDistributedLiveMetricsSplitTimeoutsOutOfDownload(t *testing.T) {
	acc := newRawMetricAccumulator("checkout")
	for _, tx := range mixedTimeoutTransactions() {
		acc.add(tx)
	}
	fleet := acc.finalize(1)
	live := &LiveMetrics{Transactions: map[string]*TxMetrics{}}
	if !applyDistributedAggregateMetrics(&ManagedTest{}, live, generatorrpc.MetricsBatch{FleetMetrics: fleet, TransactionMetrics: []generatorrpc.MetricAggregate{*fleet}}, 0, nil) {
		t.Fatal("aggregate metrics not applied")
	}
	requireLivePhaseStack(t, live)
	if tx := live.Transactions["checkout"]; tx == nil || tx.AvgNoResponseMs == nil || math.Abs(*tx.AvgNoResponseMs-34970.0/4) > 1e-6 {
		t.Fatalf("transaction split = %+v", tx)
	}

	// An older generator sends no observations: no split, and the download
	// tail falls back to avg duration - avg TTFB.
	older := *fleet
	older.PhaseObserved = nil
	legacy := &LiveMetrics{Transactions: map[string]*TxMetrics{}}
	applyDistributedAggregateMetrics(&ManagedTest{}, legacy, generatorrpc.MetricsBatch{FleetMetrics: &older, TransactionMetrics: []generatorrpc.MetricAggregate{older}}, 0, nil)
	if legacy.AvgServerMs != nil || legacy.AvgNoResponseMs != nil {
		t.Fatalf("unknown observations must not split: %v %v", legacy.AvgServerMs, legacy.AvgNoResponseMs)
	}
	if want := older.AvgMs - older.AvgTTFBMs; math.Abs(legacy.AvgWaitMs-want) > 1e-6 {
		t.Fatalf("legacy wait %v, want %v", legacy.AvgWaitMs, want)
	}
	if tx := legacy.Transactions["checkout"]; tx == nil || tx.AvgServerMs != nil {
		t.Fatalf("legacy transaction split = %+v", tx)
	}
}

func TestSnapshotLiveMetricsExposePhaseSplit(t *testing.T) {
	observed := &engine.PhaseObservations{Connect: 2, DNS: 1, TLS: 1}
	for _, tx := range mixedTimeoutTransactions() {
		if tx.TTFBMs == 0 {
			setup := tx.DNSMs + tx.ConnectMs + tx.TLSMs
			observed.NoTTFB++
			observed.NoTTFBSetupMs += setup
			observed.NoTTFBRemainderMs += tx.DurationMs - setup
		}
	}
	snap := engine.MetricsSnapshot{
		Transactions: 3, Errors: 1, AvgDuration: time.Duration(35265.0 / 4 * float64(time.Millisecond)),
		AvgDNSMs: 5.0 / 4, AvgConnectMs: 40.0 / 4, AvgTLSMs: 20.0 / 4, AvgTTFBMs: 195.0 / 4, AvgWaitMs: 70.0 / 4,
		PhaseObserved: observed,
	}
	live := buildLiveMetricsFromSnapshot(snap, nil)
	requireLivePhaseStack(t, live)
	snap.PhaseObserved = nil
	if legacy := buildLiveMetricsFromSnapshot(snap, nil); legacy.AvgServerMs != nil || legacy.AvgNoResponseMs != nil {
		t.Fatal("a snapshot without observations must not split")
	}
}

func TestGroupFilterClearsFleetPhasesUnlessEveryGroupIsSelected(t *testing.T) {
	server, noResponse := 40.0, 5.0
	withPhases := func() *LiveMetrics {
		live := liveMetricsForGroupFilterTest()
		live.AvgDNSMs, live.AvgConnectMs, live.AvgTLSMs, live.AvgTTFBMs, live.AvgWaitMs = 1, 2, 3, 46, 20
		live.PhaseObserved = &engine.PhaseObservations{DNS: 1, Connect: 2, TLS: 3}
		live.AvgServerMs, live.AvgNoResponseMs = &server, &noResponse
		return live
	}
	live := withPhases()
	filtered := filterLiveMetricsForGroups(live, groupFilterQuery{Explicit: true, Names: []string{"Checkout"}})
	if filtered.AvgDNSMs != 0 || filtered.AvgConnectMs != 0 || filtered.AvgTLSMs != 0 || filtered.AvgTTFBMs != 0 || filtered.AvgWaitMs != 0 ||
		filtered.PhaseObserved != nil || filtered.AvgServerMs != nil || filtered.AvgNoResponseMs != nil {
		t.Fatalf("a group subset kept fleet-wide phases: %+v", filtered)
	}
	if live.PhaseObserved == nil || live.AvgServerMs == nil {
		t.Fatal("filtering must not change the source point")
	}
	names := make([]string, 0, len(live.GroupMetrics))
	for name := range live.GroupMetrics {
		names = append(names, name)
	}
	all := filterLiveMetricsForGroups(withPhases(), groupFilterQuery{Explicit: true, Names: names})
	if all.AvgTTFBMs != 46 || all.PhaseObserved == nil || all.AvgServerMs == nil || *all.AvgServerMs != server || all.AvgServerMs == live.AvgServerMs {
		t.Fatalf("selecting every group should keep the fleet phases: %+v", all)
	}
	if none := filterLiveMetricsForGroups(withPhases(), groupFilterQuery{Explicit: true}); none.AvgTTFBMs != 0 || none.PhaseObserved != nil {
		t.Fatalf("an empty selection kept phases: %+v", none)
	}
}
