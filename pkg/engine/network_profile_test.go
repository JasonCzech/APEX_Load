package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func mixProfiles() []dsl.NetworkProfileWeight {
	return []dsl.NetworkProfileWeight{
		{Name: "3g", Weight: 3, NetworkSimulationConfig: dsl.NetworkSimulationConfig{Mode: "custom", LatencyMs: 200, BandwidthKbps: 750}},
		{Name: "4g", Weight: 7, NetworkSimulationConfig: dsl.NetworkSimulationConfig{Mode: "custom", LatencyMs: 50, BandwidthKbps: 12000}},
	}
}

func TestNormalizeNetworkProfilesDropsInvalid(t *testing.T) {
	got := normalizeNetworkProfiles([]dsl.NetworkProfileWeight{
		{Name: " 3g ", Weight: 3},
		{Name: "", Weight: 5},     // no name
		{Name: "zero", Weight: 0}, // no weight
		{Name: "neg", Weight: -1}, // negative weight
	})
	if len(got) != 1 || got[0].Name != "3g" || got[0].Weight != 3 {
		t.Fatalf("unexpected normalized profiles: %#v", got)
	}
}

func TestAssignNetworkProfileWeightedDistribution(t *testing.T) {
	profiles := mixProfiles()
	counts := map[string]int{}
	for id := 0; id < 10; id++ {
		p, ok := assignNetworkProfile(profiles, id)
		if !ok {
			t.Fatalf("expected assignment for id %d", id)
		}
		counts[p.Name]++
	}
	// 3/10 ids on 3g, 7/10 on 4g.
	if counts["3g"] != 3 || counts["4g"] != 7 {
		t.Fatalf("distribution = %#v, want 3x3g / 7x4g", counts)
	}
	// Assignment is stable per id.
	a, _ := assignNetworkProfile(profiles, 5)
	b, _ := assignNetworkProfile(profiles, 5)
	if a.Name != b.Name {
		t.Fatalf("assignment should be stable per id, got %s then %s", a.Name, b.Name)
	}
}

func TestVuserProtoConfigInjectsNetworkProfile(t *testing.T) {
	base := map[string]any{
		"base_url":             "https://x",
		"web_network_profiles": mixProfiles(),
	}
	// id 0 -> 3g (latency 200), id 9 -> 4g (latency 50).
	c0 := vuserProtoConfig(base, 0)
	if c0["web_network_profile_label"] != "3g" {
		t.Fatalf("id 0 label = %v, want 3g", c0["web_network_profile_label"])
	}
	if c0["web_network_simulation_latency_ms"] != float64(200) {
		t.Fatalf("id 0 latency = %v, want 200", c0["web_network_simulation_latency_ms"])
	}
	c9 := vuserProtoConfig(base, 9)
	if c9["web_network_profile_label"] != "4g" || c9["web_network_simulation_latency_ms"] != float64(50) {
		t.Fatalf("id 9 profile mismatch: label=%v latency=%v", c9["web_network_profile_label"], c9["web_network_simulation_latency_ms"])
	}
	// base must not be mutated.
	if _, ok := base["web_network_profile_label"]; ok {
		t.Fatalf("base config mutated")
	}
}

func TestBuildProtoConfigEmitsNetworkProfiles(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL:    "https://x",
			WebRuntime: dsl.WebRuntimeConfig{NetworkProfiles: mixProfiles()},
		},
	}
	cfg := buildProtoConfig(script)
	profiles, ok := cfg["web_network_profiles"].([]dsl.NetworkProfileWeight)
	if !ok || len(profiles) != 2 {
		t.Fatalf("web_network_profiles missing/wrong: %#v", cfg["web_network_profiles"])
	}
}
