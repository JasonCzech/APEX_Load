package collectoragent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{CollectorID: "c", Token: "t"}); err == nil {
		t.Fatal("expected error for missing controller URL")
	}
	if _, err := New(Config{ControllerURL: "http://localhost", Token: "t"}); err == nil {
		t.Fatal("expected error for missing collector id")
	}
	if _, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c"}); err == nil {
		t.Fatal("expected error for missing token")
	}
	a, err := New(Config{ControllerURL: "http://localhost/", CollectorID: "c", Token: "t"})
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if a.cfg.ControllerURL != "http://localhost" {
		t.Fatalf("trailing slash not trimmed: %q", a.cfg.ControllerURL)
	}
	if a.cfg.Interval <= 0 || len(a.cfg.Packs) == 0 {
		t.Fatalf("defaults not applied: %+v", a.cfg)
	}
	wantPack := "linux-hosts"
	if runtime.GOOS != "linux" {
		wantPack = "go-runtime"
	}
	if a.cfg.Packs[0] != wantPack {
		t.Fatalf("default pack = %q, want %q", a.cfg.Packs[0], wantPack)
	}
}

func TestNewRejectsCleartextTokenTransport(t *testing.T) {
	cases := []struct {
		name          string
		url           string
		allowInsecure bool
		wantErr       bool
	}{
		{"loopback http ok", "http://127.0.0.1:8080", false, false},
		{"localhost http ok", "http://localhost:8080", false, false},
		{"https ok", "https://controller.example:8080", false, false},
		{"non-loopback http rejected", "http://controller.example:8080", false, true},
		{"non-loopback http allowed with opt-in", "http://controller.example:8080", true, false},
		{"unknown scheme rejected", "ftp://controller.example", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{ControllerURL: tc.url, CollectorID: "c", Token: "t", AllowInsecure: tc.allowInsecure})
			if tc.wantErr && err == nil {
				t.Fatalf("New(%q) = nil error, want rejection", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("New(%q) = %v, want accepted", tc.url, err)
			}
		})
	}
}

func TestHostSamplesNonEmpty(t *testing.T) {
	s := hostSamples()
	if len(s) == 0 {
		t.Fatal("expected non-empty host samples")
	}
	if s["num_cpu"] < 1 {
		t.Fatalf("num_cpu = %v, want >= 1", s["num_cpu"])
	}
}

func TestCollectBuildsPackPerName(t *testing.T) {
	a, _ := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"go-runtime"}})
	hb, err := a.collect(context.Background())
	if err != nil || hb.Health != "healthy" || len(hb.Packs) != 1 {
		t.Fatalf("collect = %+v", hb)
	}
	if hb.Packs[0].Pack != "go-runtime" || len(hb.Packs[0].Samples) == 0 {
		t.Fatalf("pack 0 = %+v", hb.Packs[0])
	}
}

func TestNewRejectsUnimplementedPacks(t *testing.T) {
	for _, pack := range []string{"jvm", "oracle-db", "prometheus"} {
		if _, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{pack}}); err == nil {
			t.Fatalf("pack %q was accepted without an implementation or target", pack)
		}
	}
}

func TestNewRejectsUnsafePrometheusURL(t *testing.T) {
	for _, target := range []string{
		"http://metrics.example.test/metrics",
		"https://user:password@metrics.example.test/metrics",
		"https://metrics.example.test/metrics#secret",
		"https://metrics.example.test/metrics?token=secret",
	} {
		if _, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"prometheus"}, PrometheusURL: target}); err == nil {
			t.Fatalf("unsafe Prometheus URL %q accepted", target)
		}
	}
}

func TestPrometheusScrapeIsSourceSpecificAndBounded(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("# HELP load load\nload 1.5\nqueue_depth 0\nsecret{token=\"value\"} 2\ninvalid NaN\n"))}, nil
	})}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"prometheus"}, PrometheusURL: "http://localhost/metrics", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := a.collect(context.Background())
	if err == nil || hb.Health != "degraded" || len(hb.Packs) != 1 || hb.Packs[0].SkippedSamples != 2 {
		t.Fatalf("collect = %+v, %v", hb, err)
	}
	if got := hb.Packs[0].Samples; len(got) != 2 || got["load"] != 1.5 || got["queue_depth"] != 0 {
		t.Fatalf("samples = %+v", got)
	}
	if _, err := parsePrometheusScalars(strings.Repeat("x", maxPrometheusBodyBytes+1)); err == nil {
		t.Fatal("unsupported samples were accepted")
	}
}

func TestPrometheusFailureDegradesWithoutFalsePack(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"go-runtime", "prometheus"}, PrometheusURL: "http://localhost/metrics", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := a.collect(context.Background())
	if err == nil || hb.Health != "degraded" || len(hb.Packs) != 1 || hb.Packs[0].Pack != "go-runtime" {
		t.Fatalf("collect = %+v, %v", hb, err)
	}
}

func TestPrometheusScrapeDoesNotFollowRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://unapproved.example/metrics"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"prometheus"}, PrometheusURL: "http://localhost/metrics", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prometheusSamples(context.Background()); err == nil || requests != 1 {
		t.Fatalf("redirect result = %v, requests = %d", err, requests)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPushHeartbeatSetsAuthAndPath(t *testing.T) {
	var gotAuth, gotPath string
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var hb heartbeat
		_ = json.NewDecoder(r.Body).Decode(&hb)
		if hb.Health == "" || len(hb.Packs) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	agent, err := New(Config{
		ControllerURL: srv.URL, CollectorID: "collector-7", Token: "srk_abc",
		Interval: time.Hour, Packs: []string{"go-runtime"}, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	hb, err := agent.collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.pushHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("push: %v", err)
	}
	if gotAuth != "Bearer srk_abc" {
		t.Fatalf("Authorization = %q, want Bearer srk_abc", gotAuth)
	}
	if gotPath != "/api/v1/monitoring/collectors/collector-7/heartbeat" {
		t.Fatalf("path = %q", gotPath)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}

func TestPushHeartbeatHandles401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	agent, _ := New(Config{ControllerURL: srv.URL, CollectorID: "c", Token: "bad", Packs: []string{"go-runtime"}, HTTPClient: srv.Client()})
	hb, err := agent.collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.pushHeartbeat(context.Background(), hb); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestPrometheusPreservesTypeAndSourceTimestamp(t *testing.T) {
	pack, err := parsePrometheusScalars("# TYPE count counter\ncount 3 1700000000123\n# TYPE gauge_total gauge\ngauge_total 4\nunknown_total 5\n# TYPE latency histogram\nlatency_sum 6\nlatency_count 7\n")
	if err != nil {
		t.Fatal(err)
	}
	if pack.SampleMetadata["count"].Kind != "counter" || pack.SampleMetadata["gauge_total"].Kind != "gauge" || pack.SampleMetadata["unknown_total"].Kind != "unknown" || pack.SampleMetadata["latency_sum"].Kind != "histogram" || pack.SampleMetadata["latency_count"].Kind != "histogram" {
		t.Fatalf("types: %+v", pack.SampleMetadata)
	}
	at := pack.SampleMetadata["count"].Timestamp
	if at == nil || at.UnixMilli() != 1700000000123 || at.Location() != time.UTC {
		t.Fatalf("timestamp: %v", at)
	}
	if pack.SampleMetadata["gauge_total"].Timestamp != nil {
		t.Fatal("timestamp fabricated")
	}
}

func TestPrometheusRejectsAmbiguousOrOversizeSamples(t *testing.T) {
	var tooMany strings.Builder
	for i := 0; i <= maxPrometheusSamples; i++ {
		fmt.Fprintf(&tooMany, "metric_%d 1\n", i)
	}
	for _, input := range []string{
		"a 1\na 2\n", "a 1 2 extra", "a 1 bad-timestamp", "a 1 9223372036854775807", "a bad",
		"# TYPE a gauge\n# TYPE a counter\na 1", "# TYPE a invented\na 1", tooMany.String(),
		"a 1\n" + strings.Repeat("#", maxPrometheusBodyBytes),
	} {
		if _, err := parsePrometheusScalars(input); err == nil {
			t.Fatalf("invalid scrape accepted: %.80s", input)
		}
	}
}

func TestPrometheusHTTPBodyBoundAndDeadline(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("scrape lacks bounded deadline")
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("controller token sent to scrape endpoint")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("a 1\n" + strings.Repeat("#", maxPrometheusBodyBytes)))}, nil
	})}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "secret", Packs: []string{"prometheus"}, PrometheusURL: "http://localhost/metrics", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prometheusSamples(context.Background()); err == nil {
		t.Fatal("oversized HTTP response accepted")
	}
}

func TestPackConfigurationIsOwnedAndUnique(t *testing.T) {
	cfg := Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"go-runtime"}}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Packs[0] = "prometheus"
	if a.cfg.Packs[0] != "go-runtime" {
		t.Fatal("caller changed validated pack config")
	}
	cfg.Packs = []string{"go-runtime", "go-runtime"}
	if _, err := New(cfg); err == nil {
		t.Fatal("duplicate packs accepted")
	}
}

func TestPrometheusRejectsOtherTimestampFormats(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Accept") != "text/plain; version=0.0.4" {
			t.Fatal("exposition version not requested")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/openmetrics-text; version=1.0.0"}}, Body: io.NopCloser(strings.NewReader("a 1 1700000000\n# EOF\n"))}, nil
	})}
	a, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"prometheus"}, PrometheusURL: "http://localhost/metrics", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prometheusSamples(context.Background()); err == nil {
		t.Fatal("OpenMetrics seconds timestamp interpreted as milliseconds")
	}
}
