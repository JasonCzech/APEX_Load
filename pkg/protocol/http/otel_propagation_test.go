package http

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestHTTPInjectsActiveOTelContextAndPreservesExplicitTraceparent(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	received := make(chan string, 5)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("traceparent")
		if r.URL.Path == "/active-response-correlation" {
			w.Header().Set("X-Request-Id", "response-correlation")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	client := &HTTP{}
	if err := client.Init(context.Background(), map[string]any{
		"base_url":            target.URL,
		"traceparent_enabled": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	ctx, parent := provider.Tracer("http-propagation-test").Start(context.Background(), "apexload.action")
	result, err := client.Execute(ctx, &protocol.Action{Name: "active", Type: http.MethodGet, Target: "/active"})
	if err != nil {
		parent.End()
		t.Fatalf("Execute(active): %v", err)
	}
	activeTraceparent := <-received
	if activeTraceparent == "" {
		parent.End()
		t.Fatal("active context was not injected")
	}
	spanContext := parent.SpanContext()
	want := "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-01"
	if activeTraceparent != want || result.CorrelationID != want {
		parent.End()
		t.Fatalf("active traceparent/result = (%q, %q), want %q", activeTraceparent, result.CorrelationID, want)
	}
	result, err = client.Execute(ctx, &protocol.Action{
		Name:    "active-with-request-id",
		Type:    http.MethodGet,
		Target:  "/active-with-request-id",
		Headers: map[string]string{"X-Request-Id": "request-correlation"},
	})
	if err != nil {
		parent.End()
		t.Fatalf("Execute(active with request ID): %v", err)
	}
	if got := <-received; got != want {
		parent.End()
		t.Fatalf("traceparent with X-Request-Id = %q, want active context %q", got, want)
	}
	if result.CorrelationID != "request-correlation" {
		parent.End()
		t.Fatalf("CorrelationID with X-Request-Id = %q, want existing request correlation", result.CorrelationID)
	}
	result, err = client.Execute(ctx, &protocol.Action{
		Name:   "active-with-response-correlation",
		Type:   http.MethodGet,
		Target: "/active-response-correlation",
	})
	if err != nil {
		parent.End()
		t.Fatalf("Execute(active with response correlation): %v", err)
	}
	if got := <-received; got != want {
		parent.End()
		t.Fatalf("traceparent with response X-Request-Id = %q, want active context %q", got, want)
	}
	if result.CorrelationID != "response-correlation" {
		parent.End()
		t.Fatalf("CorrelationID with response X-Request-Id = %q, want response correlation", result.CorrelationID)
	}

	const explicit = "00-11111111111111111111111111111111-2222222222222222-00"
	result, err = client.Execute(ctx, &protocol.Action{
		Name:    "explicit",
		Type:    http.MethodGet,
		Target:  "/explicit",
		Headers: map[string]string{"traceparent": explicit},
	})
	parent.End()
	if err != nil {
		t.Fatalf("Execute(explicit): %v", err)
	}
	if got := <-received; got != explicit || result.CorrelationID != explicit {
		t.Fatalf("explicit traceparent/result = (%q, %q), want %q", got, result.CorrelationID, explicit)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for closed transport target: %v", err)
	}
	closedTarget := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close transport target: %v", err)
	}
	transportFailure := &HTTP{}
	if err := transportFailure.Init(context.Background(), map[string]any{
		"base_url":            closedTarget,
		"traceparent_enabled": true,
		"request_timeout_ms":  250,
	}); err != nil {
		t.Fatalf("Init(transport failure): %v", err)
	}
	defer transportFailure.Teardown(context.Background())
	ctx, failureParent := provider.Tracer("http-propagation-test").Start(context.Background(), "transport-failure-parent")
	result, err = transportFailure.Execute(ctx, &protocol.Action{Name: "transport-failure", Type: http.MethodGet, Target: "/failure"})
	failureParent.End()
	if err != nil {
		t.Fatalf("Execute(transport failure): %v", err)
	}
	wantFailure := "00-" + failureParent.SpanContext().TraceID().String() + "-" + failureParent.SpanContext().SpanID().String() + "-01"
	if result.ErrorMessage == "" {
		t.Fatal("transport failure result did not report an error")
	}
	if result.CorrelationID != wantFailure {
		t.Fatalf("transport failure CorrelationID = %q, want %q", result.CorrelationID, wantFailure)
	}

	disabled := &HTTP{}
	if err := disabled.Init(context.Background(), map[string]any{
		"base_url":            target.URL,
		"traceparent_enabled": false,
	}); err != nil {
		t.Fatalf("Init(disabled): %v", err)
	}
	defer disabled.Teardown(context.Background())
	ctx, disabledParent := provider.Tracer("http-propagation-test").Start(context.Background(), "disabled-parent")
	result, err = disabled.Execute(ctx, &protocol.Action{Name: "disabled", Type: http.MethodGet, Target: "/disabled"})
	disabledParent.End()
	if err != nil {
		t.Fatalf("Execute(disabled): %v", err)
	}
	if got := <-received; got != "" || result.CorrelationID != "" {
		t.Fatalf("disabled traceparent/result = (%q, %q), want empty", got, result.CorrelationID)
	}
}
