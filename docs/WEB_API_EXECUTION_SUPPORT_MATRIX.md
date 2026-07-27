# Web/API Execution Support Matrix

This document is the W1 evidence record for modern Web/API execution parity.

## API Surface

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/scripts/web-api/support-matrix` | Code-owned Web/API support, benchmark envelope, diagnostics, and sample-project matrix. |
| `GET /api/v1/runtime-settings/support-matrix` | Unified runtime setting parity matrix, presets, report exports, and streaming/media execution decision across Web/API/browser protocols. |
| `POST /api/v1/scripts/import/sample` | Imports raw HTTP, cURL, and REST samples through the shared HTTP request finalizer. |
| `POST /api/v1/scripts/import/har` | Imports HAR requests while preserving ordered header occurrences and governed source review. |
| `POST /api/v1/scripts/import/postman` | Imports Postman collections with collection/folder/request inheritance resolved by the adapter. |
| `POST /api/v1/scripts/import/jmeter` | Imports JMeter plans; HTTP samplers use the shared request finalizer while non-HTTP sampler routes retain their existing converters. |
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

## Shared Request Import Pipeline

HTTP-producing adapters now converge on:

**source adapter → effective request → shared credential projection → shared DSL finalization/readiness → existing API workflow**

The source adapter still owns format-specific parsing, inheritance, schema/reference resolution, and recognition of source language constructs. The shared stages own runtime methods and limits, URL/header/auth/body projection, credential requirements, request-shaped action traversal, missing binding analysis, durable execution gates, and final DSL validation.

`header_list` is the authoritative logical occurrence list in the DSL and retains active duplicates, authored casing, and source order. The compatibility `headers` map is derived with the last case-insensitive occurrence winning, and map-only sources receive deterministic name order plus the informational `source_order_unavailable` diagnostic. HTTP transports may normalize header casing or order, combine protocol-compatible repeated values, and own framing headers. When a special header occurrence cannot retain its meaning—such as duplicate singleton, transport-owned, or protocol-incompatible connection headers—the importer omits the unsafe occurrence, records value-free fidelity evidence, and attaches a durable `import_review_required` gate; acknowledgement does not make the request executable.

HTTP version selection follows the native runtime contract: an empty `config.http.version` auto-negotiates HTTP/2 or HTTP/1.1, `"1.1"` requires exact HTTP/1.1, and `"2"` requires HTTP/2 with no HTTP/1.1 fallback. HTTP/1.0 is neither authorable nor valid for the native runtime. Imports that require another version retain value-free source evidence in a blocked draft instead of silently changing the requested wire protocol.

Credential projection covers URL components, every ordered header occurrence, typed auth, JSON, XML, form-urlencoded, multipart, and GraphQL. Exact governed runtime values remain dynamic; source literals become whole `{{secret.NAME}}` references only when encoding and wire shape can be preserved. Raw migration values are confined to protected in-memory/session handoff and are excluded from scripts, diagnostics, IDs, source review, logs, and receipts. `sanitize_sensitive=false` remains a converter compatibility option, but detected literals add the durable `import_credentials_unmanaged` execution gate.

### Conversion dispositions

These dispositions describe the result of conversion, not the breadth labels (`supported`, `partial`, or `review_only`) used by source support matrices:

| Disposition | Meaning |
| --- | --- |
| `executable_exact` | The supported source behavior is represented without a known wire-semantic loss. Missing deployed values for valid `{{secret.*}}` references remain a dynamic execution preflight check. |
| `executable_bounded` | The request is replay-safe after a documented clamp to a native runtime ceiling, such as timeout or redirect limits. A stable diagnostic is retained; an adapter may still require acknowledgement before promotion. |
| `blocked_draft` | A structurally valid, disclosure-safe script can be saved, but durable review, missing-variable/file, unsupported-auth, residual-source-logic, or unmanaged-credential gates stop replay and launch before transport. Acknowledgement alone never clears an execution gate. |
| `rejected` | No valid disclosure-safe draft can be constructed. Examples include credential-bearing malformed structured content, type-unsafe sensitive values, or an unsafe archive. No runnable script is returned. |

Any DSL validation issue remaining after shared finalization is an importer invariant failure, not a warning attached to a runnable script.

### Tested adopter dispositions

| Adapter | `executable_exact` | `executable_bounded` | `blocked_draft` | `rejected` |
| --- | --- | --- | --- | --- |
| Sample / raw HTTP / cURL / REST / HAR | Static supported requests; ordered raw/HAR headers; byte-preserved nonsensitive bodies; governed URL/header/body credentials | Source timeout values use the native HTTP ceiling and retain a clamp diagnostic | Missing references, unmanaged credentials, malformed-but-disclosure-safe declared bodies, non-origin raw request targets, or per-request HTTP versions the native runtime cannot reproduce exactly | Sensitive non-string structured values or credential-bearing malformed content that cannot be made safe |
| Postman | Supported static requests; ordered duplicate headers; collection → folder → request auth; basic, bearer, digest, API key; supported body modes and translated assertion/correlation subset | Not currently emitted by the Postman adapter | NTLM/signing/unknown auth, residual pre-request/test JavaScript, missing/cyclic bindings, or missing multipart files | Unsafe structured credentials or a request for which no valid safe draft can be built |
| JMeter HTTP | HTTP samplers, ordered HeaderManager entries, basic/bearer/digest, supported assertions/extractors, and mapped control flow | Oversized HTTP timeout/redirect settings are clamped to runtime capabilities with stable diagnostics | Kerberos/NTLM/negotiate, complex or residual scripting/control behavior, and missing bindings | Unsafe structured credential content or invalid input that cannot produce a valid draft; non-HTTP samplers keep their existing routes |
| OpenAPI | Supported operations and parameters, examples, native digest, and the first fully supported OR security alternative with every scheme in its AND requirement | Not currently source-emitted | Missing base URL/files/variables, conflicting or unsupported security combinations, and ambiguous behavior that affects replay | Unsafe typed/malformed credential examples or a document with no safely constructible operation |
| WSDL | Fully resolved HTTP document/literal operations with preserved SOAP version, action, namespaces, content type, and nonsensitive envelope bytes | Not currently source-emitted | RPC/encoded ambiguity, missing endpoints/binding operations, non-HTTP or undeclared transports, unresolved imports, incomplete input/header/XSD skeletons, mixed sensitive XML, or unresolved credential/file/variable bindings | Unsafe WSDL ZIP input, malformed sensitive XML, or no viable service/binding/operation |
| k6 | Statically proven string-literal GET, POST, PUT, PATCH, DELETE, HEAD, and OPTIONS calls | Not currently emitted by the k6 adapter | Dynamic expressions, loops/branches, checks, thresholds, modules, browser behavior, or other residual JavaScript | Unsafe structured credential literals or a call that cannot produce a disclosure-safe valid request |
| Safe archive ingress | A validated regular-file archive proceeds to WSDL, DevWeb, or Enterprise Script Archive root selection | — | Unsafe containers are never downgraded to drafts; downstream adapter behavior may still be blocked | Traversal/absolute/drive paths, links/devices, invalid names, depth/case/NFC/separator collisions, entry/size floods, excessive compression ratios, declared-size mismatch, or decompression bombs |

The shared ZIP/TAR reader is bounded and in-memory. WSDL ZIP, DevWeb ZIP, and Enterprise Script Archive ZIP currently use it. Bruno archive ingestion retains its compatibility path until its path, collision, identifier, and receipt-order tests can adopt the shared reader without changing the Bruno contract.

## Support Matrix

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

## OpenAPI Import

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

## WSDL Import

The WSDL importer converts SOAP service metadata into SOAP DSL drafts:

- Extracts service name, target namespace, endpoint address, port types, bindings, and operations.
- Detects SOAP 1.1 versus SOAP 1.2 from the WSDL content.
- Preserves SOAPAction values when present.
- Generates SOAP envelope scaffolds per operation.
- Uses byte-preserving shared XML credential projection for simple element text and attributes.
- Enables SOAP fault handling by default.
- Returns review sections for operations, payload review, and namespace preservation.

Document/literal operations can be exact. RPC/encoded ambiguity and sensitive mixed-content XML are sanitized and durably blocked; complex XSD payload generation remains scaffolded for review. Authors must confirm required elements and replace unresolved placeholders before production replay.

## DevWeb Matrix

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

## Planning Envelopes

The Web/API support endpoint mirrors the protocol density envelopes in `docs/ENTERPRISE_AUTOSCALING_SIGNALS.md`. These are design/planning targets, not measured benchmark results.

| Protocol | Default envelope |
| --- | ---: |
| HTTP / HTTPS / HTTP/2 | 10,000 VUsers / generator |
| gRPC | 12,000 VUsers / generator |
| Java-over-HTTP | 7,000 VUsers / generator |
| WebSocket | 6,000 VUsers / generator |
| SOAP | 5,000 VUsers / generator |
| JMS / Solace / IBM MQ | 3,000 VUsers / generator |
| Browser canaries | 20-40 VUsers / generator |

These are conservative planning envelopes unless a deployment publishes local benchmark evidence with hardware, generator profile, script complexity, VUsers, TPS, and archive evidence.

## Diagnostics

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

## Sample Projects

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

## Plan Mapping

| Work item | Status | Evidence |
| --- | --- | --- |
| W1.1 Web/API execution support matrix | Complete | `GET /api/v1/scripts/web-api/support-matrix`, `pkg/api/web_api_support.go` |
| W1.2 OpenAPI import/authoring review | Complete for backend import/review foundation | `POST /api/v1/scripts/import/openapi`, `pkg/convert/openapi` |
| W1.3 WSDL-assisted SOAP authoring | Complete for backend import/review foundation | `POST /api/v1/scripts/import/wsdl`, `pkg/convert/wsdl` |
| W1.4 Benchmark-calibrated density envelopes | Complete for code-owned publication of existing envelopes | Support endpoint plus `docs/ENTERPRISE_AUTOSCALING_SIGNALS.md` |
| W1.5 Execution smoke fixtures | Complete for sample pack, archive-signal fixture contract, and import/API tests; live archived-report publication remains benchmark evidence | `examples/web-api`, converter/API tests |
| W1.6 DevWeb construct support matrix | Complete | `GET /api/v1/scripts/import/devweb/support-matrix`, `pkg/convert/devweb/support.go` |
| W1.7 Protocol-specific diagnostics | Complete for code-owned support publication using existing live/archive signals | Support endpoint diagnostics plus protocol archive implementations |
| W1.8 Web/API sample projects | Complete | `examples/web-api/*.json` |
