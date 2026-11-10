package generatorrpc

import (
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

func TestMetricAggregatePhaseObservationsRoundTripAndClamp(t *testing.T) {
	in := &MetricAggregate{Name: "checkout", Count: 10, PhaseObserved: &engine.PhaseObservations{DNS: 2, Connect: 3, TLS: 3}}
	wire, err := proto.Marshal(metricAggregateToProto(in))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricAggregate
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	out := metricAggregateFromProto(&decoded)
	if out.PhaseObserved == nil || *out.PhaseObserved != *in.PhaseObserved {
		t.Fatalf("round trip = %+v", out.PhaseObserved)
	}

	// A peer cannot claim more observations than samples.
	decoded.PhaseObserved.Tls = 99
	decoded.PhaseObserved.Dns = -1
	if got := metricAggregateFromProto(&decoded).PhaseObserved; got.TLS != 10 || got.DNS != 0 {
		t.Fatalf("clamped = %+v", got)
	}
	// An older generator sends nothing; an unknown version is unknown too.
	decoded.PhaseObserved = nil
	if got := metricAggregateFromProto(&decoded).PhaseObserved; got != nil {
		t.Fatalf("missing observations should be unknown, got %+v", got)
	}
	decoded.PhaseObserved = &pb.PhaseObservations{Version: 99, Dns: 1}
	if got := metricAggregateFromProto(&decoded).PhaseObserved; got != nil {
		t.Fatalf("unknown version should be unknown, got %+v", got)
	}
}

func TestMetricAccumulatorAndMergeCountObservedPhases(t *testing.T) {
	acc := &metricAccumulator{name: "checkout", digest: engine.NewTDigest(200), sketch: engine.NewDurationSketch()}
	acc.add(TransactionMetric{Success: true, DurationMs: 50, DNSMs: 1, ConnectMs: 2, TLSMs: 3, TTFBMs: 40})
	acc.add(TransactionMetric{Success: true, DurationMs: 30, TTFBMs: 20})
	first := acc.finalize(1)
	if first.PhaseObserved == nil || *first.PhaseObserved != (engine.PhaseObservations{DNS: 1, Connect: 1, TLS: 1}) {
		t.Fatalf("accumulated = %+v", first.PhaseObserved)
	}
	merged := mergeMetricAggregates([]MetricAggregate{*first, {Name: "checkout", Count: 2, PhaseObserved: &engine.PhaseObservations{Connect: 2, TLS: 2}}})
	if merged.PhaseObserved == nil || *merged.PhaseObserved != (engine.PhaseObservations{DNS: 1, Connect: 3, TLS: 3}) {
		t.Fatalf("merged = %+v", merged.PhaseObserved)
	}
	mixed := mergeMetricAggregates([]MetricAggregate{*first, {Name: "checkout", Count: 2}})
	if mixed.PhaseObserved != nil {
		t.Fatalf("a part from an older generator should make the merge unknown, got %+v", mixed.PhaseObserved)
	}
}

func TestPhaseObservationsCarryNoFirstByteSumsAcrossTheWire(t *testing.T) {
	acc := &metricAccumulator{name: "checkout", digest: engine.NewTDigest(200), sketch: engine.NewDurationSketch()}
	acc.add(TransactionMetric{Success: true, DurationMs: 100, ConnectMs: 10, TTFBMs: 70})
	acc.add(TransactionMetric{Success: false, DurationMs: 3000, ConnectMs: 20}) // timed out before a first byte
	first := acc.finalize(1)
	want := engine.PhaseObservations{Connect: 2, NoTTFB: 1, NoTTFBSetupMs: 20, NoTTFBRemainderMs: 2980}
	if first.PhaseObserved == nil || *first.PhaseObserved != want {
		t.Fatalf("accumulated = %+v", first.PhaseObserved)
	}
	wire, err := proto.Marshal(metricAggregateToProto(first))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricAggregate
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := metricAggregateFromProto(&decoded).PhaseObserved; got == nil || *got != want {
		t.Fatalf("round trip = %+v", got)
	}
	merged := mergeMetricAggregates([]MetricAggregate{*first, *first})
	if got := merged.PhaseObserved; got == nil || got.NoTTFB != 2 || got.NoTTFBSetupMs != 40 || got.NoTTFBRemainderMs != 5960 {
		t.Fatalf("merged = %+v", got)
	}
	// A peer cannot send negative sums or claim more no-TTFB samples than it has.
	decoded.PhaseObserved.NoTtfb = 50
	decoded.PhaseObserved.NoTtfbSetupMs = -5
	if got := metricAggregateFromProto(&decoded).PhaseObserved; got.NoTTFB != 2 || got.NoTTFBSetupMs != 0 {
		t.Fatalf("clamped = %+v", got)
	}
}
