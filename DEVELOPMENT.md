# Development Guide

## Project Structure

```
apexload/
├── cmd/                     # Runtime binaries, agents, and all 6 Go code generators (see AGENTS.md)
├── pkg/                     # Reusable Go packages (engine, protocol, dsl, api, storage, rca, etc.)
├── api/proto/               # Protocol Buffer definitions for controller ↔ generator gRPC (see api/proto/README.md)
├── dashboard/               # React 18 + TypeScript SPA (see dashboard/README.md)
├── desktop/                 # Wails v2 native client shell embedding controller API + dashboard (see desktop/README.md)
├── mcp/                     # Python MCP server exposing the REST API as agent tools
├── deployments/             # Kustomize overlays, Helm chart, Docker Compose, Azure & AWS Terraform
├── examples/                # Sample JSON DSL scripts, CSV prompt feeds, raw .http fixtures, monitoring samples (see examples/README.md)
├── scripts/                 # CI checks, E2E harnesses, manifest renderers, and packaging scripts (see scripts/README.md)
└── .github/workflows/       # Authoritative CI/CD and release workflows
```

See [AGENTS.md](AGENTS.md) for the complete `cmd/` and `pkg/` inventory.

## Go Packages Overview

### `pkg/engine`

The **VUser execution engine** — the heart of load generation.

**Key Types:**
- `Engine`: Main orchestrator; manages goroutine lifecycle, pacing, metrics collection
- `VUser`: Individual goroutine representing a single user
- `MetricsCollector`: Aggregates latency, throughput, errors; produces duration-sketch percentiles and exposes dropped-result telemetry so hot-path loss is visible
- `dsl.PacingConfig`: Per-script pacing between iterations (`none`, `fixed_delay`, `random_delay`, `fixed_interval`, `random_interval`, `tps`); the engine enforces it (see `pkg/engine/pacing.go`)

**Hot Path Behavior:**
- Metrics use **buffered channels** (capacity 10,000) for zero-lock collection
- Buffered metrics that overflow are dropped instead of blocking VUsers; drop counts must be monitored in live telemetry
- VUser goroutines run idle wait loops on short-lived contexts
- Sync.Pool reuses request/response buffers to reduce GC pressure

**Example Usage:**
```go
cfg := engine.Config{
    Script:   script,
    VUsers:   100,
    Duration: 5 * time.Minute,
    RampUp:   30 * time.Second,
}
eng, err := engine.New(cfg)
result, err := eng.Run(ctx)
```

### `pkg/protocol`

**Protocol abstraction layer** for extensibility. All protocols implement the `Protocol` interface:

```go
type Protocol interface {
    Name() string
    Init(ctx context.Context, config map[string]any) error
    Execute(ctx context.Context, action *Action) (*Result, error)
    Teardown(ctx context.Context) error
    HealthCheck(ctx context.Context) error
}
```

**Bundled Protocols** (registered via `init()` when their package is imported in a binary's `main.go`):
- **HTTP**: Supports HTTP/1.1, HTTP/2 and, as a preview where `APEXLOAD_HTTP3=true`, exact HTTP/3 and the `auto+h3` Alt-Svc upgrade; `http` negotiates while `http/1.1`, `http/2` and `http/3` supply exact inherited defaults. Request `http_version` may inherit or select `auto`, `1.1`, `2`, `auto+h3` or `3`. Handles redirects, cookies, extractors, assertions
- **SOAP**: Reuses the HTTP transport with SOAP-specific defaults, namespaces, and fault-handling semantics
- **Java over HTTP** (`java_http`): Reuses the HTTP transport with the bounded `java_serialized_object_base64` codec for opaque serialized payloads
- **Java Vuser** (`java_vuser`): External-runner sidecar bridge for JVM Vuser scripts; governed and disabled unless runner config enables it
- **WebSocket**: Connect, send/receive, configurable timeouts and max message size
- **gRPC**: Reflection-based dynamic protobuf marshaling, TLS, metadata, streaming
- **JMS (Solace)** (`jms-solace`): Message queue protocol
- **JMS (IBM MQ)** (`jms-ibmmq`): Message queue protocol
- **JMS (Azure Service Bus)** (`jms-servicebus`): Message queue protocol; real adapter opt-in via `-tags jms_servicebus_real`
- **Kafka**: produce/consume/request-reply; in-memory mock broker by default, real franz-go network adapter opt-in via `-tags kafka_real`
- **Azure Event Hubs** (`eventhubs`): Native producer-first protocol with observer consumption; real AMQP adapter opt-in via `-tags eventhubs_real`
- **database**: SQL query/exec/transactions
- **network**: DNS/TCP/UDP/FTP/SMTP/POP3/IMAP/LDAP
- **browser**: Playwright replay + canary diagnostics (FCP/LCP/CLS/INP/TTFB)
- **appium**: Device observer; drives one real device or simulator through an Appium server's W3C WebDriver API (tap, type, wait, app launch, mobile commands, screenshots)

The controller, generator, and `apexctl` binaries bundle all of the above through
`pkg/protocol/builtin`. Bruno Runner is retired under `_retired/bruno-runner` and excluded from builds; native Bruno HTTP collection import remains built into `pkg/convert`.

**Adding a New Protocol:**
1. Create `pkg/protocol/myproto/` package
2. Implement the `Protocol` interface
3. Register a per-VUser factory from the protocol package's `init`: `protocol.Register("myproto", func() protocol.Protocol { return &MyProtocol{} })`
4. Import the package from `pkg/protocol/builtin` if it must be executable from the standard binaries

### `pkg/dsl`

**DSL parser** converts JSON scripts to runtime `Script` structures.

**Script Sections:**
- **config**: Global settings (`base_url`, think time, pacing, and protocol-specific blocks such as `http`, `browser`, `grpc`)
- **data_sources**: `"csv"`, `"dat"`, and `"file"` sources for row-based or payload-backed data-driven testing
- **parameters**: named generated values and file/list-backed bindings for VuGen-style parameterization
- **init**: Setup actions (login, session init)
- **actions**: Main test flow (define named transactions)
- **teardown**: Cleanup (logout, resource release)

**Template Variables:**
- `{{run.id}}` — managed test run ID
- `{{env.VARIABLE}}` — allowlisted environment variables (`APEXLOAD_SCRIPT_ENV_ALLOWLIST`) or CLI `--env` overrides
- `{{data.source_name.column}}` — data source columns
- `{{param.name}}` — named script parameters
- `{{var.name}}` — runtime extracted variables
- `{{secret.name}}` — project secret bindings
- `{{file.name}}` — `"file"` data-source contents
- `{{fn.*}}` / `{{fake.*}}` (e.g. `{{fn.uuid()}}`, `{{fn.random(1,100)}}`, `{{fn.timestamp.iso8601()}}`, `{{fn.random_string(10)}}`, `{{fake.email()}}`) — built-in functions

**Example Script:**
```json
{
  "script": {
    "name": "Browse_Products",
    "version": "1.0.0",
    "protocol": "http",
    "config": { "base_url": "https://api.example.com" },
    "actions": [
      { "name": "browse_products", "type": "GET", "target": "/products" }
    ]
  }
}
```

`protocol` is set on the top-level `script` object, and HTTP actions use the method as `type`. See the full skeleton in [README.md](README.md#dsl-script-structure) and `examples/simple-http.json`.

### `pkg/api`

**REST API and WebSocket server** — the controller's interface.

**Key Components:**
- `server.go`: HTTP server setup, route registration
- `handlers.go`: RESTful endpoints (scripts, tests, projects, health), including test start (`handleStartTest`); start/queueing tests live in `handlers_start_test_test.go`
- `websocket.go`: Real-time metrics streaming (JSON with `type` field)
- `runner.go`: Test orchestration loop and in-memory distributed execution state
- `distributed_execution_backend.go`: Controller-side remote generator orchestration, metrics aggregation, archive staging

**Distributed Execution Notes:**
- The controller stays on the same HTTP/WebSocket contract in both local and distributed modes.
- Distributed mode is enabled by passing `GeneratorTargets` into `api.NewServer`, which `cmd/controller` currently sources from `APEXLOAD_GENERATORS`.
- Group fan-out, remote control routing, and remote archive staging all live behind the `testExecutionBackend` seam so handlers do not branch on local vs distributed execution.
- Synthetic distributed transport tests and real remote execution tests both live in `pkg/api/execution_backend_test.go`.

**WebSocket Message Types:**
```
metrics_update    — periodic metrics snapshot
telemetry_diagnostic — sampled telemetry degradation/recovery events
test_created      — test queued/initialized
test_started      — test moved to running state
test_complete     — test finished successfully
test_failed       — test encountered error
test_stopping     — graceful stop initiated
```

Live execution telemetry preserves generator collection and publish timestamps for distributed runs and stamps controller receipt and publish times, so the dashboard can show generator lag, transport lag, controller fan-out lag, client delivery lag, and end-to-end freshness separately from websocket drop pressure.
When telemetry becomes stale or starts dropping samples/messages, the controller emits sampled `telemetry_diagnostic` events so Monitoring can show degradation details without inflating the main live metrics envelope. These events include bounded slow/error transaction hotspot samples derived from live transaction rollups.
Interval history is persisted as typed summary rows carrying an explicit schema version; store initialization backfills legacy typed summary and archive rows, and the latest schema version also persists distributed lag-stage timestamps. Distributed generator batches carry generator-side telemetry fields so dropped-result and freshness signals survive remote aggregation.
HTTP and JMS results stamp first-class correlation IDs plus dependency type/target metadata, which the engine rolls into bounded live transaction summaries and the archive layer preserves in JSONL, Parquet, and archive query filters.

### `pkg/engine` metrics (`pkg/engine/metrics.go`)

**Metrics aggregation** with streaming percentiles.

**Key Types:**
- `MetricsSnapshot`: Point-in-time metrics (count, latency p50/p95/p99, errors)
- `MetricsCollector`: Channels for sample ingestion, a duration sketch for percentile tracking

**Streaming Percentile Algorithm:**
Uses a duration sketch (`engine.DurationSketch`, logarithmic buckets in the style of DDSketch) to compute percentiles without storing all latency samples. Every percentile is within 0.5% of the value at its rank, whatever the distribution; count, sum, minimum and maximum are exact, and merging sketches from generators is exact. A sketch holds at most 4,096 buckets (32 KiB). For one release, generators and archives also carry a t-digest for older peers.

## Frontend Architecture

### Dashboard Directory Structure

```
dashboard/
├── src/
│   ├── components/        # React components by page/feature
│   │   ├── Sidebar.tsx                — Navigation sidebar
│   │   ├── ScriptsPage.tsx           — Script management + authoring
│   │   ├── ScriptsAuthoringSurface.tsx — Shared script import/editor surface
│   │   ├── TestHub.tsx               — Scenarios and test execution
│   │   ├── ResultsPage.tsx           — Historical results + comparison
│   │   ├── CreateTestModal.tsx       — Test configuration UI
│   │   └── StatusBadge.tsx           — Status indicator component
│   ├── hooks/
│   │   ├── useApi.ts                 — REST client (fetch + caching)
│   │   └── useWebSocket.ts           — WebSocket connection to /api/v1/ws
│   ├── types.ts                      — TypeScript interfaces (Script, Test, Result, etc.)
│   ├── App.tsx                       — Main router + page layout
│   ├── main.tsx                      — Vite entry point
│   └── index.css                     — Global styles
├── vite.config.ts                    — Bundler configuration
├── tsconfig.json                     — TypeScript compiler options
├── package.json                      — Dependencies (React, Vite, Recharts)
└── public/                           — Static assets

```

### Key React Components

**ScriptsPage.tsx**
- Script library and editor interface
- Displays local and federated scripts with health, release, and source filters
- Actions: Create new drafts, open sample imports, import from LoadRunner/JMeter, edit inline, delete

**ScriptsAuthoringSurface.tsx**
- Hosts the shared import, recording, and editor workflows
- Supplies a destination-folder selector for new imported or recorded scripts

**TestHub.tsx**
- Scenario (test configuration) management and execution
- Shows scenarios with VUser count, run count, and StatusBadge
- Real-time test execution monitoring with live metrics via WebSocket

**ResultsPage.tsx**
- Historical test results with comparison feature
- Checkbox selection for multi-result comparison
- Archive and delete actions inline

### Styling

- **CSS Variables**: `--bg-primary`, `--text`, `--border`, `--danger` (defined in `index.css`)
- **Handwritten CSS**: component styles and theme tokens are implemented directly with the CSS variables in `dashboard/src/index.css`; Tailwind and DaisyUI are not dependencies
- **Recharts**: Time-series charting for metrics visualization

## API Documentation

The REST API surface is generated from the controller routing layer. Do not add
hand-maintained endpoint catalogs here; they drift from `pkg/api/server.go` and
`pkg/api/route_catalog.go`.

- Generated OpenAPI: [docs/swagger.yaml](docs/swagger.yaml)
- Generated endpoint catalog: [docs/api-endpoint-catalog.json](docs/api-endpoint-catalog.json)
- Operator workflow guide: [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)
- Runtime docs endpoints: `GET /api/v1/docs/swagger.yaml` and `GET /api/v1/docs/endpoint-catalog`

The OpenAPI file and endpoint catalog are generated by `cmd/swaggergen` and the dashboard TypeScript contract (`dashboard/src/types/generated/openapi.ts`) by `cmd/openapits` through `go generate ./pkg/api` (which also runs `cmd/storeoverlay` for `pkg/api/store_overlay_test.go`); never hand-edit them. The DSL schema ([docs/dsl.schema.json](docs/dsl.schema.json)) and action reference ([docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md)) are generated from `pkg/dsl/catalog*.go` by `cmd/dslschema` (`go generate ./pkg/dsl`); `cmd/datamodeldoc` generates [docs/DATA_MODEL.md](docs/DATA_MODEL.md) (`go generate ./pkg/storage`), and `cmd/webcoverage` generates [docs/LOADRUNNER_COVERAGE.md](docs/LOADRUNNER_COVERAGE.md) (`go generate ./pkg/convert/enterprisearchive`). Running `make generate` (`go generate ./...`) invokes all six generators. Script lifecycle overview: [docs/SCRIPT_LIFECYCLE.md](docs/SCRIPT_LIFECYCLE.md).

After route or DSL catalog changes, run:

```bash
go generate ./pkg/api
go generate ./pkg/dsl
go test ./pkg/api ./pkg/dsl
python3 scripts/check-docs.py
```

## Distributed Runtime Commands

```bash
# Controller in local mode
go run ./cmd/controller

# Controller in distributed mode
APEXLOAD_GENERATORS=127.0.0.1:9090,127.0.0.1:9091 \
APEXLOAD_GENERATOR_TIMEOUT=10s \
go run ./cmd/controller

# Generators — one process per configured target address, each with a distinct bind address
APEXLOAD_GENERATOR_LISTEN_ADDR=127.0.0.1:9090 go run ./cmd/generator
APEXLOAD_GENERATOR_LISTEN_ADDR=127.0.0.1:9091 go run ./cmd/generator
```

Current distributed test coverage is centered on:

- remote start/stop/pause/resume/duration control
- multi-generator group fan-out
- real remote execution and scale routing
- remote archive fetch and controller-local archive merge

For current route names, access rules, and generated summaries, use the generated docs above.

## Development Workflow

### 1. Running Locally

**Terminal 1: Start the backend**
```bash
go run ./cmd/controller
# Listens on 127.0.0.1:8080, serves dashboard from pkg/api/static/
```

**Terminal 2: Start the dashboard dev server** (optional for frontend dev)
```bash
cd dashboard
npm install
npm run dev
# Starts Vite dev server on :4000 with API proxy to :8080
```

**Windows local helper** (optional)
```powershell
.\scripts\start-local-windows.ps1

# Open directly to recorder authoring and manage Windows recorder proxy state
.\scripts\start-local-windows.ps1 -ConfigureRecorderProxy -DashboardRoute '#/scripts?mode=create'
```

### Enterprise npm and network defaults

- `dashboard/.npmrc` is an optional, local-only file (gitignored) for an enterprise npm registry; create it only when your environment requires one.
- `.env.example` is gitignored (it carries organization-specific proxy/registry defaults), so it is not in the repository. Create a local `.env` from your organization's approved non-secret proxy, CA-path, and endpoint defaults.
- `cmd/controller`, `cmd/generator`, and `cmd/apexctl` auto-load `.env` from the working directory at startup via `config.LoadDotEnv`. Values never override variables already in the environment, and loading is skipped when `APEXLOAD_ENV=production|prod`.
- Helper scripts, `npm`, Docker, and the MCP client do not read `.env`; export values into your shell or CI job before running them.

### 2. Making Changes

**Backend (Go):**
- Edit files in `pkg/`, `cmd/`
- Run tests: `go test ./...`
- Rebuild controller: `go build ./cmd/controller` (or just run `go run ./cmd/controller` again)

**Frontend (React/TypeScript):**
- Edit files in `dashboard/src/`
- If using dev server: changes hot-reload automatically
- If serving from controller: rebuild with `cd dashboard && npm run build`, restart controller

### 3. Testing

**Go Tests:**
```bash
# Run all tests as CI does: third_party/playwright-go fork tests + repo-wide race suite
make test   # (cd third_party/playwright-go && go test -race -count=1 ./...) && go test -race -count=1 -timeout=20m ./...

# Run specific package
go test ./pkg/engine

# Run with coverage
go test -timeout=20m -cover ./...

# Full coverage report
make test-cover
```

**Frontend Tests & Verification:**
```bash
cd dashboard
npm run typecheck
npm run lint
npm run test
```

See [dashboard/README.md](dashboard/README.md) for the complete npm script matrix (`dev`, `build`, `lint:a11y`, `test:workflows`, `test:cycle`, `contracts:check`, `bundle:check`, and the `dev-admin` / `local-auth` Playwright auth profiles).

### 4. Building for Production

**Build Go binaries:**
```bash
make build
# Outputs to ./bin/controller, ./bin/generator, ./bin/apexctl,
# ./bin/apexload-record-agent, and ./bin/apexload-collector-agent
```

Other useful targets: `make generate` (runs all 6 Go code generators: `dslschema`, `swaggergen`, `openapits`, `datamodeldoc`, `webcoverage`, `storeoverlay`), `make desktop` / `make clients` (Wails desktop app; see [desktop/README.md](desktop/README.md)), `make java-vuser-runner`, `make performance-smoke`, and the `k8s-*` deployment targets (see [deployments/README.md](deployments/README.md)).

**Package the production Recording Agent:**
```bash
make package-recording-agent
# Outputs release archives to ./dist/recording-agent
```

**Build dashboard for embedding:**
```bash
cd dashboard
npm run build
# Outputs to ../pkg/api/static/ (embedded into controller binary)
```

## Code Conventions

### Go

- **Package organization**: Related types and functions in same file; `*_test.go` files alongside source
- **Comments**: Exported functions (capitalized) must have doc comments: `// FunctionName does X.`
- **Error handling**: Return `error` as last return value; wrap with `fmt.Errorf("context: %w", err)`
- **Tests**: Table-driven tests preferred for multiple scenarios; always test happy path + edge cases
- **Logging**: Use `log/slog` with structured fields (`.With("key", value)`)

### TypeScript/React

- **Component names**: PascalCase; default export per file
- **Hooks**: Prefix with `use` (e.g., `useWebSocket`, `useApi`)
- **Props interfaces**: `ComponentNameProps` interface in same file
- **Typing**: Avoid `any`; use TypeScript interfaces from `types.ts` and generated contracts (`src/types/generated/openapi.ts`)
- **Standard `.css` stylesheets**: plain `.css` files live next to components alongside global design tokens in `dashboard/src/index.css` using kebab-case class names (CSS Modules, Tailwind, and DaisyUI are not used)

## Performance Considerations

### Backend

- **Goroutine scaling**: 1 VUser = 1 goroutine, parked on an 8 KB stack between actions. The `pkg/perfevidence` footprint harness measures an idle VUser at about 12 KB of Go memory (heap and stack) with no protocol and about 41 KB with HTTP on macOS; process RSS per VUser is higher.
- **Metrics hot path**: Buffered channel (10,000) avoids locks; aggregated periodically.
- **Database**: SQLite is adequate for single-controller metadata. Running multiple controller replicas requires `APEXLOAD_CONTROLLER_HA`, PostgreSQL, Redis/Valkey, and shared archive storage (see [ARCHITECTURE.md](ARCHITECTURE.md)).
- **Memory**: Reuse buffers via `sync.Pool` to reduce GC pressure during high-throughput runs.

### Frontend

- **Data fetching**: Use `useApi` hook for caching; avoid refetching unchanged data.
- **Real-time updates**: WebSocket with smart reconnect logic; dashboard stays responsive even if connection drops.
- **Chart rendering**: Recharts memoizes large datasets; consider pagination for very long result histories.

## Testing Policy

**All new features and bug fixes MUST include corresponding tests.**

- **Go**: Unit tests in `*_test.go` files alongside source; CI publishes a coverage baseline report (the ratchet in `.github/workflows/ci.yml` is currently report-only), so avoid lowering coverage in touched packages
- **React**: Component tests using React Testing Library; test user interactions, not implementation details
- **Edge cases**: Empty inputs, error conditions, boundary values, concurrency scenarios

Example:
```go
// table-driven test for engine pacing
func TestPacing(t *testing.T) {
    tests := []struct {
        name     string
        vusers   int
        rampUp   time.Duration
        expected time.Duration
    }{
        {"instant", 100, 0, 0},
        {"linear ramp", 100, 10 * time.Second, 10 * time.Second},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test body
        })
    }
}
```

## Troubleshooting

For controller startup errors, SQLite lock diagnosis, dashboard asset embedding, WebSocket stream health, and distributed generator issues, see [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## Useful Commands

```bash
# Start the opt-in loopback-only pprof listener
APEXLOAD_PPROF_ADDR=127.0.0.1:6060 go run ./cmd/controller

# From another terminal, capture a 30-second CPU profile and a heap snapshot
curl -fsS -o cpu.prof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'
curl -fsS -o heap.prof 'http://127.0.0.1:6060/debug/pprof/heap'

# Analyze profile
go tool pprof cpu.prof
go tool pprof heap.prof

# Run linter
golangci-lint run ./...

# Format code
go fmt ./...

# Benchmark engine performance
go test -run='^$' -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/

# Or run the apexbench smoke profile
make performance-smoke

# Generate test coverage HTML
go test -cover -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Quick Reference

Cheat-sheet for the commands above. Environment variables: the
[README Environment Variables section](README.md#environment-variables) is
canonical; the ones you need most often are `APEXLOAD_ADDR` (HTTP bind address,
`127.0.0.1:8080` locally), `APEXLOAD_DB` (SQLite path, default `apexload.db`),
`APEXLOAD_GENERATORS` (generator targets; switches the controller to the
distributed backend), `APEXLOAD_GENERATOR_LISTEN_ADDR` (generator bind address,
default `:9090`; each generator on one host needs a distinct address), and
`APEXLOAD_LOG_LEVEL` (`debug`, `info`, `warn`, `error`). CLI flags and subcommands
are in the README [CLI Usage](README.md#cli-usage) section.

```bash
# Start on a custom port
APEXLOAD_ADDR=:9000 go run ./cmd/controller

# Run a test locally, no controller needed
go run ./cmd/apexctl run --script test.json --vusers 50 --duration 2m

# Frontend dev server with hot reload
cd dashboard && npm run dev

# Inspect or reset the local SQLite database
sqlite3 apexload.db "SELECT id, name, status FROM tests;"
cp apexload.db apexload.db.backup    # backup
rm apexload.db                        # reset; restart the controller to recreate
```

Useful URLs: dashboard `http://localhost:8080`, health
`http://localhost:8080/api/v1/system/health`, API docs in
[docs/swagger.yaml](docs/swagger.yaml) and [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md).

Files: `apexload.db` (SQLite, auto-created), `coverage.html` (after `make test-cover`),
`pkg/api/static/` (dashboard build output embedded by the controller; `npm run build`
empties it first), `examples/` (sample scripts, CSV feeds, raw `.http` fixtures, and monitoring configs; see [examples/README.md](examples/README.md)).

### DSL Authoring & Troubleshooting References

- **DSL authoring, parameters, think time, and correlation**: [docs/HELP_DSL_OPERATOR_GUIDE.md](docs/HELP_DSL_OPERATOR_GUIDE.md) and [README DSL Script Structure](README.md#dsl-script-structure)
- **Generated per-action DSL catalog & JSON Schema**: [docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md) and [docs/dsl.schema.json](docs/dsl.schema.json)
- **Protocol & opt-in broker build-tag matrix**: [docs/PROTOCOL_SUPPORT.md](docs/PROTOCOL_SUPPORT.md)
- **Troubleshooting common startup, script, UI, and runtime errors**: [TROUBLESHOOTING.md](TROUBLESHOOTING.md)

## References

- **[AGENTS.md](AGENTS.md)**: High-level architecture, machine-checked package inventory, and commit/testing policy
- **[ARCHITECTURE.md](ARCHITECTURE.md)**: Control-plane, distributed generator, and storage architecture
- **`pkg/dsl/parser.go`**: DSL parsing logic
- **`pkg/api/server.go`**: HTTP server setup, route registries, and middleware
