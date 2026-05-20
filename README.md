# Project APEX Load

Kubernetes-native performance testing platform replacing LoadRunner Enterprise. APEX Load is a Go-based load testing platform with an agentic script generation engine, real-time React dashboard, and OpenTelemetry telemetry pipeline.

## What This Is

APEX Load provides **three primary surfaces**:

- **Controller API + Dashboard**: Web UI for authoring, scheduling, and monitoring tests with real-time WebSocket-fed metrics
- **CLI (`apexctl`)**: Local script execution for testing during development or in CI/CD pipelines
- **Load Generator**: Kubernetes-native pods (StatefulSet) running goroutine-based virtual users, scalable 1–50 pods with ~10k VUsers/pod

The project is organized to support both direct local runs and controller-managed distributed execution across multiple pods.

## Distributed Execution Today

The current controller can run in either local or distributed mode without changing the HTTP or dashboard surface.

- Local mode: no generator targets configured; the controller executes tests in-process.
- Distributed mode: set `APEXLOAD_GENERATORS=host1:9090,host2:9090,...`; the controller distributes groups across configured generators, aggregates their metrics into one live test view, and stages merged run archives back into the controller archive directory.
- Region-tagged generators: append `|region=<name>` to a configured generator target, for example `APEXLOAD_GENERATORS=us-east-1.example:9090|region=us-east,eu-west-1.example:9090|region=eu-west`.
- Region-weighted placement: test and scenario requests can set `assignment.mode` to `region_weighted` with an `assignment.region_weights` map such as `{ "us-east": 2, "eu-west": 1 }`; the controller applies those weights when placing groups across tagged generators while still preferring healthy capacity.
- Runtime controls: distributed runs support stop, pause, resume, duration adjustment, and per-group VUser scaling through the same `/api/v1/tests/...` endpoints used for local runs.
- Archive behavior: generators produce manifest, JSONL, and parquet artifacts locally; the controller downloads and merges those artifacts so the existing archive/report endpoints continue to work unchanged.

## Key Design Principles

- **Goroutine-based VUsers**: 1 VUser = 1 goroutine (~2–4 KB stack) for extreme concurrency
- **Zero-lock metrics**: Buffered channels (cap 10,000) in hot path; metrics aggregated downstream
- **Protocol plugins**: HTTP/1.1, HTTP/2, SOAP, Java-over-HTTP (bounded codec MVP), JMS (Solace, IBM MQ) — compiled-in, not runtime-loadable
- **JSON DSL**: Human-readable test scripts with template variables and data sources
- **Vendor-neutral telemetry**: OpenTelemetry native; exportable to Prometheus, Dynatrace, ELK
- **Streaming percentiles**: T-digest for accurate p95/p99 computation without storing all samples

## Core Components

| Component | Purpose |
|-----------|---------|
| `cmd/controller` | HTTP API server + embedded React dashboard |
| `cmd/apexctl` | CLI for direct script execution (no controller needed) |
| `cmd/generator` | Distributed load generator (Kubernetes StatefulSet pod) |
| `pkg/engine` | VUser goroutine engine, pacing, metrics, lifecycle |
| `pkg/protocol` | Protocol interface; HTTP, SOAP, Java-over-HTTP, JMS implementations |
| `pkg/dsl` | Script parser and runtime structures |
| `pkg/api` | REST handlers, WebSocket hub, test runner |
| `dashboard` | React 18 + TypeScript + Recharts SPA (compiles to `pkg/api/static/`) |
| `pkg/config` | Configuration types and validation |
| `pkg/storage` | SQLite persistence layer |

## Documentation

- **[QUICK_REFERENCE.md](QUICK_REFERENCE.md)** — Fast lookup: commands, API endpoints, common errors
- **[DEVELOPMENT.md](DEVELOPMENT.md)** — Developer setup, packages overview, frontend architecture, conventions, testing policy
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — System architecture, data flow diagrams, engine internals, metrics collection, performance characteristics
- **[API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md)** — Complete REST API reference, DSL syntax guide, example scripts, template variables
- **[TROUBLESHOOTING.md](TROUBLESHOOTING.md)** — Common issues, debugging techniques, solutions
- **[docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md](docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md)** — Platform parity roadmap for architecture, scale-out, authoring, load modeling, monitoring, reporting, and scheduling
- **[docs/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md](docs/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md)** — Implementation plan for PostgreSQL, object archives, Prometheus long-term metrics, and Redis/Valkey enterprise data services
- **[docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md](docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md)** — Epic backlog and delivery plan to close practical parity gaps with LoadRunner in browser testing, script authoring UX, runtime settings, and auth/RBAC
- **[docs/LOADRUNNER_PROTOCOL_IMPORT_ROUTING.md](docs/LOADRUNNER_PROTOCOL_IMPORT_ROUTING.md)** — Protocol-aware LoadRunner/VuGen ZIP import routing, executable/review modes, and review-artifact API contract
- **[docs/K6_NEOLOAD_PARITY_PLAN.md](docs/K6_NEOLOAD_PARITY_PLAN.md)** — Competitive parity plan for closing APEX Load gaps against Grafana k6 and Tricentis NeoLoad
- **[docs/COMPETITIVE_SUPPORT_MATRIX.md](docs/COMPETITIVE_SUPPORT_MATRIX.md)** — Evidence-backed support matrix for k6, NeoLoad, LoadRunner, JMeter, DevWeb, HAR, and native APEX Load flows
- **[docs/MOBILE_BROWSER_EMULATION_PLAN.md](docs/MOBILE_BROWSER_EMULATION_PLAN.md)** — Current browser engine and device profile support matrix plus conservative mobile-browser benchmark guidance
- **[docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md](docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md)** — Self-hosted auth/RBAC operations, break-glass recovery, service-account handling, and audit export guidance
- **[AGENTS.md](AGENTS.md)** — High-level platform overview, tech stack, release phases

## Requirements

- Go `1.26+`
- Node.js `18+` (recommended for dashboard builds)

## Quick Start

### 1. Start the controller API and dashboard

```bash
go run ./cmd/controller
```

By default the controller listens on `:8080`.

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
# Starts dev server on :5173 with proxy to :8080 for API calls
```

## Environment Variables

Controller runtime configuration:

- `APEXLOAD_ADDR`: HTTP bind address (default `:8080`)
- `APEXLOAD_STORE`: metadata store backend: `sqlite` or `postgres` (default `sqlite`)
- `APEXLOAD_DB`: SQLite DB path (default `apexload.db`)
- `APEXLOAD_DATABASE_URL`: PostgreSQL connection URL when `APEXLOAD_STORE=postgres`
- `APEXLOAD_DATABASE_MAX_OPEN_CONNS`, `APEXLOAD_DATABASE_MAX_IDLE_CONNS`, `APEXLOAD_DATABASE_CONN_MAX_LIFETIME`: production PostgreSQL pool tuning
- `APEXLOAD_GENERATORS`: comma-separated generator gRPC targets; when set, the controller uses the distributed backend
- `APEXLOAD_GENERATOR_TIMEOUT`: timeout for controller-to-generator RPCs (default `10s`)
- `APEXLOAD_ARCHIVE_BACKEND`: archive backend: `filesystem`, `s3`, `azure_blob`, or `gcs` (default `filesystem`)
- `APEXLOAD_ARCHIVE_DIR`: archive root for local runs and staged remote archives (default `artifacts/archives`)
- `APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_CONTAINER`, `APEXLOAD_ARCHIVE_PREFIX`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT`: object archive target metadata for enterprise deployments
- `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL`: optional Prometheus-compatible long-term metrics retention endpoint

## Build and Test

### Building

Use Make targets from repo root:

```bash
make build       # builds generator/controller/apexctl into ./bin
make test        # runs Go tests with race detector
make test-cover  # race + coverage report (coverage.out / coverage.html)
make lint        # runs golangci-lint (if installed)
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
POST   /api/v1/scripts/browser-replay/sessions — Start browser replay for a draft script
POST   /api/v1/scripts/{id}/validate    — Validate script
POST   /api/v1/scripts/{id}/replay      — Replay a saved script
POST   /api/v1/scripts/{id}/browser-replay/sessions — Start browser replay for a saved script
POST   /api/v1/scripts/{id}/export      — Export script JSON to disk
GET    /api/v1/scripts/{id}/download    — Download script JSON
POST   /api/v1/scripts/import/sample    — Build a script from raw HTTP, cURL, REST JSON, or HAR samples
GET    /api/v1/scripts/web-api/support-matrix — Get Web/API support, diagnostics, envelopes, and sample projects
POST   /api/v1/scripts/import/loadrunner — Detect and route a LoadRunner/VuGen ZIP archive to executable script or review artifact
POST   /api/v1/scripts/import/loadrunner/validate — Validate a LoadRunner/VuGen import draft without saving
POST   /api/v1/scripts/import/jmeter    — Convert a JMeter plan to DSL
POST   /api/v1/scripts/import/jmeter/save — Save a reviewed JMeter import draft
POST   /api/v1/scripts/import/jmeter/validate — Validate a JMeter import draft
GET    /api/v1/scripts/import/jmeter/support-matrix — Get JMeter support matrix
POST   /api/v1/scripts/import/devweb   — Convert a DevWeb JavaScript file or archive to DSL
GET    /api/v1/scripts/import/devweb/support-matrix — Get DevWeb construct support matrix
POST   /api/v1/scripts/import/har      — Convert a HAR file to HTTP DSL
POST   /api/v1/scripts/import/openapi  — Convert OpenAPI/Swagger JSON or YAML to HTTP DSL
POST   /api/v1/scripts/import/wsdl     — Convert WSDL to SOAP DSL
```

**Browser Replay:**
```
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
- **Named parameters**: random number, date/time, timestamp, counter, file/list, UUID, and random string generators with `once`, `each_iteration`, and `each_occurrence` update modes
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
  apexctl/          # CLI tool
  generator/         # Distributed load generator

pkg/
  engine/            # Core VUser engine, pacing, metrics
  protocol/          # Protocol interface + HTTP/SOAP/Java-over-HTTP/JMS implementations
  dsl/              # Script parser and types
  api/              # REST handlers, WebSocket, test runner
  controller/       # Test orchestration logic
  storage/          # SQLite persistence
  recorder/         # HTTP recording + HAR conversion
  config/           # Configuration

dashboard/           # React 18 + TypeScript frontend
  src/
    components/     # React components (Sidebar, FolderTree, ScriptsPage, etc.)
    hooks/         # Custom hooks (useApi, useWebSocket)
    types.ts       # TypeScript interfaces
    main.tsx       # Vite entry point
  vite.config.ts
  package.json

deployments/         # Kubernetes manifests, Helm charts
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
- ✅ Capacity-aware generator placement for distributed test startup
- ✅ Generator-loss detection and controlled failure handling during distributed execution
- ✅ Runtime telemetry SLA hardening baseline now includes visible drop counters, bounded live history, end-to-end freshness, sampled telemetry diagnostics for transaction hotspots, schema-versioned typed telemetry retention, distributed telemetry propagation, first-class correlation/dependency metadata, and distributed lag-stage breakdowns from generator capture through controller fan-out
- 🟡 Browser client metrics future plan for web-app synthetic UX telemetry
- 🟡 JMS protocols (Solace, IBM MQ)
- 🟡 Agentic script generation (LLM-powered)

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
