# LoadRunner Web HTTP/HTML RTS Mapping

This document maps the Web HTTP/HTML runtime settings surfaced in Stormrunner to their current support status. The goal is to make imported and manually authored RTS values visible, preserve compatibility where possible, and make execution gaps explicit.

## Status Legend

- `Executed`: Stormrunner enforces the setting at runtime today.
- `Partial`: Stormrunner executes part of the setting surface, but not the full LoadRunner behavior.
- `Compatibility-only`: Stormrunner preserves and edits the setting, but does not execute its full runtime semantics yet.
- `Non-applicable`: The LoadRunner concept does not map cleanly to Stormrunner's architecture.
- `Blocked`: The setting is modeled, but execution depends on missing metadata or infrastructure.

## Mapping

| RTS area | LoadRunner concept | Stormrunner field(s) | Status | Notes |
| --- | --- | --- | --- | --- |
| Run Logic | Iterations | `config.run_logic.iterations` | Executed | Applied by the engine iteration loop. |
| Run Logic | Auto-transactions | `config.run_logic.auto_transactions` | Executed | Supports per-action and per-iteration wrapping. |
| Run Logic | Block scheduling | `config.run_logic.block_scheduling` | Executed | Supports sequential, random, and percentage scheduling. |
| Run Logic | Action sequence | `config.run_logic.action_sequence` | Executed | Preserved and enforced by the engine action ordering. |
| Pacing | Delay mode and bounds | `config.pacing.*` | Executed | Fixed, random delay, and related pacing fields execute in the engine. |
| Think Time | Ignore, multiplier, limit | `config.think_time.*` | Executed | Applied in the engine after action execution. |
| Logging | Log level, error-only, extended options | `config.log.*` | Partial | Preserved and round-tripped; runtime fidelity is narrower than VuGen extended tracing. |
| Misc | Continue on error | `config.error_handling.continue_on_error` | Executed | Supported through runtime error handling paths. |
| Misc | Snapshot on error | `config.error_handling.snapshot_on_error` | Partial | Supported where protocol runtimes can capture snapshots. |
| Browser Emulation | Cache enabled | `config.browser.cache_enabled` | Executed | Browser runtime toggles Chrome cache usage. |
| Browser Emulation | Cache URLs requiring content | `config.browser.cache_content_urls` | Executed | Browser runtime forces content request revalidation when disabled. |
| Browser Emulation | Check for newer versions | `config.browser.check_newer_versions` | Executed | Browser runtime injects revalidation headers. |
| Browser Emulation | New user each iteration | `config.browser.new_user_per_iteration` | Executed | Browser protocol is reset between iterations. |
| Browser Emulation | Download resources = all | `config.browser.download_resources = "all"` | Executed | Default browser behavior. |
| Browser Emulation | Download resources = none | `config.browser.download_resources = "none"` | Executed | Static asset URLs are blocked in browser replay. |
| Browser Emulation | Download resources = recorded | `config.browser.download_resources = "recorded"` | Partial | Browser replay enforces recorded-only assets when preserved recorded-resource metadata is available from import/recording flows. |
| Browser Emulation | User agent | `config.browser.user_agent`, `config.http.user_agent` | Executed | Supported in browser and HTTP protocols. |
| Browser Emulation | Accept-Language / Accept-Encoding | `config.http.accept_language`, `config.http.accept_encoding` | Executed | Applied as HTTP defaults and imported from runtime settings. |
| Proxy | Proxy mode and custom proxy | `config.http.proxy_mode`, `config.http.proxy.*` | Executed | Supports `none`, `system`, and `custom`. |
| Additional Attributes | Arbitrary RTS name/value metadata | `config.web_runtime.additional_attributes` | Compatibility-only | Preserved for authoring/import parity. |
| Download Filters | Allow list / block list hosts | `config.web_runtime.download_filters.*` | Executed | Browser runtime enforces host-based asset filtering. |
| Content Check | Global required/forbidden text checks | `config.web_runtime.content_checks[]` | Executed | HTTP runtime enforces checks against main-document responses. |
| Network Speed Simulation | Preset and custom bandwidth/latency | `config.web_runtime.network_simulation.*` | Executed | Browser runtime maps settings onto DevTools network throttling. |
| Streaming | Adaptive vs fixed mode | `config.web_runtime.streaming.mode` | Compatibility-only | Stored and imported, but Stormrunner does not execute media playback semantics. |
| Streaming | Buffer size | `config.web_runtime.streaming.buffer_size_seconds` | Compatibility-only | Preserved for parity only. |
| Streaming | Fixed bitrate | `config.web_runtime.streaming.fixed_bitrate_kbps` | Compatibility-only | Preserved for parity only. |
| Streaming | Bandwidth estimation | `config.web_runtime.streaming.bandwidth_estimation` | Compatibility-only | Preserved for parity only. |
| Data Formatting | Auto-detect DFEs | `config.web_runtime.data_formatting.auto_detect` | Partial | Preserved and passed into HTTP runtime handling. |
| Data Formatting | DFE transform chain | `config.web_runtime.data_formatting.chain[]` | Partial | HTTP runtime executes `base64`, `json_to_xml`, `url_decode`, and `html_unescape`; broader parity is still incomplete. |
| Replay engine mode | WinInet / process-vs-thread runtime concepts | N/A | Non-applicable | Stormrunner uses Go HTTP/browser runtimes and goroutine VUsers instead of WinInet or Windows process/thread replay modes. |

## Current gaps

- `download_resources = recorded` depends on preserved recorded-resource metadata. Browser replay now enforces it for import/recording flows that capture those URLs, but manually authored scripts without metadata still cannot execute that mode.
- Streaming/media playback semantics are intentionally compatibility-only for now.
- Browser/runtime-wide DFE parity is not complete beyond the current HTTP response transform chain.
- Logging fidelity is narrower than full VuGen extended trace behavior.
