# HTTP/3 Implementation Plan and Decision Records

Status: Waves 0–3 complete; Wave 4 (recording and import mapping) in progress: WP4.1 done. Both HTTP/3 policies run as a preview where `APEXLOAD_HTTP3=true`: exact HTTP/3 (`3`, or the `http/3` protocol alias) and the `auto+h3` Alt-Svc upgrade.

This file is the in-repo companion to the HTTP/3 delivery specification (24 work packages in 7 waves). It records the decisions the code depends on, the measurements behind them, and what each wave has delivered. Code references are to `main`.

## Delivery status

| Wave | Scope | Status |
| --- | --- | --- |
| 0 | Fixture, quic-go contract tests, Alt-Svc observation, browser protocol evidence and QUIC setting, cURL TCP versions, DevWeb disclosure | Complete (see deviations) |
| 1 | Version vocabulary and resolver, `config.http.quic`, `APEXLOAD_HTTP3` flag and `native_http3_v1` capability | Complete (see deviations) |
| 2 | Exact HTTP/3 (`3`) preview | Complete (see deviations) |
| 3 | `auto+h3` upgrade pilot | Complete (see deviations) |
| 4 | Recording and import mapping | WP4.1 complete |
| 5 | Evidence, reports, UI, docs | Not started |
| 6 | Capacity, deployment and fault qualification | Not started |

## Test infrastructure (WP0.1)

`pkg/internal/h3fixture` serves one handler over TCP (HTTP/1.1, HTTP/2) and QUIC (HTTP/3) on the same loopback port, with an optional alternate QUIC port and a throwaway test CA. Switches cover Alt-Svc advertisements, rejected and blackholed UDP, per-packet handshake delay, `H3_REQUEST_REJECTED` and `H3_VERSION_FALLBACK` refusals issued before any response header, partial responses after a side effect, redirects, server stream limits and connection drops. Its `Ledger` records every application attempt the target saw and counts side effects, so tests can prove a write ran exactly once.

`pkg/internal/h3fixture/quicgo_contract_test.go` pins the quic-go behaviors below. A quic-go upgrade that changes one of them fails there first.

## Decision records

### D1. Timing comes from quic-go trace hooks plus APEX's own dial

Verified against quic-go v0.63.0 source and the contract tests:

| Hook | Default dialer | Custom `Dial` |
| --- | --- | --- |
| `GetConn` | Once per attempt | Once per attempt |
| `GotConn` | Yes; `Reused` true only when the handshake had already completed | Same |
| `DNSStart` / `DNSDone` | Yes, through Go's resolver, when a name is resolved | No |
| `ConnectStart` / `ConnectDone`, `TLSHandshakeStart` / `Done` | Yes | No |
| `WroteHeaders`, `WroteRequest` | Yes | Yes |
| `GotFirstResponseByte` | Yes, on the first byte of the response stream | Yes |

Decision:

- APEX needs a custom `Dial` (resolve once, endpoint authorization, source-IP binding, socket ownership), so it times DNS and the QUIC handshake inside that dial.
- `ConnectTime` is the handshake, including TLS; `TLSTime` is 0 with `tls_in_transport_handshake: true`, so `finalizeHTTPServerTelemetry` never subtracts the handshake twice.
- `TTFB` keeps its existing boundary on QUIC: from the action start to `GotFirstResponseByte`. This corrects the delivery specification, which assumed quic-go emitted no first-byte event and planned a "headers available" boundary.
- Connection reuse is classified by APEX, not by `GotConn.Reused`. A request that waits on another request's in-progress dial reports `Reused=false` without dialing, so the receipt distinguishes `new`, `reused` and `shared_pending`.

### D2. Retries are counted, and dials are detached from the first request

- quic-go retries a request once, after dropping the connection, when the stream could not be opened (the request was not sent) or the server answered `H3_REQUEST_REJECTED`. A body is replayed only when it is absent or `GetBody` rebuilds it; otherwise quic-go returns a "cannot retry" error. These retries are RFC 9114-sanctioned and stay enabled.
- The retry is invisible except that `GetConn` fires once per attempt. APEX counts attempts from `GetConn` and charges them to the per-action attempt budget.
- quic-go runs a new connection's dial on a context derived from the first request that needed it. If that request is cancelled, every request waiting on the same dial fails. APEX's dial therefore detaches from request cancellation (`context.WithoutCancel`) and is bounded instead by the connect timeout and the VUser's lifetime context.

### D3. One UDP socket per VUser

Client-only cost per VUser, one QUIC connection each, measured with `TestSpikeSocketOwnership` (`APEXLOAD_H3_SPIKE=1`), fixture server in a child process, macOS arm64, Go 1.26.5, quic-go v0.63.0:

| Model | VUsers | Goroutines per VUser | Heap in use per VUser | File descriptors |
| --- | --- | --- | --- | --- |
| Socket per VUser | 100 | 6.0 | 58.0–63.1 KiB | +100 |
| Socket per VUser | 1,000 | 6.0 | 57.8–61.4 KiB | +1,000 |
| Shared socket | 100 | 4.0 | 42.2–46.6 KiB | +1 |
| Shared socket | 1,000 | 4.0 | 47.0–51.2 KiB | +1 |

A shared socket saves about 2 goroutines, 10–15 KiB and one descriptor per VUser. It is still rejected for core, for fidelity:

- Real clients each use their own UDP source port. Layer-4 load balancers and ECMP paths that hash the 5-tuple would send every VUser behind a shared socket to the same backend, which a load test must not do.
- Per-VUser sockets keep source-IP binding, NAT behavior and future connection migration per VUser.

Decision: each VUser lazily creates one `quic.Transport` and UDP socket per local address and IP family, and closes them at VUser teardown. Admission counts one extra file descriptor per QUIC-capable VUser. One of 2,000 per-socket handshakes in the 1,000-VUser runs timed out on macOS loopback; the cause was not isolated, and Linux measurement belongs to WP6.1.

### D4. Initial QUIC defaults

| Setting | Default | Reason |
| --- | --- | --- |
| `handshake_timeout_ms` | 5000 | quic-go's handshake idle timeout; `connect_timeout_ms` still caps DNS plus handshake |
| `max_idle_timeout_ms` | 30000 | quic-go default; the server may negotiate lower |
| `keep_alive_ms` | 0 | No PINGs unless asked; matches TCP keep-alive being transport-managed |
| `alt_svc_cache` | `per_vuser` | A VUser behaves like one long-lived browser profile |
| `broken_ttl_ms` | 300000 | Five minutes, Chromium's initial broken-alternative period; no exponential backoff in core |

0-RTT stays off: the core client uses quic-go's `Dial`, which completes the handshake before returning, never `DialEarly` with early data.

## Corrections to the delivery specification

- Timing: quic-go emits `GotFirstResponseByte` and `WroteHeaders` (D1). The specification's QUIC `ttfb_boundary: headers` is unnecessary; `first_byte` applies to both transports.
- Dial cancellation coupling (D2) is new; the transport work package must implement the detached dial.

## QUIC settings and operator controls (WP1.2)

Scripts tune QUIC with `config.http.quic`; every field is optional, and the defaults are D4. Values outside the ranges, and any other key in the block, are validation errors naming the field, at the script, scenario and group layers.

| Field | Range | Default |
| --- | --- | --- |
| `handshake_timeout_ms` | 1000–60000 | 5000 |
| `max_idle_timeout_ms` | 1000–600000 | 30000 |
| `keep_alive_ms` | 0 to the max idle timeout | 0 (no PINGs) |
| `alt_svc_cache` | `per_vuser`, `per_iteration` | `per_vuser` |
| `broken_ttl_ms` | 0–600000 | 300000 |

Receive windows, the response header limit and packet sizing size memory per connection, so they are installation settings on the controller and generators. They are defined and validated now; the transport reads them at startup from WP2.1, and an invalid value then fails startup naming its variable.

| Variable | Range | Default |
| --- | --- | --- |
| `APEXLOAD_QUIC_INITIAL_STREAM_RECEIVE_WINDOW` | 16 KiB–1 GiB, at most the stream maximum | 512 KiB |
| `APEXLOAD_QUIC_MAX_STREAM_RECEIVE_WINDOW` | 16 KiB–1 GiB | 6 MiB |
| `APEXLOAD_QUIC_INITIAL_CONNECTION_RECEIVE_WINDOW` | 16 KiB–1 GiB, at most the connection maximum | 768 KiB |
| `APEXLOAD_QUIC_MAX_CONNECTION_RECEIVE_WINDOW` | 16 KiB–1 GiB | 15 MiB |
| `APEXLOAD_QUIC_MAX_RESPONSE_HEADER_BYTES` | 4 KiB–10 MiB | 64 KiB, the TCP transport's limit |
| `APEXLOAD_QUIC_INITIAL_PACKET_SIZE` | 1200–1452 | 1280 |
| `APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY` | `true`, `false` | `false` |

The window and packet defaults are quic-go v0.63.0's. Stream budgets stay governed by `max_conns_per_host` and the server's stream limit.

## Wave 0 delivery notes

| WP | Delivered | Deviation from the specification |
| --- | --- | --- |
| WP0.1 | `pkg/internal/h3fixture`, quic-go contract tests, decisions D1–D4 | None |
| WP0.2 | `alt_svc_h3_advertised` replay evidence; RFC 7838 parser; `origin_advertises_h3` for HAR imports and both recorders, shown as Recording Studio notes | The proxy recorder finding comes from the shared HAR pipeline rather than a recorder-specific capture |
| WP0.3 | Browser resource evidence version 4 with `protocol` and `protocol_unavailable_reason`; dashboard admission, request details and resource table | Protocol rides the existing document-evidence association, so it is recorded only with `config.browser.capture_document_metrics: true` (an admission-gated opt-in with no dashboard control yet); default runs keep version 2 evidence rather than adding a CDP session to every Chromium page. Chromium never acts on `Alt-Svc` from a certificate outside the public roots, so loopback tests force QUIC with `--origin-to-force-quic-on` through a test-only launch-args hook; TCP-to-HTTP/3 discovery was verified manually against public CDNs |
| WP0.4 | `config.browser.quic` (`auto`, `off`) for VUsers, Runtime Settings and group overrides; browser recorder `quic_mode` with a Recording Studio option | Per-request protocol badges in the recorder are deferred: the recorder has no live request list. "Advertised but not observed" is reported in the conversion notes |
| WP0.5 | cURL `--http1.1`, `--http2`, `--http2-prior-knowledge` mapped (prior knowledge is exact HTTP/2 only on `http://`; on HTTPS curl still negotiates, so it maps to automatic); `--http1.0`, `-0`, `--http3`, `--http3-only` and cleartext `--http2` keep specific gates | Postman is not mapped: no real exported collection has yet shown that its HTTP version setting is serialized |
| WP0.6 | `devweb_http3_enabled` disclosure; `PROTOCOL_SUPPORT.md` HTTP version rules corrected | The DevWeb key is detected by name pattern under the transport sections until a real DevWeb 24.3+ `rts.yml` confirms it |

## Wave 1 delivery notes

| WP | Delivered | Deviation from the specification |
| --- | --- | --- |
| WP1.1 | `auto+h3` and `3` at every level (script, request, `http_sequence` children, runtime-settings overrides) in the DSL, JSON schema, OpenAPI and dashboard types; `NormalizeVersion` maps `HTTP/3`, `HTTP/3.0` and `h3` to `3` while draft tokens stay unsupported; one precedence (request, group, scenario, script, alias) shared with the existing policies; save-time rejection of exact `3` with an `http://` URL, a TLS maximum below 1.3, a custom proxy or integrated auth, and of either policy with network shaping; HTTP/1.1-only `Expect` rule skipped for exact `3`; the runtime refuses any request that resolves to an HTTP/3 policy before sending, while overrides to other policies still run | The `http/3` protocol alias waits until every dispatch path recognizes it (Wave 2). The post-resolution URL check lands with the transport (WP2.1), because this runtime refuses all HTTP/3 requests before resolution matters. The two policies are already offered in the existing version selects so a stored policy displays correctly; the QUIC panel and capability-aware messages remain WP5.2 |
| WP1.2 | `config.http.quic` with the five fields in the DSL, JSON schema, OpenAPI and dashboard types; range validation shared by save and session start; unknown keys kept through JSON clones so group and scenario overrides report them too; the block layers with the `http` section, which overrides replace whole; session parsing into `QUICSettings`; the seven operator variables with bounds and defaults | The operator limits are parsed and validated but consumed only once the transport exists (WP2.1), which is also where startup reads them. Authoring controls for the five fields are WP5.2 |
| WP1.3 | `APEXLOAD_HTTP3` (off by default) and `native_http3_v1`. A script uses HTTP/3 when any request resolves to `auto+h3` or `3` after precedence. Generators advertise the capability only when the flag is on and the build carries the transport; placement requires it, so start, scheduled launch, scale-out, replacement and HA recovery (which reuses the requirement persisted at start) reject HTTP/3 groups on generators without it, naming each generator and, for a missing capability, the flag. The engine's execution gate refuses HTTP/3 scripts for local, embedded, CLI, replay and shakeout runs and inside each generator. Disabling the flag blocks new work and never rewrites stored policies | No build carries the transport yet, so even with the flag on every HTTP/3 run is refused with "this build has no HTTP/3 transport yet"; Wave 2 flips that. Mixed pools follow the existing per-generator feature rule: generators without the capability are excluded and named, rather than a separate pool-level rejection |

## Wave 2 delivery notes

| WP | Delivered | Deviation from the specification |
| --- | --- | --- |
| WP2.1 | `internal/h3transport`, the only importer of quic-go outside tests: one UDP socket per VUser and IP family opened on first use (D3), a dial detached from the triggering request and bounded by the connect timeout and the VUser lifetime (D2), DNS and handshake reported through the request trace with no separate TLS phase (D1), full handshakes only (D4), per-VUser TLS session and address-token caches, the target allow-list checked again at dial, source-IP binding and IP-family filtering, and `H3_NO_ERROR` on close. Failures carry `not_sent`, `possibly_sent` or `rejected_unprocessed` (quic-go's single `H3_REQUEST_REJECTED` retry stays on). The session gains an exact-3 pool that shares cookies, the policy-partitioned cache, redirects, target policy, run correlation and user-agent handling with the TCP pools; OAuth token calls stay on TCP. Every redirect hop runs on HTTP/3; a TCP-only hop fails `not_sent`. A proxied route (custom, system or a PAC proxy as the first route) fails `http3_proxy_unsupported`; integrated authentication, an `http://` URL after resolution and network shaping are refused before sending. Results record `http_version_negotiated: "3"` and `http3_failure_certainty`; an HTTP/3 response never claims the Alt-Svc "not exercised" note. Teardown closes the transport and a reused session recreates it. The controller and generators validate `APEXLOAD_HTTP3` and `APEXLOAD_QUIC_*` at startup | `auto+h3` needs its own capability, `http3_upgrade_v1`, advertised only by builds with the router (Wave 3), so a rolling upgrade never sends `auto+h3` work to an exact-only generator. The selected IP is not checked against an IP-level outbound policy because the TCP transport checks hosts only; parity keeps both the same. A second, independent HTTP/3 server stack moves to WP6.3. Importers keep treating captured HTTP/3 as an observation until Wave 4 |
| WP2.2 | HTTP/3 receipts (evidence version 2 fields) on exact-3 actions: `http_version_selection_reason` (`exact_policy`, `proxy_route`, `integrated_auth`), `quic_attempted`, `attempt_count` from `GetConn` (quic-go's retry included), `quic_connection` (`new`, `reused`, `shared_pending`, or `dial_failed` when the handshake never completed), `tls_resumed`, `tls_in_transport_handshake`, `ttfb_boundary: first_byte`, `quic_error_class` and per-interval `timing_availability` with a reason instead of a zero. ConnectTime is the QUIC handshake, TLSTime stays 0, and server processing subtracts the handshake once. The dial races address families (300 ms stagger, interleaved families), so an address family that resolves but does not route costs a fraction of a second. Replay evidence shows the receipt rows and labels a negotiated HTTP/3 | Receipt fields are emitted for exact-3 actions only; TCP receipts are unchanged, so `http_version_source: unknown` waits for the Wave 3 router. `upgraded`, `not_advertised`, `broken_cache`, `tls_policy` and `http_version_fallback` belong to `auto+h3` (Wave 3) |
| WP2.3 | QUIC connections take the same per-host slots as TCP sockets in the VUser's budget (`max_conns_per_host`, `max_idle_conns`): a dial waits for a slot, evicting idle connections of any version, and the slot is released when the connection closes; idle trimming closes least-recently-used TCP sockets first and QUIC connections only while still over the limit. With `max_conns_per_host: 1`, concurrent requests multiplex on one QUIC connection. Idle closing never ends a connection while a response body is open, because quic-go counts a connection as unused once headers arrive (SSE included). A queued request still honors its own cancellation. PAC DIRECT routes keep HTTP/3 and, without a target policy, reach public addresses only, as on TCP; a PAC proxy as the first route fails `proxy_route`. One exact-3 action is allowed the hops its redirects permit plus one library retry (`attempt budget exhausted` beyond that) | A dial that waits for a slot keeps waiting, bounded by the connect timeout, after the request that started it is cancelled (D2 keeps dials detached); the cancelled request itself returns at once. Engine-level retries (`error_handling.max_retries`) are separate logical actions and keep their explicit setting |
| WP2.4 | The `http/3` protocol alias (an inherited default of `3`, overridden by an explicit config as for `http/1.1` and `http/2`) is recognized wherever the other aliases are: runtime registration, DSL family and precedence, engine dispatch, test creation, monitoring correlation, capacity planning, importer family checks and the dashboard. Exact `3` is tested end to end for `http`, `soap` (envelope POST) and `java_http`, SSE streams, run correlation to approved origins and generated trace context. The web API support matrix now lists exact HTTP/3 as a preview and the `auto+h3` upgrade as the remaining gap | The protocols' full replay-compatibility suites are not re-run with `3`; each protocol has a focused HTTP/3 test against the fixture instead |

## Wave 3 delivery notes

| WP | Delivered | Deviation from the specification |
| --- | --- | --- |
| WP3.1 | The `auto+h3` router. Each VUser keeps an Alt-Svc cache, at most 256 origins, of the same-host final `h3` alternative each https origin advertised, on any port. Each valid Alt-Svc field replaces the origin's entry (RFC 7838), so `clear`, `ma=0` and a field without a same-host `h3` alternative remove it, as does expiry; `alt_svc_cache: per_iteration` forgets alternatives at each iteration boundary. A request runs on TCP (the automatic HTTP/2-or-HTTP/1.1 pool) until its origin has a live alternative, then on QUIC to that port with `Alt-Used`; concurrent upgraded requests share one dial, and a connection to an alternative port takes the origin's slot in the connection budget. Responses on either transport refresh the cache. Selection reasons, decided before any attempt: `not_advertised` (cleartext origins included), `integrated_auth`, `tls_policy` (with a TLS maximum below 1.3 every request stays on TCP), `proxy_route` (a custom, system or PAC proxy route), `broken_cache` and `upgraded`; a PAC or target-policy failure fails before sending, as on TCP. A 421 from the alternative drops it, keeps the origin on TCP for `broken_ttl_ms` and runs the request once on TCP. Receipts on `auto+h3` actions carry the routing fields; QUIC connection facts come only from QUIC hops, and `timing_availability` and `tls_in_transport_handshake` appear only when every hop of the action ran on QUIC. Generators with the flag now advertise `http3_upgrade_v1`. Replay evidence labels each routing reason as a TCP choice rather than a refusal, says when a TCP response's advertisement will upgrade later requests, and drops the "HTTP/3 was not exercised" note on a hop that was routed to the alternative | Cross-host alternatives are ignored, as planned. A 421 is reported as `http_version_fallback: misdirected_request`, a value the specification's vocabulary lacked. Concurrent first requests to an origin all use TCP; none waits for another's discovery. With a PAC, the script is evaluated by the router and again by the TCP route. `http_version_source: unknown` is still not produced, because every routed response reports its wire version |
| WP3.2 | Conservative fallback. A QUIC failure before the request was sent (`not_sent`: the handshake failed or timed out, or the connection could not take the stream) marks the origin broken for `broken_ttl_ms` and runs the request once on TCP (`http_version_fallback: quic_connect_failed`), so the next request reports `broken_cache`; a dial that only waited out the VUser's own connection budget (`waiting for a connection slot`) falls back without marking the origin, and on either policy carries no `quic_error_class`, because no QUIC packet was sent. A successful fallback keeps the `quic_error_class` that caused it. `H3_VERSION_FALLBACK` reruns the request on HTTP/1.1 (`h3_version_fallback`) when it was not sent, or when the method is idempotent and its body can be rebuilt (RFC 9110 §9.2.2). `possibly_sent` and `rejected_unprocessed` (after quic-go's single retry) fail without fallback and name the certainty (`auto+h3 HTTP/3 attempt failed: possibly_sent: …`); a request the caller cancelled never falls back, and a body that cannot be rebuilt is never sent twice (the QUIC failure, or the 421, is returned instead). The selection reason stays the route chosen before the attempt, and the fallback is reported separately. `attempt_count` counts every HTTP attempt, TCP included; the attempt budget (the redirects plus one library retry) counts QUIC attempts only | The specification sized the budget as the exact-3 limit plus one for the fallback. Charging TCP attempts would let `auto+h3` fail where `auto` succeeds (net/http retries idle-connection failures itself), so TCP attempts are not budgeted and the fallback is bounded by the router instead: once per hop. `quic_error_class` gains `version_fallback` for an `H3_VERSION_FALLBACK` refusal, on both policies. A connection dropped after response headers arrived is a failed response read with no certainty: the request was sent, and the router had already returned it |

## Wave 4 delivery notes

| WP | Delivered | Deviation from the specification |
| --- | --- | --- |
| WP4.1 | `http_version_mode` (`upgrade` default, `exact`, `inherit`) on `sample.ConvertOptions`, the HAR converter, HAR import (multipart field), sample import, both recorders' conversion requests and browser-to-HTTP conversion; unknown modes are a 400. Captured final HTTP/3 (`h3`, `HTTP/3`, `HTTP/3.0`) is an observation: it maps to `auto+h3`, `3` or no policy by mode, while HTTP/2 and HTTP/1.1 stay exact except under `inherit`; the observation stays in `http_version_capture` with the mode that mapped it. Findings per origin: `captured_h3` (info) and `h3_not_preserved` (warning, `inherit`); draft tokens keep the raw token, replay on the inherited policy and gate promotion (`http3_draft_token`). Browser captures carry version 2 provenance (`{"version":2,"source":"browser"|"browser_run","observed":"h1"|"h2"|"h3"|"unknown"}`), written by the browser recorder at stop and by the browser VUser run HAR after scrubbing; import checks it against the entry's own version and otherwise only describes it. Malformed observations, proxy-leg rules and LoadRunner `unknown` HARs are unchanged. Dashboard: an "HTTP version in generated script" option in both HAR import dialogs and the browser recorder's HTTP-script options, and captured HTTP/3, draft and browser labels plus the import mode in the request editor. Fixtures: a scrubbed real Playwright/Chromium capture, Chrome DevTools, Firefox, draft-token and LoadRunner HARs with per-mode goldens | The browser recorder's option sits with its HTTP-script conversion options rather than the start dialog, because it applies when the HTTP script is generated and can change after recording. The proxy recorder accepts the option through its API but has no control: its client leg never carries h3. Provenance that contradicts its entry reuses `malformed_http_version_provenance`. When every request maps to the same policy, the script-wide `config.http.version` is promoted to it, `auto+h3` included, as for exact versions. `VersionExecutable` now accepts the HTTP/3 policies for import fidelity; the runtime-capabilities version list stays TCP-only until WP5.2. Page resources attached in step (`resource_mode: in_step`) keep inheriting, as before. The frozen HAR import contract records the delta `har_http_version_mode` |
