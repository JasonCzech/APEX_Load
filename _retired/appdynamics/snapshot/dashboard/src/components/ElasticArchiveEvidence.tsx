import { useEffect, useState } from 'react'
import { apiFetch } from '../hooks/apiClient'
import type { MetricsHistoryPoint, RunArchiveReport } from '../types'
import type { AnalysisPoint } from '../types/analysis'
import type { ElasticRecord } from '../types/elastic'
import { elasticEvidenceTimeError, elasticEvidenceTimestampKey, readBoundedElasticArchive } from './elasticEvidenceModel'
import { readBoundedMonitorArchive } from './appDynamicsEvidenceModel'
import { monitorSeriesIdentity } from './monitorTimelineModel'
import MonitorTimelinePlot from './MonitorTimelinePlot'
import ElasticEvidenceTable, { ElasticTraceWaterfall } from './ElasticEvidenceTable'
import DesktopApiResourceLink from './DesktopApiResourceLink'

export default function ElasticArchiveEvidence({ report, testId, history = [] }: { report: RunArchiveReport; testId: string; history?: MetricsHistoryPoint[] }) {
  const coverage = report.manifest.monitor_coverage?.filter(source => source.elastic) || []
  const [records, setRecords] = useState<ElasticRecord[]>()
  const [points, setPoints] = useState<AnalysisPoint[]>([])
  const [page, setPage] = useState(0)
  const [series, setSeries] = useState('')
  const [filters, setFilters] = useState({ kind: '', service: '', severity: '', trace_id: '', from: '', until: '' })
  const [load, setLoad] = useState<{ kind: 'records' | 'points' }>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { setRecords(undefined); setPoints([]); setPage(0); setSeries(''); setLoad(undefined); setError('') }, [testId])
  useEffect(() => {
    const name = load?.kind === 'records' ? report.manifest.monitor_diagnostics_file : report.manifest.monitor_evidence_file
    if (!load || !name) return
    const controller = new AbortController(); setLoading(true); setError('')
    void apiFetch(`/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(name)}`, { signal: controller.signal }).then(async response => {
      if (load.kind === 'records') { const result = await readBoundedElasticArchive(response); if (!controller.signal.aborted) { setRecords(result); setPage(0) } }
      else { const result = (await readBoundedMonitorArchive(response)).filter(point => point.provider?.name === 'elasticsearch' || point.provider?.name === 'opensearch'); if (!controller.signal.aborted) { setPoints(result); setSeries(result[0] ? monitorSeriesIdentity(result[0]) : '') } }
    }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [testId, load, report.manifest.monitor_diagnostics_file, report.manifest.monitor_evidence_file])
  const evidence = records || (report.monitor_diagnostics || []).filter(row => row.diagnostic.provider === 'elasticsearch' || row.diagnostic.provider === 'opensearch').map(row => ({ ...row.diagnostic, source_id: row.source_id }))
  const timeError = elasticEvidenceTimeError(filters.from, filters.until)
  if (!coverage.length && !evidence.length) return null
  const filtered = evidence.filter(record => (!filters.kind || record.kind === filters.kind) && (!filters.service || record.service === filters.service) && (!filters.severity || record.severity === filters.severity || record.log?.level === filters.severity) && (!filters.trace_id || record.span?.trace_id === filters.trace_id || record.entity_ids?.trace === filters.trace_id) && ((!filters.from && !filters.until) || Boolean(record.timestamp_utc) && (!filters.from || elasticEvidenceTimestampKey(record.timestamp_utc!) >= elasticEvidenceTimestampKey(filters.from)) && (!filters.until || elasticEvidenceTimestampKey(record.timestamp_utc!) < elasticEvidenceTimestampKey(filters.until))))
  const visible = filtered.slice(page * 100, (page + 1) * 100)
  const identities = [...new Map(points.map(point => [monitorSeriesIdentity(point), point])).entries()]
  return <section className="elastic-evidence" aria-label="Archived Elastic evidence"><h3>Elasticsearch / OpenSearch archive evidence</h3><p>Monitoring: {report.manifest.monitoring_status || 'Unknown'}. Source selections, query revisions, projections and coverage were frozen when the archive closed.</p>
    <ul>{coverage.map(source => <li key={source.job_id}>{source.source_id} · source revision {source.source_revision} · connection revision {source.connection_revision ?? 'Not recorded'} · {source.state}{source.required ? ' · required' : ' · optional'}{source.reason ? ` · ${source.reason}` : ''}{source.elastic && <p>{source.elastic.product} · mapping revision {source.elastic.mapping_revision}{source.elastic.queries?.map(query => <span key={query.id}> · {query.id} {query.language}/{query.signal} revision {query.revision}</span>)}</p>}{source.provider_coverage && <ul>{Object.entries(source.provider_coverage).map(([name, capability]) => <li key={name}>{name}: {capability.status}{capability.truncated ? ' · truncated' : ''}{capability.message ? ` · ${capability.message}` : ''}</li>)}</ul>}</li>)}</ul>
    {report.manifest.monitor_diagnostics_file && <><button type="button" disabled={loading} onClick={() => setLoad({ kind: 'records' })}>Load archived Elastic projections</button><DesktopApiResourceLink href={`/api/v1/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(report.manifest.monitor_diagnostics_file)}`} filename={report.manifest.monitor_diagnostics_file} onError={setError}>Download retained monitoring records</DesktopApiResourceLink></>}
    {report.manifest.monitor_evidence_file && <><button type="button" disabled={loading} onClick={() => setLoad({ kind: 'points' })}>Load archived Elastic timeline</button><DesktopApiResourceLink href={`/api/v1/tests/${encodeURIComponent(testId)}/archive/files/${encodeURIComponent(report.manifest.monitor_evidence_file)}`} filename={report.manifest.monitor_evidence_file} onError={setError}>Download retained monitoring metrics</DesktopApiResourceLink></>}
    {error && <p role="alert">{error}</p>}{loading && <p role="status">Loading archived Elastic evidence…</p>}
    <fieldset><legend>Archived Elastic record filters</legend>{Object.entries(filters).map(([name, value]) => <label key={name}>Archived Elastic {name.replace(/_/g, ' ')}<input value={value} onChange={event => { setFilters({ ...filters, [name]: event.target.value }); setPage(0) }} /></label>)}</fieldset>
    {timeError ? <p role="alert">{timeError}</p> : <><ElasticEvidenceTable evidence={visible} /><ElasticTraceWaterfall evidence={visible} />
    {filtered.length > 100 && <div><p>Records {page * 100 + 1}–{Math.min((page + 1) * 100, filtered.length)} of {filtered.length} loaded records.</p><button type="button" disabled={!page} onClick={() => setPage(value => value - 1)}>Previous archived Elastic page</button><button type="button" disabled={(page + 1) * 100 >= filtered.length} onClick={() => setPage(value => value + 1)}>Next archived Elastic page</button></div>}</>}
    {points.length > 0 && <><label>Archived Elastic metric series<select value={series} onChange={event => setSeries(event.target.value)}>{identities.map(([id, point]) => <option value={id} key={id}>{point.source} · {point.metric_name} ({point.unit}) · {point.provider?.statistic} · {JSON.stringify(point.dimensions || {})}</option>)}</select></label><MonitorTimelinePlot points={points.filter(point => monitorSeriesIdentity(point) === series)} history={history} /></>}
  </section>
}
