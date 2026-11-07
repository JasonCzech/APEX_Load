package http

import (
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
