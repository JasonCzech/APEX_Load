import { Fragment, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'

import type { ReportLens } from '../../appRouting'
import type { IterationReplayTarget } from '../../types/iterationReplay'
import type { RunArchiveAnatomyRequest, RunArchivePhaseStat, RunArchiveRepresentativeSample, RunArchiveTimeAnatomy, RunArchiveTransactionAnatomy } from '../../types/timeAnatomy'
import PercentileQualityMark from '../PercentileQualityMark'
import { Waterfall } from '../waterfall/Waterfall'
import RepresentativeTransactionDrawer from './RepresentativeTransactionDrawer'
import TailAttributionChart from './TailAttributionChart'
import {
  AGGREGATE_STATISTICS,
  ANATOMY_PHASE_DEFINITIONS,
  ANATOMY_PHASE_LABELS,
  COVERAGE_LABELS,
  EMPTY_TRANSACTIONS_COPY,
  STATISTIC_LABELS,
  UNSPLIT_ONLY_COPY,
  aggregateRoots,
  anatomyAnnouncement,
  aggregateSpanMs,
  clampedNotice,
  compositionSentence,
  coverageNotices,
  defaultStatistic,
  formatAnatomyMs,
  formatCount,
  honestyNote,
  othersRoot,
  phaseDefinitionsText,
  pickAnatomyTransaction,
  presentPhaseKeys,
  reportPhaseRowId,
  representativeChipLabel,
  rowsHaveOnlyUnsplit,
  selectionNote,
  truncationNotices,
  truncationReasonCopy,
  notJoinedCopy,
  type AggregateStatistic,
} from './timeAnatomyModel'
import './TimeAnatomy.css'

export const ANATOMY_STAT_STORAGE_KEY = 'apexload.report.anatomyStat'
const PICKER_FILTER_THRESHOLD = 15
const ROW_ID_PREFIX = 'rr-anatomy-row'
// Aggregate rows are independent statistics placed at their median offsets: no session timeline, no idle time.
const AGGREGATE_SCALE_LABELS = { waterfall: 'Median offsets' } as const
const AGGREGATE_SORT_MODES = ['chronological', 'slowest', 'dominant'] as const
const NO_SINKS: never[] = []

interface Props {
  testId: string
  anatomy: RunArchiveTimeAnatomy
  /** The report's selected transaction (SLA table, Summary headline). */
  selectedName?: string
  /**
   * When given, the picker reports its choice here instead of keeping a local
   * pick, so the report's selection stays the one source of truth (re-selecting
   * the same SLA row then still shows that row's transaction).
   */
  onSelectName?: (name: string) => void
  lens: ReportLens
  /** Opens a representative's iteration in the iteration replay. */
  onOpenIteration?: (target: IterationReplayTarget) => void
}

function readStoredStatistic(): AggregateStatistic | null {
  try {
    const value = window.localStorage.getItem(ANATOMY_STAT_STORAGE_KEY)
    return AGGREGATE_STATISTICS.includes(value as AggregateStatistic) ? value as AggregateStatistic : null
  } catch {
    return null
  }
}

function storeStatistic(value: AggregateStatistic) {
  try {
    window.localStorage.setItem(ANATOMY_STAT_STORAGE_KEY, value)
  } catch {
    // Storage is a convenience: a blocked or full store keeps the choice for this view only.
  }
}

function chipAriaLabel(sample: RunArchiveRepresentativeSample): string {
  const parts = [representativeChipLabel(sample)]
  if (sample.reason === 'failed') parts.push(formatAnatomyMs(sample.duration_ms))
  if (sample.vuser_id) parts.push(`VUser ${sample.vuser_id}`)
  if (sample.iteration) parts.push(`iteration ${sample.iteration}`)
  return `${parts.join(', ')}. Open its waterfall`
}

const PHASE_TABLE_ROWS: readonly [keyof RunArchiveAnatomyRequest['phases'], string, string?][] = [
  ['dns', ANATOMY_PHASE_LABELS.dns], ['connect', ANATOMY_PHASE_LABELS.connect], ['tls', ANATOMY_PHASE_LABELS.tls],
  ['server', ANATOMY_PHASE_LABELS.wait, ANATOMY_PHASE_DEFINITIONS.wait], ['download', ANATOMY_PHASE_LABELS.receive],
  ['unsplit', ANATOMY_PHASE_LABELS.total, ANATOMY_PHASE_DEFINITIONS.total], ['total', 'Total'],
]

function PhaseStatCells({ stat }: { stat: RunArchivePhaseStat }) {
  const cell = (value: number) => <><PercentileQualityMark quality={stat.percentile_quality} value={value} />{formatAnatomyMs(value)}</>
  return <>
    <td>{formatAnatomyMs(stat.avg_ms)}</td>
    <td>{cell(stat.p50_ms)}</td>
    <td>{cell(stat.p90_ms)}</td>
    <td>{cell(stat.p95_ms)}</td>
    <td>{cell(stat.p99_ms)}</td>
    <td>{formatAnatomyMs(stat.max_ms)}</td>
    <td>{stat.observed_pct === undefined ? '—' : `${Math.round(stat.observed_pct)}%`}</td>
    <td>{stat.avg_when_observed_ms === undefined ? '—' : formatAnatomyMs(stat.avg_when_observed_ms)}</td>
  </>
}

function RowPane({ row }: { row: RunArchiveAnatomyRequest }) {
  const c = row.connections
  return <section className="rr-anatomy-row-pane" aria-label={`${row.action_name} phase statistics`}>
    <h4>{row.action_name}{row.occurrence > 1 ? ` #${row.occurrence}` : ''}</h4>
    <dl className="rr-anatomy-facts">
      <dt>Samples</dt><dd>{formatCount(row.samples)}</dd>
      <dt>Failed</dt><dd>{formatCount(row.failed)}</dd>
      <dt>In instances</dt><dd>{Math.round(row.presence_pct)}%</dd>
      {row.source_step_index ? <><dt>Step</dt><dd>{row.source_step_index}</dd></> : null}
      {row.dependency_type && <><dt>Type</dt><dd>{row.dependency_type}</dd></>}
      <dt>Connections</dt><dd>{formatCount(c?.new)} new · {formatCount(c?.reused)} reused · {formatCount(c?.unknown)} unknown{c?.reuse_pct !== undefined ? ` · reuse ${Math.round(c.reuse_pct)}%` : ''}{c?.inconsistent ? ` · ${formatCount(c.inconsistent)} inconsistent` : ''}</dd>
      {row.occurrence_folded && <><dt>Occurrence</dt><dd>Repeats from #{row.occurrence} on are folded together</dd></>}
    </dl>
    <div className="rr-anatomy-table-scroll"><table className="tx-table">
      <thead><tr><th scope="col">Phase</th><th scope="col">Avg</th><th scope="col">P50</th><th scope="col">P90</th><th scope="col">P95</th><th scope="col">P99</th><th scope="col">Max</th><th scope="col">Observed</th><th scope="col">Avg when observed</th></tr></thead>
      <tbody>{PHASE_TABLE_ROWS.map(([key, label, definition]) => row.phases?.[key] && <tr key={key}><th scope="row" title={definition}>{label}</th><PhaseStatCells stat={row.phases[key]} /></tr>)}</tbody>
    </table></div>
  </section>
}

export function StatisticRadios({ value, onChange }: { value: AggregateStatistic; onChange: (next: AggregateStatistic) => void }) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const last = AGGREGATE_STATISTICS.length - 1
    const next = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? (index === last ? 0 : index + 1)
      : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? (index === 0 ? last : index - 1)
        : event.key === 'Home' ? 0 : event.key === 'End' ? last : -1
    if (next < 0) return
    event.preventDefault()
    onChange(AGGREGATE_STATISTICS[next])
    refs.current[next]?.focus()
  }
  return <div className="rr-anatomy-stat" role="radiogroup" aria-label="Statistic per phase">
    {AGGREGATE_STATISTICS.map((stat, index) => <button key={stat} ref={node => { refs.current[index] = node }} type="button" role="radio"
      aria-checked={stat === value} tabIndex={stat === value ? 0 : -1} className={stat === value ? 'active' : ''}
      onClick={() => onChange(stat)} onKeyDown={event => onKeyDown(event, index)}>{STATISTIC_LABELS[stat]}</button>)}
  </div>
}

/** R1-R3 for business transactions: where each transaction's time goes. */
export default function TransactionTimeAnatomy({ testId, anatomy, selectedName, onSelectName, lens, onOpenIteration }: Props) {
  const headingId = useId()
  const pickerId = useId()
  const [pickedName, setPickedName] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [storedStatistic, setStoredStatistic] = useState<AggregateStatistic | null>(() => readStoredStatistic())
  const [showBreakdown, setShowBreakdown] = useState(false)
  const [selectedRowKey, setSelectedRowKey] = useState('')
  const [drawerSample, setDrawerSample] = useState<RunArchiveRepresentativeSample | null>(null)
  const [announce, setAnnounce] = useState(false)
  // Each contributor click bumps the tick, so a repeat click scrolls again and a
  // later bar click (which does not bump it) never replays a stale scroll.
  const [scrollTick, setScrollTick] = useState(0)
  const pendingScrollRef = useRef('')

  // A new selection from the report (the SLA table, the Summary line) wins over the local picker.
  useEffect(() => { setPickedName(null) }, [selectedName])

  const transactions = useMemo(() => anatomy.transactions || [], [anatomy.transactions])
  const picked = pickedName ? transactions.find(entry => entry.name === pickedName) : undefined
  const { entry: fallbackEntry, matchedSelection } = pickAnatomyTransaction(anatomy, selectedName)
  const entry: RunArchiveTransactionAnatomy | undefined = picked || fallbackEntry
  const statistic = storedStatistic ?? defaultStatistic(lens)
  const breakdownVisible = lens !== 'executive' || showBreakdown

  useEffect(() => { setSelectedRowKey('') }, [entry?.name])

  useEffect(() => {
    const key = pendingScrollRef.current
    if (!key || !breakdownVisible) return // nothing asked to scroll, or its row is not drawn yet
    pendingScrollRef.current = ''
    document.getElementById(reportPhaseRowId(ROW_ID_PREFIX, key))?.scrollIntoView?.({ block: 'nearest' })
  }, [breakdownVisible, scrollTick])

  const roots = useMemo(() => {
    if (!entry) return []
    const rows = aggregateRoots(entry, statistic)
    const others = othersRoot(entry, statistic)
    return others ? [...rows, others] : rows
  }, [entry, statistic])
  const legendPhases = useMemo(() => presentPhaseKeys(roots), [roots])
  // The selection before the click being handled: a click on the selected row clears it.
  const selectedRowKeyRef = useRef(selectedRowKey)
  selectedRowKeyRef.current = selectedRowKey

  const chooseStatistic = (next: AggregateStatistic) => {
    setStoredStatistic(next)
    storeStatistic(next)
  }

  const selectContributor = (key: string) => {
    setShowBreakdown(true)
    setSelectedRowKey(key)
    pendingScrollRef.current = key
    setScrollTick(tick => tick + 1)
  }

  const pickTransaction = (name: string) => {
    setAnnounce(true)
    if (onSelectName) {
      setPickedName(null)
      onSelectName(name)
    } else {
      setPickedName(name)
    }
  }

  // Always mounted, so a screen reader is already watching it when the text changes.
  const liveRegion = <div className="sr-only" aria-live="polite" aria-atomic="true">{announce ? anatomyAnnouncement(entry) : ''}</div>

  if (!entry) {
    return <section className="run-report-panel run-report-time-anatomy" data-report-focus="time-anatomy" aria-labelledby={headingId}>
      {liveRegion}
      <h2 id={headingId}>Where the time goes</h2>
      <p>{EMPTY_TRANSACTIONS_COPY}</p>
    </section>
  }

  const filterLower = filter.trim().toLowerCase()
  const options = transactions.length > PICKER_FILTER_THRESHOLD && filterLower
    ? transactions.filter(item => item.name === entry.name || item.name.toLowerCase().includes(filterLower))
    : transactions
  const showSelectionNote = Boolean(selectedName) && !matchedSelection && !picked
  const joined = entry.joined_instances > 0
  const selectedRow = selectedRowKey === 'other-requests' ? entry.other_requests : entry.requests.find(row => row.key === selectedRowKey)
  const notices = truncationNotices(anatomy, entry)
  const coverage = coverageNotices(anatomy, lens)
  const clamped = lens !== 'executive' ? clampedNotice(anatomy) : null
  const composition = compositionSentence(entry.name, entry.decomposition)
  const representatives = entry.representatives || []
  const durationQuality = entry.duration.percentile_quality

  return (
    <section className="run-report-panel run-report-time-anatomy" data-report-focus="time-anatomy" aria-labelledby={headingId}>
      {liveRegion}
      <header className="rr-anatomy-header">
        <h2 id={headingId}>Where the time goes</h2>
        <div className="rr-anatomy-picker">
          {transactions.length > PICKER_FILTER_THRESHOLD && <input type="search" autoComplete="off" aria-label="Filter transactions" placeholder="Filter transactions" value={filter} onChange={event => setFilter(event.currentTarget.value)} />}
          <label htmlFor={pickerId}>Transaction</label>
          <select id={pickerId} value={entry.name} onChange={event => pickTransaction(event.currentTarget.value)}>
            {options.map(item => <option key={item.name} value={item.name}>{item.name}{item.sla_status === 'failed' ? ' (SLA failed)' : ''}</option>)}
          </select>
        </div>
        <p className="rr-anatomy-duration">
          Duration P50 <PercentileQualityMark quality={durationQuality} value={entry.duration.p50_ms} />{formatAnatomyMs(entry.duration.p50_ms)} · P95 <PercentileQualityMark quality={durationQuality} value={entry.duration.p95_ms} />{formatAnatomyMs(entry.duration.p95_ms)} · n={formatCount(entry.instances)}
        </p>
      </header>
      {showSelectionNote && <p className="rr-anatomy-note">{selectionNote(entry.name)}</p>}
      {notices.map(notice => <p key={notice} className="rr-anatomy-callout">{notice}</p>)}

      {!joined && <p className="rr-anatomy-callout">{notJoinedCopy(entry)}</p>}
      {joined && entry.tail && <TailAttributionChart tail={entry.tail} lens={lens} onSelectRequest={selectContributor} />}

      {representatives.length > 0 && <div className="rr-anatomy-chips" role="group" aria-label="Representative instances">
        {representatives.map(sample => <button key={`${sample.reason}-${sample.event_id}`} type="button"
          className={`btn btn-secondary btn-sm rr-anatomy-chip${sample.reason === 'failed' ? ' rr-anatomy-chip-failed' : ''}`}
          aria-label={chipAriaLabel(sample)} onClick={() => setDrawerSample(sample)}>{representativeChipLabel(sample)}</button>)}
      </div>}

      {joined && composition && <p className="rr-anatomy-composition">{composition}</p>}

      {joined && !breakdownVisible && <button type="button" className="btn btn-secondary btn-sm" aria-expanded={false} onClick={() => setShowBreakdown(true)}>Show request breakdown</button>}
      {joined && breakdownVisible && <div className="rr-anatomy-breakdown">
        <div className="rr-anatomy-breakdown-toolbar">
          <h3>Requests inside {entry.name}</h3>
          <StatisticRadios value={statistic} onChange={chooseStatistic} />
          {lens === 'executive' && <button type="button" className="btn btn-ghost btn-sm" aria-expanded onClick={() => setShowBreakdown(false)}>Hide request breakdown</button>}
        </div>
        <p className="rr-anatomy-honesty">{honestyNote(entry, statistic)}</p>
        {rowsHaveOnlyUnsplit(entry) && <p className="rr-anatomy-callout">{UNSPLIT_ONLY_COPY}</p>}
        {legendPhases.some(key => ANATOMY_PHASE_DEFINITIONS[key]) && <p className="rr-anatomy-definitions">{phaseDefinitionsText(legendPhases, false)}</p>}
        <Waterfall<RunArchiveAnatomyRequest>
          // Another transaction compares other rows: start over (sort, zoom, hidden phases).
          key={entry.name}
          roots={roots}
          spanMs={aggregateSpanMs(roots)}
          ariaLabel={`Aggregate waterfall for ${entry.name}`}
          rowIdPrefix={ROW_ID_PREFIX}
          selectedId={selectedRowKey || null}
          onSelect={node => setSelectedRowKey(node.id)}
          onRowClick={node => { if (selectedRowKeyRef.current === node.id) setSelectedRowKey('') }}
          // Enter on the selected row clears it, as a second click does.
          onActivate={node => setSelectedRowKey(current => (current === node.id ? '' : node.id))}
          sinks={NO_SINKS}
          phaseLabels={ANATOMY_PHASE_LABELS}
          legendPhases={legendPhases}
          showIdleLegend={false}
          criticalPath={false}
          scaleLabels={AGGREGATE_SCALE_LABELS}
          sortModes={AGGREGATE_SORT_MODES}
          nameColumnLabel="Request"
          tooltipClassName="rr-anatomy-tooltip"
          className="rr-anatomy-waterfall"
          emptyMessage="No requests were matched to this transaction."
        />
        {selectedRow && <RowPane row={selectedRow} />}
      </div>}

      {(coverage.length > 0 || clamped) && <ul className="rr-anatomy-coverage" aria-label="Coverage">
        {coverage.map(notice => <li key={notice}>{notice}</li>)}
        {clamped && <li>{clamped}</li>}
      </ul>}

      {lens === 'audit' && <details className="rr-anatomy-audit">
        <summary>How this was computed</summary>
        <dl className="rr-anatomy-facts">
          <dt>Join rule</dt><dd>{anatomy.join_rule}: requests from the same VUser lifetime that start inside the transaction&apos;s window</dd>
          <dt>Phase model</dt><dd>{anatomy.phase_model}</dd>
          <dt>Instances</dt><dd>{formatCount(entry.instances)} ({formatCount(entry.joined_instances)} matched to requests, {formatCount(entry.partial_instances ?? 0)} may have lost requests, {formatCount(entry.failed_instances)} failed, {formatCount(entry.interrupted_instances)} interrupted)</dd>
          <dt>Percentiles</dt><dd>{durationQuality === 'approximate' ? 'Approximate (sketch, within 0.5%)' : 'Exact'}</dd>
          {entry.tail && <><dt>Tail bands</dt><dd>{entry.tail.band_estimate === 'sampled' ? `Sampled: ${formatCount(entry.tail.sampled_instances)} of ${formatCount(entry.tail.instances)} instances` : 'Exact: every completed instance'}</dd></>}
          {(Object.keys(COVERAGE_LABELS) as (keyof typeof COVERAGE_LABELS)[]).map(key => <Fragment key={key}><dt>{COVERAGE_LABELS[key]}</dt><dd>{formatCount(anatomy.coverage?.[key])}</dd></Fragment>)}
        </dl>
        {anatomy.truncation?.reasons?.length ? <>
          <h4>Limits reached</h4>
          <ul>{anatomy.truncation.reasons.map(reason => <li key={reason}>{truncationReasonCopy(reason)}</li>)}</ul>
        </> : <p>No limits were reached.</p>}
      </details>}

      {drawerSample && <RepresentativeTransactionDrawer testId={testId} transactionName={entry.name} sample={drawerSample} onClose={() => setDrawerSample(null)} onOpenIteration={onOpenIteration} />}
    </section>
  )
}
