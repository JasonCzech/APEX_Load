package generatorrpc

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestOTelGRPCStatsHandlersPropagateAcrossAuthenticatedTransport(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcAuthTokenEnvVar, "transport-otel-token")

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
	)
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	target := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, parent := provider.Tracer("transport-test").Start(ctx, "controller.orchestration")
	client, err := Dial(ctx, target)
	if err != nil {
		parent.End()
		t.Fatalf("Dial: %v", err)
	}
	_, err = client.DistributeScript(ctx, Script{ID: "otel-script", Content: []byte(`{}`), ContentHash: "otel-hash"})
	_ = client.Close()
	parent.End()
	if err != nil {
		t.Fatalf("authenticated DistributeScript: %v", err)
	}

	spans := exporter.GetSpans()
	var clientSpan, serverSpan *tracetest.SpanStub
	for index := range spans {
		span := &spans[index]
		if span.Name != "apexload.v1.GeneratorControl/DistributeScript" {
			continue
		}
		switch span.SpanKind {
		case oteltrace.SpanKindClient:
			clientSpan = span
		case oteltrace.SpanKindServer:
			serverSpan = span
		}
	}
	if clientSpan == nil || serverSpan == nil {
		t.Fatalf("spans = %#v, want client and server DistributeScript spans", grpcSpanSummary(spans))
	}
	if clientSpan.SpanContext.TraceID() != serverSpan.SpanContext.TraceID() {
		t.Fatalf("client trace %s != server trace %s", clientSpan.SpanContext.TraceID(), serverSpan.SpanContext.TraceID())
	}
	if serverSpan.Parent.SpanID() != clientSpan.SpanContext.SpanID() || !serverSpan.Parent.IsRemote() {
		t.Fatalf("server parent = %v, want remote client span %s", serverSpan.Parent, clientSpan.SpanContext.SpanID())
	}
}

type grpcSpanSummaryEntry struct {
	Name string
	Kind oteltrace.SpanKind
}

func grpcSpanSummary(spans []tracetest.SpanStub) []grpcSpanSummaryEntry {
	result := make([]grpcSpanSummaryEntry, len(spans))
	for index, span := range spans {
		result[index] = grpcSpanSummaryEntry{Name: span.Name, Kind: span.SpanKind}
	}
	return result
}
