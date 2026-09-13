import { executionLabel, eventLocation, type DebugRunRecord } from './debuggerExperience'
import { DebugProfiles, DebugVariableWatches, DebugTimeline, DebugRunHistory, DebugBreakpointControls } from './DebuggerSessionTools'
import { debugOverrideCount, sessionVariablesError, handleOutlineKeys } from './scriptSidebarModel'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import { DebugParameterInspector } from './DebugParameterInspector'
import { ParameterDefinitionGuide } from './ParameterDefinitionGuide'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import type * as React from 'react'
import { memo, useMemo, useState, useEffect } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
  ScriptDebugBreakpoint,
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
  formatConditionEvidence,
  formatDebugLeafSuffix,
  formatReplayDiagnosisCategory,
  highlightDebugLogRaw,
  hasStructuredReplaySnapshot,
  humanizeStepSegment,
} from './scriptEditorReplayModel'
import {
  DEBUGGER_LAYOUT_LIMITS,
  buildDebugRailRenderRows,
  type DebugRailRenderRow,
  debugStepPathKey,
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

export interface ScriptReplayDebuggerViewProps {
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
  debugBreakpointKeys: Set<string>
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
  parameterReferenceCounts: Map<string, number>
  problemCount: number
  projectId?: string
  replayActivity: ReplayActivityItem[]
  replayEventRefs: React.MutableRefObject<Record<number, HTMLDivElement | null>>
  replayFailingEvent: BrowserReplayEvent | null
  replayFailingEventIndex: number
  replaying: boolean
  replayLatestActivity?: ReplayActivityItem
  replayOptions: ReplayOptionsState
  replayReport: BrowserReplayReport | null
  replayEditableActionsKey: string | null
  currentEditableActionsKey: string | null
  replayStatusLabel: 'Stopping' | 'Paused' | 'Running' | 'Success' | 'Failed' | 'Stopped'
  replayVariableDiffs: Array<Record<string, { previous?: string; next: string }>>
  replayVariableInspector: Record<string, string> | null
  replayVariableTimeline: ReplayVariableTimelineEntry[]
  saving: boolean
  script: ScriptDefinition
  scriptId?: string | null
  scriptLevelIssues: AuthoringIssue[]
  scriptRecordingStatus: ScriptRecordingStatus | null
  scriptRecordingStatusState: RecordingStatusState
  retryScriptRecordingStatus: () => void
  selectedActionKey?: string
  selectedOutlineParameterName: string | null
  selectOutlineParameter: (parameterName: string) => void
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
  debugBreakpointKeys,
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
  onActivateDebug,
  onEditParameterDefinition,
  onSelectProblemAction,
  parameterReferenceCounts,
  problemCount,
  projectId,
  replayActivity,
  replayEventRefs,
  replayFailingEvent,
  replayFailingEventIndex,
  replaying,
  replayLatestActivity,
  replayOptions,
  replayReport,
  replayEditableActionsKey,
  currentEditableActionsKey,
  replayStatusLabel,
  replayVariableDiffs,
  replayVariableInspector,
  replayVariableTimeline,
  saving,
  script,
  scriptId,
  scriptLevelIssues,
  scriptRecordingStatus,
  scriptRecordingStatusState,
  retryScriptRecordingStatus,
  selectedActionKey,
  selectedOutlineParameterName,
  selectOutlineParameter,
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
  const {
    activeDebuggerResize,
    currentDebugAction,
    currentDebugActionKey,
    currentDebugAncestorKeys,
    currentDebugStepKey,
    debugActionListCollapsed,
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
    debugParameterListCollapsed,
    debugRailFailuresOnly,
    debugRailFilter,
    debugRailFilterText,
    debugRawAutoScroll,
    debugRawLevel,
    debugRawOutputRef,
    debugRawOutputText,
    debugRawSearch,
    debugRowPositionByKey,
    effectiveExpandedDebugSequences,
    eventIndexByLocation,
    eventsByStepPath,
    filterForcedDebugSequences,
    handleDebuggerResizeKeyDown,
    selectedReplayEvent,
    selectedReplayEventIndex,
    setDebugActionListCollapsed,
    setDebugDetailCollapsed,
    setDebuggerBottomTab,
    setDebuggerSettingsOpen,
    setDebuggerTab,
    setDebugParameterListCollapsed,
    setDebugRailFailuresOnly,
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
  const optionsError = sessionVariablesError(replayOptions.variablesText)
  const [narrowPane, setNarrowPane] = useState<'outline' | 'editor' | 'inspector'>('editor')
  const [dockCollapsed, setDockCollapsed] = useState(true)
  const [rawLogs, setRawLogs] = useState(false)
  const [breakpointsPending, setBreakpointsPending] = useState(false)
  const disabledBreakpointKeys = new Set(debugBreakpoints.filter(bp => bp.disabled).map(bp => bp.step_path?.length ? debugStepPathKey(bp.phase, bp.action_index, bp.step_path) : debugBreakpointKey(bp.phase, bp.action_index, bp.request_index)))
  const runtimeFailures = debugEvents.map((event, index) => ({ event, index })).filter(({ event }) => !event.success)
  const statusText = executionLabel(replaying, browserReplaySession, replayReport)
  const jumpToEvent = (index: number) => { setExpandedReplayEvent(index); setDebuggerTab('request'); setDebugDetailCollapsed(false); setNarrowPane('editor') }
  const goCurrent = () => { const failureIndex = isReplayPaused && browserReplaySession?.current_event?.success === false ? debugEvents.findIndex(e => e.event_id != null && e.event_id === browserReplaySession.current_event?.event_id) : -1; setExpandedReplayEvent(failureIndex >= 0 ? failureIndex : null); onFollowExecution(true); setDebugRailFilter(''); setDebugRailFailuresOnly(false); setDebuggerTab('request'); setNarrowPane('editor'); setDebugDetailCollapsed(false) }
  const selectedItem = useMemo(() => buildDebugRailRenderRows(debugActionRows, effectiveExpandedDebugSequences).find(item => item.key === selectedActionKey), [debugActionRows, effectiveExpandedDebugSequences, selectedActionKey])
  const selectedRow = controller.selectedDebugRow || selectedItem?.row

  return (
          <div
            className={`config-panel replay-panel script-ide-debugger debugger-dialog open ${replayReport ? 'has-report' : ''}`}
            role="region"
            aria-label="Script Debugger"
          >
            <div className="debug-options-toolbar">
              <button type="button" className="btn btn-sm btn-secondary" ref={controller.debuggerSettingsBtnRef} aria-expanded={debuggerSettingsOpen} aria-controls="debug-session-options" onClick={() => setDebuggerSettingsOpen(value => !value)}>Debug options ({debugOverrideCount(replayOptions)})</button>
              <span>1 virtual user · {replayOptions.ignoreThinkTime ? 'Think time ignored' : 'Script think time'} · {replayOptions.logLevel} logging{replayOptions.startPaused ? ' · Start paused' : ''}</span>
              <button type="button" className="btn btn-sm btn-secondary" disabled={!replayReport} title="Download Debug JSON" onClick={() => void onExportSession()}>Export session</button>
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
                    <span>Log Level</span>
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
                  <label className="checkbox-label replay-checkbox" title="Pause the debugger automatically whenever an action fails, so you can inspect it before continuing.">
                    <input
                      type="checkbox"
                      checked={replayOptions.breakOnFailure}
                      onChange={e => void setBreakOnFailure(e.target.checked)}
                    />
                    <span>Break on failure</span>
                  </label>
                  <div className="full-width replay-variables-field">
                    <span>Session Variables JSON</span>
                    <div className="replay-variables-editor" style={{ border: '1px solid var(--border)', borderRadius: '8px', overflow: 'hidden', marginTop: '6px' }}>
                      <textarea aria-label="Session Variables JSON" aria-invalid={Boolean(optionsError)} value={replayOptions.variablesText} onChange={event => setReplayOptions(prev => ({ ...prev, variablesText: event.target.value }))} rows={5} />
                    </div>
                  </div>
                </fieldset>
              </div>
            )}

            <div className="debugger-body">
              {isBrowserScript && selectedOutlineParameterName && <DebugParameterInspector key={`${selectedOutlineParameterName}:${selectedReplayEventIndex}`} script={script} name={selectedOutlineParameterName} values={replayVariableInspector} event={selectedReplayEvent} matchesDraft={replayMatchesCurrentScript} onEdit={onEditParameterDefinition} onFindUsages={() => { selectOutlineParameter(selectedOutlineParameterName); setNarrowPane('editor') }} />}
              {isBrowserScript && <details className="debugger-evidence-hint"><summary>About debug evidence</summary><p>Recording verification checks a conversion draft. Debugging here checks the current saved or edited script; changes require a new debug run.</p></details>}
              <div className="debug-session-disclosures">
              <details className="debug-effective-settings"><summary>Effective run configuration · {runRecord?.draft ? 'Unsaved draft' : 'Script settings'}</summary><dl><dt>Target</dt><dd>{runRecord?.target || script.config.base_url || 'Action-specific targets'}</dd><dt>Evidence capture</dt><dd>{runRecord?.options.capture || replayOptions.capture || 'diagnostic'}</dd><dt>Failure policy</dt><dd>{runRecord?.failurePolicy || (script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error')}</dd><dt>Inputs</dt><dd>{runRecord?.inputs?.length ?? (script.parameters?.length || 0)} parameters · {runRecord?.dataSourceCount ?? (script.data_sources?.length || 0)} data sources. Row selection follows Parameter Data; session variables override matching inputs.</dd></dl><ul>{(runRecord?.inputs || (script.parameters || []).map(p => ({ name: p.name, source: p.data_source || p.type, selection: p.selection || 'default', update: p.update, startRow: p.start_row, seed: p.seed }))).map(p => <li key={p.name}>{p.name}: {p.source} · {p.selection} · {p.update || 'default update'}{p.startRow != null ? ` · start row ${p.startRow}` : ''}{p.seed != null ? ` · seed ${p.seed}` : ''}</li>)}</ul></details>
              {isBrowserScript && <details className="debug-effective-settings"><summary>Variable watches</summary><DebugVariableWatches events={debugEvents} seed={seedVariables} latest={replayVariableInspector} selected={selectedReplayEventIndex} onSelect={jumpToEvent} /></details>}
              <DebugRunHistory key={scriptId || projectId || 'draft'} runs={runHistory} current={replayReport} currentRecord={runRecord} />
              <DebugBreakpointControls breakpoints={debugBreakpoints} disabled={breakpointsPending} onChange={next => { setBreakpointsPending(true); void onUpdateBreakpoints(next).finally(() => setBreakpointsPending(false)) }} />
              </div>
              {(replaying || replayReport) && (
                <div className={`debugger-live-strip ${debuggerStatusState}`}>
                  <span className="replay-live-badge">{replaying ? (isReplayPaused ? 'Paused' : browserReplaySession?.status === 'stopped' ? 'Stopping' : 'Running') : replayReport?.stopped ? 'Stopped' : replayReport?.success ? 'Passed' : 'Failed'}</span>
                  <div className="debugger-live-strip-text">
                    <strong role="status" aria-live="polite">{statusText}</strong>
                    {isReplayPaused && runtimeFailures.length > 0 && <span>Continue follows the run’s error policy; stop-on-error ends this iteration after failure.</span>}
                  </div>
                  <small>{replayReport?.events.length || 0} recorded executions · {replayReport?.events.filter(event => !event.success).length || 0} failures</small>
                  <span className="debugger-live-strip-time">{formatReplayTime(replayLatestActivity?.timestamp || replayReport?.completed_at || replayReport?.started_at)}</span>
                </div>
              )}
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

              <div className="debug-session-tools debug-selection-status">
                <span>{selectedReplayEvent ? `Inspecting ${eventLocation(selectedReplayEvent)} · ${selectedReplayEvent.action_name}` : currentDebugAction ? `Current: ${currentDebugAction.action_name}` : 'Select an action to inspect evidence'}</span>
                <label><input type="checkbox" checked={followExecution} onChange={e => { if (e.target.checked) goCurrent(); else onFollowExecution(false) }} />Follow execution</label>
                <button type="button" disabled={!replaying || !currentDebugAction} onClick={goCurrent}>Go to current action</button>
                <button type="button" title="Runs prerequisites; during a paused run, stops at the next matching execution" disabled={!selectedRow || (replaying && !isReplayPaused)} onClick={() => { if (selectedRow) void onRunTo({ phase: selectedRow.phase, action_index: selectedRow.actionIndex, ...(selectedReplayEvent?.step_path?.length ? { step_path: selectedReplayEvent.step_path } : selectedReplayEvent?.request_index != null ? { request_index: selectedReplayEvent.request_index } : selectedItem?.kind === 'leaf' ? { step_path: selectedItem.stepPath } : {}) }) }}>Run to selected action</button>
              </div>
              <div className="script-pane-switch" role="group" aria-label="Debug panes">{(isBrowserScript ? ['outline', 'editor'] as const : ['outline', 'editor', 'inspector'] as const).map(pane => <button type="button" key={pane} aria-pressed={narrowPane === pane} onClick={() => { setNarrowPane(pane); if (pane === 'inspector') setDebugDetailCollapsed(false) }}>{pane === 'outline' ? 'Outline' : pane === 'editor' ? 'Editor' : 'Inspector'}</button>)}</div>
              <div data-narrow-pane={narrowPane}
                className={`debugger-shell ${debugDetailCollapsed || isBrowserScript || debugDetailHost ? 'debugger-detail-collapsed' : ''} ${isBrowserScript ? 'browser-evidence-workspace' : ''} ${dockCollapsed ? 'debug-dock-collapsed' : ''} ${activeDebuggerResize ? `resizing resizing-${activeDebuggerResize}` : ''}`}
                style={{
                  '--rail-w': `${debuggerRailWidth}px`,
                  '--vars-w': `${debuggerVarsWidth}px`,
                  '--bottom-h': `${debuggerBottomHeight}px`,
                } as React.CSSProperties}
              >
                <aside className="debugger-rail" aria-label="Actions">
                  <section className="debugger-rail-section">
                    <div className="debugger-rail-header">
                      <div className="debugger-rail-header-top">
                        <button
                          type="button"
                          className="debugger-rail-section-toggle"
                          aria-expanded={!debugActionListCollapsed}
                          onClick={() => setDebugActionListCollapsed(collapsed => !collapsed)}
                        >
                          <span aria-hidden="true" className="outline-section-chevron">{debugActionListCollapsed ? '▸' : '▾'}</span>
                          <span>Action List</span>
                        </button>
                        <div className="debugger-rail-header-meta">
                          <small>{breakpointSummary}</small>
                          {debugBreakpoints.length > 0 && (
                            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void clearDebugBreakpoints()}>Clear</button>
                          )}
                        </div>
                      </div>
                      {!debugActionListCollapsed && (
                        <div className="debugger-rail-filter">
                          <input value={debugRailFilter} onChange={e => setDebugRailFilter(e.target.value)} placeholder="Filter actions" aria-label="Filter actions" />
                          <label className="checkbox-label replay-checkbox">
                            <input type="checkbox" checked={debugRailFailuresOnly} disabled={!debugEvents.length} onChange={e => setDebugRailFailuresOnly(e.target.checked)} />
                            <span>Failures</span>
                          </label>
                        </div>
                      )}
                    </div>
                    {!debugActionListCollapsed && (
                      debugActionRows.length === 0 ? (
                        <div className="debugger-rail-empty">Add actions to the script to debug.</div>
                      ) : (
                        <div
                          className="debugger-rail-list"
                          role="region"
                          aria-label="Debugger actions"
                          tabIndex={0}
                          onKeyDownCapture={handleOutlineKeys}
                        >
                          {(() => {
                            const renderRows = buildDebugRailRenderRows(debugActionRows, effectiveExpandedDebugSequences)
                            const conditionEvidenceMap = browserReplaySession?.condition_evidence || {}
                            const conditionEvidenceFor = (p: string, ai: number, sp: string[]) => conditionEvidenceMap[`${p}#${ai}#${sp.join('/')}`]
                            const matchesFilter = (text: string) => !debugRailFilterText || text.toLowerCase().includes(debugRailFilterText)
                            const indentStyle = (depth: number) => depth > 0 ? ({ paddingLeft: `${depth * 14}px` } as React.CSSProperties) : undefined
                            const rollupByPath = new Map<string, { count: number; failed: boolean }>()
                            const rollupKey = (phase: string, actionIndex: number, stepPath: string[]) => `${phase}#${actionIndex}#${stepPath.join('/')}`
                            for (const event of debugEvents) {
                              if (event.action_index === undefined) continue
                              const eventPath = event.step_path || []
                              for (let depth = 0; depth <= eventPath.length; depth += 1) {
                                const key = rollupKey(event.phase, event.action_index, eventPath.slice(0, depth))
                                const current = rollupByPath.get(key) || { count: 0, failed: false }
                                current.count += 1
                                current.failed ||= !event.success
                                rollupByPath.set(key, current)
                              }
                            }
                            const childBreakpointsByAction = new Map<string, number>()
                            for (const breakpoint of debugBreakpoints) {
                              if (breakpoint.action_index === undefined) continue
                              if (breakpoint.request_index == null && (!breakpoint.step_path || breakpoint.step_path.length === 0)) continue
                              const key = `${breakpoint.phase}#${breakpoint.action_index}`
                              childBreakpointsByAction.set(key, (childBreakpointsByAction.get(key) || 0) + 1)
                            }
                            const out: React.ReactNode[] = []
                            for (const item of renderRows) {
                              const { phase, actionIndex, action } = item.row
                              if (item.kind === 'container') {
                                const containerKey = item.key
                                const isTopLevel = item.stepPath.length === 0
                                const subtreeRollup = rollupByPath.get(rollupKey(phase, actionIndex, item.stepPath))
                                const anyFail = Boolean(subtreeRollup?.failed)
                                const isCurrent = currentDebugAncestorKeys.has(containerKey)
                                const rollup = anyFail ? 'fail' : isCurrent ? 'current' : subtreeRollup?.count ? 'pass' : 'pending'
                                const wholeActive = isTopLevel && debugBreakpointKeys.has(debugBreakpointKey(phase, actionIndex))
                                const childBpCount = isTopLevel ? childBreakpointsByAction.get(`${phase}#${actionIndex}`) || 0 : 0
                                const gutterState = wholeActive ? 'active' : childBpCount > 0 ? 'partial' : ''
								const typeLabel = item.containerType === 'http_sequence' ? 'SEQ' : item.containerType === 'control_if' ? 'IF' : item.containerType === 'control_switch' ? 'SWITCH' : 'LOOP'
                                const unitWord = item.containerType === 'http_sequence' ? 'request' : 'step'
								const controlEvidence = item.containerType === 'control_if' || item.containerType === 'control_switch' || item.containerType === 'control_loop'
                                  ? conditionEvidenceFor(phase, actionIndex, item.stepPath)
                                  : undefined
								const branchBadge = item.containerType === 'control_if' && controlEvidence
								  ? (controlEvidence.branch === 'then' ? 'THEN' : 'ELSE')
								  : item.containerType === 'control_switch' && controlEvidence?.branch
									? controlEvidence.branch.toUpperCase()
									: null
                                if (isTopLevel) {
                                  const parentVisible = debugRailFailuresOnly
                                    ? anyFail
                                    : !debugRailFilterText || matchesFilter(`${action.name || ''} ${action.target || ''}`) || filterForcedDebugSequences.has(containerKey)
                                  if (!parentVisible) continue
                                }
                                out.push(
                                  <div key={containerKey} className={`debugger-action-row container ${rollup} ${isCurrent ? 'current' : ''}`} style={indentStyle(item.depth)}>
                                    {isTopLevel ? (
                                      <button
                                        type="button"
                                        className={`debugger-gutter ${gutterState}`}
                                        title={wholeActive ? `Remove breakpoint (whole ${typeLabel === 'SEQ' ? 'sequence' : 'block'})` : childBpCount > 0 ? `${childBpCount} sub-step breakpoint${childBpCount === 1 ? '' : 's'} — click to break before this block` : 'Break before this block'}
                                        aria-label={`${wholeActive ? 'Remove' : 'Add'} breakpoint on ${action.name || `action ${actionIndex + 1}`}`}
                                        onClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                                      >
                                        <span className="debugger-breakpoint-dot" />
                                      </button>
                                    ) : (
                                      <span className="debugger-gutter debugger-gutter-spacer" aria-hidden="true" />
                                    )}
                                    <button
                                      type="button"
                                      className="debugger-action-row-main"
                                      aria-expanded={item.expanded}
                                      onClick={() => toggleDebugSequenceExpanded(containerKey)}
                                    >
                                      <span className="debugger-action-row-head">
                                        <span className="replay-event-phase">
                                          <span aria-hidden="true" className="outline-section-chevron">{item.expanded ? '▾' : '▸'}</span>
                                          {isTopLevel ? `${formatDebugPhaseLabel(phase)} ${actionIndex + 1}` : typeLabel}
                                        </span>
                                        <span className={`debugger-action-status ${rollup}`} aria-hidden="true" />
                                      </span>
                                      <strong>{item.name}</strong>
                                      {branchBadge && (
										<span className={`control-branch-badge ${controlEvidence?.result ? 'taken' : 'skipped'}`} title={`Condition was ${controlEvidence?.result ? 'true' : 'false'} → ${branchBadge.toLowerCase()} branch`}>{branchBadge}</span>
                                      )}
                                      <small>{typeLabel} · {item.leafCount} {unitWord}{item.leafCount === 1 ? '' : 's'}{childBpCount > 0 ? ` · ${childBpCount} BP` : ''}{controlEvidence ? ` · ${formatConditionEvidence(controlEvidence)}` : ''}</small>
                                    </button>
                                  </div>
                                )
                                if (item.expanded && isTopLevel && item.containerType === 'http_sequence') {
                                  const allChildBreakpoints = item.leafCount > 0 && childBpCount >= item.leafCount
                                  out.push(
                                    <div key={`${containerKey}:controls`} className="debugger-sequence-controls">
                                      <small>{childBpCount} of {item.leafCount} sub-request breakpoint{item.leafCount === 1 ? '' : 's'}</small>
                                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => void setSequenceChildBreakpoints(phase, actionIndex, item.leafCount, !allChildBreakpoints)}>
                                        {allChildBreakpoints ? 'Clear all' : 'Break all'}
                                      </button>
                                    </div>
                                  )
                                }
                                continue
                              }
                              if (item.kind === 'leaf') {
                                const isHttpChild = item.requestIndex != null
                                const stepKey = debugStepPathKey(phase, actionIndex, item.stepPath)
                                const iterationIndices = isHttpChild ? [] : (eventsByStepPath.get(stepKey) ?? [])
                                const stepBpKey = isHttpChild ? item.key : stepKey
                                const stepBpActive = debugBreakpointKeys.has(stepBpKey)
                                const headLabelBase = isHttpChild ? `Request ${(item.requestIndex ?? 0) + 1}` : humanizeStepSegment(item.stepPath[item.stepPath.length - 1] || '')
                                // A control-flow step that ran more than once becomes an expandable
                                // iteration group: one row per loop pass.
                                if (iterationIndices.length > 1) {
                                  const groupFail = iterationIndices.some(i => !debugEvents[i].success)
                                  const isCurrentGroup = currentDebugStepKey != null && currentDebugStepKey === stepKey
                                  const groupStatus = groupFail ? 'fail' : isCurrentGroup ? 'current' : 'pass'
                                  if (debugRailFailuresOnly && !groupFail) continue
                                  if (!debugRailFailuresOnly && !matchesFilter(`${item.name} ${item.target}`)) continue
                                  const groupExpanded = effectiveExpandedDebugSequences.has(stepKey)
                                  out.push(
                                    <div key={stepKey} className={`debugger-action-row child ${groupStatus} ${isCurrentGroup ? 'current' : ''}`} style={indentStyle(item.depth)}>
                                      <button
                                        type="button"
                                        className={`debugger-gutter ${stepBpActive ? 'active' : ''}`}
                                        title={stepBpActive ? 'Remove sub-step breakpoint' : 'Add sub-step breakpoint (fires every iteration)'}
                                        aria-label={`${stepBpActive ? 'Remove' : 'Add'} sub-step breakpoint on ${item.name}`}
                                        onClick={() => void toggleStepPathBreakpoint(phase, actionIndex, item.stepPath)}
                                      >
                                        <span className="debugger-breakpoint-dot" />
                                      </button>
                                      <button type="button" className="debugger-action-row-main" aria-expanded={groupExpanded} onClick={() => toggleDebugSequenceExpanded(stepKey)}>
                                        <span className="debugger-action-row-head">
                                          <span className="replay-event-phase">
                                            <span aria-hidden="true" className="outline-section-chevron">{groupExpanded ? '▾' : '▸'}</span>
                                            {headLabelBase} ·×{iterationIndices.length}
                                          </span>
                                          <span className={`debugger-action-status ${groupStatus}`} aria-hidden="true" />
                                        </span>
                                        <strong>{item.name || headLabelBase}</strong>
                                        <small>{item.method || 'request'} {item.target || ''}</small>
                                      </button>
                                    </div>
                                  )
                                  if (groupExpanded) {
                                    iterationIndices.forEach((evIdx, k) => {
                                      const ev = debugEvents[evIdx]
                                      // Full iteration vector (outer→inner) disambiguates nested loops;
                                      // falls back to the innermost pass for single-level loops.
                                      const iterPath = ev.iteration_path && ev.iteration_path.length ? ev.iteration_path : [ev.iteration ?? (k + 1)]
                                      const iterLabel = iterPath.join('·')
                                      const iterFail = !ev.success
                                      if (debugRailFailuresOnly && !iterFail) return
                                      const curPath = currentDebugAction?.iteration_path
                                      const isCurrentIter = isCurrentGroup && curPath != null && curPath.length === iterPath.length && curPath.every((n, i) => n === iterPath[i])
                                      const isSelectedIter = selectedReplayEventIndex != null && selectedReplayEventIndex === evIdx
                                      out.push(
                                        <div
                                          key={`${stepKey}#iter${k}`}
                                          className={`debugger-action-row child iteration ${iterFail ? 'fail' : 'pass'} ${isSelectedIter ? 'selected' : ''} ${isCurrentIter ? 'current' : ''}`}
                                          style={indentStyle(item.depth + 1)}
                                          ref={node => { replayEventRefs.current[evIdx] = node }}
                                        >
                                          <span className="debugger-gutter debugger-gutter-spacer" aria-hidden="true" />
                                          <button
                                            type="button"
                                            className="debugger-action-row-main"
                                            onClick={() => {
                                              setExpandedReplayEvent(evIdx)
                                              setDebugDetailCollapsed(false)
                                              if (debuggerTab === 'browser' && !isBrowserScript) setDebuggerTab('request')
                                            }}
                                          >
                                            <span className="debugger-action-row-head">
                                              <span className="replay-event-phase">Iteration {iterLabel}</span>
                                              <span className={`debugger-action-status ${iterFail ? 'fail' : 'pass'}`} aria-hidden="true" />
                                            </span>
                                            <small>{(ev.method || item.method || 'request')} {ev.target || item.target || ''}</small>
                                          </button>
                                        </div>
                                      )
                                    })
                                  }
                                  continue
                                }
                                const eventIndex = isHttpChild
                                  ? eventIndexByLocation.get(item.key)
                                  : (iterationIndices.length ? iterationIndices[iterationIndices.length - 1] : undefined)
                                const leafEvent = eventIndex != null ? debugEvents[eventIndex] : undefined
                                const anyFail = isHttpChild ? !!(leafEvent && !leafEvent.success) : iterationIndices.some(i => !debugEvents[i].success)
                                const bpKey = isHttpChild ? item.key : stepKey
                                const bpActive = debugBreakpointKeys.has(bpKey)
                                const isCurrentRow = currentDebugActionKey === item.key || (currentDebugStepKey != null && currentDebugStepKey === stepKey)
                                const isSelectedRow = selectedReplayEventIndex != null ? selectedReplayEventIndex === eventIndex : selectedActionKey === item.key
                                const hasEvent = leafEvent != null || iterationIndices.length > 0
                                const rowStatus = hasEvent ? (anyFail ? 'fail' : 'pass') : isCurrentRow ? 'current' : replayTerminal ? 'not-run' : 'pending'
                                if (debugRailFailuresOnly && !anyFail) continue
                                if (!debugRailFailuresOnly && !matchesFilter(`${item.name} ${item.target} ${leafEvent?.target || ''}`)) continue
                                const headLabel = isHttpChild ? `Request ${(item.requestIndex ?? 0) + 1}` : humanizeStepSegment(item.stepPath[item.stepPath.length - 1] || '')
                                out.push(
                                  <div
                                    key={item.key}
                                    className={`debugger-action-row child ${rowStatus} ${isSelectedRow ? 'selected' : ''} ${isCurrentRow ? 'current' : ''}`}
                                    style={indentStyle(item.depth)}
                                    ref={node => { if (eventIndex != null) replayEventRefs.current[eventIndex] = node }}
                                  >
                                    <button
                                      type="button"
                                      className={`debugger-gutter ${bpActive ? 'active' : ''}`}
                                      data-disabled={disabledBreakpointKeys.has(bpKey)}
                                      title={bpActive ? 'Remove sub-step breakpoint' : 'Add sub-step breakpoint'}
                                      aria-label={`${bpActive ? 'Remove' : 'Add'} sub-step breakpoint on ${item.name}`}
                                      onClick={() => isHttpChild ? void toggleDebugBreakpoint(phase, actionIndex, item.requestIndex) : void toggleStepPathBreakpoint(phase, actionIndex, item.stepPath)}
                                    >
                                      <span className="debugger-breakpoint-dot" />
                                    </button>
                                    <button
                                      type="button"
                                      className="debugger-action-row-main"
                                      aria-pressed={isSelectedRow}
                                      aria-current={isCurrentRow ? 'step' : undefined}
                                      onClick={() => {
                                        onSelectDebugItem(item, eventIndex != null)
                                        setNarrowPane('editor')
                                        setDebuggerTab('request')
                                        if (eventIndex == null) return
                                        setExpandedReplayEvent(eventIndex)
                                        setDebugDetailCollapsed(false)
                                        if (debuggerTab === 'browser' && !isBrowserScript) setDebuggerTab('request')
                                      }}
                                    >
                                      <span className="debugger-action-row-head">
                                        <span className="replay-event-phase">{headLabel}{iterationIndices.length > 1 ? ` ·×${iterationIndices.length}` : ''}</span>
                                        <span className={`debugger-action-status ${rowStatus}`} aria-hidden="true" />
                                      </span>
                                      <strong>{item.name || leafEvent?.action_name || headLabel}</strong>
                                      <small>{(leafEvent?.method || item.method || 'request')} {leafEvent?.target || item.target || ''}</small><span className="debug-action-status-text">{rowStatus === 'pass' ? 'Passed' : rowStatus === 'fail' ? 'Failed' : rowStatus === 'not-run' ? 'Not run' : rowStatus === 'current' ? 'Current' : 'Pending'}</span>
                                    </button>
                                  </div>
                                )
                                continue
                              }
                              const rowKey = item.key
                              const eventIndex = eventIndexByLocation.get(rowKey)
                              const rowEvent = eventIndex != null ? debugEvents[eventIndex] : undefined
                              const breakpointActive = debugBreakpointKeys.has(rowKey)
                              const isCurrentRow = currentDebugActionKey === rowKey
                              const isSelectedRow = selectedReplayEventIndex != null ? selectedReplayEventIndex === eventIndex : selectedActionKey === item.key
                              const rowStatus = rowEvent ? (rowEvent.success ? 'pass' : 'fail') : isCurrentRow ? 'current' : replayTerminal ? 'not-run' : 'pending'
                              const haystack = `${action.name || ''} ${action.target || ''} ${action.selector || ''} ${rowEvent?.action_name || ''} ${rowEvent?.target || ''}`
                              if (debugRailFailuresOnly && !(rowEvent && !rowEvent.success)) continue
                              if (!debugRailFailuresOnly && !matchesFilter(haystack)) continue
                              out.push(
                                <div
                                  key={rowKey}
                                  className={`debugger-action-row ${rowStatus} ${isSelectedRow ? 'selected' : ''} ${isCurrentRow ? 'current' : ''}`}
                                  ref={node => { if (eventIndex != null) replayEventRefs.current[eventIndex] = node }}
                                >
                                  <button
                                    type="button"
                                    className={`debugger-gutter ${breakpointActive ? 'active' : ''}`}
                                    data-disabled={disabledBreakpointKeys.has(rowKey)}
                                    title={breakpointActive ? 'Remove breakpoint' : 'Add breakpoint'}
                                    aria-label={`${breakpointActive ? 'Remove' : 'Add'} breakpoint on ${action.name || `action ${actionIndex + 1}`}`}
                                    onClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                                  >
                                    <span className="debugger-breakpoint-dot" />
                                  </button>
                                  <button
                                    type="button"
                                    className="debugger-action-row-main"
                                    aria-pressed={isSelectedRow}
                                    aria-current={isCurrentRow ? 'step' : undefined}
                                    onDoubleClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                                    onClick={() => {
                                      onSelectDebugItem(item, eventIndex != null)
                                      setNarrowPane('editor')
                                      setDebuggerTab('request')
                                      if (eventIndex != null) {
                                        setExpandedReplayEvent(eventIndex)
                                        setDebugDetailCollapsed(false)
                                        if (debuggerTab === 'browser' && !isBrowserScript) setDebuggerTab('request')
                                      }
                                    }}
                                  >
                                    <span className="debugger-action-row-head">
                                      <span className="replay-event-phase">{formatDebugPhaseLabel(phase)} {actionIndex + 1}</span>
                                      <span className={`debugger-action-status ${rowStatus}`} aria-hidden="true" />
                                    </span>
                                    <strong>{action.name || rowEvent?.action_name || 'Unnamed action'}</strong>
                                    <small>{(rowEvent?.method || action.method || rowEvent?.action_type || action.type || 'action')} {rowEvent?.target || action.target || action.selector || ''}</small><span className="debug-action-status-text">{rowStatus === 'pass' ? 'Passed' : rowStatus === 'fail' ? 'Failed' : rowStatus === 'not-run' ? 'Not run' : rowStatus === 'current' ? 'Current' : 'Pending'}</span>
                                  </button>
                                </div>
                              )
                            }
                            return out.length > 0 ? out : <div className="debugger-rail-empty">{debugRailFailuresOnly ? 'No failures match the current filter.' : 'No actions match the current filter.'}</div>
                          })()}
                        </div>
                      )
                    )}
                  </section>
                  <section className="debugger-rail-section debugger-parameter-section" hidden={!script.parameters?.length && !script.data_sources?.length}>
                    <div className="debugger-rail-header debugger-parameter-header">
                      <button
                        type="button"
                        className="debugger-rail-section-toggle"
                        aria-expanded={!debugParameterListCollapsed}
                        onClick={() => setDebugParameterListCollapsed(collapsed => !collapsed)}
                      >
                        <span aria-hidden="true" className="outline-section-chevron">{debugParameterListCollapsed ? '▸' : '▾'}</span>
                        <span>Parameter List</span>
                      </button>
                      <small>{script.parameters?.length || 0} defined</small>
                    </div>
                    {!debugParameterListCollapsed && (
                      <div className="debugger-parameter-list">
                        {(script.parameters || []).map((parameter, index) => {
                          const parameterName = parameter.name || `Parameter ${index + 1}`
                          const referenceCount = parameter.name ? parameterReferenceCounts.get(parameter.name) || 0 : 0
                          const selected = selectedOutlineParameterName === parameter.name
                          return (
                            <button
                              key={`debug-${parameter.name || 'parameter'}-${index}`}
                              type="button"
                              className={`parameter-outline-item debugger-parameter-item ${selected ? 'selected' : ''}`}
                              onClick={() => { if (parameter.name) { selectOutlineParameter(parameter.name); setNarrowPane(isBrowserScript ? 'editor' : 'inspector') } }}
                              disabled={!parameter.name}
                            >
                              <strong>{parameterName}</strong>
                              <span>{parameter.type || 'list'} · {referenceCount} ref{referenceCount === 1 ? '' : 's'}</span>
                            </button>
                          )
                        })}
                        {(script.parameters || []).length === 0 && (
                          <div className="debugger-rail-empty">No parameters defined yet.</div>
                        )}
                      </div>
                    )}
                  </section>
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
                    <TabList className="debugger-center-tablist" role="tablist" onKeyDown={handleTabKey}>
                      <button type="button" role="tab" aria-selected={debuggerTab === 'request'} className={debuggerTab === 'request' ? 'active' : ''} onClick={() => setDebuggerTab('request')} tabIndex={(debuggerTab === 'request') ? 0 : -1}>Request / Response</button>
                      {isBrowserScript && (
                        <button type="button" role="tab" aria-selected={debuggerTab === 'browser'} className={debuggerTab === 'browser' ? 'active' : ''} onClick={() => setDebuggerTab('browser')} tabIndex={(debuggerTab === 'browser') ? 0 : -1}>Browser Evidence</button>
                      )}
                      <button type="button" role="tab" aria-selected={debuggerTab === 'variables'} className={debuggerTab === 'variables' ? 'active' : ''} onClick={() => setDebuggerTab('variables')} tabIndex={(debuggerTab === 'variables') ? 0 : -1}>{replaying ? 'Live Variables' : 'Variables'}</button>
                    </TabList>
                    <button
                      type="button"
                      className="debugger-pane-close"
                      title="Close debug detail panel"
                      aria-label="Close debug detail panel"
                      onClick={() => { setDebugDetailCollapsed(true); setNarrowPane('editor') }}
                    >
                      ×
                    </button>
                  </div>
                  <div className="debugger-center-body">
                    {debuggerTab === 'variables' ? (
                      <div className="debugger-variables-body">
                        {selectedOutlineParameterName && <DebugParameterInspector key={`${selectedOutlineParameterName}:${selectedReplayEventIndex}`} script={script} name={selectedOutlineParameterName} values={replayVariableInspector} event={selectedReplayEvent} matchesDraft={replayMatchesCurrentScript} onFindUsages={() => { selectOutlineParameter(selectedOutlineParameterName); setNarrowPane('editor') }} onEdit={onEditParameterDefinition} />}
                        <details><summary>Template namespaces and definitions</summary><ParameterDefinitionGuide script={script} /></details>
                        <DebugVariableWatches events={debugEvents} seed={seedVariables} latest={replayVariableInspector} selected={selectedReplayEventIndex} onSelect={jumpToEvent} />
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
                      <div className="debugger-empty">No debugger session yet. Review Debug options, then run one iteration. <button type="button" onClick={() => { setNarrowPane('outline'); setDebugActionListCollapsed(false) }}>Open outline to set breakpoints</button></div>
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
                                  <p>Response body: {(event.snapshot?.response_body_state || event.response_body_state)?.replace(/_/g, ' ') || 'Unknown capture state'}</p>
                                  {event.error_message && <p role="alert">{event.error_message}</p>}
                                  {event.snapshot?.response_body ? <button type="button" onClick={() => document.getElementById('debug-captured-response')?.scrollIntoView({ block: 'nearest' })}>View captured response below</button> : <p>{(event.snapshot?.response_body_state || event.response_body_state) === 'empty' ? 'The target returned an empty response body.' : (event.snapshot?.response_body_state || event.response_body_state) === 'redacted' ? 'Response body removed by the credential-redaction policy.' : (event.snapshot?.response_body_state || event.response_body_state) === 'expired' ? 'Response body expired under the retention policy.' : (event.snapshot?.response_body_state || event.response_body_state) === 'not_captured' ? 'Response body was not captured. Open Debug options and choose diagnostic capture before rerunning.' : event.response_body_state === 'not_retained' ? 'Response body was removed by the evidence-retention limit.' : 'Response body unavailable; the capture state is unknown. This does not establish that the response was empty.'}</p>}
                                  {!event.snapshot?.response_body && <button type="button" onClick={() => setDebuggerSettingsOpen(true)}>Review evidence capture settings</button>}
                                </section>}
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
                                  <div className="replay-code-block">
                                    <span>Protocol Evidence{formatProtocolEvidenceSummary(event.protocol_evidence) ? ` · ${formatProtocolEvidenceSummary(event.protocol_evidence)}` : ''}</span>
                                    <SnapshotBodyPreview
                                      body={JSON.stringify(event.protocol_evidence, null, 2)}
                                      headers={{ 'Content-Type': 'application/json' }}
                                      emptyMessage="No protocol evidence captured"
                                    />
                                  </div>
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
                                {event.extractions && Object.keys(event.extractions).length > 0 && (
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
                                    <div className="replay-event-detail-grid">
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
                                        <SnapshotBodyPreview
                                          body={event.snapshot.request_body}
                                          headers={event.snapshot.request_headers}
                                          emptyMessage="No request body captured"
                                        />
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
                                        <pre>{String(event.snapshot.response_status || event.status_code || 'n/a')}</pre>
                                        <SnapshotTextPreview text={formatReplayHeaders(event.snapshot.response_headers)} />
                                        <SnapshotBodyPreview
                                          body={event.snapshot.response_body}
                                          headers={event.snapshot.response_headers}
                                          emptyMessage="No response body captured"
                                        />
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
                    <TabList role="tablist" aria-label="Debug logs and problems" className="debugger-bottom-tab-list" onKeyDown={handleTabKey}>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'console'} className={debuggerBottomTab === 'console' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('console') }} title="Wire log — raw protocol request/response and engine output" tabIndex={(debuggerBottomTab === 'console') ? 0 : -1}>Engine logs</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'timeline'} className={debuggerBottomTab === 'timeline' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('timeline') }} title="Flow log — actions, transactions, and variable changes in order" tabIndex={(debuggerBottomTab === 'timeline') ? 0 : -1}>Timeline</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'problems'} className={debuggerBottomTab === 'problems' ? 'active' : ''} onClick={() => { setDockCollapsed(false); setDebuggerBottomTab('problems') }} tabIndex={(debuggerBottomTab === 'problems') ? 0 : -1}>Problems{problemCount + runtimeFailures.length > 0 ? ` ${problemCount + runtimeFailures.length}` : ''}</button>

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
                      <details><summary>Session lifecycle ({replayActivity.length})</summary>{replayActivity.map(item => <p key={item.id}>{item.timestamp} · {item.title} · {item.detail}</p>)}</details>
                    </div>
                  ) : debuggerBottomTab === 'problems' ? (
                    <div className="debugger-problems" role="tabpanel" aria-label="Problems">
                      <div className="debugger-problems-header">
                        <div>
                          <strong>{runtimeFailures.length} runtime failures · {problemCount} validation/editor issues</strong>
                          <span>Validation and editor issues remain visible in the Author inspector.</span>
                        </div>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={handleValidateScript} disabled={saving || replaying || validating || !script.name}>
                          {validating ? 'Validating...' : 'Validate'}
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
                <span className={`debugger-statusbar-item ${replayStatusLabel === 'Failed' ? 'text-danger' : replayStatusLabel === 'Running' ? '' : 'text-success'}`}>{replayReport ? replayStatusLabel : 'Idle'}</span>
                <span className="debugger-statusbar-item">Group: {replayReport?.group || replayOptions.groupName || 'replay'}</span>
                <span className="debugger-statusbar-item">Protocol: {replayReport?.protocol || script.protocol}</span>
                <span className="debugger-statusbar-item">{replayReport ? `${replayReport.events.length} recorded executions` : `${debugActionRows.length} actions planned`}</span>
                {replayReport?.completed_at && <span className="debugger-statusbar-item">Completed {formatReplayTime(replayReport.completed_at)}</span>}
                {debugBreakpoints.length > 0 && <span className="debugger-statusbar-item">{debugBreakpoints.length} breakpoint{debugBreakpoints.length === 1 ? '' : 's'}</span>}
              </div>
            </div>
          </div>
  )
}

function EvidenceDestination({ host, children }: { host?: HTMLElement | null; children: ReactNode }) { return host ? createPortal(children, host) : children }
