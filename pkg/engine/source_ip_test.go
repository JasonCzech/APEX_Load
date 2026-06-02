package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNormalizeSourceIPs(t *testing.T) {
	got := normalizeSourceIPs([]string{" 10.0.0.1 ", "10.0.0.2", "", "10.0.0.1", "  "})
	want := []string{"10.0.0.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q want %q", i, got[i], want[i])
		}
	}
	if normalizeSourceIPs(nil) != nil || normalizeSourceIPs([]string{" ", ""}) != nil {
		t.Fatalf("blank pools should normalize to nil")
	}
}

func TestVuserProtoConfigAssignsStableIP(t *testing.T) {
	base := map[string]any{
		"base_url":       "https://x",
		"web_source_ips": []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
	}
	// VUsers are distributed round-robin and the assignment is stable per id.
	cases := map[int]string{0: "10.0.0.1", 1: "10.0.0.2", 2: "10.0.0.3", 3: "10.0.0.1", 5: "10.0.0.3"}
	for vuserID, wantIP := range cases {
		cfg := vuserProtoConfig(base, vuserID)
		if cfg["web_source_ip"] != wantIP {
			t.Fatalf("vuser %d -> %v, want %s", vuserID, cfg["web_source_ip"], wantIP)
		}
	}
	// The base config must not be mutated.
	if _, mutated := base["web_source_ip"]; mutated {
		t.Fatalf("base config was mutated")
	}
}

func TestVuserProtoConfigNoPoolReturnsBase(t *testing.T) {
	base := map[string]any{"base_url": "https://x"}
	cfg := vuserProtoConfig(base, 7)
	// No pool -> same map returned, no source ip injected.
	if _, ok := cfg["web_source_ip"]; ok {
		t.Fatalf("should not inject source ip without a pool")
	}
}

func TestBuildProtoConfigEmitsSourceIPs(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "https://x",
			WebRuntime: dsl.WebRuntimeConfig{
				SourceIPs: []string{"10.0.0.1", " 10.0.0.1 ", "10.0.0.2"},
			},
		},
	}
	cfg := buildProtoConfig(script)
	pool, ok := cfg["web_source_ips"].([]string)
	if !ok {
		t.Fatalf("web_source_ips missing or wrong type: %T", cfg["web_source_ips"])
	}
	if len(pool) != 2 {
		t.Fatalf("expected deduped pool of 2, got %#v", pool)
	}
}
