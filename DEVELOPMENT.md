# Development Guide

## Project Structure

```
stormrunner/
├── cmd/                     # Executable entry points
│   ├── controller/          # REST API + dashboard server (port 8080)
│   ├── generator/           # Distributed load generator (Kubernetes pod)
│   └── stormctl/           # CLI for local script execution
├── pkg/                    # Reusable packages
│   ├── engine/            # Core VUser engine, pacing, metrics
│   ├── protocol/          # Protocol interface + implementations (HTTP, JMS)
│   ├── dsl/              # Script parser and types
│   ├── api/              # REST handlers, WebSocket, test runner
│   ├── config/           # Configuration types
│   ├── controller/       # Test orchestration logic
│   ├── storage/          # SQLite persistence
│   ├── recorder/         # HTTP recording and HAR conversion
│   └── convert/          # LoadRunner migration tools
├── api/proto/            # Protocol Buffer definitions (gRPC)
├── dashboard/            # React 18 + TypeScript SPA
├── deployments/          # Kubernetes manifests, Helm charts
└── examples/            # Sample scripts, HAR files
```

## Go Packages Overview

### `pkg/engine`

The **VUser execution engine** — the heart of load generation.

**Key Types:**
- `Engine`: Main orchestrator; manages goroutine lifecycle, pacing, metrics collection
- `VUser`: Individual goroutine representing a single user
- `MetricsCollector`: Aggregates latency, throughput, errors; produces t-digest percentiles
- `Pacing`: Ramp-up scheduling (linear, stepped, immediate)

**Hot Path Behavior:**
- Metrics use **buffered channels** (capacity 10,000) for zero-lock collection
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

**Bundled Protocols:**
- **HTTP**: Supports HTTP/1.1 and HTTP/2; handles redirects, cookies, extractors, assertions
- **SOAP**: Reuses the HTTP transport with SOAP-specific defaults, namespaces, and fault-handling semantics
- **Java over HTTP**: Reuses the HTTP transport with the bounded `java_serialized_object_base64` codec for opaque serialized payloads
- **JMS (Solace)**: Message queue protocol
- **JMS (IBM MQ)**: Message queue protocol (Phase 5)

**Adding a New Protocol:**
1. Create `pkg/protocol/myproto/` package
2. Implement the `Protocol` interface
3. Register in `pkg/protocol/registry.go`: `RegisterProtocol(new(myProto.MyProtocol))`
4. Import it from the controller and generator binaries if it must be executable from both surfaces

### `pkg/dsl`

**DSL parser** converts JSON scripts to runtime `Script` structures.

**Script Sections:**
- **config**: Global settings (timeouts, SSL, connection pooling)
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
  "config": {
    "protocol": "http",
    "base_url": "https://api.example.com"
  },
  "init": [
    {
      "name": "login",
      "type": "http",
      "method": "POST",
      "target": "/auth/login",
      "body": "{\"user\": \"{{data.users.username}}\", \"pass\": \"{{data.users.password}}\"}"
    }
  ],
  "actions": [
    {
      "name": "browse_products",
      "type": "http",
      "method": "GET",
      "target": "/products"
    }
  ]
}
```

### `pkg/api`

**REST API and WebSocket server** — the controller's interface.

**Key Components:**
- `server.go`: HTTP server setup, route registration
- `handlers.go`: RESTful endpoints (scripts, tests, projects, health)
- `handlers_start_test.go`: Test queueing and execution
- `websocket.go`: Real-time metrics streaming (JSON with `type` field)
- `runner.go`: Test orchestration loop and in-memory distributed execution state
- `distributed_execution_backend.go`: Controller-side remote generator orchestration, metrics aggregation, archive staging

**Distributed Execution Notes:**
- The controller stays on the same HTTP/WebSocket contract in both local and distributed modes.
- Distributed mode is enabled by passing `GeneratorTargets` into `api.NewServer`, which `cmd/controller` currently sources from `STORMRUNNER_GENERATORS`.
- Group fan-out, remote control routing, and remote archive staging all live behind the `testExecutionBackend` seam so handlers do not branch on local vs distributed execution.
- Synthetic distributed transport tests and real remote execution tests both live in `pkg/api/execution_backend_test.go`.

**WebSocket Message Types:**
```
metrics_update    — periodic metrics snapshot
test_created      — test queued/initialized
test_started      — test moved to running state
test_complete     — test finished successfully
test_failed       — test encountered error
test_stopping     — graceful stop initiated
```

### `pkg/engine/metrics`

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
│   │   ├── FolderTree.tsx            — Shared folder tree (Scripts, Scenarios, Results)
│   │   ├── ScriptsPage.tsx           — Script management + authoring
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

**FolderTree.tsx**
- Shared tree navigation component used by Scripts, Scenarios, Results pages
- Props: `folders`, `items`, `renderItemExtra` (for page-specific inline actions)
- Renders folders hierarchically with inline item actions (Edit, Delete, etc.)

**ScriptsPage.tsx**
- Script library and editor interface
- Displays scripts in FolderTree with inline protocol badge and Edit/Delete buttons
- Actions: Create new drafts, open sample imports, import from LoadRunner/JMeter, edit inline, delete

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
- **DaisyUI**: TailwindCSS-based UI kit for buttons, modals, badges
- **Recharts**: Time-series charting for metrics visualization

## API Endpoints Reference

## Distributed Runtime Commands

```bash
# Controller in local mode
go run ./cmd/controller

# Controller in distributed mode
STORMRUNNER_GENERATORS=127.0.0.1:9090,127.0.0.1:9091 \
STORMRUNNER_GENERATOR_TIMEOUT=10s \
go run ./cmd/controller

# Generator
go run ./cmd/generator
```

Current distributed test coverage is centered on:

- remote start/stop/pause/resume/duration control
- multi-generator group fan-out
- real remote execution and scale routing
- remote archive fetch and controller-local archive merge

### Tests

```
POST   /api/v1/tests                          — Create and queue test
GET    /api/v1/tests                          — List all tests
GET    /api/v1/tests/{id}                     — Get test details + status
POST   /api/v1/tests/{id}/start               — Start a queued test
POST   /api/v1/tests/{id}/stop                — Graceful stop (wait for cleanup)
POST   /api/v1/tests/{id}/abort               — Immediate abort
GET    /api/v1/tests/{id}/metrics             — Current metrics snapshot
WS     /api/v1/tests/{id}/stream              — WebSocket stream (test-specific)
```

### Scripts

```
POST   /api/v1/scripts                        — Upload/create script
GET    /api/v1/scripts                        — List all scripts
GET    /api/v1/scripts/{id}                   — Get script content
PUT    /api/v1/scripts/{id}                   — Update script in-place
DELETE /api/v1/scripts/{id}                   — Delete script
POST   /api/v1/scripts/{id}/validate          — Dry-run validation
POST   /api/v1/scripts/{id}/export            — Export as JSON file
GET    /api/v1/scripts/{id}/download          — Download as JSON
POST   /api/v1/scripts/import/sample          — Convert raw HTTP, cURL, REST JSON, or HAR samples into a draft
POST   /api/v1/scripts/import/loadrunner      — Convert LoadRunner .zip to DSL
```

### Projects & Folders

```
GET    /api/v1/projects                       — List projects
POST   /api/v1/projects                       — Create project
GET    /api/v1/projects/{id}/folders          — List folders in project
POST   /api/v1/projects/{id}/folders          — Create folder
PUT    /api/v1/projects/{id}/folders/{fid}    — Rename folder
DELETE /api/v1/projects/{id}/folders/{fid}    — Delete folder
```

### Recorder

```
GET    /api/v1/recorder/status                — Recorder status
POST   /api/v1/recorder/start                 — Start HTTP recording
POST   /api/v1/recorder/stop                  — Stop recording + save HAR
GET    /api/v1/recorder/har                   — Download recorded HAR file
```

### System

```
GET    /api/v1/system/health                  — Health check (liveness)
WS     /api/v1/ws                             — Global WebSocket (all test events)
```

## Development Workflow

### 1. Running Locally

**Terminal 1: Start the backend**
```bash
go run ./cmd/controller
# Listens on :8080, serves dashboard from pkg/api/static/
```

**Terminal 2: Start the dashboard dev server** (optional for frontend dev)
```bash
cd dashboard
npm install
npm run dev
# Starts Vite dev server on :5173 with API proxy to :8080
```

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
# Run all tests with race detector
go test ./...

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
# Outputs to ./bin/controller, ./bin/generator, ./bin/stormctl
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
- **Database**: SQLite adequate for metadata; scale horizontally with multiple controllers if needed.
- **Memory**: Reuse buffers via `sync.Pool` to reduce GC pressure during high-throughput runs.

### Frontend

- **Data fetching**: Use `useApi` hook for caching; avoid refetching unchanged data.
- **Real-time updates**: WebSocket with smart reconnect logic; dashboard stays responsive even if connection drops.
- **Chart rendering**: Recharts memoizes large datasets; consider pagination for very long result histories.

## Testing Policy

**All new features and bug fixes MUST include corresponding tests.**

- **Go**: Unit tests in `*_test.go` files alongside source; aim for >80% coverage
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
# Profile CPU/memory during test
go run ./cmd/controller -cpuprofile=cpu.prof -memprofile=mem.prof

# Analyze profile
go tool pprof cpu.prof

# Run linter
golangci-lint run ./...

# Format code
go fmt ./...

# Benchmark engine performance
go test -bench=BenchmarkEngine ./pkg/engine -benchmem

# Generate test coverage HTML
go test -cover -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## References

- **AGENTS.md**: High-level architecture and tech stack
- **CLAUDE.md**: Project context and conventions (if present)
- **pkg/dsl/parser.go**: DSL parsing logic
- **pkg/api/server.go**: Route setup and middleware
