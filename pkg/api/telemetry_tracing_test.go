package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	apptelemetry "github.com/jasonczech/apexload/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type telemetryCompletionBackend struct{}

func (telemetryCompletionBackend) Run(tr *TestRunner, test *ManagedTest) {
	now := time.Now()
	tr.mu.Lock()
	if tr.tests[test.ID] == test {
		test.Status = "COMPLETE"
		test.StartedAt = &now
		test.EndedAt = &now
	}
	tr.mu.Unlock()
}

func TestHTTPStartRequestPropagatesIntoTestLifecycleSpans(t *testing.T) {
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

	server := NewServer(ServerConfig{
		TracingStatus: apptelemetry.Status{Enabled: true, Ready: true, ServiceName: "test", Protocol: "http/protobuf", Sampler: "always_on"},
	})
	server.runner.setExecutionBackend(telemetryCompletionBackend{})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-otel-lifecycle",
		ProjectID: "project-observability",
		Name:      "OTel lifecycle",
		Protocol:  "http",
		Version:   "1.0.0",
		Actions:   []dsl.Action{{Name: "noop", Type: "GET", Target: "/"}},
	})
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:      "must-not-export-test-name",
		ProjectID: "project-observability",
		Duration:  "1s",
		ScriptID:  "script-otel-lifecycle",
		VUsers:    1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[testRun.ID].ScenarioID = "scenario-checkout"
	server.runner.mu.Unlock()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start?token=must-not-be-exported", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("start response = %d body=%s", response.Code, response.Body.String())
	}

	// The HTTP start response does not join the asynchronous execution goroutine.
	// Wait for its ended span, not just a terminal status written by the backend.
	var spans tracetest.SpanStubs
	byName := make(map[string]tracetest.SpanStub)
	deadline := time.Now().Add(5 * time.Second)
	for {
		spans = exporter.GetSpans()
		for _, span := range spans {
			byName[span.Name] = span
		}
		if _, done := byName["apexload.test.execute"]; done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	httpSpan, httpOK := byName["POST /api/v1/tests/{id}/start"]
	startSpan, startOK := byName["apexload.test.start"]
	executeSpan, executeOK := byName["apexload.test.execute"]
	if !httpOK || !startOK || !executeOK {
		t.Fatalf("span names = %#v, want HTTP, start, and execute spans", spanNames(spans))
	}
	for name, span := range map[string]tracetest.SpanStub{"http": httpSpan, "start": startSpan, "execute": executeSpan} {
		if got := span.SpanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("%s trace ID = %s", name, got)
		}
	}
	if startSpan.Parent.SpanID() != httpSpan.SpanContext.SpanID() {
		t.Fatalf("start parent = %s, want HTTP span %s", startSpan.Parent.SpanID(), httpSpan.SpanContext.SpanID())
	}
	if executeSpan.Parent.SpanID() != startSpan.SpanContext.SpanID() {
		t.Fatalf("execute parent = %s, want start span %s", executeSpan.Parent.SpanID(), startSpan.SpanContext.SpanID())
	}
	if executeSpan.Status.Code != codes.Ok {
		t.Fatalf("execute status = %#v", executeSpan.Status)
	}
	attrs := spanStringAttributes(executeSpan)
	if attrs["apexload.test.id"] != testRun.ID || attrs["apexload.project.id"] != "project-observability" || attrs["apexload.scenario.id"] != "scenario-checkout" || attrs["apexload.test.outcome"] != "complete" || attrs["apexload.test.operation_scope"] != "complete_execution" {
		t.Fatalf("execute attributes = %#v", attrs)
	}
	for _, span := range spans {
		for _, attr := range span.Attributes {
			if attr.Value.String() == "must-not-be-exported" || attr.Value.String() == "must-not-export-test-name" {
				t.Fatalf("sensitive operator text leaked in %s attribute %s", span.Name, attr.Key)
			}
		}
	}
}

func TestExecutionTraceScopeDistinguishesDistributedLaunch(t *testing.T) {
	for _, tt := range []struct {
		name      string
		backend   testExecutionBackend
		wantSpan  string
		wantScope string
	}{
		{name: "local or blocking backend", backend: telemetryCompletionBackend{}, wantSpan: "apexload.test.execute", wantScope: "complete_execution"},
		{name: "distributed backend", backend: newDistributedExecutionBackend(nil, time.Second, nil), wantSpan: "apexload.test.launch", wantScope: "distributed_launch"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spanName, scope := testExecutionSpanDescriptor(tt.backend)
			if spanName != tt.wantSpan || scope != tt.wantScope {
				t.Fatalf("descriptor = (%q, %q), want (%q, %q)", spanName, scope, tt.wantSpan, tt.wantScope)
			}
		})
	}
}

func TestTraceContextReplacementRollbackKeepsPreviousExecutionContextLive(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil, withUnfencedDeploymentLeaseStartForFaultInjection())
	previousCtx, previousCancel := context.WithCancel(context.Background())
	defer previousCancel()
	test := &ManagedTest{
		ID:           "trace-start-persistence-rollback",
		ProjectID:    "project-observability",
		Status:       "CREATED",
		TotalVUsers:  1,
		executionCtx: previousCtx,
		cancel:       previousCancel,
	}
	runner.tests[test.ID] = test
	runner.testStore = failingDistributedStartStore{err: errors.New("database unavailable")}

	runner.mu.Lock()
	prepared, err := runner.prepareTestStartLocked(test.ID, StartTestOptions{
		OverrideCapacityShortfall: true,
		SkipLaunchWindowPreflight: true,
		traceContext:              context.Background(),
	})
	runner.mu.Unlock()
	if err == nil || prepared != nil {
		t.Fatalf("prepare result = (%v, %v), want persistence failure", prepared, err)
	}
	if test.executionCtx != previousCtx || test.Status != "CREATED" {
		t.Fatalf("rollback restored context/status = (%v, %q), want previous/CREATED", test.executionCtx == previousCtx, test.Status)
	}
	if err := previousCtx.Err(); err != nil {
		t.Fatalf("previous execution context was canceled before persistence committed: %v", err)
	}
}

func TestAPMProfileReflectsActualTracingReadiness(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status apptelemetry.Status
		want   string
	}{
		{name: "disabled", status: apptelemetry.Status{}, want: "available"},
		{name: "desktop correlation only", status: apptelemetry.Status{CorrelationOnly: true}, want: "correlation_only"},
		{name: "enabled but not ready", status: apptelemetry.Status{Enabled: true}, want: "unavailable"},
		{name: "exporter ready", status: apptelemetry.Status{Enabled: true, Ready: true}, want: "configured"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			profiles := buildAPMIntegrationProfiles(SystemSettings{}, tt.status)
			for _, profile := range profiles {
				if profile.ID == "opentelemetry" {
					if profile.Status != tt.want {
						t.Fatalf("status = %q, want %q", profile.Status, tt.want)
					}
					return
				}
			}
			t.Fatal("OpenTelemetry profile missing")
		})
	}
}

func spanNames(spans []tracetest.SpanStub) []string {
	names := make([]string, len(spans))
	for index, span := range spans {
		names[index] = span.Name
	}
	return names
}

func spanStringAttributes(span tracetest.SpanStub) map[string]string {
	attrs := make(map[string]string, len(span.Attributes))
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.String()
	}
	return attrs
}
