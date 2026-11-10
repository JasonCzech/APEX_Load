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
  adjacentFailureIndexes,
  availableInspectorTabs,
  buildCurlCommand,
  capturedResponseBody,
  canTestExtractor,
  countLiteralMatches,
  curlCommandGaps,
  defaultInspectorTabForEvent,
  diagnosisCauseRepeatsError,
  eventIndexById,
  extractorTesterUnappliedSettings,
  extractorTesterUnsupportedSurface,
  failingExtractorName,
  findExtractorDefinition,
  followerVerdictText,
  formatFindResult,
  formatMillisecondsAsSecondsLabel,
  formatTimingMs,
  hasReplayExtractions,
  htmlPageInsight,
  inspectorStatusLabel,
  inspectorTabCount,
  inspectorTimingBar,
  inspectorTimingBarLabel,
  isFollowerFailure,
  replayNetworkEntries,
  unresolvedVariables,
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
  /**
   * Replaces the event's step's extractor named name with the given source,
   * type and expression, in place: true when it did, else why not. Lets the
   * tester fix the failing extractor instead of adding a copy beside it.
   */
  replaceExtractorDefinition?: (event: BrowserReplayEvent, name: string, extractor: Pick<ScriptExtractorDefinition, 'source' | 'type' | 'expression'>) => true | string
  http3UpgradeScopes?: (event: BrowserReplayEvent) => HTTP3UpgradeScope[]
  onSwitchToHTTP3Upgrade?: (event: BrowserReplayEvent, scope: HTTP3UpgradeScope) => void
  responseActions?: ResponseActions
  onViewScreenshot: (selection: ReplayScreenshotSelection) => void
  /** Opens the debug Summary's Recording diff; absent when it cannot be reached. */
  onCompareWithRecording?: () => void
  /** The run's events, in the same positions as index (Previous/Next failure, Go to first failure). */
  events?: readonly BrowserReplayEvent[]
  /** Selects another event by its position in events, as the action list does. */
  onSelectEvent?: (index: number) => void
}

type RequestView = 'wire' | 'scripted'

function ChevronIcon({ direction }: { direction: 'up' | 'down' }) {
  return (
    <svg className="rr-inspector-icon" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
      <path d={direction === 'up' ? 'M4 10l4-4 4 4' : 'M4 6l4 4 4-4'} fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

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
  replaceExtractorDefinition,
  http3UpgradeScopes,
  onSwitchToHTTP3Upgrade,
  responseActions,
  onViewScreenshot,
  onCompareWithRecording,
  events,
  onSelectEvent,
}: RequestResponseInspectorProps) {
  const baseId = useId()
  // The Request view choice belongs to one event: another event, or a new run, opens on the default.
  const eventKey = `${index}:${event.started_at}`
  const [requestViewChoice, setRequestViewChoice] = useState<{ eventKey: string; view: RequestView } | null>(null)
  const preferredRequestView = requestViewChoice?.eventKey === eventKey ? requestViewChoice.view : null
  const setPreferredRequestView = (view: RequestView) => setRequestViewChoice({ eventKey, view })
  const [findInBody, setFindInBody] = useState<{ eventKey: string; query: string } | null>(null)
  const findQuery = findInBody?.eventKey === eventKey ? findInBody.query : ''
  const available = availableInspectorTabs(event)
  // A failed event opens on the view that explains it, until the user picks a
  // tab for it; a passed one shows the user's last pick. Display only: the
  // remembered pick (tab) is never overwritten by a default.
  const [tabPickedFor, setTabPickedFor] = useState<string | null>(null)
  // The pick lasts until the event changes: coming back to an event opens it
  // on its default again, whatever was clicked elsewhere in between.
  const [pickEventKey, setPickEventKey] = useState(eventKey)
  if (pickEventKey !== eventKey) {
    setPickEventKey(eventKey)
    setTabPickedFor(null)
  }
  const suggestedTab = tabPickedFor === eventKey ? null : defaultInspectorTabForEvent(event)
  const requestedTab = suggestedTab ?? tab
  const activeTab = available.has(requestedTab) ? requestedTab : 'response'
  const pickTab = (id: RequestResponseInspectorTab) => {
    setTabPickedFor(eventKey)
    onTabChange(id)
  }
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
  const follower = isFollowerFailure(event)
  const firstFailureIndex = follower && events ? eventIndexById(events, event.diagnosis?.follows_failure_event_id) : null
  const failureNav = onSelectEvent && events ? adjacentFailureIndexes(events, index) : null
  // A failure-navigation click keeps focus in the inspector: the button pressed
  // may be disabled (last failure) or gone (Go to first failure) once the
  // selection moves, so focus goes to it when it can, else to the other one,
  // else (the run's only failure: both disabled) to the selected view's tab.
  const previousFailureRef = useRef<HTMLButtonElement>(null)
  const nextFailureRef = useRef<HTMLButtonElement>(null)
  const pendingNavFocus = useRef<'previous' | 'next' | null>(null)
  const selectFailure = (target: number, focus: 'previous' | 'next') => {
    if (!onSelectEvent) return
    pendingNavFocus.current = focus
    onSelectEvent(target)
  }
  useLayoutEffect(() => {
    const wanted = pendingNavFocus.current
    if (!wanted) return
    pendingNavFocus.current = null
    const [first, second] = wanted === 'previous' ? [previousFailureRef.current, nextFailureRef.current] : [nextFailureRef.current, previousFailureRef.current]
    const target = first && !first.disabled ? first
      : second && !second.disabled ? second
        : rootRef.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')
    if (target && document.activeElement !== target) target.focus()
  }, [index])
  const curlCommand = structuredSnapshot ? buildCurlCommand(structuredSnapshot) : null
  // A snapshot cut by retention still copies, labelled incomplete (the button
  // and its tooltip name each gap) rather than passing for the wire request.
  const curlGaps = structuredSnapshot ? curlCommandGaps(structuredSnapshot) : []
  const curlGapsId = `${baseId}-curl-gaps`

  // The step's failing extractor seeds the tester. It is resolved only while
  // the replay matches the script (so a reordered action's same-named
  // extractor is not taken for it), and once found it stays for the event:
  // updating it in place must not re-resolve or re-key what the user is
  // testing. Until found (unapplied JSON edits, say) it is looked for again
  // whenever the replay matches, unless the user has already edited the plain
  // tester for this event, whose draft a seed would replace.
  const testable = canTestExtractor(event)
  const failingName = testable ? failingExtractorName(event) : null
  const testerKey = failingName ? `${eventKey}:${failingName}` : 'free'
  const resolveFailingExtractor = () => {
    const stepKey = replayEventStepKey(event)
    return failingName && stepKey && replayMatchesCurrentScript
      ? findExtractorDefinition(resolveAuthorJsonScopeByKey(script, stepKey)?.action.extractors, failingName)
      : null
  }
  const [failingSeed, setFailingSeed] = useState(() => ({ key: testerKey, extractor: resolveFailingExtractor() }))
  const [plainTesterEdited, setPlainTesterEdited] = useState<string | null>(null)
  if (failingSeed.key !== testerKey) {
    setFailingSeed({ key: testerKey, extractor: resolveFailingExtractor() })
    setPlainTesterEdited(null)
  } else if (failingSeed.extractor === null && failingName && replayMatchesCurrentScript && plainTesterEdited !== testerKey) {
    const extractor = resolveFailingExtractor()
    if (extractor) setFailingSeed({ key: testerKey, extractor })
  }
  const failingExtractor = failingSeed.key === testerKey ? failingSeed.extractor : null

  // The engine's rule-based diagnosis: the verdict for most failures, and
  // background (in Raw) for a failure that follows the run's first one.
  const renderDiagnosis = (diagnosis: NonNullable<BrowserReplayEvent['diagnosis']>, options: { compare: boolean; suggestions: NonNullable<BrowserReplayEvent['repair_suggestions']> }) => (
    <div className="replay-diagnosis-panel">
      <div>
        <span className="replay-detail-label">Diagnosis</span>
        <span className="replay-diagnosis-chip rr-inspector-diagnosis-chip">{formatReplayDiagnosisCategory(diagnosis.category)}</span>
        {/* The error is shown once: a cause that repeats it is left out. */}
        {diagnosis.cause && !diagnosisCauseRepeatsError(diagnosis.cause, verdictError) && <p>{diagnosis.cause}</p>}
        {diagnosis.evidence && <small>{diagnosis.evidence}</small>}
        {diagnosis.action && <small>{diagnosis.action}</small>}
        {options.compare && !event.success && diagnosis.category === CORRELATION_DIAGNOSIS_CATEGORY && onCompareWithRecording && (
          <button type="button" className="link-button rr-inspector-link" onClick={onCompareWithRecording}>Compare with recording</button>
        )}
      </div>
      {options.suggestions.length > 0 && (
        <RepairSuggestionList
          suggestions={options.suggestions}
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
  )

  const renderFollowerVerdict = () => {
    const { lead, reason } = followerVerdictText(event)
    const noteId = `${baseId}-first-failure-note`
    return (
      <section className="rr-inspector-verdict is-failed is-follower" aria-label="Verdict">
        <span className="rr-inspector-follows-chip">Follows an earlier failure</span>
        <p className="rr-inspector-follows-text">{reason ? `${lead} ${reason}` : lead}</p>
        <p className="rr-inspector-verdict-error is-quiet" role="alert">{verdictError}</p>
        {onSelectEvent && events && (
          <div className="rr-inspector-verdict-actions">
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={firstFailureIndex === null}
              aria-describedby={firstFailureIndex === null ? noteId : undefined}
              onClick={() => { if (firstFailureIndex !== null) selectFailure(firstFailureIndex, 'next') }}
            >
              Go to first failure
            </button>
            {firstFailureIndex === null && <small id={noteId} className="rr-inspector-verdict-note">First failure is no longer retained</small>}
          </div>
        )}
      </section>
    )
  }

  const renderVerdict = () => follower ? renderFollowerVerdict() : (
    <section className={`rr-inspector-verdict ${event.success ? 'is-passed' : 'is-failed'}`} aria-label="Verdict">
      {verdictError === null
        ? <p className="rr-inspector-verdict-line">Passed{event.status_code ? ` · HTTP ${event.status_code}` : ''}</p>
        : <p className="rr-inspector-verdict-error" role="alert">{verdictError}</p>}
      {event.diagnosis && renderDiagnosis(event.diagnosis, { compare: true, suggestions: event.repair_suggestions || [] })}
    </section>
  )

  // What the response is (an HTML page, maybe an unexpected login page) and a
  // find over its captured body.
  // A body cut by retention (or captured in part) is said to be partial, so
  // "No matches" or a missing login form does not read as proof of absence.
  const renderResponseSummary = (structured: NonNullable<typeof structuredSnapshot>) => {
    const page = htmlPageInsight(event)
    const captured = structured.response_body ? capturedResponseBody(event) : null
    const body = captured?.text || ''
    const partial = captured?.partial ?? false
    if (!page?.title && !page?.looksLikeLogin && !body) return null
    return (
      <div className="rr-inspector-response-summary">
        {page && (page.title || page.partial) && (
          <span className="rr-inspector-page-title">HTML page{page.title ? ` “${page.title}”` : ''}{page.partial ? ' (partial body)' : ''}</span>
        )}
        {page?.looksLikeLogin && <span className="rr-inspector-login-chip">Looks like a login page</span>}
        {body && (
          <div className="rr-inspector-find">
            <input
              type="search"
              autoComplete="off"
              aria-label="Find in response body"
              placeholder="Find in body"
              value={findQuery}
              onChange={change => setFindInBody({ eventKey, query: change.target.value })}
              onKeyDown={key => {
                if (key.key !== 'Escape' || !findQuery) return
                key.preventDefault()
                key.stopPropagation()
                setFindInBody({ eventKey, query: '' })
              }}
            />
            <span className="rr-inspector-find-count" role="status">
              {findQuery ? formatFindResult(countLiteralMatches(body, findQuery), partial) : ''}
            </span>
          </div>
        )}
      </div>
    )
  }

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
          {renderResponseSummary(structuredSnapshot)}
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
    const unset = unresolvedVariables(event)
    const empty = !liveTable && !hasReplayExtractions(event) && changedVariables.length === 0 && !testable && unset.length === 0
    const testerHeadingId = `${baseId}-extractor-tester`
    const stepKey = replayEventStepKey(event)
    // A failing extractor that reads what the tester cannot show (the URL,
    // the status code) is named instead of being tried against the body.
    const unsupportedSurface = failingExtractor ? extractorTesterUnsupportedSurface(failingExtractor) : null
    const seed = failingExtractor && !unsupportedSurface ? failingExtractor : null
    // Transforms and a response scope the tester cannot apply: its matches can
    // differ from the replay's, so it says so instead of claiming to test it.
    const unapplied = extractorTesterUnappliedSettings(seed)
    return <>
      {liveTable && <LiveTableStepResult event={event} action={replayMatchesCurrentScript && stepKey
        ? resolveAuthorJsonScopeByKey(script, stepKey)?.action
        : undefined} />}
      {unset.length > 0 && (
        <p className="rr-inspector-unset">
          <span className="replay-detail-label">Not set in this run:</span> {unset.join(', ')}
        </p>
      )}
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
          <h4 id={testerHeadingId}>{!seed ? 'Test an extractor against this response'
            : unapplied.length ? `Trying the expression of the failing extractor ${seed.name}`
              : `Testing the failing extractor ${seed.name}`}</h4>
          {seed && unapplied.length > 0 && (
            <p className="rr-inspector-tester-note">
              The tester tries the expression on this step’s final response only. It cannot apply {unapplied.length > 1 ? `${unapplied.slice(0, -1).join(', ')} and ${unapplied[unapplied.length - 1]}` : unapplied[0]}, so its matches can differ from the replay’s. Update keeps those settings.
            </p>
          )}
          {failingExtractor && unsupportedSurface && (
            <p className="rr-inspector-tester-note">
              The failing extractor {failingExtractor.name} reads {unsupportedSurface}, which the tester cannot show; edit it in the action instead.
            </p>
          )}
          <ExtractorTester
            // A failing extractor's draft belongs to its event: another event
            // starts afresh, and a seed found late (the replay matches the
            // script again) starts the untouched tester from it.
            key={seed ? `${testerKey}:seeded` : testerKey}
            event={event}
            actionIndex={editableActionIndex}
            onAddExtractor={addExtractorDefinition}
            initialDraft={seed ? {
              name: seed.name,
              source: seed.source,
              type: seed.type,
              expression: seed.expression,
            } : undefined}
            onUpdateExtractor={seed && replaceExtractorDefinition
              ? next => replaceExtractorDefinition(event, seed.name, next)
              : undefined}
            onDraftChange={seed ? undefined : () => setPlainTesterEdited(testerKey)}
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
    // Hop phases are runtime evidence: withheld with it, leaving the total.
    const bar = runtimeVariablesUnavailable ? inspectorTimingBar({ ...event, network_entries: undefined, snapshot: undefined }) : inspectorTimingBar(event)
    return <>
      <section className="rr-inspector-timing" aria-label="Timing">
        <p className="rr-inspector-timing-span">
          {formatReplayTime(event.started_at)} {'→'} {formatReplayTime(event.completed_at)} · <strong>{formatTimingMs(bar.totalMs)} total</strong>
        </p>
        <div className="rr-inspector-timing-bar" role="img" aria-label={inspectorTimingBarLabel(bar)}>
          {bar.segments.map(segment => (
            <span key={segment.key} className={`rr-inspector-timing-segment is-${segment.key}`} style={{ width: `${segment.widthPct}%` }} />
          ))}
        </div>
        {bar.phased && (
          <ul className="rr-inspector-timing-legend" aria-hidden="true">
            {bar.segments.map(segment => (
              <li key={segment.key}>
                <span className={`rr-inspector-timing-swatch is-${segment.key}`} />
                {segment.label} <span className="rr-inspector-timing-ms">{formatTimingMs(segment.durationMs)}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
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
    const ruleHeadingId = `${baseId}-rule-diagnosis`
    return <>
      {follower && event.diagnosis && (
        <section className="rr-inspector-rule-diagnosis" aria-labelledby={ruleHeadingId}>
          <h4 id={ruleHeadingId}>Rule-based diagnosis</h4>
          {/* The engine's own fix-first-failure suggestion is the verdict's Go to first failure. */}
          {renderDiagnosis(event.diagnosis, { compare: false, suggestions: (event.repair_suggestions || []).filter(suggestion => suggestion.draft_patch_type !== 'follows_failure') })}
        </section>
      )}
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
        <div className="rr-inspector-header-tools">
          {failureNav && onSelectEvent && (
            <div className="rr-inspector-failure-nav" role="group" aria-label="Failures">
              <button
                ref={previousFailureRef}
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={failureNav.previous === null}
                onClick={() => { if (failureNav.previous !== null) selectFailure(failureNav.previous, 'previous') }}
              >
                <ChevronIcon direction="up" /><span>Previous failure</span>
              </button>
              <button
                ref={nextFailureRef}
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={failureNav.next === null}
                onClick={() => { if (failureNav.next !== null) selectFailure(failureNav.next, 'next') }}
              >
                <ChevronIcon direction="down" /><span>Next failure</span>
              </button>
            </div>
          )}
          {curlCommand && (
            <button
              type="button"
              className={`btn btn-sm btn-ghost rr-inspector-curl${curlGaps.length ? ' is-incomplete' : ''}`}
              aria-describedby={curlGaps.length ? curlGapsId : undefined}
              onClick={() => void onCopySection(`${index}:curl`, curlCommand)}
            >
              {copiedSection === `${index}:curl` ? 'Copied' : curlGaps.length ? 'Copy as cURL (incomplete)' : 'Copy as cURL'}
            </button>
          )}
          {!replaying && replayMatchesCurrentScript && <button type="button" className="btn btn-sm btn-secondary rr-inspector-edit" onClick={() => onEditAction(event)}>Edit this action</button>}
        </div>
      </header>
      {/* Visible (not a title tooltip): touch and keyboard users see why too. */}
      {curlCommand && curlGaps.length > 0 && (
        <p id={curlGapsId} className="rr-inspector-curl-gaps">
          cURL copy is incomplete: {curlGaps.join('; ')}. The copy is the captured request only.
        </p>
      )}
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
              onClick={() => pickTab(item.id)}
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
