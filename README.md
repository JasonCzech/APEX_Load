# Project APEX Load

Kubernetes-native performance testing platform replacing LoadRunner Enterprise. APEX Load is a Go-based load testing platform with a deterministic assisted-authoring engine (external LLM integration is policy-gated and not enabled by default), a real-time React dashboard, and a Prometheus/Dynatrace telemetry pipeline.

## What This Is

APEX Load provides **four primary surfaces**:

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
- **Vendor-neutral telemetry**: Prometheus scrape metrics, W3C `traceparent` propagation, Dynatrace event webhooks, and opt-in OTLP traces for controller HTTP requests, test lifecycle/orchestration, sampled engine actions, and controller↔generator gRPC. Metrics and logs are not exported through OpenTelemetry.
- **Streaming percentiles**: T-digest for accurate p95/p99 computation without storing all samples

## Core Components

| Component | Purpose |
|-----------|---------|
| `cmd/controller` | HTTP API server + embedded React dashboard |
| `cmd/apexctl` | CLI for direct script execution (no controller needed) |
| `cmd/apexload-record-agent` | Production local browser recording bridge for remote dashboards |
| `cmd/generator` | Distributed load generator (Kubernetes StatefulSet pod) |
| `cmd/apexload-collector-agent` | Monitoring collector agent (controller heartbeat plus host-metric packs) |
| `cmd/apexload-java-vuser-runner` | Java Vuser runner bridge |
| `cmd/apexbench` | Component benchmark and performance-evidence harness |
| `cmd/dslschema`, `cmd/swaggergen`, `cmd/openapits`, `cmd/webcoverage` | Generators (invoked via `make generate` / `go generate`): DSL schema/reference, `docs/swagger.yaml` + endpoint catalog, dashboard TypeScript API contract, LoadRunner import coverage docs |
| `pkg/engine` | VUser goroutine engine, pacing, metrics, lifecycle |
| `pkg/protocol` | Protocol interface; HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, Kafka, database, network, browser implementations |
| `pkg/dsl` | Script parser and runtime structures |
| `pkg/api` | REST handlers, WebSocket hub, test runner |
| `pkg/convert` | Multi-format import hub (LR/enterprise archives, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, SAP GUI, Java-family imports) |
| `dashboard` | React 18 + TypeScript + Recharts SPA (compiles to `pkg/api/static/`) |
| `pkg/config` | Configuration types and validation |
| `pkg/storage` | SQLite (default) / PostgreSQL persistence layer |

## Documentation

- **[docs/INDEX.md](docs/INDEX.md)** — Canonical documentation map with current/generated/roadmap/historical labels
- **[DEVELOPMENT.md](DEVELOPMENT.md)** — Developer setup, packages overview, frontend architecture, conventions, testing policy, quick-reference cheat-sheet
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — System architecture, data flow diagrams, engine internals, metrics collection, performance characteristics
- **[docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)** — Operator and CI/CD API workflows; generated Swagger/Endpoint Map remain the exhaustive route source
- **[TROUBLESHOOTING.md](TROUBLESHOOTING.md)** — Common issues, debugging techniques, solutions
- **[docs/ENTERPRISE_PLATFORM_RUNBOOK.md](docs/ENTERPRISE_PLATFORM_RUNBOOK.md)** — Current enterprise storage, cache, archive, and telemetry operations
- **[docs/COMPETITIVE_COMPARISON.md](docs/COMPETITIVE_COMPARISON.md)** — How APEX Load compares to LoadRunner/LRE, k6, NeoLoad, JMeter, and DevWeb; honest leads/matches/trails; migration and import reference (RTS mappings, protocol routing, bundle coverage)
- **[docs/PROTOCOL_SUPPORT.md](docs/PROTOCOL_SUPPORT.md)** — Current web/API, browser/mobile, messaging, Java Vuser, and SAP GUI support boundaries
- **[docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md](docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md)** — Self-hosted auth/RBAC operations, break-glass recovery, service-account handling, and audit export guidance
- **[docs/ACCESS_SURFACES.md](docs/ACCESS_SURFACES.md)** — Hosted browser, desktop workspace, CLI, deployment identity, binding defaults, and desktop promotion governance
- **[docs/recording-agent.md](docs/recording-agent.md)** — Production packaging and deployment guidance for the local browser Recording Agent
- **[AGENTS.md](AGENTS.md)** — Canonical agent/platform context

## Requirements

- Go `1.26+`
- Node.js `22.22.0` (pinned by `.nvmrc`, CI, the production builder, and dashboard `engines`)
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

This also starts the generator, Bruno runner, and Java Vuser runner containers
defined in `compose.yaml`. Open the dashboard at `http://127.0.0.1:8080` and Prometheus at
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
# Terminal 1
APEXLOAD_GENERATOR_LISTEN_ADDR=127.0.0.1:9090 go run ./cmd/generator

# Terminal 2
APEXLOAD_GENERATOR_LISTEN_ADDR=127.0.0.1:9091 go run ./cmd/generator
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
# Starts dev server on :4000 with proxy to :8080 for API calls
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

Collection imports that contain credentials use the encrypted protected-import
artifact store. On Windows, place that store on a local fixed NTFS or ReFS
volume. SMB/network shares, removable drives, and other filesystems are rejected
before inspection with an actionable `protected_import_store_unsupported_filesystem`
response. Artifact publication, session-claim transitions, and deletion use
write-through Windows namespace operations; long local paths are supported.

Paths inside uploaded collections and archives are logical `/`-delimited paths
on every platform. Windows directory uploads using `\` separators are normalized
before format routing and multipart construction, while drive-qualified, UNC,
absolute, and traversal paths remain invalid.

## Environment Variables

Controller runtime configuration:

- `APEXLOAD_ADDR`: HTTP bind address. When unset, local/dev defaults to `127.0.0.1:8080`; `APEXLOAD_ENV=production|prod` defaults to `:8080`; explicit values are honored exactly.
- `APEXLOAD_STORE`: metadata store backend: `sqlite` or `postgres` (default `sqlite`)
- `APEXLOAD_DB`: SQLite DB path (default `apexload.db`)
- `APEXLOAD_DATABASE_URL`: PostgreSQL connection URL when `APEXLOAD_STORE=postgres`
- `APEXLOAD_DATABASE_MAX_OPEN_CONNS`, `APEXLOAD_DATABASE_MAX_IDLE_CONNS`, `APEXLOAD_DATABASE_CONN_MAX_LIFETIME`: production PostgreSQL pool tuning
- `APEXLOAD_CONTROLLER_HA`: set `true` (or `1`/`yes`/`on`/`enabled`) before running more than one controller replica; it installs the durable execution-leader lease so only the lease holder runs tests and followers answer `503` on execution routes. When it is unset every replica behaves as its own leader and independently runs the scheduler sweep, queued-run promotion/reconciliation, provisioning recovery, archive finalization recovery, and retention mutations. Replicas must also share PostgreSQL, Redis/Valkey, and the archive backend. See [HA Controller Behavior](docs/ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior)
- `APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION`: how long terminal scenario and execution-plan schedule occurrences are retained (default `2160h`, or 90 days); set `0` to disable pruning. `APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION_INTERVAL` controls the leader-only prune cadence (default `6h`). Each pass removes at most 1,000 rows from each occurrence table.
- `APEXLOAD_GENERATORS`: comma-separated generator gRPC targets; when set, the controller uses the distributed backend. `dns://host:port` entries enable dynamic discovery of a Kubernetes headless service
- `APEXLOAD_GENERATOR_DISCOVERY_INTERVAL`: re-resolve interval for `dns://` generator entries (default `15s`)
- `APEXLOAD_GENERATOR_TIMEOUT`: timeout for controller-to-generator RPCs (default `10s`)
- `APEXLOAD_GENERATOR_LISTEN_ADDR`: generator gRPC bind address (default `:9090`, fallback alias `APEXLOAD_GENERATOR_ADDR`); each local generator process needs a distinct address
- `APEXLOAD_GENERATOR_MAX_VUSERS`: maximum VUsers a single generator process accepts (default `10000`)
- `APEXLOAD_GENERATOR_HEARTBEAT_INTERVAL`: generator heartbeat interval (default `5s`)
- `APEXLOAD_LOG_LEVEL`: structured controller/generator log level: `debug`, `info`, `warn`, or `error` (default `info`; legacy `LOG_LEVEL` is accepted when the prefixed variable is unset)
- `APEXLOAD_GRPC_TLS_CERT`/`APEXLOAD_GRPC_TLS_KEY`/`APEXLOAD_GRPC_TLS_CA`: TLS/mTLS for the controller↔generator gRPC channel (symmetric contract: cert/key = this process's identity, CA = trust for the peer; setting the CA on the generator requires verified client certs). `APEXLOAD_GRPC_TLS_SERVER_NAME` overrides certificate verification when targets are pod IPs from `dns://` discovery
- `APEXLOAD_GRPC_AUTH_TOKEN`: shared bearer token required on every generator RPC — application-layer auth that holds even when the CNI does not enforce NetworkPolicies. When distributed targets are configured in `production`/`prod`, controller and generator startup fail closed unless the channel uses mTLS or verified TLS plus this token; token-only client authentication requires at least 32 bytes (generate 32 random bytes, commonly encoded as 64 hex characters).
- `APEXLOAD_METRICS_ADDR`: when set (e.g. `:9091`), serves `GET /metrics` on a dedicated listener instead of the API port, so network policy can scope Prometheus access separately from API access
- `APEXLOAD_METRICS_TOKEN`: optional bearer token required for `GET /metrics` on either the API port or the dedicated metrics listener
- `APEXLOAD_PPROF_ADDR`: optional unauthenticated Go pprof bind address for controller/generator runtime debugging. In production it must be an explicit loopback address (for example `127.0.0.1:6060`); use port forwarding for remote access
- `APEXLOAD_ENABLE_TLS_COMPAT`: set `true` to force TLS compatibility defaults even in `APEXLOAD_ENV=production|prod`. When enabled, missing `GODEBUG` tokens are appended as `x509negativeserial=1,tlsmlkem=0,tlssecpmlkem=0` to improve compatibility with legacy or enterprise TLS intermediaries.
- `APEXLOAD_DISABLE_TLS_COMPAT`: set `true` to disable automatic TLS compatibility defaults in all environments.
- `APEXLOAD_DISABLE_DEV_TLS_COMPAT`: legacy alias for disabling dev-mode TLS compatibility defaults; `APEXLOAD_DISABLE_TLS_COMPAT` is preferred for new setups.
- `APEXLOAD_PROMETHEUS_URL`: Prometheus base URL used by dashboard graph overlays; independent from the controller `/metrics` scrape endpoint
- `APEXLOAD_OTEL_ENABLED`: explicit opt-in for controller/generator OpenTelemetry tracing (default `false`). Enabling it also requires `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`; see [OpenTelemetry tracing](docs/OBSERVABILITY_INTEGRATIONS.md)
- `OTEL_EXPORTER_OTLP_PROTOCOL` / `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`: OTLP transport, `http/protobuf` (default) or `grpc`; standard OTLP headers, TLS, timeout, compression, service/resource, and sampler variables are honored
- `APEXLOAD_OTEL_ACTION_SAMPLE_RATIO`: independent sampled fraction for high-volume engine action spans (default `0.01`, range `0..1`)
- `APEXLOAD_BROWSER_EXECUTABLE`: path to a system Chromium/Chrome for browser-protocol runs (set to `/usr/bin/google-chrome` in the generator image, where Playwright's managed glibc Chromium cannot run on musl)
- `APEXLOAD_BROWSER_SANDBOX`: set `true` to drop `--no-sandbox` and use Chromium's own sandbox where the environment supports it (unprivileged user namespaces); default keeps `--no-sandbox` because hardened pod securityContexts cannot start the sandbox
- `APEXLOAD_BROWSER_VERSION_SKEW`: set `fail` to fail a browser session whose browser major version differs from the one the bundled Playwright driver was tested with (its `browsers.json`); by default the session runs and reports the mismatch in its first step's evidence and in the run report's Browser Metrics section
- `APEXLOAD_AZURE_KEY_VAULT_URL`: Azure Key Vault for `{{secret.*}}` resolution; `APEXLOAD_AZURE_KEY_VAULT_AUTH` selects `token` (static `APEXLOAD_AZURE_KEY_VAULT_TOKEN`), `azure_identity` (workload/managed identity via DefaultAzureCredential), or `auto` (default); `APEXLOAD_AZURE_KEY_VAULT_SCOPE` overrides the token audience for sovereign clouds
- `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED`: set `true` on the controller only to use Azure Key Vault as the governed writable Project Secrets provider; the controller identity needs `Key Vault Secrets Officer`
- `APEXLOAD_AZURE_KEY_VAULT_API_VERSION`: optional Key Vault data-plane API version for managed Project Secrets (default `7.4`)
- `APEXLOAD_WRAPPING_MASTER_KEY`: stable 32-byte (or base64-encoded 32-byte) protocol-neutral wrapping key for encrypted database-backed Project Secrets and protected source artifacts. Bruno import needs an envelope key to retain its source; when neither this nor `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID` is set, a non-production controller generates a local key beside its import assets (`.bruno-artifact-key`, owner-only) so import works unconfigured. That key is Bruno-scoped — it never enables encrypted database-backed Project Secrets or the shared protected-import flow — and sources retained under it cannot be read after switching to an operator-managed key. `APEXLOAD_ENV=production` (or `APEXLOAD_REQUIRE_AUTH`) requires an operator-managed key instead
- `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID`: optional versioned Azure Key Vault cryptographic key used instead of the local wrapping master key
- `APEXLOAD_ARCHIVE_BACKEND`: archive backend: `filesystem`, `s3`, `azure_blob`, or `gcs` (default `filesystem`)
- `APEXLOAD_ARCHIVE_DIR`: archive root for local runs and staged remote archives (default `<UserCacheDir>/apexload/archives`, falling back to the OS temp directory)
- `APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_CONTAINER`, `APEXLOAD_ARCHIVE_PREFIX`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT`: object archive target metadata for enterprise deployments
- `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL`: optional Prometheus-compatible long-term metrics retention endpoint

See [Project Secrets](docs/ADMIN_USER_GUIDE.md#project-secrets) for provider setup, import
migration governance, and secure distributed delivery requirements.

Note: If you run with `APEXLOAD_ENV=production|prod` or enable `APEXLOAD_REQUIRE_AUTH`, the persisted authentication mode (Settings → Authentication, stored as `auth.mode`) must not be `dev`, `disabled`, or the fixed-credential `local` test-login mode. The controller refuses to start in that combination by design; use `oidc` or `trusted_headers`.

## Build and Test

### Building

Use Make targets from repo root:

```bash
make build       # builds generator, controller, apexctl, apexload-record-agent, and apexload-collector-agent into ./bin
make package-recording-agent  # builds cross-platform Recording Agent release archives
make test        # runs Go tests with race detector
make test-cover  # race + coverage report (coverage.out / coverage.html)
make lint        # runs golangci-lint (if installed)
make generate    # regenerates committed DSL schema/reference and OpenAPI/Swagger artifacts
make desktop     # builds the Wails desktop app (also: make clients, make desktop-preflight)
make java-vuser-runner  # builds the Java Vuser runner jar and bridge binary
make performance-smoke  # runs the apexbench smoke profile (JSON to stdout)
make docker-local-up                 # runs controller + Prometheus (plus generator and runners) locally
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
# Run all Go tests (20-minute package timeout)
go test -timeout=20m ./...

# Run specific package tests
go test ./pkg/engine

# Run with coverage
go test -timeout=20m -cover ./...

# Benchmark performance
go test -run='^$' -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/
# or the apexbench smoke profile
make performance-smoke
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
- `--api-key` / `APEXLOAD_API_KEY` — Service-account API key for secured controller-backed commands
- `--bearer-token` / `APEXLOAD_BEARER_TOKEN` — Alternate bearer-token auth when an API key is not used
- `--api` — Execute through the controller API instead of locally
- `--env, -env` — Runtime environment substitution for `{{env.KEY}}` placeholders
- `--base-url` — Override the script base URL at run time
- `--p95-latency-ms`, `--max-error-pct`, `--max-avg-server-processing-ms`, `--max-avg-transfer-ms`, `--max-error-count` — CI/CD guardrails that make `apexctl` exit non-zero when breached

Additional `run` flags: `--junit` (write a JUnit XML report), `--timeout` (API run wait timeout; default duration + 30m), and `--transaction-rule` / `--transaction-rules-file` (exact per-transaction SLA rules; require `--api`).

**Subcommands** (`apexctl help` prints the full list):

| Command | Purpose |
|---------|---------|
| `run` | Execute a performance test locally or via `--api` |
| `ci` | Run a CI performance gate locally or through a controller |
| `script` | Validate or explain a JSON DSL script |
| `status` | Print a test's current status and SLA verdict (exits 1 on breach/failure) |
| `wait` | Block until a started test finishes, then report the SLA verdict |
| `archive` | Inspect an archive (`verify` checks integrity; `evidence` captures a pinned final result) |
| `record` | Record a browser script with the embedded recorder |
| `record-agent` | Start the localhost browser recording bridge for a remote dashboard |
| `github` | Inspect and run GitHub Enterprise sync/check workflows |
| `import` | Import Bruno collections or Grafana dashboards |
| `library` | Export or import portable project library bundles |
| `version` | Print version information |

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

## API Documentation

The exhaustive REST route inventory and portable parameter/body/response contract are
generated from the controller routing and handler layers.
Do not maintain endpoint catalogs by hand in Markdown.

- Generated OpenAPI: [docs/swagger.yaml](docs/swagger.yaml)
- Generated endpoint catalog: [docs/api-endpoint-catalog.json](docs/api-endpoint-catalog.json)
- Runtime docs endpoints: `GET /api/v1/docs/swagger.yaml` and `GET /api/v1/docs/endpoint-catalog`
- Workflow guide: [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)
- Regeneration (`go generate ./pkg/api`, `go generate ./pkg/dsl`) is described in [DEVELOPMENT.md](DEVELOPMENT.md#api-documentation)

## DSL Script Structure

Scripts are JSON with these sections:

```json
{
  "script": {
    "name": "Products_API",
    "version": "1.0.0",
    "protocol": "http",
    "goal_config": {
      "p95_latency_ms": 800,
      "max_error_pct": 2.5,
      "max_error_count": 10
    },
    "config": {
      "base_url": "https://api.example.com",
      "think_time": { "type": "fixed", "mean_ms": 1000 }
    },
    "data_sources": [
      {
        "name": "users",
        "type": "csv",
        "path": "users.csv",
        "strategy": "sequential",
        "first_row_header": true
      }
    ],
    "init": [
      {
        "name": "login",
        "type": "POST",
        "target": "/auth/login",
        "headers": { "Content-Type": "application/json" },
        "body": "{\"user\": \"{{data.users.email}}\", \"pass\": \"{{data.users.password}}\"}"
      }
    ],
    "actions": [
      {
        "name": "list_products",
        "type": "GET",
        "target": "/products",
        "assertions": [{ "type": "status_code", "expected": 200 }]
      }
    ],
    "teardown": []
  }
}
```

`protocol` lives on the top-level `script` object (not in `config`). HTTP actions use the method as `type` (`GET`, `POST`, ...) with a `target` path relative to `config.base_url`. Data sources use `strategy` (`sequential`, `random`, `unique`, `pop`). See `examples/simple-http.json` for a complete runnable script.

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

See [docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md) and
[docs/dsl.schema.json](docs/dsl.schema.json) for generated DSL syntax.

## Repository Layout

```
cmd/                 # controller, apexctl, generator, apexbench, apexload-collector-agent,
                     # apexload-java-vuser-runner, apexload-record-agent, dslschema,
                     # openapits, swaggergen, webcoverage
pkg/                 # Go packages: engine, protocol, dsl, api, controller, distributed,
                     # storage, recorder, convert, config, and more (see AGENTS.md for the full inventory)

dashboard/           # React 18 + TypeScript frontend
  src/
    components/     # React components (Sidebar, ScriptsPage, TestHub, etc.)
    hooks/         # Custom hooks (useApi, useWebSocket)
    types.ts       # TypeScript interfaces
    main.tsx       # Vite entry point
  vite.config.ts
  package.json

desktop/             # Wails native client shell (embeds controller API + dashboard)
mcp/                 # Python MCP server exposing the REST API as agent tools
deployments/         # Kustomize manifests, Helm chart, Docker Compose, Azure/AWS Terraform, CI templates
examples/            # Sample scripts and HAR files
```

## Development Workflow

### Common Tasks

**Add a new API endpoint:**
1. Define handler in `pkg/api/handlers.go`
2. Register route in `pkg/api/server.go`
3. Add REST tests in the matching `pkg/api/handlers_*_test.go` file for the endpoint's domain
4. Register access requirements in `pkg/api/route_catalog.go` when the route is under `/api/v1`
5. Regenerate API docs with `go generate ./pkg/api`
6. Run `go test ./pkg/api`

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
go test -timeout=20m ./...

# Frontend tests
cd dashboard && npm test

# Coverage
make test-cover
```

## Performance Targets

These are design targets. A reproducible local component harness covers all four target areas and
emits validated JSON/Markdown evidence as described in
[docs/PERFORMANCE_BENCHMARKS.md](docs/PERFORMANCE_BENCHMARKS.md). Its comparisons are
explicitly `not_evaluated`; complete, release-specific target-scope evidence is still required before
any target is cited as achieved.

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
- ✅ Browser client metrics for web-app synthetic UX telemetry are implemented for the current browser contract; WebKit process attribution and broader benchmark publication remain evidence/hardening work.
- 🟡 Messaging protocols are mixed maturity: Kafka and Azure Event Hubs have opt-in real adapters, Solace/Service Bus are build-tag gated, IBM MQ is mock-only with fail-fast real-mode validation, and deeper broker-native parity is tracked in [docs/PROTOCOL_SUPPORT.md](docs/PROTOCOL_SUPPORT.md).
- ✅ GitHub Actions CI/CD gating: controller-native `apexctl ci run`, signed canonical action releases, JUnit/JSON evidence, and active immutable deployment workflows (see [docs/CICD_INTEGRATION.md](docs/CICD_INTEGRATION.md))
- ✅ Evaluator trust pack: one-command demo seeding plus bounded k6 HTTP subset import/export (see [docs/COMPETITIVE_COMPARISON.md](docs/COMPETITIVE_COMPARISON.md#evaluator-trust-pack))
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
- **Runtime telemetry SLA plan**: `docs/ROADMAP.md#runtime-telemetry-sla` — Telemetry capture, streaming, and storage hardening roadmap
- **Prometheus metrics catalog**: `docs/OBSERVABILITY_INTEGRATIONS.md` — Controller scrape series, labels, and telemetry troubleshooting
- **Dynatrace webhook setup**: `docs/OBSERVABILITY_INTEGRATIONS.md` — Dynatrace Events API webhooks, Dynatrace SaaS monitoring, and Elastic (ELK) monitoring
- **OpenTelemetry tracing**: `docs/OBSERVABILITY_INTEGRATIONS.md` — OTLP configuration, span coverage, sampling, propagation, and data-safety behavior
- **Browser and mobile support**: `docs/PROTOCOL_SUPPORT.md` — Current browser canary, mobile emulation, client metrics, and benchmark evidence guidance

## License

[License information here]
