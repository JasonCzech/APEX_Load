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

## Browser DSL Quick Reference

Protocol `browser`; a mobile example sets `config.browser` to
`{"engine": "webkit", "device_profile": "iphone_15_pro", "touch_mode": true,
"orientation": "portrait", "client_metrics": true, "slow_mo_ms": 50,
"viewport": {"width": 393, "height": 852}}`.

- Actions: `browser_navigate`, `browser_click`, `browser_fill` (`value` = text),
  `browser_select` (`value` = option value, text, or label), `browser_wait`,
  `browser_screenshot`, `browser_tap`, `browser_long_press`
  (`params.duration_ms`), and `browser_swipe` (`params.direction` of `left`,
  `right`, `up`, `down`; `params.distance` in positive pixels).
- Engines and presets: `chromium` supports `desktop_1080p`, `galaxy_s24`,
  `pixel_8`; `webkit` supports `ipad_air`, `iphone_15_pro`, `iphone_se` and
  requires Playwright WebKit assets on the controller host.
- Config keys: `config.browser.engine`, `device_profile`, `touch_mode`,
  `orientation` (`portrait`/`landscape`), `client_metrics`.
- Summary metrics: `avg_fcp_ms`, `avg_lcp_ms`, `avg_cls_score`, `avg_inp_ms`,
  `avg_blocking_resources`, `avg_long_task_count`, `avg_js_heap_used_mb`; Chromium
  with `client_metrics` also reports `browser_process_cpu_pct` /
  `browser_process_mem_mb` per transaction and `avg_browser_process_cpu_pct` /
  `avg_browser_process_mem_mb` on summaries.
- Live and archived summaries appear under `metrics.browser` and
  `metrics.browser_by_device`. Evidence bundles download from
  `/api/v1/tests/{id}/archive/browser-diagnostics` (`browser_diagnostics.json`
  plus screenshot and HTML artifacts when available).
- Java-over-HTTP reuses the HTTP transport with the `java_serialized_object_base64`
  codec; sample imports are limited to raw HTTP and cURL bodies that already
  contain base64-encoded serialized Java bytes.

## Evidence And Benchmarks

- Benchmark artifact: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts)
- Benchmark runbook: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook)
- Opt-in harness:

```bash
APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser' -benchmem -count 5
```

Update density guidance only when repeated controlled runs justify it.

## Detailed Planning References

The following files are retained for implementation history and tactical detail:

- [ROADMAP.md](ROADMAP.md#browser-and-mobile)
