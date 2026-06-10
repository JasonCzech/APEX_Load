# Synthetic mixed-protocol 45-minute test

This folder contains a runnable synthetic 45-minute load test for HTTP, browser/HTML, and DevWeb-converted users.

## 1) Start the dummy backend

```bash
python3 scripts/synthetic-mixed-protocol-test/dummy_server.py
```

The dummy server exposes:

- `GET /` and `GET /next` HTML pages for browser navigation
- `GET /api/health`
- `POST /api/orders`
- `GET /api/orders`

## 2) Start the controller (local/dev mode)

```bash
cd /Users/jasonczech/Documents/GitHub/Project_Stormrunner
go run ./cmd/controller
```

## 3) Create and start the mixed-protocol test

```bash
python3 scripts/synthetic-mixed-protocol-test/create_synthetic_test.py
```

The script:

1. creates one HTTP script,
2. creates one browser script,
3. imports and saves one DevWeb script,
4. creates a 45m multi-group test,
5. starts it.

## 4) Verify and control

```bash
TEST_ID=<paste output test id>
curl -s http://127.0.0.1:8080/api/v1/tests/$TEST_ID | jq '{id:.id,status:.status,total_vusers:.total_vusers,duration:.duration}'
curl -s http://127.0.0.1:8080/api/v1/tests/$TEST_ID/metrics
curl -s "http://127.0.0.1:8080/api/v1/tests/$TEST_ID/errors/evidence?signature=500&limit=20"
```

Stop early if needed:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/tests/$TEST_ID/abort
```

## Optional env overrides

- `APEXLOAD_API_URL` (default `http://127.0.0.1:8080`)
- `SYNTHETIC_DUMMY_BASE_URL` (default `http://127.0.0.1:18080`)
- `APEXLOAD_API_KEY` (if you run controller with auth enabled)
