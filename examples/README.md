# APEX Load Examples

Runnable sample JSON DSL scripts, CSV parameter feeds, raw `.http` request/response fixtures, and target-monitoring configuration samples.

> Note: This directory contains JSON DSL scripts, CSV feeds, `.http` fixtures, and monitoring samples — it does **not** store `.har` captures. Sample HAR import fixtures live alongside importer tests under `pkg/convert/har/` and `pkg/recorder/`.

## Directory Inventory

### Core Protocol & Distributed Smoke Scripts

| File | Description |
| --- | --- |
| `simple-http.json` | Minimal runnable HTTP JSON DSL script (`GET` + assertions + think time). |
| `simple-java-http-embedded.json` | `java_http` script using an inline `java_serialized_object_base64` payload. |
| `simple-java-http-sidecar.json` | `java_http` / sidecar-oriented Java HTTP payload example. |
| `k8s-distributed-smoke.json` | Lightweight HTTP smoke script for validating distributed Kubernetes generator pools. |
| `cnn-top-banner-browser.json`, `cnn-top-banner-browser-local-test.json` | Playwright `browser` protocol canary script and local controller test payload. |

### Multi-Protocol & Browser-Canary Scenarios (`web-api/`)

| File | Description |
| --- | --- |
| `web-api/ecommerce-http.json` | Multi-step e-commerce HTTP flow with correlation extractors and assertions. |
| `web-api/soap-account-service.json` | `soap` protocol XML envelope and fault-checked operation example. |
| `web-api/grpc-inventory.json` | `grpc` unary/streaming service invocation example. |
| `web-api/websocket-order-feed.json` | `websocket` connect/send/receive order-feed script. |
| `web-api/jms-order-flow.json` | `jms-solace` publish/consume messaging flow. |
| `web-api/browser-canary-plus-protocol-load.json`, `web-api/mixed-http-browser-canary.json` | Hybrid scenario definitions pairing high-density HTTP groups with low-density `browser` observer canaries. |
| `web-api/archive-smoke-fixtures.json` | Fixture definitions for archive generation and report smoke checks. |

### External File Payloads (`bodyrefs/`)

| File | Description |
| --- | --- |
| `bodyrefs/body-file-http.json` | HTTP script referencing an external request payload via `"file"` data sources / body references. |
| `bodyrefs/payloads/order.json` | Sample JSON order body consumed by `bodyrefs/body-file-http.json`. |

### Async LLM Polling Scripts & CSV Prompt Feeds

| File | Description |
| --- | --- |
| `openai-chat-completions-poll.json`, `openai-chat-completions-poll-csv.json`, `openai-responses-poll.json`, `openai-local-controller-test.json`, `openai-prompts.csv` | OpenAI Chat Completions and Responses API load scripts (parameterized via `openai-prompts.csv` and `{{env.*}}` secrets). |
| `grok-chat-completions-poll.json`, `grok-chat-completions-poll-csv.json`, `grok-responses-poll.json`, `grok-local-controller-test.json`, `grok-prompts.csv` | xAI Grok Chat Completions and Responses API load scripts and CSV prompt feed. |

### Raw `.http` Request / Response & REST API Fixtures

| File | Description |
| --- | --- |
| `raw-request-1.http`, `raw-request-2.http`, `raw-response-1.http`, `raw-response-2.http` | Raw HTTP request/response pairs for testing the **Import → Request / Response Sample** converter (`pkg/convert/requestimport`). |
| `cnn-upload-top-banner-browser-script.http`, `cnn-create-top-banner-browser-test.http`, `cnn-start-top-banner-browser-test.http` | Step-by-step controller REST requests for uploading, creating, and starting a browser test. |
| `grok-upload-chat-completions-script.http`, `grok-upload-chat-completions-csv-script.http`, `grok-upload-responses-script.http`, `grok-create-local-test.http`, `grok-start-local-test.http` | Controller REST `.http` requests for uploading and launching Grok API scripts. |

### Target Monitoring Samples (`monitoring/`)

| File | Description |
| --- | --- |
| `monitoring/dynatrace-dry-run-request.json` | Sample Dynatrace connection/discovery dry-run payload (see [../docs/DYNATRACE.md](../docs/DYNATRACE.md)). |
| `monitoring/elastic-collector-targets.json` | Sample Elastic/OpenSearch monitoring target configuration (see [../docs/ELASTIC.md](../docs/ELASTIC.md)). |
| `monitoring/oracle-db-external-metrics.json`, `monitoring/oracle-db-prometheus-sample.prom` | Sample Prometheus external-metrics overlay definition and `.prom` exposition fixture (see [../docs/PROMETHEUS_MONITORING.md](../docs/PROMETHEUS_MONITORING.md)). |

## Running an Example Locally

```bash
go run ./cmd/apexctl run --script examples/simple-http.json --vusers 5 --duration 15s
```

See [../docs/HELP_DSL_OPERATOR_GUIDE.md](../docs/HELP_DSL_OPERATOR_GUIDE.md) and [../docs/DSL_ACTION_REFERENCE.md](../docs/DSL_ACTION_REFERENCE.md) for full DSL authoring syntax.
