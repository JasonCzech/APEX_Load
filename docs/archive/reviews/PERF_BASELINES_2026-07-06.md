# APEX Load Performance Baselines - 2026-07-06

Status: Historical (archived 2026-09-24).

Baseline captured before applying the staged performance changes in `PERF_REVIEW_2026-07-06.md`.

## Local Controller Endpoint Payloads

Source: existing local controller process `./bin/controller` on `127.0.0.1:8080`, PID 52203.

| Probe | Status | Bytes | Time | Notes |
| --- | ---: | ---: | ---: | --- |
| `GET /api/v1/tests` | 200 | 41,823,812 | 245.861 ms | 113 tests; first response included `history` arrays. |
| `GET /api/v1/tests/e484fd1a-1ee3-4677-87fb-35d0e42ca221` | 200 | 2,404,117 | 20.853 ms | 2,734 history points. |
| `GET /api/v1/tests/e484fd1a-1ee3-4677-87fb-35d0e42ca221/metrics` | 200 | 2,402,436 | 23.262 ms | 2,734 history points. |

Controller process sample:

| Sample | CPU | RSS |
| --- | ---: | ---: |
| Before detail/metrics probes | 0.0% | 28,896 KiB |
| After detail/metrics probes | 0.0% | 278,320 KiB |

The dashboard live-view CPU/RSS baseline from the plan was not collected here because no mature live run with an attached dashboard viewer was available in this session.

## Microbenchmarks

Command:

```bash
go test -run=^$ -bench='Benchmark(VUserResolveAction|ScrubSensitiveText|HTTPExecute)' -benchmem ./pkg/engine ./pkg/protocol/http
```

Environment: `goos=darwin`, `goarch=arm64`, CPU `Apple M4`.

| Benchmark | ns/op | MB/s | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `BenchmarkScrubSensitiveTextClean1KB-10` | 102,563 | 10.53 | 14,112 | 18 |
| `BenchmarkScrubSensitiveTextSensitive50KB-10` | 5,784,770 | 8.85 | 972,586 | 1,224 |
| `BenchmarkVUserResolveActionLiteralLargeBody-10` | 38,271 | 1,712.77 | 452,568 | 73 |
| `BenchmarkVUserResolveActionTemplated-10` | 726,574 | 30.30 | 196,240 | 97 |
| `BenchmarkHTTPExecuteSmallJSON-10` | 32,508 | 1.32 | 11,094 | 133 |
| `BenchmarkHTTPExecute50KBJSON-10` | 84,823 | 634.41 | 406,725 | 145 |

## Compile Check

```bash
go test ./pkg/engine ./pkg/protocol/http
```

Result: passed.

## Stage 1 Payload Check

Source: fresh controller from current source on `127.0.0.1:18080`, using a copy of `apexload.db`.

| Probe | Status | Bytes | Time | Notes |
| --- | ---: | ---: | ---: | --- |
| `GET /api/v1/tests` | 200 | 397,469 | 5.239 ms | 113 tests; max list `history` length 0; max `sparkline` length 60. |
| `GET /api/v1/tests/e484fd1a-1ee3-4677-87fb-35d0e42ca221` | 200 | 6,139 | 1.367 ms | Default detail has no `history`; includes 60-point sparkline. |
| `GET /api/v1/tests/e484fd1a-1ee3-4677-87fb-35d0e42ca221?include=history&limit=5` | 200 | 10,522 | 1.054 ms | Opt-in history returned 5 points. |
| `GET /api/v1/tests/e484fd1a-1ee3-4677-87fb-35d0e42ca221/metrics?limit=5` | 200 | 5,336 | 0.738 ms | Metrics history window returned 5 points. |

Compared with the baseline, the default list payload dropped from 41,823,812 bytes to 397,469 bytes, and the default detail payload dropped from 2,404,117 bytes to 6,139 bytes.

## Final Implementation Validation

Captured after implementing the full staged plan.

| Check | Result | Notes |
| --- | --- | --- |
| `go generate ./pkg/api` | Passed | Regenerated `docs/swagger.yaml`, `docs/api-endpoint-catalog.json`, and mirrored `pkg/api/apidocs/*` artifacts. |
| `go test ./... -timeout=600s` | Passed | Full Go suite completed successfully after adding the new route authorization golden and stabilizing async test waits. |
| `npx tsc --noEmit` in `dashboard/` | Passed | Dashboard TypeScript check. |
| `npm run test` in `dashboard/` | Passed | 200 test files, 1,567 tests. Existing React `act(...)` warnings remain in unrelated test files. |
| Playwright dashboard smoke | Passed | Vite dev server rendered `#/dashboard` at desktop and 390px mobile widths; console contained only the React DevTools info message. |
