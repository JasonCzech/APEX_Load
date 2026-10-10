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

export default function RunReportDiagnostics({ report, loading, error }: Props) {
  const network = report?.network_phases?.overall
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
