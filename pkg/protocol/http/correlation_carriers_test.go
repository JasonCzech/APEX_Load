package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// headerCapture records selected request headers per server.
type headerCapture struct {
	mu      sync.Mutex
	headers []http.Header
}

func (c *headerCapture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headers = append(c.headers, r.Header.Clone())
}

func (c *headerCapture) last(t *testing.T) http.Header {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.headers) == 0 {
		t.Fatal("server received no request")
	}
	return c.headers[len(c.headers)-1]
}

// redirectPair serves an approved origin that redirects to a second,
// unapproved origin.
func redirectPair(t *testing.T) (approved *httptest.Server, other *httptest.Server, approvedSeen, otherSeen *headerCapture) {
	t.Helper()
	approvedSeen, otherSeen = &headerCapture{}, &headerCapture{}
	other = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherSeen.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	approved = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		approvedSeen.record(r)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL+"/landing", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(approved.Close)
	return approved, other, approvedSeen, otherSeen
}

func correlationConfig(origin string, carriers ...string) map[string]any {
	return map[string]any{
		"tls_insecure_skip_verify":         true,
		"apexload_run_correlation_id":      "run-123",
		"apexload_run_correlation_origins": []string{origin},
		"apexload_correlation_carriers":    carriers,
		"apexload_correlation_test_name":   "Checkout peak",
		"apexload_correlation_script_name": "checkout_flow",
	}
}

func TestTraceparentCarrierScopesGeneratedContextToApprovedOrigins(t *testing.T) {
	approved, other, approvedSeen, otherSeen := redirectPair(t)
	cfg := correlationConfig(approved.URL, CorrelationCarrierTraceparent)
	// Request-file imports persist traceparent: false; the run's opt-in wins on
	// approved origins.
	cfg["traceparent_enabled"] = false
	runtime := initHTTP(t, cfg)
	defer runtime.Teardown(context.Background())

	result, err := runtime.Execute(context.Background(), &protocol.Action{Method: http.MethodGet, Target: approved.URL + "/redirect"})
	if err != nil || !result.Success {
		t.Fatalf("Execute: %+v %v", result, err)
	}
	if got := approvedSeen.last(t).Get("Traceparent"); got == "" {
		t.Fatal("approved origin received no traceparent after opt-in")
	}
	if got := otherSeen.last(t).Get("Traceparent"); got != "" {
		t.Fatalf("redirect to an unapproved origin carried generated traceparent %q", got)
	}
	if got := approvedSeen.last(t).Get(ManagedRunHeader); got != "" {
		t.Fatalf("explicit carriers without run_marker still sent %s=%q", ManagedRunHeader, got)
	}

	// A first hop to an unapproved origin gets no generated context at all.
	direct, err := runtime.Execute(context.Background(), &protocol.Action{Method: http.MethodGet, Target: other.URL + "/direct"})
	if err != nil || !direct.Success {
		t.Fatalf("Execute: %+v %v", direct, err)
	}
	if got := otherSeen.last(t).Get("Traceparent"); got != "" || direct.TraceContext != nil {
		t.Fatalf("unapproved origin got traceparent %q (context %#v), want none", got, direct.TraceContext)
	}
}

func TestTraceparentCarrierNeverStripsAuthoredContext(t *testing.T) {
	approved, _, _, otherSeen := redirectPair(t)
	runtime := initHTTP(t, correlationConfig(approved.URL, CorrelationCarrierTraceparent))
	defer runtime.Teardown(context.Background())
	const authored = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	result, err := runtime.Execute(context.Background(), &protocol.Action{Method: http.MethodGet, Target: approved.URL + "/redirect", Headers: map[string]string{"traceparent": authored}})
	if err != nil || !result.Success {
		t.Fatalf("Execute: %+v %v", result, err)
	}
	if got := otherSeen.last(t).Get("Traceparent"); got != authored {
		t.Fatalf("redirected traceparent = %q, want the authored header kept", got)
	}
}

func TestTransactionMarkerCarrier(t *testing.T) {
	approved, _, approvedSeen, otherSeen := redirectPair(t)
	runtime := initHTTP(t, correlationConfig(approved.URL, CorrelationCarrierRunMarker, CorrelationCarrierTransactionMarker))
	defer runtime.Teardown(context.Background())
	ctx := protocol.WithCorrelationScope(context.Background(), protocol.CorrelationScope{VUserID: 7, Transaction: "Submit;order=1"})
	result, err := runtime.Execute(ctx, &protocol.Action{Method: http.MethodGet, Target: approved.URL + "/redirect"})
	if err != nil || !result.Success {
		t.Fatalf("Execute: %+v %v", result, err)
	}
	first := approvedSeen.last(t)
	if got, want := first.Get(TransactionMarkerHeader), "VU=7;SI=APEXLoad;TSN=Submitorder1;LSN=checkout_flow;LTN=Checkout peak"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	if first.Get(ManagedRunHeader) != "run-123" {
		t.Fatalf("run marker = %q, want run-123 alongside the transaction marker", first.Get(ManagedRunHeader))
	}
	if got := otherSeen.last(t).Get(TransactionMarkerHeader); got != "" {
		t.Fatalf("redirect to an unapproved origin carried marker %q", got)
	}
}

func TestTransactionMarkerIsBounded(t *testing.T) {
	policy, err := newRunCorrelationPolicy(map[string]any{
		"apexload_run_correlation_id":      "run-123",
		"apexload_run_correlation_origins": []string{"https://app.example.test"},
		"apexload_correlation_carriers":    []string{CorrelationCarrierTransactionMarker},
		"apexload_correlation_script_name": strings.Repeat("s", 500),
		"apexload_correlation_test_name":   strings.Repeat("t", 500),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := protocol.WithCorrelationScope(context.Background(), protocol.CorrelationScope{VUserID: 1, Transaction: strings.Repeat("x", 500) + "\r\nInjected: yes"})
	marker := policy.transactionMarkerValue(ctx)
	if len(marker) > maxTransactionMarkerBytes || strings.ContainsAny(marker, "\r\n") || strings.Count(marker, "=") > 5 {
		t.Fatalf("marker %q exceeds bounds or carries injected fields", marker)
	}
}

func TestCorrelationCarrierValidation(t *testing.T) {
	tests := []struct {
		name     string
		origins  []string
		carriers []string
		wantErr  string
	}{
		{name: "carriers without origins", carriers: []string{CorrelationCarrierTraceparent}, wantErr: "at least one approved origin"},
		{name: "unknown carrier", origins: []string{"https://app.example.test"}, carriers: []string{"baggage"}, wantErr: "unsupported correlation carrier"},
		{name: "valid carriers", origins: []string{"https://app.example.test"}, carriers: []string{CorrelationCarrierTraceparent, CorrelationCarrierTransactionMarker}},
		{name: "legacy origins only", origins: []string{"https://app.example.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateManagedRunCorrelation("run-123", tt.origins, tt.carriers)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
