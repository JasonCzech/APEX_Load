import { useId, useMemo, useState } from 'react'
import type { BrowserReplayReport } from '../types/replay'
import type { DebugRunRecord } from './debuggerExperience'
import { Waterfall } from './waterfall/Waterfall'
import { findNode } from './waterfall/analysis'
import { buildDebugRunDiffTree, buildRunDiffOptions, formatDeltaMs, runTransactionRows, type RunDiffEntry } from './waterfall/debugRunDiff'
import type { DebugWaterfallNode } from './waterfall/debugTree'
import { TIMING_SOURCE_LABELS, WATERFALL_PHASES, formatWaterfallMs } from './waterfall/phases'

function PhaseComparison({ entry }: { entry: RunDiffEntry }) {
  const base = entry.base
  const current = entry.current
  const totals = (row?: RunDiffEntry['base']) => {
    const m = new Map<string, number>()
    for (const s of row?.segments ?? []) m.set(s.key, (m.get(s.key) ?? 0) + s.durationMs)
    return m
  }
  const bt = totals(base)
  const ct = totals(current)
  // Without comparable phases only the total is a real change; per-phase rows
  // show each side's own breakdown with no change column.
  const comparable = entry.phasesComparable
  const keys = WATERFALL_PHASES.filter(p => (bt.get(p.key) ?? 0) > 0 || (ct.get(p.key) ?? 0) > 0)
  const sum = (m: Map<string, number>) => [...m].filter(([k]) => k !== 'total').reduce((s, [, v]) => s + v, 0)
  const bOut = base && comparable ? Math.max(0, base.durationMs - sum(bt)) : 0
  const cOut = current && comparable ? Math.max(0, current.durationMs - sum(ct)) : 0
  return (
    <table className="wf-headers wf-compare-table">
      <caption className="sr-only">Phase comparison</caption>
      <thead>
        <tr><th scope="col">Phase</th><th scope="col">Baseline</th><th scope="col">This run</th><th scope="col">Change</th></tr>
      </thead>
      <tbody>
        {keys.map(p => {
          const b = bt.get(p.key) ?? 0
          const c = ct.get(p.key) ?? 0
          return (
            <tr key={p.key}>
              <th scope="row"><i className={`wf-swatch phase-${p.key}`} aria-hidden="true" /> {p.label}</th>
              <td>{base ? formatWaterfallMs(b) : '—'}</td>
              <td>{current ? formatWaterfallMs(c) : '—'}</td>
              <td>{comparable ? formatDeltaMs(c - b) : '—'}</td>
            </tr>
          )
        })}
        {(bOut >= 0.5 || cOut >= 0.5) && (
          <tr>
            <th scope="row">Outside measured phases</th>
            <td>{formatWaterfallMs(bOut)}</td>
            <td>{formatWaterfallMs(cOut)}</td>
            <td>{formatDeltaMs(cOut - bOut)}</td>
          </tr>
        )}
        <tr>
          <th scope="row">Total</th>
          <td>{base ? formatWaterfallMs(base.durationMs) : '—'}</td>
          <td>{current ? formatWaterfallMs(current.durationMs) : '—'}</td>
          <td>{base && current ? formatDeltaMs(entry.deltaMs) : '—'}</td>
        </tr>
      </tbody>
    </table>
  )
}

const sourceLabel = (row?: RunDiffEntry['base']) => (row ? TIMING_SOURCE_LABELS[row.timingSource].label : '—')
/** Only rows that measured network phases say anything about connection reuse. */
const connectionOf = (row?: RunDiffEntry['base']) => (!row ? '—' : row.timingSource === 'duration' ? 'not observed' : row.connectionReused ? 'reused' : 'new')

function DiffDrawer({ node, entry }: { node: DebugWaterfallNode; entry?: RunDiffEntry }) {
  const base = entry?.base
  const current = entry?.current
  const urlChanged = base && current && base.target !== current.target
  const showConnection = (base && base.timingSource !== 'duration') || (current && current.timingSource !== 'duration')
  return (
    <div className="wf-drawer-inner">
      <header className="wf-drawer-header">
        <div>
          <strong>{node.label}</strong>
          <small>{node.delta?.label ?? 'No comparison for this row'}</small>
        </div>
        <code>{node.delta && node.delta.outcome !== 'added' && node.delta.outcome !== 'removed' ? formatDeltaMs(node.delta.ms) : '—'}</code>
      </header>
      <div className="wf-drawer-panel">
        {entry ? (
          <>
            <dl className="wf-kv">
              <dt>Outcome</dt>
              <dd>{base ? (base.success ? 'Passed' : 'Failed') : 'Not executed'} → {current ? (current.success ? 'Passed' : 'Failed') : 'Not executed'}</dd>
              {(base?.statusCode != null || current?.statusCode != null) && <><dt>HTTP status</dt><dd>{base?.statusCode ?? '—'} → {current?.statusCode ?? '—'}</dd></>}
              <dt>Timing source</dt><dd>{sourceLabel(base)} → {sourceLabel(current)}</dd>
              {showConnection && <><dt>Connection</dt><dd>{connectionOf(base)} → {connectionOf(current)}</dd></>}
              {urlChanged && <><dt>Target</dt><dd>{base!.target} → {current!.target}</dd></>}
            </dl>
            <h5>Phase comparison</h5>
            <PhaseComparison entry={entry} />
            {entry.phasesReason === 'timing sources differ' && (
              <p className="wf-drawer-status">The two runs measured this execution differently ({sourceLabel(base)} vs {sourceLabel(current)}), so only the total is compared.</p>
            )}
            {entry.phasesReason === 'total only' && (
              <p className="wf-drawer-status">One of the runs retained only the total time for this execution, so the change cannot be split into phases.</p>
            )}
          </>
        ) : (
          <p className="wf-drawer-status">
            {node.children.length ? 'The change is this row’s own measured time. Select a request to see its phases side by side.' : 'This row has no counterpart in the other run.'}
          </p>
        )}
      </div>
    </div>
  )
}

/**
 * Debug-run diff (plan D7): overlay two retained runs aligned by action, with
 * a baseline ghost bar, a per-row change bar and a phase-level explanation.
 */
export function DebugRunDiffPanel({
  runs,
  current,
  currentRecord,
}: {
  runs: readonly DebugRunRecord[]
  current?: BrowserReplayReport | null
  currentRecord?: Pick<DebugRunRecord, 'transactions' | 'draft'> | null
}) {
  const baseId = useId()
  const options = useMemo(() => buildRunDiffOptions(runs, current, currentRecord), [runs, current, currentRecord])
  const [baselineId, setBaselineId] = useState<string | null>(null)
  const [compareId, setCompareId] = useState<string | null>(null)
  const compare = options.find(o => o.id === compareId) ?? options[0]
  const baseline = options.find(o => o.id === baselineId && o.id !== compare?.id) ?? options.find(o => o.id !== compare?.id)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  // Keyed on the reports themselves: a streaming current run must not rebuild
  // a comparison between two retained runs.
  const baseReport = baseline?.report
  const compareReport = compare?.report
  const baseRecord = baseline?.record
  const compareRecord = compare?.record
  const diffTree = useMemo(() => {
    if (!baseReport || !compareReport) return null
    return buildDebugRunDiffTree(
      { events: baseReport.events, transactions: runTransactionRows(baseReport, baseRecord), stopped: baseReport.stopped },
      { events: compareReport.events, transactions: runTransactionRows(compareReport, compareRecord), stopped: compareReport.stopped },
    )
  }, [baseReport, compareReport, baseRecord, compareRecord])

  const entryByNodeId = useMemo(() => {
    const map = new Map<string, RunDiffEntry>()
    if (!diffTree) return map
    for (const [p, id] of diffTree.nodeIdByCurrentPosition) {
      const entry = diffTree.diff.byCurrentPosition.get(p)
      if (entry) map.set(id, entry)
    }
    for (const e of diffTree.diff.onlyInBaseline) map.set(`baseline-only:${e.key}`, e)
    return map
  }, [diffTree])

  if (options.length < 2 || !baseline || !compare || !diffTree) {
    return <p className="wf-empty">Run the script again to compare timing between two runs. The editor keeps the last 5 runs.</p>
  }

  const selected = selectedId ? findNode(diffTree.roots, selectedId) : null
  return (
    <div className="debug-run-diff">
      {/* Outside the keyed waterfall: changing runs must not drop keyboard focus. */}
      <div className="wf-toolbar debug-run-diff-controls" role="group" aria-label="Runs to compare">
        <div className="wf-toolbar-group wf-toolbar-caller">
          <label htmlFor={`${baseId}-baseline`}>Baseline</label>
          <select id={`${baseId}-baseline`} value={baseline.id} onChange={e => setBaselineId(e.target.value)}>
            {options.filter(o => o.id !== compare.id).map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
          </select>
          <label htmlFor={`${baseId}-compare`}>Compared with</label>
          <select id={`${baseId}-compare`} value={compare.id} onChange={e => setCompareId(e.target.value)}>
            {options.map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
          </select>
          <button type="button" className="wf-text-btn" onClick={() => { setBaselineId(compare.id); setCompareId(baseline.id) }}>Swap</button>
        </div>
      </div>
      <Waterfall<DebugWaterfallNode['payload']>
        key={`${baseline.id}|${compare.id}`}
        roots={diffTree.roots}
        spanMs={diffTree.spanMs}
        ariaLabel="Run comparison"
        selectedId={selectedId}
        onSelect={n => setSelectedId(n.id)}
        sinks={diffTree.sinks}
        sinksLabel="Comparison highlights"
        initialSortMode="chronological"
        drawer={selected ? <DiffDrawer key={selected.id} node={selected as DebugWaterfallNode} entry={entryByNodeId.get(selected.id)} /> : (
          <p className="wf-drawer-status" style={{ padding: 12 }}>Select a row to compare its phases between the two runs. Dashed outlines are the baseline; amber marks extra time, green time saved.</p>
        )}
        emptyMessage="Neither run retained executions to compare."
      />
    </div>
  )
}

export default DebugRunDiffPanel
