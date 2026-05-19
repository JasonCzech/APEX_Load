# Web/API Execution Support Matrix

This document is the W1 evidence record for modern Web/API execution parity.

## API Surface

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/scripts/web-api/support-matrix` | Code-owned Web/API support, benchmark envelope, diagnostics, and sample-project matrix. |
| `POST /api/v1/scripts/import/openapi` | Imports OpenAPI 3.x or Swagger 2.0 JSON/YAML into a reviewable HTTP script draft. |
| `POST /api/v1/scripts/import/wsdl` | Imports WSDL into a reviewable SOAP script draft with generated operation envelopes. |
| `GET /api/v1/scripts/import/devweb/support-matrix` | Publishes DevWeb construct coverage and review requirements. |

OpenAPI and WSDL import endpoints return the shared script-import envelope:

- `script` and `scripts`
- `warnings`
- `stats`
- `summary`
- `review_sections`
- `unsupported_elements`
- `repair_suggestions`

This keeps OpenAPI/WSDL acquisition aligned with HAR, JMeter, LoadRunner, sample, and DevWeb imports.

## Support Matrix

`GET /api/v1/scripts/web-api/support-matrix` currently covers:

| Capability | Status | Execution posture |
| --- | --- | --- |
| HTTP/Web request execution | Supported | Executable |
| OpenAPI-assisted REST authoring | Supported | Review, then executable |
| SOAP/WSDL-assisted execution | Supported | Review, then executable |
| Java-over-HTTP | Supported | Executable |
| WebSocket | Supported | Executable |
| gRPC | Supported | Executable |
| JMS Solace | Supported | Executable with mock/local validation and controlled broker settings |
| Browser canaries | Supported | Diagnostic canary, not density load |
| DevWeb import | Supported | Review, then executable for mapped constructs |
| HAR/JMeter/LoadRunner migration | Supported | Review, then executable for mapped constructs |

## OpenAPI Import

The OpenAPI importer converts API design documents into HTTP DSL drafts:

- Extracts title, version, servers, and supported operations.
- Supports OpenAPI 3.x and Swagger 2.0 in JSON or YAML form.
- Converts GET, POST, PUT, PATCH, and DELETE operations into request actions.
- Builds path/query parameter placeholders using `{{data.openapi.<column>}}`.
- Generates an inline CSV seed data source for detected parameters and request-body fields.
- Infers bearer, basic, and header API-key auth placeholders.
- Generates request bodies from examples or simple object schemas.
- Adds status-code assertions from successful response definitions.
- Returns review sections for operations, auth, and data fit.

Complex schemas, customer-grade test data, OAuth flows, and environment-specific auth still require author review before replay or promotion.

## WSDL Import

The WSDL importer converts SOAP service metadata into SOAP DSL drafts:

- Extracts service name, target namespace, endpoint address, port types, bindings, and operations.
- Detects SOAP 1.1 versus SOAP 1.2 from the WSDL content.
- Preserves SOAPAction values when present.
- Generates SOAP envelope scaffolds per operation.
- Enables SOAP fault handling by default.
- Returns review sections for operations, payload review, and namespace preservation.

Complex XSD payload generation remains scaffolded for review. Authors must confirm required elements and replace placeholders before production replay.

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
| Custom correlation helpers | Partial; use Stormrunner guided correlation repair before approval |

## Benchmark Envelopes

The Web/API support endpoint mirrors the protocol density envelopes in `docs/ENTERPRISE_AUTOSCALING_SIGNALS.md`.

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
