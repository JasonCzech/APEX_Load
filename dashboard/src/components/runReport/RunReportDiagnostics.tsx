import type { RunArchiveAnalysisResponse, RunArchiveReport } from '../../types'
import { formatMs, formatNumber, formatPct } from './runReportModel'

interface Props {
  report: RunArchiveReport | null
  analysis?: RunArchiveAnalysisResponse | null
  loading?: boolean
  error?: string
}

function EmptyNote({ children }: { children: string }) {
  return <p className="run-report-muted">{children}</p>
}

export default function RunReportDiagnostics({ report, analysis, loading, error }: Props) {
  const network = report?.network_phases?.overall
  const peaks = report?.resource_peaks
  return (
    <section className="run-report-panel run-report-diagnostics">
      <div className="run-report-section-heading">
        <div>
          <h2>Diagnostics</h2>
          <p>Expand only when the verdict needs evidence.</p>
        </div>
      </div>
      {loading && <p role="status">Loading diagnostics...</p>}
      {error && <p className="run-report-error">{error}</p>}
      <details open>
        <summary>Completeness</summary>
        {report?.completeness ? (
          <div className="run-report-diagnostic-grid">
            <div><span>Score</span><strong>{formatPct(report.completeness.score_pct, 0)}</strong></div>
            <div><span>Status</span><strong>{report.completeness.status}</strong></div>
            <div><span>Findings</span><strong>{formatNumber(report.completeness.findings?.length || 0, 0)}</strong></div>
          </div>
        ) : <EmptyNote>No completeness score is available.</EmptyNote>}
      </details>
      <details>
        <summary>Error patterns</summary>
        {report?.error_patterns?.length ? (
          <ul className="run-report-diagnostic-list">
            {report.error_patterns.slice(0, 6).map(pattern => (
              <li key={pattern.signature}>
                <strong>{pattern.sample || pattern.signature}</strong>
                <span>{formatNumber(pattern.count, 0)} occurrences</span>
              </li>
            ))}
          </ul>
        ) : <EmptyNote>No recurring error patterns were detected.</EmptyNote>}
      </details>
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
      <details>
        <summary>Archive analysis</summary>
        {analysis ? (
          <div className="run-report-diagnostic-grid">
            <div><span>Nodes</span><strong>{formatNumber(analysis.graph.nodes.length, 0)}</strong></div>
            <div><span>Edges</span><strong>{formatNumber(analysis.graph.edges.length, 0)}</strong></div>
            <div><span>Drill-down rows</span><strong>{formatNumber(analysis.total, 0)}</strong></div>
          </div>
        ) : <EmptyNote>No archive analysis graph is available.</EmptyNote>}
      </details>
    </section>
  )
}
