package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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

func TestMeasureEndpointLatencyCapturesHTTPProbeDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	tr := NewTestRunner(1, NewHub(), nil)

	target := tr.measureEndpointLatency(u.Host, server.URL)

	if !target.LastSuccess {
		t.Fatalf("expected HTTP probe success, got %#v", target)
	}
	if target.ProbeMethod != http.MethodHead {
		t.Fatalf("probe method = %q, want HEAD", target.ProbeMethod)
	}
	if target.HTTPStatus != http.StatusNoContent {
		t.Fatalf("HTTP status = %d, want %d", target.HTTPStatus, http.StatusNoContent)
	}
	if target.ProbeTotalMs <= 0 || target.TTFBMs <= 0 {
		t.Fatalf("expected total and TTFB timings, got total=%v ttfb=%v", target.ProbeTotalMs, target.TTFBMs)
	}
	if target.LatencyMs <= 0 || target.ConnectMs <= 0 {
		t.Fatalf("expected TCP/connect timing to remain populated, got latency=%v connect=%v", target.LatencyMs, target.ConnectMs)
	}
	if target.IP == "" {
		t.Fatal("expected remote IP to be captured")
	}
	if target.RouteStatus != "local" {
		t.Fatalf("route status = %q, want local", target.RouteStatus)
	}
}

func TestMeasureEndpointLatencyFallsBackToGETWhenHEADRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	tr := NewTestRunner(1, NewHub(), nil)

	target := tr.measureEndpointLatency(u.Host, server.URL)

	if !target.LastSuccess {
		t.Fatalf("expected GET fallback success, got %#v", target)
	}
	if target.ProbeMethod != http.MethodGet {
		t.Fatalf("probe method = %q, want GET", target.ProbeMethod)
	}
	if target.HTTPStatus != http.StatusAccepted {
		t.Fatalf("HTTP status = %d, want %d", target.HTTPStatus, http.StatusAccepted)
	}
}

func TestMeasureEndpointLatencyFallsBackToTCPOnHTTPFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
		close(accepted)
	}()

	tr := NewTestRunner(1, NewHub(), nil)
	target := tr.measureEndpointLatency(addr, "http://"+addr)

	if !target.LastSuccess {
		t.Fatalf("expected TCP fallback success, got %#v", target)
	}
	if target.ProbeMethod != "tcp" {
		t.Fatalf("probe method = %q, want tcp", target.ProbeMethod)
	}
	if target.Error == "" {
		t.Fatal("expected HTTP failure context to be retained")
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("expected TCP fallback to connect to listener")
	}
}

func TestLatencyStabilityUsesRollingWindow(t *testing.T) {
	tr := &TestRunner{}
	test := &ManagedTest{ID: "test-latency"}
	now := time.Date(2026, time.June, 2, 12, 0, 0, 0, time.UTC)
	var latest LatencyTarget

	for index := 0; index < latencyStabilityWindowSize+1; index++ {
		latency := &NetworkLatency{Targets: []LatencyTarget{{
			Name:         "edge",
			Endpoint:     "https://example.test",
			LatencyMs:    float64(10 + index),
			ProbeTotalMs: float64(100 + index),
			LastSuccess:  true,
			Timestamp:    now.Add(time.Duration(index) * time.Second),
		}}}
		tr.mu.Lock()
		tr.applyLatencyStabilityLocked(test, latency)
		tr.mu.Unlock()
		latest = latency.Targets[0]
	}

	if latest.SampleCount != latencyStabilityWindowSize {
		t.Fatalf("sample count = %d, want %d", latest.SampleCount, latencyStabilityWindowSize)
	}
	if latest.SuccessRatePct != 100 {
		t.Fatalf("success rate = %v, want 100", latest.SuccessRatePct)
	}
	if latest.JitterMs <= 0 {
		t.Fatalf("expected jitter from varied samples, got %v", latest.JitterMs)
	}

	latency := &NetworkLatency{Targets: []LatencyTarget{{
		Name:        "edge",
		Endpoint:    "https://example.test",
		LastSuccess: false,
		Timestamp:   now.Add(20 * time.Second),
	}}}
	tr.mu.Lock()
	tr.applyLatencyStabilityLocked(test, latency)
	tr.mu.Unlock()
	latest = latency.Targets[0]

	if latest.SampleCount != latencyStabilityWindowSize {
		t.Fatalf("sample count after failure = %d, want %d", latest.SampleCount, latencyStabilityWindowSize)
	}
	wantSuccessRate := float64(latencyStabilityWindowSize-1) / float64(latencyStabilityWindowSize) * 100
	if latest.SuccessRatePct != wantSuccessRate {
		t.Fatalf("success rate = %v, want %v", latest.SuccessRatePct, wantSuccessRate)
	}
}

func TestMeasureNetworkLatency(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	baseURL := endpoint.URL

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
	test.LiveMetrics = live
	tr.tests[test.ID] = test
	tr.measureNetworkLatency(test, live)
	live = test.LiveMetrics

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

func TestLatencyEndpointsForTestCachesDerivedEndpointMapDefensively(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{
		ID: "test-latency",
		Groups: []TestGroup{{
			Name: "Group1",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://app.example.com"},
				Actions: []dsl.Action{{
					Name:   "Health",
					Type:   "GET",
					Method: "GET",
					Target: "https://app.example.com/health",
				}},
			},
		}},
	}

	first := tr.latencyEndpointsForTest(test)
	first["app.example.com"] = "mutated"
	second := tr.latencyEndpointsForTest(test)

	if test.latencyEndpointsCache.signature == "" {
		t.Fatal("expected latency endpoint cache signature to be recorded")
	}
	if got := second["app.example.com"]; got != "https://app.example.com" {
		t.Fatalf("cached endpoint = %q, want defensive cached value", got)
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

func TestLatencyEndpointInlineDataOnlyRequiresPreviewRows(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{
			Name:       "url",
			Type:       "file",
			DataSource: "url",
			Column:     "URL",
		}},
		DataSources: []dsl.DataSource{{
			Name:           "url",
			Type:           "csv",
			InlineData:     "URL\nhttps://preview.example.test\n\"unterminated trailing row",
			FirstRowHeader: true,
		}},
		Actions: []dsl.Action{{
			Name:   "PreviewRows",
			Type:   "GET",
			Method: "GET",
			Target: "{{var.url}}/health",
		}},
	}

	endpoints := make(map[string]string)
	addLatencyEndpoint(endpoints, script)

	if got := endpoints["preview.example.test"]; got != "https://preview.example.test/health" {
		t.Fatalf("parameterized endpoint = %q, want endpoint from first valid data row", got)
	}
}

func TestMeasureNetworkLatencyHydratesScriptByGroupScriptID(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	baseURL := endpoint.URL

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
	test.LiveMetrics = live
	tr.tests[test.ID] = test
	tr.measureNetworkLatency(test, live)
	live = test.LiveMetrics

	if live.Latency == nil || len(live.Latency.Targets) != 1 {
		t.Fatalf("expected hydrated script to produce one latency target, got %#v", live.Latency)
	}
}
