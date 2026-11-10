import { useEffect, useId, useMemo, useRef, useState } from 'react'
import DialogLayer from './ux/DialogLayer'
import { useOptionalApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { LIVE_TRACE_LINKS_MAX_IDS, type ExemplarLink, type LiveTraceLinksResponse } from '../types/rca'
import { formatTimeOfDay } from '../utils/dateTime'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { formatWaterfallMs } from './waterfall/phases'
import { EXEMPLAR_RETENTION_MS, EXEMPLAR_WINDOW_TOLERANCE_MS, exemplarWindowsInRange, exemplarsForTransactions, nearestExemplarWindow, slowestExemplarWindowInRange, type ExemplarTimeRange, type ExemplarWindow } from './liveWindowRetention'
import './LiveWaterfallPanel.css'

/**
 * At most this many trace ids per link request: the endpoint's limit
 * (maxLiveTraceLinkIDs, pinned by a Go test), sized so one request carries a
 * whole window's exemplars (EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW x 3), so the
 * server resolves the run's link targets once per window.
 */
const LIVE_TRACE_LINK_BATCH = LIVE_TRACE_LINKS_MAX_IDS
/** Links open the trace this far either side of its window. */
const LINK_PAD_MS = 5 * 60_000

const REASON_LABELS: Record<string, string> = {
  slowest_success: 'Slowest success',
  first_error: 'First error',
  sample: 'Sample',
}

const LINK_PROVIDER_LABELS: Record<string, string> = {
  dynatrace: 'Dynatrace',
  elasticsearch: 'Kibana (ELK)',
}

const linkLabel = (link: ExemplarLink) => `Open in ${LINK_PROVIDER_LABELS[link.provider] ?? link.provider}`

interface Props {
  testId: string
  ring: readonly ExemplarWindow[]
  /** The clicked time; the nearest retained window within tolerance opens. */
  atMs: number
  /**
   * The span the clicked point averages (a time-bucketed chart point): the
   * window in it with the slowest traced request opens instead, and nothing
   * outside it does.
   */
  range?: ExemplarTimeRange
  /** Transactions the chart shows; empty (with `groups` empty) for all. */
  transactionNames: readonly string[]
  /** Groups the chart shows: their transactions are listed too. */
  groups?: readonly string[]
  firstWindowMs?: number
  /** Where focus returns on close when the opener is gone (e.g. a closed menu item). */
  returnFocusSelector?: string
  onClose: () => void
}

const NO_GROUPS: readonly string[] = []

type LinkState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: LiveTraceLinksResponse; partial?: boolean }
  | { kind: 'error'; message: string }

export default function LiveExemplarDrawer({ testId, ring, atMs, range, transactionNames, groups: groupFilter = NO_GROUPS, firstWindowMs, returnFocusSelector, onClose }: Props) {
  const api = useOptionalApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose, returnFocusSelector })

  // The ring keeps growing while the drawer is open: pin the window by time.
  const [windowMs, setWindowMs] = useState<number | null>(() => (
    range ? slowestExemplarWindowInRange(ring, range, transactionNames, groupFilter) : nearestExemplarWindow(ring, atMs)
  )?.tsMs ?? null)
  // A bucket longer than a second averages several windows: say which one opened.
  const [bucket] = useState(() => (range && range.endMs - range.startMs > 1000
    ? { range, kept: exemplarWindowsInRange(ring, range).length }
    : undefined))
  const current = windowMs === null ? undefined : ring.find(w => w.tsMs === windowMs)
  const index = current ? ring.indexOf(current) : -1
  // The time the drawer is about: the pinned window, else the clicked time.
  // A pinned window the ring has since pruned keeps its time, so the
  // neighbours, the header and the retention note stay about that window.
  const focusMs = windowMs ?? atMs
  const agedOut = windowMs !== null && !current
  const earlier = index > 0 ? ring[index - 1] : index === 0 ? undefined : [...ring].reverse().find(w => w.tsMs < focusMs)
  const later = index >= 0 ? ring[index + 1] : ring.find(w => w.tsMs > focusMs)
  const groups = useMemo(() => exemplarsForTransactions(current, transactionNames, groupFilter), [current, transactionNames, groupFilter])
  // Every distinct trace id, requested in endpoint-sized batches.
  const traceIds = useMemo(() => [...new Set(groups.flatMap(g => g.exemplars.map(e => e.trace_id)))], [groups])
  const traceKey = traceIds.join(',')
  const [links, setLinks] = useState<LinkState>({ kind: 'idle' })
  // `attempt` counts presses, so a repeat copy of the same id changes the
  // live-region text and is announced again.
  const [copy, setCopy] = useState<{ traceId: string; ok: boolean; attempt: number } | null>(null)

  useEffect(() => {
    if (!api || !current || !traceKey) {
      setLinks({ kind: 'idle' })
      return
    }
    const controller = new AbortController()
    setLinks({ kind: 'loading' })
    const ids = traceKey.split(',')
    const batches: string[][] = []
    for (let i = 0; i < ids.length; i += LIVE_TRACE_LINK_BATCH) batches.push(ids.slice(i, i + LIVE_TRACE_LINK_BATCH))
    const from = new Date(current.tsMs - LINK_PAD_MS).toISOString()
    const to = new Date(current.tsMs + LINK_PAD_MS).toISOString()
    // One request per window in practice; a failed batch drops only its own links.
    void Promise.allSettled(batches.map(trace_ids => api.getLiveTraceLinks(testId, { trace_ids, from, to }, { signal: controller.signal })))
      .then(results => {
        if (controller.signal.aborted) return
        const responses = results.flatMap(r => (r.status === 'fulfilled' ? [r.value] : []))
        if (!responses.length) {
          setLinks({ kind: 'error', message: 'APM links could not be loaded.' })
          return
        }
        // Configuration is per run, so every batch answers it the same way; traces add up.
        const data: LiveTraceLinksResponse = {
          ...responses[0],
          configured: responses.some(r => r.configured),
          links_unavailable: responses.some(r => r.links_unavailable) || undefined,
          traces: responses.flatMap(r => r.traces ?? []),
        }
        setLinks({ kind: 'ready', data, partial: responses.length < results.length })
      })
    return () => controller.abort()
  }, [api, current, testId, traceKey])

  const linksFor = (traceId: string) => (links.kind === 'ready' ? links.data.traces.find(t => t.trace_id === traceId)?.links ?? [] : [])
  const copyTrace = async (traceId: string) => {
    const ok = await copyTextToClipboard(traceId)
    setCopy(prev => ({ traceId, ok, attempt: prev?.traceId === traceId && prev.ok === ok ? prev.attempt + 1 : 1 }))
  }
  const atText = formatTimeOfDay(current?.tsMs ?? focusMs, { seconds: true })
  const rangeText = range ? `${formatTimeOfDay(range.startMs, { seconds: true })} to ${formatTimeOfDay(range.endMs, { seconds: true })}` : ''
  const outsideRetention = agedOut || (firstWindowMs !== undefined && focusMs < Math.max(firstWindowMs, (ring[ring.length - 1]?.tsMs ?? focusMs) - EXEMPLAR_RETENTION_MS) - EXEMPLAR_WINDOW_TOLERANCE_MS)

  return (
    <DialogLayer><div className="modal-overlay" role="presentation" onPointerDown={event => { if (event.target === event.currentTarget) onClose() }}>
      <div ref={dialogRef} className="modal live-exemplar-drawer" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <div>
            <h3 id={titleId}>Trace exemplars · {atText}</h3>
            <p className="page-subtitle">Up to three requests per transaction and window that carried trace context: the slowest success, the first error and a sample. Each generator picks its sample at random; in a distributed run the fleet keeps one generator's sample, so it is not drawn across every generator.</p>
          </div>
          <button ref={closeRef} type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button>
        </div>
        <div className="live-exemplar-body">
          <div className="live-exemplar-nav" role="group" aria-label="Exemplar windows">
            {/* aria-disabled, not disabled: paging to the last window must not
                drop focus out of the dialog when the pressed button runs out. */}
            <button type="button" className="btn btn-secondary btn-sm" aria-disabled={!earlier} onClick={() => earlier && setWindowMs(earlier.tsMs)}>Earlier window</button>
            <button type="button" className="btn btn-secondary btn-sm" aria-disabled={!later} onClick={() => later && setWindowMs(later.tsMs)}>Later window</button>
            <span className="live-exemplar-window" aria-live="polite">{current ? `Window at ${atText}` : agedOut ? `Window at ${atText} is no longer kept` : range ? `No retained window from ${rangeText}` : `No retained window within ${EXEMPLAR_WINDOW_TOLERANCE_MS / 1000} s of ${atText}`}</span>
          </div>
          {/* With none kept, the note below says no request in the span carried trace context. */}
          {bucket && bucket.kept > 0 && (
            <p className="live-exemplar-note" role="note">
              The clicked point averages every window from {rangeText}. {bucket.kept === 1 ? 'One of them' : `${bucket.kept.toLocaleString()} of them`} kept exemplars; this opened the one with the slowest traced request for the transactions shown. Use Earlier window and Later window to see the others.
            </p>
          )}
          {!ring.length ? (
            <p className="live-exemplar-note" role="status">No trace exemplars received yet. Exemplars are recorded only for requests that send trace context (a W3C traceparent header), so a script without trace propagation has none.</p>
          ) : !current ? (
            <p className="live-exemplar-note" role="status">{agedOut
                ? 'This window is now older than the exemplars this page keeps (the last 15 minutes it received). Use Later window to move to the oldest one kept.'
                : outsideRetention ? 'This time is older than the exemplars this page keeps (the last 15 minutes it received).' : range ? `No request from ${rangeText} carried trace context.` : 'No request in this window carried trace context.'}</p>
          ) : !groups.length ? (
            <p className="live-exemplar-note" role="status">{current.truncated
              ? `This page keeps exemplars for only the first ${current.truncated.kept.toLocaleString()} of the ${current.truncated.seen.toLocaleString()} traced transactions in this window, and the transactions shown on this chart are not among them.`
              : 'The transactions shown on this chart have no exemplars in this window.'}</p>
          ) : (
            <>
              {current.truncated && (
                <p className="live-exemplar-note" role="status">This page keeps exemplars for only the first {current.truncated.kept.toLocaleString()} of the {current.truncated.seen.toLocaleString()} traced transactions in this window, so a transaction missing here may still have had some.</p>
              )}
              {links.kind === 'loading' && <p className="live-exemplar-note" role="status">Loading APM links…</p>}
              {links.kind === 'error' && <p className="live-exemplar-note" role="alert">{links.message}</p>}
              {links.kind === 'ready' && links.partial && <p className="live-exemplar-note" role="status">Some APM links could not be loaded; the trace ids without links are still listed.</p>}
              {links.kind === 'ready' && links.data.links_unavailable && <p className="live-exemplar-note" role="status">APM link settings could not be read; trace ids are still listed.</p>}
              {links.kind === 'ready' && !links.data.links_unavailable && !links.data.configured && <p className="live-exemplar-note" role="status">No APM connection with a trace link is configured for this run. Bind a Dynatrace or Elasticsearch connection to open traces from here.</p>}
              {groups.map(group => (
                <section key={group.name} className="live-exemplar-group" aria-label={`${group.name} exemplars`}>
                  <h4>{group.name}</h4>
                  <table className="live-exemplar-table">
                    <thead><tr><th scope="col">Reason</th><th scope="col">Duration</th><th scope="col">Status</th><th scope="col">Started</th><th scope="col">Trace</th></tr></thead>
                    <tbody>
                      {group.exemplars.map(e => (
                        <tr key={`${e.reason}-${e.trace_id}`}>
                          <td>{REASON_LABELS[e.reason] ?? e.reason}</td>
                          <td>{formatWaterfallMs(e.duration_ms)}</td>
                          <td>{e.success ? 'Passed' : 'Failed'}{e.status_code ? ` · HTTP ${e.status_code}` : ''}</td>
                          <td>{formatTimeOfDay(e.timestamp, { seconds: true })}</td>
                          <td>
                            <span className="live-exemplar-trace">
                              <code>{e.trace_id}</code>
                              <button
                                type="button"
                                className="btn btn-ghost btn-sm"
                                aria-label={`${copy?.traceId === e.trace_id ? (copy.ok ? 'Copied' : 'Copy failed for') : 'Copy'} trace id ${e.trace_id}`}
                                onClick={() => void copyTrace(e.trace_id)}
                              >
                                {copy?.traceId === e.trace_id ? (copy.ok ? 'Copied' : 'Copy failed') : 'Copy'}
                              </button>
                            </span>
                            {copy?.traceId === e.trace_id && !copy.ok && <span className="live-exemplar-copy-error">Copy failed: select the id and copy it by hand.</span>}
                            {linksFor(e.trace_id).length > 0 && (
                              <span className="live-exemplar-links">
                                {linksFor(e.trace_id).map(link => (
                                  <a key={`${link.provider}-${link.connection_id}`} href={link.url} target="_blank" rel="noopener noreferrer">{linkLabel(link)}</a>
                                ))}
                              </span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </section>
              ))}
            </>
          )}
          <p className="sr-only" role="status" aria-live="polite">{copy ? `${copy.ok ? `Copied trace id ${copy.traceId}` : `Could not copy trace id ${copy.traceId}`}${copy.attempt > 1 ? ` (${copy.attempt} times)` : ''}` : ''}</p>
          <p className="live-exemplar-note">This page keeps the exemplars of the last 15 minutes it received; earlier windows and reloads start empty.</p>
        </div>
      </div>
    </div></DialogLayer>
  )
}
