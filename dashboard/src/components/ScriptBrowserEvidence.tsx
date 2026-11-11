import { useId, useRef, useState } from 'react'
import type { BrowserReplayEvent, BrowserReplayReport, ReplayRepairSuggestion } from '../types/replay'
import BrowserReplayHARDownloads from './BrowserReplayHARDownloads'
import BrowserReplayProgress from './BrowserReplayProgress'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import RepairSuggestionList from './RepairSuggestionList'
import { BrowserReplayScreenshotDialog } from './BrowserReplayDebugger'
import { formatConsoleEntries, formatNetworkEntries } from './scriptEditorUtils'
import { omitUnavailableReplayReport } from './runtimeVariableEvidence'
import { ReplayExtractions } from './ReplayExtractions'
import TabList from './ux/TabList'

const tabs = ['Screenshot', 'Assertions', 'DOM', 'Network', 'Browser console', 'Variables'] as const

function tabSlug(name: typeof tabs[number]): string {
  return name.toLowerCase().replace(/\s+/g, '-')
}

function CompletedNativeReplayCard({
  sessionId,
  report,
  harAvailable,
  unavailable,
  onDismiss,
}: {
  sessionId: string
  report?: BrowserReplayReport | null
  harAvailable?: boolean
  unavailable?: boolean
  onDismiss?: () => void
}) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const [rejected, setRejected] = useState<Record<string, boolean>>({})
  const evidenceUnavailable = Boolean(report?.runtime_evidence_unavailable)
  const safeReport = report && evidenceUnavailable ? omitUnavailableReplayReport(report) : report
  const showVerifiedArtifacts = !unavailable && !evidenceUnavailable && (harAvailable ?? Boolean(safeReport))
  const phase: 'completed' | 'failed' | 'stopped' = unavailable
    ? 'failed'
    : safeReport?.stopped
      ? 'stopped'
      : safeReport?.success
        ? 'completed'
        : 'failed'
  const real = (safeReport?.events ?? []).filter(Boolean)
  const passed = real.filter(ev => ev.success).length
  const failed = real.filter(ev => !ev.success).length
  const reportSuggestions: ReplayRepairSuggestion[] = unavailable || evidenceUnavailable ? [] : (safeReport?.repair_suggestions ?? [])
  return (
    <div className="brp-replay-progress" data-testid="replay-progress">
      <div className="brp-replay-header">
        <span className={`brp-status brp-replay-${phase}`}>
          {phase === 'completed' ? 'Verified ✓' : phase === 'failed' ? 'Issues found' : 'Stopped'}
        </span>
        <span className="brp-replay-counts">{passed} passed · {failed} failed</span>
        {onDismiss && <button type="button" className="rec-inline-action" onClick={onDismiss}>Close</button>}
      </div>
      {showVerifiedArtifacts && <BrowserReplayHARDownloads sessionId={sessionId} />}
      {showVerifiedArtifacts && safeReport && (
        <details>
          <summary>Verified output and scope</summary>
          <p>{safeReport.group} · {safeReport.protocol}</p>
          <p>Script fingerprint: {safeReport.script_hash || 'Unavailable'}</p>
          <p>{safeReport.completed_at}</p>
        </details>
      )}
      {unavailable && (
        <div className="brp-replay-empty" role="alert">Replay session could not be confirmed. Start verification again.</div>
      )}
      {!unavailable && evidenceUnavailable && (
        <div className="brp-replay-empty" role="status">Runtime evidence unavailable. Run details and downloads cannot be shown.</div>
      )}
      <div className="brp-replay-events">
        {real.map((ev, idx) => {
          const key = ev.event_id || `evt-${idx}`
          const suggestions = unavailable || evidenceUnavailable ? [] : (ev.repair_suggestions ?? [])
          return (
            <div key={key} className={`brp-replay-event ${ev.success ? 'ok' : 'fail'}`}>
              <span className="brp-replay-icon">{ev.success ? '✓' : '✕'}</span>
              <span className="brp-replay-action" title={ev.target || ev.selector}>{ev.action_name || ev.action_type}</span>
              <span className="brp-replay-dur">{Math.round(ev.duration_ms || 0)}ms</span>
              {suggestions.length > 0 && (
                <button type="button" className="rec-inline-action" onClick={() => setExpanded(expanded === key ? null : key)}>
                  {suggestions.length} fix{suggestions.length === 1 ? '' : 'es'}
                </button>
              )}
              {!ev.success && ev.error_message && expanded !== key && (
                <span className="brp-replay-error" title={ev.error_message}>{ev.error_message}</span>
              )}
              {expanded === key && (
                <div className="brp-replay-suggestions">
                  {ev.error_message && <div className="brp-replay-error-full">{ev.error_message}</div>}
                  <RepairSuggestionList
                    suggestions={suggestions}
                    itemClassName="brp-repair-suggestion"
                    confidenceClassName="brp-repair-confidence"
                    descriptionClassName="brp-repair-desc"
                    actionsClassName="brp-repair-actions"
                    actionButtonClassName="rec-inline-action"
                    rejected={rejected}
                    onRejectSuggestion={suggestion => setRejected(prev => ({ ...prev, [suggestion.id]: true }))}
                  />
                </div>
              )}
            </div>
          )
        })}
      </div>
      {reportSuggestions.length > 0 && (
        <RepairSuggestionList
          suggestions={reportSuggestions}
          className="brp-replay-suggestions"
          itemClassName="brp-repair-suggestion"
          label="Self-heal suggestions"
          labelClassName="brp-warnings-title"
          confidenceClassName="brp-repair-confidence"
          descriptionClassName="brp-repair-desc"
          actionsClassName="brp-repair-actions"
          actionButtonClassName="rec-inline-action"
          rejected={rejected}
          onRejectSuggestion={suggestion => setRejected(prev => ({ ...prev, [suggestion.id]: true }))}
        />
      )}
    </div>
  )
}

/** Evidence for exactly one selected action. Navigation belongs to the action rail. */
export default function ScriptBrowserEvidence({
  event,
  running,
  variables,
  projectId,
  nativeReplaySessionId: controlledSessionId,
  nativeReplayRunning: controlledRunning,
  nativeReplayReport,
  nativeReplayHarAvailable,
  nativeReplayUnavailable,
  nativeReplayDisabled,
  nativeReplayTitle,
  onStartNativeReplay,
  onCompleteNativeReplay,
  onDismissNativeReplay,
  onEdit,
  onApplyLocator,
}: {
  event?: BrowserReplayEvent | null
  running: boolean
  variables?: Record<string, string> | null
  projectId?: string
  nativeReplaySessionId?: string | null
  nativeReplayRunning?: boolean
  nativeReplayReport?: BrowserReplayReport | null
  nativeReplayHarAvailable?: boolean
  nativeReplayUnavailable?: boolean
  nativeReplayDisabled?: boolean
  nativeReplayTitle?: string
  onStartNativeReplay?: () => Promise<string | null>
  onCompleteNativeReplay?: (report: BrowserReplayReport | null, events: BrowserReplayEvent[]) => void
  onDismissNativeReplay?: () => void
  onEdit?: () => void
  onApplyLocator?: (event: BrowserReplayEvent, locator: string) => void
}) {
  const tabIdPrefix = useId()
  const [tab, setTab] = useState<typeof tabs[number]>('Screenshot')
  const [fullSize, setFullSize] = useState(false)
  const [uncontrolledSessionId, setUncontrolledSessionId] = useState<string | null>(null)
  const [uncontrolledRunning, setUncontrolledRunning] = useState(false)
  const [nativeReplayStarting, setNativeReplayStarting] = useState(false)
  const [nativeReplayError, setNativeReplayError] = useState('')
  const nativeReplayInFlightRef = useRef(false)

  const isControlled = controlledSessionId !== undefined
  const activeNativeSessionId = isControlled ? controlledSessionId : uncontrolledSessionId
  const activeNativeRunning = controlledRunning !== undefined ? controlledRunning : uncontrolledRunning

  const nativeReplaySection = onStartNativeReplay ? (
    <section className="script-browser-native-replay" aria-label="Native browser replay">
      <div className="selected-evidence-header">
        <div>
          <h4>Native browser replay</h4>
          <p>Run one browser iteration and retain a native HAR and replay evidence archive.</p>
        </div>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={running || nativeReplayStarting || activeNativeRunning || Boolean(nativeReplayDisabled)}
          title={nativeReplayTitle}
          onClick={() => {
            if (nativeReplayInFlightRef.current || activeNativeRunning) return
            nativeReplayInFlightRef.current = true
            setNativeReplayStarting(true)
            setNativeReplayError('')
            void onStartNativeReplay()
              .then(sessionId => {
                if (sessionId && !isControlled) {
                  setUncontrolledSessionId(sessionId)
                  setUncontrolledRunning(true)
                }
              })
              .catch((reason: unknown) => {
                setNativeReplayError(reason instanceof Error ? reason.message : 'Could not start native browser replay.')
              })
              .finally(() => {
                nativeReplayInFlightRef.current = false
                setNativeReplayStarting(false)
              })
          }}
        >
          {nativeReplayStarting ? 'Starting replay…' : 'Run native browser replay'}
        </button>
      </div>
      {nativeReplayError && <p role="alert">{nativeReplayError}</p>}
      {activeNativeSessionId && (
        isControlled && !activeNativeRunning ? (
          <CompletedNativeReplayCard
            key={activeNativeSessionId}
            sessionId={activeNativeSessionId}
            report={nativeReplayReport}
            harAvailable={nativeReplayHarAvailable}
            unavailable={nativeReplayUnavailable}
            onDismiss={onDismissNativeReplay}
          />
        ) : (
          <BrowserReplayProgress
            key={activeNativeSessionId}
            replaySessionId={activeNativeSessionId}
            projectId={projectId}
            onComplete={(report, events) => {
              if (!isControlled) setUncontrolledRunning(false)
              onCompleteNativeReplay?.(report, events)
            }}
            onDismiss={() => {
              if (!isControlled) {
                setUncontrolledRunning(false)
                setUncontrolledSessionId(null)
              }
              onDismissNativeReplay?.()
            }}
          />
        )
      )}
    </section>
  ) : null
  const snapshot = event?.snapshot
  const screenshot = event?.screenshot_base64 || snapshot?.screenshot_base64
  const screenshotError = typeof event?.protocol_evidence?.screenshot_capture_error === 'string' ? event.protocol_evidence.screenshot_capture_error : undefined
  const htmlError = typeof event?.protocol_evidence?.html_capture_error === 'string' ? event.protocol_evidence.html_capture_error : undefined
  const failure = event?.protocol_evidence?.assertion_failure as { expected?: unknown; actual?: unknown; type?: string } | undefined
  const showValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value) ?? 'Not captured'
  const activeTabId = `${tabIdPrefix}-tab-${tabSlug(tab)}`
  const panelId = `${tabIdPrefix}-panel`
  return <section className="script-browser-evidence" aria-label={event ? 'Selected browser action evidence' : undefined}>
    {!event ? (
      <>
        <h3>Browser evidence</h3>
        <p>{running || activeNativeRunning ? 'Waiting for the first action. Select a completed action to inspect its evidence.' : 'Choose Debug > Run a single iteration to capture screenshots, assertions, DOM, network, and variables. Configure the target and capture options in Runtime Settings.'}</p>
      </>
    ) : (
      <>
        <div className="selected-evidence-header">
          <div><small>{event.phase} {(event.action_index ?? 0) + 1} · {(event.duration_ms || 0).toFixed(1)} ms · {event.success ? 'Passed' : 'Failed'}</small><h3>{event.action_name || event.action_type}</h3><p>{event.page_title || snapshot?.page_title} · {event.page_url || snapshot?.page_url}</p></div>
          {onEdit && <button type="button" className="btn btn-secondary btn-sm" onClick={onEdit}>{failure ? 'Edit this assertion' : 'Edit this action'}</button>}
        </div>
        {!event.success && <div className="selected-evidence-failure" role="status">
          <strong>{event.diagnosis?.category === 'assertion' ? 'Assertion failed' : 'Action failed'}</strong>
          {failure ? <dl className="browser-assertion-result" aria-label="Assertion result"><dt>Expected</dt><dd>{showValue(failure.expected)}</dd><dt>Actual</dt><dd>{showValue(failure.actual)}</dd></dl> : <p>{event.diagnosis?.cause || event.error_message}</p>}
          {event.diagnosis?.suggested_locator && onApplyLocator && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyLocator(event, event.diagnosis!.suggested_locator!)}>Use suggested selector</button>}
        </div>}
        <RepairSuggestionList className="selected-evidence-repairs" descriptionClassName="selected-evidence-repair-description" suggestions={event.repair_suggestions || []} renderExtraAction={suggestion => {
          const locator = suggestion.fields?.selector || suggestion.fields?.locator
          return suggestion.draft_patch_type === 'promote_selector' && locator && onApplyLocator ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => onApplyLocator(event, locator)}>Apply suggested selector</button> : null
        }} />

        <TabList className="selected-evidence-tabs" aria-label="Browser evidence views">
          {tabs.map(name => {
            const isSelected = tab === name
            const id = `${tabIdPrefix}-tab-${tabSlug(name)}`
            return (
              <button
                key={name}
                id={id}
                type="button"
                role="tab"
                aria-selected={isSelected}
                aria-controls={panelId}
                tabIndex={isSelected ? 0 : -1}
                className={isSelected ? 'active' : ''}
                onClick={() => setTab(name)}
              >
                {name}
              </button>
            )
          })}
        </TabList>
        <div id={panelId} role="tabpanel" aria-labelledby={activeTabId} className="selected-evidence-body">
          {tab === 'Screenshot' && <>{screenshotError && <p role="status">Screenshot capture failed: {screenshotError}</p>}{(event.screenshot_omitted || snapshot?.screenshot_omitted) && <p role="status">A screenshot was omitted because it exceeded the retained evidence limit.</p>}{screenshot ? <><button type="button" className="btn btn-sm btn-secondary" onClick={() => setFullSize(true)}>View full size</button><img className="selected-evidence-screenshot" src={`data:image/png;base64,${screenshot}`} alt={`Screenshot after ${event.action_name || event.action_type}`} /></> : !(screenshotError || event.screenshot_omitted || snapshot?.screenshot_omitted) && <p>No screenshot was captured for this action. Enable per-action capture in Runtime Settings and debug again.</p>}</>}
          {tab === 'Assertions' && <>{Boolean(event.protocol_evidence_omitted_count) && <p role="status">{event.protocol_evidence_omitted_count} protocol evidence {event.protocol_evidence_omitted_count === 1 ? 'item was' : 'items were'} omitted or shortened by retention limits.</p>}<SnapshotBodyPreview body={JSON.stringify(event.protocol_evidence || { result: event.success ? 'Action passed; no detailed assertion result captured.' : event.error_message }, null, 2)} headers={{ 'Content-Type': 'application/json' }} /></>}
          {tab === 'DOM' && <>{htmlError && <p role="status">HTML capture failed: {htmlError}</p>}<SnapshotBodyPreview body={JSON.stringify(event.dom_summary || snapshot?.dom_summary || {}, null, 2)} headers={{ 'Content-Type': 'application/json' }} /><SnapshotTextPreview text={event.html || snapshot?.html || (htmlError ? '' : 'No HTML captured for this action.')} /></>}
          {tab === 'Network' && <SnapshotTextPreview text={formatNetworkEntries(event.network_entries || snapshot?.network_entries || []) || 'No network entries captured for this action.'} />}
          {tab === 'Browser console' && <SnapshotTextPreview text={formatConsoleEntries(event.console_entries || snapshot?.console_entries || []) || 'No browser console entries captured for this action.'} />}
          {tab === 'Variables' && <><SnapshotBodyPreview body={JSON.stringify(variables || event.extractions || {}, null, 2)} headers={{ 'Content-Type': 'application/json' }} /><ReplayExtractions event={event} /></>}
        </div>
        {fullSize && screenshot && <BrowserReplayScreenshotDialog src={`data:image/png;base64,${screenshot}`} onClose={() => setFullSize(false)} />}
      </>
    )}
    {nativeReplaySection}
  </section>
}
