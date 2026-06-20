# Messaging Platform Coverage — Evaluation & Implementation Plan

> Scope: general (non-LoadRunner) support for **importing, running, and evaluating results** from
> message-queue platforms — **JMS, Apache Kafka, Solace PubSub+, IBM MQ, Azure Service Bus, and
> Azure Event Hubs** — measured against each platform's public specification, with a phased plan to
> close the gaps.

---

## Context

Why this is being done: APEX Load advertises broad message-queue support, but an evaluation against
the public specs (Jakarta Messaging/JMS, Apache Kafka + Confluent Schema Registry, Solace PubSub+,
IBM MQ, Azure Service Bus, Azure Event Hubs) found the support is a **thin, provider-neutral
publish/consume/request-reply veneer** with a good metrics pipeline bolted on — not broad coverage of
any platform. Three structural facts drive everything below:

1. **No real broker transport ships by default.** Every messaging provider defaults to an in-memory
   FIFO mock or a fail-closed "unavailable" stub. Real adapters are opt-in Go build tags and exist for
   only **three of six** paths (`kafka_real`, `jms_solace_real`, `jms_servicebus_real`). *(This is
   scoped to message brokers — HTTP/2, gRPC, WebSocket, SOAP, DB, and the network protocols all have
   real default-build transport.)*
2. **IBM MQ has no real adapter at all** — permanent `unavailableIbmMqAdapter`
   (`pkg/protocol/jms/jms.go:189-190,629-658`); **Solace is JMS-over-AMQP only** (no native SMF);
   **Event Hubs has zero awareness today**.
3. **Results are messaging-aware for throughput + latency but shallow elsewhere** — consumer lag is
   wall-clock time not offset/queue-depth, there are no reliability counters beyond dead-letter, no
   percentiles for message-latency/lag, no messaging SLA/goals, and **Kafka metrics never render in
   the dashboard** (the surface filters to `jms_`/`websocket`/`grpc`).

Intended outcome: a dependency-ordered program that brings all six platforms to broad spec coverage
across import → run → results, building shared foundations once and reusing the protocol-neutral
results pipeline.

This evaluation was produced by parallel code-recon + public-spec research, with an adversarial pass
that re-verified every high-impact "missing" claim against the actual code (it corrected three draft
findings — folded in below). Azure Event Hubs additionally has a complete, decisions-locked **day-1 implementation spec** in **Annex A** (producer-first; combined in from a parallel planning session).

---

## Current-state coverage assessment

Rating scale: **None** (not implemented / mock-only) · **Thin/Partial** (limited) · **Basic** (core
ops over a real adapter, behind a build tag) · **Broad** (spec-level). `†` = real adapter is opt-in
behind a Go build tag.

| Platform | Import | Run (execute) | Results (evaluate) |
|---|---|---|---|
| JMS (generic) | None | Thin | Partial |
| Apache Kafka | Partial | Basic † | Hidden (computed, not shown) |
| Solace PubSub+ | None | AMQP-only † | Partial |
| IBM MQ | None | **Mock only** | Partial |
| Azure Service Bus | None | Basic † | Partial |
| Azure Event Hubs | Partial (via Kafka binding) | **Runtime works, blocked by validator** † | Partial (inherits Kafka path) |

### Key findings by platform

- **JMS family (shared runtime, `pkg/protocol/jms`)** — exactly 5 broker capabilities (publish /
  consume / request-reply / health / teardown). Missing: message-type model (Text/Bytes/Map/Stream/
  Object — body is opaque `[]byte`), acknowledgement modes, local transactions (`jms_ack`/
  `jms_commit`/`jms_rollback` are **authorable no-ops**, `jms.go:240-241`), delivery-mode application
  (dead config `jms.go:158-160`), TTL/expiry, priority, selectors, durable/shared subscriptions,
  queue browse, topic fan-out.
- **Kafka** — real produce/consume/request-reply behind `kafka_real`. Missing: transactions/EOS,
  `read_committed`, Schema Registry/Avro/Protobuf serde, working `kafka_seek`/`kafka_commit`
  (no-ops); idempotence force-disabled at acks 0/1; offset lag computed then **discarded** to a string
  extraction (`realclient_franz.go:127-136`). **`validate.go:854` unconditionally blocks any non-mock
  Kafka script at test creation even in a `kafka_real` build** — a P0 that also gates Event Hubs.
- **Solace** — JMS-over-AMQP only. No native SMF, direct/guaranteed distinction, wildcards,
  partitioned queues, DMQ/max-redelivery, replay.
- **IBM MQ** — mock-only; needs an entire real adapter (native MQI + MQMD control + QMgr/SVRCONN/
  CCDT connectivity).
- **Azure Service Bus** — real adapter behind tag, with queue-vs-subscription routing and a peek-lock/
  receive-and-delete switch. Missing: sessions, client-controlled settlement (abandon/defer/DLQ),
  lock renewal, scheduled/deferred messages, dedup, DLQ receive, prefetch, cross-entity tx.
- **Azure Event Hubs** — *added in this evaluation.* Zero code today. **The existing Kafka adapter's
  runtime can reach the Event Hubs Kafka endpoint with config alone** (SASL PLAIN + `sasl_ssl` +
  `$ConnectionString` are all wired: `realclient_franz.go:201-217`, `config_protocols.go:455-470`) —
  but **not no-code end-to-end**: the `validate.go:854` block (P0.0) and the missing OAUTHBEARER
  mechanism (advertised but silently downgraded to PLAIN) require code. A native AMQP path needs the
  `azeventhubs` SDK in its own `pkg/protocol/eventhubs` package (Event Hubs is not JMS). The entire
  protocol-neutral results pipeline carries over verbatim.

### Cross-cutting gaps (all platforms)

- Generic reused request metrics, not messaging-aware reliability accounting.
- DSL **catalog-vs-runtime drift** — runtime reads `correlation_id`, `destination_type`,
  `dead_letter`, `consumer_group` params the catalog never declares; no messaging enum carries catalog
  `Enum` metadata (editor/JSON-schema/API give zero closed-set guidance).
- Import is AsyncAPI→Kafka only; all JMS/AMQP/Solace/MQ/MQTT bindings are dropped or review-only.
- No messaging-specific SLA/goal pass-fail (`GoalConfig` is HTTP-only).

---

## Architecture & placement constraints

- A new protocol lives in `pkg/protocol/<name>/`, implements `pkg/protocol.Protocol`, **self-registers
  via `init() -> protocol.Register(name, factory)`** (no central table edit), and is compiled into the
  **load generator** and **controller** binaries via blank imports in `cmd/generator/main.go` +
  `cmd/controller/main.go` (and `cmd/apexctl/main.go` for the subset). Real execution runs on the
  generator pods.
- Real network adapters follow a **build-tag seam**: a real file behind `//go:build <tag>_real` and a
  fail-closed default stub. New Event Hubs/IBM MQ adapters replicate this (`eventhubs_real`,
  `ibmmq_real`).
- The **results pipeline is protocol-neutral** (`protocol.ServerTelemetry` → `ServerTelemetrySummary`/
  `ProtocolSummary` → proto/distributed merge → dashboard). Any adapter that fills `ServerTelemetry`
  inherits throughput, message-latency, consumer-lag, payload sizing, throughput-gap SLA, and the
  broker-pressure analyzer with **zero new results code** — so the per-platform work focuses on the
  adapter + DSL, and the results work is mostly cross-cutting (P1.1/P1.5/P4.1).

---

## Cross-cutting design decisions (build once, reuse everywhere)

1. **Unified messaging result model** — reliability counters (send-failure, redelivery, dead-lettered,
   duplicate, out-of-order, commit/abort) + t-digest percentiles for message-latency & consumer-lag.
2. **Request-reply correlation + round-trip primitive** with safe requeue of uncorrelated replies.
3. **Generic AMQP 1.0 adapter layer** factored from the Solace adapter, reusable by AMQP brokers.
4. **Schema-registry / serialization layer** (Confluent wire format; Avro/Protobuf/JSON-Schema).
5. **Consumer-lag / queue-depth / backlog telemetry** unified probe + single dashboard surface.
6. **Dead-letter / redelivery accounting** as true counts, replacing metadata-only flagging.
7. **Acknowledgement / transaction state machine** replacing the no-op ack/commit/rollback.
8. **Messaging DSL catalog enrichment** — enum metadata + declare all runtime-read params.
9. **Messaging SLA/goal evaluation** — consumer-lag/queue-depth/rate-gap/latency-p95/redelivery/DLQ/
   throttle thresholds.
10. **Mock-broker fidelity uplift** — topic fan-out, partitions, consumer groups, ack/redelivery, TTL.

---

## Implementation plan — per-item specifications

Each item is a code-grounded engineering spec (problem, design, DSL/catalog changes, types, files,
build tags, test plan, acceptance criteria, effort/deps/risks). Effort roll-up:

| Item | Work | Effort | Depends on |
|---|---|---|---|
| P0.0 | Build-tag-aware Kafka non-mock validator (unblocks real Kafka + Event Hubs) | S | — |
| P1.1 | Unified messaging result model + reliability counters | L | — |
| P1.2 | Shared ack / transaction / session state machine | XL | P1.1 |
| P1.3 | Request-reply correlation primitive with safe requeue | M | P1.1 |
| P1.4 | DSL catalog enrichment + catalog/runtime parity | M | — |
| P1.5 | Consumer-lag / queue-depth / DLQ telemetry + dashboard surfacing | L | P1.1 |
| P2.1 | Kafka producer/consumer depth (idempotence, EOS, isolation, batching, seek) | XL | P1.2, P1.4 |
| P2.2 | Schema Registry / Avro / Protobuf / JSON-Schema serde | XL | P2.1 |
| P2.3 | JMS semantics (message-types, selectors, durable subs, priority, TTL, browse) | XL | P1.2, P1.4 |
| P2.4 | Azure Service Bus advanced (sessions, locks, scheduled, defer, dedup, DLQ, tx) | XL | P1.2, P1.3, P2.3 |
| P2.5 | IBM MQ real adapter (native MQI) + connectivity | XL | P1.2, P1.4, P2.3 |
| P2.6 | Solace native SMF transport | XL | P1.2, P2.3 |
| EH.1 | Event Hubs Kafka-endpoint enablement (preset + AsyncAPI detection + docs) | S | P0.0 |
| EH.2 | OAUTHBEARER / Microsoft Entra ID SASL for the Kafka adapter | M | P0.0 |
| EH.3 | Native Event Hubs AMQP adapter (`pkg/protocol/eventhubs`, azeventhubs) | XL | P1.1, P1.5 |
| EH.4 | Event Hubs results extension (skew, ServerBusy/throttle, checkpoint, TU/PU) | M | P1.1, P1.5, EH.3 |
| EH.5 | Event Hubs Capture (Avro) import + AsyncAPI EH binding (Annex A Ph5) | M | EH.1 |
| EH.6 | Event Hubs Prometheus + deployments/secrets/CI matrix (Annex A Ph6,8) | M | EH.3 |
| P3.1 | Messaging import breadth (AsyncAPI JMS/AMQP/Solace/MQ/MQTT + configs) | L | P2.2, P2.3 |
| P4.1 | Messaging-aware results, SLA/goals, dashboard panels | L | P1.1, P1.5, P2.1, P2.3, P2.4, EH.4 |

### P0.0 — Build-tag-aware Kafka non-mock validator + real-broker creation path

**Problem / current state** — `pkg/dsl/validate.go:854` unconditionally appends `"real Kafka network adapter is not available in this build; enable config.kafka.mock for replay"` for **any** script with `config.kafka.mock == false`, regardless of build tags. `pkg/api/testcreation.go` runs `dsl.ValidateScriptIssues` and turns any issue into a hard error (the API path), so even a `-tags kafka_real` controller binary cannot create a non-mock Kafka test through `POST /api/v1/tests`. This is inconsistent with the JMS pattern at `validate.go:707-712`, which gates Solace/Service Bus on the build-tag capability flags `SolaceJMSRealAdapterAvailable()` / `ServiceBusJMSRealAdapterAvailable()` (defined in `pkg/dsl/capabilities_jms_solace_real.go` / `capabilities_default.go`). The runtime already supports the real path: `pkg/protocol/kafka/kafka.go:123-129` dispatches to `newRealClient(k.cfg)`, which is the franz adapter under `kafka_real` (`realclient_franz.go`) and the fail-closed `unavailableClient` stub under the default build (`realclient_default.go`). So the runtime is correctly build-tag-aware; only the **validator** is not. **apexctl finding:** the `apexctl run` direct-execution path does NOT call the validator — `loadScriptForRun` (`cmd/apexctl/run_overrides.go:17-23`) only parses + applies overrides; validation is only invoked by `apexctl script validate` (`cmd/apexctl/script_commands.go:102`). So apexctl direct runs are gated solely by the runtime `unavailableClient` stub, which is already correct. Only the shared validator (used by the API and `apexctl script validate`) needs the fix.

**Goal** — A `-tags kafka_real` build of the controller can create and run real Kafka tests (and Event Hubs Kafka-endpoint tests, which use the same broker path) through the REST API, while the default build continues to fail-closed with actionable guidance — mirroring the JMS capability gating.

**Design** — Add a Kafka capability flag, set true only under `kafka_real`, and make the `validate.go:854` rejection conditional on `!KafkaRealAdapterAvailable()`. Reuse the existing JMS two-file split-by-build-tag pattern (no new mechanism). The franz adapter compiles in the same binary so no proto/results changes are needed — the results pipeline is already protocol-neutral and the franz client already populates `Extractions`/`ServerTelemetry`. Rejected alternatives: (a) a runtime env flag instead of a build tag — rejected because the real adapter is *physically absent* from a default build, so a runtime flag would lie; (b) removing the check entirely — rejected because default builds must fail-closed at validation time, not only at first action dispatch.

**DSL & catalog changes** — None. No new action types, fields, or enums. `config.kafka.mock` already exists (`types.go:213`). The OAUTHBEARER silent-downgrade and the `kafka_consumer_lag` discard are separate work items, not in scope here. No `go generate ./pkg/dsl` needed since the catalog is untouched; the freshness test stays green.

**Types & interfaces** — One package-level function in `pkg/dsl`:
```go
func KafkaRealAdapterAvailable() bool
```
Returns `true` in `capabilities_kafka_real.go` (`//go:build kafka_real`), `false` in `capabilities_kafka_default.go` (`//go:build !kafka_real`). Mirrors `SolaceJMSRealAdapterAvailable`.

**Files to touch**
- NEW `pkg/dsl/capabilities_kafka_real.go` — `//go:build kafka_real`, returns `true`.
- NEW `pkg/dsl/capabilities_kafka_default.go` — `//go:build !kafka_real`, returns `false`.
- EDIT `pkg/dsl/validate.go:854` — wrap the rejection in `if !KafkaRealAdapterAvailable() { ... }`; keep the `len(kafka.Brokers) == 0` check unconditional. Update the message to add `"...or build with -tags kafka_real"` (mirror the Solace wording at :707).
- NEW/EDIT `pkg/dsl/validate_kafka_test.go` (or extend existing kafka validate test) — table-driven, plus a `kafka_real`-tagged variant.
- EDIT `.github/workflows/go-protocol-tags.yml` — add a `kafka-real` job mirroring the `jms-solace-real` job.
- VERIFY-ONLY (no edit): `cmd/apexctl/run_overrides.go`, `pkg/protocol/kafka/realclient_default.go` — confirm direct-run + fail-closed stub already correct.

**Build tags / dependencies** — No `go.mod` additions (franz-go already vendored for `kafka_real`). New build tag `kafka_real` is already used by the protocol package; this extends it to `pkg/dsl`. CI matrix: add a job that runs `go test -tags kafka_real ./pkg/dsl ./pkg/protocol/kafka` and a default `go test ./pkg/dsl` to prove both gating directions.

**Test plan** — *Unit (default build, table-driven):* non-mock Kafka script → issue contains `"-tags kafka_real"`; mock script → no Kafka availability issue; non-mock with empty brokers → both broker + availability issues. *Unit (`kafka_real` tag):* non-mock script with brokers → NO availability issue; empty brokers still → broker issue. Add `KafkaRealAdapterAvailable()` direct assertions in each build. *Integration (behind `kafka_real` + testcontainers, optional CI):* `POST /api/v1/tests` with a non-mock Kafka script on a `kafka_real` controller returns 201, not a validation 400 — reuse existing franz integration harness (`franz_integration_test.go`). *Event Hubs:* a smoke unit asserting an Event Hubs-shaped config (`NAMESPACE.servicebus.windows.net:9093`, `sasl_ssl`/`plain`) passes validation under `kafka_real`. No UI touched → no dashboard verification.

**Acceptance criteria**
- [ ] `KafkaRealAdapterAvailable()` returns `false` (default) / `true` (`kafka_real`).
- [ ] Default build: non-mock Kafka script is rejected with a message naming both `config.kafka.mock` and `-tags kafka_real`.
- [ ] `kafka_real` build: non-mock Kafka script with brokers passes `ValidateScriptIssues`, and `POST /api/v1/tests` accepts it.
- [ ] Empty `config.kafka.brokers` still rejected in both builds.
- [ ] `go test ./pkg/dsl` and `go test -tags kafka_real ./pkg/dsl ./pkg/protocol/kafka` both green; new CI job added.
- [ ] `apexctl run` confirmed unaffected (no validator call); fail-closed stub confirmed for default direct runs.

**Effort / dependencies / risks** — Effort **S** (smaller than the M estimate; it's a 2-file capability mirror + a 1-line conditional + tests). Depends on: none. Risks: (1) low — a real `kafka_real` controller binary will now *attempt* real connections, so downstream items (SASL/OAUTHBEARER fidelity, offset-lag) become user-visible; flag those as follow-ups. (2) Operators must actually deploy a `kafka_real`-tagged image; document in the Kafka/Event Hubs runbook that the default image still fail-closes.

### P1.1 — Unified messaging result model + reliability counters

**Problem / current state** — The messaging telemetry path today carries only coarse averages and a single max. `ServerTelemetry` (`pkg/protocol/protocol.go:169-193`) exposes `MessageLatencyMs`, `MessagesSent/Received`, `MessagesSent/ReceivedPerSec`, `ConsumerLagMs`, `FirstEventMs`, `EventCount` — and no reliability signal at all. The engine summary `ServerTelemetrySummary` (`pkg/engine/metrics.go:105-128`) accumulates these as running sums divided to averages in `finalizeServerTelemetrySummary` (`metrics.go:836-849`) with only `Max*` extremes — no percentiles. The proto `ServerTelemetry` message (`api/proto/generator.proto`, fields 1-15) and the distributed merge (`pkg/api/distributed_metrics.go:449-500`, `cloneEngineServerTelemetrySummary` at 419) mirror exactly those fields. Crucially, latency percentiles for transaction duration are computed controller-side by re-collecting raw `duration_ms` values (`distributed_metrics.go:307-308`, `588-589`) — there is NO t-digest snapshot transport in proto (only `snapshot_json` field 18 on `TransactionMetric`, unrelated). So messaging reliability (send failures, redeliveries, DLQ, duplicates, ordering, commit/abort) is completely invisible, and lag/latency are averages-only. The cited touchpoint `pkg/protocol/result.go` does not exist; the `Result` struct lives at `pkg/protocol/protocol.go:236` and embeds `*ServerTelemetry` at line 266.

**Goal** — Give every messaging provider (JMS, Kafka, Event Hubs, future SMF) a uniform place to report seven reliability counters and true percentile distributions for `MessageLatencyMs` and `ConsumerLagMs`, finalized through engine merge, transported over gRPC, and re-aggregated correctly in distributed runs — the data backbone all later messaging-program items (dashboard, SLA, anomaly) build on.

**Design** — Three layers, each additive and back-compatible (new fields are optional/omitempty; zero values from old generators are harmless):
1. *Per-result counters* added to `protocol.ServerTelemetry` as `int` deltas. Providers increment them per action (e.g. Kafka commit handler sets `CommitCount=1`). These sum cleanly across merge — no special logic.
2. *Percentiles via raw-value transport*, mirroring the existing duration pattern rather than shipping t-digest snapshots. Generators already send one `ServerTelemetry` per transaction, so `MessageLatencyMs`/`ConsumerLagMs` raw values per tx already cross the wire. The controller collects them into `engine.NewTDigest(200)` (same as `distributed_metrics.go:308`) and emits P50/P90/P95/P99. *Rejected alternative:* adding a `TDigestSnapshot` proto message and merging via `TDigest.Merge` (tdigest.go:144) — more code, new proto surface, and unnecessary since raw values already transit. The local (non-distributed) engine path feeds a per-action `*TDigest` in `finalizeActionMetrics` (metrics.go:728-746); we add two parallel digests there.
3. The reliability counters and percentile fields land in `ServerTelemetrySummary`, propagated through `mergeServerTelemetrySummary` (metrics.go:801), `finalizeServerTelemetrySummary` (836), `cloneEngineServerTelemetrySummary` (419), `mergeDistributedServerTelemetry` (449), and `finalizeAPIServerTelemetrySummary` (487).

**DSL & catalog changes** — None. Reliability counters are runtime-emitted telemetry, not authorable DSL fields; the action catalog (`pkg/dsl/catalog*.go`) is untouched, so no `go generate ./pkg/dsl` run and no schema-freshness impact.

**Types & interfaces** —
`pkg/protocol/protocol.go` `ServerTelemetry` gains:
```go
SendFailureCount   int `json:"send_failure_count,omitempty"`
RedeliveryCount    int `json:"redelivery_count,omitempty"`
DeadLetteredCount  int `json:"dead_lettered_count,omitempty"`
DuplicateCount     int `json:"duplicate_count,omitempty"`
OutOfOrderCount    int `json:"out_of_order_count,omitempty"`
CommitCount        int `json:"commit_count,omitempty"`
AbortCount         int `json:"abort_count,omitempty"`
```
`pkg/engine/metrics.go` `ServerTelemetrySummary` gains the seven counters as `int64` plus `P50/P90/P95/P99MessageLatencyMs` and `P50/P90/P95/P99ConsumerLagMs` (`float64`, omitempty). `engine.ServerTelemetrySummary` also gains two unexported `*TDigest` fields (excluded from JSON via `json:"-"`) used only during accumulation.
`api/proto/generator.proto` `ServerTelemetry` adds fields 16-22 (`int32 send_failure_count = 16;` … `int32 abort_count = 22;`). `MessageLatencyMs`/`ConsumerLagMs` reuse existing fields 8-9 for raw transport; percentiles are derived controller-side, never serialized from generators.

**Files to touch** —
- EDIT `pkg/protocol/protocol.go:169-193` — 7 counter fields on `ServerTelemetry`.
- EDIT `pkg/engine/metrics.go:105-128` (summary fields), `:801-834` (merge sums + digest.Add), `:728-746`/`:836-849` (finalize → Quantile).
- EDIT `api/proto/generator.proto` — fields 16-22; then regenerate stubs.
- EDIT `pkg/api/distributed_metrics.go:419-441` (clone), `:449-485` (merge sums + collect raw latencies/lags), `:487-500` (finalize percentiles).
- EDIT each provider to populate counters: `pkg/protocol/jms/jms.go` (commit/rollback/ack at :240-241 → CommitCount/AbortCount; redelivery/DLQ on consume), `pkg/protocol/kafka/kafka.go` (:165-166 commit; send-failure path).
- NEW tests alongside each edited file (see below).

**Build tags / dependencies** — No go.mod additions. Counters populate regardless of `kafka_real`/`jms_*_real` tags — the fail-closed stubs simply leave them zero. The real adapters (`realclient_franz.go`, `realadapter_amqp.go`) populate `RedeliveryCount`/`SendFailureCount`/`OutOfOrderCount` from broker metadata. CI tag matrix unchanged; ensure proto regeneration runs in the default (untagged) build.

**Test plan** — Unit (table-driven, mandatory per policy): `metrics_test.go` — merge N `ServerTelemetry` with assorted counter deltas + latency samples, assert summed counters and Quantile-derived P95 within tolerance; edge cases: zero samples (no NaN, percentiles stay 0), single sample (all percentiles equal). `distributed_metrics_test.go` — two generators' batches merge to correct counter sums and pooled percentiles (verify pooling ≠ averaging of per-generator P95). Provider tests: `jms_test.go`/`kafka_test.go` assert commit/rollback/redelivery handlers set the right counter. Round-trip proto test: marshal→unmarshal preserves fields 16-22. Run `go test ./...`. No UI in this item — dashboard rendering is a downstream item; no browser verification required.

**Acceptance criteria** —
- [ ] `protocol.ServerTelemetry` exposes all 7 named counters; proto fields 16-22 added and stubs regenerated.
- [ ] `ServerTelemetrySummary` (engine + api) sums the 7 counters and exposes P50/P90/P95/P99 for both MessageLatencyMs and ConsumerLagMs.
- [ ] Single-pod and distributed paths produce identical aggregates for the same input (parity test passes).
- [ ] Distributed percentiles are pooled from raw samples, not averaged from per-generator percentiles.
- [ ] At least JMS commit/rollback and Kafka commit populate `CommitCount`/`AbortCount`.
- [ ] `go test ./...` green; old-generator batches (counters=0) merge without error.

**Effort / dependencies / risks** — Effort: **L** (matches estimate — wide but mechanical fan-out across 5 layers + per-provider wiring). Depends on: none. Risks: (1) proto field-number drift if another in-flight branch also extends `ServerTelemetry` — coordinate field 16+ allocation; (2) raw-value percentile transport scales with tx volume, but it reuses the already-shipped per-tx telemetry, so no new wire cost; (3) double-counting if a provider sets a counter on both send and an internal retry path — assert idempotency in provider tests; (4) several "counter" sources (RedeliveryCount, OutOfOrderCount) require real-broker metadata that the default mock/stub adapters can't supply, so coverage is honest-zero outside `*_real` builds — document this rather than fabricating values.

### P1.2 — Shared ack / transaction / session state machine

**Problem / current state** — Today the settlement actions are pure no-ops. In `pkg/protocol/jms/jms.go:240-241`, the `jms_ack`, `jms_commit`, and `jms_rollback` cases only stamp `result.Extractions["jms_operation"] = action.Type` and return success — no acknowledgement, commit, or redelivery happens. Identically, `pkg/protocol/kafka/kafka.go:165-166` handles `kafka_commit`/`kafka_seek` with a lone `result.Extractions["kafka_operation"]` stamp. Acknowledgement mode is parsed (`jms.go:138`, defaulted to `auto` at `jms.go:155-157`) but **never threaded to any settlement call**. The `brokerAdapter` interface (`jms.go:71-77`) exposes only `Publish/Consume/RequestReply/HealthCheck/Teardown` — there is no concept of an unsettled message, a session, or a transaction handle. The franz Kafka client (`realclient_franz.go:108-138`) returns records but tracks no offset cursor for later commit; consume is effectively auto-commit-on-poll. Net effect: a script can "ack" a poisoned message and "rollback" a transaction with zero broker-side consequence, and no Commit/Abort counts exist to surface.

**Goal** — A real per-VUser session/ack/transaction state machine, extracted into a new shared `pkg/protocol/messaging` package, supporting JMS-equivalent `auto`, `client`, `dups_ok`, and `transacted` modes plus Kafka transactional/offset-commit semantics, with rollback-driven redelivery mapped onto each SDK's settlement API. Settlement actions become real and emit `CommitCount`/`AbortCount`/redelivery telemetry consumed by P1.1.

**Design** — Introduce `pkg/protocol/messaging` as a protocol-neutral state machine that both `jms` and `kafka` embed. The core is a per-VUser `Session` owning a list of **unsettled deliveries** (message + SDK-native settlement closures). `Consume` registers an unsettled delivery; `Ack`/`Commit`/`Rollback` resolve them. Mode behavior:
- `auto` — settle immediately inside `Consume`; `jms_ack` is a no-op-with-warning (already settled).
- `dups_ok` — lazy/batched settle; ack flushes the batch.
- `client` — `jms_ack` settles all pending deliveries on the session.
- `transacted` — `Consume`/`Publish` enlist in an open transaction; `commit` settles+commits, `rollback` releases for redelivery and increments `AbortCount`.

Each SDK supplies a `Settler` implementation, so the state machine stays SDK-agnostic and lives behind the existing build-tag split. For Kafka real (`kafka_real`), wire franz-go transactions (`kgo.TransactionSessionOptions`/`kgo.Client.BeginTransaction`/`EndTransaction`) and `CommitRecords`/`CommitOffsets`; the default stub (`realclient_default.go`) returns the fail-closed error. For JMS Service Bus real (`jms_servicebus_real`), map to azservicebus `CompleteMessage`/`AbandonMessage`/`DeferMessage` and transaction scope. The mock `memoryAdapter`/`memoryBroker` (`jms.go:89-120`) gets an in-memory unsettled queue so rollback re-enqueues to the front — giving deterministic, build-tag-free unit coverage. **Rejected alternatives:** (a) per-protocol duplicate state machines — violates the protocol-neutral results mandate and doubles test surface; (b) settling inside `Execute` per action without a session object — cannot model client-mode multi-message ack or transaction enlistment.

**DSL & catalog changes** — No new action *types* (reuse `jms_ack`/`jms_commit`/`jms_rollback`/`kafka_commit`; add `kafka_abort` as a new type for transactional rollback parity). New optional action field `messaging_settle_scope` (enum: `last`, `all`, `transaction`) and config field already exists as `jms_acknowledgement`; add `kafka_transactional_id` and `kafka_isolation_level` (enum: `read_committed`, `read_uncommitted`) to the Kafka config block, plus `jms_acknowledgement` enum tightening to `auto|client|dups_ok|transacted` in `pkg/dsl/catalog*.go`. Run `go generate ./pkg/dsl` to regenerate `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`; freshness is enforced by `pkg/dsl/schemagen`. Add the new enum/field validation in `pkg/dsl/validate.go` (alongside existing Kafka rules near `validate.go:854-859`).

**Types & interfaces** — In new `pkg/protocol/messaging/session.go`:
```go
type AckMode int // AckAuto, AckClient, AckDupsOK, AckTransacted
type Delivery struct { ID string; Settler Settler; Redelivered bool; DeliveryCount int }
type Settler interface {
    Ack(ctx context.Context) error
    Nack(ctx context.Context, requeue bool) error // redelivery on rollback
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}
type Session struct { Mode AckMode; pending []Delivery; tx Settler }
func (s *Session) Track(d Delivery)
func (s *Session) Ack(ctx context.Context, scope SettleScope) (acked int, err error)
func (s *Session) Commit(ctx) (CommitResult, error)   // -> CommitCount++
func (s *Session) Rollback(ctx) (AbortResult, error)  // -> AbortCount++, redeliveries
```
Extend `brokerAdapter` (`jms.go:71-77`) with `Settle(ctx, op SettleOp, deliveries []Delivery) error`. Add `CommitCount int64`, `AbortCount int64`, `RedeliveryCount int64` to the protocol `ServerTelemetry` (`pkg/protocol/protocol.go`) and the `ProtocolSummary`/`ServerTelemetrySummary` in `pkg/engine/metrics.go`; add matching fields to `api/proto/generator.proto` and the merge in `pkg/api/distributed_metrics.go` (P1.1 consumes these).

**Files to touch**
- NEW `pkg/protocol/messaging/session.go`, `session_test.go`, `settler.go`.
- EDIT `pkg/protocol/jms/jms.go:71-77` (interface), `:206-257` (wire Session into Execute, replace `:240-241`).
- EDIT `pkg/protocol/kafka/kafka.go:154-169` (replace `:165-166`, add `kafka_abort`).
- EDIT `pkg/protocol/kafka/realclient_franz.go` (txn begin/commit, offset tracking), `realclient_default.go` (stub errors).
- EDIT `pkg/protocol/jms/realadapter_servicebus.go` (Complete/Abandon/Defer + txn).
- EDIT `pkg/dsl/catalog*.go`, `pkg/dsl/validate.go`, `pkg/dsl/types.go`, `pkg/engine/config_protocols.go`.
- EDIT `pkg/protocol/protocol.go`, `pkg/engine/metrics.go`, `api/proto/generator.proto`, `pkg/api/distributed_metrics.go`.
- REGEN `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`, generated proto.

**Build tags / dependencies** — Reuse `kafka_real` (franz-go already vendored) and `jms_servicebus_real` (azservicebus). No new go.mod modules expected. CI matrix: real-settlement integration tests gated behind `kafka_real` + `jms_servicebus_real`; default/no-tag build must still compile and pass the mock-backed unit suite. The `validate.go:854` unconditional non-mock Kafka rejection must be relaxed under P1.1 so transactional real tests can run — flag as a cross-item coordination point.

**Test plan** — Table-driven unit tests in `pkg/protocol/messaging/session_test.go` over `{mode × op × scope}` against an in-memory `Settler`: auto-mode ack is idempotent no-op; client-mode ack settles all pending; transacted commit increments `CommitCount`; rollback re-enqueues with `Redelivered=true`/`DeliveryCount++` and increments `AbortCount`. JMS mock-broker test asserting rollback re-delivers to queue front (`jms.go:89-120`). Integration (testcontainers Kafka behind `kafka_real`): produce→consume→rollback→reconsume sees same offset; commit advances group offset. Service Bus integration behind `jms_servicebus_real`. Run `go test ./...` and the tag-on builds. No dashboard change beyond the existing messaging filter; if `CommitCount`/`AbortCount` surface in `TestExecution*.tsx`, browser-verify.

**Acceptance criteria**
- [ ] `jms_ack` (client mode) settles pending JMS deliveries; auto mode is a warned no-op.
- [ ] `jms_commit`/`jms_rollback` and `kafka_commit`/`kafka_abort` perform real broker settlement, not Extraction stamps.
- [ ] Rollback redelivers the un-acked message (mock + real Kafka) with `Redelivered`/`DeliveryCount` set.
- [ ] `CommitCount`/`AbortCount`/`RedeliveryCount` populate `ServerTelemetry`, survive the distributed merge, and feed P1.1.
- [ ] `go generate ./pkg/dsl` produces no diff; `pkg/dsl/schemagen` freshness test passes.
- [ ] `go test ./...` green at default build; `kafka_real`/`jms_servicebus_real` integration tests green.

**Effort / dependencies / risks** — Effort **XL**. Depends on **P1.1** (CommitCount/AbortCount telemetry fields + the relaxed `validate.go:854` real-Kafka gate). Risks: (1) franz-go transactional + consumer-group offset semantics are subtle — eager-commit-on-poll in current `Consume` (`realclient_franz.go:117`) must change to deferred commit, risking duplicate delivery if mis-sequenced; (2) azservicebus lock-expiry can invalidate a settler before a deferred ack — needs lock-renewal or bounded session lifetime; (3) IBM MQ stays unavailable (`jms.go:189-190`), so transacted-mode coverage there is intentionally absent and must fail-closed; (4) per-VUser session state increases memory — bound `pending` length to avoid the leak class tracked in prior audits.

### P1.3 — Request-reply correlation primitive with safe requeue

**Problem / current state** — Request-reply is implemented three times with divergent correctness. The JMS dispatcher (`pkg/protocol/jms/jms.go:315-344`) builds the request, defaults `CorrelationID` to the message ID, and delegates to `adapter.RequestReply`. Two of three real adapters are already correct: the Solace AMQP adapter (`pkg/protocol/jms/realadapter_amqp.go:75-122`) loops, matches via the shared-but-amqp-tagged `correlationMatches` (`realadapter_amqp.go:124-132`), and **safely requeues** non-matching replies with `receiver.ReleaseMessage` (line 117); the Kafka franz client (`pkg/protocol/kafka/realclient_franz.go:140-167`) filters within a fetch without committing offsets, so unmatched records stay claimable. **The bug is Service Bus** (`pkg/protocol/jms/realadapter_servicebus.go:88-118`): its loop calls `a.Consume` (line 109), and `Consume` (lines 62-85) unconditionally `CompleteMessage`s the received message at line 81 unless `receive_and_delete`. An uncorrelated reply belonging to another VUser is therefore **permanently destroyed** before the line 113 correlation check runs — under concurrent VUsers on a shared reply queue this drops replies and causes spurious timeouts. Secondary issues: `correlationMatches` is duplicated logic gated behind the `jms_solace_real` tag (untested in default builds); there is no temporary/dynamic reply-destination support — every script must hardcode a shared `reply_to` (`pkg/dsl/catalog_jms.go:38`), the root cause of cross-VUser contention.

**Goal** — A single shared correlation-matcher + reply-destination manager reused by all three JMS adapters and Kafka, so no adapter re-implements publish-then-poll. Fix Service Bus to inspect-before-settle (requeue uncorrelated replies via `AbandonMessage`). Add real per-VUser temporary reply destinations for providers that support them (Service Bus session-id, Solace/AMQP dynamic reply queue), eliminating shared-queue contention.

**Design** — Introduce `pkg/protocol/msgreply` (build-tag-free, default-compiled) holding the protocol-neutral matcher and a `ReplyDestination` resolver. Move `correlationMatches` there as `msgreply.Matches(reqID, replyID string) bool` (preserving permissive-on-empty semantics) and delete the amqp-tagged copy. Define a `Settler` contract — `Accept`/`Requeue` — that each adapter implements over its native settlement API, so the poll-match-requeue loop lives in one `msgreply.Poll(...)` helper parameterized by a `receive func() (msg, handle)` and the `Settler`. AMQP maps Accept→`AcceptMessage`, Requeue→`ReleaseMessage`; Service Bus maps Accept→`CompleteMessage`, Requeue→`AbandonMessage`; Kafka uses a no-op settler (fetch-scoped, uncommitted). **The Service Bus fix is structural**: `RequestReply` must stop calling `Consume` (which settles) and instead `ReceiveMessages` directly, settling via the `Settler` only after the correlation decision. For temporary destinations, prefer Service Bus **session ids** (set request `ReplyTo`/`SessionID` to a per-VUser token, open a session receiver filtered to it — no uncorrelated traffic to requeue) over true temp queues, which Service Bus cannot create at runtime. AMQP uses a dynamic reply node. **Rejected:** a background reply-demux goroutine per connection (correct but over-scoped for M, complicates teardown/leak accounting per the memory-leak audit); keeping per-adapter loops (the current divergence is exactly the defect).

**DSL & catalog changes** — Add to the `jms_request_reply` entry (`pkg/dsl/catalog_jms.go:30-42`) and `kafka_request_reply` (`pkg/dsl/catalog_kafka.go:33-40`): `params.reply_to_mode` (enum `shared` default | `temporary`) and `params.correlation_id` (string; optional explicit override, else message id). Make `params.reply_to` optional when mode=`temporary`. Add `reply_to_mode` to config types in `pkg/dsl/types.go` near `ReplyTo` (line 207). Enforce in `pkg/dsl/validate.go` (enum membership; `temporary` requires a provider that supports it — reject for Kafka, which has no temp-topic concept). Run `go generate ./pkg/dsl` to refresh `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`; freshness is enforced by `pkg/dsl/schemagen`.

**Types & interfaces** — New package `pkg/protocol/msgreply`:
```go
func Matches(requestID, replyID string) bool
type Settler interface { Accept(ctx context.Context, h any) error; Requeue(ctx context.Context, h any) error }
type Polled struct{ Msg any; Handle any }
func Poll(ctx context.Context, deadline time.Time, requestID string, replyID func(any) string,
    recv func(context.Context) (Polled, error), s Settler) (any, error)
type ReplyDestination struct{ Name, SessionID string; Temporary bool }
func ResolveReply(mode, configured, vuserToken string, caps Capabilities) (ReplyDestination, error)
```
Service Bus adapter gains a `sbSettler` (wraps `*azservicebus.Receiver`); session path adds a session-receiver acquisition helper. No `generator.proto` change — results stay protocol-neutral (`Extractions["jms_operation"]="request_reply"`, `jms_request_message_id`).

**Files to touch**
- NEW `pkg/protocol/msgreply/msgreply.go` — matcher, `Poll`, `Settler`, `ResolveReply`.
- NEW `pkg/protocol/msgreply/msgreply_test.go`.
- EDIT `pkg/protocol/jms/realadapter_servicebus.go:88-118` — rewrite `RequestReply` to receive-then-settle via `sbSettler`; **stop routing through `Consume`**; add session-receiver path.
- EDIT `pkg/protocol/jms/realadapter_amqp.go:110-119,124-132` — call `msgreply.Poll`/`Matches`; delete local `correlationMatches`.
- EDIT `pkg/protocol/kafka/realclient_franz.go:159-164` — match via `msgreply.Matches`.
- EDIT `pkg/protocol/jms/jms.go:315-344` — pass `reply_to_mode`/`correlation_id`, resolve temp reply dest.
- EDIT `pkg/dsl/catalog_jms.go`, `catalog_kafka.go`, `types.go`, `validate.go`; regenerated `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`.

**Build tags / dependencies** — No new go.mod deps (`azservicebus.Receiver.AbandonMessage`/session receivers already in the SDK; `azamqp.ReleaseMessage` in use). `pkg/protocol/msgreply` is **untagged/default-compiled** so `Matches`/`Poll` get tested in the standard `go test ./...` run, fixing the current gap where matcher logic only built under `jms_solace_real`. CI matrix: existing `jms_solace_real` / `jms_servicebus_real` / `kafka_real` jobs now also exercise the shared package; add the Service Bus requeue integration assertion to the `jms_servicebus_real` lane.

**Test plan** — Unit (table-driven, default build): `msgreply.Matches` (empty/empty, empty/set, set/empty, equal, unequal); `Poll` with a fake settler asserting Accept-once-on-match and Requeue-on-mismatch and deadline expiry → error; `ResolveReply` per mode/capability. JMS dispatcher: extend `pkg/protocol/jms/jms_test.go` (the mock adapter at `jms.go:383`) with a two-VUser concurrent scenario proving an uncorrelated reply survives for the other claimant. Integration (testcontainers/real, behind tags): `pkg/protocol/jms/servicebus_integration_test.go:92` — assert that after an uncorrelated reply is published, it remains receivable (was destroyed before this fix); add a `temporary` session-id round-trip. Kafka real lane: correlated-reply-among-noise. Dashboard/browser: no UI change (results model unchanged) — verify the messaging surface still renders `request_reply` rows; no browser verification required beyond `npm run build` type-check since no component edits.

**Acceptance criteria**
- [ ] `pkg/protocol/msgreply` exists, default-compiled, with `Matches`/`Poll`/`ResolveReply`; amqp-local `correlationMatches` deleted.
- [ ] Service Bus `RequestReply` no longer routes through `Consume`; uncorrelated replies are `AbandonMessage`'d, not `CompleteMessage`'d.
- [ ] Integration test proves an uncorrelated Service Bus reply survives for its owning VUser (fails on current `main`).
- [ ] `reply_to_mode`/`correlation_id` in catalog + validate; `temporary` rejected for Kafka; `go generate ./pkg/dsl` clean, schemagen freshness test passes.
- [ ] AMQP and Kafka adapters call the shared matcher; `go test ./...` and all three real-tag lanes green.

**Effort / dependencies / risks** — **M** (the hard correctness fix is isolated to Service Bus; AMQP/Kafka are refactors-to-shared, not rewrites). Depends on **P1.1** (shared message-body/header model the matcher reads `CorrelationID`/`Headers` from). Risks: (1) Service Bus `AbandonMessage` increments delivery count toward dead-lettering under churn — document and keep max-delivery high on reply queues, or prefer the session path which avoids requeue entirely; (2) session-receiver acquisition adds a per-VUser receiver — account for it against the memory-leak audit (ensure teardown closes session receivers in `Teardown`, `realadapter_servicebus.go:131`); (3) `temporary` mode for AMQP dynamic nodes is provider-dependent — gate behind a capability check in `ResolveReply` and fail-closed with a clear error rather than silently downgrading to shared.

Verified files: `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/protocol/jms/realadapter_servicebus.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/protocol/jms/realadapter_amqp.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/protocol/jms/jms.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/protocol/kafka/realclient_franz.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/protocol/kafka/kafka.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/dsl/catalog_jms.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/dsl/catalog_kafka.go`, `/Users/jasonczech/Documents/GitHub/Project_Stormrunner/pkg/dsl/types.go`.

NOTE: The cited touchpoints `realadapter_amqp.go:75-122` and `kafka.go` already implement correct correlation + safe requeue; the live defect is confined to Service Bus (`realadapter_servicebus.go:88-118` routing through the settling `Consume` at line 81). Scope is therefore "fix Service Bus + extract/dedup the already-correct AMQP/Kafka logic + add temporary destinations," not a greenfield primitive.

### P1.4 — DSL catalog enrichment + catalog/runtime parity

**Problem / current state** — The messaging action specs carry no enum metadata and omit most params the runtime actually reads. `pkg/dsl/catalog_kafka.go:6-58` declares only `target`/`body`/`params.topic`/`params.key`/`params.partition`/`params.offset`/`params.timeout_ms`/`params.reply_to`; `pkg/dsl/catalog_jms.go:7-58` declares only `target`/`body`/`params.subscription`/`params.reply_to`/`params.timeout_ms`. None of the config-level messaging enums are surfaced as `FieldKindEnum` — they exist only as free-form validator string-lists (`validateKafkaScriptIssues` at `pkg/dsl/validate.go:837-880` checks `security_protocol`/`sasl_mechanism`/`acks`/`compression`/`partition_strategy`/`auto_offset_reset` via `isAllowedValue`; the JMS validator at `validate.go:686-758` checks neither `delivery_mode` nor `acknowledgement` at all). Meanwhile the runtime reads undeclared params: `correlation_id` (`jms.go:692`, `kafka.go:283`), `destination` (`jms.go:717`), `destination_type` (`jms.go:728,811`), `dead_letter` (`jms.go:822`), `consumer_group` (`kafka.go:343`). The result: the dashboard editor, `GET /api/v1/dsl/actions`, `docs/dsl.schema.json`, and `docs/DSL_ACTION_REFERENCE.md` cannot offer enum pickers and silently hide valid params from authors. Ground-truth caveat: the work-item params `priority`, `ttl`, `selector`, `session_id`, `message_type`, `partition_key` are NOT read by today's runtime (verified absent from `pkg/protocol/jms/` and `pkg/protocol/kafka/`).

**Goal** — Make the catalog a faithful, machine-readable contract: every messaging enum is declared as `FieldKindEnum` with the exact allowed values, every runtime-read param is declared, and JMS `delivery_mode`/`acknowledgement` are validated like the Kafka enums. Editors and generated docs then expose accurate pickers and field lists.

**Design** — Enums live at config level (`config.kafka.*`, `config.jms.*`), but `FieldSpec` is per-action; config fields aren't part of `ActionSpec`. Two-part approach: (1) declare the per-action params as `FieldSpec` entries on the existing action specs (the natural home), and (2) for config-level enums, add them via the existing config-field surfacing path used by other protocols — confirm whether `catalog_*.go` exposes config specs; if not, the minimum viable change is to (a) make the validator the single enum source and (b) add an `Enum` slice to the validation error path so the allowed set is discoverable. To avoid drift, define the allowed-value lists ONCE as package vars (e.g. `kafkaAcksEnum = []string{"none","leader","all"}`) and reference them from BOTH `isAllowedValue` in `validate.go` and the `FieldSpec.Enum` declarations. Rejected: hardcoding enum literals twice (drift risk); inventing runtime support for the six unread params (out of scope — declaring fields the runtime ignores would mislead authors, so they are explicitly deferred to a runtime work item and NOT added here).

**DSL & catalog changes** — Add `FieldKindEnum` `FieldSpec`s. Kafka (`catalog_kafka.go`): document `params.consumer_group` (string) on `kafka_consume`/`kafka_commit`/`kafka_request_reply`; `params.correlation_id` on `kafka_request_reply`. JMS (`catalog_jms.go`): add `params.correlation_id`, `params.destination`, `params.destination_type` (enum `queue|topic`), `params.dead_letter` (bool) to `jms_publish`/`jms_consume`/`jms_request_reply` as applicable. Config enums (allowed sets, exact values from `validate.go`): `acks` = none/leader/all; `compression` = none/gzip/snappy/lz4/zstd; `partition_strategy` = key-hash/round-robin/explicit; `auto_offset_reset` = earliest/latest; `security_protocol` = plaintext/ssl/sasl_ssl/sasl_plaintext; `sasl_mechanism` = plain/scram-sha-256/scram-sha-512/oauthbearer; JMS `acknowledgement` = auto/client/dups_ok/transacted (confirm against `jms.go` usage); `delivery_mode` = persistent/non_persistent; `destination_type` = queue/topic. Regenerate: `go generate ./pkg/dsl` (directive at `catalog.go:1`); freshness enforced by `TestGeneratedSchemaIsFresh`/`TestGeneratedReferenceIsFresh` (`pkg/dsl/schemagen/schemagen_test.go:17,36`).

**Types & interfaces** — No new types; `FieldSpec` already has `Enum []string` (`catalog.go:36`). Add package-level vars in `validate.go`:
```go
var (
    jmsDeliveryModeEnum    = []string{"persistent", "non_persistent"}
    jmsAcknowledgementEnum = []string{"auto", "client", "dups_ok", "transacted"}
)
```
Extend `validateJMSScriptIssues` to validate `script.Config.JMS.DeliveryMode`/`.Acknowledgement` via `isAllowedValue(...)` mirroring the Kafka block (`validate.go:857-872`).

**Files to touch** —
- EDIT `pkg/dsl/catalog_kafka.go:6-58` — add param `FieldSpec`s.
- EDIT `pkg/dsl/catalog_jms.go:7-58` — add param + enum `FieldSpec`s.
- EDIT `pkg/dsl/validate.go:686-758` (JMS validator) — add delivery_mode/acknowledgement enum checks; introduce shared enum vars and reference from Kafka block at `837-880`.
- EDIT (regenerated, do not hand-edit) `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`.
- EDIT `pkg/dsl/catalog_test.go` — new spec assertions.
- EDIT `pkg/dsl/validate_test.go` (or NEW if absent) — JMS enum validation cases.

**Build tags / dependencies** — None. No go.mod changes, no build tags; pure metadata/validation. CI tag matrix unaffected (`kafka_real`/`jms_*_real` paths untouched).

**Test plan** — Unit (table-driven): in `catalog_test.go`, assert each new `FieldSpec` is present with correct `Kind`/`Enum` (e.g. `jms_publish` has `params.destination_type` with `Enum == jmsDestinationTypeEnum`). In `validate_test.go`, table of `(delivery_mode, acknowledgement) → wantIssue` covering valid values, invalid value, empty (allowed), and case-insensitivity to match `isAllowedValue`'s `strings.ToLower`. Add a freshness guard run: `go generate ./pkg/dsl` then `go test ./pkg/dsl/...` must pass `TestGeneratedSchemaIsFresh`/`TestGeneratedReferenceIsFresh`. No new integration/broker tests (no runtime behavior change). Dashboard: spot-check `GET /api/v1/dsl/actions?protocol=jms` returns the enum arrays and that the editor renders enum pickers (browser verification per policy, since UI consumes the catalog).

**Acceptance criteria** —
- [ ] All nine config enums are discoverable with exact allowed values (catalog enum and/or validator error lists agree).
- [ ] `correlation_id`, `destination`, `destination_type`, `dead_letter`, `consumer_group` are declared `FieldSpec`s on the correct action types.
- [ ] `validateJMSScriptIssues` rejects an invalid `delivery_mode` and invalid `acknowledgement`; accepts valid + empty.
- [ ] Enum allowed-value lists are defined once and shared between validator and catalog (no duplicated literals).
- [ ] `go generate ./pkg/dsl` produces no diff after commit; `go test ./...` green.
- [ ] The six unread params (`priority`/`ttl`/`selector`/`session_id`/`message_type`/`partition_key`) are explicitly NOT added, with a code comment pointing to the runtime follow-up item.

**Effort / dependencies / risks** — Effort: **M** (confirmed; bounded to two catalog files + one validator + regenerated docs + tests). Dependencies: none upstream. Risks: (1) **scope creep / honesty** — the work item lists six params the runtime ignores; declaring them would create a catalog/runtime lie, so they are deferred (flag to program owner). (2) **config-vs-action enum placement** — `FieldSpec` is action-scoped; if no config-field catalog surface exists, config enums may only be enforceable via the validator, not the action catalog — verify the config-spec path before promising enum pickers for `config.kafka.*` fields. (3) Forgetting `go generate` breaks CI via the freshness tests — include it in the PR checklist.

### P1.5 — Consumer-lag / queue-depth / DLQ telemetry abstraction + dashboard surfacing

**Problem / current state** — Backlog signals are fragmented and partially discarded across three protocols. Kafka's `franzClient.Consume` computes a real **offset** lag (`pkg/protocol/kafka/realclient_franz.go:133-137`), but the caller only stuffs it into a string extraction `result.Extractions["kafka_consumer_lag"]` (`pkg/protocol/kafka/kafka.go:237`), consumed solely by the `consumer_lag_below` assertion (`kafka.go:478-480`). It never reaches `ServerTelemetry`. The field that *does* flow through (`ServerTelemetry.ConsumerLagMs`, `protocol.go:191`; proto field 8, `generator.proto:132`; aggregated as `AvgConsumerLagMs/MaxConsumerLagMs`, `metrics.go:122-123,825-827`) is **wall-clock** age (`kafka.go:388`), not offset depth — two different units share one field. JMS DLQ handling is metadata-only: `isDeadLetterAction` flips `Extractions["jms_dead_letter"]="true"` (`jms.go:219-222`) with no count and no queue-depth probe. There is no queue-depth probe anywhere; Solace/SB adapters expose only Publish/Consume/RequestReply/HealthCheck (`jms.go:602-623`). The dashboard hard-filters the messaging panel to `websocket`/`grpc` (`TestExecutionSummary.tsx:1372-1374`), so `kafka_topic`/`kafka_consumer_group`/`jms_queue` rows never render; the lag chart reads only `avg_consumer_lag_ms` (`testExecutionMetricsModel.ts:81-96`).

**Goal** — One protocol-neutral backlog abstraction surfacing three distinct, correctly-typed signals — **offset/message lag (depth)**, **queue depth (uncommitted/enqueued)**, and **DLQ message count** — merged into `ServerTelemetrySummary`, distributed-merge-safe, with percentile charts and Kafka/JMS rows visible on the dashboard.

**Design** — Split the overloaded `ConsumerLagMs` semantics: keep it as time-lag, add a typed **`BacklogDepth int64`** (offset/message count) plus **`QueueDepth int64`** and **`DeadLetterCount int64`** to `ServerTelemetry`. Kafka: stop discarding the franz offset lag — thread it from `Consume` (it already returns `lag`) into `telemetry.BacklogDepth` in the telemetry finalizer (alongside `kafka.go:388`), keeping the string extraction for backward-compat assertions. Add an optional **`BacklogProbe` capability interface** (mirroring `CookieController`, `protocol.go:62`) that protocols implement when they can cheaply probe depth/DLQ without consuming. JMS: implement `BacklogProbe` on the Solace AMQP adapter via AMQP management/`queue browse`, and on Service Bus via `azservicebus` runtime properties (`ActiveMessageCount`/`DeadLetterMessageCount`) — both behind their existing real-build tags; the default stubs return `ErrBacklogUnsupported` (fail-open, no telemetry). Replace DLQ metadata flagging with a true `DeadLetterCount` from the probe; if no probe, fall back to counting DLQ-destination consumes (current behavior, but as a count not a bool). **Rejected:** a separate "monitor" action type (clutters the catalog, breaks pacing) — probing rides existing consume/publish results via the capability hook. **Rejected:** reusing `ConsumerLagMs` for offset lag (unit collision already burned us).

**DSL & catalog changes** — Optional, additive. Extend the existing `consumer_lag_below` assertion family with **`backlog_depth_below`** and **`dlq_count_below`** assertion types in `pkg/dsl/catalog*.go` + `pkg/dsl/validate.go`. Add an optional config flag **`probe_backlog: bool`** (default false) on kafka/jms config in `pkg/dsl/types.go` to gate the management-API probe (avoids per-consume cost when off). Run `go generate ./pkg/dsl` to refresh `docs/dsl.schema.json` + `DSL_ACTION_REFERENCE.md` (freshness enforced by `pkg/dsl/schemagen`).

**Types & interfaces**
```go
// pkg/protocol/protocol.go — ServerTelemetry additions
BacklogDepth    int64 `json:"backlog_depth,omitempty"`     // offset/message lag (count)
QueueDepth      int64 `json:"queue_depth,omitempty"`       // enqueued/uncommitted
DeadLetterCount int64 `json:"dead_letter_count,omitempty"`

// pkg/protocol/protocol.go — new optional capability
type BacklogProbe interface {
    ProbeBacklog(ctx context.Context, destination string) (BacklogStats, error)
}
type BacklogStats struct{ Depth, QueueDepth, DeadLetterCount int64 }
var ErrBacklogUnsupported = errors.New("backlog probe not supported in this build")
```
`metrics.go` `ServerTelemetrySummary`: add `AvgBacklogDepth/MaxBacklogDepth/AvgQueueDepth/MaxQueueDepth/TotalDeadLetterCount` (mirror the lag accumulate/divide at `metrics.go:825-849`). `generator.proto` `ServerTelemetry`: fields 16–18 (`backlog_depth`, `queue_depth`, `dead_letter_count`); regenerate; merge in `distributed_metrics.go:440-500`.

**Files to touch**
- EDIT `pkg/protocol/protocol.go:169-193` — telemetry fields + `BacklogProbe`/`BacklogStats`/`ErrBacklogUnsupported`.
- EDIT `pkg/protocol/kafka/kafka.go:229-241,~388` — thread franz offset `lag` into `telemetry.BacklogDepth`.
- EDIT `pkg/protocol/jms/jms.go:209-222,602-658` — replace bool DLQ flag with `DeadLetterCount`; wire probe.
- NEW `pkg/protocol/jms/backlog_amqp.go` (`jms_solace_real`) / `backlog_servicebus.go` (`jms_servicebus_real`) + `backlog_default.go` (stub).
- EDIT `pkg/engine/metrics.go:115-130,820-850`; `pkg/api/distributed_metrics.go:430-500`; `api/proto/generator.proto:124-140`.
- EDIT `pkg/dsl/catalog*.go`, `pkg/dsl/validate.go`, `pkg/dsl/types.go`; regenerate `docs/*`.
- EDIT `dashboard/src/components/TestExecutionSummary.tsx:1372-1374` (drop ws/grpc-only filter — render any tx with messaging/backlog telemetry), `testExecutionMetricsModel.ts:81-96` (add backlog/queue percentiles), `TestExecution*.tsx` (charts).

**Build tags / dependencies** — Reuse existing `jms_solace_real`/`jms_servicebus_real`. Service Bus probe uses `azservicebus` admin client (likely already in go.mod via P1.1 SB work — verify); Solace AMQP probe reuses the AMQP client in `realadapter_amqp.go`. No new tags. CI matrix: add backlog assertions to the existing real-adapter tag jobs; default (mock) build must still compile and pass with stubs.

**Test plan** — Unit (table-driven): `ServerTelemetrySummary` accumulate/divide for backlog/queue/DLQ incl. zero-sample and max-tracking (`metrics_test.go`); distributed merge across two generators (`distributed_metrics_test.go`); kafka offset-lag → `BacklogDepth` mapping with a fake `Consume` returning known lag; DLQ count from probe vs fallback; `validate.go` accepts/rejects new assertions; schemagen freshness. Integration (testcontainers, behind `kafka_real`/`jms_servicebus_real`): produce N, consume 1, assert `BacklogDepth==N-1`; enqueue to a real SB queue + DLQ, assert `QueueDepth`/`DeadLetterCount`. Dashboard/browser: load a run with a `kafka_topic` tx, confirm the row now renders and backlog/queue percentile charts populate (type-check alone insufficient per policy).

**Acceptance criteria**
- [ ] Kafka offset lag flows to `ServerTelemetry.BacklogDepth` (not discarded); `ConsumerLagMs` remains time-only.
- [ ] `BacklogProbe` implemented for SB + Solace real builds; default stub returns `ErrBacklogUnsupported` and compiles.
- [ ] `DeadLetterCount` is a true integer count; bool-only flagging removed from `jms.go:219-222`.
- [ ] New summary fields merge correctly across distributed generators; proto regenerated and committed.
- [ ] `backlog_depth_below`/`dlq_count_below` assertions validate; `go generate ./pkg/dsl` clean; freshness test green.
- [ ] Dashboard renders kafka/jms rows (ws/grpc-only filter gone) with lag/depth percentile charts.
- [ ] `go test ./...` green on default build; real-adapter tag jobs green.

**Effort / dependencies / risks** — Effort **L** (confirmed; 3 protocols + proto + dashboard). Depends on **P1.1** (real SB/Event Hubs adapter — the SB management client and AMQP path land there). Risks: (1) management/browse APIs add latency and broker permissions — gate via `probe_backlog` and probe out-of-band, not per-consume; (2) IBM MQ has no real adapter (`jms.go:629-658`) so it returns `ErrBacklogUnsupported` — document, don't fake; (3) unit collision regression if any existing chart/assertion silently relied on `ConsumerLagMs` carrying offset data — grep for `consumer_lag` readers before shipping; (4) Solace AMQP-over-JMS may not expose native SMF queue depth — verify the management endpoint exists or mark queue-depth unsupported for Solace.

### P2.1 — Kafka producer/consumer depth (idempotence, transactions/EOS, isolation, batching, seek)

**Problem / current state** — The franz adapter is shallow. The acks switch at `realclient_franz.go:53-60` force-calls `kgo.DisableIdempotentWrite()` on both `none` and `leader`, and never opts into idempotence on `all` (it just sets `AllISRAcks()`), so exactly-once producer semantics are unreachable. There is no `transactional.id`, no `Begin/Commit/Abort` — `kafka_commit`/`kafka_seek` are pure no-ops (`kafka.go:165-166` only writes `result.Extractions["kafka_operation"]`). `Consume` calls `cl.AddConsumeTopics` (`realclient_franz.go:113`) with no `read_committed` isolation and a hardcoded `PollRecords(cctx, 1)` (no `max.poll.records`); subscribe-vs-assign is implicit. `config.kafka.properties` is parsed (`kafka.go:89`, plumbed at `config_protocols.go:513`) but never applied — `linger.ms`/`batch.size` are dropped. The `kafkaClient` interface (`broker.go:17-24`) has no `Seek`. Worst, `validate.go:854` *unconditionally* appends "real Kafka network adapter is not available in this build" for any non-mock script, so even a `kafka_real` build cannot author/run a real script.

**Goal** — Bring the real adapter to Kafka producer/consumer spec: idempotent producer, transactional EOS (`transactional.id` + begin/commit/abort), `read_committed` consumer isolation, batching tunables (`linger.ms`/`batch.size` via `properties`), real `Seek` (offset/earliest/latest, per partition), explicit subscribe-vs-assign, and `max.poll.records`. Mock broker must support the same surface so CI exercises it without a real broker.

**Design** — Extend the `config` struct and DSL `KafkaConfig` with the new producer/consumer knobs, plumb them through `config_protocols.go:appendKafkaConfig` and `kafka.go:Init`. In the franz adapter: stop unconditionally disabling idempotence — only disable when `acks != all` (franz auto-enables idempotence at AllISRAcks when no `transactional.id`); add `kgo.TransactionalID` + `kgo.RequiredAcks(AllISRAcks())` when a tx id is set; add `kgo.FetchIsolationLevel(kgo.ReadCommitted())` when `isolation.level=read_committed`; apply `linger.ms`/`batch.size` from `properties` via `kgo.ProducerLinger` / `kgo.ProducerBatchMaxBytes`; honor `max.poll.records` in the `PollRecords` cap. Add `Seek` to the `kafkaClient` interface and implement on franz (`kgo.Client.SetOffsets` for assign, or `AddConsumePartitions` for explicit assign) and on the mock broker. Make `kafka_commit`/`kafka_seek`/transaction actions dispatch to the client rather than being string no-ops. The transaction lifecycle is per-VUser (the protocol instance is per-goroutine), so a `kafka_txn_begin`/`kafka_txn_commit`/`kafka_txn_abort` triad maps to `cl.BeginTransaction()` / `cl.EndTransaction(ctx, kgo.TryCommit|TryAbort)`. **Rejected:** a single auto-commit-on-produce flag (insufficient for multi-message EOS units) and inferring transactions from acks (ambiguous). Fix `validate.go:854` to gate the rejection on whether the binary is a `kafka_real` build via a build-tagged `realAdapterAvailable()` seam (default false, true under `kafka_real`), preserving fail-closed default builds.

**DSL & catalog changes** — `KafkaConfig` (`types.go:222`) gains: `enable_idempotence *bool`, `transactional_id string`, `isolation_level string` (`read_uncommitted|read_committed`), `max_poll_records int`, `assignment_mode string` (`subscribe|assign`). New action types in `catalog_kafka.go`: `kafka_txn_begin`, `kafka_txn_commit`, `kafka_txn_abort` (protocols `["kafka"]`, no fields). Enrich `kafka_seek` field docs (already has `params.offset`/`params.partition`). Add validate.go enum checks for `isolation_level` and `assignment_mode`. Run `go generate ./pkg/dsl` to regenerate `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md` (freshness enforced by `pkg/dsl/schemagen`).

**Types & interfaces** —
```go
// broker.go kafkaClient interface, add:
Seek(ctx context.Context, topic string, partition int32, offset int64, named string) error
BeginTxn(ctx context.Context) error
EndTxn(ctx context.Context, commit bool) error
// config struct (kafka.go) add:
EnableIdempotence *bool; TransactionalID string; IsolationLevel string; MaxPollRecords int; AssignmentMode string
```
`recordMeta` unchanged. franz `linger`/`batch` read from `cfg.Properties["linger.ms"]`/`["batch.size"]`.

**Files to touch** —
- EDIT `pkg/protocol/kafka/realclient_franz.go:53-60` (acks/idempotence), `:113` (isolation+max.poll), add Seek/Begin/EndTxn — NEW methods.
- EDIT `pkg/protocol/kafka/broker.go:17-24` (interface), mock `Seek`/`BeginTxn`/`EndTxn` impls; `unavailableClient` stubs.
- EDIT `pkg/protocol/kafka/kafka.go:41-64` (config), `:73-133` (Init), `:165-166` (dispatch txn/commit/seek to client).
- EDIT `pkg/protocol/kafka/realclient_default.go` — stub new methods (return `unavailableClient.err()`).
- EDIT `pkg/dsl/types.go:222`, `pkg/dsl/catalog_kafka.go:44-57`, `pkg/dsl/validate.go:854,856-869`.
- EDIT `pkg/engine/config_protocols.go:452-518` (plumb new keys).
- NEW build-tagged `realadapteravail_default.go` / `realadapteravail_real.go` for `realAdapterAvailable()`.
- REGEN `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`, `docs/swagger.yaml` if routes touched (they aren't).

**Build tags / dependencies** — No new go.mod deps (franz-go already vendored). New work lives under existing `kafka_real` tag; the `realAdapterAvailable` seam needs the `kafka_real`/default file pair. CI: the integration suite stays behind `kafka_integration` (live broker); add testcontainers-kafka for the EOS path. Validate the default (no-tag) build still compiles and fails closed.

**Test plan** — Unit (table-driven, default build, mock broker): partition/seek/offset resolution; `kafka_txn_*` dispatch reaches the client; config knob plumbing (`enable_idempotence`/`transactional_id`/`isolation_level`/`max_poll_records` land in the map); validate.go enum rejection for bad `isolation_level`/`assignment_mode`; validate.go *allows* a non-mock script when `realAdapterAvailable()` is true (build-tagged test). Mock broker: Seek to earliest/latest/explicit changes the next consumed offset; txn begin→produce→abort makes records invisible to a `read_committed` mock consumer. Integration (`-tags kafka_real,kafka_integration`, testcontainers): idempotent producer dedup on retry; transactional produce visible only after commit; `read_committed` consumer skips aborted batch; `linger.ms`/`batch.size` honored (batch metadata). Schemagen freshness test must pass post-`go generate`. No dashboard change (P3.x covers Kafka rows); confirm `go test ./...` green.

**Acceptance criteria** —
- [ ] `acks: all` enables idempotence; only `none`/`leader` disable it (no blanket `DisableIdempotentWrite`).
- [ ] `transactional_id` set → producer is transactional; `kafka_txn_begin/commit/abort` actions drive `BeginTransaction`/`EndTransaction`.
- [ ] `isolation_level: read_committed` consumer skips aborted records (integration-proven).
- [ ] `linger.ms` / `batch.size` from `properties` reach franz producer opts.
- [ ] `Seek` on interface + franz + mock; `kafka_seek` honors numeric offset / `earliest` / `latest` + partition.
- [ ] `assignment_mode` selects subscribe vs assign; `max_poll_records` caps `PollRecords`.
- [ ] `validate.go:854` no longer hard-rejects non-mock scripts in a `kafka_real` build; default build still fails closed.
- [ ] `go generate ./pkg/dsl` regenerated artifacts committed; `go test ./...` and schemagen freshness pass.

**Effort / dependencies / risks** — Effort **XL**. Depends on **P1.2** (config-plumbing/properties pass) and **P1.4** (validate gating / real-adapter-available seam). Risks: franz idempotence/transaction interaction with `RequiredAcks` is order-sensitive (must be `AllISRAcks` for txns, easy to misconfigure); per-VUser transactional.id must be unique per producer or brokers fence each other — derive a VUser-suffixed id; mock-broker txn/isolation semantics are an approximation and could diverge from real broker behavior (document as best-effort); the `validate.go:854` change is fail-open-adjacent if the build-tag seam is wrong — test both build variants explicitly.

### P2.2 — Schema Registry / Avro / Protobuf / JSON-Schema serde

**Problem / current state** — Kafka records are opaque byte payloads end-to-end. `kafkaRecord.Value` is `[]byte` (`pkg/protocol/kafka/broker.go:29`), built directly from `action.Body` (`pkg/protocol/kafka/kafka.go:295`) and written/read raw by the franz adapter (`realclient_franz.go:85`, `:137`). Extractors run against the raw bytes (`kafka.go:421-427` regex/JSONPath over `rec.Value`). There is no Confluent wire framing (magic byte `0x00` + 4-byte schema-id), no Schema Registry client, and no Avro/Protobuf/JSON-Schema codec. `config` (`kafka.go:41-64`) and `KafkaConfig` (`pkg/dsl/types.go:223-242`) carry no schema fields. The AsyncAPI importer explicitly punts: `asyncapi-payload-avro` is `review_required` and `asyncapi-schema-registry` is `not_executable` with note "Schema registry integration (Avro/Protobuf encode/decode) is not yet implemented" (`pkg/convert/asyncapi/support.go:25,28,37`).

**Goal** — Produce/consume Confluent-wire-format records (`magic 0x00` + big-endian 4-byte schema id; Protobuf adds a message-index varint array) with Avro / Protobuf / JSON-Schema encode on produce and decode on consume/request-reply, backed by a Schema Registry client (subject-name strategy + ID cache). Expose decoded payloads to extractors/assertions, emit registry round-trip latency telemetry, add catalog fields, and flip the AsyncAPI matrix entries to executable.

**Design** — Introduce a `kafka.serdes` sub-layer behind a small interface so framing/codec is independent of transport (mock and franz alike). A `serde` is selected per-config `value_format` (`none|avro|protobuf|jsonschema`); `none` preserves today's raw passthrough (zero behavior change, default). On produce, `buildRecord` (`kafka.go:281`) hands `action.Body` to `serde.Encode(subject, payload)` which resolves/registers the schema id, encodes the payload, and prepends the Confluent frame. On consume/request-reply, `serde.Decode(value)` strips the frame, looks up the schema by id (cache-first), decodes to canonical JSON, and stores both `rec.Value` (decoded JSON, so existing JSONPath/regex extractors at `kafka.go:421-427` work unchanged) and `Extractions["kafka_schema_id"]`/`["kafka_value_format"]`. Raw framed bytes stay available via `Extractions["kafka_raw_b64"]`.

The Schema Registry **client** is build-tag gated like the broker: a real HTTP client (`serde_registry_real.go`, tag `kafka_real`, reusing the existing TLS/SASL knobs for registry basic-auth) and a fail-closed default stub (`serde_registry_default.go`) that errors with actionable guidance unless `config.kafka.mock` supplies an in-memory registry (mirrors `realclient_default.go:9`). Subject naming strategy is `topic_name` (default) | `record_name` | `topic_record_name`. Schema-id→codec and subject→id caches are `sync.Map` on the registry client (per-VUser-process, shared like `sharedBroker` at `broker.go:64`).

Rejected alternatives: (a) decoding inside the franz adapter — couples serde to the real build and leaves the mock untested; (b) a new `kafka_avro` action type — the format is a record property, not an operation, so it belongs in config + per-action override params; (c) vendoring a heavy registry SDK — a thin HTTP client over `/subjects/.../versions` + `/schemas/ids/{id}` is sufficient and tag-isolatable.

Registry latency: add `RegistryLookupMs float64 json:"registry_lookup_ms,omitempty"` to `protocol.ServerTelemetry` (`pkg/protocol/protocol.go:169`), populated in `finalizeKafkaServerTelemetry` (`kafka.go:370`); propagate through `api/proto/generator.proto` + `pkg/api/distributed_metrics.go` merge and surface in `testExecutionMetricsModel.ts`.

**DSL & catalog changes** — Extend `KafkaConfig` (`types.go:223`) with `ValueFormat`, `KeyFormat`, `SchemaRegistryURL`, `SchemaRegistryAuth` (`*KafkaRegistryAuth{Username,Password,PasswordSecret}`), `SubjectNameStrategy`, `ValueSchema`/`ValueSchemaFile` (inline schema for produce/auto-register), `AutoRegisterSchemas bool`. Add per-action override fields in `catalog_kafka.go` (`kafka.go:6`) on `kafka_produce`/`kafka_request_reply`: `params.value_format`, `params.subject`, `params.schema_id`. New enums: `value_format ∈ {none,avro,protobuf,jsonschema}`; `subject_name_strategy ∈ {topic_name,record_name,topic_record_name}`. Add validation in `pkg/dsl/validate.go` (require `schema_registry_url` when format ≠ none; require schema source for produce when `auto_register`). Regenerate with `go generate ./pkg/dsl` (freshness enforced by `pkg/dsl/schemagen`; updates `docs/dsl.schema.json` + `DSL_ACTION_REFERENCE.md`).

**Types & interfaces** — New `pkg/protocol/kafka/serde.go`:
```go
type serde interface {
    Encode(ctx context.Context, subject string, payload []byte) (framed []byte, schemaID int, err error)
    Decode(ctx context.Context, framed []byte) (decoded []byte, schemaID int, err error)
    Format() string
}
type schemaRegistry interface {
    SchemaByID(ctx context.Context, id int) (schemaText, schemaType string, err error)
    IDForSubject(ctx context.Context, subject, schemaText, schemaType string) (id int, err error)
}
func newSerde(cfg config) (serde, error)        // returns rawSerde for "none"
func newSchemaRegistry(cfg config) schemaRegistry // build-tag gated
```
`config` gains `ValueFormat, KeyFormat, RegistryURL, RegistryUser, RegistryPass, SubjectStrategy, ValueSchema string; AutoRegister bool`. Confluent framing helpers `frame(id int, msgIndexes []int, body []byte)` / `unframe(b []byte) (id int, idx []int, body []byte, err error)` live in `serde_wire.go`.

**Files to touch**
- EDIT `pkg/protocol/kafka/kafka.go` — wire serde into `produce`/`consume`/`requestReply` (:204,:224,:244), `buildRecord` (:281), Init config load (:73), telemetry (:370), new extractions.
- NEW `pkg/protocol/kafka/serde.go`, `serde_wire.go`, `serde_avro.go`, `serde_protobuf.go`, `serde_jsonschema.go` (+ `_test.go` each).
- NEW `pkg/protocol/kafka/serde_registry_real.go` (`//go:build kafka_real`) and `serde_registry_default.go` (`//go:build !kafka_real`) + in-memory `serde_registry_mock.go`.
- EDIT `pkg/protocol/kafka/realclient_franz.go` — pass framed bytes through unchanged (it already round-trips `rec.Value`); add registry basic-auth dial if needed.
- EDIT `pkg/dsl/types.go:223`, `pkg/dsl/catalog_kafka.go`, `pkg/dsl/validate.go`, `pkg/engine/config_protocols.go:453` (plumb new `kafka_*` keys).
- EDIT `pkg/protocol/protocol.go:169` (telemetry field), `api/proto/generator.proto`, `pkg/api/distributed_metrics.go`, `pkg/api/telemetry.go`.
- EDIT `pkg/convert/asyncapi/converter.go` + `support.go:25,28,37` (emit format/subject/registry config; flip matrix to executable).
- EDIT `dashboard/src/components/testExecutionMetricsModel.ts` + `TestExecution*.tsx` (registry-latency row).
- Regenerated: `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`, `docs/swagger.yaml`.

**Build tags / dependencies** — `go.mod`: `github.com/hamba/avro/v2` (Avro), `google.golang.org/protobuf` (already present, for dynamicpb), `github.com/santhosh-tekuri/jsonschema/v6` (JSON-Schema validate). Codecs compile in the default build (pure-Go, no network). The registry HTTP client and live wiring are gated behind the existing `kafka_real` tag; default build keeps fail-closed stub. CI: serde unit tests run in the default lane; registry/round-trip integration runs in the existing `kafka_integration` + `kafka_real` lane (add a Schema Registry container alongside the broker).

**Test plan** — Unit, table-driven (default build): `serde_wire_test.go` frame/unframe round-trip incl. Protobuf message-index varints and malformed-frame errors (truncated, wrong magic byte); `serde_avro_test.go`/`serde_protobuf_test.go`/`serde_jsonschema_test.go` encode→decode fidelity + decode-to-JSON for extractor reuse; `kafka_test.go` additions asserting decoded `rec.Value` feeds JSONPath extractors (`kafka.go:238`) and `Extractions["kafka_schema_id"]`. Mock-registry tests cover all three subject strategies + cache hits (one registry call per unseen id). Integration (tag `kafka_real,kafka_integration`, testcontainers broker + Schema Registry): produce Avro, consume with a second registry, assert frame/id and decoded payload; assert `RegistryLookupMs > 0`. AsyncAPI: converter test that an Avro `schemaFormat` spec yields `value_format: avro` + registry config and validates. Dashboard: browser-verify the registry-latency metric row renders on a Kafka run (Recharts). `go test ./...` and `go test -tags kafka_real ./pkg/protocol/kafka/...` must pass.

**Acceptance criteria**
- [ ] `value_format: none` (default) produces byte-identical behavior to today; no telemetry/extraction regressions.
- [ ] Produce emits a valid Confluent frame (`0x00` + 4-byte BE id; Protobuf message-index varints) verifiable on the wire against a real broker.
- [ ] Avro/Protobuf/JSON-Schema decode on consume populates `rec.Value` as JSON so existing JSONPath/regex extractors and assertions pass unchanged.
- [ ] Registry calls are cached: ≤1 lookup per unseen schema id/subject per process (asserted by mock call counter).
- [ ] Default build with a non-`none` format and no mock registry fails closed with actionable guidance.
- [ ] `RegistryLookupMs` flows generator→controller→dashboard and renders.
- [ ] `go generate ./pkg/dsl` + `./pkg/api` leave a clean tree; freshness tests pass.
- [ ] AsyncAPI matrix entries `asyncapi-payload-avro` and `asyncapi-schema-registry` flip to executable; converter emits registry config.
- [ ] Full + tagged test suites green.

**Effort / dependencies / risks** — Effort: **XL**. Depends on **P2.1** (real-broker serde plumbing / config groundwork). Risks: (1) Protobuf message-index varint framing and `FileDescriptorSet`→`dynamicpb` resolution are the highest-fidelity-risk pieces — schemas with imports may not resolve without the full descriptor set; (2) Avro logical types (decimal/timestamp/union) decode ambiguity into JSON; document supported subset; (3) registry auth/TLS reuse vs. broker creds may diverge (separate registry credentials needed); (4) decode-to-JSON changes the extraction surface — gate strictly on `value_format ≠ none` to avoid silently breaking raw-bytes scripts; (5) new third-party deps expand the supply-chain/CVE surface in the default build (codecs are compiled in even when unused).

### P2.3 — JMS messaging semantics (message-types, selectors, durable/shared subs, priority, TTL, delivery-mode, browse)

**Problem / current state** — The JMS message model is an opaque `[]byte` body with no type discrimination (`pkg/protocol/jms/jms.go:79-87`): `message` carries `Body []byte`, `Headers`, `Properties`, but no JMS message-type (Text/Bytes/Map/Stream/Object). `config.DeliveryMode` is parsed (`jms.go:139`) and defaulted to `"persistent"` (`jms.go:158-160`) but **never applied** — `buildMessage` (`jms.go:691-714`) ignores it, and neither `toAMQPMessage` (`realadapter_amqp.go:318-359`) nor `toServiceBusMessage` (`realadapter_servicebus.go:330-356`) set durability/priority/TTL. There are no message selectors (consume always pulls FIFO head — `jms.go:102-114`, `realadapter_amqp.go:57-73`), no durable/shared subscription paths (`session.NewReceiver(ctx, destination, nil)` — `realadapter_amqp.go:201`), no QueueBrowser/peek action, and the mock `memoryBroker` (`jms.go:91-120`) is a flat queue map with **no topic fan-out** (a topic publish lands in one slice, consumed once). `catalog_jms.go:7-58` declares only publish/consume/request_reply/ack/commit/rollback with no message-type/selector/priority/ttl fields.

**Goal** — Implement the core JMS 2.0 spec surface provider-neutrally: typed messages (Text/Bytes/Map/Stream/Object), applied delivery-mode + priority (0–9) + TTL on send, JMS message selectors (SQL-92 subset on properties/headers) on consume, durable/shared/non-durable subscription paths, `QueueBrowser`/peek (non-destructive), and topic fan-out in the mock broker so authored scripts replay faithfully and execute against AMQP/Service Bus brokers.

**Design** — Extend the provider-neutral `message` struct as the single carrier; adapters translate to/from broker-native fields. (1) **Message types**: add `Type` (enum) + `MapBody map[string]string` / `StreamBody []any` for Map/Stream; Text/Bytes/Object stay in `Body` with `Type` discriminating. AMQP: TextMessage → `amqp.Message.Value` string or `Data` with `content-type=text/plain`; MapMessage → AMQP map body; carry `x-opt-jms-msg-type` app-property for round-trip fidelity (mirrors how Qpid JMS marks types). Service Bus has no typed body, so encode type via `ApplicationProperties["jms_message_type"]`. (2) **Delivery-mode/priority/TTL**: AMQP `amqp.MessageHeader{Durable, Priority, TTL}`; Service Bus `Message.TimeToLive` (TTL) + app-property for priority/durability (SB lacks native priority). Fix the dead config at `jms.go:158-160` by threading it through `buildMessage`. (3) **Selectors**: parse a SQL-92 subset (`prop = 'x' AND priority > 4`) into a predicate evaluated against `Headers`+`Properties`; AMQP passes the selector to `NewReceiver` via filter `apache.org:selector-filter:string`; Service Bus uses subscription rules (selector applied client-side as fallback when rule creation is out of scope); mock evaluates the predicate in `consume`. (4) **Subscriptions**: `subscription_mode` (non_durable/durable/shared) + `subscription_name`; AMQP durable = `ReceiverOptions{Durability: DurabilityUnsettledState, Name: subName}`; SB durable subs are first-class via `NewReceiverForSubscription`. (5) **Browse**: new `jms_browse` action → `Browse(ctx, dest, selector, max)` adapter method; mock returns a copy without dequeue; SB uses `PeekMessages`; AMQP uses a receiver in a browse/`copy` mode. (6) **Mock topic fan-out**: `memoryBroker` gains per-topic subscriber registries so each durable/shared sub gets its own cursor. Rejected alternatives: a separate `MapMessage`/`StreamMessage` Go type hierarchy (breaks the flat results pipeline and sidecar JSON contract); server-side-only selectors (mock can't honor them, breaks local shakeout parity).

**DSL & catalog changes** — In `pkg/dsl/catalog_jms.go`: add fields to `jms_publish`/`jms_request_reply`: `params.message_type` (enum: `text|bytes|map|stream|object`, default `text`), `params.delivery_mode` (`persistent|non_persistent`), `params.priority` (int 0–9), `params.time_to_live_ms` (int), `params.map_body` (object), `params.stream_body` (array). Add to `jms_consume`/`jms_request_reply`: `params.selector` (string), `params.subscription_mode` (`non_durable|durable|shared`), `params.subscription_name` (string). New action `jms_browse` (Protocols `["jms"]`, fields `target`, `params.selector`, `params.max`). Add `JMSConfig.MessageType` and per-message defaults to `pkg/dsl/types.go:197-214`. Regenerate with `go generate ./pkg/dsl` (freshness enforced by `pkg/dsl/schemagen`); add enum validation in `pkg/dsl/validate.go` (priority bounds, selector parse, durable requires `client_id`/`subscription_name`).

**Types & interfaces** —
```go
// jms.go message struct additions
Type        string         `json:"type,omitempty"`         // text|bytes|map|stream|object
MapBody     map[string]string `json:"map_body,omitempty"`
StreamBody  []any          `json:"stream_body,omitempty"`
DeliveryMode string        `json:"delivery_mode,omitempty"`
Priority    *uint8         `json:"priority,omitempty"`
TTLMs       int64          `json:"ttl_ms,omitempty"`
// brokerAdapter interface addition
Browse(ctx context.Context, destination, selector string, max int) ([]message, error)
// consume signature gains a selector + subscription spec
Consume(ctx context.Context, destination string, opts consumeOptions, timeout time.Duration) (message, error)
type consumeOptions struct{ Selector, SubscriptionMode, SubscriptionName string }
```
Sidecar `sidecarRequest` gains `Selector`/`Subscription`/`Browse` fields. `pkg/protocol/protocol.go` action handling: add `jms_browse` case in `Execute` (`jms.go:229-244`).

**Files to touch** —
- EDIT `pkg/protocol/jms/jms.go:79-87` (message fields), `:158-160`+`:691-714` (apply delivery-mode/priority/TTL/type in buildMessage), `:91-120` (mock topic fan-out + selector + browse), `:71-77` (interface), `:229-244` (jms_browse dispatch), `:733-745` (extractions for type).
- EDIT `pkg/protocol/jms/realadapter_amqp.go:318-394` (typed body, header durable/priority/TTL, selector filter, durable receiver, Browse).
- EDIT `pkg/protocol/jms/realadapter_servicebus.go:330-395` (TTL/priority/type app-props, PeekMessages browse, durable sub).
- EDIT `pkg/dsl/catalog_jms.go`, `pkg/dsl/types.go:197-214`, `pkg/dsl/validate.go`, `pkg/engine/config_protocols.go:427` (plumb new config keys).
- NEW `pkg/protocol/jms/selector.go` (+`selector_test.go`) — SQL-92 subset parser/evaluator.
- EDIT `pkg/protocol/jms/realadapter_*_default.go` stubs to satisfy the widened interface.
- REGEN `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`, `docs/swagger.yaml`.

**Build tags / dependencies** — No new go.mod deps: `github.com/Azure/go-amqp` already exposes `MessageHeader`/filters; `azservicebus` already exposes `TimeToLive`/`PeekMessages`. Real paths stay behind `jms_solace_real` / `jms_servicebus_real`; default stubs (`*_default.go`) remain the fail-closed path. CI tag matrix unchanged in shape but both real-tag builds must compile against the widened `brokerAdapter` — add a compile-check job per tag.

**Test plan** — Unit (table-driven, `pkg/protocol/jms/jms_test.go`): message-type round-trip through `buildMessage`+mock; delivery-mode/priority/TTL applied; selector matches/rejects across operators (`=`,`<>`,`>`,`AND`,`OR`,`IN`,`LIKE`); durable/shared sub independent cursors; topic fan-out delivers to N subs; `jms_browse` non-destructive (count unchanged after browse). `pkg/protocol/jms/selector_test.go`: parser happy-path + malformed-selector errors. Catalog/schema: `pkg/dsl/schemagen` freshness test must pass post-regen; `validate.go` priority-bound + durable-requires-name cases. Integration (testcontainers, behind `jms_solace_real`/`jms_servicebus_real` or a new `jms_integration` tag): publish typed/priority/TTL to a real broker, selector-filtered consume, durable resubscribe survives reconnect, browse via PeekMessages — skipped when the tag/broker is absent. Dashboard: extend the messaging filter (TestExecution) to surface `jms_browse` + message-type column; verify in a browser per policy.

**Acceptance criteria** —
- [ ] `message` carries Type/MapBody/StreamBody/DeliveryMode/Priority/TTL; Text/Bytes/Map/Stream/Object all round-trip mock → consume.
- [ ] `delivery_mode`, `priority` (0–9, validated), `time_to_live_ms` are applied on send by mock, AMQP, and Service Bus adapters (dead config at `jms.go:158-160` eliminated).
- [ ] `jms_consume`/`jms_request_reply` honor `params.selector`; non-matching messages are not delivered (mock + both real adapters).
- [ ] Durable and shared subscriptions resubscribe to the same logical cursor; non-durable is the default.
- [ ] `jms_browse` returns messages without consuming them; queue depth unchanged.
- [ ] Mock broker fans a topic publish out to every registered subscription.
- [ ] `go generate ./pkg/dsl` + `./pkg/api` produce no diff; all freshness tests + `go test ./...` pass; both real-tag builds compile.

**Effort / dependencies / risks** — Effort **XL**. Depends on **P1.2** (typed-message model groundwork) and **P1.4** (subscription/config plumbing). Risks: AMQP selector-filter and durable-subscription semantics vary by broker (Solace vs generic AMQP) — keep mock as the parity contract and degrade gracefully when a broker rejects a filter; Service Bus has no native message priority (must document the app-property emulation as non-portable); selector parser scope-creep — fix the SQL-92 subset explicitly and reject unsupported syntax loudly rather than silently passing; sidecar JSON contract change requires versioning the `sidecarRequest`/`sidecarResponse` envelope to avoid breaking deployed sidecars.

### P2.4 — Azure Service Bus advanced features (sessions, locks, scheduled, defer, dedup, DLQ receive, prefetch, transactions, partitioning)

**Problem / current state** — The Service Bus adapter (`pkg/protocol/jms/realadapter_servicebus.go`, build tag `jms_servicebus_real`) only does fire-and-forget Publish + auto-settle Consume. `Consume` (lines 62–86) receives exactly 1 message and immediately `CompleteMessage`s it unless `receive_and_delete` mode (line 80–84); there is no way to Abandon, DeadLetter, or Defer. The `brokerAdapter` interface (`pkg/protocol/jms/jms.go:71-77`) exposes only Publish/Consume/RequestReply/HealthCheck/Teardown — no settlement token, no session handle, no batch. The `message` struct (`jms.go:79-87`) carries no `LockToken`/`SequenceNumber`/`SessionID`/`ScheduledEnqueueTime`. `jms_ack`/`jms_commit`/`jms_rollback` are pure no-ops (`jms.go:240-241` — just `Extractions["jms_operation"]`). `toServiceBusMessage` (lines 330–356) ignores SessionID, PartitionKey, ScheduledEnqueueTime, TimeToLive. Receivers are created queue/subscription-keyed only (lines 151–172) — no DLQ sub-queue, no `AcceptSessionForQueue`, no `PrefetchCount` (only `ReceiveMode` set, line 157). The catalog (`pkg/dsl/catalog_jms.go:7-59`) has 6 generic verbs and no settlement/session/scheduled actions.

**Goal** — Full parity with the azservicebus client-settlement and advanced-entity model: peek-lock settle (Complete/Abandon/DeadLetter/Defer), RenewMessageLock + auto-renew, sessions (SessionId send, AcceptNextSession/AcceptSessionForQueue, session state get/set), ScheduledEnqueueTime + ScheduleMessages/CancelScheduledMessages, defer→ReceiveDeferredMessages by sequence number, duplicate detection (MessageID + window), receive from `$deadletterqueue`, PrefetchCount + batch receive, cross-entity transactions, PartitionKey.

**Design** — These features require *stateful, client-controlled settlement*, which the current stateless verb model cannot express. Core decision: extend `message` and `brokerAdapter` with optional settlement state rather than fork a parallel Service Bus interface (keeps the protocol-neutral results pipeline intact; Solace/IBM-MQ adapters return `errUnsupportedSettlement` for the new verbs). A VUser-scoped `serviceBusSession` holds the active `*azservicebus.Receiver`/`*azservicebus.SessionReceiver` plus the last `*azservicebus.ReceivedMessage` keyed by a returned `settlement_token` (the SDK message pointer is stashed in a per-VUser map; only the token string crosses the DSL boundary). Settlement verbs look up the stashed message and call the SDK method. Transactions use `client.NewTransaction`-style context (`azservicebus` cross-entity TX via `context` carrying the TX state) wrapping Publish+settlement. Auto-lock-renew via `receiver` option / background goroutine bounded by VUser lifetime (reuse the panic-recovery + bounded-teardown patterns from the runtime-replay-hardening work). Rejected alternative: a separate `servicebus` protocol — would duplicate the t-digest/telemetry merge and break `jms` provider routing at `jms.go:191-192`. Rejected: encoding lock tokens in `Extractions` only — they must round-trip as first-class so the engine can sequence settle-after-consume.

**DSL & catalog changes** — New action types in `jmsActionSpecs()` (`catalog_jms.go`): `jms_settle` (field `params.disposition` enum `complete|abandon|dead_letter|defer`, `params.settlement_token`, `params.dead_letter_reason`, `params.dead_letter_description`, `params.modified_properties`), `jms_renew_lock` (`params.settlement_token`), `jms_receive_deferred` (`params.sequence_numbers` int list), `jms_session_accept` (`params.session_id` optional → AcceptNextSession when empty), `jms_session_state` (`params.session_state` set / get), `jms_cancel_scheduled` (`params.sequence_numbers`). Extend `jms_publish` fields: `params.session_id`, `params.partition_key`, `params.scheduled_enqueue_ms`, `params.time_to_live_ms`, `params.message_id` (dedup). Extend `jms_consume`: `params.prefetch_count`, `params.batch_size`, `params.sub_queue` enum `none|dead_letter|transfer_dead_letter`, `params.peek_lock` bool. New config props (Init, `jms.go:126-148` + `servicebus_config.go`): `servicebus_prefetch_count`, `servicebus_duplicate_detection`, `servicebus_session_enabled`, `servicebus_auto_renew_lock_ms`. After editing the catalog, run `go generate ./pkg/dsl` (regenerates `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`; freshness enforced by `pkg/dsl/schemagen`) and validation rules in `pkg/dsl/validate.go`.

**Types & interfaces** —
```go
// jms.go message struct additions
SessionID           string        `json:"session_id,omitempty"`
PartitionKey        string        `json:"partition_key,omitempty"`
SequenceNumber      *int64        `json:"sequence_number,omitempty"`
ScheduledEnqueueAt  time.Time     `json:"scheduled_enqueue_at,omitempty"`
TimeToLive          time.Duration `json:"time_to_live,omitempty"`
SettlementToken     string        `json:"settlement_token,omitempty"`
DeliveryCount       int           `json:"delivery_count,omitempty"`

// brokerAdapter interface additions (jms.go:71)
Settle(ctx context.Context, token string, d disposition, opts settleOptions) error
RenewLock(ctx context.Context, token string) error
ReceiveDeferred(ctx context.Context, dest string, seqs []int64) ([]message, error)
AcceptSession(ctx context.Context, dest, sessionID string) (string, error) // returns session handle/id
SessionState(ctx context.Context, sessionToken string, set []byte) ([]byte, error)
CancelScheduled(ctx context.Context, dest string, seqs []int64) error
```
Non-Service-Bus adapters get default-stub methods returning `errSettlementUnsupported`. New `disposition`/`settleOptions`/`subQueue` types in a new `servicebus_settle.go`.

**Files to touch** —
- EDIT `pkg/protocol/jms/jms.go:71-87` (interface + message fields), `:230-241` (dispatch new verbs), `:126-160` (Init new props).
- EDIT `pkg/protocol/jms/realadapter_servicebus.go:62-86` (peek-lock no-auto-complete path), `:151-172` (receiver opts: PrefetchCount, SubQueue, session receiver), `:330-356` (Session/Partition/Scheduled/TTL on send), implement new interface methods + token stash map.
- EDIT `pkg/protocol/jms/servicebus_config.go` (parse new props; add `servicebus_sub_queue` resolver).
- NEW `pkg/protocol/jms/servicebus_settle.go` (disposition/settleOptions/token store + auto-renew goroutine).
- EDIT `pkg/protocol/jms/realadapter_default.go` / sidecar / solace / ibmmq stub adapters (new no-op methods).
- EDIT `pkg/dsl/catalog_jms.go:7-59`, `pkg/dsl/validate.go`, `pkg/dsl/types.go` (config fields), `pkg/engine/config_protocols.go` (plumb new jms_* config keys).
- REGEN `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`, `docs/swagger.yaml` (via `go generate`).
- EDIT dashboard `dashboard/src/components/TestExecution*.tsx` + messaging filter (sessions/DLQ/deferred counters; the filter currently drops non-jms_/ws/grpc rows).

**Build tags / dependencies** — All real settlement code stays behind `//go:build jms_servicebus_real`; `realadapter_servicebus_default.go` (or extend existing default) provides the fail-closed stub for the no-tag build. `azservicebus` is already in `go.mod` (`realadapter_servicebus.go:14`) and supplies all needed APIs (`SessionReceiver`, `ScheduleMessages`, `ReceiveDeferredMessages`, sub-queue `ReceiverOptions.SubQueue`) — no new dependency. CI: the `jms_servicebus_real` build/vet/test job must compile the new code; gate integration tests behind a separate `jms_servicebus_integration` tag + Azure connection string env (no emulator exists for Service Bus).

**Test plan** — Unit (table-driven, no tag, against an in-memory fake adapter implementing the extended interface): catalog freshness (`schemagen`); validation accept/reject for each new action + enum (bad disposition, missing settlement_token, non-int sequence list); `toServiceBusMessage` maps SessionID/PartitionKey/ScheduledEnqueue/TTL; dispatch routes new verbs; token-stash lifecycle (settle unknown token → error; double-settle → error); auto-renew goroutine stops on Teardown (no leak — assert goroutine count). Integration (tag `jms_servicebus_integration`, real namespace): defer→receive-by-sequence round-trip; dead_letter then receive from `$deadletterqueue`; session accept + ordered receive + session state set/get; scheduled enqueue + cancel; duplicate detection drops same MessageID within window; cross-entity TX commit vs abandon. Dashboard: browser-verify session/DLQ/deferred metrics render for a `servicebus` run (type-check alone insufficient per policy).

**Acceptance criteria** —
- [ ] `jms_settle` performs Complete/Abandon/DeadLetter/Defer against the correct received message via settlement_token.
- [ ] Peek-lock consume no longer auto-completes when a settlement action is scripted; `receive_and_delete` unchanged.
- [ ] `jms_renew_lock` extends the lock; `servicebus_auto_renew_lock_ms` auto-renews and stops cleanly at Teardown (no goroutine leak).
- [ ] Sessions: send with SessionId, AcceptNextSession/specific session, session-state get/set work against a session-enabled entity.
- [ ] Scheduled enqueue + cancel by sequence number verified; PartitionKey + TTL set on outgoing messages.
- [ ] `jms_receive_deferred` returns the deferred message by sequence; `$deadletterqueue` receive returns DLQ messages with reason/description.
- [ ] PrefetchCount + batch_size honored; cross-entity transaction commits/aborts atomically.
- [ ] Duplicate detection drops a re-sent identical MessageID.
- [ ] `go generate ./pkg/dsl` + `go generate ./pkg/api` produce no diff; `go test ./...` and `go test -tags jms_servicebus_real ./pkg/protocol/jms/...` pass.
- [ ] Non-Service-Bus jms adapters return a clear unsupported error for new verbs.

**Effort / dependencies / risks** — Effort: **XL** (interface change ripples to every adapter; stateful settlement is a new execution concept; UI work). Depends on P1.2, P1.3, P2.3 (config/auth/results plumbing those establish for Service Bus). Risks: (1) settlement state is VUser-scoped — token stash must be per-VUser, not adapter-shared, or settlement crosses virtual users; the current adapter is shared via the client pool (`realadapter_servicebus.go:198-262`), so the token store must live on the per-VUser `serviceBusAdapter` instance, not the pooled `serviceBusPoolEntry`. (2) Lock expiry races — a renew goroutine must not outlive the VUser. (3) No Service Bus emulator means advanced features are only provable against a live Azure namespace; CI integration coverage requires a billed resource. (4) Broadening `brokerAdapter` touches Solace/IBM-MQ/sidecar/memory adapters — must add stubs or the package won't compile.

Plan file: `/Users/jasonczech/.claude/plans/whimsical-moseying-phoenix-agent-abb31d09565ddd9fc.md` was not created since this task is producing a spec document as the deliverable, returned above.

### P2.5 — IBM MQ real adapter (native MQI) + connectivity

**Problem / current state** — IBM MQ is wired into the JMS protocol only as a permanent fail-closed stub. `selectAdapter()` returns `unavailableIbmMqAdapter{cfg: j.cfg}` for `provider == "ibmmq"` (`pkg/protocol/jms/jms.go:188-190`), and every method of that type returns `"real IBM MQ adapter is not available in this build; enable mock mode for local replay"` (`pkg/protocol/jms/jms.go:629-658`). Unlike Solace (`realadapter_amqp.go` / `realadapter_default.go` + `pkg/dsl/capabilities_jms_solace_real.go`) and Service Bus (`realadapter_servicebus.go` + `capabilities_servicebus_*.go`), there is **no build-tag pair and no `IbmMqJMSRealAdapterAvailable()` capability helper**. `validate.go:707-712` gates only `jms-solace` and Service Bus; an `ibmmq` script with `mock:false` passes validation, then fails at runtime with an opaque adapter error. `go.mod` has no MQI dependency. The shared `message` struct (`jms.go:79-87`) carries `Headers`/`Properties`/`Body []byte` only — no MQMD field model.

**Goal** — A real, native-MQI IBM MQ transport (`MQCONNX`→`MQOPEN`→`MQPUT`/`MQPUT1`/`MQGET`→`MQCMIT`/`MQBACK`→`MQSUB`→`MQDISC`) opt-in behind `-tags ibmmq_real`, fail-closed by default, with full MQMD control (persistence, priority, expiry, MsgId/CorrelId as `MQBYTE24`), RFH2-vs-MQMD property split, SVRCONN/CCDT/CipherSpec/MQCSP connectivity, queue-object taxonomy with DLQ/backout, and validate-gate parity with Solace/Service Bus.

**Design** — Mirror the established real/default pattern. Add `newIbmMqAdapter(cfg) brokerAdapter` in two files keyed by `//go:build ibmmq_real` / `//go:build !ibmmq_real`; the default returns the existing `unavailableIbmMqAdapter`, so behavior is unchanged unless the tag is set. Replace the `case "ibmmq":` line at `jms.go:189-190` with `return newIbmMqAdapter(j.cfg), nil`. The real file uses `ibm-messaging/mq-golang/v5/ibmmq` (cgo against the MQ C client / redistributable). Map the 5-method `brokerAdapter` interface (`jms.go:71-77`) onto MQI: `Publish`→`MQPUT1`/`MQPUT`, `Consume`→`MQGET` with `MQGMO` (wait via `WaitInterval`, browse via `MQGMO_BROWSE_*`, syncpoint via `MQGMO_SYNCPOINT` + `MQCMIT`/`MQBACK`), `RequestReply`→`MQPUT` with `ReplyToQ` then correlated `MQGET`. Connection is `MQCONNX` with an `MQCNO`/`MQCD` (SVRCONN channel, conn name) or CCDT URL, `SSLCipherSpec`, and `MQCSP` for user/password. Results stay protocol-neutral: surface `MQMD.PutDate/PutTime`, backout count, and resolved queue manager into `result.Extractions` and reuse existing JMS dependency classification (`jms.go:216`). **Rejected:** a sidecar JMS bridge (already exists generically via `APEXLOAD_JMS_SIDECAR_COMMAND`, `jms.go:195-200`) — native MQI gives true MQMD fidelity and lower latency, which is the whole point of P2.5. **Rejected:** pure-Go MQ reimplementation (no viable library; MQI is the supported path).

**DSL & catalog changes** — No new action types; `jms_publish`/`jms_consume`/`jms_request_reply` are reused. Add a new protocol alias `jms-ibmmq` to the family-mapping in `catalog.go` (alongside `jms-solace`, comment at `catalog.go:75`). Add MQMD-control fields to the catalog field set for the JMS actions and config: `mq_persistence` (enum: `persistent`/`non_persistent`/`as_queue`), `mq_priority` (0–9), `mq_expiry_ms`, `mq_msg_id`, `mq_correl_id`, `mq_format` (e.g. `MQSTR`), `mq_reply_to_qmgr`, and config-level `mq_queue_manager`, `mq_channel` (SVRCONN), `mq_conn_name`, `mq_ccdt_url`, `mq_cipher_spec`, `mq_backout_queue`, `mq_backout_threshold`, `mq_property_style` (enum: `rfh2`/`mqmd`). After editing the catalog, run `go generate ./pkg/dsl` to regenerate `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`; freshness is enforced by `pkg/dsl/schemagen`.

**Types & interfaces** — Extend `config` (`jms.go:46-63`) with the `mq_*` connectivity fields above, parsed in `Init()` (`jms.go:126-148`). Extend `message` (`jms.go:79-87`) with `MQMD` metadata so the adapter contract carries field control without breaking other adapters:
```go
type mqMessageDescriptor struct {
    Persistence   string // persistent|non_persistent|as_queue
    Priority      int
    ExpiryMs      int
    MsgID         []byte // MQBYTE24
    CorrelID      []byte // MQBYTE24
    Format        string // e.g. ibmmq.MQFMT_STRING
    BackoutCount  int
    ReplyToQMgr   string
}
// message gains: MQ *mqMessageDescriptor `json:"mq,omitempty"`
```
New capability helper (package `dsl`): `func IbmMqJMSRealAdapterAvailable() bool` in `capabilities_ibmmq_real.go` (returns `true`, tag `ibmmq_real`) and `capabilities_ibmmq_default.go` (returns `false`). Real adapter file exposes `newIbmMqAdapter(cfg config) brokerAdapter` returning an `*ibmMqAdapter` implementing all 5 `brokerAdapter` methods.

**Files to touch** —
- EDIT `pkg/protocol/jms/jms.go:189-190` (return `newIbmMqAdapter`), `:46-63` (config `mq_*` fields), `:79-87` (`message.MQ`), `:126-148` (parse in `Init`).
- NEW `pkg/protocol/jms/realadapter_ibmmq.go` (`//go:build ibmmq_real`) — MQI adapter.
- NEW `pkg/protocol/jms/realadapter_ibmmq_default.go` (`//go:build !ibmmq_real`) — `newIbmMqAdapter` → `unavailableIbmMqAdapter`.
- NEW `pkg/dsl/capabilities_ibmmq_real.go` + `pkg/dsl/capabilities_ibmmq_default.go`.
- EDIT `pkg/dsl/validate.go:700-713` — add `isIbmMqJMSScript()` (protocol `jms-ibmmq` or `Config.JMS.Provider=="ibmmq"`, mirroring `isServiceBusJMSScript` at `:758-763`) and an `!IbmMqJMSRealAdapterAvailable()` issue parallel to `:707-712`. Validate `mq_priority`/`mq_persistence` enums.
- EDIT `pkg/dsl/catalog*.go` — `jms-ibmmq` alias + `mq_*` fields; regenerate `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`.
- EDIT `pkg/engine/config_protocols.go` — plumb new `mq_*` config keys.
- EDIT `go.mod` / `go.sum`.

**Build tags / dependencies** — `go.mod`: add `github.com/ibm-messaging/mq-golang/v5` (cgo; requires the MQ C client redistributable / `CGO_ENABLED=1` + `MQ_INSTALLATION_PATH`). Build tag `ibmmq_real` selects the real adapter and the `true` capability; default build is fully self-contained and fail-closed (no cgo). CI: add an `ibmmq_real` lane to the tag matrix alongside `kafka_real`/`jms_solace_real`/`jms_servicebus_real`, with the MQ client provisioned in the runner image; the cgo-free default lane must keep compiling/testing without the MQ SDK.

**Test plan** — Unit (default build, no tag): table-driven tests that an `ibmmq` script with `mock:false` yields the validate issue and that `newIbmMqAdapter` returns `unavailableIbmMqAdapter` whose methods error (mirror `servicebus_unavailable_test.go`). Table-driven catalog/validate tests for `mq_persistence`/`mq_priority` enum bounds and MQMD field parsing. Capability-flip test: `IbmMqJMSRealAdapterAvailable()==false` default. Integration (`//go:build ibmmq_real`, mirror `solace_integration_test.go`): testcontainers `icr.io/ibm-messaging/mq` (`DEV.QUEUE.1`/`DEV.QUEUE.MANAGER`, `DEV.APP.SVRCONN`) exercising MQPUT/MQGET round-trip, persistence/priority/expiry on `MQMD`, MsgId/CorrelId correlation in `RequestReply`, syncpoint commit vs backout, browse, and DLQ/backout-threshold routing. `go test ./...` (default) and `go test -tags ibmmq_real ./pkg/protocol/jms/...` must both pass. No dashboard change (Kafka/messaging-row UI is out of scope here).

**Acceptance criteria** —
- [ ] `-tags ibmmq_real` builds; default build compiles without the MQ SDK and stays fail-closed.
- [ ] An `ibmmq`/`jms-ibmmq` script with `mock:false` validates only when `IbmMqJMSRealAdapterAvailable()` (validate parity with Solace/Service Bus).
- [ ] `newIbmMqAdapter` returns a working MQI adapter under the tag and the unavailable stub without it.
- [ ] MQPUT/MQGET round-trip works against a real qmgr; persistence/priority/expiry/MsgId/CorrelId are honored on `MQMD`.
- [ ] Syncpoint commit/backout and DLQ/backout-threshold routing verified by integration tests.
- [ ] `go generate ./pkg/dsl` clean; schemagen freshness test passes; `go test ./...` and `go test -tags ibmmq_real ./...` green.

**Effort / dependencies / risks** — Effort **XL**. Depends on **P1.2, P1.4** (shared messaging/MQMD body-type + property-split foundations) and **P2.3** (broker-pressure/lag-style telemetry plumbing the adapter feeds). Risks: cgo + MQ C-client redistributable complicates CI/runtime images and cross-compilation (linux/amd64 generator pods only); `MQBYTE24` MsgId/CorrelId byte semantics and RFH2-vs-MQMD property placement are subtle and a common interop foot-gun; testcontainers MQ image licensing/startup time; persistence/syncpoint interactions with the existing no-op `jms_ack`/`jms_commit`/`jms_rollback` actions (`jms.go:240-241`) — those must be made real for MQ or explicitly documented as not yet wired.

### P2.6 — Solace native SMF transport

**Problem / current state** — The only non-mock Solace path is JMS-over-AMQP 1.0 via `pkg/protocol/jms/realadapter_amqp.go` (build tag `jms_solace_real`, `newSolaceAdapter` at line 20, `solaceAMQPAdapter` using `github.com/Azure/go-amqp`). It explicitly rejects native transports: `normalizedBrokerURL` (lines 271-277) errors on `smf://`/`smfs://`/`tcp(s)://`, forcing `amqp(s)://`. The shared `brokerAdapter` interface (jms.go:71-77) is reduced to Publish/Consume/RequestReply/HealthCheck/Teardown — no flow control, ack/replay/redelivery surface. `jms_ack`/`jms_commit`/`jms_rollback` are no-ops (jms.go:240-241); `config.DeliveryMode`/`Acknowledgement` are parsed (jms.go:138-139, defaulted 155-160) but never applied. The default stub `realadapter_default.go` returns `unavailableSolaceAdapter` (jms.go:598-627). None of guaranteed delivery, publisher-ack windows, consumer flow, wildcard subs, partitioned queues, exclusive access, DMQ/max-redelivery, or replay exists.

**Goal** — A native Solace SMF transport (Solace PubSub+ semantics) selectable alongside the AMQP adapter: direct vs guaranteed messaging with a publisher-ack window and bounded consumer-flow; `*`/`>` wildcard topic subscriptions and topic-to-queue mapping; partitioned queues via partition keys; exclusive vs non-exclusive queue access; DMQ + max-redelivery (0-255); message replay (all/msg-id/timestamp); Message-VPN + TLS + compression connectivity. Behind a build tag, fail-closed by default.

**Design** — Add a NEW `pkg/protocol/jms/solace_smf.go` (build tag `jms_solace_smf`) implementing `brokerAdapter` via the official `solace.dev/go/messaging` SDK (CGO-free messaging API). Keep the JMS package — Solace is a JMS provider already, the results/telemetry plumbing is JMS-shaped, and a separate `pkg/protocol/solace/` would fork all of `addMessageExtractions`/`finalizeJMSServerTelemetry`/catalog membership for no gain. Selection: extend `selectAdapter` (jms.go:181-204) — when `provider==solace` and `properties["transport"]=="smf"`, return `newSolaceSMFAdapter(cfg)` (new constructor, tag-gated like `newSolaceAdapter`); default remains AMQP. A NEW `solace_smf_default.go` (`!jms_solace_smf`) returns an `unavailableSolaceSMFAdapter` mirroring jms.go:598-627. URL scheme handling moves into the SMF path: it accepts `tcp(s)://`/`smf(s)://` (the AMQP `normalizedBrokerURL` stays AMQP-only). Direct-vs-guaranteed, ack window, flow window, partition key, exclusivity, DMQ/redelivery, and replay are driven by config/params (below) and applied at builder time — closing the parsed-but-ignored `delivery_mode`/`acknowledgement` gap for this path. Publisher acks satisfy `jms_publish`; persistent-mode consumer ack satisfies the previously-no-op `jms_ack`. Rejected alternatives: native SMF C wrapper (CGO in generator pods — no); a brand-new protocol name (breaks catalog/import/dashboard reuse, P2.3 messaging-surface work). Depends on **P1.2** (messaging telemetry/dashboard surface so SMF rows render) and **P2.3** (broker-pressure/redelivery telemetry fields).

**DSL & catalog changes** — Extend `pkg/dsl/catalog_jms.go` `jmsActionSpecs()`: add fields on `jms_publish` (`params.delivery_mode` enum `direct|persistent`, `params.partition_key`, `params.dmq_eligible` bool, `params.ttl_ms` int) and on `jms_consume` (`params.access_type` enum `exclusive|nonexclusive`, `params.max_redelivery` int 0-255, `params.flow_window` int, `params.subscription` already present; `params.replay` enum `none|all|msg-id|timestamp`, `params.replay_from`). Wire `jms_ack` to be honored under SMF persistent flow (catalog summary updated; behavior in adapter). New config keys plumbed through jms.go `Init` (128-148): `jms_transport`, `jms_publisher_ack_window`, `jms_compression` (1-9), `jms_replay`, `jms_replay_from`. Regenerate with `go generate ./pkg/dsl` (updates `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`); freshness is enforced by `pkg/dsl/schemagen` tests. Add validation in `validateJMSScriptIssues` (validate.go:686): max_redelivery range 0-255, replay-mode requires replay_from for msg-id/timestamp, and an SMF availability gate paralleling the line-707 Solace-adapter check (new `SolaceSMFRealAdapterAvailable()`).

**Types & interfaces** — Extend `config` (jms.go:46-63): `Transport string`, `PublisherAckWindow int`, `Compression int`, `Replay string`, `ReplayFrom string`. New per-message/per-action fields surface via existing `Properties`/`Params` (partition_key set into SMF partition-key header). Adapter:
```go
// solace_smf.go  //go:build jms_solace_smf
type solaceSMFAdapter struct {
    cfg       config
    messaging solace.MessagingService
    pubDirect solace.DirectMessagePublisher
    pubPersist solace.PersistentMessagePublisher
    receivers map[string]solace.PersistentMessageReceiver
}
func newSolaceSMFAdapter(cfg config) brokerAdapter
```
Populate existing telemetry: set `ServerTelemetry.RedeliveryCount` (NEW int field to add to `protocol.ServerTelemetry`, protocol.go:174-193, + proto field in `api/proto/generator.proto` and merge in `pkg/api/distributed_metrics.go`) from SMF delivery-count; reuse `ConsumerLagMs`/`MessageLatencyMs`. Expose `Extractions["jms_redelivered"]`, `["solace_partition_key"]`, `["solace_replay_offset"]`.

**Files to touch** —
- NEW `pkg/protocol/jms/solace_smf.go` (`//go:build jms_solace_smf`) — adapter
- NEW `pkg/protocol/jms/solace_smf_default.go` (`//go:build !jms_solace_smf`) — `unavailableSolaceSMFAdapter` + `newSolaceSMFAdapter` stub + `SolaceSMFRealAdapterAvailable()`
- NEW `pkg/protocol/jms/solace_smf_test.go` + `solace_smf_integration_test.go` (`//go:build jms_solace_smf && jms_solace_smf_integration`, mirroring `solace_integration_test.go:1`)
- EDIT `pkg/protocol/jms/jms.go:181-204` (`selectAdapter` SMF branch), `:46-63`/`:126-175` (config plumbing), `:240-241` (honor `jms_ack` under SMF)
- EDIT `pkg/dsl/catalog_jms.go:7-59` (action fields), regenerate artifacts
- EDIT `pkg/dsl/validate.go:686+` (range/replay/availability checks)
- EDIT `pkg/protocol/protocol.go:174-193` (`RedeliveryCount`), `api/proto/generator.proto`, `pkg/api/distributed_metrics.go` (merge)
- EDIT `go.mod` / `go.sum`

**Build tags / dependencies** — Add `solace.dev/go/messaging` to go.mod. New build tag `jms_solace_smf` (real) with `!jms_solace_smf` fail-closed default; integration tag `jms_solace_smf_integration`. CI matrix (`deployments/ci/`) gains a `jms_solace_smf` build/vet/test lane; integration lane runs against a `solace/solace-pubsub-standard` testcontainer. No change to the default generator/controller build (stub compiled in via existing blank imports at cmd/{generator,controller,apexctl}/main.go:21-24).

**Test plan** — Unit (table-driven, default build): config→builder option mapping (transport/delivery_mode/ack-window/compression/replay/exclusivity/max_redelivery), URL-scheme acceptance (`smf(s)`/`tcp(s)` accepted, `amqp` routed to AMQP), validate.go range/replay rules, default-stub fail-closed error text. Integration (tag `jms_solace_smf jms_solace_smf_integration`, `solace/solace-pubsub-standard` testcontainer): direct vs guaranteed publish+ack; consumer-flow window backpressure; `*`/`>` wildcard delivery; topic→queue mapping; partitioned-queue key affinity; exclusive vs non-exclusive consumer competition; DMQ landing after max-redelivery; replay all/msg-id/timestamp; TLS + Message-VPN connect. Run `go test ./...` (default) plus the tagged lane. Dashboard: verify SMF `jms_*` rows + `RedeliveryCount`/replay markers render in `TestExecution*.tsx`/`testExecutionMetricsModel.ts` (browser-checked per policy, gated on P1.2/P2.3).

**Acceptance criteria** —
- [ ] `jms_solace_smf` build connects to PubSub+ via SMF over TLS with Message-VPN + compression.
- [ ] Direct and guaranteed publish both work; guaranteed publishes block on a bounded publisher-ack window.
- [ ] `*` and `>` wildcard subscriptions and topic-to-queue mapping deliver correctly.
- [ ] Partitioned-queue messages with the same partition key land on one consumer; exclusive vs non-exclusive access behaves per Solace semantics.
- [ ] Messages exceeding `max_redelivery` (validated 0-255) route to the DMQ; `RedeliveryCount` surfaces in telemetry.
- [ ] Replay all/msg-id/timestamp each function; `jms_ack` is honored under SMF persistent flow.
- [ ] Default (`!jms_solace_smf`) build fail-closes with a clear "build with -tags jms_solace_smf" error; `go test ./...` green; regenerated DSL artifacts pass freshness tests.

**Effort / dependencies / risks** — Effort **XL**. Depends on P1.2 (messaging surface) and P2.3 (redelivery/broker-pressure telemetry). Risks: `solace.dev/go/messaging` bundles a native shared library — confirm it is pure-Go/CGO-free for the linux/amd64 generator image, else this needs the sidecar path instead (existing `sidecarAdapter`, jms.go:413) and the build-tag design changes; replay/DMQ require broker-side config the test harness must provision; partitioned queues need a recent PubSub+ broker version in the testcontainer; ack-window/flow-window tuning interacts with VUser-per-pod density targets.

> **Reconciliation note (Event Hubs).** Items EH.1–EH.6 are the umbrella-level entries; the full,
> decisions-locked day-1 implementation spec for Event Hubs (producer-first; Kafka-endpoint + native
> AMQP; Capture import; Prometheus; dashboard; Azure deployment) is in **Annex A** below and is
> authoritative for Event Hubs execution detail. **P0.0 is a prerequisite**, and the locked
> **producer-first** scope defers EH.3's full consumer-side checkpoint/Processor/epoch work past day-1.

### EH.1 — Event Hubs Kafka-endpoint enablement (preset + AsyncAPI host detection + docs)

**Problem / current state** — Azure Event Hubs has zero awareness in the codebase today (grep for `servicebus.windows.net`/`$ConnectionString`/`event_hub` across `pkg/`, `docs/`, `dashboard/src` returns nothing). The AsyncAPI converter's `deriveBrokers` (`pkg/convert/asyncapi/converter.go:338-375`) only distinguishes `kafka` vs `kafka-secure`: a `kafka-secure` host yields `securityProtocol = "sasl_ssl"` plus a generic warning, but it never sets a SASL mechanism, username, or password-secret. The final config block (`converter.go:248-253`) writes only `Brokers`, `SecurityProtocol`, `Acks:"all"`, `Mock` — so an Event Hubs `*.servicebus.windows.net:9093` host imports as a bare `sasl_ssl` Kafka config that fails validation (`pkg/dsl/validate.go:849-852` requires brokers + a real adapter, and the operator must hand-supply mechanism/username `$ConnectionString`/password). The support matrix (`pkg/convert/asyncapi/support.go:19,27`) describes only generic kafka-secure behavior. There is no DSL preset/snippet mechanism for the Event Hubs "magic values" anywhere.

**Goal** — Make targeting Event Hubs over its Kafka endpoint a guided, first-class experience: (1) a canonical DSL `KafkaConfig` preset/snippet carrying the Event Hubs magic values; (2) AsyncAPI host auto-detection for `*.servicebus.windows.net:9093` that pre-fills `security_protocol=sasl_ssl`, `sasl_mechanism=plain`, `sasl_username=$ConnectionString`, `sasl_password_secret`, plus a specific review hint; (3) operator docs. No new runtime code — runtime already works against the Kafka endpoint per P0.0 verification.

**Design** — Event Hubs' Kafka surface is plain SASL/SSL with username literal `$ConnectionString` and the connection string as password. All four values are already representable in `dsl.KafkaConfig` (`pkg/dsl/types.go:222-230`) and accepted by `validate.go:856-860` (`plain` is allowed). So this item is detection + defaulting + documentation, not new types.
- Add a detector `isEventHubsHost(host string) bool` in `converter.go` matching host (case-insensitive) `*.servicebus.windows.net` with port `:9093` (and the explicit `kafka` binding). When any derived broker matches, `deriveBrokers` returns an `eventHubs bool` so the assembly block can populate `SASLMechanism:"plain"`, `SASLUsername:"$ConnectionString"`, `SASLPasswordSecret:"eventhubs_connection_string"` and force `SecurityProtocol:"sasl_ssl"`, plus append a targeted warning to `ReviewSummary`/`Warnings`.
- Add an exported preset helper `dsl.EventHubsKafkaPreset(namespace, passwordSecret string) KafkaConfig` in a new `pkg/dsl/presets.go` — single source of truth reused by the converter and surfaced (later items) to the editor; keeps the magic values from being duplicated.
- Rejected alternative: a new `event_hubs` security_protocol enum value — unnecessary, the wire protocol is genuinely Kafka `sasl_ssl/plain`; adding an enum would diverge runtime + validation needlessly.
- Fits results architecture trivially: protocol stays `kafka`, no proto/metrics changes.

**DSL & catalog changes** — No new action types or enums. `sasl_username` literal `$ConnectionString` already validates (free-form string). No catalog field additions, so `go generate ./pkg/dsl` output is unchanged in theory — still run it and rely on the `pkg/dsl/schemagen` freshness test to confirm no drift. Update the `sasl_username`/`security_protocol` field doc strings in `catalog*.go` to mention Event Hubs as an example (this IS a catalog change → must `go generate ./pkg/dsl` and pass freshness).

**Types & interfaces** —
- NEW `pkg/dsl/presets.go`: `func EventHubsKafkaPreset(namespace, passwordSecret string) KafkaConfig` returning `{Brokers:[]string{namespace+".servicebus.windows.net:9093"}, SecurityProtocol:"sasl_ssl", SASLMechanism:"plain", SASLUsername:"$ConnectionString", SASLPasswordSecret:passwordSecret, Acks:"all"}`.
- EDIT `converter.go:338`: signature → `deriveBrokers(...) (brokers, protocols []string, securityProtocol string, eventHubs bool, warnings []string)`. NEW unexported `isEventHubsHost(host string) bool`.

**Files to touch** —
- EDIT `pkg/convert/asyncapi/converter.go:338-375` (detection + return), `:218` (capture `eventHubs`), `:248-253` + `:267` (populate SASL fields + summary).
- NEW `pkg/dsl/presets.go` + `pkg/dsl/presets_test.go`.
- EDIT `pkg/dsl/catalog*.go` (doc-string examples) → regenerate `docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`.
- EDIT `pkg/convert/asyncapi/support.go:19,27` (matrix note for Event Hubs auto-detect) + `GetSupportMatrixNotes` (`:33`).
- NEW `docs/EVENT_HUBS_HOWTO.md` (connection-string secret setup, `kafka_real` build requirement, mechanism table).

**Build tags / dependencies** — None. No `go.mod` additions; no new build tags; runtime path is the existing `kafka_real` (`realclient_franz.go`). CI tag matrix unaffected — detection/preset compile under the default (mock) build. Note in docs that *execution* needs the `kafka_real` binary (`validate.go:850-853` still blocks non-mock on default builds; not in scope to change here).

**Test plan** —
- Unit (table-driven) `pkg/dsl/presets_test.go`: assert preset field values for several namespaces/secrets.
- Unit (table-driven) `converter_test.go`: `isEventHubsHost` cases (`ns.servicebus.windows.net:9093` true; `:9092`, `kafka.example.com:9093`, empty, mixed-case host all correctly classified). End-to-end: feed an AsyncAPI doc with a `kafka-secure` server on `ns.servicebus.windows.net:9093` and assert resulting `KafkaConfig` has `sasl_mechanism=plain`, `sasl_username=$ConnectionString`, non-empty `sasl_password_secret`, `security_protocol=sasl_ssl`, and that `Warnings`/`Summary` contain the Event Hubs review hint. Negative: a plain `broker:9092` doc keeps existing behavior (no SASL prefill).
- Support-matrix freshness test for the new note. `pkg/dsl/schemagen` freshness must pass after `go generate`.
- Integration: none required (no new runtime); P0.0 already covers real-broker execution.
- Dashboard/browser: not touched in EH.1 (Kafka rows render is a separate item) — no UI verification needed beyond confirming the existing AsyncAPI import-review surface shows the new warning text.

**Acceptance criteria** —
- [ ] `dsl.EventHubsKafkaPreset` exists with the exact magic values and is unit-tested.
- [ ] AsyncAPI import of a `*.servicebus.windows.net:9093` server auto-fills `security_protocol=sasl_ssl`, `sasl_mechanism=plain`, `sasl_username=$ConnectionString`, and a `sasl_password_secret`.
- [ ] The import emits an Event-Hubs-specific review warning naming the connection-string secret and the `kafka_real` build requirement.
- [ ] Non-Event-Hubs Kafka imports are byte-for-byte unchanged (regression test green).
- [ ] `go generate ./pkg/dsl` produces no diff beyond the intended doc-string examples; `schemagen` + support-matrix freshness tests pass.
- [ ] `docs/EVENT_HUBS_HOWTO.md` documents secret setup, the `$ConnectionString` username, and the build/validation gate.
- [ ] `go test ./...` green.

**Effort / dependencies / risks** — Effort: **S**. Depends on **P0.0** (real Kafka adapter / runtime verification against the Event Hubs endpoint). Risks: (1) host-match false positives if Microsoft introduces a non-9093 Kafka port — keep the port check strict; (2) operators may expect *execution* to work after import, but `validate.go:850-853` still hard-blocks non-mock on default builds — the docs and review hint must make the `kafka_real` requirement explicit to avoid a confusing fail-closed; (3) low risk of `go generate` drift if catalog doc-strings touch shared text — confine edits to the two Kafka fields.

Spec written to /Users/jasonczech/.claude/plans/whimsical-moseying-phoenix-agent-ac10f5f48c7dd5ed9.md.

### EH.2 — OAUTHBEARER / Microsoft Entra ID SASL for the Kafka adapter

**Problem / current state** — The franz adapter's `saslMechanism()` (`pkg/protocol/kafka/realclient_franz.go:201-213`) handles only `scram-sha-256` and `scram-sha-512` explicitly; its `default:` branch (line 210-211) maps *everything else* — including `oauthbearer` and `plain` — to `plain.Auth{}`. So `oauthbearer` is a silent downgrade to PLAIN. Meanwhile `oauthbearer` is advertised as a legal value in three places: the DSL doc comment (`pkg/dsl/types.go:226`), the validator allow-list (`pkg/dsl/validate.go:859` accepts `"oauthbearer"`), and there is no token-source config anywhere. The franz import block (`realclient_franz.go:20-23`) pulls in `sasl`, `sasl/plain`, `sasl/scram` but not `sasl/oauth`. `azidentity v1.13.1` and `azcore v1.20.0` are already direct deps (`go.mod:64-65`). Config plumbing flows DSL `KafkaConfig` → `appendKafkaConfig` (`pkg/engine/config_protocols.go:452-470`) → flat `cfg` map → `config` struct (`pkg/protocol/kafka/kafka.go:41-64,74-104`); there is no field carrying a token source.

**Goal** — Make `sasl_mechanism: oauthbearer` actually authenticate via SASL/OAUTHBEARER, with a pluggable token provider whose default-and-flagship backing is `azidentity` (DefaultAzureCredential / managed identity / client-secret), enabling Event Hubs (`*.servicebus.windows.net:9093`, SASL_SSL) and any generic OAuth Kafka. Static-token mode also supported for non-Azure brokers.

**Design** — Add an OAuth branch to `saslMechanism()` returning `oauth.Oauth(func(ctx) (oauth.Auth, error){...})` from `github.com/twmb/franz-go/pkg/sasl/oauth`. The token-fetch closure delegates to a small `tokenSource` interface so the franz file stays free of Azure SDK coupling beyond the `kafka_real` tag. Two implementations: (1) `azureTokenSource` wrapping `azidentity.NewDefaultAzureCredential` (honors managed identity, env, client-secret via `azidentity.NewClientSecretCredential` when explicit tenant/client/secret are set) requesting scope `https://<namespace>.servicebus.windows.net/.default` (or operator-supplied scope), and (2) `staticTokenSource` returning a pre-supplied bearer token. Selection is driven by a new `oauth_token_source` config value (`azure` | `static`). Tokens are cached and refreshed on expiry by franz's own re-invocation of the closure; we additionally cache the `azcore.AccessToken` and only re-request within ~5 min of `ExpiresOn`. This is generator-only execution and lives entirely behind the existing `kafka_real` tag — the default stub build is untouched, preserving fail-closed behavior. **Rejected alternatives:** (a) baking azidentity into the always-compiled `kafka.go` — pollutes the lean default binary, violates the opt-in-adapter rule; (b) a separate `kafka_oauth` build tag — fragments the matrix unnecessarily since OAuth is meaningless without the real network adapter; (c) manual AAD token HTTP calls — reinvents azidentity's credential chain.

**DSL & catalog changes** — No new *action* types. Extend `KafkaConfig` (`pkg/dsl/types.go`) with config-level fields: `OAuthTokenSource string` (`azure`|`static`), `OAuthScope string`, `OAuthToken string` + `OAuthTokenSecret string` (static), and `AzureTenantID`/`AzureClientID`/`AzureClientSecret`/`AzureClientSecretSecret string` (explicit client-secret credential). Update the `sasl_mechanism` doc comment at `types.go:226` (already lists oauthbearer; no change to the enum string). The Kafka config is not part of the action catalog tables, so `go generate ./pkg/dsl` only needs to refresh `docs/dsl.schema.json` if the schemagen reflects config structs — run `go generate ./pkg/dsl` regardless and confirm the freshness test (`pkg/dsl/schemagen`) passes.

**Types & interfaces** — In `realclient_franz.go` (tagged):
```go
type tokenSource interface { Token(ctx context.Context) (oauth.Auth, error) }
type azureTokenSource struct { cred azcore.TokenCredential; scope string }
type staticTokenSource struct { token string }
func newTokenSource(cfg config) (tokenSource, error)
func (c *franzClient) saslMechanism() sasl.Mechanism // + case "oauthbearer"
```
Add to `config` struct (`kafka.go:41-64`): `OAuthTokenSource, OAuthScope, OAuthToken, AzureTenantID, AzureClientID, AzureClientSecret string`, parsed in `Init` (`kafka.go:74-104`) via `stringValue(cfg, "kafka_oauth_*"/"kafka_azure_*")`, with `OAuthToken`/`AzureClientSecret` falling back to their `_secret` siblings (mirroring the existing SASLPassword fallback at line 99-101).

**Files to touch** —
- EDIT `pkg/protocol/kafka/realclient_franz.go:20-23` (import `sasl/oauth`, azidentity/azcore) and `:201-213` (oauthbearer case + token-source helpers). NEW build-tag-tagged token-source code can live in a NEW `pkg/protocol/kafka/oauth_azure_franz.go` (`//go:build kafka_real`) to isolate Azure imports.
- EDIT `pkg/protocol/kafka/kafka.go:41-64,74-104` (config fields + parse + secret fallback).
- EDIT `pkg/dsl/types.go:222-242` (KafkaConfig fields; comment at :226).
- EDIT `pkg/dsl/validate.go:856-861` (validate `oauth_token_source` enum; require token-source when mechanism is oauthbearer; keep line 854 removal coordinated with P0.0).
- EDIT `pkg/engine/config_protocols.go:452-470` (`appendKafkaConfig`: emit `kafka_oauth_*`/`kafka_azure_*` keys).
- EDIT `docs/dsl.schema.json` via `go generate ./pkg/dsl`.

**Build tags / dependencies** — No new go.mod deps (`azidentity v1.13.1`, `azcore v1.20.0` already present; franz-go ships `sasl/oauth` in v1.21.3). Promote `azidentity`/`azcore` from indirect if `go mod tidy` flags it. New Azure-coupled code stays under `//go:build kafka_real`; CI matrix unchanged (real path already only built under the `kafka_real` / `kafka_integration` tags).

**Test plan** — Unit (table-driven, **untagged** where possible): test `config` parsing of all new keys + secret fallback in `kafka_test.go`. Tagged (`//go:build kafka_real`) unit in `realclient_franz_test.go`: table over `sasl_mechanism × token_source` asserting `saslMechanism()` returns the correct concrete mechanism type (oauth vs scram vs plain vs nil), and that `oauthbearer` no longer falls through to PLAIN (regression guard for the line-210 bug). `staticTokenSource.Token` returns the configured bearer. Mock `azcore.TokenCredential` to verify scope construction and token caching/refresh-window logic without network. Integration (behind `kafka_integration`, opt-in, not default CI): authenticate to a SASL_SSL broker — Event Hubs emulator or a real namespace via env-gated test — produce/consume one message. Validation tests in `validate_test.go` for the new enum + oauthbearer-requires-token-source rule. No dashboard surface changes (Kafka rows already filtered out — separate item), so no browser verification.

**Acceptance criteria** —
- [ ] `saslMechanism()` returns a SASL/OAUTHBEARER mechanism (not PLAIN) when `sasl_mechanism: oauthbearer`.
- [ ] `oauth_token_source: azure` obtains a token via azidentity DefaultAzureCredential and (with explicit tenant/client/secret) via client-secret credential, scoped to `<namespace>.servicebus.windows.net/.default` by default.
- [ ] `oauth_token_source: static` uses the supplied/secret-resolved bearer token.
- [ ] Tokens are cached and refreshed before expiry; no per-request credential call.
- [ ] All new config fields plumb DSL → `appendKafkaConfig` → `config` with `_secret` fallback for token and client secret.
- [ ] `validate.go` rejects an unknown `oauth_token_source` and flags `oauthbearer` with no token source.
- [ ] `go generate ./pkg/dsl` produces no diff; schemagen freshness test passes.
- [ ] Default (untagged) build still compiles with zero Azure/oauth imports; stub fail-closed behavior unchanged.
- [ ] `go test ./...` green; `go test -tags kafka_real ./pkg/protocol/kafka/...` green.

**Effort / dependencies / risks** — Effort **M**. Depends on **P0.0** (the unconditional non-mock rejection at `validate.go:854` and the `testcreation.go:499-501` hard error must be lifted before any real Kafka script, OAuth or not, can run). Risks: (1) franz's `oauth.Auth` re-invocation/refresh semantics must align with our cache window — verify against franz v1.21.3 docs to avoid stale-token auth failures; (2) Event Hubs requires SASL_SSL — ensure `usesTLS()` (`realclient_franz.go:215-217`) returns true (it keys off `security_protocol` containing `ssl`, which `sasl_ssl` satisfies); (3) azidentity DefaultAzureCredential's credential-chain latency/timeouts under high VUser fan-out — token caching mitigates but confirm the closure is not invoked per-message; (4) leaking the static token / client secret into logs or `Extractions` — must route through the secret fallback and never be echoed.

### EH.3 — Native Event Hubs AMQP adapter (pkg/protocol/eventhubs, azeventhubs SDK)

**Problem / current state** — APEX Load has zero Event Hubs awareness. The only Azure-messaging native path is Service Bus (`pkg/protocol/jms/realadapter_servicebus.go`, build-tag `jms_servicebus_real`), which is queue/topic-semantics, not partitioned-stream semantics. Event Hubs is reachable today only by pointing the Kafka protocol at the `NAMESPACE.servicebus.windows.net:9093` SASL_SSL/PLAIN surface — but that path is doubly broken: `validate.go:854` unconditionally appends "real Kafka network adapter is not available" for any non-mock script (rejecting even a `kafka_real` build), Kafka SASL silently downgrades OAUTHBEARER to PLAIN, and offset lag is computed then discarded (`realclient_franz.go:127-136`). There is no partition/checkpoint/consumer-group ownership model anywhere. The Service Bus adapter is the correct structural template: ref-counted client pool (`serviceBusClientPool`, `realadapter_servicebus.go:198-262`), dual auth via `newServiceBusClient` (connection-string vs `azidentity.NewDefaultAzureCredential`, lines 299-308), fail-closed default stub (`realadapter_servicebus_default.go`), and a `serviceBusConfigErrorAdapter` for deferred config errors.

**Goal** — A first-class `pkg/protocol/eventhubs` protocol (its own package, NOT a JMS provider) using the `azeventhubs` Go SDK, giving authors native producer batching, partitioned consume with start positions, and checkpoint-based exclusive consumption — matching the public capability spec (EventDataBatch with PartitionKey vs PartitionID, ~1 MB batch / 20 MB throughput-unit caps, `ErrEventDataTooLarge`; `StartPosition` earliest/latest/offset/sequence/enqueued-time + prefetch; `Processor` + Blob `CheckpointStore` ownership claim/steal + `UpdateCheckpoint`; epoch/owner-level exclusivity).

**Design** — Mirror the Service Bus adapter exactly so the architecture is consistent. Build-tag split: `realadapter_eventhubs.go` (`//go:build eventhubs_real`) holds the real `azeventhubs.ProducerClient`/`ConsumerClient`/`Processor`; `realadapter_default.go` (`//go:build !eventhubs_real`) returns a loud `unavailableEventHubsAdapter` (fail-closed, mirrors `realadapter_servicebus_default.go`). `eventhubs.go` is the protocol entry point (always compiled): implements `protocol.Protocol` (Name/Init/Execute/Teardown/HealthCheck per `pkg/protocol/protocol.go:34-53`), self-registers via `init()→protocol.Register("eventhubs", …)` (mirroring `kafka.go:24-28`), parses the `cfg map[string]any` into a typed `config`, and dispatches `eh_*` actions to a `brokerAdapter`-style interface. A ref-counted `producerClientPool`/`consumerClientPool` keyed by `sha256(authMode + namespace + entity)` shards N clients (reuse the `serviceBusShardCount`/`serviceBusPoolKey` pattern). Dual auth in `newEventHubsClient`: connection-string → `azeventhubs.NewProducerClientFromConnectionString`, else `azidentity.NewDefaultAzureCredential` → `NewProducerClient(fqns, eventHub, cred, …)`. Producer batches via `EventDataBatch.AddEventData` until `ErrEventDataTooLarge`, then `SendEventDataBatch`; PartitionKey and PartitionID are mutually exclusive (validated). Consume uses `PartitionClient` with `StartPosition` mapped from a string enum. Checkpointed mode uses `Processor` + `checkpoints.BlobStore` (azblob container). Results flow through the existing protocol-neutral pipeline: populate `protocol.ServerTelemetry.MessagesSent/MessagesReceived/ConsumerLagMs/MessageLatencyMs` (`protocol.go:186-191`) — these already aggregate into `ProtocolSummary.MessagesSent/AvgConsumerLagMs/MaxConsumerLagMs` (`metrics.go:118-123,821-849`) and the distributed merge, so no telemetry-schema change is required. Critically, compute real **offset lag** (last enqueued sequence − checkpoint sequence) into `ConsumerLagMs`-adjacent fields rather than wall-clock, fixing the Kafka design flaw at the source. **Rejected:** (a) modeling Event Hubs as a Kafka `kafka_real` endpoint — loses partition ownership/checkpoint/epoch semantics and inherits the OAUTHBEARER downgrade; (b) a JMS-over-AMQP provider — Event Hubs has no JMS broker semantics and the message model is partitioned streams. Dashboard already renders Kafka/JMS messaging rows; the messaging surface filter must add `eventhubs`/`eh_*` (one-line change in `testExecutionMetricsModel.ts`).

**DSL & catalog changes** — New `pkg/dsl/catalog_eventhubs.go` declaring `ActionSpec`s with `Protocols: []string{"eventhubs"}`, mirroring `catalog_kafka.go`:
- `eh_send` — fields `target` (event hub), `body`, `body_file`, `headers`, `params.partition_key`, `params.partition_id`, `params.batch` (bool, accumulate-then-flush).
- `eh_send_batch` — `params.max_batch_bytes`, plus body-array source; surfaces `ErrEventDataTooLarge` as a structured failure.
- `eh_receive` — `params.partition_id`, `params.start_position` (enum `earliest|latest|offset|sequence|enqueued_time`), `params.start_offset`, `params.start_sequence`, `params.start_enqueued_time`, `params.prefetch`, `params.timeout_ms`.
- `eh_checkpoint` — `params.consumer_group`, persists `UpdateCheckpoint` via the Blob store (NOT a no-op — explicitly contrast with the no-op `kafka_commit`).
New `EventHubsConfig` enum values: `start_position`, `consumer_group`, `auth_mode (connection_string|azidentity)`, `checkpoint_store_url`, `prefetch`, `owner_level`. After editing the catalog, run `go generate ./pkg/dsl` to regenerate `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`; freshness is enforced by `pkg/dsl/schemagen`.

**Types & interfaces** — In `pkg/dsl/types.go`, add `EventHubs EventHubsConfig` to `ScriptConfig` (alongside `Kafka KafkaConfig`, line 67) and:
```go
type EventHubsConfig struct {
    Namespace          string `json:"namespace,omitempty"`            // NAMESPACE.servicebus.windows.net
    ConnectionString   string `json:"connection_string,omitempty"`
    ConnectionStringSecret string `json:"connection_string_secret,omitempty"`
    EventHub           string `json:"event_hub,omitempty"`
    ConsumerGroup      string `json:"consumer_group,omitempty"`       // default "$Default"
    AuthMode           string `json:"auth_mode,omitempty"`            // connection_string|azidentity
    StartPosition      string `json:"start_position,omitempty"`       // earliest|latest|offset|sequence|enqueued_time
    Prefetch           int    `json:"prefetch,omitempty"`
    OwnerLevel         int    `json:"owner_level,omitempty"`          // epoch / exclusive consumer
    CheckpointStoreURL string `json:"checkpoint_store_url,omitempty"` // azblob container URL
    MaxClients         int    `json:"max_clients,omitempty"`
    TimeoutMs          int    `json:"timeout_ms,omitempty"`
    Properties         map[string]string `json:"properties,omitempty"`
}
```
In `pkg/engine/config_protocols.go` add `appendEventHubsConfig(cfg, script)` mirroring `appendKafkaConfig` (line 452), flattening to `eh_*` keys (`eh_namespace`, `eh_connection_string`, `eh_event_hub`, `eh_start_position`, …). In `pkg/protocol/eventhubs/eventhubs.go`:
```go
type eventHubsAdapter interface {
    Send(ctx context.Context, eventHub string, msg message) (message, error)
    SendBatch(ctx context.Context, eventHub string, msgs []message, maxBytes int) (int, error)
    Receive(ctx context.Context, eventHub string, opts receiveOptions) (message, error)
    Checkpoint(ctx context.Context, group, partition string, offset, sequence int64) error
    HealthCheck(ctx context.Context) error
    Teardown(ctx context.Context) error
}
```
No proto change needed — `ServerTelemetry`/`ProtocolSummary` already carry the messaging fields.

**Files to touch**
- NEW `pkg/protocol/eventhubs/eventhubs.go` — protocol + `init()` registration + config parse + action dispatch.
- NEW `pkg/protocol/eventhubs/realadapter_eventhubs.go` (`//go:build eventhubs_real`) — azeventhubs ProducerClient/ConsumerClient/Processor + client pools.
- NEW `pkg/protocol/eventhubs/realadapter_default.go` (`//go:build !eventhubs_real`) — `unavailableEventHubsAdapter` fail-closed stub.
- NEW `pkg/protocol/eventhubs/eventhubs_config.go` — conn resolve (connection-string vs namespace), shard count, pool key, start-position mapping (template: `servicebus_config.go`).
- NEW `pkg/dsl/catalog_eventhubs.go` — `eventHubsActionSpecs()`.
- EDIT `pkg/dsl/catalog.go` — wire `eventHubsActionSpecs()` into the aggregate spec list (find where `kafkaActionSpecs()` is appended).
- EDIT `pkg/dsl/types.go:67` — add `EventHubs EventHubsConfig` + the struct.
- EDIT `pkg/dsl/validate.go` (~line 850, after the Kafka block) — capability gate; enum validation for `start_position`/`auth_mode`; require `event_hub` + (connection_string XOR namespace+azidentity); reject `partition_key`+`partition_id` together; `eh_checkpoint` requires `checkpoint_store_url`.
- EDIT `pkg/engine/config_protocols.go:452` — add `appendEventHubsConfig`.
- EDIT `cmd/generator/main.go:22`, `cmd/controller/main.go:24`, `cmd/apexctl/main.go:20` — add `_ ".../pkg/protocol/eventhubs"` (alpha-ordered, before `http`).
- EDIT `dashboard/src/components/testExecutionMetricsModel.ts` — add `eventhubs`/`eh_*` to the messaging filter.
- EDIT `go.mod` — promote azblob from indirect; add azeventhubs.
- Regenerate `docs/swagger.yaml` (`go generate ./pkg/api`) if a new route is added (none planned) and `docs/dsl.schema.json` (`go generate ./pkg/dsl`).

**Build tags / dependencies** — `go.mod` adds `github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs` and `.../sdk/messaging/azeventhubs/checkpoints`; promote `.../sdk/storage/azblob v1.6.3` from `// indirect` to direct (already present at go.mod:67); reuse existing `azidentity v1.13.1` (line 65) and `azcore v1.20.0` (line 64). New build tag `eventhubs_real` (parallels `jms_servicebus_real`/`kafka_real`). CI matrix: add an `eventhubs_real` build job to the tag matrix (compile-only by default; integration tests gated behind a `EVENTHUBS_CONNECTION_STRING` env + tag). Default build remains fail-closed — no new transitive AMQP deps reach the default generator/controller binaries unless `eventhubs_real` is set.

**Test plan** — Per project policy, tests are mandatory.
- Unit, table-driven (default build, no tag): `eventhubs_config_test.go` — conn resolution (connection-string vs namespace vs sb://; missing event_hub; both creds vs azidentity), start-position enum mapping, shard count clamp, pool key determinism. `eventhubs_test.go` — action parse/dispatch, `partition_key`+`partition_id` rejection, batch accumulation boundary (synthetic `ErrEventDataTooLarge`). `pkg/dsl/validate_test.go` — capability-gate cases (good/bad enums, checkpoint without store). `pkg/dsl/catalog_eventhubs_test.go` + the schemagen freshness test. `pkg/engine/config_protocols_test.go` — `appendEventHubsConfig` key flattening.
- Unavailable-path test (default build): asserts every `eh_*` action returns the loud "eventhubs real adapter not available; build with -tags eventhubs_real" error (mirror `servicebus_unavailable_test.go`).
- Integration (`//go:build eventhubs_real`, behind `EVENTHUBS_CONNECTION_STRING`): produce→consume round-trip across partitions; StartPosition earliest/latest/sequence; Processor ownership claim + `UpdateCheckpoint` against a real azurite/Blob `checkpoint_store_url`; epoch/owner-level exclusivity (second consumer at higher owner level evicts the first). Mirror `servicebus_integration_test.go`. No emulator exists for Event Hubs, so integration is real-broker-only and skipped without the env var; azurite (testcontainer) backs the checkpoint-store half.
- Dashboard/browser: after the `testExecutionMetricsModel.ts` filter change, run a mock/local `eventhubs` test and confirm the messaging table renders `eh_*` rows with messages-sent/received and consumer-lag columns (type-check alone is insufficient per policy).

**Acceptance criteria**
- [ ] `pkg/protocol/eventhubs` exists with `eventhubs.go`, `realadapter_eventhubs.go` (`eventhubs_real`), `realadapter_default.go` (`!eventhubs_real`); `go build ./...` (default) and `go build -tags eventhubs_real ./...` both pass.
- [ ] `protocol.Register("eventhubs", …)` fires via `init()`; protocol is listed by the controller and generator (blank imports in all three `cmd/*/main.go`, alpha-ordered).
- [ ] Default build is fail-closed: any non-mock `eh_*` script returns a loud, actionable unavailable error; no AMQP/azeventhubs symbols linked into the default binary.
- [ ] `eh_send`/`eh_send_batch`/`eh_receive`/`eh_checkpoint` exist in the catalog; `go generate ./pkg/dsl` is clean and the schemagen freshness test passes.
- [ ] `eh_checkpoint` performs a real `UpdateCheckpoint` (verified in integration); it is NOT a no-op.
- [ ] `validate.go` rejects: missing `event_hub`, both `partition_key`+`partition_id`, invalid `start_position`/`auth_mode`, and `eh_checkpoint` without `checkpoint_store_url`.
- [ ] Producer surfaces `ErrEventDataTooLarge` as a structured per-action failure rather than silently truncating.
- [ ] `ServerTelemetry.MessagesSent/MessagesReceived/ConsumerLagMs` populate; computed lag is offset/sequence-based, not wall-clock; values aggregate into `ProtocolSummary` and the distributed merge.
- [ ] Dashboard messaging table renders `eventhubs`/`eh_*` rows (browser-verified).
- [ ] CI tag matrix builds `eventhubs_real`; integration tests run when `EVENTHUBS_CONNECTION_STRING` is set, skip cleanly otherwise.

**Effort / dependencies / risks** — Effort **XL** (new package + dual-auth pooling + producer batching + partitioned consume + Blob checkpoint store + ownership/epoch semantics + catalog/validate/plumbing/imports + dashboard + two test tiers). Depends on **P1.1** (messaging telemetry/results-pipeline groundwork) and **P1.5** (messaging capability-gate / config-plumbing pattern these `eh_*` keys ride on). Risks: (1) no Event Hubs emulator — checkpoint/ownership/epoch behavior is only fully exercisable against a real namespace, so default CI is compile-only and ownership-steal logic is the most under-tested area; (2) the azeventhubs `Processor` is long-running/callback-driven, which is an awkward fit for the one-shot `Execute(action)` VUser model — the adapter must run the Processor in a background goroutine and bridge events to per-action receives without leaking goroutines (apply the bounded-teardown discipline from the runtime-hardening pass); (3) Blob `CheckpointStore` adds an azblob dependency and a second set of credentials (the checkpoint container may use different auth than the hub) — config and SSRF/confinement review needed on `checkpoint_store_url`; (4) `owner_level` epoch eviction can starve other VUsers if mis-authored — validation must warn when `owner_level` is combined with many VUsers on one partition.

### EH.4 — Event Hubs results extension (per-partition skew, ServerBusy/throttle, checkpoint, TU/PU, batch fill)

**Problem / current state** — The unified result model is HTTP/JMS-shaped. `protocol.ServerTelemetry` (pkg/protocol/protocol.go:169-193) carries only generic messaging fields (`MessageLatencyMs`, `MessagesSent/Received`, `ConsumerLagMs`, `MessagesSentPerSec`). The engine aggregator (`ServerTelemetrySummary`, pkg/engine/metrics.go:105-128; merge/finalize at metrics.go:801-863) and the distributed merge (pkg/api/distributed_metrics.go:419-500, proto fields generator.proto:124-158) mirror exactly that set. Telemetry analysis (pkg/api/telemetry.go:251-318) only knows `jms_broker_pressure` driven by `AvgConsumerLagMs`. The dashboard (dashboard/src/components/TestExecutionSummary.tsx:41-72,649-672) filters messaging to `jms_*`/stream protocols. There is **no** `pkg/protocol/eventhubs/` package yet (EH.3 creates it), no notion of throttling/ServerBusy, per-partition skew, checkpointing, TU/PU, batch fill, or `ActiveConnections`. `GoalConfig` (pkg/api/runner.go:972-990) has no messaging SLA inputs at all.

**Goal** — Carry Event-Hubs-relevant signals end-to-end (generator → proto merge → engine summary → API → dashboard → SLA verdict) that mirror Azure Monitor namespace metrics: `ThrottledRequests`/`ServerBusy` + `QuotaExceeded` rate; per-partition throughput skew (stddev + hot-partition flag vs the 1 MB/s, 1000 ev/s per-partition ingress ceiling); end-to-end latency derived from `EnqueuedTime`; checkpoint write rate/latency/failures; producer batch fill ratio (`NumBytes`/`MaxBytes`) and `ErrEventDataTooLarge` reject rate; `ActiveConnections`. These become first-class messaging SLA inputs and a dashboard panel.

**Design** — Reuse the existing protocol-neutral telemetry pipeline rather than a sidecar model: the EH protocol (EH.3) populates new fields on `protocol.ServerTelemetry` per `Result`; the engine sum/finalize and distributed proto-merge fold them into `ServerTelemetrySummary` exactly like `ConsumerLagMs`. Per-partition skew cannot live on a flat per-result struct, so add a compact `PartitionStat` slice (partition id → bytes/events/lag) on `ServerTelemetry`; the engine accumulates a partition-keyed map and computes stddev/hot-flag in finalize against constants `eventHubsPartitionIngressBytesPerSec = 1<<20` and `eventHubsPartitionIngressEventsPerSec = 1000`. Throttle/ServerBusy/QuotaExceeded/EventDataTooLarge are monotone counters merged by addition (not averaged). Checkpoint metrics: write count + summed latency + failure count, averaged in finalize. ActiveConnections is a gauge (take max across samples). A new analyzer `eventhubs_throttle_pressure` in telemetry.go follows the `jms_broker_pressure` template (signature parts, summaries). Rejected alternatives: (a) a separate Event-Hubs-only metrics message in proto — breaks the unified merge and dashboard model; (b) overloading `ConsumerLagMs` for partition lag — loses per-partition resolution.

**DSL & catalog changes** — No new action types are required (EH.3 owns the `eventhubs_*` actions). EH.4 adds catalog-level documentation strings on EH.3's checkpoint/batch fields so docs reflect the emitted telemetry, and (optional) a `checkpoint_store` config doc. Any catalog edit requires `go generate ./pkg/dsl` to refresh `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md` (freshness enforced by pkg/dsl/schemagen). If no catalog field changes, no regeneration is needed — verify with the freshness test.

**Types & interfaces** — Add to `protocol.ServerTelemetry` (pkg/protocol/protocol.go):
```go
ThrottledRequests   int     `json:"throttled_requests,omitempty"`
ServerBusyCount     int     `json:"server_busy_count,omitempty"`
QuotaExceededCount  int     `json:"quota_exceeded_count,omitempty"`
EndToEndLatencyMs   float64 `json:"end_to_end_latency_ms,omitempty"` // now - EnqueuedTime
CheckpointWrites    int     `json:"checkpoint_writes,omitempty"`
CheckpointLatencyMs float64 `json:"checkpoint_latency_ms,omitempty"`
CheckpointFailures  int     `json:"checkpoint_failures,omitempty"`
BatchFillRatio      float64 `json:"batch_fill_ratio,omitempty"`      // NumBytes/MaxBytes
EventTooLargeCount  int     `json:"event_too_large_count,omitempty"`
ActiveConnections   int     `json:"active_connections,omitempty"`
Partitions          []PartitionStat `json:"partitions,omitempty"`
```
`type PartitionStat struct { PartitionID string; Bytes int64; Events int64; LagMs float64 }`. Mirror summed/averaged equivalents on `engine.ServerTelemetrySummary` (pkg/engine/metrics.go:105) and `api.ServerTelemetrySummary` (runner.go:~407): add `ThrottledRequests`, `ServerBusyCount`, `QuotaExceededCount`, `AvgEndToEndLatencyMs`, `MaxEndToEndLatencyMs`, `CheckpointWrites`, `AvgCheckpointLatencyMs`, `CheckpointFailures`, `AvgBatchFillRatio`, `EventTooLargeCount`, `MaxActiveConnections`, `PartitionSkewStdDevBytes float64`, `HotPartitionCount int`. Add matching proto fields (tags 17+) in generator.proto `ServerTelemetry` + a repeated `PartitionStat` message; run protoc. Extend `GoalConfig` (runner.go:972): `MaxThrottledRequests *int`, `MaxEndToEndLatencyMs *float64`, `MaxPartitionSkewPct *float64`.

**Files to touch**
- EDIT pkg/protocol/protocol.go:169-193 — new `ServerTelemetry` fields + `PartitionStat`.
- EDIT pkg/protocol/eventhubs/*.go (NEW, from EH.3) — populate the fields per send/receive/checkpoint.
- EDIT pkg/engine/metrics.go:105-128, 801-863 — summary fields + merge/finalize (skew stddev, hot-partition, counter sums, gauge max).
- EDIT api/proto/generator.proto:124-158 — new fields + `PartitionStat` message; regenerate `*.pb.go`.
- EDIT pkg/api/distributed_metrics.go:419-500 — clone + accumulate + finalize the new fields.
- EDIT pkg/api/runner.go:407-,539-,972-990 — API summary + `GoalConfig` SLA fields.
- EDIT pkg/api/telemetry.go:~251-318 — new `eventhubs_throttle_pressure` analyzer.
- EDIT pkg/api/report_verdict.go + pkg/api/goal_controller.go — evaluate new SLA inputs.
- EDIT dashboard/src/components/TestExecutionSummary.tsx:41-72,649-672 + testExecutionMetricsModel.ts — EH panel (throttle rate, skew bar, checkpoint, batch fill) and TS types.
- EDIT pkg/api/swagger regeneration (`go generate ./pkg/api`) for `GoalConfig` change.

**Build tags / dependencies** — No new go.mod deps for the result model itself (the `azeventhubs` SDK is pulled in by EH.3 under the `eventhubs_real` tag with a fail-closed default stub, matching the kafka_real/jms_*_real pattern). The summary/proto/dashboard changes are tag-agonostic and compile in the default build. CI matrix: the partition-skew and throttle assertions that need a live emulator run under `eventhubs_real` (testcontainers azure-eventhubs-emulator); default-build tests cover merge/finalize/SLA math with synthetic `ServerTelemetry`.

**Test plan** — Unit (table-driven): `pkg/engine/metrics_test.go` — skew stddev + hot-partition flag across partition mixes (even, one hot, single partition, empty); counter summation vs averaging; ActiveConnections max; checkpoint averaging with zero writes. `pkg/api/distributed_metrics_test.go` — multi-generator merge preserves counter sums and recomputes skew. `pkg/api/telemetry_test.go` — `eventhubs_throttle_pressure` fires only above threshold; stable signature. `pkg/api/report_verdict_test.go` — each new `GoalConfig` field breaches/passes at boundary. Integration (behind `eventhubs_real`): produce skewed traffic against the emulator and assert hot-partition + throttle counters surface. Dashboard: browser-verify the EH panel renders for `dependency_type` `eventhubs_*` and hides for non-EH runs (per testing policy, type-check alone insufficient). Run `go test ./...`.

**Acceptance criteria**
- [ ] `protocol.ServerTelemetry` carries all listed EH fields + `PartitionStat`; populated by the EH protocol.
- [ ] Engine + distributed merge sum counters, average latencies/fill, max gauges, and recompute partition skew stddev + hot-partition count.
- [ ] Proto round-trips the new fields (regenerated `*.pb.go` committed; multi-generator merge test green).
- [ ] `eventhubs_throttle_pressure` analyzer emits with a stable signature only above threshold.
- [ ] `GoalConfig` exposes `MaxThrottledRequests`, `MaxEndToEndLatencyMs`, `MaxPartitionSkewPct`; verdict honors them; swagger regenerated and `TestSwaggerYAMLIsFresh` passes.
- [ ] Dashboard EH panel shows throttle rate, per-partition skew, checkpoint, and batch-fill; hidden for non-EH runs.
- [ ] `go test ./...` and `go test -tags eventhubs_real ./...` pass; DSL freshness test passes.

**Effort / dependencies / risks** — Effort **M** (broad but mechanical fan-out across an already-established neutral pipeline; the only non-trivial logic is partition-skew stddev/hot-partition detection). Hard deps: **EH.3** (creates `pkg/protocol/eventhubs/` and the action types this telemetry attaches to), **P1.1**/**P1.5** (unified messaging result-model + SLA-input groundwork). Risks: (1) proto field churn must stay backward-compatible — append tags 17+, never renumber, or distributed runs silently drop fields; (2) per-result `Partitions` slices can bloat the 10k-cap metrics channel — keep `PartitionStat` minimal and cap slice length; (3) Azure's per-partition ceilings differ for Premium/Dedicated tiers — the 1 MB/s·1000 ev/s constant is Standard-tier; expose it as a config-overridable constant to avoid false hot-partition flags; (4) `EnqueuedTime` clock skew between broker and generator can produce negative end-to-end latency — clamp at zero.

### P3.1 — Messaging import breadth (AsyncAPI JMS/AMQP/Solace/MQ/MQTT bindings + standalone configs)

**Problem / current state** — `pkg/convert/asyncapi/converter.go:6-7` declares the converter Kafka-only: `Convert` hard-pins `Protocol: "kafka"` (`converter.go:235`), `deriveBrokers` ignores any non-`kafka*` server (`converter.go:350-354`), and `bindings` only models `kafkaBinding` (`converter.go:138-144`). `collectOperations` emits exclusively `kafka_produce/kafka_consume/kafka_request_reply` (`buildAction`, `converter.go:486-496`). Non-Kafka servers/channels are reported review-only (`support.go:20`, `asyncapi-schema-registry`/`asyncapi-servers-other`). There is no importer for broker config (MQSC/.bindings/SEMP/ARM/.avsc/.proto). The paramize hook (`import_paramize.go:15-27`) runs generic data/secret detection only; its correlation pass (`paramize/correlations.go`) is HTTP-extractor-shaped and never reasons about Kafka headers or JMS message properties. JMS actions already exist in the catalog (`catalog_jms.go:21,31`; `jms_publish/jms_consume/jms_request_reply`), but no importer targets them. There is zero Event Hubs awareness anywhere.

**Goal** — Convert AsyncAPI `jms`/`amqp`/`solace`/`ibmmq`/`mqtt` bindings into **executable** `jms_*`/`kafka_*` (Event Hubs via Kafka-binding `eh_*` alias or `kafka_*` against the EH endpoint) actions instead of review-only, and ingest standalone broker artifacts so an operator can hydrate `config.jms`/`config.kafka` without hand-editing. Add a messaging-aware correlation pass. This realizes the messaging-import column of the LRE parity matrix.

**Design** — Generalize the converter from "Kafka emitter" to a binding-dispatched emitter. Introduce a `protocolBinding` enum derived from each channel/server (`kafka`, `jms`, `amqp`, `solace`, `ibmmq`, `mqtt`, `eventhub`) and route per-operation to a `targetProtocol` + action-type family. JMS/AMQP/Solace/IBM-MQ bindings → `jms_publish`/`jms_consume`/`jms_request_reply` (Solace/SB sub-protocols flow through the existing JMS-over-AMQP adapter, respecting `unavailableIbmMqAdapter` — MQ stays importable-but-flagged honest, not silently executable). `mqtt` → JMS-over-AMQP best-effort with a review note (no native MQTT runtime). Azure `kafka`-binding servers whose host matches `*.servicebus.windows.net:9093` are recognized as Event Hubs and emit Kafka actions with `security_protocol=sasl_ssl`, `sasl_mechanism=plain`, username `$ConnectionString` prefilled. A mixed document yields **one script per target protocol** (reuse the multi-script paramize path, `import_paramize.go:33`), since a `dsl.Script` has a single `Protocol`. Standalone config importers are thin parsers producing a partial `dsl.ScriptConfig` (or a `ConfigOverlay`) merged into a draft: Kafka topic/cluster config + Confluent `.avsc`/`.proto` (registry/serializer hints, review-only execution), MQSC + JNDI `.bindings` → `config.jms`, Solace SEMP → JMS endpoints, SB ARM → Event Hub/SB namespace. Rejected alternative: a new native MQTT/SMF protocol plugin — out of scope (that is a separate P-item); rejected: forcing all bindings into one script via per-action protocol override — `dsl` has no per-action protocol field and the engine dispatches by `Script.Protocol`.

**DSL & catalog changes** — No new action *types* required for JMS/Kafka (reuse existing). Add an optional `eh_produce`/`eh_consume` alias family in `catalog_kafka.go` **only if** Event Hubs needs distinct validation; preferred is reuse of `kafka_*` with a new `config.kafka` field `endpoint_kind` enum (`kafka|eventhub`). Add `config.jms` fields if absent for imported metadata: `delivery_mode`, `acknowledgement`, `message_type` (enum `text|bytes|map`), `connection_factory` (in `dsl/types.go`). Add `schema_registry_url`/`schema_subject` to `KafkaConfig`. Regenerate with `go generate ./pkg/dsl` (and `./pkg/api` for routes); freshness is enforced by `pkg/dsl/schemagen` and `TestSwaggerYAMLIsFresh`.

**Types & interfaces** — In `converter.go`:
```go
type bindings struct {
    Kafka  *kafkaBinding  `json:"kafka,omitempty"`
    JMS    *jmsBinding    `json:"jms,omitempty"`
    AMQP   *amqpBinding   `json:"amqp,omitempty"`
    Solace *solaceBinding `json:"solace,omitempty"`
    IBMMQ  *ibmmqBinding  `json:"ibmmq,omitempty"`
    MQTT   *mqttBinding   `json:"mqtt,omitempty"`
}
type targetProtocol string // "kafka" | "jms-solace" | "jms-servicebus" | "jms-ibmmq" | "eventhub"
func (op messagingOp) emit(includeAssertion bool) (dsl.Action, []string)
```
New packages: `pkg/convert/brokerconfig` with `func ParseMQSC([]byte) (ConfigOverlay, error)`, `ParseJNDIBindings`, `ParseSolaceSEMP`, `ParseServiceBusARM`, `ParseConfluentSchema`; `type ConfigOverlay struct { Protocol string; JMS *dsl.JMSConfig; Kafka *dsl.KafkaConfig; Warnings []string }`. Extend `paramize`: add `KindMessageHeader Kind = "message_header"` (`paramize/paramize.go:30`) and a `messagingCorrelations(script)` pass keyed off Kafka `params.headers` / JMS `params.properties`.

**Files to touch** —
- EDIT `pkg/convert/asyncapi/converter.go` (binding dispatch, multi-protocol output) — currently single-result; change `ConvertResult.Script` → add `Scripts []*dsl.Script`.
- EDIT `pkg/convert/asyncapi/support.go:18-39` (flip `asyncapi-servers-other`/binding rows to executable; add jms/amqp/solace/ibmmq/mqtt/eventhub rows + notes).
- NEW `pkg/convert/asyncapi/binding_jms.go`, `binding_eventhub.go` + `_test.go`.
- NEW `pkg/convert/brokerconfig/{mqsc.go,jndi.go,semp.go,arm.go,confluent.go,*_test.go}`.
- EDIT `pkg/api/handlers_web_api_imports.go:386-462` (handle multi-script result, EH review section) + NEW handlers for config-overlay imports.
- EDIT `pkg/api/import_paramize.go` (call messaging correlation pass).
- EDIT `pkg/api/route_catalog.go` (after `:127`) — add `POST /api/v1/scripts/import/broker-config` + `/support-matrix`, `editorImportAccess(...)`.
- EDIT `pkg/dsl/types.go` (`JMSConfig`/`KafkaConfig` fields), `pkg/dsl/validate.go:718-750,891` (EH/registry validation), `pkg/convert/paramize/{paramize.go,correlations.go}`.
- EDIT `dashboard/src/components/CreateTestModal` import surface (file-type accept + new review sections).

**Build tags / dependencies** — Parsers are pure-Go (XML for MQSC/ARM, JSON/Avro-IDL/protobuf-text for `.avsc`/`.proto` — parse structurally, do not link `franz-go` schema libs). No new `go.mod` deps needed for import (execution still gates real adapters behind existing `kafka_real`/`jms_solace_real`/`jms_servicebus_real`). No new CI tag matrix entries; integration tests reuse the existing `kafka_real`/`jms_*_real` jobs.

**Test plan** — Unit (table-driven) in `pkg/convert/asyncapi/binding_*_test.go`: each binding → expected action type/target/protocol, mixed-document → N scripts, EH host detection, MQTT review note. `pkg/convert/brokerconfig/*_test.go`: golden MQSC/.bindings/SEMP/ARM/.avsc/.proto → `ConfigOverlay`, malformed input → error, zip-bomb guard parity. `pkg/convert/paramize`: Kafka-header + JMS-property correlation detection (happy + no-correlation). Run `go test ./...` and the schemagen/swagger freshness tests. Integration: emitted JMS script executes end-to-end under `-tags jms_solace_real` against a testcontainers AMQP broker; Kafka/EH script under `-tags kafka_real`. Dashboard: browser-verify the new review sections render and EH/JMS rows appear (the messaging surface filter must be widened — coordinate with P2.x dashboard work).

**Acceptance criteria** —
- [ ] AsyncAPI doc with a `jms` binding imports to an executable `jms_publish`/`jms_consume` script (no `review_only` for that channel).
- [ ] AsyncAPI doc with `*.servicebus.windows.net:9093` kafka server imports as Event Hubs with `$ConnectionString`/`sasl_ssl` prefilled.
- [ ] Mixed-binding doc yields one script per target protocol; each validates via `validateScript`.
- [ ] `POST /api/v1/scripts/import/broker-config` parses MQSC/.bindings/SEMP/ARM/.avsc/.proto into a `config` overlay with warnings; route appears in regenerated `swagger.yaml`.
- [ ] Messaging correlation pass surfaces Kafka-header / JMS-property suggestions (`KindMessageHeader`) in the review panel.
- [ ] `support-matrix` flips jms/amqp/solace/ibmmq/eventhub rows to executable; mqtt/IBM-MQ honestly flagged.
- [ ] `go generate ./pkg/dsl ./pkg/api` clean; all freshness tests + `go test ./...` pass.

**Effort / dependencies / risks** — Effort **L** (binding dispatch + 5 config parsers + paramize + multi-script plumbing). Depends on **P2.2** and **P2.3** (the JMS message-body/property and Event Hubs runtime work those add must exist before imports can claim "executable"). Risks: (1) `ConvertResult` single-script → multi-script is a breaking shape change touching the handler and dashboard — stage behind the multi-script paramize path already used for JMeter; (2) honesty trap — emitting "executable" JMS/MQ when the underlying adapter is a stub (`unavailableIbmMqAdapter`, no native MQTT/SMF) would regress the project's documented review-only honesty; the import must downgrade to review with a clear note when the target adapter is unavailable; (3) `.avsc`/`.proto` parsing scope creep — limit to structural metadata + registry hints, defer real serialize/deserialize to the separate schema-registry item.

### P4.1 — Messaging-aware results, SLA/goals, and dashboard panels

**Problem / current state** — Messaging telemetry today is a flat, single-dimension rollup. `protocol.ServerTelemetry` (pkg/protocol/protocol.go:169-193) carries `MessageLatencyMs`, `MessagesSent/Received`, `MessagesSentPerSec/ReceivedPerSec`, `ConsumerLagMs` (a wall-clock value, not offset depth) — with **no** delivery-mode, partition, subscription, redelivery, DLQ-reason, dup/out-of-order, commit/abort, or throttle fields. `ServerTelemetrySummary` (pkg/engine/metrics.go:105-128) and the distributed merge (pkg/api/distributed_metrics.go:434-500, proto fields generator.proto:132-133) faithfully carry only those. `GoalConfig` (pkg/api/runner.go:972-990) has zero messaging thresholds; `evaluateGoalController` (goal_controller.go:138-161) and `goalBreachSet` (goal_controller.go:471-480) only know latency/error/serverProcessing/transfer/vitals; `goalStopReason` (goal_controller.go:496-520) and `deriveReportVerdict`/`reportConditionalConcerns` (report_verdict.go:50-100) have no messaging vocabulary. The dashboard already has substantial reliability panels (TestExecutionSummary.tsx:41-62 — payload hotspots, dead-letter activity, topic fan-out, consumer-lag trend) **but every builder is hard-filtered to `dependency_type.startsWith('jms_')`** (testExecutionMetricsModel.ts:73, TestExecutionSummary.tsx:41), so Kafka and Event Hubs rows never render. Anomaly detection (telemetry.go:235-380) is likewise JMS-only.

**Goal** — A protocol-neutral messaging results story: per-delivery-mode (PERSISTENT/NON_PERSISTENT), per-partition, and per-subscription throughput/lag rollups; reliability dashboards (redelivery rate, DLQ-by-reason, dup/out-of-order, commit/abort, queue-depth trend) with dedicated Kafka + Event Hubs panels alongside JMS; and GoalConfig messaging thresholds wired through goal evaluation, runtime auto-stop, and the report verdict.

**Design** — Keep the results pipeline protocol-neutral. Extend the per-result `ServerTelemetry` with optional scalar reliability counters plus three small breakdown maps keyed by dimension value (`DeliveryMode`, `Partition`, `Subscription`). The engine aggregator (metrics.go `mergeServerTelemetrySummary`/`finalizeServerTelemetrySummary`, lines 801/836) sums them into new `ServerTelemetrySummary` maps; the distributed merge (distributed_metrics.go:434-500) and proto mirror them. Reliability **rates** (redelivery %, DLQ %, dup %, throttle %) are derived in the summary finalizer from counts ÷ messages, never sent per-result (avoids double-division across the distributed merge). Goal evaluation adds messaging breaches to `goalBreachSet` using the same `hasServerTelemetryBreach` pick-function pattern already used for serverProcessing/transfer (goal_controller.go:141-146) — no new evaluation machinery. Dashboard: generalize the JMS builders to a `messaging`-protocol predicate (jms_/kafka_/eventhub_ prefixes) and add protocol-tagged Kafka + Event Hubs panel sections, reusing existing chart components. **Alternatives rejected:** (a) a separate `MessagingTelemetry` struct parallel to `ServerTelemetry` — rejected; it would fork the merge/proto/dashboard plumbing and break the protocol-neutral contract. (b) Computing rates per-result — rejected; non-associative under distributed summation.

**DSL & catalog changes** — None required for results/goals plumbing (telemetry is emitted by protocol runtimes, not authored). GoalConfig is a scenario/runtime construct, not a DSL action. The DSL catalog and `go generate ./pkg/dsl` are **not** touched; the catalog freshness test is unaffected. (Consumer/producer actions that populate partition/subscription/delivery-mode already exist from P1/P2/EH dependencies.) The swagger spec (`go generate ./pkg/api`) **must** be regenerated because GoalConfig is part of the `POST /api/v1/scenarios` / test-create request body — `TestSwaggerYAMLIsFresh` will fail otherwise.

**Types & interfaces** — `pkg/protocol/protocol.go` `ServerTelemetry` additions:
```go
DeliveryMode      string  `json:"delivery_mode,omitempty"`      // "PERSISTENT"|"NON_PERSISTENT"
Partition         string  `json:"partition,omitempty"`
Subscription      string  `json:"subscription,omitempty"`
ConsumerLagOffset int64   `json:"consumer_lag_offset,omitempty"` // real offset depth (Kafka/EH)
QueueDepth        int64   `json:"queue_depth,omitempty"`
RedeliveredCount  int     `json:"redelivered_count,omitempty"`
DeadLetterCount   int     `json:"dead_letter_count,omitempty"`
DeadLetterReason  string  `json:"dead_letter_reason,omitempty"`
DuplicateCount    int     `json:"duplicate_count,omitempty"`
OutOfOrderCount   int     `json:"out_of_order_count,omitempty"`
CommitCount       int     `json:"commit_count,omitempty"`
AbortCount        int     `json:"abort_count,omitempty"`
ThrottledCount    int     `json:"throttled_count,omitempty"`
```
`pkg/engine/metrics.go` `ServerTelemetrySummary` additions: `MaxConsumerLagOffset int64`, `MaxQueueDepth int64`, count totals + derived `RedeliveryRatePct/DLQRatePct/DuplicateRatePct/ThrottleRatePct float64`, `DLQByReason map[string]int64`, and `ByDeliveryMode/ByPartition/BySubscription map[string]*MessagingDimensionMetrics` (new struct: `{Messages, AvgLatencyMs, MaxConsumerLagOffset, ThroughputPerSec}`). `pkg/api/runner.go` `GoalConfig` additions: `MaxConsumerLagMs/MaxConsumerLagOffset/MaxQueueDepth *float64`, `MaxProduceConsumeRateGapPct *float64`, `MaxMessageLatencyP95Ms *float64`, `MaxRedeliveryRatePct *float64`, `MaxDLQRatePct *float64`, `MaxThrottleRatePct *float64`. `goalBreachSet` (goal_controller.go:471) gains matching bool fields; `GoalStatus` gains the matching `*Breached` fields; `goalStopReason` gains cases. `api/proto/generator.proto` `ServerTelemetry` message gains the new fields (next free tags), regenerate via existing protoc gen.

**Files to touch** —
- EDIT pkg/protocol/protocol.go:169-193 — extend `ServerTelemetry`.
- EDIT pkg/engine/metrics.go:105-128, 801, 836 — `ServerTelemetrySummary` fields + merge/finalize; NEW `MessagingDimensionMetrics`.
- EDIT api/proto/generator.proto:120-135 — new fields; regenerate `generator.pb.go`.
- EDIT pkg/api/distributed_metrics.go:434-500 — carry/sum new fields, merge dimension maps, finalize derived rates.
- EDIT pkg/api/runner.go:972-990 — `GoalConfig` thresholds.
- EDIT pkg/api/goal_controller.go:138-161, 471-480, 496-520 — breach wiring, stop reasons.
- EDIT pkg/api/report_verdict.go:56-100 — messaging concerns/findings.
- EDIT pkg/api/telemetry.go:235-380, 549-562 — generalize JMS samplers to messaging predicate + threshold alerts for new goals.
- EDIT dashboard/src/components/testExecutionMetricsModel.ts:73-96 — messaging-protocol predicate; per-mode/partition/subscription + reliability builders.
- EDIT dashboard/src/components/TestExecutionSummary.tsx:41-62, panels — dedicated Kafka + Event Hubs sections; reliability charts.
- EDIT pkg/api/server.go (route layer) only if GoalConfig validation changes; regenerate docs/swagger.yaml.

**Build tags / dependencies** — No go.mod additions. No new build tags; reliability/offset fields are populated by the existing `kafka_real` / `jms_*_real` / Event Hubs adapters (the default fail-closed stubs simply leave them zero). CI tag matrix is unchanged, but the real-adapter integration jobs (`-tags kafka_real`, `-tags jms_servicebus_real`, Event Hubs) now assert the new fields are non-zero end-to-end.

**Test plan** — Unit (table-driven, mandatory): `metrics_test.go` for merge/finalize of new counts, derived-rate math (zero-message divide guard, rate = count/messages), and dimension-map aggregation; `distributed_metrics_test.go` for cross-generator summation of dimension maps + that derived rates are computed post-merge, not double-divided; `goal_controller_test.go` covering each new threshold (set/unset, at-boundary, over) and `goalStopReason` precedence; `report_verdict_test.go` for messaging concerns downgrading Pass→Conditional and a confirmed messaging breach→Fail. Integration: `kafka_real` test (testcontainers Redpanda/Kafka) asserting `ConsumerLagOffset`/`Partition` populate and a lag-offset goal trips; Event Hubs adapter test asserting partition + throttle counts surface; JMS service-bus DLQ-by-reason path. Dashboard/browser verification (per policy, type-check insufficient): load a run with synthetic Kafka + Event Hubs + JMS telemetry, confirm dedicated Kafka and Event Hubs panels render with redelivery/DLQ-by-reason/queue-depth/commit-abort charts and per-partition/subscription rollups, and that the JMS panels still render unchanged.

**Acceptance criteria** —
- [ ] `ServerTelemetry`, `ServerTelemetrySummary`, and the proto carry delivery-mode/partition/subscription/redelivery/DLQ-reason/dup/out-of-order/commit/abort/throttle/offset-lag/queue-depth, surviving the distributed merge.
- [ ] Per-delivery-mode, per-partition, and per-subscription throughput + lag rollups appear in the metrics snapshot and via the API.
- [ ] All seven GoalConfig messaging thresholds evaluate, set `*Breached`, drive `StopOnSLABreach`, and produce distinct `goalStopReason` strings.
- [ ] A confirmed messaging-goal breach yields a Fail verdict; a soft messaging concern yields Conditional Pass.
- [ ] Dashboard renders dedicated Kafka and Event Hubs panels plus reliability charts (redelivery rate, DLQ-by-reason, dup/out-of-order, commit/abort, queue-depth trend); existing JMS panels unchanged.
- [ ] `go test ./...` passes; `TestSwaggerYAMLIsFresh` passes after `go generate ./pkg/api`; real-adapter integration jobs assert non-zero new fields.

**Effort / dependencies / risks** — Effort **L** (confirmed; touches protocol, engine, proto/distributed merge, goals, verdict, telemetry, and two dashboard files). Depends on P1.1, P1.5, P2.1, P2.3, P2.4, EH.4 supplying the source fields (partition/subscription/delivery-mode/DLQ/throttle at emit time) — this item only aggregates and surfaces them; if upstreams under-populate, panels render empty. Risks: (1) derived-rate correctness under distributed summation — mitigated by finalizing rates only post-merge; (2) dimension-map cardinality blow-up (high partition counts) — cap/bucket partitions in the finalizer; (3) proto tag-number drift — append-only, never reuse tags; (4) dashboard predicate generalization could regress JMS-only panels — covered by the unchanged-JMS browser check.

---

## Sequencing & recommended order

The dependency graph yields five waves. Items within a wave can run in parallel.

- **Wave 0 — Unblock (days):** `P0.0` then `EH.1`. P0.0 is a 2-file capability mirror + 1-line
  conditional that makes real Kafka **and** the Event Hubs Kafka endpoint usable through the API on a
  `kafka_real` build; EH.1 makes Event Hubs a guided, first-class import/config target with no new
  runtime code. Highest value-per-effort in the program.
- **Wave 1 — Cross-cutting foundations:** `P1.1` + `P1.4` (no deps, parallel), then `P1.2`, `P1.3`,
  `P1.5` (all depend on P1.1). These make existing mock/tagged runs honest and visible and provide the
  ack/tx, request-reply, result-model, and lag/depth primitives every platform reuses.
- **Wave 2 — Per-platform depth:** pick **one vertical first** and drive it end-to-end before fanning
  out. Recommended first vertical = **Kafka/Event Hubs** (`P2.1 → P2.2 → EH.2 → EH.3 → EH.4`) because
  it has the strongest enterprise pull and the most reuse. Then the JMS vertical (`P2.3 → P2.4`), then
  the two greenfield adapters (`P2.5` IBM MQ, `P2.6` Solace) which are the largest single lifts.
- **Wave 3 — Import breadth:** `P3.1` (needs P2.2 serde + P2.3 JMS semantics to emit executable, not
  review-only, scripts).
- **Wave 4 — Results completion:** `P4.1` (per-mode/partition/subscription rollups, reliability
  dashboards, messaging SLA/goals) — last, because it consumes the signals every prior wave produces.

**Critical path:** `P1.1 → P1.2 → P2.1 → P2.2 → P3.1 → P4.1`. IBM MQ (`P2.5`) and Solace
(`P2.6`) are off the critical path and can be scheduled by business priority.

## Testing & CI strategy

- **Mandatory tests** per the project policy: unit (table-driven) alongside each change, run
  `go test ./...` before any Go change is complete; browser-verify every dashboard change (P1.5, EH.4,
  P4.1).
- **Build-tag matrix** — add CI jobs so both gating directions are proven: `default`, `kafka_real`,
  `jms_solace_real`, `jms_servicebus_real`, `eventhubs_real`, `ibmmq_real`. Each tagged job runs
  `go test -tags <tag> ./pkg/dsl ./pkg/protocol/...`.
- **Integration via testcontainers** where a local broker image exists: Kafka (Redpanda or Apache
  Kafka) + Confluent/Apicurio Schema Registry (P2.1/P2.2), IBM MQ (`icr.io/ibm-messaging/mq`, P2.5),
  Solace (`solace/solace-pubsub-standard`, P2.6), ActiveMQ Artemis for generic JMS-over-AMQP (P2.3).
  Gate these behind their build tag + an opt-in env so they don't run on every PR.
- **Azure caveat (open):** Azure Service Bus has an official emulator
  (`mcr.microsoft.com/azure-messaging/servicebus-emulator`) usable for P2.4; **Event Hubs has no
  GA local emulator** — EH.3/EH.4 integration needs either a live namespace (secrets/cost) or the
  preview Event Hubs emulator, otherwise mock + contract tests only. The Kafka-endpoint path (P0.0/
  EH.1/EH.2) can be exercised against a real namespace with connection-string PLAIN.
- **Generated-artifact freshness** (test-enforced): after catalog changes run `go generate ./pkg/dsl`
  (regenerates `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`); after route changes run
  `go generate ./pkg/api` (regenerates `docs/swagger.yaml`).

## Risks & open questions

1. **Which real adapters ship in the default generator image?** Closing these gaps means real brokers
   in CI and a build-tag policy. Decide the default-image tag set vs. opt-in images, and document that
   the default image still fail-closes on non-mock messaging.
2. **IBM MQ client is cgo** — `ibm-messaging/mq-golang` needs the MQ client C libraries, which
   complicates cross-compiling the `linux/amd64` generator binary. Open: native MQI (cgo) vs. a pure-Go
   MQ REST/AMQP fallback. (P2.5 design must pick one.)
3. **Solace native SMF has no pure-Go client** — options are the C SDK via cgo (CCSMP) or staying on
   AMQP. (P2.6 open question.)
4. **Event Hubs has no GA emulator** — see Testing caveat; affects EH.3/EH.4 coverage confidence.
5. **Percentile transport** — proto carries raw per-transaction durations (no t-digest snapshot), so
   message-latency/lag percentiles ship as raw values (confirmed in P1.1); watch payload volume at
   high TPS.
6. **Kafka EOS/transactions on Event Hubs are Premium/Dedicated preview-only** — scope P2.1
   transactions as broker-capability-gated so Standard-tier runs degrade gracefully.

## Verification (end-to-end)

- Each item: `go test ./...` (default) plus `go test -tags <real> ./pkg/...` for tagged items; new CI
  job green in both directions.
- **P0.0 + Event Hubs slice:** on a `kafka_real` controller, `POST /api/v1/tests` with a non-mock
  Kafka script returns 201 (not a 400); import an AsyncAPI doc whose server host is
  `*.servicebus.windows.net:9093` and confirm it imports to a runnable script with the Event Hubs
  defaults pre-filled; run it against a namespace (or emulator) and confirm throughput + consumer-lag
  render in the dashboard.
- **Dashboard items (P1.5, EH.4, P4.1):** `cd dashboard && npm run build`, then browser-verify the new
  panels render with live WebSocket data and messaging SLA pass/fail shows in the report verdict.
- **Freshness:** `go generate ./pkg/dsl` and `go generate ./pkg/api`; confirm the catalog freshness
  test, `pkg/dsl/schemagen`, and `TestSwaggerYAMLIsFresh` all pass.

---

## Annex A — Azure Event Hubs: full day-1 implementation spec (producer-first)

This annex is the detailed, decisions-locked day-1 implementation spec for Azure Event Hubs (from a
parallel planning session). It **expands items EH.1–EH.4** above with file-level detail, and adds
Event Hubs Capture import, Prometheus exposition, dashboard wiring, and Azure deployment work not
otherwise itemized in the umbrella program. Reconciliation with the umbrella program:

- **P0.0 is a hard prerequisite** to Phase 2 below. The Kafka-endpoint profile cannot be *created*
  through the REST API until the unconditional non-mock-Kafka validator block (`validate.go:854`) is
  build-tag-gated; Phase 2's `validate.go` edits assume P0.0 has landed.
- **Mapping:** EH.1 ⇄ Phase 2 (Kafka-endpoint as a `profile:"eventhubs"` on the `kafka` protocol —
  this **supersedes** EH.1's lighter "config preset" design); EH.2 ⇄ Phase 2's OAUTHBEARER work;
  EH.3 ⇄ Phase 3 (native AMQP `eventhubs` protocol) + Phase 4 (catalog/validate/capabilities);
  EH.4 ⇄ Phases 1, 6, 7 (telemetry plumbing + Prometheus + dashboard); EH.5 ⇄ Phase 5 (Capture
  import); EH.6 ⇄ Phases 6 & 8 (Prometheus + deployments/CI).
- **Scope is producer-first (locked with the user).** APEX Load acts as the **producer** stressing
  ingestion; a read-only **observer consumer probe** measures end-to-end produce→consume latency and
  downstream backlog. This **supersedes EH.3's earlier "full Processor + Blob checkpoint store + epoch
  exclusive consumer at day-1"** — those consumer-side features are defined in the catalog but
  **deferred past day-1**.
- **Phase 1 telemetry plumbing shares the same 7-layer pipe as P1.1** (the unified result model). Do
  them in **one coordinated pass** — add P1.1's reliability counters/percentiles and the Event Hubs
  per-partition/throttle/E2E/credit fields to `ServerTelemetry` + proto + merge together, so the proto
  and distributed-merge layers are touched once, not twice.

---

### Context

APEX Load can test Apache Kafka today (produce/consume/request-reply, mock broker by
default, real franz-go adapter via `-tags kafka_real`) and has a strong Azure pattern
precedent in the JMS Azure Service Bus adapter. It has **no** first-class Azure Event Hubs
support. Event Hubs is a primary Azure messaging target and exposes **two faces**:

- **Kafka-protocol endpoint** — `{namespace}.servicebus.windows.net:9093`, TLS mandatory,
  SASL `PLAIN` (username literal `$ConnectionString`, password = connection string) **or**
  `OAUTHBEARER` (AAD token), 1 MB message cap.
- **Native AMQP** (`azeventhubs` SDK) — partition keys/IDs, consumer groups, checkpoint
  store, Event Hubs Capture, AMQP credit/flow control — the full first-class feature set.

Goal: **full day-1 coverage** across **import → execution → metrics → reporting** for both
faces, following the codebase's established protocol/adapter/import/metrics conventions.

**Primary use case (producer-first):** APEX Load acts as a **producer**, publishing messages to
Event Hubs at load for downstream consumers — i.e. stress-testing the **ingestion** path. A
lightweight, read-only **observer consumer probe** runs alongside the producers to measure real
end-to-end produce→consume latency and downstream backlog/lag. APEX is **not** load-testing the
consumer side, so checkpointing and request/reply are deferred from day-1 core.

**Decisions locked with the user:**
1. **Auth:** connection-string (SAS) **and** AAD/workload-identity. → native AMQP gets AAD
   free via `pkg/engine/azure_token.go`; the **Kafka endpoint requires implementing the
   currently-missing `OAUTHBEARER`** mechanism (franz `pkg/sasl/oauth`, confirmed present).
2. **Packaging:** build-tag opt-in, mock-by-default — `eventhubs_real` (native AMQP) and
   `kafka_real` (Kafka-endpoint profile), matching `kafka_real`/`jms_servicebus_real`.
3. **Import:** AsyncAPI Event Hubs binding (publish/send operations) **+** Event Hubs Capture
   files (real captured payloads + partition keys to replay). (ARM/Bicep dropped.)
4. **Metrics:** producer-first aggregate **and** per-partition. **Per-partition is interpreted as
   produce throughput/bytes per partition (skew/hot-partition detection)**, not consumer lag.
5. **Role:** producer **plus** a read-only observer consumer probe (E2E latency + downstream
   backlog). Checkpoint + request/reply deferred.

**Headline producer metrics (day-1 priority order):** publish throughput (events/sec + MB/sec),
ack/produce latency, **throttling / 429 `ServerBusy`** (Event Hubs throttles on Throughput/
Processing Units — the key ingestion-stress signal), ingress bytes/sec, per-partition produce
distribution/skew. **Probe-derived:** end-to-end produce→consume latency, downstream consumer
lag/backlog.

### Architecture decision — dual surface

- **(A) Kafka-endpoint = a `profile` on the existing `kafka` protocol.** Cheapest, highest
  reuse: franz client already does PLAIN SASL + TLS + consumer groups + lag
  (`pkg/protocol/kafka/realclient_franz.go`). Add a `profile: "eventhubs"` discriminator that
  forces TLS, wires `$ConnectionString` PLAIN / OAUTHBEARER AAD, and guards the 1 MB cap.
  Reuses 100% of the metrics/import/dashboard pipeline.
- **(B) Native AMQP = a new dedicated `eventhubs` protocol** behind `eventhubs_real`,
  cloning the JMS provider-adapter pattern (`pkg/protocol/jms/jms.go` `selectAdapter()` +
  `realadapter_servicebus.go` pooling + paired `*_real.go`/`*_default.go`). Hosts the
  AMQP-only features: partition keys, checkpointing, Capture parity, AMQP credit metrics.

This mirrors how the repo already runs one Azure service over two transports (JMS Service
Bus via `azservicebus` **and** generic `go-amqp` in `realadapter_amqp.go`).

**Producer-first execution shape:** both surfaces implement `produce` as the centerpiece
(high-rate publish with batching, partition-key routing, acks, 1 MB guard). The **observer
probe** is a thin read-only `consume` running on a small, configurable number of VUsers (or a
dedicated background goroutine) that polls latest events to compute E2E latency (producer
timestamp in a record header → consume time) and downstream backlog. `checkpoint`/`request_reply`
actions are implemented behind the catalog but marked deferred/secondary (not day-1 core).

---

### Workstreams (sequenced)

#### Phase 1 — Telemetry plumbing (cross-cutting; do first so protocols have somewhere to emit)

Per-partition + new aggregate metrics must traverse **7 layers**. Lay the pipe before wiring emitters.

- **`pkg/protocol/protocol.go`** (`ServerTelemetry` struct, line 169): add producer-side scalar
  fields `ThrottledRequestCount int`, `ThrottleMs float64`, `IngressBytesPerSec float64`,
  `AckLatencyMs float64`; probe-side scalars `EndToEndLatencyMs float64`,
  `ConsumerBacklog int64` (reuses existing `ConsumerLagMs`/`MessagesSentPerSec`); native-AMQP
  `AmqpLinkCredit int`. Add a per-partition slice `PerPartition []PartitionTelemetry` (new
  struct: `Partition int32; ProduceMessagesPerSec float64; ProduceBytesPerSec float64;
  ProbeLagMs float64`) — produce distribution/skew is the primary read; `ProbeLagMs` populated
  only by the observer probe. A repeated struct (not a map) so it maps cleanly to a proto
  `repeated` message.
- **`pkg/engine/metrics.go`**: extend `mergeServerTelemetrySummary` (accumulate) and
  `finalizeServerTelemetrySummary` (divide-by-count for avg, max for credit/throttle); merge
  `PerPartition` by partition id.
- **`api/proto/generator.proto`** (`ServerTelemetry`, line 124, currently 15 fields): add
  fields 16–21 for the new scalars + `repeated PartitionTelemetry per_partition = 22;` and a
  new `message PartitionTelemetry {...}`. Regenerate Go stubs.
- **`pkg/distributed/generatorrpc/`**: map `protocol.ServerTelemetry` ↔ proto both directions,
  including `PerPartition`.
- **`pkg/api/distributed_metrics.go`**: extend `mergeDistributedServerTelemetry` (~line 449)
  and `finalizeAPIServerTelemetrySummary` (~line 487) for the new scalars + per-partition merge.
- **`pkg/api/runner.go`**: add JSON-tagged fields (+ per-partition array) to
  `ServerTelemetrySummary` so REST `GET /tests/{id}/metrics` and WS `metrics_update` carry them
  (handlers pass the DTO through unchanged — `handlers.go:handleGetMetrics`,
  `metrics.go:publishLiveMetrics`).
- Tests: extend `pkg/engine/metrics_test.go`, `pkg/api/distributed_metrics_test.go`.

#### Phase 2 — Kafka-endpoint profile (executable path #1)

- **`pkg/dsl/types.go`** (`KafkaConfig`, line ~224): add `Profile string` (`""|"eventhubs"`)
  and `MaxMessageBytes int`.
- **`pkg/protocol/kafka/kafka.go`**: add `Profile`/`MaxMessageBytes` to the private `config`;
  in `Init()` when `profile==eventhubs` → force `security_protocol=sasl_ssl`, default
  `sasl_mechanism=plain`, inject username `$ConnectionString` when password is a connection
  string, default `MaxMessageBytes=1<<20`; add a produce-side size guard.
- **`pkg/protocol/kafka/realclient_franz.go`** (`//go:build kafka_real`): in `saslMechanism()`
  add the `oauthbearer` case using `github.com/twmb/franz-go/pkg/sasl/oauth` with a refreshing
  token callback; force TLS for the profile in `usesTLS()`/`handle()` even if `security_protocol`
  is blank.
- **New `pkg/protocol/kafka/oauth_token_real.go`** (`//go:build kafka_real`): AAD token source
  via `azidentity.DefaultAzureCredential` with scope
  `https://{namespace}.servicebus.windows.net/.default`; reuse the caching approach in
  `pkg/engine/azure_token.go` (2-min refresh skew).
- **`pkg/protocol/kafka/realclient_default.go`**: message tweak only.
- **`pkg/dsl/catalog_kafka.go`**: add `kafka.profile` enum field + `params.partition_key`.
- **`pkg/dsl/validate.go`** (`validateKafkaScriptIssues`): when `profile==eventhubs` enforce
  TLS/auth presence and connection-string-or-AAD; reject plaintext.
- Secrets: connection string flows via `SASLPasswordSecret` → `resolveProtoConfigSecrets`
  (`pkg/engine/config.go`) + fail-closed `pkg/engine/secrets.go` (no new plumbing).
- Tests: **new** `pkg/protocol/kafka/eventhubs_profile_test.go` (profile defaults, TLS forcing,
  `$ConnectionString` injection, size guard) + a tagged OAUTHBEARER unit test.

#### Phase 3 — Native AMQP `eventhubs` protocol (executable path #2)

Clone the JMS layout (`pkg/protocol/jms/`). `apexctl` bundles kafka/jms today, so include eventhubs there too.

- **New `pkg/protocol/eventhubs/eventhubs.go`**: `protocol.Register("eventhubs", …)` in
  `init()`; `Init/Execute/Teardown/HealthCheck`; `selectAdapter()` mock-vs-real; action dispatch
  `eventhubs_produce` (primary), `eventhubs_consume` (observer probe — read-only, latest-offset),
  with `eventhubs_request_reply`/`eventhubs_checkpoint` defined but **deferred/secondary**
  (stubbed + no-op-friendly, not day-1 core). `eventhubs_produce` stamps a producer-timestamp
  header so the probe can compute E2E latency.
- **New `pkg/protocol/eventhubs/broker.go`**: `eventHubsClient` interface + in-memory mock that
  models **partitions/offsets/consumer-groups/checkpoints** (richer than the kafka mock, so
  per-partition metrics + checkpoint can be unit-tested without a live namespace). Seed from
  `pkg/protocol/kafka/broker.go`.
- **New `pkg/protocol/eventhubs/realadapter.go`** (`//go:build eventhubs_real`): `azeventhubs`
  `ProducerClient`/`ConsumerClient` (+ optional checkpoint store); connection-string **or**
  `DefaultAzureCredential` selection; client pooling keyed by auth+namespace+entity (copy
  `pkg/protocol/jms/realadapter_servicebus.go` pool); populate `PerPartition`, `AmqpLinkCredit`,
  throttle (on `amqp:resource-limit-exceeded`), bytes, and E2E latency telemetry.
- **New `pkg/protocol/eventhubs/realadapter_default.go`** (`//go:build !eventhubs_real`):
  fail-closed unavailable stub (copy `realadapter_servicebus_default.go`).
- **`cmd/{controller,generator,apexctl}/main.go`**: add blank import
  `_ "github.com/jasonczech/apexload/pkg/protocol/eventhubs"`.
- **`go.mod`**: add `github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs` (only new
  Azure dep; `go-amqp`, `azidentity`, `azcore` already present).
- Tests: `pkg/protocol/eventhubs/{eventhubs_test.go,broker_test.go}` (mock path) +
  tagged adapter/integration tests gated like `servicebus_integration_test.go`.

#### Phase 4 — DSL catalog, validation, capabilities, schemagen

- **`pkg/dsl/types.go`** (`ScriptConfig`, after `Kafka` line ~67): add
  `EventHubs EventHubsConfig`; define `EventHubsConfig` (copy `KafkaConfig`):
  `Namespace, ConnectionString, ConnectionStringSecret, EntityPath/EventHub, ConsumerGroup,
  AuthType ("connection_string"|"aad"), PartitionKey, PartitionID, CheckpointStore, Mock,
  TimeoutMs, MaxMessageBytes`.
- **New `pkg/dsl/catalog_eventhubs.go`**: `eventhubsActionSpecs()` — `eventhubs_produce`
  (primary, with `params.partition_key`/`partition_id`/batch fields), `eventhubs_consume`
  (observer probe), and `eventhubs_request_reply`/`eventhubs_checkpoint` (deferred/secondary),
  all `Protocols:[]string{"eventhubs"}`; wire into `catalog.go:actionCatalogSeeds()`. Register
  `eventhubs` as its own protocol family (no suffix → no `ProtocolFamily()` change needed).
- **`pkg/dsl/validate.go`**: add `validateEventHubsScriptIssues` (copy
  `validateKafkaScriptIssues`); enforce TLS-required, auth presence, consumer-group rules; gate
  the real adapter with the capability check; dispatch from `ValidateScriptIssues`.
- **New `pkg/dsl/capabilities_eventhubs_real.go` / `capabilities_eventhubs_default.go`**:
  `EventHubsRealAdapterAvailable()` (copy `capabilities_servicebus_*.go`).
- **Regenerate:** `go generate ./pkg/dsl` → `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`
  (freshness enforced by `pkg/dsl/schemagen/schemagen_test.go`).

#### Phase 5 — Import (AsyncAPI EH binding + Capture files) + paramize + API/RBAC

- **`pkg/convert/asyncapi/converter.go` + `support.go`**: in `deriveBrokers` detect Event Hubs
  Kafka bindings (`*.servicebus.windows.net` FQDN heuristic + `kafka-secure` → already defaults
  `sasl_ssl` at converter.go:369) and emit `kafka.profile=eventhubs`. **Producer focus:** map
  AsyncAPI **publish/send** operations + message schemas/examples to `*_produce` actions (the
  things APEX sends); update the support-matrix entry so EH servers are no longer `review_only`.
- **New `pkg/convert/eventhubscapture/`**: importer for **Event Hubs Capture Avro** blobs →
  `eventhubs_produce` DSL seeded from real event shapes. Uniform `Convert(req) (*ConvertResult,
  error)` + `SupportMatrix` (copy a small importer like `pkg/convert/jmeter/`). **Needs a new
  Avro decoder dep** (Capture is Avro by default; repo only has `parquet-go`) — add
  `github.com/hamba/avro/v2` (pure-Go, no cgo).
- **`pkg/api/handlers_web_api_imports.go`**: add `handleImportEventHubsCapture` (+ optional
  preview/validate); call `buildImportParameterization` (`pkg/api/import_paramize.go:15`) before
  responding, per the standard importer call-site pattern.
- **`pkg/api/route_catalog.go`**: add
  `routeAuth(POST, "/api/v1/scripts/import/eventhubs-capture", editorImportAccess(resolveImportProjectIDFromJSONOrForm, <maxBytes>))`
  + `routeAuth(GET, "/api/v1/scripts/import/eventhubs-capture/support-matrix", viewerGlobalAccess())`
  (copy the asyncapi entries at lines 126–127).
- **`pkg/api/server.go`**: register the handlers in the mux.
- **Regenerate:** `go generate ./pkg/api` → `docs/swagger.yaml` (`TestSwaggerYAMLIsFresh`).
- Tests: importer table tests + handler tests + a fresh-schema/swagger run.

#### Phase 6 — Prometheus exposition (gap-closing)

- **`pkg/api/prometheus_exposition.go`**: today exports only system gauges. Add per-test
  per-protocol gauges: `apexload_consumer_lag_ms`, `apexload_throttled_total`,
  `apexload_bytes_in_per_sec`, `apexload_bytes_out_per_sec`, `apexload_e2e_latency_ms`
  (labels `test_id`, `protocol`, optional `partition`). Net-new time series.

#### Phase 7 — Dashboard charts + reporting

Use the existing **JMS consumer-lag precedent** as the template.

- **`dashboard/src/types.ts`**: extend `ServerTelemetrySummary` + add `MetricsHistoryPoint`
  fields (`eventHubsConsumerLagMs`, `throttlePct`, `ingressBytesPerSec`, `egressBytesPerSec`,
  `e2eLatencyMs`, `amqpCredit`, per-partition array).
- **`dashboard/src/components/testExecutionMetricsModel.ts`** (`mapExecutionMetricToHistoryPoint`):
  copy the JMS block — filter transactions where `dependency_type` starts with `kafka`/
  `eventhubs`, aggregate `server_telemetry.*` into the new point fields.
- **`dashboard/src/components/testExecutionChartModel.ts`**: add `ChartConfig` entries +
  `chartMetricRows` cases — **producer-first**: publish throughput (events/sec + MB/sec),
  ack/produce latency, **throttling/429 rate**, ingress bytes/sec, per-partition produce
  distribution (skew); plus probe charts: E2E latency and downstream backlog/lag.
- **`dashboard/src/components/TestExecution.tsx`** (`executionWorkspaceCards`): add an
  "Event Hubs (Producer)" card/tab with a throttling callout and a per-partition skew view.
- **`dashboard/src/components/ReportCharts.tsx` / `ReportComposer.tsx`**: broker report section;
  requires extending `RunArchiveSummaryPoint` to persist broker fields into the archive timeline.
- **Browser-verify** (CLAUDE.md: type-check alone insufficient) via the preview workflow.

#### Phase 8 — Deployments, secrets, CI, docs

- **New `deployments/azure/terraform/eventhubs.tf`**: `azurerm_eventhub_namespace`,
  `azurerm_eventhub`, and two `azurerm_eventhub_authorization_rule`s — a **Send** rule (producers)
  and a **Listen** rule (observer probe); write both connection strings/SAS to Key Vault.
  `azurerm_role_assignment`: **Azure Event Hubs Data Sender** on the controller + generator
  workload identities, and **Data Receiver** only for the probe path (pattern: `identity.tf`,
  `keyvault.tf`).
- **`deployments/k8s/aks/configmap.yaml`**: `APEXLOAD_EVENTHUBS_NAMESPACE`,
  `APEXLOAD_EVENTHUBS_AUTH`, and `APEXLOAD_EVENTHUBS_PROBE_CONSUMER_GROUP` (reuse the
  already-wired workload identity).
- **`deployments/helm/apexload/values.yaml` + templates**: Event Hubs env/secret examples.
- **`.github/workflows/go-protocol-tags.yml` + `Dockerfile`**: add `eventhubs_real` (and ensure
  `kafka_real`) to the build-tag compile/test matrix so the tagged paths don't rot.
- Docs: update `CLAUDE.md` protocol list + `docs/COMPETITIVE_COMPARISON.md` if claims change.

---

### Key files to reuse (don't reinvent)

| Need | Reuse |
|---|---|
| Mock/real build-tag seam | `pkg/protocol/kafka/realclient_{default,franz}.go` |
| Provider-adapter dispatch + Azure pooling | `pkg/protocol/jms/jms.go` `selectAdapter()`, `realadapter_servicebus.go` |
| Generic AMQP transport reference | `pkg/protocol/jms/realadapter_amqp.go` (go-amqp) |
| AAD token caching | `pkg/engine/azure_token.go`, `pkg/engine/secrets.go` |
| Capability gating files | `pkg/dsl/capabilities_servicebus_{real,default}.go` |
| Telemetry struct + merge | `pkg/protocol/protocol.go:169`, `pkg/engine/metrics.go`, `pkg/api/distributed_metrics.go:449/487` |
| Importer shape + paramize hook | `pkg/convert/jmeter/`, `pkg/api/import_paramize.go:15`, `pkg/convert/asyncapi/converter.go` |
| Import route + RBAC | `pkg/api/route_catalog.go:126-127`, `editorImportAccess`/`resolveImportProjectIDFromJSONOrForm` |
| Dashboard per-broker metric precedent | JMS lag block in `dashboard/src/components/testExecutionMetricsModel.ts` |

### New dependencies
- `github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs` (native AMQP)
- `github.com/hamba/avro/v2` (Event Hubs Capture decode; only `parquet-go` exists today)
- (`franz-go/pkg/sasl/oauth` already in module cache — no go.mod change for OAUTHBEARER)

### Risks / call-outs
- **OAUTHBEARER for the Kafka endpoint is net-new** (declared but unimplemented today) — needed
  for AAD auth on path (A); native AMQP gets AAD free.
- **Per-partition metrics are the heaviest single piece** — a new repeated proto message +
  merge logic across engine, gRPC, controller, DTO, and dashboard.
- **Mock broker can't fully model** partitions/throttling/consumer-group semantics; the new
  `eventhubs` mock must be richer, and behavior parity ultimately needs tagged integration tests
  against a live namespace (gated, not default CI).
- **Freshness gates** (`go generate ./pkg/dsl` and `./pkg/api`) will fail the build if not rerun
  after catalog/route changes.

### Verification
1. `go build ./...` then `go build -tags "kafka_real eventhubs_real" ./...` (both adapters compile).
2. `go test ./...` (mock paths, catalog/validate, telemetry merge, importers).
3. `go test -tags "kafka_real eventhubs_real" ./pkg/protocol/...` for tagged unit tests; the live
   `*_integration` tests run only when an Event Hubs namespace/connection string is supplied.
4. `go generate ./pkg/dsl && go generate ./pkg/api`, then confirm `TestGeneratedSchemaIsFresh`
   and `TestSwaggerYAMLIsFresh` pass (no diff).
5. End-to-end mock run (producer-first): `apexctl run` with an `eventhubs` producer script
   (`mock:true`) plus the observer probe, and a `kafka` script with `profile:eventhubs`
   (`mock:true`) — confirm publish throughput, ack latency, simulated throttling/429,
   per-partition produce distribution, and probe-derived E2E latency in
   `GET /api/v1/tests/{id}/metrics`. (The mock broker simulates partition routing + a
   configurable throttle threshold so 429/skew metrics are exercisable without a live namespace.)
6. Import: `POST /api/v1/scripts/import/asyncapi` with an EH-binding spec → `kafka.profile=eventhubs`
   DSL; `POST /api/v1/scripts/import/eventhubs-capture` with a sample Capture Avro → produce DSL;
   verify support-matrix endpoints.
7. Dashboard: run a mock Event Hubs test and **browser-verify** the new broker panel/charts
   (consumer lag, produce vs consume, throttle, ingress/egress, per-partition) via the preview
   tools — screenshots as proof.
8. `terraform validate` in `deployments/azure/terraform`; `helm template` / `helm lint` on the chart.
