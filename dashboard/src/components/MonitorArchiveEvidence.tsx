import DesktopApiResourceLink from './DesktopApiResourceLink'
import { useEffect, useState } from 'react'
import { apiFetch } from '../hooks/apiClient'
import type { MetricsHistoryPoint, RunArchiveReport } from '../types'
import type { AnalysisPoint } from '../types/analysis'
import MonitorEvidenceTable from './MonitorEvidenceTable'
import MonitorTimelinePlot from './MonitorTimelinePlot'
import { readBoundedMonitorArchive } from './monitorArchiveModel'
import { monitorSeriesIdentity, monitorEntityLabel } from './monitorTimelineModel'

export default function MonitorArchiveEvidence({ report, testId, history = [] }: { report: RunArchiveReport; testId: string; history?: MetricsHistoryPoint[] }) {
  const coverage = report.manifest.monitor_coverage?.filter(source => source.appdynamics || source.dynatrace) || []
  const provider = coverage.some(source => source.dynatrace) ? coverage.some(source => source.appdynamics) ? 'Monitoring' : 'Dynatrace' : 'AppDynamics'
  const evidence = (report.monitor_diagnostics || []).map(row => ({ ...row.diagnostic, source_id: row.source_id, provider: row.diagnostic.provider || (coverage.find(source => source.source_id === row.source_id)?.dynatrace ? 'dynatrace' as const : 'appdynamics' as const) }))
  const [evidencePage, setEvidencePage] = useState(0)
  const [points, setPoints] = useState<AnalysisPoint[]>([])
  const [series, setSeries] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [load, setLoad] = useState(0)
  useEffect(() => { setPoints([]); setSeries(''); setError(''); setLoad(0); setEvidencePage(0) }, [testId])
  useEffect(() => {
    const name = report.manifest.monitor_evidence_file
    if (!load || !name) return
    const controller = new AbortController()
    setLoading(true); setError('')
    void apiFetch(`/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(name)}`, { signal: controller.signal }).then(readBoundedMonitorArchive).then(result => { if (!controller.signal.aborted) { setPoints(result); setSeries(result[0] ? monitorSeriesIdentity(result[0]) : '') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [testId, report.manifest.monitor_evidence_file, load])
  if (!coverage.length && !evidence.length) return null
  const identities = [...new Map(points.map(point => [monitorSeriesIdentity(point), point])).entries()]
  const selected = points.filter(point => monitorSeriesIdentity(point) === series)
  const visible = evidence.slice(evidencePage * 100, (evidencePage + 1) * 100)
  return <section aria-label={`Archived ${provider} evidence`}><h3>{provider} archive evidence</h3>
    <p>Monitoring: {report.manifest.monitoring_status || 'Unknown'}. Canonical observations and bounded diagnostic summaries were frozen when this archive closed.</p>
    <ul>{coverage.map(source => <li key={source.job_id}>{source.source_id} · revision {source.source_revision} · {source.executor_mode || 'collector'} · {source.state}{source.required ? ' · required' : ' · optional'}{source.reason ? ` · ${source.reason}` : ''}
      {source.provider_coverage && <ul>{Object.entries(source.provider_coverage).map(([capability, health]) => <li key={capability}>{capability}: {health.status}{health.truncated ? ' · truncated' : ''}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}
      {source.topology?.length ? <p>{source.topology.length} retained topology entities.</p> : null}
    </li>)}</ul>
    <MonitorEvidenceTable evidence={visible} provider={provider} />
    {report.manifest.monitor_diagnostics_file && <DesktopApiResourceLink href={`/api/v1/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(report.manifest.monitor_diagnostics_file)}`} filename={report.manifest.monitor_diagnostics_file}>Download retained monitoring diagnostics</DesktopApiResourceLink>}
    {report.manifest.monitor_evidence_file && <DesktopApiResourceLink href={`/api/v1/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(report.manifest.monitor_evidence_file)}`} filename={report.manifest.monitor_evidence_file}>Download retained monitoring metrics</DesktopApiResourceLink>}
    {evidence.length > 100 && <div><p>Diagnostics {evidencePage * 100 + 1}–{Math.min((evidencePage + 1) * 100, evidence.length)} of {evidence.length}.</p><button type="button" disabled={evidencePage === 0} onClick={() => setEvidencePage(value => value - 1)}>Previous archived evidence page</button><button type="button" disabled={(evidencePage + 1) * 100 >= evidence.length} onClick={() => setEvidencePage(value => value + 1)}>Next archived evidence page</button></div>}
    {report.manifest.monitor_evidence_file && <button type="button" disabled={loading} onClick={() => setLoad(value => value + 1)}>{loading ? 'Loading archived monitoring…' : 'Load archived metric timeline'}</button>}
    {error && <p role="alert">{error}</p>}
    {points.length > 0 && <><label>Archived monitor series<select value={series} onChange={event => setSeries(event.target.value)}>{identities.map(([id, point]) => <option key={id} value={id}>{point.source} · {point.metric_name} ({point.unit}) · {monitorEntityLabel(point, coverage.find(source => source.source_id === point.source)?.topology)} · {point.provider?.statistic} / {point.provider?.query_window ? 'query window' : `${point.provider?.resolution_seconds}s`}</option>)}</select></label><MonitorTimelinePlot points={selected} history={history} /></>}
  </section>
}
