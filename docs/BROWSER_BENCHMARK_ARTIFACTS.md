# Browser Benchmark Artifacts

This document is the code-owned browser benchmark artifact manifest for W4 browser testing parity. It defines the release evidence shape for desktop Chromium, mobile Chromium, and mobile WebKit canaries without turning browser VUsers into a high-density load claim.

The machine-readable version is exposed at:

- `GET /api/v1/system/browser-benchmarks`
- `GET /api/v1/system/parity-status` under `browser_benchmarks`

## Claim Policy

Browser VUsers are diagnostic canaries paired with high-density protocol load. They provide screenshots, DOM summaries, console/network evidence, and Core Web Vitals. They are not Stormrunner's HTTP-density load generator.

The default browser envelope remains:

| Envelope | Value |
| --- | --- |
| Recommended canary density | 1-2 browser VUsers per generator |
| Maximum claimed density without release evidence | 5 browser VUsers per generator |
| High-density real-browser claim | Not allowed without controlled raw benchmark output |
| Pairing model | Run browser canaries beside HTTP/API/JMS/WebSocket/gRPC load groups |

## Reference Hardware

| Field | Value |
| --- | --- |
| Reference profile | `generator-canary-4vcpu-8gb` |
| CPU | 4 vCPU |
| Memory | 8 GB |
| Runtime | Local, container, or Kubernetes generator image with browser dependencies installed |
| Evidence rule | Attach raw benchmark output for the exact release generator image before raising density envelopes |

## Profiles

### Desktop Chromium 1080p

| Field | Value |
| --- | --- |
| Profile ID | `desktop_chromium_1080p` |
| Browser engine | Chromium |
| Device profile | `desktop_1080p` |
| Acceptance | Launch p50 < 3000 ms, action overhead < 10 ms, Chromium RSS < 250 MB per browser VUser |
| RSS attribution | Chromium CDP process snapshot |
| Evidence level | Code + docs + release artifact required |

### Mobile Chromium Pixel 8

| Field | Value |
| --- | --- |
| Profile ID | `mobile_chromium_pixel_8` |
| Browser engine | Chromium |
| Device profile | `pixel_8` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms, Chromium RSS < 250 MB per browser VUser |
| RSS attribution | Chromium CDP process snapshot |
| Evidence level | Code + docs + release artifact required |

### Mobile WebKit iPhone 15 Pro

| Field | Value |
| --- | --- |
| Profile ID | `mobile_webkit_iphone_15_pro` |
| Browser engine | WebKit |
| Device profile | `iphone_15_pro` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms |
| RSS attribution | Not available through CDP; use host-level memory telemetry |
| Evidence level | Code + docs + release artifact required |

## Environment Matrix

Normal CI compiles the benchmark package and verifies all profiles remain registered:

```bash
go test ./pkg/protocol/browser
```

Release or dependency-upgrade validation runs the opt-in benchmark suite:

```bash
STORMRUNNER_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser \
  -run '^$' \
  -bench 'BenchmarkBrowser(InitWithDeviceProfile|TouchOrClickActionLatency|RSSPerChromiumDeviceProfile)' \
  -benchmem \
  -count 5 \
  -timeout 30m
```

The benchmark code lives in `pkg/protocol/browser/mobile_benchmark_test.go`.

## Release Evidence Checklist

For each release candidate or browser dependency upgrade:

1. Record CPU, memory, OS, container limits, browser versions, Playwright WebKit version, and Stormrunner commit SHA.
2. Run the full benchmark command on a quiet host that matches the release generator image.
3. Attach raw benchmark output to the release evidence record.
4. Keep the default canary envelope unless all three profile classes meet the acceptance targets.
5. Update `docs/MOBILE_BROWSER_EMULATION_PLAN.md` only when repeated controlled runs justify a new envelope.

## Scenario Template

Use `examples/web-api/browser-canary-plus-protocol-load.json` when creating release-gate scenarios. It demonstrates HTTP and WebSocket protocol load with desktop and mobile browser canaries, each tied back to benchmark profile IDs.
