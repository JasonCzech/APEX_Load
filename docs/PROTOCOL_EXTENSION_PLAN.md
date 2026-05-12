# Implementation Plan: Advanced Protocol Support

## Objective
To elevate **Web Services (SOAP)** and **Java over HTTP** to first-class protocols within Stormrunner, maintaining the high-performance Go engine while providing enterprise-grade script management.

## 1. Web Services (SOAP/WSDL)

### Architecture
Instead of treating SOAP as generic HTTP, Stormrunner will implement a specialized protocol wrapper that handles XML complexities natively.

### Key Components
- **Go Engine (`pkg/protocol/soap`):**
    - **SOAP Wrapper:** Inherits from HTTP but automates `Content-Type: text/xml` and `SOAPAction` header management.
    - **WS-Security Module:** Support for `WSSecurity` headers (UsernameToken, Timestamp).
    - **XPath Extractor:** Native XPath support in `pkg/protocol/http/extract.go` for robust XML data correlation.
- **Agentic Script Engine (Python):**
    - **WSDL Introspection:** Tooling to ingest WSDL URLs and generate DSL templates.
    - **SOAP Body Builder:** UI/CLI component for structured SOAP envelope generation.

---

## 2. Java over HTTP (Bridge Pattern)

### Architecture
Uses a **Bridge Architecture** to handle Java serialization without bloating the Go engine's memory or GC cycles.

### Key Components
- **Java Serialization Helper:** A lightweight sidecar (integrated into the MCP server) that handles binary translation.
- **Go Engine (`pkg/protocol/java_http`):**
    - **Binary Buffer Pooling:** Uses `sync.Pool` for high-performance binary traffic handling.
    - **Correlation Engine:** Support for "In-Object" correlation where JSONPaths map to fields inside nested Java objects.
- **Developer Experience:**
    - **Capture:** Decodes binary streams into readable JSON during import.
    - **Execution:** Go engine sends JSON-structured objects to the sidecar for binary serialization just before network transmission.

---

## 3. Dashboard & UX

- **XML Tree View:** Specialized editor with XML linting and XPath extraction tools.
- **Java Object Inspector:** Recursive tree viewer for Java objects, moving beyond hex dumps to readable state inspection.

---

## Implementation Roadmap

| Phase | Milestone | Description |
| :--- | :--- | :--- |
| **Phase 1** | **XPath & SOAP Basics** | Native XPath extractors and SOAPAction automation in Go. |
| **Phase 2** | **WSDL Automation** | Python-based WSDL-to-DSL converter. |
| **Phase 3** | **Java Serialization Bridge** | gRPC-based translation layer for JSON ↔ Java Binary. |
| **Phase 4** | **Visual Correlation** | Dashboard updates for XML/Java object path selection. |

> [!IMPORTANT]
> This approach preserves the **10k VUser/pod** target by offloading heavy object modeling to the sidecar while keeping the Go engine focused on high-speed IO.
