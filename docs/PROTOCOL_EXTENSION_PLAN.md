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

## 4. Implementation Roadmap

| Phase | Milestone | Deliverables | Exit Criteria |
| :--- | :--- | :--- | :--- |
| **Phase 0** | **Protocol Definition** | Choose first Java wire format, define SOAP and Java DSL schema, define benchmark plan, define failure semantics | DSL fields reviewed, wire format selected, benchmarks agreed |
| **Phase 1** | **SOAP Runtime MVP** | `pkg/protocol/soap`, HTTP-backed execution, SOAP defaults, fault parsing, UsernameToken/Timestamp support, tests | SOAP scripts execute end-to-end, faults are observable, no MCP dependency in runtime |
| **Phase 2** | **SOAP Authoring** | WSDL-to-DSL generator, namespace-aware templates, editor support for XML and XPath helpers | WSDL import produces executable starter scripts, editor can round-trip SOAP fields |
| **Phase 3** | **Java HTTP Runtime MVP** | `pkg/protocol/java_http`, chosen codec support, local helper model, decode/extract path support, tests and benchmarks | One codec runs end-to-end with measured overhead and failure handling |
| **Phase 4** | **Protocol-Specific UX** | SOAP visual helpers, Java decoded inspector, recorder/import polish | UI reflects stable DSL/runtime behavior without hidden protocol metadata |
| **Phase 5** | **Advanced Security And Scale** | Optional SOAP signing/encryption, runtime hardening, scale tuning | Non-functional targets met under benchmark load |

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

## 7. Open Decisions Before Implementation Starts

The following items must be resolved in Phase 0:

1. Which Java wire format is the first supported target?
2. Is SOAP 1.2 required in the first release, or is SOAP 1.1 sufficient?
3. Are UsernameToken and Timestamp enough for the first WS-Security milestone?
4. Which helper packaging model is preferred for Java execution: embedded library or local sidecar?
5. What overhead budget is acceptable before the feature is considered too expensive for production load?

## Summary
This plan keeps protocol execution aligned with Stormrunner's current architecture:

- SOAP extends the existing HTTP-centered runtime rather than replacing it.
- Existing XPath support is reused instead of re-planned.
- Java-over-HTTP is constrained to a runtime-safe, measurable, codec-specific implementation.
- Dashboard and authoring improvements follow stable runtime and DSL contracts.

That sequencing reduces risk, preserves the current generator model, and makes the roadmap executable instead of aspirational.
