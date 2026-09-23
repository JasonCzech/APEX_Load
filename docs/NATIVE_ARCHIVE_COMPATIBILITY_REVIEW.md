# Native compatibility code review

Reviewed the compatibility implementation against the saved pre-implementation working-tree baseline. Existing unrelated edits were retained. Review covered HTTP refresh/read/content-check paths, PAC isolation and network handling, source decoding, logging, callback lifecycle, browser configuration, and configuration/override plumbing.

## Findings fixed

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

## Validation

- Focused HTTP/PAC, WebSocket, engine lifecycle, and archive-conversion regressions passed with the race detector. Final callback overflow/lifecycle checks also passed three repeated race runs.
- Two full Go runs passed during review. The final `go test -p 2 ./...` run reported one failure: `TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion` exceeded its eight-second completion deadline. Three subsequent isolated repetitions passed. All other packages passed in that final run; the final full-suite result is therefore not reported as green.
- Dashboard type checking, lint, all 4,446 unit tests, and the embedded build passed. The HTTP/PAC editor save workflow and import-review apply/save workflows passed in both authentication profiles.
- The full `npm run test:cycle` remains red: dev-admin had 121 passed, four skipped, and nine failed; local-auth had 48 passed, one skipped, and two failed. These repeat the broader workflow failures recorded before review: command-center details, authentication/workspace navigation, live run controls, authoring scope, guided setup, and source-mapping review. They remain outstanding outside this compatibility review.
- Re-import retained all 220 source keys: 79 mapped/consumed, 137 non-applicable, and four compatibility-only. No source application endpoints were contacted.
- `git diff --check` passed. The generated QA dashboard was refreshed using its repository generator.

## Second review after fixes (2026-09-23)

Re-read the final refresh credential/header forwarding and timing paths, callback registration/queue/reconnect/error reporting, PAC policy/body ownership/DNS fallback and evaluator restrictions, source-decoding order, error-log retention, and browser emulation. No new actionable findings were identified in this focused review; no production changes were needed.

Repeated the focused HTTP/PAC, WebSocket, engine, and archive-converter regression tests with `-race -count=1`; all four packages passed. `git diff --check` also passed. The full-suite and browser-cycle limitations above remain unchanged; those suites were not rerun for this documentation-only follow-up.
