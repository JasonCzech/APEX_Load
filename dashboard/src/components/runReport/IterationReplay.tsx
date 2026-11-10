import { useEffect, useId, useMemo, useState, type FormEvent } from 'react'

import { useApi } from '../../hooks/useApi'
import type { ArchiveIterationWaterfall, IterationReplayTarget, RunArchiveIterationIndex } from '../../types/iterationReplay'
import type { ArchiveWaterfallRow } from '../../types/timeAnatomy'
import { formatDateTime } from '../../utils/dateTime'
import { Waterfall } from '../waterfall/Waterfall'
import { findNode } from '../waterfall/analysis'
import { TIMING_SOURCE_LABELS, formatWaterfallMs } from '../waterfall/phases'
import {
  ITERATION_REASON_LABELS,
  THINK_GAP_LABEL,
  buildIterationTree,
  canReplay,
  formTarget,
  iterationChipLabel,
  iterationErrorCopy,
  iterationErrorIsBusy,
  iterationIdentityLabel,
  iterationParams,
  iterationPartialCopy,
  iterationReadyAnnouncement,
  iterationRefQualifier,
  sameTarget,
  targetFromIterationRef,
  type IterationWaterfallNode,
} from './iterationReplayModel'
import { ANATOMY_PHASE_DEFINITIONS, ANATOMY_PHASE_LABELS, formatAnatomyMs, formatCount } from './timeAnatomyModel'
import './TimeAnatomy.css'
import './IterationReplay.css'

interface Props {
  testId: string
  index?: RunArchiveIterationIndex | null
  /** The iteration to show; set by the chips, the form, or another panel. */
  target: IterationReplayTarget | null
  onTargetChange: (target: IterationReplayTarget) => void
}

type LoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'error'; message: string; retryable: boolean }
  | { kind: 'ready'; data: ArchiveIterationWaterfall }

const PHASE_ROWS: readonly [keyof ArchiveWaterfallRow['phases'], string, string?][] = [
  ['dns_ms', ANATOMY_PHASE_LABELS.dns], ['connect_ms', ANATOMY_PHASE_LABELS.connect], ['tls_ms', ANATOMY_PHASE_LABELS.tls],
  ['server_ms', ANATOMY_PHASE_LABELS.wait, ANATOMY_PHASE_DEFINITIONS.wait], ['download_ms', ANATOMY_PHASE_LABELS.receive],
  ['unsplit_ms', ANATOMY_PHASE_LABELS.total, ANATOMY_PHASE_DEFINITIONS.total],
]

const LEGACY_COPY = 'Legacy archive: rows carry a VUser ID but no lifetime, so a reused VUser ID can mix another VUser\'s rows into this iteration.'

/**
 * The index counts VUser identities, not VUsers: a lifetime per VUser run (a
 * restarted or re-ramped VUser counts again), else a legacy VUser ID.
 */
function identityNoun(index: RunArchiveIterationIndex | null | undefined): string {
  const one = index?.identities === 1
  const quality = index?.identity_quality
  if (quality && quality.legacy === 0 && quality.lifetime > 0) return one ? 'VUser lifetime' : 'VUser lifetimes'
  return one ? 'VUser identity' : 'VUser identities'
}

function statusText(node: IterationWaterfallNode): string {
  if (node.kind === 'think') return 'Not recorded'
  if (node.status === 'incomplete') return 'Interrupted'
  return node.status === 'failed' ? 'Failed' : 'Passed'
}

function NodeDrawer({ node, data, thinkGapMs }: { node: IterationWaterfallNode; data: ArchiveIterationWaterfall; thinkGapMs: number }) {
  const row = node.payload?.row
  const source = node.timingSource ? TIMING_SOURCE_LABELS[node.timingSource] : null
  return (
    <div className="wf-drawer-inner">
      <header className="wf-drawer-header">
        <div>
          <strong>{node.label}</strong>
          <small>{[node.kind === 'request' ? 'Request' : node.kind === 'transaction' ? 'Transaction' : node.kind === 'think' ? 'Gap' : 'Iteration', statusText(node), row?.status_code ? `HTTP ${row.status_code}` : ''].filter(Boolean).join(' · ')}</small>
        </div>
        <code>{node.durationMs == null ? '—' : formatWaterfallMs(node.durationMs)}</code>
      </header>
      <div className="wf-drawer-panel">
        {node.kind === 'think' && <p className="wf-drawer-status">{THINK_GAP_LABEL}: the archive keeps no think-time or pacing rows, so this is the time between the previous row&apos;s end and the next row&apos;s start.</p>}
        {node.kind === 'iteration' && <dl className="wf-kv">
          {data.started_at && <><dt>Started</dt><dd><time dateTime={data.started_at}>{formatDateTime(data.started_at, { milliseconds: true })}</time></dd></>}
          <dt>Span</dt><dd>{formatAnatomyMs(data.span_ms)}</dd>
          <dt>Rows</dt><dd>{formatCount(data.rows_total)}</dd>
          <dt>Think time inside transactions</dt><dd>{data.think_ms_approximate ? 'About ' : ''}{formatAnatomyMs(data.think_ms_inside_transactions)}</dd>
          <dt>Not covered by any row</dt><dd>{data.gaps_ms == null ? 'Unknown (rows were cut or may be missing)' : formatAnatomyMs(data.gaps_ms)}</dd>
          {thinkGapMs > 0 && <><dt>Drawn as think rows</dt><dd>{formatAnatomyMs(thinkGapMs)}</dd></>}
        </dl>}
        {row && <>
          <dl className="wf-kv">
            {node.startMs != null && <><dt>Offset</dt><dd>+{formatWaterfallMs(node.startMs)}</dd></>}
            <dt>Duration</dt><dd>{formatAnatomyMs(row.duration_ms)}</dd>
            {row.elapsed_ms ? <><dt>Elapsed</dt><dd>{formatAnatomyMs(row.elapsed_ms)}</dd></> : null}
            {(node.payload?.thinkInsideMs ?? 0) > 0 && <><dt>Think time inside</dt><dd>{formatAnatomyMs(node.payload?.thinkInsideMs)} (elapsed − duration)</dd></>}
            {row.dependency_target && <><dt>Target</dt><dd>{row.dependency_target}</dd></>}
            {row.connection && <><dt>Connection</dt><dd>{row.connection}</dd></>}
            {row.source_transaction && <><dt>Script transaction</dt><dd>{row.source_transaction}</dd></>}
            {row.source_step_index ? <><dt>Step</dt><dd>{row.source_step_index}</dd></> : null}
            {row.error_class && <><dt>Error class</dt><dd>{row.error_class}</dd></>}
            {row.error_message && <><dt>Error</dt><dd>{row.error_message}</dd></>}
            {row.trace_id && <><dt>Trace ID</dt><dd>{row.trace_id}</dd></>}
            {row.clamped && <><dt>Phases</dt><dd>Clamped to fit the request duration</dd></>}
            {row.retried && <><dt>Retries</dt><dd>Retried</dd></>}
            {source && <><dt>Timing</dt><dd title={source.description}>{source.label}</dd></>}
          </dl>
          {row.population !== 'transaction' && <table className="tx-table">
            <caption className="sr-only">Phases of {row.action_name}</caption>
            <thead><tr><th scope="col">Phase</th><th scope="col">Time</th></tr></thead>
            <tbody>{PHASE_ROWS.map(([field, name, definition]) => <tr key={field}><th scope="row" title={definition}>{name}</th><td>{formatAnatomyMs(row.phases?.[field] ?? 0)}</td></tr>)}</tbody>
          </table>}
        </>}
      </div>
    </div>
  )
}

/** R4: one VUser iteration rebuilt from the archive as a debug-style waterfall. */
export default function IterationReplay({ testId, index, target, onTargetChange }: Props) {
  const api = useApi()
  const headingId = useId()
  const formId = useId()
  const [state, setState] = useState<LoadState>({ kind: 'idle' })
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const representatives = useMemo(() => index?.representatives ?? [], [index])
  const groups = useMemo(() => [...new Set(representatives.map(ref => ref.group).filter((group): group is string => Boolean(group)))], [representatives])
  const [group, setGroup] = useState(() => representatives[0]?.group ?? '')
  const [vuser, setVuser] = useState('')
  const [iteration, setIteration] = useState('')

  useEffect(() => {
    if (!target || !canReplay(target)) {
      setState({ kind: 'idle' })
      return undefined
    }
    const controller = new AbortController()
    setState({ kind: 'loading' })
    setSelectedId(null)
    api.getTestArchiveIterationWaterfall(testId, iterationParams(target), { signal: controller.signal })
      .then(data => { if (!controller.signal.aborted) setState({ kind: 'ready', data }) })
      .catch((error: unknown) => { if (!controller.signal.aborted) setState({ kind: 'error', message: iterationErrorCopy(error), retryable: iterationErrorIsBusy(error) }) })
    return () => controller.abort()
  }, [api, attempt, target, testId])

  // A target from elsewhere fills the form, so it can be adjusted.
  useEffect(() => {
    if (!target) return
    setGroup(target.group ?? '')
    setVuser(target.vuser_id != null ? String(target.vuser_id) : '')
    setIteration(target.iteration ? String(target.iteration) : '')
  }, [target])

  const tree = useMemo(() => (state.kind === 'ready' ? buildIterationTree(state.data) : null), [state])
  const selected = tree && selectedId ? findNode(tree.roots, selectedId) : null

  // Unchanged fields resubmit the shown target, lifetime and anchors included.
  const pending = formTarget({ group, vuser, iteration }, target)
  const formValid = pending != null
  const vuserZeroByID = !pending && vuser.trim() !== '' && Number(vuser) === 0
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) onTargetChange({ ...pending })
  }

  const hasIndex = Boolean(index && index.iterations_total > 0)
  const span = index?.span
  const approximate = span?.percentile_quality ? ' (approximate)' : ''

  return (
    <section className="run-report-panel run-report-time-anatomy run-report-iteration-replay" data-report-focus="iteration-replay" aria-labelledby={headingId}>
      <header className="rr-anatomy-header">
        <h2 id={headingId}>Iteration replay</h2>
        {hasIndex && span && span.samples > 0 && <p className="rr-anatomy-duration">
          {formatCount(index?.iterations_total)} iterations from {formatCount(index?.identities)} {identityNoun(index)}{(index?.identities_evicted ?? 0) > 0 && ' (an evicted one that wrote again counts twice)'} · span P50 {formatAnatomyMs(span.p50_ms)}{approximate} · P95 {formatAnatomyMs(span.p95_ms)}{approximate} · max {formatAnatomyMs(span.max_ms)}
          {(index?.partial_iterations ?? 0) > 0 && <> · span of {formatCount(span.samples)} whole iterations ({formatCount(index?.partial_iterations)} cut short by the test's stop or split are left out)</>}
        </p>}
      </header>
      <p className="rr-anatomy-note">One VUser iteration rebuilt from the archived rows. Think time and pacing between transactions are not recorded, so they show as gaps.</p>

      {!index && <p className="rr-anatomy-callout">No iteration index in this report: the archive has no rows with a VUser iteration.</p>}
      {index && index.iterations_total === 0 && <p className="rr-anatomy-callout">The iteration rows carry no VUser ID or lifetime, so iterations cannot be told apart.</p>}
      {(index?.late_rows ?? 0) > 0 && <p className="rr-anatomy-note">{formatCount(index!.late_rows!)} {index!.late_rows === 1 ? 'row was' : 'rows were'} archived after {index!.late_rows === 1 ? 'its' : 'their'} VUser had moved two or more iterations on: the spans here leave {index!.late_rows === 1 ? 'it' : 'them'} out, so an iteration&apos;s replay can be longer than its chip.</p>}
      {index?.reasons?.includes('identity_evicted') && <p className="rr-anatomy-note">Some long-idle VUsers were dropped from the index to bound memory; an iteration of theirs may be split in two.</p>}

      {representatives.length > 0 && <div className="rr-anatomy-chips" role="group" aria-label="Iterations to replay">
        {representatives.map(ref => {
          const refTarget = targetFromIterationRef(ref)
          return <button key={`${ref.reason}-${ref.first_ordinal}`} type="button"
            className={`btn btn-secondary btn-sm rr-anatomy-chip${ref.reason === 'failed' ? ' rr-anatomy-chip-failed' : ''}`}
            aria-pressed={sameTarget(target, refTarget)}
            aria-label={`${ITERATION_REASON_LABELS[ref.reason]} iteration${iterationRefQualifier(ref) ? ` (${iterationRefQualifier(ref)})` : ''}: ${formatAnatomyMs(ref.span_ms)}, ${iterationIdentityLabel(ref, true)}${ref.failed_rows ? `, ${formatCount(ref.failed_rows)} failed rows` : ''}`}
            onClick={() => onTargetChange(refTarget)}>{iterationChipLabel(ref)}</button>
        })}
      </div>}

      {(hasIndex || target) && <form className="rr-iteration-form" aria-label="Replay a VUser iteration" onSubmit={submit}>
        <label>Group <input value={group} list={groups.length ? `${formId}-groups` : undefined} autoComplete="off" maxLength={512} onChange={event => setGroup(event.currentTarget.value)} /></label>
        {groups.length > 0 && <datalist id={`${formId}-groups`}>{groups.map(item => <option key={item} value={item}>{item}</option>)}</datalist>}
        <label>VUser <input type="number" min={0} step={1} inputMode="numeric" required value={vuser} aria-describedby={vuserZeroByID ? `${formId}-vuser-zero` : undefined} onChange={event => setVuser(event.currentTarget.value)} /></label>
        <label>Iteration <input type="number" min={1} step={1} inputMode="numeric" required value={iteration} onChange={event => setIteration(event.currentTarget.value)} /></label>
        <button type="submit" className="btn btn-secondary btn-sm" disabled={!formValid}>Replay</button>
        {vuserZeroByID && <small id={`${formId}-vuser-zero`} className="rr-iteration-form-hint">VUser 0 can be replayed only from an iteration link or chip: they carry its VUser lifetime.</small>}
      </form>}

      <div aria-live="polite">
        {state.kind === 'loading' && <p role="status" className="rr-anatomy-loading"><span className="rr-anatomy-spinner" aria-hidden="true" />Rebuilding iteration...</p>}
        {/* The result renders below, outside this region: announce its completion here. */}
        {state.kind === 'ready' && <p className="sr-only">{iterationReadyAnnouncement(state.data)}</p>}
      </div>
      {state.kind === 'error' && <p role="alert" className="run-report-error">{state.message}</p>}
      {state.kind === 'error' && state.retryable && <button type="button" className="btn btn-secondary btn-sm" onClick={() => setAttempt(value => value + 1)}>Retry</button>}
      {state.kind === 'ready' && tree && <>
        <h3>{target?.source ? `${target.source}: ` : ''}{iterationIdentityLabel(state.data, true)} · {formatAnatomyMs(state.data.span_ms)}</h3>
        {state.data.identity_quality === 'legacy' && <p className="rr-anatomy-callout">{LEGACY_COPY}</p>}
        {state.data.truncated && <p className="rr-anatomy-callout">Showing the first {formatCount(state.data.rows.length)} of {formatCount(state.data.rows_total)} rows by start time.</p>}
        {state.data.partial && <p className="rr-anatomy-callout">{iterationPartialCopy(state.data.partial_reasons)}</p>}
        <p className="rr-anatomy-composition">
          {formatCount(state.data.rows_total)} rows · {state.data.think_ms_approximate ? 'about ' : ''}{formatAnatomyMs(state.data.think_ms_inside_transactions)} think time inside transactions
          {state.data.gaps_ms != null ? ` · ${formatAnatomyMs(state.data.gaps_ms)} not covered by any row (think time or pacing)` : ''}
        </p>
        <Waterfall<IterationWaterfallNode['payload']>
          roots={tree.roots}
          spanMs={tree.spanMs}
          ariaLabel={`Iteration ${state.data.iteration} of VUser ${state.data.vuser_id ?? 0}`}
          selectedId={selectedId}
          onSelect={node => setSelectedId(node.id)}
          drawer={selected
            ? <NodeDrawer key={selected.id} node={selected} data={state.data} thinkGapMs={tree.thinkGapMs} />
            : <p className="wf-drawer-status rr-iteration-drawer-hint">Select a row to see its phases, status, error and trace ID.</p>}
          emptyMessage="No rows in this iteration."
        />
      </>}
    </section>
  )
}
