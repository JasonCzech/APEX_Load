package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBuildHTTPProtocolEvidencePopulatesTimings(t *testing.T) {
	if got := buildHTTPProtocolEvidence(nil, nil); got != nil {
		t.Fatalf("nil result produced evidence: %v", got)
	}
	if got := buildHTTPProtocolEvidence(nil, &protocol.Result{}); got != nil {
		t.Fatalf("empty result produced evidence: %v", got)
	}

	result := &protocol.Result{
		HTTPVersionPolicy:     "auto",
		HTTPVersionNegotiated: "3",
		HTTPVersionSource:     "wire",
		Duration:              125500 * time.Microsecond,
		WorkerDuration:        120000 * time.Microsecond,
		DNSTime:               6400 * time.Microsecond,
		ConnectTime:           19800 * time.Microsecond,
		TLSTime:               14200 * time.Microsecond,
		TTFB:                  92100 * time.Microsecond,
	}
	evidence := buildHTTPProtocolEvidence(&protocol.Action{Type: "GET", Target: "https://example.test/orders"}, result)
	if evidence == nil {
		t.Fatal("expected non-nil protocol evidence")
	}
	timings, ok := evidence["timings"].(map[string]any)
	if !ok || timings == nil {
		t.Fatalf("expected timings map in protocol_evidence, got %#v", evidence["timings"])
	}
	want := map[string]float64{
		"duration_ms":        125.5,
		"worker_duration_ms": 120.0,
		"dns_ms":             6.4,
		"connect_ms":         19.8,
		"tls_ms":             14.2,
		"ttfb_ms":            92.1,
	}
	for key, wantVal := range want {
		gotVal, ok := timings[key].(float64)
		if !ok || gotVal != wantVal {
			t.Fatalf("timings[%q] = %v (%T), want %v", key, timings[key], timings[key], wantVal)
		}
	}

	customTimings := map[string]any{"duration_ms": 42.0, "custom_ms": 7.5}
	refreshed := buildHTTPProtocolEvidence(nil, &protocol.Result{
		Duration:         99 * time.Millisecond,
		DNSTime:          5 * time.Millisecond,
		ProtocolEvidence: map[string]any{"timings": customTimings},
	})
	gotTimings, _ := refreshed["timings"].(map[string]any)
	if gotTimings["duration_ms"] != 99.0 || gotTimings["dns_ms"] != 5.0 || gotTimings["custom_ms"] != 7.5 {
		t.Fatalf("expected refreshed timings with preserved custom key, got %#v", refreshed["timings"])
	}
	if customTimings["duration_ms"] != 42.0 {
		t.Fatalf("expected input timings map not to be mutated, got %#v", customTimings)
	}
}

func TestBuildHTTPProtocolTimingsMarksRedirectChains(t *testing.T) {
	redirected := buildHTTPProtocolEvidence(nil, &protocol.Result{
		Duration:        280 * time.Millisecond,
		TTFB:            80 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{RedirectCount: 1, RedirectMs: 85},
	})
	timings, _ := redirected["timings"].(map[string]any)
	if timings["redirect_count"] != 1 || timings["redirect_ms"] != 85.0 {
		t.Fatalf("expected redirect_count/redirect_ms in timings, got %#v", timings)
	}

	// A later pass over a result that followed no redirect drops the stale keys.
	refreshed := buildHTTPProtocolEvidence(nil, &protocol.Result{
		Duration:         90 * time.Millisecond,
		ServerTelemetry:  &protocol.ServerTelemetry{},
		ProtocolEvidence: map[string]any{"timings": timings},
	})
	refreshedTimings, _ := refreshed["timings"].(map[string]any)
	if _, ok := refreshedTimings["redirect_count"]; ok {
		t.Fatalf("stale redirect_count survived a refresh: %#v", refreshedTimings)
	}
	if _, ok := refreshedTimings["redirect_ms"]; ok {
		t.Fatalf("stale redirect_ms survived a refresh: %#v", refreshedTimings)
	}
}

func TestExecuteFollowedRedirectPublishesRedirectTimings(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.Redirect(w, r, "/home", http.StatusFound)
		case "/home":
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "login", Method: "POST", Target: "/login"})
	if err != nil || !result.Success {
		t.Fatalf("Execute = %v, success=%v, error=%q", err, result != nil && result.Success, result.ErrorMessage)
	}
	timings, _ := result.ProtocolEvidence["timings"].(map[string]any)
	if timings["redirect_count"] != 1 {
		t.Fatalf("timings[redirect_count] = %#v, want 1 (timings %#v)", timings["redirect_count"], timings)
	}

	direct, err := h.Execute(context.Background(), &protocol.Action{Name: "home", Method: "GET", Target: "/home"})
	if err != nil || !direct.Success {
		t.Fatalf("Execute = %v, success=%v", err, direct != nil && direct.Success)
	}
	directTimings, _ := direct.ProtocolEvidence["timings"].(map[string]any)
	if _, ok := directTimings["redirect_count"]; ok {
		t.Fatalf("unredirected action published redirect_count: %#v", directTimings)
	}
}
