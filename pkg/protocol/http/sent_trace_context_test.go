package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel/trace"
)

// traceRecordingServer records the traceparent of every request it receives.
func traceRecordingServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var received []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("Traceparent"))
		mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), received...)
	}
}

func sentTraceID(t *testing.T, header string) string {
	t.Helper()
	traceID, _, _, ok := protocol.ParseTraceparent(header)
	if !ok {
		t.Fatalf("received traceparent %q is not valid", header)
	}
	return traceID
}

func TestHTTPSentTraceContextSurvivesGatewayRequestID(t *testing.T) {
	ts, received := traceRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "gateway-request-7")
		w.WriteHeader(http.StatusOK)
	})
	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "gateway", Method: "GET", Target: "/"})
	if err != nil {
		t.Fatal(err)
	}
	// CorrelationID keeps its existing meaning: the response's request ID wins.
	if result.CorrelationID != "gateway-request-7" {
		t.Fatalf("CorrelationID = %q, want the gateway request ID", result.CorrelationID)
	}
	sent := result.TraceContext
	if sent == nil || !sent.Sent || sent.Source != protocol.TraceSourceGenerated || sent.TraceID != sentTraceID(t, received()[0]) {
		t.Fatalf("TraceContext = %#v, want the generated context that was sent (%q)", sent, received()[0])
	}
}

func TestHTTPSentTraceContextCacheHitsAndRevalidation(t *testing.T) {
	t.Run("fresh hit sends nothing", func(t *testing.T) {
		ts, received := traceRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "max-age=60")
			_, _ = w.Write([]byte("cached"))
		})
		h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
		first := executeCacheAction(t, h)
		second := executeCacheAction(t, h)
		if len(received()) != 1 {
			t.Fatalf("server requests = %d, want 1", len(received()))
		}
		if first.TraceContext == nil || !first.TraceContext.Sent {
			t.Fatalf("first TraceContext = %#v, want sent", first.TraceContext)
		}
		if second.TraceContext == nil || second.TraceContext.Sent {
			t.Fatalf("cache-hit TraceContext = %#v, want recorded but not sent", second.TraceContext)
		}
	})
	t.Run("revalidation sends its context", func(t *testing.T) {
		calls := 0
		ts, received := traceRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Cache-Control", "max-age=0")
				w.Header().Set("ETag", `"v1"`)
				_, _ = w.Write([]byte("cached"))
				return
			}
			w.WriteHeader(http.StatusNotModified)
		})
		h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
		executeCacheAction(t, h)
		second := executeCacheAction(t, h)
		if len(received()) != 2 {
			t.Fatalf("server requests = %d, want initial request plus revalidation", len(received()))
		}
		if second.TraceContext == nil || !second.TraceContext.Sent || second.TraceContext.TraceID != sentTraceID(t, received()[1]) {
			t.Fatalf("revalidated TraceContext = %#v, want the context sent with the 304 request", second.TraceContext)
		}
	})
}

func TestHTTPSentTraceContextSources(t *testing.T) {
	const authored = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	t.Run("authored header is recorded as authored", func(t *testing.T) {
		ts, _ := traceRecordingServer(t, ok)
		h := initHTTP(t, map[string]any{"base_url": ts.URL})
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "authored", Method: "GET", Target: "/", Headers: map[string]string{"traceparent": authored}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TraceContext == nil || result.TraceContext.Source != protocol.TraceSourceAuthored || result.TraceContext.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("TraceContext = %#v, want the authored context", result.TraceContext)
		}
	})
	t.Run("active span is recorded as propagated", func(t *testing.T) {
		ts, received := traceRecordingServer(t, ok)
		h := initHTTP(t, map[string]any{"base_url": ts.URL})
		traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
		spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
		ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
		result, err := h.Execute(ctx, &protocol.Action{Name: "propagated", Method: "GET", Target: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if result.TraceContext == nil || result.TraceContext.Source != protocol.TraceSourcePropagated || result.TraceContext.TraceID != traceID.String() || !strings.Contains(received()[0], traceID.String()) {
			t.Fatalf("TraceContext = %#v (sent %q), want the propagated span context", result.TraceContext, received()[0])
		}
	})
	t.Run("disabled propagation records nothing", func(t *testing.T) {
		ts, received := traceRecordingServer(t, ok)
		h := initHTTP(t, map[string]any{"base_url": ts.URL, "traceparent_enabled": false})
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "disabled", Method: "GET", Target: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if received()[0] != "" || result.TraceContext != nil {
			t.Fatalf("sent %q with TraceContext %#v, want no context", received()[0], result.TraceContext)
		}
	})
}

func TestHTTPSentTraceContextNotSentWhenDialFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close()
	h := initHTTP(t, map[string]any{"base_url": url})
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "refused", Method: "GET", Target: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.TraceContext == nil {
		t.Fatalf("result = %#v, want a failed request that still records its context", result)
	}
	if result.TraceContext.Sent || result.TraceContext.LookupTraceID() != "" {
		t.Fatalf("TraceContext = %#v, want not sent after a refused dial", result.TraceContext)
	}
}
