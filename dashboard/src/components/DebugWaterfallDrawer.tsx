import { Fragment, useId, useMemo, useState } from 'react'
import TabList from './ux/TabList'
import type { BrowserReplayEvent } from '../types/replay'
import { HTTPVersionReplayEvidence } from './HTTPVersionReplayEvidence'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import { HTTPNetworkTraceEvidence } from './HTTPNetworkTraceEvidence'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import { ReplayExtractions } from './ReplayExtractions'
import { appliedReplayExtractions } from './replayExtractionValues'
import { usedCorrelations } from './waterfall/correlation'
import { debugTransactionTimingBreakdown } from './debugTransactionModel'
import { formatDateTime } from '../utils/dateTime'
import { downloadBlob } from '../utils/fileUtils'
import { dominantPhase, isTimed, phaseTotals, selfChildTime } from './waterfall/analysis'
import { TIMING_SOURCE_LABELS, WATERFALL_PHASES, WATERFALL_PHASE_BY_KEY, formatWaterfallMs } from './waterfall/phases'
import type { DebugTimelineRowModel } from './waterfall/debugEventModel'
import type { DebugWaterfallNode } from './waterfall/debugTree'
import { debugRequestView, toCurl, toHarLog } from './waterfall/debugRequestExport'
import { curlCommandGaps } from './requestResponseInspectorModel'

type DrawerTab = 'timing' | 'request' | 'response' | 'headers' | 'correlation' | 'transport'
const TABS: Array<[DrawerTab, string]> = [
  ['timing', 'Timing'],
  ['request', 'Request'],
  ['response', 'Response'],
  ['headers', 'Headers'],
  ['correlation', 'Correlation'],
  ['transport', 'Transport'],
]

function HeaderTable({ caption, headers }: { caption: string; headers?: Record<string, string> }) {
  const entries = Object.entries(headers || {})
  return (
    <>
      <h5>{caption} ({entries.length})</h5>
      {entries.length ? (
        <table className="wf-headers">
          <caption className="sr-only">{caption}</caption>
          <tbody>{entries.map(([k, v]) => <tr key={k}><th scope="row">{k}</th><td>{v}</td></tr>)}</tbody>
        </table>
      ) : <p className="wf-drawer-status">None retained.</p>}
    </>
  )
}

function Breakdown({ row }: { row: DebugTimelineRowModel }) {
  return (
    <div className="wf-breakdown" aria-label="Timing breakdown">
      {row.breakdown.map((item, idx) => {
        const showCat = item.category !== 'execution' && item.category !== (idx > 0 ? row.breakdown[idx - 1].category : null)
        return (
          <div key={item.key}>
            {showCat && <div className="wf-breakdown-category">{item.category === 'connection' ? 'Connection preparation' : 'Request and response'}</div>}
            <div className="wf-breakdown-row">
              <span><i className={`wf-swatch phase-${item.key}`} aria-hidden="true" /><span>{item.label}</span></span>
              <div className="wf-breakdown-track" aria-hidden="true">
                {item.durationMs > 0 && <span className={`phase-${item.key}`} style={{ left: `${item.startPct}%`, width: `${item.widthPct}%` }} />}
              </div>
              <span>{item.durationMs > 0 ? `${item.durationMs.toFixed(1)} ms` : item.unavailableReason || '—'}</span>
            </div>
          </div>
        )
      })}
    </div>
  )
}


export function DebugWaterfallDrawer({
  node,
  row,
  events,
  position,
  onInspect,
  canRevealInScript,
  onRevealInScript,
  canEdit = true,
  onEdit,
}: {
  node: DebugWaterfallNode
  row?: DebugTimelineRowModel
  events: readonly BrowserReplayEvent[]
  position?: number
  onInspect?: () => void
  canRevealInScript?: boolean
  onRevealInScript?: () => void
  canEdit?: boolean
  onEdit?: () => void
}) {
  const baseId = useId().replace(/:/g, '')
  const [tab, setTab] = useState<DrawerTab>('timing')
  const [copyStatus, setCopyStatus] = useState('')
  const event = row?.event
  const view = useMemo(() => (event ? debugRequestView(event) : null), [event])
  const used = useMemo(() => (event && position != null ? usedCorrelations(event, events.slice(0, position)) : []), [event, events, position])
  const hop = node.kind === 'hop' && node.payload?.hopIndex != null && row ? row.hops[node.payload.hopIndex] : undefined
  const tx = node.payload?.transaction
  const split = selfChildTime(node)
  const dom = dominantPhase(node)
  const source = node.timingSource ? TIMING_SOURCE_LABELS[node.timingSource] : null

  // Clear first so repeating the same message is announced again.
  const announce = (message: string) => {
    setCopyStatus('')
    window.setTimeout(() => setCopyStatus(message), 50)
  }

  const copyCurl = async () => {
    if (!view) return
    try {
      await navigator.clipboard.writeText(toCurl(view))
      announce('cURL copied')
    } catch {
      announce('Clipboard unavailable')
    }
  }
  const openHar = async () => {
    if (!view || !row) return
    try {
      const blob = new Blob([JSON.stringify(toHarLog(view, row), null, 2)], { type: 'application/json' })
      const name = (row.actionName || 'request').replace(/[^A-Za-z0-9._-]+/g, '-').slice(0, 60) || 'request'
      await downloadBlob(blob, `${name}.har`)
      announce('HAR saved')
    } catch (error) {
      announce(error instanceof Error ? error.message : 'HAR export failed')
    }
  }

  // Parent rows (iterations, transactions, containers) get a summary instead of tabs.
  if (!row) {
    const totals = phaseTotals(node)
    const breakdown = tx ? debugTransactionTimingBreakdown(tx) : null
    const children = node.children.filter(isTimed).sort((a, b) => (b.durationMs ?? 0) - (a.durationMs ?? 0)).slice(0, 5)
    return (
      <div className="wf-drawer-inner">
        <header className="wf-drawer-header">
          <div>
            <strong>{node.label}</strong>
            <small>{node.detail || node.kind} · {node.status === 'not_run' ? 'Not run' : node.status[0].toUpperCase() + node.status.slice(1)}</small>
          </div>
          <code>{node.durationMs == null ? '—' : formatWaterfallMs(node.durationMs)}</code>
        </header>
        {onInspect && (
          <div className="wf-drawer-actions">
            <button type="button" className="btn btn-sm btn-ghost" aria-label={`Inspect ${node.label || 'action'}`} onClick={onInspect}>Inspect</button>
          </div>
        )}
        <div className="wf-drawer-panel">
          <dl className="wf-kv">
            {tx?.startedAt && <><dt>Start time</dt><dd><time dateTime={tx.startedAt}>{formatDateTime(tx.startedAt, { milliseconds: true })}</time></dd></>}
            {tx?.completedAt && <><dt>End time</dt><dd><time dateTime={tx.completedAt}>{formatDateTime(tx.completedAt, { milliseconds: true })}</time></dd></>}
            {node.startMs != null && <><dt>Offset</dt><dd>+{formatWaterfallMs(node.startMs)}</dd></>}
            {split && <><dt>Child time</dt><dd>{formatWaterfallMs(split.childMs)}</dd><dt>Self time</dt><dd>{formatWaterfallMs(split.selfMs)} (outside children: think time, pacing, client work)</dd></>}
            {dom && <><dt>Dominant phase</dt><dd>{WATERFALL_PHASE_BY_KEY[dom.key].label} · {formatWaterfallMs(dom.ms)} ({Math.round(dom.share * 100)}%)</dd></>}
            {tx?.recordedDurationMs != null && <><dt>Action time</dt><dd>{formatWaterfallMs(tx.recordedDurationMs)}</dd></>}
          </dl>
          {breakdown && (
            <p role="note" aria-label={`Timing breakdown for ${node.label}`} className="wf-drawer-status">
              {formatWaterfallMs(breakdown.childSpanTotalMs)} in child spans − {formatWaterfallMs(breakdown.overlapMs)} counted more than once + {formatWaterfallMs(breakdown.outsideChildrenMs)} outside child spans ≈ {formatWaterfallMs(breakdown.parentElapsedMs)} parent elapsed.
            </p>
          )}
          {tx?.status === 'Not observed' && <p className="wf-drawer-status">Not observed: run evidence was dropped, so this transaction may have run without being retained.</p>}
          {tx?.incomplete && node.status !== 'not_run' && tx.status !== 'Not observed' && <p className="wf-drawer-status">Incomplete evidence: some timing for this transaction was not retained.</p>}
          {totals.size > 0 && (
            <>
              <h5>Phase totals across requests</h5>
              <div className="wf-breakdown">
                {WATERFALL_PHASES.filter(p => (totals.get(p.key) ?? 0) > 0).map(p => {
                  const ms = totals.get(p.key) ?? 0
                  const max = Math.max(...totals.values())
                  return (
                    <div key={p.key} className="wf-breakdown-row">
                      <span><i className={`wf-swatch phase-${p.key}`} aria-hidden="true" /><span>{p.label}</span></span>
                      <div className="wf-breakdown-track" aria-hidden="true"><span className={`phase-${p.key}`} style={{ left: 0, width: `${(ms / max) * 100}%` }} /></div>
                      <span>{formatWaterfallMs(ms)}</span>
                    </div>
                  )
                })}
              </div>
            </>
          )}
          {children.length > 0 && (
            <>
              <h5>Slowest children</h5>
              <dl className="wf-kv">{children.map(c => <Fragment key={c.id}><dt>{c.label}</dt><dd>{formatWaterfallMs(c.durationMs ?? 0)}</dd></Fragment>)}</dl>
            </>
          )}
        </div>
      </div>
    )
  }

  const ev = row.event
  const actionLabel = (hop ? row.actionName : node.label) || 'action'
  const started = ev.started_at ? Date.parse(ev.started_at) : Number.NaN
  const completed = ev.completed_at ? Date.parse(ev.completed_at) : Number.NaN
  const elapsedMs = Number.isFinite(started) && Number.isFinite(completed) && completed >= started ? completed - started : null
  const curlGaps = view && ev.snapshot ? curlCommandGaps(ev.snapshot) : []
  const curlGapsId = `${baseId}-curl-gaps`
  const curlGapsNote = curlGaps.length > 0 ? `cURL note: ${curlGaps.join(', ')}` : ''
  return (
    <div className="wf-drawer-inner">
      <header className="wf-drawer-header">
        <div>
          <strong>{node.label}</strong>
          <small>
            {row.location} · {row.success ? 'Passed' : 'Failed'}
            {row.statusCode != null ? ` · HTTP ${row.statusCode}` : ''} · {row.protocolLabel}
            {row.connectionReused ? ' · Reused connection' : ''}
          </small>
          {row.errorMessage && <small className="wf-tooltip-error">{row.errorMessage}</small>}
        </div>
        <code>{formatWaterfallMs(hop ? hop.durationMs : row.durationMs)}</code>
      </header>
      <div className="wf-drawer-actions">
        {onEdit && <button type="button" className="btn btn-sm btn-secondary" disabled={!canEdit} aria-label={`Edit ${actionLabel}`} onClick={onEdit}>Edit</button>}
        {onRevealInScript && <button type="button" className="btn btn-sm btn-secondary" disabled={!canRevealInScript} onClick={onRevealInScript}>Reveal in script</button>}
        <button
          type="button"
          className="btn btn-sm btn-secondary"
          disabled={!view}
          aria-describedby={curlGaps.length > 0 ? curlGapsId : undefined}
          title={curlGapsNote || undefined}
          onClick={() => void copyCurl()}
        >
          Copy as cURL
        </button>
        {view && <button type="button" className="btn btn-sm btn-secondary" onClick={() => void openHar()}>Download HAR</button>}
        {onInspect && <button type="button" className="btn btn-sm btn-ghost" aria-label={`Inspect ${actionLabel}`} onClick={onInspect}>Inspect</button>}
        {/* Always mounted so screen readers announce each change. */}
        <span className="wf-copy-status" role="status">{copyStatus}</span>
      </div>
      {curlGaps.length > 0 && (
        <p id={curlGapsId} className="wf-drawer-status">
          {curlGapsNote}.
        </p>
      )}
      <TabList className="wf-drawer-tabs" aria-label="Request details">
        {TABS.map(([key, label]) => (
          <button
            key={key}
            id={`${baseId}-tab-${key}`}
            type="button"
            role="tab"
            aria-selected={tab === key}
            aria-controls={`${baseId}-panel`}
            tabIndex={tab === key ? 0 : -1}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </TabList>
      <div className="wf-drawer-panel" role="tabpanel" id={`${baseId}-panel`} aria-labelledby={`${baseId}-tab-${tab}`}>
        {tab === 'timing' && (
          <>
            <dl className="wf-kv">
              {ev.started_at && <><dt>Start time</dt><dd><time dateTime={ev.started_at}>{formatDateTime(ev.started_at, { milliseconds: true })}</time></dd></>}
              {ev.completed_at && <><dt>End time</dt><dd><time dateTime={ev.completed_at}>{formatDateTime(ev.completed_at, { milliseconds: true })}</time></dd></>}
              {elapsedMs != null && <><dt>Elapsed</dt><dd>{formatWaterfallMs(elapsedMs)} (wall clock, includes pauses)</dd></>}
              <dt>Action time</dt><dd>{formatWaterfallMs(ev.duration_ms)} (recorded measurement)</dd>
            </dl>
            <p className="wf-drawer-status">
              Queued at +{row.startOffsetMs.toFixed(1)} ms · Started at +{row.startedOffsetMs.toFixed(1)} ms
              {source ? ` · ${source.label}: ${source.description}` : ''}
            </p>
            {hop ? (
              <div className="wf-breakdown">
                {hop.segments.map((s, i) => (
                  <div key={`${s.key}-${i}`} className="wf-breakdown-row">
                    <span><i className={`wf-swatch phase-${s.key}`} aria-hidden="true" /><span>{s.label}</span></span>
                    <div className="wf-breakdown-track" aria-hidden="true"><span className={`phase-${s.key}`} style={{ left: `${s.offsetPct}%`, width: `${s.widthPct}%` }} /></div>
                    <span>{s.durationMs.toFixed(1)} ms</span>
                  </div>
                ))}
              </div>
            ) : (
              <Breakdown row={row} />
            )}
            {row.timingSource === 'duration' && (
              <p className="wf-drawer-status">
                Phases are missing because this execution retained only its total time
                {node.redirect?.count ? `; it followed ${node.redirect.count} redirects, so one trace cannot be split into phases` : ''}. Enable detailed HAR timings in the debug options to capture DNS, connect, TLS, wait and download.
              </p>
            )}
            {row.hops.length > 1 && !hop && <p className="wf-drawer-status">{row.hops.length} network hops · expand the row to see each one.</p>}
          </>
        )}
        {tab === 'request' && (
          <>
            {ev.snapshot?.request_wire_headers_unavailable && <p role="status">Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.</p>}
            {ev.snapshot?.request_headers_omitted && <p role="status">Some request headers were omitted or shortened by retention limits.</p>}
            <dl className="wf-kv">
              <dt>Method</dt><dd>{ev.snapshot?.request_method || ev.method || ev.action_type}</dd>
              <dt>URL</dt><dd>{ev.snapshot?.request_url || ev.target}</dd>
              <dt>Started</dt><dd>{ev.started_at ? formatDateTime(ev.started_at, { milliseconds: true }) : '—'}</dd>
            </dl>
            <h5>Body</h5>
            <SnapshotBodyPreview body={ev.snapshot ? ev.snapshot.request_body : ev.body} emptyMessage="No request body retained" />
          </>
        )}
        {tab === 'response' && (
          <>
            <dl className="wf-kv"><dt>Status</dt><dd>{ev.snapshot?.response_status ?? ev.status_code ?? 'Not captured'}</dd></dl>
            <HTTPResponseContentEvidence evidence={ev.protocol_evidence} bodyState={ev.snapshot?.response_body_state || ev.response_body_state} />
            <SnapshotBodyPreview body={ev.snapshot?.response_body} headers={ev.snapshot?.response_headers} emptyMessage={`Response body: ${ev.snapshot?.response_body_state || ev.response_body_state || 'not retained'}`} />
          </>
        )}
        {tab === 'headers' && (
          <>
            <HeaderTable caption="Request headers" headers={ev.snapshot ? ev.snapshot.request_headers : ev.headers} />
            <HeaderTable caption="Response headers" headers={ev.snapshot?.response_headers} />
          </>
        )}
        {tab === 'correlation' && (
          <>
            <h5>Values used</h5>
            {used.length ? (
              <ul>{used.map(u => <li key={u.name}><code>{u.name}</code> · extracted by {u.from}{u.unresolved ? ' · referenced but not substituted: check the reference syntax ({{var.name}})' : ''}</li>)}</ul>
            ) : <p className="wf-drawer-status">No value extracted earlier in this run appears in this request.</p>}
            <h5>Values extracted</h5>
            {Object.keys(appliedReplayExtractions(ev)).length || ev.unapplied_extractions || ev.streamed_children?.length ? <ReplayExtractions event={ev} /> : <p className="wf-drawer-status">This execution extracted no values.</p>}
          </>
        )}
        {tab === 'transport' && (
          <>
            <dl className="wf-kv">
              <dt>Protocol</dt><dd>{row.protocolLabel}</dd>
              <dt>Connection</dt><dd>{row.connectionReused ? 'Reused' : 'New or not observed'}</dd>
              {node.redirect?.count ? <><dt>Redirects</dt><dd>{node.redirect.count} · {formatWaterfallMs(node.redirect.ms)}</dd></> : null}
            </dl>
            <HTTPVersionReplayEvidence evidence={ev.protocol_evidence} />
            <HTTPNetworkTraceEvidence evidence={ev.protocol_evidence} networkEntries={ev.network_entries || ev.snapshot?.network_entries} />
            {!ev.protocol_evidence && !(ev.network_entries || ev.snapshot?.network_entries)?.length && <p className="wf-drawer-status">No transport evidence was retained for this execution.</p>}
          </>
        )}
      </div>
    </div>
  )
}
