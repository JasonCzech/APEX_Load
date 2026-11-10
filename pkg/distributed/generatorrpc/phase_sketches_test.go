package generatorrpc

import (
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

func phaseTransactions() []TransactionMetric {
	return []TransactionMetric{
		{Name: "checkout", Group: "g", Success: true, DurationMs: 120, DNSMs: 4, ConnectMs: 10, TLSMs: 30, TTFBMs: 100},
		{Name: "checkout", Group: "g", Success: true, DurationMs: 25, TTFBMs: 20},
		{Name: "checkout", Group: "g", Success: false, DurationMs: 3000, ConnectMs: 8},
	}
}

func phaseAggregate(t *testing.T) *MetricAggregate {
	t.Helper()
	window := newTransactionWindowAggregate()
	for _, tx := range phaseTransactions() {
		window.add(tx)
	}
	set := window.finalize(1000)
	if set.Fleet == nil || len(set.Transactions) != 1 {
		t.Fatalf("finalized = %+v", set)
	}
	for _, group := range set.Groups {
		if group.Metrics != nil && group.Metrics.PhaseSketches != nil {
			t.Fatal("group aggregates should not carry phase sketches")
		}
	}
	return set.Fleet
}

func TestWindowAggregateRecordsPhaseSketchesForFleetAndTransactions(t *testing.T) {
	window := newTransactionWindowAggregate()
	for _, tx := range phaseTransactions() {
		window.add(tx)
	}
	set := window.finalize(1000)
	for name, aggregate := range map[string]*MetricAggregate{"fleet": set.Fleet, "transaction": &set.Transactions[0]} {
		sketches := aggregate.PhaseSketches
		if sketches == nil || !sketches.ConsistentWith(aggregate.PhaseObserved, aggregate.Count) {
			t.Fatalf("%s sketches %+v do not match observations %+v", name, sketches, aggregate.PhaseObserved)
		}
		if sketches.Connect.Count != 2 || sketches.Server.Count != 2 || sketches.TLS.Count != 1 {
			t.Fatalf("%s sketches = %+v", name, sketches)
		}
	}
	if set.Dependencies != nil {
		for _, dependency := range set.Dependencies {
			if dependency.PhaseSketches != nil {
				t.Fatal("dependency aggregates should not carry phase sketches")
			}
		}
	}
}

func TestPhaseSketchesProtoRoundTripAndValidation(t *testing.T) {
	in := phaseAggregate(t)
	wire, err := proto.Marshal(metricAggregateToProto(in))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricAggregate
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	out := metricAggregateFromProto(&decoded)
	if out.PhaseSketches == nil {
		t.Fatal("round trip lost the phase sketches")
	}
	for _, phase := range engine.NetworkPhases {
		got, want := out.PhaseSketches.Phase(phase), in.PhaseSketches.Phase(phase)
		if (got == nil) != (want == nil) || got != nil && (got.Count != want.Count || got.Sum != want.Sum || len(got.Bins) != len(want.Bins)) {
			t.Fatalf("%s round trip %+v, sent %+v", phase, got, want)
		}
	}
	if got, want := out.PhaseSketches.Percentiles(), in.PhaseSketches.Percentiles(); *got.Server != *want.Server {
		t.Fatalf("server percentiles %+v, sent %+v", got.Server, want.Server)
	}

	// Sketches that disagree with the phase observations are refused.
	tampered := proto.Clone(&decoded).(*pb.MetricAggregate)
	tampered.PhaseObserved.Connect = 1
	if got := metricAggregateFromProto(tampered).PhaseSketches; got != nil {
		t.Fatalf("mismatched counts should be unknown, got %+v", got)
	}
	// Without observations there is nothing to check them against.
	tampered = proto.Clone(&decoded).(*pb.MetricAggregate)
	tampered.PhaseObserved = nil
	if got := metricAggregateFromProto(tampered).PhaseSketches; got != nil {
		t.Fatalf("sketches without observations should be unknown, got %+v", got)
	}
	// An unknown version is unknown.
	tampered = proto.Clone(&decoded).(*pb.MetricAggregate)
	tampered.PhaseSketches.Version = 99
	if got := metricAggregateFromProto(tampered).PhaseSketches; got != nil {
		t.Fatalf("unknown version should be unknown, got %+v", got)
	}
	// An older generator sends none.
	tampered = proto.Clone(&decoded).(*pb.MetricAggregate)
	tampered.PhaseSketches = nil
	if got := metricAggregateFromProto(tampered).PhaseSketches; got != nil {
		t.Fatalf("missing sketches should be unknown, got %+v", got)
	}
}

func TestMetricsBatchPhaseSketchVersionGatesSketches(t *testing.T) {
	svc := newIsolatedService(t, 10)
	fleet := phaseAggregate(t)
	action := *fleet
	action.Name = "GET /cart"
	dependency := *fleet
	dependency.Name = "api.example"
	pbBatch := svc.metricsBatchForMetrics("test-1", 1, GeneratorStats{ActiveVUsers: 1}, nil,
		metricsAggregateSet{NativeVersion: 3, Fleet: fleet, Transactions: []MetricAggregate{*fleet}, Actions: []MetricAggregate{action}, Dependencies: []MetricAggregate{dependency}},
		false, map[string]int32{"g": 1}, "RUNNING", "", nil)
	if pbBatch.GetPhaseSketchVersion() != engine.PhaseSketchVersion {
		t.Fatalf("phase_sketch_version = %d", pbBatch.GetPhaseSketchVersion())
	}
	wire, err := proto.Marshal(pbBatch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricsBatch
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	current := metricsBatchFromProto(&decoded)
	if current.PhaseSketchVersion != engine.PhaseSketchVersion || current.FleetMetrics.PhaseSketches == nil ||
		current.TransactionMetrics[0].PhaseSketches == nil || current.ActionMetrics[0].PhaseSketches == nil {
		t.Fatalf("current batch lost sketches: %+v", current.FleetMetrics)
	}
	if current.DependencyMetrics[0].PhaseSketches != nil {
		t.Fatal("only fleet, transaction and action aggregates carry phase sketches")
	}

	// An older generator's batch (version 0) is unknown even if it somehow
	// carried sketches.
	decoded.PhaseSketchVersion = 0
	older := metricsBatchFromProto(&decoded)
	if older.FleetMetrics.PhaseSketches != nil || older.TransactionMetrics[0].PhaseSketches != nil || older.ActionMetrics[0].PhaseSketches != nil {
		t.Fatal("a version 0 batch must not yield phase sketches")
	}
	if older.FleetMetrics.PhaseObserved == nil {
		t.Fatal("gating phase sketches must keep the phase observations")
	}
}

func TestMergeMetricAggregatesMergesPhaseSketchesAndMixedVersionIsUnknown(t *testing.T) {
	first := phaseAggregate(t)
	merged := mergeMetricAggregates([]MetricAggregate{*first, *first})
	if merged.PhaseSketches == nil || !merged.PhaseSketches.ConsistentWith(merged.PhaseObserved, merged.Count) || merged.PhaseSketches.Connect.Count != 4 {
		t.Fatalf("merged = %+v", merged.PhaseSketches)
	}
	older := *first
	older.PhaseSketches = nil
	if got := mergeMetricAggregates([]MetricAggregate{*first, older}).PhaseSketches; got != nil {
		t.Fatalf("a part without sketches must leave the merge unknown, got %+v", got)
	}
}

func TestBoundPhaseSketchAggregatesKeepsFleetAndCopies(t *testing.T) {
	wide := func(n int) *engine.PhaseSketches {
		var recorder engine.PhaseSketchRecorder
		for i := 1; i <= n; i++ {
			recorder.ObserveMs(float64(i)*1.5, 0, 0, 0, 0)
		}
		return recorder.Snapshot()
	}
	per := wide(1000).Bins()
	entries := engine.MaxWindowPhaseSketchBins/per + 2
	transactions := make([]MetricAggregate, entries)
	for i := range transactions {
		transactions[i] = MetricAggregate{Name: string(rune('a' + i%26)), Count: int64(i + 1), PhaseSketches: wide(1000)}
	}
	fleet := &MetricAggregate{Name: "fleet", Count: 1, PhaseSketches: wide(1000)}
	in := metricsAggregateSet{Fleet: fleet, Transactions: transactions}
	out := boundPhaseSketchAggregates(in)
	if out.Fleet.PhaseSketches == nil {
		t.Fatal("the fleet keeps its sketches first")
	}
	kept, total := 0, out.Fleet.PhaseSketches.Bins()
	for _, aggregate := range out.Transactions {
		if aggregate.PhaseSketches != nil {
			kept++
			total += aggregate.PhaseSketches.Bins()
		}
	}
	if total > engine.MaxWindowPhaseSketchBins || kept == len(transactions) {
		t.Fatalf("kept %d of %d entries, %d bins", kept, len(transactions), total)
	}
	if out.Transactions[0].PhaseSketches != nil {
		t.Fatal("the quietest entry should lose its sketches first")
	}
	for _, aggregate := range in.Transactions {
		if aggregate.PhaseSketches == nil {
			t.Fatal("bounding must not change the caller's aggregates")
		}
	}
}
