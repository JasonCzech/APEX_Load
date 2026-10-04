package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func withActionSampler(t *testing.T, configured bool, threshold uint64) {
	t.Helper()
	previousConfigured := actionSpanSampling.configured.Load()
	previousThreshold := actionSpanSampling.threshold.Load()
	previousSequence := actionSpanSampling.sequence.Load()
	t.Cleanup(func() {
		actionSpanSampling.configured.Store(previousConfigured)
		actionSpanSampling.threshold.Store(previousThreshold)
		actionSpanSampling.sequence.Store(previousSequence)
	})
	actionSpanSampling.configured.Store(configured)
	actionSpanSampling.threshold.Store(threshold)
}

func TestNewTransactionRootWithoutExportStillPropagatesFreshTraces(t *testing.T) {
	withActionSampler(t, false, 0)
	run := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})
	parent := trace.ContextWithSpanContext(context.Background(), run)

	first, endFirst := NewTransactionRoot(parent, "Checkout", true)
	endFirst()
	second, endSecond := NewTransactionRoot(parent, "Checkout", false)
	endSecond()
	a, b := trace.SpanContextFromContext(first), trace.SpanContextFromContext(second)
	if !a.IsValid() || !b.IsValid() || a.TraceID() == run.TraceID() || a.TraceID() == b.TraceID() {
		t.Fatalf("roots = %v and %v, want two fresh traces apart from the run trace", a.TraceID(), b.TraceID())
	}
	if !a.IsSampled() || b.IsSampled() {
		t.Fatalf("sampled flags = %v and %v, want the requested preference while not exporting", a.IsSampled(), b.IsSampled())
	}
}

func TestNewTransactionRootExportsAdmittedRootsLinkedToTheRun(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	runCtx, runSpan := provider.Tracer("test").Start(context.Background(), "apexload.test")
	defer runSpan.End()

	withActionSampler(t, true, actionSampleScale)
	rootCtx, end := NewTransactionRoot(runCtx, "Checkout", true)
	root := trace.SpanContextFromContext(rootCtx)
	end()
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "apexload.transaction" {
		t.Fatalf("ended spans = %d, want one exported transaction root", len(ended))
	}
	if root.TraceID() == runSpan.SpanContext().TraceID() || ended[0].Parent().IsValid() {
		t.Fatal("transaction root was parented by the run instead of starting a new trace")
	}
	if links := ended[0].Links(); len(links) != 1 || links[0].SpanContext.TraceID() != runSpan.SpanContext().TraceID() {
		t.Fatalf("links = %+v, want one link to the run span", links)
	}

	// While exporting, an unadmitted root stays unsampled so no action span is
	// exported under a parent that never was.
	withActionSampler(t, true, 0)
	skipped, endSkipped := NewTransactionRoot(runCtx, "Checkout", true)
	endSkipped()
	if sc := trace.SpanContextFromContext(skipped); !sc.IsValid() || sc.IsSampled() || len(recorder.Ended()) != 1 {
		t.Fatalf("unadmitted root = %+v (ended %d), want a non-exported unsampled root", sc, len(recorder.Ended()))
	}
}
