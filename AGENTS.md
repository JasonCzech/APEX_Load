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
- **Telemetry**: Prometheus scrape + W3C `traceparent` propagation + Dynatrace event webhooks + opt-in OTLP traces for controller HTTP, test lifecycle/orchestration, sampled engine actions, and controller↔generator gRPC. Controller aggregates with t-digest for percentiles. OpenTelemetry metrics/log export is not implemented.
- **Storage**: SQLite by default (`apexload.db`); PostgreSQL is an optional enterprise backend (via pgx). Live state is in-memory with optional Redis/Valkey for HA. Archives are written to the local filesystem; S3/Azure Blob/GCS are optional enterprise backends.
- **Deployment**: Kustomize manifests for local/kind (`deployments/k8s/local*`), production on-prem (`deployments/k8s/onprem`: gRPC mTLS+token, TLS ingress, dedicated metrics port, PSA restricted, scoped network policies), production AKS (`deployments/k8s/aks`: workload identity, KEDA, ingress), and EKS (`deployments/k8s/eks`), each with `*-ha` and related overlays; Terraform IaC for the Azure stack (`deployments/azure/terraform`: AKS/ACR/PostgreSQL/Redis/Blob/Key Vault) and the AWS stack (`deployments/aws/terraform`: EKS/ECR/RDS/ElastiCache/S3/EFS/KMS). Generator fleet scales via KEDA + `dns://` headless-service discovery. Azure App Service uses the launcher in `deployments/azure/appservice/` with PostgreSQL and static generator targets. A Helm chart for Kubernetes/AKS enterprise installs lives in `deployments/helm/apexload`. (No operator/CRDs yet.) System administrators can additionally register N external clusters (generic Kubernetes via a ServiceAccount token, AKS via an Azure AD service principal) from the dashboard and set per-pool CPU/memory and node placement — see [docs/CLUSTERS_AND_CLOUD.md](docs/CLUSTERS_AND_CLOUD.md). Node reads for pool/zone discovery need the opt-in `deployments/k8s/aks/node-read.optional.yaml` ClusterRole; everything degrades gracefully without it.

## Module Path
`github.com/jasonczech/apexload`

## Project Structure
```
cmd/
  generator/     — Load generator binary (deployed as StatefulSet pods)
  controller/    — Test controller + API server binary (Deployment; HA replicas are opt-in with APEXLOAD_CONTROLLER_HA + shared storage)
  apexctl/       — CLI tool for running/managing tests
  apexload-record-agent/ — Local browser-recording bridge agent (run on operator workstations)
  apexload-collector-agent/ — Monitoring collector agent (controller heartbeat + host-metric packs)
  apexload-java-host-facade/ — Experimental loopback facade for dedicated broker execution
  apexload-java-vuser-runner/ — Java Vuser runner bridge (external-runner container for `java_vuser`)
  apexload-java-host-supervisor/ — Experimental dedicated Java execution-pod PID 1 supervisor (not enabled by default)
  apexload-java-host-broker/ — Experimental mTLS broker for dedicated Java pods and externally attested cleanup (not enabled by default)
  apexfault-target/ — Authenticated independent HTTP traffic counter for disposable fault trials
  apexbench/     — Controlled local performance-evidence harness (`make performance-smoke`)
  dslschema/     — Code generator for docs/dsl.schema.json + DSL_ACTION_REFERENCE.md
  swaggergen/    — Code generator for docs/swagger.yaml + docs/api-endpoint-catalog.json
  openapits/     — Code generator for the dashboard's TypeScript API contract from the OpenAPI document
  webcoverage/   — Generator for the LoadRunner import coverage docs (`docs/LOADRUNNER_COVERAGE.md`)
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
  convert/       — Multi-format import hub (LR archive, Bruno, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient, SAP GUI, Java-family imports)
  selector/      — Shared element-selector model (CSS/XPath/role/text) + replay self-heal
  scrub/         — PII/credential redaction for recorder artifacts and HAR exports
  refkey/        — Human-readable, environment-aware reference keys
  buildinfo/     — Build metadata (version/commit/build-time) via -ldflags
  config/        — Configuration types
  kubeops/       — Kubernetes operations (generator pod restart/drain, scaling, node/quota reads).
                   Pluggable auth via TokenSource: in-cluster service-account file, static
                   token, or Azure AD service principal for AKS. NewRemoteClient reaches
                   registered external clusters; see docs/CLUSTERS_AND_CLOUD.md.
  recordagent/   — Local recording-agent runtime (Playwright/Chromium provisioning + bridge)
  collectoragent/ — Monitoring collector with signed jobs, sealed credentials, Linux/Prometheus, SSH, OTLP and restricted Java packs
  compute/       — Provider-neutral async capacity backends (Kubernetes, Azure Container Apps/Jobs, ECS Fargate)
  telemetry/     — Opt-in OpenTelemetry tracing setup + HTTP instrumentation (off unless APEXLOAD_OTEL_ENABLED)
  trafficprofile/ — Production time-series parsing, normalization, shaping, and runtime profile contracts
  debugserver/   — Optional net/http/pprof listener, gated by APEXLOAD_PPROF_ADDR
  runtimetuning/ — GOMAXPROCS/GOMEMLIMIT resolution from cgroup limits
  perfevidence/  — Controlled local performance probes + component evidence reports
  analysis/      — Shared validated metric-point model and time-series correlation primitives
  rca/           — Pure root-cause analysis engine (workload qualification); no storage, provider or HTTP imports
  prometheus/    — Governed PromQL target monitoring and Azure managed Prometheus query adapter
  monitorprovider/ — Provider-neutral monitoring topology, diagnostics, coverage, cursors, and the governed provider registry (dispatch through it; never compare provider names)
  appdynamics/   — Classic AppDynamics Controller and Events Service client, governed discovery and typed test monitoring
  dynatrace/     — Dynatrace SaaS Classic and Grail client, governed discovery, bounded queries and typed test monitoring
  Java execution stack:
  javarunner/    — Shared policy-enforcing Java Vuser primitives (classpath, command, containment);
                   deliberately depends on neither dsl nor protocol so authoring and execution share it
  javasidecar/   — Credential-minimized loopback HTTP contract to the external Java Vuser runner container
  Secrets, credentials, and disclosure:
  secretstore/   — Project-scoped secret providers (env, Azure Key Vault, AWS Secrets Manager) + migration
  keywrap/       — Protocol-neutral envelope-key wrapping (master key, Azure/AWS KMS providers)
  credential/    — Source-neutral credential naming/classification shared by importers, scrub, and paramize
  scriptdisclosure/ — Irreversible disclosure-safe projections of executable scripts
  identitypolicy/ — Binds process credentials to operator-approved remote identities before token use
  netpolicy/     — Shared outbound-network trust decisions (public-IP / SSRF guard)
  Import and asset plumbing:
  dslwalk/       — Bounded deterministic traversal of every action incl. nested control flow
  importsession/ — Importer-neutral protected policy-governed inspection sessions
  importartifact/ — Content-addressed, encrypted immutable source bundles for governed runners
  fileasset/     — Bounded local file/asset opening for import paths
  internal/      — Cross-package internal guards: archiveingest, archivequota, boundedcache, boundedtext, diskspace,
                   filelock, subprocessguard, zipbounds
api/
  proto/         — gRPC protobuf definitions (controller ↔ generator)
dashboard/       — React 18 + TypeScript + Recharts SPA
  src/components/ — Header, TestHub, TestExecution, StatusBadge, CreateTestModal
  src/hooks/      — useWebSocket (real-time), useApi (REST client)
desktop/         — Wails native client shell embedding the controller API + dashboard
mcp/             — Python MCP server exposing the REST API as agent tools
deployments/
  k8s/           — Kubernetes manifests (Kustomize overlays: local, local-kind, onprem, aks, eks, plus `*-ha`, `*-prometheus`, and `*-java-vuser` variants)
  helm/          — Helm chart (apexload) for Kubernetes/AKS enterprise installs
  docker/        — docker-compose stack for local multi-service runs
  azure/
    terraform/   — Azure IaC (AKS, ACR, PostgreSQL, Redis, Blob, Key Vault)
    appservice/  — Node.js launcher wrapper for Azure App Service deployments
  aws/
    terraform/   — AWS IaC (EKS, ECR, RDS, ElastiCache, S3, EFS, KMS)
.github/workflows/ — Authoritative CI/CD workflows (repo root)
```

The `pkg/` listing above is machine-checked: `check_package_inventory` in
`scripts/check-docs.py` fails the CI documentation job if a top-level `pkg/`
directory has no entry line of its own in the `pkg/` section of this block, so a
new package means a new line here. Only that section counts, and only a line
whose first token is the directory name — a mention elsewhere in the fence
(`cmd/controller/`, the top-level `api/` tree) or a nested entry like
`api/static/` does not stand in for the package's own line.

## Key Design Decisions
- 1 goroutine = 1 VUser (~2–4 KB stack)
- Metrics via buffered channels (cap 10,000) — zero-lock hot path
- Protocol plugins compiled in (not Go runtime plugins)
- `sync.Pool` for request/response buffers to reduce GC pressure
- t-digest for streaming percentile computation
- JSON DSL scripts — no proprietary language
- Prometheus scrape metrics, W3C `traceparent` propagation, Dynatrace event webhooks, and opt-in OTLP control-plane/action traces; OpenTelemetry metrics/log export remains out of scope
- Real-time Network Latency monitoring via concurrent TCP dialer
- "Soft Pause" execution gate (pauses between actions to preserve transaction integrity)
- Manual timer-based duration management to support robust pause/resume

## Protocol Interface
All protocols implement `pkg/protocol.Protocol`. Register new protocols in `pkg/protocol/registry.go`.
Bundled and compiled into the standard binaries via `pkg/protocol/builtin`: HTTP (http/1.1, http/2),
SOAP (+ WSDL import), Java-over-HTTP (`java_http`, bounded codec MVP), Java Vuser sidecar bridge
(`java_vuser`, external-runner; governed and disabled unless runner config enables it), WebSocket,
gRPC, JMS Solace, JMS IBM MQ, JMS Azure Service Bus (real adapter opt-in via
`-tags jms_servicebus_real`), Kafka (produce/consume/request-reply; in-memory mock broker by default,
real franz-go network adapter opt-in via `-tags kafka_real`; Event Hubs Kafka-endpoint via
`config.kafka.profile: eventhubs`), Azure Event Hubs (native `eventhubs` protocol, producer-first:
`eventhubs_produce` + observer-probe `eventhubs_consume`; in-memory mock by default, real
azeventhubs/AMQP adapter opt-in via `-tags eventhubs_real`), database (SQL query/exec/transactions),
network (DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP), and browser (replay + canary diagnostics).
Bruno execution is also bundled through the version-pinned Node compatibility runner in
`runners/bruno`; it is egress-governed and remains disabled unless its runner policy is configured.
`sap_gui` actions are authorable/importable via the DSL catalog but have no registered runtime
protocol and cannot be executed.

## Script DSL
JSON-based. Template namespaces: `{{run.id}}` (managed test ID), `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`,
`{{param.name}}`, `{{secret.name}}`, plus built-in functions under `{{fn.*}}`/`{{fake.*}}`
(e.g. `fn.uuid()`, `fn.timestamp.unix()`, `fn.timestamp.iso8601()`, `fn.random(min,max)`, `fn.random_string(n)`, `fake.email()`).
Sections: config, data_sources, init, actions, teardown.
The per-action-type contract is declared in the action catalog (`pkg/dsl/catalog*.go`), which drives
validation, dashboard editor metadata, `GET /api/v1/dsl/actions`, and the generated artifacts
`docs/dsl.schema.json` plus `docs/DSL_ACTION_REFERENCE.md`. After catalog changes, run
`go generate ./pkg/dsl`; freshness is test-enforced.

## REST API
The controller binary serves a REST API on `127.0.0.1:8080` locally (`:8080` when `APEXLOAD_ENV=production|prod`; configurable via `APEXLOAD_ADDR`).
The OpenAPI spec `docs/swagger.yaml` and endpoint catalog `docs/api-endpoint-catalog.json` are
generated from the controller routing layer by `cmd/swaggergen`; do not hand-edit them. After route
changes, run `go generate ./pkg/api`; freshness is test-enforced by `pkg/api`.

Use the dashboard Swagger page, `GET /api/v1/docs/swagger.yaml`, or
`GET /api/v1/docs/endpoint-catalog` for the complete current route list. Repository prose should
describe workflows and link to those generated artifacts instead of duplicating endpoint catalogs.

## Testing Policy
Every new feature, bug fix, or refactor **must** include corresponding tests. This is a default
requirement — not optional. When adding or modifying code:
- Add unit tests for new functions and methods in `*_test.go` files alongside the source.
- Cover both the happy path and meaningful edge cases (empty inputs, error conditions, boundary values).
- During Go development, run the tests for the affected packages for fast feedback. The full `go test ./...` suite remains a required CI merge gate; run it locally before merge when the change crosses package boundaries, affects shared infrastructure, or is likely to change behavior outside the directly affected packages.
- If a change touches the dashboard, verify UI behavior in a browser (type checking alone is insufficient).
- During dashboard development, run typecheck, lint, relevant unit tests, and the affected Playwright workflows in each impacted auth profile. The full dashboard checks remain a required CI merge gate; CI builds the embedded dashboard and runs both dev-admin and local-auth Playwright profiles. Run `cd dashboard && npm run test:cycle` locally before merge for broad UI changes or changes to shared workflows, authentication, routing, or build configuration. Do not replace browser verification with route-only smoke checks.
- Test files live next to the code they test (e.g. `pkg/engine/metrics_test.go` tests `metrics.go`).
- Use table-driven tests where multiple input/output combinations exist.
- Do not mock internal packages unless required for isolation; prefer testing against real implementations.

## Build & Run
```bash
# Build default binaries into ./bin (generator, controller, apexctl, record-agent, collector-agent)
make build

# Run controller with API server + dashboard
./bin/controller    # serves on 127.0.0.1:8080 locally

# Run a test via CLI (direct, no controller)
./bin/apexctl run --script order-flow.json --vusers 100 --duration 5m

# Dashboard dev server (proxies /api to :8080)
cd dashboard && npm install && npm run dev   # http://localhost:4000

# Build dashboard into Go binary (embedded)
cd dashboard && npm run build   # outputs to pkg/api/static/

# Run tests
go test ./...
```

## Build Cache Cleanup
Agents **must** clean up local and Docker build caches once all tasks in their session
that need those caches are complete, including builds, tests, browser verification,
and any follow-up fixes. Perform cleanup before reporting the session work complete;
retain caches while any dependent work remains.

- Clear Go build and test caches with `go clean -cache -testcache`, using the same cache configuration as the session's builds and tests.
- Remove disposable frontend build caches used during the session, such as `dashboard/node_modules/.vite` and `dashboard/node_modules/.vite-temp`, plus any session-created build-cache directories.
- Prune unused Docker build cache with `docker builder prune --all --force`; for separate Buildx builders used during the session, use `docker buildx prune --builder <name> --all --force`.
- Preserve source changes, dependencies (`node_modules`, Go module downloads), deliverable build outputs, images, containers, volumes, and application data. Do not use `docker system prune` for this cleanup.
- Do not clear shared local caches while another active build or agent still needs them. Defer that cleanup until the shared work is complete, and report any deferred or permission-blocked cleanup in the final response.
- Verify cleanup completed and briefly report the result, including reclaimed space when available.

## Performance Targets
These are design targets, not yet backed by published benchmark artifacts. Do not cite as achieved.
- Harness overhead per transaction: < 50 μs
- VUsers per pod (HTTP): 10,000 on 4 vCPU / 8 GB
- Metrics latency (generator → dashboard): < 2 seconds
- Ramp-up rate: 500 VUsers/second/pod

## Tech Stack
Go 1.26+ | SQLite (default) / PostgreSQL 16 (optional) | Redis/Valkey (optional, HA) | React 18 + TS | Prometheus + gRPC | AKS | Helm | Terraform
