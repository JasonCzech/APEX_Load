// Package telemetry configures process-level OpenTelemetry tracing for APEX Load.
// Tracing is deliberately opt-in: no exporter or SDK provider is installed unless
// APEXLOAD_OTEL_ENABLED is true and a standard OTLP endpoint is configured.
package telemetry

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	EnabledEnv = "APEXLOAD_OTEL_ENABLED"

	defaultSamplerName = "parentbased_traceidratio"
	defaultSampleRatio = 0.10
	// Action spans can be orders of magnitude more numerous than controller
	// lifecycle spans. Independently retain one percent by default.
	defaultActionSampleRatio = 0.01
	actionSampleScale        = uint64(1_000_000)
)

// Config is the validated process tracing configuration. Endpoint is retained
// for validation and startup diagnostics only; it is intentionally omitted from
// Status because OTLP endpoint URLs can contain sensitive routing information.
type Config struct {
	Enabled           bool
	ServiceName       string
	ServiceVersion    string
	Environment       string
	Endpoint          string
	Protocol          string
	SamplerName       string
	SampleRatio       float64
	ActionSampleRatio float64
}

// Status is safe to expose through the API integration catalog.
type Status struct {
	Enabled         bool   `json:"enabled"`
	Ready           bool   `json:"ready"`
	CorrelationOnly bool   `json:"correlation_only,omitempty"`
	ServiceName     string `json:"service_name,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	Sampler         string `json:"sampler,omitempty"`
}

// Runtime owns the installed SDK provider and restores the prior global state
// during shutdown. The disabled runtime contains no exporter/provider.
type Runtime struct {
	status                   Status
	provider                 *sdktrace.TracerProvider
	previousProvider         trace.TracerProvider
	previousPropagator       propagation.TextMapPropagator
	shutdownOnce             sync.Once
	shutdownErr              error
	statusMu                 sync.RWMutex
	previousActionThreshold  uint64
	previousActionConfigured bool
}

var actionSpanSampling struct {
	configured atomic.Bool
	threshold  atomic.Uint64
	sequence   atomic.Uint64
}

// LoadConfig reads APEXLOAD_OTEL_ENABLED plus the standard OTEL_* exporter,
// resource, service, and sampler variables. Exporter configuration remains
// strictly dormant until the APEX Load opt-in is true.
func LoadConfig(getenv func(string) string, defaultServiceName, serviceVersion string) (Config, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := Config{
		ServiceName:    strings.TrimSpace(getenv("OTEL_SERVICE_NAME")),
		ServiceVersion: strings.TrimSpace(serviceVersion),
		Environment:    strings.TrimSpace(getenv("APEXLOAD_ENV")),
		SamplerName:    strings.ToLower(strings.TrimSpace(getenv("OTEL_TRACES_SAMPLER"))),
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = strings.TrimSpace(defaultServiceName)
	}
	if cfg.ServiceName == "" {
		return Config{}, fmt.Errorf("OpenTelemetry service name is required")
	}

	rawEnabled := strings.TrimSpace(getenv(EnabledEnv))
	if rawEnabled == "" {
		return cfg, nil
	}
	enabled, err := strconv.ParseBool(rawEnabled)
	if err != nil {
		return Config{}, fmt.Errorf("%s must be a boolean: %w", EnabledEnv, err)
	}
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
	}

	cfg.Endpoint = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if cfg.Endpoint == "" {
		cfg.Endpoint = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or OTEL_EXPORTER_OTLP_ENDPOINT is required when %s=true", EnabledEnv)
	}
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return Config{}, err
	}

	cfg.Protocol = strings.ToLower(strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")))
	if cfg.Protocol == "" {
		cfg.Protocol = strings.ToLower(strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "http/protobuf"
	}
	if cfg.Protocol != "http/protobuf" && cfg.Protocol != "grpc" {
		return Config{}, fmt.Errorf("unsupported OTLP traces protocol %q (want http/protobuf or grpc)", cfg.Protocol)
	}

	if cfg.SamplerName == "" {
		cfg.SamplerName = defaultSamplerName
	}
	if err := validateSampler(cfg.SamplerName); err != nil {
		return Config{}, err
	}
	if cfg.SamplerName == "traceidratio" || cfg.SamplerName == "parentbased_traceidratio" {
		ratioRaw := strings.TrimSpace(getenv("OTEL_TRACES_SAMPLER_ARG"))
		if ratioRaw == "" {
			cfg.SampleRatio = defaultSampleRatio
		} else {
			ratio, parseErr := parseRatio(ratioRaw)
			if parseErr != nil {
				return Config{}, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be a number between 0 and 1")
			}
			cfg.SampleRatio = ratio
		}
	}
	cfg.ActionSampleRatio = defaultActionSampleRatio
	if raw := strings.TrimSpace(getenv("APEXLOAD_OTEL_ACTION_SAMPLE_RATIO")); raw != "" {
		ratio, parseErr := parseRatio(raw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("APEXLOAD_OTEL_ACTION_SAMPLE_RATIO must be a number between 0 and 1")
		}
		cfg.ActionSampleRatio = ratio
	}
	return cfg, nil
}

func parseRatio(raw string) (float64, error) {
	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
		return 0, fmt.Errorf("ratio is outside [0,1]")
	}
	return ratio, nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("OTLP traces endpoint must be an absolute http(s) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("OTLP traces endpoint must not contain credentials, a query string, or a fragment; use OTEL_EXPORTER_OTLP_HEADERS for authentication")
	}
	return nil
}

func validateSampler(name string) error {
	switch name {
	case "always_on", "always_off", "traceidratio", "parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio":
		return nil
	default:
		return fmt.Errorf("unsupported OTEL_TRACES_SAMPLER %q", name)
	}
}

func samplerForConfig(cfg Config) sdktrace.Sampler {
	switch cfg.SamplerName {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(cfg.SampleRatio)
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))
	}
}

// Configure installs a batched OTLP trace provider and W3C Trace Context
// propagator. A disabled config installs neither an SDK provider nor exporter.
func Configure(ctx context.Context, cfg Config) (*Runtime, error) {
	return configureWithExporter(ctx, cfg, nil)
}

type exporterFactory func(context.Context, Config) (sdktrace.SpanExporter, error)

func configureWithExporter(ctx context.Context, cfg Config, factory exporterFactory) (*Runtime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtime := &Runtime{
		status: Status{
			Enabled:     cfg.Enabled,
			ServiceName: cfg.ServiceName,
			Protocol:    cfg.Protocol,
			Sampler:     cfg.SamplerName,
		},
		previousProvider:         otel.GetTracerProvider(),
		previousPropagator:       otel.GetTextMapPropagator(),
		previousActionThreshold:  actionSpanSampling.threshold.Load(),
		previousActionConfigured: actionSpanSampling.configured.Load(),
	}
	if !cfg.Enabled {
		return runtime, nil
	}
	if factory == nil {
		factory = newOTLPExporter
	}
	exporter, err := factory(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize OpenTelemetry exporter: %w", err)
	}
	resource, err := sdkresource.New(ctx,
		sdkresource.WithFromEnv(),
		sdkresource.WithHost(),
		sdkresource.WithOS(),
		sdkresource.WithProcess(),
		sdkresource.WithTelemetrySDK(),
		sdkresource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
			attribute.String("deployment.environment.name", cfg.Environment),
		),
	)
	if err != nil && !errors.Is(err, sdkresource.ErrPartialResource) {
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("build OpenTelemetry resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(samplerForConfig(cfg)),
		sdktrace.WithResource(resource),
		sdktrace.WithBatcher(exporter),
	)
	runtime.provider = provider
	runtime.status.Ready = true
	actionSpanSampling.threshold.Store(uint64(cfg.ActionSampleRatio * float64(actionSampleScale)))
	actionSpanSampling.sequence.Store(0)
	actionSpanSampling.configured.Store(true)
	otel.SetTracerProvider(provider)
	// Only W3C trace-context is propagated. Baggage is intentionally excluded
	// because arbitrary caller-controlled baggage can contain sensitive data.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return runtime, nil
}

func newOTLPExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.Protocol {
	case "grpc":
		return otlptracegrpc.New(ctx)
	default:
		return otlptracehttp.New(ctx)
	}
}

// Status returns a copy of the safe runtime readiness summary.
func (r *Runtime) Status() Status {
	if r == nil {
		return Status{}
	}
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

// Shutdown flushes queued spans and restores the process globals that preceded
// this runtime. It is safe to call more than once.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.shutdownOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		if r.provider != nil {
			r.shutdownErr = r.provider.Shutdown(ctx)
			if otel.GetTracerProvider() == r.provider && r.previousProvider != nil {
				otel.SetTracerProvider(r.previousProvider)
			}
		}
		if r.previousPropagator != nil {
			otel.SetTextMapPropagator(r.previousPropagator)
		}
		actionSpanSampling.threshold.Store(r.previousActionThreshold)
		actionSpanSampling.configured.Store(r.previousActionConfigured)
		r.statusMu.Lock()
		r.status.Ready = false
		r.statusMu.Unlock()
	})
	return r.shutdownErr
}

// Detach retains only the immutable remote/local span context while removing
// request cancellation and arbitrary context values. It is safe for async test
// execution that outlives the HTTP request which started it.
func Detach(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return context.Background()
	}
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}

// BoundedIdentifier prevents operator-controlled identifiers from creating
// oversized span attributes while preserving enough value for correlation.
func BoundedIdentifier(value string) string {
	value = strings.TrimSpace(value)
	return boundedtext.TruncateUTF8(value, 256)
}

// ShouldStartActionSpan applies the independent high-volume action sampler.
// A sampled parent is still required, and a skipped action keeps the parent's
// context so outbound protocols can continue trace propagation without
// exporting a span for every load-generator operation.
func ShouldStartActionSpan(ctx context.Context) bool {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() || !spanContext.IsSampled() {
		return false
	}
	return admitHighVolumeSpan()
}

// admitHighVolumeSpan applies the action sampler shared by action spans and
// transaction roots.
func admitHighVolumeSpan() bool {
	if !actionSpanSampling.configured.Load() {
		// Embedders/tests that install an SDK directly retain ordinary OTel
		// behavior. APEX Load processes always configure the explicit sampler.
		return true
	}
	threshold := actionSpanSampling.threshold.Load()
	if threshold == 0 {
		return false
	}
	if threshold >= actionSampleScale {
		return true
	}
	sequence := actionSpanSampling.sequence.Add(1)
	// Multiplication by an odd 64-bit golden-ratio constant permutes sequential
	// counters before the modulo, avoiding a retained burst at process startup.
	mixed := sequence * uint64(0x9e3779b97f4a7c15)
	return mixed%actionSampleScale < threshold
}

// NewTransactionRoot starts a new trace for one top-level business transaction
// instance, linked to the run's trace rather than parented by it, so every
// transaction instance has its own trace ID. The root is a real span only when
// the action sampler admits it and a tracer provider records it. Otherwise a
// fresh, non-recording root still propagates: trace context never depends on
// export. sampledWhenUnexported sets the W3C sampled flag on such a root when
// APEX Load is not exporting traces; while exporting, an unadmitted root stays
// unsampled so action spans are never exported under a parent that was not.
func NewTransactionRoot(ctx context.Context, name string, sampledWhenUnexported bool) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	var links []trace.Link
	if parent := trace.SpanContextFromContext(ctx); parent.IsValid() {
		links = append(links, trace.Link{SpanContext: parent})
	}
	if admitHighVolumeSpan() {
		rootCtx, span := otel.Tracer("github.com/jasonczech/apexload/pkg/engine").Start(
			ctx,
			"apexload.transaction",
			trace.WithNewRoot(),
			trace.WithLinks(links...),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("apexload.transaction", BoundedIdentifier(name))),
		)
		// Only a recording span is a new, exported root. A no-op tracer ignores
		// WithNewRoot and returns the parent's context, and an unsampled SDK span
		// is never exported; both fall through to a fresh propagated root.
		if span.IsRecording() {
			return rootCtx, func() { span.End() }
		}
		span.End()
	}
	config := trace.SpanContextConfig{TraceID: newRootTraceID(), SpanID: newRootSpanID()}
	if sampledWhenUnexported && !actionSpanSampling.configured.Load() {
		config.TraceFlags = trace.FlagsSampled
	}
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(config)), func() {}
}

func newRootTraceID() trace.TraceID {
	var id trace.TraceID
	if _, err := cryptorand.Read(id[:]); err != nil || !id.IsValid() {
		id[len(id)-1] = 1
	}
	return id
}

func newRootSpanID() trace.SpanID {
	var id trace.SpanID
	if _, err := cryptorand.Read(id[:]); err != nil || !id.IsValid() {
		id[len(id)-1] = 1
	}
	return id
}
