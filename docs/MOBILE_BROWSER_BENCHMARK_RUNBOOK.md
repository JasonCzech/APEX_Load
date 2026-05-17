# Mobile Browser Benchmark Runbook

This runbook turns the mobile browser emulation benchmark requirements into a repeatable evidence collection path. Normal test runs compile the benchmark code but skip launching browsers. Operators opt in when they have a controlled host with Chromium/Chrome and Playwright WebKit assets installed.

## Scope

The benchmark suite covers:

- browser launch time with registered device profiles
- touch or click action latency against a local HTML fixture
- Chromium browser-process RSS per device profile when `browser.client_metrics` is enabled

WebKit profiles are included for launch and action-latency coverage. RSS attribution is currently Chromium-only because Stormrunner collects browser-process metrics through CDP process information.

## Prerequisites

- Record the host shape: CPU model, core count, memory, OS, container limits, Chrome/Chromium version, Playwright version, and Stormrunner commit SHA.
- Install Chromium/Chrome for `chromedp`.
- Install Playwright WebKit assets if WebKit-backed device profiles are part of the evidence run.
- Run on an otherwise quiet host. Browser benchmarks are sensitive to CPU contention, thermal throttling, and background browser processes.

## Commands

Compile-only check used by normal CI:

```bash
go test ./pkg/protocol/browser
```

Full benchmark run:

```bash
STORMRUNNER_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser \
  -run '^$' \
  -bench 'BenchmarkBrowser(InitWithDeviceProfile|TouchOrClickActionLatency|RSSPerChromiumDeviceProfile)' \
  -benchmem \
  -count 5 \
  -timeout 30m \
  | tee mobile-browser-benchmark.txt
```

The benchmark code lives in `pkg/protocol/browser/mobile_benchmark_test.go`.

## Acceptance Review

Use the conservative targets from `docs/MOBILE_BROWSER_EMULATION_PLAN.md` until measured project data supports a higher envelope:

| Measurement | Starting Acceptance |
|:---|:---|
| Browser init with device profile | p50 under 3s on the benchmark host |
| Touch action latency over click baseline | under 10ms additional median latency |
| Chromium RSS per mobile browser VUser | under 250 MB per browser process |

Treat failures as capacity guidance, not necessarily product defects. The first response should be to lower concurrent browser VUser recommendations for that host class.

## Publication Checklist

For each published benchmark update:

1. Capture raw output from the command above.
2. Summarize median and worst observed launch time, action latency, allocation count, and RSS per profile.
3. Include hardware, OS, browser/runtime versions, commit SHA, and whether the run was local, containerized, or Kubernetes-hosted.
4. Update the conservative VUser envelope in `docs/MOBILE_BROWSER_EMULATION_PLAN.md` only when repeated runs are stable.
5. Link the raw benchmark artifact from the release note or internal evidence record.

Do not publish higher browser-density claims from one-off laptop runs. Mobile browser VUsers remain diagnostic canaries around high-scale HTTP/JMS workloads until a controlled hardware matrix says otherwise.
