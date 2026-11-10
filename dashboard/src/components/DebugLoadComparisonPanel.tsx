import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type { DebugCompletionSnapshot } from './debugCompletionModel'
import type { ScriptLoadRun, ScriptLoadRunsResponse } from '../types/scriptLoadRuns'
import { formatDateTime } from '../utils/dateTime'
import {
  AMPLIFICATION_PHASES,
  AMPLIFICATION_PHASE_LABELS,
  AMPLIFICATION_STATISTICS,
  AMPLIFICATION_STATISTIC_LABELS,
  ANATOMY_MISS_LABELS,
  NOT_COMPARABLE_LABELS,
  buildAmplificationModel,
  formatFactor,
  formatPhaseMs,
  type AmplificationFactor,
  type AmplificationLoadReport,
  type AmplificationRow,
  type AmplificationStatistic,
  amplificationLoadReportOf,
} from './debugLoadAmplificationModel'

/** Load run reports kept while the tab is open (only the fields compared). */
const MAX_CACHED_REPORTS = 8

/** Loads the script's recent load runs and one run's report. */
export interface DebugLoadComparisonSource {
  listLoadRuns: () => Promise<ScriptLoadRunsResponse>
  getLoadReport: (testId: string) => Promise<AmplificationLoadReport>
}

type Loadable<T> = { state: 'idle' } | { state: 'loading' } | { state: 'error'; message: string } | { state: 'ready'; value: T }

const errorMessage = (error: unknown, fallback: string) => error instanceof Error && error.message ? error.message : fallback

function runLabel(run: ScriptLoadRun): string {
  const parts = [run.name || run.test_id]
  const at = run.started_at || run.completed_at
  if (at) parts.push(formatDateTime(at))
  parts.push(run.status)
  if (run.script_shakeout) parts.push('shakeout')
  if (!run.archive_available) parts.push('no archive')
  return parts.join(' · ')
}

const FACTOR_MEANINGS = {
  new: 'About zero in debug; load spent time here.',
  na: 'Not measured on one side, or too small to compare.',
} as const

function FactorText({ factor, suffix }: { factor: AmplificationFactor; suffix?: string }) {
  const meaning = factor.kind === 'ratio' ? undefined : FACTOR_MEANINGS[factor.kind]
  return <strong className={`debug-load-factor debug-load-factor-${factor.kind}`} title={meaning}>
    {formatFactor(factor)}
    {/* The title is not reachable by keyboard, touch or every screen reader. */}
    {meaning && <span className="sr-only"> ({meaning})</span>}
    {suffix && <span className="debug-load-factor-note"> {suffix}</span>}
  </strong>
}

/** Why a request in a debug transaction had no anatomy row, or that it ran outside one. */
function missText(row: AmplificationRow): string {
  if (!row.claimedBy) return 'outside a transaction'
  return `${row.claimedBy}: ${ANATOMY_MISS_LABELS[row.anatomyMiss ?? 'no_row']}`
}

function basisText(row: AmplificationRow, statistic: AmplificationStatistic): string {
  switch (row.basis) {
    case 'anatomy':
      return `${row.transactionName} · ${AMPLIFICATION_STATISTIC_LABELS[statistic]}${row.fallbackKey ? ' · matched by name' : ''}${row.occurrence && row.occurrence > 1 ? ` · #${row.occurrence}` : ''}${row.loadNoFirstBytePct != null ? ` · ${Math.round(row.loadNoFirstBytePct)}% no TTFB split under load` : ''}${row.noTtfbSplit ? ' · no TTFB split' : ''}`
    case 'mean_only':
      return `Mean only (${missText(row)})`
    case 'unmatched':
      return row.claimedBy ? `No matching request in this load run (${missText(row)})` : 'No matching request in this load run'
    case 'not_comparable':
      return NOT_COMPARABLE_LABELS[row.debug.notComparable ?? 'total_only']
  }
}

export default function DebugLoadComparisonPanel({ snapshot, source, active, stale }: {
  snapshot: DebugCompletionSnapshot
  source: DebugLoadComparisonSource
  /** Nothing is fetched until the sub-tab is first shown. */
  active: boolean
  stale?: boolean
}) {
  const runId = useId()
  const statId = useId()
  const [runs, setRuns] = useState<Loadable<ScriptLoadRun[]>>({ state: 'idle' })
  const [selected, setSelected] = useState('')
  const [statistic, setStatistic] = useState<AmplificationStatistic>('p95')
  const [report, setReport] = useState<Loadable<AmplificationLoadReport>>({ state: 'idle' })
  const reports = useRef(new Map<string, AmplificationLoadReport>())
  // The editor rebuilds the source each render; requests use the latest one.
  const sourceRef = useRef(source)
  useEffect(() => { sourceRef.current = source })

  const [listAttempt, setListAttempt] = useState(0)
  const [reportAttempt, setReportAttempt] = useState(0)
  const listed = useRef(false)
  useEffect(() => {
    if (!active || listed.current) return
    listed.current = true
    let cancelled = false
    let settled = false
    setRuns({ state: 'loading' })
    sourceRef.current.listLoadRuns()
      .then(response => {
        settled = true
        if (cancelled) return
        const list = response?.runs || []
        setRuns({ state: 'ready', value: list })
        // A shakeout is a single unloaded VUser: prefer a real load run.
        const archived = list.filter(run => run.archive_available)
        setSelected((archived.find(run => !run.script_shakeout) || archived[0])?.test_id || '')
      })
      .catch(error => {
        settled = true
        // A failed list is asked again on Retry.
        listed.current = false
        if (!cancelled) setRuns({ state: 'error', message: errorMessage(error, 'Load runs could not be listed.') })
      })
    return () => {
      cancelled = true
      // A request dropped before it answered is asked again on the next run.
      if (!settled) listed.current = false
    }
  }, [active, listAttempt])

  useEffect(() => {
    if (!selected) { setReport({ state: 'idle' }); return }
    const cached = reports.current.get(selected)
    if (cached) { setReport({ state: 'ready', value: cached }); return }
    let cancelled = false
    setReport({ state: 'loading' })
    sourceRef.current.getLoadReport(selected)
      .then(response => {
        const value = amplificationLoadReportOf(response)
        const cache = reports.current
        cache.delete(selected)
        cache.set(selected, value)
        // Oldest first: a Map iterates in insertion order.
        while (cache.size > MAX_CACHED_REPORTS) cache.delete(cache.keys().next().value!)
        if (!cancelled) setReport({ state: 'ready', value })
      })
      .catch(error => { if (!cancelled) setReport({ state: 'error', message: errorMessage(error, 'The load run report could not be loaded.') }) })
    return () => { cancelled = true }
  }, [selected, reportAttempt])

  const runList = runs.state === 'ready' ? runs.value : []
  const selectedRun = runList.find(run => run.test_id === selected)
  const shakeout = Boolean(selectedRun?.script_shakeout)
  const model = useMemo(() => report.state === 'ready' ? buildAmplificationModel(snapshot, report.value, statistic, { shakeout }) : null, [report, snapshot, statistic, shakeout])

  return <section aria-label="Debug vs load" className="debug-load-comparison">
    <h3>Debug vs load</h3>
    <p className="debug-load-intro">Each request of this debug run against the same request in a load run of this script. The factor is load ÷ debug per phase at the chosen statistic; the Server column is the server wait at that statistic too. The Network column (DNS, connect, TLS and download together) and the headline compare means: phase percentiles do not add up.</p>
    <ul className="debug-load-caveats" aria-label="Comparison caveats">
      <li>Debug runs from the controller and load runs from generators, so the network path differs: network factors include that difference.</li>
      <li>The script revision cannot be verified: runs are matched by script only.{snapshot.draft ? ' This debug run used an unsaved draft.' : ''}{stale ? ' These results describe an earlier draft.' : ''}</li>
      <li>Percentiles come from the load report's transaction anatomy. Requests it does not list (outside a transaction, or left out by its caps) compare with run-wide means.</li>
      <li>The load side covers every group of the load run, including groups running other scripts: same-named transactions and requests are merged.</li>
      {model && model.counts.meanOnly > 0 && <li>Run-wide means estimate server as mean TTFB minus mean setup. Requests that got no first byte add setup time but no TTFB, so mean-only server is understated when the run had timeouts or resets.</li>}
      {shakeout && <li>The selected run is a script shakeout: one VUser, not a loaded run. Its factors show run-to-run variation, not load.</li>}
    </ul>
    {runs.state === 'loading' && <p role="status">Loading load runs…</p>}
    {runs.state === 'error' && <div role="alert" className="error-message">
      <p>{runs.message}</p>
      <button type="button" className="btn btn-secondary btn-sm" onClick={() => setListAttempt(attempt => attempt + 1)}>Retry</button>
    </div>}
    {runs.state === 'ready' && runList.length === 0 && <p>No finished load runs of this script in this project yet. Run a load test with this script to compare.</p>}
    {runList.length > 0 && <div className="debug-summary-issues-toolbar">
      <div className="debug-summary-issues-field">
        <label htmlFor={runId}>Load run</label>
        <select id={runId} value={selected} onChange={event => setSelected(event.target.value)}>
          {!selected && <option value="">Choose a load run</option>}
          {runList.map(run => <option key={run.test_id} value={run.test_id} disabled={!run.archive_available}>{runLabel(run)}</option>)}
        </select>
      </div>
      <div className="debug-summary-issues-field">
        <label htmlFor={statId}>Load statistic</label>
        <select id={statId} value={statistic} onChange={event => setStatistic(event.target.value as AmplificationStatistic)}>
          {AMPLIFICATION_STATISTICS.map(value => <option key={value} value={value}>{AMPLIFICATION_STATISTIC_LABELS[value]}</option>)}
        </select>
      </div>
    </div>}
    {runList.length > 0 && !runList.some(run => run.archive_available) && <p>None of these runs has an archived report to compare with.</p>}
    {selectedRun && selectedRun.groups.length > 0 && <p className="debug-load-run-groups">Groups using this script: {selectedRun.groups.join(', ')}{selectedRun.vusers ? ` · ${selectedRun.vusers.toLocaleString()} VUsers` : ''}</p>}
    {report.state === 'loading' && <p role="status">Loading the load run report…</p>}
    {report.state === 'error' && <div role="alert" className="error-message">
      <p>{report.message}</p>
      <button type="button" className="btn btn-secondary btn-sm" onClick={() => setReportAttempt(attempt => attempt + 1)}>Retry</button>
    </div>}
    {model && !model.hasLoadPhaseData && <p>This load run's report has no per-request phase data to compare with.</p>}
    {model && model.hasLoadPhaseData && model.rows.length === 0 && <p>This debug run retained no requests to compare.</p>}
    {model && model.hasLoadPhaseData && model.rows.length > 0 && <>
      <p role="status" className="debug-load-headline">{model.summary.headline}</p>
      <p className="debug-load-counts">
        {model.counts.anatomy} by transaction · {model.counts.meanOnly} mean only · {model.counts.unmatched} unmatched · {model.counts.notComparable} not comparable
      </p>
      <p className="debug-load-legend"><strong>new</strong>: {FACTOR_MEANINGS.new} <strong>n/a</strong>: {FACTOR_MEANINGS.na}{model.rows.some(row => row.networkSetupOnly) ? <> <strong>setup only</strong>: download was not measured on one side (run-wide means have no download split), so Network is DNS, connect and TLS.</> : null}</p>
      <div className="debug-transaction-table-wrap" tabIndex={0} role="region" aria-label="Debug vs load requests">
        <table className="debug-transaction-table debug-load-table" data-table-pagination="off">
          <caption className="sr-only">Per-request phases: debug value, load value and the load amplification factor</caption>
          <thead>
            <tr>
              <th scope="col">Request</th>
              <th scope="col">Compared with</th>
              {AMPLIFICATION_PHASES.map(phase => <th key={phase} scope="col">{AMPLIFICATION_PHASE_LABELS[phase]}</th>)}
              <th scope="col">Server</th>
              <th scope="col">Network</th>
            </tr>
          </thead>
          <tbody>
            {model.rows.map(row => (
              <tr key={row.key} className={`debug-load-row debug-load-row-${row.basis}`}>
                <th scope="row">{row.debug.actionName || 'Request'}</th>
                <td>{basisText(row, statistic)}</td>
                {AMPLIFICATION_PHASES.map(phase => (
                  <td key={phase}>
                    {row.load && row.debug.phases ? <>
                      <span className="debug-load-values"><span className="debug-load-value">{formatPhaseMs(row.debug.phases[phase])}</span> → <span className="debug-load-value">{formatPhaseMs(row.load[phase])}</span></span>{' '}
                      <FactorText factor={row.factors[phase]} />
                    </> : '—'}
                  </td>
                ))}
                <td><FactorText factor={row.server} /></td>
                <td><FactorText factor={row.network} suffix={row.networkSetupOnly && row.network.kind !== 'na' ? 'setup only' : undefined} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>}
  </section>
}
