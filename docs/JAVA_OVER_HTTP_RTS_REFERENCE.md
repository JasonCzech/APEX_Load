# OpenText VuGen Runtime Settings (RTS) Reference
## Java over HTTP Protocol

The "Java over HTTP" protocol is a hybrid protocol in LoadRunner. It allows for Java-based scripting while utilizing the underlying HTTP transport for communication. Consequently, its Runtime Settings (RTS) are a combination of standard Java execution settings and web-specific protocol configurations.

---

### 1. Java VM (Critical)
This section defines the Java Virtual Machine environment used for executing the script logic.
*   **JDK/JRE Location:** Specifies the absolute path to the Java Development Kit or Runtime Environment.
*   **Java VM Arguments:** Custom parameters for the JVM (e.g., `-Xmx512m` for heap size).
*   **Classpath:** List of external `.jar` files, libraries, or directories required for script execution.
*   **Enable Java VM Logging:** Captures internal JVM console output and errors.

### 2. Run Logic
*   **Iterations:** Number of times the Java `Action` block repeats.
*   **Block Scheduling:** Logic for nested action execution (Sequential/Random).
*   **Init/Action/End Sequence:** Standard Vuser execution flow.

### 3. Pacing
*   **Iteration Delay:** Fixed, random, or interval-based delays between loop executions.

### 4. Logging
*   **Standard Log:** Basic operation and script execution logs.
*   **Extended Log:**
    *   **Parameter Substitution:** Logs data values used in Java variables.
    *   **Data Returned by Server:** Logs the raw HTTP responses (even if handled via Java API).
    *   **Advanced Trace:** Detailed low-level execution info.

### 5. Think Time
*   **Replay Modes:** Ignore, Replay as recorded, Multiply, or Limit think times.

### 6. Miscellaneous (Misc)
*   **Error Handling:** "Continue on Error" behavior.
*   **Multithreading:**
    *   **Run Vuser as a thread:** Recommended for high-density Java Vusers.
    *   **Run Vuser as a process:** Better isolation, but much higher memory footprint per Vuser.

### 7. Internet Protocol (Web Features)
Since the underlying communication is HTTP, these standard web settings apply:

#### 7.1 Proxy
*   **Proxy Configuration:** Direct, System, or Custom proxy settings for the Java-generated HTTP requests.

#### 7.2 Preferences (HTTP Engine)
*   **HTTP Version:** (1.1, 2.0).
*   **Timeouts:** Connect, Receive, and Step download timeouts for the HTTP transport.
*   **Keep-Alive:** Controls whether to maintain persistent connections.

#### 7.3 Browser Emulation
*   **User-Agent:** Identifying string sent in the HTTP headers.
*   **Cache Simulation:** Simulates browser-level caching of static resources.

#### 7.4 Content Check
*   **Text/String Search:** Global rules to fail transactions if specific error strings (e.g., "Exception", "Login Failed") appear in server responses.

---

### Implementation Note for Stormrunner:
When importing "Java over HTTP" scripts, the **Java VM** settings must be mapped to the environment configuration of the Load Generator pods, while the **Internet Protocol** settings map to the `pkg/protocol/http` configuration map within the Stormrunner DSL.
