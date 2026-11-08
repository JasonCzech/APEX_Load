import { useRef, useEffect, useState, useMemo } from 'react'
import { debugSummaryModel, type DebugCompletionSnapshot } from './debugCompletionModel'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'
import './DebugCompletionSummary.css'
import DebugTransactionTable from './DebugTransactionTable'
import { DebugTimeline } from './DebuggerSessionTools'
import TabList from './ux/TabList'

export default function DebugCompletionSummary({ snapshot, stale, unavailable, onSelectEvent, onDownloadHAR, onExportSession }: {
  snapshot: DebugCompletionSnapshot; stale: boolean; unavailable: boolean
  onSelectEvent: (index: number) => void
  onDownloadHAR: () => Promise<void>
  onExportSession: () => Promise<void> | void
}) {
  const model = useMemo(() => debugSummaryModel(snapshot), [snapshot])
  const heading = useRef<HTMLHeadingElement>(null)
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState('')
  const [activeTabByRun, setActiveTabByRun] = useState<{ key: string; tab: 'transactions' | 'issues' } | null>(null)
  const [selectedTimelineByRun, setSelectedTimelineByRun] = useState<{ key: string; index: number } | null>(null)

  const timelineEvents = useMemo(() => model.events.map((entry) => entry.event), [model.events])
  const defaultTimelineIndex = useMemo(() => {
    const firstFailed = model.events.findIndex((entry) => entry.event.success === false)
    return firstFailed >= 0 ? firstFailed : 0
  }, [model.events])

  const activeTab = activeTabByRun?.key === snapshot.key ? activeTabByRun.tab : 'transactions'
  const setActiveTab = (tab: 'transactions' | 'issues') => setActiveTabByRun({ key: snapshot.key, tab })
  const selectedTimelineIndex =
    selectedTimelineByRun?.key === snapshot.key ? selectedTimelineByRun.index : defaultTimelineIndex
  const setSelectedTimelineIndex = (index: number) => setSelectedTimelineByRun({ key: snapshot.key, index })

  useEffect(() => {
    heading.current?.focus()
    setDownloadError('')
  }, [snapshot.key])

  const hidden = unavailable || model.unavailable
  const har = snapshot.session.har
  return <section className="debug-completion-summary" aria-label="Debug run summary">
    <header className="debug-summary-heading">
      <div><h2 ref={heading} tabIndex={-1}>Debug summary</h2><p role="status">{model.outcome}{model.incomplete || hidden ? ' · Incomplete evidence' : ''}</p></div>
      <div className="debug-summary-actions">
        <button type="button" className="btn btn-secondary" onClick={() => { void onExportSession() }}>Export session</button>
        {!hidden && har?.eligible && har.available && (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={downloading}
            onClick={() => {
              setDownloading(true)
              setDownloadError('')
              void onDownloadHAR()
                .catch((error) => setDownloadError(error instanceof Error ? error.message : 'HAR download failed.'))
                .finally(() => setDownloading(false))
            }}
          >
            {downloading ? 'Downloading…' : 'Download HAR'}
          </button>
        )}
      </div>
    </header>
    {!hidden && downloadError && <p role="alert" className="error-message">{downloadError}</p>}
    {stale && <p className="warning-message">These results describe an earlier draft. Start a new run from the Debug menu to check your current changes.</p>}
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
      <TabList className="debug-summary-tabs ux-underline-tabs" aria-label="Debug summary sections">
        <button
          type="button"
          role="tab"
          id="debug-summary-tab-transactions"
          aria-selected={activeTab === 'transactions'}
          aria-controls="debug-summary-panel-transactions"
          tabIndex={activeTab === 'transactions' ? 0 : -1}
          className={`debug-summary-tab ux-underline-tab ${activeTab === 'transactions' ? 'active' : ''}`}
          onClick={() => setActiveTab('transactions')}
        >
          <span>Transactions</span>
        </button>
        <button
          type="button"
          role="tab"
          id="debug-summary-tab-issues"
          aria-selected={activeTab === 'issues'}
          aria-controls="debug-summary-panel-issues"
          tabIndex={activeTab === 'issues' ? 0 : -1}
          className={`debug-summary-tab ux-underline-tab ${activeTab === 'issues' ? 'active' : ''}`}
          onClick={() => setActiveTab('issues')}
        >
          <span>Errors and warnings</span>
          {model.issues.length > 0 && <span className="debug-summary-tab-badge">{model.issues.length}</span>}
        </button>
      </TabList>
      <div
        id="debug-summary-panel-transactions"
        role="tabpanel"
        aria-labelledby="debug-summary-tab-transactions"
        className="debug-summary-tabpanel"
        hidden={activeTab !== 'transactions'}
      >
        <DebugTransactionTable key={snapshot.key} snapshot={snapshot} onSelectEvent={onSelectEvent} />
        {timelineEvents.length > 0 && (
          <section aria-label="Execution waterfall" className="debug-summary-waterfall-section">
            <h3>Execution waterfall</h3>
            <DebugTimeline
              key={snapshot.key}
              variant="summary"
              events={timelineEvents}
              selectedIndex={selectedTimelineIndex}
              onSelect={setSelectedTimelineIndex}
              onInspect={(idx) => {
                const target = model.events[idx]
                onSelectEvent(target ? target.index : idx)
              }}
            />
          </section>
        )}
        {har?.eligible && (
          <section aria-label="HAR download" className="debug-summary-har-meta">
            <h3>Network evidence</h3>
            <p>{har.limitations}</p>
            {har.options && <p>Bodies: {({ none: 'none', request: 'request only', response: 'response only', both: 'request and response' })[har.options.bodies]} · Intermediate redirects: {har.options.redirects ? 'on' : 'off'} · Detailed timings: {har.options.timings ? 'on' : 'off'}{har.options.bodies !== 'none' ? ` · ${har.options.max_body_bytes / 1024} KiB per body · ${har.options.max_total_body_bytes / 1048576} MiB per run` : ''}</p>}
            {har.available ? <p>{har.retained_request_count.toLocaleString()} retained requests</p> : <p>{har.reason || 'No network traffic was retained for this run.'}</p>}
          </section>
        )}
      </div>
      <div
        id="debug-summary-panel-issues"
        role="tabpanel"
        aria-labelledby="debug-summary-tab-issues"
        className="debug-summary-tabpanel"
        hidden={activeTab !== 'issues'}
      >
        <section aria-label="Errors and warnings" className="debug-summary-issues-section">
          <h3>Errors and warnings</h3>
          {model.issues.length === 0 ? <p>No errors or warnings were retained.</p> : <ul className="debug-summary-issues">{model.issues.map(issue => <li key={issue.key}>
            <strong>{issue.severity === 'error' ? 'Error' : 'Warning'}{issue.actionName ? ` · ${issue.actionName}` : ''}</strong>
            <p>{issue.message}</p><small>{[issue.phase, issue.statusCode ? `HTTP ${issue.statusCode}` : ''].filter(Boolean).join(' · ')}</small>
            {issue.eventIndex != null && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectEvent(issue.eventIndex!)}>Inspect {issue.actionName || 'action'}</button>}
          </li>)}</ul>}
        </section>
      </div>
    </>}
  </section>
}

