# Development Guide

## Project Structure

```
apexload/
├── cmd/                     # Executable entry points (controller, generator, apexctl, apexbench,
│                            #   apexload-collector-agent, apexload-java-vuser-runner,
│                            #   apexload-record-agent, dslschema, openapits, swaggergen, webcoverage)
├── pkg/                     # Reusable Go packages (engine, protocol, dsl, api, config, controller,
│                            #   distributed, storage, recorder, convert, and more)
├── api/proto/            # Protocol Buffer definitions (gRPC)
├── dashboard/            # React 18 + TypeScript SPA
├── desktop/             # Wails native client shell (embeds controller API + dashboard)
├── mcp/                 # Python MCP server exposing the REST API as agent tools
├── deployments/          # Kustomize manifests, Helm chart, Docker Compose, Azure/AWS Terraform, CI templates
└── examples/            # Sample scripts, HAR files
```

See [AGENTS.md](AGENTS.md) for the complete `cmd/` and `pkg/` inventory.

## Go Packages Overview

### `pkg/engine`

The **VUser execution engine** — the heart of load generation.

**Key Types:**
- `Engine`: Main orchestrator; manages goroutine lifecycle, pacing, metrics collection
- `VUser`: Individual goroutine representing a single user
- `MetricsCollector`: Aggregates latency, throughput, errors; produces t-digest percentiles and exposes dropped-result telemetry so hot-path loss is visible
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
- **HTTP**: Supports HTTP/1.1 and HTTP/2; handles redirects, cookies, extractors, assertions
- **SOAP**: Reuses the HTTP transport with SOAP-specific defaults, namespaces, and fault-handling semantics
- **Java over HTTP** (`java_http`): Reuses the HTTP transport with the bounded `java_serialized_object_base64` codec for opaque serialized payloads
- **Java Vuser** (`java_vuser`): External-runner sidecar bridge for JVM Vuser scripts; governed and disabled unless runner config enables it
- **Bruno** (`bruno`): Version-pinned compatibility runner for imported Bruno collections; policy-gated and disabled until configured
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

The controller, generator, and `apexctl` binaries bundle all of the above through
`pkg/protocol/builtin`.

**Adding a New Protocol:**
1. Create `pkg/protocol/myproto/` package
2. Implement the `Protocol` interface
3. Register a per-VUser factory from the protocol package's `init`: `protocol.Register("myproto", func() protocol.Protocol { return &MyProtocol{} })`
4. Import the package from `pkg/protocol/builtin` if it must be executable from the standard binaries

### `pkg/dsl`

**DSL parser** converts JSON scripts to runtime `Script` structures.

**Script Sections:**
- **config**: Global settings (`base_url`, think time, pacing, and protocol-specific blocks such as `http`, `browser`, `grpc`)
- **data_sources**: CSV and DAT files for row-based data-driven testing
- **parameters**: named generated values and file/list-backed bindings for VuGen-style parameterization
- **init**: Setup actions (login, session init)
- **actions**: Main test flow (define named transactions)
- **teardown**: Cleanup (logout, resource release)

**Template Variables:**
- `{{env.VARIABLE}}` — environment variables
- `{{data.source_name.column}}` — data source columns
- `{{param.name}}` — named script parameters
- `{{var.name}}` — runtime variables
- `{{fn.uuid()}}`, `{{fn.random(1,100)}}`, `{{fn.timestamp.iso8601}}`, `{{fn.random_string(10)}}` — functions

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
- `MetricsCollector`: Channels for sample ingestion, t-digest for percentile tracking

**Streaming Percentile Algorithm:**
Uses t-digest (centroid-based) to compute accurate percentiles without storing all latency samples. Trade-off: Memory-efficient but slightly lossy compared to exact percentile computation.

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
- Compatibility landing page: [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md)

After route changes, run:

```bash
go generate ./pkg/api
go test ./pkg/api
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
# Run all tests with race detector (as CI does)
make test   # go test -race -count=1 ./...

# Run specific package
go test ./pkg/engine

# Run with coverage
go test -cover ./...

# Full coverage report
make test-cover
```

**Frontend Tests:**
```bash
cd dashboard
npm run test
```

### 4. Building for Production

**Build Go binaries:**
```bash
make build
# Outputs to ./bin/controller, ./bin/generator, ./bin/apexctl,
# ./bin/apexload-record-agent, and ./bin/apexload-collector-agent
```

Other useful targets: `make generate` (regenerate DSL schema and OpenAPI artifacts), `make desktop` / `make clients` (Wails desktop app), `make java-vuser-runner`, `make performance-smoke`, and the `k8s-*` deployment targets (see [deployments/README.md](deployments/README.md)).

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
- **Typing**: Avoid `any`; use TypeScript interfaces from `types.ts`
- **CSS modules** or inline `style` props; class names with kebab-case

## Performance Considerations

### Backend

- **Goroutine scaling**: 1 VUser = 1 goroutine (~2–4 KB stack). 10,000 VUsers = ~40–80 MB overhead.
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

## Common Issues & Solutions

### Dashboard not updating after backend changes?
- If serving from controller: rebuild dashboard (`cd dashboard && npm run build`), restart controller
- If using dev server: changes hot-reload; just refresh browser

### Metrics not appearing in dashboard?
- Check WebSocket connection: `WS /api/v1/ws` should be connected
- Verify controller logs for errors: `grep -i error` on controller output
- Check browser console for JavaScript errors

### Memory usage too high during test?
- Reduce VUsers (each is a goroutine)
- Check for goroutine leaks: use `pprof` profiling
- Verify protocol implementation isn't buffering large responses

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

## References

- **AGENTS.md**: High-level architecture and tech stack
- **CLAUDE.md**: Compatibility pointer to AGENTS.md
- **pkg/dsl/parser.go**: DSL parsing logic
- **pkg/api/server.go**: Route setup and middleware
