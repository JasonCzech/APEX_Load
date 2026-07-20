# Benchmark Artifact: Harness Overhead Per Transaction

This is a reproducible benchmark of one component of per-transaction harness
overhead: the metrics hot path every result pays on a load generator. It does
not, by itself, evaluate the `< 50 µs` complete-harness design target.

## What it measures (and what it does not)

- **Measures:** `MetricsCollector.Record` — the documented "zero-lock hot path":
  two atomic counter updates plus a non-blocking buffered-channel handoff
  (`pkg/engine/metrics.go`). This is the per-transaction bookkeeping cost the
  harness adds on top of the actual protocol I/O.
- **Does NOT measure:** action resolution, redaction, protocol dispatch, result
  handling outside the collector, end-to-end protocol time, or full-pod density.
  Those remain environment- and protocol-specific and require hardware-pinned
  evidence before being claimed.

## How to reproduce

```bash
go test -run='^$' -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/
```

A CI component guardrail (`TestHarnessOverheadUnderTarget` in
`pkg/engine/harness_overhead_benchmark_test.go`) runs the benchmark inline and
fails if the metrics hot path regresses past a generous fraction of the budget,
so a future change that adds a lock or allocation to `Record` is caught. Passing
that guardrail is not a full-harness target-attainment result.

For versioned JSON, machine/build metadata, raw samples, and the other three
performance-target probes, use
[`PERFORMANCE_BENCHMARK_EVIDENCE.md`](PERFORMANCE_BENCHMARK_EVIDENCE.md).

## Result (reference run)

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
