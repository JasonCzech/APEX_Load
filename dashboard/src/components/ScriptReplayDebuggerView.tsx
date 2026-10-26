import LiveTableSandboxEditor from './LiveTableSandboxEditor'
import LiveTableStepResult from './LiveTableStepResult'
import ResponseSelection, { type ResponseSelectionValue } from './ResponseSelection'
import { resolveAuthorJsonScopeByKey } from './scriptEditorJsonModel'
import { replayEventStepKey } from './actionChecksModel'

import { scriptDataUsage } from './scriptDataUsageModel'
import { liveTableSandboxError } from './liveTableSandboxModel'
import TooltipButton from './ux/TooltipButton'
import ResourceSnapshotList from './ResourceSnapshotList'
import { executionLabel, eventLocation, type DebugRunRecord, responseBodyMessage } from './debuggerExperience'
import { DebugProfiles, DebugVariableWatches, DebugTimeline, DebugRunHistory, DebugBreakpointControls } from './DebuggerSessionTools'
import { HTTPVersionReplayEvidence, type HTTP3UpgradeScope } from './HTTPVersionReplayEvidence'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import { httpReplayVersionEvidence } from './httpActionVersionModel'
import { httpResponseContentEvidence } from './httpResponseContentEvidenceModel'
import { debugContinuesAfterFailure, debugOverrideCount, debugRunIdError, sessionVariablesError } from './scriptSidebarModel'
import type { OutlineModel } from './scriptOutlineModel'
import ScriptOutlineTree, { type OutlineDebug, type ScriptOutlineSelection } from './ScriptOutlineTree'
import { buildDebugOutline } from './debugOutlineModel'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import { currentRuntimeLookup, currentRuntimeParameter, type RuntimeInspectionMode } from './debugRuntimeState'
import { DebugParameterInspector } from './DebugParameterInspector'
import { debugNextAction, debugNextRequestParts, previewTemplate, type PreviewSegment } from './debugNextRequest'
import { ParameterDefinitionGuide } from './ParameterDefinitionGuide'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import type * as React from 'react'
import { memo, useMemo, useRef, useState, useEffect } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
  ScriptDebugAction,
  ScriptDebugBreakpoint,
  ReplayRuntimeSnapshot,
} from '../types/replay'
import type {
  ScriptDefinition,
  ScriptExtractorDefinition,
} from '../types/scripts'
import type { useApi } from '../hooks/useApi'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import { CorrelationDiagnosePanel, type RecordingStatusState } from './CorrelationDiagnosePanel'
import { ExtractorTester } from './ExtractorTester'
import RepairSuggestionList from './RepairSuggestionList'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import type { CorrelationApplyResult, CorrelationExtractorRepair } from './scriptCorrelationRepair'
import {
  type AuthoringIssue,
} from './scriptAuthoringAssist'
import {
  debugBreakpointKey,
  formatDebugLeafSuffix,
  formatReplayDiagnosisCategory,
  highlightDebugLogRaw,
  hasStructuredReplaySnapshot,
  normalizeDebugPhase,
} from './scriptEditorReplayModel'
import {
  DEBUGGER_LAYOUT_LIMITS,
  buildDebugRailRenderRows,
  type DebugRailRenderRow,
  formatDebugPhaseLabel,
  formatProtocolEvidenceSummary,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'
import {
  formatConsoleEntries,
  formatNetworkEntries,
  formatReplayHeaders,
  formatReplayTime,
  hasBrowserReplayArtifacts,
} from './scriptEditorUtils'
import { sanitizeDownloadName } from './scriptEditorHelpers'
import {
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import type {
  ReplayActivityItem,
  ReplayOptionsState,
  ReplayScreenshotSelection,
  ScriptEditorStateSetter,
} from './scriptEditorState'
import type { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

const DEBUG_LOG_LEVELS: ScriptDebugLogLevel[] = ['error', 'warn', 'info', 'debug', 'trace']
const DEBUG_LOG_VIRTUALIZE_THRESHOLD = 100

const DebugConsoleLogEntry = memo(function DebugConsoleLogEntry({ entry }: { entry: ScriptDebugLogEntry }) {
  const location = entry.phase ? `${entry.phase} ${(entry.action_index ?? 0) + 1}` : null
  const highlightedRaw = useMemo(() => highlightDebugLogRaw(entry.raw || ''), [entry.raw])
  return (
    <>
      <span className="debug-log-summary">
        <span className="debug-log-timestamp">[{formatReplayTime(entry.timestamp)}]</span>{' '}
        <span className={`debug-log-level level-${entry.level}`}>{entry.level.toUpperCase()}</span>{' '}
        <span className="debug-log-category">{entry.category}</span>
        {location && <> <span className="debug-log-location">{location}</span></>}{' '}
        <span className="debug-log-message">{entry.message}</span>
      </span>
      {entry.raw && (
        <span className="debug-log-raw">
          {'\n'}
          {highlightedRaw.tokens.map((token, tokenIndex) => (
            <span key={`${entry.id}-raw-${tokenIndex}`} className={`debug-log-token token-${token.kind}`}>{token.text}</span>
          ))}
          {highlightedRaw.plainRemainder && <span className="debug-log-unhighlighted">{highlightedRaw.plainRemainder}</span>}
          {highlightedRaw.omittedCharCount > 0 && (
            <span className="debug-log-omission">{`\n… ${highlightedRaw.omittedCharCount.toLocaleString()} characters omitted from display; use Copy or Download for complete output.`}</span>
          )}
        </span>
      )}
    </>
  )
})

type ReplayActionEvent = BrowserReplayEvent
type ReplayDebuggerApi = Pick<ReturnType<typeof useApi>, 'diagnoseScriptReplay' | 'diagnoseCorrelations'>
type ReplayDebuggerController = ReturnType<typeof useScriptReplayDebuggerController>

interface ReplayVariableTimelineEntry {
  action: string
  index: number
  key: string
  name: string
  next: string
  previous?: string
}

/** What text selected in a replayed response can become, by the event's step. */
export interface ResponseActions {
  /** Whether the response's status can become a check (HTTP). */
  offersStatus: boolean
  disabledReason: (event: BrowserReplayEvent) => string
  laterUses: (event: BrowserReplayEvent, text: string) => number
  /** Each adds to the event's step and says whether it did (false: the step already had it). */
  onContains: (event: BrowserReplayEvent, text: string) => boolean
  onSave: (event: BrowserReplayEvent, selection: ResponseSelectionValue) => boolean
  onCorrelate: (event: BrowserReplayEvent, selection: ResponseSelectionValue) => void
  /** Sets the step's status check to status, adding one when it has none. */
  onStatus: (event: BrowserReplayEvent, status: number) => boolean
  /** The status the event's step already checks for, if any. */
  checkedStatus: (event: BrowserReplayEvent) => number | undefined
}

export interface ScriptReplayDebuggerViewProps {
  /** The debugger's run controls (Step, Continue, Stop…), drawn first in the session bar. */
  sessionControls?: ReactNode
  /** Session toggles that stay put through a run (Break on failure), drawn beside Debug options. */
  sessionToggles?: ReactNode
  runHistory: DebugRunRecord[]
  runRecord: Omit<DebugRunRecord, 'report'> | null
  seedVariables: Record<string, string>
  followExecution: boolean
  onFollowExecution: (enabled: boolean) => void
  onExportSession: () => Promise<void>
  onUpdateBreakpoints: (next: ScriptDebugBreakpoint[]) => Promise<void>
  onRunTo: (target: ScriptDebugBreakpoint) => Promise<void>
  actionProblemRows: Array<{ issue: AuthoringIssue; row: ScriptPhaseActionRow }>
  activeReplaySessionId: string | null
  addExtractorDefinition: (actionIndex: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair) => CorrelationApplyResult
  api: ReplayDebuggerApi
  applyReplaySuggestedLocator: (event: ReplayActionEvent, locator: string) => void
  breakpointSummary: string
  browserReplaySession: ScriptDebugSession | null
  clearDebugBreakpoints: () => Promise<void>
  controller: ReplayDebuggerController
  copiedReplaySnapshotSection: string | null
  debugActionRows: ScriptPhaseActionRow[]
  /** The Author outline: Debug's sidebar is the same tree, with breakpoints and run status. */
  debugOutline: OutlineModel
  /** The outline state Debug shares with Author: its selection, problem flags and markers. */
  outlineSelection: ScriptOutlineSelection | null
  outlineResetKey: string
  outlineHasIssue: (row: ScriptPhaseActionRow) => boolean
  outlineShowMarkers: boolean
  onOutlineShowMarkersChange: (show: boolean) => void
  debugBreakpointKeys: Set<string>
  /** The breakpoint keys plus each action holding a breakpoint anywhere in it, for the outline's flags. */
  outlineBreakpointKeys: Set<string>
  debugDetailHost?: HTMLDivElement | null
  debuggerWorkbench: ReactNode
  error: string
  expandedReplayBrowserArtifacts: Record<string, boolean>
  expandedReplayEvent: number | null
  exportGeneratedTextFile: (content: string, filename: string, savedMessage?: string) => Promise<void>
  handleCopyReplaySnapshotSection: (key: string, value: string) => Promise<void>
  handleValidateScript: () => Promise<void>
  isBrowserScript: boolean
  jsonParseMessage: string
  onEditReplayAction: (event: BrowserReplayEvent) => void
  onEditParameterDefinition: (name: string) => void
  onActivateDebug: () => void
  onSelectProblemAction: (row: ScriptPhaseActionRow) => void
  problemCount: number
  projectId?: string
  replayActivity: ReplayActivityItem[]
  replayFailingEvent: BrowserReplayEvent | null
  replayFailingEventIndex: number
  replaying: boolean
  replayLatestActivity?: ReplayActivityItem
  replayOptions: ReplayOptionsState
  replayReport: BrowserReplayReport | null
  replayEditableActionsKey: string | null
  currentEditableActionsKey: string | null
  replayVariableDiffs: Array<Record<string, { previous?: string; next: string }>>
  replayVariableInspector: Record<string, string> | null
  currentRuntimeSnapshot: ReplayRuntimeSnapshot | null
  currentRuntimeReason: string
  canInspectRuntimeFrame: (frame: ReplayRuntimeSnapshot['frames'][number]) => boolean
  onInspectRuntimeFrame: (frame: ReplayRuntimeSnapshot['frames'][number]) => void
  runtimeInspectionMode: RuntimeInspectionMode
  onRuntimeInspectionMode: (mode: RuntimeInspectionMode) => void
  runtimeVariablesUnavailable?: boolean
  /** The start request is out and no session is admitted yet: nothing runs until the controller answers. */
  replayStartPending?: boolean
  /** Why a new run cannot start now (blockers to resolve, a save first); Run to selected action says so. */
  runStartBlockedReason?: string
  replayVariableTimeline: ReplayVariableTimelineEntry[]
  saving: boolean
  /** Which policies an event's request can switch to auto+h3: its own, the script default, or both. */
  http3UpgradeScopes?: (event: BrowserReplayEvent) => HTTP3UpgradeScope[]
  onSwitchToHTTP3Upgrade?: (event: BrowserReplayEvent, scope: HTTP3UpgradeScope) => void
  script: ScriptDefinition
  /** What text selected in a step's replayed response can become, for that step. */
  responseActions?: ResponseActions
  scriptId?: string | null
  scriptLevelIssues: AuthoringIssue[]
  scriptRecordingStatus: ScriptRecordingStatus | null
  scriptRecordingStatusState: RecordingStatusState
  retryScriptRecordingStatus: () => void
  selectedActionKey?: string
  selectedOutlineParameterName: string | null
  selectOutlineParameter: (parameterName: string) => void
  parameterReferenceCounts: Map<string, number>
  onSelectDebugItem: (item: DebugRailRenderRow, hasEvidence: boolean) => void
  setBreakOnFailure: (enabled: boolean) => Promise<void>
  setExpandedReplayEvent: ScriptEditorStateSetter<number | null>
  setReplayOptions: ScriptEditorStateSetter<ReplayOptionsState>
  setReplayScreenshotSelection: ScriptEditorStateSetter<ReplayScreenshotSelection | null>
  setSequenceChildBreakpoints: (phase: string, actionIndex: number, requestCount: number, enabled: boolean) => Promise<void>
  toggleDebugBreakpoint: (phase: string, actionIndex: number, requestIndex?: number) => Promise<void>
  toggleReplayBrowserArtifact: (key: string, expanded: boolean) => void
  toggleStepPathBreakpoint: (phase: string, actionIndex: number, stepPath: string[]) => Promise<void>
  validating: boolean
  warnings: string[]
}

function formatMillisecondsAsSecondsLabel(valueMs: number | null | undefined, fallbackMs = 0): string {
  const normalized = Number.isFinite(valueMs) ? Number(valueMs) : fallbackMs
  return `${(normalized / 1000).toLocaleString(undefined, { maximumFractionDigits: 3 })}s`
}

export function ScriptReplayDebuggerView({
  sessionControls,
  sessionToggles,
  runHistory, runRecord, seedVariables, followExecution, onFollowExecution, onExportSession, onUpdateBreakpoints, onRunTo,
  actionProblemRows,
  activeReplaySessionId,
  addExtractorDefinition,
  api,
  breakpointSummary,
  browserReplaySession,
  clearDebugBreakpoints,
  controller,
  copiedReplaySnapshotSection,
  debugActionRows,
  debugOutline,
  outlineSelection,
  outlineResetKey,
  outlineHasIssue,
  outlineShowMarkers,
  onOutlineShowMarkersChange,
  debugBreakpointKeys,
  outlineBreakpointKeys,
  debugDetailHost,
  debuggerWorkbench,
  error,
  expandedReplayBrowserArtifacts,
  exportGeneratedTextFile,
  handleCopyReplaySnapshotSection,
  handleValidateScript,
  isBrowserScript,
  jsonParseMessage,
  onEditReplayAction,
  http3UpgradeScopes,
  onSwitchToHTTP3Upgrade,
  onActivateDebug,
  onEditParameterDefinition,
  onSelectProblemAction,
  problemCount,
  projectId,
  replayActivity,
  replayFailingEvent,
  replayFailingEventIndex,
  replaying,
  replayLatestActivity,
  replayOptions,
  replayReport,
  replayEditableActionsKey,
  currentEditableActionsKey,
  replayVariableDiffs,
  replayVariableInspector,
  runtimeVariablesUnavailable = false,
  replayStartPending = false,
  runStartBlockedReason,
  currentRuntimeSnapshot, currentRuntimeReason, runtimeInspectionMode, onRuntimeInspectionMode, canInspectRuntimeFrame, onInspectRuntimeFrame,
  replayVariableTimeline,
  saving,
  script,
  responseActions,
  scriptId,
  scriptLevelIssues,
  scriptRecordingStatus,
  scriptRecordingStatusState,
  retryScriptRecordingStatus,
  selectedActionKey,
  selectedOutlineParameterName,
  selectOutlineParameter,
  parameterReferenceCounts,
  onSelectDebugItem,
  setBreakOnFailure,
  setExpandedReplayEvent,
  setReplayOptions,
  setReplayScreenshotSelection,
  setSequenceChildBreakpoints,
  toggleDebugBreakpoint,
  toggleReplayBrowserArtifact,
  toggleStepPathBreakpoint,
  validating,
  warnings,
}: ScriptReplayDebuggerViewProps) {
  // The Base URL applies only to relative action targets.
  const absoluteTargetCount = [...(script.init || []), ...(script.actions || []), ...(script.teardown || [])].filter(action => /^https?:\/\//i.test(action.target || "")).length
  const {
    activeDebuggerResize,
    currentDebugAction,
    currentDebugActionKey,
    currentDebugAncestorKeys,
    currentDebugStepKey,
    debugBreakpoints,
    debugDetailCollapsed,
    debugEvents,
    debugFilteredLogs,
    debuggerBottomHeight,
    debuggerBottomTab,
    debuggerRailWidth,
    debuggerSettingsOpen,
    debuggerSettingsRef,
    debuggerStatusState,
    debuggerTab,
    debuggerVarsWidth,
    debugRailFilter,
    debugRawAutoScroll,
    debugRawLevel,
    debugRawOutputRef,
    debugRawOutputText,
    debugRawSearch,
    debugRowPositionByKey,
    effectiveExpandedDebugSequences,
    eventIndexByLocation,
    eventsByStepPath,
    handleDebuggerResizeKeyDown,
    selectedReplayEvent,
    selectedReplayEventIndex,
    setDebugDetailCollapsed,
    setDebuggerBottomTab,
    setDebuggerSettingsOpen,
    setDebuggerTab,
    setDebugRailFilter,
    setDebugRawAutoScroll,
    setDebugRawClearedAt,
    setDebugRawLevel,
    setDebugRawSearch,
    startDebuggerDrag,
    toggleDebugSequenceExpanded,
  } = controller
  const virtualizeDebugLogs = debugFilteredLogs.length > DEBUG_LOG_VIRTUALIZE_THRESHOLD
  const debugLogVirtualizer = useVirtualizer({
    count: debugFilteredLogs.length,
    enabled: virtualizeDebugLogs && debuggerBottomTab === 'console',
    estimateSize: () => 46,
    getScrollElement: () => debugRawOutputRef.current,
    overscan: 12,
  })
  const debugLogsToRender: Array<{ entry: ScriptDebugLogEntry; virtualItem: VirtualItem | null }> = virtualizeDebugLogs
    ? debugLogVirtualizer.getVirtualItems().map(virtualItem => ({ entry: debugFilteredLogs[virtualItem.index], virtualItem }))
    : debugFilteredLogs.map(entry => ({ entry, virtualItem: null }))
  const isReplayPaused = browserReplaySession?.status === 'paused'
  const replayTerminal = Boolean(replayReport?.completed_at) &&
    !replaying &&
    (!browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status))
  const replayMatchesCurrentScript = replayEditableActionsKey !== null &&
    currentEditableActionsKey !== null &&
    replayEditableActionsKey === currentEditableActionsKey
  const dataUsage = useMemo(() => scriptDataUsage(script), [script])
  // Identify the diagnosis inputs: the replay session plus the editable-action
  // snapshot taken when that replay started. The live draft is intentionally
  // not part of this key — applying a diagnosis fix edits the draft, and that
  // must not wipe the presented diagnosis (the panel tracks draft drift itself
  // through currentEditableActionsKey). Memoized: the event list can be large
  // and grows on every replay progress event.
  const correlationDiagnosisKey = useMemo(() => JSON.stringify({
    scriptId,
    replaySessionId: replayReport?.replay_session_id || activeReplaySessionId,
    scriptHash: replayReport?.script_hash,
    scriptRevision: replayReport?.script_revision,
    startedAt: replayReport?.started_at,
    completedAt: replayReport?.completed_at,
    eventCount: replayReport?.events?.length ?? 0,
    replayEditableActionsKey,
  }), [scriptId, replayReport, activeReplaySessionId, replayEditableActionsKey])

  useEffect(() => { if (debuggerSettingsOpen) debuggerSettingsRef.current?.querySelector<HTMLInputElement>('input')?.focus() }, [debuggerSettingsOpen, debuggerSettingsRef])
  const variablesError = sessionVariablesError(replayOptions.variablesText)
  const runIdError = debugRunIdError(replayOptions.runId)
  const optionsError = variablesError || runIdError || liveTableSandboxError(replayOptions.liveTableSeedsText)
  // What a test run of this script does after a failure, for the default choice.
  const testRunContinues = debugContinuesAfterFailure({ afterFailure: 'test-run' }, script)
  const [narrowPane, setNarrowPane] = useState<'outline' | 'editor' | 'inspector'>('editor')
  const [dockCollapsed, setDockCollapsed] = useState(true)
  const [rawLogs, setRawLogs] = useState(false)
  const [breakpointsPending, setBreakpointsPending] = useState(false)
  const runtimeFailures = debugEvents.map((event, index) => ({ event, index })).filter(({ event }) => !event.success)
  // Until the controller admits a session, the start is only being checked.
  const startPending = replaying && replayStartPending && !browserReplaySession
  const statusText = executionLabel(replaying, browserReplaySession, replayReport, startPending)
  // Shown beside Debug options, except while a run is live and the step controls need the room; the button's tooltip always carries it.
  const debugOptionsSummary = `1 virtual user · ${replayOptions.ignoreThinkTime ? 'Think time ignored' : 'Script think time'} · ${replayOptions.logLevel} logging${replayOptions.startPaused ? ' · Start paused' : ''} · ${debugContinuesAfterFailure(replayOptions, script) ? 'Continues' : 'Stops'} after a failure`
  const jumpToEvent = (index: number) => { setExpandedReplayEvent(index); setDebuggerTab('request'); setDebugDetailCollapsed(false); setNarrowPane('editor') }
  const debugOutlineState = useMemo(() => buildDebugOutline({
    events: debugEvents,
    eventIndexByLocation,
    eventsByStepPath,
    breakpoints: debugBreakpoints,
    breakpointKeys: debugBreakpointKeys,
    currentActionKey: currentDebugActionKey,
    currentStepKey: currentDebugStepKey,
    currentAncestorKeys: currentDebugAncestorKeys,
    terminal: replayTerminal,
  }), [currentDebugActionKey, currentDebugAncestorKeys, currentDebugStepKey, debugBreakpointKeys, debugBreakpoints, debugEvents, eventIndexByLocation, eventsByStepPath, replayTerminal])
  const debugOutlineOverlay = useMemo<OutlineDebug>(() => ({
    breakpoint: debugOutlineState.breakpoint,
    status: debugOutlineState.status,
    onToggleBreakpoint: row => {
      const target = debugOutlineState.breakpointTarget(row)
      if (!target) return
      if ('requests' in target) void setSequenceChildBreakpoints(target.phase, target.actionIndex, target.requests, target.set)
      else if ('stepPath' in target) void toggleStepPathBreakpoint(target.phase, target.actionIndex, target.stepPath)
      else void toggleDebugBreakpoint(target.phase, target.actionIndex, target.requestIndex)
    },
  }), [debugOutlineState, setSequenceChildBreakpoints, toggleDebugBreakpoint, toggleStepPathBreakpoint])
  // The outline marks the step whose evidence is open (the run's step while
  // following execution); otherwise the step selected in either workspace.
  const debugOutlineSelection = useMemo<ScriptOutlineSelection | null>(() => {
    if (!selectedReplayEvent || typeof selectedReplayEvent.action_index !== 'number') return outlineSelection
    const stepPath = selectedReplayEvent.request_index != null
      ? [`requests[${selectedReplayEvent.request_index}]`]
      : selectedReplayEvent.step_path?.length ? selectedReplayEvent.step_path : undefined
    return { phase: normalizeDebugPhase(selectedReplayEvent.phase), actionIndex: selectedReplayEvent.action_index, stepPath }
  }, [outlineSelection, selectedReplayEvent])
  const selectDebugOutlineItem = (item: DebugRailRenderRow) => {
    const eventIndex = debugOutlineState.eventIndex(item)
    onSelectDebugItem(item, eventIndex != null)
    setNarrowPane('editor')
    setDebuggerTab('request')
    if (eventIndex == null) return
    setExpandedReplayEvent(eventIndex)
    setDebugDetailCollapsed(false)
  }
  // Following execution keeps the action about to run in view in the action list.
  const debuggerRailRef = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (!followExecution || (!currentDebugActionKey && !currentDebugStepKey)) return
    debuggerRailRef.current?.querySelector<HTMLElement>('[aria-current="step"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [currentDebugActionKey, currentDebugStepKey, followExecution])
  const goCurrent = () => { const failureIndex = isReplayPaused && browserReplaySession?.current_event?.success === false ? debugEvents.findIndex(e => e.event_id != null && e.event_id === browserReplaySession.current_event?.event_id) : -1; setExpandedReplayEvent(failureIndex >= 0 ? failureIndex : null); onFollowExecution(true); setDebugRailFilter(''); setDebuggerTab('request'); setNarrowPane('editor'); setDebugDetailCollapsed(false) }
  const selectedItem = useMemo(() => buildDebugRailRenderRows(debugActionRows, effectiveExpandedDebugSequences).find(item => item.key === selectedActionKey), [debugActionRows, effectiveExpandedDebugSequences, selectedActionKey])
  const selectedRow = controller.selectedDebugRow || selectedItem?.row

  return (
          <div
            className={`config-panel replay-panel script-ide-debugger debugger-dialog open ${replayReport ? 'has-report' : ''}`}
            role="region"
            aria-label="Script Debugger"
          >
            {/* One session bar: the run's status, its step controls and its options.
                The selection tools and disclosures share the row below it. */}
            <div className={`debug-options-toolbar debug-session-bar${replaying ? ' is-running' : ''}`}>
              {sessionControls}
              {(replaying || replayReport) ? (
                <div className={`debugger-live-strip ${debuggerStatusState}`}>
                  <span className="replay-live-badge">{replaying ? (startPending ? 'Starting' : isReplayPaused ? 'Paused' : browserReplaySession?.status === 'stopped' ? 'Stopping' : 'Running') : replayReport?.stopped ? 'Stopped' : replayReport?.success ? 'Passed' : 'Failed'}</span>
                  {/* The badge carries the outcome; the text adds where the run is,
                      or what it recorded once it is over, never the same word. */}
                  <div className="debugger-live-strip-text">
                    <strong role="status" aria-live="polite">{replaying ? statusText : `${replayReport?.events.length || 0} recorded executions · ${replayReport?.events.filter(event => !event.success).length || 0} failures`}</strong>
                    {isReplayPaused && runtimeFailures.length > 0 && <span>{browserReplaySession?.continue_on_error === false ? 'Continue ends this iteration: this session stops after a failure.' : 'Continue runs the next action, as a test run would.'}</span>}
                  </div>
                  {replaying && <small>{replayReport?.events.length || 0} recorded executions · {replayReport?.events.filter(event => !event.success).length || 0} failures</small>}
                  <span className="debugger-live-strip-time">{formatReplayTime(replayLatestActivity?.timestamp || replayReport?.completed_at || replayReport?.started_at)}</span>
                </div>
              ) : <span className="debug-session-idle">No debug run yet</span>}
              <span className="debug-session-summary">{debugOptionsSummary}</span>
              <button type="button" className="btn btn-sm btn-secondary" ref={controller.debuggerSettingsBtnRef} aria-expanded={debuggerSettingsOpen} aria-controls="debug-session-options" title={debugOptionsSummary} onClick={() => setDebuggerSettingsOpen(value => !value)}>Debug options ({debugOverrideCount(replayOptions)})</button>
              {sessionToggles}
              <TooltipButton type="button" className="btn btn-sm btn-secondary" disabled={!replayReport} title="Download Debug JSON" onClick={() => void onExportSession()}>Export session</TooltipButton>
              {optionsError && <span role="alert">{optionsError}</span>}
            </div>
            {debuggerSettingsOpen && (
              <div id="debug-session-options" role="region" aria-label="Session Overrides" className="debugger-settings-popover" ref={debuggerSettingsRef}>
                <div className="debugger-settings-head">
                  <strong>Session Overrides</strong>
                  <button type="button" className="btn btn-sm btn-ghost" disabled={replaying} onClick={() => setReplayOptions({ ...DEFAULT_REPLAY_OPTIONS })}>Reset session options</button>
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setDebuggerSettingsOpen(false); controller.debuggerSettingsBtnRef.current?.focus() }}>Done</button>
                </div>
                <p className="replay-panel-subtitle">Temporary settings for this debugger session. They do not change saved Runtime Settings.</p>
                <DebugProfiles options={replayOptions} disabled={replaying} onChange={setReplayOptions} />
                <fieldset className="replay-options-grid" disabled={replaying}>
                  {(['browser', 'http', 'soap', 'java_http'].includes(script.protocol) || script.protocol.startsWith('http/')) && <fieldset className="replay-options-grid debug-har-options"><legend>HAR capture</legend>
                    <label><span>Retain bodies</span><select value={replayOptions.harBodies || 'none'} onChange={e => setReplayOptions(prev => ({ ...prev, harBodies: e.target.value as 'none' | 'request' | 'response' | 'both' }))}>
                      <option value="none">None</option><option value="request">Request only</option><option value="response">Response only</option><option value="both">Request and response</option>
                    </select></label>
                    <label className="replay-checkbox"><input type="checkbox" checked={replayOptions.harRedirects ?? true} onChange={e => setReplayOptions(prev => ({ ...prev, harRedirects: e.target.checked }))} />Retain intermediate redirects</label>
                    <label className="replay-checkbox"><input type="checkbox" checked={replayOptions.harTimings ?? true} onChange={e => setReplayOptions(prev => ({ ...prev, harTimings: e.target.checked }))} />Retain detailed timings</label>
                    <label><span>Per-body limit</span><select disabled={!replayOptions.harBodies || replayOptions.harBodies === 'none'} value={replayOptions.harMaxBodyBytes || 65536} onChange={e => setReplayOptions(prev => ({ ...prev, harMaxBodyBytes: Number(e.target.value) }))}>
                      {[16384, 65536, 262144, 1048576].map(bytes => <option key={bytes} value={bytes}>{bytes / 1024} KiB</option>)}
                    </select></label>
                    <label><span>Total body limit per run</span><select disabled={!replayOptions.harBodies || replayOptions.harBodies === 'none'} value={replayOptions.harMaxTotalBodyBytes || 4194304} onChange={e => setReplayOptions(prev => ({ ...prev, harMaxTotalBodyBytes: Number(e.target.value) }))}>
                      {[1048576, 4194304, 16777216].map(bytes => <option key={bytes} value={bytes}>{bytes / 1048576} MiB</option>)}
                    </select></label>
                    <p>Applies to the next run. Text bodies are redacted; binary, oversized, or incomplete bodies are omitted. Only observed redirect hops and measured timing phases can be retained. Existing evidence limits still apply.</p>
                  </fieldset>}
                  <label><span>Evidence capture</span><select value={replayOptions.capture || 'diagnostic'} onChange={e => setReplayOptions(prev => ({ ...prev, capture: e.target.value as 'diagnostic' | 'errors' | 'script' }))}><option value="diagnostic">Diagnostic — bounded detailed capture</option><option value="errors">Failures only</option><option value="script">Use saved Runtime Settings (custom)</option></select></label>
                  <p>HTTP/SOAP diagnostic capture: up to 100 messages, 64 KiB per message, 4 MiB total, subject to server retention and redaction. Browser capture uses per-action screenshots. Other protocols retain their native evidence.</p>
                  <label>
                    <span>Debugger Group</span>
                    <input
                      value={replayOptions.groupName}
                      onChange={e => setReplayOptions(prev => ({ ...prev, groupName: e.target.value }))}
                      placeholder={`${script.name || 'script'}-debug`}
                    />
                  </label>
                  <label>
                    <span>Log level</span>
                    <select
                      value={replayOptions.logLevel}
                      onChange={e => setReplayOptions(prev => ({ ...prev, logLevel: e.target.value as ScriptDebugLogLevel }))}
                    >
                      {DEBUG_LOG_LEVELS.map(level => (
                        <option key={level} value={level}>{level.toUpperCase()}</option>
                      ))}
                    </select>
                  </label>
                  <label className="checkbox-label replay-checkbox">
                    <input
                      type="checkbox"
                      checked={replayOptions.ignoreThinkTime}
                      onChange={e => setReplayOptions(prev => ({ ...prev, ignoreThinkTime: e.target.checked }))}
                    />
                    <span>Ignore think time</span>
                  </label>
                  <label className="checkbox-label replay-checkbox">
                    <input
                      type="checkbox"
                      checked={replayOptions.startPaused}
                      onChange={e => setReplayOptions(prev => ({ ...prev, startPaused: e.target.checked }))}
                    />
                    <span>Step through (start paused)</span>
                  </label>
                  <label className="checkbox-label replay-checkbox" title="Pause at a failed action so you can inspect it; Continue then does what After a failure says.">
                    <input
                      type="checkbox"
                      checked={replayOptions.breakOnFailure}
                      onChange={e => void setBreakOnFailure(e.target.checked)}
                    />
                    <span>Break on failure</span>
                  </label>
                  <label>
                    <span>After a failure</span>
                    <select aria-label="After a failure" value={replayOptions.afterFailure} onChange={e => setReplayOptions(prev => ({ ...prev, afterFailure: e.target.value as typeof prev.afterFailure }))}>
                      <option value="test-run">As a test run: {testRunContinues ? 'continue to the next action' : 'stop this iteration'}</option>
                      <option value="continue">Continue to the next action</option>
                      <option value="stop">Stop this iteration</option>
                    </select>
                  </label>
                  <label>
                    <span>Run identity <code>{'{{run.id}}'}</code></span>
                    <input aria-label="Run identity" aria-invalid={Boolean(runIdError)} value={replayOptions.runId} placeholder="debug-<session id>" onChange={e => setReplayOptions(prev => ({ ...prev, runId: e.target.value }))} />
                  </label>
                  <div className="full-width replay-variables-field">
                    <span>Session Variables JSON</span>
                    <div className="replay-variables-editor" style={{ border: '1px solid var(--border)', borderRadius: '8px', overflow: 'hidden', marginTop: '6px' }}>
                      <textarea aria-label="Session Variables JSON" aria-invalid={Boolean(variablesError)} value={replayOptions.variablesText} onChange={event => setReplayOptions(prev => ({ ...prev, variablesText: event.target.value }))} rows={5} />
                    </div>
                  </div>
                  <LiveTableSandboxEditor value={replayOptions.liveTableSeedsText} usage={dataUsage} onChange={liveTableSeedsText => setReplayOptions(prev => ({ ...prev, liveTableSeedsText }))} />
                </fieldset>
              </div>
            )}

            <div className="debugger-body">
              {isBrowserScript && selectedOutlineParameterName && <DebugParameterInspector key={`${selectedOutlineParameterName}:${selectedReplayEventIndex}`} script={script} name={selectedOutlineParameterName} values={replayVariableInspector} evidenceUnavailable={runtimeVariablesUnavailable} event={selectedReplayEvent} matchesDraft={replayMatchesCurrentScript} runtimeParameter={runtimeInspectionMode === 'current' ? currentRuntimeParameter(currentRuntimeSnapshot, selectedOutlineParameterName) : undefined} currentMode={runtimeInspectionMode === 'current'} onEdit={onEditParameterDefinition} onFindUsages={() => { selectOutlineParameter(selectedOutlineParameterName); setNarrowPane('editor') }} />}
              {isBrowserScript && <details className="debugger-evidence-hint"><summary>About debug evidence</summary><p>Recording verification checks a conversion draft. Debugging here checks the current saved or edited script; changes require a new debug run.</p></details>}
              <div className="debug-session-row">
              <div className="debug-session-tools debug-selection-status">
                <span>{selectedReplayEvent ? `Inspecting ${eventLocation(selectedReplayEvent)} · ${selectedReplayEvent.action_name}` : currentDebugAction ? `Current: ${currentDebugAction.action_name}` : 'Select an action to inspect evidence'}</span>
                <label><input type="checkbox" checked={followExecution} onChange={e => { if (e.target.checked) goCurrent(); else onFollowExecution(false) }} />Follow execution</label>
                <button type="button" disabled={!replaying || !currentDebugAction} onClick={goCurrent}>Go to current action</button>
                <TooltipButton type="button" title={!replaying && runStartBlockedReason ? runStartBlockedReason : 'Runs prerequisites; during a paused run, stops at the next matching execution'} disabled={!selectedRow || (replaying ? !isReplayPaused : Boolean(runStartBlockedReason))} onClick={() => { if (selectedRow) void onRunTo({ phase: selectedRow.phase, action_index: selectedRow.actionIndex, ...(selectedReplayEvent?.step_path?.length ? { step_path: selectedReplayEvent.step_path } : selectedReplayEvent?.request_index != null ? { request_index: selectedReplayEvent.request_index } : selectedItem?.kind === 'leaf' ? { step_path: selectedItem.stepPath } : {}) }) }}>Run to selected action</TooltipButton>
              </div>
              <div className="debug-session-disclosures">
              <details className="debug-effective-settings"><summary>Effective run configuration · {runRecord?.draft ? 'Unsaved draft' : 'Script settings'}</summary><dl><dt>Base URL (relative targets)</dt><dd>{runRecord?.target || script.config.base_url || 'Action-specific targets'}{absoluteTargetCount > 0 ? ` · ${absoluteTargetCount} action${absoluteTargetCount === 1 ? '' : 's'} use their own absolute URL` : ''}</dd><dt>Evidence capture</dt><dd>{runRecord?.options.capture || replayOptions.capture || 'diagnostic'}</dd><dt>Failure policy</dt><dd>{runRecord?.failurePolicy || (debugContinuesAfterFailure(replayOptions, script) ? 'Continue on error' : 'Stop on error')}</dd><dt>Inputs</dt><dd>{runRecord?.inputs?.length ?? (script.parameters?.length || 0)} parameters · {runRecord?.dataSourceCount ?? (script.data_sources?.length || 0)} data sources. Row selection follows Parameters; session variables override matching inputs.</dd></dl><ul>{(runRecord?.inputs || (script.parameters || []).map(p => ({ name: p.name, source: p.data_source || p.type, selection: p.selection || 'default', update: p.update, startRow: p.start_row, seed: p.seed }))).map(p => <li key={p.name}>{p.name}: {p.source} · {p.selection} · {p.update || 'default update'}{p.startRow != null ? ` · start row ${p.startRow}` : ''}{p.seed != null ? ` · seed ${p.seed}` : ''}</li>)}</ul></details>
              {isBrowserScript && <details className="debug-effective-settings"><summary>Variable watches</summary><DebugVariableWatches events={debugEvents} seed={seedVariables} latest={replayVariableInspector} evidenceUnavailable={runtimeVariablesUnavailable} selected={selectedReplayEventIndex} onSelect={jumpToEvent} snapshot={currentRuntimeSnapshot} currentReason={currentRuntimeReason} mode={runtimeInspectionMode} onModeChange={onRuntimeInspectionMode} completed={Boolean(replayReport?.completed_at)} finalValuesRecorded={Object.keys(replayReport?.final_variables || {}).length > 0} canInspectFrame={canInspectRuntimeFrame} onInspectFrame={frame => { onInspectRuntimeFrame(frame); setNarrowPane('editor') }} /></details>}
              <DebugRunHistory key={scriptId || projectId || 'draft'} runs={runHistory} current={replayReport} currentRecord={runRecord} />
              <DebugBreakpointControls breakpoints={debugBreakpoints} disabled={breakpointsPending} onChange={next => { setBreakpointsPending(true); void onUpdateBreakpoints(next).finally(() => setBreakpointsPending(false)) }} />
              </div>
              </div>
              {!replayReport?.stopped && !replayFailingEvent && replayReport?.error && <p className="replay-report-error">{replayReport.error}</p>}
              {replayFailingEvent && !isBrowserScript && selectedReplayEvent?.success !== false && (
                <div className="replay-failure-pin debugger-failure-pin">
                  <div>
                    <span className="replay-detail-label">First failing action</span>
                    <strong>{replayFailingEvent.action_name || `Action ${replayFailingEventIndex + 1}`}</strong>
                    <p>{replayFailingEvent.diagnosis?.cause || replayFailingEvent.error_message || 'Replay action failed.'}</p>
                    {replayFailingEvent.diagnosis && (
                      <div className="replay-diagnosis-chip-row">
                        <span className="replay-diagnosis-chip">{formatReplayDiagnosisCategory(replayFailingEvent.diagnosis.category)}</span>
                        {replayFailingEvent.diagnosis.evidence && <span>{replayFailingEvent.diagnosis.evidence}</span>}
                      </div>
                    )}
                  </div>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    onClick={() => {
                      onActivateDebug()
                      setDebuggerTab('request')
                      setDebugDetailCollapsed(false)
                      setExpandedReplayEvent(replayFailingEventIndex)
                    }}
                  >
                    Inspect
                  </button>
                </div>
              )}

              <div className="script-pane-switch" role="group" aria-label="Debug panes">{(isBrowserScript ? ['outline', 'editor'] as const : ['outline', 'editor', 'inspector'] as const).map(pane => <button type="button" key={pane} aria-pressed={narrowPane === pane} onClick={() => { setNarrowPane(pane); if (pane === 'inspector') setDebugDetailCollapsed(false) }}>{pane === 'outline' ? 'Outline' : pane === 'editor' ? 'Editor' : 'Inspector'}</button>)}</div>
              <div data-narrow-pane={narrowPane}
                className={`debugger-shell ${debugDetailCollapsed || isBrowserScript || debugDetailHost ? 'debugger-detail-collapsed' : ''} ${isBrowserScript ? 'browser-evidence-workspace' : ''} ${dockCollapsed ? 'debug-dock-collapsed' : ''} ${activeDebuggerResize ? `resizing resizing-${activeDebuggerResize}` : ''}`}
                style={{
                  '--rail-w': `${debuggerRailWidth}px`,
                  '--vars-w': `${debuggerVarsWidth}px`,
                  '--bottom-h': `${debuggerBottomHeight}px`,
                } as React.CSSProperties}
              >
                <aside className="debugger-rail" aria-label="Actions" ref={debuggerRailRef}>
                  {/* The Author outline, with breakpoints in its gutter and each step's run status. */}
                  <input className="script-outline-filter" type="search" autoComplete="off" aria-label="Find action" placeholder="Find action" value={debugRailFilter} onChange={event => setDebugRailFilter(event.target.value)} />
                  <div className="debug-outline-breakpoints">
                    <small>{breakpointSummary}</small>
                    {debugBreakpoints.length > 0 && <button type="button" className="btn btn-sm btn-ghost" onClick={() => void clearDebugBreakpoints()}>Clear</button>}
                  </div>
                  <nav className="script-action-navigator script-outline-tree" aria-label="Script outline">
                    <ScriptOutlineTree
                      model={debugOutline}
                      resetKey={outlineResetKey}
                      query={debugRailFilter}
                      expandedContainers={effectiveExpandedDebugSequences}
                      onToggleContainer={toggleDebugSequenceExpanded}
                      selection={debugOutlineSelection}
                      onSelect={selectDebugOutlineItem}
                      hasBreakpoint={key => outlineBreakpointKeys.has(key)}
                      hasIssue={outlineHasIssue}
                      showMarkers={outlineShowMarkers}
                      onShowMarkersChange={onOutlineShowMarkersChange}
                      debug={debugOutlineOverlay}
                    />
                  </nav>
                  {/* Debug only: a parameter opens its captured values in the inspector. */}
                  {(script.parameters || []).length > 0 && (
                    <details className="debug-outline-parameters" open>
                      <summary>Parameters <small>{script.parameters!.length}</small></summary>
                      {script.parameters!.map((parameter, index) => {
                        const referenceCount = parameter.name ? parameterReferenceCounts.get(parameter.name) || 0 : 0
                        const selected = Boolean(parameter.name) && selectedOutlineParameterName === parameter.name
                        return (
                          <button
                            key={`${parameter.name || 'parameter'}-${index}`}
                            type="button"
                            className={`debug-outline-parameter${selected ? ' selected' : ''}`}
                            aria-pressed={selected}
                            disabled={!parameter.name}
                            onClick={() => { if (parameter.name) { selectOutlineParameter(parameter.name); setNarrowPane(isBrowserScript ? 'editor' : 'inspector') } }}
                          >
                            <span>{parameter.name || `Parameter ${index + 1}`}</span>
                            <small>{parameter.type || 'list'} · {referenceCount} ref{referenceCount === 1 ? '' : 's'}</small>
                          </button>
                        )
                      })}
                    </details>
                  )}
                  <div
                    className={`debugger-resizer debugger-resizer-col ${activeDebuggerResize === 'rail' ? 'active' : ''}`}
                    onKeyDownCapture={e => handleDebuggerResizeKeyDown('rail', e)}
                    onPointerDown={e => startDebuggerDrag('rail', e)}
                    role="separator"
                    tabIndex={0}
                    title="Resize actions panel"
                    aria-label="Resize actions panel"
                    aria-orientation="vertical"
                    aria-valuemax={DEBUGGER_LAYOUT_LIMITS.rail.max}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.rail.min}
                    aria-valuenow={debuggerRailWidth}
                  />
                </aside>

                {debuggerWorkbench}

                {(!debugDetailCollapsed || debugDetailHost) && !isBrowserScript && (<EvidenceDestination host={debugDetailHost}>
                <section className="debugger-center" aria-label="Debug detail">
                  <div
                    className={`debugger-resizer debugger-resizer-col debugger-resizer-vars ${activeDebuggerResize === 'vars' ? 'active' : ''}`}
                    onKeyDownCapture={e => handleDebuggerResizeKeyDown('vars', e)}
                    onPointerDown={e => startDebuggerDrag('vars', e)}
                    role="separator"
                    tabIndex={0}
                    title="Resize debug panel"
                    aria-label="Resize debug panel"
                    aria-orientation="vertical"
                    aria-valuemax={DEBUGGER_LAYOUT_LIMITS.vars.max}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.vars.min}
                    aria-valuenow={debuggerVarsWidth}
                  />
                  <div className="debugger-center-tabs">
                    <TabList className="debugger-center-tablist ux-underline-tabs" role="tablist" aria-label="Action evidence views" onKeyDown={handleTabKey}>
                      <button type="button" role="tab" aria-selected={debuggerTab === 'request'} className={debuggerTab === 'request' ? 'active' : ''} onClick={() => setDebuggerTab('request')} tabIndex={(debuggerTab === 'request') ? 0 : -1}>Request / Response</button>
                      {isBrowserScript && (
                        <button type="button" role="tab" aria-selected={debuggerTab === 'browser'} className={debuggerTab === 'browser' ? 'active' : ''} onClick={() => setDebuggerTab('browser')} tabIndex={(debuggerTab === 'browser') ? 0 : -1}>Browser Evidence</button>
                      )}
                      <button type="button" role="tab" aria-selected={debuggerTab === 'variables'} className={debuggerTab === 'variables' ? 'active' : ''} onClick={() => setDebuggerTab('variables')} tabIndex={(debuggerTab === 'variables') ? 0 : -1}>{replaying ? 'Live Variables' : 'Variables'}</button>
                    </TabList>
                    <TooltipButton
                      type="button"
                      className="debugger-pane-close"
                      title="Close debug detail panel"
                      aria-label="Close debug detail panel"
                      onClick={() => { setDebugDetailCollapsed(true); setNarrowPane('editor') }}
                    >
                      ×
                    </TooltipButton>
                  </div>
                  <div className={`debugger-center-body ${debuggerTab === 'request' ? 'debugger-request-body' : ''}`}>
                    {debuggerTab === 'variables' ? (
                      <div className="debugger-variables-body">
                        {selectedOutlineParameterName && <DebugParameterInspector key={`${selectedOutlineParameterName}:${selectedReplayEventIndex}`} script={script} name={selectedOutlineParameterName} values={replayVariableInspector} evidenceUnavailable={runtimeVariablesUnavailable} event={selectedReplayEvent} matchesDraft={replayMatchesCurrentScript} runtimeParameter={runtimeInspectionMode === 'current' ? currentRuntimeParameter(currentRuntimeSnapshot, selectedOutlineParameterName) : undefined} currentMode={runtimeInspectionMode === 'current'} onFindUsages={() => { selectOutlineParameter(selectedOutlineParameterName); setNarrowPane('editor') }} onEdit={onEditParameterDefinition} />}
                        <details><summary>Template namespaces and definitions</summary><ParameterDefinitionGuide script={script} /></details>
                        <DebugVariableWatches events={debugEvents} seed={seedVariables} latest={replayVariableInspector} evidenceUnavailable={runtimeVariablesUnavailable} selected={selectedReplayEventIndex} onSelect={jumpToEvent} snapshot={currentRuntimeSnapshot} currentReason={currentRuntimeReason} mode={runtimeInspectionMode} onModeChange={onRuntimeInspectionMode} completed={Boolean(replayReport?.completed_at)} finalValuesRecorded={Object.keys(replayReport?.final_variables || {}).length > 0} canInspectFrame={canInspectRuntimeFrame} onInspectFrame={frame => { onInspectRuntimeFrame(frame); setNarrowPane('editor') }} />
                        {replayVariableTimeline.length > 0 && (
                          <div className="replay-variable-timeline">
                            <h5>Variable Timeline</h5>
                            {replayVariableTimeline.map(item => (
                              <button
                                key={item.key}
                                type="button"
                                className="replay-variable-timeline-row"
                                onClick={() => {
                                  setDebuggerTab('request')
                                  setExpandedReplayEvent(item.index)
                                }}
                              >
                                <span>{item.name}</span>
                                <strong>{item.action}</strong>
                                <small>{item.previous ?? 'unset'} {'->'} {item.next}</small>
                              </button>
                            ))}
                          </div>
                        )}
                      </div>
                    ) : (<>
                    {!isBrowserScript && debuggerTab !== 'browser' && replayReport && (
                      <details><summary>Compare with a recording (optional)</summary><CorrelationDiagnosePanel
                        scriptId={scriptId ?? null}
                        replaySessionId={replayReport.replay_session_id || activeReplaySessionId || undefined}
                        replayTerminal={replayTerminal}
                        replayMatchesCurrentScript={replayMatchesCurrentScript}
                        projectId={projectId}
                        events={replayReport.events}
                        recordingStatus={scriptRecordingStatus}
                        recordingStatusState={scriptRecordingStatusState}
                        onRetryRecordingStatus={retryScriptRecordingStatus}
                        diagnosisKey={correlationDiagnosisKey}
                        currentEditableActionsKey={currentEditableActionsKey}
                        actionCount={script.actions.length}
                        diagnoseWithBaseline={(id, replay, replaySessionId) => api.diagnoseScriptReplay(id, replay, replaySessionId)}
                        diagnoseWithUpload={(recorded, replay) => api.diagnoseCorrelations(recorded, replay)}
                        onAddExtractor={addExtractorDefinition}
                      /></details>
                    )}
                    {!replayReport ? (
                      <div className="debugger-empty">No debugger session yet. Set breakpoints in the action list (a row's gutter, or F9), review Debug options, then run one iteration.</div>
                    ) : selectedReplayEvent && selectedReplayEventIndex != null ? (
                      (() => {
                        const event = selectedReplayEvent
                        const index = selectedReplayEventIndex
                        const editableActionIndex = event.phase === 'action' && typeof event.action_index === 'number' && event.action_index < script.actions.length
                          ? event.action_index
                          : null
                        const variableDiff = replayVariableDiffs[index] || {}
                        const changedVariables = Object.entries(variableDiff)
                        const snapshotSoapFault = event.snapshot ? extractSOAPFaultSummaryFromBody(event.snapshot.response_body) : null
                        const replayPageURL = event.page_url || event.snapshot?.page_url
                        const replayPageTitle = event.page_title || event.snapshot?.page_title
                        const replayDOMSummary = event.dom_summary || event.snapshot?.dom_summary
                        const replayConsoleEntries = event.console_entries || event.snapshot?.console_entries
                        const replayNetworkEntries = event.network_entries || event.snapshot?.network_entries
                        const replayHTML = event.html || event.snapshot?.html
                        const replayScreenshotBase64 = event.screenshot_base64 || event.snapshot?.screenshot_base64
                        return (
                          <div className="debugger-detail">
                            <div className="debugger-detail-head">
                              {!replaying && replayMatchesCurrentScript && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onEditReplayAction(event)}>Edit this action</button>}
                              <span className="replay-event-phase">{event.phase}</span>
                              <strong>{event.action_name || 'Unnamed action'}</strong>
                              <span className="replay-event-target">{event.method || event.action_type} {event.target}</span>
                              <span className={event.success ? 'text-success' : 'text-danger'}>{event.success ? `HTTP ${event.status_code || 'OK'}` : event.error_message || 'Failed'}</span>
                              <span>{formatMillisecondsAsSecondsLabel(event.duration_ms)}</span>
                            </div>
                            <div className="replay-event-body">
                                <div className="replay-event-detail-grid">
                                  <div>
                                    <span className="replay-detail-label">Started</span>
                                    <div>{formatReplayTime(event.started_at)}</div>
                                  </div>
                                  <div>
                                    <span className="replay-detail-label">Completed</span>
                                    <div>{formatReplayTime(event.completed_at)}</div>
                                  </div>
                                  {replayPageURL && (
                                    <div>
                                      <span className="replay-detail-label">Page URL</span>
                                      <div className="replay-page-url">{replayPageURL}</div>
                                    </div>
                                  )}
                                  {replayPageTitle && (
                                    <div>
                                      <span className="replay-detail-label">Page Title</span>
                                      <div>{replayPageTitle}</div>
                                    </div>
                                  )}
                                </div>
                                      {!isBrowserScript && <section aria-label="Response evidence">
                                  <strong>Actual response: {event.status_code || 'No protocol status'}</strong>
                                  {event.error_message && <p role="alert">{event.error_message}</p>}
                                  {event.snapshot?.response_body
                                    ? <p>Body captured. <button type="button" className="link-button" onClick={() => document.getElementById('debug-captured-response')?.scrollIntoView({ block: 'nearest' })}>View captured response below</button></p>
                                    : (() => {
                                      const body = responseBodyMessage(event.snapshot?.response_body_state || event.response_body_state)
                                      return <p>{body.text}{body.capturable && <> <button type="button" className="link-button" onClick={() => setDebuggerSettingsOpen(true)}>Open Debug options</button></>}</p>
                                    })()}
                                  {/* Provenance answers "how do we know", after the answer itself: closed by default, headline kept in the summary. */}
                                  {!runtimeVariablesUnavailable && (() => {
                                    const bodyState = event.snapshot?.response_body_state || event.response_body_state
                                    const version = httpReplayVersionEvidence(event.protocol_evidence)
                                    const content = httpResponseContentEvidence(event.protocol_evidence, bodyState)
                                    if (!version && !content) return null
                                    return <details className="replay-protocol-evidence">
                                      <summary>Protocol evidence{[version?.actual, content?.state].filter(Boolean).map(part => ` · ${part}`).join('')}</summary>
                                      <HTTPVersionReplayEvidence evidence={event.protocol_evidence}
                                        upgrade={!replaying && replayMatchesCurrentScript && onSwitchToHTTP3Upgrade && http3UpgradeScopes
                                          ? { scopes: http3UpgradeScopes(event), onSwitch: scope => onSwitchToHTTP3Upgrade(event, scope) } : undefined} />
                                      <HTTPResponseContentEvidence evidence={event.protocol_evidence} bodyState={bodyState} />
                                    </details>
                                  })()}
                                </section>}
                                <ResourceSnapshotList snapshot={event.snapshot} />
                                {event.diagnosis && (
                                  <div className="replay-diagnosis-panel">
                                    <div>
                                      <span className="replay-detail-label">Diagnosis</span>
                                      <strong>{formatReplayDiagnosisCategory(event.diagnosis.category)}</strong>
                                      <p>{event.diagnosis.cause}</p>
                                      {event.diagnosis.evidence && <small>{event.diagnosis.evidence}</small>}

                                {event.diagnosis.action && <small>{event.diagnosis.action}</small>}
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
                                {event.protocol_evidence && (
                                  <details className="replay-code-block">
                                    <summary>Protocol Evidence{formatProtocolEvidenceSummary(event.protocol_evidence) ? ` · ${formatProtocolEvidenceSummary(event.protocol_evidence)}` : ''}</summary>
                                    <SnapshotBodyPreview
                                      body={JSON.stringify(event.protocol_evidence, null, 2)}
                                      headers={{ 'Content-Type': 'application/json' }}
                                      emptyMessage="No protocol evidence captured"
                                    />
                                  </details>
                                )}
                                {hasBrowserReplayArtifacts(event) && (
                                  <div className="replay-browser-grid">
                                    {(() => {
                                      const browserArtifacts = [
                                        replayDOMSummary ? 'dom_summary' : null,
                                        replayConsoleEntries?.length ? 'console' : null,
                                        replayNetworkEntries?.length ? 'network' : null,
                                        replayHTML ? 'html' : null,
                                      ].filter((artifact): artifact is 'dom_summary' | 'console' | 'network' | 'html' => Boolean(artifact))
                                      const browserArtifactKey = (artifact: 'dom_summary' | 'console' | 'network' | 'html') => `${index}:${artifact}`
                                      const allBrowserArtifactsExpanded = browserArtifacts.length > 0 && browserArtifacts.every(artifact => expandedReplayBrowserArtifacts[browserArtifactKey(artifact)])

                                      return (
                                        <div className="replay-browser-grid-header">
                                          <span className="replay-detail-label">Browser Artifacts</span>
                                          {browserArtifacts.length > 0 && (
                                            <button
                                              type="button"
                                              className="btn btn-sm btn-ghost"
                                              onClick={() => {
                                                browserArtifacts.forEach(artifact => {
                                                  toggleReplayBrowserArtifact(browserArtifactKey(artifact), !allBrowserArtifactsExpanded)
                                                })
                                              }}
                                            >
                                              {allBrowserArtifactsExpanded ? 'Collapse artifacts' : 'Expand artifacts'}
                                            </button>
                                          )}
                                        </div>
                                      )
                                    })()}
                                    {replayDOMSummary && (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">DOM Summary</span>
                                        <SnapshotBodyPreview
                                          body={JSON.stringify(replayDOMSummary, null, 2)}
                                          headers={{ 'Content-Type': 'application/json' }}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:dom_summary`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:dom_summary`, expanded)}
                                        />
                                      </div>
                                    )}
                                    {replayConsoleEntries?.length ? (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">Console</span>
                                        <SnapshotTextPreview
                                          text={formatConsoleEntries(replayConsoleEntries)}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:console`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:console`, expanded)}
                                        />
                                      </div>
                                    ) : null}
                                    {replayNetworkEntries?.length ? (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">Network</span>
                                        <SnapshotTextPreview
                                          text={formatNetworkEntries(replayNetworkEntries)}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:network`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:network`, expanded)}
                                        />
                                      </div>
                                    ) : null}
                                    {replayScreenshotBase64 && (
                                      <div className="replay-screenshot-block">
                                        <div className="replay-code-block-header">
                                          <span className="replay-detail-label">Screenshot</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => setReplayScreenshotSelection({
                                              alt: `${event.action_name || 'Replay action'} screenshot`,
                                              src: `data:image/png;base64,${replayScreenshotBase64}`,
                                            })}
                                          >
                                            View full size
                                          </button>
                                        </div>
                                        <button
                                          type="button"
                                          className="replay-screenshot-button"
                                          onClick={() => setReplayScreenshotSelection({
                                            alt: `${event.action_name || 'Replay action'} screenshot`,
                                            src: `data:image/png;base64,${replayScreenshotBase64}`,
                                          })}
                                        >
                                          <img src={`data:image/png;base64,${replayScreenshotBase64}`} alt={`${event.action_name || 'Replay action'} screenshot`} />
                                        </button>
                                      </div>
                                    )}
                                    {replayHTML && (
                                      <div className="replay-code-block replay-browser-html">
                                        <span className="replay-detail-label">HTML</span>
                                        <SnapshotTextPreview
                                          text={replayHTML}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:html`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:html`, expanded)}
                                        />
                                      </div>
                                    )}
                                  </div>
                                )}

                                <div className="replay-code-block">
                                  <span className="replay-detail-label">Resolved Headers</span>
                                  <SnapshotTextPreview text={formatReplayHeaders(event.headers)} />
                                </div>
                                {event.body && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Resolved Body</span>
                                    <SnapshotBodyPreview
                                      body={event.body}
                                      headers={event.headers}
                                    />
                                  </div>
                                )}
                                {event.action_type === 'live_table' && <LiveTableStepResult event={event} action={replayMatchesCurrentScript && replayEventStepKey(event)
                                  ? resolveAuthorJsonScopeByKey(script, replayEventStepKey(event)!)?.action
                                  : undefined} />}
                                {event.extractions && Object.keys(event.extractions).length > 0 && event.action_type !== 'live_table' && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Extractions</span>
                                    <SnapshotBodyPreview
                                      body={JSON.stringify(event.extractions, null, 2)}
                                      headers={{ 'Content-Type': 'application/json' }}
                                    />
                                  </div>
                                )}
                                {!isBrowserScript && (event.snapshot?.response_body || event.snapshot?.response_headers) && (
                                  <details><summary>Test an extractor against this response</summary><ExtractorTester
                                    event={event}
                                    actionIndex={editableActionIndex}
                                    onAddExtractor={addExtractorDefinition}
                                  /></details>
                                )}
                                {changedVariables.length > 0 && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Variable Changes</span>
                                    <div className="replay-variable-diff-list">
                                      {changedVariables.map(([name, diff]) => (
                                        <div key={`${index}-${name}`} className="replay-variable-diff-item">
                                          <div className="replay-variable-diff-name">{name}</div>
                                          <div className="replay-variable-diff-values">
                                            <span className="replay-variable-diff-before">{diff.previous ?? 'unset'}</span>
                                            <span className="replay-variable-diff-arrow">{'->'}</span>
                                            <span className="replay-variable-diff-after">{diff.next}</span>
                                          </div>
                                        </div>
                                      ))}
                                    </div>
                                  </div>
                                )}
                                {event.snapshot && (
                                  hasStructuredReplaySnapshot(event.snapshot) ? (
                                    <div className="replay-event-detail-grid debugger-http-snapshot">
                                      {snapshotSoapFault && (
                                        <div className="replay-code-block">
                                          <div className="replay-code-block-header">
                                            <span className="replay-detail-label">SOAP Fault</span>
                                            <button
                                              type="button"
                                              className="btn btn-sm btn-ghost"
                                              onClick={() => void handleCopyReplaySnapshotSection(`${index}:soap-fault`, formatSOAPFaultSummary(snapshotSoapFault))}
                                            >
                                              {copiedReplaySnapshotSection === `${index}:soap-fault` ? 'Copied' : 'Copy fault'}
                                            </button>
                                          </div>
                                          <pre>{formatSOAPFaultSummary(snapshotSoapFault)}</pre>
                                        </div>
                                      )}
                                      <div className="replay-code-block">
                                        <div className="replay-code-block-header">
                                          <span className="replay-detail-label">Request</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopyReplaySnapshotSection(`${index}:request`, formatSnapshotRequestSection(event.snapshot))}
                                          >
                                            {copiedReplaySnapshotSection === `${index}:request` ? 'Copied' : 'Copy full request'}
                                          </button>
                                        </div>
                                        <pre>{`${event.snapshot.request_method || 'n/a'} ${event.snapshot.request_url || ''}`.trim()}</pre>
                                        <SnapshotTextPreview text={formatReplayHeaders(event.snapshot.request_headers)} />
                                        <div className="debugger-snapshot-body">
                                          <SnapshotBodyPreview
                                            body={event.snapshot.request_body}
                                            headers={event.snapshot.request_headers}
                                            emptyMessage="No request body captured"
                                          />
                                        </div>
                                      </div>
                                      <div className="replay-code-block">
                                        <div className="replay-code-block-header">
                                          <span id="debug-captured-response" className="replay-detail-label">Response</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopyReplaySnapshotSection(`${index}:response`, formatSnapshotResponseSection(event.snapshot, event.status_code))}
                                          >
                                            {copiedReplaySnapshotSection === `${index}:response` ? 'Copied' : 'Copy full response'}
                                          </button>
                                        </div>
                                        {(() => {
                                          const response = <>
                                            <pre>{String(event.snapshot.response_status || event.status_code || 'n/a')}</pre>
                                            <SnapshotTextPreview text={formatReplayHeaders(event.snapshot.response_headers)} />
                                            <div className="debugger-snapshot-body">
                                              <SnapshotBodyPreview
                                                body={event.snapshot.response_body}
                                                headers={event.snapshot.response_headers}
                                                emptyMessage="No response body captured"
                                              />
                                            </div>
                                          </>
                                          const bodyState = event.snapshot.response_body_state || event.response_body_state
                                          // One selection per event: another event, or a new replay, starts afresh.
                                          return responseActions ? <ResponseSelection
                                            key={`${index}:${event.started_at}`}
                                            body={event.snapshot.response_body}
                                            bodyState={bodyState === 'partial' || bodyState === 'redacted' ? bodyState : 'whole'}
                                            status={responseActions.offersStatus ? event.snapshot.response_status || event.status_code : undefined}
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
                                  ) : (
                                    <div className="replay-code-block">
                                      <span className="replay-detail-label">Snapshot</span>
                                      <SnapshotBodyPreview
                                        body={JSON.stringify(event.snapshot, null, 2)}
                                        headers={{ 'Content-Type': 'application/json' }}
                                      />
                                    </div>
                                  )
                                )}
                            </div>
                          </div>
                        )
                      })()
                    ) : (replaying && browserReplaySession?.current_action) ? (
                      (() => {
                        const ca = browserReplaySession?.current_action
                        if (!ca) return null
                        return (
                          <div className="debugger-detail debugger-current-action">
                            <span className={`debugger-current-badge ${isReplayPaused ? 'paused' : 'running'}`}>{isReplayPaused ? 'Paused — about to execute' : 'Executing'}</span>
                            <div className="debugger-detail-head">
                              <span className="replay-event-phase">{formatDebugPhaseLabel(ca.phase)} {ca.action_index + 1}{formatDebugLeafSuffix(ca)}</span>
                              <strong>{ca.parent_action_name ? `${ca.parent_action_name} › ${ca.action_name || 'sub-request'}` : (ca.action_name || 'Next action')}</strong>
                              <span className="replay-event-target">{ca.method || ca.action_type} {ca.target || ca.selector}</span>
                            </div>
                            {isReplayPaused && <DebugNextRequest script={script} action={ca} values={runtimeVariablesUnavailable ? null : currentRuntimeLookup(currentRuntimeSnapshot)} runId={browserReplaySession?.run_id} />}
                            {isReplayPaused && <p className="debugger-current-hint">Press Step (F10) to execute this action, or Continue (F5) to run to the next breakpoint.</p>}
                          </div>
                        )
                      })()
                    ) : (
                      <div className="debugger-empty">Select an action from the list to inspect its request and response.</div>
                    )}
                    </>)}
                  </div>
                </section>
                </EvidenceDestination>)}

                <section className="debugger-bottom" aria-label="Debug dock">
                  <div
                    className={`debugger-resizer debugger-resizer-row ${activeDebuggerResize === 'bottom' ? 'active' : ''}`}
                    onKeyDownCapture={e => handleDebuggerResizeKeyDown('bottom', e)}
                    onPointerDown={e => startDebuggerDrag('bottom', e)}
                    role="separator"
                    tabIndex={0}
                    title="Resize bottom panel"
                    aria-label="Resize bottom panel"
                    aria-orientation="horizontal"
                    aria-valuemax={DEBUGGER_LAYOUT_LIMITS.bottom.max}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.bottom.min}
                    aria-valuenow={debuggerBottomHeight}
                  />
                  <div className="debugger-bottom-tabs">
                    <button type="button" aria-label={dockCollapsed ? "Expand logs and problems" : "Collapse logs and problems"} onClick={() => setDockCollapsed(value => !value)}>{dockCollapsed ? "Expand" : "Collapse"}</button>
                    <TabList role="tablist" aria-label="Debug logs and problems" className="debugger-bottom-tab-list ux-underline-tabs" onKeyDown={handleTabKey}>
                    <TooltipButton type="button" role="tab" aria-selected={debuggerBottomTab === 'console'} className={debuggerBottomTab === 'console' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('console') }} title="Wire log — raw protocol request/response and engine output" tabIndex={(debuggerBottomTab === 'console') ? 0 : -1}>Engine logs</TooltipButton>
                    <TooltipButton type="button" role="tab" aria-selected={debuggerBottomTab === 'timeline'} className={debuggerBottomTab === 'timeline' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('timeline') }} title="Flow log — actions, transactions, and variable changes in order" tabIndex={(debuggerBottomTab === 'timeline') ? 0 : -1}>Timeline</TooltipButton>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'problems'} className={debuggerBottomTab === 'problems' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('problems') }} tabIndex={(debuggerBottomTab === 'problems') ? 0 : -1} aria-label={`Problems: ${problemCount} script ${problemCount === 1 ? 'issue' : 'issues'}${runtimeFailures.length ? `, ${runtimeFailures.length} failed ${runtimeFailures.length === 1 ? 'execution' : 'executions'}` : ''}`}>
                      {/* The script's problems are the count Author shows; run failures are their own badge, never added in. */}
                      Problems{problemCount > 0 ? ` ${problemCount}` : ''}{runtimeFailures.length > 0 && <span className="debugger-tab-failed-count"> · {runtimeFailures.length} failed</span>}
                    </button>

                    </TabList>
                  </div>
                  {debuggerBottomTab === 'console' ? (
                    <div className="debug-raw-panel debugger-console">
                      <div className="debug-raw-toolbar"><label><input type="checkbox" checked={rawLogs} onChange={e => setRawLogs(e.target.checked)} />Raw text</label>
                        <label>
                          <span>Level</span>
                          <select value={debugRawLevel} onChange={e => setDebugRawLevel(e.target.value as ScriptDebugLogLevel | 'all')}>
                            <option value="all">All</option>
                            {DEBUG_LOG_LEVELS.map(level => <option key={level} value={level}>{level.toUpperCase()}+</option>)}
                          </select>
                        </label>
                        <label>
                          <span>Search</span>
                          <input value={debugRawSearch} onChange={e => setDebugRawSearch(e.target.value)} placeholder="Filter raw output" />
                        </label>
                        <label className="checkbox-label replay-checkbox">
                          <input type="checkbox" checked={debugRawAutoScroll} onChange={e => setDebugRawAutoScroll(e.target.checked)} />
                          <span>Auto-scroll</span>
                        </label>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => void navigator.clipboard?.writeText(debugRawOutputText)}>Copy</button>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => { void exportGeneratedTextFile(debugRawOutputText || 'No raw output captured', `${sanitizeDownloadName(script.name, 'script')}-debug.log`) }}>Download</button>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => setDebugRawClearedAt(browserReplaySession?.logs?.length || 0)}>Clear View</button>
                      </div>
                      <div ref={debugRawOutputRef} className={`debug-raw-output ${virtualizeDebugLogs ? 'virtualized' : ''}`}>
                        {rawLogs ? <pre>{debugRawOutputText || (browserReplaySession?.logs?.length ? 'No logs match the current filters.' : 'No logs captured yet.')}</pre> : debugFilteredLogs.length === 0 ? (
                          <span className="debug-log-empty">{browserReplaySession?.logs?.length ? 'No logs match the current filters.' : 'No raw output captured yet.'}</span>
                        ) : (
                          <div
                            className={virtualizeDebugLogs ? 'debug-log-virtual-list' : 'debug-log-list'}
                            style={virtualizeDebugLogs ? { height: `${debugLogVirtualizer.getTotalSize()}px` } : undefined}
                          >
                            {debugLogsToRender.map(({ entry, virtualItem }) => {
                              const jumpPosition = entry.phase ? debugRowPositionByKey.get(debugBreakpointKey(entry.phase, entry.action_index ?? 0)) : undefined
                              const canJump = jumpPosition != null && !!debugEvents[jumpPosition]
                              return (
                                <button
                                  key={entry.id}
                                  ref={virtualItem ? debugLogVirtualizer.measureElement : undefined}
                                  data-index={virtualItem?.index}
                                  type="button"
                                  className={`debug-log-line level-${entry.level} ${canJump ? 'jumpable' : ''}`}
                                  disabled={!canJump}
                                  style={virtualItem ? { transform: `translateY(${virtualItem.start}px)` } : undefined}
                                  onClick={() => {
                                    if (jumpPosition == null) return
                                    setDebuggerTab('request')
                                    setExpandedReplayEvent(jumpPosition)
                                  }}
                                >
                                  <DebugConsoleLogEntry entry={entry} />
                                </button>
                              )
                            })}
                          </div>
                        )}
                      </div>
                    </div>
                  ) : debuggerBottomTab === 'timeline' ? (
                    <div className="debugger-timeline">
                      <DebugTimeline events={debugEvents} onSelect={jumpToEvent} />
                      <details><summary>Session lifecycle ({replayActivity.length})</summary>{replayActivity.map(item => <p key={item.id}>{formatDisplayDateTime(item.timestamp, { milliseconds: true })} · {item.title} · {item.detail}</p>)}</details>
                    </div>
                  ) : debuggerBottomTab === 'problems' ? (
                    <div className="debugger-problems" role="tabpanel" aria-label="Problems">
                      <div className="debugger-problems-header">
                        <div>
                          <strong>{runtimeFailures.length} runtime failures · {problemCount} validation/editor issues</strong>
                          <span>Validation and editor issues remain visible in the Author inspector.</span>
                        </div>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={handleValidateScript} disabled={saving || replaying || validating || !script.name}>
                          {validating ? 'Validating…' : 'Validate'}
                        </button>
                      </div>
                      <section aria-label="Runtime failures">{runtimeFailures.length ? runtimeFailures.map(({ event, index }) => <button className="debug-timeline-row" type="button" key={event.event_id || index} onClick={() => jumpToEvent(index)}><strong>{event.action_name} · {eventLocation(event)}</strong><span>{event.error_message || 'Execution failed'}</span><small>{event.status_code ? `Actual response: ${event.status_code}` : ''}</small></button>) : <p>{replayReport ? 'No runtime failures in retained evidence.' : 'Run the script to check runtime behavior.'}</p>}</section>
                      {problemCount === 0 ? (
                        <div className="debugger-empty">No validation warnings or authoring issues are currently reported.</div>
                      ) : (
                        <div className="debugger-problem-list">
                          {jsonParseMessage && (
                            <div className="debugger-problem-row error">
                              <span>JSON</span>
                              <strong>{jsonParseMessage}</strong>
                            </div>
                          )}
                          {error && (
                            <div className="debugger-problem-row error">
                              <span>Error</span>
                              <strong>{error}</strong>
                            </div>
                          )}
                          {warnings.map((warning, index) => (
                            <div key={`runtime-warning-${index}`} className="debugger-problem-row warning">
                              <span>Warning</span>
                              <strong>{warning}</strong>
                            </div>
                          ))}
                          {scriptLevelIssues.map(issue => (
                            <div key={issue.id} className={`debugger-problem-row ${issue.severity}`}>
                              <span>{issue.severity}</span>
                              <strong>{issue.message}</strong>
                            </div>
                          ))}
                          {actionProblemRows.map(({ issue, row }) => (
                            <button
                              key={issue.id}
                              type="button"
                              className={`debugger-problem-row ${issue.severity} action-problem`}
                              onClick={() => {
                                onSelectProblemAction(row)
                              }}
                            >
                              <span>{row.phaseLabel} {row.actionIndex + 1}</span>
                              <strong>{row.action.name || issue.message}</strong>
                              <small>{row.action.name ? issue.message : ''}</small>
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  ) : null}

                </section>
              </div>

              <div className="debugger-statusbar">
                <span className="debugger-statusbar-item">Group: {replayReport?.group || replayOptions.groupName || 'replay'}</span>
                <span className="debugger-statusbar-item">Protocol: {replayReport?.protocol || script.protocol}</span>
                {/* After a run its counts are in the session bar. */}
                {!replayReport && <span className="debugger-statusbar-item">{debugActionRows.length} actions planned</span>}
                {replayReport?.completed_at && <span className="debugger-statusbar-item">Completed {formatReplayTime(replayReport.completed_at)}</span>}
                {debugBreakpoints.length > 0 && <span className="debugger-statusbar-item">{debugBreakpoints.length} breakpoint{debugBreakpoints.length === 1 ? '' : 's'}</span>}
              </div>
            </div>
          </div>
  )
}

const PREVIEW_BODY_LIMIT = 4000
const PREVIEW_TITLES: Record<PreviewSegment['kind'], string | undefined> = {
  text: undefined,
  value: 'This session\u2019s current value',
  unresolved: 'No value yet; it is sent as written unless an earlier step provides it',
  secret: 'Project secret; resolved when the step runs and never shown',
  generated: 'Generated when the step runs',
}

/** A template with the session's current values filled in, each kind marked. */
function PreviewText({ segments }: { segments: PreviewSegment[] }) {
  return <>{segments.map((segment, index) => segment.kind === 'text'
    ? <span key={index}>{segment.text}</span>
    : <span key={index} className={`debug-preview-${segment.kind}`} title={PREVIEW_TITLES[segment.kind]}>{segment.text}</span>)}</>
}

/**
 * The request a paused session runs next, as VuGen's next-step view shows it:
 * the action's target, headers and body with the values this session holds
 * now. Values an earlier step has yet to provide stay as written.
 */
function DebugNextRequest({ script, action, values, runId }: { script: ScriptDefinition; action: ScriptDebugAction; values: Readonly<Record<string, string>> | null; runId?: string }) {
  const definition = debugNextAction(script, action)
  if (!definition) return null
  const { target, headers, body } = debugNextRequestParts(definition)
  if (!target && !headers.length && !body) return null
  const shownBody = body && body.length > PREVIEW_BODY_LIMIT ? `${body.slice(0, PREVIEW_BODY_LIMIT)}…` : body
  return (
    <section className="debugger-next-request" aria-label="Next request">
      <h4>Next request <small>{values ? 'with this session\u2019s current values' : 'this session\u2019s values are not available, so references show as written'}</small></h4>
      {target && <p className="debugger-next-request-line"><code>{definition.method || action.method || action.action_type}</code> <PreviewText segments={previewTemplate(target, values, runId)} /></p>}
      {headers.length > 0 && <dl className="debugger-next-request-headers">{headers.map(([name, value], index) => <div key={`${name}-${index}`}><dt>{name}</dt><dd><PreviewText segments={previewTemplate(value, values, runId)} /></dd></div>)}</dl>}
      {shownBody && <pre className="debugger-next-request-body"><PreviewText segments={previewTemplate(shownBody, values, runId)} /></pre>}
    </section>
  )
}

function EvidenceDestination({ host, children }: { host?: HTMLElement | null; children: ReactNode }) { return host ? createPortal(children, host) : children }
