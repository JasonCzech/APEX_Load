import { Fragment, useCallback, useLayoutEffect, useMemo, useRef, useState, type MouseEventHandler, type ReactNode, type Ref } from 'react'
import type { LiveMetrics } from '../types'
import { Waterfall } from './waterfall/Waterfall'
import { formatWaterfallMs } from './waterfall/phases'
import {
  LIVE_PHASE_STATISTIC_LABELS,
  PERCENTILE_PHASES,
  PERCENTILE_PHASE_LABELS,
  PHASES_NOT_REPORTED_TEXT,
  PHASE_PERCENTILES_NOT_REPORTED_TEXT,
  SETUP_UNMEASURED_TEXT,
  phasePercentileMs,
  reuseBasisText,
  reuseValueText,
  txObservationStats,
  txPercentilesReported,
  type LivePhaseStatistic,
} from './livePhaseModel'
import { isDegradedLivePercentileQuality, percentileQualityDescription } from '../utils/percentileQuality'
import { formatTimeOfDay } from '../utils/dateTime'
import { stableMinuteBaseline, type StableMinuteState } from './liveWindowRetention'
import {
  LIVE_WATERFALL_LEGEND_PHASES,
  LIVE_WATERFALL_MAX_ROWS,
  LIVE_WATERFALL_PHASE_DEFINITIONS,
  LIVE_WATERFALL_FINISHED_EMPTY_TEXT,
  LIVE_WATERFALL_PHASE_LABELS,
  LIVE_WATERFALL_RESTORED_TEXT,
  advanceLiveWaterfallHold,
  liveWaterfallHasPhasePercentiles,
  liveWaterfallNodes,
  liveWaterfallTransactionCount,
  liveWaterfallSinks,
  liveWaterfallSpanMs,
  stableMinuteNote,
  type LiveWaterfallHold,
  type LiveWaterfallPayload,
} from './liveWaterfallModel'
import './LiveWaterfallPanel.css'

interface Props {
  title: string
  latestMetrics?: LiveMetrics
  stableMinute?: StableMinuteState
  /** The run has ended: no further window will arrive, so a missing first stable minute never forms. */
  runFinished?: boolean
  className?: string
  dataChartId?: string
  cardRef?: Ref<HTMLElement>
  /** Header controls the chart grid adds to every card (actions, expand). */
  headerActions?: ReactNode
  onContextMenu?: MouseEventHandler<HTMLElement>
  onMouseEnter?: MouseEventHandler<HTMLElement>
  onMouseLeave?: MouseEventHandler<HTMLElement>
}

// Rows are independent aggregates that all start at 0: no session timeline, no chronology.
const LIVE_WATERFALL_SCALE_LABELS = { waterfall: 'Absolute (ms)' } as const
const LIVE_WATERFALL_SORT_MODES = ['slowest', 'dominant', 'change'] as const
const msText = (v: number | undefined) => (v === undefined ? 'None this window' : formatWaterfallMs(v))
const LIVE_WATERFALL_STATISTICS: readonly LivePhaseStatistic[] = ['avg', 'p50', 'p95', 'p99']

/** Each phase's percentiles over the samples that spent time in it. */
function PhasePercentileTable({ name, tx }: { name: string; tx: LiveWaterfallPayload['tx'] }) {
  const set = tx.phase_percentiles
  if (!set) return <p className="wf-drawer-status" role="note">{PHASE_PERCENTILES_NOT_REPORTED_TEXT}.</p>
  return (
    <>
      <table className="wf-headers live-waterfall-phase-table">
        <caption className="sr-only">Per-phase percentiles for {name}</caption>
        <thead><tr><th scope="col">Phase</th><th scope="col">Samples</th><th scope="col">P50</th><th scope="col">P90</th><th scope="col">P95</th><th scope="col">P99</th></tr></thead>
        <tbody>
          {PERCENTILE_PHASES.map(phase => {
            const row = set[phase]
            const cell = (statistic: 'p50' | 'p95' | 'p99') => {
              const ms = phasePercentileMs(set, phase, statistic)
              return ms === undefined ? '—' : formatWaterfallMs(ms)
            }
            return (
              <tr key={phase}>
                <th scope="row">{PERCENTILE_PHASE_LABELS[phase]}</th>
                <td>{row && row.samples > 0 ? row.samples.toLocaleString() : 'None this window'}</td>
                <td>{cell('p50')}</td>
                <td>{row && row.samples > 0 ? formatWaterfallMs(row.p90_ms) : '—'}</td>
                <td>{cell('p95')}</td>
                <td>{cell('p99')}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <p className="wf-drawer-status">Each phase's percentiles are over the samples that spent time in it: DNS, connect and TLS when that phase took time, server and download when a first byte arrived. They do not add up to the transaction's percentiles.</p>
    </>
  )
}

function LiveWaterfallDrawer({ payload, onClose }: { payload: LiveWaterfallPayload; onClose: () => void }) {
  const { tx, vector, baseline } = payload
  const stats = txObservationStats(tx)
  const total = vector.avgMs > 0 ? vector.avgMs : 1
  const rows: Array<[string, number, number | undefined, string | undefined]> = [
    ['DNS', vector.dns, baseline?.dns, undefined],
    ['Connect', vector.connect, baseline?.connect, undefined],
    ['TLS', vector.tls, baseline?.tls, undefined],
    [LIVE_WATERFALL_PHASE_LABELS.wait!, vector.server, baseline?.server, LIVE_WATERFALL_PHASE_DEFINITIONS.wait],
    ['Download', vector.download, baseline?.download, LIVE_WATERFALL_PHASE_DEFINITIONS.receive],
    [LIVE_WATERFALL_PHASE_LABELS.total!, vector.noResponse, baseline?.noResponse, LIVE_WATERFALL_PHASE_DEFINITIONS.total],
  ]
  const errorPct = tx.count > 0 ? (tx.errors / tx.count) * 100 : 0
  const phasesUnknown = vector.serverSource === 'unknown'
  const percentilesReported = txPercentilesReported(tx)
  const quality = isDegradedLivePercentileQuality(tx.percentile_quality) ? (tx.percentile_quality === 'averaged' ? ' (averaged)' : ' (approximate)') : ''
  return (
    <div className="wf-drawer-inner">
      <div className="wf-drawer-header">
        <div>
          <strong>{payload.name}</strong>
          <small>Window averages over {tx.count.toLocaleString()} samples · {(tx.tps ?? 0).toFixed(1)} tps · {errorPct.toFixed(1)}% errors</small>
        </div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button>
      </div>
      <div className="wf-drawer-panel">
        {phasesUnknown ? (
          <>
            <p className="wf-drawer-status" role="note"><strong>{PHASES_NOT_REPORTED_TEXT}.</strong> {LIVE_WATERFALL_RESTORED_TEXT}</p>
            <dl className="wf-kv">
              <dt>Average</dt><dd>{formatWaterfallMs(vector.avgMs)}</dd>
              {baseline && <><dt>First stable minute</dt><dd>{formatWaterfallMs(baseline.avgMs)}</dd></>}
            </dl>
          </>
        ) : <>
        <table className="wf-headers live-waterfall-phase-table">
          <caption className="sr-only">Average phase time for {payload.name}</caption>
          <thead><tr><th scope="col">Phase</th><th scope="col">Average</th><th scope="col">Share</th>{baseline && <th scope="col">First stable minute</th>}</tr></thead>
          <tbody>
            {rows.map(([name, ms, base, definition]) => (
              <tr key={name}><th scope="row">{name}{definition && <sup aria-hidden="true">*</sup>}</th><td>{formatWaterfallMs(ms)}</td><td>{((ms / total) * 100).toFixed(0)}%</td>{baseline && <td>{formatWaterfallMs(base ?? 0)}</td>}</tr>
            ))}
            <tr><th scope="row">Total</th><td>{formatWaterfallMs(vector.avgMs)}</td><td>100%</td>{baseline && <td>{formatWaterfallMs(baseline.avgMs)}</td>}</tr>
          </tbody>
        </table>
        <dl className="wf-kv live-waterfall-definitions" aria-label="Phase definitions">
          {rows.filter(([, , , definition]) => definition).map(([name, , , definition]) => (
            <Fragment key={name}><dt>{name}<span aria-hidden="true">*</span></dt><dd>{definition}</dd></Fragment>
          ))}
        </dl>
        </>}
        {payload.heldFrom && <p className="wf-drawer-status" role="status">No samples in the latest window: these figures are from the window at {formatTimeOfDay(payload.heldFrom, { seconds: true })}.</p>}
        {vector.serverSource === 'ttfb' && <p className="wf-drawer-status">A generator predates the server split: server time is average TTFB minus average setup, and Download is the rest of the average, so it also holds the time of samples with no first byte (timeouts) after their setup.</p>}
        {vector.serverSource === 'none' && <p className="wf-drawer-status">No sample reported a first byte, so the time after setup is unsplit.</p>}
        {!phasesUnknown && <h5>Observed phases</h5>}
        {phasesUnknown ? null : stats.setupUnmeasured ? (
          <p className="wf-drawer-status">{SETUP_UNMEASURED_TEXT}: no sample timed DNS, connect, TLS or a first byte (only HTTP and network protocols time connection setup).</p>
        ) : stats.reusePct === undefined ? (
          <p className="wf-drawer-status">Not reported: a contributing generator predates phase observations.</p>
        ) : (
          <dl className="wf-kv">
            <dt>Transactions with no new connection</dt><dd>{reuseValueText(stats) ?? 'Not reported'} {reuseBasisText(stats)}</dd>
            <dt>Transactions that opened a connection</dt><dd>{stats.newConnectionSamples?.toLocaleString()} of {stats.samples.toLocaleString()}</dd>
            <dt>Connect time per sample that connected</dt><dd>{msText(stats.avgConnectMs)}</dd>
            <dt>TLS time per sample with a handshake</dt><dd>{msText(stats.avgHandshakeMs)} ({stats.tlsSamples?.toLocaleString()} samples)</dd>
            <dt>DNS time per sample with a lookup</dt><dd>{msText(stats.avgDnsLookupMs)} ({stats.dnsSamples?.toLocaleString()} samples)</dd>
            {stats.noTtfbSamples !== undefined && <><dt>No first byte</dt><dd>{stats.noTtfbSamples.toLocaleString()} samples</dd></>}
          </dl>
        )}
        {!phasesUnknown && !stats.setupUnmeasured && stats.reusePct !== undefined && (
          <p className="wf-drawer-status">A sample is one transaction: one whose requests opened several connections, handshakes or lookups counts their total time, so these are not the cost of a single handshake or lookup.</p>
        )}
        <h5>Response time</h5>
        {percentilesReported ? (
          <dl className="wf-kv">
            <dt>P50 / P90{quality}</dt><dd>{formatWaterfallMs(tx.p50_ms)} / {formatWaterfallMs(tx.p90_ms)}</dd>
            <dt>P95 / P99{quality}</dt><dd>{tx.p95_ms === undefined ? '—' : formatWaterfallMs(tx.p95_ms)} / {formatWaterfallMs(tx.p99_ms)}</dd>
          </dl>
        ) : (
          <p className="wf-drawer-status">Percentiles not reported for this window.</p>
        )}
        {isDegradedLivePercentileQuality(tx.percentile_quality) && (
          <p className="wf-drawer-status" role="note">{percentileQualityDescription(tx.percentile_quality)}</p>
        )}
        <h5>Phase percentiles</h5>
        {phasesUnknown ? <p className="wf-drawer-status">{PHASES_NOT_REPORTED_TEXT}.</p> : <PhasePercentileTable name={payload.name} tx={tx} />}
      </div>
    </div>
  )
}

/** L1: the latest window's transactions as aggregate phase bars. */
export default function LiveWaterfallPanel({ title, latestMetrics, stableMinute, runFinished = false, className, dataChartId, cardRef, headerActions, onContextMenu, onMouseEnter, onMouseLeave }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const sectionRef = useRef<HTMLElement | null>(null)
  const setSectionRef = useCallback((element: HTMLElement | null) => {
    sectionRef.current = element
    if (typeof cardRef === 'function') cardRef(element)
    else if (cardRef) (cardRef as { current: HTMLElement | null }).current = element
  }, [cardRef])
  // Whether focus is inside the drawer: when the drawer unmounts (its row
  // dropped out, or Close), focus would fall to <body>, so it goes back to the grid.
  const drawerFocused = useRef(false)
  const [compare, setCompare] = useState(true)
  const [requestedStatistic, setStatistic] = useState<LivePhaseStatistic>('avg')
  const baseline = useMemo(() => (stableMinute ? stableMinuteBaseline(stableMinute) : null), [stableMinute])
  // Transactions missing from a window stay listed for a few windows, so a
  // low-rate one does not vanish and reappear (taking an open drawer with it).
  // Keyed by the window's timestamp, so a re-render of the same window does not age the hold.
  const windowKey = latestMetrics?.timestamp
  const [hold, setHold] = useState<{ windowKey?: string; rows: LiveWaterfallHold }>(() => ({ windowKey, rows: advanceLiveWaterfallHold({}, latestMetrics) }))
  let heldRows = hold.rows
  if (hold.windowKey !== windowKey) {
    heldRows = advanceLiveWaterfallHold(hold.rows, latestMetrics)
    setHold({ windowKey, rows: heldRows })
  }
  // Percentile bars need per-phase percentiles: with none in view (older
  // generators) the bars stay averages and a note says why.
  const percentilesAvailable = useMemo(() => liveWaterfallHasPhasePercentiles(latestMetrics?.transactions, heldRows), [latestMetrics?.transactions, heldRows])
  const statistic: LivePhaseStatistic = percentilesAvailable ? requestedStatistic : 'avg'
  // The first stable minute keeps averages only.
  const showBaseline = compare && Boolean(baseline) && statistic === 'avg'
  const roots = useMemo(() => liveWaterfallNodes(latestMetrics?.transactions, { baseline, showBaseline, held: heldRows, statistic }), [latestMetrics?.transactions, baseline, showBaseline, heldRows, statistic])
  const spanMs = liveWaterfallSpanMs(roots)
  const sinks = useMemo(() => liveWaterfallSinks(roots, latestMetrics?.transactions, statistic), [roots, latestMetrics?.transactions, statistic])
  const percentileFallbackRows = statistic !== 'avg' && roots.some(n => n.payload?.percentileFallback)
  const transactionCount = useMemo(() => liveWaterfallTransactionCount(latestMetrics?.transactions), [latestMetrics?.transactions])
  const selected = selectedId ? roots.find(n => n.id === selectedId)?.payload : undefined
  // A selected row that has dropped out closes its drawer for good, so it cannot reopen by itself later.
  if (selectedId && !selected) setSelectedId(null)
  // Restored rows carry no phases at all: they are not "older generators".
  const unobserved = roots.some(n => n.payload && n.payload.vector.serverSource !== 'unknown' && !n.payload.tx.phase_observed)
  const restored = roots.some(n => n.payload?.vector.serverSource === 'unknown')
  // A finished run with no window to show: the controller drops the
  // per-transaction breakdown when a run ends (compactTerminalLiveMetricsTail),
  // so "no transactions completed" would be false.
  const finishedWithoutLiveRows = runFinished && roots.length === 0
  const note = stableMinuteNote(stableMinute, { finished: runFinished })
  const drawerOpen = Boolean(selected)
  useLayoutEffect(() => {
    if (drawerOpen || !drawerFocused.current) return
    drawerFocused.current = false
    const active = document.activeElement
    if (active && active !== document.body && active.isConnected) return
    const section = sectionRef.current
    if (!section) return
    // The grid is gone when every row dropped out (the Waterfall then shows
    // its empty message): fall back to the card's Actions button, then to the
    // card itself, so focus stays in this card rather than on <body>.
    const target = section.querySelector<HTMLElement>('[role="treegrid"]')
      ?? section.querySelector<HTMLElement>('.chart-actions-button')
    if (target) {
      target.focus()
      return
    }
    if (!section.hasAttribute('tabindex')) section.setAttribute('tabindex', '-1')
    section.focus()
  }, [drawerOpen])

  const statisticNoteId = dataChartId ? `${dataChartId}-statistic-note` : undefined
  const toolbar = (
    <>
      <div className="live-waterfall-statistic" role="group" aria-label="Bar statistic" aria-describedby={statisticNoteId}>
        {LIVE_WATERFALL_STATISTICS.map(option => (
          <button
            key={option}
            type="button"
            className={`wf-chip-toggle${statistic === option ? ' active' : ''}`}
            aria-pressed={statistic === option}
            disabled={option !== 'avg' && !percentilesAvailable}
            onClick={() => setStatistic(option)}
          >
            {LIVE_PHASE_STATISTIC_LABELS[option]}
          </button>
        ))}
      </div>
      <button
        type="button"
        className={`wf-chip-toggle${showBaseline ? ' active' : ''}`}
        aria-pressed={showBaseline}
        disabled={!baseline || statistic !== 'avg'}
        aria-describedby={dataChartId ? `${dataChartId}-baseline-note` : undefined}
        onClick={() => setCompare(v => !v)}
      >
        Compare with first stable minute
      </button>
      <span id={dataChartId ? `${dataChartId}-baseline-note` : undefined} className="wf-toolbar-note">{note}</span>
    </>
  )

  return (
    <section
      ref={setSectionRef}
      className={['chart-card', 'live-waterfall-card', className].filter(Boolean).join(' ')}
      data-chart-id={dataChartId}
      aria-label={title}
      onContextMenu={onContextMenu}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <div className="chart-card-header">
        <div className="chart-title-wrap">
          <h3 className="chart-title">{title}</h3>
        </div>
        {headerActions && <div className="chart-card-actions">{headerActions}</div>}
      </div>
      <p className="live-waterfall-copy">
        {finishedWithoutLiveRows
          ? 'The live waterfall shows the latest window while a run is in progress.'
          : `One row per transaction from the ${runFinished ? 'last window this page received' : 'latest window'}. Bars are averages over many samples, not one request; rows are independent and all start at 0.`}
        {unobserved ? ' Some generators predate phase observations, so their reuse and handshake counts are not reported.' : ''}
      </p>
      {restored && <p className="live-waterfall-copy" role="note">{LIVE_WATERFALL_RESTORED_TEXT}</p>}
      <p id={statisticNoteId} className="live-waterfall-copy" role="note">
        {statistic === 'avg'
          ? percentilesAvailable || roots.length === 0
            ? 'Bars show phase averages.'
            : `Bars show phase averages. ${PHASE_PERCENTILES_NOT_REPORTED_TEXT}, so P50, P95 and P99 are unavailable.`
          : `Each segment is that phase's own ${LIVE_PHASE_STATISTIC_LABELS[statistic]} over the samples that spent time in it. Percentiles do not add up: a bar is not the transaction's ${LIVE_PHASE_STATISTIC_LABELS[statistic]}, and the first stable minute comparison is off.${percentileFallbackRows ? ' Rows marked "averages" come from a load generator that predates per-phase percentiles.' : ''}`}
      </p>
      {transactionCount > LIVE_WATERFALL_MAX_ROWS && (
        <p className="live-waterfall-copy" role="note">
          Showing the {LIVE_WATERFALL_MAX_ROWS} slowest of {transactionCount.toLocaleString()} transactions this window (rows held from earlier windows are listed only in space this window leaves); the phase share in the facts covers all of them.
        </p>
      )}
      <Waterfall<LiveWaterfallPayload>
        roots={roots}
        spanMs={spanMs}
        ariaLabel={`${title}: ${statistic === 'avg' ? 'average phases' : `phase ${LIVE_PHASE_STATISTIC_LABELS[statistic]}s`} per transaction`}
        selectedId={selected ? selectedId : null}
        onSelect={n => setSelectedId(n.id)}
        initialSortMode="slowest"
        toolbar={toolbar}
        sinks={sinks}
        sinksLabel="Live waterfall facts"
        phaseLabels={LIVE_WATERFALL_PHASE_LABELS}
        baselineLabel="First stable minute"
        nameColumnLabel="Transaction"
        criticalPath={false}
        scaleLabels={LIVE_WATERFALL_SCALE_LABELS}
        sortModes={LIVE_WATERFALL_SORT_MODES}
        legendPhases={LIVE_WATERFALL_LEGEND_PHASES}
        showIdleLegend={false}
        showStartOffset={false}
        drawer={selected ? (
          <div
            className="live-waterfall-drawer-focus"
            onFocus={() => { drawerFocused.current = true }}
            onBlur={event => {
              // A removed element's blur has no target to go to: keep the flag so the grid takes focus.
              if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node)) drawerFocused.current = false
            }}
          >
            <LiveWaterfallDrawer payload={selected} onClose={() => setSelectedId(null)} />
          </div>
        ) : undefined}
        emptyMessage={finishedWithoutLiveRows ? LIVE_WATERFALL_FINISHED_EMPTY_TEXT : latestMetrics ? 'No transactions completed in the latest window.' : 'Waiting for live metrics.'}
      />
    </section>
  )
}
