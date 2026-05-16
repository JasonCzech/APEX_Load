package api

import (
	"net"
	"net/url"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestMeasureEndpointLatency(t *testing.T) {
	// Start a local TCP listener to act as a target
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	host, _, _ := net.SplitHostPort(addr)

	tr := &TestRunner{}
	target := tr.measureEndpointLatency(addr, "http://"+addr)

	if !target.LastSuccess {
		t.Errorf("expected success for reachable host %s", addr)
	}
	if target.LatencyMs <= 0 {
		t.Errorf("expected positive latency, got %v", target.LatencyMs)
	}
	if target.IP != host && target.IP != "127.0.0.1" {
		t.Errorf("expected IP 127.0.0.1, got %s", target.IP)
	}

	// Test unreachable host
	targetUnreachable := tr.measureEndpointLatency("127.0.0.1:65535", "http://127.0.0.1:65535")
	if targetUnreachable.LastSuccess {
		t.Error("expected failure for unreachable port")
	}
}

func TestMeasureNetworkLatency(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	baseURL := "http://" + addr

	hub := NewHub()
	tr := NewTestRunner(1, hub, nil)

	test := &ManagedTest{
		ID: "test-latency",
		Groups: []TestGroup{
			{
				Name: "Group1",
				script: &dsl.Script{
					Config: dsl.ScriptConfig{
						BaseURL: baseURL,
					},
				},
			},
		},
	}

	live := &LiveMetrics{}
	tr.measureNetworkLatency(test, live)

	if live.Latency == nil {
		t.Fatal("expected live.Latency to be populated")
	}
	if len(live.Latency.Targets) != 1 {
		t.Fatalf("expected 1 latency target, got %d", len(live.Latency.Targets))
	}

	target := live.Latency.Targets[0]
	u, _ := url.Parse(baseURL)
	if target.Name != u.Host {
		t.Errorf("expected target name %s, got %s", u.Host, target.Name)
	}
	if !target.LastSuccess {
		t.Error("expected latency measurement to succeed")
	}
}
