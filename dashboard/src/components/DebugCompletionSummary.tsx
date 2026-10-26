import { useRef, useEffect, useState } from 'react'
import { debugSummaryModel, type DebugCompletionSnapshot } from './debugCompletionModel'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'
import './DebugCompletionSummary.css'
import DebugTransactionTable from './DebugTransactionTable'

export default function DebugCompletionSummary({ snapshot, stale, unavailable, onReturn, onDebugAgain, onSelectEvent, onDownloadHAR }: {
  snapshot: DebugCompletionSnapshot; stale: boolean; unavailable: boolean
  onReturn: () => void; onDebugAgain: () => void; onSelectEvent: (index: number) => void
  onDownloadHAR: () => Promise<void>
}) {
  const model = debugSummaryModel(snapshot)
  const heading = useRef<HTMLHeadingElement>(null)
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState('')
  useEffect(() => { heading.current?.focus() }, [snapshot.key])
  const hidden = unavailable || model.unavailable
  const har = snapshot.session.har
  return <section className="debug-completion-summary" aria-label="Debug run summary">
    <header className="debug-summary-heading">
      <div><h2 ref={heading} tabIndex={-1}>Debug summary</h2><p role="status">{model.outcome}{model.incomplete || hidden ? ' · Incomplete evidence' : ''}</p></div>
      <div className="debug-summary-actions"><button type="button" className="btn btn-secondary" onClick={onReturn}>Return to debugger</button><button type="button" className="btn btn-primary" onClick={onDebugAgain}>Debug again</button></div>
    </header>
    {stale && <p className="warning-message">These results describe an earlier draft. Debug again to check your current changes.</p>}
    {hidden && <p className="warning-message">Runtime evidence unavailable. Run details and downloads cannot be shown.</p>}
    {!hidden && <>
      <dl className="debug-summary-facts">
        <div><dt>Script</dt><dd>{snapshot.scriptName}</dd></div>
        <div><dt>Group</dt><dd>{snapshot.report?.group || snapshot.session.group}</dd></div>
        <div><dt>Session ID</dt><dd>{snapshot.session.id}</dd></div>
        <div><dt>Source</dt><dd>{snapshot.draft ? 'Unsaved draft' : 'Saved script'}{snapshot.report?.script_revision ? ` · ${snapshot.report.script_revision}` : ''}</dd></div>
        <div><dt>Started</dt><dd>{model.startedAt ? formatDisplayDateTime(model.startedAt) : 'Unavailable'}</dd></div>
        <div><dt>Elapsed time</dt><dd>{model.elapsedMs == null ? 'Unavailable' : `${(model.elapsedMs / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} s`}</dd></div>
        <div><dt>Passed actions</dt><dd>{model.passed}</dd></div><div><dt>Failed actions</dt><dd>{model.failed}</dd></div>
      </dl>
      {model.incomplete && <p className="warning-message">Some run evidence was dropped or truncated. Counts reflect retained actions.</p>}
      {model.outcome === 'Stopped' && <p>The run was stopped before completion. Results cover actions that finished.</p>}
      <DebugTransactionTable key={snapshot.key} snapshot={snapshot} onSelectEvent={onSelectEvent} />
      <h3>Errors and warnings</h3>
      {model.issues.length === 0 ? <p>No errors or warnings were retained.</p> : <ul className="debug-summary-issues">{model.issues.map(issue => <li key={issue.key}>
        <strong>{issue.severity === 'error' ? 'Error' : 'Warning'}{issue.actionName ? ` · ${issue.actionName}` : ''}</strong>
        <p>{issue.message}</p><small>{[issue.phase, issue.statusCode ? `HTTP ${issue.statusCode}` : ''].filter(Boolean).join(' · ')}</small>
        {issue.eventIndex != null && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectEvent(issue.eventIndex!)}>Inspect {issue.actionName || 'action'}</button>}
      </li>)}</ul>}
      {har?.eligible && <section aria-label="HAR download"><h3>Network evidence</h3><p>{har.limitations}</p>
        {har.options && <p>Bodies: {({ none: 'none', request: 'request only', response: 'response only', both: 'request and response' })[har.options.bodies]} · Intermediate redirects: {har.options.redirects ? 'on' : 'off'} · Detailed timings: {har.options.timings ? 'on' : 'off'}{har.options.bodies !== 'none' ? ` · ${har.options.max_body_bytes / 1024} KiB per body · ${har.options.max_total_body_bytes / 1048576} MiB per run` : ''}</p>}
        {har.available ? <><p>{har.retained_request_count.toLocaleString()} retained requests</p><button type="button" className="btn btn-secondary" disabled={downloading} onClick={() => {
          setDownloading(true); setDownloadError(''); void onDownloadHAR().catch(error => setDownloadError(error instanceof Error ? error.message : 'HAR download failed.')).finally(() => setDownloading(false))
        }}>{downloading ? 'Downloading…' : 'Download HAR'}</button></> : <p>{har.reason || 'No network traffic was retained for this run.'}</p>}
        {downloadError && <p role="alert">{downloadError}</p>}
      </section>}
    </>}
  </section>
}
