# Native archive compatibility

Re-import an archive into a new review draft to use these mappings. Stored scripts and approved runtime overrides are not rewritten. The original source values and their selected-file provenance remain in import metadata.

For `acc_wls_reg_1.zip`, the selected runtime profiles contain 220 keys. Re-import classifies 79 as mapped/consumed, 137 as non-applicable, and four as compatibility-only. These are source-key counts, not dashboard row counts: the dashboard expands native settings and groups related source keys. The dashboard now has four **Preserved only** rows. Proxy selection and partial receive-buffer equivalence still require review.

| Source setting | Native behavior |
| --- | --- |
| `Encoding=ANSI`, `RecorderWinCodePage=1252` | Strict Windows-1252 decoding of C/header/configuration/parameter text and referenced parameter data. Binary assets remain unchanged. Unsupported/ambiguous encodings and undefined text bytes fail import instead of introducing replacement characters. |
| `AutoLogBufferSize` | KiB converted to `config.log.error_buffer_bytes`. Error-only logging holds redacted history until a failure; successful history is otherwise discarded. |
| `NetBufSize` | `config.http.receive_buffer_bytes`. Application response-copy buffer, **not** OS socket buffers or HTTP/2 flow-control windows; mapping remains partial. |
| `MaxSelfMetaRefreshCount` | `config.http.max_meta_refresh`. Limits refresh visits to each URL; also enforces an overall 50-navigation ceiling. |
| `MaxErrorMatchesAsERRORS` | `config.http.content_check_error_limit`. Boundary-match failure counts above the cap are informational; failure status remains unchanged. |
| Disabled `ProxyAutoConfigScriptURL` | Non-applicable when an unambiguous selector explicitly disables PAC. |
| `ProxyUseBrowser` | Portable environment proxy selection with a review notice. Windows browser inheritance requires an explicit exported configuration. Conflicting selectors leave proxying disabled pending a decision. |
| `ProxyPasswordIsEncrypted` | Consumed credential metadata. Provision the protected proxy password using Credentials. |
| `WebSocketCallBackTimerIntervalMS` | Non-applicable when inspection proves no WebSocket operation or indirect reference in the native HTTP source. Source callbacks remain review-required until converted to the supported native callback contract. |
| System language / available-screen dimensions | Non-applicable to HTTP replay. Explicit browser-protocol emulation is available below. |
| `ResetContext`, recording timestamps and DST | Recording/code-generation provenance; no iteration resets or recorded-session replay semantics are inferred. |
| `IgnoreContentCachingTypes`, `KeepNonTextMimeType`, `AnalogMode`, `SSLVersionKey` | Remain preserved with review explanations. Native MIME controls are explicit operator-selected alternatives. Numeric SSL selectors and `SSL_VERSION="2&3"` are not interpreted as modern TLS versions. |

## Logging and HTTP contracts

- `config.log.error_buffer_bytes`: default 102400 bytes in error-only mode; maximum 1 MiB per VUser. All buffers share a 16 MiB process reservation budget. Capacity includes record metadata. Oldest entries are evicted; entries larger than capacity or beyond the shared budget are dropped. Failures still emit. Flush clears history, and teardown releases retained capacity. Disabled logging emits nothing. Error-only output applies to ordinary requests, sequence children, lifecycle actions, and diagnostic replay.
- `config.http.receive_buffer_bytes`: zero retains the existing copy strategy; 1–1048576 selects a bounded application read buffer. Existing body-capture limits remain separate. Buffer allocations use the shared HTTP capture budget.
- `config.http.max_meta_refresh`: zero preserves existing behavior (no automatic meta-refresh); 1–50 enables it. Relative URLs, quoted targets and delays are supported. Refresh navigation and resource fetches share the outer page deadline and outbound policy. Cross-origin navigation strips explicit and default authentication headers and action credentials. Timing and bytes include the navigation chain. Authored action assertions/extractions apply to the final page; global content checks and registered verification observe the navigation responses.
- Boundary checks use `config.web_runtime.content_checks[].left_boundary` and `.right_boundary`, with optional `.text` to match inside each pair. Both boundaries are required together. `config.http.content_check_error_limit` is 0 (uncapped count) or 1–1000 per response. Counts are exposed as `content_check_matches` protocol evidence; they never turn a failed check into a passing one. Main-document behavior is supported; existing resource-scope limitations remain.
- `body_encoding: "windows-1252"` encodes text after parameter substitution. Unrepresentable characters fail before sending. Fixed binary bodies use base64. Imported URL-encoded form components retain their wire charset when substituting values. Mixed binary/deferred bodies without a lossless representation require repair.

## PAC proxy mode

Set `config.http.proxy_mode: "pac"` and `config.http.proxy.pac_url` to an HTTP(S) URL without userinfo. Existing none/system/custom modes remain available. PAC retrieval is capped at 256 KiB and five seconds. The evaluator runs in a separate credential-minimized process, with at most four concurrent workers, a two-second parent deadline, a 500 ms evaluation interrupt, 4096 AST nodes and nesting depth 64. Linux also applies a 256 MiB data limit. Each literal and output is bounded to 4096 bytes; DNS helpers are limited to 32 calls.

The supported PAC language profile is intentionally restricted: one `FindProxyForURL` function, variables, `if`/`return`, conditional expressions, boolean/comparison operators, and these helpers: `dnsResolve`, `isPlainHostName`, `dnsDomainIs`, `localHostOrDomainIs`, `dnsDomainLevels`, `isResolvable`, `shExpMatch`, `isInNet`. Loops, recursion, objects, dynamic/property calls, string-building operators and unlisted helpers fail closed. This is not arbitrary JavaScript compatibility.

PAC downloads, DNS helpers, destination resolution and returned proxy addresses are policy checked. Without an explicit target allowlist, PAC-related network resolution requires public addresses. Private enterprise PAC/proxy/destination hosts must be explicitly permitted. DNS answers used for physical dialing are pinned. An ordered list may contain `PROXY`, `HTTPS`, `SOCKS5`, and explicit `DIRECT` entries (maximum 16). Only connection-establishment failures advance to the next route; ambiguous write/read failures are not replayed. No download, evaluation, or exhausted-route failure silently becomes direct access.

Evaluation and route transport setup currently happen per request. Measure PAC overhead for the intended workload; use a reviewed static proxy when that overhead is unsuitable. PAC proxy credentials are not inferred from encrypted source values.

## Browser and WebSocket contracts

Browser settings are `config.browser.system_language`, `available_screen_width`, and `available_screen_height` (dimensions 0–8192; zero means unchanged). An initialization script emulates `navigator.systemLanguage` and `screen.availWidth/availHeight`. It does not change viewport dimensions or reproduce host OS / Internet Explorer behavior.

WebSocket callbacks use `config.websocket.callback_interval_ms` (default 500; authored positive range 1–60000). After `ws_connect`, register `ws_on_message` with 1–64 `control.steps`. For each message the engine sets `{{var.ws_message}}` and `{{var.ws_message_type}}`, then executes the steps serially on that VUser. Binary message bodies use the existing base64 representation.

The reader queues at most 64 messages / 1 MiB of message text per connection. Overflow is an execution error. Dispatch occurs when due at safe protocol-action boundaries; `ws_dispatch` waits until the next dispatch interval when the script is otherwise idle. This is not a concurrent callback thread or a precise real-time timer. `ws_receive` cannot compete with registered callbacks. Close, teardown and reconnect invalidate the queue; register again on the new connection. Arbitrary C callbacks are not executed.

New settings are available in the existing script/import editors and scenario/group overrides. No new REST routes are required. Generated DSL/schema and API/type artifacts describe the native fields and callback actions.

## Verification

Regression coverage includes the sanitized `pkg/convert/enterprisearchive/testdata/native_acc_compat` source structure and settings, source-key accounting, strict decoding/binary preservation, logging suppression/history/eviction/redaction, HTTP/1.1 and HTTP/2 reads, refresh limits/cancellation/cross-origin credentials, content failure caps, PAC ordering/failure/resource controls, browser page-visible properties, WebSocket queue ordering/lifecycle, and override cloning/application. The dashboard workflow edits and saves the new HTTP and PAC controls through the real UI.

Local test servers provide behavior evidence. No application endpoint from the supplied archive is contacted by the import assessment or these tests.

### Validation record (2026-09-23)

Point-in-time record (2026-09-23); the "not green" statements below are historical. `docs/contracts/orchestration-v1/CODE_REVIEW.md` records a full `npm run test:cycle` passing (typecheck, lint, unit tests, build, dev-admin and local-auth browser workflows) on a different change set, so the browser-suite failures listed here were not reproduced there; this document has not been re-run since. The following records the initial implementation checks. See [Review notes](#review-notes) below for review fixes and the latest test outcomes.

- The final `go test ./...` passed. An earlier concurrent run exposed the existing 80 ms generator execution-lease test's timing sensitivity; five isolated repetitions and the final full suite passed. Focused race checks passed for the engine, HTTP/PAC, WebSocket, and browser changes, including the final PAC alias/shadowing regressions.
- Dashboard type checking, lint, all 4,446 unit tests, and the embedded build passed.
- The new HTTP/PAC settings edit/save workflow passed against real controllers in both dev-admin and local-auth modes. Import-review apply/save/source-evidence workflows also passed in both profiles.
- The complete `npm run test:cycle` is **not green**: dev-admin reported 120 passed, four skipped, and ten failed; one of those failures was the new test's counted-tab selector, which was corrected and passed on rerun. The nine remaining failures are in command-center details, authentication-shell navigation, live run controls, workspace navigation/setup, and source-mapping review. Local-auth reported 48 passed, one skipped, and two failed (the workspace journey's obsolete “Current project” selector and the existing source-mapping review selector). These broader workflow failures remain outstanding; this change does not claim a clean full browser suite.

## Review notes

Historical code review (all listed findings were fixed), folded in 2026-09-24 from the former `docs/reviews/NATIVE_ARCHIVE_COMPATIBILITY_REVIEW.md`. Reviewed the compatibility implementation against the saved pre-implementation working-tree baseline. Existing unrelated edits were retained. Review covered HTTP refresh/read/content-check paths, PAC isolation and network handling, source decoding, logging, callback lifecycle, browser configuration, and configuration/override plumbing.

### Findings fixed

| Priority | Finding | Fix and regression evidence |
| --- | --- | --- |
| P1 | An HTTP redirect followed by a same-origin meta-refresh on the redirected page could reattach credentials from the original request. | Refresh credential forwarding now compares the destination with the original request origin, rather than the redirected response origin, while treating default ports as equivalent. Local redirect/refresh server test verifies credentials are absent. |
| P1 | Same-origin refresh discarded caller-supplied header redactions, allowing custom secret headers into protocol failure snapshots. | Preserve redacted header maps/lists when forwarding headers; clear both actual and redacted credentials together on origin changes. Failure-snapshot regression checks the custom header. |
| P2 | Refresh GETs inherited entity headers from default header layers, and latency assertions measured only the final response. | Clear entity headers from all header layers; evaluate latency against the complete navigation chain. Tests cover default content type and a delayed refresh that must fail its latency assertion. |
| P2 | A rejected duplicate WebSocket callback registration replaced the active callback body during action resolution. | Commit the body only after successful registration, in load, lifecycle, and diagnostic replay execution. The lifecycle test rejects replacement, then verifies original callbacks still execute. |
| P2 | Peer close discarded messages already queued for callbacks. | Deliver the queued batch before surfacing the terminal read error on the next dispatch. A real server sends a final message and immediately closes. Queue overflow remains fatal before dispatch, with a real 65-message regression preventing reconnect from erasing the error. |
| P2 | Failed explicit reconnect left the old callback reader blocked; close/teardown did not wake an interval wait. | Close the previous connection before dialing its replacement, and wake dispatch waits through callback cancellation. Tests cover failed handshake cleanup and teardown. |
| P2 | Callback queue/reader errors could stop execution without a failed sample. | Record dispatch failures through the normal metrics/evidence path before returning the error. The real WebSocket lifecycle test checks the aggregate error count after peer close. |
| P2 | ANSI parameter filenames were resolved before decoding their metadata, so referenced data with non-ASCII filenames could remain undecoded. | Decode metadata first, resolve referenced data afterward, and decode each file once. Regression includes a Windows-1252 filename, repeated references, and an unchanged binary asset. |
| P2 | PAC rejection before transport handoff leaked the request body; `isResolvable` aborted on NXDOMAIN instead of returning false. | Close bodies on early rejection. Treat DNS name-not-found as the helper's empty result while keeping policy violations and other failures fatal. Tests use a tracked body and a local DNS responder; no external DNS is required. |
| P2 | PAC DNS pinning dialed only the first address, losing IPv4/IPv6 and multi-address fallback. | Try the validated, pinned addresses under the existing deadline before rejecting a route; an unresolvable proxy can advance to the next explicit PAC route. Tests cover fallback and cancellation without external network access. |

### Review validation

- Focused HTTP/PAC, WebSocket, engine lifecycle, and archive-conversion regressions passed with the race detector. Final callback overflow/lifecycle checks also passed three repeated race runs.
- Two full Go runs passed during review. The final `go test -p 2 ./...` run reported one failure: `TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion` exceeded its eight-second completion deadline. Three subsequent isolated repetitions passed. All other packages passed in that final run; the final full-suite result is therefore not reported as green.
- Dashboard type checking, lint, all 4,446 unit tests, and the embedded build passed. The HTTP/PAC editor save workflow and import-review apply/save workflows passed in both authentication profiles.
- The full `npm run test:cycle` remains red: dev-admin had 121 passed, four skipped, and nine failed; local-auth had 48 passed, one skipped, and two failed. These repeat the broader workflow failures recorded before review: command-center details, authentication/workspace navigation, live run controls, authoring scope, guided setup, and source-mapping review. They remain outstanding outside this compatibility review.
- Re-import retained all 220 source keys: 79 mapped/consumed, 137 non-applicable, and four compatibility-only. No source application endpoints were contacted.
- `git diff --check` passed. The generated QA dashboard was refreshed using its repository generator.

### Second review after fixes (2026-09-23)

Re-read the final refresh credential/header forwarding and timing paths, callback registration/queue/reconnect/error reporting, PAC policy/body ownership/DNS fallback and evaluator restrictions, source-decoding order, error-log retention, and browser emulation. No new actionable findings were identified in this focused review; no production changes were needed.

Repeated the focused HTTP/PAC, WebSocket, engine, and archive-converter regression tests with `-race -count=1`; all four packages passed. `git diff --check` also passed. The full-suite and browser-cycle limitations above remain unchanged; those suites were not rerun for this documentation-only follow-up.
