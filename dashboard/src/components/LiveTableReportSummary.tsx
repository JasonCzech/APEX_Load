import type { LiveTableOperationStats, RunArchiveLiveTableSummary } from '../types/dataPools'
import { metricHeader } from '../metricLabels'
import './LiveTables.css'

export function LiveTableFinalizationOutcome({ finalization, mode }: { finalization: RunArchiveLiveTableSummary['finalization']; mode?: string }) {
  if (finalization?.state === 'pending' && finalization.reason === 'output_limits') return <span> Final output exceeds data-pool execution limits. Export it and reduce its size before importing it into a pool.</span>
  if (finalization?.state === 'pending' && finalization.reason === 'reconciliation_required') return <span> Finalization was not recorded. Review the retained output before saving it as a new data pool.</span>
  if (finalization?.state !== 'complete' || !finalization.output_pool_id) return null
  const completedMode = finalization.mode || mode
  if (completedMode === 'write_back') return <span> Original data pool updated.</span>
  if (completedMode === 'save_as_new') return <span> Output saved in a separate data pool.</span>
  return null
}

export function LiveTableOperationSummary({ stats }: { stats: LiveTableOperationStats }) {
  const mean = stats.op_count && stats.op_latency_total_ns !== undefined ? stats.op_latency_total_ns / stats.op_count / 1e6 : undefined
  const max = stats.op_latency_max_ns === undefined ? undefined : stats.op_latency_max_ns / 1e6
  const observed = stats.observations
  return <><p>Table operation metrics cover the current serving controller’s interval and reset after adoption. Controller timing includes waits and commits, excludes transport time, and is separate from target request timings.</p>
    <dl className="live-table-stats">
      <div><dt>Table operations</dt><dd>{stats.op_count ?? 'Unknown'}</dd></div>
      <div><dt>Operation errors</dt><dd>{stats.op_errors ?? 'Unknown'}</dd></div>
      <div><dt>Unknown outcomes</dt><dd>{stats.op_unknown ?? 'Unknown'}</dd></div>
      <div><dt>{metricHeader('tableOpMean')}</dt><dd>{mean === undefined ? 'Unavailable' : `${mean.toFixed(2)} ms`}</dd></div>
      <div><dt>{metricHeader('tableOpMax')}</dt><dd>{max === undefined ? 'Unavailable' : `${max.toFixed(2)} ms`}</dd></div>
    </dl>
    {observed && <>
      <h4>Retained shared-data evidence</h4>
      <p>Waiting for shared data: {observed.wait_count} waits, {(observed.wait_total_ns / 1e6).toFixed(2)} ms total. This component includes waiting for fulfillment and settlement; it is separate from target response time.</p>
      <p>Empty outcomes: {observed.empty_outcomes}. Capacity rejections: {observed.throttled}. Unknown operation outcomes: {observed.unknown_outcomes}, including {observed.unknown_pop_attempts} pop attempts. These do not establish whether the target processed a removed item.</p>
      <p>Recovery observation gaps: {observed.recovery_gaps}. {observed.incomplete ? 'Observation coverage is incomplete; a checkpoint cannot retain observations made after it.' : 'No recovery observation gap recorded.'}{observed.dropped_events > 0 && <> {observed.dropped_events} older event groups omitted.</>}</p>
      {!!observed.events?.length && <ul aria-label="Shared-data event history">{observed.events.slice(0, 16).map((event, index) => <li key={index}>{event.kind.replace(/_/g, ' ')}: {event.count} occurrences, {new Date(event.first).toLocaleString()} to {new Date(event.last).toLocaleString()}</li>)}</ul>}
    </>}
  </>
}

export default function LiveTableReportSummary({ tables }: { tables?: RunArchiveLiveTableSummary[] }) {
  if (!tables?.length) return null
  return <section className="report-section" aria-label="Live table run summary"><h2>Live data tables</h2>
    <p>Retained metadata and finalization outcomes. Row values and lease capabilities are excluded.</p>
    {tables.map(table => <article key={table.table_id}><h3>{table.alias}</h3>
      <p>{table.profile === 'native_queue' ? 'Native work queue' : table.profile === 'vts_columns' ? 'Shared columns' : table.profile} · {table.durability === 'durable' ? 'Durable recovery' : 'Memory only'} · {table.state} · Revision {table.state === 'unavailable' ? 'Unknown' : table.revision} · End policy: {({ discard: 'Discard runtime changes', save_as_new: 'Save as a new data pool', write_back: 'Write back to the source pool' } as Record<string, string>)[table.end_mode] || table.end_mode}</p>
      <p>Lease redeliveries: {table.counts?.redeliveries ?? 'Not recorded'}.</p>
      <div className="data-table"><table className="report-table"><caption>Record reconciliation for {table.alias}</caption><thead><tr>{['Seeded', 'Added', 'Available', 'Leased', 'Acknowledged', 'Removed', 'Quarantined', 'Discarded', 'Delivery attempts'].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead>
        <tbody><tr>{(['seeded', 'pushed', 'available', 'leased', 'acknowledged', 'popped', 'quarantined', 'discarded', 'delivery_attempts'] as const).map(key => <td key={key}>{table.state === 'unavailable' ? 'Unknown' : table.counts?.[key] ?? 'Unknown'}</td>)}</tr></tbody>
      </table></div>
      <p>Finalization: {table.finalization?.state || 'Not recorded'}.<LiveTableFinalizationOutcome finalization={table.finalization} mode={table.end_mode} /></p>
      <LiveTableOperationSummary stats={table} />
    </article>)}
  </section>
}
