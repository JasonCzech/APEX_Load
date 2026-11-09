import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptDefinition, ScriptExtractorDefinition } from '../types/scripts'
import LiveTableStepResult from './LiveTableStepResult'
import { ReplayExtractions } from './ReplayExtractions'
import ResponseSelection from './ResponseSelection'
import { resolveAuthorJsonScopeByKey } from './scriptEditorJsonModel'
import { replayEventStepKey } from './actionChecksModel'
import ResourceSnapshotList from './ResourceSnapshotList'
import { responseBodyMessage } from './debuggerExperience'
import { HTTPVersionReplayEvidence, type HTTP3UpgradeScope } from './HTTPVersionReplayEvidence'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import { HTTPNetworkTraceEvidence } from './HTTPNetworkTraceEvidence'
import { httpReplayVersionEvidence } from './httpActionVersionModel'
import { httpResponseContentEvidence } from './httpResponseContentEvidenceModel'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { ExtractorTester } from './ExtractorTester'
import RepairSuggestionList from './RepairSuggestionList'
import SnapshotBodyPreview, { DebugLogHighlightedText } from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { AuraRequestBodyTabs, AuraResponsePreview } from './AuraEnvelopeEditor'
import { isAuraEndpointTarget } from './auraEnvelopeModel'
import type { CorrelationApplyResult, CorrelationExtractorRepair } from './scriptCorrelationRepair'
import {
  CORRELATION_DIAGNOSIS_CATEGORY,
  formatReplayDiagnosisCategory,
  hasStructuredReplaySnapshot,
} from './scriptEditorReplayModel'
import { formatProtocolEvidenceSummary } from './scriptEditorWorkbench'
import {
  formatConsoleEntries,
  formatNetworkEntries,
  formatReplayHeaders,
  formatReplayTime,
} from './scriptEditorUtils'
import {
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import type { ReplayScreenshotSelection } from './scriptEditorState'
import {
  REQUEST_RESPONSE_INSPECTOR_TABS,
  availableInspectorTabs,
  canTestExtractor,
  diagnosisCauseRepeatsError,
  formatMillisecondsAsSecondsLabel,
  hasReplayExtractions,
  inspectorStatusLabel,
  inspectorTabCount,
  replayNetworkEntries,
  type ReplayVariableDiff,
  type RequestResponseInspectorTab,
  type ResponseActions,
} from './requestResponseInspectorModel'
import './RequestResponseInspector.css'

export interface RequestResponseInspectorProps {
  event: BrowserReplayEvent
  /** The event's index in the run: copy keys and the response selection are per event. */
  index: number
  script: ScriptDefinition
  /** The chosen sub-tab, kept by the caller so it survives selecting another event. */
  tab: RequestResponseInspectorTab
  onTabChange: (tab: RequestResponseInspectorTab) => void
  replaying: boolean
  replayMatchesCurrentScript: boolean
  runtimeVariablesUnavailable: boolean
  variableDiff: ReplayVariableDiff
  copiedSection: string | null
  onCopySection: (key: string, value: string) => Promise<void>
  onEditAction: (event: BrowserReplayEvent) => void
  onOpenDebugOptions: () => void
  addExtractorDefinition: (actionIndex: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair) => CorrelationApplyResult
  http3UpgradeScopes?: (event: BrowserReplayEvent) => HTTP3UpgradeScope[]
  onSwitchToHTTP3Upgrade?: (event: BrowserReplayEvent, scope: HTTP3UpgradeScope) => void
  responseActions?: ResponseActions
  onViewScreenshot: (selection: ReplayScreenshotSelection) => void
  /** Opens the debug Summary's Recording diff; absent when it cannot be reached. */
  onCompareWithRecording?: () => void
}

type RequestView = 'wire' | 'scripted'

/** One replayed action's evidence: a header, the verdict, then one view at a time. */
export default function RequestResponseInspector({
  event,
  index,
  script,
  tab,
  onTabChange,
  replaying,
  replayMatchesCurrentScript,
  runtimeVariablesUnavailable,
  variableDiff,
  copiedSection,
  onCopySection,
  onEditAction,
  onOpenDebugOptions,
  addExtractorDefinition,
  http3UpgradeScopes,
  onSwitchToHTTP3Upgrade,
  responseActions,
  onViewScreenshot,
  onCompareWithRecording,
}: RequestResponseInspectorProps) {
  const baseId = useId()
  // The Request view choice belongs to one event: another event, or a new run, opens on the default.
  const eventKey = `${index}:${event.started_at}`
  const [requestViewChoice, setRequestViewChoice] = useState<{ eventKey: string; view: RequestView } | null>(null)
  const preferredRequestView = requestViewChoice?.eventKey === eventKey ? requestViewChoice.view : null
  const setPreferredRequestView = (view: RequestView) => setRequestViewChoice({ eventKey, view })
  const available = availableInspectorTabs(event)
  const activeTab = available.has(tab) ? tab : 'response'
  const tabs = REQUEST_RESPONSE_INSPECTOR_TABS.filter(item => available.has(item.id))
  const tabId = (id: RequestResponseInspectorTab) => `${baseId}-tab-${id}`
  const panelId = (id: RequestResponseInspectorTab) => `${baseId}-panel-${id}`

  // Focus follows the selection when the shown tab changes under it: a focused
  // sub-tab the next event does not offer is removed from the DOM (focus would
  // drop to the page), and a fallen-back Response tab must not keep focus once
  // the remembered tab is offered again (focus would sit on an unselected tab).
  const focusedTab = useRef<RequestResponseInspectorTab | null>(null)
  const renderedTab = useRef(activeTab)
  useLayoutEffect(() => {
    const previous = renderedTab.current
    renderedTab.current = activeTab
    const focused = focusedTab.current
    if (previous === activeTab || focused === null || focused === activeTab) return
    const active = document.activeElement
    const stillOnTab = active instanceof HTMLElement && active.id === tabId(focused)
    const dropped = !active || active === document.body || !active.isConnected
    if (!stillOnTab && !dropped) {
      focusedTab.current = null
      return
    }
    document.getElementById(tabId(activeTab))?.focus()
  })

  // The sticky header's height, set on the scroll container (the inspector's
  // parent), becomes its scroll padding: content scrolled into view (focus,
  // arrow keys) and page-wise scrolling (PageDown) both stay clear of the header.
  const rootRef = useRef<HTMLDivElement>(null)
  const headerRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const scroller = rootRef.current?.parentElement
    const header = headerRef.current
    if (!scroller || !header || typeof ResizeObserver === 'undefined') return
    const update = () => scroller.style.setProperty('--rr-inspector-header-h', `${Math.ceil(header.getBoundingClientRect().height)}px`)
    update()
    const observer = new ResizeObserver(update)
    observer.observe(header)
    return () => {
      observer.disconnect()
      scroller.style.removeProperty('--rr-inspector-header-h')
    }
  }, [])

  const editableActionIndex = event.phase === 'action' && typeof event.action_index === 'number' && event.action_index < script.actions.length
    ? event.action_index
    : null
  const actionName = event.action_name || 'Unnamed action'
  const methodTarget = `${event.method || event.action_type || ''} ${event.target || ''}`.trim()
  const snapshot = event.snapshot
  const structuredSnapshot = snapshot && hasStructuredReplaySnapshot(snapshot) ? snapshot : null

  const verdictError = event.success ? null : event.error_message || 'Action failed.'
  const renderVerdict = () => (
    <section className={`rr-inspector-verdict ${event.success ? 'is-passed' : 'is-failed'}`} aria-label="Verdict">
      {verdictError === null
        ? <p className="rr-inspector-verdict-line">Passed{event.status_code ? ` · HTTP ${event.status_code}` : ''}</p>
        : <p className="rr-inspector-verdict-error" role="alert">{verdictError}</p>}
      {event.diagnosis && (
        <div className="replay-diagnosis-panel">
          <div>
            <span className="replay-detail-label">Diagnosis</span>
            <span className="replay-diagnosis-chip rr-inspector-diagnosis-chip">{formatReplayDiagnosisCategory(event.diagnosis.category)}</span>
            {/* The error is shown once: a cause that repeats it is left out. */}
            {event.diagnosis.cause && !diagnosisCauseRepeatsError(event.diagnosis.cause, verdictError) && <p>{event.diagnosis.cause}</p>}
            {event.diagnosis.evidence && <small>{event.diagnosis.evidence}</small>}
            {event.diagnosis.action && <small>{event.diagnosis.action}</small>}
            {!event.success && event.diagnosis.category === CORRELATION_DIAGNOSIS_CATEGORY && onCompareWithRecording && (
              <button type="button" className="link-button rr-inspector-link" onClick={onCompareWithRecording}>Compare with recording</button>
            )}
          </div>
          {(event.repair_suggestions || []).length > 0 && (
            <RepairSuggestionList
              suggestions={event.repair_suggestions || []}
              className="replay-repair-suggestions"
              itemClassName="replay-repair-suggestion"
              label="Draft repair suggestions"
              labelClassName="replay-detail-label"
              renderExtraAction={suggestion => {
                const fields = suggestion.fields || {}
                const expression = fields.expression
                const isExtractorFix = !!expression && (
                  suggestion.draft_patch_type?.includes('extractor')
                  || suggestion.draft_patch_type === 'add_extractor'
                  || !!fields.source
                )
                if (!isExtractorFix || editableActionIndex === null) return null
                return (
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={() => addExtractorDefinition(editableActionIndex, {
                      name: fields.name || 'correlation_value',
                      source: fields.source || 'body',
                      type: fields.type || fields.extract_type || 'regex',
                      expression: expression as string,
                    })}
                  >
                    Add as extractor
                  </button>
                )
              }}
            />
          )}
        </div>
      )}
    </section>
  )

  const renderResponse = () => {
    const soapFault = snapshot ? extractSOAPFaultSummaryFromBody(snapshot.response_body) : null
    const bodyMessage = snapshot?.response_body ? null : responseBodyMessage(snapshot?.response_body_state || event.response_body_state)
    return <>
      {bodyMessage && (
        <p className="rr-inspector-body-state">
          {bodyMessage.text}
          {bodyMessage.capturable && <> <button type="button" className="link-button" onClick={onOpenDebugOptions}>Open Debug options</button></>}
        </p>
      )}
      {structuredSnapshot && (
        <div className="debugger-http-snapshot rr-inspector-snapshot">
          {soapFault && (
            <div className="replay-code-block">
              <div className="replay-code-block-header">
                <span className="replay-detail-label">SOAP Fault</span>
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => void onCopySection(`${index}:soap-fault`, formatSOAPFaultSummary(soapFault))}
                >
                  {copiedSection === `${index}:soap-fault` ? 'Copied' : 'Copy fault'}
                </button>
              </div>
              <pre><DebugLogHighlightedText text={formatSOAPFaultSummary(soapFault)} /></pre>
            </div>
          )}
          <div className="replay-code-block">
            <div className="replay-code-block-header">
              <span className="replay-detail-label">Response</span>
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => void onCopySection(`${index}:response`, formatSnapshotResponseSection(structuredSnapshot, event.status_code))}
              >
                {copiedSection === `${index}:response` ? 'Copied' : 'Copy full response'}
              </button>
            </div>
            {(() => {
              const showAuraResponse = isAuraEndpointTarget(structuredSnapshot.request_url || event.target)
                || Boolean(structuredSnapshot.response_body && /^\s*(?:while\s*\(\s*1\s*\)|for\s*\(\s*;\s*;\s*\))/.test(structuredSnapshot.response_body))
              const response = <>
                <pre><DebugLogHighlightedText text={String(structuredSnapshot.response_status || event.status_code || 'n/a')} /></pre>
                <SnapshotTextPreview text={formatReplayHeaders(structuredSnapshot.response_headers)} />
                {showAuraResponse && <AuraResponsePreview body={structuredSnapshot.response_body} />}
                <div className="debugger-snapshot-body">
                  <SnapshotBodyPreview
                    body={structuredSnapshot.response_body}
                    headers={structuredSnapshot.response_headers}
                    emptyMessage="No response body captured"
                  />
                </div>
              </>
              const bodyState = structuredSnapshot.response_body_state || event.response_body_state
              // One selection per event: another event, or a new replay, starts afresh.
              return responseActions ? <ResponseSelection
                key={`${index}:${event.started_at}`}
                body={structuredSnapshot.response_body}
                bodyState={bodyState === 'partial' || bodyState === 'redacted' ? bodyState : 'whole'}
                status={responseActions.offersStatus ? structuredSnapshot.response_status || event.status_code : undefined}
                checkedStatus={responseActions.checkedStatus(event)}
                disabledReason={responseActions.disabledReason(event)}
                laterUses={text => responseActions.laterUses(event, text)}
                onContains={text => responseActions.onContains(event, text)}
                onSave={selection => responseActions.onSave(event, selection)}
                onCorrelate={selection => responseActions.onCorrelate(event, selection)}
                onStatus={status => responseActions.onStatus(event, status)}
              >{response}</ResponseSelection> : response
            })()}
          </div>
        </div>
      )}
    </>
  }

  const renderRequest = () => {
    const hasScripted = Boolean(event.body || (event.headers && Object.keys(event.headers).length > 0))
    const view: RequestView = structuredSnapshot && hasScripted ? (preferredRequestView ?? 'wire') : structuredSnapshot ? 'wire' : 'scripted'
    return <>
      {structuredSnapshot && hasScripted && (
        <div className="rr-inspector-segmented" role="group" aria-label="Request view">
          <button type="button" aria-pressed={view === 'wire'} onClick={() => setPreferredRequestView('wire')}>Sent on the wire</button>
          <button type="button" aria-pressed={view === 'scripted'} onClick={() => setPreferredRequestView('scripted')}>As scripted</button>
        </div>
      )}
      {view === 'wire' && structuredSnapshot ? (
        <div className="debugger-http-snapshot rr-inspector-snapshot">
          <div className="replay-code-block">
            <div className="replay-code-block-header">
              <span className="replay-detail-label">Request</span>
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => void onCopySection(`${index}:request`, formatSnapshotRequestSection(structuredSnapshot))}
              >
                {copiedSection === `${index}:request` ? 'Copied' : 'Copy full request'}
              </button>
            </div>
            <pre><DebugLogHighlightedText text={`${structuredSnapshot.request_method || 'n/a'} ${structuredSnapshot.request_url || ''}`.trim()} /></pre>
            {structuredSnapshot.request_wire_headers_unavailable && <p role="status">Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.</p>}
            {structuredSnapshot.request_headers_omitted && <p role="status">Some request headers were omitted or shortened by retention limits.</p>}
            <SnapshotTextPreview text={formatReplayHeaders(structuredSnapshot.request_headers)} />
            {/* The resolved body has its own Aura tabs; the wire body gets them only without it. */}
            <AuraRequestBodyTabs body={event.body ? undefined : structuredSnapshot.request_body}>
              <div className="debugger-snapshot-body">
                <SnapshotBodyPreview
                  body={structuredSnapshot.request_body}
                  headers={structuredSnapshot.request_headers}
                  emptyMessage="No request body captured"
                />
              </div>
            </AuraRequestBodyTabs>
          </div>
        </div>
      ) : (
        <>
          <div className="replay-code-block">
            <span className="replay-detail-label">Resolved Headers</span>
            <SnapshotTextPreview text={formatReplayHeaders(event.headers)} />
          </div>
          {event.body && (
            <div className="replay-code-block">
              <span className="replay-detail-label">Resolved Body</span>
              <AuraRequestBodyTabs body={event.body}>
                <SnapshotBodyPreview
                  body={event.body}
                  headers={event.headers}
                />
              </AuraRequestBodyTabs>
            </div>
          )}
        </>
      )}
    </>
  }

  const renderCorrelation = () => {
    const changedVariables = Object.entries(variableDiff)
    const liveTable = event.action_type === 'live_table'
    const testable = canTestExtractor(event)
    const empty = !liveTable && !hasReplayExtractions(event) && changedVariables.length === 0 && !testable
    const testerHeadingId = `${baseId}-extractor-tester`
    return <>
      {liveTable && <LiveTableStepResult event={event} action={replayMatchesCurrentScript && replayEventStepKey(event)
        ? resolveAuthorJsonScopeByKey(script, replayEventStepKey(event)!)?.action
        : undefined} />}
      <ReplayExtractions event={event} />
      {changedVariables.length > 0 && (
        <div className="replay-code-block">
          <span className="replay-detail-label">Variable Changes</span>
          <div className="replay-variable-diff-list">
            {changedVariables.map(([name, diff]) => (
              <div key={`${index}-${name}`} className="replay-variable-diff-item">
                <div className="replay-variable-diff-name">{name}</div>
                <div className="replay-variable-diff-values">
                  <span className="replay-variable-diff-before">{diff.previous ?? 'unset'}</span>
                  <span className="replay-variable-diff-arrow">{'→'}</span>
                  <span className="replay-variable-diff-after">{diff.next}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {testable && (
        <section className="rr-inspector-extractor-tester" aria-labelledby={testerHeadingId}>
          <h4 id={testerHeadingId}>Test an extractor against this response</h4>
          <ExtractorTester
            event={event}
            actionIndex={editableActionIndex}
            onAddExtractor={addExtractorDefinition}
          />
        </section>
      )}
      {empty && <p className="rr-inspector-empty">No extractions or variable changes were recorded for this action.</p>}
    </>
  }

  const renderTiming = () => {
    const pageURL = event.page_url || snapshot?.page_url
    const pageTitle = event.page_title || snapshot?.page_title
    const bodyState = snapshot?.response_body_state || event.response_body_state
    const version = runtimeVariablesUnavailable ? null : httpReplayVersionEvidence(event.protocol_evidence)
    const content = runtimeVariablesUnavailable ? null : httpResponseContentEvidence(event.protocol_evidence, bodyState)
    return <>
      <div className="replay-event-detail-grid">
        <div>
          <span className="replay-detail-label">Started</span>
          <div>{formatReplayTime(event.started_at)}</div>
        </div>
        <div>
          <span className="replay-detail-label">Completed</span>
          <div>{formatReplayTime(event.completed_at)}</div>
        </div>
        {pageURL && (
          <div>
            <span className="replay-detail-label">Page URL</span>
            <div className="replay-page-url">{pageURL}</div>
          </div>
        )}
        {pageTitle && (
          <div>
            <span className="replay-detail-label">Page Title</span>
            <div>{pageTitle}</div>
          </div>
        )}
      </div>
      {(version || content) && (
        <section className="replay-protocol-evidence rr-inspector-protocol-evidence" aria-label="Protocol evidence">
          <h4>Protocol evidence{[version?.actual, content?.state].filter(Boolean).map(part => ` · ${part}`).join('')}</h4>
          <HTTPVersionReplayEvidence evidence={event.protocol_evidence}
            upgrade={!replaying && replayMatchesCurrentScript && onSwitchToHTTP3Upgrade && http3UpgradeScopes
              ? { scopes: http3UpgradeScopes(event), onSwitch: scope => onSwitchToHTTP3Upgrade(event, scope) } : undefined} />
          <HTTPResponseContentEvidence evidence={event.protocol_evidence} bodyState={bodyState} />
        </section>
      )}
      <HTTPNetworkTraceEvidence
        evidence={event.protocol_evidence}
        networkEntries={replayNetworkEntries(event)}
        unavailable={runtimeVariablesUnavailable}
        inline
      />
    </>
  }

  const renderNetwork = () => {
    const networkEntries = replayNetworkEntries(event)
    const domSummary = event.dom_summary || snapshot?.dom_summary
    const consoleEntries = event.console_entries || snapshot?.console_entries
    const html = event.html || snapshot?.html
    const screenshotBase64 = event.screenshot_base64 || snapshot?.screenshot_base64
    const screenshot: ReplayScreenshotSelection | null = screenshotBase64 ? {
      alt: `${event.action_name || 'Replay action'} screenshot`,
      src: `data:image/png;base64,${screenshotBase64}`,
    } : null
    const hasPageArtifacts = Boolean(domSummary || consoleEntries?.length || screenshot || html)
    return <>
      <ResourceSnapshotList snapshot={snapshot} />
      {networkEntries?.length ? (
        <div className="replay-code-block replay-browser-network">
          <span className="replay-detail-label">Network entries</span>
          <SnapshotTextPreview text={formatNetworkEntries(networkEntries)} />
        </div>
      ) : null}
      {hasPageArtifacts && (
        <section className="rr-inspector-page-artifacts" aria-label="Page artifacts">
          <h4>Page artifacts</h4>
          <div className="replay-browser-grid">
            {domSummary && (
              <div className="replay-code-block">
                <span className="replay-detail-label">DOM Summary</span>
                <SnapshotBodyPreview
                  body={JSON.stringify(domSummary, null, 2)}
                  headers={{ 'Content-Type': 'application/json' }}
                />
              </div>
            )}
            {consoleEntries?.length ? (
              <div className="replay-code-block">
                <span className="replay-detail-label">Console</span>
                <SnapshotTextPreview text={formatConsoleEntries(consoleEntries)} />
              </div>
            ) : null}
            {screenshot && (
              <div className="replay-screenshot-block">
                <div className="replay-code-block-header">
                  <span className="replay-detail-label">Screenshot</span>
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => onViewScreenshot(screenshot)}>
                    View full size
                  </button>
                </div>
                <button type="button" className="replay-screenshot-button" onClick={() => onViewScreenshot(screenshot)}>
                  <img src={screenshot.src} alt={screenshot.alt} />
                </button>
              </div>
            )}
            {html && (
              <div className="replay-code-block replay-browser-html">
                <span className="replay-detail-label">HTML</span>
                <SnapshotTextPreview text={html} />
              </div>
            )}
          </div>
        </section>
      )}
    </>
  }

  const renderRaw = () => {
    const omittedCount = event.protocol_evidence_omitted_count
    const protocolSummary = formatProtocolEvidenceSummary(event.protocol_evidence)
    return <>
      {Boolean(omittedCount) && <p role="status">{omittedCount} protocol evidence {omittedCount === 1 ? 'item was' : 'items were'} omitted or shortened by retention limits.</p>}
      {event.protocol_evidence && (
        <div className="replay-code-block">
          <span className="replay-detail-label">Protocol Evidence{protocolSummary ? ` · ${protocolSummary}` : ''}</span>
          <SnapshotBodyPreview
            body={JSON.stringify(event.protocol_evidence, null, 2)}
            headers={{ 'Content-Type': 'application/json' }}
            emptyMessage="No protocol evidence captured"
          />
        </div>
      )}
      {snapshot && !structuredSnapshot && (
        <div className="replay-code-block">
          <span className="replay-detail-label">Snapshot</span>
          <SnapshotBodyPreview
            body={JSON.stringify(snapshot, null, 2)}
            headers={{ 'Content-Type': 'application/json' }}
          />
        </div>
      )}
    </>
  }

  const panels: Record<RequestResponseInspectorTab, () => ReactNode> = {
    response: renderResponse,
    request: renderRequest,
    correlation: renderCorrelation,
    timing: renderTiming,
    network: renderNetwork,
    raw: renderRaw,
  }

  return (
    <div className="debugger-detail rr-inspector" ref={rootRef}>
      <header className="debugger-detail-head rr-inspector-header" ref={headerRef}>
        <span className="replay-event-phase">{event.phase}</span>
        <strong className="rr-inspector-name" title={actionName}>{actionName}</strong>
        {methodTarget && <span className="replay-event-target rr-inspector-target" title={methodTarget}>{methodTarget}</span>}
        <span className={`rr-inspector-status ${event.success ? 'is-success' : 'is-failure'}`}>{inspectorStatusLabel(event)}</span>
        <span className="rr-inspector-duration">{formatMillisecondsAsSecondsLabel(event.duration_ms)}</span>
        {!replaying && replayMatchesCurrentScript && <button type="button" className="btn btn-sm btn-secondary rr-inspector-edit" onClick={() => onEditAction(event)}>Edit this action</button>}
      </header>
      {renderVerdict()}
      <TabList className="rr-inspector-tabs ux-underline-tabs" aria-label="Request and response views" onKeyDown={handleTabKey}>
        {tabs.map(item => {
          const selected = item.id === activeTab
          const count = inspectorTabCount(event, item.id, variableDiff)
          return (
            <button
              key={item.id}
              type="button"
              role="tab"
              id={tabId(item.id)}
              aria-selected={selected}
              aria-controls={panelId(item.id)}
              tabIndex={selected ? 0 : -1}
              className={selected ? 'active' : ''}
              onClick={() => onTabChange(item.id)}
              onFocus={() => { focusedTab.current = item.id }}
              onBlur={() => { focusedTab.current = null }}
            >
              <span>{item.label}</span>
              {count > 0 && <>{' '}<span className="rr-inspector-tab-badge">{count}</span></>}
            </button>
          )
        })}
      </TabList>
      {/* Every view stays mounted (inactive ones hidden), so an extractor being
          written or a response selection survives a look at another view. */}
      {tabs.map(item => (
        <div
          key={item.id}
          id={panelId(item.id)}
          role="tabpanel"
          aria-labelledby={tabId(item.id)}
          hidden={item.id !== activeTab}
          className={`replay-event-body rr-inspector-panel rr-inspector-panel-${item.id}`}
        >
          {panels[item.id]()}
        </div>
      ))}
    </div>
  )
}
