# Performance Benchmarks

Status: Current

Consolidated benchmark and performance-evidence reference. Merged from the former BENCHMARK_HARNESS_OVERHEAD,
PERFORMANCE_BENCHMARK_EVIDENCE, BROWSER_BENCHMARK_ARTIFACTS, and
MOBILE_BROWSER_BENCHMARK_RUNBOOK documents (recover from git history if needed).

Contents:

1. [Harness overhead per transaction](#harness-overhead-per-transaction)
2. Evidence and artifacts: [Local performance evidence harness](#local-performance-evidence-harness), [Browser benchmark artifacts](#browser-benchmark-artifacts)
3. [Browser benchmark runbook](#browser-benchmark-runbook)

## Harness overhead per transaction

This is a reproducible benchmark of one component of per-transaction harness
overhead: the metrics hot path every result pays on a load generator. It does
not, by itself, evaluate the `< 50 µs` complete-harness design target.

### What it measures (and what it does not)

- **Measures:** `MetricsCollector.Record` — the documented "zero-lock hot path":
  two atomic counter updates plus a non-blocking buffered-channel handoff
  (`pkg/engine/metrics.go`). This is the per-transaction bookkeeping cost the
  harness adds on top of the actual protocol I/O.
- **Does NOT measure:** action resolution, redaction, protocol dispatch, result
  handling outside the collector, end-to-end protocol time, or full-pod density.
  Those remain environment- and protocol-specific and require hardware-pinned
  evidence before being claimed.

### How to reproduce

```bash
go test -run='^$' -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/
```

A CI component guardrail (`TestHarnessOverheadUnderTarget` in
`pkg/engine/harness_overhead_benchmark_test.go`) runs the benchmark inline and
fails if the metrics hot path regresses past a generous fraction of the budget,
so a future change that adds a lock or allocation to `Record` is caught. Passing
that guardrail is not a full-harness target-attainment result.

For versioned JSON, machine/build metadata, raw samples, and the other three
performance-target probes, see [Local performance evidence harness](#local-performance-evidence-harness).

### Result (reference run)

| Benchmark | Hardware | ns/op | allocs/op |
| --- | --- | --- | --- |
| `BenchmarkHarnessOverheadPerTransaction` | Apple M4 (darwin/arm64) | ~8.4 | 0 |
| `BenchmarkHarnessOverheadParallel` (contended) | Apple M4 (darwin/arm64) | ~45 | 0 |

Interpretation: this historical developer-machine sample characterizes the
collector hot path only. Its numerical relation to 50 µs must not be presented
as proof that the complete transaction harness meets the target.

> Reproduce on generator-class hardware and collect the complete transaction
> path before quoting a target result. The table is a developer-machine
> component reference, not a generator-pod SLA.

## Local performance evidence harness

Java Vuser measurements use a separate evidence schema and opt-in commands. See
[Java Vuser pooling implementation status](JAVA_VUSER_POOLING_IMPLEMENTATION.md)
for fixture builds, dedicated Linux collection, operator AppCDS, comparison
rules, and the unimplemented pooled/AKS execution and qualification stages.

APEX Load has an opt-in, reproducible local benchmark harness for each design
target listed in `AGENTS.md`. The harness emits a versioned JSON artifact and a
deterministic Markdown companion. It does **not** claim that any production
target is attained.

The command is `cmd/apexbench`. Normal `go test ./...` runs only its report and
validator unit tests; they do not run timed probes. No Kubernetes cluster or
external target is required.

### Coverage and interpretation

| Target | Local measurement | Report status | What remains for a target claim |
| --- | --- | --- | --- |
| Harness overhead per transaction `< 50 µs` | Production `MetricsCollector.Record` path with a concurrent drain | `not_evaluated` | Measure the complete transaction harness path, including action resolution, redaction, dispatch, result processing, and enabled telemetry |
| HTTP VUsers per pod `10,000` on 4 vCPU / 8 GB | Real Engine HTTP VUsers held concurrently at a deterministic loopback server, with request concurrency, transaction results, runtime goroutines, stack, and allocation deltas | `not_evaluated` | Repeat in the exact constrained generator image with a representative non-loopback HTTP fixture and publish duration, TPS, error, p95/p99, CPU, memory, GC, and drop evidence |
| Ramp-up `500 VUsers/second/pod` | Real `Engine.Run` VUser creation with an in-process no-op protocol | `not_evaluated` | Repeat with a representative HTTP script in the constrained generator pod and verify offered/achieved load plus generator health |
| Metrics latency `< 2 seconds` | `MetricsCollector.Record` to `LatestSnapshot` publication latency | `not_evaluated` | Timestamp and correlate the full generator → gRPC → controller merge → WebSocket → dashboard path |

Every JSON target comparison includes the raw target, the observed statistic,
and a descriptive numerical relation such as `below_target`. Its
`attainment_status` is always `not_evaluated`, because the probe scope and the
production target scope differ. The schema validator rejects a report that
changes this to `met` or otherwise claims target attainment.

### Profiles

The `smoke` profile is bounded for a developer workstation:

- 10,000 metrics-record iterations per sample, three samples;
- 100 real HTTP VUsers against the gated loopback target;
- 100 synthetic VUsers, two ramp samples;
- three collector-latency samples at a 40 ms interval.

The `release` profile collects stronger component evidence:

- 250,000 metrics-record iterations per sample, seven samples;
- 10,000 real HTTP VUsers against the gated loopback target;
- 1,000 synthetic VUsers, five ramp samples;
- seven collector-latency samples at the production one-second interval.

The exact effective configuration is embedded in every report. Raw samples are
retained alongside nearest-rank min/median/p95/max summaries.

### Fast smoke run

Print JSON to standard output without creating a file:

```bash
go run ./cmd/apexbench -profile smoke
```

Print the deterministic Markdown view instead:

```bash
go run ./cmd/apexbench -profile smoke -format markdown
```

Run the opt-in integration test:

```bash
APEXLOAD_RUN_PERFORMANCE_SMOKE=1 \
  go test ./pkg/perfevidence -run '^TestPerformanceEvidenceSmoke$' -count=1 -v
```

### Create a release-profile artifact

The repository runner requires both output paths and refuses to overwrite
either file. It never chooses or updates a canonical evidence location.

```bash
APEXLOAD_BENCHMARK_HOST_CLASS='generator-candidate-4vcpu-8gb' \
APEXLOAD_BENCHMARK_RUNNER_IMAGE='registry.example/apexload-generator@sha256:...' \
./scripts/run-performance-evidence.sh \
  /explicit/evidence/path/apexload-performance.json \
  /explicit/evidence/path/apexload-performance.md
```

`APEXLOAD_BENCHMARK_PROFILE=smoke` may be supplied when validating the artifact
pipeline itself. The runner stamps version, revision, build time, and clean or
modified worktree state. The report also discovers Go version, OS/architecture,
kernel, CPU model, logical and physical core counts, `GOMAXPROCS`, and physical
memory. Hostnames and user-identifying fields are intentionally omitted.

The command can write files directly, but only when paths are explicit:

```bash
go run ./cmd/apexbench \
  -profile release \
  -output /explicit/path/report.json \
  -markdown-output /explicit/path/report.md
```

Existing files are rejected unless the caller also supplies `-overwrite`.

### Validate or regenerate the Markdown view

Strict validation rejects unknown JSON fields, trailing JSON values, a wrong
schema version, stale summary statistics, missing probes, non-finite values,
incorrect target relations, and target-attainment claims.

```bash
go run ./cmd/apexbench -validate /explicit/path/report.json

go run ./cmd/apexbench \
  -validate /explicit/path/report.json \
  -format markdown > /tmp/review.md
```

The JSON artifact is canonical. Markdown is a deterministic rendering of that
validated JSON, not an independently editable evidence source.

### Publication checklist

Before attaching a component report to release evidence:

1. Use the `release` profile on an otherwise quiet, thermally stable host.
2. Build from the exact release revision with a clean worktree.
3. Record a meaningful host class and immutable runner-image digest when an
   image is involved.
4. Keep the JSON and Markdown files together and retain the raw JSON.
5. Confirm `validation.status` is `valid` and
   `validation.release_evidence_ready` is `true`.
6. Continue to describe all four production targets as design targets. This
   artifact is component evidence only; use the full-scope evidence in the
   coverage table before changing that language.

### Browser benchmark artifacts

This section is the code-owned browser benchmark artifact manifest for W4 browser testing parity. It defines the release evidence shape for desktop Chromium, mobile Chromium, and mobile WebKit canaries without turning browser VUsers into a high-density load claim.

The machine-readable version is exposed at:

- `GET /api/v1/system/browser-benchmarks`
- `GET /api/v1/system/parity-status` under `browser_benchmarks`

#### Claim Policy

Browser VUsers are diagnostic canaries paired with high-density protocol load. They provide screenshots, DOM summaries, console/network evidence, and Core Web Vitals. They are not APEX Load's HTTP-density load generator.

The default browser envelope remains:

| Envelope | Value |
| --- | --- |
| Recommended canary density | 1-2 browser VUsers per generator |
| Maximum claimed density without release evidence | 5 browser VUsers per generator |
| High-density real-browser claim | Not allowed without controlled raw benchmark output |
| Pairing model | Run browser canaries beside HTTP/API/JMS/WebSocket/gRPC load groups |

#### Reference Hardware

| Field | Value |
| --- | --- |
| Reference profile | `generator-canary-4vcpu-8gb` |
| CPU | 4 vCPU |
| Memory | 8 GB |
| Runtime | Local, container, or Kubernetes generator image with browser dependencies installed |
| Evidence rule | Attach raw benchmark output for the exact release generator image before raising density envelopes |

#### Profiles

##### Desktop Chromium 1080p

| Field | Value |
| --- | --- |
| Profile ID | `desktop_chromium_1080p` |
| Browser engine | Chromium |
| Device profile | `desktop_1080p` |
| Acceptance | Launch p50 < 3000 ms, action overhead < 10 ms, Chromium RSS < 250 MB per browser VUser |
| RSS attribution | Chromium CDP process snapshot |
| Evidence level | Code + docs + release artifact required |

##### Mobile Chromium Pixel 8

| Field | Value |
| --- | --- |
| Profile ID | `mobile_chromium_pixel_8` |
| Browser engine | Chromium |
| Device profile | `pixel_8` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms, Chromium RSS < 250 MB per browser VUser |
| RSS attribution | Chromium CDP process snapshot |
| Evidence level | Code + docs + release artifact required |

##### Mobile WebKit iPhone 15 Pro

| Field | Value |
| --- | --- |
| Profile ID | `mobile_webkit_iphone_15_pro` |
| Browser engine | WebKit |
| Device profile | `iphone_15_pro` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms |
| RSS attribution | Not available through CDP; use host-level memory telemetry |
| Evidence level | Code + docs + release artifact required |

#### Environment Matrix

Normal CI compiles the benchmark package and verifies all profiles remain registered. Release or dependency-upgrade validation runs the opt-in suite; see the commands in [Browser benchmark runbook](#browser-benchmark-runbook).

#### Scenario Template

Use `examples/web-api/browser-canary-plus-protocol-load.json` when creating release-gate scenarios. It demonstrates HTTP and WebSocket protocol load with desktop and mobile browser canaries, each tied back to benchmark profile IDs.

## Browser benchmark runbook

This runbook turns the mobile browser emulation benchmark requirements into a repeatable evidence collection path. Normal test runs compile the benchmark code but skip launching browsers. Operators opt in when they have a controlled host with Chromium/Chrome and Playwright WebKit assets installed.

The code-owned artifact manifest is [Browser benchmark artifacts](#browser-benchmark-artifacts) above. Controllers also expose the same benchmark profile metadata from `GET /api/v1/system/browser-benchmarks` and include profile summaries in `GET /api/v1/system/parity-status`.

### Scope

The benchmark suite covers:

- browser launch time with registered device profiles
- touch or click action latency against a local HTML fixture
- Chromium browser-process RSS per device profile when `browser.client_metrics` is enabled

WebKit profiles are included for launch and action-latency coverage. RSS attribution is currently Chromium-only because APEX Load collects browser-process metrics through CDP process information.

### Prerequisites

- Record the host shape: CPU model, core count, memory, OS, container limits, Chrome/Chromium version, Playwright version, and APEX Load commit SHA.
- Install a Chromium/Chrome binary that Playwright can drive (`npx playwright install chromium`, or point `APEXLOAD_BROWSER_EXECUTABLE` at a system Chromium as the container image does).
- Install Playwright WebKit assets (`npx playwright install webkit`) if WebKit-backed device profiles are part of the evidence run.
- Run on an otherwise quiet host. Browser benchmarks are sensitive to CPU contention, thermal throttling, and background browser processes.

### Commands

Compile-only check used by normal CI:

```bash
go test ./pkg/protocol/browser
```

Full benchmark run:

```bash
APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser \
  -run '^$' \
  -bench 'BenchmarkBrowser(InitWithDeviceProfile|TouchOrClickActionLatency|RSSPerChromiumDeviceProfile)' \
  -benchmem \
  -count 5 \
  -timeout 30m \
  | tee mobile-browser-benchmark.txt
```

The benchmark code lives in `pkg/protocol/browser/mobile_benchmark_test.go`.

### Acceptance Review

Use the conservative targets from `docs/ROADMAP.md#browser-and-mobile` until measured project data supports a higher envelope:

| Measurement | Starting Acceptance |
|:---|:---|
| Browser init with device profile | p50 under 3s on the benchmark host |
| Touch action latency over click baseline | under 10ms additional median latency |
| Chromium RSS per mobile browser VUser | under 250 MB per browser process |

Treat failures as capacity guidance, not necessarily product defects. The first response should be to lower concurrent browser VUser recommendations for that host class.

### Publication Checklist

For each release candidate, browser dependency upgrade, or published benchmark update:

1. Record CPU, memory, OS, container limits, browser versions, Playwright WebKit version, and APEX Load commit SHA; run on a quiet host matching the release generator image and note whether it was local, containerized, or Kubernetes-hosted.
2. Capture raw output from the command above and attach it to the release evidence record.
3. Summarize median and worst observed launch time, action latency, allocation count, and RSS per profile.
4. Keep the default canary envelope unless all three profile classes meet the acceptance targets, and update the conservative VUser envelope in `docs/ROADMAP.md#browser-and-mobile` only when repeated controlled runs are stable.

Do not publish higher browser-density claims from one-off laptop runs. Mobile browser VUsers remain diagnostic canaries around high-scale HTTP/JMS workloads until a controlled hardware matrix says otherwise.
