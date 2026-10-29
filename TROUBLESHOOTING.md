# Troubleshooting Guide

## Common Issues and Solutions

### Dashboard Issues

#### Dashboard not loading after startup
**Symptom:** Browser shows blank page or 404 when visiting `http://localhost:8080`

**Solutions:**
1. Verify controller is running: Check terminal for `apexload API server starting` message
2. Check port conflict: `lsof -i :8080` (should show the Go process)
3. Clear browser cache: Hard refresh (Ctrl+Shift+R or Cmd+Shift+R)
4. Check browser console: Open DevTools (F12) → Console tab for JavaScript errors
5. Verify static assets were embedded: Look for `pkg/api/static/` directory with files

**If rebuilding frontend:**
```bash
cd dashboard
npm run build
# Restart controller to serve new assets
```

#### Dashboard shows an error but the controller logs look empty
**Symptom:** A dashboard action fails, but `kubectl logs` shows nothing obviously related.

Every API request writes one JSON `"msg":"http request"` line: `INFO` for success, `WARN` for
4xx and `ERROR` for 5xx. Failed requests carry the `error` text the dashboard displayed, plus
`request_id`, `route`, `actor` and (when tracing is enabled) `trace_id`. The `X-Request-ID`
response header carries the same request ID, and an ingress-supplied `X-Request-ID` is reused.

**Solutions:**
1. Server errors appear in the dashboard as `internal server error (ref <id>)`. Search for that ID.
   It matches both the access line and the line holding the internal detail:
   ```bash
   kubectl logs -n <namespace> -l app.kubernetes.io/name=apexload-controller --prefix --since=1h | grep '<id>'
   ```
2. For a 4xx, search for the message the dashboard showed, or list recent failures:
   ```bash
   kubectl logs -n <namespace> -l app.kubernetes.io/name=apexload-controller --prefix --since=1h | grep -E '"status":(4|5)[0-9]{2}'
   ```
3. A handler panic logs `"msg":"http handler panic"` with a stack and returns a 500 with a ref.
4. Use `--prefix` with the label selector so HA replicas are all searched, and `--previous` if the pod restarted.
5. Failures the controller never saw are reported by the dashboard and logged at `WARN` as
   `"msg":"dashboard client error"`. The `kind` field is `render` (a page crashed), `error` or
   `unhandledrejection` (an uncaught browser error), `network` (a request failed or timed out in the
   browser), or `gateway` (a 5xx from an ingress or proxy, with no controller request ID). Each line carries
   the page, stack and failed request path; query strings are dropped. Reports are deduplicated and
   rate-limited, so a burst of identical errors appears once.
   ```bash
   kubectl logs -n <namespace> -l app.kubernetes.io/name=apexload-controller --prefix --since=1h | grep 'dashboard client error'
   ```
6. If nothing matches, the ingress or network was down, so the report could not be delivered either.
   Check the request in the browser DevTools Network tab and the ingress logs.

#### Dashboard appears but real-time metrics not updating
**Symptom:** Charts show no data; WebSocket connection shows as "Disconnected"

**Solutions:**
1. Check WebSocket connection:
   - Open browser DevTools → Network → WS filter
   - Look for `/api/v1/ws` connection
   - Should show "101 Switching Protocols" status

2. If connection shows error:
   ```bash
   # Check controller logs (wherever you redirect them) for WebSocket errors
   grep -i "websocket" <controller-log-file>
   ```

3. Verify API server is responding:
   ```bash
   curl http://localhost:8080/api/v1/system/health
   # Should return: {"status":"healthy",...}
   ```

4. Check browser console for fetch/network errors

#### Scripts or scenarios are missing from the dashboard
**Symptom:** The Script Library or scenario list is empty even though records exist in the database

**Solutions:**
1. Check browser DevTools → Network tab:
   - `/api/v1/scripts` should return 200 with script list
   - `/api/v1/projects/{id}/folders` should return folder structure

2. Verify database has data:
   ```bash
   sqlite3 apexload.db "SELECT count(*) FROM scripts;"
   ```

3. Check for JavaScript errors in console (F12)

4. Try browser hard refresh (Ctrl+Shift+R)

---

### Backend / Controller Issues

#### TLS handshake fails with x509 negative serial number
**Symptom:** Requests to enterprise targets fail with an error similar to:
`tls: failed to parse certificate from server: x509: negative serial number`

**Why this happens:**
- Go 1.23+ rejects certificates with negative serial numbers by default.
- Some enterprise proxies/intermediaries still present legacy chains that trigger this path.

**Solutions:**
1. Enable compatibility mode through controller environment variables:
   ```bash
   APEXLOAD_ENABLE_TLS_COMPAT=true go run ./cmd/controller
   ```
   This appends missing `GODEBUG` tokens:
   `x509negativeserial=1,tlsmlkem=0,tlssecpmlkem=0`

2. For local dev, compatibility defaults are applied automatically unless disabled.

3. If you need to force-disable compatibility defaults:
   ```bash
   APEXLOAD_DISABLE_TLS_COMPAT=true go run ./cmd/controller
   ```

4. Verify startup log includes:
   - `applied TLS compatibility defaults`

**Production caveat:**
- If `APEXLOAD_ENV=production|prod`, the persisted `auth.mode` setting must not be a bypass mode such as `dev`; the controller refuses to start.
- Use `oidc` or `trusted_headers` auth mode in production environments.

#### Port 8080 already in use
**Symptom:** `listen tcp :8080: bind: address already in use`

**Solutions:**
```bash
# Find process using port 8080
lsof -i :8080
# Kill it
kill -9 <PID>

# Or specify different port
APEXLOAD_ADDR=:9000 go run ./cmd/controller
```

#### Database connection error
**Symptom:** `Error opening database` or `database is locked`

**Solutions:**
1. Check database file exists and is writable:
   ```bash
   ls -la apexload.db
   chmod 644 apexload.db
   ```

2. If locked, close other connections:
   ```bash
   # Kill other Go processes
   pkill -f "cmd/controller"
   ```

3. Reset database (WARNING: deletes all data):
   ```bash
   rm apexload.db
   # Restart controller to create new empty DB
   ```

#### Test won't start / stays in "queued" state
**Symptom:** Click "Start" but test remains queued

**Solutions:**
1. Check structured JSON controller/generator logs and queue status:
   ```bash
   # Controller and generator emit JSON slog lines ("msg" field), e.g.:
   #   "controller bind address resolved", "distributed execution enabled",
   #   "HA execution leader election enabled; only the lease holder will run tests",
   #   "apexload generator starting", or error records ("level":"ERROR")
   # Inspect the queued run's pending_reason and readiness checks directly:
   curl -fsS "http://localhost:8080/api/v1/tests/<test-id>" | jq '{status, pending_reason, error}'
   curl -fsS "http://localhost:8080/api/v1/system/readiness" | jq .
   ```

2. Verify script is valid:
   - Go to Scripts page
   - Click script → Click "Validate"
   - Should show "Valid" with no errors

3. Check runtime protocol availability vs authoring-only catalog entries:
   ```bash
   # Pass -H "X-APEXLoad-API-Key: $APEXLOAD_API_KEY" (or Bearer token) when auth is enabled.
   # Prefer the native runtime support matrix to distinguish executable protocols (and opt-in
   # runners like java_vuser) from authoring/import-only catalog entries such as sap_gui:
   curl -fsS "http://localhost:8080/api/v1/protocols/native-support-matrix" | jq .
   # Full DSL action catalog (includes authoring-only sap_gui actions):
   curl -fsS "http://localhost:8080/api/v1/dsl/actions" | jq '.protocols'
   ```

4. Try restarting controller and retry

#### Memory usage grows during long test
**Symptom:** Controller/generator process memory increases steadily

**Solutions:**
1. This may be normal for long tests (metrics accumulation)
2. Check if there's a goroutine leak:
   ```bash
   # Start the process with APEXLOAD_PPROF_ADDR=127.0.0.1:6060 first.
   # For an in-cluster process, keep it loopback-only and use kubectl port-forward.
   go tool pprof http://localhost:6060/debug/pprof/goroutine
   ```

3. Check for buffered channel backlog:
   - Monitor metrics channel capacity in engine logs
   - If consistently full, increase buffer size in `pkg/engine/metrics.go`

---

### Test Execution Issues

#### Test runs but no metrics collected
**Symptom:** Test completes successfully but all metrics show 0

**Solutions:**
1. Check if actions are actually executing:
   - Add logging to protocol.Execute()
   - Run test with small duration and high vusers

2. Verify protocol implementation:
   ```bash
   # Check protocol health check passes
   curl http://target-system:port/health
   ```

3. Check protocol initialization logs and collector drop telemetry:
   - `engine.MetricsCollector` starts automatically with each group (there is no separate collector startup log banner); check structured logs for `"failed to create protocol"`, `"failed to init protocol"`, or `"health check failed, proceeding anyway"`
   - Query `GET /api/v1/tests/{id}/metrics` and inspect `telemetry.dropped_results` and `telemetry.websocket_dropped_events`

#### High error rate despite valid script
**Symptom:** Many transactions fail with 500 errors

**Solutions:**
1. Check target system health:
   ```bash
   curl -v http://target-system/endpoint
   ```

2. Reduce VUsers to find breaking point:
   - Start with 10 VUsers
   - Gradually increase (10 → 50 → 100)
   - Identify where error rate jumps

3. Check target system logs for errors

#### Latency seems unrealistically low
**Symptom:** All transactions showing 1-5 ms latency

**Solutions:**
1. Confirm you are not hitting a loopback/mock target or fast error response:
   - Requests against `127.0.0.1` / `localhost`, an in-cluster stub, or a reverse proxy returning an immediate `401`/`403`/`404`/`502` tiny body legitimately complete in 1–5 ms
   - Note: `think_time` sleeps **between** actions outside measured action/transaction duration, so adding `think_time` lowers request rate (TPS) without increasing per-action latency

2. Verify status and body assertions are configured:
   - Without assertions, fast HTTP error responses or empty cached payloads may be recorded as low-latency completions; add `status_code` and `body` assertions and inspect `assertion_failed` counts

3. Check for upstream/CDN caching or connection-reuse effects:
   - Test the target directly (`curl -w '\n%{http_code} %{size_download}B %{time_total}s\n' -o /dev/null -s http://target-system/endpoint`) to verify payload size, cache headers (`Age`, `X-Cache`), and actual server processing time

---

### CLI (apexctl) Issues

#### apexctl command not found
**Symptom:** `apexctl: command not found`

**Solutions:**
```bash
# Use go run instead
go run ./cmd/apexctl run --script test.json --vusers 10

# Or build first
go build -o apexctl ./cmd/apexctl
./apexctl run --script test.json --vusers 10
```

#### Script validation fails in CLI
**Symptom:** `Invalid script: ...` error when running test

**Solutions:**
1. Validate JSON syntax:
   ```bash
   jq . test.json
   # Should show parsed JSON without errors
   ```

2. Check required fields:
   - `protocol` must be set on the top-level `script` object (not in `config`)
   - actions array must not be empty
   - All template variables must be defined

3. Validate the script with structured output before running it:
   ```bash
   go run ./cmd/apexctl script validate --script test.json --format json
   ```

#### Test completes but shows 0 transactions
**Symptom:** `Total Transactions: 0`

**Solutions:**
1. Check actions are defined in script
2. Increase duration: `--duration 10s` at minimum
3. Add logging to see if actions execute
4. Verify no assertions are failing immediately

---

### Development / Build Issues

#### Dashboard doesn't rebuild when files change
**Symptom:** Frontend changes don't appear after `npm run build`

**Solutions:**
1. Clean build cache:
   ```bash
   cd dashboard
   rm -rf ../pkg/api/static/
   npm run build
   ```

2. Verify build output:
   ```bash
   ls -la ../pkg/api/static/ ../pkg/api/static/assets/
   # Should show index.html and Vite's hashed assets/index-<hash>.js + assets/index-<hash>.css
   ```

3. Restart controller to load new assets:
   ```bash
   # Kill old controller
   pkill -f "cmd/controller"
   # Start new instance
   go run ./cmd/controller
   ```

#### Go tests fail with import errors
**Symptom:** `cannot find package` errors

**Solutions:**
```bash
# Download dependencies
go mod download
go mod tidy

# Run tests again
go test -timeout=20m ./...
```

#### TypeScript compilation errors in frontend
**Symptom:** Build fails with `error TS...`

**Solutions:**
1. Check TypeScript errors:
   ```bash
   cd dashboard
   npm run typecheck
   ```

2. Fix type issues in source files

3. Clear cache and rebuild:
   ```bash
   rm -rf node_modules ../pkg/api/static
   npm install
   npm run build
   ```

---

### Performance Issues

#### Test throughput lower than expected
**Symptom:** Only getting 100 req/sec with 1000 VUsers

**Causes and solutions:**
1. **Think time too high:**
   - Check script think_time config
   - Reduce or set `ignore: true` for performance testing

2. **Target system bottleneck:**
   - Test target directly: `ab -n 10000 http://target/endpoint`
   - Increase target server capacity

3. **Network latency:**
   - Measure end-to-end latency: `ping target`
   - Consider network proximity

#### Ramp-up takes too long
**Symptom:** 10,000 VUsers take 5+ minutes to ramp up

**Solutions:**
1. Reduce ramp-up time:
   ```bash
   --ramp-up 60s  # Instead of 5m
   ```

2. Accept that slower ramp = more stability
   - Fast ramp can overwhelm target system
   - Default 30-60s is usually appropriate

#### Memory leak suspected
**Symptom:** Process memory grows 1 MB/sec

**Solutions:**
Start the process with `APEXLOAD_PPROF_ADDR=127.0.0.1:6060` before collecting profiles.
Production rejects non-loopback pprof binds; use `kubectl port-forward` for in-cluster pods.

1. Check for goroutine leaks:
   ```bash
   # Get goroutine count while test running
   curl http://localhost:6060/debug/pprof/goroutine?debug=1 | grep goroutine
   ```

2. Verify all VUser goroutines exit:
   - After test completes, goroutine count should return to baseline

3. Profile heap:
   ```bash
   go tool pprof http://localhost:6060/debug/pprof/heap
   ```

---

## Debugging Techniques

### Enable detailed logging

**In controller:**
```bash
export APEXLOAD_LOG_LEVEL=debug
go run ./cmd/controller
```

**In engine:**
Edit `pkg/engine/engine.go` to add:
```go
slog.Debug("action executed", "action", action.Name, "latency_ms", latency.Milliseconds())
```

### Inspect database state

```bash
# Connect to SQLite
sqlite3 apexload.db

# List tables
.tables

# Check scripts
SELECT id, name, protocol, created_at FROM scripts;

# Check tests
SELECT id, name, status, total_vusers, started_at FROM tests;

# Check run archives (per-run result data lives in the archive; imported external results are in external_result_imports)
SELECT test_id, transaction_count, completed_at FROM run_archives;

# Exit
.quit
```

### Trace HTTP requests

**Using curl:**
```bash
# Verbose output
curl -v -X GET http://localhost:8080/api/v1/scripts

# Measure timings
curl -w '@curl-format.txt' -o /dev/null -s http://localhost:8080/api/v1/scripts
```

**Using browser DevTools:**
1. Open DevTools (F12)
2. Go to Network tab
3. Reload page
4. Click on requests to see headers, response body, timing

### Profile engine performance

```bash
# CPU profile
go test -run='^$' -cpuprofile=cpu.prof -bench=BenchmarkHarnessOverhead ./pkg/engine
go tool pprof cpu.prof

# Memory profile
go test -run='^$' -memprofile=mem.prof -bench=BenchmarkHarnessOverhead ./pkg/engine
go tool pprof mem.prof
```

---

## Getting Help

When reporting issues:

1. **Collect logs:**
   ```bash
   # Controller logs
   go run ./cmd/controller > controller.log 2>&1 &
   # Let it run, then attach the log file
   ```

2. **Include details:**
   - Go version: `go version`
   - Node version: `node -v`
   - OS: `uname -a`
   - Script (sanitized): Your DSL script
   - Exact error message
   - Steps to reproduce

3. **Minimal reproduction:**
   - Use `examples/simple-http.json` to verify basic functionality
   - Narrow down to specific protocol/feature

4. **Check existing documentation:**
   - [DEVELOPMENT.md](DEVELOPMENT.md) — Dev setup
   - [ARCHITECTURE.md](ARCHITECTURE.md) — System internals
   - [docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md) and [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md) — DSL and API syntax
