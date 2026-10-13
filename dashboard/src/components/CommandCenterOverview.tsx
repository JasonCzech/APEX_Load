import { useState } from 'react'
import type { Release, TestSummary } from '../types'
import StatusBadge from './StatusBadge'
import { commandRunHealth, commandRunThroughput, commandRunTiming, commandRunWaiting, commandRunWaitReason, filterCommandRuns } from './commandCenterOverviewModel'
import { recordedTime } from './runReport/executionPresentation'
import LibraryPagination from './LibraryPagination'
import { useLibraryPagination } from './useLibraryPagination'
import './CommandCenterOverview.css'

export interface CommandCenterOverviewProps {
  runs: TestSummary[]
  loading: boolean
  error: string
  updatedAt?: number
  scopeLabel: string
  scopeReady: boolean
  onRefresh: () => void
  onSelectRun: (id: string) => void
  onNewTest?: () => void
  newTestDisabledReason?: string
  onBrowseScenarios?: () => void
  onOpenResults?: () => void
  releases?: Release[]
  releaseId?: string
  onSelectRelease?: (release: Release) => void
  onOpenRepository?: () => void
}

export default function CommandCenterOverview(props: CommandCenterOverviewProps) {
  const { runs, loading, error, updatedAt, scopeLabel, scopeReady, onRefresh, onSelectRun } = props
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('all')
  const [sort, setSort] = useState('attention')
  const now = Date.now()
  const filtered = filterCommandRuns(runs, search, status, sort, now)
  const pagination = useLibraryPagination(filtered.length, `${scopeLabel}\u001f${search}\u001f${status}\u001f${sort}`)
  const visible = filtered.slice(pagination.start, pagination.end)
  const statuses = [...new Set(runs.map(run => run.status))].sort()
  const waiting = runs.filter(commandRunWaiting).length
  const empty = scopeReady && !loading && !error && runs.length === 0
  const missingRelease = Boolean(props.onSelectRelease && !props.releaseId)
  return <section className="command-overview" aria-label="Active run overview">
    <div className="command-overview-heading">
      <div><h2>{!scopeReady ? 'Choose a workspace' : empty ? 'No active runs' : 'Active runs'}</h2><p>{scopeLabel}</p></div>
      <div className="command-overview-actions">
        {props.onNewTest && <button className="btn btn-primary" type="button" disabled={Boolean(props.newTestDisabledReason)} title={props.newTestDisabledReason} onClick={props.onNewTest}>New run</button>}
        {props.onBrowseScenarios && <button className="btn btn-secondary" type="button" onClick={props.onBrowseScenarios}>Browse scenarios</button>}
        <button className="btn btn-secondary" type="button" disabled={!scopeReady || loading} onClick={onRefresh}>{error ? 'Retry active runs' : 'Refresh runs'}</button>
      </div>
    </div>
    {props.newTestDisabledReason && <p>{props.newTestDisabledReason}</p>}
    {!scopeReady && <p>Select a project in the sidebar to discover its active runs.</p>}
    {error && <div className="page-error-banner" role="alert">Unable to refresh active runs: {error}. {updatedAt ? 'Showing the last successful list; statuses and metrics may have changed.' : 'Active runs could not be determined. Retry to check this workspace.'}</div>}
    {scopeReady && <p role="status">{updatedAt ? `${error ? 'Stale list' : 'Run list'} · Last updated ${new Date(updatedAt).toLocaleTimeString()}` : loading ? 'Looking for active runs…' : error ? 'Run list unavailable' : 'No run list loaded'}{loading && updatedAt ? ' · Refreshing…' : ''}</p>}
    {empty && <p>Start a new run or choose a saved scenario. Live controls and metrics appear when you open its run.</p>}
    {runs.length > 0 && <>
      <p>{runs.length} active {runs.length === 1 ? 'run' : 'runs'} · {runs.length - waiting} executing or paused · {waiting} waiting or scheduled</p>
      <div className="command-overview-filters">
        <label>Find a run<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Name or run reference" /></label>
        <label>Status<select value={status} onChange={event => setStatus(event.target.value)}><option value="all">All statuses</option>{[...new Set([...statuses, ...(status === 'all' ? [] : [status])])].map(value => <option key={value} value={value}>{value}</option>)}</select></label>
        <label>Sort runs<select value={sort} onChange={event => setSort(event.target.value)}><option value="attention">Needs attention first</option><option value="recent">Most recent first</option><option value="name">Name</option></select></label>
        {(search || status !== 'all') && <button className="btn btn-secondary" type="button" onClick={() => { setSearch(''); setStatus('all') }}>Clear filters</button>}
      </div>
      <LibraryPagination label="Active runs" pagination={pagination} />
      {filtered.length === 0 && <p role="status">No runs match these filters.</p>}
      {visible.length > 0 && <div className="command-overview-table"><table>
        <caption>Run health and live metrics. Elapsed and remaining time leave out pauses, which a run makes up.</caption>
        <thead><tr><th scope="col">Run</th><th scope="col">Status / health</th><th scope="col">Current / planned VU</th><th scope="col">Throughput (last sample)</th><th scope="col">Error % (run to date)</th><th scope="col">Elapsed / remaining</th><th scope="col">Metrics updated</th></tr></thead>
        <tbody>{visible.map(run => {
          const health = commandRunHealth(run, now)
          const timing = commandRunTiming(run, now)
          const metricTime = recordedTime(run.live_metrics?.timestamp)
          const waitingRun = commandRunWaiting(run)
          const throughput = commandRunThroughput(run)
          return <tr key={run.id}>
            <td data-label="Run"><button type="button" className="command-overview-run" onClick={() => onSelectRun(run.id)} aria-label={`Open ${run.name} (${run.ref_key || run.id})`}><strong>{run.name}</strong><small>{run.ref_key || run.id}</small></button>{waitingRun && <small>{commandRunWaitReason(run)}</small>}</td>
            <td data-label="Status / health"><StatusBadge status={run.status} /><small className={health.priority < 2 ? 'command-overview-warning' : ''}>{health.label}</small></td>
            <td data-label="Current / planned VU">{waitingRun ? 'Not started' : number(run.live_metrics?.active_vusers)} / {number(run.total_vusers)}</td>
            <td data-label="Throughput (last sample)">{health.hasSamples && !waitingRun ? `${number(throughput.rate, 1)} ${throughput.unit}` : '—'}</td>
            <td data-label="Error % (run to date)">{waitingRun ? '—' : number(health.errorPct, 2)}</td>
            <td data-label="Elapsed / remaining">{timing.elapsed}<small>{timing.remaining}{run.termination_mode === 'iterations' ? '' : ' remaining'}</small></td>
            <td data-label="Metrics updated">{waitingRun || metricTime === undefined ? 'Unavailable' : new Date(metricTime).toLocaleTimeString()}</td>
          </tr>
        })}</tbody>
      </table></div>}
    </>}
    <div className="command-overview-results">
      {missingRelease && <>
        <label>Release for Results<select value="" disabled={!props.releases?.length} onChange={event => { const release = props.releases?.find(item => item.id === event.target.value); if (release) props.onSelectRelease?.(release) }}><option value="">Choose a release</option>{props.releases?.map(release => <option key={release.id} value={release.id}>{release.name}</option>)}</select></label>
        {!props.releases?.length && <p>No releases are available in this application. {props.onOpenRepository && <button className="btn btn-secondary" type="button" onClick={props.onOpenRepository}>Open Repository</button>}</p>}
      </>}
      {props.onOpenResults && <button className="btn btn-secondary" type="button" disabled={missingRelease || !scopeReady} onClick={props.onOpenResults}>Open Results</button>}
    </div>
  </section>
}

function number(value?: number, digits = 0) {
  return value === undefined || !Number.isFinite(value) || value < 0 ? '—' : value.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits })
}
