# Stormrunner — Enterprise Performance Testing Platform

## What This Is
Kubernetes-native performance testing platform replacing LoadRunner Enterprise.
Go-based load engine using goroutines as virtual users, with an agentic (LLM-powered)
script generation engine in Python, React dashboard, and OpenTelemetry telemetry pipeline.

## Architecture Overview
- **Load Generators** (Go): VUsers are goroutines. Pods scale 1–50 via HPA. Target: 10k VUsers/pod for HTTP.
- **Test Controller** (Go): Orchestrates tests, distributes VUsers across generators, aggregates metrics via gRPC streams.
- **Agentic Script Engine** (Python + Azure OpenAI): Generates JSON DSL test scripts from Jira stories, OpenAPI specs, HAR files.
- **Dashboard** (React 18 + TypeScript + Recharts): Real-time WebSocket-fed monitoring.
- **Telemetry**: OpenTelemetry → Prometheus/Dynatrace/ELK. Controller aggregates with t-digest for percentiles.
- **Storage**: PostgreSQL (metadata), Redis (live state), Azure Blob (results/artifacts).
- **Deployment**: AKS with CRDs, K8s operator, Helm charts, Terraform IaC.

## Module Path
`github.com/jasonczech/stormrunner`

## Project Structure
```
cmd/
  generator/     — Load generator binary (deployed as StatefulSet pods)
  controller/    — Test controller + API server binary (Deployment, 2 replicas)
  stormctl/      — CLI tool for running/managing tests
pkg/
  protocol/      — Protocol plugin interface + registry
  protocol/http/ — HTTP/1.1 + HTTP/2 protocol implementation
  engine/        — VUser goroutine engine, metrics collection, pacing
  dsl/           — JSON DSL parser and type definitions
  controller/    — Test orchestration, scheduling, distribution, aggregation
  api/           — REST API server, WebSocket hub, test runner, middleware
  api/static/    — Embedded dashboard build output (from dashboard/)
  config/        — Configuration types
api/
  proto/         — gRPC protobuf definitions (controller ↔ generator)
dashboard/       — React 18 + TypeScript + Recharts SPA
  src/components/ — Header, TestHub, TestExecution, StatusBadge, CreateTestModal
  src/hooks/      — useWebSocket (real-time), useApi (REST client)
deployments/
  k8s/           — Kubernetes manifests, Helm charts
```

## Key Design Decisions
- 1 goroutine = 1 VUser (~2–4 KB stack)
- Metrics via buffered channels (cap 10,000) — zero-lock hot path
- Protocol plugins compiled in (not Go runtime plugins)
- `sync.Pool` for request/response buffers to reduce GC pressure
- t-digest for streaming percentile computation
- JSON DSL scripts — no proprietary language
- OpenTelemetry-native metrics, vendor-neutral export

## Protocol Interface
All protocols implement `pkg/protocol.Protocol`. Register new protocols in `pkg/protocol/registry.go`.
Bundled: HTTP (http/1.1, http/2), JMS Solace, JMS IBM MQ (Phase 5).

## Script DSL
JSON-based. Template variables: `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`, `{{fn.uuid()}}`.
Sections: config, data_sources, init, actions, teardown.

## REST API
The controller binary serves a REST API on `:8080` (configurable via `STORMRUNNER_ADDR`).

```
POST   /api/v1/tests                 — Create and queue a test
GET    /api/v1/tests                 — List all tests
GET    /api/v1/tests/{id}            — Get test status + live metrics
POST   /api/v1/tests/{id}/start      — Start a queued test
POST   /api/v1/tests/{id}/stop       — Graceful stop
POST   /api/v1/tests/{id}/abort      — Immediate abort
GET    /api/v1/tests/{id}/metrics    — Current metrics snapshot
WS     /api/v1/tests/{id}/stream     — WebSocket stream for one test
POST   /api/v1/scripts               — Upload a script (JSON DSL)
GET    /api/v1/scripts               — List scripts
GET    /api/v1/scripts/{id}          — Get script details
PUT    /api/v1/scripts/{id}          — Update a script in-place
DELETE /api/v1/scripts/{id}          — Delete a script
POST   /api/v1/scripts/{id}/validate — Dry-run validation
POST   /api/v1/scripts/{id}/export   — Export script JSON to disk
GET    /api/v1/scripts/{id}/download — Download script as JSON file
POST   /api/v1/scripts/import/loadrunner — Upload LR .zip, get converted DSL
GET    /api/v1/system/health         — Health check
WS     /api/v1/ws                    — Global WebSocket (all test events)
```

WebSocket messages are JSON with `type` field: `metrics_update`, `test_created`,
`test_started`, `test_complete`, `test_failed`, `test_stopping`.

## Testing Policy
Every new feature, bug fix, or refactor **must** include corresponding tests. This is a default
requirement — not optional. When adding or modifying code:
- Add unit tests for new functions and methods in `*_test.go` files alongside the source.
- Cover both the happy path and meaningful edge cases (empty inputs, error conditions, boundary values).
- Run `go test ./...` before considering any Go change complete.
- If a change touches the dashboard, verify UI behavior in a browser (type checking alone is insufficient).
- Test files live next to the code they test (e.g. `pkg/engine/metrics_test.go` tests `metrics.go`).
- Use table-driven tests where multiple input/output combinations exist.
- Do not mock internal packages unless required for isolation; prefer testing against real implementations.

## Build & Run
```bash
# Build all binaries
go build ./cmd/generator
go build ./cmd/controller
go build ./cmd/stormctl

# Run controller with API server + dashboard
./bin/controller    # serves on :8080

# Run a test via CLI (direct, no controller)
./stormctl run --script order-flow.json --vusers 100 --duration 5m

# Dashboard dev server (proxies /api to :8080)
cd dashboard && npm install && npm run dev   # http://localhost:3000

# Build dashboard into Go binary (embedded)
cd dashboard && npm run build   # outputs to pkg/api/static/

# Run tests
go test ./...
```

## Performance Targets
- Harness overhead per transaction: < 50 μs
- VUsers per pod (HTTP): 10,000 on 4 vCPU / 8 GB
- Metrics latency (generator → dashboard): < 2 seconds
- Ramp-up rate: 500 VUsers/second/pod

## Development Phases
1. **Foundation (Wk 1–8)**: Single-pod HTTP, basic controller, CLI — CURRENT
2. Distribution & Telemetry (Wk 9–16): Multi-pod, OTel, persistence
3. Dashboard MVP (Wk 17–22): React real-time monitoring
4. Agentic Scripting (Wk 23–28): LLM script generation
5. JMS & Advanced (Wk 29–36): JMS protocols, multi-test concurrency
6. Scale & Harden (Wk 37–44): 50k VUsers, HA, production readiness

## Tech Stack
Go 1.22+ | PostgreSQL 16 | Redis 7 | React 18 + TS | OpenTelemetry | gRPC | AKS | Helm | Terraform
