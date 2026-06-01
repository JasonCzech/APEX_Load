# Architecture Overview

## System Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                       Stormrunner Platform                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                  React Dashboard (Browser)                   │   │
│  │  ┌─────────────┬─────────────┬─────────────┬─────────────┐  │   │
│  │  │  Scripts    │  Scenarios  │  Results    │ Monitoring  │  │   │
│  │  └─────────────┴─────────────┴─────────────┴─────────────┘  │   │
│  │         ↓ REST API         ↓ WebSocket Stream               │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              ↓                                        │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │           Controller (Go + Embedded Static Assets)           │   │
│  │                    :8080 HTTP API                            │   │
│  ├──────────────────────────────────────────────────────────────┤   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │   │
│  │  │  REST API    │  │  WebSocket   │  │   Test      │      │   │
│  │  │  Handlers    │  │  Hub         │  │   Runner    │      │   │
│  │  └──────────────┘  └──────────────┘  └──────────────┘      │   │
│  │         ↓                              ↓                    │   │
│  │  ┌──────────────────────────────────────────────────┐       │   │
│  │  │           Test Orchestrator                       │       │   │
│  │  │  - Queue management (queued→running→completed)   │       │   │
│  │  │  - Metrics aggregation                           │       │   │
│  │  │  - Persistence (SQLite)                          │       │   │
│  │  └──────────────────────────────────────────────────┘       │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              ↓                                        │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │       Load Generators (Kubernetes Pods / Local)              │   │
│  │                                                               │   │
│  │  ┌────────────────────────────────────────────────────┐      │   │
│  │  │  Engine Pod 1              Engine Pod 2           │      │   │
│  │  │  ┌──────────────────────┐  ┌──────────────────┐  │      │   │
│  │  │  │  VUser Goroutines    │  │  VUser           │  │      │   │
│  │  │  │  ├─ G1 (user1)       │  │  Goroutines      │  │      │   │
│  │  │  │  ├─ G2 (user2)       │  │  ├─ G101 (user1) │  │      │   │
│  │  │  │  └─ G100 (user100)   │  │  └─ ...          │  │      │   │
│  │  │  └──────────────────────┘  └──────────────────┘  │      │   │
│  │  │         ↓                        ↓                 │      │   │
│  │  │  ┌──────────────────────┐  ┌──────────────────┐  │      │   │
│  │  │  │  Metrics Channels    │  │  Protocol Impl   │  │      │   │
│  │  │  │  - Buffered (10k cap)│  │  (HTTP, JMS)     │  │      │   │
│  │  │  │  - Zero-lock         │  │                  │  │      │   │
│  │  │  └──────────────────────┘  └──────────────────┘  │      │   │
│  │  └────────────────────────────────────────────────────┘      │   │
│  │                         ↓                                    │   │
│  │                    Target System                             │   │
│  │          (API Server, Message Queue, etc.)                   │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
```

## Data Flow During Test Execution

```
1. USER ACTION
   ├─ Create test in Dashboard
   │  └─ POST /api/v1/tests (queue test with script + vusers)
   │
2. CONTROLLER RECEIVES TEST
   ├─ Validates script
   ├─ Stores in SQLite
   ├─ Updates WebSocket clients: "test_created"
   │
3. USER STARTS TEST
   ├─ POST /api/v1/tests/{id}/start
   │
4. TEST RUNNER INITIALIZES
   ├─ Loads script + protocol
   ├─ Creates Engine with Config{VUsers, Duration, RampUp}
   │  └─ Engine spawns N goroutines (1 per VUser)
   │
5. VUSER EXECUTION (per goroutine)
   ├─ Init phase
   │  ├─ Protocol.Init(ctx, config)
   │  ├─ Execute init actions
   │  └─ Extract variables (auth_token, etc.)
   │
   ├─ Ramp-up scheduling
   │  ├─ Pacing algorithm delays goroutine start
   │  ├─ Linear: start at t = (vuser_id / total_vusers) * ramp_up
   │  └─ Result: smooth VUser increase over ramp_up duration
   │
   ├─ Steady-state loop
   │  ├─ For each action in script.actions:
   │  │  ├─ Apply templates ({{data.X}}, {{var.Y}}, {{fn.uuid()}})
   │  │  ├─ Execute action via Protocol.Execute()
   │  │  ├─ Extract values if Extractors defined
   │  │  ├─ Validate assertions
   │  │  ├─ Record metrics: latency, status code, errors
   │  │  │  └─ Send via buffered metrics channel (cap 10,000)
   │  │  └─ Apply think time (delay before next action)
   │  │
   │  └─ Repeat until test duration expires or stop signal received
   │
   ├─ Teardown phase
   │  ├─ Execute teardown actions (logout, cleanup)
   │  └─ Protocol.Teardown(ctx)
   │
   └─ Goroutine exits
      └─ Final metrics sent to metrics channel
      
6. METRICS AGGREGATION (Controller)
   ├─ Reads from metrics channel
   ├─ Updates t-digest percentile algorithm
   ├─ Computes rolling window statistics (last 5s, 60s)
   ├─ Emits via WebSocket: "metrics_update"
   │  └─ Dashboard charts update in real-time
   │
7. TEST COMPLETION
   ├─ All VUsers finish or test duration reached
   ├─ Compute final metrics snapshot
   ├─ Store results in SQLite
   ├─ Emit WebSocket: "test_complete"
   ├─ Dashboard shows: total throughput, error rate, p95/p99 latency
   │
8. RESULTS EXPORT
   ├─ User downloads results as JSON/CSV
   ├─ Compare multiple test runs
   └─ Historical trend analysis
```

## Distributed Controller Path (Current State)

When `STORMRUNNER_GENERATORS` is configured, the controller keeps the same REST and WebSocket contract but swaps test execution to the distributed backend.

```
1. Controller starts with one or more generator gRPC targets.
2. Test start selects distributed execution backend instead of local engine execution.
3. Test groups are assigned across configured generators.
4. Each generator either runs assigned groups through a real `engine.MultiGroupEngine` or uses the synthetic transport path in tests.
5. Generators stream `MetricsBatch` snapshots back to the controller over long-lived gRPC streams.
6. Controller aggregates per-generator batches into one `LiveMetrics` view for the dashboard and REST API.
7. Stop / pause / resume / duration adjustments still flow through the existing `/api/v1/tests/...` endpoints.
8. On completion, generators expose archive artifacts over gRPC and the controller merges them into one controller-local archive bundle.
```

Current distributed implementation details:

- Group fan-out is implemented at the controller backend layer, not by changing the public test model.
- Metrics aggregation happens before publishing `metrics_update`, so the dashboard still sees one test-level stream.
- Group-level scale requests are routed to the owning generator.
- Archive/report handlers remain local-filesystem based; the distributed backend stages remote data into the controller archive root under `remote/<test-id>`.

## Engine Execution Flow

```
┌────────────────────────────────────────┐
│ Engine.Run(ctx context.Context)        │
└────────────┬───────────────────────────┘
             │
             ▼
┌────────────────────────────────────────┐
│ Initialize MetricsCollector            │
│ Start metrics aggregation goroutine    │
└────────────┬───────────────────────────┘
             │
             ▼
┌────────────────────────────────────────┐
│ For each VUser (i = 0 to N-1):         │
│   Calculate scheduled start time       │
│   Launch VUser goroutine               │
│     └─ VUser.Run()                     │
└────────────┬───────────────────────────┘
             │
       ┌─────┴──────┐
       │ Pacing     │
       │ Algorithm  │
       └──────┬─────┘
              │
       ┌──────▼────────────┐
       │ Init phase        │
       │ Execute init[]    │
       │ Extract vars      │
       └──────┬────────────┘
              │
       ┌──────▼────────────┐
       │ Ramp-up delay    │
       │ (sleep until     │
       │  scheduled start) │
       └──────┬────────────┘
              │
       ┌──────▼────────────┐
       │ Steady-state loop │
       │ for action in     │
       │   script.actions[]│
       │ {                 │
       │   Template        │
       │   Execute         │
       │   Extract         │
       │   Assert          │
       │   Record metrics  │
       │   Think time      │
       │ }                 │
       └──────┬────────────┘
              │
       ┌──────▼────────────┐
       │ Teardown phase    │
       │ Execute teardown[]│
       └──────┬────────────┘
              │
       ┌──────▼────────────┐
       │ Signal completion │
       │ Goroutine exit    │
       └─────────┬─────────┘
                 │
    ┌────────────▼──────────────┐
    │ Main goroutine waits for  │
    │ all VUsers to complete    │
    │ or context cancellation   │
    └────────────┬──────────────┘
                 │
    ┌────────────▼──────────────┐
    │ Compute final snapshot    │
    │ Return RunResult          │
    └────────────┬──────────────┘
                 │
                 ▼
            ┌──────────┐
            │ Done     │
            └──────────┘
```

## Metrics Collection Architecture

```
┌──────────────────────────────────────────────────┐
│         VUser Goroutines (1 per user)            │
├──────────────────────────────────────────────────┤
│                                                   │
│  ┌──────────────┐  ┌──────────────┐             │
│  │   G1         │  │   G2         │  ... G100   │
│  │              │  │              │             │
│  │ Execute()    │  │ Execute()    │             │
│  │   ↓          │  │   ↓          │             │
│  │ Record:      │  │ Record:      │             │
│  │ - Latency    │  │ - Latency    │             │
│  │ - Status     │  │ - Status     │             │
│  │ - Errors     │  │ - Errors     │             │
│  └──────┬───────┘  └──────┬───────┘             │
│         │                 │                      │
│         └────────┬────────┘                      │
│                  │                               │
└──────────────────┼───────────────────────────────┘
                   │ Send via buffered channel
                   │ (capacity 10,000)
                   │ (Non-blocking with drop detection)
                   ▼
        ┌──────────────────────────┐
        │ Metrics Channel          │
        │ [Sample, Sample, ...]    │
        │ (FIFO queue)             │
        └──────────────┬───────────┘
                       │
         ┌─────────────┴──────────────┐
         │ Metrics Aggregation        │
         │ Goroutine                  │
         │                            │
         │ Read from channel          │
         │ ↓                          │
         │ Update t-digest            │
         │ (streaming percentiles)    │
         │ ↓                          │
         │ Compute window stats       │
         │ - Last 5s snapshot         │
         │ - Last 60s aggregate       │
         │ - Overall stats            │
         │ ↓                          │
         │ Emit to WebSocket hub      │
         │ Type: metrics_update       │
         │ Every 1-5 seconds          │
         └────────────┬───────────────┘
                      │
        ┌─────────────▼──────────────┐
        │ WebSocket Hub              │
        │ Broadcast to dashboard     │
        │ + other subscribers        │
        └─────────────┬──────────────┘
                      │
        ┌─────────────▼──────────────┐
        │ Dashboard (Browser)        │
        │ Recharts graph updates     │
        │ Real-time metrics display  │
        └────────────────────────────┘
```

## T-Digest Percentile Algorithm

The engine uses t-digest for streaming percentile computation:

```
Why t-digest?
- Memory efficient: O(k) where k ≈ 100 (not O(n) for all samples)
- Streaming: Can compute percentiles on-the-fly without storing all samples
- Accurate: Trade-off between accuracy and memory

How it works:
1. Centroids: Group similar latency values into centroids
2. Merge: As new samples arrive, merge into nearest centroid
3. Compute: Interpolate p50, p95, p99 from centroid distribution

Example:
Latency samples: [10, 15, 20, 25, 30, 100, 105, 110, 1000]

t-digest groups into centroids:
- Centroid 1: ~17 (center, weight 4)
- Centroid 2: ~105 (center, weight 3)
- Centroid 3: ~1000 (center, weight 1)

p50: ~25 (median of distribution)
p95: ~105 (95th percentile)
p99: ~1000 (99th percentile)
```

## Protocol Extension Points

Adding a new protocol (e.g., WebSocket, gRPC):

```
1. Create pkg/protocol/myproto/myproto.go
   ├─ type MyProtocol struct { ... }
   ├─ func (p *MyProtocol) Name() string { return "myproto" }
   ├─ func (p *MyProtocol) Init(ctx, config) error { ... }
   ├─ func (p *MyProtocol) Execute(ctx, action) (*Result, error) { ... }
   ├─ func (p *MyProtocol) Teardown(ctx) error { ... }
   └─ func (p *MyProtocol) HealthCheck(ctx) error { ... }

2. Register in pkg/protocol/registry.go
   └─ RegisterProtocol(new(myproto.MyProtocol))

3. Update DSL parser to accept "myproto" as config.protocol value

4. VUsers automatically use the protocol when script specifies it
```

## Kubernetes Deployment Architecture

```
┌────────────────────────────────────────────────┐
│         Kubernetes Cluster (AKS)               │
├────────────────────────────────────────────────┤
│                                                │
│  ┌─────────────────────────────────────────┐  │
│  │  Controller Deployment (2 replicas)     │  │
│  │  - Pod 1 (Stormrunner controller)      │  │
│  │  - Pod 2 (Stormrunner controller)      │  │
│  │  Services: HTTP :8080                  │  │
│  │  Runtime: local mode or distributed    │  │
│  │  mode via STORMRUNNER_GENERATORS       │  │
│  └─────────────────────────────────────────┘  │
│                  ↓ gRPC assign/control/metrics│
│  ┌─────────────────────────────────────────┐  │
│  │  Generator StatefulSet (1-50 replicas)  │  │
│  │  - Pod generator-0 (10k VUsers)        │  │
│  │  - Pod generator-1 (10k VUsers)        │  │
│  │  - Pod generator-N (up to 50)          │  │
│  │  Auto-scaling via HPA                  │  │
│  │  Exposes assign, control, metrics,     │  │
│  │  heartbeat, and archive artifact RPCs  │  │
│  └─────────────────────────────────────────┘  │
│                                                │
│  ┌─────────────────────────────────────────┐  │
│  │  PostgreSQL (RDS)                       │  │
│  │  - Test metadata                        │  │
│  │  - Script definitions                   │  │
│  │  - Results archive                      │  │
│  └─────────────────────────────────────────┘  │
│                                                │
│  ┌─────────────────────────────────────────┐  │
│  │  Redis (ElastiCache)                    │  │
│  │  - Session state (live tests)           │  │
│  │  - Real-time metrics cache              │  │
│  └─────────────────────────────────────────┘  │
│                                                │
│  ┌─────────────────────────────────────────┐  │
│  │  Azure Blob Storage                     │  │
│  │  - Test results (JSON/Parquet)         │  │
│  │  - HAR recordings                       │  │
│  └─────────────────────────────────────────┘  │
│                                                │
│  ┌─────────────────────────────────────────┐  │
│  │  OpenTelemetry Exporter                │  │
│  │    ↓ Prometheus/Dynatrace/ELK          │  │
│  └─────────────────────────────────────────┘  │
│                                                │
└────────────────────────────────────────────────┘
```

## Performance Characteristics

| Metric | Value | Notes |
|--------|-------|-------|
| VUsers per pod | ~10,000 | HTTP; memory-bound (~2–4 KB per goroutine) |
| Throughput (HTTP) | ~5,000–10,000 req/sec | Per pod; protocol/target dependent |
| Latency overhead | < 50 μs | Harness overhead per transaction |
| Metrics latency | < 2 sec | Metrics collection to dashboard display |
| Ramp-up rate | ~500 VUsers/sec | Smooth scheduling via pacing algorithm |
| Memory per VUser | ~2–4 KB | Goroutine stack + buffers |
| CPU per pod | 4 vCPU | For 10k VUsers at steady load |
| RAM per pod | 8 GB | For 10k VUsers + buffers |
