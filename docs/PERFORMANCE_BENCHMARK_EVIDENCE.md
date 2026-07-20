# Performance Benchmark Evidence

APEX Load has an opt-in, reproducible local benchmark harness for each design
target listed in `AGENTS.md`. The harness emits a versioned JSON artifact and a
deterministic Markdown companion. It does **not** claim that any production
target is attained.

The command is `cmd/apexbench`. Normal `go test ./...` runs only its report and
validator unit tests; they do not run timed probes. No Kubernetes cluster or
external target is required.

## Coverage and interpretation

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

## Profiles

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

## Fast smoke run

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

## Create a release-profile artifact

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

## Validate or regenerate the Markdown view

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

## Publication checklist

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
