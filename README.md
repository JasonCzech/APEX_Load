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

- **Goroutine-based VUsers**: 1 VUser = 1 goroutine, parked on an 8 KB stack between actions, for extreme concurrency
- **Zero-lock metrics**: Buffered channels (cap 10,000) in hot path; metrics aggregated downstream
- **Protocol plugins**: HTTP/1.1, HTTP/2, HTTP/3, SOAP (+ WSDL import), Java-over-HTTP (bounded codec MVP), Java Vuser sidecar bridge (governed and disabled unless runner config enables it), WebSocket, gRPC, JMS (Solace, IBM MQ, Azure Service Bus; real adapters opt-in via `-tags jms_solace_real` / `-tags jms_servicebus_real`), Kafka (produce/consume/request-reply; in-memory mock broker by default, real franz-go adapter via `-tags kafka_real`), Azure Event Hubs (native `eventhubs`, real AMQP adapter via `-tags eventhubs_real`), database (SQL), network (DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP), browser (replay + canary diagnostics), and Appium (mobile device observer) — compiled-in, not runtime-loadable
- **JSON DSL**: Human-readable test scripts with template variables and data sources
- **Vendor-neutral telemetry**: Prometheus scrape metrics, W3C `traceparent` propagation, Dynatrace event webhooks, and opt-in OTLP traces for controller HTTP requests, test lifecycle/orchestration, sampled engine actions, and controller↔generator gRPC. Metrics and logs are not exported through OpenTelemetry.
- **Streaming percentiles**: a mergeable duration sketch keeps every percentile within 0.5% of the value at its rank without storing all samples

## Core Components

| Component | Purpose |
|-----------|---------|
| `cmd/controller` | HTTP API server + embedded React dashboard |
| `cmd/apexctl` | CLI for local or controller-backed execution, imports, diagnostics, and CI gates |
| `cmd/apexload-record-agent` | Production local browser recording bridge for remote dashboards |
| `cmd/generator` | Distributed load generator (Kubernetes StatefulSet pod or cloud async worker) |
| `cmd/apexload-collector-agent` | Monitoring collector agent (controller heartbeat plus host-metric packs) |
| `cmd/apexload-java-vuser-runner` | Java Vuser runner bridge (`java_vuser` external-runner container) |
| `cmd/apexbench` | Component benchmark and performance-evidence harness |
| `cmd/dslschema`, `cmd/swaggergen`, `cmd/openapits`, `cmd/datamodeldoc`, `cmd/webcoverage`, `cmd/storeoverlay` | Code generators (invoked via `make generate` / `go generate ./...`): DSL schema/reference, `docs/swagger.yaml` + endpoint catalog, dashboard TypeScript API contract, `docs/DATA_MODEL.md`, `docs/LOADRUNNER_COVERAGE.md`, and `pkg/api/store_overlay_test.go` |
| `pkg/engine` | VUser goroutine engine, pacing, metrics, lifecycle |
| `pkg/protocol` | Protocol interface + builtin HTTP (1.1/2/3), SOAP, Java-over-HTTP, Java Vuser, WebSocket, gRPC, JMS, Kafka, Event Hubs, database, network, browser, and Appium implementations |
| `pkg/dsl` | Script parser, action catalog, and runtime structures |
| `pkg/api` | REST handlers, WebSocket hub, test runner, middleware |
| `pkg/convert` | Multi-format import hub (LR/enterprise archives, native Bruno HTTP collections, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, SAP GUI, Java-family imports) |
| `dashboard` | React 18 + TypeScript + Recharts SPA (compiles to `pkg/api/static/`) |
| `desktop` | Wails v2 native client shell embedding a loopback controller + dashboard |
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

- Go `1.26.10+`
- Node.js `22.23.3` (pinned by `.nvmrc`, CI, the production builder, and dashboard `engines`)
- Windows local utility: Windows PowerShell `5.1+` or PowerShell `7+`

## Quick Start

For end users, the normal zero-install path is the hosted browser dashboard served by the Kubernetes controller. The desktop app is optional when an admin makes it available, and `apexctl` remains the headless/CI path.

### Hosted browser or desktop

- Hosted browser: open the controller URL provided by your environment. The dashboard is served by the controller itself, so no desktop client is required.
- Desktop workspace: install the Wails app when your admin offers it. It runs a local controller for local authoring/runs and can connect to Remote Workspaces for sync and remote execution.
- CLI: use `apexctl` for CI, automation, or direct local script execution.

### Docker Compose with Prometheus

For a local containerized controller, generator, and Prometheus stack:

```bash
make docker-local-up
```

This starts the `controller`, `generator`, and `prometheus` services from `compose.yaml` (plus the one-shot `bruno-artifacts-init` permissions init container for native Bruno HTTP import storage). The optional `java-vuser-runner` sidecar is gated behind the `java-vuser` Compose profile (`docker compose --profile java-vuser up -d`). Bruno Runner is retired under `_retired/bruno-runner/` and excluded from builds, while native Bruno HTTP collection import remains built in. Open the dashboard at `http://127.0.0.1:8080` and Prometheus at `http://127.0.0.1:9090`. Prometheus scrapes the controller's dedicated metrics listener at `http://127.0.0.1:9091/metrics`. See [deployments/docker/README.md](deployments/docker/README.md) for target checks, PromQL examples, and the host-controller variant.

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

Controller and generator runtime configuration grouped by domain:

### Core Controller, Bind & Storage

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_ENV` | unset (dev) | Set `production` or `prod` to enforce production bind, fail-closed gRPC security, non-bypass authentication, and operator-managed wrapping keys. |
| `APEXLOAD_ADDR` | `127.0.0.1:8080` (dev), `:8080` (`production`/`prod`) | Controller HTTP bind address; explicit values are honored verbatim. |
| `APEXLOAD_STORE` | `sqlite` | Metadata store backend: `sqlite` or `postgres`. |
| `APEXLOAD_DB` | `apexload.db` | SQLite database file path when `APEXLOAD_STORE=sqlite`. |
| `APEXLOAD_DATABASE_URL` | — | PostgreSQL connection URL when `APEXLOAD_STORE=postgres`. |
| `APEXLOAD_DATABASE_MAX_OPEN_CONNS`, `APEXLOAD_DATABASE_MAX_IDLE_CONNS`, `APEXLOAD_DATABASE_CONN_MAX_LIFETIME` | driver defaults | PostgreSQL connection-pool tuning. |
| `APEXLOAD_LOG_LEVEL` | `info` | Structured JSON `slog` level (`debug`, `info`, `warn`, `error`; falls back to `LOG_LEVEL`). |
| `APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION` / `APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION_INTERVAL` | `2160h` (90d) / `6h` | Terminal schedule-occurrence retention window (`0` disables pruning) and leader-only sweep cadence (max 1,000 rows/table/pass). |

### HA Cache / Redis, Active EndpointSlice & Evacuation

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_CONTROLLER_HA` | `false` | Enables durable PostgreSQL execution-leader election so only the lease holder runs tests, sweeps schedules, and reconciles runs while followers return `503` on execution routes. Requires `APEXLOAD_STORE=postgres`, shared Redis/Valkey, and a shared archive backend. See [HA Controller Behavior](docs/ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior). |
| `APEXLOAD_CACHE_BACKEND` | `memory` | Live-run state and WebSocket fan-out backend: `memory`, `redis`, or `valkey` (`redis`/`valkey` required for multi-replica HA and cloud PaaS controllers). |
| `APEXLOAD_REDIS_URL` | — | Redis/Valkey connection URL (`redis://` or `rediss://`) required when `APEXLOAD_CACHE_BACKEND=redis` or `valkey`. |
| `APEXLOAD_REDIS_TLS` | `false` (or inferred from `rediss://`) | Forces TLS on the Redis/Valkey transport even when the URL uses `redis://`. |
| `APEXLOAD_ACTIVE_ENDPOINT_SLICE` | — | Kubernetes `EndpointSlice` name updated by the active HA controller lease holder (`requires APEXLOAD_CONTROLLER_HA=true`). |
| `APEXLOAD_ACTIVE_ENDPOINT_SERVICE` | defaults to `APEXLOAD_ACTIVE_ENDPOINT_SLICE` | Kubernetes `Service` label owner bound to the published active-leader `EndpointSlice`. |
| `APEXLOAD_ACTIVE_ENDPOINT_CA_FILE` / `APEXLOAD_ACTIVE_ENDPOINT_TOKEN_FILE` | in-cluster SA paths | Optional CA bundle and projected service-account token file overrides for `EndpointSlice` publication. |
| `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY` | `false` | Enables version-4 evacuation journals so an HA successor can roll an interrupted pre-source-stop generator evacuation back to its prior generators for `continue`-policy runs. Enable only once all controller replicas support v4 journals. Participants reporting `RUN_TERMINAL_CAUSE_DURATION_ELAPSED` with `CompletedDuration >= expected` are trusted as natural completions and allow recovery to finish; `UNSPECIFIED`, short (`CompletedDuration < expected`), early-ended, or controller-stopped outcomes hold the run `STOPPING` until fenced or attested via `POST /api/v1/tests/{id}/interrupted-stop/resolve`. |
| `APEXLOAD_EVACUATION_ROLLFORWARD_RECOVERY` | `false` | Together with `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY`, enables version-5 journals so an HA successor can adopt surviving and replacement generators, apply the recorded target duration, commit replacement capacity, and finish post-source-stop evacuations. As with rollback, natural completions (`RUN_TERMINAL_CAUSE_DURATION_ELAPSED` with `CompletedDuration >= expected`) are trusted and finish recovery, whereas `UNSPECIFIED`, short, early-ended, or controller-stopped outcomes hold `STOPPING`. |
| `APEXLOAD_EVACUATION_REPLACEMENT_RESTART` | `false` | Together with rollback and roll-forward recovery, enables version-6 journals so an HA successor can prove neither source nor replacement runs an interrupted stateless HTTP share and restart that share on spare generator capacity within its recorded remaining duration. |

### Distributed Fleet, gRPC Security, Reverse Gateway & Generator Supervision

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_GENERATORS` | — | Comma-separated generator gRPC targets (`host:port|region=<name>` or `dns://host:port` for dynamic headless-service discovery). |
| `APEXLOAD_GENERATOR_DISCOVERY_INTERVAL` | `15s` | Re-resolution cadence for `dns://` generator targets. |
| `APEXLOAD_GENERATOR_TIMEOUT` | `10s` | Controller-to-generator RPC timeout. |
| `APEXLOAD_GENERATOR_LISTEN_ADDR` | `:9090` | Generator gRPC bind address (fallback alias `APEXLOAD_GENERATOR_ADDR`). |
| `APEXLOAD_GENERATOR_HEALTH_ADDR` | — | Optional dedicated generator health gRPC/HTTP listener address so Kubernetes probes stay isolated from mTLS control traffic. |
| `APEXLOAD_GENERATOR_MAX_VUSERS` | `10000` | Maximum concurrent VUsers accepted by one generator process. |
| `APEXLOAD_GENERATOR_HEARTBEAT_INTERVAL` | `5s` | Generator heartbeat interval. |
| `APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL` / `APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL` | `60s` (`30s..60s`) / `10s` (`1s..TTL/3`) | Lease TTL and renewal cadence for generator-side run supervision fencing. |
| `APEXLOAD_GRPC_TLS_CERT`, `APEXLOAD_GRPC_TLS_KEY`, `APEXLOAD_GRPC_TLS_CA`, `APEXLOAD_GRPC_TLS_SERVER_NAME` | — | Symmetric TLS/mTLS configuration for controller↔generator gRPC (setting `APEXLOAD_GRPC_TLS_CA` on the generator enforces verified client certs; `SERVER_NAME` overrides SAN verification for pod-IP `dns://` targets). |
| `APEXLOAD_GRPC_AUTH_TOKEN` | — | Shared bearer token required on generator RPCs. In `APEXLOAD_ENV=production|prod`, controller and generator startup **fail closed** unless gRPC uses mTLS, verified TLS + this token (>= 32 bytes when not using mTLS), or an authenticated WSS reverse tunnel; plaintext + token is rejected. |
| `APEXLOAD_GENERATOR_CONNECTIVITY_MODE` | `direct` | Generator connectivity mode: `direct` (inbound gRPC listener), `reverse` (outbound WSS tunnel to controller gateway, used by cloud PaaS workers), or `both`. |
| `APEXLOAD_GENERATOR_GATEWAY_TOKEN` | — | Token authenticating outbound reverse-generator WSS registrations with the controller gateway. |
| `APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE` | `false` | Marks a `reverse`-only generator's inner gRPC stream as protected by the authenticated WSS reverse tunnel (`valid only with APEXLOAD_GENERATOR_CONNECTIVITY_MODE=reverse`). |

### Auth, CORS & Script Environment

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_REQUIRE_AUTH` | `false` (implied in `production`/`prod`) | Forces hardened authentication posture outside `APEXLOAD_ENV=production|prod` (refuses `dev`, `disabled`, and `local` bypass/test modes at startup). |
| `APEXLOAD_AUTH_MODE` | *not an env var* | Intentionally **not** read from the environment: authentication mode (`dev`, `local`, `oidc`, `trusted_headers`) is persisted system state managed in **Access** (`#/access`, under **Authentication**) or `PUT /api/v1/system/settings`. |
| `APEXLOAD_AUTH_SESSION_SECRET` | default `auth.session_secret_env_var` | HMAC/signing secret environment variable read for browser/OIDC auth sessions and CI token signing. |
| `APEXLOAD_TRUSTED_PROXY_SECRET` | conventional `auth.trusted_proxy_secret_env_var` | Shared secret verified on inbound proxy headers when `auth.mode` is `trusted_headers` and `trusted_proxy_secret_env_var` names this variable. |
| `APEXLOAD_ALLOWED_ORIGINS` | same-origin only | Referenced in dashboard cross-origin guidance; note that `cmd/controller` enforces strict same-origin HTTP/WebSocket checks by default while `api.ServerConfig.AllowedOrigins` is wired programmatically (for example by the Wails desktop shell). Host custom browser UIs behind the same origin/reverse proxy. |
| `APEXLOAD_SCRIPT_ENV_ALLOWLIST` | empty (deny host env) | Comma-separated allowlist (exact names or `PREFIX*`, e.g. `TARGET_*,TEST_*`) permitting `{{env.KEY}}` script templates to read host/pod environment variables; `APEXLOAD_*` and cloud/credential variables remain blocked regardless. |

### Secrets & Envelope Key Wrapping (Azure Key Vault & AWS Secrets Manager / KMS)

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_WRAPPING_MASTER_KEY` | — | 32-byte (raw or base64) envelope master key for encrypted DB-backed Project Secrets and protected import artifacts. Required in `production`/`prod` (or `APEXLOAD_REQUIRE_AUTH`) unless a cloud KMS/Key Vault wrapping key is configured. |
| `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID` | — | Versioned Azure Key Vault cryptographic key URI for envelope key wrapping (mutually exclusive with `APEXLOAD_WRAPPING_KMS_KEY_ID`). |
| `APEXLOAD_WRAPPING_KMS_KEY_ID` | — | Full AWS KMS key ARN for authenticated envelope key wrapping on EKS/AWS deployments (mutually exclusive with `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID`). |
| `APEXLOAD_AZURE_KEY_VAULT_URL`, `APEXLOAD_AZURE_KEY_VAULT_AUTH`, `APEXLOAD_AZURE_KEY_VAULT_TOKEN`, `APEXLOAD_AZURE_KEY_VAULT_SCOPE`, `APEXLOAD_AZURE_KEY_VAULT_API_VERSION` | `auto` auth, API `7.4` | Azure Key Vault secret resolution (`token`, `azure_identity`, or `auto`) for `{{secret.*}}`. |
| `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED` | `false` (controller only) | Enables writable Azure Key Vault Project Secrets management on the controller (`Key Vault Secrets Officer`). |
| `APEXLOAD_AWS_SECRETS_MANAGER_REGION` | — | AWS region for AWS Secrets Manager `{{secret.*}}` reads and controller secret writes. |
| `APEXLOAD_AWS_SECRETS_MANAGER_ENDPOINT` | regional AWS endpoint | Optional custom HTTPS endpoint override for AWS Secrets Manager (VPC endpoints / sovereign partitions). |
| `APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED` | `false` (controller only) | Enables writable AWS Secrets Manager Project Secrets management on the controller (requires `APEXLOAD_AWS_SECRETS_MANAGER_REGION`). |

### Telemetry, Browser Runtime, TLS Compatibility & Diagnostics

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_METRICS_ADDR` / `APEXLOAD_METRICS_TOKEN` | API port / unset | Optional dedicated listener (e.g. `:9091`) and optional bearer token for `GET /metrics`. |
| `APEXLOAD_PPROF_ADDR` | disabled | Optional Go `pprof` bind address (must be explicit loopback such as `127.0.0.1:6060` in production). |
| `APEXLOAD_PROMETHEUS_URL` / `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL` | — | Prometheus query URL for dashboard overlays and optional remote-write retention endpoint. |
| `APEXLOAD_OTEL_ENABLED` | `false` | Opt-in OpenTelemetry tracing for controller and generator (requires `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`); see [docs/OBSERVABILITY_INTEGRATIONS.md](docs/OBSERVABILITY_INTEGRATIONS.md). |
| `OTEL_EXPORTER_OTLP_PROTOCOL` / `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` | `http/protobuf` | OTLP trace transport (`http/protobuf` or `grpc`). |
| `APEXLOAD_OTEL_ACTION_SAMPLE_RATIO` | `0.01` (`0..1`) | Sampling ratio for high-volume VUser engine action spans. |
| `APEXLOAD_BROWSER_EXECUTABLE` | Playwright bundle | Path to system Chrome/Chromium binary (set to `/usr/bin/google-chrome` in Alpine musl images). |
| `APEXLOAD_BROWSER_SANDBOX` | `false` (`--no-sandbox`) | Set `true` to enable Chromium's OS sandbox where unprivileged user namespaces are available. |
| `APEXLOAD_BROWSER_VERSION_SKEW` | warn in evidence | Set `fail` to abort browser sessions whose Chromium major version differs from bundled Playwright `browsers.json`. |
| `APEXLOAD_ENABLE_TLS_COMPAT` / `APEXLOAD_DISABLE_TLS_COMPAT` (`APEXLOAD_DISABLE_DEV_TLS_COMPAT`) | dev-only enabled | Controls automatic `GODEBUG` TLS compatibility defaults (`x509negativeserial=1,tlsmlkem=0,tlssecpmlkem=0`). |

### Archives, Import Assets, Analytics & RCA Budgets

| Variable | Default / Scope | Purpose |
|----------|-----------------|---------|
| `APEXLOAD_ARCHIVE_BACKEND` | `filesystem` | Run archive storage backend: `filesystem`, `s3`, `azure_blob`, or `gcs`. |
| `APEXLOAD_ARCHIVE_DIR` | `<UserCacheDir>/apexload/archives` | Local archive directory and staging root for remote object archives. |
| `APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_CONTAINER`, `APEXLOAD_ARCHIVE_PREFIX`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT` | — | Object-storage target coordinates for `s3`, `azure_blob`, and `gcs` archive backends. |
| `APEXLOAD_ARCHIVE_S3_FORCE_PATH_STYLE` | `true` (unless `"false"`) | Controls S3 path-style addressing for S3/MinIO-compatible endpoints; set `false` for virtual-hosted-style buckets where required. |
| `APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES` | `32 GiB` | Per-archive byte budget for retained archive collection materialization on the controller. |
| `APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES` | `128 GiB` (cap `1 PiB`) | Total local disk cache budget across materialized archive collections. |
| `APEXLOAD_IMPORT_ASSET_DIR` | local cache (dev) | Durable directory for protected/imported source assets when `APEXLOAD_ARCHIVE_BACKEND=filesystem` (required in production unless an object archive backend is configured). |
| `APEXLOAD_RCA_V2_PROJECTS` | — | Comma-separated project IDs (or `*`) opting projects into Root Cause Analysis v2 typed run-graph and hypothesis evaluation. |

See [Project Secrets](docs/ADMIN_USER_GUIDE.md#application-and-project-secrets) for provider setup, import migration governance, and secure distributed delivery requirements.

Note: If you run with `APEXLOAD_ENV=production|prod` or enable `APEXLOAD_REQUIRE_AUTH`, the persisted authentication mode (**Access** `#/access` → **Authentication**, stored as `auth.mode`) must not be `dev`, `disabled`, or the fixed-credential `local` test-login mode. The controller refuses to start in that combination by design; use `oidc` or `trusted_headers`.

## Build and Test

### Building

Use Make targets from repo root:

```bash
make build       # builds generator, controller, apexctl, apexload-record-agent, and apexload-collector-agent into ./bin
make package-recording-agent  # builds cross-platform Recording Agent release archives
make test        # runs third_party/playwright-go fork tests + Go tests with race detector (-timeout=20m)
make test-cover  # race + coverage report (coverage.out / coverage.html)
make lint        # runs golangci-lint (if installed)
make generate    # runs all 6 Go code generators (dslschema, swaggergen, openapits, datamodeldoc, webcoverage, storeoverlay)
make desktop     # builds the Wails desktop app (also: make clients, make desktop-preflight)
make java-vuser-runner  # builds the Java Vuser runner jar and bridge binary
make performance-smoke  # runs the apexbench smoke profile (JSON to stdout)
make docker-local-up                 # runs controller, generator, and Prometheus locally (--profile java-vuser adds java-vuser-runner)
make docker-local-prometheus-check   # validates the local Prometheus config
```

### Container Build Profiles

Three root Dockerfiles serve distinct runtime profiles:

| Dockerfile | Base / Toolchain | CGO & Build Tags | Chromium / Browser Runtime | Target Use |
|------------|------------------|------------------|----------------------------|------------|
| `Dockerfile` | Alpine `3.24` (`golang:1.26.10-alpine`) | `CGO_ENABLED=0`; `GO_BUILD_TAGS=standard\|enterprise` | Alpine musl `chromium` (`APEXLOAD_BROWSER_EXECUTABLE=/usr/bin/google-chrome`) | Default protocol load generator image (`make docker-generator`) and `compose.yaml` local services |
| `Dockerfile.controller` | Debian 13 (`debian:trixie-slim`, built on `golang:1.26.10-bookworm`) | `CGO_ENABLED=1` with `-tags analytics_duckdb` (plus enterprise messaging tags when `GO_BUILD_TAGS=enterprise`) | Playwright-pinned glibc Chromium in `/ms-playwright` | Production controller image (`make docker-controller`) with native DuckDB analytics and browser Verify/replay |
| `Dockerfile.browser-generator` | Debian 13 (`debian:trixie-slim`, built on `golang:1.26.10-bookworm`) | `CGO_ENABLED=0`; `BINARY=generator` (`GO_BUILD_TAGS=standard`) | Playwright-pinned glibc Chromium in `/ms-playwright` | Dedicated browser-observer generator pools sharing the exact Playwright Chromium major version with recording and Verify |

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
- `--api` / `APEXLOAD_API` — Execute through the controller API instead of locally (subcommands targeting the controller default `--api` from `APEXLOAD_API`)
- `--env, -env` — Runtime environment substitution for `{{env.KEY}}` placeholders
- `--base-url` — Override the script base URL at run time
- `--p95-latency-ms`, `--max-error-pct`, `--max-avg-server-processing-ms`, `--max-avg-transfer-ms`, `--max-error-count` — CI/CD guardrails that make `apexctl` exit non-zero when breached

Additional `run` flags: `--junit` (write a JUnit XML report), `--timeout` (API run wait timeout; default duration + 30m), `--transaction-rule` / `--transaction-rules-file` (exact per-transaction SLA rules; require `--api`), `--telemetry-policy` (judge the run by one of the project's telemetry acceptance policies instead of its default), `--telemetry-budget-policy` (set the run's live telemetry budgets from one of the project's telemetry budget policies instead of the system threshold settings), `--browser-policy ID@REV` (pin a project browser policy `<id>@<revision>` or `project_default`), and `--journey-policy ID@REV` (pin a project journey policy `<id>@<revision>` or `project_default`); policy flags require `--api`, and the controller confirms telemetry policy bindings before the run starts.

**Subcommands** (21 commands plus `help`; `apexctl help` prints full usage):

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
| `import` | Import native Bruno HTTP collections or Grafana dashboards |
| `library` | Export or import portable project library bundles |
| `prepare` | Prepare, inspect, renew, cancel, or start a prepared run |
| `schedule` | Manage scenario schedules and occurrence resolutions |
| `monitor` | Configure run-bound monitoring sources and scenario bindings |
| `analysis` | Import/query datasets, query live or archived native series, save analysis definitions, and assess baseline regressions |
| `rca` | Run, poll, inspect, export, or evaluate root-cause analyses |
| `qa` | Record and read feature acceptance evidence (`record-acceptance`, `acceptance`) |
| `prometheus` | Manage Prometheus target-system monitoring connections, discovery, and previews |
| `dynatrace` | Manage Dynatrace SaaS connections, discovery, and query previews |
| `elastic` | Manage Elastic/OpenSearch connections, discovery, and previews |
| `version` | Print version, commit, and build-time information |

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

## Project Structure

See [AGENTS.md](AGENTS.md) for the canonical, machine-checked `cmd/` and `pkg/` package inventory. At the repository root:

```
cmd/                 # Runtime binaries, agents, and Go code generators (see AGENTS.md)
pkg/                 # Core engine, protocols, DSL, API, storage, RCA, and governance packages (see AGENTS.md)
api/proto/           # gRPC protobuf definitions (controller ↔ generator; see api/proto/README.md)
dashboard/           # React 18 + TypeScript + Recharts SPA (see dashboard/README.md)
desktop/             # Wails v2 native client shell embedding controller API + dashboard (see desktop/README.md)
mcp/                 # Python MCP server exposing the REST API as agent tools
deployments/         # Kustomize overlays (local/onprem/aks/eks), Helm chart, Docker Compose, Azure & AWS Terraform
examples/            # Sample JSON DSL scripts, CSV prompt feeds, raw .http fixtures, and monitoring configs (see examples/README.md)
scripts/             # CI verification, E2E harnesses, manifest renderers, and packaging scripts (see scripts/README.md)
.github/workflows/   # Authoritative CI/CD and release workflows
```

## Development Workflow

### Common Tasks

**Add a new API endpoint:**
1. Define handler in `pkg/api/handlers_*.go`
2. Register route and access requirements in the matching `pkg/api/route_registry_*.go` / `pkg/api/route_catalog.go`
3. Add REST tests in the matching `pkg/api/handlers_*_test.go` file for the endpoint's domain
4. Regenerate OpenAPI, endpoint catalog, and TypeScript contracts with `go generate ./pkg/api`
5. Run `go test ./pkg/api`

**Add a new protocol:**
1. Create `pkg/protocol/myproto/myproto.go`
2. Implement `protocol.Protocol` and call `protocol.Register` in the package `init()`
3. Add a blank import in `pkg/protocol/builtin/builtin.go` so standard binaries compile it in
4. Test with unit tests and sample scripts

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
| VUsers per pod | 10,000 | Memory-limited; `pkg/perfevidence` measures idle per-VUser memory |
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

Proprietary / internal repository — no open-source license file is published in this repository.
