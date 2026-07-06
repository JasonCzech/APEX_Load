# Quick Reference

## Essential Commands

### Starting

```bash
# Start controller (API + dashboard)
go run ./cmd/controller

# Start with custom port
APEXLOAD_ADDR=:9000 go run ./cmd/controller

# Start controller in distributed mode
APEXLOAD_GENERATORS=127.0.0.1:9090,127.0.0.1:9091 \
APEXLOAD_GENERATOR_TIMEOUT=10s \
go run ./cmd/controller

# Start generator
go run ./cmd/generator

# Run test locally (no controller needed)
go run ./cmd/apexctl run --script test.json --vusers 50 --duration 2m
```

### Building

```bash
# Build all binaries
make build

# Build only controller
go build -o bin/controller ./cmd/controller

# Build dashboard
cd dashboard && npm run build

# Run frontend dev server (with hot reload)
cd dashboard && npm run dev
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests for one package
go test ./pkg/engine

# Run with coverage
go test -cover ./...

# Generate coverage HTML
make test-cover

# Run specific test
go test -run TestMetricsCollection ./pkg/engine
```

### Database

```bash
# Query scripts
sqlite3 apexload.db "SELECT id, name FROM scripts;"

# Query tests
sqlite3 apexload.db "SELECT id, name, status FROM tests;"

# Reset database
rm apexload.db
# Restart controller to recreate

# Backup database
cp apexload.db apexload.db.backup
```

---

## API Documentation

The full route list is generated from the controller route catalog.

- Generated OpenAPI: [docs/swagger.yaml](docs/swagger.yaml)
- Generated endpoint catalog: [docs/api-endpoint-catalog.json](docs/api-endpoint-catalog.json)
- Runtime docs endpoints: `GET /api/v1/docs/swagger.yaml` and `GET /api/v1/docs/endpoint-catalog`
- Workflow guide: [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)

---

## DSL Script Skeleton

```json
{
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
      "name": "get_profile",
      "type": "http",
      "method": "GET",
      "target": "/profile"
    }
  ],
  "teardown": []
}
```

**Supported protocol surfaces:** `http`, `soap`, `java_http`, `browser`, `websocket`, `grpc`,
`jms`, `kafka`, `eventhubs`, `database`, and `network` families. Real broker adapters are opt-in
behind build tags; see [docs/MESSAGING_SUPPORT.md](docs/MESSAGING_SUPPORT.md).

## Browser And Mobile DSL

```json
{
  "protocol": "browser",
  "config": {
    "base_url": "https://www.example.com",
    "browser": {
      "engine": "webkit",
      "device_profile": "iphone_15_pro",
      "touch_mode": true,
      "orientation": "portrait",
      "client_metrics": true,
      "slow_mo_ms": 50,
      "viewport": {
        "width": 393,
        "height": 852
      }
    }
  },
  "actions": [
    {
      "name": "open-home",
      "type": "browser_navigate",
      "target": "/"
    },
    {
      "name": "open-menu",
      "type": "browser_tap",
      "selector": "button[data-testid='menu-toggle']"
    },
    {
      "name": "swipe-carousel",
      "type": "browser_swipe",
      "selector": "[data-testid='hero-carousel']",
      "params": {
        "direction": "left",
        "distance": 220
      }
    }
  ]
}
```

Supported browser actions:
- `browser_navigate`
- `browser_click`
- `browser_fill`
- `browser_select`
- `browser_wait`
- `browser_screenshot`
- `browser_tap`
- `browser_long_press`
- `browser_swipe`

Current mobile-browser support envelope:
- `chromium`: `desktop_1080p`, `galaxy_s24`, `pixel_8`
- `webkit`: `ipad_air`, `iphone_15_pro`, `iphone_se`
- Conservative starting concurrency and benchmark guidance live in [docs/BROWSER_AND_MOBILE_SUPPORT.md](docs/BROWSER_AND_MOBILE_SUPPORT.md).

Browser/mobile config keys:
- `config.browser.engine`: `chromium` or `webkit` (`webkit` executes through Playwright WebKit and requires Playwright WebKit assets installed on the controller host)
- `config.browser.device_profile`: named preset such as `pixel_8`, `iphone_15_pro`, or `ipad_air`
- `config.browser.touch_mode`: force touch interactions on or off
- `config.browser.orientation`: `portrait` or `landscape`
- `config.browser.client_metrics`: enable FCP/LCP/CLS/INP and long-task capture

Browser action value requirements:
- `browser_fill.value`: text to enter into the targeted field
- `browser_select.value`: option value, visible text, or label to choose from the targeted `<select>` element

Gesture params:
- `browser_long_press.params.duration_ms`
- `browser_swipe.params.direction`: `left`, `right`, `up`, `down`
- `browser_swipe.params.distance`: positive pixel distance

Captured browser metrics:
- `avg_fcp_ms`
- `avg_lcp_ms`
- `avg_cls_score`
- `avg_inp_ms`
- `avg_blocking_resources`
- `avg_long_task_count`
- `avg_js_heap_used_mb`

Archived and live browser summaries are exposed through `metrics.browser`, `metrics.browser_by_device`, archive interval summaries, and archive reports. Browser evidence bundles can be downloaded from `/api/v1/tests/{id}/archive/browser-diagnostics`; each bundle contains `browser_diagnostics.json` plus captured screenshot and HTML artifacts when those snapshots are available.

Chromium browser runs with `config.browser.client_metrics` enabled also report browser-process CPU and memory as `browser_process_cpu_pct` / `browser_process_mem_mb` on transaction events and `avg_browser_process_cpu_pct` / `avg_browser_process_mem_mb` on summaries.

**Java-over-HTTP MVP:**
- Reuses the HTTP transport with the `java_serialized_object_base64` codec.
- Sample imports for Java-over-HTTP are intentionally limited to raw HTTP and cURL bodies that already contain base64-encoded serialized Java bytes.

---

## Template Variables

```
{{env.VARIABLE_NAME}}          # Environment variable
{{data.source_name.column}}    # Data source column
{{param.parameter_name}}       # Named script parameter
{{var.variable_name}}          # Extracted variable
{{fn.uuid()}}                  # UUID
{{fn.uuid}}                    # UUID
{{fn.random(1,100)}}           # Random number 1-100
{{fn.timestamp.unix}}          # Unix timestamp
{{fn.timestamp.unix_ms}}       # Unix timestamp in milliseconds
{{fn.timestamp.iso8601}}       # ISO8601 timestamp
{{fn.random_string(10)}}       # Random string (10 chars)
```

## Parameters

```json
"parameters": [
  {
    "name": "order_id",
    "type": "random_number",
    "update": "each_occurrence",
    "min": 1000,
    "max": 9999
  },
  {
    "name": "run_date",
    "type": "date_time",
    "update": "each_iteration",
    "format": "2006-01-02",
    "timezone": "UTC"
  }
]
```

- `type`: `file`, `list`, `random_number`, `date_time`, `timestamp`, `counter`, `uuid`, `random_string`
- `update`: `once`, `each_iteration`, `each_occurrence`
- `selection`: `sequential`, `random`, `unique` for `file` and `list`
- `on_exhaustion`: `wrap`, `empty`, `error` for `file` and `list`

---

## Think Time Configs

```json
// Fixed delay (1000 ms)
{
  "think_time": {
    "type": "fixed",
    "mean_ms": 1000
  }
}

// Random (500-3000 ms)
{
  "think_time": {
    "type": "random",
    "min_ms": 500,
    "max_ms": 3000
  }
}

// Gaussian distribution
{
  "think_time": {
    "type": "gaussian",
    "mean_ms": 2000,
    "std_ms": 500,
    "min_ms": 500,
    "max_ms": 5000
  }
}
```

---

## CLI Flags

### apexctl run

```bash
go run ./cmd/apexctl run \
  --script path/to/script.json \
  --vusers 100 \
  --duration 5m \
  --ramp-up 30s \
  --format json \
  --p95-latency-ms 800 \
  --max-error-pct 2.5
```

| Flag | Default | Description |
|------|---------|-------------|
| `--script, -s` | (required) | Path to DSL script |
| `--vusers, -v` | 10 | Number of virtual users |
| `--duration, -d` | 1m | Test duration |
| `--ramp-up, -r` | 0s | Ramp-up period |
| `--format` | `text` | Output format: `text` or `json` |
| `--api` | (none) | Execute through the controller API |
| `--env, -env` | (repeatable) | Runtime placeholder override (`KEY=value`) |
| `--base-url` | (none) | Override the script base URL |
| `--p95-latency-ms` | (none) | Fail the run if P95 latency exceeds this threshold |
| `--max-error-pct` | (none) | Fail the run if error rate exceeds this threshold |
| `--max-avg-server-processing-ms` | (none) | Fail the run if average server processing exceeds this threshold |
| `--max-avg-transfer-ms` | (none) | Fail the run if average transfer exceeds this threshold |
| `--max-error-count` | (none) | Fail the run if total error count exceeds this threshold |

---

## Troubleshooting Quick Fixes

| Issue | Quick Fix |
|-------|-----------|
| Dashboard won't load | Hard refresh (Ctrl+Shift+R), check port 8080 |
| Metrics not updating | Check WebSocket connection (DevTools → Network → WS) |
| Port already in use | `lsof -i :8080` then `kill -9 <PID>` |
| Test won't start | Validate script, check target system is reachable |
| Memory growing | Normal for long tests; check goroutine count |
| No transactions recorded | Verify actions in script, increase duration |

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for detailed solutions.

---

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `APEXLOAD_ADDR` | `:8080` | HTTP server bind address |
| `APEXLOAD_DB` | `apexload.db` | SQLite database path |
| `LOG_LEVEL` | `info` | Logging level (debug, info, warn, error) |

---

## File Locations

| File | Purpose |
|------|---------|
| `apexload.db` | SQLite database (auto-created) |
| `coverage.html` | Test coverage report (after `make test-cover`) |
| `pkg/api/static/` | Embedded dashboard assets |
| `dashboard/dist/` | Built frontend (before embedding) |
| `examples/` | Sample scripts and test data |

---

## Performance Tips

- **Increase throughput**: Raise `connection_pool_size` in config
- **Reduce errors**: Lower VUsers or increase `timeout_seconds`
- **Faster ramp**: Reduce `--ramp-up` duration (trade-off: less stable)
- **Smaller memory**: Reduce `--vusers` or test duration
- **Better accuracy**: Increase test duration (more stable metrics)

---

## Documentation Map

| Document | For |
|----------|-----|
| [README.md](README.md) | Overview, quick start, basic usage |
| [docs/INDEX.md](docs/INDEX.md) | Canonical documentation map |
| [DEVELOPMENT.md](DEVELOPMENT.md) | Development setup, conventions, architecture internals |
| [ARCHITECTURE.md](ARCHITECTURE.md) | System design, data flow, performance details |
| [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md) | Compatibility landing page for generated API and DSL docs |
| [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md) | Operator/CI-CD guide: auth and workflows |
| [docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md) | Generated DSL action reference |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Common issues, debugging, solutions |
| [QUICK_REFERENCE.md](QUICK_REFERENCE.md) | This file — fast lookup |

---

## Useful URLs

- **Dashboard**: http://localhost:8080
- **Health check**: http://localhost:8080/api/v1/system/health
- **API docs**: see [docs/swagger.yaml](docs/swagger.yaml), [docs/api-endpoint-catalog.json](docs/api-endpoint-catalog.json), and [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)

---

## Common Errors

```
"listen tcp :8080: bind: address already in use"
→ Change port: APEXLOAD_ADDR=:9000 go run ./cmd/controller

"Error opening database: database is locked"
→ Close other processes: pkill -f "cmd/controller"

"Invalid script: protocol required"
→ Add config.protocol to your script JSON

"No tests currently running"
→ Dashboard is working; just no active tests

"Cannot GET /api/v1/scripts"
→ Controller not started or wrong port
```

---

## Go Package Structure

```
pkg/
  engine/          # VUser execution, pacing, metrics
  protocol/        # HTTP, SOAP, WebSocket, gRPC, JMS, Kafka, database, network, browser
  dsl/            # Script parsing, action catalog
  api/            # REST handlers, WebSocket
  config/         # Configuration types
  storage/        # SQLite (default) / PostgreSQL
  controller/     # Test orchestration
  distributed/    # Generator-side gRPC worker
  recorder/       # HTTP recording + HAR + browser recording
  convert/        # Multi-format import hub (LR, JMeter, k6, DevWeb, HAR, OpenAPI, WSDL, Postman, AsyncAPI, TruClient)
  selector/       # Element-selector model + replay self-heal
  scrub/          # PII/credential redaction
  refkey/         # Human-readable reference keys
  buildinfo/      # Build metadata
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for detailed package documentation.
