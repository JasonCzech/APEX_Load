import type { BrowserReplayNetworkEntry, HTTPNetworkTrace, ProtocolEvidence, ReplayHARPhase } from '../types/replay'

// Evidence is published after redaction, which may replace a field whose key
// names a credential (cookie_names_sent, set_cookie_names_received) with the
// string "[REDACTED]". Every list is therefore read defensively: an array keeps
// its string entries, a non-empty string is shown as given, anything else is
// treated as absent.
function traceList(value: unknown): string {
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === 'string' && item !== '').join(', ')
  return typeof value === 'string' ? value : ''
}

function traceObject(value: unknown): HTTPNetworkTrace | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as HTTPNetworkTrace : undefined
}

// Socket reuse is known only when the trace says so: true is a reused
// connection, an explicit false a fresh one, and an absent key is unknown.
function socketReuseLabel(trace: HTTPNetworkTrace): 'reused socket' | 'new socket' | undefined {
  if (trace.connection_reused === true) return 'reused socket'
  if (trace.connection_reused === false) return 'new socket'
  return undefined
}

function formatHopPhases(phases?: ReplayHARPhase[]): string {
  if (!Array.isArray(phases) || phases.length === 0) return ''
  const parts: string[] = []
  for (const phase of phases) {
    if (
      phase &&
      typeof phase.start_ms === 'number' &&
      typeof phase.end_ms === 'number' &&
      phase.end_ms >= phase.start_ms
    ) {
      parts.push(`${phase.name} ${(phase.end_ms - phase.start_ms).toFixed(1)} ms`)
    }
  }
  return parts.join(' · ')
}

function formatNetworkTraceSummaryHeadline(trace?: HTTPNetworkTrace, hopCount = 0): string {
  const parts: string[] = []
  if (trace) {
    if (trace.http_protocol) {
      parts.push(trace.http_protocol)
    }
    const socket = socketReuseLabel(trace)
    if (socket) {
      parts.push(socket)
    }
    if (trace.tls_version) {
      parts.push(trace.tls_version)
    }
    if (trace.content_encoding) {
      parts.push(trace.content_encoding)
    }
  }
  if (hopCount > 1) {
    parts.push(`${hopCount} hops`)
  }
  return parts.join(' · ')
}

export function HTTPNetworkTraceEvidence({
  evidence,
  networkEntries,
  unavailable = false,
}: {
  evidence?: ProtocolEvidence
  networkEntries?: BrowserReplayNetworkEntry[]
  unavailable?: boolean
}) {
  if (unavailable) return null
  const hops = (Array.isArray(networkEntries) ? networkEntries : []).filter(entry => Boolean(entry && (traceObject(entry.trace) || entry.har)))
  const trace = traceObject(evidence?.network_trace) ?? traceObject(hops[hops.length - 1]?.trace)
  if (!trace && hops.length === 0) return null
  const dnsResolvedIPs = traceList(trace?.dns_resolved_ips)
  const transferEncoding = traceList(trace?.transfer_encoding)
  const cookieNamesSent = traceList(trace?.cookie_names_sent)
  const setCookieNamesReceived = traceList(trace?.set_cookie_names_received)
  const strippedRedirectHeaders = traceList(trace?.stripped_redirect_headers)

  const headline = formatNetworkTraceSummaryHeadline(trace, hops.length)

  return (
    <details className="replay-network-trace">
      <summary>Network &amp; transport trace{headline ? ` · ${headline}` : ''}</summary>
      {trace && (
        <section className="sub-section" aria-label="HTTP transport and TLS trace">
          <dl>
            {trace.http_protocol && (
              <>
                <dt>HTTP protocol</dt>
                <dd><code>{trace.http_protocol}</code></dd>
              </>
            )}
            <dt>Socket reuse</dt>
            <dd>
              {trace.connection_reused === true
                ? `Reused connection${trace.was_idle ? ` (idle ${typeof trace.idle_time_ms === 'number' ? `${trace.idle_time_ms.toFixed(2)} ms` : 'yes'})` : ''}`
                : trace.connection_reused === false
                  ? 'Fresh connection'
                  : 'Unknown (not observed)'}
            </dd>
            {trace.remote_addr && (
              <>
                <dt>Remote endpoint</dt>
                <dd><code>{trace.remote_addr}</code></dd>
              </>
            )}
            {trace.local_addr && (
              <>
                <dt>Local endpoint</dt>
                <dd><code>{trace.local_addr}</code></dd>
              </>
            )}
            {dnsResolvedIPs && (
              <>
                <dt>Resolved DNS IPs</dt>
                <dd>
                  <code>{dnsResolvedIPs}</code>
                  {trace.dns_coalesced ? ' (coalesced)' : ''}
                </dd>
              </>
            )}
            {(trace.got_100_continue || (typeof trace.wait_100_continue_ms === 'number' && trace.wait_100_continue_ms > 0)) && (
              <>
                <dt>100-Continue wait</dt>
                <dd>
                  Waited for 100-Continue response
                  {typeof trace.wait_100_continue_ms === 'number' && trace.wait_100_continue_ms > 0
                    ? ` (${trace.wait_100_continue_ms.toFixed(2)} ms)`
                    : ''}
                </dd>
              </>
            )}
            {trace.tls_version && (
              <>
                <dt>TLS handshake</dt>
                <dd>
                  {[
                    trace.tls_version,
                    trace.tls_cipher_suite,
                    trace.tls_negotiated_proto ? `ALPN ${trace.tls_negotiated_proto}` : '',
                    trace.tls_did_resume ? 'resumed session' : '',
                  ].filter(Boolean).join(' · ')}
                </dd>
              </>
            )}
            {trace.tls_server_name && (
              <>
                <dt>TLS SNI host</dt>
                <dd><code>{trace.tls_server_name}</code></dd>
              </>
            )}
            {(trace.tls_cert_subject || trace.tls_cert_issuer || trace.tls_cert_expiry) && (
              <>
                <dt>Peer leaf certificate</dt>
                <dd>
                  {[
                    trace.tls_cert_subject ? `subject ${trace.tls_cert_subject}` : '',
                    trace.tls_cert_issuer ? `issuer ${trace.tls_cert_issuer}` : '',
                    trace.tls_cert_expiry ? `expires ${trace.tls_cert_expiry}` : '',
                  ].filter(Boolean).join(' · ')}
                </dd>
              </>
            )}
            {(trace.request_compression || trace.content_encoding || transferEncoding) && (
              <>
                <dt>Content &amp; transfer encoding</dt>
                <dd>
                  {[
                    trace.request_compression ? `request ${trace.request_compression}` : '',
                    trace.content_encoding ? `response ${trace.content_encoding}` : '',
                    transferEncoding ? `transfer ${transferEncoding}` : '',
                  ].filter(Boolean).join(' · ')}
                </dd>
              </>
            )}
            {(typeof trace.encoded_body_bytes === 'number' || typeof trace.decoded_body_bytes === 'number') && (
              <>
                <dt>Payload byte accounting</dt>
                <dd>
                  {[
                    typeof trace.encoded_body_bytes === 'number' ? `${trace.encoded_body_bytes} B wire` : '',
                    typeof trace.decoded_body_bytes === 'number' ? `${trace.decoded_body_bytes} B decoded` : '',
                  ].filter(Boolean).join(' → ')}
                </dd>
              </>
            )}
            {(typeof trace.cookies_sent_count === 'number' || typeof trace.set_cookies_received_count === 'number') && (
              <>
                <dt>Cookie names (values omitted)</dt>
                <dd>
                  {[
                    typeof trace.cookies_sent_count === 'number'
                      ? `sent ${trace.cookies_sent_count}${cookieNamesSent ? ` (${cookieNamesSent})` : ''}`
                      : '',
                    typeof trace.set_cookies_received_count === 'number'
                      ? `received ${trace.set_cookies_received_count}${setCookieNamesReceived ? ` (${setCookieNamesReceived})` : ''}`
                      : '',
                  ].filter(Boolean).join(' · ')}
                </dd>
              </>
            )}
            {(trace.cross_origin_redirect || strippedRedirectHeaders) && (
              <>
                <dt>Redirect chain</dt>
                <dd>
                  {[
                    hops.length > 1 ? `${hops.length} hops` : 'cross-origin redirect',
                    strippedRedirectHeaders
                      ? `stripped cross-origin headers: ${strippedRedirectHeaders}`
                      : '',
                  ].filter(Boolean).join(' · ')}
                </dd>
              </>
            )}
          </dl>
        </section>
      )}
      {hops.length > 0 && (
        <section className="sub-section" aria-label="HTTP hop waterfall">
          <span className="replay-detail-label">Observed HTTP hops ({hops.length})</span>
          <ol className="replay-network-hop-list">
            {hops.map((hop, idx) => {
              const timingsLabel = formatHopPhases(hop.har?.phases)
              const hopTrace = traceObject(hop.trace)
              const hopStripped = traceList(hopTrace?.stripped_redirect_headers)
              return (
                <li key={`${idx}-${hop.method || 'GET'}-${hop.url || ''}`} className="replay-network-hop-item">
                  <div>
                    <strong>Hop {idx + 1}: {hop.method || 'GET'} {hop.status_code ?? 'n/a'}</strong>{' '}
                    <code>{hop.url}</code>
                    {typeof hop.duration_ms === 'number' && <span> · {hop.duration_ms.toFixed(1)} ms</span>}
                    {hopTrace?.http_protocol && <span> · {hopTrace.http_protocol}</span>}
                  </div>
                  {timingsLabel && <div><small>Timings: {timingsLabel}</small></div>}
                  {hopTrace && (
                    <div>
                      <small>
                        {[
                          socketReuseLabel(hopTrace) || '',
                          hopTrace.remote_addr ? `remote ${hopTrace.remote_addr}` : '',
                          hopTrace.tls_version ? `${hopTrace.tls_version}${hopTrace.tls_negotiated_proto ? ` (${hopTrace.tls_negotiated_proto})` : ''}` : '',
                          hopTrace.content_encoding ? `encoding ${hopTrace.content_encoding}` : '',
                          hopStripped ? `stripped ${hopStripped}` : '',
                        ].filter(Boolean).join(' · ')}
                      </small>
                    </div>
                  )}
                </li>
              )
            })}
          </ol>
        </section>
      )}
    </details>
  )
}
