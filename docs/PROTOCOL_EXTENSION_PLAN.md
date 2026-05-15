# Implementation Plan: Advanced Protocol Support

## Objective
Add first-class **SOAP** and targeted **Java-over-HTTP** support to Stormrunner without weakening the current execution model:

- The Go load generator remains the runtime owner for protocol execution.
- Authoring-time tooling in Python and the dashboard improves script generation and editing, but is not required in the request hot path.
- New protocol features extend the current HTTP foundation where possible instead of duplicating it.

## Current Baseline
Stormrunner already has pieces this plan can build on:

- HTTP is a registered protocol implementation and already owns request execution, extractor execution, and assertions.
- XPath extraction already exists in `pkg/protocol/http/extract.go`, including namespace-aware expressions and XML/HTML fallback behavior.
- The DSL already supports protocol actions, headers, body payloads, extractors, assertions, and metadata, but it does not yet model SOAP-specific or Java-serialization-specific configuration as first-class fields.
- The MCP server in `mcp/server.py` is an authoring/automation surface, not a load-generation runtime dependency.

The plan below starts from that baseline and avoids re-implementing existing XPath capabilities.

## Current Implementation Status
The SOAP runtime is no longer just planned work. The following Phase 1 items are now implemented in the Go runtime:

- Explicit SOAP DSL fields for script-level config and action-level SOAP metadata.
- Registered `pkg/protocol/soap` execution on top of the existing HTTP transport.
- SOAP 1.1 and SOAP 1.2 request defaults, `SOAPAction` handling, and generated-envelope shaping with namespace support.
- WS-Security UsernameToken and Timestamp header generation, including nonce and created support.
- SOAP Fault detection that maps fault envelopes into Stormrunner result semantics, preserves the raw response body for reporting, and now populates error snapshots for `snapshot_on_error` flows even when the transport status remains HTTP 200.
- Unit coverage for DSL parsing, validation, engine config propagation, envelope shaping, WS-Security headers, and SOAP 1.1/1.2 fault handling.
- A focused benchmark that compares plain HTTP execution with SOAP-generated envelope execution against the same local server path.

The main SOAP items still open from Phase 1 are broader hardening and any additional benchmark passes needed before claiming production overhead budgets.

## Design Principles

### 1. Keep Runtime Ownership in the Generator
Protocol execution must stay inside the Go load-generator runtime. Authoring-time helpers may run in Python, but generator pods must not depend on the MCP server for per-request execution.

### 2. Prefer Composition Over Duplication
SOAP should be implemented as a protocol package that composes the existing HTTP executor behavior rather than a parallel networking stack.

### 3. Make the DSL Explicit
If a capability is expected to survive import, edit, export, and execution, it must be represented explicitly in the DSL instead of being hidden in opaque metadata.

### 4. Benchmark Before Claiming Scale
Any claim about preserving the 10k VUser/pod target must be backed by measured CPU, memory, and latency budgets for the new protocol path.

## Scope

### In Scope
- SOAP request authoring and execution on top of the HTTP transport.
- SOAP fault parsing and XPath-based correlation.
- WSDL-assisted template generation for authoring workflows.
- A narrowly defined Java-over-HTTP execution path for one supported wire format at a time.
- Dashboard/editor updates that sit on top of stable DSL and runtime behavior.

### Out of Scope for the First Cut
- Full WS-Security signing and encryption.
- Multiple Java serialization formats in the same milestone.
- A separate XML or Java execution engine outside the generator.
- Dashboard-first implementation before the runtime and DSL surfaces stabilize.

## 1. SOAP Support

### Architecture
SOAP should be treated as an HTTP-backed protocol profile implemented in `pkg/protocol/soap`, using the existing HTTP transport and extractor/assertion machinery where practical.

### Runtime Components
- **SOAP Protocol Package (`pkg/protocol/soap`)**
    - Registers as a protocol implementation through the existing protocol registry.
    - Delegates network execution to HTTP-compatible request handling.
    - Applies SOAP-specific defaults such as `Content-Type`, optional `SOAPAction`, and envelope-aware fault handling.
- **SOAP Envelope Builder**
    - Builds or validates request envelopes from DSL inputs.
    - Handles namespace declarations deterministically.
    - Supports templated values in body content without hiding the raw XML from advanced users.
- **SOAP Fault Interpreter**
    - Detects SOAP Fault responses and maps them into Stormrunner result semantics.
    - Exposes fault code and fault string for assertions and reporting.

### WS-Security Strategy
WS-Security must be modeled as SOAP envelope header generation, not as HTTP header injection.

Phase 1 security support should be limited to:

- UsernameToken
- Timestamp
- Basic nonce/created generation where required

Deferred to a later phase:

- XML Signature
- XML Encryption
- X.509-based signing
- Full WS-Policy interpretation

### Authoring Components
- **WSDL Introspection Tooling**
    - Runs as authoring-time tooling, not in the load execution path.
    - Fetches WSDL/XSD, extracts operations, namespaces, endpoints, and sample request structures.
    - Produces a DSL starter script with explicit SOAP fields populated.
- **SOAP Body Builder**
    - Exists as a dashboard/editor aid, not as a required runtime service.
    - Supports raw XML editing plus a structured operation-driven view when WSDL data is available.

### Required DSL Additions
Add explicit SOAP fields rather than encoding everything into generic metadata. Proposed fields:

- `protocol`: `soap`
- `soap.version`: `1.1` or `1.2`
- `soap.action`: optional SOAPAction override
- `soap.operation`: logical operation name
- `soap.namespaces`: prefix-to-URI map
- `soap.body_mode`: `raw_xml` or `generated`
- `soap.ws_security`: object describing UsernameToken/Timestamp settings
- `soap.fault_handling`: `fail_on_fault` defaulting to true

## 2. Java over HTTP Support

### Architecture
Java-over-HTTP should use a bridge pattern only if the supported wire format cannot be executed efficiently inside the Go runtime. That bridge must be colocated with the generator runtime, not integrated into the MCP server.

### Runtime Boundary
There are two acceptable runtime models:

1. A generator-local helper library or process packaged with the load generator.
2. A sidecar that is deployed with the generator pod and reached over a local interface.

Unacceptable model for execution:

- Calling the Python MCP server for per-request serialization or deserialization.

### Scope Constraint
The first implementation must support exactly one Java wire format. Candidates may include native Java serialization or a vendor-specific codec, but the chosen format must be named explicitly before implementation starts.

### Runtime Components
- **Java HTTP Protocol Package (`pkg/protocol/java_http`)**
    - Owns request shaping, response handling, and helper integration.
    - Uses pooled buffers only after measurement shows they materially reduce allocation pressure.
- **Serializer Bridge**
    - Converts a structured DSL payload to the chosen binary form immediately before transmission.
    - Converts captured or response payloads into a stable inspection format for authoring and correlation.
- **Object Path Correlation**
    - Extracts values from the decoded object representation using a defined path syntax.
    - Avoids overloading JSONPath unless the decoded representation is guaranteed to be JSON-shaped and stable.

### Required DSL Additions
Proposed explicit fields:

- `protocol`: `java_http`
- `java_http.codec`: selected wire format identifier
- `java_http.helper_mode`: `embedded` or `sidecar`
- `java_http.object_schema`: optional type/schema hint
- `java_http.request_mapping`: mapping from DSL fields to serialized object fields
- `java_http.response_mapping`: mapping for decoded object projection
- `java_http.extractor_type`: object-path selector type for decoded payloads

## 3. Dashboard And UX

Dashboard work should be layered onto existing editor surfaces rather than treated as a separate product track.

### SOAP UX
- Add SOAP authoring support into the existing script editing flow.
- Provide XML linting, namespace assistance, and XPath helper tooling.
- Show SOAP Fault details in execution and script validation views.

### Java UX
- Add a decoded object inspector only after the decode format is stable.
- Show request and response projections in the existing editor and recorder flows.
- Avoid building a generic “Java object tree” before the supported codec and path semantics are locked.

### Ordering Constraint
No dashboard editor work should block runtime support. Editor work starts after the corresponding DSL and execution surfaces are stable enough to keep UI churn low.

## Recommended Starting Stance

To keep this plan executable, Phase 0 should use the following working defaults unless a concrete target requirement disproves them:

- Start with SOAP before Java-over-HTTP.
- Treat SOAP 1.1 as required for the first runtime slice and SOAP 1.2 as a follow-on compatibility addition.
- Limit the first WS-Security milestone to UsernameToken and Timestamp.
- Do not start Java runtime work until one named codec target and one representative sample payload are captured.
- Prefer an embedded helper boundary for Java only if the chosen codec can be implemented safely inside the generator process; otherwise use a generator-local sidecar.

This ordering keeps the first implementation aligned with the current HTTP-centered runtime and avoids starting a Java bridge without a real interoperability target.

## 4. Implementation Roadmap

| Phase | Milestone | Deliverables | Exit Criteria |
| :--- | :--- | :--- | :--- |
| **Phase 0** | **Protocol Definition** | Choose first Java wire format, define SOAP and Java DSL schema, define benchmark plan, define failure semantics | DSL fields reviewed, wire format selected, benchmarks agreed |
| **Phase 1** | **SOAP Runtime MVP** | `pkg/protocol/soap`, HTTP-backed execution, SOAP defaults, fault parsing, UsernameToken/Timestamp support, tests | Implemented in code, with a focused HTTP-vs-SOAP benchmark now in place. Remaining work is hardening and any broader benchmark validation needed for release claims. |
| **Phase 2** | **SOAP Authoring** | WSDL-to-DSL generator, namespace-aware templates, editor support for XML and XPath helpers | WSDL import produces executable starter scripts, editor can round-trip SOAP fields |
| **Phase 3** | **Java HTTP Runtime MVP** | `pkg/protocol/java_http`, chosen codec support, local helper model, decode/extract path support, tests and benchmarks | One codec runs end-to-end with measured overhead and failure handling |
| **Phase 4** | **Protocol-Specific UX** | SOAP visual helpers, Java decoded inspector, recorder/import polish | UI reflects stable DSL/runtime behavior without hidden protocol metadata |
| **Phase 5** | **Advanced Security And Scale** | Optional SOAP signing/encryption, runtime hardening, scale tuning | Non-functional targets met under benchmark load |

## Recommended First Slice

The first implementation slice should be a SOAP runtime MVP that proves the architecture without pulling UI work forward.

### Deliverables
- Add explicit SOAP DSL fields to the schema and parser.
- Create `pkg/protocol/soap` as an HTTP-backed protocol package.
- Support SOAP 1.1 envelope execution, `SOAPAction`, namespace-aware request shaping, and fault parsing.
- Reuse the existing XPath extraction path for SOAP correlation and assertions.
- Add table-driven unit tests plus a small benchmark comparing plain HTTP and SOAP-shaped requests.

Status note: the DSL, runtime, fault parsing, focused unit coverage, and an initial HTTP-vs-SOAP comparison benchmark are now in place. Any additional benchmarking should be treated as release-hardening work rather than a blocker for the current runtime slice.

### Exit Criteria
- One SOAP script can execute end-to-end through the generator without MCP runtime involvement.
- SOAP Faults are surfaced as observable assertion/reporting inputs.
- The benchmark establishes a baseline overhead budget for further SOAP work.

## 5. Testing And Benchmark Requirements

Every phase must include tests and measurable performance gates.

### Required Automated Coverage
- Unit tests for protocol registration, request shaping, extractors, and fault/error behavior.
- DSL parser tests for any new protocol fields.
- Conversion/import tests for WSDL or Java decode helpers where applicable.
- Benchmarks for request overhead, allocation count, and helper latency.

### Required Performance Checks
- Measure per-request overhead added by SOAP shaping versus plain HTTP.
- Measure serialization/deserialization overhead for the Java bridge.
- Measure memory growth and allocation rate with representative payload sizes.
- Verify helper failure modes do not deadlock or stall VUsers.

### Success Criteria
- SOAP runtime path does not materially regress existing HTTP execution outside the expected XML handling cost.
- Java-over-HTTP support stays within an explicitly defined latency and CPU budget for the chosen codec.
- No protocol requires the Python MCP service to be available during load execution.

## 6. Risks And Mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| SOAP scope grows into full WS-* too early | Delivery delay | Limit first phase to transport ergonomics plus UsernameToken/Timestamp |
| Java bridge adds unpredictable latency | Throughput regression | Keep helper local to generator, benchmark early, cap scope to one codec |
| DSL remains too generic | Poor editor and import round-trip behavior | Add explicit protocol fields before UI work |
| Dashboard starts before runtime stabilizes | Rework and churn | Gate editor work behind stable runtime and DSL milestones |
| Sidecar/helper failures break load execution | Runtime instability | Define timeouts, fallback semantics, health checks, and error surfaces before rollout |

## 7. Phase 0 Decisions To Lock

The following items should be locked before implementation expands beyond the first SOAP slice:

1. Name the first Java wire format target and capture sample payloads.
2. Confirm whether any near-term SOAP requirement forces SOAP 1.2 into the first release.
3. Confirm that UsernameToken and Timestamp are sufficient for the first WS-Security milestone.
4. Decide the acceptable overhead budget for SOAP shaping before the feature is considered too expensive for production load.
5. Choose the Java helper packaging model only after the codec target is known.

## Summary
This plan keeps protocol execution aligned with Stormrunner's current architecture:

- SOAP extends the existing HTTP-centered runtime rather than replacing it.
- Existing XPath support is reused instead of re-planned.
- Java-over-HTTP is constrained to a runtime-safe, measurable, codec-specific implementation.
- Dashboard and authoring improvements follow stable runtime and DSL contracts.

Future browser-side client metrics work is tracked separately in `docs/BROWSER_CLIENT_METRICS_PLAN.md`. That plan deliberately scopes the work to web-application browser telemetry rather than full desktop-protocol parity, so the architecture and positioning stay aligned with the current generator model.

That sequencing reduces risk, preserves the current generator model, and makes the roadmap executable instead of aspirational.
