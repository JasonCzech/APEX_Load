import LiveTableSandboxEditor from './LiveTableSandboxEditor'
import LazyLoadBoundary from './LazyLoadBoundary'
import type { RequestResponseInspectorTab, ResponseActions } from './requestResponseInspectorModel'

import { scriptDataUsage } from './scriptDataUsageModel'
import { liveTableSandboxError } from './liveTableSandboxModel'
import TooltipButton from './ux/TooltipButton'
import PanelToggle from './ux/PanelToggle'
import { executionLabel, eventLocation, type DebugRunRecord } from './debuggerExperience'
import { DebugProfiles, DebugVariableWatches, DebugRunHistory, DebugBreakpointControls } from './DebuggerSessionTools'
import { DebugWaterfallPanel } from './DebugWaterfallPanel'
import { replayFrameForEvent } from './waterfall/replaySourceFrame'
import type { HTTP3UpgradeScope } from './HTTPVersionReplayEvidence'
import { debugContinuesAfterFailure, debugOverrideCount, debugRunIdError, sessionVariablesError } from './scriptSidebarModel'
import type { OutlineModel } from './scriptOutlineModel'
import ScriptOutlineTree, { type OutlineDebug, type ScriptOutlineSelection } from './ScriptOutlineTree'
import { buildDebugOutline } from './debugOutlineModel'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import { currentRuntimeLookup, type RuntimeInspectionMode } from './debugRuntimeState'
import { debugNextAction, debugNextRequestParts, previewTemplate, type PreviewSegment } from './debugNextRequest'
import InspectorHeaderTable from './InspectorHeaderTable'
import { isPasswordLikeFieldName, MASKED_VALUE } from './inspectorHeadersModel'
import { isCredentialLocatorName, isSensitiveCredentialPreviewHeader, isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'
import type { StepPosition } from './referenceScopeModel'
import { ParameterDefinitionGuide } from './ParameterDefinitionGuide'
import { effectiveThinkTimeLabel } from './runtimeWorkspaceModel'
import { formatPacingTiming } from './runtimeTiming'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import type * as React from 'react'
import { lazy, memo, Suspense, useMemo, useRef, useState, useEffect } from 'react'
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
import { DebugLogHighlightedText } from './SnapshotBodyPreview'
import type { IssueDetail } from './IssueDetailsDialog'
import type { CorrelationApplyResult, CorrelationExtractorRepair } from './scriptCorrelationRepair'
import {
  type AuthoringIssue,
} from './scriptAuthoringAssist'
import {
  debugBreakpointKey,
  formatDebugLeafSuffix,
  formatReplayDiagnosisCategory,
  highlightDebugLogRaw,
  isReplayTerminal,
  normalizeDebugPhase,
} from './scriptEditorReplayModel'
import {
  DEBUGGER_LAYOUT_LIMITS,
  type DebugRailRenderRow,
  formatDebugPhaseLabel,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'
import { formatReplayTime } from './scriptEditorUtils'
import { sanitizeDownloadName } from './scriptEditorHelpers'
import type {
  ReplayActivityItem,
  ReplayOptionsState,
  ReplayScreenshotSelection,
  ScriptEditorStateSetter,
} from './scriptEditorState'
import type { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

// The Request/Response inspector and its header, correlation and network
// tables load with the first selected step, keeping them out of the
// ScriptEditor chunk.
const RequestResponseInspector = lazy(() => import('./RequestResponseInspector'))

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
type ReplayDebuggerController = ReturnType<typeof useScriptReplayDebuggerController>

interface ReplayVariableTimelineEntry {
  action: string
  index: number
  key: string
  name: string
  next: string
  previous?: string
}

export type { ResponseActions } from './requestResponseInspectorModel'

export interface ScriptReplayDebuggerViewProps {
  /** The debugger's run controls (Step, Continue, Stop…), drawn first in the session bar. */
  sessionControls?: ReactNode
  /** Checks & readiness popover content, drawn inside the Checks & readiness popover. */
  readinessPopover?: ReactNode
  onCloseEditor?: () => void
  onOpenHistory?: () => void
  runHistory: DebugRunRecord[]
  runRecord: Omit<DebugRunRecord, 'report'> | null
  seedVariables: Record<string, string>
  followExecution: boolean
  onFollowExecution: (enabled: boolean) => void
  onUpdateBreakpoints: (next: ScriptDebugBreakpoint[]) => Promise<void>
  actionProblemRows: Array<{ issue: AuthoringIssue; row: ScriptPhaseActionRow }>
  addExtractorDefinition: (
    actionIndex: number,
    extractor: ScriptExtractorDefinition,
    repair?: CorrelationExtractorRepair | Partial<StepPosition> | null,
    step?: Partial<StepPosition> | null,
  ) => CorrelationApplyResult
  onAddExtractor?: (
    actionIndex: number,
    extractor: ScriptExtractorDefinition,
    repair?: CorrelationExtractorRepair | Partial<StepPosition> | null,
    step?: Partial<StepPosition> | null,
  ) => CorrelationApplyResult | void
  /** Active center tab / workspace identifier (e.g. 'debug-summary' vs 'action-list'). */
  activeWorkspace?: string
  /** Replaces a replayed step's extractor in place (the inspector's failing-extractor tester): true when it did, else why not. */
  replaceExtractorDefinition?: (event: ReplayActionEvent, name: string, extractor: Pick<ScriptExtractorDefinition, 'source' | 'type' | 'expression'>) => true | string
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
  canEditAction?: (event: BrowserReplayEvent) => boolean
  onEditAction?: (event: BrowserReplayEvent) => void
  canRevealInScript?: (event: BrowserReplayEvent) => boolean
  onRevealInScript?: (event: BrowserReplayEvent) => void
  onEditReplayAction: (event: BrowserReplayEvent) => void
  onEditParameterDefinition: (name: string) => void
  onActivateDebug: () => void
  onOpenRequestResponse?: () => void
  /** Opens the debug Summary's Recording diff (the run compared with a recording); absent when it cannot be reached. */
  onCompareWithRecording?: () => void
  onSelectProblemAction: (row: ScriptPhaseActionRow) => void
  additionalIssues?: IssueDetail[]
  onSelectAdditionalIssue?: (issue: IssueDetail) => void
  /**
   * The steps whose execution blockers refuse a run, listed first under
   * "Blocking replay (N)", each opened in Author (onSelectAdditionalIssue) or
   * deleted from the draft. Their rows are left out of additionalIssues' list.
   */
  replayBlockers?: Array<IssueDetail & { onDelete: () => void }>
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

export function ScriptReplayDebuggerView({
  sessionControls,
  readinessPopover,
  onCloseEditor,
  onOpenHistory,
  runHistory, runRecord, seedVariables, followExecution, onFollowExecution, onUpdateBreakpoints,
  actionProblemRows,
  addExtractorDefinition,
  onAddExtractor,
  activeWorkspace,
  replaceExtractorDefinition,
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
  exportGeneratedTextFile,
  handleCopyReplaySnapshotSection,
  handleValidateScript,
  isBrowserScript,
  jsonParseMessage,
  canRevealInScript,
  onRevealInScript,
  canEditAction,
  onEditAction,
  onEditReplayAction,
  http3UpgradeScopes,
  onSwitchToHTTP3Upgrade,
  onActivateDebug,
  onOpenRequestResponse,
  onCompareWithRecording,
  onSelectProblemAction,
  additionalIssues = [],
  onSelectAdditionalIssue,
  replayBlockers = [],
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
  currentRuntimeSnapshot, currentRuntimeReason, runtimeInspectionMode, onRuntimeInspectionMode, canInspectRuntimeFrame, onInspectRuntimeFrame,
  replayVariableTimeline,
  saving,
  script,
  responseActions,
  scriptId,
  scriptLevelIssues,
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
  toggleStepPathBreakpoint,
  validating,
  warnings,
}: ScriptReplayDebuggerViewProps) {
  // The Base URL applies only to relative action targets.
  const absoluteTargetCount = [...(script.init || []), ...(script.actions || []), ...(script.teardown || [])].filter(action => /^https?:\/\//i.test(action.target || "")).length
  const [inspectorRetryCount, setInspectorRetryCount] = useState(0)
  const InspectorComponent = useMemo(
    () => (inspectorRetryCount === 0 ? RequestResponseInspector : lazy(() => import('./RequestResponseInspector'))),
    [inspectorRetryCount],
  )
  useEffect(() => {
    void import('./RequestResponseInspector').catch(() => {})
  }, [])
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
    debuggerBottomMaximumHeight,
    debuggerBottomTab,
    debuggerRailWidth,
    debuggerReadinessOpen,
    debuggerReadinessRef,
    debuggerSettingsOpen,
    debuggerSettingsRef,
    debuggerStatusState,
    debuggerTab,
    debuggerVarsWidth,
    debuggerVarsActualWidth,
    debuggerVarsMaximumWidth,
    setDebuggerDetailElement,
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
    setDebuggerReadinessOpen,
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
  const replayTerminal = isReplayTerminal(replayReport, replaying, browserReplaySession)
  const replayMatchesCurrentScript = replayEditableActionsKey !== null &&
    currentEditableActionsKey !== null &&
    replayEditableActionsKey === currentEditableActionsKey
  const dataUsage = useMemo(() => scriptDataUsage(script), [script])

  useEffect(() => { if (debuggerSettingsOpen) debuggerSettingsRef.current?.querySelector<HTMLInputElement>('input')?.focus() }, [debuggerSettingsOpen, debuggerSettingsRef])
  const variablesError = sessionVariablesError(replayOptions.variablesText)
  const runIdError = debugRunIdError(replayOptions.runId)
  const optionsError = variablesError || runIdError || liveTableSandboxError(replayOptions.liveTableSeedsText)
  // What a test run of this script does after a failure, for the default choice.
  const testRunContinues = debugContinuesAfterFailure({ afterFailure: 'test-run' }, script)
  const [narrowPane, setNarrowPane] = useState<'outline' | 'editor' | 'inspector'>('editor')
  const [dockCollapsed, setDockCollapsed] = useState(true)
  const effectiveAddExtractor = useMemo(
    () => (
      actionIndex: number,
      extractor: ScriptExtractorDefinition,
      repair?: CorrelationExtractorRepair | Partial<StepPosition> | null,
      step?: Partial<StepPosition> | null,
    ): CorrelationApplyResult => {
      if (onAddExtractor) {
        const res = onAddExtractor(actionIndex, extractor, repair, step)
        if (res) return res
      }
      return addExtractorDefinition(actionIndex, extractor, repair, step)
    },
    [addExtractorDefinition, onAddExtractor],
  )
  const prevActiveWorkspaceRef = useRef(activeWorkspace)
  useEffect(() => {
    if (prevActiveWorkspaceRef.current !== activeWorkspace) {
      prevActiveWorkspaceRef.current = activeWorkspace
      if (activeWorkspace === 'debug-summary') {
        setDockCollapsed(true)
      }
    }
  }, [activeWorkspace])
  const [rawLogs, setRawLogs] = useState(false)
  const [breakpointsPending, setBreakpointsPending] = useState(false)
  const [variablesSubtab, setVariablesSubtab] = useState<'values' | 'sources'>('values')
  // The Request/Response sub-tab stays put while another action is selected.
  const [inspectorTab, setInspectorTab] = useState<RequestResponseInspectorTab>('response')
  const runtimeFailures = debugEvents.map((event, index) => ({ event, index })).filter(({ event }) => !event.success)
  // Until the controller admits a session, the start is only being checked.
  const startPending = replaying && replayStartPending && !browserReplaySession
  const statusText = executionLabel(replaying, browserReplaySession, replayReport, startPending)
  // Shown beside Debug options, except while a run is live and the step controls need the room; the button's tooltip always carries it.
  const debugOptionsSummary = `1 virtual user · ${replayOptions.ignoreThinkTime ? 'Think time ignored' : 'Script think time'} · ${replayOptions.logLevel} logging${replayOptions.startPaused ? ' · Start paused' : ''} · ${debugContinuesAfterFailure(replayOptions, script) ? 'Continues' : 'Stops'} after a failure`
  const jumpToEvent = (index: number) => { setExpandedReplayEvent(index); setDebuggerTab('request'); onOpenRequestResponse?.(); setDebugDetailCollapsed(false); setNarrowPane('editor') }
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
    onOpenRequestResponse?.()
    setExpandedReplayEvent(eventIndex)
    setDebugDetailCollapsed(false)
  }
  // Following execution keeps the action about to run in view in the action list.
  const debuggerRailRef = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (!followExecution || (!currentDebugActionKey && !currentDebugStepKey)) return
    debuggerRailRef.current?.querySelector<HTMLElement>('[aria-current="step"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [currentDebugActionKey, currentDebugStepKey, followExecution])
  // The inspector's failure navigation (Previous/Next failure, Go to first
  // failure) selects like the action list: following execution turns off (the
  // wrapped setter) and the outline highlight follows the selection, whose row
  // is then scrolled into view.
  const [railScrollRequest, setRailScrollRequest] = useState(0)
  const selectInspectorEvent = (index: number) => {
    jumpToEvent(index)
    setRailScrollRequest(request => request + 1)
  }
  useEffect(() => {
    if (!railScrollRequest) return
    debuggerRailRef.current?.querySelector<HTMLElement>('[role="treeitem"][aria-selected="true"], .script-outline-row.selected')?.scrollIntoView?.({ block: 'nearest' })
  }, [railScrollRequest])
  const goCurrent = () => { const failureIndex = isReplayPaused && browserReplaySession?.current_event?.success === false ? debugEvents.findIndex(e => e.event_id != null && e.event_id === browserReplaySession.current_event?.event_id) : -1; setExpandedReplayEvent(failureIndex >= 0 ? failureIndex : null); onFollowExecution(true); setDebugRailFilter(''); setDebuggerTab('request'); onOpenRequestResponse?.(); setNarrowPane('editor'); setDebugDetailCollapsed(false) }

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
              <button
                type="button"
                className="btn btn-sm btn-secondary"
                ref={controller.debuggerSettingsBtnRef}
                aria-expanded={debuggerSettingsOpen}
                aria-controls="debug-session-options"
                title={debugOptionsSummary}
                onClick={() => {
                  setDebuggerSettingsOpen(value => {
                    const next = !value
                    if (next) setDebuggerReadinessOpen(false)
                    return next
                  })
                }}
              >
                Debug options ({debugOverrideCount(replayOptions)})
              </button>
              <button
                type="button"
                className="btn btn-sm btn-secondary"
                ref={controller.debuggerReadinessBtnRef}
                aria-expanded={debuggerReadinessOpen}
                aria-controls="debug-checks-readiness"
                onClick={() => {
                  setDebuggerReadinessOpen(value => {
                    const next = !value
                    if (next) setDebuggerSettingsOpen(false)
                    return next
                  })
                }}
              >
                Checks &amp; readiness
              </button>
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
              {optionsError && <span role="alert">{optionsError}</span>}
              {onCloseEditor && (
                <button
                  type="button"
                  className="btn btn-sm btn-secondary"
                  aria-label="Close script editor"
                  onClick={onCloseEditor}
                >
                  Close editor
                </button>
              )}
              <button
                type="button"
                className="btn btn-sm btn-secondary script-editor-more"
                disabled={!scriptId}
                title={scriptId ? 'View script history' : 'Save the script to view its history'}
                onClick={() => onOpenHistory?.()}
              >
                History
              </button>
            </div>
            <div id="debug-checks-readiness" role="region" aria-label="Checks & readiness" className="debugger-settings-popover debugger-readiness-popover" ref={debuggerReadinessRef} hidden={!debuggerReadinessOpen}>
              <div className="debugger-settings-head">
                <strong>Checks &amp; readiness</strong>
                <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setDebuggerReadinessOpen(false); controller.debuggerReadinessBtnRef.current?.focus() }}>Done</button>
              </div>
              <p className="replay-panel-subtitle">Static checks inspect the draft. Runtime prerequisites are verified when debugging starts. Replay evidence belongs to its recorded revision.</p>
              {readinessPopover}
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
                    <label className="replay-checkbox"><input type="checkbox" checked={replayOptions.networkTrace ?? true} onChange={e => setReplayOptions(prev => ({ ...prev, networkTrace: e.target.checked }))} />Capture advanced network trace</label>
                    <label><span>Per-body limit</span><select disabled={!replayOptions.harBodies || replayOptions.harBodies === 'none'} value={replayOptions.harMaxBodyBytes || 65536} onChange={e => setReplayOptions(prev => ({ ...prev, harMaxBodyBytes: Number(e.target.value) }))}>
                      {[16384, 65536, 262144, 1048576].map(bytes => <option key={bytes} value={bytes}>{bytes / 1024} KiB</option>)}
                    </select></label>
                    <label><span>Total body limit per run</span><select disabled={!replayOptions.harBodies || replayOptions.harBodies === 'none'} value={replayOptions.harMaxTotalBodyBytes || 4194304} onChange={e => setReplayOptions(prev => ({ ...prev, harMaxTotalBodyBytes: Number(e.target.value) }))}>
                      {[1048576, 4194304, 16777216].map(bytes => <option key={bytes} value={bytes}>{bytes / 1048576} MiB</option>)}
                    </select></label>
                    <p>Applies to the next run. Text bodies are redacted; binary, oversized, or incomplete bodies are omitted. Only observed redirect hops and measured timing phases can be retained. Existing evidence limits still apply.</p>
                  </fieldset>}
                  <label><span>Evidence capture</span><select value={replayOptions.capture || 'diagnostic'} onChange={e => setReplayOptions(prev => ({ ...prev, capture: e.target.value as 'diagnostic' | 'errors' | 'script' }))}><option value="diagnostic">Diagnostic — bounded detailed capture</option><option value="errors">Failures only</option><option value="script">Use saved Runtime Settings (custom)</option></select></label>
                  <p>HTTP/SOAP diagnostic capture: up to 100 messages, 64 KiB per message, 4 MiB total, subject to server retention limits. Browser capture uses per-action screenshots. Other protocols retain their native evidence. Debugger sessions and exports can contain unredacted credentials and values.</p>
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
                    <input
                      aria-label="Run identity"
                      aria-invalid={Boolean(runIdError)}
                      aria-describedby={runIdError ? 'debug-run-id-error' : undefined}
                      value={replayOptions.runId}
                      placeholder="debug-<session id>"
                      onChange={e => setReplayOptions(prev => ({ ...prev, runId: e.target.value }))}
                    />
                    {runIdError && <small id="debug-run-id-error" role="alert" style={{ color: 'var(--danger-text)', display: 'block', marginTop: '4px' }}>{runIdError}</small>}
                  </label>
                  <div className="full-width replay-variables-field">
                    <span>Session Variables JSON</span>
                    <div className="replay-variables-editor" style={{ border: '1px solid var(--border)', borderRadius: '8px', overflow: 'hidden', marginTop: '6px' }}>
                      <textarea
                        aria-label="Session Variables JSON"
                        aria-invalid={Boolean(variablesError)}
                        aria-describedby={variablesError ? 'debug-variables-error' : undefined}
                        value={replayOptions.variablesText}
                        onChange={event => setReplayOptions(prev => ({ ...prev, variablesText: event.target.value }))}
                        rows={5}
                      />
                    </div>
                    {variablesError && <small id="debug-variables-error" role="alert" style={{ color: 'var(--danger-text)', display: 'block', marginTop: '4px' }}>{variablesError}</small>}
                  </div>
                  <LiveTableSandboxEditor value={replayOptions.liveTableSeedsText} usage={dataUsage} onChange={liveTableSeedsText => setReplayOptions(prev => ({ ...prev, liveTableSeedsText }))} />
                </fieldset>
              </div>
            )}

            <div className="debugger-body">
              <div className="debug-session-row">
              <div className="debug-session-tools debug-selection-status">
                {(() => {
                  const selectionStatusText = selectedReplayEvent
                    ? `Inspecting ${eventLocation(selectedReplayEvent)} · ${selectedReplayEvent.action_name}`
                    : currentDebugAction
                      ? `Current: ${currentDebugAction.action_name}`
                      : 'Select an action to inspect evidence'
                  return <span title={selectionStatusText}>{selectionStatusText}</span>
                })()}
                <label><input type="checkbox" checked={followExecution} onChange={e => { if (e.target.checked) goCurrent(); else onFollowExecution(false) }} />Follow execution</label>
                <button type="button" className="btn btn-sm btn-ghost" disabled={!replaying || !currentDebugAction} onClick={goCurrent}>Go to current action</button>
              </div>
              <div className="debug-session-disclosures">
              <DebugRunHistory key={scriptId || projectId || 'draft'} runs={runHistory} current={replayReport} currentRecord={runRecord} />
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
                      onOpenRequestResponse?.()
                      setDebugDetailCollapsed(false)
                      setExpandedReplayEvent(replayFailingEventIndex)
                    }}
                  >
                    Inspect
                  </button>
                </div>
              )}

              <div className="script-pane-switch" role="group" aria-label="Debug panes">{(['outline', 'editor', 'inspector'] as const).map(pane => <button type="button" key={pane} aria-pressed={narrowPane === pane} onClick={() => { setNarrowPane(pane); if (pane === 'inspector') setDebugDetailCollapsed(false) }}>{pane === 'outline' ? 'Outline' : pane === 'editor' ? 'Editor' : 'Inspector'}</button>)}</div>
              <div data-narrow-pane={narrowPane}
                className={`debugger-shell ${debugDetailCollapsed ? 'debugger-detail-collapsed' : ''} ${isBrowserScript ? 'browser-evidence-workspace' : ''} ${dockCollapsed ? 'debug-dock-collapsed' : ''} ${activeDebuggerResize ? `resizing resizing-${activeDebuggerResize}` : ''}`}
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
                  <DebugBreakpointControls breakpoints={debugBreakpoints} disabled={breakpointsPending} onChange={next => { setBreakpointsPending(true); void onUpdateBreakpoints(next).finally(() => setBreakpointsPending(false)) }} />
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
                            onClick={() => { if (parameter.name) { selectOutlineParameter(parameter.name); setDebuggerTab('variables'); setVariablesSubtab('values'); setNarrowPane('inspector') } }}
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

                <section id="debug-detail-panel" ref={setDebuggerDetailElement} className={`debugger-center${debugDetailCollapsed ? ' collapsed' : ''}`} aria-label="Debug detail">
                  {debugDetailCollapsed ? (
                    <PanelToggle
                      className="debugger-detail-restore ux-panel-rail"
                      side="end"
                      expanded={false}
                      panelName="debug detail panel"
                      collapsedLabel="Debug detail"
                      controls="debug-detail-panel"
                      onToggle={() => setDebugDetailCollapsed(false)}
                    />
                  ) : (
                    <>
                  <div
                    className={`debugger-resizer debugger-resizer-col debugger-resizer-vars ${activeDebuggerResize === 'vars' ? 'active' : ''}`}
                    onKeyDownCapture={e => handleDebuggerResizeKeyDown('vars', e)}
                    onPointerDown={e => startDebuggerDrag('vars', e)}
                    role="separator"
                    tabIndex={0}
                    title="Resize debug panel"
                    aria-label="Resize debug panel"
                    aria-orientation="vertical"
                    aria-valuemax={debuggerVarsMaximumWidth}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.vars.min}
                    aria-valuenow={Math.min(debuggerVarsMaximumWidth, Math.max(DEBUGGER_LAYOUT_LIMITS.vars.min, debuggerVarsActualWidth ?? debuggerVarsWidth))}
                  />
                  <div className="debugger-center-tabs">
                    <TabList className="debugger-center-tablist ux-underline-tabs" role="tablist" aria-label="Debug detail views" onKeyDown={handleTabKey}>
                      <button type="button" role="tab" aria-selected={debuggerTab !== 'run-config'} className={debuggerTab !== 'run-config' ? 'active' : ''} onClick={() => setDebuggerTab('variables')} tabIndex={debuggerTab !== 'run-config' ? 0 : -1}>{replaying ? 'Live Variables' : 'Variables'}</button>
                      <button type="button" role="tab" aria-selected={debuggerTab === 'run-config'} className={debuggerTab === 'run-config' ? 'active' : ''} onClick={() => setDebuggerTab('run-config')} tabIndex={debuggerTab === 'run-config' ? 0 : -1}>Run Configuration</button>
                    </TabList>
                    <PanelToggle
                      className="debugger-pane-close"
                      side="end"
                      expanded={true}
                      panelName="debug detail panel"
                      collapsedLabel="Debug detail"
                      controls="debug-detail-panel"
                      onToggle={() => { setDebugDetailCollapsed(true); setNarrowPane('editor') }}
                    />
                  </div>
                  <div className="debugger-center-body">
                    {debuggerTab === 'run-config' ? (
                      <div className="debugger-run-config-body debug-effective-settings" data-table-pagination="off">
                        <div className="debug-effective-settings-header">
                          <strong>Effective run configuration · {runRecord?.draft ? 'Unsaved draft' : 'Script settings'}</strong>
                        </div>
                        <div className="debug-watch-table-scroll">
                          <table className="debug-effective-settings-table" aria-label="Effective run configuration" data-table-pagination="off">
                            <thead>
                              <tr>
                                <th scope="col">Setting</th>
                                <th scope="col">Effective value</th>
                              </tr>
                            </thead>
                            <tbody>
                              <tr>
                                <th scope="row">Source</th>
                                <td>{runRecord?.draft ? 'Unsaved draft' : 'Script settings'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Protocol</th>
                                <td>{script.protocol || 'http'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Base URL (relative targets)</th>
                                <td>{runRecord?.target || script.config.base_url || 'Action-specific targets'}{absoluteTargetCount > 0 ? ` · ${absoluteTargetCount} action${absoluteTargetCount === 1 ? '' : 's'} use their own absolute URL` : ''}</td>
                              </tr>
                              <tr>
                                <th scope="row">Iterations</th>
                                <td>{runRecord?.iterations ?? 1}</td>
                              </tr>
                              <tr>
                                <th scope="row">Action sequence</th>
                                <td>{(script.config.run_logic?.action_sequence || ['init', 'action', 'teardown']).join(' → ')}</td>
                              </tr>
                              <tr>
                                <th scope="row">Think time</th>
                                <td>{(runRecord ? runRecord.options.ignoreThinkTime : replayOptions.ignoreThinkTime) ? `Ignored for debug (${effectiveThinkTimeLabel(script.config)})` : effectiveThinkTimeLabel(script.config)}</td>
                              </tr>
                              <tr>
                                <th scope="row">Pacing</th>
                                <td>{formatPacingTiming(script.config.pacing) || 'No pacing delay'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Failure policy</th>
                                <td>{runRecord?.failurePolicy || (debugContinuesAfterFailure(replayOptions, script) ? 'Continue on error' : 'Stop on error')}</td>
                              </tr>
                              <tr>
                                <th scope="row">Break on failure</th>
                                <td>{(runRecord ? runRecord.options.breakOnFailure : replayOptions.breakOnFailure) ? 'On' : 'Off'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Max retries</th>
                                <td>{script.config.error_handling?.max_retries ?? 0}{script.config.error_handling?.max_retries ? ` (${script.config.error_handling?.retry_delay_ms ?? 0} ms, ${script.config.error_handling?.retry_backoff || 'fixed'})` : ''}</td>
                              </tr>
                              <tr>
                                <th scope="row">Log level</th>
                                <td>{(runRecord?.options.logLevel || replayOptions.logLevel || script.config.log?.level || 'info').toUpperCase()}</td>
                              </tr>
                              <tr>
                                <th scope="row">Evidence capture</th>
                                <td>{runRecord?.options.capture || replayOptions.capture || 'diagnostic'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Network trace</th>
                                <td>{(runRecord ? runRecord.options.networkTrace : replayOptions.networkTrace) !== false ? 'On' : 'Off'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Redact run evidence</th>
                                <td>{script.config.log?.redact_run_evidence ? 'On' : 'Off (platform secrets always redacted)'}</td>
                              </tr>
                              <tr>
                                <th scope="row">Debugger group</th>
                                <td>{(runRecord?.options.groupName || replayOptions.groupName || '').trim() || `${script.name || 'script'}-debug`}</td>
                              </tr>
                              <tr>
                                <th scope="row">Run identity</th>
                                <td>{(runRecord?.options.runId || replayOptions.runId || '').trim() || 'debug-<session id>'}</td>
                              </tr>
                              {(['http', 'soap', 'java_http'].includes(script.protocol) || script.protocol.startsWith('http/')) && (
                                <>
                                  <tr>
                                    <th scope="row">HTTP version</th>
                                    <td>{script.config.http?.version || 'auto (HTTP/1.1 + HTTP/2)'}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Timeouts</th>
                                    <td>{`Request ${script.config.http?.request_timeout_ms ?? 30000} ms · Connect ${script.config.http?.connect_timeout_ms ?? 10000} ms · TLS ${script.config.http?.tls_timeout_ms ?? 10000} ms`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Connections &amp; redirects</th>
                                    <td>{`Keep-alive ${script.config.http?.keep_alive !== false ? 'on' : 'off'} · Max redirects ${script.config.http?.max_redirects ?? 10} · Max conns/host ${script.config.http?.max_conns_per_host ?? 6}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Cookies &amp; cache</th>
                                    <td>{`Cookies ${script.config.http?.cookies_enabled !== false ? 'enabled' : 'disabled'}${script.config.http?.clear_cookies_per_iteration ? ' (clear each iteration)' : ''} · Cache ${script.config.http?.cache_enabled ? 'enabled' : 'disabled'}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">TLS</th>
                                    <td>{`Min ${script.config.http?.tls?.min_version || 'TLS 1.2'}${script.config.http?.tls?.max_version ? ` · Max ${script.config.http.tls.max_version}` : ''}${script.config.http?.tls?.insecure_skip_verify ? ' · Insecure skip verify' : ''}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Proxy</th>
                                    <td>{`${script.config.http?.proxy_mode || 'direct'}${script.config.http?.proxy?.url ? ` (${script.config.http.proxy.url})` : ''}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">HAR capture</th>
                                    <td>{`Bodies: ${runRecord?.options.harBodies || replayOptions.harBodies || 'none'} · Redirects: ${(runRecord ? runRecord.options.harRedirects : replayOptions.harRedirects) !== false ? 'on' : 'off'} · Timings: ${(runRecord ? runRecord.options.harTimings : replayOptions.harTimings) !== false ? 'on' : 'off'}`}</td>
                                  </tr>
                                </>
                              )}
                              {isBrowserScript && (
                                <>
                                  <tr>
                                    <th scope="row">Browser engine</th>
                                    <td>{`${script.config.browser?.engine || 'chromium'} · ${script.config.browser?.headless !== false ? 'Headless' : 'Headed'}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Viewport / device</th>
                                    <td>{script.config.browser?.device_profile || `${script.config.browser?.viewport?.width ?? 1280}×${script.config.browser?.viewport?.height ?? 720}`}</td>
                                  </tr>
                                  <tr>
                                    <th scope="row">Screenshots</th>
                                    <td>{runRecord?.options.capture === 'errors' ? 'on_error' : runRecord?.options.capture === 'script' ? (script.config.browser?.capture_screenshots || 'on_error') : 'per_action'}</td>
                                  </tr>
                                </>
                              )}
                              <tr>
                                <th scope="row">Inputs</th>
                                <td>{runRecord?.inputs?.length ?? (script.parameters?.length || 0)} parameters · {runRecord?.dataSourceCount ?? (script.data_sources?.length || 0)} data sources. Row selection follows Parameters; session variables override matching inputs.</td>
                              </tr>
                            </tbody>
                          </table>
                        </div>
                        {(() => {
                          const inputRows = runRecord?.inputs || (script.parameters || []).map(p => ({ name: p.name, source: p.data_source || p.type, selection: p.selection || 'default', update: p.update, startRow: p.start_row, seed: p.seed }))
                          if (inputRows.length === 0) return null
                          return (
                            <div className="debug-watch-table-scroll">
                              <table className="debug-effective-settings-table" aria-label="Effective parameter inputs" data-table-pagination="off">
                                <thead>
                                  <tr>
                                    <th scope="col">Parameter</th>
                                    <th scope="col">Source</th>
                                    <th scope="col">Selection</th>
                                    <th scope="col">Update</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {inputRows.map(p => (
                                    <tr key={p.name}>
                                      <th scope="row">{p.name}</th>
                                      <td>{p.source}</td>
                                      <td>{p.selection}{p.startRow != null ? ` · start row ${p.startRow}` : ''}{p.seed != null ? ` · seed ${p.seed}` : ''}</td>
                                      <td>{p.update || 'default update'}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )
                        })()}
                      </div>
                    ) : (
                      <div className="debugger-variables-body" data-table-pagination="off">
                        <TabList className="debugger-variables-subtabs ux-underline-tabs" role="tablist" aria-label="Variables views" onKeyDown={handleTabKey}>
                          <button type="button" role="tab" aria-selected={variablesSubtab === 'values'} className={variablesSubtab === 'values' ? 'active' : ''} onClick={() => setVariablesSubtab('values')} tabIndex={variablesSubtab === 'values' ? 0 : -1}>Current values</button>
                          <button type="button" role="tab" aria-selected={variablesSubtab === 'sources'} className={variablesSubtab === 'sources' ? 'active' : ''} onClick={() => setVariablesSubtab('sources')} tabIndex={variablesSubtab === 'sources' ? 0 : -1}>Parameter sources &amp; refresh cadence</button>
                        </TabList>
                        {variablesSubtab === 'sources' ? (
                          <ParameterDefinitionGuide script={script} />
                        ) : (
                          <>
                            <DebugVariableWatches script={script} replaying={replaying} events={debugEvents} seed={seedVariables} latest={replayVariableInspector} evidenceUnavailable={runtimeVariablesUnavailable} historyIncomplete={Boolean(replaying || replayReport?.dropped_event_count || replayReport?.truncated_event_evidence_count || browserReplaySession?.dropped_event_count || browserReplaySession?.report?.truncated_event_evidence_count)} selected={selectedReplayEventIndex} onSelect={jumpToEvent} snapshot={currentRuntimeSnapshot} currentReason={currentRuntimeReason} mode={runtimeInspectionMode} onModeChange={onRuntimeInspectionMode} completed={Boolean(replayReport?.completed_at)} finalValuesRecorded={Boolean(replayReport?.completed_at && replayReport?.final_variables)} canInspectFrame={canInspectRuntimeFrame} onInspectFrame={frame => { onInspectRuntimeFrame(frame); setNarrowPane('editor') }} />
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
                                      onOpenRequestResponse?.()
                                    }}
                                  >
                                    <span>{item.name}</span>
                                    <strong>{item.action}</strong>
                                    <small>{item.previous ?? 'unset'} {'→'} {item.next}</small>
                                  </button>
                                ))}
                              </div>
                            )}
                          </>
                        )}
                      </div>
                    )}
                  </div>
                    </>
                  )}
                </section>
                {!isBrowserScript && debugDetailHost && createPortal(
                  <div className="debugger-center-body debugger-request-body">
                    {!replayReport ? (
                      <div className="debugger-empty">No debugger session yet. Set breakpoints in the action list (a row's gutter, F9, or Cmd/Ctrl+B), review Debug options, then run one iteration.</div>
                    ) : selectedReplayEvent && selectedReplayEventIndex != null ? (
                      <LazyLoadBoundary
                        title="Request and response inspector could not be loaded."
                        onRetry={() => setInspectorRetryCount(count => count + 1)}
                      >
                      <Suspense fallback={<div className="debugger-empty" role="status">Loading request and response…</div>}>
                      <InspectorComponent
                        event={selectedReplayEvent}
                        index={selectedReplayEventIndex}
                        script={script}
                        tab={inspectorTab}
                        onTabChange={setInspectorTab}
                        replaying={replaying}
                        replayMatchesCurrentScript={replayMatchesCurrentScript}
                        runtimeVariablesUnavailable={runtimeVariablesUnavailable}
                        variableDiff={replayVariableDiffs[selectedReplayEventIndex] || {}}
                        copiedSection={copiedReplaySnapshotSection}
                        onCopySection={handleCopyReplaySnapshotSection}
                        onEditAction={event => {
                          if (canEditAction && !canEditAction(event)) return
                          ;(onEditAction ?? onEditReplayAction)(event)
                        }}
                        onOpenDebugOptions={() => setDebuggerSettingsOpen(true)}
                        addExtractorDefinition={(
                          actionIndex: number,
                          extractor: ScriptExtractorDefinition,
                          repairOrStep?: CorrelationExtractorRepair | Partial<StepPosition> | null,
                          step?: Partial<StepPosition> | null,
                        ) => {
                          const defaultStep: StepPosition = {
                            phase: normalizeDebugPhase(selectedReplayEvent.phase) as 'init' | 'action' | 'teardown',
                            actionIndex: selectedReplayEvent.action_index ?? actionIndex,
                            stepPath: selectedReplayEvent.step_path?.length
                              ? selectedReplayEvent.step_path
                              : typeof selectedReplayEvent.request_index === 'number' && selectedReplayEvent.request_index >= 0
                                ? [`requests[${selectedReplayEvent.request_index}]`]
                                : undefined,
                          }
                          const isRepairObj = Boolean(
                            repairOrStep &&
                            typeof repairOrStep === 'object' &&
                            ('failingActionIndex' in repairOrStep || 'requestLocations' in repairOrStep || 'matchedValue' in repairOrStep || 'replacement' in repairOrStep),
                          )
                          if (isRepairObj) {
                            return effectiveAddExtractor(actionIndex, extractor, repairOrStep, step ?? defaultStep)
                          }
                          const resolvedStep = step ?? (repairOrStep as Partial<StepPosition> | null | undefined) ?? defaultStep
                          return effectiveAddExtractor(actionIndex, extractor, resolvedStep, resolvedStep)
                        }}
                        replaceExtractorDefinition={replaceExtractorDefinition}
                        http3UpgradeScopes={http3UpgradeScopes}
                        onSwitchToHTTP3Upgrade={onSwitchToHTTP3Upgrade}
                        responseActions={responseActions}
                        onViewScreenshot={setReplayScreenshotSelection}
                        onCompareWithRecording={onCompareWithRecording}
                        events={debugEvents}
                        onSelectEvent={selectInspectorEvent}
                      />
                      </Suspense>
                      </LazyLoadBoundary>
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
                      <div className="debugger-empty">
                        Select an action from the list to inspect its request and response.
                        {onCompareWithRecording && <> <button type="button" className="link-button" onClick={onCompareWithRecording}>Compare with a recording</button></>}
                      </div>
                    )}
                  </div>,
                  debugDetailHost,
                )}

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
                    aria-valuemax={debuggerBottomMaximumHeight}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.bottom.min}
                    aria-valuenow={Math.min(debuggerBottomMaximumHeight, Math.max(DEBUGGER_LAYOUT_LIMITS.bottom.min, debuggerBottomHeight))}
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
                                    onOpenRequestResponse?.()
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
                      {activeWorkspace === 'debug-summary' && dockCollapsed ? (
                        <p className="debugger-empty" data-testid="debugger-timeline-summary-active">
                          Transaction waterfall is shown in the Summary workspace above.
                          {onOpenRequestResponse && (
                            <> <button type="button" className="link-button" onClick={onOpenRequestResponse}>Inspect requests</button></>
                          )}
                        </p>
                      ) : (
                        <DebugWaterfallPanel
                          events={debugEvents}
                          selectedEventIndex={selectedReplayEventIndex}
                          onSelectEvent={jumpToEvent}
                          onInspectEvent={jumpToEvent}
                          canRevealInScript={canRevealInScript ?? (event => { const frame = replayFrameForEvent(event); return Boolean(frame && canInspectRuntimeFrame(frame)) })}
                          onRevealInScript={onRevealInScript ?? (event => { const frame = replayFrameForEvent(event); if (frame) { onInspectRuntimeFrame(frame); setNarrowPane('editor') } })}
                          canEditAction={canEditAction}
                          onEditAction={onEditAction}
                        />
                      )}
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
                          {replayBlockers.length > 0 && (
                            <section className="debugger-problem-group" aria-label={`Blocking replay (${replayBlockers.length})`}>
                              <h4>Blocking replay ({replayBlockers.length})</h4>
                              {replayBlockers.map(issue => (
                                <div key={issue.id} className="debugger-problem-row error replay-blocker">
                                  <span>Blocker</span>
                                  <strong>{issue.title}</strong>
                                  {issue.source && <small>{issue.source}</small>}
                                  {issue.description && <small>{issue.description}</small>}
                                  <div className="debugger-problem-actions">
                                    {issue.onSelect && onSelectAdditionalIssue && (
                                      <button type="button" className="btn btn-sm btn-secondary" aria-label={`Select in Author: ${issue.title}`} onClick={() => onSelectAdditionalIssue(issue)}>Select in Author</button>
                                    )}
                                    {/* Each row's controls name its step, so tabbing through them says which one a delete removes. */}
                                    <button type="button" className="btn btn-sm btn-ghost" aria-label={`Delete blocked action: ${issue.title}`} onClick={issue.onDelete}>Delete blocked action</button>
                                  </div>
                                </div>
                              ))}
                            </section>
                          )}
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
                          {additionalIssues
                            .filter(issue => issue.id !== 'json-parse-error' && issue.id !== 'current-error' && !issue.id.startsWith('runtime-warning-') && !replayBlockers.some(blocker => blocker.id === issue.id))
                            .map(issue => {
                              const severityClass = issue.severity === 'blocker' ? 'error' : issue.severity
                              const badgeLabel = issue.severity === 'blocker' ? 'Blocker' : issue.source || issue.severity
                              if (issue.onSelect && onSelectAdditionalIssue) {
                                return (
                                  <button
                                    key={issue.id}
                                    type="button"
                                    className={`debugger-problem-row ${severityClass} action-problem`}
                                    onClick={() => onSelectAdditionalIssue(issue)}
                                  >
                                    <span>{badgeLabel}</span>
                                    <strong>{issue.title}</strong>
                                    {issue.description && <small>{issue.description}</small>}
                                  </button>
                                )
                              }
                              return (
                                <div key={issue.id} className={`debugger-problem-row ${severityClass}`}>
                                  <span>{badgeLabel}</span>
                                  <strong>{issue.title}</strong>
                                  {issue.description && <small>{issue.description}</small>}
                                </div>
                              )
                            })}
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
  secret: 'Project secret; resolved when the step runs. Debugger evidence can contain the resolved value.',
  generated: 'Generated when the step runs',
}

/** A template with the session's current values filled in, each kind marked. */
export function PreviewText({ segments, highlightText }: { segments: PreviewSegment[]; highlightText?: boolean }) {
  return <>{segments.map((segment, index) => segment.kind === 'text'
    ? <span key={index}>{highlightText ? <DebugLogHighlightedText text={segment.text} /> : segment.text}</span>
    : <span key={index} className={`debug-preview-${segment.kind}`} title={PREVIEW_TITLES[segment.kind]}>{segment.text}</span>)}</>
}

function isSensitivePreviewKey(name: string): boolean {
  const trimmed = name.trim()
  if (!trimmed || isCredentialLocatorName(trimmed)) return false
  return (
    isSensitiveCredentialPreviewHeader(trimmed) ||
    isSensitiveCredentialPreviewName(trimmed) ||
    isPasswordLikeFieldName(trimmed)
  )
}

function maskSensitivePreviewText(
  rawText: string,
  values: Readonly<Record<string, string>> | null,
  runId: string | undefined,
  reveal: boolean,
): { segments: PreviewSegment[]; hasMasked: boolean } {
  let hasMasked = false
  const sensitiveTemplateExprs = new Set<string>()
  let workingText = rawText.replace(
    /([?&]|^)([A-Za-z0-9_.-]+)=([^&#\s]+)/g,
    (full, prefix: string, key: string, val: string) => {
      if (!val || !isSensitivePreviewKey(key)) return full
      const singleTemplate = /^\{\{\s*([^{}]+?)\s*\}\}$/.exec(val)
      if (singleTemplate) {
        const expr = singleTemplate[1].trim()
        if (values?.[expr] !== undefined) {
          sensitiveTemplateExprs.add(expr)
          hasMasked = true
        }
        return full
      }
      hasMasked = true
      return reveal ? full : `${prefix}${key}=${MASKED_VALUE}`
    },
  )
  workingText = workingText.replace(
    /("([A-Za-z0-9_.-]+)"\s*:\s*")([^"]*)(")/g,
    (full, open: string, key: string, val: string, close: string) => {
      if (!val || !isSensitivePreviewKey(key)) return full
      const singleTemplate = /^\{\{\s*([^{}]+?)\s*\}\}$/.exec(val)
      if (singleTemplate) {
        const expr = singleTemplate[1].trim()
        if (values?.[expr] !== undefined) {
          sensitiveTemplateExprs.add(expr)
          hasMasked = true
        }
        return full
      }
      hasMasked = true
      return reveal ? full : `${open}${MASKED_VALUE}${close}`
    },
  )
  const rawSegments = previewTemplate(rawText, values, runId)
  for (const match of rawText.matchAll(/\{\{\s*([^{}]+?)\s*\}\}/g)) {
    const expr = match[1]
    if (expr === 'run.id' || expr.startsWith('secret.') || expr.startsWith('fn.') || expr.startsWith('fake.')) continue
    if (values?.[expr] === undefined) continue
    const leaf = expr.split('.').pop() || expr
    if (isSensitivePreviewKey(leaf) || isSensitivePreviewKey(expr)) {
      sensitiveTemplateExprs.add(expr)
      hasMasked = true
    }
  }
  if (reveal || !hasMasked) {
    return { segments: rawSegments, hasMasked }
  }
  const maskedValues: Record<string, string> | null = values
    ? Object.fromEntries(
        Object.entries(values).map(([k, v]) => {
          const leaf = k.split('.').pop() || k
          const shouldMask = sensitiveTemplateExprs.has(k) || isSensitivePreviewKey(leaf) || isSensitivePreviewKey(k)
          return [k, shouldMask ? MASKED_VALUE : v]
        }),
      )
    : null
  return {
    segments: previewTemplate(workingText, maskedValues, runId),
    hasMasked,
  }
}

/**
 * The request a paused session runs next, as VuGen's next-step view shows it:
 * the action's target, headers and body with the values this session holds
 * now. Values an earlier step has yet to provide stay as written.
 */
export function DebugNextRequest({ script, action, values, runId }: { script: ScriptDefinition; action: ScriptDebugAction; values: Readonly<Record<string, string>> | null; runId?: string }) {
  const actionIdentity = `${action.event_index ?? ''}:${action.phase}:${action.action_index}:${action.request_index ?? ''}:${(action.step_path || []).join('/')}`
  const [prevActionIdentity, setPrevActionIdentity] = useState(actionIdentity)
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [revealSensitive, setRevealSensitive] = useState(false)
  const [headerRevealOverrides, setHeaderRevealOverrides] = useState<Record<string, boolean>>({})
  if (prevActionIdentity !== actionIdentity) {
    setPrevActionIdentity(actionIdentity)
    setCopiedKey(null)
    setRevealSensitive(false)
    setHeaderRevealOverrides({})
  }
  const definition = debugNextAction(script, action)
  if (!definition) return null
  const { target, headers, body } = debugNextRequestParts(definition)
  if (!target && !headers.length && !body) return null
  const shownBody = body && body.length > PREVIEW_BODY_LIMIT ? `${body.slice(0, PREVIEW_BODY_LIMIT)}…` : body
  const targetPreview = target ? maskSensitivePreviewText(target, values, runId, revealSensitive) : null
  const bodyPreview = shownBody ? maskSensitivePreviewText(shownBody, values, runId, revealSensitive) : null
  const hasMaskedTargetOrBody = Boolean(targetPreview?.hasMasked || bodyPreview?.hasMasked)
  const resolvedHeaderRecord: Record<string, string> = {}
  const sensitiveHeaderNames: string[] = []
  for (const [name, value] of headers) {
    if (!name) continue
    const headerPreview = maskSensitivePreviewText(value, values, runId, true)
    const resolved = headerPreview.segments.map(segment => segment.text).join('')
    const isPureUnresolvedTemplate = /^\{\{\s*[^{}]+?\s*\}\}$/.test(value.trim()) && !headerPreview.segments.some(seg => seg.kind === 'value')
    const hasSecretReference = headerPreview.segments.some(seg => seg.kind === 'secret')
    const isSensitiveHeader = Boolean(resolved && !isPureUnresolvedTemplate && !hasSecretReference && (isSensitivePreviewKey(name) || headerPreview.hasMasked))
    if (isSensitiveHeader && !sensitiveHeaderNames.includes(name)) {
      sensitiveHeaderNames.push(name)
    }
    const showHeaderValue = (headerRevealOverrides[name] ?? revealSensitive) || !isSensitiveHeader
    const displayedValue = showHeaderValue ? resolved : MASKED_VALUE
    resolvedHeaderRecord[name] = resolvedHeaderRecord[name] !== undefined
      ? `${resolvedHeaderRecord[name]}, ${displayedValue}`
      : displayedValue
  }
  const toggleHeaderReveal = (name: string) => {
    setHeaderRevealOverrides(prev => ({
      ...prev,
      [name]: !(prev[name] ?? revealSensitive),
    }))
  }
  const handleCopy = (key: string, val: string) => {
    setCopiedKey(key)
    void navigator.clipboard?.writeText(val)
  }
  return (
    <section className="debugger-next-request" aria-label="Next request">
      <div className="debugger-next-request-head" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
        <h4>Next request <small>{values ? 'with this session\u2019s current values' : 'this session\u2019s values are not available, so references show as written'}</small></h4>
        {hasMaskedTargetOrBody && (
          <button
            type="button"
            className="btn btn-sm btn-ghost inspector-header-reveal-btn"
            onClick={() => {
              setRevealSensitive(prev => !prev)
              setHeaderRevealOverrides({})
            }}
            aria-label={revealSensitive ? 'Hide sensitive request values' : 'Show sensitive request values'}
          >
            {revealSensitive ? 'Hide' : 'Show value'}
          </button>
        )}
      </div>
      {target && targetPreview && <p className="debugger-next-request-line"><code className="debug-log-token token-method">{definition.method || action.method || action.action_type}</code> <PreviewText segments={targetPreview.segments} highlightText /></p>}
      {headers.length > 0 && (
        <div className="debugger-next-request-headers">
          <InspectorHeaderTable
            direction="request"
            headers={resolvedHeaderRecord}
            sources={{ authored: { action: definition, http: script.config?.http }, view: 'scripted' }}
            label="Next request headers"
            copyKeyPrefix="next-req-hdr"
            copiedKey={copiedKey}
            onCopy={handleCopy}
          />
          {sensitiveHeaderNames.map(name => {
            const isRevealed = headerRevealOverrides[name] ?? revealSensitive
            return (
              <button
                key={name}
                type="button"
                className="btn btn-sm btn-ghost inspector-header-reveal-btn"
                aria-label={isRevealed ? `Hide ${name} value` : `Show ${name} value`}
                onClick={() => toggleHeaderReveal(name)}
              >
                {isRevealed ? 'Hide' : 'Show value'}
              </button>
            )
          })}
        </div>
      )}
      {shownBody && bodyPreview && <pre className="debugger-next-request-body"><PreviewText segments={bodyPreview.segments} highlightText /></pre>}
    </section>
  )
}
