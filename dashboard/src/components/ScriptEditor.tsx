import { parseLiveTableSandboxSeeds } from './liveTableSandboxModel'
import { nativeJavaScriptTemplate } from './nativeServicesModel'
import { scriptSecretReferenceSummaries } from './scriptSecretReferenceModel'
import type { ScriptAuthoringBaseline, ScriptSaveOptions } from '../types/storedContent'
import { baselineFromSavedScript, reapplyScriptDraft, scriptSaveFailedOnServer, scriptSaveNeedsReconciliation } from '../utils/scriptAuthoringRevision'
import ScriptRevisionConflict from './ScriptRevisionConflict'
import TooltipButton from './ux/TooltipButton'
import { isSavedBlock } from './savedBlocks'
import SharedAssetsDialog from './SharedAssetsDialog'
import type { SharedAssetsSection } from './sharedAssetsNavigation'
import ScriptOutlineTree, { OutlineMeasureCard } from './ScriptOutlineTree'
import { buildOutlineModel, renameTransactionMarker, splitMeasureName, type OutlineMeasure } from './scriptOutlineModel'
import { NO_RESPONSE_ACTION_TYPES, HTTP_RUNTIME_ACTION_TYPES, liveTableFormContext } from './catalogActionFormModel'
import { buildReferenceScope, type InsertionPoint } from './referenceScopeModel'
import { ReferenceFieldContext, type FieldPath, type ReferenceFieldContextValue } from './referenceFieldContext'
import { AuthorInsertDialog, type InsertSharedData } from './AuthorInsertDialog'
import ScriptDataUsagePanel from './ScriptDataUsagePanel'
import { earlierVariables, scriptActionNames, scriptDataUsage, takenVariableNames, usesTableSteps, withUniqueResultPrefixes, type DataAliasStep } from './scriptDataUsageModel'
import { useScriptScenarioBindings } from './useScriptScenarioBindings'
import { checkFamily, lastStepEvent, replayEventStepKey, replayEventStepPath, replayResolver, type ChecksReplay } from './actionChecksModel'
import CorrelationReviewDialog from './CorrelationReviewDialog'
import { applyValueCorrelation, laterUses, stepLayoutKey, suggestValueName, updateStep, type ValueUse } from './responseSelectionModel'
import { boundaryExpression } from './extractorMatch'
import type { StepPosition } from './referenceScopeModel'
import type { ResponseActions } from './ScriptReplayDebuggerView'
import { templateStepNames, type TemplateSpot } from './sharedDataTemplates'
import { useDeferredScriptInspectorFocus } from './useDeferredScriptInspectorFocus'
import { actionBreadcrumb, dedupeAuthorIssues, insertAuthorAction, remapInsertedBreakpoints, changedDocumentLines, type InsertPosition } from './authorExperience'
import { captureScript, type DebugRunRecord } from './debuggerExperience'
import { preserveMappingBindings, withMappingReviewState } from './sourceMappingReviewState'
import { ActionBodyWorkspace } from './ActionBodyWorkspace'
import { debugContinuesAfterFailure, debugRunIdError, parameterUsageLabel } from './scriptSidebarModel'
import { DatasetChangeReview, type DatasetChange } from './DatasetChangeReview'
import { parameterUsages, renameScriptParameter, renameDatasetColumn, validateDatasetReplacement } from './parameterIntegrity'
import TabList from './ux/TabList'
import DialogLayer from './ux/DialogLayer'
import PanelToggle from "./ux/PanelToggle"
import { handleTabKey } from './tabKeyboard'
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import type { BrowserToHTTPPreviewResponse } from '../hooks/api/useScriptApi'
import { useKeyboardShortcuts } from '../hooks/KeyboardShortcutProvider'
import { useHasTopbarContributionProvider, useTopbarContribution, type TopbarAction, type TopbarContribution } from '../hooks/TopbarContributionProvider'
import TopbarActionButton from './TopbarActionButton'
import { dialogBackdropProps } from './dialogBackdropProps'
import { interactiveElementProps } from './interactiveElementProps'
import { useModalDialog } from '../hooks/useModalDialog'
import { useWebSocket } from '../hooks/useWebSocket'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'
import { buildDslAssistExtensions } from './dslEditorAssist'
import { ensureDslActionCatalog } from './dslActionCatalog'
import {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugBreakpoint,
  ScriptDebugAction,
  ScriptDebugSession,
  type ScriptDebugStepMode,
  type AuthoringComponent,
  StoredScript,
  WSMessage,
} from '../types'
import {
  type ScriptActionDefinition,
  type ScriptAssertionDefinition,
  type ScriptDefinition,
  type ScriptExtractorDefinition,
  type ScriptDataSource,
  type ScriptParameterDefinition,
  type ScriptValidationResponse,
  type ReplayPreflight,
} from '../types/scripts'
import ScriptBrowserEvidence from './ScriptBrowserEvidence'
import IssueDetailsDialog, { type IssueDetail } from './IssueDetailsDialog'
import ObjectHistoryDialog from './ObjectHistoryDialog'
import RowActions from './ux/RowActions'
import type { ObjectActivitySummary } from '../types/objectActivity'
import {
  addCorrelationExtractorIfMissing,
  applyCorrelationRepairToAction,
  correlationEditableActionsKey,
  correlationExtractorScope,
  correlationRepairRequiresStructuralBodyReview,
  currentCorrelationEditableActionsKey,
  resolveCorrelationExtractorAndRepair,
  type CorrelationApplyResult,
  type CorrelationExtractorRepair,
} from './scriptCorrelationRepair'
import ParameterizationReview from './ParameterizationReview'
import type { Parameterization } from '../types/scriptImports'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import type { ProjectSecretStatus, ScriptSecretStatus } from '../types/secrets'
import PageViewToggle from './PageViewToggle'
import ReplayPreflightCard from './ReplayPreflightCard'
import ScriptSecretBindingsDialog from './ScriptSecretBindingsDialog'
import { buildLocalReplayPreflight, reconcileReplayPreflightSecretStatus, replayPreflightStatusLabel, templateVariableNames } from './replayPreflightUtils'
import { openingEditorFocus, replayReadinessIssues, type ReadinessFacts } from './replayReadinessIssues'
import {
  applyMigrationAssistantSuggestion,
  applyAuthoringComponentToScript,
  applyScriptRepairSuggestion,
  buildMigrationAssistantSuggestions,
  buildProtocolStarterTemplates,
  buildScriptRepairSuggestions,
  collectAuthoringIssues,
  groupIssuesByPhaseAction,
  scriptBaseURLError,
  summarizeDataSources,
  summarizeParameters,
  type MigrationAssistantSuggestion,
  type ScriptRepairSuggestion,
} from './scriptAuthoringAssist'
import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'
import { loadObjectRefSuggestions } from './scriptObjectRefCatalog'
import { summarizeActionContext } from './scriptActionContext'
import {
  defaultAssertionForProtocol,
  defaultExtractorForProtocol,
  sanitizeDownloadName,
} from './scriptEditorHelpers'
import {
  EMPTY_ACTION,
  EMPTY_BROWSER_ACTION,
  EMPTY_DATABASE_ACTION,
  EMPTY_GRPC_ACTION,
  EMPTY_DATA_SOURCE,
  EMPTY_JMS_ACTION,
  EMPTY_NETWORK_ACTION,
  EMPTY_PARAMETER,
  EMPTY_SCRIPT,
  EMPTY_SOAP_ACTION,
  EMPTY_WEBSOCKET_ACTION,
  buildReplayStartPayload,
  buildReplayVariableDiffs,
  cloneDataSource,
  createReplaySessionId,
  dedupeHeaders,
  downloadTextFile,
  ensureUniqueParameterName,
  findJsonStringOccurrences,
  formatJsonPathKey,
  fillEmptyHeaders,
  getErrorIssues,
  getErrorMessage,
  getErrorWarnings,
  isDefiniteReplayStartRejection,
  isLikelyParameterValue,
  mergeReplayVariables,
  normalizeHeaders,
  parseReplayVariablesText,
  parseInlineData,
  sanitizeVarName,
  stringifyInlineData,
} from './scriptEditorUtils'
import {
  ACTION_LIST_TAB_ID,
  CENTER_TABS_STORAGE_PREFIX,
  SCRIPT_JSON_TAB_ID,
  buildScriptPhaseActionRows,
  buildDebugRailRenderRows,
  type DebugRailRenderRow,
  debugSequenceKey,
  debugStepPathKey,
  filterScriptPhaseActionRows,
  duplicateActionInList,
  actionTransactionName,
  actionTransactionMarker,
  computeTransactionBands,
  tagActionsTransaction,
  uniqueTransactionName,
  wrapActionsInTransactionMarkers,
  remapIndexAfterMarkerWrap,
  codePathForDebugRow,
  formatDebugPhaseLabel,
  readCenterTabsState,
  scriptActionsForPhase,
  type CenterTabsState,
  type CenterWorkbenchTabId,
  type ScriptActionPhase,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'
import { actionRequestCount, actionExtractorCount, isControlAction, isSourceFunctionAction, isReviewGatedJavaScriptAction, actionExecutionBlockers, actionRequiresReview, actionSelectorFallbackCount, actionTypeSummary, actionRowLabel, headerListToCompatibilityMap } from './scriptActionHelpers'
import { authoringSessionIdempotencyKey, scriptRequiresJavaVuserAuthoringApproval } from './javaVuserApprovalGate'
import { ActionEditor, ValidationEvidencePanel } from './ScriptEditorActionEditor'
import { setHTTPActionVersion } from './httpActionVersionModel'
import type { HTTP3UpgradeScope } from './HTTPVersionReplayEvidence'
import { isBuiltinParameterType, applyParameterTypeDefaults } from './scriptParameterFormat'
import {
  createScriptEditorDispatchers,
  createScriptEditorState,
  scriptEditorReducer,
  selectScriptEditorAsyncOperations,
  selectScriptEditorDocument,
  selectScriptEditorImportReview,
  selectScriptEditorNavigation,
  selectScriptEditorParametersData,
  selectScriptEditorReplayDebug,
  type ActionEditorParameterDraft,
  type CandidateOccurrence,
  type ParameterCandidate,
} from './scriptEditorState'
import {
  authorFocusKey,
  codePathForAuthorFocus,
  findCandidateLiteralRangeInJson,
  formatParameterCandidateOccurrence,
  formatScriptJson,
  jsonPathForActionStepPath,
  normalizeOccurrenceIndex,
  parseScriptJsonDraft,
  phaseActionsForScript,
  resolveAuthorJsonScope,
  resolveAuthorJsonScopeByKey,
  updateNestedAction,
  SCRIPT_JSON_DEFAULT_COMPANION_TAB,
  type AuthorCodeFocus,
  type AuthorJsonScope,
} from './scriptEditorJsonModel'
import {
  ScriptJsonCompanionHeader,
  ScriptJsonDocument,
  ScriptJsonInsightsView,
} from './ScriptJsonAuthoringView'
import {
  useJsonParameterMenu,
  useScriptJsonDraftController,
  useScriptJsonNavigationController,
} from './useScriptJsonAuthoringController'
import { useScriptAuthoringSession, type ResolvedScriptDraft } from './useScriptAuthoringSession'
import { draftFingerprint, scriptSaveAvailability, validationContextKey } from './scriptAuthoringSession'
import {
  buildBrowserHTTPConversionReview,
  replayEvidenceIndex,
  mergeReplayEvent,
  debugBreakpointKey,
  formatDebugLeafSuffix,
  humanizeStepSegment,
  normalizeDebugPhase,
  replayNetworkEntriesForConversion,
  safeParseReplayVariables,
  upsertReplayActivity,
} from './scriptEditorReplayModel'
import { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'
import { omitUnavailableReplayReport, omitUnavailableReplaySession, replayVariablesForInspection, runtimeEvidenceUnavailable } from './runtimeVariableEvidence'
import { EMPTY_DEBUG_RUNTIME, admitDebugRuntimeSession, admitDebugRuntimeDelta, beginDebugRuntime, invalidateDebugRuntime, currentRuntimeLookup, runtimeRequestIsCurrent, runtimeCommandIsAcknowledged, type DebugRuntimeAdmission, type DebugRuntimeCommand, type DebugRuntimeRequest, type RuntimeInspectionMode } from './debugRuntimeState'
import { ScriptReplayDebuggerView } from './ScriptReplayDebuggerView'
import {
  applyValidationRuntimeSettingsMetadata,
  findActionIndexByName,
  findDataSourceIndexByName,
  findFirstParameterIndexByDataSource,
  type Web2ObjectDescriptorRow,
  type Web2SelectorCandidate,
} from './scriptEditorImportModel'
import { useScriptImportReviewController } from './useScriptImportReviewController'
import {
  ImportReviewCommitPanel,
  ImportedMetadataUtilityPanel,
  ImportedRuntimeParityReview,
  ObjectDescriptorReviewDocument,
  ObjectDescriptorUtilityPanel,
  ScriptImportReviewDialogs,
  SourceMappingReviewDocument,
  type AssistantChangeLogItem,
} from './ScriptImportReviewView'
import {
  AutoParameterizationSidebar,
  ExistingParameterDialog,
  JsonOccurrenceReviewDialog,
  ParameterDataDialog,
  ParameterSidebar,
  ParameterizeDialog,
  SelectedParameterDetails,
  type JsonParameterOccurrenceReviewState,
} from './ScriptEditorParameterFeatures'
import {
  ParameterDataCellDraftProvider,
  type ParameterDataCellDraftChange,
} from './ScriptEditorParameterDataSources'
import {
  buildMergedParameterSourceScript,
  summarizeParameterSourceMerge,
} from './scriptEditorParameterDataModel'
import { useEditorDialogRef } from './useEditorDialogRef'
import { ScriptRuntimeSettingsSidebar, ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'
import type { HTTPRuntimeActionType } from './HttpRequestDefaultsEditor'
import {
  transitionScriptProtocol,
} from './scriptEditorRuntimeSettingsModel'
import { useScriptRuntimeSettingsController } from './useScriptRuntimeSettingsController'
import './ScriptEditor.css'

export type { ActionEditorParameterDraft } from './scriptEditorState'
export type { ActionEditorSelectionState } from './scriptEditorJsonModel'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition
type ScriptData = ScriptDefinition

type ValidationEvidence = Pick<ScriptValidationResponse, 'data_source_fit' | 'ai_guardrails' | 'authoring_health'>
type ValidationSidebarResult = {
  tone: 'success' | 'warning' | 'error'
  message: string
  warnings: string[]
  issues: string[]
}

type ReplayActionEvent = BrowserReplayEvent
type ReplayReport = BrowserReplayReport
type WorkbenchMode = 'author' | 'debug'

const DEBUG_TERMINAL_STATUSES = new Set(['completed', 'failed', 'stopped'])

function isFinalizedDebugSession(session: ScriptDebugSession | undefined, report?: ReplayReport | null): boolean {
  if (!session) return Boolean(report?.completed_at)
  // Session.CompletedAt is written only after ReplayScript returns and the
  // worker has drained. A nested/partial report may already carry its own
  // timestamp in the initial stop acknowledgement, so it is not sufficient
  // while an authoritative session snapshot is present.
  return DEBUG_TERMINAL_STATUSES.has(session.status) && Boolean(session.completed_at)
}

function scopedEditableActionsEvidenceKey(
  script: ScriptDefinition,
  projectId?: string | null,
  scriptId?: string | null,
): string {
  return JSON.stringify({
    project_id: projectId ?? script.project_id ?? null,
    script_id: scriptId ?? script.id ?? null,
    actions_key: correlationEditableActionsKey(script),
  })
}

interface CenterWorkbenchTabDescriptor {
  id: CenterWorkbenchTabId
  label: string
  title: string
  closeable: boolean
}

const DEBUG_HIDDEN_CENTER_TABS = new Set<CenterWorkbenchTabId>(['imported-metadata', 'request-body'])

type AuthorRightSidebarKind =
  | 'auto-parameterization'
  | 'signals'
  | 'parameters'
  | 'inspector'
  | 'imported-metadata'
  | 'object-descriptors'
  | 'http-conversion'
  | 'browser-replay'
  | 'runtime-settings'

interface AuthorRightSidebarContext {
  kind: AuthorRightSidebarKind
  title: string
  railLabel: string
  wide?: boolean
}


const AUTHORING_COMPONENT_GROUPS = [
  { kind: 'request_group', label: 'Request Groups' },
  { kind: 'auth_flow', label: 'Auth Flows' },
  { kind: 'login_transaction', label: 'Login Transactions' },
  // Label only: 'data_pool' is persisted API surface for saved script blocks and
  // must not collide with the shared Data Pools feature in the UI.
  { kind: 'data_pool', label: 'Data Blocks' },
  { kind: 'teardown', label: 'Teardown Blocks' },
] as const


const BROWSER_REPLAY_TAB_ID: CenterWorkbenchTabId = 'browser-replay'





/**
 * Only virtualize long lists. Below this count the (cheap) plain render is used, which keeps
 * behavior identical for ordinary scripts and test fixtures; large imports (thousands of
 * actions/mappings) switch to windowed rendering so only visible rows hit the DOM.
 */
const LIST_VIRTUALIZE_THRESHOLD = 100
const OUTLINE_MARKERS_STORAGE_KEY = 'scriptEditorOutlineMarkers'

interface CenterTabbedPaneProps {
  activeTabId: CenterWorkbenchTabId
  ariaLabel: string
  className?: string
  /** What the app's skip link calls this pane, e.g. "Skip to editor". */
  skipLabel: string
  onCloseTab: (tabId: CenterWorkbenchTabId) => void
  onPanelElement: (tabId: CenterWorkbenchTabId, element: HTMLDivElement | null) => void
  onSelectTab: (tabId: CenterWorkbenchTabId) => void
  renderTabPanel: (tabId: CenterWorkbenchTabId) => ReactNode
  tabs: CenterWorkbenchTabDescriptor[]
}

function CenterTabPanel({
  children,
  onPanelElement,
  selected,
  tabId,
}: {
  children: ReactNode
  onPanelElement: (tabId: CenterWorkbenchTabId, element: HTMLDivElement | null) => void
  selected: boolean
  tabId: CenterWorkbenchTabId
}) {
  const panelRef = useCallback((element: HTMLDivElement | null) => {
    if (element) onPanelElement(tabId, element)
  }, [onPanelElement, tabId])

  return (
    <div
      id={`center-tab-panel-${tabId}`}
      ref={panelRef}
      role="tabpanel"
      aria-labelledby={`center-tab-${tabId}`}
      className={`center-tab-panel ${selected ? 'active' : ''}`}
      hidden={!selected}
    >
      {children}
    </div>
  )
}

function CenterTabbedPane({
  activeTabId,
  ariaLabel,
  className = '',
  skipLabel,
  onCloseTab,
  onPanelElement,
  onSelectTab,
  renderTabPanel,
  tabs,
}: CenterTabbedPaneProps) {
  const activeTab = tabs.find(tab => tab.id === activeTabId) || tabs[0]

  const handleTabKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, index: number) => {
    if (tabs.length === 0) return
    if (event.key === 'Delete' && tabs[index]?.closeable) {
      // The visible × is mouse-only; keyboard and screen-reader users close
      // the focused tab with Delete (advertised via aria-keyshortcuts).
      event.preventDefault()
      onCloseTab(tabs[index].id)
      return
    }
    let nextIndex = index
    if (event.key === 'ArrowRight') {
      nextIndex = (index + 1) % tabs.length
    } else if (event.key === 'ArrowLeft') {
      nextIndex = (index - 1 + tabs.length) % tabs.length
    } else if (event.key === 'Home') {
      nextIndex = 0
    } else if (event.key === 'End') {
      nextIndex = tabs.length - 1
    } else {
      return
    }
    event.preventDefault()
    onSelectTab(tabs[nextIndex].id)
  }

  return (
    <section className={`center-tabbed-pane ${className}`} aria-label={ariaLabel} tabIndex={-1} data-skip-target data-skip-label={skipLabel}>
      <TabList className="center-tab-strip" role="tablist" aria-label={`${ariaLabel} tabs`} onKeyDown={handleTabKey}>
        {tabs.map((tab, index) => {
          const selected = tab.id === activeTab.id
          const tabButtonId = `center-tab-${tab.id}`
          const panelId = `center-tab-panel-${tab.id}`
          return (
            <div key={tab.id} role="presentation" className={`center-tab-item ${selected ? 'active' : ''} ${!tab.closeable ? 'pinned' : ''}`}>
              {/* The label is the whole name, so no tooltip repeats it. */}
              <button
                type="button"
                id={tabButtonId}
                role="tab"
                aria-controls={panelId}
                aria-selected={selected}
                aria-keyshortcuts={tab.closeable ? 'Delete' : undefined}
                className="center-tab-button"
                tabIndex={selected ? 0 : -1}
                onClick={() => onSelectTab(tab.id)}
                onKeyDown={event => handleTabKeyDown(event, index)}
              >
                <span className="center-tab-label">{tab.label}</span>
              </button>
              {tab.closeable && (
                <button
                  type="button"
                  className="center-tab-close"
                  aria-hidden="true"
                  tabIndex={-1}
                  title={`Close ${tab.label}`}
                  onClick={() => onCloseTab(tab.id)}
                >
                  ×
                </button>
              )}
            </div>
          )
        })}
      </TabList>
      <div className="center-tab-panels">
        {tabs.map(tab => {
          const selected = tab.id === activeTab.id
          return (
            <CenterTabPanel
              key={tab.id}
              onPanelElement={onPanelElement}
              selected={selected}
              tabId={tab.id}
            >
              {renderTabPanel(tab.id)}
            </CenterTabPanel>
          )
        })}
      </div>
    </section>
  )
}

function normalizeWarnings(messages?: string[]) {
  if (!Array.isArray(messages)) return []
  return Array.from(new Set(messages.map(message => message.trim()).filter(Boolean)))
}

type DebugTopbarIconKind = 'run' | 'pause' | 'step' | 'stepOver' | 'loop' | 'stop' | 'break'

function DebugTopbarIcon({ kind }: { kind: DebugTopbarIconKind }) {
  switch (kind) {
    case 'run':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <polygon points="8 5 18 12 8 19" fill="currentColor" stroke="none" />
        </svg>
      )
    case 'pause':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <rect x="7" y="5" width="3.5" height="14" rx="1" fill="currentColor" stroke="none" />
          <rect x="13.5" y="5" width="3.5" height="14" rx="1" fill="currentColor" stroke="none" />
        </svg>
      )
    case 'step':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <path d="M6 5v14" />
          <path d="M9 12h8" />
          <path d="M13 8l4 4-4 4" />
        </svg>
      )
    case 'stepOver':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <path d="M6 17v-4a6 6 0 0 1 10.5-4" />
          <path d="M15 5h4v4" />
          <path d="M18 13v6" />
        </svg>
      )
    case 'loop':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <path d="M7 8h8a4 4 0 0 1 4 4" />
          <path d="M15 5l4 3-4 3" />
          <path d="M17 16H9a4 4 0 0 1-4-4" />
          <path d="M9 19l-4-3 4-3" />
        </svg>
      )
    case 'stop':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none" />
        </svg>
      )
    case 'break':
      return (
        <svg viewBox="0 0 24 24" focusable="false">
          <path d="M12 4l8 15H4z" />
          <path d="M12 9v4" />
          <path d="M12 17h.01" />
        </svg>
      )
  }
}

type EditorRailActionIconKind = 'undo' | 'redo' | 'save' | 'close'

function EditorRailActionIcon({ kind }: { kind: EditorRailActionIconKind }) {
  switch (kind) {
    case 'undo':
      return (
        <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
          <path d="M9 7H4v5" />
          <path d="M5 11a8 8 0 1 1 2.3 5.7" />
        </svg>
      )
    case 'redo':
      return (
        <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
          <path d="M15 7h5v5" />
          <path d="M19 11a8 8 0 1 0-2.3 5.7" />
        </svg>
      )
    case 'save':
      return (
        <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
          <path d="M5 4h12l2 2v14H5z" />
          <path d="M8 4v6h8V4" />
          <path d="M8 20v-6h8v6" />
        </svg>
      )
    case 'close':
      return (
        <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
          <path d="M6 6l12 12" />
          <path d="M18 6L6 18" />
        </svg>
      )
  }
}

interface ReplayWSMessage extends WSMessage {
  runtime_evidence_unavailable?: boolean
  replay_session_id?: string
  debug_session_id?: string
  session_incarnation?: string
  state_revision?: string
  status?: ScriptDebugSession['status']
  event_id?: string
  event_index?: number
  action?: ScriptDebugAction
  event?: ReplayActionEvent
  report?: ReplayReport
  session?: ScriptDebugSession
}

type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition

type PendingParameterDataCellEdit = Omit<ParameterDataCellDraftChange, 'value'> & { value: string }

function parameterDataCellEditKey(edit: Omit<ParameterDataCellDraftChange, 'value'>): string {
  return `${edit.sourceIndex}:${edit.rowIndex}:${edit.columnIndex}`
}

function applyPendingParameterDataCellEdits(
  currentScript: ScriptData,
  edits: PendingParameterDataCellEdit[],
): ScriptData {
  if (edits.length === 0) return currentScript
  const dataSources = [...(currentScript.data_sources || [])]
  const editsBySource = new Map<number, PendingParameterDataCellEdit[]>()
  edits.forEach((edit) => {
    editsBySource.set(edit.sourceIndex, [...(editsBySource.get(edit.sourceIndex) || []), edit])
  })

  editsBySource.forEach((sourceEdits, sourceIndex) => {
    const source = dataSources[sourceIndex]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.map(row => [...row])
    sourceEdits.forEach((edit) => {
      while (nextRows.length <= edit.rowIndex) nextRows.push(Array(columns.length).fill(''))
      while (nextRows[edit.rowIndex].length < columns.length) nextRows[edit.rowIndex].push('')
      nextRows[edit.rowIndex][edit.columnIndex] = edit.value
    })
    dataSources[sourceIndex] = {
      ...source,
      inline_data: stringifyInlineData(source, columns, nextRows),
    }
  })
  return { ...currentScript, data_sources: dataSources }
}


interface ActionEditorParameterRequest {
  selectedText: string
  requestedName: string
  existingName?: string | null
  parameterType?: string
  typeConfig?: Partial<ParameterConfig>
  reuseExisting?: boolean
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
  initialAuthoring?: ScriptAuthoringBaseline | null
  initialRetainedDraft?: ScriptData | null
  initialRetainedNeedsReview?: boolean
  initialRetainedJsonText?: string
  initialRetainedCreatedSecretNames?: string[]
  initiallyDirty?: boolean
  initialFocusedActionName?: string | null
  initialFocusedSection?: string | null
  initialFocusedDataSourceName?: string | null
  initialFocusedRuntimeArea?: string | null
  projectId?: string
  folderId?: string | null
  /** Names of the other scripts in this script's folder; a repeat gets a hint, not a block. */
  takenScriptNames?: string[]
  onSave?: (script: StoredScript, options?: ScriptSaveOptions) => void
  onClose?: () => void
  /**
   * Where the editor returns to, besides closing (an import review it was
   * opened from). The source stays open behind the editor: its guards
   * (retainedGuardIds) are kept when Close or the return leads back to it.
   */
  returnTarget?: { label: string; description?: string; retainedGuardIds?: readonly string[]; onReturn: () => void } | null
  /** Who created and last changed the stored script; History opens from the editor's More menu. */
  activity?: ObjectActivitySummary
}

function scriptHasAuthoredActions(script: ScriptData): boolean {
  return (script.init?.length || 0) > 0 || script.actions.length > 0 || (script.teardown?.length || 0) > 0
}


const SAVED_SCRIPT_OPENING = 'This script was saved and is opening. Edits made after it was saved are not kept.'

export default function ScriptEditor({ scriptId, initialScript, initialAuthoring, initialRetainedDraft, initialRetainedNeedsReview = false, initialRetainedJsonText, initialRetainedCreatedSecretNames, initiallyDirty = false, initialFocusedActionName, initialFocusedSection, initialFocusedDataSourceName, initialFocusedRuntimeArea, projectId, folderId, takenScriptNames, onSave, onClose, returnTarget, activity }: Props) {
  const [authorOverview, setAuthorOverview] = useState(false)
  const [showHistory, setShowHistory] = useState(false)
  const [insertSeed, setInsertSeed] = useState<Action | null>(null)
  const [insertPhase, setInsertPhase] = useState<ScriptActionPhase>('action')
  const [insertPosition, setInsertPosition] = useState<InsertPosition>('end')
  // A replayed value about to replace its recorded copies in later steps, under review.
  const [correlationReview, setCorrelationReview] = useState<{ source: StepPosition; sourceName: string; value: string; boundaries: { left: string; right: string }; suggestedName: string; taken: ReadonlySet<string>; uses: ValueUse[] } | null>(null)
  // Where a linked template's user steps go, beside the step it left selected
  // (that step object: an edit to it, or the script's JSON, retires the spot).
  const [templateSpot, setTemplateSpot] = useState<{ action: Action; position: InsertPosition } | null>(null)
  const [showSavedDiff, setShowSavedDiff] = useState(false)
  const [showAuthorProblems, setShowAuthorProblems] = useState(false)
  const [datasetChange, setDatasetChange] = useState<DatasetChange | null>(null)

  const api = useApi()
  const [secretBindingsOpen, setSecretBindingsOpen] = useState(false)
  // A Shared assets section open over the editor (saved blocks, or a table a scenario binds).
  const [sharedAssetsDialog, setSharedAssetsDialog] = useState<{ section: SharedAssetsSection; resourceId?: string } | null>(null)
  const saveAndConfigureRequestedRef = useRef(false)
  const pendingCreatedSecretNamesRef = useRef<string[]>([...(initialRetainedCreatedSecretNames || [])])
  const [scriptSecretStatus, setScriptSecretStatus] = useState<ScriptSecretStatus | null>(null)
  const scriptSecretStatusRequestRef = useRef(0)
  const [projectSecretProbe, setProjectSecretProbe] = useState<{
    projectId: string
    namesKey: string
    statuses: ProjectSecretStatus[]
    state: 'loading' | 'ready' | 'unavailable'
  } | null>(null)
  const projectSecretProbeRequestRef = useRef(0)
  const [projectSecretProbeRetryGeneration, setProjectSecretProbeRetryGeneration] = useState(0)
  const replayMissingProjectSecretsRef = useRef<string[]>([])
  const replaySecretStatusKnownRef = useRef(true)
  const refreshScriptSecretStatus = useCallback(async () => {
    const requestId = ++scriptSecretStatusRequestRef.current
    if (!scriptId || typeof api.getScriptSecretStatus !== 'function') {
      setScriptSecretStatus(null)
      return
    }
    try {
      const status = await api.getScriptSecretStatus(scriptId)
      if (scriptSecretStatusRequestRef.current === requestId) {
        setScriptSecretStatus(status)
      }
    } catch {
      if (scriptSecretStatusRequestRef.current === requestId) {
        setScriptSecretStatus(null)
      }
    }
  }, [api, scriptId])
  useEffect(() => {
    void refreshScriptSecretStatus()
    return () => {
      scriptSecretStatusRequestRef.current += 1
    }
  }, [refreshScriptSecretStatus])
  const [scriptRecordingStatus, setScriptRecordingStatus] = useState<ScriptRecordingStatus | null>(null)
  const [scriptRecordingStatusState, setScriptRecordingStatusState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [scriptRecordingStatusRetryGeneration, setScriptRecordingStatusRetryGeneration] = useState(0)
  const [issueDialog, setIssueDialog] = useState<{ title: string; issues: IssueDetail[] } | null>(null)
  // Object-repository logical names for the per-action object_ref autocomplete.
  const [objectRefSuggestions, setObjectRefSuggestions] = useState<string[]>([])
  useEffect(() => {
    if (!projectId || typeof api.listObjectRepositories !== 'function') { setObjectRefSuggestions([]); return }
    let cancelled = false
    void loadObjectRefSuggestions(projectId, api.listObjectRepositories)
      .then(suggestions => {
        if (cancelled) return
        setObjectRefSuggestions(suggestions)
      })
      .catch(() => { if (!cancelled) setObjectRefSuggestions([]) })
    return () => { cancelled = true }
  }, [api, projectId])
  // Action-type catalog from GET /api/v1/dsl/actions: powers JSON-editor
  // completion and the catalog-driven action form inputs. The state bump
  // re-renders once the catalog arrives; helpers fall back to built-in
  // lists until then.
  const [dslCatalogReady, setDslCatalogReady] = useState(false)
  useEffect(() => {
    let cancelled = false
    void ensureDslActionCatalog().then(catalog => {
      if (!cancelled && catalog) setDslCatalogReady(true)
    })
    return () => {
      cancelled = true
    }
  }, [])
  // Holds the latest resolved debug-run variables so the {{...}} hover can show
  // concrete values without rebuilding the (otherwise stable) editor extensions.
  const replayVariableInspectorRef = useRef<Record<string, string> | null>(null)
  const replayVariableQualificationRef = useRef('Current paused state')
  const [unavailableRuntimeSessionId, setUnavailableRuntimeSessionId] = useState<string | null>(null)
  const runtimeAdmissionRef = useRef<DebugRuntimeAdmission>(EMPTY_DEBUG_RUNTIME)
  const debugMutationSequenceRef = useRef(0)
  const [runtimeAdmission, setRuntimeAdmission] = useState<DebugRuntimeAdmission>(EMPTY_DEBUG_RUNTIME)
  const [runtimeInspectionMode, setRuntimeInspectionMode] = useState<RuntimeInspectionMode>('current')
  const [runtimeFrameSourcePath, setRuntimeFrameSourcePath] = useState<string | null>(null)
  const publishRuntimeAdmission = useCallback((state: DebugRuntimeAdmission) => {
    runtimeAdmissionRef.current = state
    setRuntimeAdmission(state)
    if (state.privacyUnavailable && state.sessionId) setUnavailableRuntimeSessionId(state.sessionId)
  }, [])
  const invalidateRuntimeAuthority = useCallback((reason: string) => {
    publishRuntimeAdmission(invalidateDebugRuntime(runtimeAdmissionRef.current, reason))
  }, [publishRuntimeAdmission])
  const runtimeRequest = useCallback((): DebugRuntimeRequest => ({ sessionId: runtimeAdmissionRef.current.sessionId, generation: runtimeAdmissionRef.current.generation }), [])
  const admitRuntimeSession = useCallback((session: ScriptDebugSession, source: 'start' | 'get' | 'mutation' | 'socket', request?: DebugRuntimeRequest) => {
    const result = admitDebugRuntimeSession(runtimeAdmissionRef.current, session, source, request)
    publishRuntimeAdmission(result.state)
    return result.accepted
  }, [publishRuntimeAdmission])

  const validateDraftForAssist = api.validateDraftScript
  const scriptAssistExtensions = useMemo(
    () => buildDslAssistExtensions(
      script => validateDraftForAssist(script as ScriptDefinition, projectId, folderId ?? undefined),
      () => replayVariableInspectorRef.current,
      () => replayVariableQualificationRef.current,
    ),
    [validateDraftForAssist, projectId, folderId],
  )
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const handleSaveRef = useRef<() => Promise<boolean>>(async () => false)
  const openProjectSecrets = useCallback(() => {
    if (saveInFlightScopeRef.current || secretBindingsOpen) return
    if (scriptId) {
      setSecretBindingsOpen(true)
      return
    }
    saveAndConfigureRequestedRef.current = true
    void handleSaveRef.current()
  }, [scriptId, secretBindingsOpen])
  const {
    activeAction,
    activeReplaySessionId,
    browserReplaySession: retainedBrowserReplaySession,
    copiedReplaySnapshotSection,
    dataEditorMode,
    dataSourceHistory,
    error,
    editorHistory,
    expandedReplayBrowserArtifacts,
    expandedReplayEvent,
    baselineJsonRef,
    hydrateEditorState,
    jsonDirty,
    jsonText,
    markBaseline,
    openDataEditor,
    parameterCandidates,
    pendingParameterDraft,
    replayActivity: retainedReplayActivity,
    replayEventRefs,
    replayOptions,
    replayReport: retainedReplayReport,
    replayScreenshotSelection,
    replaySeedVariables,
    replaying,
    saving,
    script,
    importBaselineScript,
    setActiveAction,
    setActiveReplaySessionId,
    setBrowserReplaySession,
    setCopiedReplaySnapshotSection,
    setDataEditorMode,
    setDataSourceHistory,
    setError,
    setEditorHistory,
    setExpandedReplayBrowserArtifacts,
    setExpandedReplayEvent,
    setJsonDirty,
    setJsonText,
    setOpenDataEditor,
    setOpenSections,
    setParameterCandidates,
    setPendingParameterDraft,
    setReplayActivity,
    setReplayOptions,
    setReplayReport,
    setReplayScreenshotSelection,
    setReplaySeedVariables,
    setReplaying,
    setSaving,
    setScript: editScript,
    restoreScript,
    setSuccess,
    setValidating,
    setWarnings,
    success,
    validating,
    warnings,
    closeReplayScreenshot,
  } = useScriptEditorState(initialRetainedDraft || initialScript)
  const runtimeVariablesUnavailable = runtimeEvidenceUnavailable(retainedBrowserReplaySession, retainedReplayReport)
    || unavailableRuntimeSessionId !== null || runtimeAdmission.privacyUnavailable
  // Project before any controller, history or export consumer can use a stale
  // snapshot. The sticky signal is scoped to the current run.
  const browserReplaySession = useMemo(() => {
    const session = retainedBrowserReplaySession && runtimeVariablesUnavailable ? omitUnavailableReplaySession(retainedBrowserReplaySession) : retainedBrowserReplaySession
    if (!session || !runtimeAdmission.incarnation || !runtimeAdmission.revision) return session
    return { ...session, session_incarnation: runtimeAdmission.incarnation, state_revision: runtimeAdmission.revision,
      runtime_state: runtimeAdmission.runtime || { version: 1 as const, kind: 'full' as const, session_id: runtimeAdmission.sessionId!, session_incarnation: runtimeAdmission.incarnation, state_revision: runtimeAdmission.revision, state: 'unavailable' as const,
        reason: runtimeVariablesUnavailable ? 'runtime_evidence_unavailable' as const : runtimeAdmission.terminal ? 'session_terminal' as const : 'snapshot_unavailable' as const, snapshot: null } }
  }, [retainedBrowserReplaySession, runtimeVariablesUnavailable, runtimeAdmission])
  const replayReport = useMemo(() => retainedReplayReport && runtimeVariablesUnavailable
    ? omitUnavailableReplayReport(retainedReplayReport) : retainedReplayReport,
  [retainedReplayReport, runtimeVariablesUnavailable])
  const replayActivity = useMemo(() => runtimeVariablesUnavailable
    ? retainedReplayActivity.map(activity => ({ id: activity.id, kind: activity.kind, timestamp: activity.timestamp,
      success: activity.success, title: 'Runtime evidence unavailable' })) : retainedReplayActivity,
  [retainedReplayActivity, runtimeVariablesUnavailable])
  useEffect(() => {
    if (!runtimeEvidenceUnavailable(retainedBrowserReplaySession, retainedReplayReport)) return
    const sessionId = activeReplaySessionId || retainedBrowserReplaySession?.id || retainedReplayReport?.replay_session_id
    if (sessionId) setUnavailableRuntimeSessionId(sessionId)
  }, [activeReplaySessionId, retainedBrowserReplaySession, retainedReplayReport])
  const beforeDocumentEditRef = useRef<() => void>(() => undefined)
  const setScript = useCallback((update: React.SetStateAction<ScriptData>) => { beforeDocumentEditRef.current(); editScript(update) }, [editScript])
  const editorMountedRef = useRef(false)
  useEffect(() => {
    editorMountedRef.current = true
    return () => {
      editorMountedRef.current = false
    }
  }, [])
  const exportGeneratedTextFile = useCallback(async (
    content: string,
    filename: string,
    savedMessage = `Exported ${filename}.`,
  ): Promise<void> => {
    setError('')
    setSuccess('')
    try {
      const outcome = await downloadTextFile(content, filename)
      if (outcome === 'cancelled') {
        setSuccess('Export cancelled.')
        return
      }
      setSuccess(savedMessage)
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, `Failed to export ${filename}`))
    }
  }, [setError, setSuccess])
  const [replayStartPending, setReplayStartPending] = useState(false)
  const replayStartInFlightRef = useRef<object | null>(null)
  const replayStartAttemptRef = useRef<{
    sessionId: string
    terminalObserved: boolean
    awaitingReconciliation: boolean
    notFoundCount: number
  } | null>(null)
  const [replayEditableActionsKey, setReplayEditableActionsKey] = useState<string | null>(null)
  // Where the replayed script's steps sat, so its evidence still finds them after checks are added.
  const [replayLayoutKey, setReplayLayoutKey] = useState<string | null>(null)
  // The fingerprint of the exact document a replay or debug run was started
  // with: its evidence is current only while the editor holds that document.
  const [replayDocumentFingerprint, setReplayDocumentFingerprint] = useState<string | null>(null)
  const [focusParameterIdx, setFocusParameterIdx] = useState<number | null>(null)
  const [selectedParameterIdx, setSelectedParameterIdx] = useState<number | null>(null)
  // Holds the pending selection while the Parameterize choice dialog (File vs Built-in Function)
  // is open. Cleared once the user picks a branch.
  const [parameterizeChoice, setParameterizeChoice] = useState<ActionEditorParameterDraft | null>(null)
  const [existingParameterChoice, setExistingParameterChoice] = useState<ActionEditorParameterDraft | null>(null)
  const [jsonOccurrenceReview, setJsonOccurrenceReview] = useState<JsonParameterOccurrenceReviewState | null>(null)
  // Fresh import drafts (no scriptId yet) want the import-review aids expanded for verification;
  // long-lived edits of a saved script collapse them by default to keep the outline focused.
  const [reusableBlocksCollapsed, setReusableBlocksCollapsed] = useState(Boolean(scriptId))
  // How many seed rows the auto_params data source gets. >1 lets each VUser draw a distinct
  // value once the operator fills the extra rows (the first row holds the detected literal).
  const [autoParamSeedRows, setAutoParamSeedRows] = useState(1)
  // Multi-select of outline actions/sub-actions for "Save as block" (project-scoped reusable block).
  const [blockSelection, setBlockSelection] = useState<Set<string>>(new Set())
  // Inline naming draft when wrapping the block selection in a transaction
  // (null = not naming). Pre-filled with a unique default; null cancels.
  const [txnWrapDraft, setTxnWrapDraft] = useState<string | null>(null)
  // 'span' = lr_start/end_transaction marker pair (wall-clock span incl. think
  // time); 'grouped' = tag each selected action under one name (per-action).
  const [txnWrapMode, setTxnWrapMode] = useState<'span' | 'grouped'>('span')
  const [showSaveBlockModal, setShowSaveBlockModal] = useState(false)
  const [saveBlockName, setSaveBlockName] = useState('')
  const [saveBlockKind, setSaveBlockKind] = useState('request_group')
  const [savingBlock, setSavingBlock] = useState(false)
  const isBrowserScript = script.protocol === 'browser'
  const isJmsScript = script.protocol.startsWith('jms')
  const isSoapScript = script.protocol === 'soap'
  const isWebSocketScript = script.protocol === 'websocket'
  const isGrpcScript = script.protocol === 'grpc'
  const isDatabaseScript = script.protocol === 'database'
  const isNetworkScript = script.protocol === 'network'
  const canEditProtocol = !scriptId && !script.metadata?.import && !scriptHasAuthoredActions(script)
  // True when editing a saved script (has an id) vs. authoring a brand-new draft. Drives
  // context-aware sidebar emphasis: scaffolding aids lead for new scripts, review/parameter
  // aids lead when editing.
  const isEditingExisting = Boolean(scriptId)
  const duplicateScriptName = Boolean(script.name?.trim()) && (takenScriptNames || []).some(name => name.trim().toLowerCase() === script.name.trim().toLowerCase())
  const scriptPhaseActionRows = useMemo(() => buildScriptPhaseActionRows(script), [script])
  const replayDebuggerController = useScriptReplayDebuggerController({
    browserReplaySession,
    debugActionRows: scriptPhaseActionRows,
    expandedReplayEvent,
    replayReport,
    replaying,
    scriptId,
  })
  const {
    debugBreakpoints,
    debugDetailCollapsed,
    debuggerSettingsBtnRef,
    debuggerSettingsOpen,
    selectedDebugRow,
    selectedReplayEvent,
    setDebugBreakpoints,
    setDebugDetailCollapsed,
    setDebuggerBottomTab,
    setDebuggerSettingsOpen,
    setDebuggerTab,
  } = replayDebuggerController
  const jsonNavigationController = useScriptJsonNavigationController({
    jsonText,
    parameterCandidates,
    script,
  })
  const {
    activeAutoParameterCandidate,
    activeAutoParameterOccurrence,
    activeAutoParameterPath,
    authorCodeFocus,
    autoParameterSidebarActive,
    jsonLineJumpRequest,
    normalizedAutoParameterOccurrenceIndex,
    outlineActionFilter,
    outlineActionListCollapsed,
    parameterMatchRanges,
    parameterPaneShowAuto,
    parameterReferenceCounts,
    requestJsonLineJump,
    scriptJsonSidebarTab,
    selectedAuthorJsonScope,
    selectedAuthorJsonText,
    selectedOutlineParameter,
    selectedOutlineParameterExists,
    selectedOutlineParameterIndex,
    selectedOutlineParameterName,
    selectedParameterDataInfo,
    selectedParameterReferenceLines,
    setActiveAutoParameterCandidateId,
    setActiveAutoParameterOccurrenceIndex,
    setAuthorCodeFocus,
    setOutlineActionFilter,
    setOutlineActionListCollapsed,
    setParameterPaneShowAuto,
    setScriptJsonSidebarTab,
    setSelectedOutlineParameterName,
    setShowFullScriptJson,
    showFullScriptJson,
  } = jsonNavigationController
  const historyContext = useRef(new Map<string, { focus: typeof authorCodeFocus; active: number | null; breakpoints: ScriptDebugBreakpoint[] }>())
  beforeDocumentEditRef.current = () => {
    const key = formatScriptJson(script)
    historyContext.current.set(key, structuredClone({ focus: authorCodeFocus, active: activeAction, breakpoints: debugBreakpoints }))
    if (historyContext.current.size > 100) historyContext.current.delete(historyContext.current.keys().next().value!)
  }
  // The outline tree: phases, transactions and the steps they measure, with
  // a sub-transaction around one step folded into that step's row.
  const [outlineShowMarkers, setOutlineShowMarkers] = useState(() => {
    try { return window.localStorage.getItem(OUTLINE_MARKERS_STORAGE_KEY) === 'shown' } catch { return false }
  })
  const changeOutlineShowMarkers = (show: boolean) => {
    setOutlineShowMarkers(show)
    try { window.localStorage.setItem(OUTLINE_MARKERS_STORAGE_KEY, show ? 'shown' : 'folded') } catch { /* a viewer convenience only */ }
  }
  const outlineModel = useMemo(() => buildOutlineModel(scriptPhaseActionRows, { showMarkers: outlineShowMarkers }), [outlineShowMarkers, scriptPhaseActionRows])
  const outlineNaturalWidthRef = useRef<(() => number) | null>(null)
  // The outline filter narrows the rendered rows by name/type/target, and by the
  // names the outline shows (Class.method, a folded sub-transaction's name), so
  // the action list and the outline find the same steps. It does not mutate the
  // script; rows keep their original actionIndex so every row handler
  // (select/move/breakpoint) stays correct over the filtered view.
  const filteredActionRows = useMemo(() => {
    const needle = outlineActionFilter.trim().toLowerCase()
    if (!needle) return scriptPhaseActionRows
    const byFields = new Set(filterScriptPhaseActionRows(scriptPhaseActionRows, outlineActionFilter))
    return scriptPhaseActionRows.filter(row => {
      if (byFields.has(row)) return true
      const step = outlineModel.stepsByKey.get(debugSequenceKey(row.phase, row.actionIndex))
      return Boolean(step && [step.label, step.measuredAs?.name].some(text => text?.toLowerCase().includes(needle)))
    })
  }, [outlineActionFilter, outlineModel, scriptPhaseActionRows])
  // Per-row transaction band membership (left rail bracketing a transaction's rows).
  const transactionBands = useMemo(() => computeTransactionBands(filteredActionRows), [filteredActionRows])
  // Container actions (http_sequence / control_*) the operator has expanded in the
  // Author outline to reveal their nested sub-requests / steps.
  const editorElementRef = useRef<HTMLDivElement>(null)
  const { updateOwner: updateInspectorFocusOwner, requestFocus: requestDeferredInspectorFocus } = useDeferredScriptInspectorFocus(editorElementRef)
  const requestInspectorFocus = useCallback((focus: AuthorCodeFocus, locate: (panel: HTMLElement) => HTMLElement | null = panel => panel.querySelector('[data-inspector-focus]'), document: ScriptDefinition = script, scroll = false) => {
    const scope = resolveAuthorJsonScope(document, focus)
    if (scope) requestDeferredInspectorFocus({ scriptId, projectId, folderId, initialScript, path: scope.path, action: scope.action }, locate, scroll)
  }, [folderId, initialScript, projectId, requestDeferredInspectorFocus, script, scriptId])
  const [inspectorFocusSection, setInspectorFocusSection] = useState('request')
  const [narrowAuthorPane, setNarrowAuthorPane] = useState<'outline' | 'editor' | 'inspector'>('editor')
  useEffect(() => {
    if (narrowAuthorPane === 'inspector') editorElementRef.current?.querySelector<HTMLElement>('[data-inspector-focus]')?.focus()
  }, [narrowAuthorPane])
  const [expandedAuthorContainers, setExpandedAuthorContainers] = useState<Set<string>>(() => new Set())
  useEffect(() => {
    if (!authorCodeFocus?.stepPath?.length) return
    const rootKey = debugSequenceKey(authorCodeFocus.phase, authorCodeFocus.actionIndex)
    setExpandedAuthorContainers(previous => {
      const next = new Set(previous)
      next.add(rootKey)
      for (let depth = 1; depth < authorCodeFocus.stepPath!.length; depth++) next.add(`${rootKey}|${authorCodeFocus.stepPath!.slice(0, depth).join('/')}`)
      return next.size === previous.size ? previous : next
    })
  }, [authorCodeFocus])
  const toggleAuthorContainer = useCallback((key: string) => {
    setExpandedAuthorContainers(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])
  // The flattened, expansion-aware outline rows (containers + their nested children).
  // Collapsed by default, so a script with no expanded containers renders exactly
  // the flat top-level list as before.
  const authorRenderRows = useMemo(
    () => buildDebugRailRenderRows(filteredActionRows, expandedAuthorContainers),
    [filteredActionRows, expandedAuthorContainers],
  )
  const selectedBlockScopes = useMemo(
    () => Array.from(blockSelection)
      .map(key => resolveAuthorJsonScopeByKey(script, key))
      .filter((scope): scope is AuthorJsonScope => Boolean(scope)),
    [blockSelection, script],
  )
  const selectedTopLevelActionIndices = useMemo(
    () => selectedBlockScopes
      .filter(scope => scope.phase === 'action' && !scope.subAction)
      .map(scope => scope.actionIndex)
      .sort((a, b) => a - b),
    [selectedBlockScopes],
  )
  const canWrapBlockSelection = selectedBlockScopes.length > 0 && selectedTopLevelActionIndices.length === selectedBlockScopes.length
  // Virtualize the Author Action List so a multi-thousand-action import renders only the
  // visible rows instead of one DOM subtree per action.
  const actionListScrollRef = useRef<HTMLDivElement | null>(null)
  const virtualizeActions = authorRenderRows.length > LIST_VIRTUALIZE_THRESHOLD
  const actionListVirtualizer = useVirtualizer({
    count: authorRenderRows.length,
    getScrollElement: () => actionListScrollRef.current,
    estimateSize: () => 45,
    overscan: 12,
  })
  const actionRowsToRender: Array<{ item: DebugRailRenderRow; virtualItem: VirtualItem | null }> = virtualizeActions
    ? actionListVirtualizer.getVirtualItems().map(virtualItem => ({ item: authorRenderRows[virtualItem.index], virtualItem }))
    : authorRenderRows.map(item => ({ item, virtualItem: null }))
  const httpConversionReview = useMemo(
    () => buildBrowserHTTPConversionReview(script, replayReport, browserReplaySession),
    [browserReplaySession, replayReport, script],
  )
  const [httpConversionPreview, setHttpConversionPreview] = useState<BrowserToHTTPPreviewResponse | null>(null)
  const [httpConversionGenerating, setHttpConversionGenerating] = useState(false)
  const [httpConversionError, setHttpConversionError] = useState('')
	const httpConversionRequestRef = useRef(0)
	const httpConversionBlockingFindings = useMemo(() => {
	  if (!httpConversionPreview) return []
	  const findings = [
		...(httpConversionPreview.review.blocking_findings || []),
		...(httpConversionPreview.governance.blockers || []),
	  ]
	  return findings.filter((finding, index) => findings.findIndex(candidate => (
		candidate.severity === finding.severity
		&& candidate.summary === finding.summary
		&& candidate.action === finding.action
	  )) === index)
	}, [httpConversionPreview])

	useEffect(() => {
	  // A preview is evidence for one exact editable script and replay snapshot.
	  // Invalidate both completed and in-flight results as soon as any source
	  // input changes so a late response cannot be presented as current.
	  httpConversionRequestRef.current += 1
	  setHttpConversionPreview(null)
	  setHttpConversionGenerating(false)
	  setHttpConversionError('')
	}, [activeReplaySessionId, browserReplaySession, projectId, replayReport, script])
  const [workspaceTab, setWorkspaceTab] = useState<WorkbenchMode>(() => {
    try {
      const route = new URLSearchParams(window.location.hash.split('?')[1] || '')
      if (route.get('workspace') === 'debug') return 'debug'
      if (route.get('workspace') === 'author' || route.get('mode') === 'edit') return 'author'
      const value = window.localStorage.getItem('scriptEditorWorkbenchMode') || window.localStorage.getItem('scriptEditorWorkspaceTab')
      return value === 'debug' ? 'debug' : 'author'
    } catch {
      return 'author'
    }
  })
  const centerTabsStorageKey = useMemo(
    () => `${CENTER_TABS_STORAGE_PREFIX}:${scriptId || 'draft'}`,
    [scriptId],
  )
  const [centerTabsState, setCenterTabsState] = useState<CenterTabsState>(() => readCenterTabsState(centerTabsStorageKey))
  const [centerTabsHydratedKey, setCenterTabsHydratedKey] = useState(centerTabsStorageKey)
  const [centerTabPanelElements, setCenterTabPanelElements] = useState<Partial<Record<CenterWorkbenchTabId, HTMLDivElement | null>>>({})
  const [authorOutlineCollapsed, setAuthorOutlineCollapsed] = useState(false)
  const [authorRightSidebarCollapsed, setAuthorRightSidebarCollapsed] = useState(false)
  const handleImportWorkspaceMaximizedChange = useCallback((maximized: boolean) => {
    setAuthorOutlineCollapsed(maximized)
    setAuthorRightSidebarCollapsed(maximized)
  }, [])
  const importReviewController = useScriptImportReviewController({
    activeCenterTabId: centerTabsState.activeTabId,
    editorHistory,
    importBaselineScript,
    onWorkspaceMaximizedChange: handleImportWorkspaceMaximizedChange,
    script,
  })
  const {
    focusSourceMappingForAction,
    importMetadata,
    importedParameterFileMappings,
    importSummary,
    objectDescriptorReview,
    runtimeSettingParitySummary,
    setShowMetadataJsonDialog,
    sourceReview,
  } = importReviewController
  // The script's own documents are fixed tabs, always present in this order
  // (the import, object-descriptor and browser ones when they apply); only the
  // selected action's request body opens and closes as needed.
  const centerTabDescriptors = useMemo<CenterWorkbenchTabDescriptor[]>(() => {
    const descriptors: CenterWorkbenchTabDescriptor[] = [
      { id: ACTION_LIST_TAB_ID, label: 'Action editor', title: 'Action editor', closeable: false },
      { id: SCRIPT_JSON_TAB_ID, label: 'Source', title: 'Source JSON', closeable: false },
      ...(isBrowserScript ? [{ id: BROWSER_REPLAY_TAB_ID, label: 'Browser Replay', title: 'Browser Replay Cockpit', closeable: false }] : []),
      { id: 'runtime-settings', label: 'Runtime Settings', title: 'Runtime Settings', closeable: false },
      { id: 'parameter-data', label: 'Parameters', title: 'Parameters', closeable: false },
      // The full imported metadata JSON is not a tab: Source opens it on
      // demand, so its multi-MB read-only editor never mounts unasked.
      ...(importMetadata ? [{ id: 'imported-metadata' as const, label: 'Source Mapping', title: 'Source Mapping', closeable: false }] : []),
      ...(objectDescriptorReview.rows.length > 0 ? [{ id: 'object-descriptors' as const, label: 'Object Descriptors', title: 'Object Descriptors', closeable: false }] : []),
      ...(isBrowserScript ? [{ id: 'http-conversion' as const, label: 'HTTP Conversion', title: 'Browser To HTTP Conversion Review', closeable: false }] : []),
      // Opened from an action's "Request body" and closed when done.
      { id: 'request-body', label: 'Request body', title: 'Request body', closeable: true },
    ]
    return descriptors
  }, [importMetadata, isBrowserScript, objectDescriptorReview.rows.length])
  const centerTabDescriptorById = useMemo(() => {
    return new Map(centerTabDescriptors.map(tab => [tab.id, tab]))
  }, [centerTabDescriptors])
  const availableCenterTabIds = useMemo(() => new Set(centerTabDescriptors.map(tab => tab.id)), [centerTabDescriptors])
  const visibleCenterTabs = useMemo(() => [
    ...centerTabDescriptors.filter(tab => !tab.closeable),
    ...centerTabsState.tabs
      .map(tabId => centerTabDescriptorById.get(tabId))
      .filter((tab): tab is CenterWorkbenchTabDescriptor => Boolean(tab?.closeable)),
  ], [centerTabDescriptorById, centerTabDescriptors, centerTabsState.tabs])
  // A fixed tab's document mounts the first time the tab is shown and then
  // stays mounted, as an opened tab always has; one never shown costs nothing.
  // Runtime Settings (home of the script configuration) and an import's Source
  // Mapping were always open by default, so they mount from the start.
  const [visitedCenterTabIds, setVisitedCenterTabIds] = useState<ReadonlySet<CenterWorkbenchTabId>>(() => new Set())
  const unvisitedCenterTabIds = [centerTabsState.activeTabId, 'runtime-settings' as const, ...(importMetadata ? ['imported-metadata' as const] : [])]
    .filter(tabId => !visitedCenterTabIds.has(tabId))
  if (unvisitedCenterTabIds.length) setVisitedCenterTabIds(new Set([...visitedCenterTabIds, ...unvisitedCenterTabIds]))
  const centerTabMountTarget = (tabId: CenterWorkbenchTabId) => {
    const descriptor = centerTabDescriptorById.get(tabId)
    const mounted = descriptor && (descriptor.closeable ? centerTabsState.tabs.includes(tabId) : visitedCenterTabIds.has(tabId))
    return mounted ? centerTabPanelElements[tabId] || null : null
  }
  const selectCenterTab = useCallback((tabId: CenterWorkbenchTabId) => {
    if (!availableCenterTabIds.has(tabId)) return
    setNarrowAuthorPane('editor')
    setCenterTabsState(prev => prev.activeTabId === tabId ? prev : { ...prev, activeTabId: tabId })
  }, [availableCenterTabIds])
  const openCenterTab = useCallback((tabId: CenterWorkbenchTabId) => {
    if (!availableCenterTabIds.has(tabId)) return
    setCenterTabsState(prev => {
      const tabs = prev.tabs.includes(tabId) ? prev.tabs : [...prev.tabs, tabId]
      if (tabs === prev.tabs && prev.activeTabId === tabId) return prev
      return { tabs, activeTabId: tabId }
    })
  }, [availableCenterTabIds])
  const selectOutlineParameter = useCallback((parameterName: string) => {
    setNarrowAuthorPane('inspector')
    setAuthorRightSidebarCollapsed(false)
    setSelectedOutlineParameterName(parameterName)
    setShowFullScriptJson(true)
    setDebugDetailCollapsed(false)
    setDebuggerTab('variables')
    // Show the selected parameter's details in the Parameter companion page.
    setParameterPaneShowAuto(false)
    setScriptJsonSidebarTab('auto-params')
    openCenterTab(SCRIPT_JSON_TAB_ID)
  }, [openCenterTab, setParameterPaneShowAuto, setScriptJsonSidebarTab, setSelectedOutlineParameterName, setShowFullScriptJson, setDebugDetailCollapsed, setDebuggerTab, setAuthorRightSidebarCollapsed])
  const closeCenterTab = useCallback((tabId: CenterWorkbenchTabId) => {
    if (!centerTabDescriptorById.get(tabId)?.closeable) return
    setCenterTabPanelElements(prev => prev[tabId] ? { ...prev, [tabId]: null } : prev)
    setCenterTabsState(prev => {
      if (!prev.tabs.includes(tabId)) return prev
      const nextTabs = prev.tabs.filter(candidate => candidate !== tabId)
      const activeTabId = prev.activeTabId === tabId ? ACTION_LIST_TAB_ID : prev.activeTabId
      return { tabs: nextTabs.length > 0 ? nextTabs : [ACTION_LIST_TAB_ID, SCRIPT_JSON_TAB_ID], activeTabId }
    })
  }, [centerTabDescriptorById])
  const setCenterTabPanelElement = useCallback((tabId: CenterWorkbenchTabId, element: HTMLDivElement | null) => {
    setCenterTabPanelElements(prev => {
      if (prev[tabId] === element) return prev
      return { ...prev, [tabId]: element }
    })
  }, [])
  // "Deep scan (server)" runs the backend parameterization engine over the saved script.
  const [serverParameterization, setServerParameterization] = useState<(Parameterization & { note?: string }) | null>(null)
  const serverParameterizationDialogRef = useRef<HTMLDivElement | null>(null)
  const saveBlockDialogRef = useRef<HTMLDivElement | null>(null)
  const closeServerParameterizationDialog = useCallback(() => setServerParameterization(null), [])
  const closeSaveBlockDialog = useCallback(() => {
    if (!savingBlock) setShowSaveBlockModal(false)
  }, [savingBlock])
  useModalDialog({
    dialogRef: serverParameterizationDialogRef,
    enabled: Boolean(serverParameterization),
    onClose: closeServerParameterizationDialog,
  })
  useModalDialog({
    dialogRef: saveBlockDialogRef,
    enabled: showSaveBlockModal,
    onClose: closeSaveBlockDialog,
  })
  const [deepScanning, setDeepScanning] = useState(false)
  // Author outline rail width. null = fit content (CSS fit-content default) until
  // the user drags it, after which a fixed px is remembered.
  const authorRailRef = useRef<HTMLDivElement | null>(null)
  const [authorRailWidth, setAuthorRailWidth] = useState<number | null>(() => {
    try { const val = window.localStorage.getItem('authorRailWidth'); const n = val ? parseInt(val, 10) : NaN; return Number.isFinite(n) ? n : null } catch { return null }
  })
  const [authorRailResizing, setAuthorRailResizing] = useState(false)

  useEffect(() => {
    try {
      window.localStorage.setItem('scriptEditorWorkbenchMode', workspaceTab)
    } catch { /* storage may be unavailable */ }
  }, [workspaceTab])
  useEffect(() => {
    setCenterTabsState(readCenterTabsState(centerTabsStorageKey))
    setCenterTabsHydratedKey(centerTabsStorageKey)
  }, [centerTabsStorageKey])
  useEffect(() => {
    setCenterTabsState(prev => {
      const nextTabs = [
        ACTION_LIST_TAB_ID,
        SCRIPT_JSON_TAB_ID,
        ...prev.tabs.filter(tabId => tabId !== ACTION_LIST_TAB_ID && tabId !== SCRIPT_JSON_TAB_ID && availableCenterTabIds.has(tabId)),
      ]
      // A fixed tab is active without being in the open list; any tab this
      // script offers may stay active.
      const activeTabId = availableCenterTabIds.has(prev.activeTabId) ? prev.activeTabId : ACTION_LIST_TAB_ID
      if (activeTabId === prev.activeTabId && nextTabs.length === prev.tabs.length && nextTabs.every((tabId, index) => tabId === prev.tabs[index])) {
        return prev
      }
      return { tabs: nextTabs, activeTabId }
    })
  }, [availableCenterTabIds])
  useEffect(() => {
    if (workspaceTab === 'debug' && isBrowserScript && (centerTabsState.activeTabId === ACTION_LIST_TAB_ID || DEBUG_HIDDEN_CENTER_TABS.has(centerTabsState.activeTabId))) openCenterTab(BROWSER_REPLAY_TAB_ID)
    if (workspaceTab === 'author' && centerTabsState.activeTabId === BROWSER_REPLAY_TAB_ID) openCenterTab(ACTION_LIST_TAB_ID)
  }, [centerTabsState.activeTabId, isBrowserScript, openCenterTab, workspaceTab])
  useEffect(() => {
    if (centerTabsHydratedKey !== centerTabsStorageKey) return
    try {
      window.localStorage.setItem(centerTabsStorageKey, JSON.stringify(centerTabsState))
    } catch { /* storage may be unavailable */ }
  }, [centerTabsHydratedKey, centerTabsState, centerTabsStorageKey])
  const isParameterDataTabActive = centerTabsState.activeTabId === 'parameter-data'
  const parameterMergeSummary = useMemo(() => summarizeParameterSourceMerge(script), [script])
  useEffect(() => {
    if (!isParameterDataTabActive) return
    const parameterCount = script.parameters?.length || 0
    setSelectedParameterIdx(previous => {
      if (previous !== null && previous >= 0 && previous < parameterCount) return previous
      return parameterCount > 0 ? 0 : null
    })
  }, [isParameterDataTabActive, script.parameters])
  useEffect(() => {
    if (focusParameterIdx === null || focusParameterIdx === undefined) return
    if (!script.parameters?.[focusParameterIdx]) return
    setSelectedParameterIdx(focusParameterIdx)
  }, [focusParameterIdx, script.parameters])
  const {
    applyJsonDraft: handleApplyJsonDraft,
    changeJsonDraft: handleCodePaneChange,
    commitJsonDraft,
    currentScript: currentDraftScript,
    formatJsonDraft: handleFormatJsonDraft,
    formattedScriptJson,
    parseMessage: jsonParseMessage,
    resetJsonDraft: handleResetJsonDraft,
    resolveCurrentScript,
    status: jsonStatus,
    statusMessage: jsonStatusMessage,
  } = useScriptJsonDraftController({
    jsonDirty,
    jsonText,
    onInvalidDraft: () => setDebuggerBottomTab('problems'),
    script,
    setError,
    setJsonDirty,
    setJsonText,
    setScript,
    setSuccess,
  })
  const currentAuthoringSourceRef = useRef({ script: currentDraftScript, jsonText, jsonDirty, invalid: Boolean(jsonParseMessage) })
  currentAuthoringSourceRef.current = { script: currentDraftScript, jsonText, jsonDirty, invalid: Boolean(jsonParseMessage) }
  // Serializing the whole editable draft is O(script size); memoize it so the
  // debug workspace does not re-stringify the entire script on every render
  // (replay progress events, keystrokes, breakpoint toggles).
  const currentEditableActionsKey = useMemo(
    () => {
      if (workspaceTab !== 'debug' && replayEditableActionsKey === null) return null
      const actionsKey = currentCorrelationEditableActionsKey(currentDraftScript, jsonDirty, jsonParseMessage)
      return actionsKey === null
        ? null
        : JSON.stringify({
          project_id: projectId ?? currentDraftScript.project_id ?? null,
          script_id: scriptId ?? currentDraftScript.id ?? null,
          actions_key: actionsKey,
        })
    },
    [workspaceTab, replayEditableActionsKey, currentDraftScript, jsonDirty, jsonParseMessage, projectId, scriptId],
  )
  const openImportedDataSourceReview = useCallback((sourceName?: string | null) => {
    openCenterTab('parameter-data')
    const sourceIndex = findDataSourceIndexByName(script.data_sources, sourceName)
    if (sourceIndex !== null) {
      setOpenDataEditor(sourceIndex)
    }
    const parameterIndex = findFirstParameterIndexByDataSource(script.parameters, sourceName)
    if (parameterIndex !== null) {
      setFocusParameterIdx(parameterIndex)
      setSelectedParameterIdx(parameterIndex)
    }
  }, [openCenterTab, script.data_sources, script.parameters, setOpenDataEditor])
  const runtimeSettingsController = useScriptRuntimeSettingsController({
    onOpenRuntimeTab: () => openCenterTab('runtime-settings'),
    protocol: script.protocol,
    setOpenSections,
  })
  const {
    activeHTTPTransportTab,
    activeSection: activeRuntimeSettingsSection,
    openImportedRuntimeSettings,
    selectHTTPTransportTab: setActiveHTTPTransportTab,
    selectSection: selectRuntimeSettingsSection,
  } = runtimeSettingsController
  const [assistantChangeLog, setAssistantChangeLog] = useState<AssistantChangeLogItem[]>([])
  const [authoringComponents, setAuthoringComponents] = useState<AuthoringComponent[]>([])
  const [, setComponentLibraryStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [validationEvidence, setValidationEvidence] = useState<ValidationEvidence | null>(null)
  const [validationReplayPreflight, setValidationReplayPreflight] = useState<ReplayPreflight | null>(null)
  const [validationSidebarResult, setValidationSidebarResult] = useState<ValidationSidebarResult | null>(null)
  const saveRequestRef = useRef(0)
  const saveInFlightScopeRef = useRef<object | null>(null)
  // A create landed in this editor and the page is opening the saved script.
  const savedScriptOpeningRef = useRef(false)
  const [savedScriptOpening, setSavedScriptOpening] = useState(false)
  // The save running for the current editor scope: a second request (Save
  // changes in the leave prompt, while Save is still running) shares its
  // outcome instead of being refused.
  const runningSaveRef = useRef<{ scope: object; outcome: Promise<boolean>; result: { laterEdits: boolean } } | null>(null)
  const pendingParameterDataCellEditsRef = useRef(new Map<string, PendingParameterDataCellEdit>())
  const parameterDataEditGenerationRef = useRef(0)
  const [pendingParameterDataCellEditCount, setPendingParameterDataCellEditCount] = useState(0)
  const stageParameterDataCellEdit = useCallback((change: ParameterDataCellDraftChange) => {
    const key = parameterDataCellEditKey(change)
    const edits = pendingParameterDataCellEditsRef.current
    const previousSize = edits.size
    if (change.value === null) {
      edits.delete(key)
    } else {
      edits.set(key, { ...change, value: change.value })
      parameterDataEditGenerationRef.current += 1
    }
    if (edits.size !== previousSize) setPendingParameterDataCellEditCount(edits.size)
  }, [])
  const clearPendingParameterDataCellEdits = useCallback(() => {
    if (pendingParameterDataCellEditsRef.current.size === 0) return
    pendingParameterDataCellEditsRef.current.clear()
    setPendingParameterDataCellEditCount(0)
  }, [])
  const ownSavedEditorEchoRef = useRef<{ id: string; projectId?: string; documentKey: string; submitted: ScriptData; baseline: ScriptAuthoringBaseline | null } | null>(null)
  const initialScriptDocumentKey = useMemo(() => initialScript ? formatScriptJson(initialScript) : null, [initialScript])
  const editorOwnsSavedEcho = Boolean(ownSavedEditorEchoRef.current && scriptId === ownSavedEditorEchoRef.current.id
    && (projectId === undefined || projectId === ownSavedEditorEchoRef.current.projectId)
    && initialScriptDocumentKey === ownSavedEditorEchoRef.current.documentKey)
  const sandboxSeedScope = JSON.stringify([projectId || '', scriptId || initialScriptDocumentKey || ''])
  const sandboxSeedScopeRef = useRef(sandboxSeedScope)
  useEffect(() => {
    if (sandboxSeedScopeRef.current === sandboxSeedScope) return
    sandboxSeedScopeRef.current = sandboxSeedScope
    // A saved draft is still the same editor session. Navigating to a different
    // script or project must never reuse its explicit input data accidentally.
    if (!editorOwnsSavedEcho) setReplayOptions(previous => previous.liveTableSeedsText ? { ...previous, liveTableSeedsText: '' } : previous)
  }, [editorOwnsSavedEcho, sandboxSeedScope, setReplayOptions])
  const editorResourceScopeRef = useRef({ projectId, folderId, scriptId, initialScript })
  const editorResourceScopeVersionRef = useRef(0)
  if (
    editorResourceScopeRef.current.projectId !== projectId
    || editorResourceScopeRef.current.folderId !== folderId
    || editorResourceScopeRef.current.scriptId !== scriptId
    || editorResourceScopeRef.current.initialScript !== initialScript
  ) {
    editorResourceScopeRef.current = { projectId, folderId, scriptId, initialScript }
    editorResourceScopeVersionRef.current += 1
    debugMutationSequenceRef.current += 1
  }
  const validationDocumentKey = useMemo(
    () => jsonParseMessage ? null : jsonDirty ? formatScriptJson(currentDraftScript) : formattedScriptJson,
    [currentDraftScript, formattedScriptJson, jsonDirty, jsonParseMessage],
  )
  // A validation result depends on the project, folder and replay variables
  // it was issued with as well as the document and its baseline revision.
  const validationContext = useMemo(
    () => validationContextKey(projectId, folderId, safeParseReplayVariables(replayOptions.variablesText)),
    [folderId, projectId, replayOptions.variablesText],
  )
  const flushPendingEditsRef = useRef<(baseScript: ScriptData) => { script: ScriptData; editGeneration: number }>(baseScript => ({ script: baseScript, editGeneration: 0 }))
  const flushPendingEdits = useCallback((baseScript: ScriptData) => flushPendingEditsRef.current(baseScript), [])
  const validationDocumentKeyRef = useRef<string | null>(null)
  validationDocumentKeyRef.current = validationDocumentKey
  // The session owns the paired authoring baseline, the dirty calculation
  // and guarded navigation; the editor supplies its document and handlers.
  const {
    resolveDraft,
    beginValidation,
    admitValidation,
    adoptValidated,
    isLatestValidation,
    validationRequestIsCurrent,
    invalidateValidation,
    clearValidatedBinding,
    validatedBinding,
    currentBinding: currentValidationBinding,
    validationIsCurrent: validationResultIsCurrent,
    authoringBaseline,
    authoringRevisionState,
    authoringRevisionReason,
    reviewedAuthoringBaseline,
    authoringReviewLoading,
    loadCurrentAuthoringBaseline,
    adoptReviewedAuthoringBaseline,
    acceptSavedBaseline,
    blockForReconciliation,
    dirty: scriptEditorDirty,
    markSaved,
    close: handleCloseEditor,
    returnToSource,
  } = useScriptAuthoringSession({
    initialScript,
    initiallyDirty,
    resolveCurrentScript,
    flushPendingEdits,
    documentKey: validationDocumentKey,
    context: validationContext,
    baseline: {
      scriptId: scriptId || undefined,
      projectId,
      initialAuthoring,
      initialRetainedNeedsReview,
      getScriptAuthoring: api.getScriptAuthoring,
      resourceScopeRef: editorResourceScopeRef,
      mountedRef: editorMountedRef,
      documentKeyRef: validationDocumentKeyRef,
      hasPendingEdits: () => pendingParameterDataCellEditsRef.current.size > 0,
      onHydrate: baselineScript => {
        setAuthorCodeFocus(null)
        setShowFullScriptJson(false)
        hydrateEditorState(baselineScript)
      },
      onAdopt: (adopted, baselineScript, reapplied) => {
        restoreScript(adopted)
        setJsonText(formatScriptJson(adopted))
        setJsonDirty(false)
        clearPendingParameterDataCellEdits()
        markBaseline(baselineScript)
        setError('')
        setSuccess(reapplied ? 'Draft reapplied to the reviewed baseline. Review its fields, then save explicitly.' : 'Loaded the current script; the previous draft was discarded.')
      },
    },
    document: {
      initialRetainedDraft,
      jsonDirty,
      pendingEditCount: pendingParameterDataCellEditCount,
      formattedDocument: formattedScriptJson,
      savedDocumentKey: baselineJsonRef.current,
    },
    navigation: {
      id: `script-editor-${scriptId || 'new'}`,
      label: scriptId ? 'Edit script' : 'Create script',
      // While the saved script opens, the prompt shows why it cannot save.
      save: () => savedScriptOpeningRef.current ? Promise.reject(new Error(SAVED_SCRIPT_OPENING)) : handleSaveRef.current(),
      onClose,
      onReturn: returnTarget?.onReturn,
      retainedGuardIds: returnTarget?.retainedGuardIds,
    },
  })
  useEffect(() => {
    // Resource changes reuse this component in the authoring surface. Invalidate
    // any validation issued for the previous script/project before its response
    // can update readiness evidence or commit validation-time metadata.
    invalidateValidation()
    saveRequestRef.current += 1
    saveInFlightScopeRef.current = null
    const ownEcho = ownSavedEditorEchoRef.current
    const canonicalEcho = ownEcho && scriptId === ownEcho.id && (projectId === undefined || projectId === ownEcho.projectId)
      && initialScriptDocumentKey === ownEcho.documentKey
    if (!canonicalEcho) {
      clearPendingParameterDataCellEdits()
      pendingCreatedSecretNamesRef.current = [...(initialRetainedCreatedSecretNames || [])]
    }
    replayStartInFlightRef.current = null
    replayStartAttemptRef.current = null
    debugMutationSequenceRef.current += 1
    setValidating(false)
    setSaving(false)
    setValidationEvidence(null)
    setValidationReplayPreflight(null)
    setValidationSidebarResult(null)
    setReplayStartPending(false)
    setReplayEditableActionsKey(null)
    setReplayLayoutKey(null)
    setReplayDocumentFingerprint(null)
    setActiveReplaySessionId(null)
    setUnavailableRuntimeSessionId(null)
    publishRuntimeAdmission({ ...EMPTY_DEBUG_RUNTIME, generation: runtimeAdmissionRef.current.generation + 1 })
    setRuntimeInspectionMode('current')
    setRuntimeFrameSourcePath(null)
    setBrowserReplaySession(null)
    setReplayReport(null)
    setReplayActivity([])
    setExpandedReplayEvent(null)
    setExpandedReplayBrowserArtifacts({})
    setReplayScreenshotSelection(null)
    setReplaySeedVariables({})
    setReplaying(false)
    setSecretBindingsOpen(false)
    setError('')
    setSuccess('')
    setWarnings([])
  }, [
    folderId,
    initialScript,
    initialScriptDocumentKey,
    initialRetainedCreatedSecretNames,
    projectId,
    scriptId,
    clearPendingParameterDataCellEdits,
    invalidateValidation,
    publishRuntimeAdmission,
    setActiveReplaySessionId,
    setBrowserReplaySession,
    setError,
    setExpandedReplayBrowserArtifacts,
    setExpandedReplayEvent,
    setReplayActivity,
    setReplayReport,
    setReplayScreenshotSelection,
    setReplaySeedVariables,
    setReplaying,
    setSaving,
    setSuccess,
    setValidating,
    setWarnings,
  ])
  // These derivations scan the full action set (and for imports, run a draft diff
  // against the baseline). On a multi-thousand-action import they are expensive, so
  // they must be memoized — otherwise every render (including scroll-snapshot driven
  // re-renders) re-scans the whole script and the editor stalls.
  // dslCatalogReady is intentionally referenced so this memo recomputes once the
  // DSL action catalog resolves: collectAuthoringIssues reads the module-global
  // cachedCatalog, and the first pass after an import runs before it loads.
  // Without this, a catalog-null pass (legacy heuristic) freezes stale issues.
  const authoringIssues = useMemo(() => {
    void dslCatalogReady
    return collectAuthoringIssues(currentDraftScript)
  }, [currentDraftScript, dslCatalogReady])
  const actionIssueBuckets = useMemo(
    () => groupIssuesByPhaseAction(authoringIssues, script),
    [authoringIssues, script],
  )
  const scriptLevelIssues = useMemo(
    () => authoringIssues.filter((issue) => typeof issue.actionIndex !== 'number'),
    [authoringIssues],
  )
  const errorCount = authoringIssues.filter((issue) => issue.severity === 'error').length
  const starterTemplates = useMemo(
    () => [...buildProtocolStarterTemplates(script.protocol, script.actions.length), ...(['producer', 'consumer', 'update', 'lease'] as const).map(kind => ({ key: `native-js-${kind}`, label: `Host JavaScript ${kind}`, description: `Native work-table ${kind} example. Declare the work alias and allowed operations in Runtime settings.`, action: nativeJavaScriptTemplate(kind) })), { key: 'distributed-rendezvous', label: 'Distributed rendezvous', description: 'Coordinate a fixed group across load generators.', action: { name: `Barrier_${script.actions.length + 1}`, type: 'rendezvous', params: { scope: 'distributed_group', key: `barrier_${script.actions.length + 1}`, timeout_ms: 30000, on_timeout: 'fail' } } }],
    [script.protocol, script.actions.length],
  )
  const migrationSuggestions = useMemo(() => buildMigrationAssistantSuggestions(script), [script])
  const repairSuggestions = useMemo(
    () => buildScriptRepairSuggestions(script, authoringIssues),
    [script, authoringIssues],
  )
  const parameterInspectorItems = useMemo(() => summarizeParameters(script.parameters || []), [script.parameters])
  const dataSourceInspectorItems = useMemo(() => summarizeDataSources(script.data_sources || []), [script.data_sources])
  const extractorInspectorItems = useMemo(() => scriptPhaseActionRows.flatMap((row) =>
    (row.action.extractors || []).map((extractor, extractorIndex) => ({
      id: `${row.phase}:${row.actionIndex}:${extractorIndex}`,
      label: `${extractor.name || `extractor_${extractorIndex + 1}`} • ${row.action.name || `${row.phaseLabel} ${row.actionIndex + 1}`}`,
      actionIndex: row.actionIndex,
      phase: row.phase,
      phaseLabel: row.phaseLabel,
    })),
  ), [scriptPhaseActionRows])
  const pushEditorHistory = useCallback((before: ScriptData) => {
    setEditorHistory(previous => ({
      past: [...previous.past, JSON.parse(JSON.stringify(before)) as ScriptData].slice(-30),
      future: [],
    }))
  }, [setEditorHistory])

  const replaceScriptWithHistory = useCallback((nextScript: ScriptData, beforeScript: ScriptData = script) => {
    pushEditorHistory(beforeScript)
    setScript(nextScript)
    setJsonText(formatScriptJson(nextScript))
    setJsonDirty(false)
  }, [pushEditorHistory, script, setJsonDirty, setJsonText, setScript])

  const applyReplaySuggestedLocator = useCallback((event: ReplayActionEvent, locator: string) => {
    const nextLocator = locator.trim()
    if (!nextLocator) return
    const normalizedPhase = normalizeDebugPhase(event.phase || 'action')
    if (!['init', 'action', 'teardown'].includes(normalizedPhase)) return
    const phase = normalizedPhase as ScriptActionPhase
    const phaseActions = phaseActionsForScript(script, phase)
    // The API omits a zero action index when serializing replay events.
    const actionIndex = event.action_index ?? 0
    if (actionIndex < 0 || actionIndex >= phaseActions.length) {
      setError('Suggested selector could not be mapped to a script action.')
      setSuccess('')
      return
    }
    const stepPath = event.step_path?.length ? event.step_path : event.request_index != null ? [`requests[${event.request_index}]`] : undefined
    const key = `${phase}:${actionIndex}${stepPath?.length ? `#${stepPath.join('/')}` : ''}`
    const scope = resolveAuthorJsonScopeByKey(script, key)
    if (!scope) { setError('Suggested selector could not be mapped to a script action.'); return }
    const currentAction = scope.action
    const collection = phase === 'action' ? 'actions' : phase
    const nextScript: ScriptData = { ...script, [collection]: phaseActions.map((action, index) => index === actionIndex ? updateNestedAction(action, stepPath, leaf => ({ ...leaf, selector: nextLocator })) : action) }
    replaceScriptWithHistory(nextScript)
    setActiveAction(phase === 'action' && !stepPath?.length ? actionIndex : null)
    setAuthorCodeFocus({ phase, actionIndex, stepPath, key })
    setScriptJsonSidebarTab('details')
    setAuthorRightSidebarCollapsed(false)
    openCenterTab(SCRIPT_JSON_TAB_ID)
    setError('')
    setSuccess(`Updated selector for ${currentAction.name || `Action ${actionIndex + 1}`}.`)
  }, [openCenterTab, replaceScriptWithHistory, script, setActiveAction, setAuthorCodeFocus, setError, setScriptJsonSidebarTab, setSuccess])

  const recordAssistantChange = useCallback((title: string, summary: string) => {
    setAssistantChangeLog((previous) => [
      {
        id: `${title}:${previous.length}:${Date.now()}`,
        title,
        summary,
      },
      ...previous,
    ].slice(0, 8))
  }, [])

  const applyHistoryChange = useCallback((nextScript: ScriptData, successMessage: string) => {
    clearPendingParameterDataCellEdits()
    beforeDocumentEditRef.current()
    const context = historyContext.current.get(formatScriptJson(nextScript))
    if (context) { setAuthorCodeFocus(context.focus); setActiveAction(context.active); setDebugBreakpoints(context.breakpoints) }
    else { setAuthorCodeFocus(null); setActiveAction(null) }
    restoreScript(nextScript)
    setDataSourceHistory({})
    setDatasetChange(null)
    setJsonText(formatScriptJson(nextScript))
    setJsonDirty(false)
    setSuccess(successMessage)
    setError('')
  }, [clearPendingParameterDataCellEdits, restoreScript, setActiveAction, setAuthorCodeFocus, setDebugBreakpoints, setDataSourceHistory, setError, setJsonDirty, setJsonText, setSuccess])

  const undoEditorChange = useCallback(() => {
    if (!editorHistory.past.length) return
    const priorScript = editorHistory.past[editorHistory.past.length - 1]
    const current = JSON.parse(JSON.stringify(script)) as ScriptData
    setEditorHistory(previous => ({
      past: previous.past.slice(0, -1),
      future: [current, ...previous.future].slice(0, 30),
    }))
    applyHistoryChange(priorScript, 'Undid the last document change.')
  }, [applyHistoryChange, editorHistory.past, script, setEditorHistory])

  const redoEditorChange = useCallback(() => {
    if (!editorHistory.future.length) return
    const [nextScript, ...remainingFuture] = editorHistory.future
    if (!nextScript) return
    const current = JSON.parse(JSON.stringify(script)) as ScriptData
    setEditorHistory(previous => ({
      past: [...previous.past, current].slice(-30),
      future: remainingFuture,
    }))
    applyHistoryChange(nextScript, 'Redid the document change.')
  }, [applyHistoryChange, editorHistory.future, script, setEditorHistory])

  useEffect(() => {
    if (!replayScreenshotSelection) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeReplayScreenshot()
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [closeReplayScreenshot, replayScreenshotSelection])

  useEffect(() => {
    setAssistantChangeLog([])
    historyContext.current.clear()
    setEditorHistory({ past: [], future: [] })
  }, [scriptId, setEditorHistory])
  const hydratedInitialScriptRef = useRef<{ script: ScriptData; scriptId?: string | null } | null>(null)
  // The hydrated script still waiting for its opening selection.
  const pendingOpeningFocusRef = useRef<ScriptData | null>(null)

  // Imperative refetch used after a user saves/deletes a reusable block; the catalog
  // is keyed by protocol + project so saved blocks surface alongside the built-ins.
  const reloadAuthoringComponents = useCallback(() => {
    return api.listAuthoringComponents({ protocol: script.protocol, projectId })
      .then((response) => {
        setAuthoringComponents(response.components || [])
        setComponentLibraryStatus('ready')
      })
      .catch(() => {
        setComponentLibraryStatus('error')
      })
  }, [api, script.protocol, projectId])

  useEffect(() => {
    let cancelled = false
    setComponentLibraryStatus('loading')
    api.listAuthoringComponents({ protocol: script.protocol, projectId })
      .then((response) => {
        if (cancelled) return
        setAuthoringComponents(response.components || [])
        setComponentLibraryStatus('ready')
      })
      .catch(() => {
        if (cancelled) return
        setAuthoringComponents([])
        setComponentLibraryStatus('error')
      })
    return () => {
      cancelled = true
    }
  }, [api, script.protocol, projectId])

  const handleCopyReplaySnapshotSection = async (key: string, value: string) => {
    if (!navigator.clipboard?.writeText) return
    await navigator.clipboard.writeText(value)
    setCopiedReplaySnapshotSection(key)
  }

  const toggleReplayBrowserArtifact = (key: string, expanded: boolean) => {
    setExpandedReplayBrowserArtifacts(prev => ({
      ...prev,
      [key]: expanded,
    }))
  }

  const buildAutoParameterCandidates = (current: ScriptData): ParameterCandidate[] => {
    const buckets = new Map<string, { keyHint: string; value: string; occurrences: CandidateOccurrence[] }>()
    const add = (value: string, keyHint: string, occurrence: CandidateOccurrence) => {
      const normalizedValue = value.trim()
      if (!isLikelyParameterValue(normalizedValue)) return
      const bucketKey = `${keyHint}::${normalizedValue}`
      const bucket = buckets.get(bucketKey)
      if (bucket) {
        bucket.occurrences.push(occurrence)
      } else {
        buckets.set(bucketKey, { keyHint, value: normalizedValue, occurrences: [occurrence] })
      }
    }

    current.actions.forEach((action, actionIdx) => {
      const target = action.target || ''
      if (target) {
        const queryPart = target.includes('?') ? target.split('?')[1] : ''
        if (queryPart) {
          queryPart.split('&').forEach(pair => {
            const [rawKey, rawValue] = pair.split('=')
            if (!rawValue) return
            const key = decodeURIComponent(rawKey || 'query_value')
            const value = decodeURIComponent(rawValue || '')
            add(value, key, { actionIdx, field: 'target' })
          })
        }
      }

      const orderedHeaderNames = new Set(
        (action.header_list || []).map(header => header.name.trim().toLowerCase()).filter(Boolean),
      )
      if (action.header_list?.length) {
        action.header_list.forEach((header, headerIndex) => {
          add(String(header.value || ''), header.name || `header_${headerIndex + 1}`, {
            actionIdx,
            field: 'header',
            headerKey: header.name,
            headerIndex,
          })
        })
      }
      Object.entries(action.headers || {})
        .filter(([headerKey]) => !orderedHeaderNames.has(headerKey.trim().toLowerCase()))
        .forEach(([headerKey, headerValue]) => {
          add(String(headerValue || ''), headerKey, { actionIdx, field: 'header', headerKey })
        })

      const body = action.body || ''
      if (body) {
        try {
          const parsed = JSON.parse(body)
          const walk = (node: unknown, path: string[]) => {
            if (node === null || node === undefined) return
            if (typeof node === 'string' || typeof node === 'number' || typeof node === 'boolean') {
              const key = path[path.length - 1] || 'body_value'
              add(String(node), key, { actionIdx, field: 'body' })
              return
            }
            if (Array.isArray(node)) {
              node.forEach((child, idx) => walk(child, [...path, `item_${idx + 1}`]))
              return
            }
            if (typeof node === 'object') {
              Object.entries(node).forEach(([k, v]) => walk(v, [...path, k]))
            }
          }
          walk(parsed, [])
        } catch {
          const quoted = Array.from(body.matchAll(/"([^"]{3,})"/g))
          quoted.forEach((match, idx) => {
            add(match[1], `body_field_${idx + 1}`, { actionIdx, field: 'body' })
          })
        }
      }
    })

    return Array.from(buckets.entries())
      .map(([id, bucket]) => ({
        id,
        keyHint: bucket.keyHint,
        value: bucket.value,
        varName: sanitizeVarName(bucket.keyHint),
        occurrences: bucket.occurrences,
        selected: bucket.occurrences.length > 0,
      }))
      .sort((a, b) => b.occurrences.length - a.occurrences.length)
  }

  useEffect(() => {
    if (!initialScript) return
    const hydrated = hydratedInitialScriptRef.current
    if (hydrated?.script === initialScript && hydrated.scriptId === scriptId) return
    hydratedInitialScriptRef.current = { script: initialScript, scriptId }
    if (editorOwnsSavedEcho || (hydrated && hydrated.scriptId === scriptId && initialScriptDocumentKey === baselineJsonRef.current)) return
    if (initialRetainedDraft) {
      hydrateEditorState(initialRetainedDraft)
      markBaseline(initialScript)
      if (initialRetainedJsonText !== undefined) { setJsonText(initialRetainedJsonText); setJsonDirty(true) }
      pendingOpeningFocusRef.current = initialRetainedDraft
      return
    }
    hydrateEditorState(initialScript)
    setAuthorCodeFocus(null)
    setShowFullScriptJson(false)

		const focusedActionIndex = findActionIndexByName(initialScript.actions, initialFocusedActionName)
		if (focusedActionIndex !== null) {
			setActiveAction(focusedActionIndex)
      setAuthorCodeFocus({ phase: 'action', actionIndex: focusedActionIndex })
      setShowFullScriptJson(false)
      setScriptJsonSidebarTab('details')
      setAuthorRightSidebarCollapsed(false)
      setOpenSections(prev => ({ ...prev, import: false }))
		}

    if (initialFocusedSection === 'runtime') {
      openCenterTab('runtime-settings')
      setOpenSections(prev => ({ ...prev, runtime: true }))
    }

  if (initialFocusedRuntimeArea) {
      openImportedRuntimeSettings(initialFocusedRuntimeArea)
    }

    if (initialFocusedSection === 'parameter-data') {
      openCenterTab('parameter-data')
    }

    const focusedDataSourceIndex = findDataSourceIndexByName(initialScript.data_sources, initialFocusedDataSourceName)
    if (focusedDataSourceIndex !== null) {
      openCenterTab('parameter-data')
      setOpenDataEditor(focusedDataSourceIndex)
    }

    // Nothing asked for: open on the first problem once this script's
    // problems are known (see the effect after Author Problems).
    pendingOpeningFocusRef.current = focusedActionIndex === null && !initialFocusedSection && !initialFocusedRuntimeArea && focusedDataSourceIndex === null
      ? initialScript
      : null
  }, [initialScriptDocumentKey, editorOwnsSavedEcho, initialRetainedDraft, initialRetainedJsonText, setJsonDirty, setJsonText, markBaseline, baselineJsonRef, hydrateEditorState, initialFocusedActionName, initialFocusedDataSourceName, initialFocusedRuntimeArea, initialFocusedSection, initialScript, openCenterTab, openImportedRuntimeSettings, scriptId, setActiveAction, setAuthorCodeFocus, setOpenDataEditor, setOpenSections, setScriptJsonSidebarTab, setShowFullScriptJson])

  const selectAuthorPhaseIndex = (phase: ScriptActionPhase, index: number | null) => {
    setActiveAction(phase === 'action' ? index : null)
    setAuthorCodeFocus(index === null ? null : { phase, actionIndex: index })
    setShowFullScriptJson(false)
    if (index !== null) {
      setAuthorRightSidebarCollapsed(false)
    }
    if (index !== null && centerTabsState.activeTabId === SCRIPT_JSON_TAB_ID) {
      setScriptJsonSidebarTab('details')
    }
  }

  const selectAuthorAction = (index: number | null) => {
    selectAuthorPhaseIndex('action', index)
  }

  const selectParameterData = useCallback((index: number | null) => {
    setSelectedParameterIdx(index)
    const parameter = index !== null ? script.parameters?.[index] || null : null
    if (!parameter) {
      setOpenDataEditor(null)
      return
    }
    const sourceIndex = findDataSourceIndexByName(script.data_sources, parameter.data_source)
    setOpenDataEditor(sourceIndex)
  }, [script.data_sources, script.parameters, setOpenDataEditor])

  const selectSidebarAction = (index: number) => {
    if (sourceReview && centerTabsState.activeTabId === 'imported-metadata') {
      setActiveAction(index)
      setAuthorCodeFocus({ phase: 'action', actionIndex: index })
      setShowFullScriptJson(false)
      focusSourceMappingForAction(index)
      return
    }
    selectAuthorAction(index)
  }

  const selectAuthorPhaseAction = (row: ScriptPhaseActionRow) => {
    setAuthorOverview(false)
    openCenterTab(ACTION_LIST_TAB_ID)
    setInspectorFocusSection('request')
    setNarrowAuthorPane('editor')
    requestInspectorFocus({ phase: row.phase, actionIndex: row.actionIndex })
    setAuthorRightSidebarCollapsed(false)
    setSelectedOutlineParameterName(null)
    setScriptJsonSidebarTab('details')
    if (row.phase === 'action') {
      selectSidebarAction(row.actionIndex)
      return
    }
    selectAuthorPhaseIndex(row.phase, row.actionIndex)
  }
  const selectAuthorPhaseActionRef = useRef(selectAuthorPhaseAction)
  selectAuthorPhaseActionRef.current = selectAuthorPhaseAction

  const focusNestedStep = (phase: ScriptActionPhase, actionIndex: number, stepPath: string[], key: string, label: string) => {
    setAuthorOverview(false)
    openCenterTab(ACTION_LIST_TAB_ID)
    setInspectorFocusSection('request')
    setNarrowAuthorPane('editor')
    requestInspectorFocus({ phase, actionIndex, stepPath })
    setAuthorRightSidebarCollapsed(false)
    setSelectedOutlineParameterName(null)
    setScriptJsonSidebarTab('details')
    setActiveAction(null)
    setAuthorCodeFocus({ phase, actionIndex, stepPath, key, label })
    setShowFullScriptJson(false)
  }
  const selectAuthorNestedItem = (item: Extract<DebugRailRenderRow, { kind: 'container' | 'leaf' }>) => {
    const label = item.kind === 'leaf'
      ? item.childAction.name || item.name || humanizeStepSegment(item.stepPath[item.stepPath.length - 1] || '')
      : item.name
    focusNestedStep(item.row.phase, item.row.actionIndex, item.stepPath, item.key, label)
  }

  const [followExecution, setFollowExecution] = useState(true)
  const followExecutionRef = useRef(true)
  const changeFollowExecution = (enabled: boolean) => { setRuntimeFrameSourcePath(null); followExecutionRef.current = enabled; setFollowExecution(enabled) }
  const handleReplaySocketMessage = useCallback((message: WSMessage) => {
    const msg = message as ReplayWSMessage
    const trackedSessionId = activeReplaySessionId || (replayStartPending ? replayStartAttemptRef.current?.sessionId : null)
    if (!trackedSessionId || (msg.replay_session_id !== trackedSessionId && msg.debug_session_id !== trackedSessionId)) return


    if (msg.session) {
      if (!admitRuntimeSession({ ...msg.session, runtime_evidence_unavailable: runtimeEvidenceUnavailable(msg, msg.session, msg.report, msg.session.report) }, 'socket')) return
    } else {
      const status = msg.status || (msg.type.includes('completed') ? 'completed' : msg.type.includes('failed') && !msg.event ? 'failed' : msg.type.includes('stopped') ? 'stopped' : msg.type.includes('paused') ? 'paused' : msg.type.includes('started') || msg.type.includes('resumed') ? 'running' : undefined)
      const result = admitDebugRuntimeDelta(runtimeAdmissionRef.current, { session_id: trackedSessionId, session_incarnation: msg.session_incarnation, state_revision: msg.state_revision, status, runtime_evidence_unavailable: runtimeEvidenceUnavailable(msg, msg.report) })
      publishRuntimeAdmission(result.state)
      if (!result.accepted) return
      if (status) setBrowserReplaySession(previous => previous ? { ...previous, status } : previous)
    }

    if (msg.type === 'script_debug_started' && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      setReplayReport(prev => prev ? {
        ...prev,
        group: session.group || prev.group,
        protocol: session.protocol || prev.protocol,
        started_at: session.started_at || prev.started_at,
        completed_at: '',
        success: true,
        error: undefined,
        events: session.events || [],
        final_variables: session.report?.final_variables,
      } : {
        group: session.group || 'script-debug',
        protocol: session.protocol || 'http',
        started_at: session.started_at || new Date().toISOString(),
        completed_at: '',
        success: true,
        events: session.events || [],
        final_variables: session.report?.final_variables,
      })
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Debugger started',
        detail: `${session.group || 'script debug'} · ${session.log_level || 'info'} logs`,
        timestamp: session.started_at || new Date().toISOString(),
      }))
      setExpandedReplayEvent(null)
      setExpandedReplayBrowserArtifacts({})
      return
    }

    if ((msg.type === 'script_debug_action' || msg.type === 'script_debug_action_started' || msg.type === 'action_started') && (msg.session || msg.action)) {
      const session = msg.session
      if (session) { setBrowserReplaySession(session); setDebugBreakpoints(session.breakpoints || []) }
      const currentAction = msg.action || session?.current_action
      if (currentAction) {
        setReplayActivity(prev => upsertReplayActivity(prev, {
          id: msg.type.includes('started') ? `started-${currentAction.phase}-${currentAction.action_index}` : `ready-${currentAction.phase}-${currentAction.action_index}`,
          kind: 'event',
          title: `${msg.type.includes('started') ? 'Started' : 'Ready'}: ${currentAction.action_name || 'next action'}`,
          detail: `${formatDebugPhaseLabel(currentAction.phase)} ${currentAction.action_index + 1}`,
          timestamp: new Date().toISOString(),
        }))
      }
      return
    }

    if ((msg.type === 'script_debug_event' || msg.type === 'script_debug_action_completed' || msg.type === 'script_debug_action_failed') && msg.event) {
      const replayEvent = msg.event
      if (msg.session) setBrowserReplaySession(msg.session)
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = mergeReplayEvent(prev.events, replayEvent, msg.event_index)
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        if (followExecutionRef.current) setExpandedReplayEvent(msg.event_index)
        setDebugDetailCollapsed(false)
      }
      return
    }

    if ((msg.type === 'script_debug_paused' || msg.type === 'script_debug_resumed' || msg.type === 'script_debug_breakpoints_updated' || msg.type === 'script_debug_options_updated') && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      if (msg.type === 'script_debug_breakpoints_updated') {
        setDebugBreakpoints(session.breakpoints || [])
      }
      // Keep the local toggle in sync with the server's authoritative state.
      if (typeof session.break_on_failure === 'boolean') {
        setReplayOptions(prev => prev.breakOnFailure === session.break_on_failure ? prev : { ...prev, breakOnFailure: session.break_on_failure! })
      }
      const isPaused = msg.type === 'script_debug_paused'
      // Following execution, a pause shows the action about to run rather than
      // the one that just ran, unless the pause is at a failure.
      if (isPaused && followExecutionRef.current && session.current_event?.success !== false) setExpandedReplayEvent(null)
      const activityId = msg.type === 'script_debug_breakpoints_updated'
        ? 'breakpoints'
        : msg.type === 'script_debug_options_updated'
          ? 'options'
          : isPaused ? 'paused' : 'resumed'
      const activityTitle = msg.type === 'script_debug_breakpoints_updated'
        ? 'Breakpoints updated'
        : msg.type === 'script_debug_options_updated'
          ? `Break on failure ${session.break_on_failure ? 'on' : 'off'}`
          : isPaused ? 'Debugger paused' : 'Debugger resumed'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: activityId,
        kind: 'event',
        title: activityTitle,
        detail: session.current_action
          ? `${formatDebugPhaseLabel(session.current_action.phase)} ${session.current_action.action_index + 1}: ${session.current_action.action_name || 'next action'}${formatDebugLeafSuffix(session.current_action)}`
          : session.group || 'script debug',
        timestamp: new Date().toISOString(),
      }))
      return
    }

    if ((msg.type === 'script_debug_completed' || msg.type === 'script_debug_failed' || msg.type === 'script_debug_stopped') && (msg.report || msg.session)) {
      if (msg.session) setBrowserReplaySession(msg.session)
      const report = msg.report || msg.session?.report || null
      const finalized = isFinalizedDebugSession(msg.session, report)
      if (!finalized) {
        // Stop is a two-phase transition: the first event acknowledges the
        // cancellation request, while the worker is still draining and has no
        // CompletedAt/final report. Keep tracking the session so polling or the
        // later terminal event can publish the authoritative result.
        setReplayReport(prev => prev ? {
          ...prev,
          events: msg.session?.events?.length ? msg.session.events : prev.events,
          error: msg.session?.error || prev.error,
        } : prev)
        setReplayActivity(prev => upsertReplayActivity(prev, {
          id: 'stopping',
          kind: 'event',
          title: 'Debugger stopping',
          detail: msg.session?.error || 'Cancellation requested; waiting for the active action to finish.',
          timestamp: new Date().toISOString(),
        }))
        return
      }
      const completedAt = msg.session?.completed_at || report?.completed_at
      if (!completedAt) return
      const messageSessionId = msg.session?.id || msg.debug_session_id || msg.replay_session_id || trackedSessionId
      if (replayStartAttemptRef.current?.sessionId === messageSessionId) {
        replayStartAttemptRef.current.terminalObserved = true
      }
      if (report) {
        setReplayReport({
          ...report,
          replay_session_id: report.replay_session_id || msg.session?.id || trackedSessionId,
          completed_at: report.completed_at || completedAt,
        })
      } else {
        setReplayReport(prev => prev ? {
          ...prev,
          completed_at: completedAt,
          success: msg.type === 'script_debug_completed',
          error: msg.session?.error || prev.error,
        } : prev)
      }
      const title = msg.type === 'script_debug_completed'
        ? 'Debugger completed successfully'
        : msg.type === 'script_debug_stopped'
          ? 'Debugger stopped'
          : 'Debugger completed with failures'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: msg.type === 'script_debug_failed' ? 'error' : 'complete',
        title,
        detail: report?.error || msg.session?.error,
        timestamp: completedAt,
        success: msg.type === 'script_debug_completed' ? true : msg.type === 'script_debug_failed' ? false : undefined,
      }))
      if (report) {
        if (followExecutionRef.current) setExpandedReplayEvent(replayEvidenceIndex(report))
        if (report.events.length > 0) setDebugDetailCollapsed(false)
      }
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
      return
    }

    if (msg.type === 'browser_replay_started' && msg.session) {
      const session = msg.session
      setBrowserReplaySession(prev => {
        if (prev?.id === session.id && prev.status === 'paused' && session.status === 'running') {
          return { ...session, status: 'paused' }
        }
        return session
      })
      setReplayReport(prev => prev ? {
        ...prev,
        group: session.group || prev.group,
        protocol: session.protocol || prev.protocol,
        started_at: session.started_at || prev.started_at,
      } : {
        group: session.group || 'browser-replay',
        protocol: session.protocol || 'browser',
        started_at: session.started_at || new Date().toISOString(),
        completed_at: '',
        success: true,
        events: [],
      })
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Browser replay started',
        detail: session.group || 'browser replay',
        timestamp: session.started_at || new Date().toISOString(),
      }))
      setExpandedReplayEvent(null)
      setExpandedReplayBrowserArtifacts({})
      return
    }

    if (msg.type === 'replay_started' && msg.report) {
      const report = msg.report
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Replay started',
        detail: report.group || 'replay',
        timestamp: report.started_at,
      }))
      setExpandedReplayEvent(null)
      setExpandedReplayBrowserArtifacts({})
      return
    }

    if ((msg.type === 'replay_event' || msg.type === 'action_completed' || msg.type === 'action_failed') && msg.event) {
      const replayEvent = msg.event
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = mergeReplayEvent(prev.events, replayEvent, msg.event_index)
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        if (followExecutionRef.current) setExpandedReplayEvent(msg.event_index)
        setDebugDetailCollapsed(false)
      }
      return
    }

    if (msg.type === 'browser_replay_event' && msg.event) {
      const replayEvent = msg.event
      setBrowserReplaySession(prev => {
        if (msg.session) return msg.session
        if (!prev) return prev
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : prev.events?.length || 0
        const events = mergeReplayEvent(prev.events || [], replayEvent as unknown as BrowserReplayEvent, index)
        return {
          ...prev,
          current_event_index: index,
          current_event: replayEvent as unknown as BrowserReplayEvent,
          events,
        }
      })
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = mergeReplayEvent(prev.events, replayEvent, msg.event_index)
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        if (followExecutionRef.current) setExpandedReplayEvent(msg.event_index)
        setDebugDetailCollapsed(false)
      }
      return
    }

    if ((msg.type === 'browser_replay_paused' || msg.type === 'browser_replay_resumed') && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      const isPaused = msg.type === 'browser_replay_paused'
      if (isPaused && followExecutionRef.current && session.current_event?.success !== false) setExpandedReplayEvent(null)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: isPaused ? 'paused' : 'resumed',
        kind: 'event',
        title: isPaused ? 'Browser replay paused' : 'Browser replay resumed',
        detail: session.group || 'browser replay',
        timestamp: new Date().toISOString(),
      }))
      return
    }

    if (msg.type === 'replay_complete' && msg.report) {
      const report = msg.report
      const messageSessionId = report.replay_session_id || msg.replay_session_id || activeReplaySessionId
      if (replayStartAttemptRef.current?.sessionId === messageSessionId) {
        replayStartAttemptRef.current.terminalObserved = true
      }
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: 'complete',
        title: report.success ? 'Replay completed successfully' : 'Replay completed with failures',
        detail: report.error,
        timestamp: report.completed_at,
        success: report.success,
      }))
      if (followExecutionRef.current) setExpandedReplayEvent(replayEvidenceIndex(report))
      if (report.events.length > 0) setDebugDetailCollapsed(false)
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
      return
    }

    if ((msg.type === 'browser_replay_completed' || msg.type === 'browser_replay_failed' || msg.type === 'browser_replay_stopped') && (msg.report || msg.session)) {
      if (msg.session) setBrowserReplaySession(msg.session)
      const report = msg.report || msg.session?.report || null
      const completedAt = msg.session?.completed_at || report?.completed_at || new Date().toISOString()
      const messageSessionId = msg.session?.id || report?.replay_session_id || msg.replay_session_id || activeReplaySessionId
      if (replayStartAttemptRef.current?.sessionId === messageSessionId) {
        replayStartAttemptRef.current.terminalObserved = true
      }
      if (report) {
        setReplayReport(report)
      } else {
        setReplayReport(prev => prev ? {
          ...prev,
          completed_at: completedAt,
          success: msg.type === 'browser_replay_completed',
          error: msg.session?.error || prev.error,
        } : prev)
      }
      const title = msg.type === 'browser_replay_completed'
        ? 'Browser replay completed successfully'
        : msg.type === 'browser_replay_stopped'
          ? 'Browser replay stopped'
          : 'Browser replay completed with failures'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: msg.type === 'browser_replay_stopped' ? 'complete' : msg.type === 'browser_replay_completed' ? 'complete' : 'error',
        title,
        detail: report?.error || msg.session?.error,
        timestamp: completedAt,
        success: msg.type === 'browser_replay_completed' ? true : msg.type === 'browser_replay_failed' ? false : undefined,
      }))
      if (report) {
        if (followExecutionRef.current) setExpandedReplayEvent(replayEvidenceIndex(report))
        if (report.events.length > 0) setDebugDetailCollapsed(false)
      }
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
    }
  }, [activeReplaySessionId, replayStartPending, admitRuntimeSession, publishRuntimeAdmission, setActiveReplaySessionId, setBrowserReplaySession, setDebugBreakpoints, setDebugDetailCollapsed, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayOptions, setReplayReport, setReplaying])

  useWebSocket({ onMessage: handleReplaySocketMessage, projectId })

  useEffect(() => {
    if (expandedReplayEvent === null) return
    const node = replayEventRefs.current[expandedReplayEvent]
    if (!node) return
    node.scrollIntoView({
      block: 'nearest',
      behavior: activeReplaySessionId ? 'smooth' : 'auto',
    })
  }, [activeReplaySessionId, expandedReplayEvent, replayEventRefs])

  const updateField = (path: string, value: unknown) => {
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      const keys = path.split('.')
      let obj: Record<string, unknown> = next as unknown as Record<string, unknown>
      for (let i = 0; i < keys.length - 1; i++) {
        const key = keys[i]
        const current = obj[key]
        if (!current || typeof current !== 'object' || Array.isArray(current)) obj[key] = {}
        obj = obj[key] as Record<string, unknown>
      }
      obj[keys[keys.length - 1]] = value
      return next
    })
  }

  const updateProtocol = (protocol: string) => {
    pushEditorHistory(script)
    const transition = transitionScriptProtocol(script, protocol)
    setScript(transition.script)
    if (transition.selectedFirstAction) selectAuthorAction(0)
  }

  const updatePhaseActionList = (phase: ScriptActionPhase, updater: (actions: Action[]) => Action[]) => {
    setScript(prev => {
      const nextActions = updater([...phaseActionsForScript(prev, phase)])
      if (phase === 'init') return { ...prev, init: nextActions }
      if (phase === 'teardown') return { ...prev, teardown: nextActions }
      return { ...prev, actions: nextActions }
    })
  }

  const remapPhaseBreakpointsAfterRemove = (phase: ScriptActionPhase, removedIndex: number) => {
    setDebugBreakpoints(prev => prev
      .filter(bp => bp.phase !== phase || bp.action_index !== removedIndex)
      .map(bp => bp.phase === phase && bp.action_index > removedIndex ? { ...bp, action_index: bp.action_index - 1 } : bp))
  }

  const remapPhaseBreakpointsAfterMove = (phase: ScriptActionPhase, fromIndex: number, toIndex: number) => {
    setDebugBreakpoints(prev => prev.map(bp => {
      if (bp.phase !== phase) return bp
      if (bp.action_index === fromIndex) return { ...bp, action_index: toIndex }
      if (bp.action_index === toIndex) return { ...bp, action_index: fromIndex }
      return bp
    }))
  }

  const remapPhaseBreakpointsAfterDuplicate = (phase: ScriptActionPhase, sourceIndex: number) => {
    setDebugBreakpoints(prev => prev.map(bp =>
      bp.phase === phase && bp.action_index > sourceIndex ? { ...bp, action_index: bp.action_index + 1 } : bp))
  }

  const remapSelectionAfterRemove = (phase: ScriptActionPhase, removedIndex: number) => {
    if (phase === 'action') {
      if (activeAction === removedIndex) selectAuthorAction(null)
      else if (activeAction !== null && activeAction > removedIndex) setActiveAction(activeAction - 1)
    }
    setAuthorCodeFocus(prev => {
      if (!prev || prev.phase !== phase) return prev
      if (prev.actionIndex === removedIndex) return null
      if (prev.actionIndex > removedIndex) return { ...prev, actionIndex: prev.actionIndex - 1 }
      return prev
    })
  }

  const phaseActionNamePrefix = (phase: ScriptActionPhase) => (
    phase === 'init' ? 'Init' : phase === 'teardown' ? 'Teardown' : 'Action'
  )

  const addAction = (phase: ScriptActionPhase = 'action') => {
    const template = isBrowserScript
      ? EMPTY_BROWSER_ACTION
      : isJmsScript
        ? EMPTY_JMS_ACTION
        : isSoapScript
          ? EMPTY_SOAP_ACTION
          : isWebSocketScript
            ? EMPTY_WEBSOCKET_ACTION
            : isGrpcScript
              ? EMPTY_GRPC_ACTION
              : isDatabaseScript
                ? EMPTY_DATABASE_ACTION
                : isNetworkScript
                  ? EMPTY_NETWORK_ACTION
                  : EMPTY_ACTION
    const currentActions = phaseActionsForScript(script, phase)
    const a = JSON.parse(JSON.stringify({
      ...template,
      name: `${phaseActionNamePrefix(phase)}_${currentActions.length + 1}`,
      extractors: template.extractors || [],
      assertions: template.assertions || [],
    })) as Action
    setInsertPhase(phase)
    // A template's closing step takes the user's steps before it; an empty
    // loop or if block selected is where the new step belongs.
    const focused = authorCodeFocus?.phase === phase ? activeInspectorScope?.action : undefined
    setInsertPosition(!focused ? 'end'
      : templateSpot && templateSpot.action === focused ? templateSpot.position
        : ['control_if', 'control_loop'].includes(focused.type) && !focused.control?.steps?.length ? 'inside' : 'after')
    setInsertSeed(a)
  }

  const insertPreparedAction = (action: Action) => {
    try {
      const result = insertAuthorAction(script, action, insertPhase, insertPosition, authorCodeFocus)
      setScript(result.script)
      setDebugBreakpoints(previous => remapInsertedBreakpoints(previous, result.focus))
      setAuthorCodeFocus(result.focus)
      setActiveAction(result.focus.phase === 'action' && !result.focus.stepPath?.length ? result.focus.actionIndex : null)
      setInsertSeed(null)
      setAuthorOverview(false)
      setNarrowAuthorPane('editor')
      openCenterTab(ACTION_LIST_TAB_ID)
      setSuccess(`Inserted ${action.name}. Changes are not saved yet.`)
      setError('')
      requestInspectorFocus(result.focus, panel => panel.querySelector('input'), result.script)
    } catch (error) { setError(getErrorMessage(error, 'Could not insert action.')) }
  }

  // A linked template: its steps go in order at the chosen place, and the step
  // the user's own steps go beside is selected, so + Add Action puts them there.
  const insertPreparedActions = (actions: Action[], label: string, spot?: TemplateSpot) => {
    try {
      let next = script
      let focus = authorCodeFocus
      let breakpoints = debugBreakpoints
      let spotFocus: typeof focus = null
      for (const [index, action] of actions.entries()) {
        const inserted = insertAuthorAction(next, action, insertPhase, index ? 'after' : insertPosition, focus)
        next = inserted.script
        focus = inserted.focus
        breakpoints = remapInsertedBreakpoints(breakpoints, focus)
        const within = (action.control?.steps || []).findIndex(step => step.name === spot?.name)
        if (spot && action.name === spot.name) spotFocus = focus
        else if (spot && within >= 0) {
          const stepPath = [...(focus.stepPath || []), `then[${within}]`]
          spotFocus = { phase: focus.phase, actionIndex: focus.actionIndex, stepPath, key: authorFocusKey({ ...focus, stepPath }), label: spot.name }
        }
      }
      const selected = spotFocus || focus
      replaceScriptWithHistory(next)
      setDebugBreakpoints(breakpoints)
      if (selected) {
        setAuthorCodeFocus(selected)
        setActiveAction(selected.phase === 'action' && !selected.stepPath?.length ? selected.actionIndex : null)
      }
      const spotAction = spot && spotFocus ? resolveAuthorJsonScopeByKey(next, authorFocusKey(spotFocus))?.action : undefined
      setTemplateSpot(spotAction ? { action: spotAction, position: spot!.position } : null)
      setInsertSeed(null)
      setAuthorOverview(false)
      setNarrowAuthorPane('editor')
      openCenterTab(ACTION_LIST_TAB_ID)
      setSuccess(`Inserted ${label}: ${templateStepNames(actions).join(', ')}.${spot ? ` Your own steps go ${spot.position} “${spot.name}”, which is selected: + Add Action puts them there.` : ''} Changes are not saved yet.`)
      setError('')
    } catch (error) { setError(getErrorMessage(error, 'Could not insert the steps.')) }
  }

  const addHTTPRuntimeAction = (type: HTTPRuntimeActionType) => {
    const labels: Record<HTTPRuntimeActionType, string> = {
      cookie_clear: 'Clear learned cookies',
      cache_clear: 'Clear simulated cache',
      http_connections_close: 'Close idle HTTP connections',
    }
    const currentActions = phaseActionsForScript(script, 'action')
    const nextAction: Action = {
      name: labels[type],
      type,
      target: '',
    }
    updatePhaseActionList('action', actions => [...actions, nextAction])
    setSuccess(`${labels[type]} action added at the end of the action phase.`)
    setError('')
    selectAuthorAction(currentActions.length)
  }

  const addActionTemplate = (templateKey: string) => {
    const template = starterTemplates.find((candidate) => candidate.key === templateKey)
    if (!template) return
    const nextAction = JSON.parse(JSON.stringify(template.action)) as Action
    insertPreparedAction(nextAction)
  }

  const removePhaseAction = (phase: ScriptActionPhase, idx: number) => {
    updatePhaseActionList(phase, actions => actions.filter((_, i) => i !== idx))
    remapPhaseBreakpointsAfterRemove(phase, idx)
    remapSelectionAfterRemove(phase, idx)
  }

  // A folded step's sub-transaction: both markers carry its name.
  const renameSubTransaction = (measure: OutlineMeasure, fullName: string) => {
    const phase = measure.start.phase
    updatePhaseActionList(phase, actions => actions.map((action, index) => index === measure.start.actionIndex || index === measure.end.actionIndex
      ? renameTransactionMarker(action, measure.name, fullName) : action))
    setSuccess(`Renamed the sub-transaction to ${splitMeasureName(fullName).child}. Changes are not saved yet.`)
    setError('')
  }
  // Removing the markers leaves the step, no longer measured on its own.
  const removeSubTransaction = (measure: OutlineMeasure) => {
    const phase = measure.start.phase
    const removed = [measure.start.actionIndex, measure.end.actionIndex]
    const shift = (index: number) => index - removed.filter(at => at < index).length
    updatePhaseActionList(phase, actions => actions.filter((_, index) => !removed.includes(index)))
    setDebugBreakpoints(previous => previous
      .filter(bp => bp.phase !== phase || !removed.includes(bp.action_index))
      .map(bp => bp.phase === phase ? { ...bp, action_index: shift(bp.action_index) } : bp))
    if (phase === 'action') setActiveAction(previous => previous === null || removed.includes(previous) ? null : shift(previous))
    setAuthorCodeFocus(previous => !previous || previous.phase !== phase ? previous : removed.includes(previous.actionIndex) ? null : { ...previous, actionIndex: shift(previous.actionIndex) })
    setSuccess(`Removed the sub-transaction ${splitMeasureName(measure.name).child}; its step is no longer measured on its own. Undo restores it.`)
    setError('')
  }

  const movePhaseAction = (phase: ScriptActionPhase, idx: number, dir: -1 | 1) => {
    const newIdx = idx + dir
    const phaseActions = phaseActionsForScript(script, phase)
    if (newIdx < 0 || newIdx >= phaseActions.length) return
    updatePhaseActionList(phase, actions => {
      ;[actions[idx], actions[newIdx]] = [actions[newIdx], actions[idx]]
      return actions
    })
    remapPhaseBreakpointsAfterMove(phase, idx, newIdx)
    selectAuthorPhaseIndex(phase, newIdx)
  }

  const duplicatePhaseAction = (phase: ScriptActionPhase, idx: number) => {
    updatePhaseActionList(phase, actions => duplicateActionInList(actions, idx))
    // A copy is inserted at idx+1, so every index after it shifts down by one.
    // Bump breakpoints AND the active selection / JSON focus so they keep pointing
    // at the same action — otherwise the outline highlight and the read-only
    // Selected Action JSON pane would show, and a structured-form edit would
    // target, the wrong action. Spread preserves any nested stepPath on the focus.
    remapPhaseBreakpointsAfterDuplicate(phase, idx)
    if (phase === 'action') {
      setActiveAction(prev => (prev !== null && prev > idx ? prev + 1 : prev))
    }
    setAuthorCodeFocus(prev => (prev && prev.phase === phase && prev.actionIndex > idx ? { ...prev, actionIndex: prev.actionIndex + 1 } : prev))
    selectAuthorPhaseIndex(phase, idx + 1)
  }

  const updatePhaseActionObject = (phase: ScriptActionPhase, idx: number, updater: (action: Action) => Action) => {
    updatePhaseActionList(phase, actions => {
      if (idx < 0 || idx >= actions.length) return actions
      const path = authorCodeFocus?.phase === phase && authorCodeFocus.actionIndex === idx ? authorCodeFocus.stepPath : undefined
      actions[idx] = updateNestedAction(actions[idx], path, updater)
      return actions
    })
  }

  const updatePhaseAction = (phase: ScriptActionPhase, idx: number, field: keyof Action, value: unknown) => {
    updatePhaseActionObject(phase, idx, action => field === 'http_version'
      ? setHTTPActionVersion(action, value as Action['http_version'])
      : { ...action, [field]: value })
  }

  const updateAction = (idx: number, field: keyof Action, value: unknown) => {
    updatePhaseAction('action', idx, field, value)
  }

  const addPhaseExtractor = (phase: ScriptActionPhase, actionIdx: number) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const ext = [...(action.extractors || []), defaultExtractorForProtocol(script.protocol)]
      return { ...action, extractors: ext }
    })
  }

  const addExtractorDefinition = (actionIdx: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair): CorrelationApplyResult => {
    const sourceAction = script.actions[actionIdx]
    if (!sourceAction) return { extractorAdded: false, extractorAvailable: false, requestRepaired: false }
    const extractorScope = correlationExtractorScope(script)
    const { extractor: effectiveExtractor, repair: effectiveRepair } = resolveCorrelationExtractorAndRepair(sourceAction, extractorScope, extractor, repair)
    const repairAction = effectiveRepair &&
      effectiveRepair.failingActionIndex >= 0 &&
      effectiveRepair.failingActionIndex < script.actions.length
      ? script.actions[effectiveRepair.failingActionIndex]
      : undefined
    const repairWillApply = Boolean(repairAction && effectiveRepair && applyCorrelationRepairToAction(repairAction, effectiveRepair) !== repairAction)
    const repairRequiresStructuralReview = Boolean(repairAction && effectiveRepair && correlationRepairRequiresStructuralBodyReview(repairAction, effectiveRepair))
    const extractorAlreadyExists = Boolean(sourceAction.extractors?.some(existing => existing.name === effectiveExtractor.name))
    const actions = [...script.actions]
    actions[actionIdx] = addCorrelationExtractorIfMissing(actions[actionIdx], effectiveExtractor)
    if (effectiveRepair && effectiveRepair.failingActionIndex >= 0 && effectiveRepair.failingActionIndex < actions.length) {
      actions[effectiveRepair.failingActionIndex] = applyCorrelationRepairToAction(actions[effectiveRepair.failingActionIndex], effectiveRepair)
    }
    setScript({ ...script, actions })
    const repairSuffix = repairWillApply && effectiveRepair
      ? ` Updated action ${effectiveRepair.failingActionIndex + 1} to use ${effectiveRepair.replacement}.`
      : repairRequiresStructuralReview
        ? ' The Aura body requires structural review; automatic request replacement was not applied.'
      : effectiveRepair
        ? ' The mapped request value was not found; replace it manually.'
      : ''
    const extractorMessage = extractorAlreadyExists ? `Kept existing extractor "${effectiveExtractor.name}"` : `Added extractor "${effectiveExtractor.name}"`
    setSuccess(`${extractorMessage} on ${script.actions[actionIdx]?.name || `action ${actionIdx + 1}`}.${repairSuffix}`)
    return {
      extractorAdded: !extractorAlreadyExists,
      extractorAvailable: true,
      requestRepaired: repairWillApply,
    }
  }

  const updatePhaseExtractor = (phase: ScriptActionPhase, actionIdx: number, extIdx: number, field: string, value: string) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const ext = [...(action.extractors || [])]
      ext[extIdx] = { ...ext[extIdx], [field]: value }
      return { ...action, extractors: ext }
    })
  }

  const removePhaseExtractor = (phase: ScriptActionPhase, actionIdx: number, extIdx: number) => {
    updatePhaseActionObject(phase, actionIdx, action => ({
      ...action,
      extractors: (action.extractors || []).filter((_, i) => i !== extIdx),
    }))
  }

  const addPhaseAssertion = (phase: ScriptActionPhase, actionIdx: number, assertion?: Assertion) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const assertions = [...(action.assertions || []), assertion || defaultAssertionForProtocol(script.protocol)]
      return { ...action, assertions }
    })
  }

  const updatePhaseAssertion = (phase: ScriptActionPhase, actionIdx: number, aIdx: number, field: keyof Assertion, value: unknown) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const assertions = [...(action.assertions || [])]
      assertions[aIdx] = { ...assertions[aIdx], [field]: value }
      return { ...action, assertions }
    })
  }

  const removePhaseAssertion = (phase: ScriptActionPhase, actionIdx: number, aIdx: number) => {
    updatePhaseActionObject(phase, actionIdx, action => ({
      ...action,
      assertions: (action.assertions || []).filter((_, i) => i !== aIdx),
    }))
  }

  const addPhaseHeader = (phase: ScriptActionPhase, actionIdx: number) => {
    const key = prompt('Header name:')
    if (!key) return
    updatePhaseActionObject(phase, actionIdx, action => {
      const headers = { ...(action.headers || {}), [key]: '' }
      return { ...action, headers }
    })
  }

  const updatePhaseHeader = (phase: ScriptActionPhase, actionIdx: number, oldKey: string, newKey: string, value: string) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const headers = { ...(action.headers || {}) }
      if (oldKey !== newKey) delete headers[oldKey]
      headers[newKey] = value
      return { ...action, headers }
    })
  }

  const removePhaseHeader = (phase: ScriptActionPhase, actionIdx: number, key: string) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const headers = { ...(action.headers || {}) }
      delete headers[key]
      return { ...action, headers }
    })
  }

  const addPhaseMetadata = (phase: ScriptActionPhase, actionIdx: number) => {
    const key = prompt('Property name:')
    if (!key) return
    updatePhaseActionObject(phase, actionIdx, action => {
      const metadata = { ...(action.metadata || {}), [key]: '' }
      return { ...action, metadata }
    })
  }

  const updatePhaseMetadata = (phase: ScriptActionPhase, actionIdx: number, oldKey: string, newKey: string, value: string) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const metadata = { ...(action.metadata || {}) }
      if (oldKey !== newKey) delete metadata[oldKey]
      metadata[newKey] = value
      return { ...action, metadata }
    })
  }

  const removePhaseMetadata = (phase: ScriptActionPhase, actionIdx: number, key: string) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const metadata = { ...(action.metadata || {}) }
      delete metadata[key]
      return { ...action, metadata }
    })
  }

  const flushPendingParameterDataCellEdits = (baseScript: ScriptData): {
    script: ScriptData
    editGeneration: number
  } => {
    const edits = Array.from(pendingParameterDataCellEditsRef.current.values())
    const editGeneration = parameterDataEditGenerationRef.current
    if (edits.length === 0) return { script: baseScript, editGeneration }

    const nextScript = applyPendingParameterDataCellEdits(baseScript, edits)
    const affectedSourceIndexes = Array.from(new Set(edits.map(edit => edit.sourceIndex)))
    setDataSourceHistory(history => {
      const nextHistory = { ...history }
      affectedSourceIndexes.forEach((sourceIndex) => {
        const previousSource = baseScript.data_sources?.[sourceIndex]
        if (!previousSource) return
        const entry = nextHistory[sourceIndex] || { past: [], future: [] }
        nextHistory[sourceIndex] = {
          past: [...entry.past, cloneDataSource(previousSource)].slice(-100),
          future: [],
        }
      })
      return nextHistory
    })
    clearPendingParameterDataCellEdits()
    commitJsonDraft(nextScript)
    return { script: nextScript, editGeneration }
  }

  flushPendingEditsRef.current = flushPendingParameterDataCellEdits

  const handleDownloadScript = useCallback(async () => {
    const filename = `${sanitizeDownloadName(currentDraftScript.name, 'script')}.json`
    if (!scriptId || scriptEditorDirty) {
      await exportGeneratedTextFile(jsonText || formatScriptJson(script), filename)
      return
    }
    setError('')
    try {
      const content = await api.downloadScript(scriptId)
      await exportGeneratedTextFile(content, filename)
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Failed to download script'))
    }
  }, [api, currentDraftScript.name, exportGeneratedTextFile, jsonText, script, scriptEditorDirty, scriptId, setError])

  const draftBaseURLError = scriptBaseURLError(currentDraftScript)
  const { disabled: saveScriptDisabled, title: saveScriptTitle } = scriptSaveAvailability({
    opening: savedScriptOpening && !scriptId,
    stored: Boolean(scriptId), revisionState: authoringRevisionState, hasBaseline: Boolean(authoringBaseline), saving, secretBindingsOpen, validating,
    dirty: scriptEditorDirty, hasName: Boolean(currentDraftScript.name), jsonParseMessage: jsonParseMessage || '', baseURLError: draftBaseURLError || '',
  })
  const validateScriptDisabled = saving || replaying || validating || !currentDraftScript.name
  const validateScriptTitle = validating
    ? 'Checking script readiness'
    : !currentDraftScript.name
      ? 'Enter a script name before checking readiness'
      : 'Recheck script readiness'
  const saveScriptNow = async (result: { laterEdits: boolean } = { laterEdits: false }): Promise<boolean> => {
    if (secretBindingsOpen || (scriptId && (authoringRevisionState !== 'ready' || !authoringBaseline))) return false
    // A create already landed in this editor, which waits for the page to open
    // the saved script: another create would make a second script.
    if (savedScriptOpeningRef.current) {
      setError(SAVED_SCRIPT_OPENING)
      return false
    }
    const requestResourceScope = editorResourceScopeRef.current
    if (saveInFlightScopeRef.current === requestResourceScope) return false
    const requestID = ++saveRequestRef.current
    const requestIsCurrent = () => (
      editorMountedRef.current
      && editorResourceScopeRef.current === requestResourceScope
      && saveRequestRef.current === requestID
    )
    saveInFlightScopeRef.current = requestResourceScope
    setError(''); setSuccess(''); setWarnings([]); setSaving(true)
    setValidationEvidence(null)
    clearValidatedBinding()
    setValidationReplayPreflight(null)
    try {
      const {
        script: flushedScript,
        editGeneration: parameterDataEditGenerationAtSave,
        documentKey: documentKeyAtSave,
      } = resolveDraft()
      validationDocumentKeyRef.current = documentKeyAtSave
      const ownNormalization = ownSavedEditorEchoRef.current
      const normalizedDraft = ownNormalization?.baseline && authoringBaseline?.projection.content_revision === ownNormalization.baseline.projection.content_revision
        ? reapplyScriptDraft(flushedScript, authoringBaseline, ownNormalization.submitted) : flushedScript
      const currentScript = await preserveMappingBindings(normalizedDraft, importBaselineScript || initialScript || null)
      if (!requestIsCurrent()) return false
      // A new Java Vuser script with executable runner actions cannot be created
      // through POST /scripts — the backend answers 409 and requires governed
      // review. Save it as an authoring session and stop there; approval is a
      // separate, explicit act, the same way the archive importer treats Java
      // drafts.
      if (!scriptId && scriptRequiresJavaVuserAuthoringApproval(currentScript)) {
        const session = await api.createAuthoringSession({
          project_id: projectId,
          folder_id: folderId,
          source: 'manual',
          name: currentScript.name || 'Java Vuser Draft',
          status: 'reviewing',
          draft: currentScript,
          idempotency_key: authoringSessionIdempotencyKey('script-editor-java-vuser', currentScript),
        })
        if (!requestIsCurrent()) return true
        const unchangedSinceQueued = validationDocumentKeyRef.current === documentKeyAtSave && parameterDataEditGenerationRef.current === parameterDataEditGenerationAtSave
        result.laterEdits = !unchangedSinceQueued
        if (unchangedSinceQueued) {
          commitJsonDraft(currentScript)
        }
        // Deliberately not markBaseline/markSaved: no library
        // script exists yet, so the editor must keep treating this as unsaved
        // work and keep guarding navigation until an approval creates one.
        setSuccess(`Java Vuser draft "${session.name}" queued for approval. Approve it from Scripts to add it to the library.`)
        return true
      }
      if (collectAuthoringIssues(currentScript).some(issue => issue.severity === 'error')) { setShowAuthorProblems(true); return false }
      let savedScript: StoredScript
      if (scriptId) {
        savedScript = await api.updateScript(scriptId, currentScript, authoringBaseline!)
      } else {
        savedScript = await api.createScript(currentScript, projectId, folderId)
      }
      if (!requestIsCurrent()) return true
      const savedBaseline = scriptId || savedScript.authoring_projection ? baselineFromSavedScript(savedScript, projectId) : null
      if (savedBaseline) acceptSavedBaseline(savedBaseline)
      const savedDefinition = savedBaseline?.projection.script || savedScript.script || currentScript
      const adoptedSavedDocument = validationDocumentKeyRef.current === documentKeyAtSave && parameterDataEditGenerationRef.current === parameterDataEditGenerationAtSave
      if (adoptedSavedDocument) {
        // Adopt the server's canonical document without treating normalization as
        // an edit or resetting the current selection/history on the parent echo.
        restoreScript(savedDefinition)
        setJsonText(formatScriptJson(savedDefinition))
        setJsonDirty(false)
        validationDocumentKeyRef.current = formatScriptJson(savedDefinition)
      }
      const expectedCurrentDocumentKey = adoptedSavedDocument ? formatScriptJson(savedDefinition) : documentKeyAtSave
      ownSavedEditorEchoRef.current = { id: savedScript.id, projectId: savedScript.project_id || savedDefinition.project_id || projectId, documentKey: formatScriptJson(savedDefinition), submitted: flushedScript, baseline: savedBaseline }
      if (!scriptId) {
        savedScriptOpeningRef.current = true
        setSavedScriptOpening(true)
      }
      markBaseline(savedDefinition)
      markSaved()
      const persistedSecretNames = new Set(scriptSecretReferenceSummaries(savedDefinition).map(binding => binding.name))
      pendingCreatedSecretNamesRef.current = pendingCreatedSecretNamesRef.current.filter(name => !persistedSecretNames.has(name))
      setSuccess('Script saved successfully!')
      if (scriptId) {
        await refreshScriptSecretStatus()
        if (!requestIsCurrent()) return true
        setProjectSecretProbeRetryGeneration(generation => generation + 1)
      }
      const configureSecrets = saveAndConfigureRequestedRef.current
      saveAndConfigureRequestedRef.current = false
      if (!requestIsCurrent()) return true
      const hasLaterDraft = validationDocumentKeyRef.current !== expectedCurrentDocumentKey || parameterDataEditGenerationRef.current !== parameterDataEditGenerationAtSave
      result.laterEdits = hasLaterDraft
      const source = currentAuthoringSourceRef.current
      const lateDraft = hasLaterDraft ? applyPendingParameterDataCellEdits(source.script, Array.from(pendingParameterDataCellEditsRef.current.values())) : !savedBaseline ? currentScript : undefined
      const retainedDraft = lateDraft && savedBaseline ? reapplyScriptDraft(lateDraft, savedBaseline, flushedScript) : lateDraft
      const retainedJsonText = hasLaterDraft && source.jsonDirty ? source.jsonText : undefined
      const retainedCreatedSecretNames = pendingCreatedSecretNamesRef.current.length ? [...pendingCreatedSecretNamesRef.current] : undefined
      // Nothing is awaited after onSave: the page holds back the return that
      // follows a save in the same turn (useScriptsAuthoringState).
      if (hasLaterDraft || configureSecrets || retainedDraft || retainedCreatedSecretNames) onSave?.(savedScript, { ...(hasLaterDraft ? { laterEdits: true } : {}), ...(configureSecrets ? { configureSecrets: true } : {}), ...(retainedDraft ? { retainedDraft } : {}), ...(retainedJsonText !== undefined ? { retainedJsonText } : {}), ...(retainedCreatedSecretNames ? { retainedCreatedSecretNames } : {}) })
      else onSave?.(savedScript)
      return true
    } catch (e: unknown) {
      if (!requestIsCurrent()) return false
      saveAndConfigureRequestedRef.current = false
      if (scriptId && scriptSaveNeedsReconciliation(e)) {
        blockForReconciliation(e instanceof ApiError && e.status === 412
          ? 'The authored script or its placement changed since this editor loaded. Your draft was not saved. Load the current baseline, inspect the diff, and explicitly reapply or reload.'
          : 'We could not confirm whether this save reached the server. Your draft is kept here. Load the current version to compare it with your draft, then reapply your changes.')
      }
      const baseMessage = scriptSaveFailedOnServer(e)
        ? `Saving failed on the server; your draft is kept here. Choose Save to try again. Server detail: ${getErrorMessage(e, 'none given')}`
        : getErrorMessage(e, 'Failed to save script')
      const createdNames = pendingCreatedSecretNamesRef.current
      setError(createdNames.length > 0
        ? `${baseMessage} ${createdNames.join(', ')} ${createdNames.length === 1 ? 'was' : 'were'} already created and remain in the project vault as unused entries until this draft is saved or the entries are removed administratively.`
        : baseMessage)
      if (getErrorMessage(e, '').includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
      return false
    } finally {
      if (saveInFlightScopeRef.current === requestResourceScope) {
        saveInFlightScopeRef.current = null
      }
      if (requestIsCurrent()) setSaving(false)
    }
  }
  const handleSave = (): Promise<boolean> => {
    const scope = editorResourceScopeRef.current
    const running = runningSaveRef.current
    if (running && running.scope === scope) {
      // The running save holds the document as it was when it started; edits
      // made since are not in it, so a request to save them is not answered
      // by it (the leave prompt stays open and says so).
      return running.outcome.then(saved => {
        if (saved && running.result.laterEdits) throw new Error('The save that was running has finished, but edits made since it started are not saved yet. They are still in the editor.')
        return saved
      })
    }
    const result = { laterEdits: false }
    const outcome = saveScriptNow(result)
    runningSaveRef.current = { scope, outcome, result }
    void outcome.finally(() => {
      if (runningSaveRef.current?.outcome === outcome) runningSaveRef.current = null
    })
    return outcome
  }
  handleSaveRef.current = handleSave

  const handleValidateScript = async () => {
    const requestResourceScope = editorResourceScopeRef.current
    setError('')
    setSuccess('')
    setWarnings([])
    setValidationEvidence(null)
    setValidationReplayPreflight(null)
    setValidationSidebarResult(null)
    // A stored script is attested only while the editor holds exactly its
    // baseline; anything else validates the resolved draft, pending data
    // cells included.
    let stored = Boolean(scriptId) && !scriptEditorDirty && Boolean(authoringBaseline)
    let draft: ResolvedScriptDraft
    try {
      draft = resolveDraft()
    } catch (e: unknown) {
      setValidationSidebarResult({ tone: 'error', message: getErrorMessage(e, 'Script validation failed'), warnings: [], issues: [] })
      setDebuggerBottomTab('problems')
      return
    }
    let request = beginValidation(draft, stored)
    const requestIsCurrent = () => isLatestValidation(request) && editorResourceScopeRef.current === requestResourceScope
    setValidating(true)
    try {
      const currentScript = draft.script
      const validateAsDraft = () => api.validateDraftScript(currentScript, projectId, folderId, replayConfiguredVariables || undefined, { fingerprint: draft.fingerprint })
      let res = stored && scriptId ? await api.validateScript(scriptId) : await validateAsDraft()
      if (!requestIsCurrent()) return
      let admission = admitValidation(request, res)
      if (admission === 'unbound' && stored && !res.validated_content_revision) {
        // The controller attested no stored revision (an older peer, a
        // failed durable read): the clean editor holds exactly its baseline,
        // so validate that document as a draft, bound by its fingerprint.
        stored = false
        request = beginValidation(draft, false)
        res = await validateAsDraft()
        if (!requestIsCurrent()) return
        admission = admitValidation(request, res)
      }
      if (admission === 'superseded') return
      if (admission === 'unbound') {
        setValidationSidebarResult({
          tone: 'error',
          message: stored
            ? 'The stored script changed since this editor loaded it, so this validation does not describe your baseline. Reload the script or recheck the draft.'
            : 'The controller did not confirm which draft it validated. Recheck readiness with a compatible controller.',
          warnings: [],
          issues: [],
        })
        return
      }
      const validationScript = applyValidationRuntimeSettingsMetadata(currentScript, res)
      const validationWarnings = normalizeWarnings(res.warnings)
      setValidationReplayPreflight(res.preflight || null)
      setValidationEvidence({
        data_source_fit: res.data_source_fit,
        ai_guardrails: res.ai_guardrails,
        authoring_health: res.authoring_health,
      })
      const validatedDocumentKey = formatScriptJson(validationScript)
      adoptValidated(request, validatedDocumentKey)
      // Validation can only add derived metadata (an import's runtime
      // settings parity): debug evidence for the document it validated still
      // describes the committed one.
      setReplayDocumentFingerprint(current => current === draft.fingerprint ? draftFingerprint(validatedDocumentKey) : current)
      if (jsonDirty || currentScript.metadata?.import || validationScript !== currentScript) {
        commitJsonDraft(validationScript)
      }
      if (res.valid) {
        setValidationSidebarResult({
          tone: validationWarnings.length > 0 ? 'warning' : 'success',
          message: validationWarnings.length > 0 ? 'Script validation passed with warnings.' : 'Script validation passed.',
          warnings: validationWarnings,
          issues: [],
        })
        return
      }
      setValidationSidebarResult({
        tone: 'error',
        message: 'Script validation failed.',
        warnings: validationWarnings,
        issues: res.issues.length > 0 ? res.issues : ['Script validation failed.'],
      })
    } catch (e: unknown) {
      if (!requestIsCurrent() || !validationRequestIsCurrent(request)) return
      const validationWarnings = normalizeWarnings(getErrorWarnings(e))
      setValidationEvidence(null)
      clearValidatedBinding()
      setValidationReplayPreflight(null)
      setValidationSidebarResult({
        tone: 'error',
        message: getErrorMessage(e, 'Script validation failed'),
        warnings: validationWarnings,
        issues: [],
      })
      if (getErrorMessage(e, '').includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
    } finally {
      if (requestIsCurrent()) setValidating(false)
    }
  }

  // A replay-evidence edit (switching a request to auto+h3) validates the
  // edited draft once it has rendered, so the check sees the new policy.
  const latestValidateScript = useRef(handleValidateScript)
  useEffect(() => { latestValidateScript.current = handleValidateScript })
  const [revalidateAfterEdit, setRevalidateAfterEdit] = useState(0)
  useEffect(() => {
    if (revalidateAfterEdit > 0) void latestValidateScript.current()
  }, [revalidateAfterEdit])

  const handleGenerateBrowserHTTPPreview = async () => {
	const requestID = ++httpConversionRequestRef.current
    setHttpConversionError('')
    setSuccess('')
    setHttpConversionGenerating(true)
    try {
      const currentScript = resolveDraft().script
      const networkEntries = replayNetworkEntriesForConversion(replayReport, browserReplaySession)
      const res = await api.previewBrowserToHTTP({
        name: `${currentScript.name || 'Browser Script'} HTTP Draft`,
        project_id: projectId || currentScript.project_id,
        replay_session_id: browserReplaySession?.id || activeReplaySessionId || undefined,
        source_browser_script: currentScript,
        network_entries: networkEntries,
      })
	  if (httpConversionRequestRef.current !== requestID) return
      setHttpConversionPreview(res)
      setWarnings(normalizeWarnings(res.warnings))
      setSuccess('Generated HTTP draft preview from browser replay evidence.')
    } catch (e: unknown) {
	  if (httpConversionRequestRef.current !== requestID) return
      setHttpConversionPreview(null)
      setWarnings(normalizeWarnings(getErrorWarnings(e)))
      setHttpConversionError(getErrorMessage(e, 'Failed to generate HTTP draft preview'))
      if (getErrorMessage(e, '').includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
    } finally {
	  if (httpConversionRequestRef.current === requestID) setHttpConversionGenerating(false)
    }
  }

  const workspaceDocuments = useRef<Partial<Record<WorkbenchMode, CenterWorkbenchTabId>>>({})
  useEffect(() => { workspaceDocuments.current = {} }, [scriptId, projectId, folderId])
  const activateWorkspaceTab = useCallback((nextTab: WorkbenchMode) => {
    if (nextTab === workspaceTab) return
    workspaceDocuments.current[workspaceTab] = centerTabsState.activeTabId
    const nextDocument = workspaceDocuments.current[nextTab]
      || (nextTab === 'debug' && isBrowserScript ? BROWSER_REPLAY_TAB_ID : ACTION_LIST_TAB_ID)
    setWorkspaceTab(nextTab)
    openCenterTab(nextDocument === BROWSER_REPLAY_TAB_ID && nextTab === 'author' ? ACTION_LIST_TAB_ID : nextDocument)
    if (nextTab === 'debug') {
      setDebuggerTab('request')
    }
  }, [centerTabsState.activeTabId, isBrowserScript, openCenterTab, setDebuggerTab, workspaceTab])

  const openReplayDebugger = useCallback((openSettings = false) => {
    activateWorkspaceTab('debug')
    if (openSettings) setDebuggerSettingsOpen(true)
  }, [activateWorkspaceTab, setDebuggerSettingsOpen])

  const currentRunRef = useRef<Omit<DebugRunRecord, 'report'> | null>(null)
  const [retainedDebugRunHistory, setDebugRunHistory] = useState<DebugRunRecord[]>([])
  const debugRunHistory = useMemo(() => retainedDebugRunHistory.map(record =>
    runtimeEvidenceUnavailable(record.report) || (runtimeVariablesUnavailable &&
      (record.id === currentRunRef.current?.id || record.id === unavailableRuntimeSessionId || record.report.replay_session_id === unavailableRuntimeSessionId))
      ? { ...record, report: omitUnavailableReplayReport(record.report) } : record),
  [retainedDebugRunHistory, runtimeVariablesUnavailable, unavailableRuntimeSessionId])
  useEffect(() => {
    if (!replayReport?.completed_at || !currentRunRef.current) return
    const record = structuredClone({ ...currentRunRef.current, report: replayReport })
    setDebugRunHistory(previous => [record, ...previous.filter(r => r.id !== record.id)].slice(0, 5))
  }, [replayReport])
  useEffect(() => {
    currentRunRef.current = null
    setDebugRunHistory([])
  }, [scriptId, projectId, folderId, initialScript])

  const handleReplay = useCallback(async (options?: { startPaused?: boolean; runTo?: ScriptDebugBreakpoint }) => {
    const replayResourceScope = editorResourceScopeRef.current
    if (replayStartInFlightRef.current === replayResourceScope) return
    const replayResourceIsCurrent = () => (
      editorMountedRef.current
      && editorResourceScopeRef.current === replayResourceScope
    )
    if (!replaySecretStatusKnownRef.current) {
      setError('Project secret status is not available yet. Wait for the credential check to complete before running this script.')
      return
    }
    const missingSecrets = replayMissingProjectSecretsRef.current
    if (missingSecrets.length > 0) {
      setError(`Configure missing project secret${missingSecrets.length === 1 ? '' : 's'} before running: ${missingSecrets.join(', ')}.`)
      return
    }
    replayStartInFlightRef.current = replayResourceScope
    setError('')
    setSuccess('')
    setWarnings([])
    openReplayDebugger()
    setReplaying(true)
    setReplayStartPending(true)
    let debugSessionStarted = false
    let startRequestDispatched = false
    let requestedReplaySessionId = ''
    let startRuntimeRequest: DebugRuntimeRequest | undefined
    try {
      const replayDraft = resolveDraft()
      const currentScript = replayDraft.script
      setReplayEditableActionsKey(scopedEditableActionsEvidenceKey(currentScript, projectId, scriptId))
      setReplayLayoutKey(stepLayoutKey(currentScript))
      setReplayDocumentFingerprint(replayDraft.fingerprint)
      const variables = parseReplayVariablesText(replayOptions.variablesText)
      const liveTableSeeds = parseLiveTableSandboxSeeds(replayOptions.liveTableSeedsText)
      // Stop here, in the field's own words, rather than send a run identity the controller refuses.
      const runIdProblem = debugRunIdError(replayOptions.runId)
      if (runIdProblem) throw new Error(runIdProblem)
      const replaySessionId = createReplaySessionId()
      requestedReplaySessionId = replaySessionId
      debugMutationSequenceRef.current += 1
      publishRuntimeAdmission(beginDebugRuntime(runtimeAdmissionRef.current, replaySessionId))
      setRuntimeInspectionMode('current')
      setRuntimeFrameSourcePath(null)
      startRuntimeRequest = runtimeRequest()
      replayStartAttemptRef.current = {
        sessionId: replaySessionId,
        terminalObserved: false,
        awaitingReconciliation: false,
        notFoundCount: 0,
      }
      const startPaused = options?.startPaused ?? replayOptions.startPaused
      const baseReplayPayload = buildReplayStartPayload(captureScript(currentScript, replayOptions.capture), {
        projectId,
        groupName: replayOptions.groupName,
        replaySessionId,
        variables,
        ignoreThinkTime: replayOptions.ignoreThinkTime,
        startPaused,
      })
      const payload = {
        ...baseReplayPayload,
        live_table_seeds: liveTableSeeds.length ? liveTableSeeds : undefined,
        runtime_profile: { capture_mode: replayOptions.capture || 'diagnostic' },
        browser: currentScript.protocol === 'browser'
          ? { ...(baseReplayPayload.browser || {}), capture_screenshots: replayOptions.capture === 'script' ? baseReplayPayload.browser?.capture_screenshots : replayOptions.capture === 'errors' ? 'on_error' : 'per_action' }
          : baseReplayPayload.browser,
        group_name: replayOptions.groupName.trim() || `${currentScript.name || 'script'}-debug`,
        start_paused: startPaused,
        breakpoints: options?.runTo ? [{ ...options.runTo, once: true }, ...debugBreakpoints.filter(bp => !bp.once)] : debugBreakpoints.filter(bp => !bp.once),
        break_on_failure: replayOptions.breakOnFailure,
        log_level: replayOptions.logLevel,
        ...(replayOptions.runId.trim() ? { run_id: replayOptions.runId.trim() } : {}),
        // Unset does what a test run would; only an explicit choice is sent.
        ...(replayOptions.afterFailure !== 'test-run' ? { continue_on_error: replayOptions.afterFailure === 'continue' } : {}),
      }
      currentRunRef.current = {
        id: replaySessionId, options: structuredClone({ ...replayOptions, startPaused, liveTableSeedsText: undefined }), breakpoints: structuredClone(payload.breakpoints),
        target: currentScript.config.base_url || 'Action-specific targets', draft: scriptEditorDirty, dataSourceCount: currentScript.data_sources?.length || 0,
        failurePolicy: debugContinuesAfterFailure(replayOptions, currentScript) ? 'Continue on error' : 'Stop on error',
        inputs: (currentScript.parameters || []).map(p => ({ name: p.name, source: p.data_source || p.type, selection: p.selection || 'default', update: p.update, startRow: p.start_row, seed: p.seed })),
      }
      const replayGroup = payload.group_name || `${currentScript.name || 'script'}-replay`
      setUnavailableRuntimeSessionId(null)
      setActiveReplaySessionId(replaySessionId)
      setBrowserReplaySession(null)
      setReplaySeedVariables(variables || {})
      setReplayActivity([{
        id: 'queued',
        kind: 'start',
        title: 'Replay requested',
        detail: replayGroup,
        timestamp: new Date().toISOString(),
      }])
      setExpandedReplayBrowserArtifacts({})
      setReplayScreenshotSelection(null)
      setReplayReport({
        group: replayGroup,
        protocol: currentScript.protocol,
        started_at: new Date().toISOString(),
        completed_at: '',
        success: true,
        events: [],
        final_variables: variables,
      })
      setExpandedReplayEvent(null)

      // Replay uses the parsed current JSON draft. Apply that same snapshot to
      // the structured editor before dispatch so an accepted request with a
      // lost/408 response can still reconcile against the exact document that
      // actually ran.
      if (jsonDirty) commitJsonDraft(currentScript)
      startRequestDispatched = true
      const res = scriptId
        ? await api.startScriptDebugSession(scriptId, payload)
        : await api.startDraftScriptDebugSession(payload)
      if (!replayResourceIsCurrent()) return
      if (!res.session || !admitRuntimeSession(res.session, 'start', startRuntimeRequest)) {
        // Keep the generated public ID reachable while a bounded trusted GET
        // reconciles a malformed or overtaken start response.
        const attempt = replayStartAttemptRef.current
        if (attempt?.sessionId === replaySessionId && !attempt.terminalObserved) {
          attempt.awaitingReconciliation = true
          setActiveReplaySessionId(replaySessionId)
        }
        debugSessionStarted = true
        return
      }
      const replayWarnings = normalizeWarnings(res.warnings)
      setValidationReplayPreflight(res.preflight || res.session?.preflight || null)
      const responseSessionFinalized = isFinalizedDebugSession(res.session, res.session?.report)
      if (res.session) {
        setBrowserReplaySession(prev => {
          // A short replay can publish its terminal WebSocket message before
          // the start request resolves. Do not let that older start snapshot
          // move the authoritative terminal session back to "running".
          const previousTerminal = Boolean(prev && ['completed', 'failed', 'stopped'].includes(prev.status))
          return prev?.id === res.session!.id && previousTerminal && !responseSessionFinalized
            ? prev
            : res.session!
        })
        if (res.session.report) {
          setReplayReport(prev => prev?.completed_at && !res.session!.report!.completed_at
            ? prev
            : res.session!.report!)
          if (responseSessionFinalized) {
            if (followExecutionRef.current) setExpandedReplayEvent(replayEvidenceIndex(res.session.report))
          }
        }
        if (res.session.status === 'paused') {
          setReplayActivity(prev => upsertReplayActivity(prev, {
            id: 'paused',
            kind: 'event',
            title: 'Debugger paused',
            detail: res.session.current_action
              ? `${formatDebugPhaseLabel(res.session.current_action.phase)} ${res.session.current_action.action_index + 1}: ${res.session.current_action.action_name || 'next action'}${formatDebugLeafSuffix(res.session.current_action)}`
              : res.session.group || 'script debug',
            timestamp: new Date().toISOString(),
          }))
        }
      }
      const responseSessionId = res.debug_session_id || res.replay_session_id || res.session?.id || replaySessionId
      if (responseSessionFinalized && replayStartAttemptRef.current?.sessionId === replaySessionId) {
        replayStartAttemptRef.current.terminalObserved = true
      }
      setActiveReplaySessionId(previous => responseSessionFinalized || previous === null ? null : responseSessionId)
      if (responseSessionFinalized && res.session) {
        // The start response can itself contain the final result of a fast run.
        // Publish its completion before retiring the session: neither polling
        // nor a later WebSocket event is guaranteed to run after this point.
        const session = res.session
        const completedAt = session.report?.completed_at || session.completed_at || ''
        setReplayReport(prev => prev ? {
          ...prev,
          completed_at: completedAt,
          success: session.report?.success ?? session.status === 'completed',
          error: session.report?.error || session.error || prev.error,
        } : prev)
        setReplayActivity(prev => upsertReplayActivity(prev, {
          id: 'complete',
          kind: session.status === 'failed' ? 'error' : 'complete',
          title: session.status === 'completed' ? 'Debugger completed successfully'
            : session.status === 'stopped' ? 'Debugger stopped' : 'Debugger completed with failures',
          detail: session.report?.error || session.error,
          timestamp: completedAt,
          success: session.status === 'completed' ? true : session.status === 'failed' ? false : undefined,
        }))
        setReplaying(false)
      }
      setWarnings(replayWarnings)
      setSuccess('')
      debugSessionStarted = true
    } catch (e: unknown) {
      if (!replayResourceIsCurrent()) return
      // The start request and WebSocket are independent transports. A short
      // run can publish its authoritative terminal event before an ambiguous
      // HTTP/network failure arrives. In that case the live result wins; do
      // not erase it with stale request-failure cleanup.
      if (
        requestedReplaySessionId &&
        replayStartAttemptRef.current?.sessionId === requestedReplaySessionId &&
        replayStartAttemptRef.current.terminalObserved
      ) {
        debugSessionStarted = true
        return
      }
      const issues = getErrorIssues(e).join(' | ')
      const replayWarnings = normalizeWarnings(getErrorWarnings(e))
      const message = getErrorMessage(e, 'Replay failed')
      const currentAttempt = replayStartAttemptRef.current
      if (
        startRequestDispatched &&
        requestedReplaySessionId &&
        currentAttempt?.sessionId === requestedReplaySessionId &&
        !isDefiniteReplayStartRejection(e)
      ) {
        // A transport error or 5xx response cannot prove that the controller
        // rejected the request. Keep the client-generated session id active so
        // polling and later WebSocket events can reconcile an accepted run.
        currentAttempt.awaitingReconciliation = true
        setWarnings(replayWarnings)
        setReplayActivity(prev => upsertReplayActivity(prev, {
          id: 'start-reconcile',
          kind: 'event',
          title: 'Confirming replay start',
          detail: 'The start response was interrupted; checking the generated session ID with the controller.',
          timestamp: new Date().toISOString(),
        }))
        debugSessionStarted = true
        return
      }
      setWarnings(replayWarnings)
      setError(issues || message)
      if (message.includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
      invalidateRuntimeAuthority('Current state unavailable: debug start was rejected.')
      setReplayReport(null)
      setReplayEditableActionsKey(null)
      setReplayLayoutKey(null)
      setReplayDocumentFingerprint(null)
      setActiveReplaySessionId(null)
      setBrowserReplaySession(null)
      setExpandedReplayBrowserArtifacts({})
      setReplayScreenshotSelection(null)
      setReplaySeedVariables({})
      if (replayStartAttemptRef.current?.sessionId === requestedReplaySessionId) {
        replayStartAttemptRef.current = null
      }
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'error',
        kind: 'error',
        title: 'Replay failed before completion',
        detail: issues || message,
        timestamp: new Date().toISOString(),
        success: false,
      }))
    } finally {
      if (replayStartInFlightRef.current === replayResourceScope) {
        replayStartInFlightRef.current = null
      }
      if (replayResourceIsCurrent()) {
        setReplayStartPending(false)
        if (!debugSessionStarted) {
          setReplaying(false)
        }
      }
    }
  }, [api, admitRuntimeSession, invalidateRuntimeAuthority, publishRuntimeAdmission, runtimeRequest, commitJsonDraft, debugBreakpoints, jsonDirty, openReplayDebugger, projectId, replayOptions, scriptEditorDirty, resolveDraft, scriptId, setActiveReplaySessionId, setBrowserReplaySession, setDebuggerBottomTab, setError, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplayScreenshotSelection, setReplaySeedVariables, setReplaying, setSuccess, setWarnings])

  useEffect(() => {
    if (replayReport?.completed_at) {
      setSuccess(previous => /^(Starting|Stopping|Resuming|Pausing|Stop requested|Debugger started|Browser replay started)/.test(previous) ? '' : previous)
    }
  }, [replayReport?.completed_at, success, setSuccess])

  const performDebugMutation = useCallback(async (operation: () => Promise<{ session?: ScriptDebugSession } | void>) => {
    const scope = editorResourceScopeRef.current
    const sequence = ++debugMutationSequenceRef.current
    const { sessionId, incarnation, revision } = runtimeAdmissionRef.current
    const command: DebugRuntimeCommand = { sessionId, incarnation, revision }
    invalidateRuntimeAuthority('Current state unavailable: waiting for the debugger control response.')
    const request = runtimeRequest()
    try {
      const response = await operation()
      if (!editorMountedRef.current || editorResourceScopeRef.current !== scope) return null
      if (!response?.session && (!runtimeRequestIsCurrent(runtimeAdmissionRef.current, request) || debugMutationSequenceRef.current !== sequence)) return null
      if (!response?.session) { invalidateRuntimeAuthority('Current state unavailable: the debugger control response was incomplete.'); return null }
      // Admission still guards every state consumer, including a same-run privacy
      // failure carried by a delayed response. A command acknowledgement never
      // publishes rejected runtime detail or overwrites newer breakpoint state.
      const admitted = admitRuntimeSession(response.session, 'mutation', request)
      if (admitted) setBrowserReplaySession(response.session)
      if (debugMutationSequenceRef.current !== sequence || !runtimeCommandIsAcknowledged(runtimeAdmissionRef.current, command, response.session)) return null
      return { session: admitted ? response.session : null }
    } catch (cause) {
      if (!editorMountedRef.current || editorResourceScopeRef.current !== scope || debugMutationSequenceRef.current !== sequence || !runtimeRequestIsCurrent(runtimeAdmissionRef.current, request)) return null
      throw cause
    }
  }, [admitRuntimeSession, invalidateRuntimeAuthority, runtimeRequest, setBrowserReplaySession])

  const handleStopReplay = useCallback(async () => {
    if (!activeReplaySessionId) return
    setSuccess('Stopping debugger…')
    try {
      await performDebugMutation(() => api.stopScriptDebugSession(activeReplaySessionId))
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to stop debugger'))
    }
  }, [activeReplaySessionId, api, performDebugMutation, setError, setSuccess])

  const handlePauseResumeReplay = useCallback(async () => {
    if (!activeReplaySessionId || !browserReplaySession) return
    try {
      const isPaused = browserReplaySession.status === 'paused'
      await performDebugMutation(() => isPaused
        ? api.resumeScriptDebugSession(activeReplaySessionId)
        : api.pauseScriptDebugSession(activeReplaySessionId))
      setError('')
      setSuccess('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update debugger state'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, performDebugMutation, setError, setSuccess])

  const handleStepReplay = useCallback(async (mode: ScriptDebugStepMode = 'into') => {
    openReplayDebugger(false)
    if (!activeReplaySessionId || !browserReplaySession) {
      await handleReplay({ startPaused: true })
      return
    }
    if (browserReplaySession.status !== 'paused') return
    try {
      await performDebugMutation(() => api.stepScriptDebugSession(activeReplaySessionId, mode))
      setError('')
      setSuccess('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to step debugger'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, handleReplay, openReplayDebugger, performDebugMutation, setError, setSuccess])

  const clearDebugBreakpoints = async () => {
    setDebugBreakpoints([])
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      return
    }
    try {
      await performDebugMutation(() => api.updateScriptDebugBreakpoints(activeReplaySessionId, []))
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to clear debugger breakpoints'))
    }
  }

  const setBreakOnFailure = useCallback(async (enabled: boolean) => {
    setReplayOptions(prev => ({ ...prev, breakOnFailure: enabled }))
    // Push the change live only while a session is actually running/paused.
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      return
    }
    try {
      await performDebugMutation(() => api.updateScriptDebugOptions(activeReplaySessionId, { break_on_failure: enabled }))
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update break-on-failure'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, performDebugMutation, setError, setReplayOptions])

  const breakpointEntryKey = useCallback((bp: ScriptDebugBreakpoint) =>
    bp.step_path && bp.step_path.length > 0
      ? debugStepPathKey(bp.phase, bp.action_index, bp.step_path)
      : debugBreakpointKey(bp.phase, bp.action_index, bp.request_index), [])
  const debugBreakpointKeys = useMemo(() => new Set(debugBreakpoints.map(breakpointEntryKey)), [debugBreakpoints, breakpointEntryKey])

  // pushDebugBreakpoints commits a new breakpoint set to state and, when a
  // session is live, PATCHes it so changes apply mid-run.
  const pushDebugBreakpoints = useCallback(async (next: ScriptDebugBreakpoint[]) => {
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      setDebugBreakpoints(next)
      return
    }
    try {
      const acknowledgement = await performDebugMutation(() => api.updateScriptDebugBreakpoints(activeReplaySessionId, next))
      if (acknowledgement?.session) setDebugBreakpoints(acknowledgement.session.breakpoints || next)
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update debugger breakpoints'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, performDebugMutation, setDebugBreakpoints, setError])

  const toggleDebugBreakpoint = useCallback(async (phase: string, actionIndex: number, requestIndex?: number) => {
    const normalizedPhase = normalizeDebugPhase(phase)
    const normalized: ScriptDebugBreakpoint = requestIndex == null
      ? { phase: normalizedPhase, action_index: actionIndex }
      : { phase: normalizedPhase, action_index: actionIndex, request_index: requestIndex }
    const key = debugBreakpointKey(normalized.phase, normalized.action_index, requestIndex)
    const next = debugBreakpointKeys.has(key)
      ? debugBreakpoints.filter(bp => breakpointEntryKey(bp) !== key)
      : [...debugBreakpoints, normalized]
    await pushDebugBreakpoints(next)
  }, [breakpointEntryKey, debugBreakpointKeys, debugBreakpoints, pushDebugBreakpoints])

  // Toggle a breakpoint on a control-flow leaf, targeted by its static step path
  // (fires on every loop iteration of that step).
  const toggleStepPathBreakpoint = useCallback(async (phase: string, actionIndex: number, stepPath: string[]) => {
    const normalizedPhase = normalizeDebugPhase(phase)
    const key = debugStepPathKey(normalizedPhase, actionIndex, stepPath)
    const next = debugBreakpointKeys.has(key)
      ? debugBreakpoints.filter(bp => breakpointEntryKey(bp) !== key)
      : [...debugBreakpoints, { phase: normalizedPhase, action_index: actionIndex, step_path: stepPath }]
    await pushDebugBreakpoints(next)
  }, [breakpointEntryKey, debugBreakpointKeys, debugBreakpoints, pushDebugBreakpoints])

  // Set or clear breakpoints on every sub-request of a sequence at once.
  const setSequenceChildBreakpoints = useCallback(async (phase: string, actionIndex: number, requestCount: number, enabled: boolean) => {
    const normalizedPhase = normalizeDebugPhase(phase)
    const withoutChildren = debugBreakpoints.filter(bp => !(bp.phase === normalizedPhase && bp.action_index === actionIndex && bp.request_index != null))
    if (!enabled) {
      await pushDebugBreakpoints(withoutChildren)
      return
    }
    const children: ScriptDebugBreakpoint[] = Array.from({ length: requestCount }, (_, i) => ({ phase: normalizedPhase, action_index: actionIndex, request_index: i }))
    await pushDebugBreakpoints([...withoutChildren, ...children])
  }, [debugBreakpoints, pushDebugBreakpoints])

  const handleDownloadBrowserReplayExport = async () => {
    if (!replayReport) return

    const replaySessionId = activeReplaySessionId || browserReplaySession?.id
    const payload = {
      schema_version: 'apexload.script_debug_export.v1',
      exported_at: new Date().toISOString(),
      replay_session_id: replaySessionId,
      debug_session_id: replaySessionId,
      script: {
        id: scriptId || undefined,
        name: script.name,
        project_id: script.project_id || projectId,
        protocol: script.protocol,
      },
      replay_options: {
        group_name: replayReport.group || replayOptions.groupName || undefined,
        ignore_think_time: currentRunRef.current?.options.ignoreThinkTime,
        start_paused: currentRunRef.current?.options.startPaused,
        log_level: currentRunRef.current?.options.logLevel,
        break_on_failure: currentRunRef.current?.options.breakOnFailure,
        capture: currentRunRef.current?.options.capture,
        breakpoints: currentRunRef.current?.breakpoints,
        seed_variable_names: Object.keys(replaySeedVariables),
      },
      execution: currentRunRef.current ? { target: currentRunRef.current.target, draft: currentRunRef.current.draft, failure_policy: currentRunRef.current.failurePolicy, inputs: currentRunRef.current.inputs } : undefined,
      preflight: browserReplaySession?.preflight || replayPreflight,
      artifact_manifest: replayReport.artifact_manifest,
      diagnosis_summary: replayReport.diagnosis_summary,
      repair_suggestions: replayReport.repair_suggestions,
      session: browserReplaySession,
      report: replayReport,
    }

    const scriptBase = sanitizeDownloadName(script.name, 'script')
    const replayBase = sanitizeDownloadName(replaySessionId || replayReport.group || 'script-debug', 'script-debug')
    await exportGeneratedTextFile(
      JSON.stringify(payload, null, 2),
      `${scriptBase}-${replayBase}-debug.json`,
      'Exported debugger session JSON.',
    )
  }

  // Method labels are text: every colour here is a text-safe token.
  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success-text)'
      case 'POST': return 'var(--info-text)'
      case 'PUT': return 'var(--warning-text)'
      case 'DELETE': return 'var(--danger-text)'
      case 'PATCH': return 'var(--accent-text)'
      case 'http_sequence': return 'var(--accent-text)'
      case 'jms_publish': return 'var(--info-text)'
      case 'jms_consume': return 'var(--success-text)'
      case 'jms_request_reply': return 'var(--accent-text)'
      case 'jms_ack':
      case 'jms_commit':
      case 'jms_rollback': return 'var(--warning-text)'
      default: return 'var(--text-secondary)'
    }
  }

  const replayLatestActivity = replayActivity[replayActivity.length - 1]
  const currentRuntimeSnapshot = runtimeVariablesUnavailable ? null : runtimeAdmission.runtime?.snapshot || null
  const replayVariableInspector = runtimeVariablesUnavailable ? null : runtimeInspectionMode === 'current'
    ? currentRuntimeLookup(currentRuntimeSnapshot)
    : runtimeInspectionMode === 'selected'
      ? replayReport ? mergeReplayVariables(replaySeedVariables, replayReport.events.slice(0, (replayDebuggerController.selectedReplayEventIndex ?? -1) + 1)) : null
      : replayVariablesForInspection(replayReport, replaySeedVariables, false)
  const hasReplayVariableInspectorData = replayVariableInspector ? Object.keys(replayVariableInspector).length > 0 : false
  replayVariableInspectorRef.current = replayVariableInspector
  replayVariableQualificationRef.current = runtimeInspectionMode === 'current' ? 'Current paused state · held engine boundary' : 'Retained history · historical evidence'

  // Whether a recorded baseline exists for this script, so the debugger can
  // offer an auto record-vs-replay correlation diagnosis (vs HAR-upload).
  useEffect(() => {
    if (workspaceTab !== 'debug' || !scriptId || isBrowserScript) {
      setScriptRecordingStatus(null)
      setScriptRecordingStatusState('idle')
      return
    }
    let cancelled = false
    setScriptRecordingStatus(null)
    setScriptRecordingStatusState('loading')
    api.getScriptRecordingStatus(scriptId)
      .then(status => {
        if (!cancelled) {
          setScriptRecordingStatus(status)
          setScriptRecordingStatusState('ready')
        }
      })
      .catch(() => {
        if (!cancelled) {
          setScriptRecordingStatus(null)
          setScriptRecordingStatusState('error')
        }
      })
    return () => { cancelled = true }
  }, [api, scriptId, workspaceTab, isBrowserScript, projectId, scriptRecordingStatusRetryGeneration])
  const retryScriptRecordingStatus = () => {
    setScriptRecordingStatusRetryGeneration(generation => generation + 1)
  }
  const replayVariableDiffs = useMemo(
    () => replayReport && !runtimeVariablesUnavailable ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events) : [],
    [replayReport, replaySeedVariables, runtimeVariablesUnavailable],
  )
  const replayConfiguredVariables = useMemo(
    () => safeParseReplayVariables(replayOptions.variablesText),
    [replayOptions.variablesText],
  )
  // Raw JSON is an executable draft even before the user applies it back to the
  // structured authoring controls. Never reuse an older server/session preflight
  // for that draft: derive requirements from the exact valid script that the Run
  // handler will parse and send.
  const unreconciledReplayPreflight = useMemo(
    () => (!jsonDirty && (browserReplaySession?.preflight || validationReplayPreflight))
      || buildLocalReplayPreflight(currentDraftScript, replayConfiguredVariables),
    [browserReplaySession?.preflight, currentDraftScript, jsonDirty, replayConfiguredVariables, validationReplayPreflight],
  )
  const requiredProjectSecretNames = Array.from(new Set([
    ...(unreconciledReplayPreflight.required_secrets || []),
    ...(!jsonDirty && scriptSecretStatus
      ? scriptSecretStatus.required || []
      : []),
  ])).sort()
  const requiredProjectSecretNamesKey = requiredProjectSecretNames.join('\u0000')
	const secretScopeProjectId = currentDraftScript.project_id || projectId
  const preflightWithAllRequiredSecrets: ReplayPreflight = {
    ...unreconciledReplayPreflight,
    required_secrets: requiredProjectSecretNames,
  }

  useEffect(() => {
    const requestId = ++projectSecretProbeRequestRef.current
    const requiredNames = requiredProjectSecretNamesKey === ''
      ? []
      : requiredProjectSecretNamesKey.split('\u0000')
    if (!secretScopeProjectId || requiredNames.length === 0 || typeof api.getProjectSecretStatuses !== 'function') {
      setProjectSecretProbe(null)
      return
    }
    setProjectSecretProbe({
      projectId: secretScopeProjectId,
      namesKey: requiredProjectSecretNamesKey,
      statuses: [],
      state: 'loading',
    })
    void api.getProjectSecretStatuses(secretScopeProjectId, requiredNames)
      .then(response => {
		if (projectSecretProbeRequestRef.current !== requestId || response.project_id !== secretScopeProjectId) return
        setProjectSecretProbe({
          projectId: secretScopeProjectId,
          namesKey: requiredProjectSecretNamesKey,
          statuses: response.secrets || [],
          state: 'ready',
        })
      })
      .catch(() => {
        if (projectSecretProbeRequestRef.current === requestId) {
          setProjectSecretProbe({
            projectId: secretScopeProjectId,
            namesKey: requiredProjectSecretNamesKey,
            statuses: [],
            state: 'unavailable',
          })
        }
      })
    return () => {
      if (projectSecretProbeRequestRef.current === requestId) projectSecretProbeRequestRef.current += 1
    }
  }, [api, secretScopeProjectId, projectSecretProbeRetryGeneration, requiredProjectSecretNamesKey])

  const projectProbeIsCurrent = projectSecretProbe !== null
    && projectSecretProbe.projectId === secretScopeProjectId
    && projectSecretProbe.namesKey === requiredProjectSecretNamesKey
  const projectProbeIsReady = projectProbeIsCurrent && projectSecretProbe?.state === 'ready'
  const projectProbeIsUnavailable = projectProbeIsCurrent && projectSecretProbe?.state === 'unavailable'
  const persistedStatusByName = new Map((scriptSecretStatus && scriptSecretStatus.project_id === secretScopeProjectId ? scriptSecretStatus.secrets : [])
    .map(status => [status.name, status]))
  const persistedStatusCoversCurrentDraft = Boolean(scriptId)
    && requiredProjectSecretNames.every(name => persistedStatusByName.has(name))
  // A ready project-scoped probe is newer and authoritative. In particular, a
  // persisted true must never hide a fresh false after a provider value expires
  // or is removed. While a project-scoped probe is supported, replay waits for
  // that current answer; persisted script status is only a legacy-capability
  // fallback when the project probe endpoint is unavailable altogether.
  const canUsePersistedStatusFallback = persistedStatusCoversCurrentDraft
    && typeof api.getProjectSecretStatuses !== 'function'
  const secretStatuses = projectProbeIsReady
    ? projectSecretProbe?.statuses || []
    : canUsePersistedStatusFallback
      ? Array.from(persistedStatusByName.values())
      : []
  const secretStatusKnown = requiredProjectSecretNames.length === 0 || projectProbeIsReady || canUsePersistedStatusFallback
  const secretReconciledPreflight = reconcileReplayPreflightSecretStatus(
    preflightWithAllRequiredSecrets,
    secretStatuses,
    secretStatusKnown,
    projectProbeIsUnavailable,
  )
  const replayPreflight = errorCount || jsonParseMessage || (validationResultIsCurrent && validationSidebarResult?.tone === 'error') ? { ...secretReconciledPreflight, valid: false, checks: [...(secretReconciledPreflight.checks || []), { id: 'author-problems', label: 'Current draft', status: 'fail' as const, detail: 'The current draft has blocking issues. Open Author Problems to repair them.' }] } : secretReconciledPreflight
  // Readiness problems come from the preflight Checks & readiness shows, so
  // Author Problems, the top bar, the rail and the debugger agree with it.
  // Keyed by the facts' text: the preflight object is rebuilt every render.
  // Secrets count only once their status is known, not while it is checked.
  const readinessFactsJson = JSON.stringify({
    issues: secretReconciledPreflight.issues,
    blocking_variables: secretReconciledPreflight.blocking_variables,
    placeholder_variables: secretReconciledPreflight.placeholder_variables,
    missing_variables: secretReconciledPreflight.missing_variables,
    missing_secrets: secretStatusKnown ? secretReconciledPreflight.missing_secrets : [],
    data_source_issues: secretReconciledPreflight.data_source_issues,
    warnings: secretReconciledPreflight.warnings,
  } satisfies ReadinessFacts)
  // Text Author Problems already lists from validation or the last replay.
  const alreadyListedProblemsJson = JSON.stringify([...(validationResultIsCurrent ? validationSidebarResult?.issues || [] : []), ...warnings])
  const readinessIssues = useMemo(
    () => replayReadinessIssues(JSON.parse(readinessFactsJson) as ReadinessFacts, JSON.parse(alreadyListedProblemsJson) as string[]),
    [readinessFactsJson, alreadyListedProblemsJson],
  )
  const missingProjectSecrets = replayPreflight.missing_secrets || []
  replayMissingProjectSecretsRef.current = missingProjectSecrets
  replaySecretStatusKnownRef.current = secretStatusKnown
  const replayBlockedByUnknownSecretStatus = requiredProjectSecretNames.length > 0 && !secretStatusKnown
  const replayBlockedByMissingSecrets = missingProjectSecrets.length > 0 || replayBlockedByUnknownSecretStatus
  const replaySecretStatusState = replayBlockedByUnknownSecretStatus
    ? (projectProbeIsUnavailable || typeof api.getProjectSecretStatuses !== 'function' ? 'unavailable' : 'checking')
    : undefined

  useEffect(() => {
    if (validatedBinding === null || validationResultIsCurrent) return
    setValidationReplayPreflight(null)
    setValidationEvidence(null)
    clearValidatedBinding()
    setValidationSidebarResult(null)
  }, [clearValidatedBinding, validatedBinding, validationResultIsCurrent])
  const replayFailingEventIndex = replayReport?.events.findIndex(event => !event.success) ?? -1
  const replayFailingEvent = replayReport && replayFailingEventIndex >= 0 ? replayReport.events[replayFailingEventIndex] : null
  const replayVariableTimeline = replayReport
    ? replayReport.events.flatMap((event, index) => Object.entries(replayVariableDiffs[index] || {}).map(([name, diff]) => ({
        key: `${index}-${name}`,
        name,
        previous: diff.previous,
        next: diff.next,
        action: event.action_name || `Action ${index + 1}`,
        index,
      })))
    : []
  const isReplayPaused = browserReplaySession?.status === 'paused'
  const replayStartButtonLabel = replayOptions.startPaused
    ? (isBrowserScript ? 'Start Browser Step Through' : 'Start Step Through')
    : (isBrowserScript ? 'Run Browser Replay' : 'Run Replay')
  const replayRunButtonTitle = replayBlockedByUnknownSecretStatus
    ? (projectProbeIsUnavailable
        ? 'Project secret status is temporarily unavailable. Retry the credential check before running.'
        : 'Checking project secret configuration before running…')
    : replayBlockedByMissingSecrets
      ? `Configure missing project secret${missingProjectSecrets.length === 1 ? '' : 's'} before running: ${missingProjectSecrets.join(', ')}.`
      : replayStartButtonLabel
  const debugActionRows = scriptPhaseActionRows
  const stepReplayDisabled = replaying
    ? (!activeReplaySessionId || !isReplayPaused)
    : (saving || replayBlockedByMissingSecrets || !script.name || debugActionRows.length === 0)
  const breakpointSummary = `${debugBreakpoints.length} breakpoints across ${debugActionRows.length} executable action${debugActionRows.length === 1 ? '' : 's'}`
  useEffect(() => {
    if (hasReplayVariableInspectorData) {
      setDebugDetailCollapsed(false)
    }
  }, [hasReplayVariableInspectorData, setDebugDetailCollapsed])
  const scriptDslDocument = useMemo(() => buildDslDocument({ script }), [script])
  const selectedDebugCodeRow = workspaceTab === 'debug'
    ? (selectedReplayEvent ? selectedDebugRow : (debugActionRows[0] ?? null))
    : null
  const scriptCodeActivePath = workspaceTab === 'debug' && runtimeFrameSourcePath && replayEditableActionsKey === currentEditableActionsKey
    ? runtimeFrameSourcePath
    : workspaceTab === 'debug' && selectedReplayEvent
    ? codePathForDebugRow(selectedDebugCodeRow)
    : activeAutoParameterPath
      ? activeAutoParameterPath
      : authorCodeFocus
      ? codePathForAuthorFocus(authorCodeFocus)
      : activeAction !== null
        ? `script.actions[${activeAction}]`
      : ''
  const scriptCodeActiveRange = jsonDirty || jsonParseMessage
    ? null
    : lineRangeForTargetPath(scriptDslDocument.ranges, scriptCodeActivePath)
  const safeParameterMatchRanges = jsonDirty || jsonParseMessage ? [] : parameterMatchRanges
  const autoParameterCandidateMatchRanges = jsonDirty || jsonParseMessage
    ? []
    : findCandidateLiteralRangeInJson(
        jsonText,
        activeAutoParameterPath ? scriptCodeActiveRange : null,
        activeAutoParameterCandidate,
        normalizedAutoParameterOccurrenceIndex,
      )
  const scriptCodeStatusPath = scriptCodeActivePath.replace(/^script\./, '')

  // Floor matches the .actions-sidebar min-width so a narrow drag can't make the
  // panel content overflow its grid track.
  const clampAuthorRail = (px: number) => Math.max(200, Math.min(620, Math.round(px)))

  const startAuthorRailDrag = (event: React.MouseEvent) => {
    event.preventDefault()
    const startX = event.clientX
    const startW = authorRailRef.current?.offsetWidth ?? authorRailWidth ?? 240
    setAuthorRailResizing(true)
    const onMove = (ev: globalThis.MouseEvent) => setAuthorRailWidth(clampAuthorRail(startW + (ev.clientX - startX)))
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      window.removeEventListener('blur', onUp)
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
      setAuthorRailResizing(false)
    }
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'col-resize'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    window.addEventListener('blur', onUp)
  }

  useEffect(() => {
    if (authorRailWidth == null) return
    try { window.localStorage.setItem('authorRailWidth', String(authorRailWidth)) } catch { /* storage may be unavailable */ }
  }, [authorRailWidth])

  // Double-click the handle to snap the rail to its natural content width
  // (like double-clicking a column divider). Probes the content's max-content by
  // sizing the grid track, then sets a fixed px capped so it can't crowd out the
  // center/utility columns into an overflow.
  const fitAuthorRailToContent = () => {
    const el = authorRailRef.current
    const grid = el?.closest('.actions-content-grid') as HTMLElement | null
    if (!el || !grid) return
    // The outline fits names to the column and renders only the rows in view, so
    // it says how wide its whole names need it; the rest of the column is chrome.
    const tree = el.querySelector<HTMLElement>('.script-outline-treeview')
    const treeNeeds = outlineNaturalWidthRef.current?.() || 0
    const treeStyle = tree ? getComputedStyle(tree) : null
    const treeChrome = tree && treeStyle ? el.getBoundingClientRect().width - (tree.clientWidth - (parseFloat(treeStyle.paddingLeft) || 0) - (parseFloat(treeStyle.paddingRight) || 0)) : 0
    grid.style.setProperty('--author-rail-width', 'max-content')
    const natural = Math.max(Math.ceil(el.getBoundingClientRect().width), treeNeeds ? Math.ceil(treeNeeds + treeChrome) : 0)
    if (authorRailWidth != null) grid.style.setProperty('--author-rail-width', `${authorRailWidth}px`)
    else grid.style.removeProperty('--author-rail-width')
    // Reserve room for the center + utility columns at their widest minimums.
    const cap = Math.min(620, Math.max(280, Math.round(grid.getBoundingClientRect().width - (520 + 320))))
    setAuthorRailWidth(Math.max(200, Math.min(cap, natural)))
  }

  const scriptEditorShortcuts = useMemo(() => [
    {
      id: 'script.escape',
      label: 'Close debugger panel',
      keys: 'Escape',
      group: 'Script Editor',
      allowInEditable: true,
      priority: 850,
      onTrigger: () => {
        if (debuggerSettingsOpen) {
          setDebuggerSettingsOpen(false)
          debuggerSettingsBtnRef.current?.focus()
          return
        }
        if (workspaceTab === 'debug') {
          activateWorkspaceTab('author')
        }
      },
    },
    {
      id: 'script.save',
      label: 'Save script',
      keys: 'Cmd/Ctrl+S',
      group: 'Script Editor',
      disabled: saveScriptDisabled,
      allowInEditable: true,
      priority: 900,
      onTrigger: () => { void handleSaveRef.current() },
    },
    {
      id: 'script.debug.run',
      label: replaying ? 'Continue debugger' : 'Run debugger',
      keys: 'F5',
      group: 'Script Editor',
      disabled: !replaying && (saving || replayBlockedByMissingSecrets || !script.name || debugActionRows.length === 0),
      priority: 850,
      onTrigger: () => {
        if (replaying) {
          if (isReplayPaused) void handlePauseResumeReplay()
        } else {
          void handleReplay()
        }
      },
    },
    {
      id: 'script.debug.stop',
      label: 'Stop debugger',
      keys: 'Shift+F5',
      group: 'Script Editor',
      disabled: !replaying,
      priority: 850,
      onTrigger: () => { void handleStopReplay() },
    },
    {
      id: 'script.debug.step',
      label: 'Step debugger',
      keys: 'F10',
      group: 'Script Editor',
      disabled: !((replaying && isReplayPaused) || (!replaying && !saving && !replayBlockedByMissingSecrets && script.name && debugActionRows.length > 0)),
      priority: 850,
      onTrigger: () => { void handleStepReplay('into') },
    },
    {
      id: 'script.debug.step_over',
      label: 'Step over block',
      keys: 'F11',
      group: 'Script Editor',
      disabled: !(replaying && isReplayPaused),
      priority: 850,
      onTrigger: () => { void handleStepReplay('over') },
    },
    {
      id: 'script.debug.finish_loop',
      label: 'Finish loop',
      keys: 'Shift+F11',
      group: 'Script Editor',
      disabled: !(replaying && isReplayPaused),
      priority: 850,
      onTrigger: () => { void handleStepReplay('loop_end') },
    },
    {
      id: 'script.debug.breakpoint',
      label: 'Toggle breakpoint',
      keys: 'Cmd/Ctrl+B',
      group: 'Script Editor',
      disabled: debugActionRows.length === 0 && activeAction === null,
      priority: 850,
      onTrigger: () => {
        const row = workspaceTab === 'debug'
          ? (selectedReplayEvent ? selectedDebugRow : debugActionRows[0])
          : activeAction !== null
            ? { phase: 'action' as const, actionIndex: activeAction, action: script.actions[activeAction] }
            : debugActionRows[0]
        if (row) void toggleDebugBreakpoint(row.phase, row.actionIndex)
      },
    },
  ], [
    activateWorkspaceTab,
    activeAction,
    debugActionRows,
    debuggerSettingsOpen,
    handlePauseResumeReplay,
    handleReplay,
    handleStepReplay,
    handleStopReplay,
    isReplayPaused,
    replayBlockedByMissingSecrets,
    replaying,
    saveScriptDisabled,
    saving,
    script.actions,
    script.name,
    selectedReplayEvent,
    selectedDebugRow,
    setDebuggerSettingsOpen,
    debuggerSettingsBtnRef,
    toggleDebugBreakpoint,
    workspaceTab,
  ])
  useKeyboardShortcuts(scriptEditorShortcuts)

  // Reconcile session state by polling while a debug session is active. This is a
  // safety net for live updates that arrive before activeReplaySessionId is registered
  // (e.g. a breakpoint on the first action pauses before the start response resolves),
  // which the WebSocket handler would otherwise drop.
  useEffect(() => {
    if (!activeReplaySessionId || !replaying || replayStartPending) return
    const scope = editorResourceScopeRef.current
    let cancelled = false
    let inFlight = false
    const reconcile = async () => {
      if (inFlight) return
      inFlight = true
      const request = runtimeRequest()
      try {
        const res = await api.getScriptDebugSession(activeReplaySessionId)
        if (!editorMountedRef.current || editorResourceScopeRef.current !== scope) return
        if (!res?.session && (cancelled || !runtimeRequestIsCurrent(runtimeAdmissionRef.current, request))) return
        if (!res?.session) { invalidateRuntimeAuthority('Current state unavailable: session response was empty.'); return }
        const session = res.session
        if (!admitRuntimeSession(session, 'get', request) || cancelled) return
        if (runtimeEvidenceUnavailable(session, session.report)) setUnavailableRuntimeSessionId(session.id)
        if (
          replayStartAttemptRef.current?.sessionId === session.id &&
          replayStartAttemptRef.current.awaitingReconciliation
        ) {
          replayStartAttemptRef.current.awaitingReconciliation = false
          replayStartAttemptRef.current.notFoundCount = 0
          setReplayActivity(prev => upsertReplayActivity(prev, {
            id: 'start-reconcile',
            kind: 'start',
            title: 'Replay start confirmed',
            detail: session.group || 'script debug',
            timestamp: session.started_at || new Date().toISOString(),
          }))
        }
        setBrowserReplaySession(session)
        const terminalStatus = DEBUG_TERMINAL_STATUSES.has(session.status)
        const finalized = isFinalizedDebugSession(session, session.report)
        const terminalReport = finalized && session.report
          ? {
              ...session.report,
              replay_session_id: session.report.replay_session_id || session.id,
              completed_at: session.report.completed_at || session.completed_at || '',
            }
          : null
        setReplayReport(prev => {
          if (!prev) {
            return terminalReport || prev
          }
          // Merge live events while a run is in flight; only adopt the terminal
          // report (completed_at/success) once the session has actually finished.
          return {
            ...prev,
            events: session.events && session.events.length ? session.events : prev.events,
            ...(terminalReport || {}),
            ...(finalized && !terminalReport
              ? {
                  completed_at: session.completed_at || '',
                  success: session.status === 'completed',
                  error: session.error || prev.error,
                }
              : {}),
          }
        })
        if (terminalStatus && !finalized) {
          setReplayActivity(prev => upsertReplayActivity(prev, {
            id: 'stopping',
            kind: 'event',
            title: 'Debugger stopping',
            detail: session.error || 'Cancellation requested; waiting for the active action to finish.',
            timestamp: new Date().toISOString(),
          }))
        }
        if (finalized) {
          if (terminalReport && followExecutionRef.current) setExpandedReplayEvent(replayEvidenceIndex(terminalReport))
          // Polling is the loss-recovery path for a missed terminal WebSocket
          // message. Reconcile the lifecycle flags too, otherwise the UI keeps
          // showing a live run and correlation diagnosis remains disabled even
          // though the authoritative session is terminal.
          if (replayStartAttemptRef.current?.sessionId === session.id) {
            replayStartAttemptRef.current.terminalObserved = true
          }
          setActiveReplaySessionId(null)
          setReplaying(false)
        }
      } catch (e: unknown) {
        if (cancelled || !runtimeRequestIsCurrent(runtimeAdmissionRef.current, request)) return
        invalidateRuntimeAuthority('Current state unavailable: the controller could not confirm this session.')
        const unconfirmedStart = replayStartAttemptRef.current?.sessionId === activeReplaySessionId && Boolean(replayStartAttemptRef.current?.awaitingReconciliation)
        // While a start is unconfirmed every failed check counts toward the
        // bounded window, not just 404s: otherwise a controller that keeps
        // failing leaves the debugger "Starting" with live controls forever.
        if ((e instanceof ApiError && e.status === 404) || unconfirmedStart) {
          const pendingAttempt = replayStartAttemptRef.current
          if (
            pendingAttempt?.sessionId === activeReplaySessionId &&
            pendingAttempt.awaitingReconciliation &&
            pendingAttempt.notFoundCount < 4
          ) {
            // A client-selected ID can take a few polling intervals to become
            // visible after an ambiguous start response (for example through
            // a load balancer or HA replica). Do not declare it expired on the
            // first transient miss, but keep the retry window bounded.
            pendingAttempt.notFoundCount += 1
            return
          }
          debugMutationSequenceRef.current += 1
          setActiveReplaySessionId(null)
          setBrowserReplaySession(prev => prev ? { ...prev, status: 'stopped' } : null)
          setReplaying(false)
          const notStarted = unconfirmedStart && !(e instanceof ApiError && e.status === 404)
          setError(notStarted
            ? `The debugger could not be started: ${getErrorMessage(e, 'the controller did not respond')}. Start a new debug run to try again.`
            : 'Debugger session expired. Start a new debug run to continue.')
          if (notStarted) setDebuggerBottomTab('problems')
          setReplayActivity(prev => upsertReplayActivity(prev, {
            id: 'expired',
            kind: 'error',
            title: notStarted ? 'Debugger did not start' : 'Debugger session expired',
            detail: notStarted ? 'The controller could not confirm the debug session.' : 'The backend no longer has this debug session. Start a new run to continue.',
            timestamp: new Date().toISOString(),
            success: false,
          }))
          return
        }
        // Transient network/API errors retry on the next tick.
      } finally { inFlight = false }
    }
    void reconcile()
    const timer = window.setInterval(reconcile, 1200)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [activeReplaySessionId, replaying, replayStartPending, api, admitRuntimeSession, invalidateRuntimeAuthority, runtimeRequest, setActiveReplaySessionId, setBrowserReplaySession, setDebuggerBottomTab, setError, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplaying])

  const addParameter = (type = 'list') => {
    const parameters = script.parameters || []
    const name = ensureUniqueParameterName(`param_${parameters.length + 1}`, parameters)
    replaceScriptWithHistory({ ...script, parameters: [...parameters, applyParameterTypeDefaults({ ...EMPTY_PARAMETER, name, type }, type)] })
    setSelectedParameterIdx(parameters.length)
    setOpenDataEditor(null)
  }

  const addParameterDataset = () => {
    const sources = script.data_sources || []
    let index = sources.length + 1
    while (sources.some(source => source.name.toLowerCase() === `dataset_${index}`)) index++
    replaceScriptWithHistory({ ...script, data_sources: [...sources, { ...EMPTY_DATA_SOURCE, name: `dataset_${index}`, type: 'csv', first_row_header: true, inline_data: 'value\n' }] })
    setOpenDataEditor(sources.length)
  }

  const updateParameter = (idx: number, field: keyof ParameterConfig, value: unknown) => {
    if (field === 'name') {
      try {
        const next = renameScriptParameter(script, idx, String(value))
        if (next.parameters?.[idx]?.name === script.parameters?.[idx]?.name) return
        replaceScriptWithHistory(next)
        setSelectedOutlineParameterName(next.parameters?.[idx]?.name || null)
        setError('')
        setSuccess('Renamed parameter and updated all executable references. Undo is available.')
      } catch (error) { setError(error instanceof Error ? error.message : 'Unable to rename parameter.') }
      return
    }
    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const current = parameters[idx] || { ...EMPTY_PARAMETER }
      parameters[idx] = { ...current, [field]: value }
      return { ...prev, parameters }
    })
  }

  const upsertParameterFromActionEditor = ({ selectedText, requestedName, existingName, parameterType, typeConfig, reuseExisting }: ActionEditorParameterRequest): string => {
    const normalizedExistingName = existingName ? sanitizeVarName(existingName) : ''
    const resolvedName = ensureUniqueParameterName(
      requestedName || selectedText,
      script.parameters || [],
      normalizedExistingName || undefined,
    )
    const seedValue = (selectedText || '').trim()
    const builtin = isBuiltinParameterType(parameterType)
    const currentParameters = script.parameters || []
    const existingIdx = normalizedExistingName
      ? currentParameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === normalizedExistingName)
      : -1
    const byResolvedNameIdx = currentParameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === resolvedName)
    const selectedIdx = existingIdx >= 0 ? existingIdx : byResolvedNameIdx >= 0 ? byResolvedNameIdx : currentParameters.length

    if (reuseExisting && selectedIdx < currentParameters.length) {
      openCenterTab('parameter-data')
      setSelectedParameterIdx(selectedIdx)
      setError('')
      setSuccess(`Inserted existing parameter ${currentParameters[selectedIdx].name || resolvedName}.`)
      return currentParameters[selectedIdx].name || resolvedName
    }

    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const existingIdx = normalizedExistingName
        ? parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === normalizedExistingName)
        : -1
      const byResolvedNameIdx = parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === resolvedName)
      const targetIdx = existingIdx >= 0 ? existingIdx : byResolvedNameIdx

      if (targetIdx >= 0) {
        const current = { ...(parameters[targetIdx] || EMPTY_PARAMETER) }
        if (builtin) {
          parameters[targetIdx] = applyParameterTypeDefaults({ ...current, ...typeConfig, name: resolvedName, type: parameterType! }, parameterType!)
        } else {
          const nextParameter: ParameterConfig = {
            ...current,
            name: resolvedName,
            type: current.type || 'list',
            update: current.update || 'each_iteration',
          }
          if (nextParameter.type === 'list') {
            const values = [...(nextParameter.values || [])]
            if (seedValue && !values.includes(seedValue)) values.unshift(seedValue)
            nextParameter.values = values
          }
          parameters[targetIdx] = nextParameter
        }
      } else if (builtin) {
        parameters.push(applyParameterTypeDefaults({ ...EMPTY_PARAMETER, ...typeConfig, name: resolvedName, type: parameterType! }, parameterType!))
      } else {
        parameters.push({
          ...EMPTY_PARAMETER,
          name: resolvedName,
          type: 'list',
          update: 'each_iteration',
          values: seedValue ? [seedValue] : [],
        })
      }

      return { ...prev, parameters }
    })

    openCenterTab('parameter-data')
    setSelectedParameterIdx(selectedIdx)
    setError('')
    setSuccess(existingName ? `Updated parameter ${resolvedName}.` : `Created parameter ${resolvedName}.`)
    return resolvedName
  }

  const beginParameterEditFromActionEditor = (draft: ActionEditorParameterDraft) => {
    setPendingParameterDraft(draft)
    openCenterTab('parameter-data')
    setError('')
  }

  const applyJsonParameterDraft = (draft: ActionEditorParameterDraft) => {
    if (jsonText.slice(draft.start, draft.end) !== draft.selectedText) {
      setError('The script JSON changed since this selection was made. Re-select the value to parameterize.')
      setPendingParameterDraft(null)
      return
    }

    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) {
      setError(parsed.message)
      setDebuggerBottomTab('problems')
      return
    }

    const parameters = [...(parsed.script.parameters || [])]
    const normalizedExisting = draft.existingName ? sanitizeVarName(draft.existingName) : ''
    let resolvedName = ensureUniqueParameterName(
      draft.requestedName || draft.seedValue,
      parameters,
      normalizedExisting || undefined,
    )
    const seedValue = (draft.seedValue || '').trim()
    const existingIdx = normalizedExisting
      ? parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === normalizedExisting)
      : -1
    const byResolvedNameIdx = parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === resolvedName)
    const targetIdx = existingIdx >= 0 ? existingIdx : byResolvedNameIdx

    const builtin = isBuiltinParameterType(draft.parameterType)

    let paramIdx: number
    if (draft.reuseExisting) {
      if (targetIdx < 0) {
        setError(`Parameter ${draft.requestedName || draft.existingName || 'selection'} could not be found.`)
        return
      }
      resolvedName = parameters[targetIdx].name || resolvedName
      paramIdx = targetIdx
    } else if (targetIdx >= 0) {
      const current = { ...(parameters[targetIdx] || EMPTY_PARAMETER) }
      if (builtin) {
        parameters[targetIdx] = applyParameterTypeDefaults({ ...current, ...draft.typeConfig, name: resolvedName, type: draft.parameterType! }, draft.parameterType!)
      } else {
        const nextParameter: ParameterConfig = {
          ...current,
          name: resolvedName,
          type: current.type || 'list',
          update: current.update || 'each_iteration',
        }
        if (nextParameter.type === 'list') {
          const values = [...(nextParameter.values || [])]
          if (seedValue && !values.includes(seedValue)) values.unshift(seedValue)
          nextParameter.values = values
        }
        parameters[targetIdx] = nextParameter
      }
      paramIdx = targetIdx
    } else if (builtin) {
      parameters.push(applyParameterTypeDefaults({ ...EMPTY_PARAMETER, ...draft.typeConfig, name: resolvedName, type: draft.parameterType! }, draft.parameterType!))
      paramIdx = parameters.length - 1
    } else {
      parameters.push({
        ...EMPTY_PARAMETER,
        name: resolvedName,
        type: 'list',
        update: 'each_iteration',
        values: seedValue ? [seedValue] : [],
      })
      paramIdx = parameters.length - 1
    }

    const replacement = `{{param.${resolvedName}}}`
    const rawRanges = [{ from: draft.start, to: draft.end }, ...(draft.replacementRanges || [])]
    const ranges = Array.from(
      new Map(rawRanges.map(range => [`${range.from}:${range.to}`, range])).values(),
    ).sort((left, right) => left.from - right.from)
    const invalidRange = ranges.find(range =>
      range.from < 0 ||
      range.to > jsonText.length ||
      range.from >= range.to ||
      jsonText.slice(range.from, range.to) !== draft.selectedText,
    )
    if (invalidRange) {
      setError('The script JSON changed since matching occurrences were reviewed. Re-select the value to parameterize.')
      setPendingParameterDraft(null)
      setJsonOccurrenceReview(null)
      return
    }
    let nextText = jsonText
    ranges
      .slice()
      .sort((left, right) => right.from - left.from)
      .forEach(range => {
        nextText = `${nextText.slice(0, range.from)}${replacement}${nextText.slice(range.to)}`
      })
    const parsedNext = parseScriptJsonDraft(nextText)
    if (!parsedNext.ok) {
      setError(parsedNext.message)
      setDebuggerBottomTab('problems')
      return
    }
    const nextScript: ScriptData = { ...parsedNext.script, parameters }

    pushEditorHistory(script)
    const occurrenceMessage = ranges.length > 1 ? ` Replaced ${ranges.length} occurrences.` : ''
    commitJsonDraft(
      nextScript,
      draft.reuseExisting
        ? `Inserted existing parameter ${resolvedName}.${occurrenceMessage}`
        : `${draft.existingName ? 'Updated' : 'Created'} parameter ${resolvedName}.${occurrenceMessage}`,
    )
    setPendingParameterDraft(null)
    setJsonOccurrenceReview(null)
    setExistingParameterChoice(null)
    setFocusParameterIdx(paramIdx)
    openCenterTab('parameter-data')
  }

  const applyParameterDraft = (draft: ActionEditorParameterDraft) => {
    if (draft.source === 'json') {
      applyJsonParameterDraft(draft)
      return
    }

    pushEditorHistory(script)
    const phase = draft.phase || 'action'
    const name = upsertParameterFromActionEditor({
      selectedText: draft.seedValue,
      requestedName: draft.requestedName,
      existingName: draft.existingName,
      parameterType: draft.parameterType,
      typeConfig: draft.typeConfig,
      reuseExisting: draft.reuseExisting,
    })
    const key = `${phase}:${draft.actionIndex}${draft.stepPath?.length ? `#${draft.stepPath.join('/')}` : ''}`
    const action = resolveAuthorJsonScopeByKey(script, key)?.action
    if (action) {
      const body = action.body || ''
      const replacement = `{{param.${name}}}`
      const nextBody = `${body.slice(0, draft.start)}${replacement}${body.slice(draft.end)}`
      updatePhaseActionList(phase, actions => {
        actions[draft.actionIndex] = updateNestedAction(actions[draft.actionIndex], draft.stepPath, leaf => ({ ...leaf, body: nextBody }))
        return actions
      })
    }
    setPendingParameterDraft(null)
  }

  const applyPendingParameterDraft = () => {
    if (pendingParameterDraft) applyParameterDraft(pendingParameterDraft)
  }

  // Opens the Parameterize choice dialog (File vs Built-in Function) for a fresh selection.
  const openParameterizeChoice = (draft: ActionEditorParameterDraft) => {
    setParameterizeChoice(draft)
    setExistingParameterChoice(null)
    setJsonOccurrenceReview(null)
    setError('')
  }

  const openExistingParameterChoice = (draft: ActionEditorParameterDraft) => {
    setExistingParameterChoice(draft)
    setParameterizeChoice(null)
    setJsonOccurrenceReview(null)
    setError('')
  }

  const openJsonOccurrenceReview = (draft: ActionEditorParameterDraft) => {
    if (draft.source !== 'json') {
      openParameterizeChoice(draft)
      return
    }
    if (jsonText.slice(draft.start, draft.end) !== draft.selectedText) {
      setError('The script JSON changed since this selection was made. Re-select the value to parameterize.')
      return
    }
    const occurrences = findJsonStringOccurrences(jsonText, draft.selectedText, { from: draft.start, to: draft.end })
      .filter(occurrence =>
        !occurrence.path.startsWith('$.parameters') &&
        !occurrence.path.startsWith('$.script.parameters') &&
        !occurrence.path.startsWith('$.data_sources') &&
        !occurrence.path.startsWith('$.script.data_sources'),
      )
    setJsonOccurrenceReview({ draft, occurrences })
    setExistingParameterChoice(null)
    setParameterizeChoice(null)
    setError('')
  }

  // "File / Data" branch: fall back to the existing inline-value flow in the Parameter Data tab.
  const chooseParameterizeFile = () => {
    if (!parameterizeChoice) return
    const draft = parameterizeChoice
    setParameterizeChoice(null)
    beginParameterEditFromActionEditor(draft)
  }

  // "Built-in Function" branch: create a runtime-generated parameter of the chosen type and
  // insert its {{param.NAME}} token immediately.
  const confirmBuiltinParameterize = (name: string, type: string, typeConfig: Partial<ParameterConfig>) => {
    if (!parameterizeChoice) return
    const draft: ActionEditorParameterDraft = {
      ...parameterizeChoice,
      requestedName: name,
      parameterType: type,
      typeConfig,
    }
    setParameterizeChoice(null)
    applyParameterDraft(draft)
  }

  const applyExistingParameterChoice = (parameterName: string, replacementRanges: Array<{ from: number; to: number }> = []) => {
    const draft = existingParameterChoice || jsonOccurrenceReview?.draft
    if (!draft || !parameterName.trim()) return
    const parameter = (currentDraftScript.parameters || []).find(candidate =>
      sanitizeVarName(candidate.name || '') === sanitizeVarName(parameterName),
    )
    if (!parameter?.name) {
      setError(`Parameter ${parameterName} could not be found.`)
      return
    }
    applyParameterDraft({
      ...draft,
      existingName: parameter.name,
      requestedName: parameter.name,
      seedValue: draft.seedValue,
      replacementRanges,
      reuseExisting: true,
    })
  }

  const createParameterFromOccurrenceReview = (replacementRanges: Array<{ from: number; to: number }>) => {
    if (!jsonOccurrenceReview) return
    setParameterizeChoice({
      ...jsonOccurrenceReview.draft,
      replacementRanges,
    })
    setJsonOccurrenceReview(null)
    setExistingParameterChoice(null)
  }

  const updatePendingParameterDraftName = (value: string) => {
    setPendingParameterDraft(prev => (prev ? { ...prev, requestedName: sanitizeVarName(value) } : prev))
  }

  const updatePendingParameterDraftValue = (value: string) => {
    setPendingParameterDraft(prev => (prev ? { ...prev, seedValue: value } : prev))
  }

  const clearPendingParameterDraft = () => {
    setPendingParameterDraft(null)
  }

  const {
    selectionState: jsonParameterSelection,
    openSelectionMenu: openJsonParameterSelectionMenu,
    openParameterEditor: openJsonParameterEditor,
    openExistingParameterChooser: openJsonExistingParameterChooser,
    openOccurrenceReview: openJsonParameterOccurrenceReview,
  } = useJsonParameterMenu({
    parameters: currentDraftScript.parameters || [],
    onBeginParameterEdit: openParameterizeChoice,
    onChooseExistingParameter: openExistingParameterChoice,
    onReviewOccurrences: openJsonOccurrenceReview,
  })

  const toggleDataEditorModeAt = (idx: number) => {
    setDataEditorMode(prev => ({ ...prev, [idx]: (prev[idx] || 'grid') === 'grid' ? 'raw' : 'grid' }))
  }

  const mutateDataSource = (idx: number, mutator: (current: DataSourceConfig) => DataSourceConfig, recordHistory = true): boolean => {
    const source = script.data_sources?.[idx]
    if (!source) return false
    const proposed = mutator(cloneDataSource(source))
    try { validateDatasetReplacement(script, idx, proposed) }
    catch (error) { setError(error instanceof Error ? error.message : 'Unable to change dataset.'); return false }
    if (recordHistory) setDataSourceHistory(history => {
      const entry = history[idx] || { past: [], future: [] }
      return { ...history, [idx]: { past: [...entry.past, cloneDataSource(source)].slice(-100), future: [] } }
    })
    setScript(prev => ({ ...prev, data_sources: prev.data_sources?.map((item, index) => index === idx ? proposed : item) }))
    setError('')
    return true
  }

  const updateDataSource = (idx: number, field: keyof DataSourceConfig, value: unknown) => {
    proposeDatasetChange(idx, current => ({ ...current, [field]: value }), 'Review dataset format or raw data change')
  }

  const undoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    const source = script.data_sources?.[idx]
    if (!source || !entry?.past.length) return
    if (mutateDataSource(idx, () => cloneDataSource(entry.past[entry.past.length - 1]), false)) {
      setDataSourceHistory(history => ({ ...history, [idx]: { past: entry.past.slice(0, -1), future: [cloneDataSource(source), ...entry.future].slice(0, 100) } }))
    }
  }

  const redoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    const source = script.data_sources?.[idx]
    if (!source || !entry?.future.length) return
    if (mutateDataSource(idx, () => cloneDataSource(entry.future[0]), false)) {
      setDataSourceHistory(history => ({ ...history, [idx]: { past: [...entry.past, cloneDataSource(source)].slice(-100), future: entry.future.slice(1) } }))
    }
  }

  const proposeDatasetChange = (index: number, mutate: (source: DataSourceConfig) => DataSourceConfig, title = 'Review dataset transformation') => {
    const before = script.data_sources?.[index]
    if (!before) return
    setDatasetChange({ index, before: cloneDataSource(before), after: mutate(cloneDataSource(before)), title })
  }
  const datasetChangeError = (() => {
    if (!datasetChange) return ''
    if (JSON.stringify(script.data_sources?.[datasetChange.index]) !== JSON.stringify(datasetChange.before)) return 'The dataset changed while this review was open. Cancel and prepare the change again.'
    try { validateDatasetReplacement(script, datasetChange.index, datasetChange.after); return '' }
    catch (error) { return error instanceof Error ? error.message : 'Dataset change is invalid.' }
  })()
  const applyDatasetChange = () => {
    if (!datasetChange || datasetChangeError) return
    const sources = [...(script.data_sources || [])]
    sources[datasetChange.index] = datasetChange.after
    replaceScriptWithHistory({ ...script, data_sources: sources })
    setDataSourceHistory({})
    setDatasetChange(null)
    setError('')
    setSuccess('Applied dataset change. Undo structural edit restores the previous source.')
  }

  const importDataSourceFile = async (idx: number, file: File | null) => {
    if (!file) return
    if (file.size > 8 * 1024 * 1024) { setError('Inline dataset imports are limited to 8 MB.'); return }
    const before = script.data_sources?.[idx]
    if (!before) return
    try {
      const text = await file.text()
      setDatasetChange({ index: idx, before: cloneDataSource(before), after: { ...cloneDataSource(before), inline_data: text, path: undefined, storage_ref: undefined, staged_asset: undefined }, title: `Import ${file.name}` })
      setError('')
    } catch {
      setError('Failed to import parameter file')
    }
  }

  const exportDataSourceFile = async (source: DataSourceConfig, idx: number) => {
    const content = source.inline_data || ''
    if (!content.trim()) {
      setError('No inline parameter data to export for this source')
      return
    }
    const base = (source.name || `source_${idx + 1}`).replace(/[^a-zA-Z0-9_-]+/g, '_')
    const ext = (source.type || 'csv').toLowerCase() === 'dat' ? 'dat' : 'csv'
    await exportGeneratedTextFile(content, `${base}.${ext}`, `Exported ${base}.${ext}`)
  }

  const mergeParameterSourceFiles = () => {
    const merge = buildMergedParameterSourceScript(script)
    if (!merge) {
      setError('Merge requires compatible .dat or .csv sources with equal row counts, matching strategies, and no unbound or directly referenced columns.')
      return
    }

    pushEditorHistory(script)
    setScript(merge.script)
    setOpenDataEditor(merge.mergedSourceIndex)
    setDataSourceHistory({})
    setDataEditorMode({ [merge.mergedSourceIndex]: 'grid' })
    setError('')
    setSuccess(`Merged ${merge.sourceCount} parameter source files into ${merge.mergedSourceName}. Save Script to make this file change persist.`)
  }

  const updateDataCell = (sourceIdx: number, rowIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.map(row => [...row])
    while (nextRows.length <= rowIdx) nextRows.push(Array(columns.length).fill(''))
    while (nextRows[rowIdx].length < columns.length) nextRows[rowIdx].push('')
    nextRows[rowIdx][colIdx] = value
    mutateDataSource(sourceIdx, current => ({ ...current, inline_data: stringifyInlineData(source, columns, nextRows) }))
  }

  const updateDataColumnName = (sourceIdx: number, colIdx: number, value: string) => {
    try {
      replaceScriptWithHistory(renameDatasetColumn(script, sourceIdx, colIdx, value))
      setDataSourceHistory({})
      setError('')
      setSuccess('Renamed column and updated parameter mappings and executable references. Undo is available.')
    } catch (error) { setError(error instanceof Error ? error.message : 'Unable to rename column.') }
  }

  const addDataRow = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = [...rows, Array(columns.length).fill('')]
    mutateDataSource(sourceIdx, current => ({ ...current, inline_data: stringifyInlineData(source, columns, nextRows) }))
  }

  const removeDataRow = (sourceIdx: number, rowIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.filter((_, idx) => idx !== rowIdx)
    mutateDataSource(sourceIdx, current => ({ ...current, inline_data: stringifyInlineData(source, columns, nextRows) }))
  }

  const addDataColumn = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns, `col_${columns.length + 1}`]
    const nextRows = rows.map(row => [...row, ''])
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const removeDataColumn = (sourceIdx: number, colIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    if (columns.length <= 1) return
    const nextColumns = columns.filter((_, idx) => idx !== colIdx)
    const nextRows = rows.map(row => row.filter((_, idx) => idx !== colIdx))
    proposeDatasetChange(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const autoFixDataSource = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = normalizeHeaders(parsed.columns)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < fixedColumns.length) next.push('')
      return next.slice(0, fixedColumns.length)
    })
    proposeDatasetChange(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, fixedRows),
    }))
  }

  const applyFillHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = fillEmptyHeaders(parsed.columns)
    proposeDatasetChange(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
  }

  const applyDedupeHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = dedupeHeaders(parsed.columns)
    proposeDatasetChange(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
  }

  const applyNormalizeRows = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < parsed.columns.length) next.push('')
      return next.slice(0, parsed.columns.length)
    })
    proposeDatasetChange(sourceIdx, current => ({ ...current, inline_data: stringifyInlineData(source, parsed.columns, fixedRows) }))
  }

  const selectAutoParameterCandidate = (candidateId: string, occurrenceIndex = 0) => {
    setActiveAutoParameterCandidateId(candidateId)
    setActiveAutoParameterOccurrenceIndex(occurrenceIndex)
    setSelectedOutlineParameterName(null)
    setAuthorCodeFocus(null)
    setScriptJsonSidebarTab('auto-params')
    setAuthorRightSidebarCollapsed(false)
    openCenterTab(SCRIPT_JSON_TAB_ID)
  }

  const moveAutoParameterOccurrence = (delta: number) => {
    if (!activeAutoParameterCandidate) return
    setSelectedOutlineParameterName(null)
    setAuthorCodeFocus(null)
    setAuthorRightSidebarCollapsed(false)
    setActiveAutoParameterOccurrenceIndex(index => normalizeOccurrenceIndex(activeAutoParameterCandidate.occurrences.length, index + delta))
  }

  const scanForAutoParameters = () => {
    let sourceScript = script
    if (jsonDirty || jsonParseMessage) {
      const parsed = parseScriptJsonDraft(jsonText)
      if (!parsed.ok) {
        setParameterCandidates([])
        setActiveAutoParameterCandidateId(null)
        setActiveAutoParameterOccurrenceIndex(0)
        setScriptJsonSidebarTab(SCRIPT_JSON_DEFAULT_COMPANION_TAB)
        setError(parsed.message)
        setSuccess('')
        setDebuggerBottomTab('problems')
        openCenterTab(SCRIPT_JSON_TAB_ID)
        return
      }
      sourceScript = parsed.script
      commitJsonDraft(sourceScript)
    }

    const candidates = buildAutoParameterCandidates(sourceScript)
    openCenterTab(SCRIPT_JSON_TAB_ID)
    setScriptJsonSidebarTab('auto-params')
    setAuthorRightSidebarCollapsed(false)
    setSelectedOutlineParameterName(null)
    setAuthorCodeFocus(null)
    setParameterCandidates(candidates)
    if (candidates.length === 0) {
      setActiveAutoParameterCandidateId(null)
      setActiveAutoParameterOccurrenceIndex(0)
      setSuccess('No strong parameterization candidates were detected.')
      setError('')
      return
    }
    setActiveAutoParameterCandidateId(candidates[0].id)
    setActiveAutoParameterOccurrenceIndex(0)
    setSuccess(`Detected ${candidates.length} parameter candidates. Review and apply below.`)
    setError('')
  }

  const updateCandidate = (id: string, patch: Partial<ParameterCandidate>) => {
    setParameterCandidates(prev => prev.map(candidate => candidate.id === id ? { ...candidate, ...patch } : candidate))
  }

  const applyAutoParameterization = () => {
    const selected = parameterCandidates
      .filter(candidate => candidate.selected)
      .map(candidate => ({ ...candidate, varName: sanitizeVarName(candidate.varName) }))
      .filter(candidate => candidate.varName.length > 0)

    if (selected.length === 0) {
      setError('Select at least one candidate before applying auto-parameterization.')
      return
    }

    const replacementMap = new Map<string, string>()
    selected.forEach(candidate => {
      replacementMap.set(candidate.id, `{{data.auto_params.${candidate.varName}}}`)
    })

    pushEditorHistory(script)
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      next.actions = next.actions.map((action, actionIdx) => {
        const updated = {
          ...action,
          headers: { ...(action.headers || {}) },
          header_list: action.header_list?.map(header => ({ ...header })),
        }
        selected.forEach(candidate => {
          candidate.occurrences
            .filter(occ => occ.actionIdx === actionIdx)
            .forEach(occ => {
              const token = replacementMap.get(candidate.id) || candidate.value
              if (occ.field === 'target' && updated.target) {
                updated.target = updated.target.split(candidate.value).join(token)
              } else if (occ.field === 'body' && typeof updated.body === 'string') {
                updated.body = updated.body.split(candidate.value).join(token)
              } else if (
                occ.field === 'header'
                && typeof occ.headerIndex === 'number'
                && updated.header_list?.[occ.headerIndex]
              ) {
                updated.header_list[occ.headerIndex].value = updated.header_list[occ.headerIndex].value
                  .split(candidate.value)
                  .join(token)
              } else if (occ.field === 'header' && occ.headerKey && updated.headers && updated.headers[occ.headerKey]) {
                updated.headers[occ.headerKey] = updated.headers[occ.headerKey].split(candidate.value).join(token)
              }
            })
        })
        if (updated.header_list?.length) {
          updated.headers = headerListToCompatibilityMap(updated.header_list, updated.headers)
        }
        return updated
      })

      const autoSourceIdx = (next.data_sources || []).findIndex(source => source.name === 'auto_params')
      const existing = autoSourceIdx >= 0 ? next.data_sources?.[autoSourceIdx] : undefined
      const existingParsed = existing ? parseInlineData(existing) : { columns: [] as string[], rows: [] as string[][] }
      const mergedColumns = [...existingParsed.columns]
      selected.forEach(candidate => {
        if (!mergedColumns.includes(candidate.varName)) mergedColumns.push(candidate.varName)
      })

      // Build at least `autoParamSeedRows` rows. The first row carries the detected literals;
      // additional rows preserve any existing data and otherwise start blank for the operator to
      // fill, so VUsers can draw distinct values instead of all sending the same seed.
      const rowCount = Math.max(1, autoParamSeedRows, existingParsed.rows.length)
      const mergedRows: string[][] = Array.from({ length: rowCount }, (_unused, rowIdx) =>
        mergedColumns.map((column, idx) => {
          const selectedMatch = selected.find(candidate => candidate.varName === column)
          if (rowIdx === 0 && selectedMatch) return selectedMatch.value
          return existingParsed.rows[rowIdx]?.[idx] || ''
        }),
      )

      const sourcePayload: DataSourceConfig = {
        ...(existing || EMPTY_DATA_SOURCE),
        name: 'auto_params',
        type: (existing?.type || 'csv'),
        strategy: existing?.strategy || 'sequential',
        first_row_header: true,
        columns: [],
        inline_data: stringifyInlineData(
          { ...(existing || EMPTY_DATA_SOURCE), type: existing?.type || 'csv', delimiter: existing?.delimiter || 'auto' },
          mergedColumns,
          mergedRows,
        ),
      }

      if (!next.data_sources) next.data_sources = []
      if (autoSourceIdx >= 0) next.data_sources[autoSourceIdx] = sourcePayload
      else next.data_sources.push(sourcePayload)
      return next
    })

    setSuccess(`Applied auto-parameterization for ${selected.length} candidate${selected.length === 1 ? '' : 's'}.`)
    setError('')
  }

  // Deep scan: ask the backend engine to analyze the saved script (data params + secrets).
  // Correlations need captured observations the editor lacks, so they're out of scope here.
  const runServerParameterization = async () => {
    if (!scriptId || deepScanning) return
    setDeepScanning(true)
    setError('')
    try {
      const result = await api.suggestParameterization(scriptId)
      if (!result.suggestions || result.suggestions.length === 0) {
        setServerParameterization(null)
        setSuccess('Server scan found no data-parameter or secret candidates.')
        return
      }
      setServerParameterization(result)
      setSuccess('')
    } catch (e) {
      setError(getErrorMessage(e, 'Server parameterization scan failed'))
    } finally {
      setDeepScanning(false)
    }
  }

  const openEditorSection = (section: 'config' | 'replay' | 'runtime' | 'import' | 'parameter-data' | 'object-descriptors' | 'http-conversion') => {
    setNarrowAuthorPane('editor')
    const tabBySection: Record<typeof section, CenterWorkbenchTabId> = {
      config: 'runtime-settings',
      replay: SCRIPT_JSON_TAB_ID,
      runtime: 'runtime-settings',
      import: 'imported-metadata',
      'parameter-data': 'parameter-data',
      'object-descriptors': 'object-descriptors',
      'http-conversion': 'http-conversion',
    }
    openCenterTab(tabBySection[section])
    if (section === 'config') {
      selectRuntimeSettingsSection('script_config')
    } else if (section === 'runtime') {
      selectRuntimeSettingsSection('think')
    }
    setOpenSections(prev => ({ ...prev, [section]: true }))
  }

  const applyObjectDescriptorCandidate = (row: Web2ObjectDescriptorRow, candidate: Web2SelectorCandidate) => {
    if (row.actionIndex === null) {
      setError('No generated browser action is available for this imported object descriptor.')
      return
    }
    updateAction(row.actionIndex, 'selector', candidate.value)
    selectAuthorAction(row.actionIndex)
    setSuccess(`Applied ${candidate.kind} selector to ${row.label}.`)
    setError('')
  }

  const applyMigrationSuggestion = (suggestion: MigrationAssistantSuggestion) => {
    const result = applyMigrationAssistantSuggestion(script, suggestion.key)
    replaceScriptWithHistory(result.script)
    recordAssistantChange(suggestion.title, result.summary)
    setSuccess(result.summary)
    setError('')
  }

  const applyRepairSuggestion = (suggestion: ScriptRepairSuggestion) => {
    if (suggestion.key === 'stabilize_browser_selectors') {
      openCenterTab(ACTION_LIST_TAB_ID)
      const index = script.actions.findIndex(action => action.type.startsWith('browser_') && /nth-child|>/.test(action.selector || action.target || ''))
      if (index >= 0) selectAuthorAction(index)
      setSuccess('Review the effective selector against the captured DOM. No selectors changed.')
      return
    }
    const result = applyScriptRepairSuggestion(script, suggestion.key)
    replaceScriptWithHistory(result.script)
    recordAssistantChange(suggestion.title, result.summary)
    setSuccess(result.summary)
    setError('')
  }

  const insertAuthoringComponent = (component: AuthoringComponent) => {
    try {
      // A block keeps its declared init/teardown and data prerequisites. Main
      // actions follow the same explicit placement as individual insertions.
      // A saved block's table steps get result prefixes this script does not use yet.
      const lists = [component.init || [], component.actions || [], component.teardown || []]
      const { actions: renamed } = withUniqueResultPrefixes(lists.flat(), takenVariableNames(script))
      const unique = { ...component, init: renamed.slice(0, lists[0].length), actions: renamed.slice(lists[0].length, lists[0].length + lists[1].length), teardown: renamed.slice(lists[0].length + lists[1].length) }
      const result = applyAuthoringComponentToScript(script, unique, null)
      const generated = result.script.actions.slice(script.actions.length)
      const initCount = component.init?.length || 0
      const mainCount = component.actions?.length || 0
      let next = { ...result.script, actions: [...script.actions],
        init: [...(script.init || []), ...generated.slice(0, initCount)],
        teardown: [...(script.teardown || []), ...generated.slice(initCount + mainCount)],
      }
      let focus = authorCodeFocus
      let breakpoints = debugBreakpoints
      for (const [index, action] of generated.slice(initCount, initCount + mainCount).entries()) {
        const inserted = insertAuthorAction(next, action, insertPhase, index ? 'after' : insertPosition, focus)
        next = { ...inserted.script, init: inserted.script.init || [], teardown: inserted.script.teardown || [] }
        focus = inserted.focus
        breakpoints = remapInsertedBreakpoints(breakpoints, focus)
      }
      replaceScriptWithHistory(next)
      setDebugBreakpoints(breakpoints)
      if (focus) { setAuthorCodeFocus(focus); setActiveAction(focus.phase === 'action' && !focus.stepPath?.length ? focus.actionIndex : null) }
      setInsertSeed(null)
      setAuthorOverview(false)
      setNarrowAuthorPane('editor')
      openCenterTab(ACTION_LIST_TAB_ID)
      recordAssistantChange(`Inserted ${component.name}`, result.summary)
      setSuccess(`Inserted ${component.name}. Its data prerequisites and lifecycle phases were preserved.`)
      setError('')
    } catch (error) { setError(getErrorMessage(error, 'Could not insert block.')) }
  }

  const toggleBlockSelection = (key: string) => {
    setBlockSelection(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const openSaveBlockModal = () => {
    if (selectedBlockScopes.length === 0) return
    setSaveBlockName('')
    setSaveBlockKind('request_group')
    setShowSaveBlockModal(true)
  }

  const beginWrapSelectionInTransaction = () => {
    if (!canWrapBlockSelection) return
    setTxnWrapDraft(uniqueTransactionName(script.actions))
  }

  const applyWrapSelectionInTransaction = () => {
    const name = (txnWrapDraft || '').trim()
    const indices = selectedTopLevelActionIndices
    if (!name || indices.length === 0) { setTxnWrapDraft(null); return }
    if (txnWrapMode === 'span') {
      const lo = indices[0]
      const hi = indices[indices.length - 1]
      setScript(prev => ({ ...prev, actions: wrapActionsInTransactionMarkers(prev.actions, lo, hi, name) }))
      // Two markers are inserted (start at lo, end after hi); keep breakpoints AND
      // the active selection / JSON focus pinned so they don't drift onto a shifted
      // action or an inserted marker (same family as duplicateAction).
      setDebugBreakpoints(prev => prev.map(bp => {
        if (bp.phase !== 'action') return bp
        const shifted = remapIndexAfterMarkerWrap(bp.action_index, lo, hi)
        return shifted === bp.action_index ? bp : { ...bp, action_index: shifted }
      }))
      setActiveAction(prev => (prev === null ? prev : remapIndexAfterMarkerWrap(prev, lo, hi)))
      setAuthorCodeFocus(prev => (prev && prev.phase === 'action'
        ? { ...prev, actionIndex: remapIndexAfterMarkerWrap(prev.actionIndex, lo, hi) }
        : prev))
    } else {
      setScript(prev => ({ ...prev, actions: tagActionsTransaction(prev.actions, indices, name) }))
    }
    setBlockSelection(new Set())
    setTxnWrapDraft(null)
  }

  // Capture the selected outline actions as a project-scoped reusable block.
  const saveSelectedAsBlock = async () => {
    const name = saveBlockName.trim()
    if (!name) {
      setError('Block name is required.')
      return
    }
    if (!projectId) {
      setError('A project is required to save a reusable block.')
      return
    }
    const actions = selectedBlockScopes.map(scope => JSON.parse(JSON.stringify(scope.action)) as Action)
    if (actions.length === 0) {
      setError('Select at least one action to save as a block.')
      return
    }
    setSavingBlock(true)
    try {
      await api.createAuthoringComponent({
        id: '',
        kind: saveBlockKind,
        name,
        protocols: [script.protocol],
        summary: `${actions.length} action${actions.length === 1 ? '' : 's'} saved from ${script.name || 'this script'}.`,
        actions,
        project_id: projectId,
      })
      await reloadAuthoringComponents()
      setBlockSelection(new Set())
      setShowSaveBlockModal(false)
      setReusableBlocksCollapsed(false)
      setSuccess(`Saved "${name}" as a reusable block.`)
      setError('')
    } catch (e) {
      setError(getErrorMessage(e, 'Failed to save reusable block'))
    } finally {
      setSavingBlock(false)
    }
  }

  const activeInspectorScope = useMemo(() => {
    const focus = authorCodeFocus || (activeAction !== null ? { phase: 'action' as const, actionIndex: activeAction } : null)
    if (!focus) return null
    const key = `${focus.phase}:${focus.actionIndex}${focus.stepPath?.length ? `#${focus.stepPath.join('/')}` : ''}`
    return resolveAuthorJsonScopeByKey(script, key)
  }, [activeAction, authorCodeFocus, script])
  updateInspectorFocusOwner(activeInspectorScope ? { scriptId, projectId, folderId, initialScript, path: activeInspectorScope.path, action: activeInspectorScope.action } : null)
  const selectedJsonUnavailable = Boolean(jsonDirty || jsonParseMessage)
  const showSelectedActionJson = Boolean(
    workspaceTab === 'author' &&
    selectedAuthorJsonScope &&
    !showFullScriptJson &&
    !selectedJsonUnavailable,
  )
  const hasScriptJsonDetailsTab = activeInspectorScope !== null
  const activeScriptJsonSidebarTab = scriptJsonSidebarTab === 'details' && !hasScriptJsonDetailsTab
    ? SCRIPT_JSON_DEFAULT_COMPANION_TAB
    : scriptJsonSidebarTab
  // Debug evidence describes the whole document it ran (configuration,
  // parameters and data sources included), not only its actions.
  const replayEvidenceMatchesCurrentDraft = replayEditableActionsKey !== null
    && currentEditableActionsKey !== null
    && replayEditableActionsKey === currentEditableActionsKey
    && replayDocumentFingerprint !== null
    && replayDocumentFingerprint === currentValidationBinding?.fingerprint
  // Validation evidence describes one bound document (fingerprint, baseline
  // revision and context); any change to them makes it stale.
  const validationEvidenceMatchesCurrentDraft = validationResultIsCurrent
  const debugEvidenceStatus = replayReport?.completed_at
    ? (!replayEvidenceMatchesCurrentDraft ? 'stale' : replayReport.stopped ? 'stopped' : replayReport.success ? 'passed' : 'failed')
    : activeReplaySessionId || browserReplaySession
      ? 'in progress'
      : 'missing'
  const validationPassed = validationSidebarResult?.tone === 'success' || validationSidebarResult?.tone === 'warning'
  const approvalReady = Boolean(
    validationEvidence
    && validationPassed
    && validationEvidenceMatchesCurrentDraft
    && replayReport?.completed_at
    && replayReport.success
    && replayEvidenceMatchesCurrentDraft
    && errorCount === 0
  )
  // The status row shows each state as a quiet chip: a mark and a word, with
  // the full sentence in its tooltip and for screen readers. In Debug the
  // session bar owns the run's status, so the debug chip steps aside there.
  const scriptStatusChips: Array<{ key: string; tone: 'ok' | 'warn' | 'bad' | 'none'; word: string; detail: string }> = [
    // A draft not yet in the library is never "Saved", however few edits it has.
    !scriptId && !savedScriptOpening
      ? { key: 'saved', tone: 'warn', word: 'Not saved', detail: 'This new script is not in the script library yet. Save adds it.' }
      : { key: 'saved', tone: scriptEditorDirty ? 'warn' : 'ok', word: scriptEditorDirty ? 'Unsaved' : 'Saved', detail: scriptEditorDirty ? 'This draft has unsaved changes.' : 'All changes are saved.' },
    {
      key: 'checked',
      tone: !validationEvidence ? 'none' : !validationEvidenceMatchesCurrentDraft ? 'warn' : validationPassed ? 'ok' : 'bad',
      word: !validationEvidence ? 'Unchecked' : !validationEvidenceMatchesCurrentDraft ? 'Recheck' : validationPassed ? 'Checked' : 'Checks failed',
      detail: `Validation evidence: ${!validationEvidence ? 'not yet' : !validationEvidenceMatchesCurrentDraft ? 'stale; recheck this draft' : validationPassed ? 'passed' : 'needs changes'}. Checks & readiness has the details.`,
    },
    ...(workspaceTab === 'debug' ? [] : [{
      key: 'debugged',
      tone: debugEvidenceStatus === 'passed' ? 'ok' as const : debugEvidenceStatus === 'failed' ? 'bad' as const : debugEvidenceStatus === 'missing' ? 'none' as const : 'warn' as const,
      word: debugEvidenceStatus === 'passed' ? 'Debugged' : debugEvidenceStatus === 'failed' ? 'Debug failed' : debugEvidenceStatus === 'stale' ? 'Debug stale' : debugEvidenceStatus === 'stopped' ? 'Debug stopped' : debugEvidenceStatus === 'in progress' ? 'Debugging' : 'Not debugged',
      detail: `Last debug run: ${debugEvidenceStatus}.`,
    }]),
    { key: 'ready', tone: approvalReady ? 'ok' : 'none', word: approvalReady ? 'Ready' : 'Not ready', detail: approvalReady ? 'Approval readiness: ready for review.' : 'Approval readiness: needs a passing check and debug run of this draft.' },
  ]
  const debugWorkspaceStatusLabel = replaying
    ? (isReplayPaused ? 'Paused' : 'Running')
    : replayReport?.completed_at
      ? (!replayEvidenceMatchesCurrentDraft ? 'Stale' : replayReport.stopped ? 'Stopped' : replayReport.success ? 'Passed' : 'Failed')
      : activeReplaySessionId || browserReplaySession
        ? 'Active'
        : ''
  const actionProblemRows = scriptPhaseActionRows.flatMap((row) =>
    (actionIssueBuckets[row.phase]?.[row.actionIndex] || []).map(issue => ({ issue, row })),
  )
  // The first action that uses each value a readiness problem names: one pass
  // over the actions, and none when no value is missing.
  const readinessVariableFirstUse = useMemo(() => {
    const pending = new Set(readinessIssues.flatMap(issue => issue.variable ? [issue.variable] : []))
    const firstUse = new Map<string, ScriptPhaseActionRow>()
    for (const row of pending.size ? scriptPhaseActionRows : []) {
      const names = templateVariableNames(JSON.stringify(row.action))
      for (const name of pending) {
        if (names.has(name)) { firstUse.set(name, row); pending.delete(name) }
      }
      if (!pending.size) break
    }
    return firstUse
  }, [readinessIssues, scriptPhaseActionRows])
  const editorIssueDetails = useMemo<IssueDetail[]>(() => {
    const structured = [
      ...scriptLevelIssues.map(issue => ({
        id: issue.id,
        severity: issue.severity,
        title: issue.message,
        source: 'Script',
        onSelect: issue.id === 'script:base_url' ? () => {
          openCenterTab('runtime-settings')
          selectRuntimeSettingsSection('script_config')
          requestAnimationFrame(() => editorElementRef.current?.querySelector<HTMLElement>('[data-author-field="base_url"]')?.focus())
        } : undefined,
      })),
      ...actionProblemRows.map(({ issue, row }) => ({
        id: issue.id,
        severity: issue.severity,
        title: issue.message,
        source: actionBreadcrumb(script, { phase: row.phase, actionIndex: row.actionIndex, stepPath: issue.stepPath }),
        ownsDestinationFocus: true,
        onSelect: () => {
          selectAuthorPhaseActionRef.current(row)
          if (issue.stepPath?.length) { setActiveAction(null); setAuthorCodeFocus({ phase: row.phase, actionIndex: row.actionIndex, stepPath: issue.stepPath }) }
          const field = issue.id.split(':').pop() || ''
          requestInspectorFocus({ phase: row.phase, actionIndex: row.actionIndex, stepPath: issue.stepPath }, panel => {
            const input = ['name', 'target', 'selector'].includes(field) ? panel.querySelector<HTMLElement>(`[data-author-field="${field}"]`) : null
            return input || panel.querySelector<HTMLElement>('[data-inspector-focus]')
          })
        },
      })),
    ] satisfies IssueDetail[]
    const operational: IssueDetail[] = [
      ...warnings.map((message, index) => ({
        id: `runtime-warning-${index}`,
        severity: 'warning' as const,
        title: message,
        source: 'Validation or replay',
      })),
      ...(error ? [{
        id: 'current-error',
        severity: 'error' as const,
        title: error,
        source: 'Current operation',
      }] : []),
      ...(jsonParseMessage ? [{
        id: 'json-parse-error',
        severity: 'error' as const,
        title: jsonParseMessage,
        source: 'Script JSON',
        onSelect: () => { setShowFullScriptJson(true); openCenterTab(SCRIPT_JSON_TAB_ID) },
      }] : []),
    ]
    const serverIssues: IssueDetail[] = validationResultIsCurrent ? (validationSidebarResult?.issues || []).map((title, index) => ({
      id: `server-${index}`, severity: 'error', title, source: 'Server validation', onSelect: () => {
        const match = /(?:script\.)?(actions|init|teardown)\[(\d+)\]/.exec(title)
        if (match) { const row = scriptPhaseActionRows.find(row => row.phase === (match[1] === 'actions' ? 'action' : match[1]) && row.actionIndex === Number(match[2])); if (row) selectAuthorPhaseActionRef.current(row) }
        else { setShowFullScriptJson(true); openCenterTab(SCRIPT_JSON_TAB_ID) }
      },
    })) : []
    const overlapsLocal = (issue: IssueDetail) => {
      const match = /(actions|init|teardown)\[(\d+)\].*?(?:missing|required)\s+(target|name)|(?:missing|required)\s+(target|name).*?(actions|init|teardown)\[(\d+)\]/i.exec(issue.title)
      if (!match) return false
      const phase = (match[1] || match[5]) === 'actions' ? 'action' : match[1] || match[5]
      const index = Number(match[2] || match[6]), field = match[3] || match[4]
      return authoringIssues.some(local => local.phase === phase && local.actionIndex === index && local.id.endsWith(`:${field}`))
    }
    // Like an authoring problem, opening one moves focus to the action it names.
    const openActionRow = (row: ScriptPhaseActionRow) => {
      selectAuthorPhaseActionRef.current(row)
      requestInspectorFocus({ phase: row.phase, actionIndex: row.actionIndex })
    }
    const readiness: IssueDetail[] = readinessIssues.map(issue => {
      const location = issue.location
      const row = location ? scriptPhaseActionRows.find(candidate => candidate.phase === location.phase && candidate.actionIndex === location.actionIndex) : undefined
      const variable = issue.variable
      // Where the value is used; with no use found, the parameters open to add one.
      const used = !row && variable ? readinessVariableFirstUse.get(variable) : undefined
      return {
        id: issue.id,
        severity: issue.severity,
        title: issue.title,
        description: issue.description,
        source: row ? actionBreadcrumb(script, { phase: row.phase, actionIndex: row.actionIndex }) : 'Replay readiness',
        ownsDestinationFocus: Boolean(row || used),
        onSelect: row ? () => openActionRow(row)
          : issue.secret ? openProjectSecrets
          : used ? () => openActionRow(used)
          : variable ? () => openCenterTab('parameter-data')
          : undefined,
      }
    })
    // Blocking problems lead, whichever check found them; the sort is stable,
    // so each source keeps its own order within a severity.
    const rank = (issue: IssueDetail) => issue.severity === 'blocker' || issue.severity === 'error' ? 0 : issue.severity === 'warning' ? 1 : 2
    return dedupeAuthorIssues([...structured, ...serverIssues.filter(issue => !overlapsLocal(issue)), ...readiness, ...operational.filter(issue => !overlapsLocal(issue))])
      .sort((a, b) => rank(a) - rank(b))
  }, [openProjectSecrets, readinessIssues, readinessVariableFirstUse, selectRuntimeSettingsSection, actionProblemRows, authoringIssues, error, jsonParseMessage, openCenterTab, requestInspectorFocus, script, setActiveAction, setAuthorCodeFocus, scriptLevelIssues, scriptPhaseActionRows, setShowFullScriptJson, validationResultIsCurrent, validationSidebarResult, warnings])
  const problemCount = editorIssueDetails.length
  const problemErrorCount = editorIssueDetails.filter(issue => issue.severity === 'error' || issue.severity === 'blocker').length
  const problemWarningCount = editorIssueDetails.filter(issue => issue.severity === 'warning').length
  // The rail lists the authoring checks its own way; every other problem the
  // counts include (validation, replay readiness, the current operation) is
  // listed after them, so the counts and the list agree.
  const authoringIssueIds = new Set([...scriptLevelIssues.map(issue => issue.id), ...actionProblemRows.map(({ issue }) => issue.id)])
  const railAdditionalIssues = editorIssueDetails.filter(issue => !authoringIssueIds.has(issue.id))
  const editorIssueDetailsRef = useRef(editorIssueDetails)
  editorIssueDetailsRef.current = editorIssueDetails
  // Open a script on the first problem Author Problems lists, else the first
  // action, rather than an empty "select an action" pane: from the problems
  // already computed for the hydrated script, so a large import pays nothing
  // extra. Selection only; keyboard focus stays, and Source keeps the whole
  // script, positioned on that action.
  useEffect(() => {
    if (!pendingOpeningFocusRef.current || pendingOpeningFocusRef.current !== script) return
    pendingOpeningFocusRef.current = null
    if (authorCodeFocus) return
    const opening = openingEditorFocus(
      actionProblemRows.filter(({ issue }) => issue.severity === 'error').map(({ row }) => row),
      readinessIssues,
      scriptPhaseActionRows,
    )
    if (!opening) return
    setActiveAction(opening.phase === 'action' ? opening.actionIndex : null)
    setAuthorCodeFocus(opening)
    setShowFullScriptJson(true)
  }, [actionProblemRows, authorCodeFocus, readinessIssues, script, scriptPhaseActionRows, setActiveAction, setAuthorCodeFocus, setShowFullScriptJson])
  const toggleSelectedBreakpoint = () => {
    const authorSelectedAction = authorCodeFocus && !authorCodeFocus.stepPath?.length
      ? {
        phase: authorCodeFocus.phase,
        actionIndex: authorCodeFocus.actionIndex,
        action: phaseActionsForScript(script, authorCodeFocus.phase)[authorCodeFocus.actionIndex],
      }
      : activeAction !== null
        ? { phase: 'action' as const, actionIndex: activeAction, action: script.actions[activeAction] }
        : null
    const selectedRow = workspaceTab === 'debug'
      ? (selectedReplayEvent ? selectedDebugRow : debugActionRows[0])
      : authorSelectedAction?.action
        ? authorSelectedAction
        : debugActionRows[0]
    if (selectedRow) void toggleDebugBreakpoint(selectedRow.phase, selectedRow.actionIndex)
  }

  const convergenceItems = [
    {
      key: 'repair',
      label: 'Repair',
      status: repairSuggestions.length > 0 ? `${repairSuggestions.length} suggested` : 'done',
      action: repairSuggestions[0] ? () => applyRepairSuggestion(repairSuggestions[0]) : undefined,
      disabled: repairSuggestions.length === 0,
    },
    {
      key: 'replay',
      label: 'Debug Evidence',
      status: debugEvidenceStatus,
      action: () => openReplayDebugger(false),
      actionLabel: 'Debug',
      disabled: saving || validating,
    },
    {
      key: 'save',
      label: 'Approval readiness',
      status: approvalReady ? 'ready' : 'needs evidence',
      action: handleSave,
      disabled: saveScriptDisabled || !approvalReady,
    },
  ]
  const topbarActionHandlersRef = useRef({
    activateWorkspaceTab,
    handlePauseResumeReplay,
    handleReplay,
    handleStepReplay,
    handleStopReplay,
    handleSave,
    setBreakOnFailure,
  })
  topbarActionHandlersRef.current = {
    activateWorkspaceTab,
    handlePauseResumeReplay,
    handleReplay,
    handleStepReplay,
    handleStopReplay,
    handleSave,
    setBreakOnFailure,
  }
  // The script is renamed in place in the top bar (the outline keeps only the
  // steps); the handler goes through a ref so typing does not rebuild it.
  const renameScriptRef = useRef((name: string) => updateField('name', name))
  renameScriptRef.current = (name: string) => updateField('name', name)
  const duplicateScriptNameNotice = `Another script in this folder is already named “${script.name.trim()}”. Both are kept; rename this one to tell them apart.`
  const scriptTopbarContribution = useMemo<TopbarContribution>(() => ({
    id: 'script-editor',
    priority: 80,
    title: currentDraftScript.name || (scriptId ? 'Edit script' : 'Create script'),
    titleNode: (
      <input
        className="topbar-title-input"
        aria-label="Script name"
        placeholder="Name this script"
        value={script.name}
        title={duplicateScriptName ? duplicateScriptNameNotice : script.name}
        onChange={event => renameScriptRef.current(event.target.value)}
      />
    ),
    meta: [
      {
        id: 'duplicate-name',
        label: 'Name in use',
        tone: 'warning',
        title: duplicateScriptNameNotice,
        hidden: !duplicateScriptName,
      },
      {
        id: 'problems',
        label: `${problemCount} issue${problemCount === 1 ? '' : 's'}`,
        // Red only when something blocks; warnings alone (placeholders) do not.
        tone: problemErrorCount > 0 ? 'danger' : problemCount > 0 ? 'warning' : 'success',
        hidden: problemCount === 0 && !validationEvidence,
        onClick: problemCount > 0
          ? () => setIssueDialog({ title: 'Script issues', issues: editorIssueDetailsRef.current })
          : undefined,
        ariaLabel: problemCount > 0 ? `View ${problemCount} script issue ${problemCount === 1 ? 'detail' : 'details'}` : undefined,
      },
    ],
    controls: (
      <PageViewToggle
        value={workspaceTab}
        ariaLabel="Script workspace"
        className="script-mode-switcher script-page-toggle topbar-page-section-toggle"
        onChange={value => topbarActionHandlersRef.current.activateWorkspaceTab(value)}
        options={[
          { value: 'author', label: 'Author' },
          { value: 'debug', label: 'Debug', title: debugWorkspaceStatusLabel || undefined },
        ]}
      />
    ),
    secondaryActions: [
      { id: 'save-script', label: saving ? 'Saving…' : 'Save', title: saveScriptTitle, ariaLabel: 'Save script', disabled: saveScriptDisabled, tone: 'primary', onClick: () => { void topbarActionHandlersRef.current.handleSave() } },
      {
        id: 'run-debug',
        label: replayReport ? 'Debug again' : 'Debug one iteration',
        hidden: replaying,
        compactLabel: 'Debug',
        icon: <DebugTopbarIcon kind="run" />,
        className: 'topbar-debug-action topbar-debug-action-run',
        title: replayRunButtonTitle,
        tone: 'success',
        disabled: replaying || saving || replayBlockedByMissingSecrets || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0,
        onClick: () => { void topbarActionHandlersRef.current.handleReplay() },
      },
    ],
  }), [currentDraftScript.actions, currentDraftScript.name, debugWorkspaceStatusLabel, duplicateScriptName, duplicateScriptNameNotice, script.name, problemCount, problemErrorCount, replayBlockedByMissingSecrets, replayReport, replayRunButtonTitle, replaying, saving, saveScriptDisabled, saveScriptTitle, scriptId, validationEvidence, workspaceTab])
  useTopbarContribution(scriptTopbarContribution)
  // The step controls belong to the debug session, beside its evidence: the
  // top bar keeps Save and the one action that starts a debug run, so it
  // never wraps while a run is paused.
  const debugControlActions = useMemo<TopbarAction[]>(() => [
    {
      id: 'step-debug',
      label: replayReport?.completed_at ? 'Restart in step mode' : 'Step',
      compactLabel: replayReport?.completed_at ? 'Restart step' : 'Step',
      icon: <DebugTopbarIcon kind="step" />,
      className: 'topbar-debug-action topbar-debug-action-step',
      hidden: workspaceTab !== 'debug',
      title: replayReport?.completed_at ? 'Restart in step mode at the first action; previous replay evidence is retained' : 'Step to next action',
      tone: 'secondary',
      disabled: stepReplayDisabled,
      onClick: () => { void topbarActionHandlersRef.current.handleStepReplay('into') },
    },
    {
      id: 'pause-debug',
      label: isReplayPaused ? 'Continue' : 'Pause',
      compactLabel: isReplayPaused ? 'Continue' : 'Pause',
      icon: <DebugTopbarIcon kind={isReplayPaused ? "run" : "pause"} />,
      className: 'topbar-debug-action topbar-debug-action-pause',
      hidden: workspaceTab !== 'debug' || !replaying,
      title: isReplayPaused ? 'Continue debugger' : 'Pause debugger',
      tone: 'secondary',
      disabled: !activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped',
      onClick: () => { void topbarActionHandlersRef.current.handlePauseResumeReplay() },
    },
    {
      id: 'step-over-debug',
      label: replayReport?.completed_at ? 'Restart in step mode (over)' : 'Step Over',
      compactLabel: replayReport?.completed_at ? 'Restart over' : 'Over',
      icon: <DebugTopbarIcon kind="stepOver" />,
      className: 'topbar-debug-action topbar-debug-action-step-over',
      hidden: workspaceTab !== 'debug' || !isReplayPaused,
      title: 'Step over the current block (run the rest of it, pause after)',
      tone: 'secondary',
      disabled: stepReplayDisabled,
      onClick: () => { void topbarActionHandlersRef.current.handleStepReplay('over') },
    },
    {
      id: 'finish-loop-debug',
      label: 'Finish Loop',
      compactLabel: 'Loop',
      icon: <DebugTopbarIcon kind="loop" />,
      className: 'topbar-debug-action topbar-debug-action-loop',
      hidden: workspaceTab !== 'debug' || !isReplayPaused || !(browserReplaySession?.current_action?.iteration_path?.length || browserReplaySession?.current_action?.iteration != null),
      title: 'Run to the end of the enclosing loop',
      tone: 'secondary',
      disabled: stepReplayDisabled,
      onClick: () => { void topbarActionHandlersRef.current.handleStepReplay('loop_end') },
    },
    {
      id: 'stop-debug',
      label: 'Stop',
      compactLabel: 'Stop',
      icon: <DebugTopbarIcon kind="stop" />,
      className: 'topbar-debug-action topbar-debug-action-stop',
      hidden: workspaceTab !== 'debug' || !replaying,
      title: 'Stop debugger',
      tone: 'danger',
      disabled: !activeReplaySessionId || !replaying,
      onClick: () => { void topbarActionHandlersRef.current.handleStopReplay() },
    },
    {
      id: 'break-on-failure',
      // The state shows in the text; the name stays, with aria-pressed saying on or off.
      label: `Break on failure: ${replayOptions.breakOnFailure ? 'on' : 'off'}`,
      ariaLabel: 'Break on failure',
      icon: <DebugTopbarIcon kind="break" />,
      className: 'topbar-debug-action topbar-debug-action-break',
      hidden: workspaceTab !== 'debug',
      title: replayOptions.breakOnFailure
        ? 'Break on failure is on — the debugger pauses on the first failing action'
        : 'Break on failure: pause automatically when an action fails',
      tone: 'ghost',
      pressed: replayOptions.breakOnFailure,
      onClick: () => { void topbarActionHandlersRef.current.setBreakOnFailure(!replayOptions.breakOnFailure) },
    },
  ], [activeReplaySessionId, browserReplaySession, isReplayPaused, replayOptions.breakOnFailure, replayReport, replaying, stepReplayDisabled, workspaceTab])
  // Run controls come and go with the run, so they lead the session bar and
  // Break on failure sits with the controls that never move: when a run ends,
  // nothing persistent slides under the pointer that just pressed Continue.
  // Step is the one run control always shown (Restart in step mode once a run
  // ends), so it leads and keeps its place.
  const debugSessionControls = (
    <div className="debug-session-controls" role="group" aria-label="Debugger controls">
      {debugControlActions.filter(action => action.id !== 'break-on-failure').map(action => <TopbarActionButton key={action.id} action={action} />)}
    </div>
  )
  const breakOnFailureAction = debugControlActions.find(action => action.id === 'break-on-failure')
  const debugSessionToggles = breakOnFailureAction && <TopbarActionButton action={breakOnFailureAction} />

  const renderBlockedCenterDocument = (title: string) => (
    <section className="center-document center-document-blocked" aria-label={title}>
      <div className="center-document-header">
        <div>
          <h3 className="panel-title">{title}</h3>
          <p>Structured editing is paused until the Source JSON parses again.</p>
        </div>
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => selectCenterTab(SCRIPT_JSON_TAB_ID)}>
          Open Source
        </button>
      </div>
      <div className="center-document-blocked-body">
        <strong>Source JSON is invalid</strong>
        <span>{jsonParseMessage || 'The current JSON draft could not be parsed.'}</span>
      </div>
    </section>
  )
  const renderStructuredCenterDocument = (title: string, content: ReactNode) => {
    return jsonParseMessage ? renderBlockedCenterDocument(title) : content
  }
  // The data aliases the script reads or changes, and how saved scenarios bind them.
  const dataUsage = useMemo(() => scriptDataUsage(script), [script])
  const scenarioDataUsage = useMemo(() => dataUsage.filter(usage => !usage.suppliedByScript || usesTableSteps(usage)), [dataUsage])
  const scenarioBindings = useScriptScenarioBindings(scriptId || undefined, projectId || undefined, scenarioDataUsage.length > 0)
  const unboundAliases = useMemo(() => !scriptId || scenarioBindings.status === 'ready'
    ? new Set(scenarioDataUsage.filter(usage => !scenarioBindings.byAlias.has(usage.alias)).map(usage => usage.alias))
    : undefined, [scenarioBindings.byAlias, scenarioBindings.status, scenarioDataUsage, scriptId])
  const inspectorStepPath = activeInspectorScope?.subAction ? authorCodeFocus?.stepPath : undefined
  const inspectorStepPathKey = (inspectorStepPath || []).join('/')
  // Only a live-table step's form reads the script's aliases, columns and earlier outputs.
  const inspectorLiveTableContext = useMemo(() => activeInspectorScope?.action.type === 'live_table'
    ? liveTableFormContext(script, { phase: activeInspectorScope.phase, position: 'before', anchor: { phase: activeInspectorScope.phase, actionIndex: activeInspectorScope.actionIndex, stepPath: inspectorStepPathKey ? inspectorStepPathKey.split('/') : undefined } })
    : undefined, [activeInspectorScope?.action.type, activeInspectorScope?.actionIndex, activeInspectorScope?.phase, inspectorStepPathKey, script])
  // What the selected step can reference (slice: references you pick, not type).
  const referenceScope = useMemo(() => {
    if (!activeInspectorScope) return null
    const dataAliases = Object.fromEntries(scenarioDataUsage.map(usage => [usage.alias, usage.columns]))
    return buildReferenceScope(script, { phase: activeInspectorScope.phase, actionIndex: activeInspectorScope.actionIndex, stepPath: inspectorStepPathKey ? inspectorStepPathKey.split('/') : undefined }, { dataAliases, unboundAliases })
  }, [activeInspectorScope?.actionIndex, activeInspectorScope?.phase, inspectorStepPathKey, scenarioDataUsage, script, unboundAliases]) // eslint-disable-line react-hooks/exhaustive-deps
  // The selected step's last replay: each check's and saved value's result, re-checked as they are edited.
  const currentLayoutKey = useMemo(() => replayLayoutKey === null ? null : stepLayoutKey(script), [replayLayoutKey, script])
  // The replay's steps are where they were and the structured script is the one being edited.
  const replayStepsInPlace = replayLayoutKey !== null && replayLayoutKey === currentLayoutKey && !jsonDirty && !jsonParseMessage
  // Or why a replayed script has no result for this step.
  const checksReplay = useMemo((): { replay: ChecksReplay | null; unavailable: string } => {
    if (!activeInspectorScope || !replayReport?.events?.length) return { replay: null, unavailable: '' }
    if (!replayStepsInPlace) return { replay: null, unavailable: jsonDirty || jsonParseMessage ? 'Apply or discard the JSON edits to see each result here.' : 'The steps changed since the last replay: replay again to see each result here.' }
    const at = lastStepEvent(replayReport.events, activeInspectorScope.phase, activeInspectorScope.actionIndex, inspectorStepPathKey ? inspectorStepPathKey.split('/') : [])
    if (at < 0) return { replay: null, unavailable: 'This step did not run in the last replay.' }
    return { replay: { event: replayReport.events[at], stale: !replayEvidenceMatchesCurrentDraft, resolve: replayResolver(mergeReplayVariables(replaySeedVariables, replayReport.events.slice(0, at))) }, unavailable: '' }
  }, [activeInspectorScope, inspectorStepPathKey, jsonDirty, jsonParseMessage, replayEvidenceMatchesCurrentDraft, replayReport, replaySeedVariables, replayStepsInPlace])
  // What text selected in a replayed response becomes, on the step that received it.
  const eventStep = (event: BrowserReplayEvent): StepPosition | null => {
    if (!['init', 'action', 'teardown'].includes(event.phase)) return null
    const stepPath = replayEventStepPath(event)
    return { phase: event.phase as ScriptActionPhase, actionIndex: event.action_index ?? 0, stepPath: stepPath.length ? stepPath : undefined }
  }
  const stepName = (position: StepPosition) => resolveAuthorJsonScopeByKey(script, authorFocusKey(position))?.action.name || 'the step'
  // Adds to the event's step unless it already has the same check or value; says which.
  const changeEventStep = (event: BrowserReplayEvent, has: (action: Action) => boolean, update: (action: Action) => Action, message: (name: string) => string): boolean => {
    const position = eventStep(event)
    const action = position ? resolveAuthorJsonScopeByKey(script, authorFocusKey(position))?.action : undefined
    if (!position || !action || has(action)) return false
    replaceScriptWithHistory(updateStep(script, position, update))
    setSuccess(`${message(stepName(position))} Changes are not saved yet.`)
    setError('')
    return true
  }
  const addCheck = (event: BrowserReplayEvent, assertion: Assertion, message: (name: string) => string) => changeEventStep(event,
    action => (action.assertions || []).some(existing => existing.type === assertion.type && String(existing.expected ?? '') === String(assertion.expected ?? '') && (existing.value ?? '') === (assertion.value ?? '') && !existing.source),
    action => ({ ...action, assertions: [...(action.assertions || []), assertion] }), message)
  const savedValue = (name: string, boundaries: { left: string; right: string }): ScriptExtractorDefinition => ({ name, source: 'body', type: 'boundary', expression: boundaryExpression(boundaries.left, boundaries.right) })
  const responseActions: ResponseActions = {
    offersStatus: checkFamily(script.protocol) === 'http',
    disabledReason: event => !eventStep(event) ? 'This response is not from a step of the script.' : replayStepsInPlace ? '' : jsonDirty || jsonParseMessage ? 'Apply or discard the JSON edits to add from this response.' : 'The steps changed since this replay. Replay again to add from its response.',
    laterUses: (event, text) => { const position = eventStep(event); return position ? laterUses(script, position, text).length : 0 },
    onStatus: (event, status) => addCheck(event, { type: 'status_code', expected: status }, name => `Added the check “Status is ${status}” to ${name}.`),
    onContains: (event, text) => addCheck(event, { type: checkFamily(script.protocol) === 'http' ? 'text_contains' : 'body_contains', value: text }, name => `Added a check that the response contains it to ${name}.`),
    onSave: (event, selection) => {
      if (!selection.boundaries) return false
      const extractor = savedValue(suggestValueName(event.snapshot?.response_body || '', selection.start, takenVariableNames(script)), selection.boundaries)
      return changeEventStep(event, action => (action.extractors || []).some(existing => existing.type === 'boundary' && (existing.source || 'body') === 'body' && existing.expression === extractor.expression), action => addCorrelationExtractorIfMissing(action, extractor), name => `Saved it as {{var.${extractor.name}}} on ${name}; it is in the step’s Saved values.`)
    },
    onCorrelate: (event, selection) => {
      const position = eventStep(event)
      if (!position || !selection.boundaries) return
      // A value the step already saves this way keeps its name.
      const expression = savedValue('', selection.boundaries).expression
      const saved = resolveAuthorJsonScopeByKey(script, authorFocusKey(position))?.action.extractors?.find(existing => existing.type === 'boundary' && (existing.source || 'body') === 'body' && existing.expression === expression)?.name
      const taken = new Set(takenVariableNames(script))
      if (saved) taken.delete(saved)
      setCorrelationReview({ source: position, sourceName: stepName(position), value: selection.text, boundaries: selection.boundaries, suggestedName: saved || suggestValueName(event.snapshot?.response_body || '', selection.start, taken), taken, uses: laterUses(script, position, selection.text) })
    },
  }
  const applyCorrelationReview = (name: string, uses: ValueUse[]) => {
    if (!correlationReview) return
    const result = applyValueCorrelation(script, correlationReview.source, savedValue(name, correlationReview.boundaries), uses)
    replaceScriptWithHistory(result.script)
    setCorrelationReview(null)
    setSuccess(`Saved {{var.${name}}} on ${correlationReview.sourceName} and used it in ${result.replaced} ${result.replaced === 1 ? 'place' : 'places'}.${result.skipped ? ` ${result.skipped} could not be replaced exactly and keep the recorded value.` : ''} Undo puts the recorded values back; changes are not saved yet.`)
    setError('')
  }
  // The reference field last focused, for "This step produces" › Insert.
  const referenceInsertTarget = useRef<((expression: string) => void) | null>(null)
  useEffect(() => { referenceInsertTarget.current = null }, [activeInspectorScope?.path])
  // "Make this a parameter" on any field: the field's value in the script JSON,
  // through the occurrence review that offers the same text elsewhere.
  const makeFieldParameter = (request: { field: FieldPath; value: string; from: number; to: number }) => {
    if (!activeInspectorScope) return
    if (jsonDirty || jsonParseMessage) {
      setError('Apply or reset your Source changes first, then make the parameter.')
      return
    }
    const { phase, actionIndex } = activeInspectorScope
    const collection = phase === 'action' ? 'actions' : phase
    const path = `$.script.${collection}[${actionIndex}]${jsonPathForActionStepPath(inspectorStepPath)}${request.field.map(segment => typeof segment === 'number' ? `[${segment}]` : formatJsonPathKey(segment)).join('')}`
    const occurrence = findJsonStringOccurrences(jsonText, request.value).find(item => item.path === path)
    if (!occurrence) {
      setError('That value could not be found in the script JSON; select it in Source to make it a parameter.')
      return
    }
    // A value written with JSON escapes is taken whole; the offsets of a part would not match.
    const plain = occurrence.rawText === request.value
    const start = plain ? occurrence.from + request.from : occurrence.from
    const end = plain ? occurrence.from + request.to : occurrence.to
    const selected = plain ? request.value.slice(request.from, request.to) : request.value
    openJsonOccurrenceReview({ source: 'json', phase, stepPath: inspectorStepPath, actionIndex, start, end, selectedText: jsonText.slice(start, end), existingName: null, requestedName: sanitizeVarName(selected), seedValue: selected })
  }
  // "Add an extractor" for a variable nothing sets: on the nearest earlier step with a response.
  const addExtractorForReference = (name: string) => {
    if (!activeInspectorScope) return
    const { phase, actionIndex } = activeInspectorScope
    const actions = phaseActionsForScript(script, phase)
    for (let index = actionIndex - 1; index >= 0; index -= 1) {
      const candidate = actions[index]
      if (!candidate || isSourceFunctionAction(candidate) || isControlAction(candidate) || NO_RESPONSE_ACTION_TYPES.has(candidate.type) || HTTP_RUNTIME_ACTION_TYPES.has(candidate.type) || candidate.type === 'native_javascript' || candidate.type.startsWith('browser_')) continue
      updatePhaseActionList(phase, list => list.map((action, at) => at === index ? { ...action, extractors: [...(action.extractors || []), { ...defaultExtractorForProtocol(script.protocol), name }] } : action))
      setInspectorFocusSection('checks')
      selectAuthorPhaseIndex(phase, index)
      setSuccess(`Added extractor ${name} to ${candidate.name || `step ${index + 1}`}; set what it captures.`)
      setError('')
      return
    }
    setError(`No earlier step in this phase has a response to extract ${name} from.`)
  }
  const referenceFieldContext: ReferenceFieldContextValue = {
    scope: referenceScope,
    onMakeParameter: makeFieldParameter,
    onAddExtractor: addExtractorForReference,
    onCreateParameter: name => upsertParameterFromActionEditor({ selectedText: '', requestedName: name, existingName: null }),
    registerInsertTarget: insert => { referenceInsertTarget.current = insert },
    releaseInsertTarget: insert => { if (referenceInsertTarget.current === insert) referenceInsertTarget.current = null },
  }
  const renderActionInspectorContent = (): ReactNode => (
    activeInspectorScope ? (
      <ReferenceFieldContext.Provider value={referenceFieldContext}>
        <p className="inspector-selection-path" title={`${actionBreadcrumb(script, authorCodeFocus || activeInspectorScope)} (${activeInspectorScope.path})`}>{actionBreadcrumb(script, authorCodeFocus || activeInspectorScope)}<small>{activeInspectorScope.path}</small></p>
        <ActionEditor inspector initialSection={inspectorFocusSection} key={`${activeInspectorScope.path}:${inspectorFocusSection}`}
          onOpenBody={() => { openCenterTab('request-body'); setNarrowAuthorPane('editor') }}
          action={activeInspectorScope.action}
          index={activeInspectorScope.actionIndex}
          parameters={script.parameters || []}
          onUpdate={(idx, field, value) => updatePhaseAction(activeInspectorScope.phase, idx, field, value)}
          onAddExtractor={(idx) => addPhaseExtractor(activeInspectorScope.phase, idx)}
          onUpdateExtractor={(actionIdx, extIdx, field, value) => updatePhaseExtractor(activeInspectorScope.phase, actionIdx, extIdx, field, value)}
          onRemoveExtractor={(actionIdx, extIdx) => removePhaseExtractor(activeInspectorScope.phase, actionIdx, extIdx)}
          onAddAssertion={(idx, assertion) => addPhaseAssertion(activeInspectorScope.phase, idx, assertion)}
          onUpdateAssertion={(actionIdx, assertionIdx, field, value) => updatePhaseAssertion(activeInspectorScope.phase, actionIdx, assertionIdx, field, value)}
          onRemoveAssertion={(actionIdx, assertionIdx) => removePhaseAssertion(activeInspectorScope.phase, actionIdx, assertionIdx)}
          onAddHeader={(idx) => addPhaseHeader(activeInspectorScope.phase, idx)}
          onUpdateHeader={(actionIdx, oldKey, newKey, value) => updatePhaseHeader(activeInspectorScope.phase, actionIdx, oldKey, newKey, value)}
          onRemoveHeader={(actionIdx, key) => removePhaseHeader(activeInspectorScope.phase, actionIdx, key)}
          onAddMetadata={(idx) => addPhaseMetadata(activeInspectorScope.phase, idx)}
          onUpdateMetadata={(actionIdx, oldKey, newKey, value) => updatePhaseMetadata(activeInspectorScope.phase, actionIdx, oldKey, newKey, value)}
          onRemoveMetadata={(actionIdx, key) => removePhaseMetadata(activeInspectorScope.phase, actionIdx, key)}
          onBeginParameterEdit={(draft) => openParameterizeChoice({ ...draft, phase: activeInspectorScope.phase, stepPath: authorCodeFocus?.stepPath })}
          methodColor={methodColor}
          protocol={script.protocol}
          scriptHTTPVersion={script.config.http?.version}
          scriptHTTPContext={script.config}
          isBrowserScript={isBrowserScript}
          isJmsScript={isJmsScript}
          isSoapScript={isSoapScript}
          issues={(actionIssueBuckets[activeInspectorScope.phase]?.[activeInspectorScope.actionIndex] || []).filter(issue => JSON.stringify(issue.stepPath || []) === JSON.stringify(authorCodeFocus?.stepPath || []))}
          catalogContext={inspectorLiveTableContext}
          onOpenSource={() => { setShowFullScriptJson(false); openCenterTab(SCRIPT_JSON_TAB_ID) }}
          checksReplay={checksReplay.replay}
          checksUnavailable={checksReplay.unavailable}
        />
      </ReferenceFieldContext.Provider>
    ) : null
  )
  // A new table step at the insertion point: what is set before it, and the names in use.
  const insertSharedData = useMemo((): InsertSharedData => ({
    usage: dataUsage,
    bindings: scenarioBindings.byAlias,
    context: alias => {
      const anchored = insertPosition !== 'end' && authorCodeFocus?.phase === insertPhase
      const point: InsertionPoint = anchored ? { phase: insertPhase, position: insertPosition, anchor: { phase: insertPhase, actionIndex: authorCodeFocus!.actionIndex, stepPath: authorCodeFocus!.stepPath } } : { phase: insertPhase, position: 'end' }
      return {
        alias,
        columns: dataUsage.find(usage => usage.alias === alias)?.columns || [],
        taken: takenVariableNames(script),
        names: scriptActionNames(script),
        earlier: liveTableFormContext(script, point).outputs,
        variables: earlierVariables(script, point),
      }
    },
  }), [authorCodeFocus, dataUsage, insertPhase, insertPosition, scenarioBindings.byAlias, script])
  // A step the data panel names: select it in the action list.
  const focusDataStep = (step: DataAliasStep) => {
    const { phase, actionIndex, stepPath } = step.position
    const row = scriptPhaseActionRows.find(item => item.phase === phase && item.actionIndex === actionIndex)
    if (!row) return
    if (!stepPath?.length) { selectAuthorPhaseAction(row); return }
    focusNestedStep(phase, actionIndex, stepPath, `${phase}:${actionIndex}#${stepPath.join('/')}`, step.name)
  }
  const renderActionLibrary = () => <details className="action-library"><summary>Add action from a block</summary><div className="action-library-body">
                  <div className="starter-blocks-panel">
                    <div className="starter-blocks-header">
                      <strong>Starter Blocks</strong>
                      <span>{starterTemplates.length}</span>
                    </div>
                    <div className="starter-blocks-list">
                      {starterTemplates.map((template) => (
                        <button key={template.key} type="button" className="starter-block-card" onClick={() => addActionTemplate(template.key)}>
                          <strong>{template.label}</strong>
                          <span>{template.description}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                {authoringComponents.length > 0 && (
                  <section className="outline-section reusable-blocks-section">
                    <div className="actions-sidebar-header outline-section-header">
                      <button
                        type="button"
                        className="outline-section-toggle"
                        aria-expanded={!reusableBlocksCollapsed}
                        onClick={() => setReusableBlocksCollapsed(collapsed => !collapsed)}
                      >
                        <span aria-hidden="true" className="outline-section-chevron">{reusableBlocksCollapsed ? '▸' : '▾'}</span>
                        <span>
                          <strong>Reusable Blocks</strong>
                          <small>
                            {authoringComponents.length} block{authoringComponents.length === 1 ? '' : 's'}
                            {isEditingExisting ? ' · inserts after selected action' : ''}
                          </small>
                        </span>
                      </button>
                    </div>
                    {!reusableBlocksCollapsed && (
                      <div className="starter-blocks-panel reusable-blocks-panel">
                        <div className="starter-blocks-list">
                          {AUTHORING_COMPONENT_GROUPS.map(group => {
                            const groupComponents = authoringComponents.filter(component => !isSavedBlock(component) && component.kind === group.kind)
                            if (groupComponents.length === 0) return null
                            return (
                              <div key={group.kind} className="reusable-blocks-group">
                                <span className="reusable-blocks-group-label">{group.label}</span>
                                {groupComponents.map(component => (
                                  <TooltipButton
                                    key={component.id}
                                    type="button"
                                    className="starter-block-card"
                                    onClick={() => insertAuthoringComponent(component)}
                                    title={component.usage_notes?.[0] || component.summary}
                                  >
                                    <strong>{component.name}</strong>
                                    <span>{component.summary}</span>
                                  </TooltipButton>
                                ))}
                              </div>
                            )
                          })}
                          {authoringComponents.some(isSavedBlock) && (
                            <div className="reusable-blocks-group">
                              <span className="reusable-blocks-group-label">Your Blocks {projectId && <button type="button" className="link-button" onClick={() => setSharedAssetsDialog({ section: 'saved-blocks' })}>Manage saved blocks</button>}</span>
                              {authoringComponents.filter(isSavedBlock).map(component => (
                                <div key={component.id} className="starter-block-card user-block-card">
                                  <TooltipButton
                                    type="button"
                                    className="user-block-insert"
                                    onClick={() => insertAuthoringComponent(component)}
                                    title={component.usage_notes?.[0] || component.summary}
                                  >
                                    <strong>{component.name}</strong>
                                    <span>{component.summary}</span>
                                  </TooltipButton>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>
                    )}
                  </section>
                )}
  </div></details>
  const renderBulkActionTools = () => (
                <details className="advanced-action-tools" open><summary>Bulk action tools</summary>
                <section className="outline-section">
                  <div className="actions-sidebar-header outline-section-header">
                    <button
                      type="button"
                      className="outline-section-toggle"
                      aria-expanded={!outlineActionListCollapsed}
                      onClick={() => setOutlineActionListCollapsed(collapsed => !collapsed)}
                    >
                      <span aria-hidden="true" className="outline-section-chevron">{outlineActionListCollapsed ? '▸' : '▾'}</span>
                      <span>
                        <strong>Action List</strong>
                        <small>{scriptPhaseActionRows.length} executable{scriptPhaseActionRows.length === 1 ? '' : 's'}</small>
                      </span>
                    </button>
                    <div className="actions-sidebar-controls">
                      {selectedBlockScopes.length > 0 && txnWrapDraft === null && (
                        <>
                          {canWrapBlockSelection && (
                            <TooltipButton className="btn btn-sm btn-secondary" onClick={beginWrapSelectionInTransaction} title="Wrap the selected actions in a named transaction (timed; SLAs attach to it)">
                              Wrap {selectedBlockScopes.length} in transaction
                            </TooltipButton>
                          )}
                          <TooltipButton className="btn btn-sm btn-secondary" onClick={openSaveBlockModal} title="Save the selected actions as a reusable block">
                            Save {selectedBlockScopes.length} as block
                          </TooltipButton>
                        </>
                      )}
                      {txnWrapDraft !== null && (() => {
                        const wrapSel = selectedTopLevelActionIndices
                        const wrapLo = wrapSel.length ? wrapSel[0] : 0
                        const wrapHi = wrapSel.length ? wrapSel[wrapSel.length - 1] : 0
                        const wrapBetween = (wrapHi - wrapLo + 1) - wrapSel.length
                        const wrapName = (txnWrapDraft || '').trim()
                        const wrapNameClash = wrapName.length > 0 && script.actions.some(a => actionTransactionName(a) === wrapName || actionTransactionMarker(a)?.name === wrapName)
                        return (
                        <span className="txn-wrap-inline">
                          <span className="txn-wrap-modes" role="group" aria-label="Transaction timing">
                            <TooltipButton
                              type="button"
                              className={`btn btn-sm ${txnWrapMode === 'span' ? 'btn-secondary' : 'btn-ghost'}`}
                              onClick={() => setTxnWrapMode('span')}
                              title="Timed span — lr_start/end_transaction markers measure wall-clock time across the selection (includes think time)"
                            >
                              Timed span
                            </TooltipButton>
                            <TooltipButton
                              type="button"
                              className={`btn btn-sm ${txnWrapMode === 'grouped' ? 'btn-secondary' : 'btn-ghost'}`}
                              onClick={() => setTxnWrapMode('grouped')}
                              title="Grouped — tag each selected action under one name (per-action response times, no span)"
                            >
                              Grouped
                            </TooltipButton>
                          </span>
                          <input
                            className="txn-wrap-input"
                            value={txnWrapDraft}
                            placeholder="Transaction name"
                            aria-label="Transaction name"
                            onChange={e => setTxnWrapDraft(e.target.value)}
                            onKeyDown={e => { if (e.key === 'Enter') applyWrapSelectionInTransaction(); else if (e.key === 'Escape') setTxnWrapDraft(null) }}
                          />
                          <button className="btn btn-sm btn-primary" onClick={applyWrapSelectionInTransaction} disabled={!txnWrapDraft.trim()}>Wrap</button>
                          <button className="btn btn-sm btn-ghost" onClick={() => setTxnWrapDraft(null)}>Cancel</button>
                          {txnWrapMode === 'span' && (
                            <span className="txn-wrap-hint" title="Timed span brackets every action between the first and last selected row">
                              times rows {wrapLo + 1}–{wrapHi + 1}{wrapBetween > 0 ? ` (+${wrapBetween} between)` : ''}
                            </span>
                          )}
                          {wrapNameClash && <span className="txn-wrap-hint warning" title="Another transaction already uses this name">name in use</span>}
                        </span>
                        )
                      })()}
                      <button className="btn btn-sm btn-primary" onClick={() => addAction('action')}>+ Add</button>
                    </div>
                  </div>
                  {!outlineActionListCollapsed && (
                    scriptPhaseActionRows.length === 0 ? (
                      <div className="action-list">
                        <div className="action-empty">No actions yet. Click "+ Add" to create one.</div>
                      </div>
                    ) : (
                    <>
                    <div className="action-list-filter">
                      <input
                        type="search"
                        className="action-list-filter-input"
                        placeholder="Filter actions…"
                        value={outlineActionFilter}
                        onChange={event => setOutlineActionFilter(event.target.value)}
                        aria-label="Filter the action list by name, type, or target"
                      />
                      {outlineActionFilter.trim() && (
                        <span className="action-list-filter-count" aria-live="polite">{filteredActionRows.length}/{scriptPhaseActionRows.length}</span>
                      )}
                    </div>
                    {filteredActionRows.length === 0 ? (
                      <div className="action-list">
                        <div className="action-empty">No actions match “{outlineActionFilter.trim()}”.</div>
                      </div>
                    ) : (
                    <div className="action-table">
                    <div className="action-table-head" aria-hidden="true">
                      <span className="action-cell action-cell-gutter" />
                      <span className="action-cell action-col-step">Step</span>
                      <span className="action-cell action-col-type">Type</span>
                      <span className="action-cell action-col-name">Name</span>
                      <span className="action-cell action-col-txn">Txn</span>
                      <span className="action-cell action-col-signals">Info</span>
                    </div>
                    <div className="action-list" ref={actionListScrollRef}>
                      <div className="action-list-virtual" style={virtualizeActions ? { height: actionListVirtualizer.getTotalSize(), position: 'relative' } : undefined}>
                      {actionRowsToRender.map(({ item, virtualItem }) => {
                        const row = item.row
                        const { action: a, actionIndex: i, phase, phaseLabel } = row
                        const virtualRowProps = {
                          'data-index': virtualItem ? virtualItem.index : undefined,
                          ref: virtualItem ? actionListVirtualizer.measureElement : undefined,
                          style: virtualItem ? ({ position: 'absolute' as const, top: 0, left: 0, width: '100%', transform: `translateY(${virtualItem.start}px)` }) : undefined,
                        }
                        // Nested rows (sub-requests of an http_sequence, control-flow leaves,
                        // and nested containers) render as lightweight indented rows. Top-level
                        // rows keep the full editing affordances below.
                        if (item.kind === 'leaf') {
                          const indent = { paddingLeft: `${item.depth * 14}px` } as React.CSSProperties
                          const leafBpKey = item.requestIndex != null
                            ? debugBreakpointKey(phase, i, item.requestIndex)
                            : debugStepPathKey(phase, i, item.stepPath)
                          const leafBpActive = debugBreakpointKeys.has(leafBpKey)
                          const leafLabel = item.requestIndex != null ? `Request ${item.requestIndex + 1}` : humanizeStepSegment(item.stepPath[item.stepPath.length - 1] || '')
                          const requestIndex = item.requestIndex
                          const stepPath = item.stepPath
                          const leafActive = authorCodeFocus?.key === item.key
                          const leafBpHandler = () => requestIndex != null
                            ? void toggleDebugBreakpoint(phase, i, requestIndex)
                            : void toggleStepPathBreakpoint(phase, i, stepPath)
                          return (
                            <div
                              key={item.key}
                              {...virtualRowProps}
                              className={`action-item action-item-nested ${leafActive ? 'active' : ''} ${leafBpActive ? 'has-breakpoint' : ''}`}
                              {...interactiveElementProps(() => selectAuthorNestedItem(item), `Select ${item.childAction.name || leafLabel}`)}
                            >
                              <div className="action-cell action-cell-gutter">
                                <input
                                  type="checkbox"
                                  className="action-block-select"
                                  checked={blockSelection.has(item.key)}
                                  aria-label={`Select ${item.childAction.name || leafLabel} for a reusable block`}
                                  title="Select for 'Save as block'"
                                  onClick={event => event.stopPropagation()}
                                  onChange={() => toggleBlockSelection(item.key)}
                                />
                                <TooltipButton
                                  type="button"
                                  className={`action-breakpoint-button ${leafBpActive ? 'active' : ''}`}
                                  aria-label={`${leafBpActive ? 'Remove' : 'Set'} breakpoint on ${leafLabel}`}
                                  title={`${leafBpActive ? 'Remove' : 'Set'} breakpoint on ${leafLabel}`}
                                  onClick={event => { event.stopPropagation(); leafBpHandler() }}
                                >
                                  <span aria-hidden="true" />
                                </TooltipButton>
                              </div>
                              <span className="action-cell action-phase-label" style={indent} title={leafLabel}>{leafLabel}</span>
                              <span className="action-cell action-method" style={{ color: methodColor(item.method) }} title={item.method}>{item.method}</span>
                              <span className="action-cell action-name-text" title={item.childAction.name || leafLabel}>{item.childAction.name || leafLabel}</span>
                              <div className="action-cell action-cell-txn" />
                              <div className="action-cell action-item-signals" />
                            </div>
                          )
                        }
                        if (item.kind === 'container' && item.depth > 0) {
                          const indent = { paddingLeft: `${item.depth * 14}px` } as React.CSSProperties
                          const containerActive = expandedAuthorContainers.has(item.key)
                          const containerSelected = authorCodeFocus?.key === item.key
						  const typeLabel = item.containerType === 'http_sequence' ? 'SEQ' : item.containerType === 'control_if' ? 'IF' : item.containerType === 'control_switch' ? 'SWITCH' : 'LOOP'
                          return (
                            <div key={item.key} {...virtualRowProps} className={`action-item action-item-nested lifecycle-action ${containerSelected ? 'active' : ''}`} {...interactiveElementProps(() => selectAuthorNestedItem(item), `Select ${item.name}`)}>
                              <div className="action-cell action-cell-gutter">
                                <input
                                  type="checkbox"
                                  className="action-block-select"
                                  checked={blockSelection.has(item.key)}
                                  aria-label={`Select ${item.name} for a reusable block`}
                                  title="Select for 'Save as block'"
                                  onClick={event => event.stopPropagation()}
                                  onChange={() => toggleBlockSelection(item.key)}
                                />
                                <span className="action-breakpoint-spacer" aria-hidden="true" />
                              </div>
                              <span className="action-cell action-phase-label" style={indent}>
                                <span aria-hidden="true" className="action-expand-glyph">{containerActive ? '▾' : '▸'}</span>
                                {typeLabel}
                              </span>
                              <span className="action-cell action-method" aria-hidden="true" />
                              <TooltipButton
                                type="button"
                                className="action-cell action-name-text action-name-toggle"
                                title={item.name}
                                aria-label={`${containerActive ? 'Collapse' : 'Expand'} ${item.name}`}
                                aria-expanded={containerActive}
                                onClick={event => { event.stopPropagation(); toggleAuthorContainer(item.key) }}
                              >
                                {item.name}
                              </TooltipButton>
                              <div className="action-cell action-cell-txn" />
                              <div className="action-cell action-item-signals">
                                <span className="action-item-meta"><span className="metric-badge">{item.leafCount} {item.containerType === 'http_sequence' ? 'req' : 'step'}</span></span>
                              </div>
                            </div>
                          )
                        }
                        const isContainerRow = item.kind === 'container'
                        const containerExpanded = isContainerRow && expandedAuthorContainers.has(item.key)
                        const actionBreakpointActive = debugBreakpointKeys.has(debugBreakpointKey(phase, i))
                        const breakpointPhaseLabel = phase === 'action' ? 'action' : phaseLabel
                        const actionBreakpointLabel = `${actionBreakpointActive ? 'Remove' : 'Set'} breakpoint on ${breakpointPhaseLabel} ${i + 1}`
                        const requestCount = actionRequestCount(a)
                        const extractorCount = actionExtractorCount(a)
                        const fallbackCount = actionSelectorFallbackCount(a)
                        const txnMarker = actionTransactionMarker(a)
                        const txnBandCell = transactionBands.get(`${phase}-${i}`)
                        // Only bracket multi-row transactions; a single-action transaction is
                        // already marked by its pill, so a one-row band is just noise.
                        const txnBand = txnBandCell && !(txnBandCell.first && txnBandCell.last) ? txnBandCell : undefined
                        const reviewGated = actionRequiresReview(a)
                        const executionBlockers = actionExecutionBlockers(a)
                        const executionBlocked = executionBlockers.length > 0
                        const controlAction = isControlAction(a)
                        const rowIssues = actionIssueBuckets[phase]?.[i] || []
                        const rowBlockKey = debugSequenceKey(phase, i)
                        const phaseActionCount = scriptActionsForPhase(script, phase).length
                        const rowActionKind = phase === 'action' ? 'action' : `${phaseLabel.toLowerCase()} action`
                        const rowActive = authorCodeFocus
                          ? authorCodeFocus.phase === phase && authorCodeFocus.actionIndex === i && !authorCodeFocus.stepPath?.length
                          : phase === 'action' && activeAction === i
                        return (
                          <div
                            key={`${phase}-${i}`}
                            data-index={virtualItem ? virtualItem.index : undefined}
                            ref={virtualItem ? actionListVirtualizer.measureElement : undefined}
                            className={`action-item ${rowActive ? 'active' : ''} ${actionBreakpointActive ? 'has-breakpoint' : ''} ${phase !== 'action' ? 'lifecycle-action' : ''} ${txnBand ? 'in-txn-band' : ''} ${txnBand?.first ? 'txn-band-first' : ''} ${txnBand?.last ? 'txn-band-last' : ''}`}
                            title={txnBand ? `Transaction: ${txnBand.name}` : undefined}
                            style={virtualItem ? { position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${virtualItem.start}px)` } : undefined}
                            {...interactiveElementProps(() => selectAuthorPhaseAction(row), `Select ${row.action.name || rowActionKind}`)}
                          >
                            <div className="action-cell action-cell-gutter">
                              {phase === 'action' && (
                                <input
                                  type="checkbox"
                                  className="action-block-select"
                                  checked={blockSelection.has(rowBlockKey)}
                                  aria-label={`Select ${a.name || 'action'} for a reusable block`}
                                  title="Select for 'Save as block'"
                                  onClick={event => event.stopPropagation()}
                                  onChange={() => toggleBlockSelection(rowBlockKey)}
                                />
                              )}
                              <TooltipButton
                                type="button"
                                className={`action-breakpoint-button ${actionBreakpointActive ? 'active' : ''}`}
                                aria-label={actionBreakpointLabel}
                                title={actionBreakpointLabel}
                                onClick={event => {
                                  event.stopPropagation()
                                  void toggleDebugBreakpoint(phase, i)
                                }}
                              >
                                <span aria-hidden="true" />
                              </TooltipButton>
                            </div>
                            <span className="action-cell action-phase-label" title={`${phaseLabel} ${i + 1}`}>
                              {isContainerRow && (
                                <span aria-hidden="true" className="action-expand-glyph">{containerExpanded ? '▾' : '▸'}</span>
                              )}
                              {phaseLabel} {i + 1}
                            </span>
                            <span className="action-cell action-method" style={{ color: methodColor(a.type) }} title={actionTypeSummary(a)}>{txnMarker ? 'TXN' : a.type === 'http_sequence' ? 'SEQ' : a.type}</span>
                            {isContainerRow ? (
                              <TooltipButton
                                type="button"
                                className="action-cell action-name-text action-name-toggle"
                                title={actionRowLabel(a)}
                                aria-label={`${containerExpanded ? 'Collapse' : 'Expand'} ${actionRowLabel(a)}`}
                                aria-expanded={containerExpanded}
                                onClick={event => { event.stopPropagation(); toggleAuthorContainer(item.key) }}
                              >
                                {actionRowLabel(a)}
                              </TooltipButton>
                            ) : (
                              <span className="action-cell action-name-text" title={actionRowLabel(a)}>{actionRowLabel(a)}</span>
                            )}
                            <div className="action-cell action-cell-txn">
                              {actionTransactionName(a) && (
                                <span className="action-txn-pill" title={`Transaction: ${actionTransactionName(a)}`}>
                                  ⏱ {actionTransactionName(a)}
                                </span>
                              )}
                              {txnMarker && (
                                <span
                                  className={`action-txn-marker ${txnMarker.role}`}
                                  title={`Transaction ${txnMarker.role === 'start' ? 'start' : 'end'}: ${txnMarker.name}`}
                                >
                                  {txnMarker.role === 'start' ? '⟦ TXN' : 'TXN ⟧'} {txnMarker.name}
                                </span>
                              )}
                            </div>
                            <div className="action-cell action-item-signals">
                              {(executionBlocked || reviewGated || isReviewGatedJavaScriptAction(a)) && (
                                <span
                                  className={`action-signal-pill ${executionBlocked ? 'error' : 'warning'}`}
                                  title={executionBlocked
                                    ? `Execution blocked: ${executionBlockers.join(', ')}`
                                    : isReviewGatedJavaScriptAction(a)
                                      ? 'JavaScript review gate — must be reviewed before this script can replay'
                                      : 'Review gate — must be reviewed before this script can replay'}
                                >
                                  {executionBlocked ? 'Blocked' : 'Review'}
                                </span>
                              )}
                              {rowIssues.length > 0 && (
                                <span
                                  className={`action-signal-pill ${rowIssues.some((issue) => issue.severity === 'error') ? 'error' : 'warning'}`}
                                  title={`${rowIssues.length} ${rowIssues.some((issue) => issue.severity === 'error') ? 'problem' : 'warning'}${rowIssues.length === 1 ? '' : 's'} on this action`}
                                >
                                  {rowIssues.length}
                                </span>
                              )}
                              {(controlAction || requestCount > 0 || extractorCount > 0 || fallbackCount > 0) && (
                                <span className="action-item-meta">
                                  {controlAction && <span className="metric-badge" title="Control block (if / loop / group)">ctrl</span>}
                                  {requestCount > 0 && <span className="metric-badge" title={`${requestCount} request${requestCount === 1 ? '' : 's'}`}>{requestCount} req</span>}
                                  {extractorCount > 0 && <span className="metric-badge" title={`${extractorCount} extractor${extractorCount === 1 ? '' : 's'}`}>{extractorCount} ext</span>}
                                  {fallbackCount > 0 && <span className="metric-badge" title={`${fallbackCount} selector fallback${fallbackCount === 1 ? '' : 's'}`}>{fallbackCount} fb</span>}
                                </span>
                              )}
                            </div>
                            <div className="action-item-actions">
                              <TooltipButton className="btn-icon" onClick={e => { e.stopPropagation(); movePhaseAction(phase, i, -1) }} disabled={i === 0} aria-label={`Move ${rowActionKind} up`} title="Move up">↑</TooltipButton>
                              <TooltipButton className="btn-icon" onClick={e => { e.stopPropagation(); movePhaseAction(phase, i, 1) }} disabled={i === phaseActionCount - 1} aria-label={`Move ${rowActionKind} down`} title="Move down">↓</TooltipButton>
                              <TooltipButton className="btn-icon" onClick={e => { e.stopPropagation(); duplicatePhaseAction(phase, i) }} aria-label={`Duplicate ${rowActionKind}`} title="Duplicate">⧉</TooltipButton>
                              <TooltipButton className="btn-icon danger" onClick={e => { e.stopPropagation(); removePhaseAction(phase, i) }} aria-label={`Delete ${rowActionKind}`} title="Delete">✕</TooltipButton>
                            </div>
                          </div>
                        )
                      })}
                      </div>
                    </div>
                    </div>
                    )}
                    </>
                    )
                  )}
                </section>
                </details>
  )
  const renderActionListCenterDocument = () => renderStructuredCenterDocument('Action editor', (
    <section className="center-document author-main-action" aria-label="Action editor">
      <div className="author-edit-toolbar">
        {/* Selecting an action in the outline leaves the overview. */}
        <button type="button" className="btn btn-sm btn-secondary" aria-pressed={authorOverview} onClick={() => setAuthorOverview(open => !open)}>Overview & bulk</button>
        <button type="button" className="btn btn-sm btn-primary" onClick={() => addAction(authorCodeFocus?.phase || 'action')}>+ Add Action</button>
      </div>
      {<div hidden={!authorOverview}>{renderBulkActionTools()}</div>}
      <div hidden={authorOverview}>{activeInspectorScope ? renderActionInspectorContent() : <div className="action-list-center-empty"><h3>Select an action to start editing</h3><p>Use the outline to navigate init, actions, teardown, and nested blocks, or add an action.</p></div>}</div>
    </section>
  ))

  const editReplayAction = (event: BrowserReplayEvent) => {
    const key = replayEventStepKey(event)
    const scope = key ? resolveAuthorJsonScopeByKey(script, key) : null
    if (!key || !scope) return
    const actionIndex = event.action_index ?? 0
    const phase = event.phase as ScriptActionPhase
    const path = replayEventStepPath(event)
    setInspectorFocusSection(scope.action.assertions?.length ? 'checks' : 'request')
    setNarrowAuthorPane('editor')
    setSelectedOutlineParameterName(null)
    activateWorkspaceTab('author')
    openCenterTab(ACTION_LIST_TAB_ID)
    setActiveAction(phase === 'action' && path.length === 0 ? actionIndex : null)
    setAuthorCodeFocus({ phase, actionIndex, stepPath: path, key, label: event.action_name })
    setAuthorRightSidebarCollapsed(false)
    setShowFullScriptJson(false)
    requestInspectorFocus({ phase, actionIndex, stepPath: path }, panel => {
      // The first check's answer: what it expects, else what it looks for.
      const assertions = panel.querySelector('[data-action-section="assertions"]')
      const check = assertions?.querySelector('[data-check-index]') || assertions
      return check?.querySelector<HTMLElement>('[data-check-field="expected"] input, [data-check-field="max"] input')
        || check?.querySelector<HTMLElement>('[data-check-field="value"] input')
        || check?.querySelector<HTMLElement>('input, select')
        || panel.querySelector<HTMLElement>('.action-editor-panel input')
    }, script, true)
  }
  const replayEventRequestScope = (event: BrowserReplayEvent) => {
    const key = replayEventStepKey(event)
    const scope = key ? resolveAuthorJsonScopeByKey(script, key) : null
    return scope ? { scope, phase: event.phase as ScriptActionPhase, actionIndex: event.action_index ?? 0, stepPath: replayEventStepPath(event) } : null
  }
  const http3UpgradeScopes = (event: BrowserReplayEvent): HTTP3UpgradeScope[] => {
    const resolved = replayEventRequestScope(event)
    if (!resolved) return []
    // A request with its own policy ignores the script default.
    return resolved.scope.action.http_version ? ['request'] : ['request', 'script']
  }
  const switchReplayEventToHTTP3Upgrade = (event: BrowserReplayEvent, scope: HTTP3UpgradeScope) => {
    const resolved = replayEventRequestScope(event)
    if (!resolved) return
    let nextScript: ScriptData
    if (scope === 'script') {
      nextScript = { ...script, config: { ...script.config, http: { ...script.config.http, version: 'auto+h3' } } }
    } else {
      const collection = resolved.phase === 'action' ? 'actions' : resolved.phase
      nextScript = { ...script, [collection]: phaseActionsForScript(script, resolved.phase).map((action, index) => index === resolved.actionIndex
        ? updateNestedAction(action, resolved.stepPath, leaf => setHTTPActionVersion(leaf, 'auto+h3')) : action) }
    }
    replaceScriptWithHistory(nextScript)
    setRevalidateAfterEdit(count => count + 1)
  }
  const renderCenterTabPanel = (tabId: CenterWorkbenchTabId) => {
    if (tabId === BROWSER_REPLAY_TAB_ID && isBrowserScript) {
      return (
        <ScriptBrowserEvidence
          key={activeReplaySessionId || 'idle'}
          event={selectedReplayEvent}
          variables={replayVariableInspector || {}}
          running={replaying}
          onEdit={!replaying && !jsonParseMessage && selectedReplayEvent && replayEditableActionsKey === currentEditableActionsKey ? () => editReplayAction(selectedReplayEvent) : undefined}
          onApplyLocator={!replaying && !jsonParseMessage && replayEditableActionsKey === currentEditableActionsKey ? applyReplaySuggestedLocator : undefined}
        />
      )
    }
    if (tabId === 'request-body') return renderStructuredCenterDocument('Request body', activeInspectorScope ? <ReferenceFieldContext.Provider value={referenceFieldContext}><ActionBodyWorkspace key={activeInspectorScope.path} action={activeInspectorScope.action} index={activeInspectorScope.actionIndex} path={activeInspectorScope.path} parameters={script.parameters || []} script={script} onInspectReference={reference => { if (reference.startsWith('param.') || reference.startsWith('data.')) openCenterTab('parameter-data'); else { setShowFullScriptJson(true); openCenterTab(SCRIPT_JSON_TAB_ID) } }} onUpdate={(field, value) => updatePhaseAction(activeInspectorScope.phase, activeInspectorScope.actionIndex, field, value)} onParameterize={draft => openParameterizeChoice({ ...draft, phase: activeInspectorScope.phase, stepPath: authorCodeFocus?.stepPath })} /></ReferenceFieldContext.Provider> : <p>Select an action to edit its request body.</p>)
    if (tabId === ACTION_LIST_TAB_ID) return workspaceTab === 'debug' ? null : renderActionListCenterDocument()
    if (tabId !== SCRIPT_JSON_TAB_ID) return null
    return (
      <ScriptJsonDocument fullScriptOnly={workspaceTab === 'debug'}
        scope={selectedAuthorJsonScope}
        selectedJsonText={selectedAuthorJsonText}
        showSelectedActionJson={showSelectedActionJson}
        showFullScriptJson={workspaceTab === 'debug' || showFullScriptJson}
        selectedJsonUnavailable={selectedJsonUnavailable}
        onShowFullScriptJson={setShowFullScriptJson}
        onCopySelected={() => void navigator.clipboard?.writeText(selectedAuthorJsonText)}
        onDownloadSelected={() => {
          if (selectedAuthorJsonScope) {
            void exportGeneratedTextFile(selectedAuthorJsonText, `${sanitizeDownloadName(selectedAuthorJsonScope.label, 'selected-action')}.json`)
          }
        }}
        sourcePane={{
          value: jsonText,
          status: jsonStatus,
          statusMessage: jsonStatusMessage,
          activePath: scriptCodeStatusPath,
          activeRange: scriptCodeActiveRange,
          activeParameterName: selectedOutlineParameterExists ? selectedOutlineParameterName : null,
          activeCandidateName: autoParameterSidebarActive ? activeAutoParameterCandidate?.varName || activeAutoParameterCandidate?.keyHint || null : null,
          actionCount: scriptPhaseActionRows.length,
          assistExtensions: scriptAssistExtensions,
          candidateMatchRanges: autoParameterCandidateMatchRanges,
          jumpToLineRequest: jsonLineJumpRequest,
          parameterMatchRanges: safeParameterMatchRanges,
          resolvedValues: workspaceTab === 'debug' && hasReplayVariableInspectorData ? replayVariableInspector : null,
          ariaLabel: 'Source',
          title: 'Source',
          onApply: handleApplyJsonDraft,
          onChange: handleCodePaneChange,
          onCopy: () => void navigator.clipboard?.writeText(jsonText),
          onDownload: () => { void handleDownloadScript() },
          onFormat: handleFormatJsonDraft,
          onReset: handleResetJsonDraft,
          onRequestParameterize: openJsonParameterSelectionMenu,
        }}
        selectionState={jsonParameterSelection}
        onOpenParameterEditor={openJsonParameterEditor}
        onOpenExistingParameter={openJsonExistingParameterChooser}
        onOpenOccurrenceReview={openJsonParameterOccurrenceReview}
        sourceActions={importMetadata && workspaceTab !== 'debug'
          ? <button type="button" className="btn btn-sm btn-ghost" onClick={() => setShowMetadataJsonDialog(true)}>Imported metadata JSON</button>
          : undefined}
      />
    )
  }
  const scriptNameField = (
    <div className="script-name-panel side-panel-script-name" aria-label="Script identity">
      <label className="script-name-field">
        <span>Script name</span>
        <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" aria-describedby={duplicateScriptName ? 'script-name-duplicate' : undefined} />
      </label>
      {duplicateScriptName && <small id="script-name-duplicate" className="script-name-duplicate">{duplicateScriptNameNotice}</small>}
    </div>
  )
  const renderCenterWorkbench = (className: string, ariaLabel: string, mode: WorkbenchMode) => {
    const workbenchTabs = mode === 'debug'
      ? visibleCenterTabs.filter(tab => !DEBUG_HIDDEN_CENTER_TABS.has(tab.id) && (!isBrowserScript || tab.id !== ACTION_LIST_TAB_ID)).map(tab => tab.id === ACTION_LIST_TAB_ID ? { ...tab, label: 'Action evidence', title: 'Action evidence' } : tab)
      : visibleCenterTabs.filter(tab => tab.id !== BROWSER_REPLAY_TAB_ID)
    const fallbackTabs = centerTabDescriptors.filter(tab => mode !== 'debug' || !DEBUG_HIDDEN_CENTER_TABS.has(tab.id))
    return (
      <CenterTabbedPane
        activeTabId={centerTabsState.activeTabId}
        ariaLabel={ariaLabel}
        className={className}
        skipLabel={mode === 'debug' ? 'Skip to debugger' : 'Skip to editor'}
        onCloseTab={closeCenterTab}
        onPanelElement={setCenterTabPanelElement}
        onSelectTab={selectCenterTab}
        renderTabPanel={renderCenterTabPanel}
        tabs={workbenchTabs.length > 0 ? workbenchTabs : fallbackTabs.slice(0, 1)}
      />
    )
  }
  // The rail tells what the form does not: what the action reads, what it
  // extracts, who reads that, and how its last debug run went.
  const renderActionContextCard = () => {
    if (!activeInspectorScope) return null
    const context = summarizeActionContext(script, activeInspectorScope.phase, activeInspectorScope.actionIndex, replayReport?.events, activeInspectorScope.subAction ? activeInspectorScope.action : undefined)
    if (!context) return null
    const references = (items: string[], empty: string) => items.length
      ? <ul className="action-context-tokens">{items.map(item => <li key={item}><code>{item}</code></li>)}</ul>
      : <p className="action-context-empty">{empty}</p>
    const debug = context.lastDebug
    const stepKey = debugSequenceKey(activeInspectorScope.phase, activeInspectorScope.actionIndex)
    const measure = activeInspectorScope.subAction ? undefined : outlineModel.measureByAction.get(stepKey)
    return (
      <>
      {measure && <OutlineMeasureCard
        measure={measure}
        label={outlineModel.stepsByKey.get(stepKey)?.label || activeInspectorScope.label}
        onRename={fullName => renameSubTransaction(measure, fullName)}
        onRemove={() => removeSubTransaction(measure)}
      />}
      <div className="utility-card action-context-card">
        <h3>{activeInspectorScope.label}</h3>
        <dl>
          <dt>Reads</dt>
          <dd>{references(context.uses, 'No parameters or variables')}</dd>
          <dt>This step produces</dt>
          <dd>{context.outputs.length ? <ul className="action-context-outputs">{context.outputs.map(output => (
            <li key={output.expression}>
              <code>{`{{${output.expression}}}`}</code>
              <small>{output.detail}</small>
              <span className="action-context-output-actions">
                <button type="button" className="btn btn-ghost btn-sm" title="Insert into the field you last edited" onClick={() => {
                  if (referenceInsertTarget.current) referenceInsertTarget.current(output.expression)
                  else setError('Click into a field of a step first, then Insert.')
                }}>Insert</button>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => void navigator.clipboard?.writeText(`{{${output.expression}}}`)}>Copy</button>
              </span>
              {output.usedBy.length ? <span className="action-context-links">Used by {output.usedBy.map((location, at) => {
                const row = scriptPhaseActionRows.find(item => item.phase === location.phase && item.actionIndex === location.actionIndex)
                return <span key={`${location.phase}:${location.actionIndex}`}>{at > 0 ? ', ' : ''}{row ? <button type="button" className="link-button" onClick={() => selectAuthorPhaseAction(row)}>{location.name || `${row.phaseLabel} ${location.actionIndex + 1}`}</button> : location.name}</span>
              })}</span> : <span className="action-context-empty">Not used yet</span>}
            </li>
          ))}</ul> : <p className="action-context-empty">Nothing</p>}</dd>
          <dt>{context.debugOfEnclosingAction ? 'Last debug of its block' : 'Last debug'}</dt>
          <dd>{debug
            ? <p className={`action-context-debug ${debug.success ? 'passed' : 'failed'}`}>{debug.success ? 'Passed' : 'Failed'}{debug.statusCode ? ` · HTTP ${debug.statusCode}` : ''} · {Math.round(debug.durationMs)} ms{!replayEvidenceMatchesCurrentDraft ? ' · before your latest edits' : ''}{!debug.success && debug.error ? <span>{debug.error}</span> : null}</p>
            : <p className="action-context-empty">Not debugged yet</p>}</dd>
        </dl>
      </div>
      </>
    )
  }
  const authorRightSidebarContext: AuthorRightSidebarContext = (() => {
    switch (centerTabsState.activeTabId) {
      case 'request-body':
      case ACTION_LIST_TAB_ID:
        if (activeInspectorScope) {
          return { kind: 'inspector', title: 'Action context', railLabel: 'Context' }
        }
        return { kind: 'signals', title: 'Insights', railLabel: 'Insights' }
      case 'parameter-data':
        return { kind: 'parameters', title: 'Parameters', railLabel: 'Parameters' }
      case SCRIPT_JSON_TAB_ID:
        if (activeScriptJsonSidebarTab === 'auto-params') {
          return { kind: 'auto-parameterization', title: selectedOutlineParameterExists && !parameterPaneShowAuto ? 'Parameter' : 'Parameters', railLabel: 'Parameters', wide: true }
        }
        if (activeScriptJsonSidebarTab === 'details' && activeInspectorScope) {
          return { kind: 'inspector', title: 'Action context', railLabel: 'Context' }
        }
        return { kind: 'signals', title: 'Insights', railLabel: 'Insights' }
      case 'imported-metadata':
        return { kind: 'imported-metadata', title: 'Source Mapping', railLabel: 'Map' }
      case 'object-descriptors':
        return { kind: 'object-descriptors', title: 'Object Descriptors', railLabel: 'Objects' }
      case 'http-conversion':
        return { kind: 'http-conversion', title: 'HTTP Conversion', railLabel: 'HTTP' }
      case BROWSER_REPLAY_TAB_ID:
        return { kind: 'browser-replay', title: 'Browser Replay', railLabel: 'Replay' }
      case 'runtime-settings':
        if (activeRuntimeSettingsSection === 'script_config') {
          return { kind: 'signals', title: 'Insights', railLabel: 'Insights' }
        }
        return { kind: 'runtime-settings', title: 'Runtime Settings', railLabel: 'Runtime' }
      default:
        return { kind: 'signals', title: 'Insights', railLabel: 'Insights' }
    }
  })()
  const renderReadinessValidationResult = () => {
    if (!validationSidebarResult && !validationEvidence) return null
    return (
      <div className="readiness-validation-result" aria-live="polite">
        {validationSidebarResult && (
          <div className={`readiness-validation-message ${validationSidebarResult.tone}`}>
            <strong>{validationSidebarResult.message}</strong>
            {validationSidebarResult.issues.length > 0 && (
              <ul>
                {validationSidebarResult.issues.map((issue, index) => (
                  <li key={`validation-issue-${index}`}>{issue}</li>
                ))}
              </ul>
            )}
            {validationSidebarResult.warnings.length > 0 && (
              <ul>
                {validationSidebarResult.warnings.map((warning, index) => (
                  <li key={`validation-warning-${index}`}>{warning}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {validationEvidence && <ValidationEvidencePanel evidence={validationEvidence} />}
      </div>
    )
  }
  const renderReadyToRunCard = ({ showChecklist = false }: { showChecklist?: boolean } = {}) => (
    <div className="utility-card debug-readiness-card">
      <div className="utility-card-header readiness-card-header">
        <strong>Ready for debug?</strong>
        <div className="readiness-header-actions">
          <span>{replayPreflightStatusLabel(replayPreflight)}</span>
          <TooltipButton
            type="button"
            className="btn btn-sm btn-secondary"
            onClick={() => { void handleValidateScript() }}
            disabled={validateScriptDisabled}
            title={validateScriptTitle}
          >
            {validating ? 'Checking…' : 'Recheck'}
          </TooltipButton>
        </div>
      </div>
      <ReplayPreflightCard
        preflight={replayPreflight}
        compact
        onConfigureSecrets={projectId ? openProjectSecrets : undefined}
        configureSecretsLabel={scriptId ? 'Configure secrets' : 'Save & configure secrets'}
        secretStatusState={replaySecretStatusState}
        onRetrySecretStatus={replaySecretStatusState === 'unavailable'
          ? () => setProjectSecretProbeRetryGeneration(generation => generation + 1)
          : undefined}
      />
      {workspaceTab === 'debug' ? renderReadinessValidationResult() : <><p>Validation evidence: {validationResultIsCurrent ? validationPassed ? 'passed' : 'needs changes' : validationEvidence ? 'stale; recheck this draft' : 'not checked'}. Repair individual issues in Author Problems.</p><details><summary>Detailed validation evidence</summary>{renderReadinessValidationResult()}</details></>}
      {showChecklist && (
        <details><summary>Evidence required for approval</summary><div className="utility-checklist">
          {convergenceItems.map(item => (
            <div key={item.key} className="utility-checklist-item">
              <div>
                <strong>{item.label}</strong>
                <span>{item.status}</span>
              </div>
              {item.action && (
                <button type="button" className="btn btn-sm btn-secondary" onClick={item.action} disabled={item.disabled}>
                  {item.actionLabel || item.label}
                </button>
              )}
            </div>
          ))}
        </div></details>
      )}
    </div>
  )
  const renderContextualRightSidebarContent = (): ReactNode => {
    switch (authorRightSidebarContext.kind) {
      case 'auto-parameterization': {
        const showParameterDetails = selectedOutlineParameterExists && selectedOutlineParameter && !parameterPaneShowAuto
        const parameterPaneToggle = selectedOutlineParameterExists ? (
          <TabList className="parameter-pane-toggle" role="tablist" aria-label="Parameter view" onKeyDown={handleTabKey}>
            <TooltipButton
              type="button"
              role="tab"
              aria-selected={!parameterPaneShowAuto}
              className={!parameterPaneShowAuto ? 'active' : ''}
              onClick={() => setParameterPaneShowAuto(false)}
              title={`Settings for ${selectedOutlineParameterName}`} tabIndex={(!parameterPaneShowAuto) ? 0 : -1}
            >
              Parameters
            </TooltipButton>
            <button
              type="button"
              role="tab"
              aria-selected={parameterPaneShowAuto}
              className={parameterPaneShowAuto ? 'active' : ''}
              onClick={() => setParameterPaneShowAuto(true)} tabIndex={(parameterPaneShowAuto) ? 0 : -1}
            >
              Auto Params
            </button>
          </TabList>
        ) : null
        if (showParameterDetails) {
          return (
            <div className="parameter-pane">
              <SelectedParameterDetails
                parameter={selectedOutlineParameter}
                referenceLines={selectedParameterReferenceLines}
                usages={jsonDirty ? undefined : parameterUsages(script, selectedOutlineParameter.name).map(usage => ({ path: usage.path, label: parameterUsageLabel(script, usage.path), line: lineRangeForTargetPath(scriptDslDocument.ranges, `script.${usage.path}`)?.startLine || 0 })).filter(usage => usage.line > 0)}
                dataInfo={selectedParameterDataInfo}
                onJumpToLine={line => { setNarrowAuthorPane('editor'); setShowFullScriptJson(true); openCenterTab(SCRIPT_JSON_TAB_ID); requestJsonLineJump(line) }}
                onEditInDataTab={() => {
                  if (selectedOutlineParameterIndex >= 0) selectParameterData(selectedOutlineParameterIndex)
                  openEditorSection('parameter-data')
                }}
              />
            </div>
          )
        }
        return (
          <div className="parameter-pane">
            {parameterPaneToggle}
          <AutoParameterizationSidebar
            activeCandidateId={activeAutoParameterCandidate?.id || null}
            activeOccurrenceIndex={normalizedAutoParameterOccurrenceIndex}
            activeOccurrenceLabel={formatParameterCandidateOccurrence(activeAutoParameterOccurrence)}
            candidateMatchCount={autoParameterCandidateMatchRanges.length}
            onApplyAutoParameterization={applyAutoParameterization}
            onNextOccurrence={() => moveAutoParameterOccurrence(1)}
            onPreviousOccurrence={() => moveAutoParameterOccurrence(-1)}
            onSelectCandidate={selectAutoParameterCandidate}
            onUpdateParameterCandidate={updateCandidate}
            parameterCandidates={parameterCandidates}
            seedRows={autoParamSeedRows}
            onSeedRowsChange={setAutoParamSeedRows}
          />
          </div>
        )
      }
      case 'parameters':
        return (
          <ParameterSidebar
            mergeSummary={parameterMergeSummary}
            onAddParameter={addParameter}
            onMergeParameterSources={mergeParameterSourceFiles}
            onSelectParameter={selectParameterData}
            parameters={script.parameters || []}
            selectedParameterIdx={selectedParameterIdx}
          />
        )
      case 'inspector':
        return renderActionContextCard()
      case 'imported-metadata':
        return (
          <ImportedMetadataUtilityPanel
            activeAction={activeAction}
            controller={importReviewController}
            migrationSuggestions={migrationSuggestions}
            onApplyMigrationSuggestion={applyMigrationSuggestion}
            onOpenMappedDataSource={openImportedDataSourceReview}
            scriptName={script.name}
          />
        )
      case 'object-descriptors':
        return (
          <ObjectDescriptorUtilityPanel
            controller={importReviewController}
            onApplyCandidate={applyObjectDescriptorCandidate}
            onSelectAction={selectAuthorAction}
          />
        )
      case 'http-conversion':
        return (
          <>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Conversion Gate</strong>
                <span>{httpConversionReview.status.replace(/_/g, ' ')}</span>
              </div>
              <div className="utility-metric-row">
                <span className="metric-badge">{httpConversionReview.browserActionCount} browser</span>
                <span className="metric-badge">{httpConversionReview.candidateRequestCount} requests</span>
                <span className="metric-badge">{httpConversionReview.replayEvidence ? 'replay' : 'no replay'}</span>
              </div>
              <div className="utility-stack">
                {httpConversionReview.findings.map((finding, index) => (
                  <div key={`http-sidebar-${index}`} className="utility-list-item">{finding}</div>
                ))}
              </div>
            </div>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Review Actions</strong>
                <span>{httpConversionPreview ? 'Preview ready' : 'No preview'}</span>
              </div>
              <div className="utility-stack">
                <button type="button" className="btn btn-sm btn-primary" onClick={handleGenerateBrowserHTTPPreview} disabled={httpConversionGenerating || !httpConversionReview.networkEvidence}>
                  {httpConversionGenerating ? 'Generating…' : 'Generate HTTP Draft Preview'}
                </button>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => activateWorkspaceTab('debug')}>Debug</button>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => openEditorSection('parameter-data')}>Review Parameters</button>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => openEditorSection('runtime')}>Review Runtime Settings</button>
              </div>
            </div>
          </>
        )
      case 'browser-replay':
        return (
          <>
            {renderReadyToRunCard()}
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Replay Evidence</strong>
                <span>{debugEvidenceStatus}</span>
              </div>
              <ul className="utility-list">
                <li className="utility-list-item">{replayReport ? `${replayReport.events.length} captured event${replayReport.events.length === 1 ? '' : 's'}` : 'No completed replay report attached.'}</li>
                <li className="utility-list-item">{browserReplaySession?.events?.length || 0} live session event{(browserReplaySession?.events?.length || 0) === 1 ? '' : 's'}</li>
              </ul>
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => activateWorkspaceTab('debug')}>Open Debugger</button>
            </div>
          </>
        )
      case 'runtime-settings':
        return (
          <ScriptRuntimeSettingsSidebar
            parityEntries={importSummary.runtimeSettingsParity}
            paritySummary={runtimeSettingParitySummary}
            readiness={renderReadyToRunCard()}
            script={script}
            // Only imported scripts have the read-only source panel; an authored
            // script's parity entries are config-derived with no source rows to show.
            onOpenImportedSource={script.metadata?.import?.preserved?.runtime_files
              ? () => {
                  openCenterTab('runtime-settings')
                  selectRuntimeSettingsSection('imported_source')
                }
              : undefined}
          />
        )
      default:
        return null
    }
  }

  return (<>
    {/* The return target (an import review this draft came from) leaves the
        editor through the session's guard, as Close does. */}
    {returnTarget && <div className="authoring-return-bar">
      <button className="btn btn-secondary" onClick={returnToSource}>{returnTarget.label}</button>
      {returnTarget.description && <span>{returnTarget.description}</span>}
    </div>}
<div className="script-editor" ref={editorElementRef} data-author-pane={narrowAuthorPane} onKeyDown={event => {
      if (workspaceTab !== 'author' || jsonDirty || !(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'z' || (event.target as Element).closest('.cm-editor')) return
      event.preventDefault()
      if (event.shiftKey) redoEditorChange(); else undoEditorChange()
    }}>
      {insertSeed && <AuthorInsertDialog protocol={script.protocol} seed={insertSeed} phase={insertPhase} position={insertPosition} anchorLabel={authorCodeFocus?.phase === insertPhase ? actionBreadcrumb(script, authorCodeFocus) : undefined} canInsertInside={Boolean(activeInspectorScope && ['control_loop', 'control_if'].includes(activeInspectorScope.action.type))} onPlan={(phase, position) => { setInsertPhase(phase); setInsertPosition(position) }} onInsert={insertPreparedAction} onInsertMany={insertPreparedActions} sharedData={insertSharedData} onClose={() => setInsertSeed(null)} onOpenThinkTime={() => { setInsertSeed(null); openCenterTab('runtime-settings'); selectRuntimeSettingsSection('think') }} library={renderActionLibrary()} />}
      {correlationReview && <CorrelationReviewDialog value={correlationReview.value} sourceName={correlationReview.sourceName} suggestedName={correlationReview.suggestedName} taken={correlationReview.taken} uses={correlationReview.uses} onApply={applyCorrelationReview} onClose={() => setCorrelationReview(null)} />}
      {showSavedDiff && <IssueDetailsDialog title="Changes since last save" issues={(() => { const diff = changedDocumentLines(baselineJsonRef.current, jsonDirty ? jsonText : formattedScriptJson); return diff.removed || diff.added ? [{ id: 'saved-diff', severity: 'info', title: `Changed region starting at line ${diff.line}`, description: `BEFORE\n${diff.removed || '(empty)'}\n\nAFTER\n${diff.added || '(empty)'}` }] : [] })()} onClose={() => setShowSavedDiff(false)} />}
      {showAuthorProblems && <IssueDetailsDialog title="Author Problems" issues={editorIssueDetails} onClose={() => setShowAuthorProblems(false)} />}
      {datasetChange && <DatasetChangeReview change={datasetChange} affected={(script.parameters || []).filter(parameter => parameter.data_source?.trim().toLowerCase() === datasetChange.before.name.trim().toLowerCase()).map(parameter => parameter.name)} error={datasetChangeError} onApply={applyDatasetChange} onCancel={() => setDatasetChange(null)} />}
      {/* One row of editor furniture, so the work below gets the height:
          the status reads as one line, History and layout reset live in More. */}
      <div className="script-evidence-states" aria-label="Script evidence states">
        {workspaceTab === 'author' && <>
          <TooltipButton type="button" className="btn-icon editor-outline-action-button" onClick={undoEditorChange} disabled={jsonDirty || editorHistory.past.length === 0} title="Undo document change" aria-label="Undo document change"><EditorRailActionIcon kind="undo" /></TooltipButton>
          <TooltipButton type="button" className="btn-icon editor-outline-action-button" onClick={redoEditorChange} disabled={jsonDirty || editorHistory.future.length === 0} title="Redo document change" aria-label="Redo document change"><EditorRailActionIcon kind="redo" /></TooltipButton>
        </>}
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => setShowSavedDiff(true)}>Review changes</button>
        <ul className="script-status-chips" aria-label="Script status">
          {scriptStatusChips.map(chip => <li key={chip.key} className={`script-status-chip ${chip.tone}`} title={chip.detail}>
            <span className="script-status-mark" aria-hidden="true" />{chip.word}<span className="sr-only">: {chip.detail}</span>
          </li>)}
        </ul>
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => setShowAuthorProblems(true)}>Author Problems ({problemCount})</button>
        <details className="script-health-summary"><summary>Checks &amp; readiness</summary><div className="author-readiness-popover">
          <p>Static checks inspect the draft. Runtime prerequisites are verified when debugging starts. Replay evidence belongs to its recorded revision.</p>
          {renderReadyToRunCard({ showChecklist: true })}
        </div></details>
        {onClose && <button type="button" className="btn btn-sm btn-ghost" aria-label="Close script editor" onClick={handleCloseEditor}>Close editor</button>}
        <RowActions className="script-editor-more" label="script editor">
          {scriptId && <button type="button" className="btn btn-sm btn-ghost" onClick={() => setShowHistory(true)}>History</button>}
          <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setAuthorRailWidth(220); setAuthorOutlineCollapsed(false); setAuthorRightSidebarCollapsed(false); replayDebuggerController.resetLayout() }}>Reset layout</button>
        </RowActions>
      </div>
      {showHistory && scriptId && <ObjectHistoryDialog title={`History · ${currentDraftScript.name || 'Script'}`} projectId={projectId} resourceType="script" resourceId={scriptId} activity={activity} onClose={() => setShowHistory(false)} />}
      {/* Tabs swap the center only: the outline and context panes stay for
          every tab, so the frame never reflows. */}
      {workspaceTab === 'author' && <div className="script-pane-switch" role="group" aria-label="Author panes">
        {(['outline', 'editor', 'inspector'] as const).map(pane => <button type="button" key={pane} aria-pressed={narrowAuthorPane === pane} onClick={() => { setNarrowAuthorPane(pane); if (pane === 'outline') setAuthorOutlineCollapsed(false); if (pane === 'inspector') setAuthorRightSidebarCollapsed(false) }}>{pane === 'outline' ? 'Outline' : pane === 'editor' ? 'Editor' : 'Context'}</button>)}
      </div>}
      <datalist id="object-ref-suggestions">
        {objectRefSuggestions.map(name => <option key={name} value={name} aria-label={name} />)}
      </datalist>
      {projectId && (missingProjectSecrets.length > 0 || replayBlockedByUnknownSecretStatus) && (
        <div className="script-secret-banner" role="status">
          <div>
            <strong>
              {replayBlockedByUnknownSecretStatus
                ? replaySecretStatusState === 'unavailable' ? 'Project secret status is unavailable' : 'Checking project secret configuration'
                : `${missingProjectSecrets.length} project secret${missingProjectSecrets.length === 1 ? ' still needs' : 's still need'} configuration`}
            </strong>
            <span>
              {replayBlockedByUnknownSecretStatus
                ? 'The script is safely saved with references, but replay remains blocked until credential status can be verified.'
                : 'The script is safely saved with references, but replay will remain blocked until values are available.'}
            </span>
          </div>
          <div className="settings-inline-actions">
            <button className="btn btn-secondary btn-sm" type="button" disabled={saving || secretBindingsOpen} onClick={openProjectSecrets}>{scriptId ? 'Configure secrets' : 'Save & configure secrets'}</button>
            {replaySecretStatusState === 'unavailable' && (
              <button className="btn btn-secondary btn-sm" type="button" onClick={() => setProjectSecretProbeRetryGeneration(generation => generation + 1)}>
                Retry credential check
              </button>
            )}
          </div>
        </div>
      )}
      {scriptId && authoringRevisionState === 'blocked' && <ScriptRevisionConflict
        reason={authoringRevisionReason} current={reviewedAuthoringBaseline}
        draft={currentDraftScript} loading={authoringReviewLoading}
        onLoad={() => { void loadCurrentAuthoringBaseline() }}
        onReload={() => adoptReviewedAuthoringBaseline(false)} onReapply={() => adoptReviewedAuthoringBaseline(true)}
      />}
      {sharedAssetsDialog && projectId && <SharedAssetsDialog projectId={projectId} section={sharedAssetsDialog.section} resourceId={sharedAssetsDialog.resourceId} onClose={() => {
        setSharedAssetsDialog(null)
        // Back in the editor: what it shows of them, as they are now.
        if (sharedAssetsDialog.section === 'saved-blocks') void reloadAuthoringComponents()
        else scenarioBindings.retry()
      }} />}
      {secretBindingsOpen && scriptId && projectId && (
        <ScriptSecretBindingsDialog
          scriptId={scriptId}
          script={currentDraftScript}
          projectId={projectId}
          authoringBaseline={authoringBaseline}
          applicationId={authoringBaseline?.projection.library_placement.application_id || undefined}
          rebindDisabled={authoringRevisionState !== 'ready'}
          scriptName={currentDraftScript.name || 'Script'}
          onClose={() => setSecretBindingsOpen(false)}
          onConfigured={async () => {
            await refreshScriptSecretStatus()
            setProjectSecretProbeRetryGeneration(generation => generation + 1)
          }}
          onRebound={(rebound, changedLocations, outcome) => {
            commitJsonDraft(rebound)
            if (outcome?.createdSecretName) {
              pendingCreatedSecretNamesRef.current = Array.from(new Set([
                ...pendingCreatedSecretNamesRef.current,
                outcome.createdSecretName,
              ]))
            }
            setSecretBindingsOpen(false)
            setSuccess(`Updated ${changedLocations.length} secret reference location${changedLocations.length === 1 ? '' : 's'} in the draft. Save the script to make the rebind authoritative.`)
          }}
        />
      )}
      <ScriptImportReviewDialogs
        controller={importReviewController}
        onOpenMappedDataSource={openImportedDataSourceReview}
      />
      {serverParameterization && (
        <DialogLayer><div className="editor-dialog-backdrop" {...dialogBackdropProps(closeServerParameterizationDialog)}>
          <div
            ref={serverParameterizationDialogRef}
            className="editor-dialog server-parameterization-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Server parameterization scan"
          >
            <div className="editor-dialog-header">
              <div>
                <h3 className="panel-title">Server Parameterization Scan</h3>
                <p className="editor-dialog-subtitle">
                  {serverParameterization.note || 'Review and apply the detected data parameters and secrets.'}
                </p>
              </div>
              <button type="button" className="btn btn-sm btn-ghost" onClick={closeServerParameterizationDialog}>Close</button>
            </div>
            <div className="editor-dialog-body">
              <ParameterizationReview
                parameterization={serverParameterization}
                script={script}
                disabled={saving || validating || replaying}
                onApplied={(applied, diffs) => {
                  replaceScriptWithHistory(applied)
                  recordAssistantChange('Applied server parameterization', `${diffs.length} change${diffs.length === 1 ? '' : 's'}`)
                  setServerParameterization(null)
                  setSuccess(`Applied server parameterization (${diffs.length} change${diffs.length === 1 ? '' : 's'}).`)
                  setError('')
                }}
              />
            </div>
          </div>
        </div></DialogLayer>
      )}
      {showSaveBlockModal && (
        <DialogLayer><div className="editor-dialog-backdrop" {...dialogBackdropProps(closeSaveBlockDialog)}>
          <div
            ref={saveBlockDialogRef}
            className="editor-dialog save-block-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Save selected actions as a reusable block"
          >
            <div className="editor-dialog-header">
              <div>
                <h3 className="panel-title">Save as Reusable Block</h3>
                <p className="editor-dialog-subtitle">
                  {selectedBlockScopes.length} action{selectedBlockScopes.length === 1 ? '' : 's'} will be saved to this project's block library.
                </p>
              </div>
              <button type="button" className="btn btn-sm btn-ghost" disabled={savingBlock} onClick={closeSaveBlockDialog}>Close</button>
            </div>
            <div className="editor-dialog-body save-block-dialog-body">
              <label className="save-block-field">
                <span>Block name</span>
                <input
                  type="text"
                  value={saveBlockName}
                  placeholder="e.g. Login transaction"
                  onChange={event => setSaveBlockName(event.target.value)}
                />
              </label>
              <label className="save-block-field">
                <span>Kind</span>
                <select value={saveBlockKind} onChange={event => setSaveBlockKind(event.target.value)}>
                  {AUTHORING_COMPONENT_GROUPS.map(group => (
                    <option key={group.kind} value={group.kind}>{group.label}</option>
                  ))}
                </select>
              </label>
              {!projectId && (
                <p className="save-block-warning">This script isn't associated with a project, so the block can't be saved.</p>
              )}
              <div className="save-block-actions">
                <button type="button" className="btn btn-sm btn-ghost" disabled={savingBlock} onClick={closeSaveBlockDialog}>Cancel</button>
                <button type="button" className="btn btn-sm btn-primary" disabled={savingBlock || !saveBlockName.trim() || !projectId} onClick={saveSelectedAsBlock}>
                  {savingBlock ? 'Saving…' : 'Save block'}
                </button>
              </div>
            </div>
          </div>
        </div></DialogLayer>
      )}
      {parameterizeChoice && (
        <ParameterizeDialog
          draft={parameterizeChoice}
          onClose={() => setParameterizeChoice(null)}
          onChooseFile={chooseParameterizeFile}
          onConfirmBuiltin={confirmBuiltinParameterize}
        />
      )}
      {existingParameterChoice && (
        <ExistingParameterDialog
          draft={existingParameterChoice}
          onClose={() => setExistingParameterChoice(null)}
          onSelect={parameterName => applyExistingParameterChoice(parameterName)}
          parameters={currentDraftScript.parameters || []}
        />
      )}
      {jsonOccurrenceReview && (
        <JsonOccurrenceReviewDialog
          onClose={() => setJsonOccurrenceReview(null)}
          onCreateNew={createParameterFromOccurrenceReview}
          onUseExisting={applyExistingParameterChoice}
          parameters={currentDraftScript.parameters || []}
          review={jsonOccurrenceReview}
        />
      )}
      {!hasTopbarContributionProvider && (
        <div className="editor-header script-editor-local-action-proxy" hidden>
          <div className="editor-title-row">
            <div className="editor-actions">
              <TooltipButton className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
                {saving ? 'Saving…' : 'Save Script'}
              </TooltipButton>
              {onClose && <button className="btn btn-ghost" onClick={handleCloseEditor}>Close</button>}
            </div>
          </div>
          <div className="script-ide-command-bar" aria-label="Script IDE command bar">
            <TabList className="script-ide-tabs" role="tablist" aria-label="Script workspace" onKeyDown={handleTabKey}>
              <button type="button" role="tab" aria-selected={workspaceTab === 'author'} className={workspaceTab === 'author' ? 'active' : ''} onClick={() => activateWorkspaceTab('author')} tabIndex={(workspaceTab === 'author') ? 0 : -1}>
                Author
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={workspaceTab === 'debug'}
                className={workspaceTab === 'debug' ? 'active' : ''}
                data-debug-status={debugWorkspaceStatusLabel || undefined}
                onClick={() => activateWorkspaceTab('debug')} tabIndex={(workspaceTab === 'debug') ? 0 : -1}
              >
                Debug
              </button>
            </TabList>
            <div className="script-ide-quick-actions" role="group" aria-label="Quick actions">
              <TooltipButton type="button" className="debugger-icon-btn" title={replayRunButtonTitle} onClick={() => void handleReplay()} disabled={replaying || saving || replayBlockedByMissingSecrets || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0}>
                <span className="debugger-icon" aria-hidden="true">&#9654;</span>
                <span>{replaying ? 'Running…' : replayReport ? 'Debug again' : 'Debug one iteration'}</span>
              </TooltipButton>
              {workspaceTab === 'debug' && (
                <>
                  <TooltipButton type="button" className="debugger-icon-btn" title={isReplayPaused ? 'Continue' : 'Pause'} onClick={handlePauseResumeReplay} disabled={!activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped'}>
                    <span className="debugger-icon" aria-hidden="true">{isReplayPaused ? '▶' : '⏸'}</span>
                    <span>{isReplayPaused ? 'Continue' : 'Pause'}</span>
                  </TooltipButton>
                  <TooltipButton type="button" className="debugger-icon-btn" title="Step to next action" onClick={() => void handleStepReplay('into')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#10551;</span>
                    <span>{replayReport?.completed_at ? 'Restart in step mode' : 'Step'}</span>
                  </TooltipButton>
                  <TooltipButton type="button" className="debugger-icon-btn" title="Step over the current block (run the rest of it, pause after)" onClick={() => void handleStepReplay('over')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#10550;</span>
                    <span>{replayReport?.completed_at ? 'Restart in step mode (over)' : 'Step Over'}</span>
                  </TooltipButton>
                  <TooltipButton type="button" className="debugger-icon-btn" title="Run to the end of the enclosing loop" onClick={() => void handleStepReplay('loop_end')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#8635;</span>
                    <span>Finish Loop</span>
                  </TooltipButton>
                  <TooltipButton type="button" className="debugger-icon-btn" title="Stop debugger" onClick={handleStopReplay} disabled={!activeReplaySessionId || !replaying}>
                    <span className="debugger-icon" aria-hidden="true">&#9209;</span>
                    <span>Stop</span>
                  </TooltipButton>
                  <TooltipButton type="button" className="debugger-icon-btn" title="Toggle breakpoint on selected action" onClick={toggleSelectedBreakpoint} disabled={debugActionRows.length === 0}>
                    <span className="debugger-icon" aria-hidden="true">●</span>
                    <span>Breakpoint</span>
                  </TooltipButton>
                  <TooltipButton
                    ref={debuggerSettingsBtnRef}
                    type="button"
                    className={`debugger-icon-btn ${centerTabsState.activeTabId === 'runtime-settings' ? 'active' : ''}`}
                    title="Session overrides"
                    aria-pressed={centerTabsState.activeTabId === 'runtime-settings'}
                    onClick={() => {
                      activateWorkspaceTab('debug')
                      setDebuggerTab('request')
                      setDebuggerSettingsOpen(false)
                      openCenterTab('runtime-settings')
                    }}
                  >
                    <span className="debugger-icon" aria-hidden="true">&#9881;</span>
                    <span>Settings</span>
                  </TooltipButton>
                  <TooltipButton
                    type="button"
                    className={`debugger-icon-btn ${!debugDetailCollapsed ? 'active' : ''}`}
                    title={debugDetailCollapsed ? 'Show detail panel (request/response + variables)' : 'Hide detail panel'}
                    aria-pressed={!debugDetailCollapsed}
                    onClick={() => setDebugDetailCollapsed(value => !value)}
                  >
                    <span className="debugger-icon" aria-hidden="true">▣</span>
                    <span>Panel</span>
                  </TooltipButton>
                  {replayReport && (
                    <TooltipButton type="button" className="debugger-icon-btn" title="Download Debug JSON" onClick={handleDownloadBrowserReplayExport}>
                      <span className="debugger-icon" aria-hidden="true">&#8675;</span>
                      <span>Export</span>
                    </TooltipButton>
                  )}
                </>
              )}
            </div>
          </div>
        </div>
      )}
      {(error || warnings.length > 0 || success) && (
        <div className="editor-feedback-strip">
          {error && <div className="editor-toast error">{error}</div>}
          {warnings.length > 0 && (
            <div className="editor-toast warning">
              {warnings.map((warning, index) => (
                <div key={`${warning}-${index}`}>{warning}</div>
              ))}
            </div>
          )}
          {success && <div className="editor-toast success">{success}</div>}
        </div>
      )}

      <div className={`editor-body workspace-${workspaceTab}`}>
          {workspaceTab === 'debug' && (
          <ScriptReplayDebuggerView
            // Without a top bar (embedded use and tests) the editor's own action proxy carries the step controls.
            sessionControls={hasTopbarContributionProvider ? debugSessionControls : undefined}
            sessionToggles={hasTopbarContributionProvider ? debugSessionToggles : undefined}
            actionProblemRows={actionProblemRows}
            activeReplaySessionId={activeReplaySessionId}
            addExtractorDefinition={addExtractorDefinition}
            responseActions={responseActions}
            api={api}
            applyReplaySuggestedLocator={applyReplaySuggestedLocator}
            breakpointSummary={breakpointSummary}
            browserReplaySession={browserReplaySession}
            clearDebugBreakpoints={clearDebugBreakpoints}
            controller={replayDebuggerController}
            copiedReplaySnapshotSection={copiedReplaySnapshotSection}
            debugActionRows={debugActionRows}
            debugOutline={outlineModel}
            debugBreakpointKeys={debugBreakpointKeys}
            debugDetailHost={!isBrowserScript && centerTabsState.activeTabId === ACTION_LIST_TAB_ID ? centerTabPanelElements[ACTION_LIST_TAB_ID] : null}
            debuggerWorkbench={renderCenterWorkbench('debugger-code-center', 'Debugger center workbench', 'debug')}
            error={error}
            expandedReplayBrowserArtifacts={expandedReplayBrowserArtifacts}
            expandedReplayEvent={expandedReplayEvent}
            exportGeneratedTextFile={exportGeneratedTextFile}
            handleCopyReplaySnapshotSection={handleCopyReplaySnapshotSection}
            handleValidateScript={handleValidateScript}
            isBrowserScript={isBrowserScript}
            jsonParseMessage={jsonParseMessage}
            onEditReplayAction={editReplayAction}
            http3UpgradeScopes={http3UpgradeScopes}
            onSwitchToHTTP3Upgrade={switchReplayEventToHTTP3Upgrade}
            onActivateDebug={() => activateWorkspaceTab('debug')}
            onSelectProblemAction={row => {
              activateWorkspaceTab('author')
              openCenterTab(ACTION_LIST_TAB_ID)
              selectAuthorPhaseAction(row)
            }}
            parameterReferenceCounts={parameterReferenceCounts}
            problemCount={problemCount}
            projectId={projectId}
            replayActivity={replayActivity}
            replayEventRefs={replayEventRefs}
            replayFailingEvent={replayFailingEvent}
            replayFailingEventIndex={replayFailingEventIndex}
            replaying={replaying}
            replayLatestActivity={replayLatestActivity}
            replayOptions={replayOptions}
            replayReport={replayReport}
            replayEditableActionsKey={replayEditableActionsKey}
            currentEditableActionsKey={currentEditableActionsKey}
            runHistory={debugRunHistory.filter(r => r.id !== currentRunRef.current?.id)}
            runRecord={currentRunRef.current}
            seedVariables={replaySeedVariables}
            followExecution={followExecution}
            onFollowExecution={changeFollowExecution}
            onExportSession={handleDownloadBrowserReplayExport}
            onUpdateBreakpoints={pushDebugBreakpoints}
            onRunTo={async target => {
              if (!replaying) { await handleReplay({ startPaused: false, runTo: target }); return }
              if (!activeReplaySessionId || browserReplaySession?.status !== 'paused') return
              try {
                const next = [{ ...target, once: true }, ...debugBreakpoints.filter(bp => !bp.once)]
                const updated = await performDebugMutation(() => api.updateScriptDebugBreakpoints(activeReplaySessionId, next))
                if (!updated) return
                if (updated.session) setDebugBreakpoints(updated.session.breakpoints || [])
                await performDebugMutation(() => api.resumeScriptDebugSession(activeReplaySessionId))
              } catch (cause) { setError(getErrorMessage(cause, 'Could not run to the selected action')) }
            }}
            replayVariableDiffs={replayVariableDiffs}
            replayVariableInspector={replayVariableInspector}
            runtimeVariablesUnavailable={runtimeVariablesUnavailable}
            currentRuntimeSnapshot={currentRuntimeSnapshot}
            currentRuntimeReason={runtimeAdmission.reason}
            canInspectRuntimeFrame={frame => frame.action_index >= 0 && replayEditableActionsKey === currentEditableActionsKey && Boolean(resolveAuthorJsonScopeByKey(script, `${frame.phase}:${frame.action_index}${frame.step_path?.length ? `#${frame.step_path.join('/')}` : ''}`))}
            onInspectRuntimeFrame={frame => {
              if (frame.action_index < 0 || replayEditableActionsKey !== currentEditableActionsKey) return
              const key = `${frame.phase}:${frame.action_index}${frame.step_path?.length ? `#${frame.step_path.join('/')}` : ''}`
              const scope = resolveAuthorJsonScopeByKey(script, key)
              if (!scope) return
              changeFollowExecution(false)
              setRuntimeFrameSourcePath(scope.path)
              setSelectedOutlineParameterName(null)
              setActiveAction(null)
              setAuthorCodeFocus({ phase: frame.phase, actionIndex: frame.action_index, stepPath: frame.step_path || [], key, label: scope.action.name || scope.path })
              setShowFullScriptJson(true)
              openCenterTab(SCRIPT_JSON_TAB_ID)
            }}
            runtimeInspectionMode={runtimeInspectionMode}
            onRuntimeInspectionMode={setRuntimeInspectionMode}
            replayVariableTimeline={replayVariableTimeline}
            saving={saving}
            script={script}
            scriptId={scriptId}
            scriptLevelIssues={scriptLevelIssues}
            scriptRecordingStatus={scriptRecordingStatus}
            scriptRecordingStatusState={scriptRecordingStatusState}
            retryScriptRecordingStatus={retryScriptRecordingStatus}
            selectedActionKey={authorCodeFocus ? authorCodeFocus.key || `${authorCodeFocus.phase}:${authorCodeFocus.actionIndex}` : activeAction !== null ? `action:${activeAction}` : undefined}
            selectedOutlineParameterName={selectedOutlineParameterName}
            onEditParameterDefinition={name => { activateWorkspaceTab('author'); const index = (script.parameters || []).findIndex(parameter => parameter.name === name); if (index >= 0) selectParameterData(index); openEditorSection('parameter-data') }}
            selectOutlineParameter={selectOutlineParameter}
            onSelectDebugItem={(item, hasEvidence) => {
              changeFollowExecution(false)
              if (item.kind === 'leaf' || (item.kind === 'container' && item.depth > 0)) selectAuthorNestedItem(item)
              else selectAuthorPhaseAction(item.row)
              setShowFullScriptJson(true)
              if (!hasEvidence) { setExpandedReplayEvent(null); openCenterTab(SCRIPT_JSON_TAB_ID) }
            }}
            setBreakOnFailure={setBreakOnFailure}
            setExpandedReplayEvent={update => { changeFollowExecution(false); setExpandedReplayEvent(update) }}
            setReplayOptions={setReplayOptions}
            setReplayScreenshotSelection={setReplayScreenshotSelection}
            setSequenceChildBreakpoints={setSequenceChildBreakpoints}
            toggleDebugBreakpoint={toggleDebugBreakpoint}
            toggleReplayBrowserArtifact={toggleReplayBrowserArtifact}
            toggleStepPathBreakpoint={toggleStepPathBreakpoint}
            validating={validating}
            warnings={warnings}
          />
          )}

          {objectDescriptorReview.rows.length > 0 && centerTabMountTarget('object-descriptors') && createPortal(
            renderStructuredCenterDocument('Object Descriptors', (
              <ObjectDescriptorReviewDocument
                controller={importReviewController}
                onApplyCandidate={applyObjectDescriptorCandidate}
                onSelectAction={selectAuthorAction}
              />
            )), centerTabMountTarget('object-descriptors')!)}

          {isBrowserScript && centerTabMountTarget('http-conversion') && createPortal(
            renderStructuredCenterDocument('Browser To HTTP Conversion Review', (
              <section className="center-document config-panel center-document-conversion" style={{ padding: 0 }} aria-label="Browser To HTTP Conversion Review">
                <div className="center-document-header">
                  <div>
                    <h3 className="panel-title">Browser To HTTP Conversion Review</h3>
                    <p>Gate generated HTTP load scripts behind replay, network, correlation, and approval evidence.</p>
                  </div>
                </div>
              <div className="panel-body" style={{ padding: '0 20px 20px' }}>
                <div className="import-metadata-grid">
                  <div className="import-metadata-card">
                    <span>Status</span>
                    <strong>{httpConversionReview.status.replace(/_/g, ' ')}</strong>
                    <small>{httpConversionReview.networkEvidence ? 'Network evidence captured' : 'Network evidence required'}</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Browser Actions</span>
                    <strong>{httpConversionReview.browserActionCount}</strong>
                    <small>Canary flow context</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Replay Evidence</span>
                    <strong>{httpConversionReview.replayEvidence ? 'Attached' : 'Missing'}</strong>
                    <small>Required before conversion</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Request Candidates</span>
                    <strong>{httpConversionReview.candidateRequestCount}</strong>
                    <small>Captured replay requests</small>
                  </div>
                </div>
                <div className="import-correlation-section">
                  <h4>Conversion Gate</h4>
                  <p>Keep Web 2.0 browser scripts as canaries until an HTTP draft is generated from captured traffic and passes validation/replay independently.</p>
                  <div className="import-correlation-list">
                    {httpConversionReview.findings.map((finding, index) => (
                      <div key={`http-conversion-${index}`} className="import-correlation-card">
                        <strong>{index === 0 ? 'Flow' : index === 1 ? 'Replay' : index === 2 ? 'Network' : 'Approval'}</strong>
                        <span>{finding}</span>
                      </div>
                    ))}
                  </div>
                </div>
                {httpConversionError && <div className="error-message">{httpConversionError}</div>}
                {httpConversionPreview && (
                  <div className="import-correlation-section">
                    <h4>Generated HTTP Draft Preview</h4>
					<p>
					  {httpConversionPreview.generated_script?.name || 'HTTP draft'}{' '}
					  {httpConversionBlockingFindings.length > 0
						? 'is blocked from promotion until the findings below are resolved.'
						: 'is ready for validation, replay, correlation repair, and approval review.'}
					</p>
                    <div className="import-metadata-grid">
                      <div className="import-metadata-card">
                        <span>Backend Status</span>
                        <strong>{httpConversionPreview.review.status.replace(/_/g, ' ')}</strong>
                        <small>{httpConversionPreview.review.generated_http_actions} HTTP actions generated</small>
                      </div>
                      <div className="import-metadata-card">
                        <span>Captured Requests</span>
                        <strong>{httpConversionPreview.review.captured_requests}</strong>
                        <small>{httpConversionPreview.review.skipped_static_assets} static assets skipped</small>
                      </div>
                      <div className="import-metadata-card">
                        <span>Correlation Candidates</span>
                        <strong>{httpConversionPreview.review.correlation_candidates?.length || 0}</strong>
                        <small>Review before approval</small>
                      </div>
                      <div className="import-metadata-card">
                        <span>Data Candidates</span>
                        <strong>{httpConversionPreview.review.data_source_candidates?.length || 0}</strong>
                        <small>Bind to parameters or data pools</small>
                      </div>
					  <div className="import-metadata-card">
						<span>Evidence</span>
						<strong>{httpConversionPreview.review.evidence.verification_status.replace(/_/g, ' ')}</strong>
						<small>{httpConversionPreview.review.evidence.durable ? 'Durable replay and correlation evidence' : 'Evidence is not durable'}</small>
					  </div>
					  <div className="import-metadata-card">
						<span>Promotion</span>
						<strong>{httpConversionPreview.review.promotion_status.replace(/_/g, ' ')}</strong>
						<small>{httpConversionPreview.governance.status.replace(/_/g, ' ')} · approval {httpConversionPreview.governance.approval_status.replace(/_/g, ' ')}</small>
					  </div>
                    </div>
					{httpConversionBlockingFindings.length > 0 && (
					  <div className="import-correlation-section" aria-label="HTTP draft promotion blockers">
						<h4>Promotion Blockers</h4>
						<div className="import-correlation-list">
						  {httpConversionBlockingFindings.map((finding, index) => (
							<div key={`${finding.severity}-${finding.summary}-${index}`} className="import-correlation-card">
							  <strong>{finding.severity.toUpperCase()} · {finding.summary}</strong>
							  {finding.evidence && <span>{finding.evidence}</span>}
							  {finding.action && <span>Next: {finding.action}</span>}
							</div>
						  ))}
						</div>
					  </div>
					)}
                    <div className="import-correlation-list">
                      {(httpConversionPreview.review.correlation_candidates || []).slice(0, 4).map((candidate, index) => (
                        <div key={`http-correlation-${candidate.name}-${candidate.source}-${index}`} className="import-correlation-card">
                          <strong>Correlation · {candidate.name}</strong>
                          <span>{candidate.evidence || candidate.source}</span>
                        </div>
                      ))}
                      {(httpConversionPreview.review.data_source_candidates || []).slice(0, 4).map((candidate, index) => (
                        <div key={`http-data-${candidate.name}-${candidate.value}-${index}`} className="import-correlation-card">
                          <strong>Data Source · {candidate.name}</strong>
                          <span>{candidate.evidence || candidate.source}</span>
                          {candidate.value && <code>{candidate.value}</code>}
                        </div>
                      ))}
                    </div>
                    <div className="import-correlation-card">
                      <strong>{httpConversionPreview.scenario_template.name}</strong>
                      <span>{httpConversionPreview.scenario_template.groups.map(group => `${group.name}: ${group.protocol} / ${group.vusers} VUsers`).join(' · ')}</span>
                    </div>
                  </div>
                )}
                <div className="replay-panel-actions" style={{ justifyContent: 'flex-end', flexWrap: 'wrap' }}>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={handleGenerateBrowserHTTPPreview}
                    disabled={httpConversionGenerating || !httpConversionReview.networkEvidence}
                  >
                    {httpConversionGenerating ? 'Generating…' : 'Generate HTTP Draft Preview'}
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => activateWorkspaceTab('debug')}>
                    Debug
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => openEditorSection('parameter-data')}>
                    Review Parameters
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => openEditorSection('runtime')}>
                    Review Runtime Settings
                  </button>
                </div>
              </div>
              </section>
            )), centerTabMountTarget('http-conversion')!)}

          {/* Runtime Settings Panels */}
          {centerTabMountTarget('runtime-settings') && createPortal(
            renderStructuredCenterDocument('Runtime Settings', (<>
              {importMetadata && <ImportedRuntimeParityReview controller={importReviewController} onOpenRuntimeSettings={openImportedRuntimeSettings} />}
              <ScriptRuntimeSettingsView
                hideSave
                debugContext={replayOptions}
                baselineConfig={(() => { const parsed = parseScriptJsonDraft(baselineJsonRef.current); return parsed.ok ? parsed.script.config : undefined })()}
                activeHTTPTransportTab={activeHTTPTransportTab}
                activeSection={activeRuntimeSettingsSection}
                canEditProtocol={canEditProtocol}
                draftScope={`script-editor:${editorResourceScopeVersionRef.current}`}
                onAddHTTPRuntimeAction={addHTTPRuntimeAction}
                onSave={handleSave}
                onSelectHTTPTransportTab={setActiveHTTPTransportTab}
                onSelectSection={selectRuntimeSettingsSection}
                onUpdateField={updateField}
                onUpdateProtocol={updateProtocol}
                saveDisabled={saveScriptDisabled}
                saveTitle={saveScriptTitle}
                saving={saving}
                script={script}
              />
            </>)), centerTabMountTarget('runtime-settings')!)}

          {/* Actions List + Detail */}
          {workspaceTab === 'author' && (
          <div className="actions-layout" role="tabpanel" aria-label="Authoring workspace">
            <div
              className={`actions-content-grid ${authorOutlineCollapsed ? 'author-outline-collapsed' : ''} ${authorRightSidebarCollapsed ? 'author-right-sidebar-collapsed' : ''} ${authorRightSidebarContext.wide ? 'author-right-sidebar-wide' : ''}`}
              style={!authorOutlineCollapsed && authorRailWidth != null ? ({ ['--author-rail-width']: `${authorRailWidth}px` } as React.CSSProperties) : undefined}
            >
              {!authorOutlineCollapsed && (
                <div
                  className={`author-rail-resize ${authorRailResizing ? 'resizing' : ''}`}
                  role="separator"
                  aria-orientation="vertical"
                  aria-label="Resize outline panel (double-click to fit content)"
                  title="Drag to resize · double-click to fit content"
                  onPointerDown={startAuthorRailDrag}
                  onDoubleClick={fitAuthorRailToContent}
                />
              )}
              <div className={`actions-sidebar ${authorOutlineCollapsed ? 'collapsed' : ''}`} ref={authorRailRef} id="script-outline-panel">
                {authorOutlineCollapsed ? (
                  <PanelToggle className="actions-sidebar-restore ux-panel-rail" expanded={false} panelName="outline side panel" collapsedLabel="Outline" controls="script-outline-panel" onToggle={() => setAuthorOutlineCollapsed(false)} />
                ) : (
                <>
                <div className="editor-outline-nav">
                  <div className="editor-outline-header editor-outline-header-actions-only">
                    <div className="editor-outline-actions">
                      {!hasTopbarContributionProvider && <TooltipButton type="button" className="btn-icon editor-outline-action-button primary" onClick={() => { void handleSave() }} disabled={saveScriptDisabled} title={saveScriptTitle} aria-label="Save script">
                        <EditorRailActionIcon kind="save" />
                      </TooltipButton>}

                      <PanelToggle expanded={true} panelName="outline side panel" collapsedLabel="Outline" controls="script-outline-panel" onToggle={() => setAuthorOutlineCollapsed(true)} />
                    </div>
                  </div>
                </div>
                {!hasTopbarContributionProvider && scriptNameField}
                <input className="script-outline-filter" type="search" aria-label="Find action" placeholder="Find action" value={outlineActionFilter} onChange={event => setOutlineActionFilter(event.target.value)} />
                <nav className="script-action-navigator script-outline-tree" aria-label="Script outline">
                  <ScriptOutlineTree
                    model={outlineModel}
                    resetKey={scriptId || 'draft'}
                    query={outlineActionFilter}
                    expandedContainers={expandedAuthorContainers}
                    onToggleContainer={toggleAuthorContainer}
                    selection={selectedOutlineParameterName ? null : authorCodeFocus}
                    onSelect={item => item.kind === 'leaf' || (item.kind === 'container' && item.depth > 0) ? selectAuthorNestedItem(item) : selectAuthorPhaseAction(item.row)}
                    hasBreakpoint={key => debugBreakpointKeys.has(key)}
                    hasIssue={row => (actionIssueBuckets[row.phase]?.[row.actionIndex] || []).length > 0}
                    showMarkers={outlineShowMarkers}
                    onShowMarkersChange={changeOutlineShowMarkers}
                    naturalWidthRef={outlineNaturalWidthRef}
                  />
                </nav>
                </>
                )}
              </div>

              {renderCenterWorkbench('author-code-center', 'Author center workbench', 'author')}

              {importMetadata && centerTabMountTarget('imported-metadata') && createPortal(
                renderStructuredCenterDocument('Source Mapping', (
                  <SourceMappingReviewDocument
                    controller={importReviewController}
                    baselineScript={importBaselineScript || initialScript}
                    onChangeReview={state => replaceScriptWithHistory(withMappingReviewState(script, state))}
                    draftDirty={scriptEditorDirty}
                    onEditTarget={path => {
                      const action = /^(actions|init|teardown)\[(\d+)\]/.exec(path)
                      if (action) { selectAuthorPhaseIndex(action[1] === 'actions' ? 'action' : action[1] as 'init' | 'teardown', Number(action[2])); setNarrowAuthorPane('inspector') }
                      else if (path.startsWith('config.')) openImportedRuntimeSettings()
                      else { setNarrowAuthorPane('editor'); setShowFullScriptJson(true); openCenterTab(SCRIPT_JSON_TAB_ID) }
                    }}
                    onSelectAction={selectAuthorAction}
                    script={script}
                  />
                )), centerTabMountTarget('imported-metadata')!)}

              <div id="script-details-panel" className={`editor-utility-sidebar context-${authorRightSidebarContext.kind} ${authorRightSidebarContext.wide ? 'wide' : ''} ${authorRightSidebarCollapsed ? 'collapsed' : ''}`}>
                {authorRightSidebarCollapsed ? (
                  <PanelToggle className="utility-sidebar-restore ux-panel-rail" side="end" expanded={false} panelName="right sidebar" collapsedLabel={authorRightSidebarContext.railLabel} controls="script-details-panel" onToggle={() => setAuthorRightSidebarCollapsed(false)} />
                ) : (
                <>
                <div className="side-pane-header">
                  {centerTabsState.activeTabId === SCRIPT_JSON_TAB_ID ? (
                    <ScriptJsonCompanionHeader
                      activeTab={activeScriptJsonSidebarTab}
                      hasDetailsTab={hasScriptJsonDetailsTab}
                      title={authorRightSidebarContext.title}
                      onChange={setScriptJsonSidebarTab}
                    />
                  ) : (
                    <span>{authorRightSidebarContext.title}</span>
                  )}
                  <PanelToggle side="end" expanded={true} panelName="right sidebar" collapsedLabel={authorRightSidebarContext.railLabel} controls="script-details-panel" onToggle={() => setAuthorRightSidebarCollapsed(true)} />
                </div>
                {authorRightSidebarContext.kind !== 'signals' ? renderContextualRightSidebarContent() : (
                <ScriptJsonInsightsView
                  actionProblems={actionProblemRows}
                  dataSourceInspectorItems={dataSourceInspectorItems}
                  errorCount={problemErrorCount}
                  extractorInspectorItems={extractorInspectorItems}
                  importReview={importMetadata ? (
                    <ImportReviewCommitPanel
                      assistantChangeLog={assistantChangeLog}
                      controller={importReviewController}
                      editorHistoryPastCount={editorHistory.past.length}
                      jsonTextLength={jsonText.length}
                      migrationSuggestions={migrationSuggestions}
                      onApplyMigrationSuggestion={applyMigrationSuggestion}
                      onSelectAction={selectAuthorAction}
                      script={script}
                    />
                  ) : null}
                  importedParameterFileMappings={importedParameterFileMappings}
                  onApplyRepairSuggestion={applyRepairSuggestion}
                  onOpenParameterData={() => openCenterTab('parameter-data')}
                  onOpenErrorDetails={() => setIssueDialog({
                    title: 'Script errors',
                    issues: editorIssueDetails.filter(issue => issue.severity === 'error' || issue.severity === 'blocker'),
                  })}
                  onOpenWarningDetails={() => setIssueDialog({
                    title: 'Script warnings',
                    issues: editorIssueDetails.filter(issue => issue.severity === 'warning'),
                  })}
                  onSelectActionProblem={selectAuthorPhaseAction}
                  onSelectExtractor={(item) => {
                    if (item.phase === 'action') selectAuthorAction(item.actionIndex)
                    else {
                      setActiveAction(null)
                      setAuthorCodeFocus({ phase: item.phase, actionIndex: item.actionIndex })
                      openCenterTab(SCRIPT_JSON_TAB_ID)
                    }
                  }}
                  parameterInspectorItems={parameterInspectorItems}
                  readiness={<p>Use Checks &amp; readiness above to review validation and runtime evidence.</p>}
                  repairSuggestions={repairSuggestions}
                  scriptLevelIssues={scriptLevelIssues}
                  warningCount={problemWarningCount}
                  additionalIssues={railAdditionalIssues}
                />
                )}
                </>
                )}
              </div>
            </div>
          </div>
          )}

          {centerTabMountTarget('parameter-data') && createPortal(
            <ParameterDataCellDraftProvider onDraftChange={stageParameterDataCellEdit}>
              {renderStructuredCenterDocument('Parameters', (<>
                {scenarioDataUsage.length > 0 && <ScriptDataUsagePanel usage={scenarioDataUsage} saved={Boolean(scriptId)} bindings={scenarioBindings} onSelectStep={focusDataStep}
                  onOpenTable={projectId ? (section, resourceId) => setSharedAssetsDialog({ section, resourceId }) : undefined} />}
                <ParameterDataDialog
              onAddDataset={addParameterDataset}
              onShowUsesInSource={selectOutlineParameter}
              toolbarActions={<>
                <button type="button" className="btn btn-sm btn-ghost" onClick={scanForAutoParameters} title="Quick scan for hardcoded values to externalize">Auto Params</button>
                {isEditingExisting && <button type="button" className="btn btn-sm btn-ghost" onClick={() => { void runServerParameterization() }} disabled={deepScanning} title="Server scan of the saved script; also flags secrets">{deepScanning ? 'Scanning…' : 'Deep Scan'}</button>}
                {importedParameterFileMappings.length > 0 && <button type="button" className="btn btn-sm btn-ghost" onClick={() => importReviewController.setShowImportedParameterMappingsDialog(true)} title="How the source tool's parameter files mapped into data sources">Imported parameter files ({importedParameterFileMappings.length})</button>}
              </>}
              onInspectUsage={path => {
                const match = /^(init|actions|teardown)\[(\d+)\]/.exec(path)
                if (match) {
                  const phase = match[1] === 'actions' ? 'action' : match[1] as 'init' | 'teardown'
                  const key = `${phase}:${match[2]}`
                  setAuthorCodeFocus({ phase, actionIndex: Number(match[2]), key })
                }
                openCenterTab(SCRIPT_JSON_TAB_ID)
                setSuccess(`Parameter reference: ${path}`)
              }}
              onUndoStructure={undoEditorChange}
              canUndoStructure={editorHistory.past.length > 0}
              onMergeSources={mergeParameterSourceFiles}
              mergeSummary={parameterMergeSummary}
              dataEditorMode={dataEditorMode}
              dataSourceHistory={dataSourceHistory}
              onAddDataColumn={addDataColumn}
              onAddDataRow={addDataRow}
              onAddParameter={addParameter}
              onApplyDedupeHeaders={applyDedupeHeaders}
              onApplyFillHeaders={applyFillHeaders}
              onApplyNormalizeRows={applyNormalizeRows}
              onApplyPendingParameterDraft={applyPendingParameterDraft}
              onAutoFixDataSource={autoFixDataSource}
              onClearPendingParameterDraft={clearPendingParameterDraft}
              onExportDataSourceFile={exportDataSourceFile}
              onImportDataSourceFile={importDataSourceFile}
              onRedoDataSource={redoDataSource}
              onRemoveDataColumn={removeDataColumn}
              onRemoveDataRow={removeDataRow}
              onToggleDataEditorMode={toggleDataEditorModeAt}
              onUndoDataSource={undoDataSource}
              onUpdateDataCell={updateDataCell}
              onUpdateDataColumnName={updateDataColumnName}
              onUpdateDataSource={updateDataSource}
              onUpdateParameter={updateParameter}
              onUpdateParameterDraftName={updatePendingParameterDraftName}
              onUpdateParameterDraftValue={updatePendingParameterDraftValue}
              onSelectDataSource={setOpenDataEditor}
              openDataEditor={openDataEditor}
              pendingParameterDraft={pendingParameterDraft}
              selectedParameterIdx={selectedParameterIdx}
              onSelectParameter={selectParameterData}
              script={script}
                variant="document"
              />
              </>))}
            </ParameterDataCellDraftProvider>, centerTabMountTarget('parameter-data')!)}

          {replayScreenshotSelection && !runtimeVariablesUnavailable && (
            <ReplayScreenshotDialog
              alt={replayScreenshotSelection.alt}
              onClose={closeReplayScreenshot}
              src={replayScreenshotSelection.src}
            />
          )}
          {issueDialog && (
            <IssueDetailsDialog
              title={issueDialog.title}
              issues={issueDialog.issues}
              onClose={() => setIssueDialog(null)}
            />
          )}
        </div>
    </div>
  </>)
}

interface ReplayScreenshotDialogProps {
  alt: string
  onClose: () => void
  src: string
}

function ReplayScreenshotDialog({ alt, onClose, src }: ReplayScreenshotDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <DialogLayer><div className="editor-dialog-backdrop" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="editor-dialog replay-screenshot-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Replay Screenshot"
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Replay Screenshot</h3>
            <p className="editor-dialog-subtitle">Inspect the captured browser replay screenshot at full size.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body replay-screenshot-dialog-body">
          <img className="replay-screenshot-dialog-image" src={src} alt={alt} />
        </div>
      </div>
    </div></DialogLayer>
  )
}

function useScriptEditorState(initialScript?: ScriptData | null) {
  const [state, dispatch] = useReducer(scriptEditorReducer, initialScript, createScriptEditorState)
  const baselineJsonRef = useRef(formatScriptJson(initialScript || { ...EMPTY_SCRIPT }))
  const replayEventRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const dispatchers = useMemo(() => createScriptEditorDispatchers(dispatch), [dispatch])
  const historyFieldRef = useRef<{ element: Element | null; group: number }>({ element: null, group: 0 })
  const setScript = useCallback((update: React.SetStateAction<ScriptData>) => {
    const element = typeof document === 'undefined' ? null : document.activeElement
    const field = element?.matches('input, textarea, [contenteditable="true"]') ? element : null
    if (field !== historyFieldRef.current.element || !field) historyFieldRef.current = { element: field, group: historyFieldRef.current.group + 1 }
    dispatch({ type: 'document/edit', update, time: Date.now(), group: field ? String(historyFieldRef.current.group) : undefined })
  }, [])
  const documentHistory = selectScriptEditorDocument(state)
  const navigation = selectScriptEditorNavigation(state)
  const parametersData = selectScriptEditorParametersData(state)
  const importReview = selectScriptEditorImportReview(state)
  const replayDebug = selectScriptEditorReplayDebug(state)
  const asyncOperations = selectScriptEditorAsyncOperations(state)

  const hydrateEditorState = useCallback((nextScript: ScriptData) => {
    baselineJsonRef.current = formatScriptJson(nextScript)
    dispatch({ type: 'editor/hydrate', script: nextScript })
  }, [dispatch])

  const markBaseline = useCallback((nextScript: ScriptData) => {
    baselineJsonRef.current = formatScriptJson(nextScript)
  }, [])

  const closeParameterDialog = useCallback(() => {
    dispatch({ type: 'parametersData/closeDialog' })
  }, [dispatch])
  const closeReplayScreenshot = useCallback(() => {
    dispatch({ type: 'replayDebug/closeScreenshot' })
  }, [dispatch])
  const toggleSection = useCallback((id: string) => {
    dispatch({ type: 'navigation/toggleSection', id })
  }, [dispatch])

  return {
    ...documentHistory,
    ...parametersData,
    ...replayDebug,
    ...asyncOperations,
    activeAction: navigation.activeAction,
    openSections: navigation.openSections,
    importBaselineScript: importReview.baselineScript,
    baselineJsonRef,
    closeParameterDialog,
    hydrateEditorState,
    markBaseline,
    replayEventRefs,
    ...dispatchers,
    setScript,
    restoreScript: dispatchers.setScript,
    toggleSection,
    closeReplayScreenshot,
  }
}
