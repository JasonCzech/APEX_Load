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

Java Vuser measurements use a separate evidence schema and opt-in subcommands under `cmd/apexbench java`. See
[Java shared JVM and pilot runtime](JAVA_SHARED_AND_PILOT_RUNTIME.md)
for fixture builds, dedicated Linux collection, operator AppCDS, comparison
rules, and pooled pilot runtime qualification.

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

### Java Vuser benchmark subcommands (`apexbench java`)

`cmd/apexbench` exposes a dedicated `java` subcommand family (`go run ./cmd/apexbench java <subcommand> [flags]`). Every subcommand other than `target` accepts `-output <path>` (writes validated JSON instead of stdout) and `-overwrite` (replaces an explicit output file):

| Subcommand | Key flags | Purpose |
| --- | --- | --- |
| `fixture` | `-workload light\|middleware-heavy`, `-endpoint http://127.0.0.1:18099`, repeatable `-classpath <jar>` | Emits a deterministic persistent `java_vuser` JSON DSL fixture script (`{"script": ...}`). |
| `target` | `-addr 127.0.0.1:18099` | Runs the loopback HTTP fixture target server (must bind a loopback IP and run outside the benchmark cgroup). |
| `inspect` | repeatable `-artifact <path>` | Runs `javarunner.InspectApplicationNative` over application source/class/JAR/ZIP artifacts (never JDK internals) to detect native/JNI or shared-JVM blockers. |
| `baseline` | `-script`, `-cgroup`, `-workload`, `-profile` (default `dedicated-default`), `-runtime-sha256`, `-environment`, `-resources`, `-target`, `-runner-pid`, `-vusers` (default `25`), `-repetitions` (`1..5`, default `5`), `-soak` (`0` or `1h..2h`), `-warmup` (default `40s`), `-duration` (default `1m`) | Collects dedicated one-JVM-per-VUser cgroup-v2 baseline evidence (`perfevidence.RunJavaDedicated`). |
| `pilot` | All `baseline` flags (default `-profile pooled-pilot`) plus `-pilot-manifest <path>` and `-preparation-timeout` (default `2m`) | Collects shared/pooled JVM pilot evidence (`perfevidence.RunJavaPooled`) using an operator-approved runner manifest. |
| `compare` | `-baseline <dedicated.json>`, `-candidate <pooled.json>` | Compares validated dedicated baseline JSON against pooled candidate JSON (`perfevidence.CompareJavaEvidence`) and exits non-zero if acceptance gates fail. |

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

##### Memory units and measured population

The published Chromium target of **less than 250 MB** means **less than
250,000,000 bytes**, not 250 MiB (262,144,000 bytes). The benchmark acceptance API
publishes the authoritative `rss_per_vuser_target_bytes` (`250000000`) and omits
the legacy ambiguous `rss_per_vuser_target_mb` field under measurement version
`apexload.browser_benchmark.v3`. Capacity planning converts the decimal MB tier
budgets (`180`, `220`, `260`, `280` decimal MB around the `250,000,000`-byte
target) into binary MiB (`browserDecimalMBToMiB`) to match `Generator.MemoryMB`
(`8192` MiB = 8 GiB), and observer-group scaling caps `observerMaxVUsers` at `5`
to match `MaximumClaimedVUsersPerGenerator: 5`. The target and
five-repetition/density requirements are unchanged.

Historical `apexload.browser_benchmark.v1` artifacts calculated `peak_rss_mb` and
host `memory_mb` in binary MiB despite those field names, and `apexload.browser_benchmark.v2`
included a transitional host `memory_mib` field. Keep those historical files and
their hashes intact; interpret their recorded values using 1 MiB = 1,048,576
bytes. New `apexload.browser_benchmark.v3` artifacts emit only byte-denominated
memory fields (`per_vuser_rss_bytes`, optional Linux `per_vuser_pss_bytes`,
`fixed_overhead_bytes`, `peak_rss_bytes`, and host `memory_bytes`), or null when
unavailable, along with `rss_attribution` (`cdp_renderer_per_vuser` on Chromium,
`unavailable` on WebKit) and `action_overhead_ms` / `action_overhead_kind`. They
count all attempted RSS snapshots, including partial and unavailable samples, and
name the observed process population. New Go benchmark output uses `rss_bytes/op`,
`per_vuser_rss_bytes/op`, `fixed_overhead_bytes/op`, and `action_overhead_ms/op`;
it refuses an unavailable or non-finite process snapshot instead of reporting a
measured zero. Its byte display preserves the platform sampler's resolution (for
example, KiB from `ps` or `/proc/<pid>/smaps_rollup`); converting units does not
add measurement precision.

For Chromium profiles (`desktop_1080p`, `pixel_8`), `TestBrowserBenchmark`,
`BenchmarkBrowserRSSPerChromiumDeviceProfile`, and `TestBrowserVUserMarginalRSS`
query `SystemInfo.getProcessInfo` over a browser-level CDP session before and
after creating isolated `BrowserContext`s, attributing newly spawned `renderer`
PIDs to `per_vuser_rss_bytes` (plus Linux `/proc/<pid>/smaps_rollup` `Pss` in
`per_vuser_pss_bytes`) while partitioning the Playwright driver and shared
`browser` / `gpu` / `utility` / `zygote` processes into `fixed_overhead_bytes`.
`TestBrowserBenchmark` also retains the combined descendant-tree peak in
`peak_rss_bytes` with `rss_population: "observed_test_process_descendants"`.
WebKit exposes neither `Browser.NewBrowserCDPSession` nor `SystemInfo.getProcessInfo`,
so WebKit profiles explicitly record `rss_attribution: "unavailable"`, leave
`per_vuser_rss_bytes`, `fixed_overhead_bytes`, and `peak_rss_bytes` null, and keep
`rss_per_vuser_target_bytes` omitted (`0`). Acceptance requests remain pending
review or failed, never passed automatically. See the [units review](archive/qa-reviews/BROWSER_SLICES_2026-10.md#roadmap-browser-benchmark-units-2026-10-20)
and [per-VUser attribution review](archive/qa-reviews/BROWSER_SLICES_2026-10.md#roadmap-browser-vuser-attribution-2026-10-26)
for local contract tests; reference-host qualification remains pending (`BE-C2`).

##### Desktop Chromium 1080p

| Field | Value |
| --- | --- |
| Profile ID | `desktop_chromium_1080p` |
| Browser engine | Chromium |
| Device profile | `desktop_1080p` |
| Acceptance | Launch p50 < 3000 ms, action overhead < 10 ms, Chromium RSS < 250 MB (250,000,000 bytes) per browser VUser |
| RSS attribution | `cdp_renderer_per_vuser` (`SystemInfo.getProcessInfo` renderer PIDs per `BrowserContext`, separate from `fixed_overhead_bytes`) |
| Evidence level | Code + docs + release artifact required |

##### Mobile Chromium Pixel 8

| Field | Value |
| --- | --- |
| Profile ID | `mobile_chromium_pixel_8` |
| Browser engine | Chromium |
| Device profile | `pixel_8` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms, Chromium RSS < 250 MB (250,000,000 bytes) per browser VUser |
| RSS attribution | `cdp_renderer_per_vuser` (`SystemInfo.getProcessInfo` renderer PIDs per `BrowserContext`, separate from `fixed_overhead_bytes`) |
| Evidence level | Code + docs + release artifact required |

##### Mobile WebKit iPhone 15 Pro

| Field | Value |
| --- | --- |
| Profile ID | `mobile_webkit_iphone_15_pro` |
| Browser engine | WebKit |
| Device profile | `iphone_15_pro` |
| Acceptance | Launch p50 < 3000 ms, touch overhead < 10 ms |
| RSS attribution | `unavailable` (WebKit has no CDP `SystemInfo.getProcessInfo`; use launch/action latency plus host-level memory telemetry) |
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

WebKit profiles are included for launch and action-latency coverage. The RSS benchmark runs only for the declared Chromium profiles and attributes per-context renderer RSS (`per_vuser_rss_bytes/op`) separately from shared browser/driver overhead (`fixed_overhead_bytes/op`) via CDP `SystemInfo.getProcessInfo`. WebKit RSS attribution remains `"unavailable"`.

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
| Chromium RSS per mobile browser VUser | under 250 MB (250,000,000 bytes) per browser VUser |

Treat failures as capacity guidance, not necessarily product defects. The first response should be to lower concurrent browser VUser recommendations for that host class.

### Publication Checklist

For each release candidate, browser dependency upgrade, or published benchmark update:

1. Record CPU, memory, OS, container limits, browser versions, Playwright WebKit version, and APEX Load commit SHA; run on a quiet host matching the release generator image and note whether it was local, containerized, or Kubernetes-hosted.
2. Capture raw output from the command above and attach it to the release evidence record.
3. Summarize median and worst observed launch time, action latency, allocation count, and RSS per profile.
4. Keep the default canary envelope unless all three profile classes meet the acceptance targets, and update the conservative VUser envelope in `docs/ROADMAP.md#browser-and-mobile` only when repeated controlled runs are stable.

Do not publish higher browser-density claims from one-off laptop runs. Mobile browser VUsers remain diagnostic canaries around high-scale HTTP/JMS workloads until a controlled hardware matrix says otherwise.
