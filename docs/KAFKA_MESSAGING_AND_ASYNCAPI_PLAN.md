# Implementation Plan: Direct Kafka Messaging Testing + AsyncAPI Import

> Status: Roadmap/detail. For current messaging support guidance, start with
> [MESSAGING_SUPPORT.md](MESSAGING_SUPPORT.md). Kafka protocol plugin, DSL integration, binary wiring,
> AsyncAPI converter + import wizard, direct Kafka authoring, and the franz-go real
> network adapter (opt-in via the `kafka_real` build tag) are all in place. This document
> is the design of record.

## Opt-in real adapter (franz-go)
The default build ships the in-memory mock broker plus an "unavailable" stub for the
non-mock path, so the standard binary stays self-contained and fully tested without a
broker dependency. The real network adapter is compiled in via a build tag:

```
go build -tags kafka_real ./cmd/generator ./cmd/controller ./cmd/apexctl
```

With the tag, `config.kafka.mock: false` uses a `github.com/twmb/franz-go`-backed client
(`pkg/protocol/kafka/realclient_franz.go`) supporting SASL PLAIN/SCRAM-256/SCRAM-512, TLS
(insecure / client cert / CA), acks, compression, consumer groups, and produce / consume /
request-reply / commit. A live-broker integration test lives behind the `kafka_integration`
tag:

```
KAFKA_BROKERS=localhost:9092 go test -tags "kafka_real kafka_integration" ./pkg/protocol/kafka/...
```

The build-tagged seam is `newRealClient(config)` — `realclient_default.go` (default) returns
the unavailable stub; `realclient_franz.go` (`//go:build kafka_real`) returns the franz
client. The default `go test ./...` exercises only the mock path; the franz path is
built and tested under `-tags kafka_real` by the `kafka` profile of the `protocol-tags`
job in `.github/workflows/ci.yml` (and the `kafka_real,kafka_integration` lane in
`.github/workflows/protocol-integration.yml`).

## Current Implementation Status
The Kafka runtime is no longer just planned. The following are implemented in the Go
runtime and pass `go test ./...` (Kafka package is also `-race` clean):

- `pkg/protocol/kafka/` — registered `kafka` protocol with `kafka_produce`,
  `kafka_consume`, `kafka_request_reply`, `kafka_commit`, `kafka_seek` actions; a
  `kafkaClient` interface behind an in-memory mock broker (default) and an
  `unavailableClient` stub for the real network adapter (mirrors the JMS Solace/IBM MQ
  pattern). Extractors (`body`/`header`/`key`/`partition`/`offset`/`correlation_id`),
  assertions (`message_received`, `body_contains`, `header_equals`, `key_equals`,
  `partition_equals`, `offset_advanced`, `correlation_match`, `consumer_lag_below`,
  `response_time_ms`), partition strategies (key-hash/round-robin/explicit), and
  messaging telemetry (`MessagesSent/Received`, per-sec rates, `ConsumerLagMs`,
  `MessageLatencyMs`) are wired.
- `pkg/dsl/types.go` — `KafkaConfig` + `KafkaTLSConfig` on `ScriptConfig`.
- `pkg/dsl/validate.go` — `validateKafkaScriptIssues` dispatch + enum validation;
  `kafka` added to `protocolAllowsEmptyBaseURL`.
- `pkg/engine/config.go` — `kafka_*` flatten block; `kafka_sasl_password_secret`
  participates in the existing secret-resolution chain.
- `pkg/protocol/evidence.go` — `kafka` evidence inference case.
- `cmd/{generator,controller,apexctl}/main.go` — blank-imported `kafka` (and the
  previously-missing `jms` import was added to the generator).

Also implemented:

- `pkg/convert/asyncapi/` — AsyncAPI 2.x + 3.x → Kafka DSL converter (servers→brokers,
  channels→topics, send/publish→`kafka_produce`, receive/subscribe→`kafka_consume`,
  operation reply→`kafka_request_reply`), JSON-Schema payload sampling with data-source
  placeholders, `SupportMatrix()`, and source/review diagnostics. Generated scripts pass
  DSL validation.
- `pkg/api/` — `POST /api/v1/scripts/import/asyncapi` + `GET …/asyncapi/support-matrix`
  routes and handlers, modeled on the OpenAPI importer. Handler tests included.
- `dashboard/` — `'asyncapi'` import kind in `ProtocolAssetImport.tsx`, `importAsyncAPI`
  API client method, `AsyncAPIImportResult`/`AsyncAPIImportStats` types, the
  `asyncapi-import` authoring subview, and an "AsyncAPI / Kafka" card in the
  Import-From-Source surface. Verified end-to-end in a browser (upload → convert →
  review render).

Direct Kafka authoring is also implemented:

- `dashboard/` — `createKafkaDraft` starter (protocol `kafka`, mock mode, produce +
  consume actions), `startKafkaScript` threaded through `useScriptsAuthoringState` and
  the authoring shell, and a "Kafka Script" card in the Script Builder's Blank & Assisted
  Editors. Verified end-to-end in a browser (card → draft editor with produce/consume,
  no validation/console errors).
- `pkg/api/native_protocol_support.go` — a `kafka` entry in the native protocol parity
  matrix (guarded by `native_protocol_support_test.go`).

The franz-go real network adapter is now implemented as an opt-in build (see "Opt-in real
adapter" above). The sections below remain the original full design of record.

## Objective
Add direct load testing of Apache Kafka messaging services to APEX Load, plus an
AsyncAPI specification importer that scaffolds Kafka DSL scripts. The work must fit the
existing execution model rather than introduce a parallel one:

- The Go load generator remains the runtime owner for protocol execution. Kafka is a
  compiled-in protocol plugin, not a runtime/external plugin.
- Kafka is **authored directly** (it is a transport, not a recorded artifact). The
  importer surface is reserved for AsyncAPI, which is the event-streaming analog of the
  existing OpenAPI importer.
- New protocol features reuse the messaging telemetry, extractor, assertion, secret, and
  validation infrastructure already present, instead of duplicating it.

## Architectural Decision
Two distinct, additive deliverables:

| Deliverable | Type | Location | Rationale |
|---|---|---|---|
| **Kafka protocol plugin** | Direct-execution protocol | `pkg/protocol/kafka` | Kafka is a transport; models on JMS exactly. **Required core.** |
| **AsyncAPI converter** | Source-artifact importer | `pkg/convert/asyncapi` | AsyncAPI is the event-streaming analog of OpenAPI; the only importer that legitimately emits Kafka DSL. **Authoring convenience; depends on the plugin.** |

Existing importers (Enterprise Script Archive, JMeter, k6, Postman, OpenAPI, HAR, WSDL, DevWeb, ...) are
**not** reused for Kafka: none can emit Kafka actions, and no Kafka source artifact exists
for them to consume. Routing Kafka through the importer framework would miscategorize it.

## Current Baseline (what this plan builds on)
- **Protocol contract** — `pkg/protocol/protocol.go` defines `Init / Execute / Teardown /
  HealthCheck`. Protocols self-register in `init()` via `protocol.Register` and are pulled
  into standard binaries by `pkg/protocol/builtin`.
- **JMS is the near-exact analog** — `pkg/protocol/jms/jms.go` already implements
  publish / consume / request-reply, a `brokerAdapter` interface, an in-memory mock broker
  for local replay, and "unavailable" adapters where a real broker SDK is not compiled in.
- **Messaging telemetry already exists** — `protocol.ServerTelemetry` carries
  `MessagesSent/Received`, `MessagesSentPerSec`, `MessagesReceivedPerSec`, `ConsumerLagMs`,
  and `MessageLatencyMs`; `protocol.Result` carries `CorrelationID`, `DependencyType`, and
  `DependencyTarget`. **No struct changes are required** to surface Kafka metrics.
- **DSL config flattening** — `pkg/engine/config.go` (`buildProtoConfigWithSecretResolver`)
  flattens `ScriptConfig.JMS` into a `map[string]any` of `jms_*` / `broker_url` keys passed
  to `Protocol.Init`. A Kafka block slots in beside it (~lines 470–511).
- **Secret resolution** — `pkg/engine/secrets.go` resolves `*_secret` keys (e.g.
  `database_dsn_secret`) through a chained/caching resolver (env + Azure Key Vault). Kafka
  SASL credentials reuse this via `kafka_sasl_password_secret`.
- **Validation dispatch** — `pkg/dsl/validate.go` branches per protocol (JMS via the
  `jms-` prefix at line 18; `protocolAllowsEmptyBaseURL` exempts non-HTTP protocols).
- **Importer framework** — `pkg/convert/openapi/converter.go` is the template for a
  spec-to-DSL converter (`ImportRequest`, `ConvertOptions`, `ConvertStats`, `ReviewSummary`,
  `ConvertResult`), with shared `diag` + `sourceview` infrastructure. The dashboard renders
  it via `ProtocolAssetImport.tsx` (`ImportKind` union) with free `SourceMappingReview` +
  `ScriptImportReview` UI.

## Dependency Choice
Recommend **`github.com/twmb/franz-go`** over `segmentio/kafka-go`:

- Pure Go, **no CGo** — so unlike Solace/IBM MQ, Kafka can ship a **real, default-on
  network adapter** rather than an "unavailable adapter" stub.
- Full protocol coverage: idempotent producer, consumer groups, SASL PLAIN / SCRAM-SHA-256
  / SCRAM-SHA-512 / OAUTHBEARER, TLS, transactions.
- Strong load characteristics (batching, low allocation).

`go.mod` currently has **no** Kafka dependency (clean add). Module targets Go 1.26.5.

---

## Phase 1 — Kafka Protocol Plugin (`pkg/protocol/kafka/`)
Mirrors `pkg/protocol/jms/jms.go` structure.

### Files
- `kafka.go` — protocol type, `init()` registration, `Init/Execute/Teardown/HealthCheck`, action dispatch.
- `adapter.go` — `kafkaClient` interface + implementations.
- `mock.go` — in-memory broker (JMS `memoryBroker` analog) for local shakeout & CI.
- `franz_adapter.go` — real franz-go client (produce / fetch / consumer-group).
- `extract.go`, `assert.go` — extraction + assertion helpers.
- `*_test.go` — table-driven tests for every action type, happy + edge paths.

### Registration
```go
protocol.Register("kafka", func() protocol.Protocol { return &Kafka{name: "kafka"} })
```

### Config keys (read in `Init` from the `map[string]any`)
`kafka_brokers`, `kafka_security_protocol`, `kafka_sasl_mechanism`, `kafka_sasl_username`,
`kafka_sasl_password` (+ `kafka_sasl_password_secret`), `kafka_tls_*`, `kafka_client_id`,
`kafka_consumer_group`, `kafka_topic`, `kafka_acks`, `kafka_compression`,
`kafka_partition_strategy`, `kafka_auto_offset_reset`, `kafka_timeout_ms`, `kafka_mock`.

### Action types (`Execute` switch on `action.Type`)
- `kafka_produce` — publish to topic; key/partition from `action.Params`; headers from
  `action.Headers`; body from `action.Body`. Captures partition + offset into `Extractions`.
- `kafka_consume` — fetch from topic/consumer-group with timeout; runs extractors +
  assertions; sets `ConsumerLagMs`.
- `kafka_request_reply` — produce with a correlation header, then consume the reply topic
  awaiting a matching correlation id (JMS `requestReply` analog).
- `kafka_commit` / `kafka_seek` — offset management (extends the JMS `jms_ack`/`commit` idea
  for consumer-group semantics).

### Adapter interface
```go
type kafkaClient interface {
    Produce(ctx context.Context, topic string, rec record) (recordMeta, error)
    Consume(ctx context.Context, topic string, timeout time.Duration) (record, error)
    RequestReply(ctx context.Context, reqTopic, replyTopic string, req record, timeout time.Duration) (recordMeta, record, error)
    Commit(ctx context.Context) error
    HealthCheck(ctx context.Context) error // franz-go: metadata/APIVersions probe
    Teardown(ctx context.Context) error
}
```
- `mock: true` → in-memory broker (default for tests/local).
- `mock: false` → franz-go adapter. **Unlike JMS, the real path is fully functional**, not a stub.

### Result enrichment (reuses existing fields — no struct changes)
- `DependencyType`: `kafka_topic` / `kafka_consumer_group`
- `DependencyTarget`: topic name
- `Extractions`: `kafka_partition`, `kafka_offset`, `kafka_key`, `kafka_timestamp`, `kafka_consumer_lag`
- `ConsumerLagMs`: from high-watermark − consumed offset (a real Kafka concept franz-go
  exposes — more meaningful than the JMS time-based approximation).

### Extractors / Assertions
- Extractor sources: `body` (json/regex — reuse JMS `extractJSONPath`), `header`, `key`, `partition`, `offset`.
- Assertions: reuse `message_received`, `body_contains`, `header_equals`, `response_time_ms`;
  add `partition_equals`, `offset_advanced`, `consumer_lag_below`.

### Security / governance hooks
- SASL password supports a secret reference (`kafka_sasl_password_secret`) resolved by the
  existing `resolveProtoConfigSecrets` chain — same mechanism as `database_dsn_secret`.
- Broker addresses must pass the **existing SSRF/egress allow-list** when
  `APEXLOAD_ENV=production`. **Open task:** confirm the guard intercepts raw `host:port`
  broker dials (not just HTTP URLs); extend it if HTTP-only.
- Redact broker URLs containing inline credentials in evidence (`BuildProtocolEvidence`).

---

## Phase 2 — DSL Integration

### `pkg/dsl/types.go`
Add `Kafka KafkaConfig` to `ScriptConfig` (beside `JMS`):
```go
type KafkaConfig struct {
    Brokers            []string          `json:"brokers,omitempty"`
    SecurityProtocol   string            `json:"security_protocol,omitempty"`  // plaintext|ssl|sasl_ssl|sasl_plaintext
    SASLMechanism      string            `json:"sasl_mechanism,omitempty"`     // plain|scram-sha-256|scram-sha-512|oauthbearer
    SASLUsername       string            `json:"sasl_username,omitempty"`
    SASLPassword       string            `json:"sasl_password,omitempty"`
    SASLPasswordSecret string            `json:"sasl_password_secret,omitempty"`
    TLS                *KafkaTLSConfig   `json:"tls,omitempty"`
    ClientID           string            `json:"client_id,omitempty"`
    ConsumerGroup      string            `json:"consumer_group,omitempty"`
    Topic              string            `json:"topic,omitempty"`
    Acks               string            `json:"acks,omitempty"`               // none|leader|all
    Compression        string            `json:"compression,omitempty"`        // none|gzip|snappy|lz4|zstd
    PartitionStrategy  string            `json:"partition_strategy,omitempty"` // key-hash|round-robin|explicit
    AutoOffsetReset    string            `json:"auto_offset_reset,omitempty"`  // earliest|latest
    TimeoutMs          int               `json:"timeout_ms,omitempty"`
    Properties         map[string]string `json:"properties,omitempty"`
    Mock               bool              `json:"mock,omitempty"`
}
```

### `pkg/engine/config.go`
In `buildProtoConfigWithSecretResolver`, add a Kafka block mirroring the JMS block
(~lines 470–511): flatten `script.Config.Kafka` into `kafka_*` keys. Register
`kafka_sasl_password_secret` so the secret resolver picks it up.

### `pkg/dsl/validate.go`
- Early dispatch: `if script.Protocol == "kafka" { return validateKafkaScriptIssues(script) }`
  (parallel to the JMS `jms-` prefix branch at line 18).
- `validateKafkaScriptIssues`: require ≥1 broker; validate `security_protocol` /
  `sasl_mechanism` / `acks` / `compression` enums; topic present (config or per-action);
  valid action types; extractor/assertion shape.
- Add `"kafka"` to `protocolAllowsEmptyBaseURL` (no `base_url`, like JMS).

### Tests
`validate_kafka_test.go` (table-driven, following `validate_java_http_test.go`);
`config_test.go` additions for the flatten block.

---

## Phase 3 — Binary Wiring
Blank-import `pkg/protocol/kafka` in all three binaries: `cmd/generator/main.go`,
`cmd/controller/main.go`, `cmd/apexctl/main.go`.

**Also fix the pre-existing gap:** `cmd/generator/main.go` does **not** import `jms`, so
distributed JMS execution is currently broken. Add `jms` to the generator at the same time
(or track as a separate fix). Without the kafka import in the **generator**, distributed
Kafka runs fail identically.

Update the bundled-protocol list in `CLAUDE.md` and the package doc in
`pkg/protocol/protocol.go`.

---

## Phase 4 — Dashboard Authoring Surface
Kafka is authored directly, not imported, so it lives in the create/scenario path:
- Add `kafka` to the protocol enum + starter template in `ScriptCreateOptions.tsx` and the
  scenario wizard.
- Add `'kafka'` keyword to `apexDslSemanticHighlighting.ts`.
- Per the CLAUDE.md testing policy, verify the authoring flow in-browser (not just type-check).

---

## Phase 5 — AsyncAPI Converter (`pkg/convert/asyncapi/`)
This is where the importer framework is the right tool. Mirrors
`pkg/convert/openapi/converter.go` one-for-one.

### Backend — `converter.go`
- Parse AsyncAPI **2.x and 3.x** (JSON + YAML via `gopkg.in/yaml.v3`, already a dep). 2.x and
  3.x differ structurally (3.x splits `channels` / `operations`) — handle both; emit version
  in `ConvertStats`.
- Mapping AsyncAPI → Kafka DSL:
  - `servers` (kafka / kafka-secure protocol) → `KafkaConfig.Brokers` + `security_protocol`.
  - `channels` → topics. `kafka` binding → partition / key / topic name.
  - `operations`: `send`/`publish` → `kafka_produce`; `receive`/`subscribe` → `kafka_consume`.
  - `messages` payload schema → sample body (reuse OpenAPI converter's schema-to-sample-JSON;
    factor into a shared helper if practical), headers, and data-source review notes for
    parameterizable fields.
  - `correlationId` → `kafka_request_reply` where a send/receive pair shares a correlation.
- Reuse shared infrastructure: `diag`, `sourceview`, `dsl.Script`. Return
  `ConvertResult{Script, Warnings, Stats, Review, SourceReview}` (same shape as OpenAPI).
- `ConvertOptions`: `script_name`, `brokers` override, `operation_ids`/`channels` filter,
  `include_assertions`, `default_think_time_ms`.
- **Support matrix**: `SupportMatrix()` (model on `pkg/convert/k6/support.go`) enumerating
  supported / partial / unsupported features (kafka binding ✓; MQTT/AMQP/WebSocket bindings
  out of scope or routed elsewhere; Avro/Protobuf schema → partial).

### API — `pkg/api/server.go` + handlers
- `POST /api/v1/scripts/import/asyncapi` → `handleImportAsyncAPI`
- `GET /api/v1/scripts/import/asyncapi/support-matrix` → `handleAsyncAPISupportMatrix`
- Model on `handleImportOpenAPI` / `handleK6SupportMatrix` in `pkg/api/handlers_scripts.go`;
  same auth/cors/log middleware chain. Document both routes in `CLAUDE.md`.

### Dashboard
- Add `'asyncapi'` to the `ImportKind` union in `ProtocolAssetImport.tsx` with a copy block
  (title/subtitle/`accept .json,.yaml,.yml`/drop/button). Review UI is inherited.
- Add `AsyncAPIImportResult` to `dashboard/src/types/scriptImports.ts`.
- Wire into the import-source list in `ScriptCreateOptions.tsx` / authoring shell.

### Tests
`converter_test.go` with testdata fixtures (2.x + 3.x kafka specs, secure server,
correlationId case, unsupported-binding case); `handlers_web_api_imports_test.go` additions.

---

## Testing Strategy (per CLAUDE.md mandatory policy)
- **Protocol unit tests** against the in-memory mock broker (produce/consume/request-reply/
  commit, partition/offset extraction, assertion pass/fail, timeout/no-message edges).
- **DSL validation tests** — table-driven, all enum boundaries.
- **Engine config flatten test** — `KafkaConfig` → `kafka_*` map + secret resolution.
- **Converter tests** — fixture-driven for both AsyncAPI versions.
- **franz-go integration test** — build-tagged (`//go:build kafka_integration`) against a
  containerized broker (Redpanda/Kafka via testcontainers), **excluded from default
  `go test ./...`** so CI without Docker stays green. Document the opt-in.
- Run `go test ./...` before completion; in-browser check of the dashboard import + authoring flows.

## Documentation Updates
- `CLAUDE.md`: add `kafka` to bundled protocols; add the two AsyncAPI routes to the REST API
  list; note franz-go in Tech Stack.
- `docs/COMPETITIVE_COMPARISON.md` and the protocol-parity docs: add Kafka to the coverage
  matrix and the messaging-vs-LoadRunner story.
- Protocol package doc comment + an example `kafka` DSL script in the sample-import set.

## Risks & Open Questions
1. **SSRF/egress guard coverage** for raw broker dials (not HTTP) — must verify/extend;
   security-sensitive given the production fail-closed posture.
2. **Consumer-group semantics under load** — many VUsers (goroutines) sharing a group
   trigger rebalances; decide per-VUser client vs. shared client and document it (likely
   per-VUser producer, configurable consumer-group sharing).
3. **Schema registry / Avro-Protobuf payloads** — AsyncAPI specs often reference Confluent
   Schema Registry. Phase 5 scope: JSON payloads first; Avro/Protobuf + registry as a
   documented follow-on, surfaced in the support matrix as "partial."
4. **AsyncAPI 2.x vs 3.x divergence** — non-trivial; budget for both or explicitly scope to
   3.x first (flagged in the support matrix).
5. **franz-go binary size / build impact** on the generator pod image — measure.

## Sequencing
**1** (plugin) → **2** (DSL) → **3** (wiring) → end-to-end test with mock + a local
Redpanda → **4** (authoring UI) → **5** (AsyncAPI converter + wizard). Phases 1–3 are the
indivisible core; 4–5 are layered value.
