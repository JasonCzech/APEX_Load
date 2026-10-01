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
