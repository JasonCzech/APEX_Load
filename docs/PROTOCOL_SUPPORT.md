# Protocol Support

Current support boundaries and evidence for web/API, browser and mobile, messaging, Java Vuser, and SAP GUI execution.


## Web/API Execution Support Matrix

This document is the W1 evidence record for modern Web/API execution parity.

### API Surface

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/scripts/web-api/support-matrix` | Code-owned Web/API support, benchmark envelope, diagnostics, and sample-project matrix. |
| `GET /api/v1/runtime-settings/support-matrix` | Unified runtime setting parity matrix, presets, report exports, and streaming/media execution decision across Web/API/browser protocols. |
| `POST /api/v1/scripts/import/sample` | Imports raw HTTP, cURL, and REST samples through the shared HTTP request finalizer. |
| `POST /api/v1/scripts/import/har` | Imports HAR requests while preserving ordered header occurrences and governed source review. |
| `POST /api/v1/scripts/import/postman` | Imports Postman collections with collection/folder/request inheritance resolved by the adapter. |
| `POST /api/v1/scripts/import/jmeter` | Imports JMeter plans (`.jmx`, a ZIP of plans, or a LoadRunner Enterprise JMeter `.usz`); HTTP samplers use the shared request finalizer while non-HTTP sampler routes retain their existing converters. |
| `POST /api/v1/scripts/import/k6` | Imports the statically proven k6 HTTP subset; APEX Load does not execute k6 JavaScript. |
| `POST /api/v1/scripts/import/openapi` | Imports OpenAPI 3.x or Swagger 2.0 JSON/YAML into a reviewable HTTP script draft. |
| `POST /api/v1/scripts/import/wsdl` | Imports WSDL into a reviewable SOAP script draft with generated operation envelopes. |
| `POST /api/v1/scripts/import/enterprise-script-archive` | Imports a bounded, path-safe enterprise ZIP and then applies its protocol-specific adapter. |
| `GET /api/v1/scripts/import/devweb/support-matrix` | Publishes DevWeb construct coverage and review requirements. |

OpenAPI and WSDL import endpoints retain the shared script-import envelope:

- `script` and `scripts`
- `warnings`
- `stats`
- `summary`
- `review_sections`
- `unsupported_elements`
- `repair_suggestions`

This keeps OpenAPI/WSDL acquisition aligned with HAR, JMeter, Enterprise Script Archive, sample, and DevWeb imports.

### Shared Request Import Pipeline

HTTP-producing adapters now converge on:

**source adapter → effective request → shared credential projection → shared DSL finalization/readiness → existing API workflow**

The source adapter still owns format-specific parsing, inheritance, schema/reference resolution, and recognition of source language constructs. The shared stages own runtime methods and limits, URL/header/auth/body projection, credential requirements, request-shaped action traversal, missing binding analysis, durable execution gates, and final DSL validation.

`header_list` is the authoritative logical occurrence list in the DSL and retains active duplicates, authored casing, and source order. The compatibility `headers` map is derived with the last case-insensitive occurrence winning, and map-only sources receive deterministic name order plus the informational `source_order_unavailable` diagnostic. HTTP transports may normalize header casing or order, combine protocol-compatible repeated values, and own framing headers. When a special header occurrence cannot retain its meaning—such as duplicate singleton, transport-owned, or protocol-incompatible connection headers—the importer omits the unsafe occurrence, records value-free fidelity evidence, and attaches a durable `import_review_required` gate; acknowledgement does not make the request executable.

HTTP version selection follows the native runtime contract: for protocol `http`, an empty `config.http.version` negotiates HTTP/2 or HTTP/1.1; protocol aliases `http/1.1`, `http/2` and `http/3` instead supply exact inherited defaults. A nonempty global setting takes precedence. Request leaves can omit `http_version` to inherit or explicitly select `auto`, `1.1`, `2`, `auto+h3` or `3`. Exact HTTP/2 uses HTTPS h2 or cleartext prior-knowledge h2c without HTTP/1 fallback. Null, empty and malformed leaf overrides are invalid. The HTTP/3 policies (`auto+h3` upgrades to an advertised HTTP/3 alternative; `3` requires HTTP/3 with no fallback) validate and save on every installation, and `HTTP/3`, `HTTP/3.0` and `h3` in `config.http.version` normalize to `3`; draft tokens such as `h3-29` are never a policy. Exact `3` is rejected on save with an `http://` URL, a TLS maximum below 1.3, a custom proxy or integrated (NTLM / Negotiate) authentication, and either HTTP/3 policy with network shaping. Both HTTP/3 policies run as a preview on installations that set `APEXLOAD_HTTP3=true` on the controller and every generator. Elsewhere a run whose requests resolve to an HTTP/3 policy is refused at admission (start, replay, scheduled launch, scale-out, replacement, recovery, CLI and local runs) with `HTTP/3 execution is not enabled on this installation`, naming the flag; distributed groups need generators advertising `native_http3_v1` (and `http3_upgrade_v1` for `auto+h3`). A script whose HTTP/3 policy every request overrides is not HTTP/3 work and runs normally. Exact `3` carries the `http`, `soap` and `java_http` protocols, SSE streams, run correlation and trace context; it shares cookies, the policy-partitioned cache, redirects, target policy and the per-host connection budget with the TCP policies, runs every redirect hop on HTTP/3, and fails rather than falling back: a failure names whether the request was `not_sent`, `possibly_sent` or `rejected_unprocessed`. Proxied routes, integrated authentication and network shaping are refused before sending. `auto+h3` starts on TCP and moves an origin to QUIC once a response advertises a same-host `h3` alternative (any port; requests carry `Alt-Used`). It stays on TCP for cleartext origins, integrated authentication, a TLS maximum below 1.3, proxied routes and origins whose QUIC attempt failed within `broken_ttl_ms`, and receipts name the reason (`http_version_selection_reason`). It falls back to TCP only when the request cannot have reached the target (`http_version_fallback: quic_connect_failed`), reruns an `H3_VERSION_FALLBACK` refusal on HTTP/1.1 only when that is replay-safe, retries a 421 from the alternative on TCP, and fails a `possibly_sent` request rather than repeat it. HTTP/3 receipts add `quic_connection` (new, reused, joining a connection still being opened, or `dial_failed`), `tls_resumed`, `attempt_count`, `quic_error_class` and `timing_availability`; TLS is part of the QUIC handshake, so ConnectTime includes it and TLSTime is 0. Policy pools share the VUser session and physical connection ceiling; cache entries remain distinct by policy. Replay reports an actual supported response version only from wire evidence, and cache hits do not establish a negotiated version.

Compressed responses (gzip, deflate, Brotli, Zstandard) decode through a process-wide workspace budget sized at one eighth of `GOMEMLIMIT` (256 MiB to 4 GiB). Each response reserves its whole decoding workspace at once and, when the budget is full, waits up to 10 seconds for other responses to finish instead of failing. Any wait is reported as `response_decoder_wait_ms` in the request's protocol evidence; a response that still cannot be admitted fails with a load-generator capacity error rather than a target error.

Supported HAR request observations become leaf policies according to the import's `http_version_mode` (HAR import, sample import, both recorders and browser-to-HTTP conversion): `upgrade`, the default, maps captured final HTTP/3 (`h3`, `HTTP/3`) to `auto+h3` and keeps HTTP/2 and HTTP/1.1 exact; `exact` maps captured HTTP/3 to `3`; `inherit` gives no request a leaf policy and reports dropped HTTP/3 as `h3_not_preserved`. Mapped HTTP/3 is reported per origin as `captured_h3`, and the observation stays in `http_version_capture`, apart from the policy. An origin that only advertised HTTP/3 keeps its captured versions; moving it to `auto+h3` is an explicit choice. A draft HTTP/3 token (`h3-29`) is kept as the observation, replays on the inherited policy, and waits for review before promotion (`http3_draft_token`). A missing or unknown observation and HTTP/1.0 are capture-quality warnings: the request keeps the script's inherited policy and is not gated. Malformed values and contradictory recorder provenance retain durable review gates. Recorder exports identify client-to-proxy and proxy-to-origin observations separately; differing or unavailable legs require review, and legacy fabricated HTTP/1.1 values do not establish an observation. Browser captures (the browser recorder and browser VUser run HARs) name their source in the entry's provenance; import treats it as description only, and provenance that contradicts the entry's version is gated. HTTP/1.0 remains unsupported for native replay, and an HTTP/3 policy runs only where HTTP/3 execution is enabled. Original source artifacts remain available under their existing disclosure policy.

HTTP/3 observations: native replay records `alt_svc_h3_advertised` when a wire response advertises final HTTP/3 in `Alt-Svc`, without changing routing. HAR imports and both recorders raise one `origin_advertises_h3` info finding per HTTPS origin that advertised HTTP/3, saying whether any captured request used it; captured requests that used it follow `http_version_mode`. cURL's `--http1.1` maps to exact HTTP/1.1 and `--http2-prior-knowledge` on `http://` to exact HTTP/2 (h2c); `--http2` and `--http2-prior-knowledge` on HTTPS map to automatic negotiation, because curl still negotiates TLS requests through ALPN; `--http2` on `http://` (curl's `Upgrade: h2c`), `--http1.0`, `-0`, `--http3` and `--http3-only` keep execution gates. DevWeb `rts.yml` settings that enable HTTP/3 replay raise a `devweb_http3_enabled` disclosure and are not mapped. See [HTTP3_IMPLEMENTATION.md](HTTP3_IMPLEMENTATION.md) for the delivery plan.

Credential projection covers URL components, every ordered header occurrence, typed auth, JSON, XML, form-urlencoded, multipart, and GraphQL. Exact governed runtime values remain dynamic; source literals become whole `{{secret.NAME}}` references only when encoding and wire shape can be preserved. Raw migration values are confined to protected in-memory/session handoff and are excluded from scripts, diagnostics, IDs, source review, logs, and receipts. `sanitize_sensitive=false` remains a converter compatibility option, but detected literals add the durable `import_credentials_unmanaged` execution gate.

#### Conversion dispositions

These dispositions describe the result of conversion, not the breadth labels (`supported`, `partial`, or `review_only`) used by source support matrices:

| Disposition | Meaning |
| --- | --- |
| `executable_exact` | The supported source behavior is represented without a known wire-semantic loss. Missing deployed values for valid `{{secret.*}}` references remain a dynamic execution preflight check. |
| `executable_bounded` | The request is replay-safe after a documented clamp to a native runtime ceiling, such as timeout or redirect limits. A stable diagnostic is retained; an adapter may still require acknowledgement before promotion. |
| `blocked_draft` | A structurally valid, disclosure-safe script can be saved, but durable review, missing-variable/file, unsupported-auth, residual-source-logic, or unmanaged-credential gates stop replay and launch before transport. Acknowledgement alone never clears an execution gate. |
| `rejected` | No valid disclosure-safe draft can be constructed. Examples include credential-bearing malformed structured content, type-unsafe sensitive values, or an unsafe archive. No runnable script is returned. |

Any DSL validation issue remaining after shared finalization is an importer invariant failure, not a warning attached to a runnable script.

#### Tested adopter dispositions

| Adapter | `executable_exact` | `executable_bounded` | `blocked_draft` | `rejected` |
| --- | --- | --- | --- | --- |
| Sample / raw HTTP / cURL / REST / HAR | Static supported requests; ordered raw/HAR headers; byte-preserved nonsensitive bodies; governed URL/header/body credentials | Source timeout values use the native HTTP ceiling and retain a clamp diagnostic | Missing references, unmanaged credentials, malformed-but-disclosure-safe declared bodies, non-origin raw request targets, malformed captured HTTP versions, or HTTP/2 pseudo-headers that disagree with the request line. Captured HTTP/3 follows `http_version_mode`; missing, `unknown` or non-selectable captured versions (HTTP/1.0, draft HTTP/3 tokens) inherit the script policy with a warning; agreeing pseudo-headers and a Content-Length the capture explains (the exact bytes, another standard form encoding of `postData.params`, or a binary body stored one byte per character) are regenerated by the transport. A request body stored after decoding its Content-Encoding replays uncompressed with a warning; a body whose binary bytes were lost stays gated | Sensitive non-string structured values or credential-bearing malformed content that cannot be made safe |
| Postman | Supported static requests; ordered duplicate headers; collection → folder → request auth; basic, bearer, digest, API key; supported body modes and translated assertion/correlation subset | Not currently emitted by the Postman adapter | NTLM/signing/unknown auth, residual pre-request/test JavaScript, missing/cyclic bindings, or missing multipart files | Unsafe structured credentials or a request for which no valid safe draft can be built |
| JMeter HTTP | HTTP samplers, ordered HeaderManager entries, basic/bearer/digest, supported assertions/extractors, mapped control flow, and per-user Counter elements as native counters | Oversized HTTP timeout/redirect settings are clamped to runtime capabilities with stable diagnostics; shared Counters and `__counter(FALSE, ...)` become unique-number blocks (distinct per VUser, not JMeter's global order) | Kerberos/NTLM/negotiate, complex or residual scripting/control behavior, missing bindings, and shared Counters with an increment other than 1; messaging samplers inside an HTTP thread group are review-gated rather than converted | Unsafe structured credential content or invalid input that cannot produce a valid draft; non-HTTP samplers keep their existing routes |
| OpenAPI | Supported operations and parameters, examples, native digest, and the first fully supported OR security alternative with every scheme in its AND requirement | Not currently source-emitted | Missing base URL/files/variables, conflicting or unsupported security combinations, and ambiguous behavior that affects replay | Unsafe typed/malformed credential examples or a document with no safely constructible operation |
| WSDL | Fully resolved HTTP document/literal operations with preserved SOAP version, action, namespaces, content type, and nonsensitive envelope bytes | Not currently source-emitted | RPC/encoded ambiguity, missing endpoints/binding operations, non-HTTP or undeclared transports, unresolved imports, incomplete input/header/XSD skeletons, mixed sensitive XML, or unresolved credential/file/variable bindings | Unsafe WSDL ZIP input, malformed sensitive XML, or no viable service/binding/operation |
| k6 | Statically proven string-literal GET, POST, PUT, PATCH, DELETE, HEAD, and OPTIONS calls | Not currently emitted by the k6 adapter | Dynamic expressions, loops/branches, checks, thresholds, modules, browser behavior, or other residual JavaScript | Unsafe structured credential literals or a call that cannot produce a disclosure-safe valid request |
| Safe archive ingress | A validated regular-file archive proceeds to WSDL, DevWeb, or Enterprise Script Archive root selection | — | Unsafe containers are never downgraded to drafts; downstream adapter behavior may still be blocked | Traversal/absolute/drive paths, links/devices, invalid names, depth/case/NFC/separator collisions, entry/size floods, excessive compression ratios, declared-size mismatch, or decompression bombs |

The shared ZIP/TAR reader is bounded and in-memory. WSDL ZIP, DevWeb ZIP, and Enterprise Script Archive ZIP currently use it. Bruno archive ingestion retains its compatibility path until its path, collision, identifier, and receipt-order tests can adopt the shared reader without changing the Bruno contract.

### Support Matrix

`GET /api/v1/scripts/web-api/support-matrix` currently covers:

| Capability | Status | Execution posture |
| --- | --- | --- |
| HTTP/Web request execution | Supported | Executable |
| OpenAPI-assisted REST authoring | Supported | Disposition-dependent: exact supported operations execute; incomplete behavior remains a blocked draft |
| SOAP/WSDL-assisted execution | Supported | Disposition-dependent: document/literal operations can execute; RPC/encoded ambiguity remains blocked |
| Java-over-HTTP | Supported | Executable |
| WebSocket | Supported | Executable |
| gRPC | Supported | Executable |
| JMS Solace | Supported | Executable with mock/local validation and controlled broker settings |
| Browser canaries | Supported | Diagnostic canary, not density load |
| DevWeb import | Supported | Review, then executable for mapped constructs |
| HAR/JMeter/Enterprise Script Archive migration | Supported | Disposition-dependent after shared request and archive safety checks |

### OpenAPI Import

The OpenAPI importer converts API design documents into HTTP DSL drafts:

- Extracts title, version, servers, and supported operations.
- Supports OpenAPI 3.x and Swagger 2.0 in JSON or YAML form.
- Converts GET, POST, PUT, PATCH, DELETE, HEAD, and OPTIONS operations into request actions; TRACE remains review-only.
- Builds path/query parameter placeholders using `{{data.openapi.<column>}}`.
- Generates an inline CSV seed data source for detected parameters and request-body fields.
- Applies native basic, bearer, digest, API-key, and supported OAuth behavior per operation. It selects the first fully supported OR alternative in source order and applies every scheme in the selected AND requirement deterministically.
- Generates request bodies from examples or simple object schemas.
- Adds status-code assertions from successful response definitions.
- Returns review sections for operations, auth, and data fit.

Conflicting or unsupported security combinations and ambiguous schema behavior produce valid blocked drafts. Customer-grade data and environment-specific values still require author review or runtime binding before replay or promotion.

### WSDL Import

The WSDL importer converts SOAP service metadata into SOAP DSL drafts:

- Extracts service name, target namespace, endpoint address, port types, bindings, and operations.
- Detects SOAP 1.1 versus SOAP 1.2 from the WSDL content.
- Preserves SOAPAction values when present.
- Generates SOAP envelope scaffolds per operation.
- Uses byte-preserving shared XML credential projection for simple element text and attributes.
- Enables SOAP fault handling by default.
- Returns review sections for operations, payload review, and namespace preservation.

Document/literal operations can be exact. RPC/encoded ambiguity and sensitive mixed-content XML are sanitized and durably blocked; complex XSD payload generation remains scaffolded for review. Authors must confirm required elements and replace unresolved placeholders before production replay.

### DevWeb Matrix

`GET /api/v1/scripts/import/devweb/support-matrix` now publishes construct-by-construct coverage:

| Construct | Status |
| --- | --- |
| `load.WebRequest({...}).send()` | Supported and executable |
| `load.thinkTime(seconds)` | Supported and executable |
| `load.WebSocket` open/send/receive/close | Supported and executable when constructor URL is statically resolved |
| `client.invoke(service, method, payload)` | Partial; requires gRPC schema/metadata review |
| `load.action(name, fn)` | Supported as action-name grouping |
| Dynamic JavaScript runtime behavior | Review-only |
| Custom correlation helpers | Partial; use APEX Load guided correlation repair before approval |

### Planning Envelopes

Bruno HTTP requests can be converted into native APEX scripts. The Bruno compatibility runner is retired and excluded from builds. Canonical `bruno_run` scripts cannot execute; re-import the retained source as native HTTP drafts and resolve conversion findings before replay. See [retired integrations](RETIRED_INTEGRATIONS.md).

The Web/API support endpoint mirrors the protocol density envelopes in `docs/ENTERPRISE_FLEET_AND_CAPACITY.md`. These are design/planning targets, not measured benchmark results.

| Protocol | Default envelope |
| --- | ---: |
| HTTP / HTTPS / HTTP/2 | 10,000 VUsers / generator |
| gRPC | 12,000 VUsers / generator |
| Java-over-HTTP | 7,000 VUsers / generator |
| WebSocket | 6,000 VUsers / generator |
| SOAP | 5,000 VUsers / generator |
| JMS / Solace / IBM MQ | 3,000 VUsers / generator |
| Browser canaries | 1-5 VUsers / generator (2 recommended) |

These are conservative planning envelopes unless a deployment publishes local benchmark evidence with hardware, generator profile, script complexity, VUsers, TPS, and archive evidence.

### Diagnostics

The Web/API support endpoint also documents protocol-specific diagnostics:

| Protocol | Diagnostic examples |
| --- | --- |
| HTTP | status, TTFB, TLS/connect, redirects, content checks, network shaping |
| SOAP | operation, SOAPAction, fault code, fault string, fault detail |
| Java-over-HTTP | codec, helper mode, mapping failures, binary result preservation |
| WebSocket | connect latency, send/receive counts, timeout, message size |
| gRPC | service/method, status, metadata, stream message counts |
| JMS | destination, message ID, correlation ID, dead-letter reason, delivery count |
| Browser | client metrics, screenshots, console/network evidence |

### Sample Projects

W1 adds a first-party Web/API sample pack:

| Sample | Path |
| --- | --- |
| E-commerce HTTP checkout | `examples/web-api/ecommerce-http.json` |
| SOAP account service | `examples/web-api/soap-account-service.json` |
| gRPC inventory | `examples/web-api/grpc-inventory.json` |
| WebSocket order feed | `examples/web-api/websocket-order-feed.json` |
| JMS order flow | `examples/web-api/jms-order-flow.json` |
| Mixed HTTP plus browser canary scenario | `examples/web-api/mixed-http-browser-canary.json` |
| Browser canary plus protocol load release-gate template | `examples/web-api/browser-canary-plus-protocol-load.json` |
| Archive smoke fixture contract | `examples/web-api/archive-smoke-fixtures.json` |

The sample pack is intentionally small and reviewable. It exists to let evaluators see protocol-specific script structure, data-source placeholders, diagnostics hooks, archive-signal expectations, and the browser-canary positioning without reading the codebase first.

### Plan Mapping

| Work item | Status | Evidence |
| --- | --- | --- |
| W1.1 Web/API execution support matrix | Complete | `GET /api/v1/scripts/web-api/support-matrix`, `pkg/api/web_api_support.go` |
| W1.2 OpenAPI import/authoring review | Complete for backend import/review foundation | `POST /api/v1/scripts/import/openapi`, `pkg/convert/openapi` |
| W1.3 WSDL-assisted SOAP authoring | Complete for backend import/review foundation | `POST /api/v1/scripts/import/wsdl`, `pkg/convert/wsdl` |
| W1.4 Benchmark-calibrated density envelopes | Complete for code-owned publication of existing envelopes | Support endpoint plus `docs/ENTERPRISE_FLEET_AND_CAPACITY.md` |
| W1.5 Execution smoke fixtures | Complete for sample pack, archive-signal fixture contract, and import/API tests; live archived-report publication remains benchmark evidence | `examples/web-api`, converter/API tests |
| W1.6 DevWeb construct support matrix | Complete | `GET /api/v1/scripts/import/devweb/support-matrix`, `pkg/convert/devweb/support.go` |
| W1.7 Protocol-specific diagnostics | Complete for code-owned support publication using existing live/archive signals | Support endpoint diagnostics plus protocol archive implementations |
| W1.8 Web/API sample projects | Complete | `examples/web-api/*.json` |


## Browser execution and mobile emulation

Status: Current

APEX Load supports browser replay and mobile web emulation as diagnostic
canaries alongside higher-density protocol load. Browser VUsers are intentionally
low-concurrency and evidence-rich; they are not a replacement for HTTP/API,
JMS, Kafka, or other high-density protocol groups.

### Current Support

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

### Operating Model

- Pair a small browser canary group with larger HTTP/API or messaging groups.
- Use browser diagnostics for user-experience evidence: screenshots, DOM,
  console logs, network entries, Web Vitals, and selector/wait repair guidance.
- Keep browser density conservative until local benchmark evidence supports a
  higher envelope.
- Treat mobile emulation as web UX simulation, not native mobile app automation.

### Browser DSL Quick Reference

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
  `orientation` (`portrait`/`landscape`), `client_metrics`, `quic`
  (`auto`, the default, lets the engine use HTTP/3 when a site advertises it;
  `off` launches Chromium or Firefox with HTTP/3 disabled; WebKit supports
  only `auto`).
- With document evidence capture on (`config.browser.capture_document_metrics:
  true`), native request evidence (version 4) records each resource's
  negotiated protocol (`h1`, `h2`, `h3`) or why it is unknown: Chromium from
  CDP, WebKit from Resource Timing `nextHopProtocol` (same-origin or
  Timing-Allow-Origin only). Cached and service-worker responses never claim
  a protocol. Without that opt-in, and on Firefox, whose requests document
  evidence does not associate, resources keep version 2 evidence and no
  protocol is recorded.
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

### Evidence And Benchmarks

- Benchmark artifact: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts)
- Benchmark runbook: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook)
- Opt-in harness:

```bash
APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser' -benchmem -count 5
```

Update density guidance only when repeated controlled runs justify it.

### Detailed Planning References

The following files are retained for implementation history and tactical detail:

- [ROADMAP.md](ROADMAP.md#browser-and-mobile)


## Broker and messaging support

Status: Current

APEX Load supports messaging workloads through compiled protocol packages and
build-tag-gated real adapters. The default build keeps broker execution safe for
local validation by using in-memory mocks or fail-fast availability gates.

### Current Support Summary

| Platform | Import | Runtime | Notes |
| --- | --- | --- | --- |
| JMS family | Partial | Mock/default; Solace and Azure Service Bus real adapters are build-tag gated | IBM MQ is mock-only; non-mock scripts fail validation and initialization. Generic acknowledgement/transaction actions are mock-only. |
| Apache Kafka | Partial | Mock/default; real franz-go adapter behind `-tags kafka_real` | Includes produce/consume/request-reply, real seek for owned polled partitions, consumer-offset transactions and Event Hubs Kafka-endpoint profile guidance. Full consume-transform-produce transaction and failover certification remains open. |
| Azure Event Hubs | Partial | Mock/default; native AMQP adapter behind `-tags eventhubs_real` | Producer/observer, AAD processor ownership/checkpoints with resource admission and bounded progress evidence, plus fixed-partition exclusive consumers. Strict processor pause confirms actual receive/tail/checkpoint quiescence; recovery and live Azure qualification remain open. |
| AsyncAPI import | Partial | Generates Kafka/Event Hubs or review drafts depending on bindings and adapter availability | Unsupported bindings remain review-only rather than pretending to execute. |

Real broker adapters are intentionally opt-in so standard binaries remain
deterministic and fail closed when live broker dependencies are unavailable.

### Build Tags

| Tag | Enables |
| --- | --- |
| `kafka_real` | Real Kafka / Event Hubs Kafka-endpoint transport |
| `jms_solace_real` | Real Solace JMS-over-AMQP transport |
| `jms_servicebus_real` | Real Azure Service Bus adapter |
| `eventhubs_real` | Native Azure Event Hubs AMQP adapter |

### Results And Telemetry

Messaging results report protocol-neutral throughput, message latency, consumer
lag where supported, payload sizing, and reliability counters when adapters
populate them. Deeper broker-native parity remains uneven by provider, especially
for offset/queue-depth semantics, DLQ/retry details, transactions, and
provider-specific management-plane probes.

The optional messaging management evidence contract is separate from load action results. It preserves exact decimal positions/populations, unavailable inventory, source identity and collection ownership through run artifacts and a dedicated archive sidecar. The foundation registered no provider; the subsequent Kafka slice supplies the executable `kafka-offsets` reader with explicit TLS trust, numeric routes and pinned SASL identities. Capability responses distinguish contract availability from the actual registered pack list; the executable Service Bus `servicebus-runtime` reader uses fixed approved HTTPS entity descriptions, and the `solace-semp` reader polls exact individual queue/topic-endpoint depths under pinned Basic identity and durable provider-session quarantine. Its compiled reader and controlled local tests are separate from pending live broker and restarted-source qualification; see the [Solace verification record](qa/reviews/feature-completion-solace-management-2026-10-04/README.md). Service Bus collection, SQLite/API, full Go, targeted races and full dashboard checks pass in both auth profiles; PostgreSQL and live Azure qualification remain pending in [its verification record](qa/reviews/feature-completion-servicebus-management-2026-10-04/README.md). The Apache Kafka 3.9.2 single-broker authentication/offset and open-transaction visibility fixture passed, as did the final browser workflows in both auth profiles. The full Go repository gate also passed; broader provider qualification remains pending in its verification record. See [exact management evidence](ANALYSIS_WORKFLOWS.md#exact-messaging-management-evidence).

Standalone Event Hubs processor progress now retains ownership generations, inventory, delivered/rejected/redelivered counts, checkpoint outcomes/timing/retry provenance and separate delivered/checkpoint/tail positions across live, history, archives, reports and bounded API/CLI reads. Sequences/backlog are exact decimal strings; provider offsets are opaque. Cumulative rows merge by latest revision, including idle/final evidence, instead of adding repeated snapshot populations.

Tail queries are disabled by default. Explicit `observe_partition_tail` adds bounded management traffic after Start at 10 seconds to 5 minutes (default 10 seconds), with provider and wire capability admission. Delivered/checkpoint lag is nullable and qualifies only complete same-generation observations within 30 source-monotonic seconds of per-row capture; transit and cross-host clocks remain unqualified. Missing/disabled/stale/fenced/unconfirmed evidence is unavailable, never zero. This does not change legacy transaction lag gates or claim live Azure acceptance. See the [Event Hubs guide](EVENT_HUBS_HOWTO.md#processor-progress-and-checkpoint-evidence) and [analysis workflow](ANALYSIS_WORKFLOWS.md#event-hubs-processor-progress-analysis).

### Current Guidance

- Use mocks for local authoring and CI smoke validation.
- Use tagged controller/generator builds for live broker execution.
- Do not claim IBM MQ live execution until a real adapter is added.
- Keep unsupported AsyncAPI/JMS/MQ/MQTT bindings as review-only drafts with
  explicit notes.

### Detailed Planning References

The following files are retained for deeper implementation history and parity
work:

- [ROADMAP.md](ROADMAP.md#messaging)
- [Web/API execution matrix](PROTOCOL_SUPPORT.md#webapi-execution-support-matrix)

### JMS sidecar

Source note: consolidated 2026-09-24 from the former `JMS_SIDECAR.md`.

The `jms-solace` protocol can delegate messaging to an external, operator-owned
sidecar process. Scripts may *select* the sidecar adapter
(`jms_properties.adapter: "sidecar"`), but they can never supply the command —
a script is user-controlled JSON, so honouring a script-supplied command would
be arbitrary code execution on a generator.

#### Operator environment

| Variable | Required | Meaning |
| --- | --- | --- |
| `APEXLOAD_JMS_SIDECAR_COMMAND` | yes | Command line for the sidecar. Split on whitespace; no shell is involved. |
| `APEXLOAD_JMS_SIDECAR_MAX_PROCESSES` | no | Concurrent sidecar process ceiling (default 25, hard cap 1024). |
| `APEXLOAD_JMS_SIDECAR_PERSISTENT` | no | `1`/`true`/`yes`/`on` enables the pooled long-lived transport described below. Anything else keeps the default. |

Sidecar processes always run with a scrubbed environment (see
`pkg/internal/subprocessguard`), and the adapter refuses to start one at all if
ambient platform credentials are visible in the parent environment.

#### Default transport: one process per action

The adapter forks the command, writes a single JSON request object on stdin,
reads a single JSON response object from stdout, and lets the process exit.
stdout and stderr are each capped at 256 KiB.

This is simple and isolated, but it forks once per messaging action, which does
not hold up at load-test VUser counts.

#### Pooled transport (opt-in)

With `APEXLOAD_JMS_SIDECAR_PERSISTENT=1` the adapter keeps sidecar processes
alive and reuses them across actions. Enable it only if your sidecar binary
implements the contract below.

**Wire contract** — newline-delimited JSON, one request object per line:

- Read request lines from stdin until EOF, then exit. Exiting on stdin EOF is
  what makes the sidecar orphan-safe: if the generator dies, the OS closes the
  pipe and the sidecar terminates.
- Every request carries a non-empty `"id"`. Echo that exact `"id"` on the
  matching response line. A missing or mismatched `"id"` is treated as a
  desynchronised stream: the process is killed and dropped from the pool.
- Write exactly one response line per request line, in order.
- A response carrying `"error"` is an application-level rejection. The process
  is considered healthy and stays pooled.

**Pool behaviour**

- One pool per (command, broker configuration) pair, so a pooled process only
  ever sees one configuration.
- `APEXLOAD_JMS_SIDECAR_MAX_PROCESSES` bounds in-flight requests *and* sidecar
  processes in service. The two are enforced separately: a finished request
  releases its concurrency slot while its process stays alive parked in the
  pool, so before forking a replacement the adapter *claims* a slot in the
  process budget, retiring parked processes from any pool until one frees up.
  The claim is atomic, so concurrent actions on different pools cannot all fork
  against the same free slot. The ceiling is global, not per pool, so a test
  whose groups use different broker configurations still fits in the process
  budget you sized for the generator.
- Two bounded exceptions to that ceiling, both deliberate. A retired sidecar
  gives its slot back when the adapter decides to end it, not when the child has
  finished exiting: a sidecar that ignores the "exit on stdin EOF" contract can
  therefore linger for up to its 2-second kill grace beyond the ceiling. Waiting
  for it instead would stall a VUser action and corrupt the response time the
  run is measuring. And if every live sidecar is busy serving a request, the
  fork proceeds anyway — those processes are already bounded by the same
  variable acting as the in-flight limit, and blocking would deadlock the
  action. Size generator memory with a little headroom above the ceiling
  accordingly.
- A process is checked out exclusively for one request, so requests never
  interleave on a single pipe.
- Idle processes are reaped after 60 seconds.
- Any transport-level fault (decode failure, id mismatch, oversized output,
  EOF, timeout) kills and discards the process rather than reusing a pipe of
  unknown state.
- The 256 KiB stdout/stderr caps are charged per request, not per process
  lifetime, so ordinary per-action logging never accumulates its way into a
  failed action. Because the OS delivers a sidecar's stderr asynchronously, a
  request that floods stderr just past the cap is occasionally reported against
  the following request instead of its own; it is never silently dropped.
- The `teardown` operation retires the process it ran on instead of returning
  it to the pool, matching the one-shot behaviour where teardown always ended a
  process.

**Known limitation**: there is no run-scoped shutdown hook wired into the
engine yet, so pooled processes are reclaimed by the idle reaper (or by
generator exit) rather than at the exact end of a run.

### JMeter messaging fidelity

Source note: consolidated 2026-09-24 from the former `JMETER_MESSAGING_FIDELITY.md` (Kafka / Event Hubs import preservation).

**Current status: native draft preservation is implemented; strict replay is blocked. The complete runtime/repair plan is not implemented.**

The importer recognizes DI-KafkaMeter producer/configuration classes and PNOP Event Hubs samplers with string messages. A plan containing the three producers in the synthetic fixture becomes three native drafts and one concurrent scenario. The disabled CSV and disabled producers remain disabled.

The fixture preserves 100/50/50 threads, one-second thread ramps, 300-second linear ramps from 1 to 84/42/42 samples per second, and 3,600-second plateaus. The scenario lasts 3,900 seconds. Integrating the schedules gives 630,450 planned samples, assuming one message per sampler and sufficient available threads. This is not a guarantee of successful sends and does not rewrite the workload to 600,000.

#### Implemented

- Source identities use the original plan digest plus XML byte offsets. Samplers on the same line remain distinct. Scenario groups bind by source identity rather than filtered array position.
- Kafka configuration resolves by source scope and producer variable. `extraConfigs` override base settings; both views are retained. String payload bytes, explicit partitions, and authored empty keys are retained. Native Kafka distinguishes empty from null keys. In the pinned DI compatibility mode an authored empty string becomes a null wire key, matching the plugin; whitespace-only keys remain nonempty. Import provenance is excluded from Kafka wire headers.
- Event Hubs message lists remain one sampler draft. Multiple messages are retained together and explicitly blocked pending batch runtime support. HTTP Header Manager entries are marked ineffective and never converted into messaging properties automatically.
- Counter/random declarations retain source scope, iteration update, bounds, wrap, and seed. A session-owned shared sequence pool and Java 48-bit generator are implemented behind the execution gate. Tests cover concurrent wrapping, per-user/plan/run isolation and 600 JDK 17 oracle values; full lifecycle iteration timing remains unverified.
- Sampler schedules retain segment rates, duration, scope identities, and action references. They are distinct from thread ramps and iteration pacing. They are persisted with scripts; the saved scenario retains source-engine affinity and schedule duration. Schedule/identity mismatches are rejected on import save.
- Bounded Java date formatting handles single-`S` minimum-width milliseconds, quoted literals, UTC offsets and explicit timezones. Unsupported patterns are rejected. The supplied two-evaluation timestamp remains two evaluations unless a supported explicit repair changes it.
- Accounting separates enabled/disabled workload samplers, converted/blocked workloads, configuration contributors, and reporting elements. Unknown sampler variants retain blocked drafts instead of disappearing with their groups.
- JAAS parsing accepts a bounded set of options and never loads uploaded Java classes. Nonstandard option names, malformed/unknown options, unsupported serializers/callbacks/configuration, unresolved variables and missing credentials retain source-linked blockers.
- East/west Event Hubs bindings are distinct. Messaging source redaction checks decoded XML attributes as well as text, including credentials duplicated in message element names.
- Preview shows three workload curves, planned samples, affinity, preserved Kafka overrides and explicit converted-versus-runnable status. Warnings include static data, unused counters, literal-Z assumptions, partition availability, View Results Tree and missing downstream verification.
- The pinned reference installation starts and loads the synthetic JMX. See [reference evidence](../scripts/README.md#jmeter-messaging-reference-baseline). This is startup/formatter and deterministic parameter evidence only.

#### Execution boundary

`config.jmeter.profile = "jmeter-messaging-v1"`, `config.sampler_schedules`, parameter scope/algorithm fields and `action.pacing_ids` are versioned preservation contracts. All imported actions carry durable execution blockers. Validation permits the structurally valid blocked drafts to be saved; removing their blockers fails validation. Engine admission independently rejects the pending compatibility contract even if metadata is stripped. Single-generator affinity is persisted and admission rejects it until the allocator/runtime supports it; it cannot silently fall back to distributed placement or restart after loss.

Existing scripts with none of these compatibility settings retain their existing defaults. Empty-versus-null Kafka key preservation is a targeted protocol correction.

#### Remaining implementation

1. Complete end-to-end iteration-update conformance for the implemented shared parameter pool; full virtual-clock throughput gating, scope sharing, schedule completion and pause behavior. The pinned timer uses per-second admission accounting and inclusive segment endpoints, so a generic token bucket is not an equivalent replacement.
2. Kafka shared producer lifecycle and PNOP per-sample producer lifecycle, sampled latency versus worker occupancy, batch sends, and failure cleanup traces.
3. Typed effective Kafka producer tuning, admission/buffering versus delivery timeouts, TLS bounds, generic explicit client-credentials OAuth and complete credential/disclosure integration. Effective-settings metadata does not imply runtime support.
4. Single-generator allocation, capacity/capability negotiation and older-generator rejection, without restart or counter replay after loss.
5. Atomic protected multi-script/scenario repair preview/apply: OAuth option normalization, credential binding, UTC timestamp replacement, trace-property repair and selected payload parameterization. The `messaging_repaired.jmx` fixture currently changes only OAuth option names; it deliberately retains masked credentials and other source behavior.
6. Reference lifecycle/rate/OAuth conformance, authorized live Kafka/Event Hubs validation, and full browser repair/secret-binding workflows. Strict parity stays unverified until these pass.

Do not clear compatibility blockers merely because credentials are bound or a warning was acknowledged. Do not enable the disabled CSV, add business assertions, transmit the ineffective Header Manager, or change message volume without explicit review.

#### Validation evidence for this implementation

- Conversion of the supplied file: 3/3 enabled workload samplers mapped to Kafka/Event Hubs/Event Hubs; all three remain blocked drafts. Threads 100/50/50, one-second ramps, 3,900-second duration and single-generator affinity are preserved.
- Focused converter, import-contract, DSL, Kafka, Event Hubs and engine suites pass. API tests cover attested save/reload, blocked launch, source/schedule tampering and affinity/loss rejection.
- Shared parameter tests pass under the race detector, including 100 concurrent users, exact counter wrap counts, Java oracle vectors and run/plan isolation. Real-adapter build-tag suites pass; these are not live-service conformance results.
- The messaging browser workflow passes in both dev-admin and local-auth profiles, including workload display, effective settings, scenario persistence and blocked launch. A fresh dashboard build and visual inspection of the three-curve preview passed.
- The full dashboard cycle did not pass: review/credential tests hit standard timeouts. Their 106-test diagnostic rerun passed with a 30-second timeout. A separate local-auth HTTP JMeter repair workflow also hit a click timeout. These diagnostic passes do not replace the required normal full cycle.
- Full repository Go validation has not yet produced a clean result. The initial run timed out in the large API suite; Git-dependent QA checks in the isolated validation copy were subsequently corrected and passed. Do not report the entire repository or the full plan as verified from focused checks alone.

#### Two-pass code review follow-up

Both passes included regression tests for their findings. Fixes:

- Reject retained sampler pacing and per-user source counter identities even after the profile/metadata is removed, including direct engine callers.
- Bound standalone compatibility validation and include parallel branches in structural validation and blocker traversal.
- Require complete source identities and schedules on messaging scenario save; reject duplicate or omitted source groups.
- Redact credential-valued Kafka extra properties from source review. Omit source review when unsupported JAAS escaping cannot be decoded safely.
- Normalize numeric Kafka acknowledgement aliases while retaining their authored values in effective-settings evidence.
- Detect structured Event Hubs message options instead of silently ignoring nonempty collection properties.
- Preserve each sampler's native configuration, retain the first script configuration, and explicitly block unsupported differing producer configurations. Credential requirement identities use source-node identities rather than display names.
- Reject unsupported multi-JMX messaging scenario assembly instead of inheriting the first document's timing and affinity.
- Use the longest messaging schedule for scenario duration; do not invent a five-minute duration when no source schedule exists.
- Keep shared parameter generation bound to its session definition and reject unnamed sessions.
- Require one complete XML document and bound depth/node count before recursive conversion and source review.

These fixes do not enable the unfinished compatibility runtime or establish live parity.

Review validation: focused converter/import-contract, DSL, engine and API regressions pass, as do shared-state race tests and Kafka/Event Hubs build-tag tests. The full dashboard cycle passed type checking and lint, then reported 4,464 passing tests and one standard-timeout failure in `ScriptImportReview`; all 40 tests in that file passed on a normal-timeout rerun. A fresh dashboard build passed. The frozen full Go run stopped during linking with `no space left on device`, so repository-wide Go validation remains incomplete. These results do not establish live-service parity.

The fresh full browser run completed: dev-admin 136 passed / 4 skipped / 1 failed; local-auth 56 passed / 1 skipped / 1 failed. Both failures are the LoadRunner review scroll-position assertion at `dashboard/e2e/workflow-loadrunner-review.spec.ts:789` (outside these messaging changes). All three JMeter workflows passed in both profiles, including messaging save/reopen and blocked launch. The messaging screenshot was visually inspected. Full browser logs: `/tmp/apex-messaging-implementation/review-browser.log`; full Go environmental failure: `/tmp/apex-messaging-implementation/review-go-frozen.log`.

#### Throughput calculation continuation

Implemented a session-owned, concurrency-safe admission calculation state using the pinned TST 2.6 per-second behavior. Definitions are copied, conflicting shared identities are rejected, separate source plans/runs cannot share permits, and unused capacity does not carry into later seconds. Java rate and delay oracle vectors match exactly (7,804 rate values and 448 delay values); full-duration virtual-clock tests cover both workload curves. The reference final endpoint is inclusive: schedule exhaustion is first observed at second 3,901. The nominal authored duration remains 3,900 seconds.

This is calculation conformance only. A component wait loop now applies multiple timers in source order, notifies registered waiters, tracks contention, and cleans up on cancellation; these behaviors have focused concurrent tests. The state was subsequently wired to pre-sampler execution and the engine's active pause clock (see the continuation below). Reference stop escalation and end-to-end timing still require verification. No compatibility blocker was removed.

Continuation validation: `go test -p 1 -timeout 30m ./...` passed, the full engine suite passed after the final component changes, and `go test -race ./pkg/engine -run TestSamplerSchedule -count=1` passed. Oracle regeneration and altered-classpath rejection passed. These results supersede the earlier incomplete full Go attempts for this checkout, but do not establish live messaging parity. No dashboard behavior or public DSL contract changed in this continuation.


#### Runtime continuation — not certified for strict replay

Implemented component contracts and behavior:

- Pre-sampler schedule admission before payload interpolation, shared within a multi-group source runtime. Group completion stops new actions while draining in-flight sends. Pause freezes the active schedule clock.
- Explicit producer lifecycle and sample-timing settings. Kafka producers are borrowed from a run-owned session and flushed/closed once; iteration resets and parallel branches retain that ownership. Event Hubs creates/sends/closes per sample, supports one sampled message list, and reports worker occupancy separately from sampled latency. Cleanup errors and panics have regression coverage.
- Kafka OAuth client credentials through the existing bounded OAuth implementation, with standard POST/Basic and the pinned Kafka 3.3.1 Basic wire format. Secret references participate in resolution, redaction, rebinding, protected import validation, and outbound token-endpoint discovery. Missing references never become literal credentials.
- Typed retries, linger, buffer, admission, delivery/request timeouts, and TLS bounds. Metadata/buffer admission is separate from acknowledgement waiting. Raw invalid source settings remain in a blocked draft instead of making the draft unsavable.
- All-or-nothing single-generator placement and a versioned distributed capability requirement. The requirement is intentionally **not advertised** as available. Generator-loss recovery refuses to reset/replay source state.
- Worker occupancy is carried in gRPC transaction metrics and JSON/Parquet archives.

A review found that Java Kafka `batch.size` is an allocation target while franz-go's batch maximum rejects oversized singleton records. The importer no longer translates these as equivalent; it preserves the source setting and adds `jmeter_kafka_batch_allocation_parity_unverified`.

Remaining delivery work:

1. Distributed assignments currently create independent engines per group. They need one shared source-session lifetime, cancellation boundary and coordinated start before affinity can enable strict execution.
2. Complete producer lifecycle/timing reference traces, Java batch/buffer allocation behavior, zero-admission-timeout semantics, and timer stop escalation. Local component tests do not certify these behaviors.
3. Extend protected repair preview/apply to the atomic three-script/scenario candidate, including context confirmation and the requested source corrections, with browser workflows.
4. Complete compatibility capability reporting, pause comparison marking, and fixed-population controls.
5. Finish repository/race/build-tag/browser validation on the final tree and add controlled-service conformance evidence.

The operator has no authorized Kafka/OAuth or Event Hubs endpoints available. Continue local implementation and reference tests; live-service conformance remains **unverified**, and no mock result clears that status.


Additional pinned evidence: the actual Kafka 3.3.1 callback matches seven HTTP wire-format vectors, including Java whitespace and form-encoding behavior. The actual DI sampler record builder matches four key/partition vectors without opening a broker connection. It converts an empty string to a null Kafka key and retains whitespace-only keys. The source's authored empty value remains visible in the draft.

Latest browser validation: 4,472 unit tests passed. Dev-admin completed with 136 passed, four skipped and one failure in the general scenario-builder chart/legend placement assertion (three-pixel overlap); local-auth completed with 57 passed and one skipped. All three JMeter workflows passed in both profiles. The messaging preview screenshot was visually inspected. The full dashboard cycle is therefore not a clean pass.

Latest stable validation: the complete `go test -p 1 -timeout 30m ./...` run passed on the final Go tree. Full race suites passed for HTTP OAuth, Kafka, Event Hubs and engine, and both real-adapter build-tag suites passed. These supersede the earlier interrupted Go runs. The dashboard cycle result above remains unchanged: one general scenario chart/legend overlap failure, with all JMeter workflows passing.

Offline PNOP lifecycle evidence now executes the actual pinned sampler while substituting SDK transport methods and forbidding network access. Five cases cover repeated successful three-message samples, creation failure, send failure and close failure. They confirm per-sample producer ownership, success timing before close and zero completed sample time on creation/send failure. They also expose an outstanding difference: PNOP lets a close exception escape; APEX records a failed protocol result. Strict replay stays blocked. See `scripts/jmeter-reference/eventhubs-lifecycle-evidence.json` and its accompanying vectors; deterministic regeneration and changed-classpath rejection passed. This evidence does not certify actual Azure SDK or live-service behavior.

#### Repeated code review of runtime continuation

Two corrective passes and a subsequent review covered the uncommitted importer, runtime, credential, affinity, metric and reference-harness changes. Regression tests now cover:

- Independent Kafka source configurations cannot inherit OAuth/TLS fields from the preceding producer. Distinct configuration identities remain distinct even with identical effective settings; unsupported multiple producer lifetimes stay blocked.
- Positive metadata/buffer admission deadlines cannot be bypassed by a late metadata response. Zero-wait cached-metadata admission remains supported.
- Parallel branches inherit the parent iteration's shared parameter values without consuming new sequence values. Pending sampler gates observe group cancellation even outside the ordinary VUser run wrapper.
- Event Hubs restores the VUser client after a cleanup panic and rejects a nil sample factory result.
- Native Kafka batch settings cannot override a stricter protocol size cap.
- Shared Kafka owner cleanup preserves cancellation classification without exposing adapter error text. Already-cancelled OAuth callers cannot obtain cached tokens.

The subsequent code review found no additional actionable defects in these changes. This does not remove the separately documented implementation gaps or certify strict replay.

A further edge-case pass reproduced an unsaveable blocked draft when an Event Hubs sampler contained no messages. The importer now retains that sampler, its zero-message source evidence and its specific execution blocker without emitting an invalid native message-list parameter. The regression fails before the fix and passes after it; a subsequent review found no further actionable issues. Final validation was restarted after this correction.

Final review validation: the complete Go suite passed on the final production tree. Full engine/HTTP OAuth/Kafka/Event Hubs race suites and both messaging real-adapter tag suites passed; cancellation/shared-parameter regressions also passed 20 repeated race runs. All four pinned offline oracle vector/evidence sets reproduced exactly. The full dashboard cycle passed type checking, lint, 4,472 unit tests, the fresh embedded build, and dev-admin workflows (137 passed, four skipped). Local-auth reported 56 passed, one skipped, and one Author editor validation-message click timeout at `workflow-author-experience.spec.ts:51`, outside the messaging changes. That exact workflow passed three consecutive reruns at the normal timeout. This diagnostic pass does not make the full dashboard cycle a clean pass. No frontend behavior was modified during this review, and strict/live conformance remains unverified.

##### Distributed source ownership and atomic repair batches

The source-plan manifest and single-engine commit path now group all source
threads on one generator. Protected messaging previews can apply a complete
scripts-plus-scenario repair revision atomically. See
[the source-plan and repair contract](IMPORT_FIDELITY_AND_REPAIRS.md#jmeter-source-plan-execution-and-reviewed-batch-repairs) for fencing,
retry, storage and operator behavior. These additions do not remove the strict
compatibility gate or resolve the remaining protocol/live-conformance gaps.


## Java Vuser execution

APEX Load executes reviewed `java_vuser` scripts through an operator-enabled Java runner. The feature is disabled by default. Importing a script, or setting fields under `config.java_vuser`, cannot enable process execution or replace the operator's runner, harness, work directory, shared classpath, or process limit.

### Java selection

`jdk_selection` defaults to `internal`.

- Local internal resolution checks an explicitly configured direct `java`/`java.exe` launcher, `APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME`, `JAVA_HOME`, `JRE_HOME`, and then `PATH`, in that order.
- Sidecar internal resolution uses the image's bundled JDK 21 unless the sidecar operator config supplies a default home.
- `specified` derives only `<jdk_home>/bin/java` or `java.exe` on the executor. The home and launcher are canonicalized, the launcher must be a regular executable, and a bounded `-version` probe must report Java 17 or newer. There is no fallback and no controller-side rewriting of platform-specific imported paths.

A specified home is a trusted raw executor path for reviewed Java code; there is no script JDK allowlist. Use a signed custom runner image or a read-only operator mount when additional JDKs are required.

#### Legacy Java opt-in

Some imported Vusers depend on middleware that only runs on Java 8. `APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA=true` lowers the probe floor from Java 17 to Java 8 for both `internal` and `specified` resolution. It is read from the generator/controller process environment and can never be set from script DSL, so lowering the floor stays an operator decision about a specific deployment.

The floor is lowered, not removed: anything older than Java 8 is still rejected, and the rejection message names whichever floor applied. Because the version probe is the only content check between a script-supplied `jdk_home` and a forked JVM, enable this only where the executor's JDK paths are already controlled.

The opt-in alone is not sufficient. The shipped harness is compiled `javac --release 17` (class file major 61) and cannot load on a Java 8 JVM at all. Build the Java 8-targeted harness and point the runner at it:

```
make java-vuser-runner-legacy
export APEXLOAD_JAVA_VUSER_HARNESS_JAR=bin/java_vuser/apexload-java-vuser-runner-java8.jar
export APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA=true
```

JDK resolution is cached for about 30 seconds, so a flag change takes effect on the next resolution rather than immediately.

### VM parameters and classpath

`additional_vm_parameters` is an array of exact JVM argv tokens. The dashboard represents it as one token per line. A token containing a space remains one token; APEX Load never invokes a shell and never expands command substitution, variables, or argument files.

Runtime validation limits count and size and rejects launcher takeover, classpath/module overrides, raw Xboot options, Java/native agents, custom system class loaders, terminal/dry-run options, command-triggering error handlers, reserved `-Dapexload.*` properties, template expressions, and properties whose names indicate credentials such as passwords, tokens, or secrets. JVM arguments are visible in operating-system process metadata, so secret references are deliberately left unresolved and rejected instead of being copied into the Java command line. Pass sensitive values through governed LR parameters/action data rather than `additional_vm_parameters`. Diagnostics identify only the token index and category, not its value.

Script classpath entries identify precompiled classes or JARs already present on the selected executor. Script entries precede operator shared entries. APEX Load never compiles imported source. Every assigned executor verifies the referenced paths before assignment commit.

#### Attaching classpath JARs

An entry whose dependency is not pre-provisioned can instead have its JAR attached during import. Attached JARs are uploaded to the controller, keyed by content hash, and materialized on the runner's work directory at launch. Attaching or swapping a JAR changes what code runs, so it requires re-approval before the next launch.

The feature is fail-closed and needs both sides enabled:

- The dashboard shows the resolver panel by default; it can be turned off per browser via the `apexload.featureFlags.javaClasspathUpload` local-storage flag, or at build time via `VITE_APEXLOAD_JAVA_CLASSPATH_UPLOAD=false`.
- The controller must set `APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED=true`. While it is unset the upload endpoint answers 404, attached assets are ignored at runtime, and behavior is identical to the operator-provisioned model above.
- A local (same-host) sidecar additionally needs `APEXLOAD_JAVA_VUSER_CLASSPATH_ASSET_DIR` pointing at the controller's import asset root.

Attached JARs are supported on a local runner only. A distributed run that carries them is blocked at launch (`java_vuser_classpath_asset_distributed_unsupported`); provision those dependencies on the executors instead.

### Xboot and dedicated loading

`use_xbootclasspath` preserves the imported LoadRunner choice but does not pass an Xboot option to modern Java. Execution uses the ordinary classpath and records the stable modern-Xboot fallback warning/evidence.

With `dedicated_class_loader`, the system classpath contains only the harness. The harness creates a child `URLClassLoader` for the effective script-plus-shared classpath, uses it for class loading and the complete requested lifecycle, and sets and restores the thread context class loader (including the startup worker). Local and sidecar execution create one JVM, `Actions` instance, and loader for each VUser session, retain them across `init`, repeated `action` calls, and `end`, and close them during teardown. Java fields, static state inside that isolated loader, and `lr.save_*` parameters therefore survive between lifecycle calls for the same VUser. This is class-loading isolation inside the runner; it is not a security sandbox.

### Backends and preflight

`APEXLOAD_JAVA_VUSER_RUNNER_MODE` selects `local` (the backward-compatible default) or `sidecar`.

- Local mode rejects ambient credentials before any Java probe or fork, then starts the child with a small allowlisted environment. A non-Java legacy wrapper remains compatible only for scripts that use none of the new JVM settings.
- Sidecar mode uses the versioned `apexload-java-vuser-runner.v1` loopback HTTP contract with persistent session operations in addition to health, capability, validation, legacy one-shot execution, and cancellation endpoints. The generator container never forks Java. The sidecar owns one JVM session for each assigned Java VUser, accepts only script runtime settings and action data, owns operator configuration and process capacity, limits request/output sizes, and kills and reaps canceled, timed-out, or unusable JVMs.

Local runs preflight before starting VUsers. Distributed runs preflight during two-phase assignment staging, after secret resolution and before commit. Preflight verifies the backend contract, the `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1` capabilities, Java version, harness, VM tokens, effective classpath, unique target classes/methods, and available Java VUser capacity. The frame-token contract bootstraps a random session secret before the `Actions` class is loaded and derives each result token from that secret and the request ID; later request frames expose neither the secret nor the expected token. A failure aborts the assignment atomically; warnings use stable value-free codes. The configured process limit (25 by default) is the generator's maximum number of simultaneously persistent Java VUsers. A timed-out, canceled, or crashed session is reaped, marked unusable, and never silently restarted with lost state.

The same-pod sidecar is credential-minimized, not a hostile-code sandbox. Its deployment receives no generator gRPC credentials, service-account token, workload identity, or sensitive generator mounts, but it still shares the pod network and egress. Mutually untrusted scripts require a separate authenticated worker pool outside this contract.

### Persistent process containment

Combined Java imports use separate `init`, `action`, and `end` calls against one
long-lived JVM. That persistent lifecycle is advertised only when the runner can
atomically contain the JVM and every native descendant before user code starts:

- Windows uses a kill-on-close Job Object and supports the persistent lifecycle.
- Linux requires a writable, delegated cgroup v2 subtree, `cgroup.kill`, and an
  allowed `clone3` cgroup-fd launch. A typical host-native systemd service must
  be deliberately configured with cgroup delegation (for example,
  `Delegate=yes`) and run in the delegated subtree. Availability is proven by a
  real start-and-kill probe; the runner fails closed if delegation, the kernel
  primitive, permissions, or the syscall policy is missing.
- Darwin and the other supported Unix build targets have no equivalent
  ownership primitive in this implementation. Polling process ancestry is used
  only for backward-compatible one-shot cleanup and is never advertised as
  persistent containment.

The stock non-privileged Helm, Kustomize, and Compose sidecars do **not** receive
a writable cgroup v2 delegation. They therefore advertise legacy
`full_lifecycle` execution only; a combined phase-lifecycle script is rejected
during sidecar capability preflight with `persistent_lifecycle_unavailable`
before a JVM session starts. A local Linux runner without the required
delegation fails its corresponding strong-containment gate with
`process_containment_unavailable`. The release container smoke test verifies
that honest capability boundary. Do not work around it with a privileged
container or a writable mount of the host cgroup hierarchy. Run persistent Java
VUsers on Windows or on a separately governed Linux runner with a narrowly
delegated subtree. Kubernetes installations need an operator-provided
containment integration; this repository intentionally does not ship a
privileged cgroup-mount manifest.

### Operator configuration

The relevant environment variables are:

- `APEXLOAD_JAVA_VUSER_RUNNER_ENABLED`
- `APEXLOAD_JAVA_VUSER_RUNNER_MODE`
- `APEXLOAD_JAVA_VUSER_RUNNER_URL`
- `APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN`
- `APEXLOAD_JAVA_VUSER_RUNNER_COMMAND` (local/direct Java or legacy-wrapper compatibility)
- `APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME`
- `APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA` (lowers the Java floor from 17 to 8; requires the Java 8-targeted harness jar)
- `APEXLOAD_JAVA_VUSER_HARNESS_JAR`
- `APEXLOAD_JAVA_VUSER_CLASSPATH`
- `APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED` (honors user-uploaded classpath JARs; fail-closed when unset)
- `APEXLOAD_JAVA_VUSER_CLASSPATH_ASSET_DIR` (same-host import asset root a local sidecar reads uploaded JAR bytes from)
- `APEXLOAD_JAVA_VUSER_WORK_DIR`
- `APEXLOAD_JAVA_VUSER_MAX_PROCESSES`

Helm, Kustomize, and Compose integrations are opt-in. The standard Java runner image contains JDK 21 and a Java 17-targeted harness. Operators can mount a read-only artifact volume or publish a signed custom runner image with approved precompiled classes/JARs.
The packaged sidecar sets `APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp`; its root filesystem is read-only and `/tmp` is the bounded writable volume used by child JVMs.
Helm and Kustomize derive a per-pod sidecar authentication token from the pod UID and inject the same value into the generator and runner containers. Compose has no safe generated default: operators must provide one explicit token, which Compose passes unchanged to both services.

For the local Compose stack, service selection and Java-execution enablement
remain two explicit controls. Start both together with:

```bash
APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true \
APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN='<operator-generated-random-token>' \
docker compose --profile java-vuser up
```

The profile adds the runner in the generator's network namespace so its HTTP
endpoint remains loopback-only. Omitting the environment opt-in keeps the
generator-side Java runtime disabled even if the sidecar service is started.
Omitting the authentication token keeps sidecar readiness false and protected
runner requests unavailable. This stock Compose profile is legacy
`full_lifecycle` only: it does not delegate a writable cgroup v2 subtree, so a
combined persistent Java import fails capability preflight with
`persistent_lifecycle_unavailable`.


## SAP GUI Runner Bridge Spec

### Status

This is a runner specification, not an implemented runtime. SAP GUI imports can produce a `sap_gui` DSL draft, but launch remains blocked with `external_runner_required` until a Windows runner bridge is deployed and entitled.

### Transport

SAP GUI execution uses the existing controller-to-generator gRPC control plane with runner registration extensions:

- `runner_class`: `windows_sap_gui_runner`
- `protocols`: `sap_gui`
- `os`: `windows`
- `sap_gui_client_version`
- `sap_gui_scripting_enabled`
- `desktop_session_capacity`

The controller schedules only `sap_gui` groups to generators advertising this runner class and a positive desktop session capacity.

### Execution Model

The runner implements the standard protocol lifecycle on Windows and drives SAP GUI through SAP GUI Scripting COM:

- `FindById` resolves DSL `target` control IDs such as `wnd[0]/usr/txtRSYST-BNAME`.
- `.Text`, `.Key`, `.Selected`, `.Press()`, and table/grid/tree methods perform typed DSL actions.
- `Session.Busy`, status bar changes, screen changes, and bounded polling implement `sapgui_sync`.
- Status bar text and control values feed assertions and protocol evidence.

Each VUser owns an isolated desktop session or an explicitly governed session slot. The runner must not multiplex unrelated tests into the same SAP GUI session.

### Capacity

Linux cgroup pod metrics do not describe Windows desktop session capacity. The Windows runner reports `desktop_session_capacity`, current active sessions, and SAP GUI client readiness. The default planning envelope is 25 VUsers per runner until customer-calibrated evidence supersedes it.

### Placement And Governance

Launch requires:

- `sap_gui_foundation` entitlement.
- `windows_sap_gui_runner` placement class.
- SAP GUI client installed and version reported.
- SAP GUI Scripting enabled on the client and server policy.
- Secret provisioning for `config.sapgui.password`.

Without these signals, preflight returns an `external_runner_required` conflict and direct launch is blocked.

### Evidence To Graduate

The SAP GUI profile can move from external-runner-required to executable only after:

- Representative import fixtures validate the typed DSL mapping.
- Runner smoke tests cover logon, set, press, grid/table/tree, sync, verify, and error handling.
- Capacity calibration publishes desktop session density evidence.
- Governance verifies entitlement, placement, and secret isolation.
