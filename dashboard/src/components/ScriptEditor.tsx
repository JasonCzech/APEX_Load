import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import type { BrowserToHTTPPreviewResponse } from '../hooks/api/useScriptApi'
import { useKeyboardShortcuts } from '../hooks/KeyboardShortcutProvider'
import { useHasTopbarContributionProvider, useTopbarContribution, type TopbarContribution } from '../hooks/TopbarContributionProvider'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
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
import BrowserReplayDebugger from './BrowserReplayDebugger'
import {
  addCorrelationExtractorIfMissing,
  applyCorrelationRepairToAction,
  correlationEditableActionsKey,
  correlationExtractorScope,
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
import { buildLocalReplayPreflight, reconcileReplayPreflightSecretStatus, replayPreflightStatusLabel } from './replayPreflightUtils'
import { prepareProjectSecretsSettingsNavigation } from './projectSecretsNavigation'
import {
  applyMigrationAssistantSuggestion,
  applyAuthoringComponentToScript,
  applyScriptRepairSuggestion,
  buildMigrationAssistantSuggestions,
  buildProtocolStarterTemplates,
  buildScriptRepairSuggestions,
  collectAuthoringIssues,
  groupIssuesByPhaseAction,
  summarizeDataSources,
  summarizeParameters,
  type MigrationAssistantSuggestion,
  type ScriptRepairSuggestion,
} from './scriptAuthoringAssist'
import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'
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
import { actionRequestCount, actionExtractorCount, isControlAction, isReviewGatedJavaScriptAction, actionRequiresReview, actionSelectorFallbackCount, actionTypeSummary, actionRowLabel } from './scriptActionHelpers'
import { ActionEditor, ValidationEvidencePanel } from './ScriptEditorActionEditor'
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
  codePathForAuthorFocus,
  findCandidateLiteralRangeInJson,
  formatParameterCandidateOccurrence,
  formatScriptJson,
  normalizeOccurrenceIndex,
  parseScriptJsonDraft,
  phaseActionsForScript,
  resolveAuthorJsonScopeByKey,
  SCRIPT_JSON_DEFAULT_COMPANION_TAB,
  type AuthorJsonScope,
} from './scriptEditorJsonModel'
import {
  ScriptJsonCompanionHeader,
  ScriptJsonDocument,
  ScriptJsonInsightsView,
  ScriptJsonParameterOutline,
} from './ScriptJsonAuthoringView'
import {
  useJsonParameterMenu,
  useScriptJsonDraftController,
  useScriptJsonNavigationController,
} from './useScriptJsonAuthoringController'
import {
  buildBrowserHTTPConversionReview,
  debugBreakpointKey,
  formatDebugLeafSuffix,
  humanizeStepSegment,
  normalizeDebugPhase,
  replayNetworkEntriesForConversion,
  safeParseReplayVariables,
  upsertReplayActivity,
} from './scriptEditorReplayModel'
import { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'
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
  ImportReviewOutlineSection,
  ImportedMetadataUtilityPanel,
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
  buildMergedParameterSourceScript,
  compareDataSourceCellValues,
  summarizeParameterSourceMerge,
  type DataSourceSortDirection,
} from './scriptEditorParameterDataModel'
import { useEditorDialogRef } from './useEditorDialogRef'
import { ScriptRuntimeSettingsSidebar, ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'
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

const DEBUG_HIDDEN_CENTER_TABS = new Set<CenterWorkbenchTabId>(['imported-metadata'])

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
  { kind: 'data_pool', label: 'Data Pools' },
  { kind: 'teardown', label: 'Teardown Blocks' },
] as const

// User-saved blocks carry a server-generated "block-" id; built-in catalog entries do not.
const isUserBlock = (component: AuthoringComponent) => (component.id || '').startsWith('block-')

const BROWSER_REPLAY_TAB_ID: CenterWorkbenchTabId = 'browser-replay'





/**
 * Only virtualize long lists. Below this count the (cheap) plain render is used, which keeps
 * behavior identical for ordinary scripts and test fixtures; large imports (thousands of
 * actions/mappings) switch to windowed rendering so only visible rows hit the DOM.
 */
const LIST_VIRTUALIZE_THRESHOLD = 100

interface CenterTabbedPaneProps {
  activeTabId: CenterWorkbenchTabId
  ariaLabel: string
  className?: string
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
  onCloseTab,
  onPanelElement,
  onSelectTab,
  renderTabPanel,
  tabs,
}: CenterTabbedPaneProps) {
  const activeTab = tabs.find(tab => tab.id === activeTabId) || tabs[0]

  const handleTabKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, index: number) => {
    if (tabs.length === 0) return
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
    <section className={`center-tabbed-pane ${className}`} aria-label={ariaLabel}>
      <div className="center-tab-strip" role="tablist" aria-label={`${ariaLabel} tabs`}>
        {tabs.map((tab, index) => {
          const selected = tab.id === activeTab.id
          const tabButtonId = `center-tab-${tab.id}`
          const panelId = `center-tab-panel-${tab.id}`
          return (
            <div key={tab.id} className={`center-tab-item ${selected ? 'active' : ''} ${!tab.closeable ? 'pinned' : ''}`}>
              <button
                type="button"
                id={tabButtonId}
                role="tab"
                aria-controls={panelId}
                aria-selected={selected}
                className="center-tab-button"
                tabIndex={selected ? 0 : -1}
                title={tab.title}
                onClick={() => onSelectTab(tab.id)}
                onKeyDown={event => handleTabKeyDown(event, index)}
              >
                <span className="center-tab-dot" aria-hidden="true" />
                <span className="center-tab-label">{tab.label}</span>
              </button>
              {tab.closeable && (
                <button
                  type="button"
                  className="center-tab-close"
                  aria-label={`Close ${tab.label}`}
                  title={`Close ${tab.label}`}
                  onClick={() => onCloseTab(tab.id)}
                >
                  ×
                </button>
              )}
            </div>
          )
        })}
      </div>
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
  replay_session_id?: string
  debug_session_id?: string
  event_id?: string
  event_index?: number
  action?: ScriptDebugAction
  event?: ReplayActionEvent
  report?: ReplayReport
  session?: ScriptDebugSession
}

type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition


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
  initiallyDirty?: boolean
  initialFocusedActionName?: string | null
  initialFocusedSection?: string | null
  initialFocusedDataSourceName?: string | null
  initialFocusedRuntimeArea?: string | null
  projectId?: string
  folderId?: string | null
  onSave?: (script: StoredScript) => void
  onClose?: () => void
}

function scriptHasAuthoredActions(script: ScriptData): boolean {
  return (script.init?.length || 0) > 0 || script.actions.length > 0 || (script.teardown?.length || 0) > 0
}


export default function ScriptEditor({ scriptId, initialScript, initiallyDirty = false, initialFocusedActionName, initialFocusedSection, initialFocusedDataSourceName, initialFocusedRuntimeArea, projectId, folderId, onSave, onClose }: Props) {
  const api = useApi()
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
  // Object-repository logical names for the per-action object_ref autocomplete.
  const [objectRefSuggestions, setObjectRefSuggestions] = useState<string[]>([])
  useEffect(() => {
    if (!projectId || typeof api.listObjectRepositories !== 'function') { setObjectRefSuggestions([]); return }
    let cancelled = false
    void api.listObjectRepositories({ project_id: projectId })
      .then(res => {
        if (cancelled) return
        const names = new Set<string>()
        for (const repo of res.object_repositories || []) {
          for (const object of repo.objects || []) {
            if (object.logical_name) names.add(object.logical_name)
          }
        }
        setObjectRefSuggestions(Array.from(names).sort())
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
  const scriptAssistExtensions = useMemo(
    () => buildDslAssistExtensions(
      script => api.validateDraftScript(script as ScriptDefinition, projectId, folderId ?? undefined),
      () => replayVariableInspectorRef.current,
    ),
    [api, projectId, folderId],
  )
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const { runGuarded } = useNavigationGuardRunner()
  const openProjectSecrets = useCallback(() => {
    runGuarded(() => {
      prepareProjectSecretsSettingsNavigation(projectId)
      window.location.hash = '#/settings'
    })
  }, [projectId, runGuarded])
  const {
    activeAction,
    activeReplaySessionId,
    browserReplaySession,
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
    replayActivity,
    replayEventRefs,
    replayOptions,
    replayReport,
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
    setScript,
    setSuccess,
    setValidating,
    setWarnings,
    success,
    validating,
    warnings,
    closeReplayScreenshot,
  } = useScriptEditorState(initialScript)
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
  const [initialSaveRequired, setInitialSaveRequired] = useState(initiallyDirty)
  const initialSaveSourceRef = useRef({ script: initialScript, initiallyDirty })
  useEffect(() => {
    const previous = initialSaveSourceRef.current
    if (previous.script !== initialScript || (!previous.initiallyDirty && initiallyDirty)) {
      setInitialSaveRequired(initiallyDirty)
    }
    initialSaveSourceRef.current = { script: initialScript, initiallyDirty }
  }, [initialScript, initiallyDirty])
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
    outlineParameterListCollapsed,
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
    selectedParameterSameLineOptions,
    setActiveAutoParameterCandidateId,
    setActiveAutoParameterOccurrenceIndex,
    setAuthorCodeFocus,
    setOutlineActionFilter,
    setOutlineActionListCollapsed,
    setOutlineParameterListCollapsed,
    setParameterPaneShowAuto,
    setScriptJsonSidebarTab,
    setSelectedOutlineParameterName,
    setShowFullScriptJson,
    showFullScriptJson,
  } = jsonNavigationController
  // The outline filter narrows the rendered rows by name/type/target without
  // mutating the script; rows keep their original actionIndex so every row
  // handler (select/move/breakpoint) stays correct over the filtered view.
  const filteredActionRows = useMemo(
    () => filterScriptPhaseActionRows(scriptPhaseActionRows, outlineActionFilter),
    [scriptPhaseActionRows, outlineActionFilter],
  )
  // Per-row transaction band membership (left rail bracketing a transaction's rows).
  const transactionBands = useMemo(() => computeTransactionBands(filteredActionRows), [filteredActionRows])
  // Container actions (http_sequence / control_*) the operator has expanded in the
  // Author outline to reveal their nested sub-requests / steps.
  const [expandedAuthorContainers, setExpandedAuthorContainers] = useState<Set<string>>(() => new Set())
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
  const httpConversionReview = buildBrowserHTTPConversionReview(script, replayReport, browserReplaySession)
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
    scriptId,
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
  const centerTabDescriptors = useMemo<CenterWorkbenchTabDescriptor[]>(() => {
    const descriptors: CenterWorkbenchTabDescriptor[] = [
      { id: ACTION_LIST_TAB_ID, label: 'Action List', title: 'Action List', closeable: false },
      { id: SCRIPT_JSON_TAB_ID, label: 'Source', title: 'Source JSON', closeable: false },
      { id: 'runtime-settings', label: 'Runtime Settings', title: 'Runtime Settings', closeable: true },
      { id: 'parameter-data', label: 'Parameter Data', title: 'Parameter Data', closeable: true },
    ]
    if (importMetadata) {
      // Note: the full Metadata JSON is intentionally NOT a center tab — it is opened
      // on demand from the left outline (Imported Metadata JSON) so its multi-MB read-only
      // editor never mounts unless the user explicitly asks to see it.
      descriptors.push(
        { id: 'imported-metadata', label: 'Source Mapping', title: 'Source Mapping', closeable: true },
      )
    }
    if (objectDescriptorReview.rows.length > 0) {
      descriptors.push({ id: 'object-descriptors', label: 'Object Descriptors', title: 'Object Descriptors', closeable: true })
    }
    if (isBrowserScript) {
      descriptors.splice(2, 0, { id: BROWSER_REPLAY_TAB_ID, label: 'Browser Replay', title: 'Browser Replay Cockpit', closeable: false })
      descriptors.push({ id: 'http-conversion', label: 'HTTP Conversion', title: 'Browser To HTTP Conversion Review', closeable: true })
    }
    return descriptors
  }, [importMetadata, isBrowserScript, objectDescriptorReview.rows.length])
  const centerTabDescriptorById = useMemo(() => {
    return new Map(centerTabDescriptors.map(tab => [tab.id, tab]))
  }, [centerTabDescriptors])
  const availableCenterTabIds = useMemo(() => new Set(centerTabDescriptors.map(tab => tab.id)), [centerTabDescriptors])
  const visibleCenterTabs = useMemo(() => {
    return centerTabsState.tabs
      .map(tabId => centerTabDescriptorById.get(tabId))
      .filter((tab): tab is CenterWorkbenchTabDescriptor => Boolean(tab))
  }, [centerTabDescriptorById, centerTabsState.tabs])
  const selectCenterTab = useCallback((tabId: CenterWorkbenchTabId) => {
    if (!availableCenterTabIds.has(tabId)) return
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
    setSelectedOutlineParameterName(parameterName)
    // Show the selected parameter's details in the Parameter companion page.
    setParameterPaneShowAuto(false)
    setScriptJsonSidebarTab('auto-params')
    openCenterTab(SCRIPT_JSON_TAB_ID)
  }, [openCenterTab, setParameterPaneShowAuto, setScriptJsonSidebarTab, setSelectedOutlineParameterName])
  const closeCenterTab = useCallback((tabId: CenterWorkbenchTabId) => {
    if (tabId === ACTION_LIST_TAB_ID || tabId === SCRIPT_JSON_TAB_ID || tabId === BROWSER_REPLAY_TAB_ID) return
    setCenterTabPanelElements(prev => prev[tabId] ? { ...prev, [tabId]: null } : prev)
    setCenterTabsState(prev => {
      if (!prev.tabs.includes(tabId)) return prev
      const nextTabs = prev.tabs.filter(candidate => candidate !== tabId)
      const activeTabId = prev.activeTabId === tabId ? ACTION_LIST_TAB_ID : prev.activeTabId
      return { tabs: nextTabs.length > 0 ? nextTabs : [ACTION_LIST_TAB_ID, SCRIPT_JSON_TAB_ID], activeTabId }
    })
  }, [])
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
      const activeTabId = nextTabs.includes(prev.activeTabId) ? prev.activeTabId : ACTION_LIST_TAB_ID
      if (activeTabId === prev.activeTabId && nextTabs.length === prev.tabs.length && nextTabs.every((tabId, index) => tabId === prev.tabs[index])) {
        return prev
      }
      return { tabs: nextTabs, activeTabId }
    })
  }, [availableCenterTabIds])
  useEffect(() => {
    if (!importMetadata && !sourceReview) return
    setCenterTabsState(prev => {
      const nextTabs = [...prev.tabs]
      if (importMetadata && !nextTabs.includes('imported-metadata')) {
        nextTabs.push('imported-metadata')
      }
      if (nextTabs.length === prev.tabs.length && nextTabs.every((tabId, index) => tabId === prev.tabs[index])) return prev
      return { ...prev, tabs: nextTabs }
    })
  }, [importMetadata, sourceReview])
  useEffect(() => {
    if (workspaceTab === 'debug' && isBrowserScript) {
      openCenterTab(BROWSER_REPLAY_TAB_ID)
    }
  }, [isBrowserScript, openCenterTab, workspaceTab])
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
  const [validationEditableActionsKey, setValidationEditableActionsKey] = useState<string | null>(null)
  const [validationReplayPreflight, setValidationReplayPreflight] = useState<ReplayPreflight | null>(null)
  const [validationSidebarResult, setValidationSidebarResult] = useState<ValidationSidebarResult | null>(null)
  const validationRequestRef = useRef(0)
  const saveRequestRef = useRef(0)
  const saveInFlightScopeRef = useRef<object | null>(null)
  const editorResourceScopeRef = useRef({ projectId, folderId, scriptId, initialScript })
  if (
    editorResourceScopeRef.current.projectId !== projectId
    || editorResourceScopeRef.current.folderId !== folderId
    || editorResourceScopeRef.current.scriptId !== scriptId
    || editorResourceScopeRef.current.initialScript !== initialScript
  ) {
    editorResourceScopeRef.current = { projectId, folderId, scriptId, initialScript }
  }
  const validationDocumentKey = useMemo(
    () => jsonParseMessage ? null : formatScriptJson(currentDraftScript),
    [currentDraftScript, jsonParseMessage],
  )
  const validationDocumentKeyRef = useRef<string | null>(null)
  validationDocumentKeyRef.current = validationDocumentKey
  useEffect(() => {
    // Resource changes reuse this component in the authoring surface. Invalidate
    // any validation issued for the previous script/project before its response
    // can update readiness evidence or commit validation-time metadata.
    validationRequestRef.current += 1
    saveRequestRef.current += 1
    saveInFlightScopeRef.current = null
    replayStartInFlightRef.current = null
    replayStartAttemptRef.current = null
    setValidating(false)
    setSaving(false)
    setValidationEvidence(null)
    setValidationEditableActionsKey(null)
    setValidationReplayPreflight(null)
    setValidationSidebarResult(null)
    setReplayStartPending(false)
    setReplayEditableActionsKey(null)
    setActiveReplaySessionId(null)
    setBrowserReplaySession(null)
    setReplayReport(null)
    setReplayActivity([])
    setExpandedReplayEvent(null)
    setExpandedReplayBrowserArtifacts({})
    setReplayScreenshotSelection(null)
    setReplaySeedVariables({})
    setReplaying(false)
    setError('')
    setSuccess('')
    setWarnings([])
  }, [
    folderId,
    initialScript,
    projectId,
    scriptId,
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
    return collectAuthoringIssues(script)
  }, [script, dslCatalogReady])
  const actionIssueBuckets = useMemo(
    () => groupIssuesByPhaseAction(authoringIssues, script),
    [authoringIssues, script],
  )
  const scriptLevelIssues = useMemo(
    () => authoringIssues.filter((issue) => typeof issue.actionIndex !== 'number'),
    [authoringIssues],
  )
  const errorCount = authoringIssues.filter((issue) => issue.severity === 'error').length
  const warningCount = authoringIssues.filter((issue) => issue.severity === 'warning').length
  const starterTemplates = useMemo(
    () => buildProtocolStarterTemplates(script.protocol, script.actions.length),
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
    const phase = normalizeDebugPhase(event.phase || 'action')
    if (phase !== 'action') {
      setError('Suggested selectors can only be applied to script action steps.')
      setSuccess('')
      return
    }
    const actionIndex = typeof event.action_index === 'number'
      ? event.action_index
      : (() => {
        const eventTarget = (event.selector || event.target || '').trim()
        const matches = script.actions
          .map((action, index) => ({ action, index }))
          .filter(({ action }) => {
            const actionTarget = (action.selector || action.target || '').trim()
            return Boolean(event.action_name && action.name === event.action_name)
              || Boolean(eventTarget && actionTarget === eventTarget)
          })
        return matches.length === 1 ? matches[0].index : -1
      })()
    if (actionIndex < 0 || actionIndex >= script.actions.length) {
      setError('Suggested selector could not be mapped to a script action.')
      setSuccess('')
      return
    }
    const currentAction = script.actions[actionIndex]
    const nextScript: ScriptData = {
      ...script,
      actions: script.actions.map((action, index) => (
        index === actionIndex ? { ...action, selector: nextLocator } : action
      )),
    }
    replaceScriptWithHistory(nextScript)
    setActiveAction(actionIndex)
    setAuthorCodeFocus({ phase: 'action', actionIndex })
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

  const undoEditorChange = useCallback(() => {
    setEditorHistory(previous => {
      const priorScript = previous.past[previous.past.length - 1]
      if (!priorScript) return previous
      const nextPast = previous.past.slice(0, -1)
      const current = JSON.parse(JSON.stringify(script)) as ScriptData
      setScript(priorScript)
      setJsonText(formatScriptJson(priorScript))
      setJsonDirty(false)
      setSuccess('Undid the last guided authoring change.')
      setError('')
      return {
        past: nextPast,
        future: [current, ...previous.future].slice(0, 30),
      }
    })
  }, [script, setEditorHistory, setError, setJsonDirty, setJsonText, setScript, setSuccess])

  const redoEditorChange = useCallback(() => {
    setEditorHistory(previous => {
      const [nextScript, ...remainingFuture] = previous.future
      if (!nextScript) return previous
      const current = JSON.parse(JSON.stringify(script)) as ScriptData
      setScript(nextScript)
      setJsonText(formatScriptJson(nextScript))
      setJsonDirty(false)
      setSuccess('Redid the guided authoring change.')
      setError('')
      return {
        past: [...previous.past, current].slice(-30),
        future: remainingFuture,
      }
    })
  }, [script, setEditorHistory, setError, setJsonDirty, setJsonText, setScript, setSuccess])

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
    setEditorHistory({ past: [], future: [] })
  }, [importBaselineScript, initialScript, scriptId, setEditorHistory])
  const hydratedInitialScriptRef = useRef<{ script: ScriptData; scriptId?: string | null } | null>(null)

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

      Object.entries(action.headers || {}).forEach(([headerKey, headerValue]) => {
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
  }, [hydrateEditorState, initialFocusedActionName, initialFocusedDataSourceName, initialFocusedRuntimeArea, initialFocusedSection, initialScript, openCenterTab, openImportedRuntimeSettings, scriptId, setActiveAction, setAuthorCodeFocus, setOpenDataEditor, setOpenSections, setScriptJsonSidebarTab, setShowFullScriptJson])

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
    if (row.phase === 'action') {
      selectSidebarAction(row.actionIndex)
      return
    }
    selectAuthorPhaseIndex(row.phase, row.actionIndex)
  }

  const selectAuthorNestedItem = (item: Extract<DebugRailRenderRow, { kind: 'container' | 'leaf' }>) => {
    const { row } = item
    const label = item.kind === 'leaf'
      ? item.childAction.name || item.name || humanizeStepSegment(item.stepPath[item.stepPath.length - 1] || '')
      : item.name
    setActiveAction(null)
    setAuthorCodeFocus({
      phase: row.phase,
      actionIndex: row.actionIndex,
      stepPath: item.stepPath,
      key: item.key,
      label,
    })
    setShowFullScriptJson(false)
  }

  useEffect(() => {
    if (!scriptId || initialScript) return

    let isCancelled = false

    api.getScript(scriptId)
      .then((res) => {
        if (isCancelled) return
        if (!res.script) {
          setError('Failed to load script')
          return
        }
        setAuthorCodeFocus(null)
        setShowFullScriptJson(false)
        hydrateEditorState(res.script)
      })
      .catch((loadError: unknown) => {
        if (isCancelled) return
        setError(getErrorMessage(loadError, 'Failed to load script'))
      })

    return () => {
      isCancelled = true
    }
  }, [api, hydrateEditorState, initialScript, scriptId, setAuthorCodeFocus, setError, setShowFullScriptJson])

  const handleReplaySocketMessage = useCallback((message: WSMessage) => {
    const msg = message as ReplayWSMessage
    const trackedSessionId = activeReplaySessionId || (replayStartPending ? replayStartAttemptRef.current?.sessionId : null)
    if (!trackedSessionId || (msg.replay_session_id !== trackedSessionId && msg.debug_session_id !== trackedSessionId)) return

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
      if (session) setBrowserReplaySession(session)
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
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
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
        setExpandedReplayEvent(msg.event_index)
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
        setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
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
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
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
        setExpandedReplayEvent(msg.event_index)
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
        const events = [...(prev.events || [])]
        events[index] = replayEvent as unknown as BrowserReplayEvent
        return {
          ...prev,
          current_event_index: index,
          current_event: replayEvent as unknown as BrowserReplayEvent,
          events,
        }
      })
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
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
        setExpandedReplayEvent(msg.event_index)
        setDebugDetailCollapsed(false)
      }
      return
    }

    if ((msg.type === 'browser_replay_paused' || msg.type === 'browser_replay_resumed') && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      const isPaused = msg.type === 'browser_replay_paused'
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
      setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
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
        setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
        if (report.events.length > 0) setDebugDetailCollapsed(false)
      }
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
    }
  }, [activeReplaySessionId, replayStartPending, setActiveReplaySessionId, setBrowserReplaySession, setDebugBreakpoints, setDebugDetailCollapsed, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayOptions, setReplayReport, setReplaying])

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
    if (path.startsWith('config.')) {
      pushEditorHistory(script)
    }
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
    updatePhaseActionList(phase, actions => [...actions, a])
    selectAuthorPhaseIndex(phase, currentActions.length)
  }

  const addActionTemplate = (templateKey: string) => {
    const template = starterTemplates.find((candidate) => candidate.key === templateKey)
    if (!template) return
    const nextAction = JSON.parse(JSON.stringify(template.action)) as Action
    pushEditorHistory(script)
    setScript(prev => ({ ...prev, actions: [...prev.actions, nextAction] }))
    selectAuthorAction(script.actions.length)
    setSuccess(`Added starter block: ${template.label}.`)
    setError('')
  }

  const removePhaseAction = (phase: ScriptActionPhase, idx: number) => {
    updatePhaseActionList(phase, actions => actions.filter((_, i) => i !== idx))
    remapPhaseBreakpointsAfterRemove(phase, idx)
    remapSelectionAfterRemove(phase, idx)
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
      actions[idx] = updater(actions[idx])
      return actions
    })
  }

  const updatePhaseAction = (phase: ScriptActionPhase, idx: number, field: keyof Action, value: unknown) => {
    updatePhaseActionObject(phase, idx, action => ({ ...action, [field]: value }))
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
    const extractorAlreadyExists = Boolean(sourceAction.extractors?.some(existing => existing.name === effectiveExtractor.name))
    const actions = [...script.actions]
    actions[actionIdx] = addCorrelationExtractorIfMissing(actions[actionIdx], effectiveExtractor)
    if (effectiveRepair && effectiveRepair.failingActionIndex >= 0 && effectiveRepair.failingActionIndex < actions.length) {
      actions[effectiveRepair.failingActionIndex] = applyCorrelationRepairToAction(actions[effectiveRepair.failingActionIndex], effectiveRepair)
    }
    setScript({ ...script, actions })
    const repairSuffix = repairWillApply && effectiveRepair
      ? ` Updated action ${effectiveRepair.failingActionIndex + 1} to use ${effectiveRepair.replacement}.`
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

  const addPhaseAssertion = (phase: ScriptActionPhase, actionIdx: number) => {
    updatePhaseActionObject(phase, actionIdx, action => {
      const assertions = [...(action.assertions || []), defaultAssertionForProtocol(script.protocol)]
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

  const scriptEditorDirty = initialSaveRequired || jsonDirty || formattedScriptJson !== baselineJsonRef.current
  const saveScriptDisabled = saving || validating || !scriptEditorDirty || !currentDraftScript.name || Boolean(jsonParseMessage)
  const saveScriptTitle = saving
    ? 'Saving script'
    : validating
      ? 'Wait for validation to finish before saving'
      : jsonParseMessage
        ? 'Fix invalid Script JSON before saving'
        : !scriptEditorDirty
          ? 'No unsaved changes to save'
          : !currentDraftScript.name
            ? 'Enter a script name before saving'
            : 'Save script'
  const validateScriptDisabled = saving || replaying || validating || !currentDraftScript.name
  const validateScriptTitle = validating
    ? 'Checking script readiness'
    : !currentDraftScript.name
      ? 'Enter a script name before checking readiness'
      : 'Recheck script readiness'
  const scriptEditorDirtyRef = useRef(scriptEditorDirty)
  const handleSaveRef = useRef<() => Promise<boolean>>(async () => false)
  scriptEditorDirtyRef.current = scriptEditorDirty

  const handleSave = async (): Promise<boolean> => {
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
    setValidationEditableActionsKey(null)
    setValidationReplayPreflight(null)
    try {
      const currentScript = resolveCurrentScript()
      let savedScript: StoredScript
      if (scriptId) {
        savedScript = await api.updateScript(scriptId, currentScript)
      } else {
        savedScript = await api.createScript(currentScript, projectId, folderId)
      }
      if (!requestIsCurrent()) return true
      commitJsonDraft(currentScript)
      markBaseline(currentScript)
      setInitialSaveRequired(false)
      setSuccess('Script saved successfully!')
      if (scriptId) {
        await refreshScriptSecretStatus()
        setProjectSecretProbeRetryGeneration(generation => generation + 1)
      }
      onSave?.(savedScript)
      return true
    } catch (e: unknown) {
      if (!requestIsCurrent()) return false
      setError(getErrorMessage(e, 'Failed to save script'))
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
  handleSaveRef.current = handleSave
  const scriptEditorGuard = useMemo<NavigationGuard>(() => ({
    id: `script-editor-${scriptId || 'new'}`,
    label: scriptId ? 'Edit Script' : 'Create Script',
    isDirty: () => scriptEditorDirtyRef.current,
    save: () => handleSaveRef.current(),
    discard: () => undefined,
  }), [scriptId])
  useNavigationGuard(scriptEditorGuard, scriptEditorDirty)
  const handleCloseEditor = useCallback(() => {
    if (onClose) runGuarded(onClose)
  }, [onClose, runGuarded])

  const handleValidateScript = async () => {
    const requestID = ++validationRequestRef.current
    const requestResourceScope = editorResourceScopeRef.current
    const requestIsCurrent = () => (
      validationRequestRef.current === requestID
      && editorResourceScopeRef.current === requestResourceScope
    )
    setError('')
    setSuccess('')
    setWarnings([])
    setValidationEvidence(null)
    setValidationEditableActionsKey(null)
    setValidationReplayPreflight(null)
    setValidationSidebarResult(null)
    setValidating(true)
    const sourceDocumentKey = validationDocumentKeyRef.current
    const requestDocumentIsCurrent = () => (
      requestIsCurrent()
      && validationDocumentKeyRef.current === sourceDocumentKey
    )
    try {
      const currentScript = resolveCurrentScript()
      const res = scriptId && !scriptEditorDirty
        ? await api.validateScript(scriptId)
        : replayConfiguredVariables
          ? await api.validateDraftScript(currentScript, projectId, folderId, replayConfiguredVariables)
          : await api.validateDraftScript(currentScript, projectId, folderId)
      if (!requestDocumentIsCurrent()) return
      const validationScript = applyValidationRuntimeSettingsMetadata(currentScript, res)
      const validationWarnings = normalizeWarnings(res.warnings)
      setValidationReplayPreflight(res.preflight || null)
      setValidationEvidence({
        data_source_fit: res.data_source_fit,
        ai_guardrails: res.ai_guardrails,
        authoring_health: res.authoring_health,
      })
      if (jsonDirty || currentScript.metadata?.import || validationScript !== currentScript) {
        commitJsonDraft(validationScript)
      }
      if (res.valid) {
        setValidationEditableActionsKey(scopedEditableActionsEvidenceKey(validationScript, projectId, scriptId))
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
      setValidationEditableActionsKey(null)
    } catch (e: unknown) {
      if (!requestDocumentIsCurrent()) return
      const validationWarnings = normalizeWarnings(getErrorWarnings(e))
      setValidationEvidence(null)
      setValidationEditableActionsKey(null)
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

  const handleGenerateBrowserHTTPPreview = async () => {
	const requestID = ++httpConversionRequestRef.current
    setHttpConversionError('')
    setSuccess('')
    setHttpConversionGenerating(true)
    try {
      const currentScript = resolveCurrentScript()
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

  const activateWorkspaceTab = useCallback((nextTab: WorkbenchMode) => {
    setWorkspaceTab(nextTab)
    if (nextTab === 'debug') {
      setDebuggerTab('request')
      if (isBrowserScript) {
        openCenterTab(BROWSER_REPLAY_TAB_ID)
      }
    }
  }, [isBrowserScript, openCenterTab, setDebuggerTab])

  const openReplayDebugger = useCallback((openSettings = false) => {
    activateWorkspaceTab('debug')
    if (openSettings) setDebuggerSettingsOpen(true)
  }, [activateWorkspaceTab, setDebuggerSettingsOpen])

  const handleReplay = useCallback(async (options?: { startPaused?: boolean }) => {
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
    try {
      const currentScript = resolveCurrentScript()
      setReplayEditableActionsKey(scopedEditableActionsEvidenceKey(currentScript, projectId, scriptId))
      const variables = parseReplayVariablesText(replayOptions.variablesText)
      const replaySessionId = createReplaySessionId()
      requestedReplaySessionId = replaySessionId
      replayStartAttemptRef.current = {
        sessionId: replaySessionId,
        terminalObserved: false,
        awaitingReconciliation: false,
        notFoundCount: 0,
      }
      const startPaused = options?.startPaused ?? replayOptions.startPaused
      const baseReplayPayload = buildReplayStartPayload(currentScript, {
        projectId,
        groupName: replayOptions.groupName,
        replaySessionId,
        variables,
        ignoreThinkTime: replayOptions.ignoreThinkTime,
        startPaused,
      })
      const payload = {
        ...baseReplayPayload,
        browser: currentScript.protocol === 'browser'
          ? { ...(baseReplayPayload.browser || {}), capture_screenshots: 'per_action' }
          : baseReplayPayload.browser,
        group_name: replayOptions.groupName.trim() || `${currentScript.name || 'script'}-debug`,
        start_paused: startPaused,
        breakpoints: debugBreakpoints,
        break_on_failure: replayOptions.breakOnFailure,
        log_level: replayOptions.logLevel,
      }
      const replayGroup = payload.group_name || `${currentScript.name || 'script'}-replay`
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
            setExpandedReplayEvent(res.session.report.events?.length ? res.session.report.events.length - 1 : null)
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
      if (responseSessionFinalized) {
        setReplaying(false)
      }
      setWarnings(replayWarnings)
      setSuccess(replayWarnings.length > 0
        ? (currentScript.protocol === 'browser' ? 'Browser replay started with warnings.' : 'Debugger started with warnings.')
        : (currentScript.protocol === 'browser' ? 'Browser replay started.' : 'Debugger started.'))
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
      setReplayReport(null)
      setReplayEditableActionsKey(null)
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
  }, [api, commitJsonDraft, debugBreakpoints, jsonDirty, openReplayDebugger, projectId, replayOptions.groupName, replayOptions.ignoreThinkTime, replayOptions.logLevel, replayOptions.startPaused, replayOptions.breakOnFailure, replayOptions.variablesText, resolveCurrentScript, scriptId, setActiveReplaySessionId, setBrowserReplaySession, setDebuggerBottomTab, setError, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplayScreenshotSelection, setReplaySeedVariables, setReplaying, setSuccess, setWarnings])

  const handleStopReplay = useCallback(async () => {
    if (!activeReplaySessionId) return
    try {
      await api.stopScriptDebugSession(activeReplaySessionId)
      setError('')
      setSuccess('Stopping debugger…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to stop debugger'))
    }
  }, [activeReplaySessionId, api, setError, setSuccess])

  const handlePauseResumeReplay = useCallback(async () => {
    if (!activeReplaySessionId || !browserReplaySession) return
    try {
      const isPaused = browserReplaySession.status === 'paused'
      const res = isPaused
        ? await api.resumeScriptDebugSession(activeReplaySessionId)
        : await api.pauseScriptDebugSession(activeReplaySessionId)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
      setSuccess(isPaused ? 'Resuming debugger…' : 'Pausing debugger…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update debugger state'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, setBrowserReplaySession, setError, setSuccess])

  const handleStepReplay = useCallback(async (mode: ScriptDebugStepMode = 'into') => {
    openReplayDebugger(false)
    if (!activeReplaySessionId || !browserReplaySession) {
      await handleReplay({ startPaused: true })
      return
    }
    if (browserReplaySession.status !== 'paused') return
    try {
      const res = await api.stepScriptDebugSession(activeReplaySessionId, mode)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
      setSuccess(mode === 'over' ? 'Stepping over the current block…' : mode === 'loop_end' ? 'Running to end of loop…' : 'Stepping debugger…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to step debugger'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, handleReplay, openReplayDebugger, setBrowserReplaySession, setError, setSuccess])

  const clearDebugBreakpoints = async () => {
    setDebugBreakpoints([])
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      return
    }
    try {
      const res = await api.updateScriptDebugBreakpoints(activeReplaySessionId, [])
      if (res?.session) setBrowserReplaySession(res.session)
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
      const res = await api.updateScriptDebugOptions(activeReplaySessionId, { break_on_failure: enabled })
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update break-on-failure'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, setBrowserReplaySession, setError, setReplayOptions])

  const breakpointEntryKey = useCallback((bp: ScriptDebugBreakpoint) =>
    bp.step_path && bp.step_path.length > 0
      ? debugStepPathKey(bp.phase, bp.action_index, bp.step_path)
      : debugBreakpointKey(bp.phase, bp.action_index, bp.request_index), [])
  const debugBreakpointKeys = useMemo(() => new Set(debugBreakpoints.map(breakpointEntryKey)), [debugBreakpoints, breakpointEntryKey])

  // pushDebugBreakpoints commits a new breakpoint set to state and, when a
  // session is live, PATCHes it so changes apply mid-run.
  const pushDebugBreakpoints = useCallback(async (next: ScriptDebugBreakpoint[]) => {
    setDebugBreakpoints(next)
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      return
    }
    try {
      const res = await api.updateScriptDebugBreakpoints(activeReplaySessionId, next)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update debugger breakpoints'))
    }
  }, [activeReplaySessionId, api, browserReplaySession, setBrowserReplaySession, setDebugBreakpoints, setError])

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
        ignore_think_time: replayOptions.ignoreThinkTime,
        start_paused: replayOptions.startPaused,
        log_level: replayOptions.logLevel,
        breakpoints: debugBreakpoints,
        variables: replaySeedVariables,
      },
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

  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success)'
      case 'POST': return 'var(--info)'
      case 'PUT': return 'var(--warning)'
      case 'DELETE': return 'var(--danger)'
      case 'PATCH': return 'var(--chart-6)'
      case 'http_sequence': return 'var(--accent)'
      case 'jms_publish': return 'var(--info)'
      case 'jms_consume': return 'var(--success)'
      case 'jms_request_reply': return 'var(--chart-6)'
      case 'jms_ack':
      case 'jms_commit':
      case 'jms_rollback': return 'var(--warning)'
      default: return 'var(--text-secondary)'
    }
  }

  const replayStatusLabel = replaying && replayReport && !replayReport.completed_at
    ? 'Running'
    : replayReport?.success
      ? 'Success'
      : 'Failed'
  const replayLatestActivity = replayActivity[replayActivity.length - 1]
  const replayVariableInspector = replayReport
    ? (replayReport.completed_at && replayReport.final_variables
        ? replayReport.final_variables
        : mergeReplayVariables(replaySeedVariables, replayReport.events))
    : null
  const hasReplayVariableInspectorData = replayVariableInspector ? Object.keys(replayVariableInspector).length > 0 : false
  replayVariableInspectorRef.current = replayVariableInspector

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
  const replayVariableDiffs = replayReport
    ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events)
    : []
  const replayConfiguredVariables = safeParseReplayVariables(replayOptions.variablesText)
  // Raw JSON is an executable draft even before the user applies it back to the
  // structured authoring controls. Never reuse an older server/session preflight
  // for that draft: derive requirements from the exact valid script that the Run
  // handler will parse and send.
  const unreconciledReplayPreflight = (!jsonDirty && (browserReplaySession?.preflight || validationReplayPreflight))
    || buildLocalReplayPreflight(currentDraftScript, replayConfiguredVariables)
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
  const replayPreflight = reconcileReplayPreflightSecretStatus(
    preflightWithAllRequiredSecrets,
    secretStatuses,
    secretStatusKnown,
    projectProbeIsUnavailable,
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
    setValidationReplayPreflight(null)
    setValidationEvidence(null)
    setValidationEditableActionsKey(null)
    setValidationSidebarResult(null)
  }, [jsonText, replayOptions.variablesText, script.actions, script.data_sources, script.name, script.parameters, script.protocol])
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
  const breakpointSummary = `${debugBreakpoints.length} selected across ${debugActionRows.length} executable action${debugActionRows.length === 1 ? '' : 's'}`
  useEffect(() => {
    if (hasReplayVariableInspectorData) {
      setDebugDetailCollapsed(false)
    }
  }, [hasReplayVariableInspectorData, setDebugDetailCollapsed])
  const scriptDslDocument = useMemo(() => buildDslDocument({ script }), [script])
  const selectedDebugCodeRow = workspaceTab === 'debug'
    ? (selectedReplayEvent ? selectedDebugRow : (debugActionRows[0] ?? null))
    : null
  const scriptCodeActivePath = workspaceTab === 'debug'
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
    grid.style.setProperty('--author-rail-width', 'max-content')
    const natural = Math.ceil(el.getBoundingClientRect().width)
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
          return
        }
        if (workspaceTab === 'debug') {
          setWorkspaceTab('author')
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
    let cancelled = false
    const reconcile = async () => {
      try {
        const res = await api.getScriptDebugSession(activeReplaySessionId)
        if (cancelled || !res?.session) return
        const session = res.session
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
        if (cancelled) return
        if (e instanceof ApiError && e.status === 404) {
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
          setActiveReplaySessionId(null)
          setBrowserReplaySession(prev => prev ? { ...prev, status: 'stopped' } : null)
          setReplaying(false)
          setError('Debugger session expired. Start a new debug run to continue.')
          setReplayActivity(prev => upsertReplayActivity(prev, {
            id: 'expired',
            kind: 'error',
            title: 'Debugger session expired',
            detail: 'The backend no longer has this debug session. Start a new run to continue.',
            timestamp: new Date().toISOString(),
            success: false,
          }))
          return
        }
        // Transient network/API errors retry on the next tick.
      }
    }
    void reconcile()
    const timer = window.setInterval(reconcile, 1200)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [activeReplaySessionId, replaying, replayStartPending, api, setActiveReplaySessionId, setBrowserReplaySession, setError, setReplayActivity, setReplayReport, setReplaying])

  const addParameter = () => {
    const nextIndex = (script.parameters?.length || 0) + 1
    setScript(prev => ({
      ...prev,
      parameters: [...(prev.parameters || []), { ...EMPTY_PARAMETER, name: `param_${nextIndex}` }],
    }))
    setSelectedParameterIdx(nextIndex - 1)
  }

  const updateParameter = (idx: number, field: keyof ParameterConfig, value: unknown) => {
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
    const action = phaseActionsForScript(script, phase)[draft.actionIndex]
    if (action) {
      const body = action.body || ''
      const replacement = `{{param.${name}}}`
      const nextBody = `${body.slice(0, draft.start)}${replacement}${body.slice(draft.end)}`
      updatePhaseAction(phase, draft.actionIndex, 'body', nextBody)
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

  const mutateDataSource = (
    idx: number,
    mutator: (current: DataSourceConfig) => DataSourceConfig,
    recordHistory = true,
  ) => {
    setScript(prev => {
      const sources = [...(prev.data_sources || [])]
      const current = sources[idx] || { ...EMPTY_DATA_SOURCE }
      const next = mutator(cloneDataSource(current))
      if (recordHistory) {
        setDataSourceHistory(history => {
          const entry = history[idx] || { past: [], future: [] }
          const past = [...entry.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: [] } }
        })
      }
      sources[idx] = cloneDataSource(next)
      return { ...prev, data_sources: sources }
    })
  }

  const updateDataSource = (idx: number, field: keyof DataSourceConfig, value: unknown, recordHistory = true) => {
    mutateDataSource(idx, current => ({ ...current, [field]: value }), recordHistory)
  }

  const undoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.past.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const previous = cloneDataSource(entry.past[entry.past.length - 1])
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = active.past.slice(0, -1)
          const future = [cloneDataSource(current), ...active.future].slice(0, 100)
          return { ...history, [idx]: { past, future } }
        })
        return previous
      },
      false,
    )
  }

  const redoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.future.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const [nextState, ...remainingFuture] = entry.future
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = [...active.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: remainingFuture } }
        })
        return cloneDataSource(nextState)
      },
      false,
    )
  }

  const importDataSourceFile = async (idx: number, file: File | null) => {
    if (!file) return
    try {
      const text = await file.text()
      updateDataSource(idx, 'inline_data', text)
      setSuccess(`Imported ${file.name}`)
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
      setError('At least two .dat or .csv parameter source files are required to merge.')
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
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const updateDataColumnName = (sourceIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns]
    nextColumns[colIdx] = value || `col_${colIdx + 1}`
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, rows),
    }))
  }

  const addDataRow = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = [...rows, Array(columns.length).fill('')]
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const removeDataRow = (sourceIdx: number, rowIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.filter((_, idx) => idx !== rowIdx)
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const sortDataRows = (sourceIdx: number, colIdx: number, direction: DataSourceSortDirection) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const sortedRows = rows
      .map((row, originalIndex) => ({ row: [...row], originalIndex }))
      .sort((first, second) => {
        const comparison = compareDataSourceCellValues(first.row[colIdx], second.row[colIdx])
        const stableComparison = comparison === 0 ? first.originalIndex - second.originalIndex : comparison
        return direction === 'asc' ? stableComparison : -stableComparison
      })
      .map(({ row }) => row)
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, sortedRows))
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
    mutateDataSource(sourceIdx, current => ({
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
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, fixedRows),
    }))
    setSuccess(`Applied data auto-fix to ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyFillHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = fillEmptyHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Filled empty headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyDedupeHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = dedupeHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Deduplicated headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
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
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, parsed.columns, fixedRows))
    setSuccess(`Normalized row widths for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
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
        const updated = { ...action, headers: { ...(action.headers || {}) } }
        selected.forEach(candidate => {
          candidate.occurrences
            .filter(occ => occ.actionIdx === actionIdx)
            .forEach(occ => {
              const token = replacementMap.get(candidate.id) || candidate.value
              if (occ.field === 'target' && updated.target) {
                updated.target = updated.target.split(candidate.value).join(token)
              } else if (occ.field === 'body' && typeof updated.body === 'string') {
                updated.body = updated.body.split(candidate.value).join(token)
              } else if (occ.field === 'header' && occ.headerKey && updated.headers && updated.headers[occ.headerKey]) {
                updated.headers[occ.headerKey] = updated.headers[occ.headerKey].split(candidate.value).join(token)
              }
            })
        })
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
    const result = applyScriptRepairSuggestion(script, suggestion.key)
    replaceScriptWithHistory(result.script)
    recordAssistantChange(suggestion.title, result.summary)
    setSuccess(result.summary)
    setError('')
  }

  const insertAuthoringComponent = (component: AuthoringComponent) => {
    // When editing an existing script, drop the block in after the selected action; when
    // authoring a new draft, append (the default).
    const insertAfter = isEditingExisting ? activeAction : null
    const result = applyAuthoringComponentToScript(script, component, insertAfter)
    replaceScriptWithHistory(result.script)
    recordAssistantChange(`Inserted ${component.name}`, result.summary)
    const mainCount = component.actions?.length || 0
    const targeted = typeof insertAfter === 'number' && insertAfter >= 0 && insertAfter < script.actions.length && mainCount > 0
    selectAuthorAction(
      targeted
        ? insertAfter + 1
        : (result.script.actions.length > 0 ? result.script.actions.length - 1 : null),
    )
    setSuccess(result.summary)
    setError('')
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

  const deleteUserBlock = async (component: AuthoringComponent) => {
    setSavingBlock(true)
    try {
      await api.deleteAuthoringComponent(component.id)
      await reloadAuthoringComponents()
      setSuccess(`Deleted reusable block "${component.name}".`)
      setError('')
    } catch (e) {
      setError(getErrorMessage(e, 'Failed to delete reusable block'))
    } finally {
      setSavingBlock(false)
    }
  }

  const activeInspectorScope = useMemo(() => {
    const focus = authorCodeFocus && !authorCodeFocus.stepPath?.length
      ? authorCodeFocus
      : activeAction !== null
        ? { phase: 'action' as const, actionIndex: activeAction }
        : null
    if (!focus) return null
    const action = phaseActionsForScript(script, focus.phase)[focus.actionIndex] || null
    return action ? { phase: focus.phase, actionIndex: focus.actionIndex, action } : null
  }, [activeAction, authorCodeFocus, script])
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
  const replayEvidenceMatchesCurrentDraft = replayEditableActionsKey !== null
    && currentEditableActionsKey !== null
    && replayEditableActionsKey === currentEditableActionsKey
  const validationEvidenceMatchesCurrentDraft = validationEditableActionsKey !== null
    && currentEditableActionsKey !== null
    && validationEditableActionsKey === currentEditableActionsKey
  const debugEvidenceStatus = replayReport?.completed_at
    ? (!replayEvidenceMatchesCurrentDraft ? 'stale' : replayReport.success ? 'passed' : 'failed')
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
  const debugWorkspaceStatusLabel = replaying
    ? (isReplayPaused ? 'Paused' : 'Running')
    : replayReport?.completed_at
      ? (!replayEvidenceMatchesCurrentDraft ? 'Stale' : replayReport.success ? 'Passed' : 'Failed')
      : activeReplaySessionId || browserReplaySession
        ? 'Active'
        : ''
  const problemCount = errorCount + warningCount + warnings.length + (error ? 1 : 0) + (jsonParseMessage ? 1 : 0)
  const actionProblemRows = scriptPhaseActionRows.flatMap((row) =>
    (actionIssueBuckets[row.phase]?.[row.actionIndex] || []).map(issue => ({ issue, row })),
  )
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
      label: 'Approve / Save',
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
    setBreakOnFailure,
  })
  topbarActionHandlersRef.current = {
    activateWorkspaceTab,
    handlePauseResumeReplay,
    handleReplay,
    handleStepReplay,
    handleStopReplay,
    setBreakOnFailure,
  }
  const scriptTopbarContribution = useMemo<TopbarContribution>(() => ({
    id: 'script-editor',
    priority: 80,
    title: scriptId ? 'Edit Script' : 'Create Script',
    meta: [
      {
        id: 'problems',
        label: `${problemCount} issue${problemCount === 1 ? '' : 's'}`,
        tone: problemCount > 0 ? 'danger' : 'success',
        hidden: problemCount === 0 && !validationEvidence,
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
      {
        id: 'run-debug',
        label: replaying ? 'Running...' : replayReport ? 'Restart' : 'Run',
        compactLabel: 'Run',
        icon: <DebugTopbarIcon kind="run" />,
        className: 'topbar-debug-action topbar-debug-action-run',
        title: replayRunButtonTitle,
        tone: 'success',
        disabled: replaying || saving || replayBlockedByMissingSecrets || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0,
        onClick: () => { void topbarActionHandlersRef.current.handleReplay() },
      },
      {
        id: 'pause-debug',
        label: isReplayPaused ? 'Continue' : 'Pause',
        compactLabel: isReplayPaused ? 'Go' : 'Pause',
        icon: <DebugTopbarIcon kind="pause" />,
        className: 'topbar-debug-action topbar-debug-action-pause',
        hidden: workspaceTab !== 'debug',
        title: isReplayPaused ? 'Continue debugger' : 'Pause debugger',
        tone: 'secondary',
        disabled: !activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped',
        onClick: () => { void topbarActionHandlersRef.current.handlePauseResumeReplay() },
      },
      {
        id: 'step-debug',
        label: 'Step',
        compactLabel: 'Step',
        icon: <DebugTopbarIcon kind="step" />,
        className: 'topbar-debug-action topbar-debug-action-step',
        hidden: workspaceTab !== 'debug',
        title: 'Step to next action',
        tone: 'secondary',
        disabled: stepReplayDisabled,
        onClick: () => { void topbarActionHandlersRef.current.handleStepReplay('into') },
      },
      {
        id: 'step-over-debug',
        label: 'Step Over',
        compactLabel: 'Over',
        icon: <DebugTopbarIcon kind="stepOver" />,
        className: 'topbar-debug-action topbar-debug-action-step-over',
        hidden: workspaceTab !== 'debug',
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
        hidden: workspaceTab !== 'debug',
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
        hidden: workspaceTab !== 'debug',
        title: 'Stop debugger',
        tone: 'danger',
        disabled: !activeReplaySessionId || !replaying,
        onClick: () => { void topbarActionHandlersRef.current.handleStopReplay() },
      },
      {
        id: 'break-on-failure',
        label: 'Break on failure',
        compactLabel: 'BoF',
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
    ],
  }), [activeReplaySessionId, browserReplaySession, currentDraftScript.actions, currentDraftScript.name, debugWorkspaceStatusLabel, isReplayPaused, problemCount, replayBlockedByMissingSecrets, replayOptions.breakOnFailure, replayReport, replayRunButtonTitle, replaying, saving, scriptId, stepReplayDisabled, validationEvidence, workspaceTab])
  useTopbarContribution(scriptTopbarContribution)

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
  const renderActionInspectorContent = (): ReactNode => (
    activeInspectorScope ? (
      <>
        <ActionEditor
          action={activeInspectorScope.action}
          index={activeInspectorScope.actionIndex}
          parameters={script.parameters || []}
          onUpdate={(idx, field, value) => updatePhaseAction(activeInspectorScope.phase, idx, field, value)}
          onAddExtractor={(idx) => addPhaseExtractor(activeInspectorScope.phase, idx)}
          onUpdateExtractor={(actionIdx, extIdx, field, value) => updatePhaseExtractor(activeInspectorScope.phase, actionIdx, extIdx, field, value)}
          onRemoveExtractor={(actionIdx, extIdx) => removePhaseExtractor(activeInspectorScope.phase, actionIdx, extIdx)}
          onAddAssertion={(idx) => addPhaseAssertion(activeInspectorScope.phase, idx)}
          onUpdateAssertion={(actionIdx, assertionIdx, field, value) => updatePhaseAssertion(activeInspectorScope.phase, actionIdx, assertionIdx, field, value)}
          onRemoveAssertion={(actionIdx, assertionIdx) => removePhaseAssertion(activeInspectorScope.phase, actionIdx, assertionIdx)}
          onAddHeader={(idx) => addPhaseHeader(activeInspectorScope.phase, idx)}
          onUpdateHeader={(actionIdx, oldKey, newKey, value) => updatePhaseHeader(activeInspectorScope.phase, actionIdx, oldKey, newKey, value)}
          onRemoveHeader={(actionIdx, key) => removePhaseHeader(activeInspectorScope.phase, actionIdx, key)}
          onAddMetadata={(idx) => addPhaseMetadata(activeInspectorScope.phase, idx)}
          onUpdateMetadata={(actionIdx, oldKey, newKey, value) => updatePhaseMetadata(activeInspectorScope.phase, actionIdx, oldKey, newKey, value)}
          onRemoveMetadata={(actionIdx, key) => removePhaseMetadata(activeInspectorScope.phase, actionIdx, key)}
          onBeginParameterEdit={(draft) => openParameterizeChoice({ ...draft, phase: activeInspectorScope.phase })}
          methodColor={methodColor}
          protocol={script.protocol}
          isBrowserScript={isBrowserScript}
          isJmsScript={isJmsScript}
          isSoapScript={isSoapScript}
          issues={actionIssueBuckets[activeInspectorScope.phase]?.[activeInspectorScope.actionIndex] || []}
        />
      </>
    ) : null
  )
  const renderActionListCenterDocument = () => renderStructuredCenterDocument('Action List', (
    <section className="center-document action-list-center-document" aria-label="Action List">
      <div className="center-document-header">
        <div>
          <h3 className="panel-title">Action List</h3>
          <p>{scriptPhaseActionRows.length} executable{scriptPhaseActionRows.length === 1 ? '' : 's'} across init, action, and teardown phases.</p>
        </div>
        <button type="button" className="btn btn-sm btn-accent" onClick={() => addAction('action')}>
          + Add Action
        </button>
      </div>
      <div className="action-list-center-summary" aria-label="Lifecycle action counts">
        <span><strong>{script.init?.length || 0}</strong> init</span>
        <span><strong>{script.actions?.length || 0}</strong> actions</span>
        <span><strong>{script.teardown?.length || 0}</strong> teardown</span>
      </div>
      {scriptPhaseActionRows.length === 0 ? (
        <div className="action-list-center-empty">No actions yet.</div>
      ) : (
        <div className="action-list-center-table" role="table" aria-label="Structured action list">
          <div className="action-list-center-head" role="row">
            <span role="columnheader">Phase</span>
            <span role="columnheader">Type</span>
            <span role="columnheader">Name</span>
            <span role="columnheader">Signals</span>
            <span role="columnheader">Actions</span>
          </div>
          {scriptPhaseActionRows.map(row => {
            const { action, phase, actionIndex, phaseLabel } = row
            const actionBreakpointActive = debugBreakpointKeys.has(debugBreakpointKey(phase, actionIndex))
            const issueBucket = actionIssueBuckets[phase]?.[actionIndex] || []
            const phaseActionCount = scriptActionsForPhase(script, phase).length
            const rowActionKind = phase === 'action' ? 'action' : `${phaseLabel.toLowerCase()} action`
            const active = authorCodeFocus
              ? authorCodeFocus.phase === phase && authorCodeFocus.actionIndex === actionIndex && !authorCodeFocus.stepPath?.length
              : phase === 'action' && activeAction === actionIndex
            return (
              <div key={`center-${phase}-${actionIndex}`} className={`action-list-center-row ${active ? 'active' : ''}`} role="row">
                <div className="action-list-center-phase" role="cell">
                  <button
                    type="button"
                    className={`action-breakpoint-button ${actionBreakpointActive ? 'active' : ''}`}
                    aria-label={`${actionBreakpointActive ? 'Remove' : 'Set'} breakpoint on ${phaseLabel} ${actionIndex + 1}`}
                    title={`${actionBreakpointActive ? 'Remove' : 'Set'} breakpoint on ${phaseLabel} ${actionIndex + 1}`}
                    onClick={() => void toggleDebugBreakpoint(phase, actionIndex)}
                  >
                    <span aria-hidden="true" />
                  </button>
                  <button type="button" className="action-list-center-select" onClick={() => selectAuthorPhaseAction(row)}>
                    {phaseLabel} {actionIndex + 1}
                  </button>
                </div>
                <span role="cell" className="action-list-center-type" style={{ color: methodColor(action.type) }}>{action.type}</span>
                <div role="cell"><button type="button" className="action-list-center-name" title={actionRowLabel(action)} onClick={() => selectAuthorPhaseAction(row)}>
                  {actionRowLabel(action)}
                </button></div>
                <span role="cell" className="action-list-center-signals">
                  {issueBucket.length > 0 && (
                    <span className={`action-signal-pill ${issueBucket.some(issue => issue.severity === 'error') ? 'error' : 'warning'}`}>
                      {issueBucket.length}
                    </span>
                  )}
                  {actionRequestCount(action) > 0 && <span className="metric-badge">{actionRequestCount(action)} req</span>}
                  {actionExtractorCount(action) > 0 && <span className="metric-badge">{actionExtractorCount(action)} ext</span>}
                  {isControlAction(action) && <span className="metric-badge">ctrl</span>}
                </span>
                <span role="cell" className="action-list-center-actions">
                  <button className="btn-icon" onClick={() => movePhaseAction(phase, actionIndex, -1)} disabled={actionIndex === 0} aria-label={`Move ${rowActionKind} up`} title="Move up">↑</button>
                  <button className="btn-icon" onClick={() => movePhaseAction(phase, actionIndex, 1)} disabled={actionIndex === phaseActionCount - 1} aria-label={`Move ${rowActionKind} down`} title="Move down">↓</button>
                  <button className="btn-icon" onClick={() => duplicatePhaseAction(phase, actionIndex)} aria-label={`Duplicate ${rowActionKind}`} title="Duplicate">⧉</button>
                  <button className="btn-icon danger" onClick={() => removePhaseAction(phase, actionIndex)} aria-label={`Delete ${rowActionKind}`} title="Delete">✕</button>
                </span>
              </div>
            )
          })}
        </div>
      )}
    </section>
  ))
  const renderCenterTabPanel = (tabId: CenterWorkbenchTabId) => {
    if (tabId === BROWSER_REPLAY_TAB_ID && isBrowserScript) {
      return (
        <BrowserReplayDebugger
          sessionId={activeReplaySessionId}
          session={browserReplaySession}
          report={replayReport}
          running={replaying}
          variant="main"
          selectedEventIndex={expandedReplayEvent}
          onSelectEvent={setExpandedReplayEvent}
          onApplySuggestedLocator={applyReplaySuggestedLocator}
        />
      )
    }
    if (tabId === ACTION_LIST_TAB_ID) return renderActionListCenterDocument()
    if (tabId !== SCRIPT_JSON_TAB_ID) return null
    return (
      <ScriptJsonDocument
        scope={selectedAuthorJsonScope}
        selectedJsonText={selectedAuthorJsonText}
        showSelectedActionJson={showSelectedActionJson}
        showFullScriptJson={showFullScriptJson}
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
          parameterMatchRanges,
          resolvedValues: workspaceTab === 'debug' && hasReplayVariableInspectorData ? replayVariableInspector : null,
          ariaLabel: 'Source',
          title: 'Source',
          onApply: handleApplyJsonDraft,
          onChange: handleCodePaneChange,
          onCopy: () => void navigator.clipboard?.writeText(jsonText),
          onDownload: () => { void exportGeneratedTextFile(jsonText || formatScriptJson(script), `${sanitizeDownloadName(currentDraftScript.name, 'script')}.json`) },
          onFormat: handleFormatJsonDraft,
          onReset: handleResetJsonDraft,
          onRequestParameterize: openJsonParameterSelectionMenu,
        }}
        selectionState={jsonParameterSelection}
        onOpenParameterEditor={openJsonParameterEditor}
        onOpenExistingParameter={openJsonExistingParameterChooser}
        onOpenOccurrenceReview={openJsonParameterOccurrenceReview}
      />
    )
  }
  const renderCenterWorkbench = (className: string, ariaLabel: string, mode: WorkbenchMode) => {
    const workbenchTabs = mode === 'debug'
      ? visibleCenterTabs.filter(tab => !DEBUG_HIDDEN_CENTER_TABS.has(tab.id))
      : visibleCenterTabs
    const fallbackTabs = centerTabDescriptors.filter(tab => mode !== 'debug' || !DEBUG_HIDDEN_CENTER_TABS.has(tab.id))
    return (
      <CenterTabbedPane
        activeTabId={centerTabsState.activeTabId}
        ariaLabel={ariaLabel}
        className={className}
        onCloseTab={closeCenterTab}
        onPanelElement={setCenterTabPanelElement}
        onSelectTab={selectCenterTab}
        renderTabPanel={renderCenterTabPanel}
        tabs={workbenchTabs.length > 0 ? workbenchTabs : fallbackTabs.slice(0, 1)}
      />
    )
  }
  const authorRightSidebarContext: AuthorRightSidebarContext = (() => {
    switch (centerTabsState.activeTabId) {
      case ACTION_LIST_TAB_ID:
        if (activeInspectorScope) {
          return { kind: 'inspector', title: 'Action Details', railLabel: 'Details', wide: true }
        }
        return { kind: 'signals', title: 'Insights', railLabel: 'Insights' }
      case 'parameter-data':
        return { kind: 'parameters', title: 'Parameters', railLabel: 'Parameters' }
      case SCRIPT_JSON_TAB_ID:
        if (activeScriptJsonSidebarTab === 'auto-params') {
          return { kind: 'auto-parameterization', title: selectedOutlineParameterExists && !parameterPaneShowAuto ? 'Parameter' : 'Parameters', railLabel: 'Parameters', wide: true }
        }
        if (activeScriptJsonSidebarTab === 'details' && activeInspectorScope) {
          return { kind: 'inspector', title: 'Action Details', railLabel: 'Details', wide: true }
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
        <strong>Ready to run?</strong>
        <div className="readiness-header-actions">
          <span>{replayPreflightStatusLabel(replayPreflight)}</span>
          <button
            type="button"
            className="btn btn-sm btn-secondary"
            onClick={() => { void handleValidateScript() }}
            disabled={validateScriptDisabled}
            title={validateScriptTitle}
          >
            {validating ? 'Checking...' : 'Recheck'}
          </button>
        </div>
      </div>
      <ReplayPreflightCard
        preflight={replayPreflight}
        onConfigureSecrets={projectId ? openProjectSecrets : undefined}
        secretStatusState={replaySecretStatusState}
        onRetrySecretStatus={replaySecretStatusState === 'unavailable'
          ? () => setProjectSecretProbeRetryGeneration(generation => generation + 1)
          : undefined}
      />
      {renderReadinessValidationResult()}
      {showChecklist && (
        <div className="utility-checklist">
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
        </div>
      )}
    </div>
  )
  const renderContextualRightSidebarContent = (): ReactNode => {
    switch (authorRightSidebarContext.kind) {
      case 'auto-parameterization': {
        const showParameterDetails = selectedOutlineParameterExists && selectedOutlineParameter && !parameterPaneShowAuto
        const parameterPaneToggle = selectedOutlineParameterExists ? (
          <div className="parameter-pane-toggle" role="tablist" aria-label="Parameter view">
            <button
              type="button"
              role="tab"
              aria-selected={!parameterPaneShowAuto}
              className={!parameterPaneShowAuto ? 'active' : ''}
              onClick={() => setParameterPaneShowAuto(false)}
              title={`Settings for ${selectedOutlineParameterName}`}
            >
              Parameters
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={parameterPaneShowAuto}
              className={parameterPaneShowAuto ? 'active' : ''}
              onClick={() => setParameterPaneShowAuto(true)}
            >
              Auto Params
            </button>
          </div>
        ) : null
        if (showParameterDetails) {
          return (
            <div className="parameter-pane">
              {parameterPaneToggle}
              <SelectedParameterDetails
                parameter={selectedOutlineParameter}
                parameterIndex={selectedOutlineParameterIndex}
                referenceLines={selectedParameterReferenceLines}
                dataInfo={selectedParameterDataInfo}
                sameLineOptions={selectedParameterSameLineOptions}
                saving={saving}
                onRename={name => {
                  updateParameter(selectedOutlineParameterIndex, 'name', name)
                  setSelectedOutlineParameterName(name)
                }}
                onUpdateParameter={updateParameter}
                onSave={handleSave}
                onJumpToLine={requestJsonLineJump}
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
        return renderActionInspectorContent()
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
                  {httpConversionGenerating ? 'Generating...' : 'Generate HTTP Draft Preview'}
                </button>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => activateWorkspaceTab('debug')}>Debug</button>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => openEditorSection('parameter-data')}>Review Parameter Data</button>
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
          />
        )
      default:
        return null
    }
  }

  return (
    <div className="script-editor">
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
            <button className="btn btn-secondary btn-sm" type="button" onClick={openProjectSecrets}>Configure secrets</button>
            {replaySecretStatusState === 'unavailable' && (
              <button className="btn btn-secondary btn-sm" type="button" onClick={() => setProjectSecretProbeRetryGeneration(generation => generation + 1)}>
                Retry credential check
              </button>
            )}
          </div>
        </div>
      )}
      <ScriptImportReviewDialogs
        controller={importReviewController}
        onOpenMappedDataSource={openImportedDataSourceReview}
      />
      {serverParameterization && (
        <div className="editor-dialog-backdrop" {...dialogBackdropProps(closeServerParameterizationDialog)}>
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
        </div>
      )}
      {showSaveBlockModal && (
        <div className="editor-dialog-backdrop" {...dialogBackdropProps(closeSaveBlockDialog)}>
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
                <button type="button" className="btn btn-sm btn-accent" disabled={savingBlock || !saveBlockName.trim() || !projectId} onClick={saveSelectedAsBlock}>
                  {savingBlock ? 'Saving…' : 'Save block'}
                </button>
              </div>
            </div>
          </div>
        </div>
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
              <button className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
                {saving ? 'Saving...' : 'Save Script'}
              </button>
              {onClose && <button className="btn btn-ghost" onClick={handleCloseEditor}>Close</button>}
            </div>
          </div>
          <div className="script-ide-command-bar" aria-label="Script IDE command bar">
            <div className="script-ide-tabs" role="tablist" aria-label="Script workspace">
              <button type="button" role="tab" aria-selected={workspaceTab === 'author'} className={workspaceTab === 'author' ? 'active' : ''} onClick={() => activateWorkspaceTab('author')}>
                Author
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={workspaceTab === 'debug'}
                className={workspaceTab === 'debug' ? 'active' : ''}
                data-debug-status={debugWorkspaceStatusLabel || undefined}
                onClick={() => activateWorkspaceTab('debug')}
              >
                Debug
              </button>
            </div>
            <div className="script-ide-quick-actions" role="group" aria-label="Quick actions">
              <button type="button" className="debugger-icon-btn" title={replayRunButtonTitle} onClick={() => void handleReplay()} disabled={replaying || saving || replayBlockedByMissingSecrets || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0}>
                <span className="debugger-icon" aria-hidden="true">&#9654;</span>
                <span>{replaying ? 'Running...' : replayReport ? 'Restart' : 'Run'}</span>
              </button>
              {workspaceTab === 'debug' && (
                <>
                  <button type="button" className="debugger-icon-btn" title={isReplayPaused ? 'Continue' : 'Pause'} onClick={handlePauseResumeReplay} disabled={!activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped'}>
                    <span className="debugger-icon" aria-hidden="true">{isReplayPaused ? '▶' : '⏸'}</span>
                    <span>{isReplayPaused ? 'Continue' : 'Pause'}</span>
                  </button>
                  <button type="button" className="debugger-icon-btn" title="Step to next action" onClick={() => void handleStepReplay('into')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#10551;</span>
                    <span>Step</span>
                  </button>
                  <button type="button" className="debugger-icon-btn" title="Step over the current block (run the rest of it, pause after)" onClick={() => void handleStepReplay('over')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#10550;</span>
                    <span>Step Over</span>
                  </button>
                  <button type="button" className="debugger-icon-btn" title="Run to the end of the enclosing loop" onClick={() => void handleStepReplay('loop_end')} disabled={stepReplayDisabled}>
                    <span className="debugger-icon" aria-hidden="true">&#8635;</span>
                    <span>Finish Loop</span>
                  </button>
                  <button type="button" className="debugger-icon-btn" title="Stop debugger" onClick={handleStopReplay} disabled={!activeReplaySessionId || !replaying}>
                    <span className="debugger-icon" aria-hidden="true">&#9209;</span>
                    <span>Stop</span>
                  </button>
                  <button type="button" className="debugger-icon-btn" title="Toggle breakpoint on selected action" onClick={toggleSelectedBreakpoint} disabled={debugActionRows.length === 0}>
                    <span className="debugger-icon" aria-hidden="true">●</span>
                    <span>Breakpoint</span>
                  </button>
                  <button
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
                  </button>
                  <button
                    type="button"
                    className={`debugger-icon-btn ${!debugDetailCollapsed ? 'active' : ''}`}
                    title={debugDetailCollapsed ? 'Show detail panel (request/response + variables)' : 'Hide detail panel'}
                    aria-pressed={!debugDetailCollapsed}
                    onClick={() => setDebugDetailCollapsed(value => !value)}
                  >
                    <span className="debugger-icon" aria-hidden="true">▣</span>
                    <span>Panel</span>
                  </button>
                  {replayReport && (
                    <button type="button" className="debugger-icon-btn" title="Download Debug JSON" onClick={handleDownloadBrowserReplayExport}>
                      <span className="debugger-icon" aria-hidden="true">&#8675;</span>
                      <span>Export</span>
                    </button>
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
            actionProblemRows={actionProblemRows}
            activeReplaySessionId={activeReplaySessionId}
            addExtractorDefinition={addExtractorDefinition}
            api={api}
            applyReplaySuggestedLocator={applyReplaySuggestedLocator}
            breakpointSummary={breakpointSummary}
            browserReplaySession={browserReplaySession}
            clearDebugBreakpoints={clearDebugBreakpoints}
            controller={replayDebuggerController}
            copiedReplaySnapshotSection={copiedReplaySnapshotSection}
            debugActionRows={debugActionRows}
            debugBreakpointKeys={debugBreakpointKeys}
            debuggerWorkbench={renderCenterWorkbench('debugger-code-center', 'Debugger center workbench', 'debug')}
            error={error}
            expandedReplayBrowserArtifacts={expandedReplayBrowserArtifacts}
            expandedReplayEvent={expandedReplayEvent}
            exportGeneratedTextFile={exportGeneratedTextFile}
            handleCopyReplaySnapshotSection={handleCopyReplaySnapshotSection}
            handleValidateScript={handleValidateScript}
            isBrowserScript={isBrowserScript}
            jsonParseMessage={jsonParseMessage}
            onActivateDebug={() => activateWorkspaceTab('debug')}
            onSelectProblemAction={row => {
              selectAuthorPhaseAction(row)
              activateWorkspaceTab('author')
            }}
            onUpdateScriptName={value => updateField('name', value)}
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
            replayStatusLabel={replayStatusLabel}
            replayVariableDiffs={replayVariableDiffs}
            replayVariableInspector={replayVariableInspector}
            replayVariableTimeline={replayVariableTimeline}
            saving={saving}
            script={script}
            scriptId={scriptId}
            scriptLevelIssues={scriptLevelIssues}
            scriptRecordingStatus={scriptRecordingStatus}
            scriptRecordingStatusState={scriptRecordingStatusState}
            retryScriptRecordingStatus={retryScriptRecordingStatus}
            selectedOutlineParameterName={selectedOutlineParameterName}
            selectOutlineParameter={selectOutlineParameter}
            setActiveAction={setActiveAction}
            setBreakOnFailure={setBreakOnFailure}
            setExpandedReplayEvent={setExpandedReplayEvent}
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

          {objectDescriptorReview.rows.length > 0 && centerTabPanelElements['object-descriptors'] && createPortal(
            renderStructuredCenterDocument('Object Descriptors', (
              <ObjectDescriptorReviewDocument
                controller={importReviewController}
                onApplyCandidate={applyObjectDescriptorCandidate}
                onSelectAction={selectAuthorAction}
              />
            )), centerTabPanelElements['object-descriptors'])}

          {isBrowserScript && centerTabPanelElements['http-conversion'] && createPortal(
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
                    {httpConversionGenerating ? 'Generating...' : 'Generate HTTP Draft Preview'}
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => activateWorkspaceTab('debug')}>
                    Debug
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => openEditorSection('parameter-data')}>
                    Review Parameter Data
                  </button>
                  <button type="button" className="btn btn-secondary" onClick={() => openEditorSection('runtime')}>
                    Review Runtime Settings
                  </button>
                </div>
              </div>
              </section>
            )), centerTabPanelElements['http-conversion'])}

          {/* Runtime Settings Panels */}
          {centerTabPanelElements['runtime-settings'] && createPortal(
            renderStructuredCenterDocument('Runtime Settings', (
              <ScriptRuntimeSettingsView
                activeHTTPTransportTab={activeHTTPTransportTab}
                activeSection={activeRuntimeSettingsSection}
                canEditProtocol={canEditProtocol}
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
            )), centerTabPanelElements['runtime-settings'])}

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
              <div className={`actions-sidebar ${authorOutlineCollapsed ? 'collapsed' : ''}`} ref={authorRailRef}>
                {authorOutlineCollapsed ? (
                  <button
                    type="button"
                    className="actions-sidebar-restore"
                    title="Expand outline side panel"
                    aria-label="Expand outline side panel"
                    onClick={() => setAuthorOutlineCollapsed(false)}
                  >
                    <span aria-hidden="true">›</span>
                    <strong>Outline</strong>
                  </button>
                ) : (
                <>
                <div className="editor-outline-nav">
                  <div className="editor-outline-header editor-outline-header-actions-only">
                    <div className="editor-outline-actions">
                      <button type="button" className="btn-icon editor-outline-action-button" onClick={undoEditorChange} disabled={editorHistory.past.length === 0} title="Undo last guided change" aria-label="Undo last guided change">
                        <EditorRailActionIcon kind="undo" />
                      </button>
                      <button type="button" className="btn-icon editor-outline-action-button" onClick={redoEditorChange} disabled={editorHistory.future.length === 0} title="Redo guided change" aria-label="Redo guided change">
                        <EditorRailActionIcon kind="redo" />
                      </button>
                      <button type="button" className="btn-icon editor-outline-action-button primary" onClick={() => { void handleSave() }} disabled={saveScriptDisabled} title={saveScriptTitle} aria-label="Save script">
                        <EditorRailActionIcon kind="save" />
                      </button>
                      {onClose && (
                        <button type="button" className="btn-icon editor-outline-action-button" onClick={handleCloseEditor} title="Close script editor" aria-label="Close script editor">
                          <EditorRailActionIcon kind="close" />
                        </button>
                      )}
                      <button
                        type="button"
                        className="debugger-pane-close"
                        title="Collapse outline side panel"
                        aria-label="Collapse outline side panel"
                        onClick={() => setAuthorOutlineCollapsed(true)}
                      >
                        ‹
                      </button>
                    </div>
                  </div>
                </div>
                <div className="script-name-panel side-panel-script-name" aria-label="Script identity">
                  <label className="script-name-field">
                    <span>Script Name</span>
                    <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
                  </label>
                </div>
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
                            <button className="btn btn-sm btn-secondary" onClick={beginWrapSelectionInTransaction} title="Wrap the selected actions in a named transaction (timed; SLAs attach to it)">
                              Wrap {selectedBlockScopes.length} in transaction
                            </button>
                          )}
                          <button className="btn btn-sm btn-secondary" onClick={openSaveBlockModal} title="Save the selected actions as a reusable block">
                            Save {selectedBlockScopes.length} as block
                          </button>
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
                            <button
                              type="button"
                              className={`btn btn-sm ${txnWrapMode === 'span' ? 'btn-secondary' : 'btn-ghost'}`}
                              onClick={() => setTxnWrapMode('span')}
                              title="Timed span — lr_start/end_transaction markers measure wall-clock time across the selection (includes think time)"
                            >
                              Timed span
                            </button>
                            <button
                              type="button"
                              className={`btn btn-sm ${txnWrapMode === 'grouped' ? 'btn-secondary' : 'btn-ghost'}`}
                              onClick={() => setTxnWrapMode('grouped')}
                              title="Grouped — tag each selected action under one name (per-action response times, no span)"
                            >
                              Grouped
                            </button>
                          </span>
                          <input
                            className="txn-wrap-input"
                            value={txnWrapDraft}
                            placeholder="Transaction name"
                            aria-label="Transaction name"
                            onChange={e => setTxnWrapDraft(e.target.value)}
                            onKeyDown={e => { if (e.key === 'Enter') applyWrapSelectionInTransaction(); else if (e.key === 'Escape') setTxnWrapDraft(null) }}
                          />
                          <button className="btn btn-sm btn-accent" onClick={applyWrapSelectionInTransaction} disabled={!txnWrapDraft.trim()}>Wrap</button>
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
                      <button className="btn btn-sm btn-accent" onClick={() => addAction('action')}>+ Add</button>
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
                                <button
                                  type="button"
                                  className={`action-breakpoint-button ${leafBpActive ? 'active' : ''}`}
                                  aria-label={`${leafBpActive ? 'Remove' : 'Set'} breakpoint on ${leafLabel}`}
                                  title={`${leafBpActive ? 'Remove' : 'Set'} breakpoint on ${leafLabel}`}
                                  onClick={event => { event.stopPropagation(); leafBpHandler() }}
                                >
                                  <span aria-hidden="true" />
                                </button>
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
                          const typeLabel = item.containerType === 'http_sequence' ? 'SEQ' : item.containerType === 'control_if' ? 'IF' : 'LOOP'
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
                              <button
                                type="button"
                                className="action-cell action-name-text action-name-toggle"
                                title={item.name}
                                aria-label={`${containerActive ? 'Collapse' : 'Expand'} ${item.name}`}
                                aria-expanded={containerActive}
                                onClick={event => { event.stopPropagation(); toggleAuthorContainer(item.key) }}
                              >
                                {item.name}
                              </button>
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
                              <button
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
                              </button>
                            </div>
                            <span className="action-cell action-phase-label" title={`${phaseLabel} ${i + 1}`}>
                              {isContainerRow && (
                                <span aria-hidden="true" className="action-expand-glyph">{containerExpanded ? '▾' : '▸'}</span>
                              )}
                              {phaseLabel} {i + 1}
                            </span>
                            <span className="action-cell action-method" style={{ color: methodColor(a.type) }} title={actionTypeSummary(a)}>{txnMarker ? 'TXN' : a.type === 'http_sequence' ? 'SEQ' : a.type}</span>
                            {isContainerRow ? (
                              <button
                                type="button"
                                className="action-cell action-name-text action-name-toggle"
                                title={actionRowLabel(a)}
                                aria-label={`${containerExpanded ? 'Collapse' : 'Expand'} ${actionRowLabel(a)}`}
                                aria-expanded={containerExpanded}
                                onClick={event => { event.stopPropagation(); toggleAuthorContainer(item.key) }}
                              >
                                {actionRowLabel(a)}
                              </button>
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
                              {(reviewGated || isReviewGatedJavaScriptAction(a)) && (
                                <span
                                  className="action-signal-pill warning"
                                  title={isReviewGatedJavaScriptAction(a)
                                    ? 'JavaScript review gate — must be reviewed before this script can replay'
                                    : 'Review gate — must be reviewed before this script can replay'}
                                >
                                  Review
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
                              <button className="btn-icon" onClick={e => { e.stopPropagation(); movePhaseAction(phase, i, -1) }} disabled={i === 0} aria-label={`Move ${rowActionKind} up`} title="Move up">↑</button>
                              <button className="btn-icon" onClick={e => { e.stopPropagation(); movePhaseAction(phase, i, 1) }} disabled={i === phaseActionCount - 1} aria-label={`Move ${rowActionKind} down`} title="Move down">↓</button>
                              <button className="btn-icon" onClick={e => { e.stopPropagation(); duplicatePhaseAction(phase, i) }} aria-label={`Duplicate ${rowActionKind}`} title="Duplicate">⧉</button>
                              <button className="btn-icon danger" onClick={e => { e.stopPropagation(); removePhaseAction(phase, i) }} aria-label={`Delete ${rowActionKind}`} title="Delete">✕</button>
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
                <ScriptJsonParameterOutline
                  collapsed={outlineParameterListCollapsed}
                  deepScanning={deepScanning}
                  isEditingExisting={isEditingExisting}
                  parameters={script.parameters || []}
                  referenceCounts={parameterReferenceCounts}
                  selectedParameterName={selectedOutlineParameterName}
                  onRunServerScan={runServerParameterization}
                  onScan={scanForAutoParameters}
                  onSelectParameter={selectOutlineParameter}
                  onToggle={() => setOutlineParameterListCollapsed(collapsed => !collapsed)}
                />
                {importedParameterFileMappings.length > 0 ? (
                  <ImportReviewOutlineSection controller={importReviewController} />
                ) : (
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
                )}
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
                            const groupComponents = authoringComponents.filter(component => !isUserBlock(component) && component.kind === group.kind)
                            if (groupComponents.length === 0) return null
                            return (
                              <div key={group.kind} className="reusable-blocks-group">
                                <span className="reusable-blocks-group-label">{group.label}</span>
                                {groupComponents.map(component => (
                                  <button
                                    key={component.id}
                                    type="button"
                                    className="starter-block-card"
                                    onClick={() => insertAuthoringComponent(component)}
                                    title={component.usage_notes?.[0] || component.summary}
                                  >
                                    <strong>{component.name}</strong>
                                    <span>{component.summary}</span>
                                  </button>
                                ))}
                              </div>
                            )
                          })}
                          {authoringComponents.some(isUserBlock) && (
                            <div className="reusable-blocks-group">
                              <span className="reusable-blocks-group-label">Your Blocks</span>
                              {authoringComponents.filter(isUserBlock).map(component => (
                                <div key={component.id} className="starter-block-card user-block-card">
                                  <button
                                    type="button"
                                    className="user-block-insert"
                                    onClick={() => insertAuthoringComponent(component)}
                                    title={component.usage_notes?.[0] || component.summary}
                                  >
                                    <strong>{component.name}</strong>
                                    <span>{component.summary}</span>
                                  </button>
                                  <button
                                    type="button"
                                    className="btn-icon danger user-block-delete"
                                    aria-label={`Delete reusable block ${component.name}`}
                                    title="Delete this block"
                                    disabled={savingBlock}
                                    onClick={() => deleteUserBlock(component)}
                                  >
                                    ✕
                                  </button>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>
                    )}
                  </section>
                )}
                <div className="editor-outline-nav editor-outline-nav-bottom">
                  <div className="editor-outline-list">
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('config')}>Script Configuration</button>
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('runtime')}>Runtime Settings</button>
                    {importMetadata && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('import')}>Source Mapping</button>}
                    {importMetadata && <button className="editor-outline-link" type="button" onClick={() => setShowMetadataJsonDialog(true)}>Imported Metadata JSON</button>}
                    {objectDescriptorReview.rows.length > 0 && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('object-descriptors')}>Object Descriptors</button>}
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('parameter-data')}>Parameter Data</button>
                    {isBrowserScript && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('http-conversion')}>HTTP Conversion Review</button>}
                  </div>
                </div>
                </>
                )}
              </div>

              {renderCenterWorkbench('author-code-center', 'Author center workbench', 'author')}

              {importMetadata && centerTabPanelElements['imported-metadata'] && createPortal(
                renderStructuredCenterDocument('Source Mapping', (
                  <SourceMappingReviewDocument
                    controller={importReviewController}
                    onOpenRuntimeSettings={openImportedRuntimeSettings}
                    onSelectAction={selectAuthorAction}
                    script={script}
                  />
                )), centerTabPanelElements['imported-metadata'])}

              <div className={`editor-utility-sidebar context-${authorRightSidebarContext.kind} ${authorRightSidebarContext.wide ? 'wide' : ''} ${authorRightSidebarCollapsed ? 'collapsed' : ''}`}>
                {authorRightSidebarCollapsed ? (
                  <button
                    type="button"
                    className="utility-sidebar-restore"
                    title="Expand right sidebar"
                    aria-label="Expand right sidebar"
                    onClick={() => setAuthorRightSidebarCollapsed(false)}
                  >
                    <span aria-hidden="true">‹</span>
                    <strong>{authorRightSidebarContext.railLabel}</strong>
                  </button>
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
                  <button
                    type="button"
                    className="debugger-pane-close"
                    title="Collapse right sidebar"
                    aria-label="Collapse right sidebar"
                    onClick={() => setAuthorRightSidebarCollapsed(true)}
                  >
                    ›
                  </button>
                </div>
                {authorRightSidebarContext.kind !== 'signals' ? renderContextualRightSidebarContent() : (
                <ScriptJsonInsightsView
                  actionProblems={actionProblemRows}
                  dataSourceInspectorItems={dataSourceInspectorItems}
                  errorCount={errorCount}
                  extractorInspectorItems={extractorInspectorItems}
                  importReview={(
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
                  )}
                  importedParameterFileMappings={importedParameterFileMappings}
                  onApplyRepairSuggestion={applyRepairSuggestion}
                  onOpenParameterData={() => openCenterTab('parameter-data')}
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
                  readiness={renderReadyToRunCard({ showChecklist: true })}
                  repairSuggestions={repairSuggestions}
                  scriptLevelIssues={scriptLevelIssues}
                  warningCount={warningCount}
                />
                )}
                </>
                )}
              </div>
            </div>
          </div>
          )}

          {centerTabPanelElements['parameter-data'] && createPortal(
            renderStructuredCenterDocument('Parameter Data', (
              <ParameterDataDialog
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
              onSortDataRows={sortDataRows}
              onToggleDataEditorMode={toggleDataEditorModeAt}
              onUndoDataSource={undoDataSource}
              onUpdateDataCell={updateDataCell}
              onUpdateDataColumnName={updateDataColumnName}
              onUpdateDataSource={updateDataSource}
              onUpdateParameter={updateParameter}
              onUpdateParameterDraftName={updatePendingParameterDraftName}
              onUpdateParameterDraftValue={updatePendingParameterDraftValue}
              openDataEditor={openDataEditor}
              pendingParameterDraft={pendingParameterDraft}
              selectedParameterIdx={selectedParameterIdx}
              onSelectParameter={selectParameterData}
              script={script}
              variant="document"
            />
            )), centerTabPanelElements['parameter-data'])}

          {replayScreenshotSelection && (
            <ReplayScreenshotDialog
              alt={replayScreenshotSelection.alt}
              onClose={closeReplayScreenshot}
              src={replayScreenshotSelection.src}
            />
          )}
        </div>
    </div>
  )
}

interface ReplayScreenshotDialogProps {
  alt: string
  onClose: () => void
  src: string
}

function ReplayScreenshotDialog({ alt, onClose, src }: ReplayScreenshotDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <div className="editor-dialog-backdrop" {...dialogBackdropProps(onClose)}>
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
    </div>
  )
}

function useScriptEditorState(initialScript?: ScriptData | null) {
  const [state, dispatch] = useReducer(scriptEditorReducer, initialScript, createScriptEditorState)
  const baselineJsonRef = useRef(formatScriptJson(initialScript || { ...EMPTY_SCRIPT }))
  const replayEventRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const dispatchers = useMemo(() => createScriptEditorDispatchers(dispatch), [dispatch])
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
    toggleSection,
    closeReplayScreenshot,
  }
}
