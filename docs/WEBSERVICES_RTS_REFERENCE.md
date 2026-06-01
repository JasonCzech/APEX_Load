# OpenText VuGen Runtime Settings (RTS) Reference
## Web Services Protocol

The "Web Services" protocol in LoadRunner is designed specifically for testing SOAP and XML-based web services. While it shares many core Runtime Settings with the standard Web protocol, its primary configuration for service definition (WSDL) and message structure is handled via specialized SOA (Service-Oriented Architecture) management tools.

---

### 1. Service Management (SOA Tools)
Unlike standard RTS categories, the core definition of a Web Service call is managed through the **SOA Tools** menu.
*   **WSDL Management:** Import and manage service definitions via URL or local file.
*   **Service/Port/Operation Selection:** Defines which specific operation is being invoked.
*   **Input/Output Arguments:** Structured mapping of script parameters to SOAP envelope fields.
*   **Security Settings:** Configuration for **WS-Security** (WSE), including signatures, encryption, and security tokens.

### 2. General Settings
Standard Vuser execution controls that apply to Web Service scripts.
*   **Run Logic:** Controls iterations and the flow between `vuser_init`, `Action`, and `vuser_end`.
*   **Pacing:** Defines delays between iterations to control the rate of service calls.
*   **Logging:**
    *   **Extended Log:** Crucial for debugging XML request/response bodies and parameter substitution.
    *   **Data Returned by Server:** Captures the full SOAP response for verification.
*   **Think Time:** Simulates delays between multiple service calls.

### 3. Miscellaneous (Misc)
*   **Error Handling:** "Continue on Error" allows the script to proceed if a specific service call returns a SOAP Fault.
*   **Snapshot on Error:** Captures the full XML context of a failed service call for debugging.

### 4. Internet Protocol (Transport Layer)
Web Services utilize HTTP/HTTPS as the transport protocol, making these settings vital.

#### 4.1 Proxy
*   **Proxy Configuration:** Configure direct, system, or custom proxy settings to route SOAP traffic through corporate gateways.

#### 4.2 Preferences
*   **HTTP Version:** (1.1, 2.0).
*   **Keep-Alive:** Controls persistent connections, which is often important for high-performance SOAP services.
*   **Timeouts:** Connect, Receive, and Step download timeouts for managing long-running service operations.

#### 4.3 Content Check
*   **SOAP Fault Detection:** Global rules to search for strings like `<soap:Fault>` or specific application error codes within the XML response body.

---

### Implementation Note for Stormrunner:
When importing "Web Services" scripts, Stormrunner will map the **WSDL definitions** to the `pkg/protocol/soap` wrapper. WS-Security configurations will be translated into automated header management in the Go engine, while the transport-level settings will be integrated into the standard HTTP protocol configuration.
