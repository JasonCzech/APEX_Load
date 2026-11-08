import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { debugSummaryModel, type DebugCompletionSnapshot } from './debugCompletionModel'
import { debugTransactionRows, debugTransactionTimingBreakdown, type DebugTransactionRow } from './debugTransactionModel'
import {
  buildDebugTimelineModel,
  DEBUG_TIMELINE_PHASES,
  type DebugTimelinePhaseKey,
  type DebugTimelineRowModel,
} from './debugTimelineWaterfallModel'
import { metricHeader } from '../metricLabels'
import { formatMs } from './runReport/runReportModel'
import { formatDateTime } from '../utils/dateTime'

const phaseLabel = { init: 'Init', action: 'Actions', teardown: 'Teardown' }

interface TransactionWaterfallSegment {
  key: DebugTimelinePhaseKey
  label: string
  durationMs: number
  widthPct: number
}

interface TransactionWaterfallBar {
  offsetPct: number
  widthPct: number
  segments: TransactionWaterfallSegment[]
  title: string
}

function parseTimestampMs(raw: string | null | undefined): number | null {
  if (!raw || typeof raw !== 'string' || /^0001-01-01T00:00:00/.test(raw)) return null
  const ms = Date.parse(raw)
  if (!Number.isFinite(ms)) return null
  const subMs = /\.(\d{3})(\d+)/.exec(raw)
  return subMs ? ms + Number(`0.${subMs[2]}`) : ms
}

function isLeafActionRow(row: DebugTransactionRow): boolean {
  return (
    row.children.length === 0 &&
    row.eventIndex != null &&
    row.recordedDurationMs !== undefined &&
    row.kind !== 'transaction' &&
    row.kind !== 'subtransaction' &&
    row.kind !== 'container'
  )
}

function collectLeafEventIndices(row: DebugTransactionRow, out: number[] = []): number[] {
  if (isLeafActionRow(row)) {
    out.push(row.eventIndex!)
    return out
  }
  for (const child of row.children) {
    collectLeafEventIndices(child, out)
  }
  return out
}

function buildTransactionWaterfallBar(
  row: DebugTransactionRow,
  timelineRowByEventIndex: ReadonlyMap<number, DebugTimelineRowModel>,
  sessionStartMs: number | null,
  sessionSpanMs: number,
): TransactionWaterfallBar | null {
  if (row.durationMs == null || row.durationMs < 0) {
    return null
  }

  if (isLeafActionRow(row)) {
    const leaf = timelineRowByEventIndex.get(row.eventIndex!)
    if (!leaf) return null
    const detail =
      leaf.timingSource !== 'duration'
        ? ` (${leaf.segments.map((s) => `${s.shortLabel} ${s.durationMs.toFixed(1)} ms`).join(' · ')})`
        : ''
    return {
      offsetPct: leaf.trackOffsetPct,
      widthPct: leaf.trackWidthPct,
      segments: leaf.segments.map((s) => ({
        key: s.key,
        label: s.shortLabel,
        durationMs: s.durationMs,
        widthPct: s.widthPct,
      })),
      title: `${row.name}: ${formatMs(row.durationMs)}${detail}`,
    }
  }

  const rowStartMs = parseTimestampMs(row.startedAt)
  const hasRowSpan =
    sessionStartMs !== null &&
    rowStartMs !== null &&
    sessionSpanMs > 0 &&
    rowStartMs >= sessionStartMs &&
    rowStartMs - sessionStartMs <= sessionSpanMs
  const rowOffsetPct = hasRowSpan
    ? Math.min(98, Math.max(0, ((rowStartMs - sessionStartMs) / sessionSpanMs) * 100))
    : null
  const rowEndPct =
    hasRowSpan && rowOffsetPct !== null
      ? Math.min(100, Math.max(rowOffsetPct, ((rowStartMs - sessionStartMs + row.durationMs) / sessionSpanMs) * 100))
      : null

  const indices = collectLeafEventIndices(row)
  const leafRows = indices
    .map((idx) => timelineRowByEventIndex.get(idx))
    .filter((item): item is DebugTimelineRowModel => Boolean(item))
  if (leafRows.length > 0) {
    const leafMinOffset = Math.min(...leafRows.map((item) => item.trackOffsetPct))
    const leafMaxEnd = Math.max(...leafRows.map((item) => item.trackOffsetPct + item.trackWidthPct))
    const minOffset = rowOffsetPct !== null ? Math.min(leafMinOffset, rowOffsetPct) : leafMinOffset
    const maxEnd = rowEndPct !== null ? Math.max(leafMaxEnd, rowEndPct) : leafMaxEnd
    const widthPct = row.durationMs > 0 ? Math.max(2, Math.min(100 - minOffset, maxEnd - minOffset)) : 1.5

    const phaseTotals = new Map<DebugTimelinePhaseKey, number>()
    for (const leaf of leafRows) {
      for (const seg of leaf.segments) {
        phaseTotals.set(seg.key, (phaseTotals.get(seg.key) ?? 0) + seg.durationMs)
      }
    }
    const totalPhaseMs = Array.from(phaseTotals.values()).reduce((sum, v) => sum + v, 0)
    const segments: TransactionWaterfallSegment[] =
      totalPhaseMs > 0
        ? DEBUG_TIMELINE_PHASES.filter((phase) => (phaseTotals.get(phase.key) ?? 0) > 0).map((phase) => {
            const durationMs = phaseTotals.get(phase.key) ?? 0
            return {
              key: phase.key,
              label: phase.shortLabel,
              durationMs,
              widthPct: (durationMs / totalPhaseMs) * 100,
            }
          })
        : [
            {
              key: 'total',
              label: 'Execution',
              durationMs: row.durationMs,
              widthPct: 100,
            },
          ]

    const detail =
      segments.length > 1 || (segments[0] && segments[0].key !== 'total')
        ? ` (${segments.map((s) => `${s.label} ${s.durationMs.toFixed(1)} ms`).join(' · ')})`
        : ''
    return {
      offsetPct: minOffset,
      widthPct,
      segments,
      title: `${row.name}: ${formatMs(row.durationMs)}${detail}`,
    }
  }

  const spanMs = Math.max(sessionSpanMs, row.durationMs, 1)
  const offsetPct = rowOffsetPct ?? 0
  const widthPct =
    row.durationMs > 0
      ? rowEndPct !== null && rowOffsetPct !== null
        ? Math.max(2, Math.min(100 - offsetPct, rowEndPct - rowOffsetPct))
        : Math.max(2, Math.min(100 - offsetPct, (row.durationMs / spanMs) * 100))
      : 1.5
  return {
    offsetPct,
    widthPct,
    segments: [{ key: 'total', label: 'Execution', durationMs: row.durationMs, widthPct: 100 }],
    title: `${row.name}: ${formatMs(row.durationMs)}`,
  }
}

export default function DebugTransactionTable({ snapshot, onSelectEvent }: { snapshot: DebugCompletionSnapshot; onSelectEvent: (index: number) => void }) {
  const rows = useMemo(() => debugTransactionRows(snapshot), [snapshot])
  const summaryEvents = useMemo(() => debugSummaryModel(snapshot).events, [snapshot])
  const timelineEvents = useMemo(() => summaryEvents.map((entry) => entry.event), [summaryEvents])
  const timelineModel = useMemo(() => buildDebugTimelineModel(timelineEvents, 'waterfall'), [timelineEvents])
  const timelineRowByEventIndex = useMemo(() => {
    const map = new Map<number, DebugTimelineRowModel>()
    summaryEvents.forEach((entry, idx) => {
      const timelineRow = timelineModel.rows[idx]
      if (timelineRow) map.set(entry.index, timelineRow)
    })
    return map
  }, [summaryEvents, timelineModel.rows])
  const sessionStartMs = useMemo(() => {
    const parsedStarts = timelineEvents.map((ev) => parseTimestampMs(ev.started_at)).filter((t): t is number => t !== null)
    const allSameStart = timelineEvents.length > 1 && new Set(parsedStarts).size <= 1
    return !allSameStart && parsedStarts.length > 0 ? Math.min(...parsedStarts) : null
  }, [timelineEvents])
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const renderRows = (items: DebugTransactionRow[], depth = 0): ReactNode => items.map(row => {
    const open = expanded.has(row.key)
    const timing = open ? debugTransactionTimingBreakdown(row) : null
    const waterfallBar = buildTransactionWaterfallBar(row, timelineRowByEventIndex, sessionStartMs, timelineModel.sessionSpanMs)
    return <Fragment key={row.key}>
      <tr>
        <th scope="row"><div className="debug-transaction-name" style={{ paddingInlineStart: depth * 20 }}>
          {row.children.length > 0 && <button type="button" className="btn btn-sm btn-ghost" aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${row.name}`} onClick={() => setExpanded(previous => { const next = new Set(previous); if (next.has(row.key)) next.delete(row.key); else next.add(row.key); return next })}>{open ? '▾' : '▸'}</button>}
          <span>{row.name}<small>{row.kind === 'container' ? 'Control flow' : row.kind === 'subtransaction' ? 'Subtransaction' : row.kind === 'execution' ? 'Execution' : row.kind === 'transaction' ? 'Transaction' : 'Page / step'}{row.executions > 1 ? ` · ${row.executions} executions` : ''}</small></span>
        </div></th>
        <td>{phaseLabel[row.phase]}</td>
        <td><span className={`debug-transaction-status ${row.status === 'Passed' ? 'passed' : row.status === 'Failed' ? 'failed' : 'muted'}`}>{row.status}</span>{row.incomplete && row.status !== 'Not observed' && <small>Incomplete evidence</small>}</td>
        <td>{row.startedAt ? <time dateTime={row.startedAt} title={row.startedAt}>{formatDateTime(row.startedAt, { milliseconds: true })}</time> : '—'}</td>
        <td>{row.completedAt ? <time dateTime={row.completedAt} title={row.completedAt}>{formatDateTime(row.completedAt, { milliseconds: true })}</time> : '—'}</td>
        <td>{row.durationMs == null ? '—' : formatMs(row.durationMs)}{row.recordedDurationMs != null && <small>Action time: {formatMs(row.recordedDurationMs)}</small>}</td>
        <td className="debug-transaction-waterfall-cell">
          {waterfallBar ? (
            <div className="debug-timeline-bar-track" title={waterfallBar.title} aria-label={`Waterfall for ${row.name}: ${waterfallBar.title}`}>
              <span
                className="debug-timeline-bar-fill"
                style={{
                  left: `${waterfallBar.offsetPct}%`,
                  width: `${waterfallBar.widthPct}%`,
                }}
              >
                {waterfallBar.segments.map((seg, segIdx) => (
                  <span
                    key={`${seg.key}-${segIdx}`}
                    className={`debug-timeline-bar-segment phase-${seg.key}`}
                    style={{ width: `${seg.widthPct}%` }}
                  />
                ))}
              </span>
            </div>
          ) : '—'}
        </td>
        <td>{row.eventIndex != null && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectEvent(row.eventIndex!)}>Inspect<span className="sr-only"> {row.name}</span></button>}</td>
      </tr>
      {open && row.executions > 0 && <tr><td colSpan={8} className="debug-transaction-breakdown"><div role="note" aria-label={`Timing breakdown for ${row.name}`}>
        {timing ? <>{formatMs(timing.childSpanTotalMs)} in child spans − {formatMs(timing.overlapMs)} counted more than once + {formatMs(timing.outsideChildrenMs)} outside child spans ≈ {formatMs(timing.parentElapsedMs)} parent elapsed.</> : 'A timing breakdown is unavailable because retained timing is incomplete.'}
      </div></td></tr>}
      {open && renderRows(row.children, depth + 1)}
    </Fragment>
  })
  return <section aria-label="Transaction details" className="debug-transaction-section">
    <h3>Transactions</h3>
    {rows.length ? <>
      <p>Expand transactions for subtransactions, pages, and repeated executions. Elapsed time includes pauses, think time, and debugger work; action time is the run’s recorded action measurement.</p>
      <p>Parent elapsed includes time outside children and counts overlapping child spans once. Expand a parent to see the breakdown. Values are rounded for display; missing timing is left blank.</p>
      <div className="debug-transaction-table-wrap" tabIndex={0} role="region" aria-label="Transaction timings"><table className="debug-transaction-table" data-table-pagination="off"><caption className="sr-only">Transactions from the completed debug run</caption><thead><tr><th scope="col">Transaction / page</th><th scope="col">Phase</th><th scope="col">Status</th><th scope="col">Start time</th><th scope="col">End time</th><th scope="col">{metricHeader('elapsedTime')}</th><th scope="col" className="debug-transaction-col-waterfall"><span>Waterfall</span><div className="debug-timeline-ruler" aria-hidden="true">{timelineModel.rulerTicks.map((tick, idx) => <span key={idx}>{tick}</span>)}</div></th><th scope="col">Evidence</th></tr></thead><tbody>{renderRows(rows)}</tbody></table></div>
    </> : <p>{snapshot.transactions ? 'No transactions were defined in this run’s script.' : 'Transaction definitions were not retained for this run. Start a new run from the Debug menu to capture the transaction breakdown.'}</p>}
  </section>
}

