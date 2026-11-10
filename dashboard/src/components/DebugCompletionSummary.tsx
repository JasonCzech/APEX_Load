import { useId, useMemo, useRef, useEffect, useState, type ReactNode } from 'react'
import { debugSummaryModel, type DebugCompletionSnapshot, type DebugSummaryIssue } from './debugCompletionModel'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'
import './DebugCompletionSummary.css'
import { debugTransactionRows } from './debugTransactionModel'
import { DebugWaterfallPanel } from './DebugWaterfallPanel'
import type { BrowserReplayEvent } from '../types/replay'
import TabList from './ux/TabList'
import DebugLoadComparisonPanel, { type DebugLoadComparisonSource } from './DebugLoadComparisonPanel'

export type DebugSummaryTab = 'transactions' | 'issues' | 'recording-diff' | 'load-comparison'
/** Asks the Summary to show one of its sub-tabs; a new id asks again. */
export interface DebugSummaryTabRequest { tab: DebugSummaryTab; id: number }

type IssueSortField = 'severity' | 'action' | 'phase' | 'status' | 'message'
type IssueSortDirection = 'asc' | 'desc'
type IssueSeverityFilter = 'all' | 'error' | 'warning'
type IssuePhaseFilter = 'all' | 'init' | 'action' | 'teardown' | 'session'

function compareIssues(a: DebugSummaryIssue, b: DebugSummaryIssue, field: IssueSortField, direction: IssueSortDirection): number {
  let cmp = 0
  switch (field) {
    case 'severity': {
      const rankA = a.severity === 'error' ? 0 : 1
      const rankB = b.severity === 'error' ? 0 : 1
      cmp = rankA - rankB
      break
    }
    case 'action':
      cmp = (a.actionName || '').localeCompare(b.actionName || '', undefined, { numeric: true, sensitivity: 'base' })
      break
    case 'phase':
      cmp = (a.phase || '').localeCompare(b.phase || '', undefined, { numeric: true, sensitivity: 'base' })
      break
    case 'status':
      cmp = (a.statusCode ?? -1) - (b.statusCode ?? -1)
      break
    case 'message':
      cmp = a.message.localeCompare(b.message, undefined, { numeric: true, sensitivity: 'base' })
      break
  }
  return direction === 'asc' ? cmp : -cmp
}

function DebugSummaryIssuesTable({ issues, onSelectEvent }: { issues: DebugSummaryIssue[]; onSelectEvent: (index: number) => void }) {
  const filterId = useId()
  const severityId = useId()
  const phaseId = useId()
  const [search, setSearch] = useState('')
  const [severityFilter, setSeverityFilter] = useState<IssueSeverityFilter>('all')
  const [phaseFilter, setPhaseFilter] = useState<IssuePhaseFilter>('all')
  const [sortField, setSortField] = useState<IssueSortField | null>(null)
  const [sortDirection, setSortDirection] = useState<IssueSortDirection>('asc')

  const errorCount = useMemo(() => issues.filter(issue => issue.severity === 'error').length, [issues])
  const warningCount = useMemo(() => issues.filter(issue => issue.severity === 'warning').length, [issues])

  const filteredIssues = useMemo(() => {
    const query = search.trim().toLowerCase()
    const matching = issues.filter(issue => {
      if (severityFilter !== 'all' && issue.severity !== severityFilter) return false
      if (phaseFilter !== 'all') {
        const issuePhase = issue.phase || 'session'
        if (issuePhase !== phaseFilter) return false
      }
      if (!query) return true
      const severityLabel = issue.severity === 'error' ? 'error' : 'warning'
      const statusLabel = issue.statusCode ? `http ${issue.statusCode}` : ''
      const haystack = `${severityLabel} ${issue.actionName || 'session'} ${issue.phase || ''} ${statusLabel} ${issue.message}`.toLowerCase()
      return haystack.includes(query)
    })
    if (!sortField) return matching
    return [...matching].sort((a, b) => compareIssues(a, b, sortField, sortDirection))
  }, [issues, phaseFilter, search, severityFilter, sortDirection, sortField])

  const toggleSort = (field: IssueSortField) => {
    if (sortField === field) {
      setSortDirection(prev => (prev === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortField(field)
      setSortDirection('asc')
    }
  }

  const ariaSortFor = (field: IssueSortField): 'ascending' | 'descending' | 'none' => {
    if (sortField !== field) return 'none'
    return sortDirection === 'asc' ? 'ascending' : 'descending'
  }

  const sortIndicator = (field: IssueSortField) => {
    if (sortField !== field) return '↕'
    return sortDirection === 'asc' ? '↑' : '↓'
  }

  return (
    <div className="debug-summary-issues-section">
      <div className="debug-summary-issues-toolbar">
        <div className="debug-summary-issues-field">
          <label htmlFor={filterId}>Filter issues</label>
          <input
            id={filterId}
            type="search"
            autoComplete="off"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Filter by message, action, phase, or status"
          />
        </div>
        <div className="debug-summary-issues-field">
          <label htmlFor={severityId}>Severity</label>
          <select id={severityId} value={severityFilter} onChange={e => setSeverityFilter(e.target.value as IssueSeverityFilter)}>
            <option value="all">All ({issues.length})</option>
            <option value="error">Error ({errorCount})</option>
            <option value="warning">Warning ({warningCount})</option>
          </select>
        </div>
        <div className="debug-summary-issues-field">
          <label htmlFor={phaseId}>Phase</label>
          <select id={phaseId} value={phaseFilter} onChange={e => setPhaseFilter(e.target.value as IssuePhaseFilter)}>
            <option value="all">All phases</option>
            <option value="init">Init</option>
            <option value="action">Action</option>
            <option value="teardown">Teardown</option>
            <option value="session">Session</option>
          </select>
        </div>
      </div>
      <div className="debug-transaction-table-wrap debug-summary-issues-table-wrap" tabIndex={0} role="region" aria-label="Errors and warnings table">
        <table className="debug-transaction-table debug-summary-issues-table" data-table-pagination="off">
          <caption className="sr-only">Errors and warnings from the completed debug run</caption>
          <thead>
            <tr>
              {([['severity', 'Severity'], ['action', 'Action'], ['phase', 'Phase'], ['status', 'Status'], ['message', 'Message']] as const).map(([field, label]) => (
                <th key={field} scope="col" aria-sort={ariaSortFor(field)}>
                  <button type="button" className="debug-table-sort-btn" onClick={() => toggleSort(field)}>
                    <span>{label}</span>
                    <span className="debug-table-sort-icon" aria-hidden="true">{sortIndicator(field)}</span>
                  </button>
                </th>
              ))}
              <th scope="col">Evidence</th>
            </tr>
          </thead>
          <tbody>
            {filteredIssues.length === 0 ? (
              <tr>
                <td colSpan={6} className="debug-summary-issues-empty">No errors or warnings match the current filters.</td>
              </tr>
            ) : (
              filteredIssues.map(issue => (
                <tr key={issue.key} className="debug-summary-issue-row">
                  <td>
                    <span className={`debug-transaction-status ${issue.severity === 'error' ? 'failed' : 'warning'}`}>
                      {issue.severity === 'error' ? 'Error' : 'Warning'}
                    </span>
                  </td>
                  <th scope="row">{issue.actionName || 'Session'}</th>
                  <td>{issue.phase || '—'}</td>
                  <td>{issue.statusCode ? `HTTP ${issue.statusCode}` : '—'}</td>
                  <td className="debug-summary-issue-message">{issue.message}</td>
                  <td>
                    {issue.eventIndex != null ? (
                      <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectEvent(issue.eventIndex!)}>
                        Inspect {issue.actionName || 'action'}
                      </button>
                    ) : '—'}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/** The Recording diff without a run summary: shown while a run is live or when its summary is unavailable. */
export function DebugSummaryRecordingDiff({ children, consumeFocusRequest }: {
  children: ReactNode
  /**
   * Answers "Compare with recording" where the diff is not a sub-tab: returns
   * true once for a pending request, and the section's heading then takes focus.
   */
  consumeFocusRequest?: () => boolean
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    if (consumeFocusRequest?.()) heading.current?.focus()
  })
  return <section aria-label="Recording diff" className="debug-summary-recording-diff">
    <h3 ref={heading} tabIndex={consumeFocusRequest ? -1 : undefined}>Recording diff</h3>
    {children}
  </section>
}

export default function DebugCompletionSummary({ snapshot, stale, unavailable, onSelectEvent, onDownloadHAR, onExportSession, onRevealInScript, canRevealInScript, recordingDiff, loadComparison, tabRequest, consumeRecordingDiffFocus }: {
  snapshot: DebugCompletionSnapshot; stale: boolean; unavailable: boolean
  onSelectEvent: (index: number) => void
  /** Jump the script editor to the action that produced an execution. */
  onRevealInScript?: (event: BrowserReplayEvent) => void
  canRevealInScript?: (event: BrowserReplayEvent) => boolean
  onDownloadHAR: () => Promise<void>
  onExportSession: () => Promise<void> | void
  /** The run compared with a recording (non-browser scripts), shown as the Recording diff sub-tab. */
  recordingDiff?: ReactNode
  /** Loads the script's load runs for the Debug vs load sub-tab; absent for an unsaved script. */
  loadComparison?: DebugLoadComparisonSource
  /** Opens a sub-tab from elsewhere (Request/Response's "Compare with recording"). */
  tabRequest?: DebugSummaryTabRequest | null
  /** Focuses the plain Recording diff (no sub-tabs while evidence is unavailable) after such a request. */
  consumeRecordingDiffFocus?: () => boolean
}) {
  const model = useMemo(() => debugSummaryModel(snapshot), [snapshot])
  const heading = useRef<HTMLHeadingElement>(null)
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState('')
  const [activeTabByRun, setActiveTabByRun] = useState<{ key: string; tab: DebugSummaryTab } | null>(null)
  // A request seen at mount is already answered: only a newer one switches tabs.
  const [mountTabRequestId] = useState(() => tabRequest?.id ?? null)
  const [handledTabRequest, setHandledTabRequest] = useState<DebugSummaryTabRequest | null>(() => tabRequest ?? null)
  const recordingDiffTab = useRef<HTMLButtonElement>(null)
  if (tabRequest && tabRequest.id !== handledTabRequest?.id) {
    setHandledTabRequest(tabRequest)
    setActiveTabByRun({ key: snapshot.key, tab: tabRequest.tab })
  }
  const timelineEvents = useMemo(() => model.events.map((entry) => entry.event), [model.events])
  const timelineIndices = useMemo(() => model.events.map((entry) => entry.index), [model.events])
  const transactionRows = useMemo(() => debugTransactionRows(snapshot), [snapshot])
  // The summary's events skip containers; iteration detection needs them all.
  const allEvents = useMemo(() => snapshot.report?.events || snapshot.session.events || [], [snapshot])
  // Open on the first failure so the summary answers "what broke" first.
  const defaultSelectedIndex = useMemo(() => {
    const firstFailed = model.events.find((entry) => entry.event.success === false)
    return (firstFailed ?? model.events[0])?.index ?? null
  }, [model.events])

  const requestedTab = activeTabByRun?.key === snapshot.key ? activeTabByRun.tab : 'transactions'
  const activeTab = requestedTab === 'recording-diff' && !recordingDiff || requestedTab === 'load-comparison' && !loadComparison ? 'transactions' : requestedTab
  const setActiveTab = (tab: DebugSummaryTab) => setActiveTabByRun({ key: snapshot.key, tab })

  const summaryRef = useRef<HTMLElement>(null)
  useEffect(() => {
    // The plain Recording diff may have just taken focus on request: keep it there.
    if (!summaryRef.current?.contains(document.activeElement)) heading.current?.focus()
    setDownloadError('')
  }, [snapshot.key])
  useEffect(() => {
    if (!handledTabRequest || handledTabRequest.id === mountTabRequestId || handledTabRequest.tab !== 'recording-diff') return
    recordingDiffTab.current?.focus()
  }, [handledTabRequest, mountTabRequestId])

  const hidden = unavailable || model.unavailable
  const har = snapshot.session.har
  return <section className="debug-completion-summary" aria-label="Debug run summary" ref={summaryRef}>
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
    {hidden && recordingDiff && <DebugSummaryRecordingDiff consumeFocusRequest={consumeRecordingDiffFocus}>{recordingDiff}</DebugSummaryRecordingDiff>}
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
        {recordingDiff && (
          <button
            ref={recordingDiffTab}
            type="button"
            role="tab"
            id="debug-summary-tab-recording-diff"
            aria-selected={activeTab === 'recording-diff'}
            aria-controls="debug-summary-panel-recording-diff"
            tabIndex={activeTab === 'recording-diff' ? 0 : -1}
            className={`debug-summary-tab ux-underline-tab ${activeTab === 'recording-diff' ? 'active' : ''}`}
            onClick={() => setActiveTab('recording-diff')}
          >
            <span>Recording diff</span>
          </button>
        )}
        {loadComparison && (
          <button
            type="button"
            role="tab"
            id="debug-summary-tab-load-comparison"
            aria-selected={activeTab === 'load-comparison'}
            aria-controls="debug-summary-panel-load-comparison"
            tabIndex={activeTab === 'load-comparison' ? 0 : -1}
            className={`debug-summary-tab ux-underline-tab ${activeTab === 'load-comparison' ? 'active' : ''}`}
            onClick={() => setActiveTab('load-comparison')}
          >
            <span>Debug vs load</span>
          </button>
        )}
      </TabList>
      <div
        id="debug-summary-panel-transactions"
        role="tabpanel"
        aria-labelledby="debug-summary-tab-transactions"
        className="debug-summary-tabpanel"
        hidden={activeTab !== 'transactions'}
      >
        {timelineEvents.length > 0 || transactionRows.length > 0 ? (
          <section aria-label="Execution waterfall" className="debug-summary-waterfall-section">
            <h3>Execution waterfall</h3>
            <p className="debug-summary-waterfall-note">
              Iterations, transactions, pages and requests in one tree. Parent bars show their children’s real spans; hatched gaps are think time or other time outside child requests.
              {!snapshot.transactions && ' Transaction definitions were not retained for this run. Start a new run from the Debug menu to capture the transaction breakdown.'}
              {snapshot.transactions && transactionRows.length === 0 && ' No transactions were defined in this run’s script.'}
            </p>
            <DebugWaterfallPanel
              key={snapshot.key}
              variant="summary"
              ariaLabel="Transactions and executions"
              events={timelineEvents}
              eventIndices={timelineIndices}
              transactions={transactionRows}
              allEvents={allEvents}
              evidenceDropped={Boolean(snapshot.session.dropped_event_count || snapshot.report?.dropped_event_count)}
              selectedEventIndex={defaultSelectedIndex}
              onInspectEvent={onSelectEvent}
              onRevealInScript={onRevealInScript}
              canRevealInScript={canRevealInScript}
            />
          </section>
        ) : (
          <p>{snapshot.transactions ? 'No executions were retained for this run.' : 'Transaction definitions were not retained for this run. Start a new run from the Debug menu to capture the transaction breakdown.'}</p>
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
          {model.issues.length === 0 ? <p>No errors or warnings were retained.</p> : <DebugSummaryIssuesTable key={`issues-${snapshot.key}`} issues={model.issues} onSelectEvent={onSelectEvent} />}
        </section>
      </div>
      {recordingDiff && (
        <div
          id="debug-summary-panel-recording-diff"
          role="tabpanel"
          aria-labelledby="debug-summary-tab-recording-diff"
          className="debug-summary-tabpanel"
          hidden={activeTab !== 'recording-diff'}
        >
          <DebugSummaryRecordingDiff>{recordingDiff}</DebugSummaryRecordingDiff>
        </div>
      )}
      {loadComparison && (
        <div
          id="debug-summary-panel-load-comparison"
          role="tabpanel"
          aria-labelledby="debug-summary-tab-load-comparison"
          className="debug-summary-tabpanel"
          hidden={activeTab !== 'load-comparison'}
        >
          <DebugLoadComparisonPanel snapshot={snapshot} source={loadComparison} active={activeTab === 'load-comparison'} stale={stale} />
        </div>
      )}
    </>}
  </section>
}

