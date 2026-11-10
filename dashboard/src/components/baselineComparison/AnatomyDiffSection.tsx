import { useId, useMemo, useState } from 'react'
import type { RunArchiveAnatomyRequest } from '../../types/timeAnatomy'
import { StatisticRadios } from '../runReport/TransactionTimeAnatomy'
import { STATISTIC_LABELS, type AggregateStatistic } from '../runReport/timeAnatomyModel'
import { Waterfall } from '../waterfall/Waterfall'
import { formatDeltaMs } from '../waterfall/debugRunDiff'
import { formatWaterfallMs } from '../waterfall/phases'
import type { WaterfallNode } from '../waterfall/types'
import {
  ANATOMY_DIFF_PHASES,
  ANATOMY_DIFF_PHASE_LABELS,
  MEAN_COMPONENT_LABELS,
  WHOLE_RUN_P95_LABEL,
  WHOLE_RUN_P95_SCOPE,
  anatomyDiffAvailability,
  anatomyDiffNotes,
  anatomyDiffSinks,
  buildAnatomyDiffTree,
  formatMeanCoverage,
  meanAttribution,
  reuseSentence,
  statOf,
  transactionConnectionReuse,
  transactionPercentileChange,
  type AnatomyRowDiff,
} from './anatomyDiffModel'
import { comparisonColors, type ComparisonRow, type ComparisonSnapshot } from './model'
import type { AnatomyLoad } from './useAnatomyReports'
import '../runReport/TimeAnatomy.css'

type Props = {
  row: ComparisonRow
  snapshot: ComparisonSnapshot
  anatomy: Record<string, AnatomyLoad>
  /** Fetches the runs whose report failed again. */
  onRetry?: () => void
}

const share = (v?: number) => (v === undefined ? '—' : `${Math.round(v * 100)}%`)
const ms = (v?: number) => (v === undefined ? '—' : formatWaterfallMs(v))

function RowDrawer({ diff, statistic }: { diff: AnatomyRowDiff | undefined; statistic: AggregateStatistic }) {
  if (!diff) return <p className="wf-drawer-status bc-anatomy-hint">Select a row to compare its phases. Dashed outlines are the baseline; amber marks extra time, green time saved.</p>
  const { current, baseline } = diff.pair
  const name = (current || baseline)?.action_name || 'Request'
  const label = STATISTIC_LABELS[statistic]
  return <section aria-label={`${name} phase change`} className="bc-anatomy-row">
    <h4>{diff.pair.other ? 'Other requests' : name}</h4>
    <p>{diff.outcome === 'added' ? 'Only in this run.' : diff.outcome === 'removed' ? 'Only in the baseline.' : diff.pair.match === 'action_occurrence' ? 'Matched by action and occurrence: the step number changed.' : 'Matched by step, action and occurrence.'}</p>
    <div className="bc-table-scroll"><table>
      <caption className="sr-only">{label} of each phase</caption>
      <thead><tr><th scope="col">{label}</th><th scope="col">Baseline</th><th scope="col">This run</th><th scope="col">Change</th></tr></thead>
      <tbody>
        {ANATOMY_DIFF_PHASES.map(phase => <tr key={phase}><th scope="row">{ANATOMY_DIFF_PHASE_LABELS[phase]}</th>
          <td>{baseline ? ms(statOf(baseline.phases?.[phase], statistic)) : '—'}</td>
          <td>{current ? ms(statOf(current.phases?.[phase], statistic)) : '—'}</td>
          <td>{diff.phaseDeltas[phase] === undefined ? '—' : formatDeltaMs(diff.phaseDeltas[phase]!)}</td></tr>)}
        <tr><th scope="row">Request {label}</th><td>{baseline ? ms(statOf(baseline.phases?.total, statistic)) : '—'}</td><td>{current ? ms(statOf(current.phases?.total, statistic)) : '—'}</td><td>{diff.totalDeltaMs === undefined ? '—' : formatDeltaMs(diff.totalDeltaMs)}</td></tr>
        {diff.offsetDeltaMs !== undefined && <tr><th scope="row">Median start offset</th><td>{ms(baseline?.offset_ms?.p50_ms)}</td><td>{ms(current?.offset_ms?.p50_ms)}</td><td>{formatDeltaMs(diff.offsetDeltaMs)}</td></tr>}
        <tr><th scope="row">Samples</th><td>{baseline?.samples ?? '—'}</td><td>{current?.samples ?? '—'}</td><td>—</td></tr>
      </tbody>
    </table></div>
  </section>
}

/** R5: one transaction's time anatomy against a compared baseline, whole run, from both archive reports. */
export function AnatomyDiffSection({ row, snapshot, anatomy, onRetry }: Props) {
  const headingId = useId()
  const pickerId = useId()
  const baselines = snapshot.baselines
  const [baselineId, setBaselineId] = useState(baselines[0]?.id || '')
  const [statistic, setStatistic] = useState<AggregateStatistic>('avg')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const activeBaseline = baselines.find(run => run.id === baselineId) ? baselineId : baselines[0]?.id || ''
  const currentLoad = anatomy[snapshot.current.id]
  const baselineLoad = anatomy[activeBaseline]

  // Loads are rebuilt each render; the anatomy objects they carry are stable.
  const bothReady = currentLoad?.status === 'ready' && baselineLoad?.status === 'ready'
  const currentAnatomy = currentLoad?.status === 'ready' ? currentLoad.anatomy : undefined
  const baselineAnatomy = baselineLoad?.status === 'ready' ? baselineLoad.anatomy : undefined
  const availability = useMemo(() => (bothReady ? anatomyDiffAvailability(row.name, currentAnatomy, baselineAnatomy) : null), [bothReady, row.name, currentAnatomy, baselineAnatomy])
  const ready = availability?.status === 'ready' ? availability : null
  const tree = useMemo(() => (ready ? buildAnatomyDiffTree(ready.current, ready.baseline, statistic) : null), [ready, statistic])
  const facts = useMemo(() => {
    if (!ready || !tree) return null
    const mean = meanAttribution(ready.current, ready.baseline)
    const p95 = transactionPercentileChange(ready.current, ready.baseline, 'p95')
    const reuse = reuseSentence(transactionConnectionReuse(ready.current), transactionConnectionReuse(ready.baseline))
    return { mean, p95, reuse, sinks: anatomyDiffSinks(mean, p95, reuse, tree), notes: anatomyDiffNotes(ready.current, ready.baseline, statistic, tree.fallbackMatches) }
  }, [ready, tree, statistic])
  const diffById = useMemo(() => new Map((tree?.rows || []).map(d => [d.pair.id, d])), [tree])

  const populationOk = snapshot.request.population === 'transaction'
  let body
  if (!populationOk) body = <p>Time anatomy covers named transactions only. Compare the Named transactions population to see it.</p>
  else if (!currentLoad || !baselineLoad || currentLoad.status === 'loading' || baselineLoad.status === 'loading') body = <p role="status">Loading both runs’ time anatomy…</p>
  else if (currentLoad.status === 'error' || baselineLoad.status === 'error') body = <div role="alert">
    <p>{currentLoad.status === 'error' ? `Current run: ${currentLoad.message}` : baselineLoad.status === 'error' ? `Baseline: ${baselineLoad.message}` : ''}</p>
    {onRetry ? <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>Retry</button> : <p>Close and reopen the details to try again.</p>}
  </div>
  else if (availability?.status === 'unavailable') body = <p>{availability.message}</p>
  else if (ready && tree && facts) {
    const selected = selectedId ? diffById.get(selectedId) : undefined
    body = <>
      <p className="bc-anatomy-headline"><strong>{facts.mean ? facts.mean.headline : 'Mean change unavailable: a run has no instance matched to its requests.'}</strong>
        {facts.p95 && <span> · {WHOLE_RUN_P95_LABEL} ({WHOLE_RUN_P95_SCOPE}) {formatDeltaMs(facts.p95.deltaMs)} ({formatWaterfallMs(facts.p95.baselineMs)} → {formatWaterfallMs(facts.p95.currentMs)}{facts.p95.approximate ? ', approximate' : ''}; not split by phase)</span>}
        {facts.reuse && <span> · {facts.reuse}</span>}</p>
      <div className="bc-controls"><StatisticRadios value={statistic} onChange={setStatistic} /></div>
      <Waterfall<RunArchiveAnatomyRequest>
        // A new baseline compares other rows: start over. The statistic only
        // changes values, so sort, zoom and hidden phases stay.
        key={activeBaseline}
        roots={tree.roots}
        spanMs={tree.spanMs}
        ariaLabel={`${row.name}: this run against the baseline, ${STATISTIC_LABELS[statistic]} of each phase`}
        selectedId={selectedId}
        onSelect={(node: WaterfallNode<RunArchiveAnatomyRequest>) => setSelectedId(node.id)}
        sinks={facts.sinks}
        sinksLabel="Comparison highlights"
        initialSortMode={tree.hasDeltas ? 'change' : 'chronological'}
        drawer={<RowDrawer key={selectedId || ''} diff={selected} statistic={statistic} />}
        emptyMessage="Neither run recorded request rows for this transaction."
      />
      {facts.mean && <div className="bc-table-scroll"><table aria-label="Mean time by phase">
        <caption className="bc-anatomy-caption">Mean per matched instance: baseline {formatMeanCoverage(facts.mean.coverage.baseline)}, this run {formatMeanCoverage(facts.mean.coverage.current)}{facts.mean.lowCoverage ? '. Low match coverage: shares withheld' : ''}. The changes add up to the mean duration change.</caption>
        <thead><tr><th scope="col">Mean per instance</th><th scope="col">Baseline</th><th scope="col">This run</th><th scope="col">Change</th><th scope="col">Share of change</th></tr></thead>
        <tbody>
          {facts.mean.components.map(c => <tr key={c.component}><th scope="row">{MEAN_COMPONENT_LABELS[c.component]}</th><td>{ms(c.baselineMs)}</td><td>{ms(c.currentMs)}</td><td>{formatDeltaMs(c.deltaMs)}</td><td>{share(c.share)}</td></tr>)}
          <tr><th scope="row">Mean duration</th><td>{ms(facts.mean.baselineMs)}</td><td>{ms(facts.mean.currentMs)}</td><td>{formatDeltaMs(facts.mean.deltaMs)}</td><td>—</td></tr>
        </tbody>
      </table></div>}
      <ul className="bc-anatomy-notes">{facts.notes.map(note => <li key={note}>{note}</li>)}</ul>
    </>
  }

  return <section className="bc-anatomy" aria-labelledby={headingId}>
    <h3 id={headingId}>Time anatomy diff</h3>
    {baselines.length > 1 && <label htmlFor={pickerId}>Baseline <select id={pickerId} value={activeBaseline} onChange={e => { setBaselineId(e.target.value); setSelectedId(null) }}>
      {baselines.map((run, i) => <option key={run.id} value={run.id}>Baseline {i + 1} · {run.release_name || 'No release'} · {run.name}</option>)}
    </select></label>}
    {baselines.length === 1 && <p className="bc-anatomy-run" style={{ borderLeft: `4px solid ${comparisonColors[1]}` }}>Against {baselines[0].release_name || 'No release'} · {baselines[0].name}</p>}
    {body}
  </section>
}
