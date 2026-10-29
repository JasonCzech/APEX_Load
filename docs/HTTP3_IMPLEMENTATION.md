# HTTP/3 Implementation Architecture and Decision Records

Status: Waves 0–5 complete; Wave 6 code, Helm/Kustomize deployment controls, scenario preflight, and host UDP diagnostics are delivered, with hardware-dependent Linux soak measurements and multi-stack chaos qualification remaining environment-gated. Both HTTP/3 policies execute where `APEXLOAD_HTTP3=true`: exact HTTP/3 (`3`, or the `http/3` protocol alias) and `auto+h3` `Alt-Svc` upgrade.

For all user, authoring, and operator configuration (`config.http.quic`, `APEXLOAD_HTTP3`, `APEXLOAD_HTTP3_PREFLIGHT`, `APEXLOAD_QUIC_*`, Helm `http3.*` values, and UDP egress/buffer tuning), see [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md). This document is the engineering architecture and decision record (D1–D4) that the runtime implementation and contract tests pin.

## Delivery status

| Wave | Scope | Status |
| --- | --- | --- |
| 0 | Dual-stack fixture (`pkg/internal/h3fixture`), `quic-go` contract tests, `Alt-Svc` observation, browser QUIC setting, cURL/DevWeb disclosures | Complete |
| 1 | Version vocabulary and precedence resolver, `config.http.quic` validation, `APEXLOAD_HTTP3` gate and `native_http3_v1` capability | Complete |
| 2 | `internal/h3transport` exact HTTP/3 (`3` and `http/3` alias), connection budget integration, HTTP/3 timing receipts | Complete |
| 3 | `auto+h3` `Alt-Svc` upgrade router (`http3_upgrade_v1`) and conservative pre-send / `H3_VERSION_FALLBACK` fallback | Complete |
| 4 | Recording and import `http_version_mode` mapping (`upgrade`, `exact`, `inherit`), cURL `--http3*` gates, import-review Transport panel | Complete |
| 5 | Archive/live version-mix reporting, Protocol Mix findings, QUIC authoring panel, `apexctl` HTTP/3 reporting, [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md) | Complete |
| 6 | `pkg/hostnet` FD/UDP buffer & drop evidence, `APEXLOAD_HTTP3_PREFLIGHT` (`http3_preflight_v1`), Helm `http3.*` values and UDP 443 egress profiles | Delivered in code; Linux 8-hour soak and chaos/interoperability lanes await dedicated hosts |

## Test infrastructure

`pkg/internal/h3fixture` serves one handler over TCP (HTTP/1.1, HTTP/2) and QUIC (HTTP/3) on the same loopback port, with an optional alternate QUIC port and a ephemeral test CA. Fault switches cover `Alt-Svc` advertisements, rejected and blackholed UDP, per-packet handshake delay, `H3_REQUEST_REJECTED` and `H3_VERSION_FALLBACK` refusals issued before response headers, partial responses after a side effect, redirects, server stream limits, and connection drops. Its `Ledger` records every application attempt and counts side effects so tests prove non-idempotent writes execute at most once.

`pkg/internal/h3fixture/quicgo_contract_test.go` pins the `quic-go` trace and retry behaviors recorded in D1 and D2 below.

## Decision records

### D1. Timing comes from `quic-go` trace hooks plus APEX's custom dial

Verified against `quic-go` v0.63.0 and `quicgo_contract_test.go`:

| Hook | Default dialer | Custom `Dial` |
| --- | --- | --- |
| `GetConn` | Once per attempt | Once per attempt |
| `GotConn` | Yes; `Reused` true only when the handshake had already completed | Same |
| `DNSStart` / `DNSDone` | Yes, through Go's resolver, when a name is resolved | No |
| `ConnectStart` / `ConnectDone`, `TLSHandshakeStart` / `Done` | Yes | No |
| `WroteHeaders`, `WroteRequest` | Yes | Yes |
| `GotFirstResponseByte` | Yes, on the first byte of the response stream | Yes |

Architectural rules:

- APEX uses a custom `Dial` to resolve once, enforce outbound target policy (`netpolicy`), bind `config.web_runtime.source_ips`, race address families (300 ms stagger), and own per-VUser UDP sockets. DNS and the QUIC handshake are timed directly inside that dial.
- `ConnectTime` records the QUIC+TLS 1.3 handshake (`TLSTime` is `0` with `tls_in_transport_handshake: true`) so `finalizeHTTPServerTelemetry` never subtracts the handshake twice.
- `TTFB` retains `ttfb_boundary: first_byte` from action start to `GotFirstResponseByte`.
- Connection reuse is classified by APEX (`new`, `reused`, `shared_pending`, or `dial_failed`) rather than `GotConn.Reused`, because concurrent requests waiting on an in-flight dial observe `Reused=false` without initiating a second dial.

### D2. RFC 9114 retries are budgeted, and dials detach from triggering-request cancellation

- `quic-go` retries a request once after dropping a connection when a stream could not be opened (`not_sent`) or the server answered `H3_REQUEST_REJECTED` (`rejected_unprocessed`), replaying a body only when absent or rebuildable via `GetBody`.
- Each attempt fires `GetConn` once and is charged against the per-action QUIC attempt budget (configured redirect hops plus one library retry).
- Because `quic-go` otherwise binds a new connection dial to the first request's context, cancelling that request would fail sibling requests sharing the pending dial. APEX detaches the dial via `context.WithoutCancel`, bounding it by the connect timeout and the VUser's lifetime context.

### D3. One UDP socket per VUser (and local IP / address family)

Measured with `TestSpikeSocketOwnership` (`APEXLOAD_H3_SPIKE=1`, macOS arm64, Go 1.26.5, `quic-go` v0.63.0):

| Model | VUsers | Goroutines per VUser | Heap in use per VUser | File descriptors |
| --- | --- | --- | --- | --- |
| Socket per VUser | 100 | 6.0 | 58.0–63.1 KiB | +100 |
| Socket per VUser | 1,000 | 6.0 | 57.8–61.4 KiB | +1,000 |
| Shared socket | 100 | 4.0 | 42.2–46.6 KiB | +1 |
| Shared socket | 1,000 | 4.0 | 47.0–51.2 KiB | +1 |

Although sharing one UDP socket saves ~2 goroutines, 10–15 KiB, and 1 file descriptor per VUser, core HTTP/3 uses **one UDP socket per VUser** (lazily opened per local source IP and IP family, closed on VUser teardown, verified by `pkg/protocol/http/http3_socket_ownership_test.go`):

- Real clients present distinct UDP source ports; a single shared socket collapses Layer-4 ECMP / 5-tuple load-balancer hashing onto one backend target.
- Per-VUser sockets preserve `config.web_runtime.source_ips` binding and per-VUser NAT state.
- Startup and run admission (`pkg/hostnet`) budget one additional file descriptor per QUIC-capable VUser and plan `http3` groups against the conservative 4,000-VUser/generator envelope until Linux soak benchmarks promote the class to `measured`.

### D4. Conservative QUIC defaults and 0-RTT exclusion

- Default script QUIC timings (`5,000 ms` handshake timeout, `30,000 ms` idle timeout, `0 ms` keep-alive PINGs, `per_vuser` `Alt-Svc` cache up to 256 same-host `https` origins, `300,000 ms` broken-origin cooldown) model one long-lived browser profile per VUser without background PING overhead unless configured.
- 0-RTT early data stays disabled (`Dial`, never `DialEarly`) so load-test handshakes never replay non-idempotent application data automatically.

## Runtime architecture summary

- **Transport & connection budget (`pkg/protocol/http`)**: `internal/h3transport` is the sole non-test `quic-go` consumer. QUIC connections share `max_conns_per_host` and `max_idle_conns` slots, cookies, policy-partitioned HTTP cache, target allow-lists, and run correlation with the TCP pools across `http` (`http/3`), `soap`, and `java_http`. Proxied routes, integrated auth (NTLM/Negotiate), cleartext `http://`, TLS maximums below 1.3, and network shaping stay on TCP for `auto+h3` and fail closed before transmission for exact `3`.
- **`auto+h3` fallback boundary**: Pre-send QUIC dial failures (`not_sent`) mark the origin broken for `broken_ttl_ms` and retry once on TCP (`quic_connect_failed`); `421 Misdirected Request` clears the cached alternative and retries once on TCP (`misdirected_request`); `H3_VERSION_FALLBACK` retries on HTTP/1.1 only when unsent or RFC 9110-idempotent with a rebuildable body. `possibly_sent` and post-retry `rejected_unprocessed` failures surface directly without fallback to prevent duplicate writes.
- **Deployment, preflight & telemetry (`pkg/hostnet`, `deployments/helm/apexload`)**: Generators advertise `native_http3_v1`, `http3_upgrade_v1`, and `http3_preflight_v1` when `APEXLOAD_HTTP3=true`. Controller preflight (`APEXLOAD_HTTP3_PREFLIGHT=off|report|block`) probes `https` HTTP/3 origins before committing traffic. `pkg/hostnet` snapshots `RLIMIT_NOFILE`, `net.core.{r,w}mem_max`, and pod-scoped (`APEXLOAD_UDP_COUNTER_SCOPE=pod`) UDP drop counters into run archives and reports. Helm chart `http3.enabled`, `http3.preflight`, `http3.quic.*`, and `networkPolicy.egress` (opening UDP 443 alongside TCP 80/443) configure production clusters; see [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md).
