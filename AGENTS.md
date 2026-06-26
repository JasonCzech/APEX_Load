# APEX Load — Enterprise Performance Testing Platform

## What This Is
Kubernetes-native performance testing platform replacing LoadRunner Enterprise.
Go-based load engine using goroutines as virtual users, with a deterministic
assisted-authoring engine (external LLM integration is policy-gated and not wired by
default), a React dashboard, and a Prometheus/Dynatrace telemetry pipeline.

## Architecture Overview
- **Load Generators** (Go): VUsers are goroutines. Autoscaled Kubernetes targets use KEDA (typically min 2, configurable max); non-autoscaled local targets are static. Target: 10k VUsers/pod for HTTP (design target, not yet benchmarked).
- **Test Controller** (Go): Orchestrates tests, distributes VUsers across generators, aggregates metrics via gRPC streams.
- **Assisted-Authoring Engine** (Go): Produces and repairs JSON DSL drafts from OpenAPI specs, HAR files, and recordings using a deterministic-local provider. The MCP server exposes these as agent tools; external LLM providers are governed by policy and not enabled by default.
- **Dashboard** (React 18 + TypeScript + Recharts): Real-time WebSocket-fed monitoring.
- **Telemetry**: Prometheus scrape + W3C `traceparent` propagation + Dynatrace event webhooks. Controller aggregates with t-digest for percentiles. (Full OpenTelemetry span instrumentation is not yet implemented.)
- **Storage**: SQLite by default (`apexload.db`); PostgreSQL is an optional enterprise backend (via pgx). Live state is in-memory with optional Redis/Valkey for HA. Archives are written to the local filesystem; S3/Azure Blob/GCS are optional enterprise backends.
- **Deployment**: Kustomize manifests for local/kind, on-prem, and AKS; Helm chart for enterprise installs; KEDA-driven generator autoscaling; Terraform IaC for the Azure stack. (No operator/CRDs yet.)

## Module Path
`github.com/jasonczech/apexload`

## Project Structure
```
cmd/
  generator/     — Load generator binary (deployed as StatefulSet pods)
  controller/    — Test controller + API server binary (Deployment; HA replicas are opt-in with APEXLOAD_CONTROLLER_HA + shared storage)
  apexctl/       — CLI tool for running/managing tests
  apexload-record-agent/ — Local browser-recording bridge agent (run on operator workstations)
  dslschema/     — Code generator for docs/dsl.schema.json + DSL_ACTION_REFERENCE.md
pkg/
  protocol/      — Protocol plugin interface + registry
  protocol/http/ — HTTP/1.1 + HTTP/2 protocol implementation
  engine/        — VUser goroutine engine, metrics collection, pacing
  dsl/           — JSON DSL parser, type definitions, action catalog
  controller/    — Test orchestration, scheduling, distribution, aggregation
  distributed/   — Generator-side gRPC worker (controller ↔ generator)
  api/           — REST API server, WebSocket hub, test runner, middleware
  api/static/    — Embedded dashboard build output (from dashboard/)
  storage/       — SQLite (default) / PostgreSQL persistence layer
  recorder/      — HTTP recording + HAR conversion + browser recording
  convert/       — Multi-format import hub (LR, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient)
  selector/      — Shared element-selector model + replay self-heal
  scrub/         — PII/credential redaction for artifacts and HAR exports
  refkey/        — Human-readable, environment-aware reference keys
  buildinfo/     — Build metadata (version/commit/build-time)
  config/        — Configuration types
  kubeops/       — In-cluster Kubernetes operations (generator pod restart/drain)
  recordagent/   — Local recording-agent runtime (Playwright/Chromium provisioning + bridge)
api/
  proto/         — gRPC protobuf definitions (controller ↔ generator)
dashboard/       — React 18 + TypeScript + Recharts SPA
  src/components/ — Header, TestHub, TestExecution, StatusBadge, CreateTestModal
  src/hooks/      — useWebSocket (real-time), useApi (REST client)
desktop/         — Wails native client shell (embeds controller API + dashboard)
deployments/
  k8s/           — Kubernetes manifests (Kustomize overlays: local/local-kind/onprem/aks)
  helm/          — Helm chart for Kubernetes/AKS enterprise installs
  docker/        — docker-compose stack for local multi-service runs
  azure/         — Terraform IaC (AKS/ACR/PostgreSQL/Redis/Blob/Key Vault) + App Service launcher
  ci/            — CI pipeline templates (GitHub Actions + Azure Pipelines)
mcp/
  apexload_mcp/  — Python MCP server package (client + tools)
  server.py      — MCP server entrypoint for AI agents
  README.md      — Setup and configuration instructions
```

## Key Design Decisions
- 1 goroutine = 1 VUser (~2–4 KB stack)
- Metrics via buffered channels (cap 10,000) — zero-lock hot path
- Protocol plugins compiled in (not Go runtime plugins)
- `sync.Pool` for request/response buffers to reduce GC pressure
- t-digest for streaming percentile computation
- JSON DSL scripts — no proprietary language
- OpenTelemetry-native metrics, vendor-neutral export
- Real-time Network Latency monitoring via concurrent TCP dialer
- "Soft Pause" execution gate (pauses between actions to preserve transaction integrity)
- Manual timer-based duration management to support robust pause/resume

## Protocol Interface
All protocols implement `pkg/protocol.Protocol`. Register new protocols in `pkg/protocol/registry.go`.
Bundled and compiled into the standard binaries via `pkg/protocol/builtin`:
HTTP (http/1.1, http/2), SOAP (+ WSDL import), Java-over-HTTP (`java_http`, bounded codec MVP),
Java Vuser sidecar bridge (`java_vuser`, external-runner; governed and disabled unless runner config
enables it), WebSocket, gRPC, JMS Solace, JMS IBM MQ, JMS Azure Service Bus (real adapter opt-in via
`-tags jms_servicebus_real`), Kafka (produce/consume/request-reply; in-memory mock broker by default,
real franz-go network adapter opt-in via `-tags kafka_real`), database (SQL query/exec/transactions),
network (DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP), and browser (replay + canary diagnostics). (`sap_gui` actions are
authorable/importable via the DSL catalog but have no registered runtime protocol.)

## Script DSL
JSON-based. Template namespaces: `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`,
`{{param.name}}`, `{{secret.name}}`, plus built-in functions under `{{fn.*}}`/`{{fake.*}}`
(e.g. `fn.uuid()`, `fn.timestamp.unix()`, `fn.random(min,max)`, `fn.random_string(n)`, `fake.email()`).
Sections: config, data_sources, init, actions, teardown.

## REST API
The controller binary serves a REST API on `:8080` (configurable via `APEXLOAD_ADDR`).

```
POST   /api/v1/tests                 — Create and queue a test
GET    /api/v1/tests                 — List all tests
GET    /api/v1/tests/{id}            — Get test status + live metrics
POST   /api/v1/tests/{id}/start      — Start a queued test
POST   /api/v1/tests/{id}/stop       — Graceful stop
POST   /api/v1/tests/{id}/abort      — Immediate abort
POST   /api/v1/tests/{id}/pause      — Pause execution between actions
POST   /api/v1/tests/{id}/resume     — Resume paused test
PATCH  /api/v1/tests/{id}/vusers     — Scale VUsers for a group at runtime
PATCH  /api/v1/tests/{id}/duration   — Extend/shorten test duration at runtime
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
POST   /api/v1/scripts/import/enterprise-script-archive — Upload LR .zip, get converted DSL
GET    /api/v1/system/health         — Health check
WS     /api/v1/ws                    — Global WebSocket (all test events)
```

WebSocket messages are JSON with `type` field: `metrics_update`, `test_created`,
`test_started`, `test_complete`, `test_failed`, `test_stopping`, `test_paused`, 
`test_resumed`, `vusers_changed`, `duration_changed`, `latency_update`.

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
go build ./cmd/apexctl

# Run controller with API server + dashboard
./bin/controller    # serves on :8080

# Run a test via CLI (direct, no controller)
./apexctl run --script order-flow.json --vusers 100 --duration 5m

# Dashboard dev server (proxies /api to :8080)
cd dashboard && npm install && npm run dev   # http://localhost:3000

# Build dashboard into Go binary (embedded)
cd dashboard && npm run build   # outputs to pkg/api/static/

# Run tests
go test ./...
```

## Performance Targets
These are design targets, not yet backed by published benchmark artifacts. Do not cite as achieved.
- Harness overhead per transaction: < 50 μs
- VUsers per pod (HTTP): 10,000 on 4 vCPU / 8 GB
- Metrics latency (generator → dashboard): < 2 seconds
- Ramp-up rate: 500 VUsers/second/pod

## Development Phases
The original week-numbered phasing below is historical; the codebase is well past it (auth/RBAC, scheduling, distributed gRPC execution, governance, and authoring/import are all implemented). Treat it as a roadmap sketch, not current status.
1. Foundation: Single-pod HTTP, basic controller, CLI
2. Distribution & Telemetry: Multi-pod, telemetry, persistence
3. Dashboard MVP: React real-time monitoring
4. Assisted Scripting: deterministic DSL draft/repair (LLM integration policy-gated)
5. JMS & Advanced: JMS protocols, multi-test concurrency
6. Scale & Harden: 50k VUsers, HA, production readiness

## Tech Stack
Go 1.26+ | SQLite (default) / PostgreSQL 16 (optional) | Redis/Valkey (optional, HA) | React 18 + TS | Prometheus + gRPC | AKS | Helm | Terraform
