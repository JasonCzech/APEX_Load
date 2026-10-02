package engine

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNativeJointDistributionUsesRealObservations(t *testing.T) {
	mc := NewMetricsCollector(10)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "checkout", Duration: time.Millisecond, Success: true, PropertyDimensions: map[string]string{"kind": "paid"}},
		{ActionName: "checkout", Duration: 9 * time.Millisecond, Success: false, PropertyDimensions: map[string]string{"kind": "free"}},
		{ActionName: "search", Duration: 3 * time.Millisecond, Success: true, PropertyDimensions: map[string]string{"kind": "paid"}},
		{ActionName: "business", Duration: 100 * time.Millisecond, BusinessTransaction: true, Success: true},
	}, time.Second)
	m := snap.ByActionProperty[ActionPropertyKey("checkout", "kind", "paid")]
	if m == nil || m.Count != 1 || m.Errors != 0 || ValidateDurationDigest(m.DurationDigest, 1) != nil {
		t.Fatalf("joint %+v", m)
	}
	if len(snap.ByActionProperty) != 3 || snap.ByAction["checkout"].DurationDigest.Count != 2 || snap.ByTransaction["business"].DurationDigest.Count != 1 {
		t.Fatal("population/digest mismatch")
	}
	clone := CloneTDigestSnapshot(m.DurationDigest)
	clone.Centroids[0].Mean = 500
	if m.DurationDigest.Centroids[0].Mean == 500 {
		t.Fatal("clone alias")
	}
}
func TestNativeJointIdentityBoundsAndNoCollision(t *testing.T) {
	a := ActionPropertyKey("a=x", "y", "z")
	b := ActionPropertyKey("a", "x=y", "z")
	if a == b {
		t.Fatal("identity collision")
	}
	var parts []string
	if json.Unmarshal([]byte(a), &parts) != nil || len(parts) != 3 {
		t.Fatal("invalid identity")
	}
	values := map[string]*ActionMetrics{}
	for i := 0; i < 200; i++ {
		key := ActionPropertyKey(strings.Repeat("x", i), "p", "v")
		values[boundedJointPropertyKey(key, values)] = &ActionMetrics{}
	}
	if len(values) > 128 || values[metricOverflowLabel] == nil {
		t.Fatal("unbounded joints")
	}
	if boundedJointPropertyKey(strings.Repeat("a", 513), values) != metricOverflowLabel {
		t.Fatal("truncated identity")
	}
}
func TestValidateNativeDurationDistribution(t *testing.T) {
	d := NewTDigest(200)
	d.Add(1)
	snap := d.Snapshot()
	for _, tc := range []struct {
		name   string
		change func(*TDigestSnapshot)
	}{
		{"count", func(s *TDigestSnapshot) { s.Count = 2 }}, {"weight", func(s *TDigestSnapshot) { s.Centroids[0].Weight = 2 }}, {"nan", func(s *TDigestSnapshot) { s.Centroids[0].Mean = math.NaN() }}, {"negative", func(s *TDigestSnapshot) { s.Min = -1 }}, {"compression", func(s *TDigestSnapshot) { s.Compression = 1e9 }}, {"empty", func(s *TDigestSnapshot) { s.Centroids = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := CloneTDigestSnapshot(&snap)
			tc.change(bad)
			if ValidateDurationDigest(bad, 1) == nil {
				t.Fatal("invalid digest accepted")
			}
		})
	}
	if ValidateDurationDigest(&snap, 1) != nil {
		t.Fatal("valid digest rejected")
	}
}

func TestNativeDimensionIntersectionPopulationAndBounds(t *testing.T) {
	mc := NewMetricsCollector(10)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "checkout", NetworkProfile: "slow", Hierarchy: "cart/checkout", Phase: "actions", Duration: time.Millisecond, Success: true},
		{ActionName: "checkout", NetworkProfile: "fast", Hierarchy: "cart/checkout", Phase: "init", Duration: 9 * time.Millisecond, Success: false},
		{ActionName: "business", BusinessTransaction: true, Phase: "actions", Duration: time.Second, Success: true},
	}, time.Second)
	key := ActionDimensionKey("checkout", "slow", "cart/checkout", "actions")
	m := snap.ByDimensions[key]
	if len(snap.ByDimensions) != 2 || m == nil || m.Count != 1 || m.Errors != 0 || ValidateDurationDigest(m.DurationDigest, 1) != nil {
		t.Fatalf("intersection %+v", snap.ByDimensions)
	}
	compact := compactMetricsResult(&protocol.Result{Phase: "init", Hierarchy: "tree"})
	if compact.Phase != "init" || compact.Hierarchy != "tree" {
		t.Fatal("lost lifecycle evidence")
	}
	if ActionDimensionKey(strings.Repeat("x", 257), "", "", "") != metricOverflowLabel || ActionDimensionKey("x", string([]byte{255}), "", "") != metricOverflowLabel {
		t.Fatal("invalid identity accepted")
	}
}
