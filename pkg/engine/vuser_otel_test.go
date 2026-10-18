package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type actionTraceProtocol struct {
	context oteltrace.SpanContext
	result  *protocol.Result
}

func (*actionTraceProtocol) Name() string                               { return "trace-stub" }
func (*actionTraceProtocol) Init(context.Context, map[string]any) error { return nil }
func (*actionTraceProtocol) Teardown(context.Context) error             { return nil }
func (*actionTraceProtocol) HealthCheck(context.Context) error          { return nil }
func (p *actionTraceProtocol) Execute(ctx context.Context, _ *protocol.Action) (*protocol.Result, error) {
	p.context = oteltrace.SpanContextFromContext(ctx)
	return p.result, nil
}

func TestActionSpanWrapsProtocolWithoutExportingRequestSecrets(t *testing.T) {
	for _, tt := range []struct {
		name       string
		result     *protocol.Result
		wantStatus codes.Code
	}{
		{name: "success", result: &protocol.Result{Success: true, StatusCode: 204}, wantStatus: codes.Ok},
		{name: "unsuccessful", result: &protocol.Result{Success: false, StatusCode: 503, ErrorMessage: "Bearer must-not-export"}, wantStatus: codes.Error},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
			)
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			defer func() {
				_ = provider.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			}()

			ctx, parent := provider.Tracer("engine-test").Start(context.Background(), "test.run")
			stub := &actionTraceProtocol{result: tt.result}
			vu := &VUser{
				Group:  "must-not-export-group-name",
				Proto:  stub,
				Script: &dsl.Script{ID: "must-not-export-script-id", Protocol: "http"},
			}
			result, err := vu.executeWithRetry(ctx, &protocol.Action{
				Name:    "must-not-export-action-name",
				Type:    "POST",
				Target:  "https://secret.example/orders?token=must-not-export",
				Headers: map[string]string{"Authorization": "Bearer must-not-export"},
			}, dsl.ErrorHandlingConfig{})
			parent.End()
			if err != nil || result != tt.result {
				t.Fatalf("executeWithRetry = (%p, %v), want (%p, nil)", result, err, tt.result)
			}
			spans := exporter.GetSpans()
			var actionSpan *tracetest.SpanStub
			for index := range spans {
				if spans[index].Name == "apexload.action" {
					actionSpan = &spans[index]
					break
				}
			}
			if actionSpan == nil {
				t.Fatalf("span names = %v, want apexload.action", engineSpanNames(spans))
			}
			if stub.context.SpanID() != actionSpan.SpanContext.SpanID() {
				t.Fatalf("protocol span = %s, want action span %s", stub.context.SpanID(), actionSpan.SpanContext.SpanID())
			}
			if actionSpan.Status.Code != tt.wantStatus {
				t.Fatalf("status = %#v, want %v", actionSpan.Status, tt.wantStatus)
			}
			attrs := make(map[string]string, len(actionSpan.Attributes))
			for _, attr := range actionSpan.Attributes {
				attrs[string(attr.Key)] = attr.Value.String()
				if strings.Contains(attr.Value.String(), "must-not-export") || strings.Contains(attr.Value.String(), "secret.example") {
					t.Fatalf("request secret leaked into %s=%q", attr.Key, attr.Value.String())
				}
			}
			if attrs["apexload.action.type"] != "post" || attrs["apexload.protocol"] != "http" || attrs["apexload.action.attempts"] != "1" {
				t.Fatalf("action attributes = %#v", attrs)
			}
		})
	}
}

func engineSpanNames(spans []tracetest.SpanStub) []string {
	names := make([]string, len(spans))
	for index, span := range spans {
		names[index] = span.Name
	}
	return names
}
