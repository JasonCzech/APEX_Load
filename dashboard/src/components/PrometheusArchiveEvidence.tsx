import { useEffect, useState } from 'react'
import { apiFetch } from '../hooks/apiClient'
import type { MetricsHistoryPoint, RunArchiveReport } from '../types'
import type { AnalysisPoint } from '../types/analysis'
import { readBoundedMonitorArchive } from './appDynamicsEvidenceModel'
import { monitorSeriesIdentity, monitorEntityLabel } from './monitorTimelineModel'
import MonitorTimelinePlot from './MonitorTimelinePlot'
import { PrometheusDiagnosticTable } from './PrometheusRunEvidence'

export default function PrometheusArchiveEvidence({ report, testId, history = [] }: { report: RunArchiveReport; testId: string; history?: MetricsHistoryPoint[] }) {
  const coverage = report.manifest.monitor_coverage?.filter(item => item.prometheus) || []
  const sourceIDs = new Set(coverage.map(item => item.source_id))
  const evidence = (report.monitor_diagnostics || []).filter(row => sourceIDs.has(row.source_id)).map(row => ({ ...row.diagnostic, source_id: row.source_id }))
  const [points, setPoints] = useState<AnalysisPoint[]>([])
  const [series, setSeries] = useState('')
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState(0)
  useEffect(() => { setPoints([]); setSeries(''); setError(''); setRevision(0); setPage(0) }, [testId])
  useEffect(() => {
    if (!revision || !report.manifest.monitor_evidence_file) return
    const controller = new AbortController()
    setBusy(true); setError('')
    void apiFetch(`/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(report.manifest.monitor_evidence_file)}`, { signal: controller.signal }).then(response => readBoundedMonitorArchive(response, 'prometheus')).then(result => { if (!controller.signal.aborted) { setPoints(result); setSeries(result[0] ? monitorSeriesIdentity(result[0]) : '') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
    return () => controller.abort()
  }, [testId, report.manifest.monitor_evidence_file, revision])
  if (!coverage.length) return null
  const identities = [...new Map(points.map(point => [monitorSeriesIdentity(point), point])).entries()]
  return <section aria-label="Archived Prometheus evidence"><h3>Prometheus archive evidence</h3><p>Monitoring: {report.manifest.monitoring_status || 'Unknown'}. Frozen query definitions, canonical evaluation results, source freshness and coverage remain associated with this run.</p><ul>{coverage.map(item => <li key={item.job_id}>{item.source_id} · revision {item.source_revision} · {item.executor_mode || 'collector'} · {item.state}{item.required ? ' · required' : ' · optional'}{item.reason ? ` · ${item.reason}` : ''}{item.provider_coverage && <ul>{Object.entries(item.provider_coverage).map(([query, health]) => <li key={query}>{query}: {health.status}{health.freshness ? ` · freshness ${health.freshness}` : ''}{health.truncated ? ' · truncated' : ''}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}</li>)}</ul>{evidence.length > 0 && <><PrometheusDiagnosticTable evidence={evidence.slice(page * 100, (page + 1) * 100)} />{evidence.length > 100 && <div><button type="button" disabled={page === 0} onClick={() => setPage(value => value - 1)}>Previous Prometheus evidence page</button><button type="button" disabled={(page + 1) * 100 >= evidence.length} onClick={() => setPage(value => value + 1)}>Next Prometheus evidence page</button></div>}</>}{report.manifest.monitor_evidence_file && <button type="button" disabled={busy} onClick={() => setRevision(value => value + 1)}>{busy ? 'Loading archived Prometheus…' : 'Load archived Prometheus timeline'}</button>}{error && <p role="alert">{error}</p>}{points.length > 0 && <><label>Archived Prometheus series<select value={series} onChange={event => setSeries(event.target.value)}>{identities.map(([identity, point]) => <option key={identity} value={identity}>{point.source} · {point.metric_name} ({point.unit}) · {monitorEntityLabel(point)} · {point.provider?.statistic}</option>)}</select></label><MonitorTimelinePlot points={points.filter(point => monitorSeriesIdentity(point) === series)} history={history} /></>}</section>
}
