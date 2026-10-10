import type { HTTP3PreflightResult, RunArchiveAnalysisResponse, RunArchiveReport } from '../../types'
import { formatMs, formatNumber, formatPct } from './runReportModel'
import { versionMixView, versionPhaseRows } from './httpVersionMixModel'

interface Props {
  report: RunArchiveReport | null
  analysis?: RunArchiveAnalysisResponse | null
  loading?: boolean
  error?: string
}

function EmptyNote({ children }: { children: string }) {
  return <p className="run-report-muted">{children}</p>
}

export default function RunReportDiagnostics({ report, loading, error }: Props) {
  const network = report?.network_phases?.overall
  const versions = versionMixView(report?.network_phases)
  const versionPhases = versionPhaseRows(report?.network_phases)
  const hostNetwork = report?.host_network ?? []
  const preflight = report?.http3_preflight
  const preflightResults = preflight?.results ?? []
  const preflightUnchecked = preflight?.unchecked ?? []
  const peaks = report?.resource_peaks
  return (
    <section className="run-report-panel run-report-diagnostics">
      <div className="run-report-section-heading">
        <div>
          <h2>Network and generator evidence</h2>
          <p>Network phases and the load generators' resource peaks.</p>
        </div>
      </div>
      {loading && <p role="status">Loading diagnostics…</p>}
      {error && <p className="run-report-error">{error}</p>}
      <details>
        <summary>Network phases</summary>
        {network ? (
          <div className="run-report-diagnostic-grid">
            <div><span>DNS</span><strong>{formatMs(network.avg_dns_ms)}</strong></div>
            <div><span>Connect</span><strong>{formatMs(network.avg_connect_ms)}</strong></div>
            <div><span>TLS</span><strong>{formatMs(network.avg_tls_ms)}</strong></div>
            <div><span>TTFB</span><strong>{formatMs(network.avg_ttfb_ms)}</strong></div>
          </div>
        ) : <EmptyNote>No network phase evidence was archived.</EmptyNote>}
      </details>
      {versions && <details open={versions.mix.finding ? true : undefined}>
        <summary>HTTP versions</summary>
        <p className="run-report-muted">Every HTTP action, by the version its response used. Cache hits and requests that got no response are counted separately.</p>
        <div className="run-archive-coverage-table"><table className="tx-table" aria-label="HTTP version mix">
          <thead><tr><th scope="col">Version</th><th scope="col">Actions</th><th scope="col">Share</th></tr></thead>
          <tbody>{versions.rows.map(row => <tr key={row.key}><th scope="row">{row.label}</th><td>{formatNumber(row.count)}</td><td>{formatPct(row.sharePct, 1)}</td></tr>)}</tbody>
        </table></div>
        {versions.usedHTTP3 && <div className="run-report-diagnostic-grid">
          <div><span>Fell back to TCP</span><strong>{versions.fallback}</strong></div>
          <div><span>QUIC failures</span><strong>{versions.quicFailure}</strong></div>
          {(versions.mix.attempts ?? 0) > 0 && <div><span>HTTP attempts (HTTP/3 actions)</span><strong>{formatNumber(versions.mix.attempts)}</strong></div>}
        </div>}
        {versions.routing && <p>Routing: {versions.routing}</p>}
        {versions.mix.finding && <p role="note" className="run-report-error">Protocol Mix: {versions.mix.finding.summary}</p>}
        {versionPhases.length > 0 && <div className="run-archive-coverage-table"><table className="tx-table" aria-label="Network phases by HTTP version">
          <thead><tr><th scope="col">Version</th><th scope="col">Samples</th><th scope="col">DNS</th><th scope="col">Connect</th><th scope="col">TLS</th><th scope="col">TTFB</th></tr></thead>
          <tbody>{versionPhases.map(row => <tr key={row.key}><th scope="row">{row.label}</th><td>{formatNumber(row.phases.count)}</td><td>{formatMs(row.phases.avg_dns_ms)}</td><td>{formatMs(row.phases.avg_connect_ms)}</td><td>{formatMs(row.phases.avg_tls_ms)}</td><td>{formatMs(row.phases.avg_ttfb_ms)}</td></tr>)}</tbody>
        </table></div>}
        {versionPhases.some(row => row.key === '3') && <p className="run-report-muted">HTTP/3 connect time includes the QUIC handshake, which carries TLS, so HTTP/3 has no separate TLS time.</p>}
      </details>}
      {hostNetwork.length > 0 && <details open={hostNetwork.some(item => item.warning) ? true : undefined}>
        <summary>Load generator UDP</summary>
        <p className="run-report-muted">Each execution process's open-file limit and UDP buffer maxima, and how much its network namespace's UDP error counters grew during the run. Counters of a shared namespace are shown but not attributed to the run.</p>
        <div className="run-archive-coverage-table"><table className="tx-table" aria-label="Load generator UDP">
          <thead><tr><th scope="col">Process</th><th scope="col">Open files</th><th scope="col">UDP buffer max (receive / send)</th><th scope="col">UDP drops during the run</th></tr></thead>
          <tbody>{hostNetwork.map((item, index) => <tr key={`${item.source ?? 'process'}-${index}`}>
            <th scope="row">{item.source || 'Load generator'}</th>
            <td>{item.open_files?.available ? formatNumber(item.open_files.soft) : 'Unavailable'}</td>
            <td>{item.udp_buffers?.available ? `${formatNumber(item.udp_buffers.receive_max_bytes)} / ${formatNumber(item.udp_buffers.send_max_bytes)} bytes` : 'Unavailable'}</td>
            <td>{item.udp_drops_delta ? `${formatNumber(item.udp_drops_delta.receive_buffer_errors + item.udp_drops_delta.send_buffer_errors + item.udp_drops_delta.in_errors)}${item.udp_drops_at_end?.scope === 'pod' ? '' : ' (shared namespace)'}` : 'Unavailable'}</td>
          </tr>)}</tbody>
        </table></div>
        {hostNetwork.filter(item => item.warning).map((item, index) => <p key={index} role="note" className="run-report-error">{item.warning}</p>)}
      </details>}
      {preflight && (preflightResults.length > 0 || preflightUnchecked.length > 0) && <details open={preflightResults.some(result => !result.ok && !result.skipped) ? true : undefined}>
        <summary>HTTP/3 preflight</summary>
        <p className="run-report-muted">One QUIC handshake, without a request, to each https origin an HTTP/3 policy reaches, from each load source that runs the group, before the run started. Policy: {preflight.policy}.{preflight.blocked ? ' An exact HTTP/3 origin failed, so the run was not started.' : ''}</p>
        <div className="run-archive-coverage-table"><table className="tx-table" aria-label="HTTP/3 preflight">
          <thead><tr><th scope="col">Load source</th><th scope="col">Group</th><th scope="col">Origin</th><th scope="col">Policy</th><th scope="col">Result</th></tr></thead>
          <tbody>
            {preflightResults.map((result, index) => <tr key={`${result.source}-${result.group}-${result.origin}-${index}`}>
              <th scope="row">{preflightSource(result.source, result.region)}</th>
              <td>{result.group}</td>
              <td>{result.origin}</td>
              <td>{result.policy}</td>
              <td>{preflightResultText(result)}</td>
            </tr>)}
            {preflightUnchecked.map((source, index) => <tr key={`unchecked-${source.source}-${index}`}>
              <th scope="row">{preflightSource(source.source, source.region)}</th>
              <td colSpan={3}>All origins</td>
              <td>Not checked: {source.reason}</td>
            </tr>)}
          </tbody>
        </table></div>
        {(preflight.omitted ?? 0) > 0 && <p className="run-report-muted">{formatNumber(preflight.omitted)} more checks are not listed; failures are listed first.</p>}
      </details>}
      <details>
        <summary>Resource peaks</summary>
        {peaks ? (
          <div className="run-report-diagnostic-grid">
            <div><span>CPU</span><strong>{formatPct(peaks.cpu_process_pct, 1)}</strong></div>
            <div><span>Memory</span><strong>{formatNumber(peaks.mem_process_mb, 1)} MB</strong></div>
            <div><span>Net egress</span><strong>{formatNumber(peaks.net_sent_kbps, 1)} KB/s</strong></div>
            <div><span>Disk write</span><strong>{formatNumber(peaks.disk_write_kbps, 1)} KB/s</strong></div>
          </div>
        ) : <EmptyNote>No resource peak summary was archived.</EmptyNote>}
      </details>
    </section>
  )
}

function preflightSource(source: string, region?: string): string {
  return region ? `${source} (${region})` : source
}

function preflightResultText(result: HTTP3PreflightResult): string {
  if (result.skipped) return `Not checked: ${result.skipped}`
  if (result.ok) return `Handshake completed in ${formatMs(result.handshake_ms)}${result.address ? ` to ${result.address}` : ''}`
  const message = result.error ?? ''
  const reason = !result.error_class || message.includes(result.error_class) ? message || 'unknown error' : message ? `${result.error_class}: ${message}` : result.error_class
  return `Failed (${reason})`
}
