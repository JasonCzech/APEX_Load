# Browser And Mobile Support

Status: Current

APEX Load supports browser replay and mobile web emulation as diagnostic
canaries alongside higher-density protocol load. Browser VUsers are intentionally
low-concurrency and evidence-rich; they are not a replacement for HTTP/API,
JMS, Kafka, or other high-density protocol groups.

## Current Support

- Browser protocol actions run through Playwright-backed Chromium/WebKit flows.
- Mobile profiles include device viewport, user agent, touch mode, scale, and
  orientation for supported presets such as Pixel, Galaxy, iPhone, and iPad
  profiles.
- `config.browser.client_metrics` enables Core Web Vitals and related page
  timing metrics, including FCP, LCP, CLS, INP, DOM/load timing, long-task
  counts, blocking-resource counts, JS heap, screenshots, HTML, console, and
  network evidence where available.
- Chromium runs with client metrics enabled also capture browser-process CPU
  and RSS memory through CDP process information plus OS lookup. WebKit process
  attribution remains limited to metrics Playwright exposes reliably.
- Live metrics expose browser and browser-by-device summaries. Archives retain
  per-action browser evidence and report browser-by-engine, platform, and device
  rollups.
- Goal-oriented runs can use browser observer canaries for Web Vitals while
  keeping those canary groups pinned during scaling.

## Operating Model

- Pair a small browser canary group with larger HTTP/API or messaging groups.
- Use browser diagnostics for user-experience evidence: screenshots, DOM,
  console logs, network entries, Web Vitals, and selector/wait repair guidance.
- Keep browser density conservative until local benchmark evidence supports a
  higher envelope.
- Treat mobile emulation as web UX simulation, not native mobile app automation.

## Evidence And Benchmarks

- Benchmark artifact: [BROWSER_BENCHMARK_ARTIFACTS.md](BROWSER_BENCHMARK_ARTIFACTS.md)
- Benchmark runbook: [MOBILE_BROWSER_BENCHMARK_RUNBOOK.md](MOBILE_BROWSER_BENCHMARK_RUNBOOK.md)
- Opt-in harness:

```bash
APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser' -benchmem -count 5
```

Update density guidance only when repeated controlled runs justify it.

## Detailed Planning References

The following files are retained for implementation history and tactical detail:

- [MOBILE_BROWSER_EMULATION_PLAN.md](MOBILE_BROWSER_EMULATION_PLAN.md)
- [BROWSER_CLIENT_METRICS_PLAN.md](BROWSER_CLIENT_METRICS_PLAN.md)
- [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md)
- [BROWSER_REPLAY_HAR_CAPTURE_PLAN.md](BROWSER_REPLAY_HAR_CAPTURE_PLAN.md)
