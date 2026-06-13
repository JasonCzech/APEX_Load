# APEX Load — Enterprise Performance Testing Platform

## What This Is
Kubernetes-native performance testing platform replacing LoadRunner Enterprise.
Go-based load engine using goroutines as virtual users, with a deterministic
assisted-authoring engine (external LLM integration is policy-gated and not wired by
default), a React dashboard, and a Prometheus/Dynatrace telemetry pipeline.

## Architecture Overview
- **Load Generators** (Go): VUsers are goroutines. Pods scale 1–50 via HPA. Target: 10k VUsers/pod for HTTP (design target, not yet benchmarked).
- **Test Controller** (Go): Orchestrates tests, distributes VUsers across generators, aggregates metrics via gRPC streams.
- **Assisted-Authoring Engine** (Go): Produces and repairs JSON DSL drafts from OpenAPI specs, HAR files, and recordings using a deterministic-local provider. The MCP server exposes these as agent tools; external LLM providers are governed by policy and not enabled by default.
- **Dashboard** (React 18 + TypeScript + Recharts): Real-time WebSocket-fed monitoring.
- **Telemetry**: Prometheus scrape + W3C `traceparent` propagation + Dynatrace event webhooks. Controller aggregates with t-digest for percentiles. (Full OpenTelemetry span instrumentation is not yet implemented.)
- **Storage**: SQLite by default (`apexload.db`); PostgreSQL is an optional enterprise backend (via pgx). Live state is in-memory with optional Redis/Valkey for HA. Archives are written to the local filesystem; S3/Azure Blob/GCS are optional enterprise backends.
- **Deployment**: Kustomize manifests for local/kind (`deployments/k8s/local*`), production on-prem (`deployments/k8s/onprem`: gRPC mTLS+token, TLS ingress, dedicated metrics port, PSA restricted, scoped network policies), and production AKS (`deployments/k8s/aks`: workload identity, KEDA, ingress); Terraform IaC for the Azure stack (`deployments/azure/terraform`: AKS/ACR/PostgreSQL/Redis/Blob/Key Vault). Generator fleet scales via KEDA + `dns://` headless-service discovery. Azure App Service (Node.js/Python-only platforms): thin Node.js launcher in `deployments/azure/appservice/` spawns the pre-built linux/amd64 controller binary; requires PostgreSQL (not SQLite) and static generator targets via `APEXLOAD_GENERATORS`. (No Helm chart or operator/CRDs yet.)

## Module Path
`github.com/jasonczech/apexload`

## Project Structure
```
cmd/
  generator/     — Load generator binary (deployed as StatefulSet pods)
  controller/    — Test controller + API server binary (Deployment, 2 replicas)
  apexctl/       — CLI tool for running/managing tests
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
  convert/       — Multi-format import hub (LR archive, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, …)
  selector/      — Shared element-selector model (CSS/XPath/role/text) + replay self-heal
  scrub/         — PII/credential redaction for recorder artifacts and HAR exports
  refkey/        — Human-readable, environment-aware reference keys
  buildinfo/     — Build metadata (version/commit/build-time) via -ldflags
  config/        — Configuration types
api/
  proto/         — gRPC protobuf definitions (controller ↔ generator)
dashboard/       — React 18 + TypeScript + Recharts SPA
  src/components/ — Header, TestHub, TestExecution, StatusBadge, CreateTestModal
  src/hooks/      — useWebSocket (real-time), useApi (REST client)
desktop/         — Wails native client shell embedding the controller API + dashboard
mcp/             — Python MCP server exposing the REST API as agent tools
deployments/
  k8s/           — Kubernetes manifests, Helm charts
  azure/
    terraform/   — Azure IaC (AKS, ACR, PostgreSQL, Redis, Blob, Key Vault)
    appservice/  — Node.js launcher wrapper for Azure App Service deployments
  ci/            — CI pipeline templates (GitHub Actions + Azure Pipelines) for AKS, on-prem registry, and App Service
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
Bundled and compiled into every binary (see the `_` imports in `cmd/*/main.go`): HTTP (http/1.1,
http/2), SOAP (+ WSDL import), Java-over-HTTP (bounded codec MVP), WebSocket, gRPC, JMS Solace,
JMS IBM MQ, JMS Azure Service Bus (real adapter opt-in via `-tags jms_servicebus_real`),
Kafka (produce/consume/request-reply; in-memory mock broker by default, real
franz-go network adapter opt-in via `-tags kafka_real`), database (SQL query/exec/transactions),
network (DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP),
and browser (replay + canary diagnostics).

## Script DSL
JSON-based. Template variables: `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`, `{{fn.uuid()}}`.
Sections: config, data_sources, init, actions, teardown.
The per-action-type contract is declared in the action catalog (`pkg/dsl/catalog*.go`) — the
single source of truth for validation, the dashboard editor, `GET /api/v1/dsl/actions`, and the
generated artifacts `docs/dsl.schema.json` + `docs/DSL_ACTION_REFERENCE.md`. After changing the
catalog, regenerate with `go generate ./pkg/dsl` (freshness is test-enforced in `pkg/dsl/schemagen`).

## REST API
The controller binary serves a REST API on `:8080` (configurable via `APEXLOAD_ADDR`).

```
POST   /api/v1/tests                 — Create and queue a test
GET    /api/v1/tests                 — List all tests
POST   /api/v1/tests/estimate        — Estimate capacity for a proposed run
POST   /api/v1/tests/preflight       — Pre-flight capacity + governance check (no run created)
GET    /api/v1/tests/{id}            — Get test status + live metrics
GET    /api/v1/tests/{id}/manifest   — Generator assignment manifest for a run
POST   /api/v1/tests/{id}/start      — Start a queued test
POST   /api/v1/tests/{id}/stop       — Graceful stop
POST   /api/v1/tests/{id}/abort      — Immediate abort
PATCH  /api/v1/tests/{id}/goal-targets — Adjust goal-based SLA thresholds at runtime
GET    /api/v1/tests/{id}/metrics    — Current metrics snapshot
GET    /api/v1/tests/{id}/errors/evidence — Live error evidence for a running test
WS     /api/v1/tests/{id}/stream     — WebSocket stream for one test
POST   /api/v1/scripts               — Upload a script (JSON DSL)
GET    /api/v1/scripts               — List scripts
GET    /api/v1/scripts/{id}          — Get script details
PUT    /api/v1/scripts/{id}          — Update a script in-place
DELETE /api/v1/scripts/{id}          — Delete a script
POST   /api/v1/scripts/{id}/validate — Dry-run validation
POST   /api/v1/scripts/{id}/export   — Export script JSON to disk
GET    /api/v1/scripts/{id}/download — Download script as JSON file
POST   /api/v1/browser-recorder/sessions — Launch a headed-Chromium recording (local/desktop controllers; fail-closed in production)
GET    /api/v1/browser-recorder/sessions/{id} — Recording snapshot (steps, markers, counts)
POST   /api/v1/browser-recorder/sessions/{id}/stop · /pause · /resume — Lifecycle control (pause windows are subtracted from think times)
POST   /api/v1/browser-recorder/sessions/{id}/navigate · /focus — Drive the recording browser (http/https only)
POST   /api/v1/browser-recorder/sessions/{id}/transactions/start · /transactions/end — Transaction markers → source_transaction actions
GET    /api/v1/browser-recorder/sessions/{id}/transactions/suggestions — Auto-transaction boundary suggestions from navigations
POST   /api/v1/browser-recorder/sessions/{id}/notes — Note attached to the next recorded action's metadata
POST   /api/v1/browser-recorder/sessions/{id}/section — Switch capture into init/actions/teardown
POST   /api/v1/browser-recorder/sessions/{id}/pick-mode — Toggle the in-page verification element picker
DELETE /api/v1/browser-recorder/sessions/{id}/steps/last — Delete-last-step (mistake recovery)
PATCH  /api/v1/browser-recorder/sessions/{id}/steps/{seq} — Exclude/include or mark a step secret/param
POST   /api/v1/browser-recorder/sessions/{id}/verify — Convert + replay immediately (per-step pass/fail + self-heal)
POST   /api/v1/browser-recorder/sessions/{id}/convert[/preview] — Convert to browser DSL (import envelope + parameterization; trim/split/dual-HTTP options)
GET    /api/v1/browser-recorder/sessions/{id}/export — Side-car HAR (scrub=true for scrub-on-write)
GET    /api/v1/browser-recorder/sessions/{id}/storage-state — Exported Playwright storage state (login reuse)
GET    /api/v1/browser-recorder/devices — Device presets for emulated recording
POST   /api/v1/browser-recorder/install · GET — Provision Playwright/Chromium with status
POST   /api/v1/scripts/import/enterprise-script-archive — Upload LR .zip, get converted DSL
POST   /api/v1/scripts/import/asyncapi — Upload AsyncAPI 2.x/3.x spec, get converted Kafka DSL
GET    /api/v1/scripts/import/asyncapi/support-matrix — AsyncAPI import coverage matrix
POST   /api/v1/scripts/import/parameterize/apply — Apply approved parameterization suggestions to an import draft
POST   /api/v1/scripts/import/github — Pull a script JSON file from GitHub (GHES), get import-review draft
POST   /api/v1/scripts/{id}/github/push — Push a script to GitHub (direct commit or PR); idempotent via link
GET    /api/v1/scripts/{id}/github/status — GitHub link + drift status (local hash vs remote sha)
GET    /api/v1/projects/{id}/github/policy — Project GitHub repository/branch/path policy
PUT    /api/v1/projects/{id}/github/policy — Update project GitHub policy
GET    /api/v1/integrations/github/config — Non-secret global GitHub (GHES) integration config
GET    /api/v1/integrations/github/diagnostics — GHES reachability/auth/install diagnostics
GET    /api/v1/integrations/github/installations — Cached/refreshed GitHub App installations
POST   /api/v1/integrations/github/sync-jobs — Start guarded bulk import/push job
GET    /api/v1/integrations/github/sync-jobs[/{id}] — List/get GitHub sync jobs
POST   /api/v1/integrations/github/webhook — GHES webhook receiver with HMAC validation
GET    /api/v1/integrations/github/webhooks — Webhook delivery processing log
GET    /api/v1/integrations/github/check-links — GitHub check-run links
POST   /api/v1/tests/{id}/github/check-run — Publish/retry APEX Load result as GitHub Check Run
GET    /api/v1/integrations/github/login · /callback — Per-user OAuth web connect flow
GET    /api/v1/integrations/github/connection — Current user's GitHub connection status
DELETE /api/v1/integrations/github/connection — Disconnect + delete the per-user token
GET    /api/v1/integrations/github/repos — List repos the connected user can access
GET    /api/v1/integrations/github/branches?owner=&repo= — List a repo's branches
GET    /api/v1/integrations/github/contents?owner=&repo=&path=&ref= — Browse repo files/dirs
GET    /api/v1/releases/{id}/trend   — Multi-run trend analysis (deltas + regression flags)
POST   /api/v1/scenarios/size-from-goal — Reverse-calculate VUser count from performance goal
GET    /api/v1/run-calendar          — Calendar view of scheduled and recent runs
GET    /api/v1/fleet/provisioner     — Active generator provisioner (static fleet vs on-demand)
GET    /api/v1/fleet/load-zones      — Named self-hosted load zones with capacity envelopes
GET    /api/v1/correlations/rules    — Correlation detection rule catalog
POST   /api/v1/correlations/diagnose — Replay-failure detection + ranked repair suggestions
GET    /api/v1/dsl/actions           — DSL action-type catalog (fields/enums/docs; ?protocol= filter)
GET    /api/v1/system/health         — Health check
GET    /api/v1/system/version        — Controller version info
GET    /api/v1/system/services       — Service health detail
WS     /api/v1/ws                    — Global WebSocket (all test events)
```

WebSocket messages are JSON with `type` field: `metrics_update`, `test_created`,
`test_started`, `test_complete`, `test_failed`, `test_stopping`, plus the
`browser_recording_*` family (started/step/step_update/navigation/warning/paused/
resumed/marker/pick/section/annotation/stopped/failed/converted) emitted by the
headed browser recorder. The recorder also emits the `browser_handle_dialog` DSL
action (alert/confirm/prompt accept/dismiss; replayed in pkg/protocol/browser).

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
Design targets. The harness-overhead target now has a reproducible micro-benchmark
(`docs/BENCHMARK_HARNESS_OVERHEAD.md`, ~8 ns/op metrics hot path); the VUser-density
and ramp targets remain unbenchmarked — do not cite those as achieved.
- Harness overhead per transaction: < 50 μs (metrics hot path benchmarked ≪ target)
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
