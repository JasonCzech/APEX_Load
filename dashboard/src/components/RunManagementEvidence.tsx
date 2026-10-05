import './RunManagementEvidence.css'
import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { JsonSuccessResponseBody } from '../types/generated/openapi'

type ManagementPage = JsonSuccessResponseBody<'handleMonitorManagement', '200'>
type Capabilities = JsonSuccessResponseBody<'handleAnalysisCapabilities', '200'>
type Snapshot = NonNullable<ManagementPage['records'][number]['evidence']['messaging_management']>
type Row = Snapshot['rows'][number]
type IntegerObservation = NonNullable<Row['kafka_offsets']>['committed_next']

function entityLabel(row: Row): string {
  if (row.entity.kafka) return `${row.entity.kafka.topic} / ${row.entity.kafka.partition} / ${row.entity.kafka.group}`
  if (row.entity.service_bus) return row.entity.service_bus.queue ?? `${row.entity.service_bus.topic} / ${row.entity.service_bus.subscription}`
  if (row.entity.solace) return `${row.entity.solace.vpn} / ${row.entity.solace.queue ?? row.entity.solace.topic_endpoint}`
  return 'Unavailable entity'
}

function observations(row: Row): [string, IntegerObservation][] {
  if (row.kafka_offsets) return [
    ['Committed next', row.kafka_offsets.committed_next], ['High watermark next', row.kafka_offsets.high_watermark_next],
    ['Last stable next', row.kafka_offsets.last_stable_next], ['Physical replica log end', row.kafka_offsets.log_end_next],
    ['Current leader epoch', row.kafka_offsets.leader_epoch], ['Committed metadata epoch', row.kafka_offsets.committed_leader_epoch],
  ]
  if (row.service_bus_runtime) return [
    ['Total messages', row.service_bus_runtime.total], ['Active messages', row.service_bus_runtime.active],
    ['Scheduled messages', row.service_bus_runtime.scheduled], ['Dead letter messages', row.service_bus_runtime.dead_letter],
    ['Transfer messages', row.service_bus_runtime.transfer], ['Transfer dead letter messages', row.service_bus_runtime.transfer_dead_letter],
    ['Size (bytes)', row.service_bus_runtime.size_bytes], ['Deferred messages', row.service_bus_runtime.deferred],
  ]
  if (row.solace_semp) return [['Instantaneous depth', row.solace_semp.depth]]
  return []
}

// These exact strings never enter Number(), charts or action rollups. Source
// capture/receipt times remain distinct provenance, without assumed clock sync.
export default function RunManagementEvidence({ projectId, testId }: { projectId?: string; testId: string }) {
  const [available, setAvailable] = useState(false)
  const [page, setPage] = useState<ManagementPage>()
  const [source, setSource] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const operation = useRef<AbortController>()
  useEffect(() => {
    const abort = new AbortController()
    setAvailable(false); setPage(undefined); setSource(''); setError(''); setBusy(false)
    if (projectId) void request<Capabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: abort.signal }).then(caps => {
      if (!abort.signal.aborted) setAvailable(caps.messaging_management_version === 1)
    }).catch(() => {})
    return () => { abort.abort(); operation.current?.abort() }
  }, [projectId, testId])
  const load = async (cursor = '') => {
    operation.current?.abort()
    const abort = new AbortController(); operation.current = abort
    setBusy(true); setError('')
    try {
      const query = new URLSearchParams({ limit: '10' })
      if (source) query.set('source_id', source)
      if (cursor) query.set('cursor', cursor)
      const result = await request<ManagementPage>(`/tests/${encodeURIComponent(testId)}/monitor-management?${query}`, { signal: abort.signal })
      if (!abort.signal.aborted) setPage(result)
    } catch (cause) { if (!abort.signal.aborted) setError(String(cause)) }
    finally { if (!abort.signal.aborted) setBusy(false) }
  }
  if (!available) return null
  return <section aria-label="Messaging management evidence" className="tx-section">
    <h3>Messaging management evidence</h3>
    <p>Exact source positions and runtime populations are independent of load action throughput. Missing values remain unavailable. Source and receipt clocks are shown separately.</p>
    <label>Management source ID<input value={source} maxLength={256} onChange={event => { operation.current?.abort(); setBusy(false); setSource(event.target.value); setPage(undefined); setError('') }} /></label>
    <button type="button" disabled={busy} onClick={() => void load()}>{busy ? 'Loading management evidence…' : 'Read management evidence'}</button>
    {error && <p role="alert">{error}</p>}
    {page && page.records.length === 0 && <p>No accepted management snapshots in this page.</p>}
    {page?.records.map(record => {
      const snapshot = record.evidence.messaging_management
      if (!snapshot) return null
      return <article key={`${record.job_id}:${record.sequence}`} aria-label={`Management poll ${snapshot.poll_id}`}>
        <h4>{snapshot.source_id} · {snapshot.kind}</h4>
        <p>Source revision {snapshot.source_revision} · generation {snapshot.generation} · poll {snapshot.poll_sequence} · received batch {record.sequence}</p>
        <p>Captured {snapshot.captured_at} · received {record.received_at}</p>
        <p>{snapshot.inventory_complete && !snapshot.incomplete ? 'Complete approved inventory' : 'Incomplete coverage'}: {snapshot.rows.length} / {snapshot.expected_entities} entities · {snapshot.interval_seconds}s cadence · {snapshot.missed_ticks} missed ticks{snapshot.unavailable_reason ? ` · ${snapshot.unavailable_reason}` : ''}</p>
        {snapshot.rows.map((row, index) => <div key={index}>
          <h5>{entityLabel(row)}</h5>
          <p>Entity state: {row.state}{row.reason ? ` · ${row.reason}` : ''}{row.observed_at ? ` · observed ${row.observed_at}` : ''}{row.observation_age_ms != null ? ` · source age ${row.observation_age_ms}ms` : ' · source age unqualified'}</p>
          <div className="run-management-table-wrap" role="region" aria-label={`Management observation table for ${entityLabel(row)}`} tabIndex={0}>
          <table className="run-management-table"><caption>Exact management observations for {entityLabel(row)}</caption>
            <thead><tr><th scope="col">Measurement</th><th scope="col">Exact value</th><th scope="col">Availability</th></tr></thead>
            <tbody>{observations(row).map(([label, value]) => <tr key={label}><th scope="row">{label}</th><td>{value.state === 'observed' && value.value != null ? value.value : '—'}</td><td>{value.state}{value.reason ? ` · ${value.reason}` : ''}</td></tr>)}</tbody>
          </table>
          </div>
        </div>)}
      </article>
    })}
    {page?.has_more && <button type="button" disabled={busy || !page.cursor} onClick={() => void load(page.cursor)}>Next management page</button>}
  </section>
}
