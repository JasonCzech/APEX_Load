package api

import (
	"net"
	"net/url"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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

func TestLatencyEndpointsPreferParameterizedURL(t *testing.T) {
	hub := NewHub()
	tr := NewTestRunner(1, hub, nil)

	test := &ManagedTest{
		ID: "test-latency",
		Groups: []TestGroup{{
			Name: "Group1",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{
					BaseURL: "https://app.example.com",
				},
				Parameters: []dsl.Parameter{{
					Name:       "url",
					Type:       "file",
					DataSource: "url",
					Column:     "URL",
				}},
				DataSources: []dsl.DataSource{{
					Name:           "url",
					Type:           "dat",
					InlineData:     "URL\nhttps://csi-gw-ext.att.com:443\n",
					Strategy:       "sequential",
					FirstRowHeader: true,
				}},
				Actions: []dsl.Action{{
					Name:   "InquireSubscriberBillingAgreement",
					Type:   "POST",
					Method: "POST",
					Target: "{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
				}},
			},
		}},
	}

	endpoints := tr.latencyEndpointsForTest(test)

	if got := endpoints["csi-gw-ext.att.com:443"]; got != "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws" {
		t.Fatalf("parameterized endpoint = %q, want URL.dat value plus hardcoded path", got)
	}
	if _, exists := endpoints["app.example.com"]; exists {
		t.Fatalf("base_url fallback should not be used when URL parameter resolves, got endpoints %#v", endpoints)
	}
}

func TestLatencyEndpointsNormalizeParameterizedURLBoundaryWhitespace(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{
			Name:   "url",
			Type:   "list",
			Values: []string{"https://csi-gw-ext.att.com:443 "},
		}},
		Actions: []dsl.Action{{
			Name:   "InquireSubscriberBillingAgreement",
			Type:   "POST",
			Method: "POST",
			Target: "{{var.url}} /Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
		}},
	}

	endpoints := make(map[string]string)
	addLatencyEndpoint(endpoints, script)

	if got := endpoints["csi-gw-ext.att.com:443"]; got != "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws" {
		t.Fatalf("parameterized endpoint = %q, want normalized full URL", got)
	}
}

func TestMeasureNetworkLatencyHydratesScriptByGroupScriptID(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	baseURL := "http://" + addr

	hub := NewHub()
	tr := NewTestRunner(1, hub, nil)
	tr.scripts["script-1"] = &StoredScript{
		ID: "script-1",
		Script: &dsl.Script{
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
			},
		},
	}

	test := &ManagedTest{
		ID: "test-latency",
		Groups: []TestGroup{{
			Name:     "Group1",
			ScriptID: "script-1",
		}},
	}

	live := &LiveMetrics{}
	tr.measureNetworkLatency(test, live)

	if live.Latency == nil || len(live.Latency.Targets) != 1 {
		t.Fatalf("expected hydrated script to produce one latency target, got %#v", live.Latency)
	}
}
