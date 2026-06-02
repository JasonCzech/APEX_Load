# Benchmark Artifact: Harness Overhead Per Transaction

This is a **reproducible** benchmark of the per-transaction harness overhead — the
metrics hot path that every transaction pays on a load generator. It provides
evidence for the `< 50 µs` harness-overhead design target instead of leaving it
aspirational.

## What it measures (and what it does not)

- **Measures:** `MetricsCollector.Record` — the documented "zero-lock hot path":
  two atomic counter updates plus a non-blocking buffered-channel handoff
  (`pkg/engine/metrics.go`). This is the per-transaction bookkeeping cost the
  harness adds on top of the actual protocol I/O.
- **Does NOT measure:** end-to-end protocol time, nor full-pod density (10k
  VUsers/pod). Those remain environment- and protocol-specific and require a
  separate, hardware-pinned density run before being claimed.

## How to reproduce

```bash
go test -run='^$' -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/
```

A CI guardrail (`TestHarnessOverheadUnderTarget` in
`pkg/engine/harness_overhead_benchmark_test.go`) runs the benchmark inline and
fails if the metrics hot path regresses past a generous fraction of the budget,
so a future change that adds a lock or an allocation to `Record` is caught.

## Result (reference run)

| Benchmark | Hardware | ns/op | allocs/op |
| --- | --- | --- | --- |
| `BenchmarkHarnessOverheadPerTransaction` | Apple M4 (darwin/arm64) | ~8.4 | 0 |
| `BenchmarkHarnessOverheadParallel` (contended) | Apple M4 (darwin/arm64) | ~45 | 0 |

Interpretation: the metrics hot path costs **single-digit nanoseconds** per
transaction serially and tens of nanoseconds under heavy goroutine contention,
with **zero allocations** — three to four orders of magnitude below the 50 µs
target. The `< 50 µs` budget therefore has substantial headroom for the rest of
the per-transaction work (variable interpolation, think-time computation, result
construction).

> Reproduce on your own generator-class hardware before quoting numbers in a
> customer-facing context; the table above is a developer-machine reference, not
> a generator-pod SLA.
