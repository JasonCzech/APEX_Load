# APEX Load Architecture

APEX Load is a Kubernetes-native performance testing platform with a Go control plane, goroutine-based load generators, a React dashboard, a JSON DSL script model, and enterprise data services for shared production deployments. The platform is designed to replace practical LoadRunner Enterprise workflows while keeping local development simple.

This document reflects the current application architecture. Related operational plans and runbooks live under `docs/`.

## System Context

```text
Users and automation
  |
  |  Browser UI, REST, WebSocket, apexctl, MCP tools
  v
Controller API
  |
  |-- Dashboard static assets and API handlers
  |-- Auth/RBAC, audit, service accounts, dev preview mode
  |-- Script authoring, import, validation, replay
  |-- Scenario modeling, scheduling, governance
  |-- Test orchestration and live controls
  |-- Archive, reporting, usage, parity, fleet APIs
  |
  +--> Metadata store: SQLite for local, PostgreSQL for enterprise
  +--> Cache/live state: memory for local, Redis/Valkey for HA
  +--> Archive store: filesystem for local, S3/Azure Blob/GCS for enterprise
  +--> Metrics retention: /metrics plus Prometheus-compatible remote-write
  |
  +--> Local engine, or distributed generator gRPC targets
          |
          +--> Protocol execution against target systems
```

## Runtime Surfaces

| Surface | Code | Responsibility |
| --- | --- | --- |
| Controller API | `cmd/controller`, `pkg/api` | REST API, WebSocket streams, dashboard hosting, scheduling, orchestration, governance, reporting, readiness, inventory |
| Dashboard | `dashboard/` | React SPA for administration, repository management, authoring, scenario assembly, monitoring, reports, API guide, Access (`#/access`) |
| Desktop Workspace | `desktop/` | Wails v2 native client embedding a loopback controller + dashboard with local SQLite/keyring storage and Remote Workspace sync/remote execution |
| CLI | `cmd/apexctl` | Local script execution and controller-backed CI/CD, analysis, RCA, monitoring, and import workflows |
| Generator | `cmd/generator`, `pkg/distributed/generatorrpc` | Remote worker that executes assigned groups and streams metrics/artifacts to the controller |
| Recording Agent | `cmd/apexload-record-agent`, `pkg/recordagent` | Workstation browser-recording bridge pairing local Playwright/Chromium capture with a remote controller dashboard |
| Monitoring Collector | `cmd/apexload-collector-agent`, `pkg/collectoragent` | Out-of-band monitoring agent polling signed target-system metric jobs (Linux/Prometheus, SSH, OTLP, restricted Java packs) |
| Java Vuser Runner | `cmd/apexload-java-vuser-runner`, `pkg/javarunner`, `pkg/javasidecar` | Governed loopback/sidecar JVM runner container executing `java_vuser` actions (disabled unless operator config enables it) |
| MCP server | `mcp/` | FastMCP bridge for agents and automation against the documented REST API |

The controller embeds the built dashboard from `pkg/api/static`. During dashboard development, the Vite dev server proxies API calls to the controller.

## Domain Model

APEX Load's governance hierarchy is:

```text
Domain
  -> Application Repository
      -> Category
          -> Project
              -> Release
                  -> Scenarios, runs, reports, archives
              -> Scripts, folders, data pools, scenarios
```

Administrators grant access at the domain and application repository levels. Project-level roles still act as the main execution boundary for scripts, scenarios, tests, schedules, results, and archives. Existing application/release APIs remain as compatibility aliases while the repository hierarchy is the current product model.

## Data Ownership

| State family | Local/dev owner | Enterprise owner | Notes |
| --- | --- | --- | --- |
| Domains, repositories, categories, projects, releases, folders | SQLite | PostgreSQL | Authoritative control-plane metadata |
| Scripts, authoring sessions, imports, review artifacts | SQLite | PostgreSQL | JSON DSL plus import metadata and health signals |
| Scenarios, schedules, approvals, occurrences, leases | SQLite | PostgreSQL | Scheduler HA uses durable database leases, not Redis-only locks |
| Queued and terminal run metadata | SQLite | PostgreSQL | Active in-process state is reconciled from durable records on restart |
| Live run snapshots and generator inventory cache | Memory | Redis/Valkey | Ephemeral, short TTL, not a system of record |
| Idempotency keys and API-key rate limits | Memory | Redis/Valkey | Optional request coordination layer |
| WebSocket fanout | Local hub | Redis/Valkey fanout bus plus local hubs | Enables multi-controller live event propagation |
| Full run evidence | Filesystem archive root | S3, Azure Blob, or GCS | Immutable manifests, JSONL, CSV, Parquet, browser evidence, reports |
| Long-term operations metrics | Local scrape | Prometheus remote-write target | High-cardinality raw transaction evidence stays in archives |

Configuration entry points include `APEXLOAD_STORE`, `APEXLOAD_DATABASE_URL`, `APEXLOAD_ARCHIVE_BACKEND`, `APEXLOAD_CACHE_BACKEND`, `APEXLOAD_REDIS_URL`, and Prometheus remote-write settings.

## Controller Internals

```text
HTTP server
  |
  +-- Middleware
  |     +-- CORS, logging, auth, project/domain/repository RBAC
  |     +-- dev Admin/User preview headers in dev mode
  |     +-- service-account scope checks, rate limits, idempotency keys
  |
  +-- API handlers
  |     +-- domains/repositories/projects/releases
  |     +-- scripts/imports/authoring/replay
  |     +-- scenarios/load models/schedules
  |     +-- tests/live controls/results/archives
  |     +-- system readiness/inventory/usage/parity
  |
  +-- TestRunner
  |     +-- local execution backend
  |     +-- distributed execution backend
  |     +-- live metric publication and archive finalization
  |
  +-- WebSocket hub
        +-- local client filtering by project/test
        +-- optional Redis fanout envelope for HA controllers
```

`GET /api/v1/system/readiness` is the production traffic gate. It checks metadata store, archive writer, cache backend, generator RPC state, telemetry fanout, auth mode, and scheduler state. `GET /api/v1/system/inventory` reports controller, generator, and data-platform configuration without exposing secrets.

## Execution Modes

### Local Mode

If `APEXLOAD_GENERATORS` is not configured, the controller runs tests in-process through the same engine used by `apexctl`. This is the default for development and small diagnostic runs.

### Distributed Mode

If `APEXLOAD_GENERATORS` is configured, the controller uses the distributed backend:

1. The controller validates the run, load model, capacity, data fit, and placement constraints.
2. Groups are assigned to generator targets, with support for drain/cordon state and region-weighted placement.
3. Generators execute assigned groups and stream `MetricsBatch` snapshots over gRPC.
4. The controller merges generator metrics into one `LiveMetrics` view.
5. Live controls such as stop, abort, pause, resume, duration adjustment, and per-group VUser scale are routed to the owning generator.
6. On completion, generators expose archive artifacts and the controller stages/merges them into the run archive.

Generator targets are static `host:port` entries, or `dns://host:port` entries that the controller re-resolves on an interval (`APEXLOAD_GENERATOR_DISCOVERY_INTERVAL`, default 15s) — in Kubernetes this points at the generator headless service, so pods added or removed by KEDA/HPA autoscaling join and leave the fleet automatically.

The gRPC channel enforces TLS/mTLS plus shared-token authentication via `APEXLOAD_GRPC_TLS_*` and `APEXLOAD_GRPC_AUTH_TOKEN` (certificates are re-read per handshake/dial, so cert-manager rotation needs no restart). In `APEXLOAD_ENV=production` or `prod`, both controller (when distributed targets are configured) and generator **fail closed and refuse startup** (`TransportSecurity.MustRefuseInsecure`) unless controller↔generator gRPC uses mTLS, verified TLS + `APEXLOAD_GRPC_AUTH_TOKEN` (>= 32 bytes when not using mTLS), or an authenticated WSS reverse tunnel (`APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE=true` with `APEXLOAD_GENERATOR_CONNECTIVITY_MODE=reverse`). Bearer token authentication over plaintext is explicitly rejected, as is server-only TLS without client certificates or a shared token.

The public REST and WebSocket contracts are the same in local and distributed mode.

## Load Engine

The engine uses one goroutine per virtual user. Each VUser executes a JSON DSL script through the selected protocol implementation.

```text
Run starts
  -> init actions
  -> ramp and pacing controls
  -> iteration loop
       -> resolve templates and data rows
       -> execute protocol action
       -> extract variables
       -> evaluate assertions
       -> record metric sample
       -> apply think time and pacing
  -> teardown actions
  -> final metrics and archive flush
```

Metrics flow through buffered channels and are aggregated into streaming percentile summaries. The hot path avoids database writes for raw transaction events; full-fidelity evidence is written to archive artifacts.

## Protocol And Import Architecture

Protocols implement the compiled-in `pkg/protocol.Protocol` interface, register themselves via `protocol.Register` in their package `init()`, and are linked into standard binaries through blank imports in `pkg/protocol/builtin/builtin.go`. Current protocol/runtime surfaces include:

- HTTP/1.1, HTTP/2 and an HTTP/3 preview (exact `3` and the `auto+h3` Alt-Svc upgrade) behind `APEXLOAD_HTTP3` (`http` negotiates; `http/1.1`, `http/2` and `http/3` supply exact inherited defaults)
- SOAP and WSDL import
- Java-over-HTTP (`java_http`) bounded codec support
- Java Vuser (`java_vuser`): governed JVM sidecar runner; disabled unless an operator sets `APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true` on the generator (see [docs/JAVA_SHARED_AND_PILOT_RUNTIME.md](docs/JAVA_SHARED_AND_PILOT_RUNTIME.md) and experimental isolation contracts in [docs/JAVA_DEDICATED_BROKER_EXPERIMENTAL.md](docs/JAVA_DEDICATED_BROKER_EXPERIMENTAL.md))
- WebSocket (`websocket`)
- gRPC (`grpc`)
- JMS Solace (`jms-solace`), IBM MQ (`jms-ibmmq`), and Azure Service Bus (`jms-servicebus`; real Service Bus adapter opt-in via `-tags jms_servicebus_real`)
- Kafka (`kafka`: produce/consume/request-reply; in-memory mock broker by default, real franz-go adapter via `-tags kafka_real`)
- Azure Event Hubs (`eventhubs`): producer-first with observer consumption; real AMQP adapter opt-in via `-tags eventhubs_real`
- database (`database`) and network (`network`: DNS, TCP, UDP, FTP, SMTP, POP3, IMAP, LDAP) protocol import/runtime paths
- browser (`browser`) replay and browser canary diagnostics (see [docs/BROWSER_EVIDENCE_ARCHITECTURE.md](docs/BROWSER_EVIDENCE_ARCHITECTURE.md))
- Appium (`appium`): a device observer that drives one real device or simulator through an Appium server (a device cloud's hub, or Appium on a Mac) while HTTP groups supply the load

Bruno Runner is retired under `_retired/bruno-runner` and excluded from builds; native Bruno HTTP collection import remains built into `pkg/convert` and `apexctl import bruno`. `sap_gui` actions are authorable/importable via the DSL catalog for migration inspection but have no registered runtime protocol.

Script inputs can be created directly in the dashboard, through REST, through `apexctl`, or by import. Import surfaces include LoadRunner/VuGen ZIP, native Bruno HTTP collections, JMeter, k6, DevWeb, HAR, OpenAPI/Swagger, WSDL, Postman, AsyncAPI, raw HTTP, cURL, REST JSON, and sample packs.

The LoadRunner importer is protocol-aware. It classifies known VuGen protocol families, returns confidence/signals/conflicts, routes executable protocols to native APEX Load targets, and creates review artifacts for external-runner or unsupported protocols instead of pretending they are executable.

## Authoring, Replay, And AI Assistance

Authoring state is represented by stored scripts and authoring sessions. The dashboard emphasizes the script editor as the primary workspace, with runtime settings, replay output, parameter data, and script configuration surfaced through focused dialogs.

Key authoring services:

- template-backed script creation for supported protocols
- validation and replay for draft and saved scripts
- browser replay sessions with evidence capture
- guided correlation repair preview/apply flows
- migrated-script review and approval before promotion
- deterministic AI-assisted drafts, repairs, explanations, policy checks, review queue, and evidence-bound root-cause summaries

AI workflows are governed by policy and cite local script, scenario, runtime, or archive evidence. External LLM provider integration is intentionally policy-gated.

## Load Modeling And Scheduling

Scenarios compose one or more scripts, load model settings, data pools, runtime overrides, goals, and placement preferences. Scenario validation returns compiled load plans, capacity fit, data fit, placement fit, and blocking findings.

The scheduler persists schedules, revisions, occurrences, approvals, missed-run policy, conflict summaries, and health status. High-cost, off-hours, production, quota-burst, or manually reviewed schedules can require approval. Durable database leases provide singleton scheduler ownership in HA deployments.

## Monitoring And Reporting

Live monitoring is fed by WebSocket events:

- `metrics_update`
- lifecycle events such as created, started, paused, resumed, complete, failed
- telemetry diagnostics and threshold alerts
- browser and resource summaries where available

Completed runs write archive bundles with manifests, transaction/resource/summary datasets, browser diagnostics, JSONL/CSV/Parquet exports, HTML/JSON/CSV reports, root-cause summaries, and comparison views (see [docs/NATIVE_ANALYTICS.md](docs/NATIVE_ANALYTICS.md) and [docs/ARCHIVE_SOURCE_RETENTION.md](docs/ARCHIVE_SOURCE_RETENTION.md)). Report profiles support executive, engineer, and audit-oriented outputs.

Usage accounting snapshots, archive retention planning/apply events, release trends, parity status, fleet alerts, and support matrices are exposed through REST and dashboard views.

## Auth, RBAC, And Automation

APEX Load supports:

- dev mode for local bypass plus dashboard Admin/User preview toggle
- trusted-header mode for upstream identity gateways
- OIDC mode
- service-account API keys for MCP, CI/CD, import automation, and system administration
- global, domain, application repository, and project-scoped roles
- scope enforcement for service-account tokens
- audit events for governance-sensitive operations

Dev preview headers are honored only in dev mode and ignored by trusted-header/OIDC production modes.

## Enterprise Deployment

Production reference deployment uses:

- two or more controller replicas behind a service, with `APEXLOAD_CONTROLLER_HA=true`
- PostgreSQL 16 or newer for metadata and leases
- Redis/Valkey for live state, request coordination, and WebSocket fanout
- object storage for immutable archives
- Prometheus scraping and optional remote-write to Mimir, Thanos, or managed Prometheus
- generator StatefulSets with autoscaling signals and region/capability metadata
- readiness probes on `/api/v1/system/readiness`

Controller replicas are safe only with the complete HA contract: PostgreSQL for shared metadata and execution leases, Redis/Valkey for coordination and WebSocket fanout, shared object storage for archives/import assets, and identical auth/secret-provider configuration. Merely scaling the Deployment without enabling HA and those shared services is unsupported.

Examples live under `deployments/k8s/`, including production data-platform settings, monitoring manifests, HA controller deployment, and Redis/Valkey reference manifests.

For production on-prem clusters, `deployments/k8s/onprem/` is a hardened manifest set: gRPC mTLS + token auth, TLS ingress, a dedicated metrics port scoped to the monitoring namespace, Pod Security Admission `restricted`, scoped network policies with opt-in egress templates, ephemeral-storage bounds, and PDBs — see its README for the CNI-enforcement verification step.

For Azure, `deployments/k8s/aks/` is a complete production manifest set (workload-identity service accounts, KEDA ScaledObject, managed-NGINX ingress, Azure data-platform ConfigMap) and `deployments/azure/terraform/` provisions the matching infrastructure (AKS with OIDC issuer + workload identity + managed KEDA, ACR, PostgreSQL Flexible Server, Azure Cache for Redis, Blob Storage, Key Vault, federated identity credentials). The on-prem → AKS migration runbook is `deployments/k8s/aks/README.md`. For AWS, `deployments/k8s/eks/` and `deployments/aws/terraform/` provide the corresponding EKS/IRSA/RDS/ElastiCache/S3/KMS stack. Provider-neutral async cloud capacity (`pkg/compute`) also supports ephemeral reverse-tunnel generators on Azure Container Apps/Jobs, Azure Container Instances, and AWS ECS Fargate (see [docs/ENTERPRISE_PLATFORM_RUNBOOK.md](docs/ENTERPRISE_PLATFORM_RUNBOOK.md)).

## Performance Targets

| Metric | Target | Notes |
| --- | --- | --- |
| HTTP VUsers per generator pod | 10,000 | Target envelope for 4 vCPU / 8 GB class pods |
| Generator pod scale | 1 to 50 | HPA/KEDA signals documented for production tuning |
| HTTP harness overhead | < 50 microseconds | Per transaction target |
| Metrics latency | < 2 seconds | Generator/controller collection to dashboard display |
| Ramp-up rate | 500 VUsers/second/pod | Protocol and target dependent |
| Browser VUsers | diagnostic canaries | Not intended as HTTP-density load generators |

Protocol-specific density envelopes and benchmark evidence are tracked in [docs/PROTOCOL_SUPPORT.md](docs/PROTOCOL_SUPPORT.md) and [docs/PERFORMANCE_BENCHMARKS.md](docs/PERFORMANCE_BENCHMARKS.md).

## Extension Points

| Extension | Primary files |
| --- | --- |
| Protocol runtime | `pkg/protocol`, package `init()` (`protocol.Register`) + blank import in `pkg/protocol/builtin/builtin.go` |
| Script import converter | `pkg/convert`, `pkg/api/handlers_scripts.go` |
| API endpoint | `pkg/api/route_registry_*.go`, `pkg/api/route_catalog.go`, matching `pkg/api/handlers_*.go` & test file |
| Dashboard page or workflow | `dashboard/src/` |
| Storage schema/contract | `pkg/storage` |
| MCP tool coverage | `mcp/apexload_mcp/` |
| Deployment shape | `deployments/k8s/`, `deployments/helm/apexload` |

New features should include tests alongside the changed package and documentation updates for changed public behavior.

## Current Status

Implemented and active:

- controller API and embedded dashboard
- local and distributed execution
- JSON DSL, protocol registry, import routing, validation, replay
- domain/application repository/category/project/release hierarchy
- Auth/RBAC, service accounts, audit events, dev Admin/User preview
- scenario load modeling, scheduling governance, approvals, occurrences
- runtime controls, telemetry diagnostics, archive reports, comparison reports
- PostgreSQL store option, object archive backends, Redis/Valkey cache option
- Prometheus-compatible monitoring artifacts
- MCP API bridge

Deliberate boundaries:

- SQLite, filesystem archives, and memory cache remain the local defaults.
- PostgreSQL, object storage, and Redis/Valkey are the enterprise shared-deployment path.
- Redis/Valkey is not authoritative durable state.
- Raw high-cardinality transaction evidence belongs in archive artifacts, not SQL or Redis.
- Browser VUsers are diagnostic canaries, not the primary high-density load mechanism.
