package http

import (
	"context"
	"io"
	"math"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPExecuteRejectsUnboundedActionControlsBeforeRequest(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		calls.Add(1)
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	tooManyRedirects := MaxConfiguredRedirects + 1
	cases := []struct {
		name   string
		action *protocol.Action
		want   string
	}{
		{name: "nil", action: nil, want: "required"},
		{name: "timeout", action: &protocol.Action{Name: "bad", Type: "request", Target: "/", TimeoutMs: int(MaxConfiguredTimeout/time.Millisecond) + 1}, want: "timeout_ms"},
		{name: "redirects", action: &protocol.Action{Name: "bad", Type: "request", Target: "/", MaxRedirects: &tooManyRedirects}, want: "max_redirects"},
		{name: "sse timeout", action: &protocol.Action{Name: "bad", Type: "request", Target: "/", Params: map[string]any{"stream_timeout_ms": float64(MaxConfiguredTimeout/time.Millisecond) + 1}}, want: "stream_timeout_ms"},
		{name: "fractional sse timeout", action: &protocol.Action{Name: "bad", Type: "request", Target: "/", Params: map[string]any{"stream_timeout_ms": 1.5}}, want: "stream_timeout_ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := h.Execute(context.Background(), tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || result.StatusCode != stdhttp.StatusBadRequest || !strings.Contains(result.ErrorMessage, tc.want) {
				t.Fatalf("result = %#v, want 400 containing %q", result, tc.want)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid actions made %d outbound request(s)", got)
	}
}

func TestHTTPInitRejectsUnboundedSimulationControls(t *testing.T) {
	cases := []map[string]any{
		{"web_network_simulation_latency_ms": float64(MaxConfiguredTimeout/time.Millisecond) + 1},
		{"web_network_simulation_jitter_ms": float64(MaxConfiguredTimeout/time.Millisecond) + 1},
		{"web_network_simulation_bandwidth_kbps": MaxConfiguredNetworkKbps + 1},
		{"web_network_simulation_packet_loss_pct": math.NaN()},
		{"web_streaming_buffer_size_seconds": int(MaxConfiguredStreamBuffer/time.Second) + 1},
		{"web_streaming_fixed_bitrate_kbps": MaxConfiguredStreamKbps + 1},
	}
	for _, config := range cases {
		h := &HTTP{}
		if err := h.Init(context.Background(), config); err == nil {
			t.Fatalf("Init(%v) succeeded, want bound rejection", config)
		}
	}
}

func TestReadSSEResponseRejectsUnboundedTimeout(t *testing.T) {
	body := io.NopCloser(strings.NewReader(""))
	_, err := readSSEResponse(context.Background(), body, map[string]any{
		"stream_timeout_ms": float64(MaxConfiguredTimeout/time.Millisecond) + 1,
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "stream_timeout_ms") {
		t.Fatalf("readSSEResponse error = %v, want timeout bound rejection", err)
	}
}
