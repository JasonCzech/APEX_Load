# OpenTelemetry tracing

Status: Current

APEX Load can export vendor-neutral OTLP traces from the standalone controller
and generator binaries. Export is strictly opt-in: the default process installs
no SDK provider, creates no exporter, and opens no collector connection. The
desktop application remains correlation-only so local operator activity is not
exported implicitly.

## Enable tracing

Set the following on both controller and generator processes:

```bash
APEXLOAD_OTEL_ENABLED=true
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_SERVICE_NAME=apexload-controller # use apexload-generator on generators
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.10
APEXLOAD_OTEL_ACTION_SAMPLE_RATIO=0.01
```

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and
`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` take precedence over their common OTLP
counterparts. `http/protobuf` and `grpc` are supported. The official exporters
also honor the standard trace-specific/common headers, TLS certificate, client
certificate/key, timeout, compression, `OTEL_RESOURCE_ATTRIBUTES`, and
`OTEL_SERVICE_NAME` variables. Put authentication in
`OTEL_EXPORTER_OTLP_HEADERS`; endpoint URLs containing credentials or query
strings are rejected.

For Helm installs, set `tracing.enabled`, `tracing.endpoint`, and the sampling
values in the release configuration. If the collector requires headers, put
the complete `OTEL_EXPORTER_OTLP_HEADERS` value in an existing Kubernetes
Secret and set `tracing.headersSecretName` plus, when needed,
`tracing.headersSecretKey`. The chart injects the Secret key into both
controller and generator processes without copying the credential into the
release values or ConfigMap.

An enabled process fails startup when its endpoint, protocol, sampler, or ratio
is invalid. Export uses the SDK batch processor and shutdown is given a bounded
flush window. Integration-profile status is `configured` only after the SDK and
exporter initialize; this is startup readiness, not a live collector health
probe.

## Span coverage

- inbound controller HTTP requests, named by method and matched route pattern;
- test-start orchestration and complete local test execution;
- distributed test launch orchestration through generator commit (the durable
  terminal state remains the source of truth for distributed completion);
- sampled protocol action execution, including retry count and safe outcome;
- controller/client and generator/server unary and streaming gRPC boundaries;
- generator engine work linked to the detached `CommitWork` server context; and
- W3C context injection into HTTP protocol requests when script traceparent
  propagation is enabled.

Action span names are static (`apexload.action`). Attributes contain durable
test/project/scenario identifiers, controlled action-type/protocol values,
counts, and outcomes. Free-form test, action, and group names plus user-supplied
script IDs are excluded. Identifier length limits are defense in depth, not
redaction: operators should still treat exported identifiers as telemetry.
Targets, URLs, query strings, request/response bodies, headers, raw error
messages, and arbitrary baggage are not exported. Explicit script `traceparent`
headers remain authoritative; setting `traceparent: false` continues to
suppress automatic context injection.

The test-run sampler follows standard `OTEL_TRACES_SAMPLER` semantics. Its
default is `parentbased_traceidratio` at `0.10`. High-volume action spans have a
second deterministic sampler controlled by
`APEXLOAD_OTEL_ACTION_SAMPLE_RATIO` (default `0.01`); skipped actions retain the
active parent context, so HTTP propagation remains continuous without exporting
every action.

## Signal boundary

This implementation exports traces only. Prometheus remains the metrics
surface, structured process logs remain the logging surface, archived result
telemetry remains in APEX Load storage, and Dynatrace lifecycle notifications
remain event webhooks. Native OpenTelemetry metrics/log export is not currently
implemented.
