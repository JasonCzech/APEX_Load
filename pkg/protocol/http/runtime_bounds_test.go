package http

import (
	"context"
	"errors"
	"fmt"
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

type runtimeBoundsRoundTripFunc func(*stdhttp.Request) (*stdhttp.Response, error)

func (roundTrip runtimeBoundsRoundTripFunc) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return roundTrip(request)
}

func TestHTTPExecuteRejectsUnboundedActionControlsBeforeRequest(t *testing.T) {
	var calls atomic.Int64
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	h.client.Transport = runtimeBoundsRoundTripFunc(func(*stdhttp.Request) (*stdhttp.Response, error) {
		calls.Add(1)
		return nil, errors.New("invalid action reached transport")
	})
	tooManyRedirects := MaxConfiguredRedirects + 1
	tooManyExtractors := make([]protocol.Extractor, maxHTTPActionExtractors+1)
	cases := []struct {
		name   string
		action *protocol.Action
		want   string
	}{
		{name: "nil", action: nil, want: "required"},
		{name: "timeout", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", TimeoutMs: int(MaxConfiguredTimeout/time.Millisecond) + 1}, want: "timeout_ms"},
		{name: "redirects", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", MaxRedirects: &tooManyRedirects}, want: "max_redirects"},
		{name: "sse timeout", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", Params: map[string]any{"stream_timeout_ms": float64(MaxConfiguredTimeout/time.Millisecond) + 1}}, want: "stream_timeout_ms"},
		{name: "fractional sse timeout", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", Params: map[string]any{"stream_timeout_ms": 1.5}}, want: "stream_timeout_ms"},
		{name: "extractor expression", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", Extractors: []protocol.Extractor{{Name: "oversized", Expression: strings.Repeat("x", MaxExtractorExpressionBytes+1)}}}, want: "expression"},
		{name: "extractor count", action: &protocol.Action{Name: "bad", Type: "request", Method: "GET", Target: "/", Extractors: tooManyExtractors}, want: "extractors"},
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

func TestExtractorInputWorkBudgetChargesEveryScan(t *testing.T) {
	budget := &extractorInputWorkBudget{limit: 26, bodyBytes: 6, headerBytes: 4}
	if err := budget.charge(protocol.Extractor{Source: "body", Type: "regex"}); err != nil {
		t.Fatalf("first body scan: %v", err)
	}
	if err := budget.charge(protocol.Extractor{Source: "body", Type: "jsonpath"}); err != nil {
		t.Fatalf("structured body preflight plus parser: %v", err)
	}
	if err := budget.charge(protocol.Extractor{Source: "header", Type: "regex"}); err != nil {
		t.Fatalf("first header scan: %v", err)
	}
	if err := budget.charge(protocol.Extractor{Source: "header", Type: "header"}); err != nil {
		t.Fatalf("direct header lookup: %v", err)
	}
	if err := budget.charge(protocol.Extractor{Source: "header", Type: "header", Mode: "all"}); err != nil {
		t.Fatalf("mode-all header scan: %v", err)
	}
	if got := budget.used; got != 26 {
		t.Fatalf("budget used = %d, want one body scan, one two-pass structured parse, and two header scans (26)", got)
	}

	// Two maximum-sized single-pass scans fit exactly; a third is rejected.
	maximum := &extractorInputWorkBudget{
		limit:     maxExtractorInputWorkBytes,
		bodyBytes: hardHTTPMaxBodyBytes,
	}
	if err := maximum.charge(protocol.Extractor{Source: "body", Type: "regex"}); err != nil {
		t.Fatalf("first maximum-sized body extractor: %v", err)
	}
	if err := maximum.charge(protocol.Extractor{Source: "body", Type: "regex"}); err != nil {
		t.Fatalf("second maximum-sized body extractor: %v", err)
	}
	if err := maximum.charge(protocol.Extractor{Source: "body", Type: "css"}); !errors.Is(err, errExtractorResourceLimit) {
		t.Fatalf("third maximum-sized body extractor error = %v, want resource limit", err)
	}

	structuredMaximum := &extractorInputWorkBudget{
		limit:     maxExtractorInputWorkBytes,
		bodyBytes: hardHTTPMaxBodyBytes,
	}
	if err := structuredMaximum.charge(protocol.Extractor{Source: "body", Type: "jsonpath"}); err != nil {
		t.Fatalf("one maximum-sized structured extractor: %v", err)
	}
	if got := structuredMaximum.used; got != maxExtractorInputWorkBytes {
		t.Fatalf("structured extractor charged %d bytes, want both passes (%d)", got, maxExtractorInputWorkBytes)
	}

	xpathMaximum := &extractorInputWorkBudget{limit: 40, bodyBytes: 10}
	if err := xpathMaximum.charge(protocol.Extractor{Source: "body", Type: "xpath"}); err != nil {
		t.Fatalf("four-pass XPath extractor: %v", err)
	}
	if got := xpathMaximum.used; got != 40 {
		t.Fatalf("XPath extractor charged %d bytes, want four passes (40)", got)
	}
	xpathOverBudget := &extractorInputWorkBudget{limit: 40, bodyBytes: 11}
	if err := xpathOverBudget.charge(protocol.Extractor{Source: "body", Type: "xpath"}); !errors.Is(err, errExtractorResourceLimit) {
		t.Fatalf("XPath work beyond four-pass budget error = %v, want resource limit", err)
	}

	overBudget := &extractorInputWorkBudget{limit: 5, bodyBytes: 6}
	if err := overBudget.charge(protocol.Extractor{Source: "body", Type: "regex"}); !errors.Is(err, errExtractorResourceLimit) {
		t.Fatalf("surface beyond the budget error = %v, want resource limit", err)
	}
}

func TestExtractorInputWorkBudgetChargesOnlyNamedDirectHeaderValues(t *testing.T) {
	headers := stdhttp.Header{}
	headers.Add("X-Target", "one")
	headers.Add("X-Target", "two")
	headers.Add("X-Unrelated", strings.Repeat("x", 1024))
	budget := newExtractorInputWorkBudget(headers, nil)

	if err := budget.charge(protocol.Extractor{
		Source:     "header",
		Type:       "header",
		Expression: "x-target",
		Mode:       "all",
	}); err != nil {
		t.Fatalf("direct header mode-all charge: %v", err)
	}
	if got, want := budget.used, int64(len("one")+len("two")); got != want {
		t.Fatalf("direct header budget used = %d, want named values only (%d)", got, want)
	}

	if err := budget.charge(protocol.Extractor{
		Source:     "header",
		Type:       "header",
		Expression: "X-Missing",
		Mode:       "all",
	}); err != nil {
		t.Fatalf("missing direct header mode-all charge: %v", err)
	}
	if got, want := budget.used, int64(len("one")+len("two")); got != want {
		t.Fatalf("missing direct header changed budget to %d, want %d", got, want)
	}
}

func TestExtractorInputWorkBudgetRejectsOversizedExpression(t *testing.T) {
	budget := &extractorInputWorkBudget{limit: maxExtractorInputWorkBytes, bodyBytes: 1}
	err := budget.charge(protocol.Extractor{
		Source:     "body",
		Type:       "regex",
		Expression: strings.Repeat("x", MaxExtractorExpressionBytes+1),
	})
	if !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "expression") {
		t.Fatalf("oversized expression error = %v, want extractor expression resource limit", err)
	}
	if budget.used != 0 {
		t.Fatalf("oversized expression consumed %d input bytes before rejection", budget.used)
	}

	atLimit := &extractorInputWorkBudget{limit: 1, bodyBytes: 1}
	err = atLimit.charge(protocol.Extractor{
		Source:     "body",
		Type:       "regex",
		Expression: strings.Repeat("x", MaxExtractorExpressionBytes),
	})
	if err != nil {
		t.Fatalf("expression at the byte limit was rejected: %v", err)
	}
}

func TestHTTPExecuteRejectsAggregateExtractorWork(t *testing.T) {
	const responseBytes = 4 << 20
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", responseBytes))
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())

	extractors := make([]protocol.Extractor, 0, maxExtractorInputWorkBytes/responseBytes+1)
	for index := 0; index < cap(extractors); index++ {
		extractors = append(extractors, protocol.Extractor{
			Name:       "missing",
			Source:     "body",
			Type:       "regex",
			Expression: `(never-matches)`,
		})
	}
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:       "bounded extraction work",
		Type:       "GET",
		Target:     "/",
		Extractors: extractors,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "aggregate extractor input") {
		t.Fatalf("result = success:%t error:%q, want aggregate extractor input rejection", result.Success, result.ErrorMessage)
	}
}

func TestHTTPExecuteAllowsMaximumExtractorsOverModestBody(t *testing.T) {
	const responseBytes = 256 << 10
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", responseBytes))
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())

	const extractorCount = 256
	extractors := make([]protocol.Extractor, 0, extractorCount)
	for index := 0; index < extractorCount; index++ {
		extractors = append(extractors, protocol.Extractor{
			Name:       fmt.Sprintf("missing_%d", index),
			Source:     "body",
			Type:       "regex",
			Expression: `(never-matches)`,
		})
	}
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:       "bounded extraction work",
		Type:       "GET",
		Target:     "/",
		Extractors: extractors,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("result = success:%t error:%q, want bounded multi-extractor action to succeed", result.Success, result.ErrorMessage)
	}
}

func TestHTTPExecuteUnknownTypeMakesNoRequest(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	client := initHTTP(t, map[string]any{"base_url": server.URL})
	for _, actionType := range []string{"pots", "post_order", "db_query", "request"} {
		result, _ := client.Execute(context.Background(), &protocol.Action{Name: "bad", Type: actionType, Target: "/"})
		if result.Success || !strings.Contains(result.ErrorMessage, "unsupported HTTP method") {
			t.Fatalf("unexpected result: %#v", result)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unknown action sent a request")
	}
	result, err := client.Execute(context.Background(), &protocol.Action{Name: "explicit", Type: "request", Method: "GET", Target: "/"})
	if err != nil || !result.Success || calls.Load() != 1 {
		t.Fatalf("explicit method failed: %v, %#v", err, result)
	}
}
