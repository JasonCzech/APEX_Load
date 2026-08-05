import type * as React from 'react'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugLogLevel,
  ScriptDebugSession,
} from '../types/replay'
import type {
  ScriptDefinition,
  ScriptExtractorDefinition,
} from '../types/scripts'
import type { useApi } from '../hooks/useApi'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import { CorrelationDiagnosePanel, type RecordingStatusState } from './CorrelationDiagnosePanel'
import { ExtractorTester } from './ExtractorTester'
import RepairSuggestionList from './RepairSuggestionList'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import type { CorrelationApplyResult, CorrelationExtractorRepair } from './scriptCorrelationRepair'
import {
  type AuthoringIssue,
} from './scriptAuthoringAssist'
import {
  debugBreakpointKey,
  formatConditionEvidence,
  formatDebugLeafSuffix,
  formatDebugLogEntry,
  formatReplayDiagnosisCategory,
  hasStructuredReplaySnapshot,
  humanizeStepSegment,
} from './scriptEditorReplayModel'
import {
  DEBUGGER_LAYOUT_LIMITS,
  buildDebugRailRenderRows,
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
  debuggerWorkbench: ReactNode
  error: string
  expandedReplayBrowserArtifacts: Record<string, boolean>
  expandedReplayEvent: number | null
  exportGeneratedTextFile: (content: string, filename: string, savedMessage?: string) => Promise<void>
  handleCopyReplaySnapshotSection: (key: string, value: string) => Promise<void>
  handleValidateScript: () => Promise<void>
  isBrowserScript: boolean
  jsonParseMessage: string
  onActivateDebug: () => void
  onSelectProblemAction: (row: ScriptPhaseActionRow) => void
  onUpdateScriptName: (value: string) => void
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
  replayStatusLabel: 'Running' | 'Success' | 'Failed'
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
  selectedOutlineParameterName: string | null
  selectOutlineParameter: (parameterName: string) => void
  setActiveAction: ScriptEditorStateSetter<number | null>
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
  actionProblemRows,
  activeReplaySessionId,
  addExtractorDefinition,
  api,
  applyReplaySuggestedLocator,
  breakpointSummary,
  browserReplaySession,
  clearDebugBreakpoints,
  controller,
  copiedReplaySnapshotSection,
  debugActionRows,
  debugBreakpointKeys,
  debuggerWorkbench,
  error,
  expandedReplayBrowserArtifacts,
  expandedReplayEvent,
  exportGeneratedTextFile,
  handleCopyReplaySnapshotSection,
  handleValidateScript,
  isBrowserScript,
  jsonParseMessage,
  onActivateDebug,
  onSelectProblemAction,
  onUpdateScriptName,
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
  selectedOutlineParameterName,
  selectOutlineParameter,
  setActiveAction,
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

  return (
          <div
            className={`config-panel replay-panel script-ide-debugger debugger-dialog open ${replayReport ? 'has-report' : ''}`}
            role="region"
            aria-label="Script Debugger"
          >
            {debuggerSettingsOpen && (
              <div className="debugger-settings-popover" ref={debuggerSettingsRef}>
                <div className="debugger-settings-head">
                  <strong>Session Overrides</strong>
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => setDebuggerSettingsOpen(false)}>Done</button>
                </div>
                <p className="replay-panel-subtitle">Temporary settings for this debugger session. They do not change saved Runtime Settings.</p>
                <div className="replay-options-grid">
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
                      <CodeMirror
                        value={replayOptions.variablesText}
                        onChange={value => setReplayOptions(prev => ({ ...prev, variablesText: value }))}
                        extensions={[json(), apexCodeMirrorTheme]}
                        theme="none"
                        maxHeight="200px"
                        basicSetup={{
                          lineNumbers: true,
                          foldGutter: true,
                        }}
                      />
                    </div>
                  </div>
                </div>
              </div>
            )}

            <div className="debugger-body">
              {(replaying || replayReport) && (
                <div className={`debugger-live-strip ${debuggerStatusState}`}>
                  <span className="replay-live-badge">{replaying ? (isReplayPaused ? 'Paused' : 'Streaming live') : replayReport?.success ? 'Finished' : 'Failed'}</span>
                  <div className="debugger-live-strip-text">
                    <strong>{replayLatestActivity?.title || 'Debugger status available'}</strong>
                    {replayLatestActivity?.detail && <span>{replayLatestActivity.detail}</span>}
                  </div>
                  <span className="debugger-live-strip-time">{formatReplayTime(replayLatestActivity?.timestamp || replayReport?.completed_at || replayReport?.started_at)}</span>
                </div>
              )}
              {replayReport?.error && <p className="replay-report-error">{replayReport.error}</p>}
              {replayFailingEvent && (
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

              <div
                className={`debugger-shell ${debugDetailCollapsed ? 'debugger-detail-collapsed' : ''} ${activeDebuggerResize ? `resizing resizing-${activeDebuggerResize}` : ''}`}
                style={{
                  '--rail-w': `${debuggerRailWidth}px`,
                  '--vars-w': `${debuggerVarsWidth}px`,
                  '--bottom-h': `${debuggerBottomHeight}px`,
                } as React.CSSProperties}
              >
                <aside className="debugger-rail" aria-label="Actions">
                  <div className="script-name-panel side-panel-script-name" aria-label="Script identity">
                    <label className="script-name-field">
                      <span>Script Name</span>
                      <input value={script.name} onChange={e => onUpdateScriptName(e.target.value)} placeholder="My_Test_Script" />
                    </label>
                  </div>
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
                            <input type="checkbox" checked={debugRailFailuresOnly} onChange={e => setDebugRailFailuresOnly(e.target.checked)} />
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
                          onKeyDownCapture={e => {
                            if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
                              e.preventDefault()
                              const dir = e.key === 'ArrowDown' ? 1 : -1
                              const total = debugEvents.length
                              if (total === 0) return
                              const current = selectedReplayEventIndex ?? 0
                              const next = Math.max(0, Math.min(total - 1, current + dir))
                              setExpandedReplayEvent(next)
                            }
                          }}
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
                                const typeLabel = item.containerType === 'http_sequence' ? 'SEQ' : item.containerType === 'control_if' ? 'IF' : 'LOOP'
                                const unitWord = item.containerType === 'http_sequence' ? 'request' : 'step'
                                const controlEvidence = item.containerType === 'control_if' || item.containerType === 'control_loop'
                                  ? conditionEvidenceFor(phase, actionIndex, item.stepPath)
                                  : undefined
                                const branchBadge = item.containerType === 'control_if' && controlEvidence
                                  ? (controlEvidence.branch === 'then' ? 'THEN' : 'ELSE')
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
                                        aria-label={wholeActive ? 'Remove breakpoint' : 'Add breakpoint'}
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
                                        <span className={`control-branch-badge ${branchBadge === 'THEN' ? 'taken' : 'skipped'}`} title={`Condition was ${controlEvidence?.result ? 'true' : 'false'} → ${branchBadge.toLowerCase()} branch`}>{branchBadge}</span>
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
                                        aria-label={stepBpActive ? 'Remove sub-step breakpoint' : 'Add sub-step breakpoint'}
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
                                const isSelectedRow = selectedReplayEventIndex != null && selectedReplayEventIndex === eventIndex
                                const hasEvent = leafEvent != null || iterationIndices.length > 0
                                const rowStatus = hasEvent ? (anyFail ? 'fail' : 'pass') : isCurrentRow ? 'current' : 'pending'
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
                                      title={bpActive ? 'Remove sub-step breakpoint' : 'Add sub-step breakpoint'}
                                      aria-label={bpActive ? 'Remove sub-step breakpoint' : 'Add sub-step breakpoint'}
                                      onClick={() => isHttpChild ? void toggleDebugBreakpoint(phase, actionIndex, item.requestIndex) : void toggleStepPathBreakpoint(phase, actionIndex, item.stepPath)}
                                    >
                                      <span className="debugger-breakpoint-dot" />
                                    </button>
                                    <button
                                      type="button"
                                      className="debugger-action-row-main"
                                      disabled={eventIndex == null}
                                      onClick={() => {
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
                                      <small>{(leafEvent?.method || item.method || 'request')} {leafEvent?.target || item.target || ''}</small>
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
                              const isSelectedRow = selectedReplayEventIndex != null && selectedReplayEventIndex === eventIndex
                              const rowStatus = rowEvent ? (rowEvent.success ? 'pass' : 'fail') : isCurrentRow ? 'current' : 'pending'
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
                                    title={breakpointActive ? 'Remove breakpoint' : 'Add breakpoint'}
                                    aria-label={breakpointActive ? 'Remove breakpoint' : 'Add breakpoint'}
                                    onClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                                  >
                                    <span className="debugger-breakpoint-dot" />
                                  </button>
                                  <button
                                    type="button"
                                    className="debugger-action-row-main"
                                    onDoubleClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                                    onClick={() => {
                                      if (phase === 'action') setActiveAction(actionIndex)
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
                                    <small>{(rowEvent?.method || action.method || rowEvent?.action_type || action.type || 'action')} {rowEvent?.target || action.target || action.selector || ''}</small>
                                  </button>
                                </div>
                              )
                            }
                            return out.length > 0 ? out : <div className="debugger-rail-empty">No actions match the filter.</div>
                          })()}
                        </div>
                      )
                    )}
                  </section>
                  <section className="debugger-rail-section debugger-parameter-section">
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
                              onClick={() => parameter.name && selectOutlineParameter(parameter.name)}
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

                {!debugDetailCollapsed && (
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
                    <div className="debugger-center-tablist" role="tablist">
                      <button type="button" role="tab" aria-selected={debuggerTab === 'request'} className={debuggerTab === 'request' ? 'active' : ''} onClick={() => setDebuggerTab('request')}>Request / Response</button>
                      {isBrowserScript && (
                        <button type="button" role="tab" aria-selected={debuggerTab === 'browser'} className={debuggerTab === 'browser' ? 'active' : ''} onClick={() => setDebuggerTab('browser')}>Browser Evidence</button>
                      )}
                      <button type="button" role="tab" aria-selected={debuggerTab === 'variables'} className={debuggerTab === 'variables' ? 'active' : ''} onClick={() => setDebuggerTab('variables')}>{replaying ? 'Live Variables' : 'Variables'}</button>
                    </div>
                    <button
                      type="button"
                      className="debugger-pane-close"
                      title="Close debug detail panel"
                      aria-label="Close debug detail panel"
                      onClick={() => setDebugDetailCollapsed(true)}
                    >
                      ×
                    </button>
                  </div>
                  <div className="debugger-center-body">
                    {debuggerTab === 'variables' ? (
                      <div className="debugger-variables-body">
                        {replayVariableInspector && Object.keys(replayVariableInspector).length > 0 ? (
                          <SnapshotBodyPreview
                            body={JSON.stringify(replayVariableInspector, null, 2)}
                            headers={{ 'Content-Type': 'application/json' }}
                          />
                        ) : (
                          <div className="replay-variables-empty">
                            {replaying ? 'No replay variables captured yet.' : replayReport ? 'No extracted or seeded variables.' : 'Variables appear here once a debugger session runs.'}
                          </div>
                        )}
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
                      <CorrelationDiagnosePanel
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
                      />
                    )}
                    {debuggerTab === 'browser' && isBrowserScript ? (
                      <BrowserReplayDebugger
                        sessionId={activeReplaySessionId}
                        session={browserReplaySession}
                        report={replayReport}
                        running={replaying}
                        selectedEventIndex={expandedReplayEvent}
                        onSelectEvent={setExpandedReplayEvent}
                        onApplySuggestedLocator={applyReplaySuggestedLocator}
                      />
                    ) : !replayReport ? (
                      <div className="debugger-empty">No debugger session yet. Place optional breakpoints in the gutter, adjust settings, then press Run.</div>
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
                                  <ExtractorTester
                                    event={event}
                                    actionIndex={editableActionIndex}
                                    onAddExtractor={addExtractorDefinition}
                                  />
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
                                          <span className="replay-detail-label">Snapshot Request</span>
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
                                          <span className="replay-detail-label">Snapshot Response</span>
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
                    aria-valuemax={DEBUGGER_LAYOUT_LIMITS.bottom.max}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.bottom.min}
                    aria-valuenow={debuggerBottomHeight}
                  />
                  <div className="debugger-bottom-tabs" role="tablist">
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'console'} className={debuggerBottomTab === 'console' ? 'active' : ''} onClick={() => setDebuggerBottomTab('console')} title="Wire log — raw protocol request/response and engine output">Console</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'timeline'} className={debuggerBottomTab === 'timeline' ? 'active' : ''} onClick={() => setDebuggerBottomTab('timeline')} title="Flow log — actions, transactions, and variable changes in order">Timeline</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'problems'} className={debuggerBottomTab === 'problems' ? 'active' : ''} onClick={() => setDebuggerBottomTab('problems')}>Problems{problemCount > 0 ? ` ${problemCount}` : ''}</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'output'} className={debuggerBottomTab === 'output' ? 'active' : ''} onClick={() => setDebuggerBottomTab('output')}>Output</button>
                  </div>
                  {debuggerBottomTab === 'console' ? (
                    <div className="debug-raw-panel debugger-console">
                      <div className="debug-raw-toolbar">
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
                      <div ref={debugRawOutputRef} className="debug-raw-output">
                        {debugFilteredLogs.length === 0 ? (
                          <span className="debug-log-empty">No raw output captured yet.</span>
                        ) : (
                          debugFilteredLogs.map(entry => {
                            const jumpPosition = entry.phase ? debugRowPositionByKey.get(debugBreakpointKey(entry.phase, entry.action_index ?? 0)) : undefined
                            const canJump = jumpPosition != null && !!debugEvents[jumpPosition]
                            return (
                              <button
                                key={entry.id}
                                type="button"
                                className={`debug-log-line level-${entry.level} ${canJump ? 'jumpable' : ''}`}
                                disabled={!canJump}
                                onClick={() => {
                                  if (jumpPosition == null) return
                                  setDebuggerTab('request')
                                  setExpandedReplayEvent(jumpPosition)
                                }}
                              >
                                {formatDebugLogEntry(entry)}
                              </button>
                            )
                          })
                        )}
                      </div>
                    </div>
                  ) : debuggerBottomTab === 'timeline' ? (
                    <div className="debugger-timeline">
                      {replayActivity.length > 0 ? (
                        <div className="replay-activity-list">
                          {replayActivity.map(item => (
                            <div key={item.id} className={`replay-activity-item ${item.kind} ${item.success === false ? 'error' : item.success === true ? 'success' : ''}`}>
                              <div className="replay-activity-marker" />
                              <div className="replay-activity-content">
                                <div className="replay-activity-header-row">
                                  <strong>{item.title}</strong>
                                  <span>{formatReplayTime(item.timestamp)}</span>
                                </div>
                                {item.detail && <div className="replay-activity-detail">{item.detail}</div>}
                                {item.changedVariables && item.changedVariables.length > 0 && (
                                  <div className="replay-variable-chip-row">
                                    {item.changedVariables.map(name => (
                                      <span key={`${item.id}-${name}`} className="replay-variable-chip">{name}</span>
                                    ))}
                                  </div>
                                )}
                              </div>
                            </div>
                          ))}
                        </div>
                      ) : (
                        <div className="debugger-empty">Execution timeline will populate as the debugger runs.</div>
                      )}
                    </div>
                  ) : debuggerBottomTab === 'problems' ? (
                    <div className="debugger-problems" role="tabpanel" aria-label="Problems">
                      <div className="debugger-problems-header">
                        <div>
                          <strong>{problemCount === 0 ? 'No problems detected' : `${problemCount} problem${problemCount === 1 ? '' : 's'}`}</strong>
                          <span>Validation and editor issues remain visible in the Author inspector.</span>
                        </div>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={handleValidateScript} disabled={saving || replaying || validating || !script.name}>
                          {validating ? 'Validating...' : 'Validate'}
                        </button>
                      </div>
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
                  ) : (
                    <div className="debug-output-panel" role="tabpanel" aria-label="Debug Output">
                      <div className="debug-output-toolbar">
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => void navigator.clipboard?.writeText(debugRawOutputText)}>Copy</button>
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => { void exportGeneratedTextFile(debugRawOutputText || 'No debug output captured', `${sanitizeDownloadName(script.name, 'script')}-debug-output.log`) }}>Download</button>
                      </div>
                      <pre>{debugRawOutputText || 'No debug output captured yet.'}</pre>
                    </div>
                  )}
                </section>
              </div>

              <div className="debugger-statusbar">
                <span className={`debugger-statusbar-item ${replayStatusLabel === 'Failed' ? 'text-danger' : replayStatusLabel === 'Running' ? '' : 'text-success'}`}>{replayReport ? replayStatusLabel : 'Idle'}</span>
                <span className="debugger-statusbar-item">Group: {replayReport?.group || replayOptions.groupName || 'replay'}</span>
                <span className="debugger-statusbar-item">Protocol: {replayReport?.protocol || script.protocol}</span>
                <span className="debugger-statusbar-item">{replayReport ? `${replayReport.events.length} / ${debugActionRows.length} actions` : `${debugActionRows.length} actions planned`}</span>
                {replayReport?.completed_at && <span className="debugger-statusbar-item">Completed {formatReplayTime(replayReport.completed_at)}</span>}
                {debugBreakpoints.length > 0 && <span className="debugger-statusbar-item">{debugBreakpoints.length} breakpoint{debugBreakpoints.length === 1 ? '' : 's'}</span>}
              </div>
            </div>
          </div>
  )
}
