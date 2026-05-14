# OpenText VuGen Runtime Settings (RTS) Reference
## Web - HTTP/HTML Protocol

This document provides a comprehensive reference of the Runtime Settings available in OpenText VuGen for the Web - HTTP/HTML protocol. These settings are critical for accurate performance simulation and are typically stored in the `default.cfg` file of a VuGen script.

---

### 1. Run Logic
Defines how many times the Vuser executes the "Action" blocks and in what sequence.
*   **Number of Iterations:** Total times the `Action` block repeats.
*   **Block Scheduling:** Logic for looping specific actions (Sequential, Random, or Percentage-based).
*   **Init/Action/End Sequence:** Order of `vuser_init`, `Action(s)`, and `vuser_end`.

### 2. Pacing
Controls the time interval between iterations to manage throughput.
*   **Start new iteration immediately:** No delay between iterations.
*   **Start new iteration after previous iteration ends:** Fixed or Random delay.
*   **Start new iteration at fixed/random intervals:** (e.g., every 60 seconds from the start of the previous one).

### 3. Logging
Configures the verbosity and frequency of the execution log.
*   **Disable Logging:** No logs (recommended for peak load tests).
*   **Standard Log:** Basic execution steps and error messages.
*   **Extended Log:**
    *   **Parameter Substitution:** Shows data values used during parameterization.
    *   **Data Returned by Server:** Full HTML/JSON/XML responses.
    *   **Advanced Trace:** Detailed socket and internal engine info.
*   **Log only when an error occurs:** Standard logging detail is only written to the log if a failure is detected.

### 4. Think Time
Simulates real-world user pause time between browser actions.
*   **Ignore Think Time:** Execute steps as fast as the network/server allows.
*   **Replay Think Time:**
    *   **As recorded:** Use the exact `lr_think_time` values captured.
    *   **Multiply recorded think time by:** Scalar factor (e.g., 0.5x, 2x).
    *   **Random percentage:** Apply a range (e.g., 50% to 150% of recorded time).
*   **Limit think time to:** Cap the maximum delay (e.g., no pause longer than 10s).

### 5. Additional Attributes
Allows passing custom command-line arguments or environment-specific parameters.
*   **Name/Value pairs:** Accessible in script via `lr_get_attrib_string()` or `lr_get_attrib_long()`.

### 6. Miscellaneous (Misc)
Core execution and error handling behavior.
*   **Error Handling:**
    *   **Continue on Error:** Script continues execution even if a specific step fails.
    *   **Generate snapshot on error:** Captures page state/data for post-failure analysis.
*   **Multithreading:**
    *   **Run Vuser as a thread:** Uses shared memory, high density (default).
    *   **Run Vuser as a process:** Isolated memory, higher resource overhead.
*   **Automatic Transactions:** Automatically wrap each action block in a transaction.

### 7. Browser Emulation
Configures the Vuser to simulate specific browser characteristics.
*   **User-Agent:** Choose browser (Chrome, Firefox, Safari) or provide a custom string.
*   **Simulate Browser Cache:**
    *   **Cache URLs requiring content:** Controls what resources are stored in memory.
    *   **Check for newer versions of stored pages:** Simulates browser "If-Modified-Since" logic.
*   **Simulate a new user on each iteration:** Clears cookies and cache at the start of every iteration.
*   **Download non-HTML resources:** Determines if the Vuser fetches images, CSS, JS, etc.

### 8. Network Speed Simulation
Simulates various bandwidth constraints and latencies.
*   **Use maximum bandwidth:** No artificial throttling.
*   **Simulate bandwidth:** Choose presets (56k, DSL, T1, 10Mbps) or define custom Mbps/Kbps.

### 9. Streaming
Specific to media and segmented streaming (HLS/DASH).
*   **Streaming Mode:** Adaptive or fixed bitrate.
*   **Buffer Size:** Simulation of player buffer depth.
*   **Bandwidth Estimation:** Logic for bitrate switching.

### 10. Internet Protocol (Node)
The primary parent node for advanced HTTP engine behaviors.

#### 10.1 Proxy
*   **No Proxy:** Direct connection to the target server.
*   **Use system proxy:** Inherits Windows/Global proxy settings.
*   **Custom Proxy:** Specific IP/Port and Authentication (NTLM/Basic/Kerberos).

#### 10.2 Preferences
Deep-level HTTP engine settings.
*   **HTTP Version:** (1.0, 1.1, 2.0).
*   **Max Connections per Host:** Parallel socket count (default 6 for modern browsers).
*   **Timeouts:**
    *   **Step download timeout:** Max time for a full page download.
    *   **Receive timeout:** Max time waiting for individual packets.
    *   **Connect timeout:** Max time to establish a socket connection.
*   **WinInet Replay:** Forces usage of Windows API instead of internal Sockets.

#### 10.3 Download Filters
*   **Include/Exclude Hosts:** Regex or URL list to block/allow specific background traffic.

#### 10.4 Content Check
*   **Search for text/string:** Automated global check (e.g., "Error", "Access Denied") to fail the script if found in any response.

### 11. Data Formatting
Relates to **Data Format Extensions (DFE)** used for complex/binary data.
*   **DFE Chain:** Sequence of decoders (GWT, Base64, JSON-to-XML) to make data readable.
*   **Auto-detect:** Automatically identify and apply decoders during replay.
