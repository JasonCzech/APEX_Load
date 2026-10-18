package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestLoadConfigDisabledByDefault(t *testing.T) {
	cfg, err := LoadConfig(func(string) string { return "" }, "apexload-controller", "1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("tracing must remain disabled without APEXLOAD_OTEL_ENABLED")
	}
	if cfg.ServiceName != "apexload-controller" || cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("config identity = %#v", cfg)
	}
}

func TestDetachNilReturnsUsableBackgroundContext(t *testing.T) {
	detached := Detach(nil) //nolint:staticcheck // the nil context is the input under test
	if detached == nil {
		t.Fatal("Detach(nil) returned nil")
	}
	if err := detached.Err(); err != nil {
		t.Fatalf("Detach(nil).Err() = %v, want nil", err)
	}
	if got := oteltrace.SpanContextFromContext(detached); got.IsValid() {
		t.Fatalf("Detach(nil) retained unexpected span context %v", got)
	}
}

func TestLoadConfigUsesStandardOTLPAndSamplerEnvironment(t *testing.T) {
	values := map[string]string{
		EnabledEnv:                           "true",
		"OTEL_SERVICE_NAME":                  "controller-prod",
		"OTEL_EXPORTER_OTLP_ENDPOINT":        "https://collector.example:4317",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "grpc",
		"OTEL_TRACES_SAMPLER":                "parentbased_traceidratio",
		"OTEL_TRACES_SAMPLER_ARG":            "0.25",
		"APEXLOAD_ENV":                       "production",
	}
	cfg, err := LoadConfig(func(key string) string { return values[key] }, "fallback", "2.0.0")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Enabled || cfg.Endpoint != values["OTEL_EXPORTER_OTLP_ENDPOINT"] || cfg.Protocol != "grpc" || cfg.SampleRatio != 0.25 {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.ServiceName != "controller-prod" || cfg.Environment != "production" {
		t.Fatalf("resource identity = %#v", cfg)
	}
	if cfg.ActionSampleRatio != defaultActionSampleRatio {
		t.Fatalf("action sample ratio = %v, want %v", cfg.ActionSampleRatio, defaultActionSampleRatio)
	}
}

func TestActionSpanSamplerDistributesAndHonorsBoundaries(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	}()
	ctx, parent := provider.Tracer("sampler-test").Start(context.Background(), "sampled")
	defer parent.End()

	previousConfigured := actionSpanSampling.configured.Load()
	previousThreshold := actionSpanSampling.threshold.Load()
	previousSequence := actionSpanSampling.sequence.Load()
	defer func() {
		actionSpanSampling.configured.Store(previousConfigured)
		actionSpanSampling.threshold.Store(previousThreshold)
		actionSpanSampling.sequence.Store(previousSequence)
	}()
	actionSpanSampling.configured.Store(true)

	actionSpanSampling.threshold.Store(0)
	if ShouldStartActionSpan(ctx) {
		t.Fatal("zero action ratio sampled a span")
	}
	actionSpanSampling.threshold.Store(actionSampleScale)
	if !ShouldStartActionSpan(ctx) {
		t.Fatal("one action ratio dropped a span")
	}

	actionSpanSampling.threshold.Store(actionSampleScale / 100)
	actionSpanSampling.sequence.Store(0)
	sampled := 0
	firstWindow := 0
	const attempts = 100_000
	for index := 0; index < attempts; index++ {
		if ShouldStartActionSpan(ctx) {
			sampled++
			if index < 100 {
				firstWindow++
			}
		}
	}
	if sampled < 800 || sampled > 1200 {
		t.Fatalf("one-percent sampler retained %d/%d actions", sampled, attempts)
	}
	if firstWindow > 5 {
		t.Fatalf("sampler burst retained %d of its first 100 actions", firstWindow)
	}
}

func TestLoadConfigRejectsUnsafeOrIncompleteEnabledConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{name: "missing endpoint", values: map[string]string{EnabledEnv: "true"}, want: "OTEL_EXPORTER_OTLP"},
		{name: "credentials in endpoint", values: map[string]string{EnabledEnv: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://user:pass@collector.example"}, want: "must not contain credentials"},
		{name: "query in endpoint", values: map[string]string{EnabledEnv: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example?token=secret"}, want: "query string"},
		{name: "unsupported protocol", values: map[string]string{EnabledEnv: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example", "OTEL_EXPORTER_OTLP_PROTOCOL": "json"}, want: "unsupported OTLP"},
		{name: "invalid ratio", values: map[string]string{EnabledEnv: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example", "OTEL_TRACES_SAMPLER_ARG": "1.5"}, want: "between 0 and 1"},
		{name: "unsupported sampler", values: map[string]string{EnabledEnv: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example", "OTEL_TRACES_SAMPLER": "custom"}, want: "unsupported OTEL_TRACES_SAMPLER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(func(key string) string { return tt.values[key] }, "service", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadConfigSamplingRatiosRequireFiniteInclusiveValues(t *testing.T) {
	tests := []struct {
		name   string
		envKey string
		value  string
		want   float64
		valid  bool
	}{
		{name: "trace ratio zero", envKey: "OTEL_TRACES_SAMPLER_ARG", value: "0", want: 0, valid: true},
		{name: "trace ratio one", envKey: "OTEL_TRACES_SAMPLER_ARG", value: "1", want: 1, valid: true},
		{name: "trace ratio NaN", envKey: "OTEL_TRACES_SAMPLER_ARG", value: "NaN"},
		{name: "trace ratio positive infinity", envKey: "OTEL_TRACES_SAMPLER_ARG", value: "+Inf"},
		{name: "trace ratio negative infinity", envKey: "OTEL_TRACES_SAMPLER_ARG", value: "-Inf"},
		{name: "action ratio zero", envKey: "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO", value: "0", want: 0, valid: true},
		{name: "action ratio one", envKey: "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO", value: "1", want: 1, valid: true},
		{name: "action ratio NaN", envKey: "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO", value: "NaN"},
		{name: "action ratio positive infinity", envKey: "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO", value: "+Inf"},
		{name: "action ratio negative infinity", envKey: "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO", value: "-Inf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{
				EnabledEnv:                    "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example",
				tt.envKey:                     tt.value,
			}
			cfg, err := LoadConfig(func(key string) string { return values[key] }, "service", "")
			if !tt.valid {
				if err == nil || !strings.Contains(err.Error(), tt.envKey+" must be a number between 0 and 1") {
					t.Fatalf("LoadConfig error = %v, want finite-range error for %s", err, tt.envKey)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			got := cfg.SampleRatio
			if tt.envKey == "APEXLOAD_OTEL_ACTION_SAMPLE_RATIO" {
				got = cfg.ActionSampleRatio
			}
			if got != tt.want {
				t.Fatalf("configured ratio = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTTPMiddlewareContinuesRemoteTraceAndRedactsRequestTarget(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	cfg := Config{
		Enabled:     true,
		ServiceName: "apexload-controller-test",
		Endpoint:    "http://collector.invalid:4318",
		Protocol:    "http/protobuf",
		SamplerName: "always_on",
		SampleRatio: 1,
	}
	runtime, err := configureWithExporter(context.Background(), cfg, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		return exporter, nil
	})
	if err != nil {
		t.Fatalf("configureWithExporter: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	var observed oteltrace.SpanContext
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tests/{id}", func(w http.ResponseWriter, r *http.Request) {
		observed = oteltrace.SpanContextFromContext(r.Context())
		if got := r.URL.Query().Get("api_key"); got != "super-secret" {
			t.Errorf("handler query = %q, want original request query", got)
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	handler := HTTPMiddleware(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/test-123?api_key=super-secret", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if err := runtime.provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if got := observed.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("handler trace ID = %q, want remote parent trace ID", got)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /api/v1/tests/{id}" {
		t.Fatalf("span name = %q", span.Name)
	}
	if span.Parent.SpanID().String() != "00f067aa0ba902b7" || !span.Parent.IsRemote() {
		t.Fatalf("span parent = %v, want remote incoming parent", span.Parent)
	}
	if span.Status.Code != codes.Error {
		t.Fatalf("span status = %#v, want error for 503", span.Status)
	}
	attrs := make(map[string]string, len(span.Attributes))
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.String()
	}
	if attrs["http.route"] != "/api/v1/tests/{id}" {
		t.Fatalf("http.route = %q", attrs["http.route"])
	}
	for key, value := range attrs {
		if strings.Contains(value, "super-secret") || strings.Contains(strings.ToLower(key), "query") {
			t.Fatalf("sensitive query leaked into span attribute %s=%q", key, value)
		}
	}
}
