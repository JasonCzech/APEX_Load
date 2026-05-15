# Protocol Telemetry Parity Plan

## Objective
Bring every Stormrunner protocol to the same three-layer telemetry depth that [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md) establishes for the browser protocol. Each protocol should produce a complete performance story — not just "did it succeed?" but "why was it slow, where did time go, and how does it compare to baseline?"

## The Parity Model

The Mobile UX Strategy defines three layers. The equivalent for each protocol family:

| Layer | Browser (Mobile UX) | HTTP / SOAP / Java HTTP | WebSocket | gRPC | JMS |
|:---|:---|:---|:---|:---|:---|
| **L1: Execution** | Device emulation + touch | Request/response cycle | Connection lifecycle | RPC invocation | Message pub/sub |
| **L2: Deep Telemetry** | Core Web Vitals, JS heap, layout counts | Timing phases, response decomposition, dependency chain | Message latency distribution, throughput, backpressure | Per-method timing, stream throughput, protobuf size | Queue depth, broker latency, consumer lag |
| **L3: Intelligence** | Frustration detection, device comparison | SLA breach detection, dependency health scoring, response time decomposition | Connection stability scoring, throughput regression | Method hotspot detection, error classification | Broker health correlation, dead-letter analysis |

**Current state:** Every protocol implements Layer 1. HTTP, SOAP, Java HTTP, and JMS now share a protocol-neutral `ServerTelemetry` contract that flows through live metrics, distributed execution, archives, thresholding, goal-controller guardrails, and previous-run baseline comparison. No protocol has a full Layer 3 yet, but the server-side protocols now have a common telemetry baseline comparable to the browser protocol's `ClientMetrics` path.

## Implemented So Far

- Shared `ServerTelemetry` on `protocol.Result`, propagated through engine rollups, controller/generator RPC, archive retention, and dashboard contracts.
- HTTP timing decomposition for server processing, transfer, request/response payload sizes, request/response size classification, connection reuse, and redirect-hop timing.
- SOAP operation tagging, fault categorization, namespace-conflict detection, raw envelope-overhead tracking, and fault-code retention on archived events.
- Java HTTP dependency tagging plus helper/codec metadata propagation and raw codec timing retention.
- JMS payload sizing, request/reply timing, message counts, per-action message throughput, message latency, and consumer-lag retention on archived events.
- Goal-oriented SLA parity for server processing and transfer thresholds, including stop-on-breach behavior.
- Previous-run comparison baselines on `GetTest`, used by the live execution summary and dedicated server telemetry panel.

## Current Repo Anchors

Use the existing telemetry surfaces as the first implementation seams instead of inventing parallel plumbing:

- `pkg/protocol/protocol.go` owns `protocol.Result` and the existing `ClientMetrics` contract.
- `pkg/engine/metrics.go` owns live aggregation and is the natural place for protocol-neutral rollups, comparisons, and SLA evaluation.
- `pkg/api/runner.go`, `pkg/api/archive.go`, and `pkg/api/archive_parquet.go` own API-facing retention and archive/report propagation.
- `pkg/distributed/generatorrpc/` and `pkg/api/distributed_execution_backend.go` own generator-to-controller telemetry propagation for distributed runs.
- `dashboard/src/types.ts` plus the execution/archive/comparison components own the frontend telemetry contracts and views.

This plan assumes new protocol telemetry extends those existing surfaces, not a separate telemetry stack.

## What "Parity" Means Concretely

Each protocol must populate enough telemetry for the dashboard to answer the same class of questions:

| Question Class | Browser Answer | Server Protocol Answer |
|:---|:---|:---|
| "Where did time go?" | LCP, FCP, script duration, layout duration | DNS, TCP, TLS, TTFB, server processing, transfer |
| "What's degrading?" | CLS increase, long task growth | TTFB regression, error rate spike, dependency slowdown |
| "Which variant is worst?" | iPhone SE on 4G vs. desktop | Endpoint A vs. B, operation X vs. Y, broker region |
| "Is it the app or infra?" | JS heap growth, blocking resources | DNS spike, TLS renegotiation, connection pool exhaustion |
| "Does it meet SLA?" | LCP < 2.5s, CLS < 0.1 | P95 < 500ms, error rate < 0.1%, throughput > 100 TPS |

## Per-Protocol Gap Analysis And Plan

---

### 1. HTTP Protocol

**Current state:** Most mature. Has DNS, TCP, TLS, TTFB timing phases, server-processing and transfer decomposition, redirect hop timing, request/response payload-size classification, content checks, data formatting, proxy, auth, correlation IDs, dependency classification, run-level server telemetry thresholds, and previous-run baseline comparison. Missing the remaining analytical layer.

**L2 gaps:**

**L3 gaps:**
- No **response time decomposition** dashboard (stacked bar: DNS + TCP + TLS + server + transfer)
- No **dependency health** aggregation (P95 by downstream host)
- No **per-transaction / per-dependency** SLA scoring beyond run-level goal-controller thresholds
- No **per-transaction / per-dependency** baseline breakdown against prior runs

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| Response time decomposition chart in dashboard | L3 | `dashboard/` | 2 days |
| Per-dependency P95/P99 aggregation | L3 | `engine/metrics.go` | 2 days |
| Per-transaction / per-dependency SLA evaluation beyond run goals | L3 | `engine/`, `api/`, `dashboard/` | 2 days |
| Baseline comparison at action / dependency granularity | L3 | `api/`, `dashboard/` | 2 days |

---

### 2. SOAP Protocol

**Current state:** HTTP-backed execution with inherited HTTP timing, SOAP fault parsing, WS-Security, operation tagging, live first-class per-operation aggregation, archive-side operation grouping, archive transaction drill-down filtering by extracted operation, archive-side fault distribution summaries by code and category, archive-side minute fault timelines, archive-side minute payload-size timelines from request/response and envelope telemetry, envelope shaping, generated-body namespace conflict detection, raw envelope-overhead tracking, and fault-code categorization. Missing SOAP-specific analytical depth.

**L3 gaps:**
- No **WSDL-aware operation profiling** (timing by operation name, not just action name)

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| SOAP operation profiling dashboard card | L3 | `dashboard/` | 1.5 days |

---

### 3. Java over HTTP Protocol

**Current state:** Codec-based serialization on top of HTTP transport. The current implementation now retains HTTP-layer request/response payload sizes and size classes plus raw request-side and response-side codec helper timing in the shared `ServerTelemetry` path, but still lacks richer true object-codec analysis.

**L2 gaps:**
- No **true object-vs-wire payload delta** (serialized object size vs. original object footprint)

**L3 gaps:**
- Same as HTTP L3 gaps, plus codec-specific overhead analysis

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| Expose helper-reported object footprint vs. serialized wire size | L2 | `java_http/java_http.go` + helper contract | 1 day |
| Codec overhead chart (serialization % of total time) | L3 | `dashboard/` | 1 day |

---

### 4. WebSocket Protocol (Not Yet Implemented)

**Reference:** [DEVWEB_SUPPORT_PLAN.md](DEVWEB_SUPPORT_PLAN.md) Phase 1 covers execution (L1). This section defines L2 and L3.

**L2 requirements:**
- **Connection lifecycle timing** — handshake time, upgrade latency
- **Message latency** — per-message round-trip time (send → corresponding receive)
- **Throughput** — messages/sec sent, messages/sec received, bytes/sec
- **Backpressure detection** — write buffer depth, send delays
- **Connection stability** — reconnection count, ping/pong latency, close reason codes

**L3 requirements:**
- **Message latency distribution** — P50/P90/P99 per message type
- **Throughput regression** detection — comparison against baseline
- **Connection health scoring** — composite of reconnections + ping latency + backpressure
- **Dead connection** detection — missing pong responses, idle timeout triggers

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| `WebSocketTelemetry` struct on `protocol.Result` | L2 | `protocol.go` | 0.5 days |
| Handshake timing in `ws_connect` | L2 | `websocket/websocket.go` | 0.5 days |
| Per-message send/receive timestamps | L2 | `websocket/websocket.go` | 1 day |
| Write buffer depth + backpressure flag | L2 | `websocket/websocket.go` | 1 day |
| Ping/pong latency tracking | L2 | `websocket/websocket.go` | 0.5 days |
| Connection health score computation | L3 | `engine/metrics.go` | 1.5 days |
| WebSocket-specific dashboard cards | L3 | `dashboard/` | 2 days |
| Throughput regression alerts | L3 | `engine/`, `api/` | 1.5 days |

---

### 5. gRPC Protocol (Not Yet Implemented)

**Reference:** [DEVWEB_SUPPORT_PLAN.md](DEVWEB_SUPPORT_PLAN.md) Phase 2 covers execution (L1). This section defines L2 and L3.

**L2 requirements:**
- **Per-method timing** — unary call latency by service/method
- **Stream throughput** — messages/sec for server-stream, client-stream, bidi
- **Protobuf serialization time** — marshal/unmarshal overhead
- **gRPC status distribution** — OK vs. error codes (UNAVAILABLE, DEADLINE_EXCEEDED, etc.)
- **HTTP/2 multiplexing** — active streams per connection, stream creation overhead
- **Metadata roundtrip** — header/trailer timing

**L3 requirements:**
- **Method hotspot** detection — identify slowest gRPC methods
- **Error classification** — group by gRPC status code, correlate with method
- **Stream efficiency** — messages per stream session, idle stream detection
- **Deadline budget** tracking — how close calls come to their deadline

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| `GRPCTelemetry` struct on `protocol.Result` | L2 | `protocol.go` | 0.5 days |
| Per-method timing via gRPC interceptors | L2 | `grpc/grpc.go` | 1 day |
| Stream message counter + throughput calculation | L2 | `grpc/grpc.go` | 1 day |
| Protobuf marshal/unmarshal timing | L2 | `grpc/grpc.go` | 0.5 days |
| gRPC status code distribution tracking | L2 | `grpc/grpc.go` | 0.5 days |
| Method hotspot detection in aggregation | L3 | `engine/metrics.go` | 1.5 days |
| gRPC-specific dashboard cards | L3 | `dashboard/` | 2 days |
| Deadline budget tracking + alerts | L3 | `engine/`, `api/` | 1 day |

---

### 6. JMS Protocol

**Current state:** Solace and IBM MQ providers, pub/sub/request-reply, message properties, correlation IDs, dependency classification, and raw per-action message-throughput, size-class, message-latency, and consumer-lag retention for request/reply flows. Most complete L1 after HTTP.

**L2 gaps:**
- No **broker-side latency** isolation (time in broker vs. time in network)
- No **queue depth** sampling (requires broker management API, provider-specific)
- No **consumer acknowledgement** latency
- No **message size distribution** beyond raw payload sizes and size classes

**L3 gaps:**
- No **broker health** correlation (latency spike vs. queue depth)
- No **dead-letter** monitoring
- No **consumer lag trend / alerting** across a run
- No **topic fan-out** analysis (message delivered to N subscribers)

**Work items:**

| Item | Layer | Location | Effort |
|:---|:---|:---|:---|
| `JMSTelemetry` struct on `protocol.Result` | L2 | `protocol.go` | 0.5 days |
| Acknowledgement latency timing | L2 | `jms/jms.go` | 0.5 days |
| Message size distribution / histogram views from payload telemetry | L2 | `engine/metrics.go`, `dashboard/` | 1 day |
| Broker health inference from latency patterns | L3 | `engine/metrics.go` | 2 days |
| JMS-specific dashboard cards | L3 | `dashboard/` | 2 days |
| Consumer lag trend / alerting | L3 | `engine/metrics.go`, `dashboard/` | 1 day |
| Dead-letter queue alerting (provider-specific) | L3 | `jms/jms.go`, `api/` | 1.5 days |

---

## Shared Infrastructure: `ServerTelemetry`

Rather than extending `protocol.Result` with multiple per-protocol structs, define a **protocol-neutral server telemetry** struct that complements the existing `ClientMetrics`. The intended anchor is `pkg/protocol/protocol.go`, next to `ClientMetrics` and the rest of `protocol.Result`.

```go
// ServerTelemetry captures protocol-layer performance decomposition.
// Populated by server-side protocols (HTTP, SOAP, gRPC, WebSocket, JMS).
// Nil for browser-only metrics. Complements ClientMetrics.
type ServerTelemetry struct {
    // Timing decomposition (applicable to all request-response protocols)
    ServerProcessingMs float64 `json:"server_processing_ms,omitempty"` // TTFB minus connection setup
    TransferMs         float64 `json:"transfer_ms,omitempty"`          // Duration minus TTFB
    SerializationMs    float64 `json:"serialization_ms,omitempty"`     // Codec encode time
    DeserializationMs  float64 `json:"deserialization_ms,omitempty"`   // Codec decode time

    // Connection reuse
    ConnectionReused bool `json:"connection_reused,omitempty"`
    RedirectCount    int  `json:"redirect_count,omitempty"`
    RedirectMs       float64 `json:"redirect_ms,omitempty"`

    // Payload analysis
    RequestPayloadKB   float64 `json:"request_payload_kb,omitempty"`
    RequestSizeClass   string  `json:"request_size_class,omitempty"`
    ResponsePayloadKB  float64 `json:"response_payload_kb,omitempty"`
    ResponseSizeClass  string  `json:"response_size_class,omitempty"`
    EnvelopeOverheadKB float64 `json:"envelope_overhead_kb,omitempty"` // SOAP XML overhead

    // Protocol-specific status
    GRPCStatusCode   int    `json:"grpc_status_code,omitempty"`
    GRPCStatusName   string `json:"grpc_status_name,omitempty"`
    SOAPFaultCode    string `json:"soap_fault_code,omitempty"`
    SOAPFaultCategory string `json:"soap_fault_category,omitempty"` // client/server/version
    SOAPNamespaceConflict string `json:"soap_namespace_conflict,omitempty"`

    // Messaging (WebSocket, JMS)
    MessagesSent        int     `json:"messages_sent,omitempty"`
    MessagesReceived    int     `json:"messages_received,omitempty"`
    MessagesSentPerSec  float64 `json:"messages_sent_per_sec,omitempty"`
    MessagesReceivedPerSec float64 `json:"messages_received_per_sec,omitempty"`
    MessageLatencyMs    float64 `json:"message_latency_ms,omitempty"`
    ConsumerLagMs       float64 `json:"consumer_lag_ms,omitempty"`
    BackpressureDetected bool `json:"backpressure_detected,omitempty"`
    QueueDepth       int     `json:"queue_depth,omitempty"`

    // Stream (gRPC, WebSocket)
    StreamMessages    int     `json:"stream_messages,omitempty"`
    StreamDurationMs  float64 `json:"stream_duration_ms,omitempty"`
    StreamThroughput  float64 `json:"stream_throughput_msg_s,omitempty"`

    // Connection health (WebSocket, gRPC)
    PingLatencyMs     float64 `json:"ping_latency_ms,omitempty"`
    ReconnectionCount int     `json:"reconnection_count,omitempty"`
}
```

Add `ServerTelemetry *ServerTelemetry` to `protocol.Result` alongside the existing `ClientMetrics *ClientMetrics`. Both are nil when not populated — zero overhead for protocols that don't use them. The same field then needs to propagate through live aggregation, archives, and distributed transaction batches instead of being treated as browser-only metadata.

## Shared Infrastructure: SLA Thresholds

Define per-transaction SLA thresholds in the DSL, evaluated by the engine, protocol-agnostic:

```go
// In dsl/types.go
type SLAThresholds struct {
    P90ResponseMs   float64 `json:"p90_response_ms,omitempty"`
    P95ResponseMs   float64 `json:"p95_response_ms,omitempty"`
    P99ResponseMs   float64 `json:"p99_response_ms,omitempty"`
    MaxErrorRate    float64 `json:"max_error_rate,omitempty"`      // 0.0-1.0
    MinThroughput   float64 `json:"min_throughput_tps,omitempty"`
    MaxTTFBMs       float64 `json:"max_ttfb_ms,omitempty"`        // HTTP/SOAP
    MaxLCPMs        float64 `json:"max_lcp_ms,omitempty"`         // Browser
    MaxCLSScore     float64 `json:"max_cls_score,omitempty"`      // Browser
    MaxConsumerLagMs float64 `json:"max_consumer_lag_ms,omitempty"` // JMS
}
```

This gives every protocol the same SLA evaluation framework that [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md) envisions for Core Web Vitals.

## Shared Infrastructure: Baseline Comparison

Every protocol benefits from run-over-run comparison. This is protocol-agnostic:

- Archive stores per-transaction P50/P90/P99 per completed run
- On new run start, load baseline from most recent run of the same script
- Engine computes delta (current P90 vs. baseline P90)
- Dashboard shows regression/improvement indicators per transaction
- WebSocket `baseline_comparison` event emitted when a metric crosses a configured threshold

**Suggested implementation anchors:** `pkg/engine/metrics.go` for comparison math, `pkg/api/archive.go` plus archive storage for baseline inputs, and `dashboard/` for regression presentation. No dedicated baseline module exists yet in the current repo.

## First Implementation Slice

Before attempting every protocol-specific gap, land one shared end-to-end slice that proves the contract:

1. Extend `protocol.Result` with `ServerTelemetry` in `pkg/protocol/protocol.go`.
2. Populate a minimal HTTP version with `ServerProcessingMs`, `TransferMs`, `ConnectionReused`, and payload sizes.
3. Preserve that data through `pkg/engine/metrics.go`, `pkg/api/runner.go`, `pkg/api/archive.go`, and the distributed generator RPC path.
4. Expose one archive/report view and one live dashboard card that use the shared contract.

That slice validates the model before adding protocol-specific branches for SOAP, JMS, WebSocket, or gRPC.

## Dashboard Parity

The Mobile UX plan defines a device comparison view. Each protocol needs an equivalent "variant comparison":

| Protocol | Comparison Dimension | Dashboard View |
|:---|:---|:---|
| Browser | Device profiles (iPhone SE vs. Galaxy S24) | Side-by-side Core Web Vitals |
| HTTP/SOAP | Endpoints or operations | Side-by-side response time decomposition |
| gRPC | Service methods | Method latency comparison chart |
| WebSocket | Connection scenarios | Throughput + latency comparison |
| JMS | Queues/topics | Consumer lag + throughput comparison |
| All | Current run vs. baseline | Regression indicators per transaction |

Each protocol's dashboard card follows the same layout pattern:
1. **Hero metric** — the single most important number (LCP for browser, P95 for HTTP, consumer lag for JMS)
2. **Decomposition** — stacked bar or waterfall showing where time went
3. **Trend** — sparkline showing metric over the run duration
4. **Health indicator** — green/yellow/red based on SLA thresholds

## Implementation Sequence

### Stage 1: Shared Foundation (All Protocols)
- Add `ServerTelemetry` to `protocol.Result`
- Add `SLAThresholds` to DSL
- Build SLA evaluation in engine (protocol-agnostic)
- Build baseline comparison infrastructure
- **Effort:** 5-7 days
- **Gate:** HTTP protocol populates `ServerTelemetry` and SLA evaluation works end-to-end

### Stage 2: HTTP/SOAP Deep Telemetry
- HTTP: server processing time, transfer time, redirect tracking, connection reuse
- SOAP: operation tagging, envelope overhead, fault classification
- Java HTTP: serialization timing, payload size
- Dashboard: response time decomposition chart
- **Effort:** 5-7 days

### Stage 3: WebSocket + gRPC Deep Telemetry
- Requires L1 implementation from [DEVWEB_SUPPORT_PLAN.md](DEVWEB_SUPPORT_PLAN.md) first
- WebSocket: handshake timing, message latency, throughput, backpressure, connection health
- gRPC: per-method timing, stream throughput, protobuf overhead, status distribution
- **Effort:** 6-8 days

### Stage 4: JMS Deep Telemetry
- Message throughput, acknowledgement latency, consumer lag, message size
- Broker health inference
- **Effort:** 4-5 days

### Stage 5: Dashboard Parity
- Protocol-specific dashboard cards following the shared layout pattern
- Variant comparison views per protocol
- Run-over-run baseline comparison view
- SLA breach indicators
- **Effort:** 7-10 days

### Stage 6: Intelligence Layer (Future)
- Anomaly detection across all protocols (Z-score based)
- Dependency health scoring (HTTP/SOAP/gRPC)
- Connection stability scoring (WebSocket)
- Broker health correlation (JMS)
- Cross-protocol correlation (browser LCP increase caused by API P95 increase)
- **Effort:** Product evolution, not a single phase

## Total Effort Summary

| Stage | Scope | Effort | Dependencies |
|:---|:---|:---|:---|
| **Stage 1** | Shared foundation (ServerTelemetry, SLA, baseline) | 5-7 days | None |
| **Stage 2** | HTTP + SOAP + Java HTTP deep telemetry | 5-7 days | Stage 1 |
| **Stage 3** | WebSocket + gRPC deep telemetry | 6-8 days | Stage 1 + DevWeb L1 |
| **Stage 4** | JMS deep telemetry | 4-5 days | Stage 1 |
| **Stage 5** | Dashboard parity for all protocols | 7-10 days | Stages 2-4 |
| **Stage 6** | Intelligence layer | Ongoing | Stages 1-5 |
| **Total (Stages 1-5)** | | **27-37 days** | |

Stages 2, 3, and 4 are independent of each other and can run in parallel after Stage 1.

## Risks

| Risk | Impact | Mitigation |
|:---|:---|:---|
| `ServerTelemetry` struct grows unbounded | Memory per result increases | Keep it a pointer (nil when unused); benchmark |
| SLA thresholds add config complexity | Script authoring friction | Provide sensible defaults; SLA is optional |
| Baseline comparison needs stable transaction names | Baseline mismatches across script versions | Match by transaction name + protocol; warn on new/removed transactions |
| Protocol-specific dashboard cards multiply UI surface | Dashboard complexity | Use shared card layout pattern; protocol selector filters view |
| JMS broker metrics require provider-specific APIs | Inconsistent coverage across Solace vs. IBM MQ | Start with client-observable metrics only; broker APIs are L3 |

## Relationship To Other Plans

- [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md) defines the three-layer model this plan extends to all protocols.
- [RUNTIME_TELEMETRY_SLA_PLAN.md](RUNTIME_TELEMETRY_SLA_PLAN.md) covers pipeline hardening and live telemetry freshness; this plan adds protocol-specific depth.
- [DEVWEB_SUPPORT_PLAN.md](DEVWEB_SUPPORT_PLAN.md) provides L1 direction for WebSocket and gRPC; this plan adds L2 and L3.
- [PROTOCOL_EXTENSION_PLAN.md](PROTOCOL_EXTENSION_PLAN.md) provides L1 direction for SOAP and Java HTTP; this plan adds L2 and L3.
- [BROWSER_CLIENT_METRICS_PLAN.md](BROWSER_CLIENT_METRICS_PLAN.md) is the most complete existing L2 example; this plan generalizes that depth beyond the browser protocol.
