# Project APEX Load

Kubernetes-native performance testing platform replacing LoadRunner Enterprise. APEX Load is a Go-based load testing platform with a deterministic assisted-authoring engine (external LLM integration is policy-gated and not enabled by default), a real-time React dashboard, and a Prometheus/Dynatrace telemetry pipeline.

## What This Is

APEX Load provides **three primary surfaces**:

- **Hosted browser dashboard**: Zero-install web UI served same-origin by the controller for authoring, scheduling, running, monitoring, and reviewing tests
- **Wails desktop workspace**: Optional installed local workspace with an embedded loopback controller, local SQLite/keyring storage, local runs, and Remote Workspace sync/remote execution
- **CLI (`apexctl`)**: Local or controller-backed script execution for development, automation, and CI/CD pipelines
- **Load Generator**: Kubernetes-native pods (StatefulSet) running goroutine-based virtual users, scalable 1–50 pods (~10k VUsers/pod is a design target, not yet benchmarked)

The project is organized to support both direct local runs and controller-managed distributed execution across multiple pods.

## Distributed Execution Today

The current controller can run in either local or distributed mode without changing the HTTP or dashboard surface.

- Local mode: no generator targets configured; the controller executes tests in-process.
- Distributed mode: set `APEXLOAD_GENERATORS=host1:9090,host2:9090,...`; the controller distributes groups across configured generators, aggregates their metrics into one live test view, and stages merged run archives back into the controller archive directory.
- Region-tagged generators: append `|region=<name>` to a configured generator target, for example `APEXLOAD_GENERATORS=us-east-1.example:9090|region=us-east,eu-west-1.example:9090|region=eu-west`.
- Dynamic fleet discovery: a `dns://host:port` entry (e.g. `APEXLOAD_GENERATORS=dns://apexload-generator.apexload.svc.cluster.local:9090`) makes the controller re-resolve the host on an interval (`APEXLOAD_GENERATOR_DISCOVERY_INTERVAL`, default `15s`) and track one generator per resolved address — generators added or removed by autoscaling (KEDA/HPA) join and leave the fleet without a controller restart. Static and `dns://` entries can be mixed, and metadata suffixes like `|region=...` on a `dns://` entry apply to every generator it discovers.
- Region-weighted placement: test and scenario requests can set `assignment.mode` to `region_weighted` with an `assignment.region_weights` map such as `{ "us-east": 2, "eu-west": 1 }`; the controller applies those weights when placing groups across tagged generators while still preferring healthy capacity.
- Runtime controls: distributed runs support stop, pause, resume, duration adjustment, and per-group VUser scaling through the same `/api/v1/tests/...` endpoints used for local runs.
- Archive behavior: generators produce manifest, JSONL, and parquet artifacts locally; the controller downloads and merges those artifacts so the existing archive/report endpoints continue to work unchanged.

## Key Design Principles

- **Goroutine-based VUsers**: 1 VUser = 1 goroutine (~2–4 KB stack) for extreme concurrency
- **Zero-lock metrics**: Buffered channels (cap 10,000) in hot path; metrics aggregated downstream
- **Protocol plugins**: HTTP/1.1, HTTP/2, SOAP (+ WSDL import), Java-over-HTTP (bounded codec MVP), Java Vuser sidecar bridge (governed and disabled unless runner config enables it), WebSocket, gRPC, JMS (Solace, IBM MQ; real Solace AMQP adapter via `-tags jms_solace_real`), Kafka (produce/consume/request-reply; in-memory mock broker by default, real franz-go adapter via `-tags kafka_real`), database (SQL), network (DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP), browser (replay + canary diagnostics) — compiled-in, not runtime-loadable
- **JSON DSL**: Human-readable test scripts with template variables and data sources
- **Vendor-neutral telemetry**: OpenTelemetry native; exportable to Prometheus, Dynatrace, ELK
- **Streaming percentiles**: T-digest for accurate p95/p99 computation without storing all samples

## Core Components

| Component | Purpose |
|-----------|---------|
| `cmd/controller` | HTTP API server + embedded React dashboard |
| `cmd/apexctl` | CLI for direct script execution (no controller needed) |
| `cmd/apexload-record-agent` | Production local browser recording bridge for remote dashboards |
| `cmd/generator` | Distributed load generator (Kubernetes StatefulSet pod) |
| `pkg/engine` | VUser goroutine engine, pacing, metrics, lifecycle |
| `pkg/protocol` | Protocol interface; HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, Kafka, database, network, browser implementations |
| `pkg/dsl` | Script parser and runtime structures |
| `pkg/api` | REST handlers, WebSocket hub, test runner |
| `pkg/convert` | Multi-format import hub (LR/enterprise archives, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, SAP GUI, Java-family imports) |
| `dashboard` | React 18 + TypeScript + Recharts SPA (compiles to `pkg/api/static/`) |
| `pkg/config` | Configuration types and validation |
| `pkg/storage` | SQLite (default) / PostgreSQL persistence layer |

## Documentation

- **[QUICK_REFERENCE.md](QUICK_REFERENCE.md)** — Fast lookup: commands, API endpoints, common errors
- **[DEVELOPMENT.md](DEVELOPMENT.md)** — Developer setup, packages overview, frontend architecture, conventions, testing policy
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — System architecture, data flow diagrams, engine internals, metrics collection, performance characteristics
- **[API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md)** — Complete REST API reference, DSL syntax guide, example scripts, template variables
- **[TROUBLESHOOTING.md](TROUBLESHOOTING.md)** — Common issues, debugging techniques, solutions
- **[docs/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md](docs/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md)** — Implementation plan for PostgreSQL, object archives, Prometheus long-term metrics, and Redis/Valkey enterprise data services
- **[docs/COMPETITIVE_COMPARISON.md](docs/COMPETITIVE_COMPARISON.md)** — How APEX Load compares to LoadRunner/LRE, k6, NeoLoad, JMeter, and DevWeb; honest leads/matches/trails; migration and import reference (RTS mappings, protocol routing, bundle coverage)
- **[docs/MOBILE_BROWSER_EMULATION_PLAN.md](docs/MOBILE_BROWSER_EMULATION_PLAN.md)** — Current browser engine and device profile support matrix plus conservative mobile-browser benchmark guidance
- **[docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md](docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md)** — Self-hosted auth/RBAC operations, break-glass recovery, service-account handling, and audit export guidance
- **[docs/ACCESS_SURFACES.md](docs/ACCESS_SURFACES.md)** — Hosted browser, desktop workspace, CLI, deployment identity, binding defaults, and desktop promotion governance
- **[docs/recording-agent.md](docs/recording-agent.md)** — Production packaging and deployment guidance for the local browser Recording Agent
- **[AGENTS.md](AGENTS.md)** — High-level platform overview, tech stack, release phases

## Requirements

- Go `1.26+`
- Node.js `18+` (recommended for dashboard builds)
- Windows local utility: Windows PowerShell `5.1+` or PowerShell `7+`

## Quick Start

For end users, the normal zero-install path is the hosted browser dashboard served by the Kubernetes controller. The desktop app is optional when an admin makes it available, and `apexctl` remains the headless/CI path.

### Hosted browser or desktop

- Hosted browser: open the controller URL provided by your environment. The dashboard is served by the controller itself, so no desktop client is required.
- Desktop workspace: install the Wails app when your admin offers it. It runs a local controller for local authoring/runs and can connect to Remote Workspaces for sync and remote execution.
- CLI: use `apexctl` for CI, automation, or direct local script execution.

### Docker Compose with Prometheus

For a local containerized controller plus Prometheus:

```bash
make docker-local-up
```

Open the dashboard at `http://127.0.0.1:8080` and Prometheus at
`http://127.0.0.1:9090`. Prometheus scrapes the controller's dedicated metrics
listener at `http://127.0.0.1:9091/metrics`. See
`deployments/docker/README.md` for target checks, PromQL examples, and the
host-controller variant.

### 1. Start the controller API and dashboard

```bash
go run ./cmd/controller
```

By default a standalone local/dev controller listens on `127.0.0.1:8080`. Production/container deployments set or infer a wide bind (`:8080` or `0.0.0.0:8080`) so Services and port mappings are reachable.

To enable distributed execution, point the controller at one or more generators:

```bash
APEXLOAD_GENERATORS=127.0.0.1:9090,127.0.0.1:9091 \
APEXLOAD_GENERATOR_TIMEOUT=10s \
go run ./cmd/controller
```

Start one generator per target address:

```bash
go run ./cmd/generator
```

### 2. Open the dashboard

Navigate to `http://127.0.0.1:8080`.

### 3. Create and run a test

1. **Create a script**:
   - Go to **Scripts** page
   - Click **New Script**
   - Name: "API Load Test"
  - Paste example from `examples/simple-http.json` or `examples/simple-java-http-embedded.json`
   - Click **Save**

2. **Create a scenario** (test configuration):
   - Go to **Scenarios** page
   - Click **New Scenario**
   - Select your script
   - Set VUsers: 50, Duration: 5 minutes, Ramp-up: 30 seconds
   - Click **Start Test**

Recurring runs can be managed from the same **Scenarios** page by switching to the **Schedules** view, selecting a stored scenario, and saving a five-field cron expression.

3. **Monitor in real-time**:
   - Watch metrics dashboard: throughput, latency (p50/p95/p99), error rate
   - Results available after test completes

### 4. Building dashboard (for development)

```bash
cd dashboard
npm install
npm run build
```

`npm run build` emits static assets to `pkg/api/static/`, which are embedded into the controller binary.

For hot-reload during frontend dev:
```bash
cd dashboard
npm run dev
# Starts dev server on :3000 with proxy to :8080 for API calls
```

### 5. Windows local startup utility

On Windows 11, the local controller and dashboard can be started from one PowerShell entry point:

```powershell
.\scripts\start-local-windows.ps1
```

Useful options:

```powershell
# Start controller + dashboard dev server and open the recorder authoring route
.\scripts\start-local-windows.ps1 -DashboardRoute '#/scripts?mode=create'

# Also set the Windows per-user manual proxy to the recorder proxy while the utility is running
.\scripts\start-local-windows.ps1 -ConfigureRecorderProxy -DashboardRoute '#/scripts?mode=create'

# Restore saved Windows proxy settings without starting APEX Load
.\scripts\start-local-windows.ps1 -RestoreRecorderProxy

# Build dashboard assets and run the controller's embedded dashboard only
.\scripts\start-local-windows.ps1 -Mode Embedded -BuildDashboard
```

`-ConfigureRecorderProxy` saves the current `HKCU` WinINET proxy settings, points HTTP/HTTPS traffic at the local recorder proxy, and restores the saved state when you press Enter to stop the utility.

## Environment Variables

Controller runtime configuration:

- `APEXLOAD_ADDR`: HTTP bind address. When unset, local/dev defaults to `127.0.0.1:8080`; `APEXLOAD_ENV=production|prod` defaults to `:8080`; explicit values are honored exactly.
- `APEXLOAD_STORE`: metadata store backend: `sqlite` or `postgres` (default `sqlite`)
- `APEXLOAD_DB`: SQLite DB path (default `apexload.db`)
- `APEXLOAD_DATABASE_URL`: PostgreSQL connection URL when `APEXLOAD_STORE=postgres`
- `APEXLOAD_DATABASE_MAX_OPEN_CONNS`, `APEXLOAD_DATABASE_MAX_IDLE_CONNS`, `APEXLOAD_DATABASE_CONN_MAX_LIFETIME`: production PostgreSQL pool tuning
- `APEXLOAD_GENERATORS`: comma-separated generator gRPC targets; when set, the controller uses the distributed backend. `dns://host:port` entries enable dynamic discovery of a Kubernetes headless service
- `APEXLOAD_GENERATOR_DISCOVERY_INTERVAL`: re-resolve interval for `dns://` generator entries (default `15s`)
- `APEXLOAD_GENERATOR_TIMEOUT`: timeout for controller-to-generator RPCs (default `10s`)
- `APEXLOAD_GRPC_TLS_CERT`/`APEXLOAD_GRPC_TLS_KEY`/`APEXLOAD_GRPC_TLS_CA`: TLS/mTLS for the controller↔generator gRPC channel (symmetric contract: cert/key = this process's identity, CA = trust for the peer; setting the CA on the generator requires verified client certs). `APEXLOAD_GRPC_TLS_SERVER_NAME` overrides certificate verification when targets are pod IPs from `dns://` discovery
- `APEXLOAD_GRPC_AUTH_TOKEN`: shared bearer token required on every generator RPC — application-layer auth that holds even when the CNI does not enforce NetworkPolicies
- `APEXLOAD_METRICS_ADDR`: when set (e.g. `:9091`), serves `GET /metrics` on a dedicated listener instead of the API port, so network policy can scope Prometheus access separately from API access
- `APEXLOAD_BROWSER_EXECUTABLE`: path to a system Chromium/Chrome for browser-protocol runs (set to `/usr/bin/google-chrome` in the generator image, where Playwright's managed glibc Chromium cannot run on musl)
- `APEXLOAD_BROWSER_SANDBOX`: set `true` to drop `--no-sandbox` and use Chromium's own sandbox where the environment supports it (unprivileged user namespaces); default keeps `--no-sandbox` because hardened pod securityContexts cannot start the sandbox
- `APEXLOAD_AZURE_KEY_VAULT_URL`: Azure Key Vault for `{{secret.*}}` resolution; `APEXLOAD_AZURE_KEY_VAULT_AUTH` selects `token` (static `APEXLOAD_AZURE_KEY_VAULT_TOKEN`), `azure_identity` (workload/managed identity via DefaultAzureCredential), or `auto` (default); `APEXLOAD_AZURE_KEY_VAULT_SCOPE` overrides the token audience for sovereign clouds
- `APEXLOAD_ARCHIVE_BACKEND`: archive backend: `filesystem`, `s3`, `azure_blob`, or `gcs` (default `filesystem`)
- `APEXLOAD_ARCHIVE_DIR`: archive root for local runs and staged remote archives (default `artifacts/archives`)
- `APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_CONTAINER`, `APEXLOAD_ARCHIVE_PREFIX`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT`: object archive target metadata for enterprise deployments
- `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL`: optional Prometheus-compatible long-term metrics retention endpoint

## Build and Test

### Building

Use Make targets from repo root:

```bash
make build       # builds generator/controller/apexctl/apexload-record-agent into ./bin
make package-recording-agent  # builds cross-platform Recording Agent release archives
make test        # runs Go tests with race detector
make test-cover  # race + coverage report (coverage.out / coverage.html)
make lint        # runs golangci-lint (if installed)
make docker-local-up                 # runs controller + Prometheus locally
make docker-local-prometheus-check   # validates the local Prometheus config
```

### Frontend checks

```bash
cd dashboard
npm run lint
npm run test
```

### Running tests

```bash
# Run all Go tests
go test ./...

# Run specific package tests
go test ./pkg/engine

# Run with coverage
go test -cover ./...

# Benchmark performance
go test -bench=BenchmarkEngine ./pkg/engine -benchmem
```

## CLI Usage

### apexctl — Local script execution

Run a DSL script without the controller:

```bash
go run ./cmd/apexctl run \
  --script path/to/script.json \
  --vusers 50 \
  --duration 2m \
  --ramp-up 30s \
  --p95-latency-ms 800 \
  --max-error-pct 2.5
```

**Key flags:**

- `--script, -s` (required) — Path to DSL script file
- `--vusers, -v` (default: 10) — Number of virtual users
- `--duration, -d` (default: 1m) — Total test duration
- `--ramp-up, -r` (default: 0s) — VUser ramp-up period
- `--format` (`text`, `json`) — Human or machine-readable result output
- `--api` — Execute through the controller API instead of locally
- `--env, -env` — Runtime environment substitution for `{{env.KEY}}` placeholders
- `--base-url` — Override the script base URL at run time
- `--p95-latency-ms`, `--max-error-pct`, `--max-avg-server-processing-ms`, `--max-avg-transfer-ms`, `--max-error-count` — CI/CD guardrails that make `apexctl` exit non-zero when breached

**JSON pipeline example:**
```bash
go run ./cmd/apexctl run \
  --api http://localhost:8080 \
  --script path/to/script.json \
  --format json \
  --p95-latency-ms 800 \
  --max-error-pct 2.5
```

**Example output:**
```
=== Test Complete ===
  Duration:     2m5s
  Transactions: 12450
  Errors:       8
  Error Rate:   0.06%
  TPS:          99.6
  SLA:          passing
```

## API Endpoints (Controller)

### Quick Reference

**Tests:**
```
POST   /api/v1/tests                    — Create and queue test
GET    /api/v1/tests                    — List all tests
GET    /api/v1/tests/{id}               — Get test details
GET    /api/v1/tests/{id}/sla-status    — Get summarized SLA pass/fail status
POST   /api/v1/tests/{id}/start         — Start test
POST   /api/v1/tests/{id}/stop          — Graceful stop
POST   /api/v1/tests/{id}/abort         — Immediate abort
WS     /api/v1/ws                       — Global WebSocket stream
```

**Archives:**
```
GET    /api/v1/tests/{id}/archive                    — Download full archive bundle
GET    /api/v1/tests/{id}/archive/browser-diagnostics — Download browser evidence bundle
GET    /api/v1/tests/{id}/archive/report             — Get archive report JSON
GET    /api/v1/tests/{id}/archive/analysis           — Get merged graph + transaction drilldown analysis
GET    /api/v1/tests/{id}/report                     — Get standalone HTML report
```

**Scripts:**
```
POST   /api/v1/scripts                  — Create script
GET    /api/v1/scripts                  — List scripts
GET    /api/v1/scripts/{id}             — Get script
PUT    /api/v1/scripts/{id}             — Update script
DELETE /api/v1/scripts/{id}             — Delete script
PATCH  /api/v1/scripts/{id}/move        — Move script to another folder
POST   /api/v1/scripts/replay           — Replay a draft script
POST   /api/v1/scripts/replay/sessions  — Start a unified replay/debug session for a draft script
POST   /api/v1/scripts/browser-replay/sessions — Start browser replay for a draft script
POST   /api/v1/scripts/{id}/validate    — Validate script
POST   /api/v1/scripts/{id}/replay      — Replay a saved script
POST   /api/v1/scripts/{id}/replay/sessions — Start a unified replay/debug session for a saved script
POST   /api/v1/scripts/{id}/browser-replay/sessions — Start browser replay for a saved script
POST   /api/v1/scripts/{id}/export      — Export script JSON to disk
GET    /api/v1/scripts/{id}/download    — Download script JSON
POST   /api/v1/scripts/import/sample    — Build a script from raw HTTP, cURL, REST JSON, or HAR samples
GET    /api/v1/scripts/web-api/support-matrix — Get Web/API support, diagnostics, envelopes, and sample projects
POST   /api/v1/scripts/import/enterprise-script-archive — Detect and route an enterprise script archive ZIP to executable script, review artifact, or external-runner-required draft
POST   /api/v1/scripts/import/enterprise-script-archive/validate — Validate an enterprise script archive draft without saving
POST   /api/v1/scripts/import/jmeter    — Convert a JMeter plan to DSL
POST   /api/v1/scripts/import/jmeter/save — Save a reviewed JMeter import draft
POST   /api/v1/scripts/import/jmeter/validate — Validate a JMeter import draft
GET    /api/v1/scripts/import/jmeter/support-matrix — Get JMeter support matrix
POST   /api/v1/scripts/import/k6       — Convert a constrained k6 HTTP subset to DSL
POST   /api/v1/scripts/export/k6       — Export HTTP DSL to a reviewable k6 subset
GET    /api/v1/scripts/import/k6/support-matrix — Get k6 subset support matrix
POST   /api/v1/scripts/import/devweb   — Convert a DevWeb JavaScript file or archive to DSL
GET    /api/v1/scripts/import/devweb/support-matrix — Get DevWeb construct support matrix
POST   /api/v1/scripts/import/har      — Convert a HAR file to HTTP DSL
POST   /api/v1/scripts/import/openapi  — Convert OpenAPI/Swagger JSON or YAML to HTTP DSL
POST   /api/v1/scripts/import/wsdl     — Convert WSDL to SOAP DSL
```

**GitHub Enterprise Script Sync:**
```
GET    /api/v1/integrations/github/config       — Safe GitHub capability/config flags
GET    /api/v1/integrations/github/diagnostics  — GHES reachability and auth/readiness checks
GET    /api/v1/integrations/github/installations — GitHub App installation inventory
GET    /api/v1/integrations/github/connection   — Current user's OAuth connection
DELETE /api/v1/integrations/github/connection   — Disconnect current GitHub user
GET    /api/v1/integrations/github/repos        — List accessible repositories
GET    /api/v1/integrations/github/branches     — List repository branches
GET    /api/v1/integrations/github/contents     — Browse repository contents
GET    /api/v1/projects/{id}/github/policy      — Read project GitHub policy
PUT    /api/v1/projects/{id}/github/policy      — Update allowed repos/branches/paths and CI policy
POST   /api/v1/scripts/import/github            — Import one GitHub JSON DSL file
POST   /api/v1/scripts/{id}/github/push         — Push a script via commit or PR
GET    /api/v1/scripts/{id}/github/status       — Link, drift, blob/commit SHA, and check status
POST   /api/v1/integrations/github/sync-jobs    — Start a bulk import/push sync job
GET    /api/v1/integrations/github/sync-jobs    — List GitHub sync jobs
GET    /api/v1/integrations/github/sync-jobs/{id} — Get sync job progress
POST   /api/v1/integrations/github/webhook      — Receive verified GitHub webhooks
GET    /api/v1/integrations/github/webhooks     — List webhook delivery records
POST   /api/v1/tests/{id}/github/check-run      — Publish/retry a GitHub Check Run
GET    /api/v1/integrations/github/check-links  — List run-to-check links
```

**Browser Replay:**
```
GET    /api/v1/replay/sessions/{session_id}               — Get unified replay session state
POST   /api/v1/replay/sessions/{session_id}/pause         — Pause unified replay session
POST   /api/v1/replay/sessions/{session_id}/resume        — Resume unified replay session
POST   /api/v1/replay/sessions/{session_id}/step          — Step unified replay session
POST   /api/v1/replay/sessions/{session_id}/stop          — Stop unified replay session
PATCH  /api/v1/replay/sessions/{session_id}/breakpoints   — Update replay breakpoints
GET    /api/v1/replay/sessions/{session_id}/export        — Export replay evidence JSON or log
GET    /api/v1/browser-replay/sessions/{session_id}        — Get browser replay session state
POST   /api/v1/browser-replay/sessions/{session_id}/pause  — Pause browser replay session
POST   /api/v1/browser-replay/sessions/{session_id}/resume — Resume browser replay session
POST   /api/v1/browser-replay/sessions/{session_id}/stop   — Stop browser replay session
```

**Scenario Schedules:**
```
GET    /api/v1/scenarios/{id}/schedules              — List recurring schedules for a scenario
POST   /api/v1/scenarios/{id}/schedules              — Create a recurring schedule
PUT    /api/v1/scenarios/{id}/schedules/{schedule_id} — Update or pause a schedule
DELETE /api/v1/scenarios/{id}/schedules/{schedule_id} — Delete a recurring schedule
```

**System:**
```
GET    /api/v1/system/health            — Health check
GET    /api/v1/system/readiness         — Production readiness checks
GET    /api/v1/system/inventory         — Controller and generator inventory
GET    /api/v1/fleet/load-zones         — Named self-hosted load zones and capacity signals
GET    /api/v1/system/usage             — Usage accounting rollups
GET    /api/v1/system/parity-status     — Platform and competitive parity status
PATCH  /api/v1/system/generators/drain  — Cordon, drain, or reactivate a generator
```

**CI/CD Templates:**
- GitHub composite action: `.github/actions/apexload-run/action.yml`
- Azure Pipelines template: `azure-pipelines-task.yml`

See [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md) for complete endpoint documentation.

## DSL Script Structure

Scripts are JSON with these sections:

```json
{
  "goal_config": {
    "p95_latency_ms": 800,
    "max_error_pct": 2.5,
    "max_error_count": 10
  },
  "config": {
    "protocol": "http",
    "base_url": "https://api.example.com",
    "timeout_seconds": 30
  },
  "data_sources": [
    {
      "name": "users",
      "type": "csv",
      "path": "users.csv",
      "cycling": "sequential"
    }
  ],
  "init": [
    {
      "name": "login",
      "type": "http",
      "method": "POST",
      "target": "/auth/login",
      "body": "{\"user\": \"{{data.users.email}}\", \"pass\": \"{{data.users.password}}\"}"
    }
  ],
  "actions": [
    {
      "name": "list_products",
      "type": "http",
      "method": "GET",
      "target": "/products"
    }
  ],
  "teardown": []
}
```

**Features:**
- **Template variables**: `{{env.VAR}}`, `{{data.source.column}}`, `{{param.name}}`, `{{var.name}}`, `{{fn.uuid()}}`
- **Scenario-native guardrails**: Optional top-level `goal_config` thresholds are honored by `apexctl run`, with explicit CLI guardrail flags taking precedence when both are present
- **Named parameters**: random number, date/time, timestamp, counter, source-compatible current iteration, host, time, file/list, UUID, and random string generators with `once`, `each_iteration`, and `each_occurrence` update modes
- **Protocol surfaces**: `http`, `soap`, `java_http`, `browser`, and `jms-solace` authoring paths in the dashboard
- **Extractors**: Pull values from responses for use in subsequent requests
- **Assertions**: Validate response status, headers, body content
- **Think time**: Configurable delays between actions (fixed, random, gaussian)
- **Data sources**: CSV or DAT for row-based data-driven testing

**Java-over-HTTP MVP:**
- `protocol: "java_http"` reuses the HTTP transport with a bounded `java_serialized_object_base64` codec.
- Dashboard imports for Java-over-HTTP currently accept raw HTTP and cURL samples whose opaque request bodies are already base64-encoded serialized Java bytes.

See [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md) for complete DSL syntax.

## Repository Layout

```
cmd/
  controller/         # Main API server + dashboard
  apexctl/           # CLI tool
  generator/         # Distributed load generator
  dslschema/         # Generates docs/dsl.schema.json + DSL_ACTION_REFERENCE.md

pkg/
  engine/            # Core VUser engine, pacing, metrics
  protocol/          # Protocol interface + HTTP/SOAP/Java-over-HTTP/WebSocket/gRPC/JMS/Kafka/database/network/browser implementations
  dsl/               # Script parser, types, action catalog
  api/               # REST handlers, WebSocket, test runner
  controller/        # Test orchestration logic
  distributed/       # Generator-side gRPC worker
  storage/           # SQLite (default) / PostgreSQL persistence
  recorder/          # HTTP recording + HAR conversion + browser recording
  convert/           # Multi-format import hub (LR, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, SAP GUI, Java-family imports)
  selector/          # Shared element-selector model + replay self-heal
  scrub/             # PII/credential redaction for artifacts and HAR exports
  refkey/            # Human-readable reference keys
  buildinfo/         # Build metadata (version/commit/build-time)
  config/            # Configuration

dashboard/           # React 18 + TypeScript frontend
  src/
    components/     # React components (Sidebar, FolderTree, ScriptsPage, etc.)
    hooks/         # Custom hooks (useApi, useWebSocket)
    types.ts       # TypeScript interfaces
    main.tsx       # Vite entry point
  vite.config.ts
  package.json

desktop/             # Wails native client shell (embeds controller API + dashboard)
mcp/                 # Python MCP server exposing the REST API as agent tools
deployments/         # Kustomize manifests, Helm chart, Docker Compose, Azure Terraform/IaC, CI templates
examples/            # Sample scripts and HAR files
```

## Development Workflow

### Common Tasks

**Add a new API endpoint:**
1. Define handler in `pkg/api/handlers.go`
2. Register route in `pkg/api/server.go`
3. Add REST tests in `pkg/api/handlers_test.go`
4. Update [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md)

**Add a new protocol:**
1. Create `pkg/protocol/myproto/myproto.go`
2. Implement `protocol.Protocol` interface
3. Register in `pkg/protocol/registry.go`
4. Test with sample scripts

**Update frontend:**
1. Edit files in `dashboard/src/`
2. If using dev server: changes hot-reload
3. If serving from controller: `npm run build`, restart controller

**Run tests:**
```bash
# Go tests
go test ./...

# Frontend tests
cd dashboard && npm test

# Coverage
make test-cover
```

## Performance Targets

These are design targets, not yet backed by published benchmark artifacts. Do not cite as achieved.

| Metric | Target | Notes |
|--------|--------|-------|
| VUsers per pod | 10,000 | Memory-limited (~2–4 KB per goroutine) |
| Throughput | 5,000–10,000 req/sec | Per pod; protocol/target dependent |
| Latency overhead | < 50 μs | Harness overhead per transaction |
| Metrics latency | < 2 sec | Collection to dashboard display |
| Ramp-up rate | 500 VUsers/sec | Smooth scheduling |
| Max pods | 50 | Total platform: 500k VUsers |

## Project Status

- ✅ Controller + dashboard fully functional
- ✅ Local engine (apexctl) working
- ✅ HTTP protocol family complete (HTTP, SOAP, Java-over-HTTP MVP)
- ✅ DSL parser and templating
- ✅ Real-time metrics and WebSocket streaming
- ✅ Distributed execution backend with multi-generator group fan-out, live controls, merged metrics, and staged archives
- ✅ Capacity-aware generator placement for distributed test startup, including self-hosted named load-zone summaries (`/api/v1/fleet/load-zones`)
- ✅ Generator-loss detection and controlled failure handling during distributed execution
- ✅ Runtime telemetry SLA hardening baseline now includes visible drop counters, bounded live history, end-to-end freshness, sampled telemetry diagnostics for transaction hotspots, schema-versioned typed telemetry retention, distributed telemetry propagation, first-class correlation/dependency metadata, and distributed lag-stage breakdowns from generator capture through controller fan-out
- 🟡 Browser client metrics future plan for web-app synthetic UX telemetry
- 🟡 JMS protocols (Solace, IBM MQ)
- ✅ CI/CD gating: `apexctl` JUnit output + `status`/`wait` subcommands, local `apexctl script validate/explain`, with GitHub/Azure DevOps/Jenkins templates (see [docs/CICD_INTEGRATION.md](docs/CICD_INTEGRATION.md))
- ✅ Evaluator trust pack: one-command demo seeding plus bounded k6 HTTP subset import/export (see [docs/EVALUATOR_TRUST_PACK.md](docs/EVALUATOR_TRUST_PACK.md))
- ✅ Correlation rules engine + replay-failure detection, response-family rule packs, stale-literal evidence, and guided/batch repair (`/api/v1/correlations/diagnose`)
- ✅ Assisted authoring (deterministic-local draft/repair/explain over the DSL, via MCP)
- 🟡 External LLM script generation (policy-gated; not wired by default)

## Contributing

See [DEVELOPMENT.md](DEVELOPMENT.md) for coding conventions, testing policy, and setup instructions.

**All new features and bug fixes must include corresponding tests.**

## References

- **Go docs**: `go doc ./pkg/engine` — Engine package documentation
- **Protocol interface**: `pkg/protocol/protocol.go` — All protocols implement this
- **DSL types**: `pkg/dsl/types.go` — Script structures
- **API handlers**: `pkg/api/handlers.go` — Endpoint implementations
- **Runtime telemetry SLA plan**: `docs/RUNTIME_TELEMETRY_SLA_PLAN.md` — Telemetry capture, streaming, and storage hardening roadmap
- **Future browser client metrics plan**: `docs/BROWSER_CLIENT_METRICS_PLAN.md` — Browser-side client metrics roadmap and tradeoffs

## License

[License information here]
