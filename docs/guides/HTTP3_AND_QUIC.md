# HTTP/3 and QUIC

Status: Guide — HTTP/3 runs as a preview on installations that enable it. Scripts that use HTTP/3 save and validate on every installation; a run that needs HTTP/3 is refused where it is not enabled, and the editor says which applies to the controller you are connected to.

HTTP/3 carries HTTP over QUIC, a UDP transport with TLS 1.3 built into its handshake. Browsers use it for many large sites once the site advertises it in an `Alt-Svc` response header. APEX Load's native HTTP VUsers (`http`, `soap` and `java_http`) can use it through two version policies; everything else about a request (cookies, redirects, caching, checks, extraction) is unchanged.

## Choose a policy

Set the policy for the whole script in Runtime Settings → HTTP Transport → Connection (`config.http.version`), or for one request in the request editor (`http_version`). A request without its own policy inherits the script's.

| Policy | What it does | When the request cannot use HTTP/3 |
| --- | --- | --- |
| Auto-negotiate (empty, or `auto` on a request) | HTTP/2 or HTTP/1.1 over TCP, as before. An `Alt-Svc` advertisement is recorded in replay evidence and changes nothing | Not applicable |
| Upgrade to HTTP/3 when advertised (`auto+h3`) | Starts on TCP; once a response from the origin advertises a same-host HTTP/3 alternative, later requests to that origin use QUIC | Stays on TCP and names the reason; falls back to TCP only when the request cannot have reached the server |
| Require HTTP/3 (`3`) | HTTP/3 from the first request, on every redirect hop | Fails. The error says whether the request was `not_sent`, `possibly_sent` or `rejected_unprocessed` |
| Require HTTP/2 (`2`) or HTTP/1.1 (`1.1`) | Exactly that version over TCP | Not applicable |

Use `auto+h3` to measure a site the way browsers reach it. Use `3` to prove that an origin serves HTTP/3 at all, or to test HTTP/3 in isolation; it has no fallback by design.

## Worked examples

These fragments extend a valid HTTP script; they are not standalone scripts.

### Upgrade when the origin advertises HTTP/3

```json
{
  "config": {
    "base_url": "https://shop.example",
    "http": { "version": "auto+h3" }
  },
  "actions": [
    { "name": "Home", "type": "GET", "target": "/" },
    { "name": "Catalog", "type": "GET", "target": "/catalog" }
  ]
}
```

The first request runs on TCP and its response advertises `h3`. The second runs on QUIC. In replay, each request's evidence shows the routing reason: `TCP: the origin has not advertised HTTP/3 yet` for the first, `Upgraded to the advertised HTTP/3 alternative` for the second. Each virtual user remembers advertisements for itself; set the Alt-Svc cache to per iteration to rediscover them every iteration.

### Require HTTP/3

```json
{
  "config": {
    "base_url": "https://shop.example",
    "http": { "version": "3", "quic": { "handshake_timeout_ms": 3000 } }
  }
}
```

A network that drops UDP makes every request fail with a QUIC error; nothing silently falls back. Exact HTTP/3 needs an `https://` URL and TLS 1.3, and is refused on save with a custom proxy, integrated (NTLM or Negotiate) authentication or network shaping. Those problems are reported at the version field.

### Keep TCP but see what the origin offers

With the default policy, replay notes when a response advertised HTTP/3 that the request did not use. When the replayed draft is still the one in the editor, the evidence offers **Switch this request to auto+h3**, or **Switch the script default to auto+h3** when the request inherits. The switch is an ordinary edit: it appears in history, the draft is validated again, and the next replay exercises HTTP/3.

### Mix policies per request

```json
{
  "config": { "http": { "version": "2" } },
  "actions": [
    { "name": "API", "type": "GET", "target": "https://api.example/orders" },
    { "name": "Static", "type": "GET", "target": "https://cdn.example/app.js", "http_version": "auto+h3" }
  ]
}
```

Requests with different policies share the virtual user's cookies, cache and connection budget.

## QUIC settings

The QUIC (HTTP/3) panel sits under the HTTP version in Runtime Settings and in scenario group settings. Empty fields take the default. Each field shows its bounds when a value is out of range.

| Field | Range | Default | Purpose |
| --- | --- | --- | --- |
| `handshake_timeout_ms` | 1,000–60,000 | 5,000 | QUIC handshake limit. The request timeout still covers the whole request |
| `max_idle_timeout_ms` | 1,000–600,000 | 30,000 | Closes a QUIC connection with no traffic for this long |
| `keep_alive_ms` | 0 to the idle timeout | 0 (off) | PING interval that keeps NAT bindings and idle connections open |
| `alt_svc_cache` | `per_vuser`, `per_iteration` | `per_vuser` | Scope of remembered HTTP/3 advertisements (`auto+h3`) |
| `broken_ttl_ms` | 0–600,000 | 300,000 | How long `auto+h3` keeps an origin on TCP after its QUIC connection failed; 0 forgets at once |

Receive windows, header limits, stream budgets and packet sizing are not script settings; operators set them for the installation (see [Enable HTTP/3 execution](#enable-http-3-execution)). The connection settings you already use (maximum connections per host, keep-alive, idle timeout, IP version, DNS cache, source IP) and the TLS client certificate, CA file and verification setting apply to QUIC as well.

## What stays on TCP

`auto+h3` keeps a request on TCP, and says why in its evidence, when:

- the URL is `http://` (HTTP/3 serves only `https` origins);
- the route is a proxy (a custom proxy, a system proxy that `no_proxy` does not bypass, or a PAC proxy route);
- the request uses integrated (NTLM or Negotiate) authentication;
- the TLS maximum is below 1.3;
- the origin's QUIC connection failed recently (`broken_ttl_ms`).

Network shaping applies to TCP only, so neither HTTP/3 policy can be combined with a shaping profile. For a TCP-versus-QUIC comparison under impairment, shape the network outside APEX Load (for example with `tc netem`).

## Import captured HTTP/3

Browser captures (Chrome, Firefox, Playwright and the browser recorder) label HTTP/3 requests `h3`. The import's **HTTP version in generated script** option decides what they become: **Upgrade captured HTTP/3** (the default) maps them to `auto+h3`, **Keep captured versions exactly** maps them to `3`, and **Inherit the script setting** gives no request a policy and reports what it dropped. Captured HTTP/2 and HTTP/1.1 stay exact except under inherit. The captured version is kept as an observation either way.

Import review's **Transport** panel sets one policy for every request to an origin, nested requests included, after a preview. Use it to move an origin that advertised HTTP/3 but was captured on TCP to `auto+h3`.

cURL's `--http3-only` maps to `3`; `--http3` keeps a review gate, because replay does not race HTTP/3 against TCP the way curl does, and proposes `auto+h3` in the Transport panel.

## Read the results

- **Replay evidence** names the policy, the negotiated version, the routing reason, any fallback, the QUIC connection (new, reused, or still being opened), whether TLS resumed, and the number of HTTP attempts. TLS is part of the QUIC handshake, so connect time includes it and TLS time is 0.
- **The Run Report** shows the HTTP version mix in its outcome header when any request used HTTP/3. Diagnostics, and the Network Phase Breakdown in every export, give the counts per version, both ratios with their denominators (fell back ÷ upgrade-eligible `auto+h3` actions, failed ÷ attempted QUIC actions), the routing reasons and network phases per version. Cache hits and requests that got no response are counted separately.
- **The live run** shows the same run-to-date mix on the test page's Diagnostics tab.
- **Power BI** dataset schema version 2 carries the mix on runs and per-action version counts on transactions.

A **Protocol Mix** finding is raised when a ratio exceeds what the script expects, once the run has enough samples:

```json
{
  "config": {
    "results": {
      "protocol_mix": { "max_fallback_ratio": 0.02, "max_quic_failure_ratio": 0.05, "min_samples": 50 }
    }
  }
}
```

The defaults are 5% for each ratio at 20 samples. Across a scenario's groups the strictest value applies. The finding names the most frequent causes.

## Browser VUsers

Browsers choose HTTP/3 themselves. `config.browser.quic` is `auto` (the browser's default) or `off`, which launches Chromium or Firefox with HTTP/3 disabled; WebKit cannot disable it. With document evidence capture on, each browser request records the protocol it used, and the document waterfall shows it in a protocol column.

## Enable HTTP/3 execution

HTTP/3 execution is off until an operator enables it. Set `APEXLOAD_HTTP3=true` on the controller and on every generator, then restart them. The Helm chart does not set it yet; add it to the controller and generator deployments with your deployment tooling, for example:

```sh
kubectl -n apexload set env deployment/apexload-controller APEXLOAD_HTTP3=true
kubectl -n apexload set env statefulset/apexload-generator APEXLOAD_HTTP3=true
```

Generators that have it advertise `native_http3_v1` (exact `3`) and `http3_upgrade_v1` (`auto+h3`); a run whose requests need HTTP/3 is placed only on generators that advertise the capability, and is refused with a message naming the flag otherwise. The editor's capability note reflects the controller (`GET /api/v1/scripts/authoring/capabilities`, `http_runtime`); generators are checked again when each run is admitted.

Installation-wide QUIC limits are environment variables on the controller and generators. An invalid value stops the process at startup with a message naming the variable.

| Variable | Default | Bounds |
| --- | --- | --- |
| `APEXLOAD_QUIC_INITIAL_STREAM_RECEIVE_WINDOW` | 512 KiB | 16 KiB–1 GiB |
| `APEXLOAD_QUIC_MAX_STREAM_RECEIVE_WINDOW` | 6 MiB | 16 KiB–1 GiB |
| `APEXLOAD_QUIC_INITIAL_CONNECTION_RECEIVE_WINDOW` | 768 KiB | 16 KiB–1 GiB |
| `APEXLOAD_QUIC_MAX_CONNECTION_RECEIVE_WINDOW` | 15 MiB | 16 KiB–1 GiB |
| `APEXLOAD_QUIC_MAX_RESPONSE_HEADER_BYTES` | The TCP transport's limit | 4 KiB–10 MiB |
| `APEXLOAD_QUIC_INITIAL_PACKET_SIZE` | 1,280 | 1,200–1,452 bytes |
| `APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY` | `false` | `true` or `false` |

An initial window may not exceed its maximum. Receive windows size memory per connection; raise them only within the generator's capacity plan.

## Network egress and UDP buffers

- **Egress.** Generators need UDP to each target's HTTPS port (usually 443) and to any alternative port a target advertises (`h3=":8443"`). Cloud firewalls, corporate egress proxies and some NAT gateways drop UDP 443 by default. The Helm chart ships ingress policies only; the on-premises egress example allows every port outside the excluded cluster ranges. Whether the chart should open UDP egress by default is still to be decided.
- **What blocked UDP looks like.** `auto+h3` falls back after the QUIC handshake times out (`quic_connect_failed`, five seconds by default), keeps that origin on TCP for `broken_ttl_ms`, and the Protocol Mix finding reports the fallback ratio. Exact `3` fails every request.
- **UDP socket buffers.** QUIC throughput depends on the kernel's UDP buffer limits. On Linux generators raise `net.core.rmem_max` and `net.core.wmem_max` (for example to 7,500,000 bytes). These are node-level settings on Kubernetes, so set them on the generator nodes rather than in the pod.
- **File descriptors.** Each virtual user that uses HTTP/3 holds its own UDP socket, in addition to its TCP connections; size the generator's open-file limit for the VUser count.
- **MTU.** QUIC sends packets of at least 1,200 bytes and discovers larger sizes unless disabled. Tunnels or overlays with a small MTU can drop them; `APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY=true` keeps packets at the initial size.

## Troubleshooting

- **"HTTP/3 execution is not enabled on this installation."** The controller, or the generators the run was placed on, do not have `APEXLOAD_HTTP3=true`. The script still saves; enable the flag or choose a TCP policy.
- **High fallback ratio.** Check UDP egress from the generators to the target. A short `handshake_timeout_ms` reduces the time each fallback costs.
- **A request stays on TCP under `auto+h3`.** Its evidence names the reason: no advertisement yet, a proxy route, integrated authentication, the TLS maximum, or a recent QUIC failure.
- **"possibly_sent".** The QUIC connection failed after the request may have reached the server. APEX Load does not repeat such a request, because it might not be safe to send twice.
