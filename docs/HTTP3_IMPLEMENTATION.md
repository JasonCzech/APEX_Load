# HTTP/3 Implementation Plan and Decision Records

Status: Wave 0 complete; Wave 1 in progress. Native HTTP/3 execution is **not** available yet; the web support matrix still lists HTTP/3 as a gap.

This file is the in-repo companion to the HTTP/3 delivery specification (24 work packages in 7 waves). It records the decisions the code depends on, the measurements behind them, and what each wave has delivered. Code references are to `main`.

## Delivery status

| Wave | Scope | Status |
| --- | --- | --- |
| 0 | Fixture, quic-go contract tests, Alt-Svc observation, browser protocol evidence and QUIC setting, cURL TCP versions, DevWeb disclosure | Complete (see deviations) |
| 1 | Version vocabulary and resolver, `config.http.quic`, `APEXLOAD_HTTP3` flag and `native_http3_v1` capability | In progress (WP1.1, WP1.2 done) |
| 2 | Exact HTTP/3 (`3`) preview | Not started |
| 3 | `auto+h3` upgrade pilot | Not started |
| 4 | Recording and import mapping | Not started |
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
