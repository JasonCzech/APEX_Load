package api

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBrowserPolicyMetricFloorsValidateAndReachPinnedBaseline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		floors map[string]float64
		valid  bool
	}{
		{"legacy", nil, true}, {"timings", map[string]float64{"lcp": 300, "fcp": 50, "inp": 600000}, true},
		{"below-default", map[string]float64{"lcp": 49}, false}, {"too-high", map[string]float64{"inp": 600001}, false},
		{"cls-has-own-floor", map[string]float64{"cls": 0.02}, false}, {"unknown", map[string]float64{"cpu": 50}, false},
		{"nonfinite", map[string]float64{"lcp": math.Inf(1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := e1Policy(e1LCP)
			p.EffectFloors.TimingByMetricMs = tc.floors
			if err := p.validate(); (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	policy := e1Policy(e1LCP)
	policy.EffectFloors.TimingByMetricMs = map[string]float64{"lcp": 350, "fcp": 75}
	pin := saveBrowserPolicy(t, store, "metric-floors", 1, policy)
	manifest := &RunArchiveManifest{Provenance: RunArchiveProvenance{PolicyPins: []RunPolicyPin{pin}}}
	got, err := server.rcaBrowserBaselinePolicy(storage.DefaultProjectID, manifest)
	if err != nil || got == nil || got.MetricFloors["lcp"] != 350 || got.MetricFloors["fcp"] != 75 {
		t.Fatal("pinned timing floors lost", got, err)
	}
	policy.EffectFloors.TimingByMetricMs["lcp"] = 500
	saveBrowserPolicy(t, store, "metric-floors", 2, policy)
	old, err := server.rcaBrowserBaselinePolicy(storage.DefaultProjectID, manifest)
	if err != nil || old.MetricFloors["lcp"] != 350 {
		t.Fatal("later revision rewrote pinned floors", old, err)
	}
}
