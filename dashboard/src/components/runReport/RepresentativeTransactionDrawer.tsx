import { useEffect, useId, useMemo, useRef, useState } from 'react'

import DialogLayer from '../ux/DialogLayer'
import { useApi } from '../../hooks/useApi'
import { useModalDialog } from '../../hooks/useModalDialog'
import type { ArchiveTransactionWaterfall, ArchiveWaterfallRow, RunArchiveRepresentativeSample } from '../../types/timeAnatomy'
import { formatDateTime } from '../../utils/dateTime'
import ReportPhaseBars from './ReportPhaseBars'
import {
  ANATOMY_PHASE_DEFINITIONS,
  ANATOMY_PHASE_LABELS,
  LEGACY_IDENTITY_COPY,
  REPRESENTATIVE_LABELS,
  aggregateSpanMs,
  compositionSentence,
  drilldownErrorCopy,
  drilldownErrorIsBusy,
  drilldownHints,
  drilldownIsPartial,
  drilldownJoinMayDiffer,
  flattenNodes,
  formatAnatomyMs,
  formatCount,
  joinMayDifferCopy,
  partialDrilldownCopy,
  waterfallRootsFromDrilldown,
} from './timeAnatomyModel'
import './TimeAnatomy.css'

interface Props {
  testId: string
  transactionName: string
  sample: RunArchiveRepresentativeSample
  onClose: () => void
}

type LoadState =
  | { kind: 'loading' }
  | { kind: 'error'; message: string; retryable: boolean }
  | { kind: 'ready'; data: ArchiveTransactionWaterfall }

const PHASE_ROWS: readonly [keyof ArchiveWaterfallRow['phases'], string, string?][] = [
  ['dns_ms', ANATOMY_PHASE_LABELS.dns], ['connect_ms', ANATOMY_PHASE_LABELS.connect], ['tls_ms', ANATOMY_PHASE_LABELS.tls],
  ['server_ms', ANATOMY_PHASE_LABELS.wait, ANATOMY_PHASE_DEFINITIONS.wait], ['download_ms', ANATOMY_PHASE_LABELS.receive],
  ['unsplit_ms', ANATOMY_PHASE_LABELS.total, ANATOMY_PHASE_DEFINITIONS.total],
]

/** R3: one representative instance rebuilt from the archive, with its own requests. */
export default function RepresentativeTransactionDrawer({ testId, transactionName, sample, onClose }: Props) {
  const api = useApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [selectedId, setSelectedId] = useState('')
  const [attempt, setAttempt] = useState(0)

  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })

  useEffect(() => {
    const controller = new AbortController()
    setState({ kind: 'loading' })
    setSelectedId('')
    api.getTestArchiveTransactionWaterfall(testId, sample.event_id, drilldownHints({ ordinal: sample.ordinal, first_ordinal: sample.first_ordinal }), { signal: controller.signal })
      .then(data => { if (!controller.signal.aborted) setState({ kind: 'ready', data }) })
      .catch((error: unknown) => { if (!controller.signal.aborted) setState({ kind: 'error', message: drilldownErrorCopy(error), retryable: drilldownErrorIsBusy(error) }) })
    return () => controller.abort()
  }, [api, attempt, sample.event_id, sample.first_ordinal, sample.ordinal, testId])

  const roots = useMemo(() => (state.kind === 'ready' ? waterfallRootsFromDrilldown(state.data) : []), [state])
  const selectedRow = useMemo(() => flattenNodes(roots).find(({ node }) => node.id === selectedId)?.node.payload, [roots, selectedId])
  const label = REPRESENTATIVE_LABELS[sample.reason] ?? sample.reason

  return (
    <DialogLayer><div className="modal-overlay rr-anatomy-drawer-overlay" role="presentation" onPointerDown={event => { if (event.target === event.currentTarget) onClose() }}>
      <div ref={dialogRef} className="modal run-report-anatomy-drawer" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <div>
            <h3 id={titleId}>{label} {transactionName} · {formatAnatomyMs(sample.duration_ms)}</h3>
            <p className="page-subtitle">
              {[sample.group, sample.vuser_id ? `VUser ${sample.vuser_id}` : '', sample.iteration ? `iteration ${sample.iteration}` : '', formatDateTime(sample.timestamp)].filter(Boolean).join(' · ')}
            </p>
          </div>
          <button ref={closeRef} type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button>
        </div>
        <div className="rr-anatomy-drawer-body">
          {state.kind === 'loading' && <p role="status" className="rr-anatomy-loading"><span className="rr-anatomy-spinner" aria-hidden="true" />Rebuilding transaction...</p>}
          {state.kind === 'error' && <p role="alert" className="run-report-error">{state.message}</p>}
          {state.kind === 'error' && state.retryable && <button type="button" className="btn btn-secondary btn-sm" onClick={() => {
            // Retry unmounts this button; keep focus inside the dialog.
            closeRef.current?.focus()
            setAttempt(value => value + 1)
          }}>Retry</button>}
          {state.kind === 'ready' && <>
            {state.data.identity_quality === 'legacy' && <p className="rr-anatomy-callout">{LEGACY_IDENTITY_COPY}</p>}
            {state.data.truncated && <p className="rr-anatomy-callout">Showing first {formatCount(state.data.requests.length)} of {formatCount(state.data.requests_total)} requests.</p>}
            {(state.data.late_requests ?? 0) > 0 && <p className="rr-anatomy-callout">{formatCount(state.data.late_requests)} requests were written after this transaction and are excluded.</p>}
            {drilldownIsPartial(state.data) && <p className="rr-anatomy-callout">{partialDrilldownCopy(state.data.partial_reasons)}</p>}
            {drilldownJoinMayDiffer(state.data) && <p className="rr-anatomy-callout">{joinMayDifferCopy(state.data.join_may_differ_reasons)}</p>}
            {state.data.decomposition && !drilldownIsPartial(state.data) && <p className="rr-anatomy-composition">{compositionSentence(`This ${transactionName}`, state.data.decomposition, '')}</p>}
            {state.data.enclosing_transactions?.length ? <p>Inside {state.data.enclosing_transactions.map(ref => ref.action_name).join(' › ')}</p> : null}
            <ReportPhaseBars roots={roots} spanMs={aggregateSpanMs(roots)} ariaLabel={`Waterfall for ${label.toLowerCase()} ${transactionName}`} selectedId={selectedId} onSelect={setSelectedId} />
            {selectedRow && <section className="rr-anatomy-row-pane" aria-label={`${selectedRow.action_name} details`}>
              <h4>{selectedRow.action_name}</h4>
              <dl className="rr-anatomy-facts">
                <dt>Status</dt><dd>{selectedRow.success ? 'Passed' : 'Failed'}{selectedRow.status_code ? ` · HTTP ${selectedRow.status_code}` : ''}{selectedRow.interrupted ? ' · interrupted' : ''}</dd>
                <dt>Starts at</dt><dd>{formatAnatomyMs(selectedRow.offset_ms)}</dd>
                <dt>Duration</dt><dd>{formatAnatomyMs(selectedRow.duration_ms)}</dd>
                {selectedRow.connection && <><dt>Connection</dt><dd>{selectedRow.connection}</dd></>}
                {selectedRow.source_step_index ? <><dt>Step</dt><dd>{selectedRow.source_step_index}</dd></> : null}
                {selectedRow.error_message && <><dt>Error</dt><dd>{selectedRow.error_message}</dd></>}
                {selectedRow.clamped && <><dt>Phases</dt><dd>Clamped to fit the request duration</dd></>}
                {selectedRow.retried && <><dt>Retries</dt><dd>Retried</dd></>}
              </dl>
              <table className="tx-table">
                <thead><tr><th scope="col">Phase</th><th scope="col">Time</th></tr></thead>
                <tbody>{PHASE_ROWS.map(([field, name, definition]) => <tr key={field}><th scope="row" title={definition}>{name}</th><td>{formatAnatomyMs(selectedRow.phases?.[field] ?? 0)}</td></tr>)}</tbody>
              </table>
            </section>}
          </>}
        </div>
      </div>
    </div></DialogLayer>
  )
}
