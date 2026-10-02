package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"testing"
	"time"
)

func TestNativeEvidenceSnapshotPropagationAndMixedSupport(t *testing.T) {
	var a metricsAggregateSet
	mergeSnapshotSideChannelAggregates(&a, engine.MetricsSnapshot{ByAction: map[string]*engine.ActionMetrics{"Checkout": {Count: 3, P95: 20 * time.Millisecond}}})
	if a.NativeVersion != 1 || len(a.Actions) != 1 || a.Actions[0].Count != 3 {
		t.Fatalf("snapshot %+v", a)
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": a})
	if merged.NativeVersion != 1 || len(merged.Actions) != 1 || merged.Actions[0].Count != 6 {
		t.Fatalf("merge %+v", merged)
	}
	unsupported := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": {}})
	if unsupported.NativeVersion != 0 {
		t.Fatal("unsupported group claimed complete evidence")
	}
	var sourcePlan metricsAggregateSet
	mergeSnapshotSideChannelAggregates(&sourcePlan, engine.MetricsSnapshot{})
	if sourcePlan.NativeVersion != 0 {
		t.Fatal("source plan without side-channel dimensions claimed support")
	}
}

func TestNativeEvidenceV2JointAndDistributionPropagation(t *testing.T) {
	d := engine.NewTDigest(200)
	d.Add(float64(time.Millisecond))
	snapshot := d.Snapshot()
	key := engine.ActionPropertyKey("checkout", "kind", "paid")
	metric := &engine.ActionMetrics{Name: key, Count: 1, DurationDigest: &snapshot}
	var a metricsAggregateSet
	mergeSnapshotSideChannelAggregates(&a, engine.MetricsSnapshot{ByAction: map[string]*engine.ActionMetrics{"checkout": metric}, ByActionProperty: map[string]*engine.ActionMetrics{key: metric}})
	if a.NativeVersion != 2 || len(a.ActionProperties) != 1 || a.ActionProperties[0].DurationDigest == nil {
		t.Fatalf("v2 %+v", a)
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": a})
	if merged.NativeVersion != 2 || merged.ActionProperties[0].DurationDigest.Count != 2 {
		t.Fatalf("joint merge %+v", merged)
	}
	old := a
	old.NativeVersion = 1
	old.ActionProperties = nil
	mixed := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": old})
	if mixed.NativeVersion != 1 || len(mixed.ActionProperties) != 0 {
		t.Fatal("old peer was represented as supporting joints")
	}
	p := metricAggregateToProto(&merged.ActionProperties[0])
	round := metricAggregateFromProto(p)
	if round.DurationDigest == nil || round.DurationDigest.Count != 2 || round.Name != key {
		t.Fatal("RPC lost joint distribution")
	}
	missing := merged.ActionProperties[0]
	missing.DurationDigest = nil
	partial := mergeMetricAggregates([]MetricAggregate{merged.ActionProperties[0], missing})
	if partial.DurationDigest != nil {
		t.Fatal("partial distribution advertised as complete")
	}
}

func TestNativeEvidenceV3DimensionsAndMixedPeers(t *testing.T) {
	d := engine.NewTDigest(200)
	d.Add(float64(time.Millisecond))
	snapshot := d.Snapshot()
	key := engine.ActionDimensionKey("checkout", "slow", "cart", "actions")
	var a metricsAggregateSet
	mergeSnapshotSideChannelAggregates(&a, engine.MetricsSnapshot{ByAction: map[string]*engine.ActionMetrics{}, ByActionProperty: map[string]*engine.ActionMetrics{}, ByDimensions: map[string]*engine.ActionMetrics{key: {Name: key, Count: 1, DurationDigest: &snapshot}}})
	if a.NativeVersion != 3 || len(a.ActionDimensions) != 1 {
		t.Fatalf("v3 %+v", a)
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": a})
	if len(merged.ActionDimensions) != 1 || merged.ActionDimensions[0].Count != 2 {
		t.Fatal("lost merged joint")
	}
	old := a
	old.NativeVersion = 2
	old.ActionDimensions = nil
	mixed := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": a, "b": old})
	if mixed.NativeVersion != 2 || len(mixed.ActionDimensions) != 0 {
		t.Fatal("unsupported peer represented as v3")
	}
}
