# Troubleshooting Guide

## Common Issues and Solutions

### Dashboard Issues

#### Dashboard not loading after startup
**Symptom:** Browser shows blank page or 404 when visiting `http://localhost:8080`

**Solutions:**
1. Verify controller is running: Check terminal for `stormrunner API server starting` message
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

#### Dashboard appears but real-time metrics not updating
**Symptom:** Charts show no data; WebSocket connection shows as "Disconnected"

**Solutions:**
1. Check WebSocket connection:
   - Open browser DevTools → Network → WS filter
   - Look for `/api/v1/ws` connection
   - Should show "101 Switching Protocols" status

2. If connection shows error:
   ```bash
   # Check controller logs for WebSocket errors
   grep -i "websocket" controller.log
   ```

3. Verify API server is responding:
   ```bash
   curl http://localhost:8080/api/v1/system/health
   # Should return: {"status":"healthy",...}
   ```

4. Check browser console for fetch/network errors

#### Scripts/Scenarios not loading in tree
**Symptom:** FolderTree shows empty even though scripts exist in database

**Solutions:**
1. Check browser DevTools → Network tab:
   - `/api/v1/scripts` should return 200 with script list
   - `/api/v1/projects/{id}/folders` should return folder structure

2. Verify database has data:
   ```bash
   sqlite3 stormrunner.db "SELECT count(*) FROM scripts;"
   ```

3. Check for JavaScript errors in console (F12)

4. Try browser hard refresh (Ctrl+Shift+R)

---

### Backend / Controller Issues

#### Port 8080 already in use
**Symptom:** `listen tcp :8080: bind: address already in use`

**Solutions:**
```bash
# Find process using port 8080
lsof -i :8080
# Kill it
kill -9 <PID>

# Or specify different port
STORMRUNNER_ADDR=:9000 go run ./cmd/controller
```

#### Database connection error
**Symptom:** `Error opening database` or `database is locked`

**Solutions:**
1. Check database file exists and is writable:
   ```bash
   ls -la stormrunner.db
   chmod 644 stormrunner.db
   ```

2. If locked, close other connections:
   ```bash
   # Kill other Go processes
   pkill -f "cmd/controller"
   ```

3. Reset database (WARNING: deletes all data):
   ```bash
   rm stormrunner.db
   # Restart controller to create new empty DB
   ```

#### Test won't start / stays in "queued" state
**Symptom:** Click "Start" but test remains queued

**Solutions:**
1. Check controller logs for errors:
   ```bash
   # Look for lines like: "test starting" or error messages
   ```

2. Verify script is valid:
   - Go to Scripts page
   - Click script → Click "Validate"
   - Should show "Valid" with no errors

3. Check protocol availability:
   ```bash
   # In controller logs, look for protocol registration
   grep "registering protocol" controller.log
   ```

4. Try restarting controller and retry

#### Memory usage grows during long test
**Symptom:** Controller/generator process memory increases steadily

**Solutions:**
1. This may be normal for long tests (metrics accumulation)
2. Check if there's a goroutine leak:
   ```bash
   # Enable pprof profiling
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

3. Check metrics collector initialization:
   - Look for "metrics collector starting" in logs

#### High error rate despite valid script
**Symptom:** Many transactions fail with 500 errors

**Solutions:**
1. Check target system health:
   ```bash
   curl -v http://target-system/endpoint
   ```

2. Increase request timeout:
   ```json
   {
     "config": {
       "timeout_seconds": 60
     }
   }
   ```

3. Reduce VUsers to find breaking point:
   - Start with 10 VUsers
   - Gradually increase (10 → 50 → 100)
   - Identify where error rate jumps

4. Check target system logs for errors

#### Latency seems unrealistically low
**Symptom:** All transactions showing 1-5 ms latency

**Solutions:**
1. Verify assertions are actually running:
   - Add body assertion to force evaluation
   - Check results for "assertion_failed" count

2. Check if think time is being applied:
   ```json
   {
     "think_time": {
       "type": "fixed",
       "mean_ms": 1000
     }
   }
   ```

3. Verify target system is responding:
   - Test endpoint manually with curl
   - Check if response is properly sized

---

### CLI (stormctl) Issues

#### stormctl command not found
**Symptom:** `stormctl: command not found`

**Solutions:**
```bash
# Use go run instead
go run ./cmd/stormctl run --script test.json --vusers 10

# Or build first
go build -o stormctl ./cmd/stormctl
./stormctl run --script test.json --vusers 10
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
   - config.protocol must be set
   - actions array must not be empty
   - All template variables must be defined

3. Run with verbose output:
   ```bash
   go run ./cmd/stormctl run --script test.json --vusers 1 -verbose
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
   rm -rf dist/
   npm run build
   ```

2. Verify build output:
   ```bash
   ls -la ../pkg/api/static/
   # Should show index.html, main.js, etc.
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
go test ./...
```

#### TypeScript compilation errors in frontend
**Symptom:** Build fails with `error TS...`

**Solutions:**
1. Check TypeScript errors:
   ```bash
   cd dashboard
   npm run lint
   ```

2. Fix type issues in source files

3. Clear cache and rebuild:
   ```bash
   rm -rf node_modules dist
   npm install
   npm run build
   ```

---

### Performance Issues

#### Test throughput lower than expected
**Symptom:** Only getting 100 req/sec with 1000 VUsers

**Causes and solutions:**
1. **Connection pool too small:**
   ```json
   {
     "config": {
       "connection_pool_size": 50
     }
   }
   ```

2. **Think time too high:**
   - Check script think_time config
   - Reduce or set `ignore: true` for performance testing

3. **Target system bottleneck:**
   - Test target directly: `ab -n 10000 http://target/endpoint`
   - Increase target server capacity

4. **Network latency:**
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
# Assuming slog is configured for debug level
export LOG_LEVEL=debug
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
sqlite3 stormrunner.db

# List tables
.tables

# Check scripts
SELECT id, name, protocol, created_at FROM scripts;

# Check tests
SELECT id, name, status, vusers, started_at FROM tests;

# Check results
SELECT id, test_id, total_transactions, total_errors FROM results;

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
go test -cpuprofile=cpu.prof -bench=BenchmarkEngine ./pkg/engine
go tool pprof cpu.prof

# Memory profile
go test -memprofile=mem.prof -bench=BenchmarkEngine ./pkg/engine
go tool pprof mem.prof
```

---

## Getting Help

When reporting issues:

1. **Collect logs:**
   ```bash
   # Controller logs
   go run ./cmd/controller > controller.log 2>&1 &
   # Let it run, then attach controller.log
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
   - [API_AND_DSL_REFERENCE.md](API_AND_DSL_REFERENCE.md) — API/DSL syntax
