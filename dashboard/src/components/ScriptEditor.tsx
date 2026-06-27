import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode, type WheelEvent as ReactWheelEvent } from 'react'
import { createPortal } from 'react-dom'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import type { BrowserToHTTPPreviewResponse } from '../hooks/api/useScriptApi'
import { useKeyboardShortcuts } from '../hooks/KeyboardShortcutProvider'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { useWebSocket } from '../hooks/useWebSocket'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { EditorSelection, StateEffect, type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { openSearchPanel } from '@codemirror/search'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { apexDslSemanticHighlighting } from './apexDslSemanticHighlighting'
import { buildDslAssistExtensions } from './dslEditorAssist'
import { ensureDslActionCatalog } from './dslActionCatalog'
import {
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
  BrowserReplayReport,
  ScriptDebugBreakpoint,
  ScriptDebugAction,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
  type ConditionEvidence,
  type ScriptDebugStepMode,
  BrowserReplaySnapshot,
  type AuthoringComponent,
  StoredScript,
  WSMessage,
} from '../types'
import {
  type ContentCheckConfig,
  type DFETransform,
  type ScriptActionDefinition,
  type ScriptAssertionDefinition,
  type ScriptDefinition,
  type ScriptExtractorDefinition,
  type ScriptDataSource,
  type ScriptImportMetadata,
  type SourceArchiveReview,
  type SourceArchiveReviewMapping,
  type ScriptParameterDefinition,
  type ScriptValidationResponse,
  type ReplayPreflight,
} from '../types/scripts'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import { ExtractorTester } from './ExtractorTester'
import { CorrelationDiagnosePanel } from './CorrelationDiagnosePanel'
import ParameterizationReview from './ParameterizationReview'
import type { Parameterization } from '../types/scriptImports'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import PageViewToggle from './PageViewToggle'
import ReplayPreflightCard from './ReplayPreflightCard'
import { buildLocalReplayPreflight, replayPreflightStatusLabel } from './replayPreflightUtils'
import RepairSuggestionList from './RepairSuggestionList'
import SourceMappingReview from './SourceMappingReview'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import {
  applyMigrationAssistantSuggestion,
  applyAuthoringComponentToScript,
  applyScriptRepairSuggestion,
  buildImportedDraftDiffSummary,
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
import { buildDslDocument, lineRangeForTargetPath, type LineRange } from './dslJsonPointers'
import {
  scriptCodeRangeField,
  scriptCodeParameterMatchField,
  scriptCodeCandidateMatchField,
  scriptCodeResolvedValueField,
  setScriptCodeRange,
  setScriptCodeParameterMatches,
  setScriptCodeCandidateMatches,
  setScriptCodeResolvedValues,
  type ScriptCodeMatchRange,
} from './scriptCodeDecorations'
import {
  defaultAssertionForProtocol,
  defaultExtractorForProtocol,
  sanitizeDownloadName,
} from './scriptEditorHelpers'
import {
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
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
  formatConsoleEntries,
  formatNetworkEntries,
  formatReplayHeaders,
  formatReplayTime,
  getErrorIssues,
  getErrorMessage,
  getErrorWarnings,
  getImportSummary,
  hasBrowserReplayArtifacts,
  countUniqueColumnValues,
  inferDelimiter,
  isLikelyParameterValue,
  mergeReplayVariables,
  normalizeHeaders,
  parseReplayVariablesText,
  parseInlineData,
  buildParameterizeSelectionRequest,
  sanitizeVarName,
  stringifyInlineData,
  summarizeInlineData,
  validateInlineData,
  type JsonStringOccurrence,
} from './scriptEditorUtils'
import {
  ACTION_LIST_TAB_ID,
  BROWSER_DEVICE_PROFILES,
  CENTER_TABS_STORAGE_PREFIX,
  DEBUGGER_LAYOUT_DEFAULTS,
  DEBUGGER_LAYOUT_LIMITS,
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
  clampDebuggerPaneSize,
  codePathForDebugRow,
  resolveDebugRowForEvent,
  findParameterReferencesInJson,
  formatBrowserDeviceProfileLabel,
  formatDebugPhaseLabel,
  formatProtocolEvidenceSummary,
  formatScriptJson,
  lookupBrowserDeviceProfile,
  parseScriptJsonDraft,
  readCenterTabsState,
  scriptActionsForPhase,
  type CenterTabsState,
  type CenterWorkbenchTabId,
  type DebuggerResizeKind,
  type ScriptActionPhase,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'
import { isRecord, httpSequenceRequests, rawHttpSequenceRequest, actionRequestCount, actionExtractorCount, actionAssertionCount, isControlAction, isReviewGatedJavaScriptAction, actionRequiresReview, actionSelectorFallbackCount, actionDisplayType, actionDisplayTarget, actionTypeSummary, actionRowLabel } from './scriptActionHelpers'
import { ActionEditor, ActionParameterMenu, ValidationEvidencePanel } from './ScriptEditorActionEditor'
import { CUSTOM_PARAMETER_FORMAT_VALUE, parameterFormatForType, parameterFormatPresetsForType, isBuiltinParameterType, applyParameterTypeDefaults } from './scriptParameterFormat'
import './ScriptEditor.css'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition
type ScriptData = ScriptDefinition

interface AuthorCodeFocus {
  phase: ScriptActionPhase
  actionIndex: number
  stepPath?: string[]
  key?: string
  label?: string
}

interface AuthorJsonScope {
  action: Action
  actionIndex: number
  key: string
  label: string
  path: string
  phase: ScriptActionPhase
  subAction: boolean
}
const SCRIPT_PROTOCOL_OPTIONS = [
  { value: 'http', label: 'HTTP API' },
  { value: 'https', label: 'HTTPS API' },
  { value: 'java_http', label: 'Java over HTTP' },
  { value: 'soap', label: 'Web Services (SOAP)' },
  { value: 'websocket', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'database', label: 'Database' },
  { value: 'network', label: 'Network' },
  { value: 'browser', label: 'Web/HTML Browser' },
  { value: 'jms-solace', label: 'JMS Solace' },
  { value: 'jms-ibmmq', label: 'JMS IBM MQ' },
  { value: 'kafka', label: 'Kafka' },
] as const
const RUNTIME_SETTINGS_SECTION_IDS = [
  'script_config',
  'think',
  'pacing',
  'runlogic',
  'errors',
  'jms',
  'websocket',
  'grpc',
  'database',
  'network',
  'http',
  'soap',
  'java_http',
  'web_runtime',
  'log',
] as const
type RuntimeSettingsSectionId = typeof RUNTIME_SETTINGS_SECTION_IDS[number]
const HTTP_TRANSPORT_TAB_OPTIONS = [
  { id: 'timeouts', label: 'Timeouts' },
  { id: 'connection', label: 'Connection' },
  { id: 'tls', label: 'TLS / SSL' },
  { id: 'proxy', label: 'Proxy' },
  { id: 'authentication', label: 'Authentication' },
  { id: 'browser', label: 'Browser Emulation' },
] as const
type HTTPTransportTabId = typeof HTTP_TRANSPORT_TAB_OPTIONS[number]['id']
const SECONDS_INPUT_STEP = '0.001'

function formatMillisecondsAsSecondsValue(valueMs: number | null | undefined, fallbackMs = 0, emptyWhenUndefined = false): string {
  if ((valueMs === undefined || valueMs === null) && emptyWhenUndefined) return ''
  const safeMs = valueMs ?? fallbackMs
  if (!Number.isFinite(safeMs)) return '0'
  const roundedSeconds = Math.round((safeMs / 1000) * 1000) / 1000
  return roundedSeconds.toFixed(3).replace(/\.?0+$/, '')
}

function formatMillisecondsAsSecondsLabel(valueMs: number | null | undefined, fallbackMs = 0): string {
  return `${formatMillisecondsAsSecondsValue(valueMs, fallbackMs)}s`
}

function parseSecondsInputAsMilliseconds(value: string, minSeconds = 0): number | null | undefined {
  if (value.trim() === '') return undefined
  const seconds = Number(value)
  if (!Number.isFinite(seconds)) return null
  return Math.round(Math.max(minSeconds, seconds) * 1000)
}

interface SecondsFromMillisecondsInputProps {
  valueMs?: number | null
  fallbackMs?: number
  emptyWhenUndefined?: boolean
  min?: number
  placeholder?: string
  ariaLabel?: string
  onChangeMs: (valueMs: number | undefined) => void
}

function SecondsFromMillisecondsInput({
  valueMs,
  fallbackMs = 0,
  emptyWhenUndefined = false,
  min = 0,
  placeholder,
  ariaLabel,
  onChangeMs,
}: SecondsFromMillisecondsInputProps) {
  const formattedValue = formatMillisecondsAsSecondsValue(valueMs, fallbackMs, emptyWhenUndefined)
  const [draftValue, setDraftValue] = useState(formattedValue)
  const [focused, setFocused] = useState(false)

  useEffect(() => {
    if (!focused) setDraftValue(formattedValue)
  }, [focused, formattedValue])

  return (
    <input
      type="number"
      min={min}
      step={SECONDS_INPUT_STEP}
      value={focused ? draftValue : formattedValue}
      placeholder={placeholder}
      aria-label={ariaLabel}
      onFocus={() => {
        setFocused(true)
        setDraftValue(formattedValue)
      }}
      onBlur={() => {
        setFocused(false)
        setDraftValue(formatMillisecondsAsSecondsValue(valueMs, fallbackMs, emptyWhenUndefined))
      }}
      onChange={event => {
        const nextValue = event.target.value
        setDraftValue(nextValue)
        const nextMilliseconds = parseSecondsInputAsMilliseconds(nextValue, min)
        if (nextMilliseconds !== null) onChangeMs(nextMilliseconds)
      }}
    />
  )
}

function RuntimeSectionIcon({ section }: { section: RuntimeSettingsSectionId }) {
  const iconProps = {
    className: 'runtime-section-icon-svg',
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.8,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    focusable: 'false',
  } as const

  const icon = (() => {
    switch (section) {
      case 'script_config':
        return (
          <>
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <path d="M8 9h8" />
            <path d="M8 13h5" />
            <path d="M8 17h7" />
          </>
        )
      case 'think':
        return (
          <>
            <path d="M6 3h12" />
            <path d="M6 21h12" />
            <path d="M8 3v4.5a4 4 0 0 0 1.4 3l2.6 2.1 2.6-2.1a4 4 0 0 0 1.4-3V3" />
            <path d="M8 21v-4.5a4 4 0 0 1 1.4-3l2.6-2.1 2.6 2.1a4 4 0 0 1 1.4 3V21" />
          </>
        )
      case 'pacing':
        return (
          <>
            <path d="M9 3h6" />
            <circle cx="12" cy="13" r="7" />
            <path d="M12 13l3-3" />
            <path d="M12 6v2" />
          </>
        )
      case 'runlogic':
        return (
          <>
            <path d="M6 7h8a4 4 0 0 1 4 4v1" />
            <path d="m15 4 3 3-3 3" />
            <path d="M18 17h-8a4 4 0 0 1-4-4v-1" />
            <path d="m9 20-3-3 3-3" />
          </>
        )
      case 'errors':
        return (
          <>
            <path d="M12 3 2.5 20h19L12 3z" />
            <path d="M12 9v5" />
            <path d="M12 17h.01" />
          </>
        )
      case 'jms':
        return (
          <>
            <path d="M5 7h10a3 3 0 0 1 3 3v2a3 3 0 0 1-3 3H9l-4 4v-4a3 3 0 0 1-3-3v-2a3 3 0 0 1 3-3z" />
            <path d="M8 10h7" />
            <path d="M8 13h4" />
          </>
        )
      case 'websocket':
        return (
          <>
            <path d="M8 7V5a3 3 0 0 1 3-3h2a3 3 0 0 1 3 3v2" />
            <path d="M8 17v2a3 3 0 0 0 3 3h2a3 3 0 0 0 3-3v-2" />
            <path d="M5 10h14" />
            <path d="M5 14h14" />
            <path d="m17 10 2 2-2 2" />
            <path d="m7 10-2 2 2 2" />
          </>
        )
      case 'grpc':
        return (
          <>
            <circle cx="6" cy="12" r="2.5" />
            <circle cx="18" cy="6" r="2.5" />
            <circle cx="18" cy="18" r="2.5" />
            <path d="M8.4 11.1 15.8 7" />
            <path d="M8.4 12.9 15.8 17" />
          </>
        )
      case 'database':
        return (
          <>
            <ellipse cx="12" cy="5" rx="7" ry="3" />
            <path d="M5 5v10c0 1.7 3.1 3 7 3s7-1.3 7-3V5" />
            <path d="M5 10c0 1.7 3.1 3 7 3s7-1.3 7-3" />
          </>
        )
      case 'network':
        return (
          <>
            <circle cx="12" cy="12" r="2.4" />
            <circle cx="5" cy="6" r="2" />
            <circle cx="19" cy="6" r="2" />
            <circle cx="19" cy="18" r="2" />
            <path d="M7 7.2 10.2 10" />
            <path d="m13.8 10 3.2-2.8" />
            <path d="m13.8 14 3.4 2.8" />
          </>
        )
      case 'http':
        return (
          <>
            <circle cx="12" cy="12" r="9" />
            <path d="M3 12h18" />
            <path d="M12 3a13 13 0 0 1 0 18" />
            <path d="M12 3a13 13 0 0 0 0 18" />
          </>
        )
      case 'soap':
        return (
          <>
            <circle cx="9" cy="15" r="4" />
            <circle cx="15.5" cy="9" r="3.5" />
            <circle cx="17.5" cy="17.5" r="1.8" />
            <path d="M6.5 5.5h.01" />
          </>
        )
      case 'java_http':
        return (
          <>
            <path d="M8 8h8v6a4 4 0 0 1-4 4h0a4 4 0 0 1-4-4V8z" />
            <path d="M16 10h1.5a2.5 2.5 0 0 1 0 5H16" />
            <path d="M9 3c1 1 1 2 0 3" />
            <path d="M13 3c1 1 1 2 0 3" />
            <path d="M7 21h10" />
          </>
        )
      case 'web_runtime':
        return (
          <>
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M3 9h18" />
            <path d="M8 13h8" />
            <path d="M8 17h4" />
            <path d="M17 12v6" />
          </>
        )
      case 'log':
        return (
          <>
            <path d="M9 4h6l1 2h3v15H5V6h3l1-2z" />
            <path d="M9 11h6" />
            <path d="M9 15h6" />
            <path d="M9 19h4" />
          </>
        )
    }
  })()

  return (
    <span className="section-icon runtime-section-icon" aria-hidden="true">
      <svg {...iconProps} data-runtime-section-icon={section}>
        {icon}
      </svg>
    </span>
  )
}
type ImportedParameterFileMapping = {
  section: {
    file: string
    name: string
    values: Record<string, string>
  }
  sourceName: string | null
}
type ValidationEvidence = Pick<ScriptValidationResponse, 'data_source_fit' | 'ai_guardrails' | 'authoring_health'>
type ValidationSidebarResult = {
  tone: 'success' | 'warning' | 'error'
  message: string
  warnings: string[]
  issues: string[]
}

type ReplaySnapshot = BrowserReplaySnapshot
type ReplayActionEvent = BrowserReplayEvent
type ReplayReport = BrowserReplayReport
type WorkbenchMode = 'author' | 'debug'
type DebuggerTab = 'request' | 'browser' | 'variables'
type DebuggerBottomTab = 'console' | 'timeline' | 'problems' | 'output'
type ScriptJsonSidebarTab = 'insights' | 'auto-params' | 'details'

const SCRIPT_JSON_DEFAULT_COMPANION_TAB: ScriptJsonSidebarTab = 'insights'

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
  | 'source-map'
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


interface ScriptCodeLineJumpRequest {
  line: number
  nonce: number
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





const WEBKIT_INSTALL_ADVISORY = 'WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.'
const DEBUG_LOG_LEVELS: ScriptDebugLogLevel[] = ['error', 'warn', 'info', 'debug', 'trace']











interface JsonPaneSelectionRequest {
  start: number
  end: number
  selectedText: string
  x: number
  y: number
}

interface ScriptCodeLineLayout {
  top: number
  height: number
}

interface ScriptCodeScrollSnapshot {
  scrollTop: number
  scrollHeight: number
  clientHeight: number
  lineCount: number
  topLine: number
  bottomLine: number
  selectedStartLine: number
  selectedEndLine: number
  lineLayouts: ScriptCodeLineLayout[]
  baseLineHeight: number
}

interface ScriptCodeScrollController {
  refresh: () => void
  scrollToScrollTop: (scrollTop: number) => void
  scrollToLine: (line: number) => void
  scrollToRatio: (ratio: number) => void
}

const JSON_SOURCE_MAP_LINE_HEIGHT_PX = 6.5

/**
 * Above this line count we stop measuring per-line layout for the source-map snapshot.
 * Measuring every line (view.lineBlockAt per line) is a forced synchronous layout pass
 * that, on a tens-of-thousands-of-line imported script, blocks the main thread and — since
 * the snapshot is stored in component state — can re-fire on every render. Consumers fall
 * back to baseLineHeight when lineLayouts is empty.
 */
const SCRIPT_CODE_LINE_LAYOUT_MAX_LINES = 4000

/**
 * Above this document size the inline review-baseline/current-draft JSON diff preview is replaced
 * by a "too large to preview" placeholder (serializing multi-MB JSON inline would jank the panel).
 * The dormant source-map minimap is NOT gated by this — when re-enabled it windows its rows
 * (see virtualizeSourceMap) and renders at any size.
 */
const JSON_SOURCE_MAP_MAX_DOC_CHARS = 250_000

/**
 * Above this document size, line wrapping is disabled in the Script JSON editor. With
 * EditorView.lineWrapping CodeMirror must measure the wrapped height of every line to size
 * the document, which renders all ~100k lines of a large import (millions of px tall) and
 * defeats viewport virtualization. Without wrapping, lines have a uniform height and only the
 * visible viewport is rendered. The trade-off (horizontal scroll on long lines) is acceptable
 * for very large generated JSON that is not hand-edited line by line.
 */
const SCRIPT_CODE_LINE_WRAP_MAX_CHARS = 250_000

/**
 * Only virtualize long lists. Below this count the (cheap) plain render is used, which keeps
 * behavior identical for ordinary scripts and test fixtures; large imports (thousands of
 * actions/mappings) switch to windowed rendering so only visible rows hit the DOM.
 */
const LIST_VIRTUALIZE_THRESHOLD = 100

const SCRIPT_CODE_SCROLL_EMPTY: ScriptCodeScrollSnapshot = {
  scrollTop: 0,
  scrollHeight: 0,
  clientHeight: 0,
  lineCount: 1,
  topLine: 1,
  bottomLine: 1,
  selectedStartLine: 1,
  selectedEndLine: 1,
  lineLayouts: [],
  baseLineHeight: JSON_SOURCE_MAP_LINE_HEIGHT_PX,
}

function clampNumber(value: number, min: number, max: number) {
  if (!Number.isFinite(value)) return min
  return Math.max(min, Math.min(max, value))
}

function approximateLinePosition(scrollTop: number, scrollHeight: number, lineCount: number) {
  const lineHeight = lineCount > 0 ? Math.max(1, scrollHeight / lineCount) : scrollHeight
  return clampNumber(scrollTop / lineHeight + 1, 1, Math.max(1, lineCount))
}

function readLinePositionAtScrollTop(view: EditorView, scrollTop: number, scrollHeight: number, lineCount: number) {
  try {
    const block = view.lineBlockAtHeight(scrollTop)
    const line = view.state.doc.lineAt(block.from)
    const blockHeight = Math.max(1, block.bottom - block.top)
    const fraction = clampNumber((scrollTop - block.top) / blockHeight, 0, 1)
    return clampNumber(line.number + fraction, 1, lineCount)
  } catch {
    return approximateLinePosition(scrollTop, scrollHeight, lineCount)
  }
}

function readScriptCodeLineLayouts(view: EditorView, scrollHeight: number, lineCount: number) {
  if (lineCount > SCRIPT_CODE_LINE_LAYOUT_MAX_LINES) {
    const base = view.defaultLineHeight || Math.max(1, scrollHeight / Math.max(1, lineCount))
    return { lineLayouts: [] as ScriptCodeLineLayout[], baseLineHeight: Math.max(1, base) }
  }
  try {
    const lineLayouts: ScriptCodeLineLayout[] = []
    let minHeight = Number.POSITIVE_INFINITY
    for (let lineNo = 1; lineNo <= lineCount; lineNo += 1) {
      const line = view.state.doc.line(lineNo)
      const block = view.lineBlockAt(line.from)
      const height = Math.max(1, block.bottom - block.top)
      lineLayouts.push({ top: Math.max(0, block.top), height })
      minHeight = Math.min(minHeight, height)
    }
    return {
      lineLayouts,
      baseLineHeight: Number.isFinite(minHeight) ? minHeight : JSON_SOURCE_MAP_LINE_HEIGHT_PX,
    }
  } catch {
    const fallbackHeight = Math.max(1, scrollHeight / Math.max(1, lineCount))
    return {
      lineLayouts: Array.from({ length: lineCount }, (_, index) => ({ top: index * fallbackHeight, height: fallbackHeight })),
      baseLineHeight: fallbackHeight,
    }
  }
}

function readScriptCodeSelectionLines(view: EditorView, lineCount: number) {
  const selection = view.state.selection.main
  const from = clampNumber(selection.from, 0, view.state.doc.length)
  const inclusiveTo = selection.to === selection.from
    ? from
    : clampNumber(selection.to - 1, 0, view.state.doc.length)
  const startLine = view.state.doc.lineAt(Math.min(from, inclusiveTo)).number
  const endLine = view.state.doc.lineAt(Math.max(from, inclusiveTo)).number
  return {
    selectedStartLine: Math.max(1, Math.min(lineCount, startLine)),
    selectedEndLine: Math.max(1, Math.min(lineCount, endLine)),
  }
}

function readScriptCodeScrollSnapshot(view: EditorView | null): ScriptCodeScrollSnapshot {
  const scrollDOM = view?.scrollDOM
  if (!scrollDOM) return SCRIPT_CODE_SCROLL_EMPTY
  const lineCount = Math.max(1, view?.state.doc.lines || 1)
  const clientHeight = Math.max(0, scrollDOM.clientHeight || 0)
  const scrollHeight = Math.max(clientHeight, scrollDOM.scrollHeight || 0)
  const maxScrollTop = Math.max(0, scrollHeight - clientHeight)
  const scrollTop = clampNumber(scrollDOM.scrollTop || 0, 0, maxScrollTop)
  const { lineLayouts, baseLineHeight } = readScriptCodeLineLayouts(view, scrollHeight, lineCount)
  const topLine = view
    ? readLinePositionAtScrollTop(view, scrollTop, scrollHeight, lineCount)
    : approximateLinePosition(scrollTop, scrollHeight, lineCount)
  const bottomLine = view
    ? readLinePositionAtScrollTop(view, scrollTop + clientHeight, scrollHeight, lineCount)
    : approximateLinePosition(scrollTop + clientHeight, scrollHeight, lineCount)
  const selectionLines = readScriptCodeSelectionLines(view, lineCount)
  return { scrollTop, scrollHeight, clientHeight, lineCount, topLine, bottomLine, ...selectionLines, lineLayouts, baseLineHeight }
}

function sameScriptCodeLineLayouts(a: ScriptCodeLineLayout[], b: ScriptCodeLineLayout[]) {
  if (a.length !== b.length) return false
  for (let index = 0; index < a.length; index += 1) {
    if (a[index].top !== b[index].top || a[index].height !== b[index].height) {
      return false
    }
  }
  return true
}

function sameScriptCodeScrollSnapshot(a: ScriptCodeScrollSnapshot, b: ScriptCodeScrollSnapshot) {
  return (
    a.scrollTop === b.scrollTop &&
    a.scrollHeight === b.scrollHeight &&
    a.clientHeight === b.clientHeight &&
    a.lineCount === b.lineCount &&
    a.topLine === b.topLine &&
    a.bottomLine === b.bottomLine &&
    a.selectedStartLine === b.selectedStartLine &&
    a.selectedEndLine === b.selectedEndLine &&
    a.baseLineHeight === b.baseLineHeight &&
    sameScriptCodeLineLayouts(a.lineLayouts, b.lineLayouts)
  )
}

interface ScriptCodePaneProps {
  value: string
  status: 'synced' | 'dirty' | 'invalid'
  statusMessage: string
  activePath: string
  activeRange: LineRange | null
  activeParameterName?: string | null
  activeCandidateName?: string | null
  actionCount: number
  assistExtensions?: Extension[]
  className?: string
  title?: string
  ariaLabel?: string
  subtitle?: string
  allowDirectEdit?: boolean
  jumpToLineRequest?: ScriptCodeLineJumpRequest | null
  candidateMatchRanges?: ScriptCodeMatchRange[]
  parameterMatchRanges?: ScriptCodeMatchRange[]
  /** Resolved runtime values to render inline within the active range (debug mode). */
  resolvedValues?: Record<string, string> | null
  onApply: () => void
  onChange: (value: string) => void
  onCopy: () => void
  onDownload: () => void
  onFormat: () => void
  onReset: () => void
  onRequestParameterize?: (selection: JsonPaneSelectionRequest) => void
  onScrollControllerChange?: (controller: ScriptCodeScrollController | null) => void
  onScrollSnapshotChange?: (snapshot: ScriptCodeScrollSnapshot) => void
}

function ScriptCodePane({
  value,
  status,
  statusMessage,
  activePath,
  activeRange,
  activeParameterName,
  activeCandidateName,
  actionCount,
  assistExtensions,
  className = '',
  title = 'Script JSON',
  ariaLabel = title,
  subtitle,
  allowDirectEdit = true,
  jumpToLineRequest,
  candidateMatchRanges = [],
  parameterMatchRanges = [],
  resolvedValues = null,
  onApply,
  onChange,
  onCopy,
  onDownload,
  onFormat,
  onReset,
  onRequestParameterize,
  onScrollControllerChange,
  onScrollSnapshotChange,
}: ScriptCodePaneProps) {
  const viewRef = useRef<EditorView | null>(null)
  const lastScrollSnapshotRef = useRef<ScriptCodeScrollSnapshot>(SCRIPT_CODE_SCROLL_EMPTY)
  // The action list / forms are the primary edit surface; the raw JSON is a
  // read-only projection of structured state by default. Editing it directly is
  // an explicit opt-in (the escape hatch for power users / fields the forms
  // can't express). While read-only it can never go dirty, so it stays a clean
  // one-way mirror — no bidirectional sync to reconcile.
  const [jsonEditEnabled, setJsonEditEnabled] = useState(false)
  const [editorReadyVersion, setEditorReadyVersion] = useState(0)
  const activeStartLine = activeRange?.startLine ?? null
  const activeEndLine = activeRange?.endLine ?? null
  const parameterMatchKey = useMemo(
    () => parameterMatchRanges.map(range => `${range.from}:${range.to}`).join('|'),
    [parameterMatchRanges],
  )
  const candidateMatchKey = useMemo(
    () => candidateMatchRanges.map(range => `${range.from}:${range.to}`).join('|'),
    [candidateMatchRanges],
  )
  const parameterStatus = activeParameterName
    ? `Parameter: ${activeParameterName} (${parameterMatchRanges.length} occurrence${parameterMatchRanges.length === 1 ? '' : 's'})`
    : ''
  const candidateStatus = activeCandidateName
    ? `Auto parameter: ${activeCandidateName} (${candidateMatchRanges.length} highlighted)`
    : ''

  useEffect(() => {
    if (!allowDirectEdit && jsonEditEnabled) setJsonEditEnabled(false)
  }, [allowDirectEdit, jsonEditEnabled])

  const emitScrollSnapshot = useCallback(() => {
    const snapshot = readScriptCodeScrollSnapshot(viewRef.current)
    if (sameScriptCodeScrollSnapshot(snapshot, lastScrollSnapshotRef.current)) return
    lastScrollSnapshotRef.current = snapshot
    onScrollSnapshotChange?.(snapshot)
  }, [onScrollSnapshotChange])
  const wrapLines = value.length <= SCRIPT_CODE_LINE_WRAP_MAX_CHARS
  const extensions = useMemo(() => [
    json(),
    apexCodeMirrorTheme,
    apexDslSemanticHighlighting(),
    scriptCodeRangeField,
    scriptCodeParameterMatchField,
    scriptCodeCandidateMatchField,
    scriptCodeResolvedValueField,
    ...(wrapLines ? [EditorView.lineWrapping] : []),
    EditorView.updateListener.of(update => {
      if (update.selectionSet || update.docChanged || update.viewportChanged || update.geometryChanged) {
        emitScrollSnapshot()
      }
    }),
    ...(assistExtensions ?? []),
  ], [assistExtensions, emitScrollSnapshot, wrapLines])

  const scrollToScrollTop = useCallback((scrollTop: number) => {
    const scrollDOM = viewRef.current?.scrollDOM
    if (!scrollDOM) return
    const maxScrollTop = Math.max(0, (scrollDOM.scrollHeight || 0) - (scrollDOM.clientHeight || 0))
    scrollDOM.scrollTop = clampNumber(scrollTop, 0, maxScrollTop)
    scrollDOM.dispatchEvent(new Event('scroll', { bubbles: true }))
    emitScrollSnapshot()
  }, [emitScrollSnapshot])

  const scrollToRatio = useCallback((ratio: number) => {
    const scrollDOM = viewRef.current?.scrollDOM
    if (!scrollDOM) return
    const maxScrollTop = Math.max(0, (scrollDOM.scrollHeight || 0) - (scrollDOM.clientHeight || 0))
    scrollToScrollTop(maxScrollTop * clampNumber(ratio, 0, 1))
  }, [scrollToScrollTop])

  const scrollToLine = useCallback((line: number) => {
    const view = viewRef.current
    const scrollDOM = view?.scrollDOM
    if (!view || !scrollDOM) return
    const lineCount = Math.max(1, view.state.doc.lines)
    const linePosition = clampNumber(line, 1, lineCount)
    const lineNo = Math.max(1, Math.min(lineCount, Math.floor(linePosition)))
    const maxScrollTop = Math.max(0, (scrollDOM.scrollHeight || 0) - (scrollDOM.clientHeight || 0))
    let targetScrollTop = maxScrollTop * ((linePosition - 1) / Math.max(1, lineCount - 1))
    try {
      const currentLine = view.state.doc.line(lineNo)
      const currentBlock = view.lineBlockAt(currentLine.from)
      if (lineNo < lineCount) {
        const nextLine = view.state.doc.line(lineNo + 1)
        const nextBlock = view.lineBlockAt(nextLine.from)
        targetScrollTop = currentBlock.top + (nextBlock.top - currentBlock.top) * (linePosition - lineNo)
      } else {
        targetScrollTop = currentBlock.top
      }
    } catch {
      targetScrollTop = maxScrollTop * ((linePosition - 1) / Math.max(1, lineCount - 1))
    }
    scrollDOM.scrollTop = clampNumber(targetScrollTop, 0, maxScrollTop)
    scrollDOM.dispatchEvent(new Event('scroll', { bubbles: true }))
    emitScrollSnapshot()
  }, [emitScrollSnapshot])

  useEffect(() => {
    if (!onScrollControllerChange || !viewRef.current) return undefined
    const controller: ScriptCodeScrollController = {
      refresh: emitScrollSnapshot,
      scrollToScrollTop,
      scrollToLine,
      scrollToRatio,
    }
    onScrollControllerChange(controller)
    return () => onScrollControllerChange(null)
  }, [editorReadyVersion, emitScrollSnapshot, onScrollControllerChange, scrollToLine, scrollToRatio, scrollToScrollTop])

  useEffect(() => {
    const scrollDOM = viewRef.current?.scrollDOM
    if (!scrollDOM) return undefined
    const handleScroll = () => emitScrollSnapshot()
    const handleSelectionChange = () => emitScrollSnapshot()
    scrollDOM.addEventListener('scroll', handleScroll, { passive: true })
    scrollDOM.addEventListener('keyup', handleSelectionChange)
    scrollDOM.addEventListener('mouseup', handleSelectionChange)
    scrollDOM.addEventListener('select', handleSelectionChange)
    scrollDOM.addEventListener('input', handleSelectionChange)
    emitScrollSnapshot()

    let resizeObserver: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      resizeObserver = new ResizeObserver(() => emitScrollSnapshot())
      resizeObserver.observe(scrollDOM)
    }
    window.addEventListener('resize', emitScrollSnapshot)

    return () => {
      scrollDOM.removeEventListener('scroll', handleScroll)
      scrollDOM.removeEventListener('keyup', handleSelectionChange)
      scrollDOM.removeEventListener('mouseup', handleSelectionChange)
      scrollDOM.removeEventListener('select', handleSelectionChange)
      scrollDOM.removeEventListener('input', handleSelectionChange)
      resizeObserver?.disconnect()
      window.removeEventListener('resize', emitScrollSnapshot)
    }
  }, [editorReadyVersion, emitScrollSnapshot, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const range = activeStartLine != null ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine } : null
    const effects: StateEffect<unknown>[] = [setScriptCodeRange.of(range)]
    if (range && range.startLine >= 1 && range.startLine <= view.state.doc.lines) {
      const line = view.state.doc.line(range.startLine)
      effects.push(EditorView.scrollIntoView(line.from, { y: 'center' }))
    }
    view.dispatch({ effects })
    emitScrollSnapshot()
  }, [activeStartLine, activeEndLine, editorReadyVersion, emitScrollSnapshot, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const firstMatch = parameterMatchRanges[0]
    const effects: StateEffect<unknown>[] = [setScriptCodeParameterMatches.of(parameterMatchRanges)]
    if (firstMatch && firstMatch.to <= view.state.doc.length) {
      effects.push(EditorView.scrollIntoView(firstMatch.from, { y: 'center' }))
      view.dispatch({
        selection: EditorSelection.range(firstMatch.from, firstMatch.to),
        effects,
      })
      emitScrollSnapshot()
      return
    }
    view.dispatch({ effects })
    emitScrollSnapshot()
  }, [editorReadyVersion, emitScrollSnapshot, parameterMatchKey, parameterMatchRanges, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const firstMatch = candidateMatchRanges[0]
    const effects: StateEffect<unknown>[] = [setScriptCodeCandidateMatches.of(candidateMatchRanges)]
    if (firstMatch && firstMatch.to <= view.state.doc.length) {
      effects.push(EditorView.scrollIntoView(firstMatch.from, { y: 'center' }))
      view.dispatch({
        selection: EditorSelection.range(firstMatch.from, firstMatch.to),
        effects,
      })
      emitScrollSnapshot()
      return
    }
    view.dispatch({ effects })
    emitScrollSnapshot()
  }, [candidateMatchKey, candidateMatchRanges, editorReadyVersion, emitScrollSnapshot, value])

  const resolvedValuesKey = resolvedValues ? JSON.stringify(resolvedValues) : ''
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const range = activeStartLine != null ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine } : null
    const payload = resolvedValues && range ? { values: resolvedValues, range } : null
    view.dispatch({ effects: [setScriptCodeResolvedValues.of(payload)] })
  }, [activeStartLine, activeEndLine, editorReadyVersion, resolvedValues, resolvedValuesKey, value])

  useEffect(() => {
    const view = viewRef.current
    if (!view || !jumpToLineRequest) return
    const lineNo = Math.max(1, Math.min(view.state.doc.lines, jumpToLineRequest.line))
    const line = view.state.doc.line(lineNo)
    view.dispatch({
      selection: EditorSelection.cursor(line.from),
      effects: EditorView.scrollIntoView(line.from, { y: 'center' }),
    })
    emitScrollSnapshot()
  }, [editorReadyVersion, emitScrollSnapshot, jumpToLineRequest, value])

  const handleOpenSearch = useCallback(() => {
    const view = viewRef.current
    if (!view) return
    openSearchPanel(view)
    view.focus()
  }, [])

  const handleEditorContextMenu = useCallback((event: ReactMouseEvent<HTMLDivElement>) => {
    if (!onRequestParameterize) return
    const view = viewRef.current
    if (!view) return
    const { from, to } = view.state.selection.main
    const request = buildParameterizeSelectionRequest(view.state.doc.toString(), from, to)
    if (!request) return
    event.preventDefault()
    onRequestParameterize({ ...request, x: event.clientX, y: event.clientY })
  }, [onRequestParameterize])

  const parameterizeCurrentSelection = useCallback(() => {
    const view = viewRef.current
    if (!onRequestParameterize || !view) return
    const { from, to } = view.state.selection.main
    const request = buildParameterizeSelectionRequest(view.state.doc.toString(), from, to)
    if (!request) {
      // Nothing parameterizable is selected — focus the editor so the author
      // can highlight a value, then try again.
      view.focus()
      return
    }
    const coords = view.coordsAtPos(request.end) ?? view.coordsAtPos(to)
    onRequestParameterize({ ...request, x: coords?.left ?? 0, y: coords?.bottom ?? 0 })
  }, [onRequestParameterize])

  return (
    <section className={`script-code-pane ${className}`} aria-label={ariaLabel}>
      <div className="script-code-toolbar">
        <div className="script-code-title">
          <strong>{'{ }'} {title}</strong>
          <span>{candidateStatus || parameterStatus || subtitle || activePath || `${actionCount} executable${actionCount === 1 ? '' : 's'}`}</span>
        </div>
        <div className="script-code-status-row">
          <span className={`script-code-status ${status}`}>{statusMessage}</span>
          {!jsonEditEnabled && (
            <span className="script-code-readonly-pill" title={allowDirectEdit ? 'The JSON mirrors the structured editor. Click Edit JSON to change it directly.' : 'Switch to Full Script JSON to edit raw JSON directly.'}>Read-only</span>
          )}
          <button type="button" className="btn btn-sm btn-ghost" onClick={handleOpenSearch} title="Find in script (⌘F / Ctrl+F)">Find</button>
          {onRequestParameterize && (
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={parameterizeCurrentSelection}
              title="Parameterize the selected value (you can also right-click a selection)"
            >
              Parameterize
            </button>
          )}
          {allowDirectEdit && (
            <button
              type="button"
              className={`btn btn-sm ${jsonEditEnabled ? 'btn-ghost' : 'btn-secondary'}`}
              onClick={() => setJsonEditEnabled(enabled => !enabled)}
              title={jsonEditEnabled ? 'Return the JSON to read-only' : 'Edit the raw JSON directly'}
            >
              {jsonEditEnabled ? 'Done' : 'Edit JSON'}
            </button>
          )}
          {allowDirectEdit && jsonEditEnabled && (
            <>
              <button type="button" className="btn btn-sm btn-ghost" onClick={onFormat}>Format</button>
              <button type="button" className="btn btn-sm btn-secondary" onClick={onApply} disabled={status === 'invalid'}>
                Apply JSON
              </button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={onReset} disabled={!value}>
                Reset
              </button>
            </>
          )}
          <button type="button" className="btn btn-sm btn-ghost" onClick={onCopy}>Copy</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={onDownload}>Download</button>
        </div>
      </div>
      <div className="script-code-editor" data-dirty={status === 'dirty' ? 'true' : 'false'} data-invalid={status === 'invalid' ? 'true' : 'false'} data-readonly={allowDirectEdit && jsonEditEnabled ? 'false' : 'true'} onContextMenu={handleEditorContextMenu}>
        <CodeMirror
          value={value}
          onChange={onChange}
          extensions={extensions}
          theme="none"
          editable={allowDirectEdit && jsonEditEnabled}
          basicSetup={{
            lineNumbers: true,
            foldGutter: true,
            highlightActiveLine: true,
            highlightActiveLineGutter: true,
          }}
          onCreateEditor={view => {
            viewRef.current = view
            emitScrollSnapshot()
            setEditorReadyVersion(version => version + 1)
          }}
        />
      </div>
    </section>
  )
}

function MetadataJsonViewer({ metadata }: { metadata: ScriptImportMetadata }) {
  const metadataJson = useMemo(() => JSON.stringify(metadata, null, 2), [metadata])
  const wrapLines = metadataJson.length <= SCRIPT_CODE_LINE_WRAP_MAX_CHARS
  const extensions = useMemo(() => [
    json(),
    apexCodeMirrorTheme,
    ...(wrapLines ? [EditorView.lineWrapping] : []),
    EditorView.editable.of(false),
  ], [wrapLines])

  return (
    <section className="script-code-pane metadata-json-viewer" aria-label="Metadata JSON">
      <div className="script-code-toolbar">
        <div className="script-code-title">
          <strong>{'{ }'} Metadata JSON</strong>
          <span>Preserved import metadata</span>
        </div>
        <div className="script-code-status-row">
          <span className="script-code-status synced">Read only</span>
        </div>
      </div>
      <div className="script-code-editor metadata-json-editor">
        <CodeMirror
          value={metadataJson}
          extensions={extensions}
          theme="none"
          readOnly
          editable={false}
          basicSetup={{
            lineNumbers: true,
            foldGutter: true,
            highlightActiveLine: true,
            highlightActiveLineGutter: true,
          }}
        />
      </div>
    </section>
  )
}

/**
 * On-demand modal wrapper for the (potentially multi-MB) read-only Metadata JSON editor.
 * Mounted only while open — opened from the left outline — so the heavy CodeMirror over the
 * preserved import metadata never builds on script open.
 */
function MetadataJsonDialog({ metadata, onClose }: { metadata: ScriptImportMetadata; onClose: () => void }) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog metadata-json-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Imported Metadata JSON"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Imported Metadata JSON</h3>
            <p className="editor-dialog-subtitle">
              Read-only view of the preserved import metadata. Opened on demand to keep the editor fast on large imports.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body metadata-json-dialog-body">
          <MetadataJsonViewer metadata={metadata} />
        </div>
      </div>
    </div>
  )
}

interface JsonMinimapBaseLine {
  lineNo: number
  textPreview: string
  widthPct: number
}

interface JsonMinimapParameterLines {
  parameterLines: Set<number>
  firstParameterLine: number | null
}

interface JsonSourceMinimapProps {
  activePath: string
  activeRange: LineRange | null
  candidateMatchRanges?: ScriptCodeMatchRange[]
  jsonText: string
  parameterMatchRanges: ScriptCodeMatchRange[]
  scrollSnapshot: ScriptCodeScrollSnapshot
  selectedParameterName?: string | null
  status: 'synced' | 'dirty' | 'invalid'
  onJumpToLine: (line: number) => void
  onScrollTopChange: (scrollTop: number) => void
}

function lineNumberForOffset(lineStarts: number[], offset: number): number {
  let low = 0
  let high = lineStarts.length - 1
  let result = 0
  while (low <= high) {
    const mid = Math.floor((low + high) / 2)
    if (lineStarts[mid] <= offset) {
      result = mid
      low = mid + 1
    } else {
      high = mid - 1
    }
  }
  return result + 1
}

/**
 * Build the source-map line data in two passes so a large import stays cheap:
 *  - {@link buildJsonMinimapBaseLines} is the only heavy (one-entry-per-line) pass; it is
 *    memoized on `jsonText` alone, so cursor moves and parameter selection do NOT rebuild it.
 *  - The active/selected/parameter flags are computed per *rendered* row instead of being baked
 *    into every line up front, which is what previously forced a full ~98k-entry rebuild on
 *    every selection change and motivated disabling the minimap on big docs entirely.
 */
function buildJsonMinimapBaseLines(jsonText: string): { baseLines: JsonMinimapBaseLine[]; lineStarts: number[] } {
  const lines = jsonText.split('\n')
  const lineStarts: number[] = []
  let offset = 0
  const baseLines = lines.map((line, index) => {
    lineStarts.push(offset)
    offset += line.length + 1
    const trimmed = line.trim()
    const widthPct = Math.max(12, Math.min(100, Math.round((trimmed.length / 90) * 100)))
    return { lineNo: index + 1, textPreview: trimmed || ' ', widthPct }
  })
  return { baseLines, lineStarts }
}

function buildJsonMinimapParameterLines(
  lineStarts: number[],
  parameterMatchRanges: ScriptCodeMatchRange[],
  candidateMatchRanges: ScriptCodeMatchRange[] = [],
): JsonMinimapParameterLines {
  const parameterLines = new Set<number>()
  let firstParameterLine: number | null = null
  const highlightedRanges = [...candidateMatchRanges, ...parameterMatchRanges]
  highlightedRanges.forEach((range, index) => {
    const startLine = lineNumberForOffset(lineStarts, range.from)
    const endLine = lineNumberForOffset(lineStarts, Math.max(range.from, range.to - 1))
    for (let lineNo = startLine; lineNo <= endLine; lineNo += 1) {
      parameterLines.add(lineNo)
    }
    if (index === 0) firstParameterLine = startLine
  })
  return { parameterLines, firstParameterLine }
}

function buildMinimapLineOffsets(lineHeights: number[]): number[] {
  return lineHeights.reduce<number[]>((offsets, height) => {
    offsets.push(offsets[offsets.length - 1] + height)
    return offsets
  }, [0])
}

function minimapOffsetForLinePosition(linePosition: number, lineOffsets: number[], lineHeights: number[]): number {
  if (lineHeights.length === 0) return 0
  const boundedPosition = clampNumber(linePosition, 1, lineHeights.length + 1)
  const rawIndex = boundedPosition - 1
  const lineIndex = Math.floor(rawIndex)
  if (lineIndex >= lineHeights.length) return lineOffsets[lineHeights.length] || 0
  const lineFraction = rawIndex - lineIndex
  return (lineOffsets[lineIndex] || 0) + lineFraction * lineHeights[lineIndex]
}

/**
 * Pixel offset of a (fractional) line in the minimap. When per-row heights are measured
 * (small/wrapped docs) it defers to {@link minimapOffsetForLinePosition}; otherwise rows are a
 * uniform {@link JSON_SOURCE_MAP_LINE_HEIGHT_PX} tall, so the offset is computed analytically —
 * this avoids materializing a one-entry-per-line height array for a ~98k-line import. The
 * uniform branch matches the cumulative offsets the row virtualizer derives from the same
 * constant row height, so the viewport highlight stays aligned with the rendered rows.
 */
function minimapOffsetForLine(
  linePosition: number,
  lineCount: number,
  lineOffsets: number[],
  lineHeights: number[],
): number {
  if (lineHeights.length > 0) return minimapOffsetForLinePosition(linePosition, lineOffsets, lineHeights)
  const boundedPosition = clampNumber(linePosition, 1, lineCount + 1)
  return (boundedPosition - 1) * JSON_SOURCE_MAP_LINE_HEIGHT_PX
}

function JsonSourceMinimap({
  activePath,
  activeRange,
  candidateMatchRanges = [],
  jsonText,
  parameterMatchRanges,
  scrollSnapshot,
  selectedParameterName,
  status,
  onJumpToLine,
  onScrollTopChange,
}: JsonSourceMinimapProps) {
  const scrollTrackRef = useRef<HTMLDivElement | null>(null)
  const sourceMapLinesRef = useRef<HTMLDivElement | null>(null)
  const dragStateRef = useRef<{ pointerId: number | null; startY: number; startRatio: number; trackHeight: number } | null>(null)
  const selectedLineRange = useMemo(() => ({
    startLine: Math.min(scrollSnapshot.selectedStartLine, scrollSnapshot.selectedEndLine),
    endLine: Math.max(scrollSnapshot.selectedStartLine, scrollSnapshot.selectedEndLine),
  }), [scrollSnapshot.selectedEndLine, scrollSnapshot.selectedStartLine])
  // The minimap data is split: the heavy one-entry-per-line pass is memoized on jsonText alone,
  // while the active/selected/parameter flags are derived per rendered row (see renderSourceMapLine).
  const { baseLines, lineStarts } = useMemo(() => buildJsonMinimapBaseLines(jsonText), [jsonText])
  const parameterLineInfo = useMemo(
    () => buildJsonMinimapParameterLines(lineStarts, parameterMatchRanges, candidateMatchRanges),
    [lineStarts, parameterMatchRanges, candidateMatchRanges],
  )
  // Window the per-line minimap (only visible rows hit the DOM) once the doc is large enough that
  // the editor stops measuring per-line layouts — at that point rows are a uniform height, which
  // keeps the windowed path simple. Below it, the plain grid render handles ordinary scripts and
  // test fixtures with their measured (wrapped) row heights, identical to before.
  const virtualizeSourceMap = baseLines.length > SCRIPT_CODE_LINE_LAYOUT_MAX_LINES
  const activeLabel = selectedParameterName
    ? `${selectedParameterName}: ${parameterMatchRanges.length} occurrence${parameterMatchRanges.length === 1 ? '' : 's'}`
    : activePath || (activeRange ? `Lines ${activeRange.startLine}-${activeRange.endLine}` : 'No selection')
  const invalid = status === 'invalid' || !jsonText.trim()
  const scrollRange = Math.max(0, scrollSnapshot.scrollHeight - scrollSnapshot.clientHeight)
  const hasScrollableEditor = scrollRange > 0 && scrollSnapshot.clientHeight > 0
  const minimapScale = JSON_SOURCE_MAP_LINE_HEIGHT_PX / Math.max(1, scrollSnapshot.baseLineHeight || JSON_SOURCE_MAP_LINE_HEIGHT_PX)
  // Per-row heights are only materialized when the editor measured per-line layouts (small docs).
  // Above SCRIPT_CODE_LINE_LAYOUT_MAX_LINES lineLayouts is empty and every row is a uniform
  // JSON_SOURCE_MAP_LINE_HEIGHT_PX tall, so we skip building a ~98k-entry array entirely.
  const hasMeasuredRowHeights = scrollSnapshot.lineLayouts.length > 0
  const minimapLineHeights = useMemo(
    () => hasMeasuredRowHeights
      ? baseLines.map((_, index) => {
          const editorHeight = scrollSnapshot.lineLayouts[index]?.height || scrollSnapshot.baseLineHeight || JSON_SOURCE_MAP_LINE_HEIGHT_PX
          return Math.max(JSON_SOURCE_MAP_LINE_HEIGHT_PX, editorHeight * minimapScale)
        })
      : [],
    [hasMeasuredRowHeights, baseLines, minimapScale, scrollSnapshot.baseLineHeight, scrollSnapshot.lineLayouts],
  )
  const minimapLineOffsets = useMemo(() => buildMinimapLineOffsets(minimapLineHeights), [minimapLineHeights])
  const sourceMapVirtualizer = useVirtualizer({
    count: baseLines.length,
    getScrollElement: () => sourceMapLinesRef.current,
    estimateSize: index => minimapLineHeights[index] ?? JSON_SOURCE_MAP_LINE_HEIGHT_PX,
    overscan: 24,
  })
  const sourceMapGridTemplateRows = useMemo(() => {
    const fallbackCount = Math.max(baseLines.length, 1)
    if (minimapLineHeights.length === 0) return `repeat(${fallbackCount}, ${JSON_SOURCE_MAP_LINE_HEIGHT_PX}px)`
    const hasWrappedRows = minimapLineHeights.some(height => Math.abs(height - JSON_SOURCE_MAP_LINE_HEIGHT_PX) > 0.01)
    if (!hasWrappedRows) return `repeat(${fallbackCount}, ${JSON_SOURCE_MAP_LINE_HEIGHT_PX}px)`
    return minimapLineHeights.map(height => `${height.toFixed(2)}px`).join(' ')
  }, [baseLines.length, minimapLineHeights])
  const visibleRatio = hasScrollableEditor
    ? clampNumber(scrollSnapshot.clientHeight / scrollSnapshot.scrollHeight, 0.04, 1)
    : 1
  const scrollRatio = hasScrollableEditor
    ? clampNumber(scrollSnapshot.scrollTop / scrollRange, 0, 1)
    : 0
  const scrollPercent = Math.round(scrollRatio * 100)
  const thumbHeightPct = visibleRatio * 100
  const thumbTopPct = scrollRatio * 100
  const viewportHighlightTop = minimapOffsetForLine(scrollSnapshot.topLine, baseLines.length, minimapLineOffsets, minimapLineHeights)
  const viewportHighlightBottom = minimapOffsetForLine(
    Math.max(scrollSnapshot.topLine, scrollSnapshot.bottomLine),
    baseLines.length,
    minimapLineOffsets,
    minimapLineHeights,
  )
  const viewportHighlightHeight = Math.max(JSON_SOURCE_MAP_LINE_HEIGHT_PX, viewportHighlightBottom - viewportHighlightTop)

  useEffect(() => {
    const linesElement = sourceMapLinesRef.current
    if (!linesElement) return
    const maxScrollTop = Math.max(0, linesElement.scrollHeight - linesElement.clientHeight)
    linesElement.scrollTop = clampNumber(viewportHighlightTop, 0, maxScrollTop)
  }, [baseLines.length, viewportHighlightTop])

  const scrollFromTrackPosition = (clientY: number) => {
    const track = scrollTrackRef.current
    if (!track) return
    const rect = track.getBoundingClientRect()
    if (rect.height <= 0) return
    const thumbHeight = rect.height * visibleRatio
    const draggableHeight = Math.max(1, rect.height - thumbHeight)
    const nextRatio = clampNumber((clientY - rect.top - thumbHeight / 2) / draggableHeight, 0, 1)
    onScrollTopChange(nextRatio * scrollRange)
  }

  const handleSourceMapWheel = (event: ReactWheelEvent<HTMLDivElement>) => {
    if (!hasScrollableEditor) return
    const rawDelta = Math.abs(event.deltaY) >= Math.abs(event.deltaX) ? event.deltaY : event.deltaX
    if (rawDelta === 0) return
    const deltaPixels = event.deltaMode === 1
      ? rawDelta * JSON_SOURCE_MAP_LINE_HEIGHT_PX
      : event.deltaMode === 2
        ? rawDelta * scrollSnapshot.clientHeight
        : rawDelta
    const nextScrollTop = clampNumber(
      scrollSnapshot.scrollTop + deltaPixels / minimapScale,
      0,
      scrollRange,
    )
    const linesElement = sourceMapLinesRef.current
    if (linesElement) {
      const maxScrollTop = Math.max(0, linesElement.scrollHeight - linesElement.clientHeight)
      linesElement.scrollTop = clampNumber(nextScrollTop * minimapScale, 0, maxScrollTop)
    }
    event.preventDefault()
    event.stopPropagation()
    onScrollTopChange(nextScrollTop)
  }

  const handleTrackPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!hasScrollableEditor) return
    event.preventDefault()
    scrollFromTrackPosition(event.clientY)
  }

  const handleTrackMouseDown = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (!hasScrollableEditor || event.button !== 0) return
    event.preventDefault()
    scrollFromTrackPosition(event.clientY)
  }

  const beginThumbDrag = (clientY: number, pointerId: number | null) => {
    if (!hasScrollableEditor) return
    const track = scrollTrackRef.current
    const trackHeight = track?.getBoundingClientRect().height || 0
    if (trackHeight <= 0) return
    dragStateRef.current = {
      pointerId,
      startY: clientY,
      startRatio: scrollRatio,
      trackHeight,
    }
  }

  const applyThumbDrag = (clientY: number) => {
    const dragState = dragStateRef.current
    if (!dragState) return
    const draggableHeight = Math.max(1, dragState.trackHeight * (1 - visibleRatio))
    const nextRatio = clampNumber(dragState.startRatio + (clientY - dragState.startY) / draggableHeight, 0, 1)
    onScrollTopChange(nextRatio * scrollRange)
  }

  const handleThumbPointerDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (!hasScrollableEditor) return
    event.preventDefault()
    event.stopPropagation()
    const pointerId = typeof event.pointerId === 'number' ? event.pointerId : null
    beginThumbDrag(event.clientY, pointerId)
    if (pointerId !== null) {
      event.currentTarget.setPointerCapture?.(pointerId)
    }
  }

  const handleThumbPointerMove = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const dragState = dragStateRef.current
    if (!dragState) return
    if (dragState.pointerId !== null && dragState.pointerId !== event.pointerId) return
    event.preventDefault()
    applyThumbDrag(event.clientY)
  }

  const handleThumbPointerUp = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const dragState = dragStateRef.current
    if (!dragState) return
    if (dragState.pointerId !== null && dragState.pointerId !== event.pointerId) return
    dragStateRef.current = null
    if (dragState.pointerId !== null) {
      event.currentTarget.releasePointerCapture?.(dragState.pointerId)
    }
  }

  const handleThumbMouseDown = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (!hasScrollableEditor || event.button !== 0 || dragStateRef.current) return
    event.preventDefault()
    event.stopPropagation()
    beginThumbDrag(event.clientY, null)
  }

  useEffect(() => {
    const handleDocumentPointerMove = (event: PointerEvent) => {
      const dragState = dragStateRef.current
      if (!dragState || dragState.pointerId === null || dragState.pointerId !== event.pointerId) return
      event.preventDefault()
      applyThumbDrag(event.clientY)
    }
    const handleDocumentPointerUp = (event: PointerEvent) => {
      const dragState = dragStateRef.current
      if (!dragState || dragState.pointerId === null || dragState.pointerId !== event.pointerId) return
      dragStateRef.current = null
    }
    const handleDocumentMouseMove = (event: MouseEvent) => {
      const dragState = dragStateRef.current
      if (!dragState || dragState.pointerId !== null) return
      if (event.buttons === 0) {
        dragStateRef.current = null
        return
      }
      event.preventDefault()
      applyThumbDrag(event.clientY)
    }
    const handleDocumentMouseUp = () => {
      const dragState = dragStateRef.current
      if (!dragState || dragState.pointerId !== null) return
      dragStateRef.current = null
    }

    document.addEventListener('pointermove', handleDocumentPointerMove)
    document.addEventListener('pointerup', handleDocumentPointerUp)
    document.addEventListener('pointercancel', handleDocumentPointerUp)
    document.addEventListener('mousemove', handleDocumentMouseMove)
    document.addEventListener('mouseup', handleDocumentMouseUp)
    return () => {
      document.removeEventListener('pointermove', handleDocumentPointerMove)
      document.removeEventListener('pointerup', handleDocumentPointerUp)
      document.removeEventListener('pointercancel', handleDocumentPointerUp)
      document.removeEventListener('mousemove', handleDocumentMouseMove)
      document.removeEventListener('mouseup', handleDocumentMouseUp)
    }
  }, [applyThumbDrag])

  const handleThumbKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    if (!hasScrollableEditor) return
    const smallStep = Math.max(0.015, visibleRatio / 4)
    const pageStep = Math.max(0.08, visibleRatio * 0.9)
    let nextRatio: number | null = null
    if (event.key === 'ArrowDown') {
      nextRatio = scrollRatio + smallStep
    } else if (event.key === 'ArrowUp') {
      nextRatio = scrollRatio - smallStep
    } else if (event.key === 'PageDown') {
      nextRatio = scrollRatio + pageStep
    } else if (event.key === 'PageUp') {
      nextRatio = scrollRatio - pageStep
    } else if (event.key === 'Home') {
      nextRatio = 0
    } else if (event.key === 'End') {
      nextRatio = 1
    }
    if (nextRatio == null) return
    event.preventDefault()
    onScrollTopChange(clampNumber(nextRatio, 0, 1) * scrollRange)
  }

  // Renders one minimap row. The active/selected/parameter flags are derived here (per visible
  // row) rather than baked into the line data, so they stay cheap for windowed large docs.
  const renderSourceMapLine = (base: JsonMinimapBaseLine, virtualStyle?: React.CSSProperties) => {
    const { lineNo } = base
    const isActiveBlock = Boolean(activeRange && lineNo >= activeRange.startLine && lineNo <= activeRange.endLine)
    const isSelectedLine = Boolean(selectedLineRange && lineNo >= selectedLineRange.startLine && lineNo <= selectedLineRange.endLine)
    const hasParameterMatch = parameterLineInfo.parameterLines.has(lineNo)
    const hasFirstParameterMatch = parameterLineInfo.firstParameterLine === lineNo
    return (
      <button
        key={`json-map-line-${lineNo}`}
        type="button"
        className={`json-source-map-line ${isActiveBlock ? 'active-block' : ''} ${hasParameterMatch ? 'parameter-match' : ''} ${hasFirstParameterMatch ? 'first-parameter-match' : ''} ${isSelectedLine ? 'selected-line' : ''}`}
        style={virtualStyle}
        onClick={() => onJumpToLine(lineNo)}
        title={`Line ${lineNo}: ${base.textPreview}`}
        aria-label={`Jump to JSON line ${lineNo}`}
      >
        <span className="json-source-map-line-number">{lineNo}</span>
        <span className="json-source-map-line-code" style={{ width: `${base.widthPct}%` }}>{base.textPreview}</span>
      </button>
    )
  }

  return (
    <div className="json-source-map-panel">
      <div className="json-source-map-summary">
        <strong>Script JSON</strong>
        <span>{activeLabel}</span>
      </div>
      {invalid ? (
        <div className="utility-empty-state json-source-map-empty">
          {jsonText.trim() ? 'Fix the JSON draft to rebuild the source map.' : 'No JSON source to map yet.'}
        </div>
      ) : (
        <div className={`json-source-map ${hasScrollableEditor ? 'scroll-linked' : ''}`} aria-label="JSON source map" onWheel={handleSourceMapWheel}>
          <div
            ref={sourceMapLinesRef}
            className={`json-source-map-lines ${virtualizeSourceMap ? 'virtualized' : ''}`}
            style={virtualizeSourceMap ? undefined : { gridTemplateRows: sourceMapGridTemplateRows }}
          >
            {virtualizeSourceMap ? (
              <div
                className="json-source-map-virtual"
                style={{ position: 'relative', height: `${sourceMapVirtualizer.getTotalSize()}px` }}
              >
                {hasScrollableEditor && (
                  <div
                    aria-hidden="true"
                    className="json-source-map-viewport-highlight"
                    style={{
                      top: `${viewportHighlightTop}px`,
                      height: `${viewportHighlightHeight}px`,
                    }}
                  />
                )}
                {sourceMapVirtualizer.getVirtualItems().map(virtualItem => {
                  const base = baseLines[virtualItem.index]
                  if (!base) return null
                  return renderSourceMapLine(base, {
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    height: `${virtualItem.size}px`,
                    transform: `translateY(${virtualItem.start}px)`,
                  })
                })}
              </div>
            ) : (
              <>
                {hasScrollableEditor && (
                  <div
                    aria-hidden="true"
                    className="json-source-map-viewport-highlight"
                    style={{
                      top: `${viewportHighlightTop}px`,
                      height: `${viewportHighlightHeight}px`,
                    }}
                  />
                )}
                {baseLines.map(base => renderSourceMapLine(base))}
              </>
            )}
          </div>
          {hasScrollableEditor && (
            <div
              ref={scrollTrackRef}
              className="json-source-map-scroll-track"
              onMouseDown={handleTrackMouseDown}
              onPointerDown={handleTrackPointerDown}
            >
              <button
                type="button"
                className="json-source-map-scroll-thumb"
                style={{
                  height: `${thumbHeightPct}%`,
                  top: `${thumbTopPct}%`,
                  transform: `translateY(-${thumbTopPct}%)`,
                }}
                role="scrollbar"
                aria-label="Scroll Script JSON source"
                aria-orientation="vertical"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={scrollPercent}
                onKeyDown={handleThumbKeyDown}
                onMouseDown={handleThumbMouseDown}
                onPointerDown={handleThumbPointerDown}
                onPointerMove={handleThumbPointerMove}
                onPointerUp={handleThumbPointerUp}
                onPointerCancel={handleThumbPointerUp}
              />
            </div>
          )}
        </div>
      )}
    </div>
  )
}

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
            <div
              key={tab.id}
              id={`center-tab-panel-${tab.id}`}
              ref={element => {
                if (element) onPanelElement(tab.id, element)
              }}
              role="tabpanel"
              aria-labelledby={`center-tab-${tab.id}`}
              className={`center-tab-panel ${selected ? 'active' : ''}`}
              hidden={!selected}
            >
              {renderTabPanel(tab.id)}
            </div>
          )
        })}
      </div>
    </section>
  )
}

function applyValidationRuntimeSettingsMetadata(script: ScriptDefinition, validation: ScriptValidationResponse): ScriptDefinition {
  const importMetadata = scriptHasImportMetadata(script) ? script.metadata?.import : undefined
  if (!importMetadata) {
    return script
  }

  const preserved = { ...(importMetadata.preserved || {}) }
  const runtimeSettingsParity = Array.isArray(validation.runtime_settings_parity) ? validation.runtime_settings_parity : []
  const runtimeSettingsStatusCounts = validation.runtime_settings_status_counts
  const migrationReadiness = validation.migration_readiness

  if (runtimeSettingsParity.length > 0) {
    preserved.runtime_settings_parity = runtimeSettingsParity
  } else {
    delete preserved.runtime_settings_parity
  }

  if (runtimeSettingsStatusCounts && Object.keys(runtimeSettingsStatusCounts).length > 0) {
    preserved.runtime_settings_status_counts = runtimeSettingsStatusCounts
  } else {
    delete preserved.runtime_settings_status_counts
  }

  if (migrationReadiness) {
    preserved.runtime_settings_migration_readiness = migrationReadiness
  } else {
    delete preserved.runtime_settings_migration_readiness
  }

  return {
    ...script,
    metadata: {
      ...(script.metadata || {}),
      import: {
        ...importMetadata,
        preserved,
      },
    },
  }
}

function normalizeWarnings(messages?: string[]) {
  if (!Array.isArray(messages)) return []
  return Array.from(new Set(messages.map(message => message.trim()).filter(Boolean)))
}

interface ScriptOutlineAction {
  index: number
  actionIndex: number
  phase: ScriptActionPhase
  phaseLabel: string
  name: string
  type: string
  target: string
  isBrowserAction: boolean
  hasTransaction: boolean
  hasWait: boolean
  assertionCount: number
  parameterRefs: string[]
  selector?: string
}

interface ScriptOutlineSummary {
  actionCount: number
  initCount: number
  mainActionCount: number
  teardownCount: number
  browserActionCount: number
  transactionCount: number
  parameterRefCount: number
  waitCount: number
  assertionCount: number
  actions: ScriptOutlineAction[]
}

interface Web2SelectorCandidate {
  kind: string
  value: string
  confidence: number
  reason: string
}

interface Web2ObjectDescriptorRow {
  key: string
  label: string
  descriptorId: string
  status: string
  actionIndex: number | null
  currentSelector: string
  frameContext: string
  windowContext: string
  replayDiagnostics: string[]
  candidates: Web2SelectorCandidate[]
}

interface Web2ObjectDescriptorReview {
  rows: Web2ObjectDescriptorRow[]
  repositoryCount: number
  candidateCount: number
  unsupportedCount: number
}

interface BrowserHTTPConversionReview {
  browserActionCount: number
  replayEvidence: boolean
  networkEvidence: boolean
  candidateRequestCount: number
  status: 'blocked' | 'needs_replay' | 'ready_for_review'
  findings: string[]
}

interface TrueClientPromotionReview {
  status: string
  generatedAt: string
  source: string
  sourceScriptName: string
  compatibility: {
    sourceProfile: string
    importMode: string
    promotionStatus: string
    promotionAllowed: boolean
    runtimeParityStatus: string
    replayStatus: string
    javascriptReviewGate: string
    httpConversionReadiness: string
    selectorConfidencePct: number
    frameWindowBlockers: number
  }
  gates: {
    reviewRequiredActions: number
    javascriptReviewActions: number
    selectorFallbackCandidates: number
    replayRequired: boolean
    replayWarnings: string[]
    importWarnings: string[]
  }
  replayEvidence: {
    sessionId: string
    status: string
    startedAt: string
    completedAt: string
    events: number
    passed: number
    failed: number
    screenshots: number
    domHtmlCaptures: number
    consoleEntries: number
    networkEntries: number
    sourceSteps: number
    sourceTransactions: number
  }
  nextActions: string[]
}

function extractParameterReferences(action: Action) {
  const refs = new Set<string>()
  const json = JSON.stringify({
    target: action.target,
    selector: action.selector,
    value: action.value,
    headers: action.headers,
    body: action.body,
    params: action.params,
    metadata: action.metadata,
  })

  const tokenPattern = /\{\{\s*(?:param|data|var)\.([a-zA-Z0-9_.-]+)/g
  let match = tokenPattern.exec(json)
  while (match) {
    refs.add(match[1])
    match = tokenPattern.exec(json)
  }

  return Array.from(refs)
}

function actionHasWait(action: Action) {
  if (action.wait_for?.type || action.wait_for?.value || action.wait_for?.timeout_ms) {
    return true
  }
  return action.type === 'browser_wait'
}


function buildScriptOutlineSummary(script: ScriptData): ScriptOutlineSummary {
  const autoTransactions = script.config.run_logic?.auto_transactions
  const phaseRows = buildScriptPhaseActionRows(script)
  const actions = phaseRows.map((row) => {
    const { action } = row
    const isBrowserAction = (action.type || '').startsWith('browser_')
    const assertionCount = actionAssertionCount(action)
    const sequenceRefs = httpSequenceRequests(action).flatMap(request => extractParameterReferences(request))
    const parameterRefs = [...new Set([...extractParameterReferences(action), ...sequenceRefs])]
    const hasTransaction = autoTransactions === 'per_action' || Boolean(action.metadata?.transaction || action.metadata?.transaction_name || action.metadata?.source_transaction || action.metadata?.source_transaction)
    return {
      index: row.globalIndex,
      actionIndex: row.actionIndex,
      phase: row.phase,
      phaseLabel: row.phaseLabel,
      name: action.name || `${row.phaseLabel} ${row.actionIndex + 1}`,
      type: actionDisplayType(action),
      target: actionDisplayTarget(action),
      isBrowserAction,
      hasTransaction,
      hasWait: actionHasWait(action),
      assertionCount,
      parameterRefs,
      selector: action.selector,
    }
  })

  return {
    actionCount: actions.length,
    initCount: script.init?.length || 0,
    mainActionCount: script.actions.length,
    teardownCount: script.teardown?.length || 0,
    browserActionCount: actions.filter(action => action.isBrowserAction).length,
    transactionCount: actions.filter(action => action.hasTransaction).length,
    parameterRefCount: actions.reduce((sum, action) => sum + action.parameterRefs.length, 0),
    waitCount: actions.filter(action => action.hasWait).length,
    assertionCount: actions.reduce((sum, action) => sum + action.assertionCount, 0),
    actions,
  }
}

function readPreservedImportObject(metadata?: ScriptImportMetadata | null, key?: string): Record<string, unknown> | null {
  if (!metadata?.preserved || !key) return null
  const value = metadata.preserved[key]
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function readSourceReview(metadata?: ScriptImportMetadata | null): SourceArchiveReview | null {
  const value = readPreservedImportObject(metadata, 'source_review')
  if (!value) return null
  if (!Array.isArray(value.source_files) && !Array.isArray(value.mappings)) return null
  return value as SourceArchiveReview
}

function scriptHasImportMetadata(script?: ScriptData | null): boolean {
  const metadata = script?.metadata?.import
  if (!metadata) return false
  const preserved = isRecord(metadata.preserved) ? metadata.preserved : null
  return Boolean(
    metadata.source
    || metadata.source_script_name
    || (preserved && Object.keys(preserved).length > 0)
  )
}

function normalizeMappingText(value: unknown): string {
  return String(value || '').trim().toLowerCase()
}

function mappingTargetsAction(mapping: SourceArchiveReviewMapping, actionIndex: number): boolean {
  const targetPath = mapping.target_path || ''
  const actionPath = `actions[${actionIndex}]`
  return targetPath === actionPath || targetPath.startsWith(`${actionPath}.`) || targetPath.startsWith(`${actionPath}[`)
}

function sourceMappingActionScore(mapping: SourceArchiveReviewMapping, action: Action | undefined, actionIndex: number): number {
  const targetPath = mapping.target_path || ''
  const actionPath = `actions[${actionIndex}]`
  let score = mapping.start_line || 0
  if (targetPath === actionPath) score -= 10000
  else if (targetPath.startsWith(`${actionPath}.params.requests[`)) score -= 9000
  else if (mappingTargetsAction(mapping, actionIndex)) score -= 8000
  if (mapping.status === 'converted') score -= 1000
  if (mapping.target_kind === 'action') score -= 500
  if (mapping.support_mode === 'executable') score -= 100
  const targetName = normalizeMappingText(mapping.target_name)
  const actionName = normalizeMappingText(action?.name)
  const sourceTransaction = normalizeMappingText(action?.metadata?.source_transaction || action?.metadata?.source_transaction || action?.metadata?.transaction_name || action?.metadata?.transaction)
  if (actionName && targetName === actionName) score -= 250
  if (sourceTransaction && targetName === sourceTransaction) score -= 250
  return score
}

function findSourceMappingForAction(sourceReview: SourceArchiveReview | null, action: Action | undefined, actionIndex: number): SourceArchiveReviewMapping | null {
  const mappings = (sourceReview?.mappings || []).filter(mapping => mapping && mapping.id)
  if (mappings.length === 0 || !action || actionIndex < 0) return null
  const actionName = normalizeMappingText(action.name)
  const sourceTransaction = normalizeMappingText(action.metadata?.source_transaction || action.metadata?.source_transaction || action.metadata?.transaction_name || action.metadata?.transaction)
  const matches = mappings.filter(mapping => {
    if (mappingTargetsAction(mapping, actionIndex)) return true
    const targetName = normalizeMappingText(mapping.target_name)
    return Boolean(targetName && (targetName === actionName || targetName === sourceTransaction))
  })
  if (matches.length === 0) return null
  return [...matches].sort((a, b) => sourceMappingActionScore(a, action, actionIndex) - sourceMappingActionScore(b, action, actionIndex))[0] || null
}

function readString(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function readNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function readBoolean(value: unknown) {
  if (typeof value === 'boolean') return value
  if (typeof value !== 'string') return false
  const normalized = value.trim().toLowerCase()
  return normalized === 'true' || normalized === 'yes' || normalized === '1'
}

function readArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function readStringArray(value: unknown): string[] {
  return readArray(value).map(readString).filter(Boolean)
}

function readCount(value: unknown): number {
  return Math.max(0, Math.trunc(readNumber(value)))
}

function readPromotionRecord(parent: Record<string, unknown>, key: string): Record<string, unknown> {
  const value = parent[key]
  return isRecord(value) ? value : {}
}

function readTrueClientPromotionReview(metadata?: ScriptImportMetadata | null): TrueClientPromotionReview | null {
  const raw = readPreservedImportObject(metadata, 'trueclient2_promotion_review')
  if (!raw) return null

  const compatibility = readPromotionRecord(raw, 'compatibility')
  const gates = readPromotionRecord(raw, 'gates')
  const replayEvidence = readPromotionRecord(raw, 'replay_evidence')
  const sourceScriptName = readString(raw.source_script_name) || metadata?.source_script_name || ''

  return {
    status: readString(raw.status) || 'review_required',
    generatedAt: readString(raw.generated_at),
    source: readString(raw.source) || metadata?.source || 'enterprise_script_archive',
    sourceScriptName,
    compatibility: {
      sourceProfile: readString(compatibility.source_profile),
      importMode: readString(compatibility.import_mode),
      promotionStatus: readString(compatibility.promotion_status),
      promotionAllowed: readBoolean(compatibility.promotion_allowed),
      runtimeParityStatus: readString(compatibility.runtime_parity_status),
      replayStatus: readString(compatibility.replay_status),
      javascriptReviewGate: readString(compatibility.javascript_review_gate),
      httpConversionReadiness: readString(compatibility.http_conversion_readiness),
      selectorConfidencePct: readCount(compatibility.selector_confidence_pct),
      frameWindowBlockers: readCount(compatibility.frame_window_blockers),
    },
    gates: {
      reviewRequiredActions: readCount(gates.review_required_actions),
      javascriptReviewActions: readCount(gates.javascript_review_actions),
      selectorFallbackCandidates: readCount(gates.selector_fallback_candidates),
      replayRequired: readBoolean(gates.replay_required),
      replayWarnings: readStringArray(gates.replay_warnings),
      importWarnings: readStringArray(gates.import_warnings),
    },
    replayEvidence: {
      sessionId: readString(replayEvidence.session_id),
      status: readString(replayEvidence.status) || 'not_run',
      startedAt: readString(replayEvidence.started_at),
      completedAt: readString(replayEvidence.completed_at),
      events: readCount(replayEvidence.events),
      passed: readCount(replayEvidence.passed),
      failed: readCount(replayEvidence.failed),
      screenshots: readCount(replayEvidence.screenshots),
      domHtmlCaptures: readCount(replayEvidence.dom_html_captures),
      consoleEntries: readCount(replayEvidence.console_entries),
      networkEntries: readCount(replayEvidence.network_entries),
      sourceSteps: readCount(replayEvidence.source_steps),
      sourceTransactions: readCount(replayEvidence.source_transactions),
    },
    nextActions: readStringArray(raw.next_actions),
  }
}

function formatTrueClientPromotionStatus(status: string) {
  switch (status) {
    case 'replay_evidence_attached':
      return 'Replay evidence attached'
    case 'needs_replay_evidence':
      return 'Needs replay evidence'
    case 'promotion_ready':
      return 'Promotion ready'
    case 'promotion_blocked':
      return 'Promotion blocked'
    default:
      return status.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
  }
}

function formatPromotionCount(count: number, singular: string, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`
}

function normalizeWeb2SelectorCandidate(value: unknown): Web2SelectorCandidate | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const item = value as Record<string, unknown>
  const candidate = {
    kind: readString(item.kind) || 'selector',
    value: readString(item.value),
    confidence: readNumber(item.confidence),
    reason: readString(item.reason),
  }
  return candidate.value ? candidate : null
}

function buildWeb2ObjectDescriptorReview(script: ScriptData, metadata?: ScriptImportMetadata | null): Web2ObjectDescriptorReview {
  const inspection = readPreservedImportObject(metadata, 'trueclient_archive_inspection')
  const steps = readArray(inspection?.steps)
  const objectRepositories = readArray(inspection?.object_repositories)
  const unsupportedFragments = readArray(inspection?.unsupported_fragments)
  const rows = steps
    .map((step, index) => {
      if (!step || typeof step !== 'object' || Array.isArray(step)) return null
      const item = step as Record<string, unknown>
      const candidates = readArray(item.selector_candidates)
        .map(normalizeWeb2SelectorCandidate)
        .filter((candidate): candidate is Web2SelectorCandidate => candidate !== null)
      const actionIndex = index < script.actions.length && script.actions[index]?.type.startsWith('browser_') ? index : null
      const currentSelector = actionIndex !== null ? script.actions[actionIndex]?.selector || script.actions[actionIndex]?.target || '' : ''
      const diagnostics = readArray(item.replay_diagnostics)
        .map((diagnostic) => {
          if (!diagnostic || typeof diagnostic !== 'object' || Array.isArray(diagnostic)) return ''
          const detail = diagnostic as Record<string, unknown>
          return readString(detail.summary) || readString(detail.code)
        })
        .filter(Boolean)
      return {
        key: `${readString(item.step_id) || index}:${readString(item.index) || index}`,
        label: readString(item.action_name) || readString(item.action) || `Step ${index + 1}`,
        descriptorId: readString(item.object_descriptor_id) || 'No descriptor ID',
        status: readString(item.status) || 'review',
        actionIndex,
        currentSelector,
        frameContext: readString(item.frame_context),
        windowContext: readString(item.window_context),
        replayDiagnostics: diagnostics,
        candidates,
      }
    })
    .filter((row): row is Web2ObjectDescriptorRow => row !== null && (row.candidates.length > 0 || row.descriptorId !== 'No descriptor ID'))

  return {
    rows,
    repositoryCount: objectRepositories.length,
    candidateCount: rows.reduce((sum, row) => sum + row.candidates.length, 0),
    unsupportedCount: unsupportedFragments.length,
  }
}

function replayEventsForConversion(replayReport: ReplayReport | null, browserReplaySession?: ScriptDebugSession | null): BrowserReplayEvent[] {
  if (browserReplaySession?.events?.length) return browserReplaySession.events
  return replayReport?.events || []
}

function replayNetworkEntriesForConversion(replayReport: ReplayReport | null, browserReplaySession?: ScriptDebugSession | null): BrowserReplayNetworkEntry[] {
  const entries: BrowserReplayNetworkEntry[] = []
  const seen = new Set<string>()
  replayEventsForConversion(replayReport, browserReplaySession).forEach(event => {
    const eventEntries = [
      ...(event.network_entries || []),
      ...(event.snapshot?.network_entries || []),
    ]
    if (event.snapshot?.request_url) {
      eventEntries.push({
        url: event.snapshot.request_url,
        method: event.snapshot.request_method || event.method || 'GET',
        status_code: event.snapshot.response_status || event.status_code,
        resource_type: 'document',
        duration_ms: event.duration_ms,
      })
    }
    eventEntries.forEach(entry => {
      const url = entry.url?.trim()
      if (!url) return
      const method = (entry.method || 'GET').toUpperCase()
      const key = `${method} ${url}`
      if (seen.has(key)) return
      seen.add(key)
      entries.push({ ...entry, method, url })
    })
  })
  return entries
}

function buildBrowserHTTPConversionReview(script: ScriptData, replayReport: ReplayReport | null, browserReplaySession?: ScriptDebugSession | null): BrowserHTTPConversionReview {
  const browserActionCount = script.actions.filter(action => action.type.startsWith('browser_')).length
  const replayEvents = replayEventsForConversion(replayReport, browserReplaySession)
  const conversionEntries = replayNetworkEntriesForConversion(replayReport, browserReplaySession)
  const networkEvidence = conversionEntries.length > 0
  const candidateRequestCount = conversionEntries.length
  const replayEvidence = Boolean(replayReport?.completed_at || browserReplaySession?.completed_at || replayEvents.length > 0)
  const status = !replayEvidence ? 'needs_replay' : networkEvidence ? 'ready_for_review' : 'blocked'
  const findings = [
    browserActionCount > 0 ? `${browserActionCount} browser action${browserActionCount === 1 ? '' : 's'} available as conversion context.` : 'No browser actions are available for conversion.',
    replayEvidence ? 'Replay evidence is attached to this draft.' : 'Run browser replay before generating an HTTP draft.',
    networkEvidence ? `${candidateRequestCount} captured request${candidateRequestCount === 1 ? '' : 's'} can seed HTTP conversion review.` : 'Network capture evidence is required before HTTP conversion.',
    'Generated HTTP drafts must still pass correlation, parameter data, runtime settings, replay, and approval gates.',
  ]
  return { browserActionCount, replayEvidence, networkEvidence, candidateRequestCount, status, findings }
}

function hasStructuredReplaySnapshot(snapshot?: ReplaySnapshot | null) {
  return Boolean(
    snapshot?.request_method ||
    snapshot?.request_url ||
    snapshot?.request_headers ||
    snapshot?.request_body ||
    snapshot?.response_status ||
    snapshot?.response_headers ||
    snapshot?.response_body,
  )
}

function safeParseReplayVariables(text: string): Record<string, string> | undefined {
  try {
    return parseReplayVariablesText(text)
  } catch {
    return undefined
  }
}

function formatReplayDiagnosisCategory(category?: string) {
  return (category || 'diagnosis').replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
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

interface ReplayOptionsState {
  groupName: string
  ignoreThinkTime: boolean
  startPaused: boolean
  breakOnFailure: boolean
  logLevel: ScriptDebugLogLevel
  variablesText: string
}

interface ReplayActivityItem {
  id: string
  kind: 'start' | 'event' | 'complete' | 'error'
  title: string
  detail?: string
  timestamp: string
  success?: boolean
  changedVariables?: string[]
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

interface AssistantChangeLogItem {
  id: string
  summary: string
  title: string
}

interface EditorHistory {
  past: ScriptData[]
  future: ScriptData[]
}

type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition


interface DataSourceHistory {
  past: DataSourceConfig[]
  future: DataSourceConfig[]
}

interface CandidateOccurrence {
  actionIdx: number
  field: 'target' | 'body' | 'header'
  headerKey?: string
}

interface ParameterCandidate {
  id: string
  keyHint: string
  value: string
  varName: string
  occurrences: CandidateOccurrence[]
  selected: boolean
}

function codePathForParameterCandidateOccurrence(occurrence?: CandidateOccurrence | null) {
  if (!occurrence) return ''
  if (occurrence.field === 'header' && occurrence.headerKey) {
    return `script.actions[${occurrence.actionIdx}].headers.${occurrence.headerKey}`
  }
  return `script.actions[${occurrence.actionIdx}].${occurrence.field}`
}

function formatParameterCandidateOccurrence(occurrence?: CandidateOccurrence | null) {
  if (!occurrence) return 'No occurrence selected'
  const field = occurrence.field === 'header' && occurrence.headerKey
    ? `header ${occurrence.headerKey}`
    : occurrence.field
  return `Action ${occurrence.actionIdx + 1} / ${field}`
}

function normalizeOccurrenceIndex(count: number, index: number) {
  if (count <= 0) return 0
  return ((index % count) + count) % count
}

function offsetForLine(text: string, lineNo: number) {
  const targetLine = Math.max(1, lineNo)
  let currentLine = 1
  let offset = 0
  while (currentLine < targetLine && offset < text.length) {
    const nextBreak = text.indexOf('\n', offset)
    if (nextBreak === -1) return text.length
    offset = nextBreak + 1
    currentLine += 1
  }
  return offset
}

function offsetsForLineRange(text: string, range: LineRange): ScriptCodeMatchRange {
  const from = offsetForLine(text, range.startLine)
  const nextLineOffset = offsetForLine(text, range.endLine + 1)
  return {
    from,
    to: nextLineOffset > from ? nextLineOffset - 1 : text.length,
  }
}

function escapedJsonStringContent(value: string) {
  return JSON.stringify(value).slice(1, -1)
}

function occurrenceOrdinalForPath(candidate: ParameterCandidate, occurrenceIndex: number, path: string) {
  let ordinal = 0
  for (let index = 0; index < occurrenceIndex; index += 1) {
    if (codePathForParameterCandidateOccurrence(candidate.occurrences[index]) === path) {
      ordinal += 1
    }
  }
  return ordinal
}

function findCandidateLiteralRangeInJson(
  jsonText: string,
  lineRange: LineRange | null,
  candidate: ParameterCandidate | null,
  occurrenceIndex: number,
): ScriptCodeMatchRange[] {
  if (!lineRange || !candidate?.value) return []
  const activeOccurrence = candidate.occurrences[occurrenceIndex]
  const activePath = codePathForParameterCandidateOccurrence(activeOccurrence)
  const ordinal = occurrenceOrdinalForPath(candidate, occurrenceIndex, activePath)
  const offsets = offsetsForLineRange(jsonText, lineRange)
  const segment = jsonText.slice(offsets.from, offsets.to)
  const needle = escapedJsonStringContent(candidate.value)
  if (!needle) return []
  let matchOffset = -1
  let searchFrom = 0
  for (let index = 0; index <= ordinal; index += 1) {
    matchOffset = segment.indexOf(needle, searchFrom)
    if (matchOffset < 0) break
    searchFrom = matchOffset + Math.max(1, needle.length)
  }
  if (matchOffset < 0 && ordinal > 0) {
    matchOffset = segment.indexOf(needle)
  }
  if (matchOffset < 0) return []
  return [{ from: offsets.from + matchOffset, to: offsets.from + matchOffset + needle.length }]
}

function formatKeyValueLines(values?: Record<string, string>) {
  return Object.entries(values || {}).map(([key, value]) => `${key}=${value}`).join('\n')
}

function parseKeyValueLines(value: string) {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .reduce<Record<string, string>>((acc, line) => {
      const separator = line.indexOf('=')
      if (separator === -1) {
        acc[line] = ''
        return acc
      }
      const key = line.slice(0, separator).trim()
      const itemValue = line.slice(separator + 1).trim()
      if (key) acc[key] = itemValue
      return acc
    }, {})
}

function debugBreakpointKey(phase: string, actionIndex: number, requestIndex?: number): string {
  return requestIndex == null ? `${phase}:${actionIndex}` : `${phase}:${actionIndex}:${requestIndex}`
}

function normalizeDebugPhase(phase: string): ScriptDebugBreakpoint['phase'] {
  return phase === 'init' || phase === 'teardown' ? phase : 'action'
}

// humanizeStepSegment turns a static step-path segment into a readable label,
// e.g. "requests[2]" -> "Request 3", "then[0]" -> "Then 1", "loop[1]" -> "Loop step 2".
function humanizeStepSegment(seg: string): string {
  const m = /^(\w+)\[(\d+)\]$/.exec(seg)
  if (!m) return seg
  const n = Number(m[2]) + 1
  switch (m[1]) {
    case 'requests': return `Request ${n}`
    case 'then': return `Then ${n}`
    case 'else': return `Else ${n}`
    case 'loop': return `Loop step ${n}`
    default: return `${m[1]} ${n}`
  }
}

// normalizeStoredBreakpoints coerces persisted breakpoints, dropping malformed
// entries and stripping invalid sub-request indices (e.g. legacy negatives).
function normalizeStoredBreakpoints(raw: unknown): ScriptDebugBreakpoint[] {
  if (!Array.isArray(raw)) return []
  const out: ScriptDebugBreakpoint[] = []
  for (const entry of raw) {
    if (!entry || typeof entry !== 'object') continue
    const e = entry as Record<string, unknown>
    const actionIndex = Number(e.action_index)
    if (!Number.isInteger(actionIndex) || actionIndex < 0) continue
    const bp: ScriptDebugBreakpoint = { phase: normalizeDebugPhase(String(e.phase ?? 'action')), action_index: actionIndex }
    if (Number.isInteger(e.request_index) && (e.request_index as number) >= 0) bp.request_index = e.request_index as number
    if (Array.isArray(e.step_path) && e.step_path.every(s => typeof s === 'string')) bp.step_path = e.step_path as string[]
    out.push(bp)
  }
  return out
}

function useEditorDialogRef(onClose: () => void) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose })
  return dialogRef
}

// formatDebugLeafSuffix renders the sub-request / sub-step position of a paused
// leaf (e.g. " · request 43/78", " · loop iter 2"). Empty for a plain action.
function formatDebugLeafSuffix(leaf: { request_index?: number; request_count?: number; iteration?: number; iteration_path?: number[] } | null | undefined): string {
  if (!leaf) return ''
  const parts: string[] = []
  if (leaf.request_index != null) {
    parts.push(leaf.request_count ? `request ${leaf.request_index + 1}/${leaf.request_count}` : `request ${leaf.request_index + 1}`)
  }
  if (leaf.iteration_path && leaf.iteration_path.length) parts.push(`loop iter ${leaf.iteration_path.join('·')}`)
  else if (leaf.iteration != null) parts.push(`loop iter ${leaf.iteration}`)
  return parts.length ? ` · ${parts.join(' · ')}` : ''
}


// formatConditionEvidence renders a control_if / control_loop predicate's
// evaluated operands for the debugger rail and inspector, e.g.
// "admin eq admin → true" or "int(var.i) < 3 → false".
function formatConditionEvidence(evidence: ConditionEvidence): string {
  const result = evidence.result ? 'true' : 'false'
  if (evidence.expr && evidence.expr.trim()) return `${evidence.expr} → ${result}`
  const op = evidence.op || 'eq'
  const left = evidence.left ?? ''
  const unary = op === 'exists' || op === 'not_exists' || op === 'truthy' || op === 'falsy' || op === 'empty'
  if (unary) return `${left} ${op} → ${result}`
  return `${left} ${op} ${evidence.right ?? ''} → ${result}`
}

function debugLogLevelWeight(level: ScriptDebugLogLevel | 'all'): number {
  switch (level) {
    case 'error': return 0
    case 'warn': return 1
    case 'info': return 2
    case 'debug': return 3
    case 'trace': return 4
    default: return 5
  }
}

function formatDebugLogEntry(entry: ScriptDebugLogEntry): string {
  const location = entry.phase ? ` ${entry.phase}[${entry.action_index ?? '-'}]` : ''
  const raw = entry.raw ? `\n${entry.raw}` : ''
  return `[${formatReplayTime(entry.timestamp)}] ${entry.level.toUpperCase()} ${entry.category}${location} ${entry.message}${raw}`
}

function formatLineList(values?: string[]) {
  return (values || []).join('\n')
}

function parseLineList(value: string) {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
}

function formatContentChecks(value?: ContentCheckConfig[]) {
  return (value || [])
    .map(check => `${check.text || ''}|${check.scope || 'any_response'}|${check.fail_if_found === false ? 'false' : 'true'}`)
    .join('\n')
}

function parseContentChecks(value: string): ContentCheckConfig[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [text, scope, failIfFound] = line.split('|').map(part => part.trim())
      return {
        text,
        scope: scope || 'any_response',
        fail_if_found: failIfFound ? failIfFound !== 'false' : true,
      }
    })
}

function formatDFEChain(value?: DFETransform[]) {
  return (value || [])
    .map((transform) => {
      const type = transform.type?.trim()
      if (!type) return ''
      const options = Object.entries(transform.options || {})
        .filter(([key, optionValue]) => key.trim() && optionValue)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, optionValue]) => `${key}=${optionValue}`)
      return [type, ...options].join(' ')
    })
    .filter(Boolean)
    .join('\n')
}

const SOAP11EnvelopeNamespace = 'http://schemas.xmlsoap.org/soap/envelope/'
const SOAP12EnvelopeNamespace = 'http://www.w3.org/2003/05/soap-envelope'

function defaultSOAPEnvelopeNamespace(version?: string) {
  return version === '1.2' ? SOAP12EnvelopeNamespace : SOAP11EnvelopeNamespace
}

function syncSOAPEnvelopeNamespace(version: string, namespaces?: Record<string, string>, previousVersion?: string) {
  const nextNamespaces = { ...(namespaces || {}) }
  const currentEnvelope = nextNamespaces.soapenv?.trim()
  const previousEnvelope = defaultSOAPEnvelopeNamespace(previousVersion)
  if (!currentEnvelope || currentEnvelope === previousEnvelope) {
    nextNamespaces.soapenv = defaultSOAPEnvelopeNamespace(version)
  }
  return nextNamespaces
}

function parseDFEChain(value: string): DFETransform[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [type, ...optionTokens] = line.split(/\s+/)
      const options = optionTokens.reduce<Record<string, string>>((acc, token) => {
        const separator = token.indexOf('=')
        if (separator <= 0 || separator === token.length - 1) {
          return acc
        }
        const key = token.slice(0, separator).trim()
        const optionValue = token.slice(separator + 1).trim()
        if (!key || !optionValue) {
          return acc
        }
        acc[key] = optionValue
        return acc
      }, {})

      return Object.keys(options).length > 0 ? { type, options } : { type }
    })
}

interface ActionEditorParameterRequest {
  selectedText: string
  requestedName: string
  existingName?: string | null
  parameterType?: string
  typeConfig?: Partial<ParameterConfig>
  reuseExisting?: boolean
}

export interface ActionEditorSelectionState {
  start: number
  end: number
  selectedText: string
  x: number
  y: number
  existingName: string | null
}

type ParameterDraftSource = 'action' | 'json'

export interface ActionEditorParameterDraft {
  source: ParameterDraftSource
  phase?: ScriptActionPhase
  actionIndex: number
  start: number
  end: number
  selectedText: string
  existingName: string | null
  requestedName: string
  seedValue: string
  // Set when the user chose the "Built-in Function" branch of the Parameterize dialog; carries
  // the runtime parameter type (random_number, current_iteration, …) and its inline config.
  parameterType?: string
  typeConfig?: Partial<ParameterConfig>
  replacementRanges?: Array<{ from: number; to: number }>
  reuseExisting?: boolean
}

interface JsonParameterOccurrenceReviewState {
  draft: ActionEditorParameterDraft
  occurrences: JsonStringOccurrence[]
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
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

function parseActionStepSegment(segment: string): { name: 'requests' | 'then' | 'else' | 'loop'; index: number } | null {
  const match = segment.match(/^(requests|then|else|loop)\[(\d+)\]$/)
  if (!match) return null
  return { name: match[1] as 'requests' | 'then' | 'else' | 'loop', index: Number.parseInt(match[2], 10) }
}

function resolveNestedAction(root: Action | undefined, stepPath?: string[]): Action | null {
  let current = root
  for (const segment of stepPath || []) {
    const parsed = parseActionStepSegment(segment)
    if (!parsed || !current) return null
    if (parsed.name === 'requests') {
      // Resolve the RAW request object (not the whitelisted httpSequenceRequests
      // view) so the scoped JSON and any saved block preserve every original field.
      current = rawHttpSequenceRequest(current, parsed.index) ?? undefined
    } else if (parsed.name === 'else') {
      current = current.control?.else?.[parsed.index]
    } else {
      current = current.control?.steps?.[parsed.index]
    }
  }
  return current || null
}

function jsonPathForActionStepPath(stepPath?: string[]) {
  return (stepPath || []).map(segment => {
    const parsed = parseActionStepSegment(segment)
    if (!parsed) return ''
    if (parsed.name === 'requests') return `.params.requests[${parsed.index}]`
    if (parsed.name === 'else') return `.control.else[${parsed.index}]`
    return `.control.steps[${parsed.index}]`
  }).join('')
}

function authorFocusKey(focus: Pick<AuthorCodeFocus, 'phase' | 'actionIndex' | 'stepPath'>) {
  const base = `${focus.phase}:${focus.actionIndex}`
  return focus.stepPath?.length ? `${base}#${focus.stepPath.join('/')}` : base
}

function authorFocusFromKey(key: string): AuthorCodeFocus | null {
  const match = key.match(/^(init|action|teardown):(\d+)(?:(:|#|\|)(.+))?$/)
  if (!match) return null
  const actionIndex = Number.parseInt(match[2], 10)
  if (!Number.isInteger(actionIndex) || actionIndex < 0) return null
  const suffixType = match[3]
  const suffix = match[4] || ''
  const stepPath = suffixType === ':'
    ? [`requests[${Number.parseInt(suffix, 10)}]`]
    : suffix
      ? suffix.split('/').filter(Boolean)
      : undefined
  return {
    phase: match[1] as ScriptActionPhase,
    actionIndex,
    stepPath,
    key,
  }
}

function codePathForAuthorFocus(focus?: AuthorCodeFocus | null) {
  if (!focus) return ''
  return `${codePathForDebugRow(focus)}${jsonPathForActionStepPath(focus.stepPath)}`
}

function resolveAuthorJsonScope(script: ScriptData, focus?: AuthorCodeFocus | null): AuthorJsonScope | null {
  if (!focus) return null
  const root = scriptActionsForPhase(script, focus.phase)[focus.actionIndex]
  const action = resolveNestedAction(root, focus.stepPath)
  if (!action) return null
  const subAction = Boolean(focus.stepPath?.length)
  return {
    action,
    actionIndex: focus.actionIndex,
    key: focus.key || authorFocusKey(focus),
    label: focus.label || actionRowLabel(action),
    path: codePathForAuthorFocus(focus),
    phase: focus.phase,
    subAction,
  }
}

function resolveAuthorJsonScopeByKey(script: ScriptData, key: string): AuthorJsonScope | null {
  return resolveAuthorJsonScope(script, authorFocusFromKey(key))
}

function formatScriptProtocolLabel(protocol: string): string {
  return SCRIPT_PROTOCOL_OPTIONS.find(option => option.value === protocol)?.label || protocol.toUpperCase()
}

function findActionIndexByName(actions: ScriptActionDefinition[], targetName?: string | null): number | null {
	const normalizedTarget = targetName?.trim()
	if (!normalizedTarget) return null

	const exactMatch = actions.findIndex(action => action.name?.trim() === normalizedTarget)
	if (exactMatch >= 0) return exactMatch

	const normalizedLower = normalizedTarget.toLowerCase()
	const caseInsensitiveMatch = actions.findIndex(action => action.name?.trim().toLowerCase() === normalizedLower)
	return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

function findActionIndexByImportedLocation(actions: ScriptActionDefinition[], location?: string | null): number | null {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return null

  const match = normalizedLocation.match(/^(?:Step|Request)\s+(\d+)$/i)
  if (!match) return null

  const actionIndex = Number.parseInt(match[1], 10) - 1
  if (!Number.isInteger(actionIndex) || actionIndex < 0 || actionIndex >= actions.length) {
    return null
  }

  return actionIndex
}

function formatImportedLocationLabel(actions: ScriptActionDefinition[], location?: string | null, actionIndex?: number | null): string {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return 'Unknown action'
  if (actionIndex === null || actionIndex === undefined || actionIndex < 0 || actionIndex >= actions.length) {
    return normalizedLocation
  }

  const actionName = actions[actionIndex]?.name?.trim()
  return actionName ? `${normalizedLocation}: ${actionName}` : normalizedLocation
}

function findDataSourceIndexByName(dataSources: ScriptDataSource[] | undefined, targetName?: string | null): number | null {
  const normalizedTarget = targetName?.trim()
  if (!normalizedTarget) return null

  const exactMatch = (dataSources || []).findIndex(source => source.name?.trim() === normalizedTarget)
  if (exactMatch >= 0) return exactMatch

  const normalizedLower = normalizedTarget.toLowerCase()
  const caseInsensitiveMatch = (dataSources || []).findIndex(source => source.name?.trim().toLowerCase() === normalizedLower)
  return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

function normalizeParameterDataLookup(value?: string | null): string {
  return (value || '').trim().toLowerCase()
}

function findFirstParameterIndexByDataSource(parameters: ParameterConfig[] | undefined, sourceName?: string | null): number | null {
  const normalizedSourceName = normalizeParameterDataLookup(sourceName)
  if (!normalizedSourceName) return null

  const parameterIndex = (parameters || []).findIndex(parameter =>
    normalizeParameterDataLookup(parameter.data_source) === normalizedSourceName,
  )
  return parameterIndex >= 0 ? parameterIndex : null
}

function getImportedParameterMappingValue(values: Record<string, string>, keys: string[]): string {
  const targetKeys = new Set(keys.map(key => key.toLowerCase()))
  const match = Object.entries(values).find(([key]) => targetKeys.has(key.trim().toLowerCase()))
  return match?.[1]?.trim() || ''
}

function sanitizeImportedParameterDataSourceName(dataFile?: string | null): string {
  const normalizedFile = dataFile?.trim()
  if (!normalizedFile) return 'parameter_data'

  const baseName = normalizedFile.split(/[\\/]/).pop() || normalizedFile
  const withoutExtension = baseName.replace(/\.[^.]+$/, '')
  const normalized = withoutExtension
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')

  return normalized || 'parameter_data'
}

function findImportedParameterMappingDataSourceIndex(dataSources: ScriptDataSource[] | undefined, values: Record<string, string>): number | null {
  const dataFile = getImportedParameterMappingValue(values, ['table', 'filename', 'file', 'datafile', 'sourcefile'])
  if (!dataFile) return null

  return findDataSourceIndexByName(dataSources, sanitizeImportedParameterDataSourceName(dataFile))
}

function getImportedRuntimeSectionId(area?: string | null): RuntimeSettingsSectionId | null {
  const normalizedArea = area?.trim().toLowerCase()
  switch (normalizedArea) {
    case 'think time':
      return 'think'
    case 'pacing':
      return 'pacing'
    case 'run logic':
      return 'runlogic'
    case 'error handling':
      return 'errors'
    case 'logging':
      return 'log'
    case 'http':
      return 'http'
    case 'web http/html':
    case 'web runtime':
      return 'web_runtime'
    case 'soap':
    case 'soap settings':
      return 'soap'
    case 'java http':
    case 'java over http':
      return 'java_http'
    case 'jms':
      return 'jms'
    case 'websocket':
      return 'websocket'
    case 'grpc':
      return 'grpc'
    case 'database':
    case 'database runtime':
      return 'database'
    case 'network':
    case 'network runtime':
      return 'network'
    default:
      return null
  }
}

type DataSourceDelimiterChoice = 'auto' | 'comma' | 'tab'
type DataSourceSortDirection = 'asc' | 'desc'

const dataSourceSortCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

function getDataSourceDelimiterChoice(source: DataSourceConfig): DataSourceDelimiterChoice {
  const rawDelimiter = source.delimiter || ''
  const delimiter = rawDelimiter === '\t' ? '\t' : rawDelimiter.trim()
  const normalized = delimiter.toLowerCase()
  if (!delimiter || normalized === 'auto') return 'auto'
  if (normalized === 'comma') return 'comma'
  if (delimiter === '\t' || delimiter === '\\t' || normalized === 'tab') return 'tab'
  return delimiter === ',' && inferDelimiter(source) === '\t' ? 'auto' : 'comma'
}

function getDataSourceDelimiterValue(choice: DataSourceDelimiterChoice): string {
  if (choice === 'auto') return 'auto'
  return choice === 'tab' ? 'tab' : 'comma'
}

function formatDataSourceDelimiterLabel(source: DataSourceConfig): string {
  const choice = getDataSourceDelimiterChoice(source)
  const detected = inferDelimiter(source) === '\t' ? 'Tab' : 'Comma'
  return choice === 'auto' ? `Auto: ${detected}` : detected
}

function compareDataSourceCellValues(first: string | undefined, second: string | undefined): number {
  const normalizedFirst = (first || '').trim()
  const normalizedSecond = (second || '').trim()
  return dataSourceSortCollator.compare(normalizedFirst, normalizedSecond)
}

function formatDataSourceColumnLabel(column: string | undefined, colIdx: number): string {
  const normalized = (column || '').trim()
  return normalized || `Column ${colIdx + 1}`
}

function dataSourceRowMatchesFilter(
  row: string[],
  query: string,
  filterColumn: number | 'all',
): boolean {
  const normalizedQuery = query.trim().toLowerCase()
  if (!normalizedQuery) return true
  const values = filterColumn === 'all' ? row : [row[filterColumn] || '']
  return values.some(value => (value || '').toLowerCase().includes(normalizedQuery))
}

function upsertReplayActivity(items: ReplayActivityItem[], next: ReplayActivityItem): ReplayActivityItem[] {
  const index = items.findIndex(item => item.id === next.id)
  if (index === -1) return [...items, next]
  const updated = [...items]
  updated[index] = next
  return updated
}

interface ReplayScreenshotSelection {
  alt: string
  src: string
}


export default function ScriptEditor({ scriptId, initialScript, initialFocusedActionName, initialFocusedSection, initialFocusedDataSourceName, initialFocusedRuntimeArea, projectId, folderId, onSave, onClose }: Props) {
  const api = useApi()
  const [scriptRecordingStatus, setScriptRecordingStatus] = useState<ScriptRecordingStatus | null>(null)
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
  const {
    activeAction,
    activeReplaySessionId,
    browserReplaySession,
    copiedReplaySnapshotSection,
    dataEditorMode,
    dataSourceHistory,
    error,
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
  const [focusParameterIdx, setFocusParameterIdx] = useState<number | null>(null)
  const [selectedParameterIdx, setSelectedParameterIdx] = useState<number | null>(null)
  // Holds the pending selection while the Parameterize choice dialog (File vs Built-in Function)
  // is open. Cleared once the user picks a branch.
  const [parameterizeChoice, setParameterizeChoice] = useState<ActionEditorParameterDraft | null>(null)
  const [existingParameterChoice, setExistingParameterChoice] = useState<ActionEditorParameterDraft | null>(null)
  const [jsonOccurrenceReview, setJsonOccurrenceReview] = useState<JsonParameterOccurrenceReviewState | null>(null)
  const [authorCodeFocus, setAuthorCodeFocus] = useState<AuthorCodeFocus | null>(null)
  const [showFullScriptJson, setShowFullScriptJson] = useState(false)
  const [outlineActionListCollapsed, setOutlineActionListCollapsed] = useState(false)
  const [outlineActionFilter, setOutlineActionFilter] = useState('')
  const [outlineParameterListCollapsed, setOutlineParameterListCollapsed] = useState(false)
  const [debugActionListCollapsed, setDebugActionListCollapsed] = useState(false)
  const [debugParameterListCollapsed, setDebugParameterListCollapsed] = useState(false)
  // Fresh import drafts (no scriptId yet) want the import-review aids expanded for verification;
  // long-lived edits of a saved script collapse them by default to keep the outline focused.
  const [importReviewCollapsed, setImportReviewCollapsed] = useState(Boolean(scriptId))
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
  const [selectedOutlineParameterName, setSelectedOutlineParameterName] = useState<string | null>(null)
  // On the Parameters companion page, toggles between the selected parameter's
  // details and the auto-parameterization view (without dropping the selection).
  const [parameterPaneShowAuto, setParameterPaneShowAuto] = useState(false)
  const [scriptJsonSidebarTab, setScriptJsonSidebarTab] = useState<ScriptJsonSidebarTab>(SCRIPT_JSON_DEFAULT_COMPANION_TAB)
  const [activeAutoParameterCandidateId, setActiveAutoParameterCandidateId] = useState<string | null>(null)
  const [activeAutoParameterOccurrenceIndex, setActiveAutoParameterOccurrenceIndex] = useState(0)
  const [jsonLineJumpRequest, setJsonLineJumpRequest] = useState<ScriptCodeLineJumpRequest | null>(null)
  const [scriptCodeScrollSnapshot, setScriptCodeScrollSnapshot] = useState<ScriptCodeScrollSnapshot>(SCRIPT_CODE_SCROLL_EMPTY)
  const [scriptCodeScrollController, setScriptCodeScrollController] = useState<ScriptCodeScrollController | null>(null)
  const isBrowserScript = script.protocol === 'browser'
  const isHttpScript = script.protocol === 'http'
  const isJmsScript = script.protocol.startsWith('jms')
  const isJavaHttpScript = script.protocol === 'java_http'
  const isSoapScript = script.protocol === 'soap'
  const isWebSocketScript = script.protocol === 'websocket'
  const isGrpcScript = script.protocol === 'grpc'
  const isDatabaseScript = script.protocol === 'database'
  const isNetworkScript = script.protocol === 'network'
  const isHttpTransportScript = isHttpScript || script.protocol === 'https' || isJavaHttpScript || isSoapScript || isBrowserScript
  const canEditProtocol = !scriptId && !script.metadata?.import && !scriptHasAuthoredActions(script)
  // True when editing a saved script (has an id) vs. authoring a brand-new draft. Drives
  // context-aware sidebar emphasis: scaffolding aids lead for new scripts, review/parameter
  // aids lead when editing.
  const isEditingExisting = Boolean(scriptId)
  const httpProxyMode = script.config.http?.proxy_mode || 'none'
  const httpProxySummary = httpProxyMode === 'system'
    ? 'System proxy'
    : httpProxyMode === 'custom' && script.config.http?.proxy?.url
      ? 'Proxy configured'
      : 'Default'
  const thinkTimeLabel = script.config.think_time?.ignore
    ? 'Disabled'
    : `${script.config.think_time?.type || 'fixed'} / ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`
  const runLogicLabel = script.config.run_logic?.global_iterations
    ? `${script.config.run_logic.global_iterations} shared iterations`
    : script.config.run_logic?.max_transactions
      ? `${script.config.run_logic.max_transactions} max transactions`
      : script.config.run_logic?.iterations
        ? `${script.config.run_logic.iterations} iterations`
        : 'Run until duration'
  const logLevelLabel = script.config.log?.level || 'standard'
  const outlineSummary = useMemo(() => buildScriptOutlineSummary(script), [script])
  const scriptPhaseActionRows = useMemo(() => buildScriptPhaseActionRows(script), [script])
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
  const parameterReferenceCounts = useMemo(() => {
    const counts = new Map<string, number>()
    outlineSummary.actions.forEach(action => {
      action.parameterRefs.forEach(ref => counts.set(ref, (counts.get(ref) || 0) + 1))
    })
    return counts
  }, [outlineSummary.actions])
  const selectedOutlineParameterExists = Boolean(
    selectedOutlineParameterName
    && (script.parameters || []).some(parameter => parameter.name === selectedOutlineParameterName),
  )
  const parameterMatchRanges = useMemo(
    () => selectedOutlineParameterExists && selectedOutlineParameterName
      ? findParameterReferencesInJson(jsonText, selectedOutlineParameterName)
      : [],
    [jsonText, selectedOutlineParameterExists, selectedOutlineParameterName],
  )
  const selectedOutlineParameterIndex = useMemo(
    () => (script.parameters || []).findIndex(parameter => parameter.name === selectedOutlineParameterName),
    [script.parameters, selectedOutlineParameterName],
  )
  const selectedOutlineParameter = useMemo(
    () => (selectedOutlineParameterIndex >= 0 ? (script.parameters || [])[selectedOutlineParameterIndex] : null),
    [script.parameters, selectedOutlineParameterIndex],
  )
  const selectedParameterReferenceLines = useMemo(() => {
    if (parameterMatchRanges.length === 0) return [] as number[]
    const lines = new Set<number>()
    for (const range of parameterMatchRanges) lines.add(jsonText.slice(0, range.from).split('\n').length)
    return Array.from(lines).sort((a, b) => a - b)
  }, [parameterMatchRanges, jsonText])
  // The selected file/list parameter's data file: its name (path) and, for inline
  // data sources, the row and unique-value counts. null when there is no source.
  const selectedParameterDataInfo = useMemo((): { fileName: string; rows: number | null; unique: number | null } | null => {
    const parameter = selectedOutlineParameter
    if (!parameter || !parameter.data_source) return null
    const sourceIndex = findDataSourceIndexByName(script.data_sources, parameter.data_source)
    const source = sourceIndex !== null ? script.data_sources?.[sourceIndex] : null
    const fileName = source?.path || source?.name || parameter.data_source || ''
    if (!source) return { fileName, rows: null, unique: null }
    const parsed = parseInlineData(source)
    if (parsed.rows.length === 0) return { fileName, rows: null, unique: null }
    const columnIndex = parameter.column ? parsed.columns.findIndex(column => column === parameter.column) : -1
    const unique = countUniqueColumnValues(parsed, columnIndex >= 0 ? [columnIndex] : [])
    return { fileName, rows: parsed.rows.length, unique }
  }, [selectedOutlineParameter, script.data_sources])
  // Sibling parameters this one can be pinned to the same data row as.
  const selectedParameterSameLineOptions = useMemo(() => {
    const options = getSameLineParameterOptions(script.parameters || [], selectedOutlineParameterIndex)
    return selectedOutlineParameter ? getSameLineParameterSelectOptions(selectedOutlineParameter, options) : options
  }, [script.parameters, selectedOutlineParameterIndex, selectedOutlineParameter])
  const activeAutoParameterCandidate = useMemo(
    () => parameterCandidates.find(candidate => candidate.id === activeAutoParameterCandidateId) || parameterCandidates[0] || null,
    [activeAutoParameterCandidateId, parameterCandidates],
  )
  const activeAutoParameterOccurrenceCount = activeAutoParameterCandidate?.occurrences.length || 0
  const normalizedAutoParameterOccurrenceIndex = normalizeOccurrenceIndex(
    activeAutoParameterOccurrenceCount,
    activeAutoParameterOccurrenceIndex,
  )
  const activeAutoParameterOccurrence = activeAutoParameterCandidate
    ? activeAutoParameterCandidate.occurrences[normalizedAutoParameterOccurrenceIndex] || null
    : null
  const autoParameterSidebarActive = scriptJsonSidebarTab === 'auto-params' && (!selectedOutlineParameterExists || parameterPaneShowAuto)
  const activeAutoParameterPath = autoParameterSidebarActive
    ? codePathForParameterCandidateOccurrence(activeAutoParameterOccurrence)
    : ''
  useEffect(() => {
    if (parameterCandidates.length === 0) {
      setActiveAutoParameterCandidateId(null)
      setActiveAutoParameterOccurrenceIndex(0)
      return
    }
    setActiveAutoParameterCandidateId(current => (
      current && parameterCandidates.some(candidate => candidate.id === current)
        ? current
        : parameterCandidates[0].id
    ))
  }, [parameterCandidates])
  useEffect(() => {
    if (!activeAutoParameterCandidate) return
    setActiveAutoParameterOccurrenceIndex(index => normalizeOccurrenceIndex(activeAutoParameterCandidate.occurrences.length, index))
  }, [activeAutoParameterCandidate])
  const importMetadata = scriptHasImportMetadata(script) ? script.metadata?.import : undefined
  const importSummary = getImportSummary(importMetadata)
  const sourceReview = useMemo(() => readSourceReview(importMetadata), [importMetadata])
  const trueClientPromotionReview = useMemo(() => readTrueClientPromotionReview(importMetadata), [importMetadata])
  const objectDescriptorReview = useMemo(() => buildWeb2ObjectDescriptorReview(script, importMetadata), [script, importMetadata])
  const httpConversionReview = buildBrowserHTTPConversionReview(script, replayReport, browserReplaySession)
  const [httpConversionPreview, setHttpConversionPreview] = useState<BrowserToHTTPPreviewResponse | null>(null)
  const [httpConversionGenerating, setHttpConversionGenerating] = useState(false)
  const [httpConversionError, setHttpConversionError] = useState('')
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
  }, [openCenterTab])
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
  const [debugBreakpoints, setDebugBreakpoints] = useState<ScriptDebugBreakpoint[]>([])
  const [debuggerTab, setDebuggerTab] = useState<DebuggerTab>('request')
  const [debuggerSettingsOpen, setDebuggerSettingsOpen] = useState(false)
  const [authorOutlineCollapsed, setAuthorOutlineCollapsed] = useState(false)
  const [authorRightSidebarCollapsed, setAuthorRightSidebarCollapsed] = useState(false)
  const [sourceMappingWorkspaceMaximized, setSourceMappingWorkspaceMaximized] = useState(false)
  const [debugDetailCollapsed, setDebugDetailCollapsed] = useState(false)
  const [debuggerBottomTab, setDebuggerBottomTab] = useState<DebuggerBottomTab>('console')
  const [debugRawLevel, setDebugRawLevel] = useState<ScriptDebugLogLevel | 'all'>('all')
  const [debugRawSearch, setDebugRawSearch] = useState('')
  const [debugRawAutoScroll, setDebugRawAutoScroll] = useState(true)
  const [debugRawClearedAt, setDebugRawClearedAt] = useState(0)
  const [showImportedParameterMappingsDialog, setShowImportedParameterMappingsDialog] = useState(false)
  // "Deep scan (server)" runs the backend parameterization engine over the saved script.
  const [serverParameterization, setServerParameterization] = useState<(Parameterization & { note?: string }) | null>(null)
  const [deepScanning, setDeepScanning] = useState(false)
  const [showMetadataJsonDialog, setShowMetadataJsonDialog] = useState(false)
  const [sourceMappingFocus, setSourceMappingFocus] = useState<{ mappingId: string; requestId: number }>({ mappingId: '', requestId: 0 })
  const debugRawOutputRef = useRef<HTMLDivElement | null>(null)
  const [debugRailFilter, setDebugRailFilter] = useState('')
  const [debugRailFailuresOnly, setDebugRailFailuresOnly] = useState(false)
  // Keys (phase:actionIndex) of http_sequence rows the operator has expanded in
  // the debugger ACTION LIST to reveal their sub-requests.
  const [expandedDebugSequences, setExpandedDebugSequences] = useState<Set<string>>(() => new Set())
  const [debuggerRailWidth, setDebuggerRailWidth] = useState(() => {
    try { const val = window.localStorage.getItem('debuggerRailWidth'); return val ? clampDebuggerPaneSize('rail', parseInt(val, 10) || DEBUGGER_LAYOUT_DEFAULTS.rail) : DEBUGGER_LAYOUT_DEFAULTS.rail } catch { return DEBUGGER_LAYOUT_DEFAULTS.rail }
  })
  const [debuggerVarsWidth, setDebuggerVarsWidth] = useState(() => {
    try { const val = window.localStorage.getItem('debuggerVarsWidth'); return val ? clampDebuggerPaneSize('vars', parseInt(val, 10) || DEBUGGER_LAYOUT_DEFAULTS.vars) : DEBUGGER_LAYOUT_DEFAULTS.vars } catch { return DEBUGGER_LAYOUT_DEFAULTS.vars }
  })
  const [debuggerBottomHeight, setDebuggerBottomHeight] = useState(() => {
    try { const val = window.localStorage.getItem('debuggerBottomHeight'); return val ? clampDebuggerPaneSize('bottom', parseInt(val, 10) || DEBUGGER_LAYOUT_DEFAULTS.bottom) : DEBUGGER_LAYOUT_DEFAULTS.bottom } catch { return DEBUGGER_LAYOUT_DEFAULTS.bottom }
  })
  const [activeDebuggerResize, setActiveDebuggerResize] = useState<DebuggerResizeKind | null>(null)
  // Author outline rail width. null = fit content (CSS fit-content default) until
  // the user drags it, after which a fixed px is remembered.
  const authorRailRef = useRef<HTMLDivElement | null>(null)
  const [authorRailWidth, setAuthorRailWidth] = useState<number | null>(() => {
    try { const val = window.localStorage.getItem('authorRailWidth'); const n = val ? parseInt(val, 10) : NaN; return Number.isFinite(n) ? n : null } catch { return null }
  })
  const [authorRailResizing, setAuthorRailResizing] = useState(false)

  const toggleSourceMappingWorkspaceMaximized = useCallback(() => {
    setSourceMappingWorkspaceMaximized(current => {
      const next = !current
      setAuthorOutlineCollapsed(next)
      setAuthorRightSidebarCollapsed(next)
      return next
    })
  }, [])

  useEffect(() => {
    if (!sourceMappingWorkspaceMaximized) return
    if (centerTabsState.activeTabId === 'imported-metadata' && sourceReview) return
    setSourceMappingWorkspaceMaximized(false)
    setAuthorOutlineCollapsed(false)
    setAuthorRightSidebarCollapsed(false)
  }, [centerTabsState.activeTabId, sourceMappingWorkspaceMaximized, sourceReview])

  useEffect(() => {
    try {
      window.localStorage.setItem('scriptEditorWorkbenchMode', workspaceTab)
      window.localStorage.setItem('debuggerRailWidth', debuggerRailWidth.toString())
      window.localStorage.setItem('debuggerVarsWidth', debuggerVarsWidth.toString())
      window.localStorage.setItem('debuggerBottomHeight', debuggerBottomHeight.toString())
    } catch { /* storage may be unavailable */ }
  }, [workspaceTab, debuggerRailWidth, debuggerVarsWidth, debuggerBottomHeight])
  useEffect(() => {
    setCenterTabsState(readCenterTabsState(centerTabsStorageKey))
    setCenterTabsHydratedKey(centerTabsStorageKey)
    setCenterTabPanelElements({})
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
    if (!selectedOutlineParameterName) return
    if (script.parameters?.some(parameter => parameter.name === selectedOutlineParameterName)) return
    setSelectedOutlineParameterName(null)
  }, [script.parameters, selectedOutlineParameterName])
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
  const formattedScriptJson = useMemo(() => formatScriptJson(script), [script])
  useEffect(() => {
    if (jsonDirty || jsonText === formattedScriptJson) return
    setJsonText(formattedScriptJson)
  }, [formattedScriptJson, jsonDirty, jsonText, setJsonText])
  const debuggerSettingsRef = useRef<HTMLDivElement | null>(null)
  const debuggerSettingsBtnRef = useRef<HTMLButtonElement | null>(null)
  const importedCorrelationFocusTargets = importSummary.correlationSuggestions.map((correlation) => {
    const foundAtActionIndex = findActionIndexByImportedLocation(script.actions, correlation.found_at)
    const foundAtLabel = formatImportedLocationLabel(script.actions, correlation.found_at, foundAtActionIndex)
    const usedAtLocations = correlation.used_at
      .map((location) => ({
        location,
        actionIndex: findActionIndexByImportedLocation(script.actions, location),
        label: formatImportedLocationLabel(script.actions, location, findActionIndexByImportedLocation(script.actions, location)),
      }))

    const usedAtTargets = usedAtLocations
      .filter((target): target is { location: string; actionIndex: number; label: string } => target.actionIndex !== null)

    return {
      correlation,
      foundAtActionIndex,
      foundAtLabel,
      usedAtLocations,
      usedAtTargets,
    }
  })
  const runtimeSettingParitySummary = Object.entries(importSummary.runtimeSettingStatusCounts)
    .filter(([, count]) => count > 0)
    .map(([status, count]) => `${count} ${formatRuntimeSettingStatusLabel(status)}`)
    .join(', ')
  const runtimeSettingReadinessSummary = importSummary.runtimeSettingReadiness
    ? importSummary.runtimeSettingReadiness.summary || `${importSummary.runtimeSettingReadiness.score_pct}% readiness`
    : ''
  const importedParameterFileMappings: ImportedParameterFileMapping[] = importSummary.parameterFileSections.map((section) => {
    const sourceIndex = findImportedParameterMappingDataSourceIndex(script.data_sources, section.values)
    return {
      section,
      sourceName: sourceIndex !== null ? script.data_sources?.[sourceIndex]?.name || null : null,
    }
  })
  useEffect(() => {
    if (importedParameterFileMappings.length > 0) return
    setShowImportedParameterMappingsDialog(false)
  }, [importedParameterFileMappings.length])
  const importedDataSourceSummaries = (script.data_sources || []).map((source, index) => {
    const summary = summarizeInlineData(source)
    const columnDetails = source.first_row_header === false
      ? source.columns || []
      : []
    return {
      name: source.name || `source_${index + 1}`,
      type: source.type || 'csv',
      rows: summary.rows,
      columns: columnDetails.length > 0 ? columnDetails : summary.columns,
    }
  })
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
  const [activeRuntimeSettingsSection, setActiveRuntimeSettingsSection] = useState<RuntimeSettingsSectionId>('script_config')
  const [activeHTTPTransportTab, setActiveHTTPTransportTab] = useState<HTTPTransportTabId>('timeouts')
  const runtimeSettingsSectionIsAvailable = useCallback((sectionId: RuntimeSettingsSectionId): boolean => {
    switch (sectionId) {
      case 'jms':
        return isJmsScript
      case 'websocket':
        return isWebSocketScript
      case 'grpc':
        return isGrpcScript
      case 'database':
        return isDatabaseScript
      case 'network':
        return isNetworkScript
      case 'http':
        return isHttpTransportScript
      case 'soap':
        return isSoapScript
      case 'java_http':
        return isJavaHttpScript
      case 'web_runtime':
        return isHttpScript
      default:
        return true
    }
  }, [isDatabaseScript, isGrpcScript, isHttpScript, isHttpTransportScript, isJavaHttpScript, isJmsScript, isNetworkScript, isSoapScript, isWebSocketScript])
  const runtimeSectionIsActive = (sectionId: RuntimeSettingsSectionId) => activeRuntimeSettingsSection === sectionId
  const selectRuntimeSettingsSection = useCallback((sectionId: RuntimeSettingsSectionId) => {
    setActiveRuntimeSettingsSection(sectionId)
    setOpenSections(prev => {
      const next: Record<string, boolean> = { ...prev, runtime: true }
      RUNTIME_SETTINGS_SECTION_IDS.forEach(id => {
        next[id] = id === sectionId
      })
      return next
    })
  }, [setOpenSections])
  useEffect(() => {
    if (!runtimeSettingsSectionIsAvailable(activeRuntimeSettingsSection)) {
      selectRuntimeSettingsSection('think')
    }
  }, [activeRuntimeSettingsSection, runtimeSettingsSectionIsAvailable, selectRuntimeSettingsSection])
  const openImportedRuntimeSettings = useCallback((area?: string | null) => {
    const sectionId = getImportedRuntimeSectionId(area)
    openCenterTab('runtime-settings')
    if (sectionId) {
      selectRuntimeSettingsSection(sectionId)
      return
    }
    setOpenSections(prev => ({ ...prev, runtime: true }))
  }, [openCenterTab, selectRuntimeSettingsSection, setOpenSections])
  const selectedBrowserEngine = script.config.browser?.engine || 'chromium'
  const [assistantChangeLog, setAssistantChangeLog] = useState<AssistantChangeLogItem[]>([])
  const [editorHistory, setEditorHistory] = useState<EditorHistory>({ past: [], future: [] })
  const [authoringComponents, setAuthoringComponents] = useState<AuthoringComponent[]>([])
  const [, setComponentLibraryStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [validationEvidence, setValidationEvidence] = useState<ValidationEvidence | null>(null)
  const [validationReplayPreflight, setValidationReplayPreflight] = useState<ReplayPreflight | null>(null)
  const [validationSidebarResult, setValidationSidebarResult] = useState<ValidationSidebarResult | null>(null)
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
  const reviewBaselineScript = importBaselineScript || editorHistory.past[editorHistory.past.length - 1] || null
  const draftDiffSummary = useMemo(
    () => (reviewBaselineScript ? buildImportedDraftDiffSummary(reviewBaselineScript, script) : null),
    [reviewBaselineScript, script],
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
  const selectedBrowserProfile = lookupBrowserDeviceProfile(script.config.browser?.device_profile)
  const browserEngineAdvisory = selectedBrowserProfile && selectedBrowserProfile.engine !== selectedBrowserEngine
    ? `${selectedBrowserProfile.name} requires ${selectedBrowserProfile.engine}. Choose a matching engine or switch to a compatible preset.`
    : selectedBrowserEngine === 'webkit'
      ? WEBKIT_INSTALL_ADVISORY
      : ''

  const pushEditorHistory = useCallback((before: ScriptData) => {
    setEditorHistory(previous => ({
      past: [...previous.past, JSON.parse(JSON.stringify(before)) as ScriptData].slice(-30),
      future: [],
    }))
  }, [])

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
  }, [openCenterTab, replaceScriptWithHistory, script, setActiveAction, setError, setSuccess])

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
  }, [script, setError, setJsonDirty, setJsonText, setScript, setSuccess])

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
  }, [script, setError, setJsonDirty, setJsonText, setScript, setSuccess])

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
  }, [importBaselineScript, initialScript, scriptId])
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
      const sectionId = getImportedRuntimeSectionId(initialFocusedRuntimeArea)
      openCenterTab('runtime-settings')
      if (sectionId) {
        selectRuntimeSettingsSection(sectionId)
      } else {
        setOpenSections(prev => ({ ...prev, runtime: true }))
      }
    }

    if (initialFocusedSection === 'parameter-data') {
      openCenterTab('parameter-data')
    }

    const focusedDataSourceIndex = findDataSourceIndexByName(initialScript.data_sources, initialFocusedDataSourceName)
    if (focusedDataSourceIndex !== null) {
      openCenterTab('parameter-data')
      setOpenDataEditor(focusedDataSourceIndex)
    }
  }, [hydrateEditorState, initialFocusedActionName, initialFocusedDataSourceName, initialFocusedRuntimeArea, initialFocusedSection, initialScript, openCenterTab, scriptId, selectRuntimeSettingsSection, setActiveAction, setOpenDataEditor, setOpenSections])

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

  const focusSourceMappingForAction = (index: number) => {
    const mapping = findSourceMappingForAction(sourceReview, script.actions[index], index)
    setSourceMappingFocus(prev => ({
      mappingId: mapping?.id || '',
      requestId: prev.requestId + 1,
    }))
    window.requestAnimationFrame(() => {
      const mappingElement = document.getElementById('imported-source-mapping')
      mappingElement?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    })
  }

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
  }, [api, hydrateEditorState, initialScript, scriptId, setError])

  const handleReplaySocketMessage = useCallback((message: WSMessage) => {
    const msg = message as ReplayWSMessage
    if (!activeReplaySessionId || (msg.replay_session_id !== activeReplaySessionId && msg.debug_session_id !== activeReplaySessionId)) return

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
      const completedAt = msg.session?.completed_at || report?.completed_at || new Date().toISOString()
      if (report) {
        setReplayReport(report)
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
  }, [activeReplaySessionId, setActiveReplaySessionId, setBrowserReplaySession, setDebugDetailCollapsed, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayOptions, setReplayReport, setReplaying])

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
    let shouldSelectFirstAction = false
    pushEditorHistory(script)
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      next.protocol = protocol
      next.config = next.config || { base_url: '' }
      if (protocol === 'soap') {
        next.config.soap = {
          version: next.config.soap?.version || '1.1',
          namespaces: Object.keys(next.config.soap?.namespaces || {}).length > 0
            ? next.config.soap?.namespaces
            : { soapenv: defaultSOAPEnvelopeNamespace(next.config.soap?.version || '1.1') },
          ws_security: next.config.soap?.ws_security,
          fault_handling: {
            fail_on_fault: next.config.soap?.fault_handling?.fail_on_fault ?? true,
          },
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_SOAP_ACTION)), name: 'SOAP_Request' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'java_http') {
        next.config.java_http = {
          codec: next.config.java_http?.codec || 'java_serialized_object_base64',
          helper_mode: next.config.java_http?.helper_mode || 'embedded',
          content_type: next.config.java_http?.content_type || 'application/x-java-serialized-object',
          accept: next.config.java_http?.accept || 'application/x-java-serialized-object',
          body_encoding: next.config.java_http?.body_encoding || 'base64',
          object_schema: next.config.java_http?.object_schema,
          request_mapping: next.config.java_http?.request_mapping,
          response_mapping: next.config.java_http?.response_mapping,
          extractor_type: next.config.java_http?.extractor_type || 'opaque',
          preserve_binary_result: next.config.java_http?.preserve_binary_result ?? true,
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_ACTION)), name: 'Java_HTTP_Request', type: 'POST', method: 'POST' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'jms-solace' || protocol === 'jms-ibmmq') {
        next.config.jms = {
          provider: protocol === 'jms-solace' ? 'solace' : 'ibmmq',
          destination_type: 'queue',
          acknowledgement: 'auto',
          delivery_mode: 'persistent',
          timeout_ms: 5000,
                          mock: true,
                          ...(next.config.jms || {}),
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_JMS_ACTION)), name: 'Publish_Message' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'websocket') {
        next.config.websocket = {
          read_timeout_ms: next.config.websocket?.read_timeout_ms || 30000,
          write_timeout_ms: next.config.websocket?.write_timeout_ms || 10000,
          max_message_size: next.config.websocket?.max_message_size || 1048576,
          subprotocol: next.config.websocket?.subprotocol || '',
          origin: next.config.websocket?.origin || '',
          compression: next.config.websocket?.compression ?? false,
          reconnect: next.config.websocket?.reconnect ?? true,
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_WEBSOCKET_ACTION)), name: 'Connect_WebSocket' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'grpc') {
        next.config.grpc = {
          use_reflection: next.config.grpc?.use_reflection ?? true,
          plain_text: next.config.grpc?.plain_text ?? false,
          timeout_ms: next.config.grpc?.timeout_ms || 30000,
          max_recv_msg_size: next.config.grpc?.max_recv_msg_size || 4194304,
          metadata: next.config.grpc?.metadata || {},
          authority: next.config.grpc?.authority || '',
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_GRPC_ACTION)), name: 'Unary_Call' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'database') {
        next.config.database = {
          driver: next.config.database?.driver || 'sqlite',
          dsn: next.config.database?.dsn || ':memory:',
          dsn_secret: next.config.database?.dsn_secret || '',
          query_timeout_ms: next.config.database?.query_timeout_ms || 30000,
          max_open_conns: next.config.database?.max_open_conns || 1,
          max_idle_conns: next.config.database?.max_idle_conns,
          conn_max_lifetime_ms: next.config.database?.conn_max_lifetime_ms,
          max_rows: next.config.database?.max_rows || 100,
          read_only: next.config.database?.read_only ?? true,
          rollback_on_teardown: next.config.database?.rollback_on_teardown ?? true,
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_DATABASE_ACTION)), name: 'Query_Database' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'network') {
        next.config.network = {
          subprotocol: next.config.network?.subprotocol || 'tcp',
          host: next.config.network?.host || 'localhost',
          address: next.config.network?.address || '',
          port: next.config.network?.port || 80,
          timeout_ms: next.config.network?.timeout_ms || 30000,
          tls: next.config.network?.tls ?? false,
          username: next.config.network?.username || '',
          password: next.config.network?.password || '',
          from: next.config.network?.from || '',
          to: next.config.network?.to || [],
          base_dn: next.config.network?.base_dn || '',
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_NETWORK_ACTION)), name: 'Network_Health' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'browser' && next.actions.length === 0) {
        next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_BROWSER_ACTION)), name: 'Navigate' }]
        shouldSelectFirstAction = true
      }
      return next
    })
    if (shouldSelectFirstAction) selectAuthorAction(0)
  }

  const phaseActionsForScript = (source: ScriptData, phase: ScriptActionPhase): Action[] => (
    scriptActionsForPhase(source, phase) as Action[]
  )

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
      else if (activeAction !== null && activeAction > removedIndex) selectAuthorAction(activeAction - 1)
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

  const addExtractorDefinition = (actionIdx: number, extractor: ScriptExtractorDefinition) => {
    setScript(prev => {
      const actions = [...prev.actions]
      if (actionIdx < 0 || actionIdx >= actions.length) return prev
      const ext = [...(actions[actionIdx].extractors || []), extractor]
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
    setSuccess(`Added extractor "${extractor.name}" to ${script.actions[actionIdx]?.name || `action ${actionIdx + 1}`}.`)
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

  const addSoapNamespace = () => {
    const key = prompt('Namespace prefix:')
    if (!key) return
    setScript(prev => ({
      ...prev,
      config: {
        ...prev.config,
        soap: {
          ...(prev.config.soap || {}),
          namespaces: { ...(prev.config.soap?.namespaces || {}), [key]: '' },
        },
      },
    }))
  }

  const updateSoapNamespace = (oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const namespaces = { ...(prev.config.soap?.namespaces || {}) }
      if (oldKey !== newKey) delete namespaces[oldKey]
      namespaces[newKey] = value
      return {
        ...prev,
        config: {
          ...prev.config,
          soap: {
            ...(prev.config.soap || {}),
            namespaces,
          },
        },
      }
    })
  }

  const removeSoapNamespace = (key: string) => {
    setScript(prev => {
      const namespaces = { ...(prev.config.soap?.namespaces || {}) }
      delete namespaces[key]
      return {
        ...prev,
        config: {
          ...prev.config,
          soap: {
            ...(prev.config.soap || {}),
            namespaces,
          },
        },
      }
    })
  }

  const addJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping') => {
    const key = prompt(field === 'request_mapping' ? 'Request mapping key:' : 'Response mapping key:')
    if (!key) return
    setScript(prev => {
      const mapping = {
        ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}),
        [key]: '',
      }
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const updateJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const mapping = { ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
      if (oldKey !== newKey) delete mapping[oldKey]
      mapping[newKey] = value
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const removeJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', key: string) => {
    setScript(prev => {
      const mapping = { ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
      delete mapping[key]
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const jsonDraftParse = useMemo(() => parseScriptJsonDraft(jsonText), [jsonText])
  const currentDraftScript = jsonDraftParse.ok ? jsonDraftParse.script : script
  const jsonParseMessage = jsonDraftParse.ok ? '' : jsonDraftParse.message
  const jsonStatus: ScriptCodePaneProps['status'] = jsonParseMessage ? 'invalid' : jsonDirty ? 'dirty' : 'synced'
  const jsonStatusMessage = jsonParseMessage ? 'Invalid JSON' : jsonDirty ? 'Unsaved JSON edits' : 'Synced'
  const scriptEditorDirty = jsonDirty || formattedScriptJson !== baselineJsonRef.current
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

  const handleScriptCodeScrollSnapshotChange = useCallback((snapshot: ScriptCodeScrollSnapshot) => {
    setScriptCodeScrollSnapshot(current => (
      sameScriptCodeScrollSnapshot(current, snapshot) ? current : snapshot
    ))
  }, [])

  const handleScriptCodeScrollControllerChange = useCallback((controller: ScriptCodeScrollController | null) => {
    setScriptCodeScrollController(controller)
  }, [])

  const handleJsonSourceMapScrollTopChange = useCallback((scrollTop: number) => {
    scriptCodeScrollController?.scrollToScrollTop(scrollTop)
  }, [scriptCodeScrollController])

  const resolveCurrentScript = useCallback((): ScriptData => {
    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) {
      throw new Error(parsed.message)
    }
    return parsed.script
  }, [jsonText])

  const commitJsonDraft = useCallback((nextScript: ScriptData, message?: string) => {
    setScript(nextScript)
    setJsonText(formatScriptJson(nextScript))
    setJsonDirty(false)
    setError('')
    if (message) setSuccess(message)
  }, [setError, setJsonDirty, setJsonText, setScript, setSuccess])

  const handleApplyJsonDraft = () => {
    setSuccess('')
    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) {
      setError(parsed.message)
      setDebuggerBottomTab('problems')
      return
    }
    commitJsonDraft(parsed.script, 'Applied script JSON to the authoring tools.')
  }

  const handleFormatJsonDraft = () => {
    setSuccess('')
    const parsed = parseScriptJsonDraft(jsonText)
    if (!parsed.ok) {
      setError(parsed.message)
      setDebuggerBottomTab('problems')
      return
    }
    const formatted = formatScriptJson(parsed.script)
    setJsonText(formatted)
    setJsonDirty(formatted !== formattedScriptJson)
    setError('')
  }

  const handleResetJsonDraft = () => {
    setJsonText(formatScriptJson(script))
    setJsonDirty(false)
    setError('')
    setSuccess('Reset script JSON from the structured editor.')
  }

  const handleCodePaneChange = (value: string) => {
    setJsonText(value)
    setJsonDirty(value !== formattedScriptJson)
  }

  const handleSave = async (): Promise<boolean> => {
    setError(''); setSuccess(''); setWarnings([]); setSaving(true)
    setValidationEvidence(null)
    setValidationReplayPreflight(null)
    try {
      const currentScript = resolveCurrentScript()
      let savedScript: StoredScript
      if (scriptId) {
        savedScript = await api.updateScript(scriptId, currentScript)
      } else {
        savedScript = await api.createScript(currentScript, projectId, folderId)
      }
      commitJsonDraft(currentScript)
      markBaseline(currentScript)
      setSuccess('Script saved successfully!')
      onSave?.(savedScript)
      return true
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to save script'))
      if (getErrorMessage(e, '').includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
      return false
    } finally { setSaving(false) }
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
    setError('')
    setSuccess('')
    setWarnings([])
    setValidationEvidence(null)
    setValidationReplayPreflight(null)
    setValidationSidebarResult(null)
    setValidating(true)
    try {
      const currentScript = resolveCurrentScript()
      const res = scriptId && !jsonDirty
        ? await api.validateScript(scriptId)
        : replayConfiguredVariables
          ? await api.validateDraftScript(currentScript, projectId, folderId, replayConfiguredVariables)
          : await api.validateDraftScript(currentScript, projectId, folderId)
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
      const validationWarnings = normalizeWarnings(getErrorWarnings(e))
      setValidationEvidence(null)
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
      setValidating(false)
    }
  }

  const handleGenerateBrowserHTTPPreview = async () => {
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
      setHttpConversionPreview(res)
      setWarnings(normalizeWarnings(res.warnings))
      setSuccess('Generated HTTP draft preview from browser replay evidence.')
    } catch (e: unknown) {
      setHttpConversionPreview(null)
      setWarnings(normalizeWarnings(getErrorWarnings(e)))
      setHttpConversionError(getErrorMessage(e, 'Failed to generate HTTP draft preview'))
      if (getErrorMessage(e, '').includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
    } finally {
      setHttpConversionGenerating(false)
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
  }, [isBrowserScript, openCenterTab])

  const openReplayDebugger = useCallback((openSettings = false) => {
    activateWorkspaceTab('debug')
    if (openSettings) setDebuggerSettingsOpen(true)
  }, [activateWorkspaceTab])

  const handleReplay = useCallback(async (options?: { startPaused?: boolean }) => {
    setError('')
    setSuccess('')
    setWarnings([])
    openReplayDebugger()
    setReplaying(true)
    let debugSessionStarted = false
    try {
      const currentScript = resolveCurrentScript()
      const variables = parseReplayVariablesText(replayOptions.variablesText)
      const replaySessionId = createReplaySessionId()
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

      const res = scriptId
        ? await api.startScriptDebugSession(scriptId, payload)
        : await api.startDraftScriptDebugSession(payload)
      commitJsonDraft(currentScript)
      const replayWarnings = normalizeWarnings(res.warnings)
      setValidationReplayPreflight(res.preflight || res.session?.preflight || null)
      if (res.session) {
        setBrowserReplaySession(res.session)
        if (res.session.report) {
          setReplayReport(res.session.report)
          if (['completed', 'failed', 'stopped'].includes(res.session.status)) {
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
      setActiveReplaySessionId(res.debug_session_id || res.replay_session_id || res.session?.id || replaySessionId)
      if (res.session && ['completed', 'failed', 'stopped'].includes(res.session.status)) {
        setActiveReplaySessionId(null)
        setReplaying(false)
      }
      setWarnings(replayWarnings)
      setSuccess(replayWarnings.length > 0
        ? (currentScript.protocol === 'browser' ? 'Browser replay started with warnings.' : 'Debugger started with warnings.')
        : (currentScript.protocol === 'browser' ? 'Browser replay started.' : 'Debugger started.'))
      debugSessionStarted = true
    } catch (e: unknown) {
      const issues = getErrorIssues(e).join(' | ')
      const replayWarnings = normalizeWarnings(getErrorWarnings(e))
      const message = getErrorMessage(e, 'Replay failed')
      setWarnings(replayWarnings)
      setError(issues || message)
      if (message.includes('Script JSON is invalid')) {
        setDebuggerBottomTab('problems')
      }
      setReplayReport(null)
      setActiveReplaySessionId(null)
      setBrowserReplaySession(null)
      setExpandedReplayBrowserArtifacts({})
      setReplayScreenshotSelection(null)
      setReplaySeedVariables({})
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'error',
        kind: 'error',
        title: 'Replay failed before completion',
        detail: issues || message,
        timestamp: new Date().toISOString(),
        success: false,
      }))
    } finally {
      if (!debugSessionStarted) {
        setReplaying(false)
      }
    }
  }, [api, commitJsonDraft, debugBreakpoints, openReplayDebugger, projectId, replayOptions.groupName, replayOptions.ignoreThinkTime, replayOptions.logLevel, replayOptions.startPaused, replayOptions.breakOnFailure, replayOptions.variablesText, resolveCurrentScript, scriptId, setActiveReplaySessionId, setBrowserReplaySession, setError, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplayScreenshotSelection, setReplaySeedVariables, setReplaying, setSuccess, setWarnings])

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

  const handleDownloadBrowserReplayExport = () => {
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
    downloadTextFile(JSON.stringify(payload, null, 2), `${scriptBase}-${replayBase}-debug.json`)
    setError('')
    setSuccess('Exported debugger session JSON.')
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
      return
    }
    let cancelled = false
    api.getScriptRecordingStatus(scriptId)
      .then(status => { if (!cancelled) setScriptRecordingStatus(status) })
      .catch(() => { if (!cancelled) setScriptRecordingStatus(null) })
    return () => { cancelled = true }
  }, [api, scriptId, workspaceTab, isBrowserScript])
  const replayVariableDiffs = replayReport
    ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events)
    : []
  const replayConfiguredVariables = safeParseReplayVariables(replayOptions.variablesText)
  const replayPreflight = browserReplaySession?.preflight || validationReplayPreflight || buildLocalReplayPreflight(script, replayConfiguredVariables)

  useEffect(() => {
    setValidationReplayPreflight(null)
    setValidationEvidence(null)
    setValidationSidebarResult(null)
  }, [script.actions, script.data_sources, script.name, script.parameters, script.protocol, replayOptions.variablesText])
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
  const debugActionRows = scriptPhaseActionRows
  const stepReplayDisabled = replaying
    ? (!activeReplaySessionId || !isReplayPaused)
    : (saving || !script.name || debugActionRows.length === 0)
  const breakpointSummary = `${debugBreakpoints.length} selected across ${debugActionRows.length} executable action${debugActionRows.length === 1 ? '' : 's'}`
  const debugRawSearchText = debugRawSearch.trim().toLowerCase()
  const debugFilteredLogs = (browserReplaySession?.logs || [])
    .slice(debugRawClearedAt)
    .filter(entry => debugRawLevel === 'all' || debugLogLevelWeight(entry.level) <= debugLogLevelWeight(debugRawLevel))
    .filter(entry => {
      if (!debugRawSearchText) return true
      return formatDebugLogEntry(entry).toLowerCase().includes(debugRawSearchText)
    })
  const debugRawOutputText = debugFilteredLogs.map(formatDebugLogEntry).join('\n\n')
  const debuggerStatusState = replaying
    ? (isReplayPaused ? 'paused' : 'running')
    : replayReport
      ? (replayReport.success ? 'success' : 'error')
      : 'idle'
  // Debugger event source: prefer whichever of the live session snapshot or the
  // client-accumulated report has more events. The session snapshot is
  // authoritative during a paused/live run (and carries sub-request identity),
  // while replayReport is the final record after completion.
  const debugEvents = useMemo(() => {
    const sessionEvents = browserReplaySession?.events
    const reportEvents = replayReport?.events
    if (sessionEvents && reportEvents) return sessionEvents.length >= reportEvents.length ? sessionEvents : reportEvents
    return sessionEvents ?? reportEvents ?? []
  }, [browserReplaySession?.events, replayReport?.events])
  const selectedReplayEventIndex = expandedReplayEvent != null && expandedReplayEvent >= 0 && expandedReplayEvent < debugEvents.length
    ? expandedReplayEvent
    : null
  const selectedReplayEvent = selectedReplayEventIndex != null ? debugEvents[selectedReplayEventIndex] : null
  // Resolve the top-level action row from the selected event's own (phase,
  // action_index) rather than positionally indexing debugActionRows with the flat
  // event index, which diverges for container scripts (http_sequence / loops).
  const selectedDebugRow = useMemo(
    () => resolveDebugRowForEvent(debugActionRows, selectedReplayEvent),
    [debugActionRows, selectedReplayEvent],
  )
  useEffect(() => {
    if (selectedReplayEvent || (replaying && browserReplaySession?.current_action)) {
      setDebugDetailCollapsed(false)
    }
  }, [browserReplaySession?.current_action, replaying, selectedReplayEvent])
  useEffect(() => {
    if (hasReplayVariableInspectorData) {
      setDebugDetailCollapsed(false)
    }
  }, [hasReplayVariableInspectorData])
  const currentDebugAction = browserReplaySession?.current_action
  const currentDebugActionKey = currentDebugAction
    ? debugBreakpointKey(currentDebugAction.phase, currentDebugAction.action_index, currentDebugAction.request_index)
    : null
  // Step-path key of a currently-paused control-flow leaf (no request_index).
  const currentDebugStepKey = currentDebugAction && (currentDebugAction.step_path?.length ?? 0) > 0
    ? debugStepPathKey(currentDebugAction.phase, currentDebugAction.action_index, currentDebugAction.step_path as string[])
    : null
  // Container nodes to auto-expand so the currently-paused leaf is visible: the
  // top-level action plus every nested-container prefix of its step path.
  const currentDebugAncestorKeys = useMemo(() => {
    const keys = new Set<string>()
    if (!currentDebugAction) return keys
    const isLeaf = currentDebugAction.request_index != null || (currentDebugAction.step_path?.length ?? 0) > 0
    if (!isLeaf) return keys
    const base = debugBreakpointKey(currentDebugAction.phase, currentDebugAction.action_index)
    keys.add(base)
    const path = currentDebugAction.step_path ?? []
    for (let i = 1; i < path.length; i++) {
      keys.add(`${base}|${path.slice(0, i).join('/')}`)
    }
    // Also auto-expand the looped step's iteration group (keyed by step path).
    if (path.length > 0) keys.add(debugStepPathKey(currentDebugAction.phase, currentDebugAction.action_index, path))
    return keys
  }, [currentDebugAction])
  const debugRailFilterText = debugRailFilter.trim().toLowerCase()
  // Resolve each replay event by location. Container leaves carry request_index,
  // so a sub-request maps to its own event; plain actions map by (phase, action).
  const eventIndexByLocation = useMemo(() => {
    const map = new Map<string, number>()
    debugEvents.forEach((event, index) => {
      if (typeof event.action_index !== 'number') return
      map.set(debugBreakpointKey(event.phase, event.action_index, event.request_index), index)
    })
    return map
  }, [debugEvents])
  // First event index per top-level action — drives sequence roll-ups and the
  // raw-log "jump to event" affordance (which only knows phase + action_index).
  const debugRowPositionByKey = useMemo(() => {
    const map = new Map<string, number>()
    debugEvents.forEach((event, index) => {
      if (typeof event.action_index !== 'number') return
      const key = debugBreakpointKey(event.phase, event.action_index)
      if (!map.has(key)) map.set(key, index)
    })
    return map
  }, [debugEvents])
  // Control-flow leaves map by static step path; a looped step yields one entry
  // per iteration, so values are arrays of flat event indices in execution order.
  const eventsByStepPath = useMemo(() => {
    const map = new Map<string, number[]>()
    debugEvents.forEach((event, index) => {
      if (typeof event.action_index !== 'number' || !event.step_path || event.step_path.length === 0) return
      const key = debugStepPathKey(event.phase, event.action_index, event.step_path)
      const existing = map.get(key)
      if (existing) existing.push(index)
      else map.set(key, [index])
    })
    return map
  }, [debugEvents])
  // Sequences to force-open while a text/Failures filter is active so matching /
  // failing sub-requests are reachable even if the operator hasn't expanded them.
  const filterForcedDebugSequences = useMemo(() => {
    const keys = new Set<string>()
    if (!debugRailFilterText && !debugRailFailuresOnly) return keys
    // Force-expand a container and every nested-container prefix of an event's
    // step path so a matching/failing leaf is reachable.
    const addAncestors = (phase: string, actionIndex: number, stepPath?: string[]) => {
      const base = debugBreakpointKey(phase, actionIndex)
      keys.add(base)
      const path = stepPath ?? []
      for (let i = 1; i < path.length; i++) keys.add(`${base}|${path.slice(0, i).join('/')}`)
    }
    if (debugRailFailuresOnly) {
      for (const event of debugEvents) {
        if (typeof event.action_index === 'number' && !event.success) addAncestors(event.phase, event.action_index, event.step_path)
      }
    } else {
      for (const row of debugActionRows) {
        if (actionRequestCount(row.action) === 0) continue
        if (httpSequenceRequests(row.action).some(child => `${child.name || ''} ${child.target || ''}`.toLowerCase().includes(debugRailFilterText))) {
          keys.add(debugBreakpointKey(row.phase, row.actionIndex))
        }
      }
    }
    return keys
  }, [debugRailFilterText, debugRailFailuresOnly, debugActionRows, debugEvents])
  const effectiveExpandedDebugSequences = useMemo(() => {
    const next = new Set(expandedDebugSequences)
    currentDebugAncestorKeys.forEach(key => next.add(key))
    filterForcedDebugSequences.forEach(key => next.add(key))
    return next
  }, [expandedDebugSequences, currentDebugAncestorKeys, filterForcedDebugSequences])
  const toggleDebugSequenceExpanded = useCallback((key: string) => {
    setExpandedDebugSequences(prev => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }, [])
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

  useEffect(() => {
    if (!debugRawAutoScroll || !debugRawOutputRef.current) return
    debugRawOutputRef.current.scrollTop = debugRawOutputRef.current.scrollHeight
  }, [debugRawAutoScroll, debugRawOutputText])

  // Persist breakpoints per script so they survive closing/reopening the debugger.
  const breakpointStorageKey = scriptId ? `apexload.debug.breakpoints.${scriptId}` : null
  const breakpointsLoadedRef = useRef(false)
  useEffect(() => {
    breakpointsLoadedRef.current = false
    if (!breakpointStorageKey) return
    try {
      const raw = window.localStorage.getItem(breakpointStorageKey)
      if (raw) setDebugBreakpoints(normalizeStoredBreakpoints(JSON.parse(raw)))
    } catch { /* ignore malformed persisted breakpoints */ }
    breakpointsLoadedRef.current = true
  }, [breakpointStorageKey])
  useEffect(() => {
    if (!breakpointStorageKey || !breakpointsLoadedRef.current) return
    try { window.localStorage.setItem(breakpointStorageKey, JSON.stringify(debugBreakpoints)) } catch { /* storage may be unavailable */ }
  }, [breakpointStorageKey, debugBreakpoints])

  // Close the settings popover on outside click.
  useEffect(() => {
    if (!debuggerSettingsOpen) return
    const onDown = (event: globalThis.MouseEvent) => {
      const target = event.target as Node
      if (debuggerSettingsRef.current?.contains(target)) return
      if (debuggerSettingsBtnRef.current?.contains(target)) return
      setDebuggerSettingsOpen(false)
    }
    window.addEventListener('mousedown', onDown)
    return () => window.removeEventListener('mousedown', onDown)
  }, [debuggerSettingsOpen])

  const handleDebuggerResizeKeyDown = (kind: DebuggerResizeKind, event: React.KeyboardEvent<HTMLDivElement>) => {
    const limits = DEBUGGER_LAYOUT_LIMITS[kind]
    const step = limits.step * (event.shiftKey ? 2 : 1)
    const currentValue = kind === 'rail' ? debuggerRailWidth : kind === 'vars' ? debuggerVarsWidth : debuggerBottomHeight
    let nextValue: number | null = null

    if (kind === 'bottom') {
      if (event.key === 'ArrowUp') nextValue = currentValue + step
      else if (event.key === 'ArrowDown') nextValue = currentValue - step
    } else if (kind === 'rail') {
      if (event.key === 'ArrowRight') nextValue = currentValue + step
      else if (event.key === 'ArrowLeft') nextValue = currentValue - step
    } else {
      if (event.key === 'ArrowLeft') nextValue = currentValue + step
      else if (event.key === 'ArrowRight') nextValue = currentValue - step
    }

    if (event.key === 'Home') nextValue = limits.min
    if (event.key === 'End') nextValue = limits.max

    if (nextValue === null) return
    event.preventDefault()
    if (kind === 'rail') setDebuggerRailWidth(clampDebuggerPaneSize('rail', nextValue))
    else if (kind === 'vars') setDebuggerVarsWidth(clampDebuggerPaneSize('vars', nextValue))
    else setDebuggerBottomHeight(clampDebuggerPaneSize('bottom', nextValue))
  }

  // Drag-to-resize handler for the IDE shell panels.
  const startDebuggerDrag = (kind: 'rail' | 'vars' | 'bottom', event: React.MouseEvent) => {
    event.preventDefault()
    const startX = event.clientX
    const startY = event.clientY
    const startRail = debuggerRailWidth
    const startVars = debuggerVarsWidth
    const startBottom = debuggerBottomHeight
    setActiveDebuggerResize(kind)
    const onMove = (ev: globalThis.MouseEvent) => {
      if (kind === 'rail') setDebuggerRailWidth(clampDebuggerPaneSize('rail', startRail + (ev.clientX - startX)))
      else if (kind === 'vars') setDebuggerVarsWidth(clampDebuggerPaneSize('vars', startVars - (ev.clientX - startX)))
      else setDebuggerBottomHeight(clampDebuggerPaneSize('bottom', startBottom - (ev.clientY - startY)))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      window.removeEventListener('blur', onUp)
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
      setActiveDebuggerResize(null)
    }
    document.body.style.userSelect = 'none'
    document.body.style.cursor = kind === 'bottom' ? 'row-resize' : 'col-resize'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    window.addEventListener('blur', onUp)
  }

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
      disabled: !replaying && (saving || !script.name || debugActionRows.length === 0),
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
      disabled: !((replaying && isReplayPaused) || (!replaying && !saving && script.name && debugActionRows.length > 0)),
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
    replaying,
    saveScriptDisabled,
    saving,
    script.actions,
    script.name,
    selectedReplayEvent,
    selectedDebugRow,
    toggleDebugBreakpoint,
    workspaceTab,
  ])
  useKeyboardShortcuts(scriptEditorShortcuts)

  // Reconcile session state by polling while a debug session is active. This is a
  // safety net for live updates that arrive before activeReplaySessionId is registered
  // (e.g. a breakpoint on the first action pauses before the start response resolves),
  // which the WebSocket handler would otherwise drop.
  useEffect(() => {
    if (!activeReplaySessionId || !replaying) return
    let cancelled = false
    const reconcile = async () => {
      try {
        const res = await api.getScriptDebugSession(activeReplaySessionId)
        if (cancelled || !res?.session) return
        const session = res.session
        setBrowserReplaySession(session)
        const done = session.status === 'completed' || session.status === 'failed' || session.status === 'stopped'
        setReplayReport(prev => {
          if (!prev) return done ? (session.report || prev) : prev
          // Merge live events while a run is in flight; only adopt the terminal
          // report (completed_at/success) once the session has actually finished.
          return {
            ...prev,
            events: session.events && session.events.length ? session.events : prev.events,
            ...(done && session.report ? session.report : {}),
          }
        })
      } catch (e: unknown) {
        if (cancelled) return
        if (e instanceof ApiError && e.status === 404) {
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
  }, [activeReplaySessionId, replaying, api, setActiveReplaySessionId, setBrowserReplaySession, setError, setReplayActivity, setReplayReport, setReplaying])

  const addDataSource = () => {
    const nextIndex = (script.data_sources?.length || 0) + 1
    const nextSource: DataSourceConfig = {
      ...EMPTY_DATA_SOURCE,
      name: `source_${nextIndex}`,
    }
    setScript(prev => ({
      ...prev,
      data_sources: [...(prev.data_sources || []), nextSource],
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
  }

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

  const exportDataSourceFile = (source: DataSourceConfig, idx: number) => {
    const content = source.inline_data || ''
    if (!content.trim()) {
      setError('No inline parameter data to export for this source')
      return
    }
    const base = (source.name || `source_${idx + 1}`).replace(/[^a-zA-Z0-9_-]+/g, '_')
    const ext = (source.type || 'csv').toLowerCase() === 'dat' ? 'dat' : 'csv'
    downloadTextFile(content, `${base}.${ext}`)
    setError('')
    setSuccess(`Exported ${base}.${ext}`)
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
  const selectedAuthorJsonScope = useMemo(
    () => resolveAuthorJsonScope(script, authorCodeFocus),
    [authorCodeFocus, script],
  )
  const selectedAuthorJsonText = useMemo(
    () => selectedAuthorJsonScope ? JSON.stringify(selectedAuthorJsonScope.action, null, 2) : '',
    [selectedAuthorJsonScope],
  )
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
  const debugEvidenceStatus = replayReport?.completed_at
    ? (replayReport.success ? 'passed' : 'failed')
    : activeReplaySessionId || browserReplaySession
      ? 'in progress'
      : 'missing'
  const debugWorkspaceStatusLabel = replaying
    ? (isReplayPaused ? 'Paused' : 'Running')
    : replayReport?.completed_at
      ? (replayReport.success ? 'Passed' : 'Failed')
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
      status: validationEvidence && replayReport?.completed_at && errorCount === 0 ? 'ready' : 'needs evidence',
      action: handleSave,
      disabled: saveScriptDisabled,
    },
  ]
  useTopbarContribution({
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
        onChange={value => activateWorkspaceTab(value)}
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
        title: replayStartButtonLabel,
        tone: 'success',
        disabled: replaying || saving || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0,
        onClick: () => { void handleReplay() },
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
        onClick: () => { void handlePauseResumeReplay() },
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
        onClick: () => { void handleStepReplay('into') },
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
        onClick: () => { void handleStepReplay('over') },
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
        onClick: () => { void handleStepReplay('loop_end') },
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
        onClick: () => { void handleStopReplay() },
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
        onClick: () => { void setBreakOnFailure(!replayOptions.breakOnFailure) },
      },
    ],
  })

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
  const renderScriptJsonScopeBar = () => {
    if (!selectedAuthorJsonScope) return null
    const selectedLabel = selectedAuthorJsonScope.subAction ? 'Selected Sub-action' : 'Selected Action'
    return (
      <div className="script-json-scope-bar" aria-label="Script JSON display scope">
        <div className="script-json-scope-summary">
          <strong>{selectedLabel}</strong>
          <span title={selectedAuthorJsonScope.path}>{selectedAuthorJsonScope.label}</span>
        </div>
        <div className="script-json-scope-toggle" role="group" aria-label="JSON display scope">
          <button
            type="button"
            className={!showFullScriptJson && !selectedJsonUnavailable ? 'active' : ''}
            aria-pressed={!showFullScriptJson && !selectedJsonUnavailable}
            disabled={selectedJsonUnavailable}
            title={selectedJsonUnavailable ? 'Resolve or reset raw JSON edits before returning to selected action JSON.' : `Show only ${selectedLabel.toLowerCase()} JSON`}
            onClick={() => setShowFullScriptJson(false)}
          >
            {selectedLabel}
          </button>
          <button
            type="button"
            className={showFullScriptJson || selectedJsonUnavailable ? 'active' : ''}
            aria-pressed={showFullScriptJson || selectedJsonUnavailable}
            onClick={() => setShowFullScriptJson(true)}
          >
            Full Script JSON
          </button>
        </div>
      </div>
    )
  }
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
                <button type="button" role="cell" className="action-list-center-name" title={actionRowLabel(action)} onClick={() => selectAuthorPhaseAction(row)}>
                  {actionRowLabel(action)}
                </button>
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
    if (showSelectedActionJson && selectedAuthorJsonScope) {
      const selectedTitle = selectedAuthorJsonScope.subAction ? 'Selected Sub-action JSON' : 'Selected Action JSON'
      return (
        <>
          {renderScriptJsonScopeBar()}
          <ScriptCodePane
            value={selectedAuthorJsonText}
            status="synced"
            statusMessage={selectedAuthorJsonScope.subAction ? 'Selected sub-action' : 'Selected action'}
            activePath={selectedAuthorJsonScope.path}
            activeRange={null}
            actionCount={1}
            allowDirectEdit={false}
            ariaLabel={selectedTitle}
            title={selectedTitle}
            subtitle={selectedAuthorJsonScope.path.replace(/^script\./, '')}
            onApply={() => undefined}
            onChange={() => undefined}
            onCopy={() => void navigator.clipboard?.writeText(selectedAuthorJsonText)}
            onDownload={() => downloadTextFile(selectedAuthorJsonText, `${sanitizeDownloadName(selectedAuthorJsonScope.label, 'selected-action')}.json`)}
            onFormat={() => undefined}
            onReset={() => undefined}
          />
        </>
      )
    }
    return (
      <>
        {renderScriptJsonScopeBar()}
        <ScriptCodePane
          value={jsonText}
          status={jsonStatus}
          statusMessage={jsonStatusMessage}
          activePath={scriptCodeStatusPath}
          activeRange={scriptCodeActiveRange}
          activeParameterName={selectedOutlineParameterExists ? selectedOutlineParameterName : null}
          activeCandidateName={autoParameterSidebarActive ? activeAutoParameterCandidate?.varName || activeAutoParameterCandidate?.keyHint || null : null}
          actionCount={scriptPhaseActionRows.length}
          assistExtensions={scriptAssistExtensions}
          candidateMatchRanges={autoParameterCandidateMatchRanges}
          jumpToLineRequest={jsonLineJumpRequest}
          parameterMatchRanges={parameterMatchRanges}
          resolvedValues={workspaceTab === 'debug' && hasReplayVariableInspectorData ? replayVariableInspector : null}
          ariaLabel="Source"
          title="Source"
          onApply={handleApplyJsonDraft}
          onChange={handleCodePaneChange}
          onCopy={() => void navigator.clipboard?.writeText(jsonText)}
          onDownload={() => downloadTextFile(jsonText || formatScriptJson(script), `${sanitizeDownloadName(currentDraftScript.name, 'script')}.json`)}
          onFormat={handleFormatJsonDraft}
          onReset={handleResetJsonDraft}
          onRequestParameterize={openJsonParameterSelectionMenu}
          onScrollControllerChange={handleScriptCodeScrollControllerChange}
          onScrollSnapshotChange={handleScriptCodeScrollSnapshotChange}
        />
        <ActionParameterMenu
          selectionState={jsonParameterSelection}
          onOpenParameterEditor={openJsonParameterEditor}
          onOpenExistingParameter={openJsonExistingParameterChooser}
          onOpenOccurrenceReview={openJsonParameterOccurrenceReview}
          variant="json"
        />
      </>
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
      <ReplayPreflightCard preflight={replayPreflight} />
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
                onJumpToLine={line => setJsonLineJumpRequest({ line, nonce: Date.now() })}
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
      case 'source-map':
        return (
          <JsonSourceMinimap
            activePath={scriptCodeStatusPath}
            activeRange={scriptCodeActiveRange}
            candidateMatchRanges={autoParameterCandidateMatchRanges}
            jsonText={jsonText}
            onJumpToLine={line => setJsonLineJumpRequest({ line, nonce: Date.now() })}
            onScrollTopChange={handleJsonSourceMapScrollTopChange}
            parameterMatchRanges={parameterMatchRanges}
            scrollSnapshot={scriptCodeScrollSnapshot}
            selectedParameterName={selectedOutlineParameterExists ? selectedOutlineParameterName : null}
            status={jsonStatus}
          />
        )
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
          <>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Import Summary</strong>
                <span>{importMetadata?.source || 'import'}</span>
              </div>
              <ul className="utility-list">
                <li className="utility-list-item">Source: {importMetadata?.source_script_name || script.name}</li>
                <li className="utility-list-item">{importSummary.runtimeFiles.length} runtime file{importSummary.runtimeFiles.length === 1 ? '' : 's'}</li>
                <li className="utility-list-item">{importSummary.parameterFiles.length} parameter file{importSummary.parameterFiles.length === 1 ? '' : 's'}</li>
                <li className="utility-list-item">{importedDataSourceSummaries.length} generated data source{importedDataSourceSummaries.length === 1 ? '' : 's'}</li>
              </ul>
            </div>
            {sourceReview && (
              <div className="utility-card">
                <div className="utility-card-header">
                  <strong>Source Mapping</strong>
                  <span>{sourceReview.mappings?.length || 0} mappings</span>
                </div>
                <div className="utility-metric-row">
                  <span className="metric-badge">{sourceReview.source_files?.length || 0} files</span>
                  <span className="metric-badge">{importSummary.correlationSuggestions.length} correlations</span>
                </div>
                {activeAction !== null && (
                  <button type="button" className="btn btn-sm btn-secondary" onClick={() => focusSourceMappingForAction(activeAction)}>
                    Focus Selected Action
                  </button>
                )}
              </div>
            )}
            {trueClientPromotionReview && (
              <div className="utility-card">
                <div className="utility-card-header">
                  <strong>TruClient Promotion</strong>
                  <span>{formatTrueClientPromotionStatus(trueClientPromotionReview.status)}</span>
                </div>
                <div className="utility-metric-row">
                  <span className="metric-badge">{formatPromotionCount(trueClientPromotionReview.replayEvidence.events, 'event')}</span>
                  <span className="metric-badge">{trueClientPromotionReview.replayEvidence.passed} passed / {trueClientPromotionReview.replayEvidence.failed} failed</span>
                  <span className="metric-badge">{formatPromotionCount(trueClientPromotionReview.gates.selectorFallbackCandidates, 'fallback')}</span>
                </div>
                <ul className="utility-list">
                  <li className="utility-list-item">Replay: {trueClientPromotionReview.replayEvidence.sessionId || trueClientPromotionReview.replayEvidence.status}</li>
                  <li className="utility-list-item">{formatPromotionCount(trueClientPromotionReview.gates.reviewRequiredActions, 'review action')}</li>
                  <li className="utility-list-item">{formatPromotionCount(trueClientPromotionReview.compatibility.frameWindowBlockers, 'frame/window blocker')}</li>
                </ul>
              </div>
            )}
            {migrationSuggestions.length > 0 && (
              <div className="utility-card">
                <div className="utility-card-header">
                  <strong>Migration Assistant</strong>
                  <span>{migrationSuggestions.length} suggestion{migrationSuggestions.length === 1 ? '' : 's'}</span>
                </div>
                <div className="utility-stack">
                  {migrationSuggestions.map(suggestion => (
                    <div key={`${suggestion.key}:metadata`} className="utility-suggestion-card">
                      <strong>{suggestion.title}</strong>
                      <p>{suggestion.description}</p>
                      <button type="button" className="btn btn-sm btn-secondary" onClick={() => applyMigrationSuggestion(suggestion)}>
                        {suggestion.applyLabel}
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {importedParameterFileMappings.length > 0 && (
              <div className="utility-card">
                <div className="utility-card-header">
                  <strong>Parameter Files</strong>
                  <span>{importedParameterFileMappings.length}</span>
                </div>
                <ul className="utility-list">
                  {importedParameterFileMappings.map(({ section, sourceName }) => (
                    <li key={`${section.file}:${section.name}:metadata`} className="utility-list-item">
                      <button type="button" className="utility-link-button" onClick={() => openImportedDataSourceReview(sourceName)}>
                        {section.file}: {section.name}{sourceName ? ` -> ${sourceName}` : ''}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )
      case 'object-descriptors':
        return (
          <>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Descriptor Review</strong>
                <span>{objectDescriptorReview.rows.length} objects</span>
              </div>
              <div className="utility-metric-row">
                <span className="metric-badge">{objectDescriptorReview.repositoryCount} repos</span>
                <span className="metric-badge">{objectDescriptorReview.candidateCount} candidates</span>
                <span className="metric-badge">{objectDescriptorReview.unsupportedCount} review</span>
              </div>
            </div>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Selector Shortcuts</strong>
                <span>{objectDescriptorReview.rows.length}</span>
              </div>
              {objectDescriptorReview.rows.length > 0 ? (
                <div className="utility-stack">
                  {objectDescriptorReview.rows.map(row => (
                    <div key={`${row.key}:sidebar`} className="utility-suggestion-card">
                      <strong>{row.label}</strong>
                      <p>{row.descriptorId} · {row.status}</p>
                      {row.currentSelector && <code>{row.currentSelector}</code>}
                      <div className="utility-inline-actions">
                        {row.actionIndex !== null && (
                          <button type="button" className="btn btn-sm btn-ghost" onClick={() => selectAuthorAction(row.actionIndex)}>
                            Select Action
                          </button>
                        )}
                        {row.candidates[0] && (
                          <button type="button" className="btn btn-sm btn-secondary" onClick={() => applyObjectDescriptorCandidate(row, row.candidates[0])} disabled={row.actionIndex === null}>
                            Use Best Selector
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="utility-empty-state">No object descriptors are available.</div>
              )}
            </div>
          </>
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
          <>
            <div className="utility-card">
              <div className="utility-card-header">
                <strong>Runtime Summary</strong>
                <span>{runLogicLabel}</span>
              </div>
              <ul className="utility-list">
                <li className="utility-list-item">Think time: {thinkTimeLabel}</li>
                <li className="utility-list-item">Log level: {logLevelLabel}</li>
                <li className="utility-list-item">Proxy: {httpProxySummary}</li>
              </ul>
            </div>
            {renderReadyToRunCard()}
            {importSummary.runtimeSettingsParity.length > 0 && (
              <div className="utility-card">
                <div className="utility-card-header">
                  <strong>Runtime Parity</strong>
                  <span>{runtimeSettingParitySummary || 'No parity issues'}</span>
                </div>
                <ul className="utility-list">
                  {importSummary.runtimeSettingsParity.slice(0, 6).map(entry => (
                    <li key={`${entry.id}:runtime-sidebar`} className="utility-list-item">
                      {formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )
      default:
        return null
    }
  }

  return (
    <div className="script-editor">
      <datalist id="object-ref-suggestions">
        {objectRefSuggestions.map(name => <option key={name} value={name} />)}
      </datalist>
      {showImportedParameterMappingsDialog && importedParameterFileMappings.length > 0 && (
        <ImportedParameterMappingsDialog
          mappings={importedParameterFileMappings}
          onClose={() => setShowImportedParameterMappingsDialog(false)}
          onOpenMappedDataSource={openImportedDataSourceReview}
        />
      )}
      {serverParameterization && (
        <div className="editor-dialog-backdrop" onClick={() => setServerParameterization(null)}>
          <div
            className="editor-dialog server-parameterization-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Server parameterization scan"
            onClick={event => event.stopPropagation()}
          >
            <div className="editor-dialog-header">
              <div>
                <h3 className="panel-title">Server Parameterization Scan</h3>
                <p className="editor-dialog-subtitle">
                  {serverParameterization.note || 'Review and apply the detected data parameters and secrets.'}
                </p>
              </div>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setServerParameterization(null)}>Close</button>
            </div>
            <div className="editor-dialog-body">
              <ParameterizationReview
                parameterization={serverParameterization}
                script={script}
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
        <div className="editor-dialog-backdrop" onClick={() => !savingBlock && setShowSaveBlockModal(false)}>
          <div
            className="editor-dialog save-block-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Save selected actions as a reusable block"
            onClick={event => event.stopPropagation()}
          >
            <div className="editor-dialog-header">
              <div>
                <h3 className="panel-title">Save as Reusable Block</h3>
                <p className="editor-dialog-subtitle">
                  {selectedBlockScopes.length} action{selectedBlockScopes.length === 1 ? '' : 's'} will be saved to this project's block library.
                </p>
              </div>
              <button type="button" className="btn btn-sm btn-ghost" disabled={savingBlock} onClick={() => setShowSaveBlockModal(false)}>Close</button>
            </div>
            <div className="editor-dialog-body save-block-dialog-body">
              <label className="save-block-field">
                <span>Block name</span>
                <input
                  type="text"
                  value={saveBlockName}
                  autoFocus
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
                <button type="button" className="btn btn-sm btn-ghost" disabled={savingBlock} onClick={() => setShowSaveBlockModal(false)}>Cancel</button>
                <button type="button" className="btn btn-sm btn-accent" disabled={savingBlock || !saveBlockName.trim() || !projectId} onClick={saveSelectedAsBlock}>
                  {savingBlock ? 'Saving…' : 'Save block'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
      {showMetadataJsonDialog && importMetadata && (
        <MetadataJsonDialog
          metadata={importMetadata}
          onClose={() => setShowMetadataJsonDialog(false)}
        />
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
              <button type="button" className="debugger-icon-btn" title={replayStartButtonLabel} onClick={() => void handleReplay()} disabled={replaying || saving || !currentDraftScript.name || (currentDraftScript.actions || []).length === 0}>
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
                      activateWorkspaceTab('debug')
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
                      <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
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
                          tabIndex={0}
                          onKeyDown={e => {
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
                            const out: React.ReactNode[] = []
                            for (const item of renderRows) {
                              const { phase, actionIndex, action } = item.row
                              if (item.kind === 'container') {
                                const containerKey = item.key
                                const isTopLevel = item.stepPath.length === 0
                                const subtreeEvents = debugEvents.filter(ev => ev.phase === phase && ev.action_index === actionIndex && (isTopLevel || (!!ev.step_path && item.stepPath.every((s, i) => ev.step_path![i] === s))))
                                const anyFail = subtreeEvents.some(ev => !ev.success)
                                const isCurrent = currentDebugAncestorKeys.has(containerKey)
                                const rollup = anyFail ? 'fail' : isCurrent ? 'current' : subtreeEvents.length > 0 ? 'pass' : 'pending'
                                const wholeActive = isTopLevel && debugBreakpointKeys.has(debugBreakpointKey(phase, actionIndex))
                                const childBpCount = isTopLevel ? debugBreakpoints.filter(bp => bp.phase === phase && bp.action_index === actionIndex && (bp.request_index != null || (!!bp.step_path && bp.step_path.length > 0))).length : 0
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
                    onKeyDown={e => handleDebuggerResizeKeyDown('rail', e)}
                    onMouseDown={e => startDebuggerDrag('rail', e)}
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

                {renderCenterWorkbench('debugger-code-center', 'Debugger center workbench', 'debug')}

                {!debugDetailCollapsed && (
                <section className="debugger-center" aria-label="Debug detail">
                  <div
                    className={`debugger-resizer debugger-resizer-col debugger-resizer-vars ${activeDebuggerResize === 'vars' ? 'active' : ''}`}
                    onKeyDown={e => handleDebuggerResizeKeyDown('vars', e)}
                    onMouseDown={e => startDebuggerDrag('vars', e)}
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
                        projectId={projectId}
                        events={replayReport.events}
                        recordingStatus={scriptRecordingStatus}
                        actionCount={script.actions.length}
                        diagnoseWithBaseline={(id, replay) => api.diagnoseScriptReplay(id, replay)}
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
                    onKeyDown={e => handleDebuggerResizeKeyDown('bottom', e)}
                    onMouseDown={e => startDebuggerDrag('bottom', e)}
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
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => downloadTextFile(debugRawOutputText || 'No raw output captured', `${sanitizeDownloadName(script.name, 'script')}-debug.log`)}>Download</button>
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
                                selectAuthorPhaseAction(row)
                                activateWorkspaceTab('author')
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
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => downloadTextFile(debugRawOutputText || 'No debug output captured', `${sanitizeDownloadName(script.name, 'script')}-debug-output.log`)}>Download</button>
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
          )}

          {objectDescriptorReview.rows.length > 0 && centerTabPanelElements['object-descriptors'] && createPortal(
            renderStructuredCenterDocument('Object Descriptors', (
              <section className="center-document config-panel center-document-object" style={{ padding: 0 }} aria-label="Object Descriptors">
                <div className="center-document-header">
                  <div>
                    <h3 className="panel-title">Object Descriptors</h3>
                    <p>Review imported TruClient object IDs, current selectors, and extracted fallback candidates.</p>
                  </div>
                </div>
              <div className="panel-body" style={{ padding: '0 20px 20px' }}>
                <div className="import-metadata-grid">
                  <div className="import-metadata-card">
                    <span>Repositories</span>
                    <strong>{objectDescriptorReview.repositoryCount}</strong>
                    <small>Preserved from the source archive</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Descriptors</span>
                    <strong>{objectDescriptorReview.rows.length}</strong>
                    <small>Mapped to review rows</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Candidates</span>
                    <strong>{objectDescriptorReview.candidateCount}</strong>
                    <small>CSS, XPath, text, or role options</small>
                  </div>
                  <div className="import-metadata-card">
                    <span>Source Fragments</span>
                    <strong>{objectDescriptorReview.unsupportedCount}</strong>
                    <small>Review-only or unsupported steps</small>
                  </div>
                </div>
                <div className="object-descriptor-list">
                  {objectDescriptorReview.rows.map((row) => (
                    <div key={row.key} className="object-descriptor-card">
                      <div className="object-descriptor-heading">
                        <div>
                          <strong>{row.label}</strong>
                          <span>{row.descriptorId} · {row.status}</span>
                        </div>
                        {row.actionIndex !== null && (
                          <button type="button" className="btn btn-sm btn-secondary" onClick={() => selectAuthorAction(row.actionIndex)}>
                            Select Action
                          </button>
                        )}
                      </div>
                      <div className="object-descriptor-current">
                        <span>Current selector</span>
                        <code>{row.currentSelector || 'No selector applied'}</code>
                      </div>
                      {(row.frameContext || row.windowContext || row.replayDiagnostics.length > 0) && (
                        <div className="object-descriptor-current">
                          <span>Replay diagnostics</span>
                          {row.frameContext && <code>Frame: {row.frameContext}</code>}
                          {row.windowContext && <code>Window: {row.windowContext}</code>}
                          {row.replayDiagnostics.map((diagnostic, index) => (
                            <code key={`${row.key}-diagnostic-${index}`}>{diagnostic}</code>
                          ))}
                        </div>
                      )}
                      <div className="object-selector-candidates">
                        {row.candidates.map((candidate) => (
                          <div key={`${row.key}:${candidate.kind}:${candidate.value}`} className="object-selector-candidate">
                            <div>
                              <strong>{candidate.kind} · {candidate.confidence || 0}%</strong>
                              <code>{candidate.value}</code>
                              {candidate.reason && <span>{candidate.reason}</span>}
                            </div>
                            <button
                              type="button"
                              className="btn btn-sm btn-secondary"
                              onClick={() => applyObjectDescriptorCandidate(row, candidate)}
                              disabled={row.actionIndex === null}
                            >
                              Use Selector
                            </button>
                          </div>
                        ))}
                        {row.candidates.length === 0 && (
                          <div className="utility-empty-state">No alternate selectors were extracted for this object.</div>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
              </section>
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
                    <p>{httpConversionPreview.generated_script?.name || 'HTTP draft'} is ready for validation, replay, correlation repair, and approval review.</p>
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
                    </div>
                    <div className="import-correlation-list">
                      {(httpConversionPreview.review.correlation_candidates || []).slice(0, 4).map((candidate, index) => (
                        <div key={`http-correlation-${candidate.name}-${candidate.value}-${index}`} className="import-correlation-card">
                          <strong>Correlation · {candidate.name}</strong>
                          <span>{candidate.evidence || candidate.source}</span>
                          {candidate.value && <code>{candidate.value}</code>}
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
              <section className="center-document runtime-settings config-panel center-document-runtime" style={{ padding: 0 }} aria-label="Runtime Settings">
                <div className="center-document-header">
                  <div>
                    <h3 className="panel-title">Runtime Settings</h3>
                    <p>Execution, pacing, browser, protocol, and source runtime compatibility controls.</p>
                  </div>
                  <div className="runtime-settings-header-actions">
                    <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saveScriptDisabled} title={saveScriptTitle}>
                      {saving ? 'Saving...' : 'Save Script'}
                    </button>
                  </div>
                </div>
                <div className="runtime-settings-grid">
                <nav className="runtime-settings-nav" aria-label="Runtime setting categories">
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('script_config') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('script_config')}>
                    <RuntimeSectionIcon section="script_config" />
                    <span className="section-label">Script Configuration</span>
                    <span className="section-summary">{formatScriptProtocolLabel(script.protocol)}</span>
                  </button>
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('think') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('think')}>
                    <RuntimeSectionIcon section="think" />
                    <span className="section-label">Think Time</span>
                    <span className="section-summary">{script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} - ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`}</span>
                  </button>
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('pacing') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('pacing')}>
                    <RuntimeSectionIcon section="pacing" />
                    <span className="section-label">Iteration Pacing</span>
                    <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
                  </button>
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('runlogic') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('runlogic')}>
                    <RuntimeSectionIcon section="runlogic" />
                    <span className="section-label">Run Logic</span>
                    <span className="section-summary">
                      {script.config.run_logic?.global_iterations
                        ? `${script.config.run_logic.global_iterations} shared iterations`
                        : script.config.run_logic?.max_transactions
                          ? `${script.config.run_logic.max_transactions} max transactions`
                          : script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
                    </span>
                  </button>
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('errors') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('errors')}>
                    <RuntimeSectionIcon section="errors" />
                    <span className="section-label">Error Handling</span>
                    <span className="section-summary">{script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}</span>
                  </button>
                  {isJmsScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('jms') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('jms')}>
                      <RuntimeSectionIcon section="jms" />
                      <span className="section-label">Solace JMS</span>
                      <span className="section-summary">{script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}</span>
                    </button>
                  )}
                  {isWebSocketScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('websocket') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('websocket')}>
                      <RuntimeSectionIcon section="websocket" />
                      <span className="section-label">WebSocket Runtime</span>
                      <span className="section-summary">{script.config.websocket?.reconnect ? 'Reconnect enabled' : 'Single connection'}</span>
                    </button>
                  )}
                  {isGrpcScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('grpc') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('grpc')}>
                      <RuntimeSectionIcon section="grpc" />
                      <span className="section-label">gRPC Runtime</span>
                      <span className="section-summary">{script.config.grpc?.use_reflection ? 'Reflection' : script.config.grpc?.proto_files?.length ? 'Proto files' : 'Contract pending'}</span>
                    </button>
                  )}
                  {isDatabaseScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('database') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('database')}>
                      <RuntimeSectionIcon section="database" />
                      <span className="section-label">Database Runtime</span>
                      <span className="section-summary">{script.config.database?.driver || 'sql'}</span>
                    </button>
                  )}
                  {isNetworkScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('network') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('network')}>
                      <RuntimeSectionIcon section="network" />
                      <span className="section-label">Network Runtime</span>
                      <span className="section-summary">{script.config.network?.subprotocol || 'tcp'}</span>
                    </button>
                  )}
                  {isHttpTransportScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('http') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('http')}>
                      <RuntimeSectionIcon section="http" />
                      <span className="section-label">HTTP Transport</span>
                      <span className="section-summary">{httpProxySummary}</span>
                    </button>
                  )}
                  {isSoapScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('soap') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('soap')}>
                      <RuntimeSectionIcon section="soap" />
                      <span className="section-label">SOAP Settings</span>
                      <span className="section-summary">{script.config.soap?.version || 'soap11'}</span>
                    </button>
                  )}
                  {isJavaHttpScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('java_http') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('java_http')}>
                      <RuntimeSectionIcon section="java_http" />
                      <span className="section-label">Java over HTTP</span>
                      <span className="section-summary">{script.config.java_http?.codec || 'java_serialized_object_base64'}</span>
                    </button>
                  )}
                  {isHttpScript && (
                    <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('web_runtime') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('web_runtime')}>
                      <RuntimeSectionIcon section="web_runtime" />
                      <span className="section-label">Web HTTP/HTML Compatibility</span>
                      <span className="section-summary">
                        {httpProxySummary !== 'Default'
                          ? httpProxySummary
                          : script.config.web_runtime?.content_checks?.length
                          ? `${script.config.web_runtime.content_checks.length} content checks`
                          : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
                      </span>
                    </button>
                  )}
                  <button type="button" className={`runtime-settings-nav-button ${runtimeSectionIsActive('log') ? 'active' : ''}`} onClick={() => selectRuntimeSettingsSection('log')}>
                    <RuntimeSectionIcon section="log" />
                    <span className="section-label">Logging</span>
                    <span className="section-summary">{script.config.log?.level || 'standard'}</span>
                  </button>
                </nav>
                <div className="runtime-settings-detail" aria-live="polite">
                {/* Script Configuration */}
                <div className={`settings-section ${runtimeSectionIsActive('script_config') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('script_config')}>
                    <RuntimeSectionIcon section="script_config" />
                    <span className="section-label">Script Configuration</span>
                    <span className="section-summary">{formatScriptProtocolLabel(script.protocol)}</span>
                    <span className="chevron">{runtimeSectionIsActive('script_config') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('script_config') && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Protocol</span>
                          {canEditProtocol ? (
                            <select value={script.protocol} onChange={e => updateProtocol(e.target.value)} aria-label="Protocol">
                              {SCRIPT_PROTOCOL_OPTIONS.map(option => (
                                <option key={option.value} value={option.value}>{option.label}</option>
                              ))}
                            </select>
                          ) : (
                            <div className="form-readonly-value" aria-label="Protocol">
                              <strong>{formatScriptProtocolLabel(script.protocol)}</strong>
                              <small>{script.protocol}</small>
                            </div>
                          )}
                        </label>
                        <label>
                          <span>Version</span>
                          <input value={script.version} onChange={e => updateField('version', e.target.value)} placeholder="1.0.0" />
                        </label>
                        {!isJmsScript && !isDatabaseScript && !isNetworkScript && (
                          <label>
                            <span>{isGrpcScript ? 'gRPC Target' : isWebSocketScript ? 'WebSocket Origin' : 'Base URL'}</span>
                            <input value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder={isGrpcScript ? 'dns:///api.example.com:443' : isWebSocketScript ? 'wss://api.example.com' : 'https://api.example.com'} />
                          </label>
                        )}
                        {isDatabaseScript && (
                          <>
                            <label>
                              <span>Driver</span>
                              <select value={script.config.database?.driver || 'sqlite'} onChange={e => updateField('config.database.driver', e.target.value)}>
                                <option value="sqlite">SQLite</option>
                              </select>
                            </label>
                            <label>
                              <span>DSN Secret</span>
                              <input value={script.config.database?.dsn_secret || ''} onChange={e => updateField('config.database.dsn_secret', e.target.value)} placeholder="secret://perf/db-dsn" />
                            </label>
                            <label className="full-width">
                              <span>DSN / Fixture</span>
                              <input value={script.config.database?.dsn || ''} onChange={e => updateField('config.database.dsn', e.target.value)} placeholder=":memory: or file:test.db?mode=rw" />
                            </label>
                          </>
                        )}
                        {isNetworkScript && (
                          <>
                            <label>
                              <span>Subprotocol</span>
                              <select value={script.config.network?.subprotocol || 'tcp'} onChange={e => updateField('config.network.subprotocol', e.target.value)}>
                                <option value="dns">DNS</option>
                                <option value="tcp">TCP / Winsock</option>
                                <option value="udp">UDP / Winsock</option>
                                <option value="ftp">FTP</option>
                                <option value="smtp">SMTP</option>
                                <option value="pop3">POP3</option>
                                <option value="imap">IMAP</option>
                                <option value="ldap">LDAP</option>
                              </select>
                            </label>
                            <label>
                              <span>Host</span>
                              <input value={script.config.network?.host || ''} onChange={e => updateField('config.network.host', e.target.value)} placeholder="localhost" />
                            </label>
                            <label>
                              <span>Port</span>
                              <input type="number" value={script.config.network?.port || 0} onChange={e => updateField('config.network.port', Number(e.target.value) || undefined)} placeholder="443" />
                            </label>
                          </>
                        )}
                        {isJmsScript && (
                          <>
                            <label>
                              <span>Broker URL</span>
                              <input value={script.config.jms?.broker_url || ''} onChange={e => updateField('config.jms.broker_url', e.target.value)} placeholder="tcps://broker.example.com:55443" />
                            </label>
                            <label>
                              <span>Message VPN</span>
                              <input value={script.config.jms?.message_vpn || ''} onChange={e => updateField('config.jms.message_vpn', e.target.value)} placeholder="default" />
                            </label>
                            <label>
                              <span>Default Destination</span>
                              <input value={script.config.jms?.destination || ''} onChange={e => updateField('config.jms.destination', e.target.value)} placeholder="orders.in" />
                            </label>
                          </>
                        )}
                      </div>
                      {isJmsScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          JMS Solace scripts use publish, consume, and request/reply actions with message body, headers, properties, correlation IDs, and reply destinations in the action editor below.
                        </div>
                      )}
                      {isBrowserScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          Browser scripts can use navigate, click, fill, select, wait, tap, long-press, swipe, and screenshot actions with device presets, touch controls, and client metrics in the action editor below.
                        </div>
                      )}
                      {isSoapScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          SOAP scripts reuse the HTTP transport model and add SOAP envelope, namespace, WS-Security, SOAPAction, and fault-handling settings below.
                        </div>
                      )}
                      {isJavaHttpScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          Java-over-HTTP scripts reuse the HTTP transport client, support embedded or sidecar helper modes, and currently ship with the `java_serialized_object_base64` codec where request bodies are base64-encoded serialized bytes.
                        </div>
                      )}
                      {isWebSocketScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          WebSocket scripts model connection lifecycle, message send/receive steps, handshake headers, subprotocol/origin settings, and transcript evidence for replay.
                        </div>
                      )}
                      {isGrpcScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          gRPC scripts model service/method calls, reflection or proto setup, metadata/TLS/authority, stream controls, and gRPC status evidence.
                        </div>
                      )}
                      {isDatabaseScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          Database scripts model secret-backed DSNs, query/exec/transaction actions, bind parameters, row/scalar assertions, extractors, and read-only replay guards.
                        </div>
                      )}
                      {isNetworkScript && (
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          Network scripts model DNS, TCP, UDP, Winsock-style sockets, FTP, SMTP, POP3, IMAP, and LDAP actions with transcript and record evidence.
                        </div>
                      )}
                    </div>
                  )}
                </div>
                {/* Think Time */}
                <div className={`settings-section ${runtimeSectionIsActive('think') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('think')}>
                    <RuntimeSectionIcon section="think" />
                    <span className="section-label">Think Time</span>
                    <span className="section-summary">
                      {script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} — ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('think') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('think') && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Distribution</span>
                          <select value={script.config.think_time?.type || 'fixed'}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, type: e.target.value })}>
                            <option value="fixed">Fixed</option>
                            <option value="gaussian">Gaussian (Normal)</option>
                            <option value="uniform">Uniform (Random)</option>
                          </select>
                        </label>
                        <label>
                          <span>Mean (s)</span>
                          <SecondsFromMillisecondsInput valueMs={script.config.think_time?.mean_ms}
                            onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, mean_ms: value ?? 0 })} />
                        </label>
                        {script.config.think_time?.type === 'gaussian' && (
                          <label>
                            <span>Std Dev (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.think_time?.std_ms}
                              onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, std_ms: value ?? 0 })} />
                          </label>
                        )}
                        {(script.config.think_time?.type === 'uniform' || script.config.think_time?.type === 'gaussian') && (
                          <>
                            <label>
                              <span>Min (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.think_time?.min_ms}
                                onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, min_ms: value ?? 0 })} />
                            </label>
                            <label>
                              <span>Max (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.think_time?.max_ms}
                                onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, max_ms: value ?? 0 })} />
                            </label>
                          </>
                        )}
                        <label>
                          <span>Multiplier</span>
                          <input type="number" step="0.1" value={script.config.think_time?.multiplier || 1.0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, multiplier: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Limit (s)</span>
                          <SecondsFromMillisecondsInput valueMs={script.config.think_time?.limit_ms} placeholder="0 = no limit"
                            onChangeMs={value => updateField('config.think_time', { ...script.config.think_time, limit_ms: value ?? 0 })} />
                        </label>
                        <div className="runtime-checkbox-list">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.think_time?.ignore || false}
                              onChange={e => updateField('config.think_time', { ...script.config.think_time, ignore: e.target.checked })} />
                            <span>Ignore all think times</span>
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>

                {/* Pacing */}
                <div className={`settings-section ${runtimeSectionIsActive('pacing') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('pacing')}>
                    <RuntimeSectionIcon section="pacing" />
                    <span className="section-label">Iteration Pacing</span>
                    <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
                    <span className="chevron">{runtimeSectionIsActive('pacing') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('pacing') && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label className="full-width">
                          <span>Pacing Mode</span>
                          <select value={script.config.pacing?.type || 'none'}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
                            <option value="none">No delay (as fast as possible)</option>
                            <option value="fixed_delay">Fixed delay after iteration</option>
                            <option value="random_delay">Random delay after iteration</option>
                            <option value="fixed_interval">Fixed interval from iteration start</option>
                            <option value="random_interval">Random interval from iteration start</option>
                          </select>
                        </label>
                        {script.config.pacing?.type === 'fixed_delay' && (
                          <label><span>Delay (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.pacing?.delay_ms}
                              onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, delay_ms: value ?? 0 })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_delay' && (
                          <>
                            <label><span>Min Delay (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.min_delay_ms}
                                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, min_delay_ms: value ?? 0 })} />
                            </label>
                            <label><span>Max Delay (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.max_delay_ms}
                                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, max_delay_ms: value ?? 0 })} />
                            </label>
                          </>
                        )}
                        {script.config.pacing?.type === 'fixed_interval' && (
                          <label><span>Interval (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.pacing?.interval_ms}
                              onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, interval_ms: value ?? 0 })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_interval' && (
                          <>
                            <label><span>Min Interval (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.min_interval_ms}
                                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, min_interval_ms: value ?? 0 })} />
                            </label>
                            <label><span>Max Interval (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.pacing?.max_interval_ms}
                                onChangeMs={value => updateField('config.pacing', { ...script.config.pacing, max_interval_ms: value ?? 0 })} />
                            </label>
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {/* Run Logic */}
                <div className={`settings-section ${runtimeSectionIsActive('runlogic') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('runlogic')}>
                    <RuntimeSectionIcon section="runlogic" />
                    <span className="section-label">Run Logic</span>
                    <span className="section-summary">
                      {script.config.run_logic?.global_iterations
                        ? `${script.config.run_logic.global_iterations} shared iterations`
                        : script.config.run_logic?.max_transactions
                          ? `${script.config.run_logic.max_transactions} max transactions`
                          : script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('runlogic') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('runlogic') && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Iterations</span>
                          <input type="number" value={script.config.run_logic?.iterations || 0} placeholder="0 = run until duration"
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, iterations: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Global Iterations</span>
                          <input type="number" value={script.config.run_logic?.global_iterations || 0} placeholder="0 = no shared cap"
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, global_iterations: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Max Transactions</span>
                          <input type="number" value={script.config.run_logic?.max_transactions || 0} placeholder="0 = no transaction cap"
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, max_transactions: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Workload Model</span>
                          <select value={script.config.run_logic?.workload_model || 'closed'}
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, workload_model: e.target.value })}>
                            <option value="closed">Closed VUser</option>
                            <option value="open">Open arrival-rate</option>
                          </select>
                        </label>
                        <label>
                          <span>Auto-Transactions</span>
                          <select value={script.config.run_logic?.auto_transactions || 'none'}
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
                            <option value="none">None</option>
                            <option value="per_action">Per Action</option>
                            <option value="per_iteration">Per Iteration</option>
                          </select>
                        </label>
                        <label>
                          <span>Block Scheduling</span>
                          <select value={script.config.run_logic?.block_scheduling || 'sequential'}
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, block_scheduling: e.target.value })}>
                            <option value="sequential">Sequential</option>
                            <option value="random">Random</option>
                            <option value="percentage">Percentage-based</option>
                          </select>
                        </label>
                        <label className="full-width">
                          <span>Action Sequence</span>
                          <input value={(script.config.run_logic?.action_sequence || []).join(', ')} placeholder="init, action, teardown"
                            onChange={e => updateField('config.run_logic', {
                              ...script.config.run_logic,
                              action_sequence: e.target.value.split(',').map(value => value.trim()).filter(Boolean),
                            })} />
                        </label>
                      </div>
                    </div>
                  )}
                </div>

                {/* Error Handling */}
                <div className={`settings-section ${runtimeSectionIsActive('errors') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('errors')}>
                    <RuntimeSectionIcon section="errors" />
                    <span className="section-label">Error Handling</span>
                    <span className="section-summary">
                      {script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('errors') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('errors') && (
                    <div className="section-body">
                      <div className="form-grid">
                        <div className="runtime-checkbox-list">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.error_handling?.continue_on_error || false}
                              onChange={e => updateField('config.error_handling', { ...script.config.error_handling, continue_on_error: e.target.checked })} />
                            <span>Continue on error</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.error_handling?.snapshot_on_error || false}
                              onChange={e => updateField('config.error_handling', { ...script.config.error_handling, snapshot_on_error: e.target.checked })} />
                            <span>Capture snapshot on error</span>
                          </label>
                        </div>
                        <label>
                          <span>Max Retries</span>
                          <input type="number" value={script.config.error_handling?.max_retries || 0}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
                        </label>
                        {(script.config.error_handling?.max_retries || 0) > 0 && (
                          <>
                            <label>
                              <span>Retry Delay (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.error_handling?.retry_delay_ms} fallbackMs={1000}
                                onChangeMs={value => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: value ?? 0 })} />
                            </label>
                            <label>
                              <span>Retry Backoff</span>
                              <select value={script.config.error_handling?.retry_backoff || 'fixed'}
                                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_backoff: e.target.value })}>
                                <option value="fixed">Fixed</option>
                                <option value="exponential">Exponential</option>
                              </select>
                            </label>
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {isJmsScript && (
                  <div className={`settings-section ${runtimeSectionIsActive('jms') ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('jms')}>
                      <RuntimeSectionIcon section="jms" />
                      <span className="section-label">Solace JMS</span>
                      <span className="section-summary">
                        {script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}
                      </span>
                      <span className="chevron">{runtimeSectionIsActive('jms') ? '▾' : '▸'}</span>
                    </button>
                    {runtimeSectionIsActive('jms') && (
                      <div className="section-body">
                        <div className="settings-sub-group">
                          <h5>Connection</h5>
                          <div className="form-grid">
                            <label className="full-width"><span>Broker URL</span>
                              <input value={script.config.jms?.broker_url || ''} placeholder="tcps://broker.example.com:55443"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', broker_url: e.target.value })} /></label>
                            <label><span>Client ID</span>
                              <input value={script.config.jms?.client_id || ''} placeholder="apexload-{{fn.uuid()}}"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', client_id: e.target.value })} /></label>
                            <label><span>Message VPN</span>
                              <input value={script.config.jms?.message_vpn || ''} placeholder="default"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', message_vpn: e.target.value })} /></label>
                            <label><span>Username</span>
                              <input value={script.config.jms?.username || ''}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', username: e.target.value })} /></label>
                            <label><span>Password</span>
                              <input type="password" value={script.config.jms?.password || ''}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', password: e.target.value })} /></label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.jms?.mock ?? true}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', mock: e.target.checked })} />
                              <span>Use local mock broker for replay</span>
                            </label>
                          </div>
                        </div>
                        <div className="settings-sub-group">
                          <h5>Defaults</h5>
                          <div className="form-grid">
                            <label><span>Destination</span>
                              <input value={script.config.jms?.destination || ''} placeholder="orders.in"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', destination: e.target.value })} /></label>
                            <label><span>Destination Type</span>
                              <select value={script.config.jms?.destination_type || 'queue'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', destination_type: e.target.value })}>
                                <option value="queue">Queue</option>
                                <option value="topic">Topic</option>
                              </select></label>
                            <label><span>Reply To</span>
                              <input value={script.config.jms?.reply_to || ''} placeholder="orders.reply"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', reply_to: e.target.value })} /></label>
                            <label><span>Timeout (s)</span>
                              <SecondsFromMillisecondsInput valueMs={script.config.jms?.timeout_ms} fallbackMs={5000}
                                onChangeMs={value => updateField('config.jms', { ...script.config.jms, provider: 'solace', timeout_ms: value ?? 0 })} /></label>
                            <label><span>Acknowledgement</span>
                              <select value={script.config.jms?.acknowledgement || 'auto'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', acknowledgement: e.target.value })}>
                                <option value="auto">Auto</option>
                                <option value="client">Client</option>
                                <option value="transacted">Transacted</option>
                              </select></label>
                            <label><span>Delivery Mode</span>
                              <select value={script.config.jms?.delivery_mode || 'persistent'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', delivery_mode: e.target.value })}>
                                <option value="persistent">Persistent</option>
                                <option value="non_persistent">Non-persistent</option>
                              </select></label>
                          </div>
                        </div>
                        <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                          IBM MQ remains review-only until a registered runtime adapter is present. This editor executes the JMS Solace adapter and keeps IBM MQ imports visible for manual conversion.
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {isWebSocketScript && (
                  <div className={`settings-section ${runtimeSectionIsActive('websocket') ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('websocket')}>
                      <RuntimeSectionIcon section="websocket" />
                      <span className="section-label">WebSocket Runtime</span>
                      <span className="section-summary">{script.config.websocket?.reconnect ? 'Reconnect enabled' : 'Single connection'}</span>
                      <span className="chevron">{runtimeSectionIsActive('websocket') ? '▾' : '▸'}</span>
                    </button>
                    {runtimeSectionIsActive('websocket') && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Read Timeout (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.websocket?.read_timeout_ms} fallbackMs={30000}
                              onChangeMs={value => updateField('config.websocket', { ...script.config.websocket, read_timeout_ms: value ?? 0 })} /></label>
                          <label><span>Write Timeout (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.websocket?.write_timeout_ms} fallbackMs={10000}
                              onChangeMs={value => updateField('config.websocket', { ...script.config.websocket, write_timeout_ms: value ?? 0 })} /></label>
                          <label><span>Max Message Size</span>
                            <input type="number" value={script.config.websocket?.max_message_size || 1048576}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, max_message_size: Number(e.target.value) })} /></label>
                          <label><span>Subprotocol</span>
                            <input value={script.config.websocket?.subprotocol || ''}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, subprotocol: e.target.value })} placeholder="graphql-transport-ws" /></label>
                          <label><span>Origin</span>
                            <input value={script.config.websocket?.origin || ''}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, origin: e.target.value })} placeholder="https://app.example.com" /></label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.websocket?.compression ?? false}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, compression: e.target.checked })} />
                            <span>Compression</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.websocket?.reconnect ?? true}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, reconnect: e.target.checked })} />
                            <span>Reconnect on failure</span>
                          </label>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {isGrpcScript && (
                  <div className={`settings-section ${runtimeSectionIsActive('grpc') ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('grpc')}>
                      <RuntimeSectionIcon section="grpc" />
                      <span className="section-label">gRPC Runtime</span>
                      <span className="section-summary">{script.config.grpc?.use_reflection ? 'Reflection' : script.config.grpc?.proto_files?.length ? 'Proto files' : 'Contract pending'}</span>
                      <span className="chevron">{runtimeSectionIsActive('grpc') ? '▾' : '▸'}</span>
                    </button>
                    {runtimeSectionIsActive('grpc') && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.grpc?.use_reflection ?? true}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, use_reflection: e.target.checked })} />
                            <span>Use reflection</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.grpc?.plain_text ?? false}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, plain_text: e.target.checked })} />
                            <span>Plaintext channel</span>
                          </label>
                          <label><span>Timeout (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.grpc?.timeout_ms} fallbackMs={30000}
                              onChangeMs={value => updateField('config.grpc', { ...script.config.grpc, timeout_ms: value ?? 0 })} /></label>
                          <label><span>Max Receive Size</span>
                            <input type="number" value={script.config.grpc?.max_recv_msg_size || 4194304}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, max_recv_msg_size: Number(e.target.value) })} /></label>
                          <label><span>Authority</span>
                            <input value={script.config.grpc?.authority || ''}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, authority: e.target.value })} placeholder="api.example.com" /></label>
                          <label className="full-width"><span>Proto Files</span>
                            <input value={(script.config.grpc?.proto_files || []).join(', ')}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, proto_files: e.target.value.split(',').map(value => value.trim()).filter(Boolean) })}
                              placeholder="contracts/orders.proto, contracts/common.proto" /></label>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {isDatabaseScript && (
                  <div className={`settings-section ${runtimeSectionIsActive('database') ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('database')}>
                      <RuntimeSectionIcon section="database" />
                      <span className="section-label">Database Runtime</span>
                      <span className="section-summary">{script.config.database?.dsn_secret ? 'Secret-backed DSN' : script.config.database?.dsn ? 'DSN configured' : 'Not configured'}</span>
                      <span className="chevron">{runtimeSectionIsActive('database') ? '▾' : '▸'}</span>
                    </button>
                    {runtimeSectionIsActive('database') && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Query Timeout (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.database?.query_timeout_ms} fallbackMs={30000}
                              onChangeMs={value => updateField('config.database', { ...script.config.database, query_timeout_ms: value ?? 0 })} /></label>
                          <label><span>Max Rows</span>
                            <input type="number" value={script.config.database?.max_rows || 100}
                              onChange={e => updateField('config.database', { ...script.config.database, max_rows: Number(e.target.value) })} /></label>
                          <label><span>Max Open Connections</span>
                            <input type="number" value={script.config.database?.max_open_conns || 1}
                              onChange={e => updateField('config.database', { ...script.config.database, max_open_conns: Number(e.target.value) })} /></label>
                          <label><span>Max Idle Connections</span>
                            <input type="number" value={script.config.database?.max_idle_conns || 0}
                              onChange={e => updateField('config.database', { ...script.config.database, max_idle_conns: Number(e.target.value) || undefined })} /></label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.database?.read_only ?? true}
                              onChange={e => updateField('config.database', { ...script.config.database, read_only: e.target.checked })} />
                            <span>Read-only guard</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.database?.rollback_on_teardown ?? true}
                              onChange={e => updateField('config.database', { ...script.config.database, rollback_on_teardown: e.target.checked })} />
                            <span>Rollback open transaction on teardown</span>
                          </label>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {isNetworkScript && (
                  <div className={`settings-section ${runtimeSectionIsActive('network') ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('network')}>
                      <RuntimeSectionIcon section="network" />
                      <span className="section-label">Network Runtime</span>
                      <span className="section-summary">{script.config.network?.subprotocol || 'tcp'} · {script.config.network?.host || script.config.network?.address || 'host pending'}</span>
                      <span className="chevron">{runtimeSectionIsActive('network') ? '▾' : '▸'}</span>
                    </button>
                    {runtimeSectionIsActive('network') && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Timeout (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.network?.timeout_ms} fallbackMs={30000}
                              onChangeMs={value => updateField('config.network', { ...script.config.network, timeout_ms: value ?? 0 })} /></label>
                          <label><span>Address Override</span>
                            <input value={script.config.network?.address || ''}
                              onChange={e => updateField('config.network', { ...script.config.network, address: e.target.value })} placeholder="127.0.0.1:2525" /></label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.network?.tls ?? false}
                              onChange={e => updateField('config.network', { ...script.config.network, tls: e.target.checked })} />
                            <span>TLS</span>
                          </label>
                          <label><span>Username</span>
                            <input value={script.config.network?.username || ''}
                              onChange={e => updateField('config.network', { ...script.config.network, username: e.target.value })} /></label>
                          <label><span>Password</span>
                            <input type="password" value={script.config.network?.password || ''}
                              onChange={e => updateField('config.network', { ...script.config.network, password: e.target.value })} /></label>
                          <label><span>LDAP Base DN</span>
                            <input value={script.config.network?.base_dn || ''}
                              onChange={e => updateField('config.network', { ...script.config.network, base_dn: e.target.value })} placeholder="dc=example,dc=com" /></label>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {/* HTTP transport */}
                {isHttpTransportScript && <div className={`settings-section ${runtimeSectionIsActive('http') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('http')}>
                    <RuntimeSectionIcon section="http" />
                    <span className="section-label">HTTP Transport</span>
                    <span className="section-summary">
                      {httpProxySummary !== 'Default' ? httpProxySummary : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('http') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('http') && (
                    <div className="section-body">
                      <div className="http-transport-tabs" role="tablist" aria-label="HTTP Transport option groups">
                        {HTTP_TRANSPORT_TAB_OPTIONS.map(tab => (
                          <button
                            key={tab.id}
                            type="button"
                            role="tab"
                            id={`http-transport-tab-${tab.id}`}
                            aria-controls={`http-transport-panel-${tab.id}`}
                            aria-selected={activeHTTPTransportTab === tab.id}
                            className={`http-transport-tab ${activeHTTPTransportTab === tab.id ? 'active' : ''}`}
                            onClick={() => setActiveHTTPTransportTab(tab.id)}
                          >
                            {tab.label}
                          </button>
                        ))}
                      </div>
                      {activeHTTPTransportTab === 'timeouts' && <div
                        id="http-transport-panel-timeouts"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-timeouts"
                      >
                        <h5>Timeouts</h5>
                        <div className="form-grid">
                          <label><span>Connect (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.http?.connect_timeout_ms} fallbackMs={30000}
                              onChangeMs={value => updateField('config.http', { ...script.config.http, connect_timeout_ms: value ?? 0 })} /></label>
                          <label><span>Request (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.http?.request_timeout_ms} fallbackMs={60000}
                              onChangeMs={value => updateField('config.http', { ...script.config.http, request_timeout_ms: value ?? 0 })} /></label>
                          <label><span>TLS Handshake (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.http?.tls_timeout_ms} fallbackMs={10000}
                              onChangeMs={value => updateField('config.http', { ...script.config.http, tls_timeout_ms: value ?? 0 })} /></label>
                          <label><span>Idle Connection (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.http?.idle_timeout_ms} fallbackMs={90000}
                              onChangeMs={value => updateField('config.http', { ...script.config.http, idle_timeout_ms: value ?? 0 })} /></label>
                        </div>
                      </div>}
                      {activeHTTPTransportTab === 'connection' && <div
                        id="http-transport-panel-connection"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-connection"
                      >
                        <h5>Connection</h5>
                        <div className="form-grid">
                          <label><span>HTTP Version</span>
                            <select value={script.config.http?.version || 'auto'}
                              onChange={e => updateField('config.http', { ...script.config.http, version: e.target.value })}>
                              <option value="auto">Auto-negotiate</option>
                              <option value="1.1">HTTP/1.1</option>
                              <option value="2">HTTP/2</option>
                            </select></label>
                          <label><span>Max Conns/Host</span>
                            <input type="number" value={script.config.http?.max_conns_per_host || 100}
                              onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
                          <label><span>Max Redirects</span>
                            <input type="number" value={script.config.http?.max_redirects ?? 10}
                              onChange={e => updateField('config.http', { ...script.config.http, max_redirects: Number(e.target.value) })} /></label>
                        </div>
                      </div>}
                      {activeHTTPTransportTab === 'tls' && <div
                        id="http-transport-panel-tls"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-tls"
                      >
                        <h5>TLS / SSL</h5>
                        <div className="form-grid">
                          <label><span>Min TLS Version</span>
                            <select value={script.config.http?.tls?.min_version || '1.2'}
                              onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                              <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                              <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
                            </select></label>
                          <div className="runtime-checkbox-list">
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                                onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                              <span>Skip certificate verification</span>
                            </label>
                          </div>
                        </div>
                      </div>}
                      {activeHTTPTransportTab === 'proxy' && <div
                        id="http-transport-panel-proxy"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-proxy"
                      >
                        <h5>Proxy</h5>
                        <div className="form-grid">
                          <label>
                            <span>Proxy Mode</span>
                            <select value={httpProxyMode}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                              <option value="none">No Proxy</option>
                              <option value="system">Use system proxy</option>
                              <option value="custom">Custom proxy</option>
                            </select>
                          </label>
                          <label className="full-width"><span>Proxy URL</span>
                            <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy:8080"
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
                          {httpProxyMode === 'custom' && (
                            <>
                              <label><span>Proxy Username</span>
                                <input value={script.config.http?.proxy?.username || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                              <label><span>Proxy Password</span>
                                <input type="password" value={script.config.http?.proxy?.password || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                            </>
                          )}
                          <label className="full-width"><span>Bypass Hosts</span>
                            <textarea rows={3} value={formatLineList(script.config.http?.proxy?.no_proxy)} placeholder={'localhost\n*.internal.example.com'}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, no_proxy: parseLineList(e.target.value) } })} /></label>
                        </div>
                      </div>}
                      {activeHTTPTransportTab === 'authentication' && <div
                        id="http-transport-panel-authentication"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-authentication"
                      >
                        <h5>Authentication</h5>
                        <div className="form-grid">
                          <label><span>Auth Type</span>
                            <select value={script.config.http?.auth?.type || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, type: e.target.value } })}>
                              <option value="">None</option>
                              <option value="basic">Basic</option>
                              <option value="bearer">Bearer Token</option>
                              <option value="ntlm">NTLM</option>
                            </select></label>
                          {(script.config.http?.auth?.type === 'basic' || script.config.http?.auth?.type === 'ntlm') && (
                            <>
                              <label><span>Username</span>
                                <input value={script.config.http?.auth?.username || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, username: e.target.value } })} /></label>
                              <label><span>Password</span>
                                <input type="password" value={script.config.http?.auth?.password || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, password: e.target.value } })} /></label>
                            </>
                          )}
                          {script.config.http?.auth?.type === 'bearer' && (
                            <label className="full-width"><span>Token</span>
                              <input value={script.config.http?.auth?.token || ''} placeholder="eyJhbGciOiJIUzI1NiIs..."
                                onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, token: e.target.value } })} /></label>
                          )}
                        </div>
                      </div>}
                      {activeHTTPTransportTab === 'browser' && <div
                        id="http-transport-panel-browser"
                        className="settings-sub-group http-transport-tab-panel"
                        role="tabpanel"
                        aria-labelledby="http-transport-tab-browser"
                      >
                        <h5>Browser Emulation</h5>
                        <div className="form-grid">
                          <label>
                            <span>Browser Engine</span>
                            <select value={script.config.browser?.engine || 'chromium'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, engine: e.target.value })}>
                              <option value="chromium">Chromium</option>
                              <option value="webkit">WebKit</option>
                            </select>
                          </label>
                          <label>
                            <span>Device Profile</span>
                            <select value={script.config.browser?.device_profile || ''}
                              onChange={e => {
                                const nextDeviceProfile = e.target.value || undefined
                                const nextProfile = lookupBrowserDeviceProfile(nextDeviceProfile)
                                updateField('config.browser', {
                                  ...script.config.browser,
                                  device_profile: nextDeviceProfile,
                                  engine: nextProfile?.engine || script.config.browser?.engine,
                                })
                              }}>
                              <option value="">Custom viewport</option>
                              {BROWSER_DEVICE_PROFILES.map(profile => <option key={profile.name} value={profile.name}>{formatBrowserDeviceProfileLabel(profile)}</option>)}
                            </select>
                          </label>
                          {browserEngineAdvisory && (
                            <div className="browser-config-warning">{browserEngineAdvisory}</div>
                          )}
                          <label>
                            <span>Orientation</span>
                            <select value={script.config.browser?.orientation || ''}
                              onChange={e => updateField('config.browser', { ...script.config.browser, orientation: e.target.value || undefined })}>
                              <option value="">Profile default</option>
                              <option value="portrait">Portrait</option>
                              <option value="landscape">Landscape</option>
                            </select>
                          </label>
                          <label><span>Viewport Width</span>
                            <input type="number" min="0" value={script.config.browser?.viewport?.width ?? ''} placeholder="393"
                              onChange={e => updateField('config.browser', {
                                ...script.config.browser,
                                viewport: {
                                  ...script.config.browser?.viewport,
                                  width: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                                },
                              })} /></label>
                          <label><span>Viewport Height</span>
                            <input type="number" min="0" value={script.config.browser?.viewport?.height ?? ''} placeholder="852"
                              onChange={e => updateField('config.browser', {
                                ...script.config.browser,
                                viewport: {
                                  ...script.config.browser?.viewport,
                                  height: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                                },
                              })} /></label>
                          <label><span>Slow Mo (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.browser?.slow_mo_ms} emptyWhenUndefined placeholder="0.05"
                              onChangeMs={value => updateField('config.browser', { ...script.config.browser, slow_mo_ms: value })} /></label>
                          <label><span>Capture Screenshots</span>
                            <select value={script.config.browser?.capture_screenshots || 'per_action'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, capture_screenshots: e.target.value })}>
                              <option value="off">Off</option>
                              <option value="on_error">On error</option>
                              <option value="per_action">Per action</option>
                            </select>
                          </label>
                          <label className="full-width"><span>User-Agent</span>
                            <input value={script.config.http?.user_agent || ''} placeholder="Mozilla/5.0 ..."
                              onChange={e => updateField('config.http', { ...script.config.http, user_agent: e.target.value })} /></label>
                          <label><span>Accept-Encoding</span>
                            <input value={script.config.http?.accept_encoding || ''} placeholder="gzip, deflate, br"
                              onChange={e => updateField('config.http', { ...script.config.http, accept_encoding: e.target.value })} /></label>
                          <label><span>Accept-Language</span>
                            <input value={script.config.http?.accept_language || ''} placeholder="en-US,en;q=0.9"
                              onChange={e => updateField('config.http', { ...script.config.http, accept_language: e.target.value })} /></label>
                          <label>
                            <span>Download Resources</span>
                            <select value={script.config.browser?.download_resources || 'recorded'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, download_resources: e.target.value })}>
                              <option value="none">HTML only</option>
                              <option value="recorded">Recorded resources</option>
                              <option value="all">All non-HTML resources</option>
                            </select>
                          </label>
                          <div className="browser-emulation-checkbox-list" aria-label="Browser emulation toggles">
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.headless ?? true}
                                onChange={e => updateField('config.browser', { ...script.config.browser, headless: e.target.checked })} />
                              <span>Run headless</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.touch_mode ?? false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, touch_mode: e.target.checked })} />
                              <span>Force touch mode</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.client_metrics ?? false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, client_metrics: e.target.checked })} />
                              <span>Capture client metrics</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.review_policy?.allow_reviewed_javascript || false}
                                onChange={e => updateField('config.browser', {
                                  ...script.config.browser,
                                  review_policy: {
                                    ...script.config.browser?.review_policy,
                                    allow_reviewed_javascript: e.target.checked,
                                  },
                                })} />
                              <span>Allow reviewed JavaScript</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.review_policy?.allow_review_required || false}
                                onChange={e => updateField('config.browser', {
                                  ...script.config.browser,
                                  review_policy: {
                                    ...script.config.browser?.review_policy,
                                    allow_review_required: e.target.checked,
                                  },
                                })} />
                              <span>Allow review-required actions</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.cache_enabled || false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, cache_enabled: e.target.checked })} />
                              <span>Simulate browser cache</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.cache_content_urls || false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, cache_content_urls: e.target.checked })} />
                              <span>Cache URLs requiring content</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.check_newer_versions || false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, check_newer_versions: e.target.checked })} />
                              <span>Check for newer cached pages</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.browser?.new_user_per_iteration || false}
                                onChange={e => updateField('config.browser', { ...script.config.browser, new_user_per_iteration: e.target.checked })} />
                              <span>Simulate a new user each iteration</span>
                            </label>
                          </div>
                        </div>
                      </div>}
                    </div>
                  )}
                </div>}

                {isSoapScript && <div className={`settings-section ${runtimeSectionIsActive('soap') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('soap')}>
                    <RuntimeSectionIcon section="soap" />
                    <span className="section-label">SOAP Settings</span>
                    <span className="section-summary">
                      {script.config.soap?.ws_security?.username_token?.username
                        ? 'WS-Security configured'
                        : script.config.soap?.version || '1.1'}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('soap') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('soap') && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Envelope</h5>
                        <div className="form-grid">
                          <label>
                            <span>SOAP Version</span>
                            <select value={script.config.soap?.version || '1.1'} onChange={e => updateField('config.soap', {
                              ...(script.config.soap || {}),
                              version: e.target.value,
                              namespaces: syncSOAPEnvelopeNamespace(
                                e.target.value,
                                script.config.soap?.namespaces,
                                script.config.soap?.version,
                              ),
                            })}>
                              <option value="1.1">SOAP 1.1</option>
                              <option value="1.2">SOAP 1.2</option>
                            </select>
                          </label>
                          <div className="runtime-checkbox-list">
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.soap?.fault_handling?.fail_on_fault ?? true} onChange={e => updateField('config.soap.fault_handling.fail_on_fault', e.target.checked)} />
                              <span>Fail on SOAP Fault</span>
                            </label>
                          </div>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Namespaces</h4><button className="btn btn-sm btn-ghost" onClick={addSoapNamespace}>+ Add</button></div>
                        {Object.entries(script.config.soap?.namespaces || {}).map(([key, value]) => (
                          <div key={key} className="kv-row">
                            <input className="kv-key" value={key} onChange={e => updateSoapNamespace(key, e.target.value, value)} placeholder="Prefix" />
                            <input className="kv-val" value={value} onChange={e => updateSoapNamespace(key, key, e.target.value)} placeholder="Namespace URI" />
                            <button className="btn-icon danger" onClick={() => removeSoapNamespace(key)}>✕</button>
                          </div>
                        ))}
                      </div>

                      <div className="settings-sub-group">
                        <h5>WS-Security UsernameToken</h5>
                        <div className="form-grid">
                          <label>
                            <span>Username</span>
                            <input value={script.config.soap?.ws_security?.username_token?.username || ''} onChange={e => updateField('config.soap.ws_security.username_token.username', e.target.value)} placeholder="service-user" />
                          </label>
                          <label>
                            <span>Password</span>
                            <input type="password" value={script.config.soap?.ws_security?.username_token?.password || ''} onChange={e => updateField('config.soap.ws_security.username_token.password', e.target.value)} placeholder="secret" />
                          </label>
                          <label>
                            <span>Password Type</span>
                            <select value={script.config.soap?.ws_security?.username_token?.password_type || 'text'} onChange={e => updateField('config.soap.ws_security.username_token.password_type', e.target.value)}>
                              <option value="text">text</option>
                              <option value="digest">digest</option>
                            </select>
                          </label>
                          <div className="runtime-checkbox-list">
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.nonce || false} onChange={e => updateField('config.soap.ws_security.username_token.nonce', e.target.checked)} />
                              <span>Include nonce</span>
                            </label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.created || false} onChange={e => updateField('config.soap.ws_security.username_token.created', e.target.checked)} />
                              <span>Include created timestamp</span>
                            </label>
                          </div>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <h5>WS-Security Timestamp</h5>
                        <div className="form-grid">
                          <label>
                            <span>TTL (seconds)</span>
                            <input type="number" min="0" value={script.config.soap?.ws_security?.timestamp?.ttl_seconds ?? 0} onChange={e => updateField('config.soap.ws_security.timestamp.ttl_seconds', Math.max(0, Number(e.target.value) || 0))} />
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>}

                {isJavaHttpScript && <div className={`settings-section ${runtimeSectionIsActive('java_http') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('java_http')}>
                    <RuntimeSectionIcon section="java_http" />
                    <span className="section-label">Java HTTP Settings</span>
                    <span className="section-summary">{`${script.config.java_http?.codec || 'java_serialized_object_base64'} · ${script.config.java_http?.helper_mode || 'embedded'}`}</span>
                    <span className="chevron">{runtimeSectionIsActive('java_http') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('java_http') && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Codec</h5>
                        <div className="form-grid">
                          <label>
                            <span>Wire Format</span>
                            <select value={script.config.java_http?.codec || 'java_serialized_object_base64'} onChange={e => updateField('config.java_http.codec', e.target.value)}>
                              <option value="java_serialized_object_base64">java_serialized_object_base64</option>
                            </select>
                          </label>
                          <label>
                            <span>Helper Mode</span>
                            <select value={script.config.java_http?.helper_mode || 'embedded'} onChange={e => updateField('config.java_http.helper_mode', e.target.value)}>
                              <option value="embedded">embedded</option>
                              <option value="sidecar">sidecar</option>
                            </select>
                          </label>
                          <label>
                            <span>Body Encoding</span>
                            <select value={script.config.java_http?.body_encoding || 'base64'} onChange={e => updateField('config.java_http.body_encoding', e.target.value)}>
                              <option value="base64">base64</option>
                            </select>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <h5>Transport Defaults</h5>
                        <div className="form-grid">
                          <label>
                            <span>Content-Type</span>
                            <input value={script.config.java_http?.content_type || ''} onChange={e => updateField('config.java_http.content_type', e.target.value)} placeholder="application/x-java-serialized-object" />
                          </label>
                          <label>
                            <span>Accept</span>
                            <input value={script.config.java_http?.accept || ''} onChange={e => updateField('config.java_http.accept', e.target.value)} placeholder="application/x-java-serialized-object" />
                          </label>
                          <label>
                            <span>Object Schema</span>
                            <input value={script.config.java_http?.object_schema || ''} onChange={e => updateField('config.java_http.object_schema', e.target.value)} placeholder="com.example.AuthRequest" />
                          </label>
                          <label>
                            <span>Extractor Type</span>
                            <input value={script.config.java_http?.extractor_type || ''} onChange={e => updateField('config.java_http.extractor_type', e.target.value)} placeholder="opaque" />
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.java_http?.preserve_binary_result ?? true} onChange={e => updateField('config.java_http.preserve_binary_result', e.target.checked)} />
                            <span>Preserve binary responses as base64 text</span>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Request Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('request_mapping')}>+ Add</button></div>
                        <div className="cell-muted" style={{ marginBottom: 8 }}>Mappings are stored with the draft for future helper-aware serializers. Current Java-over-HTTP execution still sends opaque base64 payloads.</div>
                        {Object.entries(script.config.java_http?.request_mapping || {}).length === 0 && <div className="cell-muted">No request mapping entries defined.</div>}
                        {Object.entries(script.config.java_http?.request_mapping || {}).map(([key, value]) => (
                          <div key={`request-${key}`} className="pair-row">
                            <input value={key} onChange={e => updateJavaHTTPMapping('request_mapping', key, e.target.value, value)} placeholder="logical field" />
                            <input value={value} onChange={e => updateJavaHTTPMapping('request_mapping', key, key, e.target.value)} placeholder="serialized property path" />
                            <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('request_mapping', key)}>✕</button>
                          </div>
                        ))}
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Response Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('response_mapping')}>+ Add</button></div>
                        {Object.entries(script.config.java_http?.response_mapping || {}).length === 0 && <div className="cell-muted">No response mapping entries defined.</div>}
                        {Object.entries(script.config.java_http?.response_mapping || {}).map(([key, value]) => (
                          <div key={`response-${key}`} className="pair-row">
                            <input value={key} onChange={e => updateJavaHTTPMapping('response_mapping', key, e.target.value, value)} placeholder="serialized property path" />
                            <input value={value} onChange={e => updateJavaHTTPMapping('response_mapping', key, key, e.target.value)} placeholder="replay variable or logical field" />
                            <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('response_mapping', key)}>✕</button>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>}

                {isHttpScript && <div className={`settings-section ${runtimeSectionIsActive('web_runtime') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('web_runtime')}>
                    <RuntimeSectionIcon section="web_runtime" />
                    <span className="section-label">Web HTTP/HTML Compatibility</span>
                    <span className="section-summary">
                      {httpProxySummary !== 'Default'
                        ? httpProxySummary
                        : script.config.web_runtime?.content_checks?.length
                        ? `${script.config.web_runtime.content_checks.length} content checks`
                        : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
                    </span>
                    <span className="chevron">{runtimeSectionIsActive('web_runtime') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('web_runtime') && (
                    <div className="section-body">
                      <div className="cell-muted" style={{ marginBottom: 12 }}>
                        These settings are captured in the draft now so Web HTTP/HTML scripts can mirror source runtime coverage even where full APEX Load execution support is still rolling out.
                      </div>
                      <div className="settings-sub-group">
                        <h5>Proxy Routing</h5>
                        <div className="form-grid">
                          <label>
                            <span>Proxy Mode</span>
                            <select value={httpProxyMode}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                              <option value="none">No proxy</option>
                              <option value="system">Use environment proxy</option>
                              <option value="custom">Custom proxy</option>
                            </select>
                          </label>
                          <label className="full-width"><span>Proxy URL</span>
                            <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy.example.test:8080"
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
                          {httpProxyMode === 'custom' && (
                            <>
                              <label><span>Proxy Username</span>
                                <input value={script.config.http?.proxy?.username || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                              <label><span>Proxy Password</span>
                                <input type="password" value={script.config.http?.proxy?.password || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                            </>
                          )}
                          <label className="full-width"><span>Bypass Hosts</span>
                            <textarea rows={3} value={formatLineList(script.config.http?.proxy?.no_proxy)} placeholder={'localhost\n*.internal.example.com'}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, no_proxy: parseLineList(e.target.value) } })} /></label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Additional Attributes</h5>
                        <label className="full-width">
                          <span>Name=Value pairs</span>
                          <textarea rows={4} placeholder={'env=perf\nregion=east'} value={formatKeyValueLines(script.config.web_runtime?.additional_attributes)}
                            onChange={e => updateField('config.web_runtime', {
                              ...script.config.web_runtime,
                              additional_attributes: parseKeyValueLines(e.target.value),
                            })} />
                        </label>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Download Filters</h5>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.download_filters?.mode || 'none'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                download_filters: {
                                  ...script.config.web_runtime?.download_filters,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="none">No filters</option>
                              <option value="allow_list">Allow list</option>
                              <option value="block_list">Block list</option>
                            </select>
                          </label>
                          <label className="full-width">
                            <span>Hosts</span>
                            <textarea rows={4} placeholder={'cdn.example.test\nmetrics.example.test'} value={formatLineList(script.config.web_runtime?.download_filters?.hosts)}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                download_filters: {
                                  ...script.config.web_runtime?.download_filters,
                                  hosts: parseLineList(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Content Check</h5>
                        <label className="full-width">
                          <span>One per line: text|scope|fail_if_found</span>
                          <textarea rows={4} placeholder={'Access Denied|any_response|true'} value={formatContentChecks(script.config.web_runtime?.content_checks)}
                            onChange={e => updateField('config.web_runtime', {
                              ...script.config.web_runtime,
                              content_checks: parseContentChecks(e.target.value),
                            })} />
                        </label>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Network Emulation</h5>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.network_simulation?.mode || 'max'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="max">Maximum throughput</option>
                              <option value="preset">Preset profile</option>
                              <option value="custom">Custom profile</option>
                            </select>
                          </label>
                          <label>
                            <span>Preset</span>
                            <select value={script.config.web_runtime?.network_simulation?.preset || 'dsl'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  preset: e.target.value,
                                },
                              })}>
                              <option value="56k">56k</option>
                              <option value="dsl">DSL</option>
                              <option value="t1">T1</option>
                              <option value="10mbps">10 Mbps</option>
                            </select>
                          </label>
                          <label>
                            <span>Bandwidth (Kbps)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.bandwidth_kbps || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  bandwidth_kbps: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Latency (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.web_runtime?.network_simulation?.latency_ms}
                              onChangeMs={value => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  latency_ms: value ?? 0,
                                },
                              })} />
                          </label>
                          <label>
                            <span>Jitter (s)</span>
                            <SecondsFromMillisecondsInput valueMs={script.config.web_runtime?.network_simulation?.jitter_ms}
                              onChangeMs={value => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  jitter_ms: value ?? 0,
                                },
                              })} />
                          </label>
                          <label>
                            <span>Packet Loss (%)</span>
                            <input type="number" min="0" max="100" step="0.1" value={script.config.web_runtime?.network_simulation?.packet_loss_pct || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  packet_loss_pct: Number(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group" data-testid="web-runtime-source-ips">
                        <h5>Source IPs (IP spoofing)</h5>
                        <div className="cell-muted" style={{ marginBottom: 12 }}>
                          One IP per line. VUsers bind outgoing connections round-robin across this pool so the test presents many client addresses. The generator host must own these addresses.
                        </div>
                        <textarea
                          aria-label="Source IP pool"
                          rows={3}
                          value={(script.config.web_runtime?.source_ips || []).join('\n')}
                          onChange={e => updateField('config.web_runtime', {
                            ...script.config.web_runtime,
                            source_ips: e.target.value.split('\n'),
                          })}
                          placeholder={'10.0.0.5\n10.0.0.6'}
                        />
                      </div>
                      <div className="settings-sub-group" data-testid="web-runtime-network-profiles">
                        <h5>Network Profile Mix</h5>
                        <div className="cell-muted" style={{ marginBottom: 12 }}>
                          Distribute VUsers across weighted network conditions (e.g. 30% 3G / 70% 4G). Overrides the single profile above. Per-profile response metrics appear in results.
                        </div>
                        {(script.config.web_runtime?.network_profiles || []).map((profile, i) => (
                          <div key={`net-profile-${i}`} className="form-grid" style={{ marginBottom: 8 }}>
                            <label>
                              <span>Name</span>
                              <input aria-label="Profile name" value={profile.name || ''}
                                onChange={e => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, name: e.target.value } : p),
                                })} />
                            </label>
                            <label>
                              <span>Weight</span>
                              <input type="number" min="1" aria-label="Profile weight" value={profile.weight || 0}
                                onChange={e => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, weight: Number(e.target.value) } : p),
                                })} />
                            </label>
                            <label>
                              <span>Mode</span>
                              <select value={profile.mode || 'custom'}
                                onChange={e => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, mode: e.target.value } : p),
                                })}>
                                <option value="custom">Custom</option>
                                <option value="preset">Preset</option>
                                <option value="max">Maximum</option>
                              </select>
                            </label>
                            <label>
                              <span>Bandwidth (Kbps)</span>
                              <input type="number" aria-label="Profile bandwidth" value={profile.bandwidth_kbps || 0}
                                onChange={e => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, bandwidth_kbps: Number(e.target.value) } : p),
                                })} />
                            </label>
                            <label>
                              <span>Latency (s)</span>
                              <SecondsFromMillisecondsInput ariaLabel="Profile latency" valueMs={profile.latency_ms}
                                onChangeMs={value => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, latency_ms: value ?? 0 } : p),
                                })} />
                            </label>
                            <button type="button" className="btn btn-ghost" onClick={() => updateField('config.web_runtime', {
                              ...script.config.web_runtime,
                              network_profiles: (script.config.web_runtime?.network_profiles || []).filter((_, idx) => idx !== i),
                            })}>Remove</button>
                          </div>
                        ))}
                        <button type="button" className="btn btn-secondary" onClick={() => updateField('config.web_runtime', {
                          ...script.config.web_runtime,
                          network_profiles: [...(script.config.web_runtime?.network_profiles || []), { name: '', weight: 1, mode: 'custom' }],
                        })}>Add Network Profile</button>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Streaming</h5>
                        <div className="cell-muted" style={{ marginBottom: 12 }}>
                          Streaming settings apply runtime pacing to media-like HTTP responses.
                        </div>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.streaming?.mode || 'adaptive'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="adaptive">Adaptive bitrate</option>
                              <option value="fixed">Fixed bitrate</option>
                            </select>
                          </label>
                          <label>
                            <span>Buffer Size (s)</span>
                            <SecondsFromMillisecondsInput valueMs={(script.config.web_runtime?.streaming?.buffer_size_seconds || 0) * 1000}
                              onChangeMs={value => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  buffer_size_seconds: (value ?? 0) / 1000,
                                },
                              })} />
                          </label>
                          <label>
                            <span>Fixed Bitrate (Kbps)</span>
                            <input type="number" value={script.config.web_runtime?.streaming?.fixed_bitrate_kbps || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  fixed_bitrate_kbps: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Bandwidth Estimation</span>
                            <select value={script.config.web_runtime?.streaming?.bandwidth_estimation || 'rolling'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  bandwidth_estimation: e.target.value,
                                },
                              })}>
                              <option value="rolling">Rolling</option>
                              <option value="instant">Instant</option>
                            </select>
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Data Formatting</h5>
                        <div className="form-grid">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.web_runtime?.data_formatting?.auto_detect || false}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                data_formatting: {
                                  ...script.config.web_runtime?.data_formatting,
                                  auto_detect: e.target.checked,
                                },
                              })} />
                            <span>Auto-detect data format extensions</span>
                          </label>
                          <label className="full-width">
                            <span>DFE Chain</span>
                            <textarea rows={4} placeholder={'base64\ngzip mode=decode\njson_to_xml'} value={formatDFEChain(script.config.web_runtime?.data_formatting?.chain)}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                data_formatting: {
                                  ...script.config.web_runtime?.data_formatting,
                                  chain: parseDFEChain(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>}

                {/* Logging */}
                <div className={`settings-section ${runtimeSectionIsActive('log') ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => selectRuntimeSettingsSection('log')}>
                    <RuntimeSectionIcon section="log" />
                    <span className="section-label">Logging</span>
                    <span className="section-summary">{script.config.log?.level || 'standard'}</span>
                    <span className="chevron">{runtimeSectionIsActive('log') ? '▾' : '▸'}</span>
                  </button>
                  {runtimeSectionIsActive('log') && (
                    <div className="section-body">
                      <div className="logging-settings-layout">
                        <label>
                          <span>Log Level</span>
                          <select value={script.config.log?.level || 'standard'}
                            onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
                            <option value="disabled">Disabled</option>
                            <option value="standard">Standard</option>
                            <option value="extended">Extended (verbose)</option>
                          </select>
                        </label>
                        <div className="logging-checkbox-group runtime-checkbox-list">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.log?.on_error_only || false}
                              onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
                            <span>Extended log on error only</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.log?.extended_options?.includes('parameter_substitution') || false}
                              onChange={e => updateField('config.log', {
                                ...script.config.log,
                                extended_options: e.target.checked
                                  ? [...new Set([...(script.config.log?.extended_options || []), 'parameter_substitution'])]
                                  : (script.config.log?.extended_options || []).filter(option => option !== 'parameter_substitution'),
                              })} />
                            <span>Parameter substitution</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.log?.extended_options?.includes('data_returned') || false}
                              onChange={e => updateField('config.log', {
                                ...script.config.log,
                                extended_options: e.target.checked
                                  ? [...new Set([...(script.config.log?.extended_options || []), 'data_returned'])]
                                  : (script.config.log?.extended_options || []).filter(option => option !== 'data_returned'),
                              })} />
                            <span>Data returned by server</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.log?.extended_options?.includes('advanced_trace') || false}
                              onChange={e => updateField('config.log', {
                                ...script.config.log,
                                extended_options: e.target.checked
                                  ? [...new Set([...(script.config.log?.extended_options || []), 'advanced_trace'])]
                                  : (script.config.log?.extended_options || []).filter(option => option !== 'advanced_trace'),
                              })} />
                            <span>Advanced trace</span>
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>
                </div>
              </div>
              </section>
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
                  onMouseDown={startAuthorRailDrag}
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
                            autoFocus
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
                              onClick={() => selectAuthorNestedItem(item)}
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
                            <div key={item.key} {...virtualRowProps} className={`action-item action-item-nested lifecycle-action ${containerSelected ? 'active' : ''}`} onClick={() => selectAuthorNestedItem(item)}>
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
                            onClick={() => selectAuthorPhaseAction(row)}
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
                <section className="outline-section parameter-outline-section">
                  <div className="actions-sidebar-header outline-section-header">
                    <button
                      type="button"
                      className="outline-section-toggle"
                      aria-expanded={!outlineParameterListCollapsed}
                      onClick={() => setOutlineParameterListCollapsed(collapsed => !collapsed)}
                    >
                      <span aria-hidden="true" className="outline-section-chevron">{outlineParameterListCollapsed ? '▸' : '▾'}</span>
                      <span>
                        <strong>Parameter List</strong>
                        <small>{script.parameters?.length || 0} defined</small>
                      </span>
                    </button>
                    <div className="parameter-header-actions">
                      <button type="button" className="btn btn-sm btn-ghost" onClick={scanForAutoParameters} title="Auto Parameterization — quick scan for hardcoded values to externalize">
                        Auto Params
                      </button>
                      {isEditingExisting && (
                        <button type="button" className="btn btn-sm btn-ghost" onClick={runServerParameterization} disabled={deepScanning} title="Deep scan (server) — backend engine also flags secrets">
                          {deepScanning ? 'Scanning…' : 'Deep Scan'}
                        </button>
                      )}
                    </div>
                  </div>
                  {!outlineParameterListCollapsed && (
                    <>
                      <div className="parameter-outline-list">
                        {(script.parameters || []).map((parameter, index) => {
                          const parameterName = parameter.name || `Parameter ${index + 1}`
                          const referenceCount = parameter.name ? parameterReferenceCounts.get(parameter.name) || 0 : 0
                          const selected = selectedOutlineParameterName === parameter.name
                          return (
                            <button
                              key={`${parameter.name || 'parameter'}-${index}`}
                              type="button"
                              className={`parameter-outline-item ${selected ? 'selected' : ''}`}
                              onClick={() => parameter.name && selectOutlineParameter(parameter.name)}
                              disabled={!parameter.name}
                            >
                              <strong>{parameterName}</strong>
                              <span>{parameter.type || 'list'} · {referenceCount} ref{referenceCount === 1 ? '' : 's'}</span>
                            </button>
                          )
                        })}
                        {(script.parameters || []).length === 0 && (
                          <div className="action-empty parameter-outline-empty">No parameters defined yet.</div>
                        )}
                      </div>
                      <div className="parameter-outline-actions">
                        <p className="parameter-outline-hint">
                          {isEditingExisting
                            ? 'Quick scan finds hardcoded values; deep scan also flags secrets via the server engine.'
                            : 'Build your requests, then scan to externalize hardcoded values as you go.'}
                        </p>
                      </div>
                    </>
                  )}
                </section>
                {importedParameterFileMappings.length > 0 ? (
                  <section className="outline-section import-review-section">
                    <div className="actions-sidebar-header outline-section-header">
                      <button
                        type="button"
                        className="outline-section-toggle"
                        aria-expanded={!importReviewCollapsed}
                        onClick={() => setImportReviewCollapsed(collapsed => !collapsed)}
                      >
                        <span aria-hidden="true" className="outline-section-chevron">{importReviewCollapsed ? '▸' : '▾'}</span>
                        <span>
                          <strong>Import Review</strong>
                          <small>{importedParameterFileMappings.length} parameter file{importedParameterFileMappings.length === 1 ? '' : 's'}</small>
                        </span>
                      </button>
                    </div>
                    {!importReviewCollapsed && (
                      <div className="starter-blocks-panel parameter-mappings-launcher">
                        <div className="starter-blocks-header">
                          <strong>Parameter File Mappings</strong>
                          <span>{importedParameterFileMappings.length}</span>
                        </div>
                        <p>Review how the source tool's parameter files mapped into APEX data sources. Most useful right after import.</p>
                        <button
                          type="button"
                          className="btn btn-sm btn-secondary parameter-mappings-launcher-button"
                          onClick={() => setShowImportedParameterMappingsDialog(true)}
                        >
                          Open Mapping List
                        </button>
                      </div>
                    )}
                  </section>
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
                  <section id="imported-metadata-section" className="center-document import-metadata-panel config-panel imported-metadata-section open" aria-label="Source Mapping">
                    <div className="import-metadata-body">
                      {sourceReview && (
                        <SourceMappingReview
                          focusRequestId={sourceMappingFocus.requestId}
                          focusedMappingId={sourceMappingFocus.mappingId}
                          id="imported-source-mapping"
                          onToggleWorkspaceMaximized={toggleSourceMappingWorkspaceMaximized}
                          showTitle={false}
                          sourceReview={sourceReview}
                          script={script}
                          title="Source Mapping"
                          workspaceMaximized={sourceMappingWorkspaceMaximized}
                        />
                      )}

                      {trueClientPromotionReview && (
                        <div className="import-correlation-section">
                          <h4>TruClient Promotion Review</h4>
                          <p>Saved promotion evidence for this imported browser script, including replay coverage, JavaScript review gates, selector fallbacks, and frame/window blockers.</p>
                          <div className="import-metadata-grid">
                            <div className="import-metadata-card">
                              <span>Status</span>
                              <strong>{formatTrueClientPromotionStatus(trueClientPromotionReview.status)}</strong>
                              <small>{trueClientPromotionReview.compatibility.promotionAllowed ? 'Promotion allowed by report' : 'Review gates still apply'}</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Source Profile</span>
                              <strong>{trueClientPromotionReview.compatibility.sourceProfile || 'Unknown'}</strong>
                              <small>{trueClientPromotionReview.sourceScriptName || trueClientPromotionReview.source}</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Replay Evidence</span>
                              <strong>{trueClientPromotionReview.replayEvidence.events}</strong>
                              <small>{trueClientPromotionReview.replayEvidence.passed} passed / {trueClientPromotionReview.replayEvidence.failed} failed</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Replay Session</span>
                              <strong>{trueClientPromotionReview.replayEvidence.sessionId || trueClientPromotionReview.replayEvidence.status}</strong>
                              <small>{trueClientPromotionReview.replayEvidence.completedAt ? `Completed ${formatReplayTime(trueClientPromotionReview.replayEvidence.completedAt)}` : 'No completed timestamp'}</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Artifacts</span>
                              <strong>{trueClientPromotionReview.replayEvidence.screenshots} / {trueClientPromotionReview.replayEvidence.domHtmlCaptures}</strong>
                              <small>screenshots / DOM or HTML captures</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Console & Network</span>
                              <strong>{trueClientPromotionReview.replayEvidence.consoleEntries} / {trueClientPromotionReview.replayEvidence.networkEntries}</strong>
                              <small>console entries / network entries</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Review Actions</span>
                              <strong>{trueClientPromotionReview.gates.reviewRequiredActions}</strong>
                              <small>{trueClientPromotionReview.gates.javascriptReviewActions} JavaScript-gated action{trueClientPromotionReview.gates.javascriptReviewActions === 1 ? '' : 's'}</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Selector Fallbacks</span>
                              <strong>{trueClientPromotionReview.gates.selectorFallbackCandidates}</strong>
                              <small>{trueClientPromotionReview.compatibility.selectorConfidencePct ? `${trueClientPromotionReview.compatibility.selectorConfidencePct}% selector confidence` : 'No selector score recorded'}</small>
                            </div>
                            <div className="import-metadata-card">
                              <span>Frame / Window</span>
                              <strong>{trueClientPromotionReview.compatibility.frameWindowBlockers}</strong>
                              <small>{trueClientPromotionReview.compatibility.replayStatus || 'Replay status not recorded'}</small>
                            </div>
                          </div>
                          {(trueClientPromotionReview.gates.importWarnings.length > 0 || trueClientPromotionReview.gates.replayWarnings.length > 0 || trueClientPromotionReview.nextActions.length > 0) && (
                            <div className="import-correlation-list">
                              {trueClientPromotionReview.gates.importWarnings.slice(0, 4).map((warning, index) => (
                                <div key={`trueclient-import-warning-${index}`} className="import-correlation-card">
                                  <strong>Import Warning</strong>
                                  <span>{warning}</span>
                                </div>
                              ))}
                              {trueClientPromotionReview.gates.replayWarnings.slice(0, 4).map((warning, index) => (
                                <div key={`trueclient-replay-warning-${index}`} className="import-correlation-card">
                                  <strong>Replay Warning</strong>
                                  <span>{warning}</span>
                                </div>
                              ))}
                              {trueClientPromotionReview.nextActions.map((action, index) => (
                                <div key={`trueclient-next-action-${index}`} className="import-correlation-card">
                                  <strong>Next Action</strong>
                                  <span>{action}</span>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      )}

                      {importSummary.runtimeSettingReadiness && (
                        <div className="import-correlation-section">
                          <h4>Migration Readiness</h4>
                          <p>{runtimeSettingReadinessSummary}</p>
                          {importSummary.runtimeSettingReadiness.blocking_settings.length > 0 && (
                            <div className="import-correlation-list">
                              {importSummary.runtimeSettingReadiness.blocking_settings.map((entry) => (
                                <div key={`readiness-${entry.id}`} className="import-correlation-card">
                                  <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                                  <span>{entry.concept}</span>
                                  <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                                  {getImportedRuntimeSectionId(entry.area) && (
                                    <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedRuntimeSettings(entry.area)}>
                                        {`Open ${entry.area} Settings`}
                                      </button>
                                    </div>
                                  )}
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      )}

                      {importSummary.runtimeSettingsParity.length > 0 && (
                        <div className="import-correlation-section">
                          <h4>Runtime Settings Parity</h4>
                          <p>These imported source runtime settings were classified against APEX Load&apos;s code-owned support matrix.</p>
                          <div className="import-correlation-list">
                            {importSummary.runtimeSettingsParity.map((entry) => (
                              <div key={entry.id} className="import-correlation-card">
                                <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                                <span>{entry.concept}</span>
                                <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                                {getImportedRuntimeSectionId(entry.area) && (
                                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedRuntimeSettings(entry.area)}>
                                      {`Open ${entry.area} Settings`}
                                    </button>
                                  </div>
                                )}
                              </div>
                            ))}
                          </div>
                        </div>
                      )}

                      {importSummary.correlationSuggestions.length > 0 && (
                        <div className="import-correlation-section">
                          <h4>Correlation Suggestions</h4>
                          <p>Review the imported extractor mappings before you rename variables or rewrite dependent requests.</p>
                          <div className="import-correlation-list">
                            {importedCorrelationFocusTargets.map(({ correlation, foundAtActionIndex, foundAtLabel, usedAtLocations, usedAtTargets }) => (
                              <div key={`${correlation.name}:${correlation.expression}:${correlation.found_at}`} className="import-correlation-card">
                                <strong>{correlation.name}</strong>
                                <span>{correlation.source} via {correlation.expression}</span>
                                <div className="import-correlation-flow">
                                  <div className="import-correlation-flow-row">
                                    <span className="import-correlation-flow-label">Found In</span>
                                    <span className="import-correlation-flow-value">{foundAtLabel}</span>
                                  </div>
                                  {usedAtLocations.length > 0 && (
                                    <div className="import-correlation-flow-row">
                                      <span className="import-correlation-flow-label">Used In</span>
                                      <div className="import-correlation-flow-tags">
                                        {usedAtLocations.map(({ location, label }) => (
                                          <span key={`${correlation.name}:${location}:label`} className="import-correlation-flow-chip">{label}</span>
                                        ))}
                                      </div>
                                    </div>
                                  )}
                                </div>
                                {correlation.sample_value && <code>{correlation.sample_value}</code>}
                                {(foundAtActionIndex !== null || usedAtTargets.length > 0) && (
                                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                    {foundAtActionIndex !== null && (
                                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => selectAuthorAction(foundAtActionIndex)}>
                                        Open {correlation.found_at}
                                      </button>
                                    )}
                                    {usedAtTargets.map(({ location, actionIndex }) => (
                                      <button key={`${correlation.name}:${location}`} className="btn btn-secondary btn-sm" type="button" onClick={() => selectAuthorAction(actionIndex)}>
                                        Open {location}
                                      </button>
                                    ))}
                                  </div>
                                )}
                              </div>
                            ))}
                          </div>
                        </div>
                      )}

                    </div>
                  </section>
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
                    <div className="script-json-sidebar-header">
                      <span className="script-json-sidebar-header-title">{authorRightSidebarContext.title}</span>
                      <div className="script-json-sidebar-tabs" role="tablist" aria-label="Script JSON companion panel">
                        <button
                          type="button"
                          role="tab"
                          aria-selected={activeScriptJsonSidebarTab === 'insights'}
                          className={`script-json-sidebar-tab ${activeScriptJsonSidebarTab === 'insights' ? 'active' : ''}`}
                          onClick={() => setScriptJsonSidebarTab('insights')}
                        >
                          Insights
                        </button>
                        <button
                          type="button"
                          role="tab"
                          aria-selected={activeScriptJsonSidebarTab === 'auto-params'}
                          className={`script-json-sidebar-tab ${activeScriptJsonSidebarTab === 'auto-params' ? 'active' : ''}`}
                          onClick={() => setScriptJsonSidebarTab('auto-params')}
                        >
                          Parameters
                        </button>
                        {hasScriptJsonDetailsTab && (
                          <button
                            type="button"
                            role="tab"
                            aria-selected={activeScriptJsonSidebarTab === 'details'}
                            className={`script-json-sidebar-tab ${activeScriptJsonSidebarTab === 'details' ? 'active' : ''}`}
                            onClick={() => setScriptJsonSidebarTab('details')}
                          >
                            Details
                          </button>
                        )}
                      </div>
                    </div>
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
                <>
                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Problems</strong>
                    <span>{errorCount} error{errorCount === 1 ? '' : 's'} • {warningCount} warning{warningCount === 1 ? '' : 's'}</span>
                  </div>
                  {scriptLevelIssues.length > 0 || actionProblemRows.length > 0 ? (
                    <ul className="utility-list">
                      {scriptLevelIssues.map((issue) => (
                        <li key={issue.id} className={`utility-list-item ${issue.severity}`}>{issue.message}</li>
                      ))}
                      {actionProblemRows.map(({ issue, row }) => (
                        <li key={issue.id} className={`utility-list-item ${issue.severity}`}>
                          <button
                            type="button"
                            className="utility-list-button"
                            onClick={() => selectAuthorPhaseAction(row)}
                          >
                            <span>{row.phaseLabel} {row.actionIndex + 1}</span>
                            <strong>{row.action.name || issue.message}</strong>
                            {row.action.name && <small>{issue.message}</small>}
                          </button>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <div className="utility-empty-state">No script-level issues detected.</div>
                  )}
                </div>

                {renderReadyToRunCard({ showChecklist: true })}

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Repair Actions</strong>
                    <span>{repairSuggestions.length} suggestion{repairSuggestions.length === 1 ? '' : 's'}</span>
                  </div>
                  {repairSuggestions.length > 0 ? (
                    <div className="utility-stack">
                      {repairSuggestions.map((suggestion) => (
                        <div key={suggestion.key} className="utility-suggestion-card">
                          <strong>{suggestion.title}</strong>
                          <p>{suggestion.description}</p>
                          <button type="button" className="btn btn-sm btn-secondary" onClick={() => applyRepairSuggestion(suggestion)}>
                            {suggestion.applyLabel}
                          </button>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="utility-empty-state">No deterministic repair actions are needed for this draft.</div>
                  )}
                </div>

                {importMetadata && (
                  <div className="utility-card">
                    <div className="utility-card-header">
                      <strong>Migration Assistant</strong>
                      <span>{migrationSuggestions.length} suggestion{migrationSuggestions.length === 1 ? '' : 's'}</span>
                    </div>
                    {migrationSuggestions.length > 0 ? (
                      <div className="utility-stack">
                        {migrationSuggestions.map((suggestion) => (
                          <div key={suggestion.key} className="utility-suggestion-card">
                            <strong>{suggestion.title}</strong>
                            <p>{suggestion.description}</p>
                            <button type="button" className="btn btn-sm btn-secondary" onClick={() => applyMigrationSuggestion(suggestion)}>
                              {suggestion.applyLabel}
                            </button>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <div className="utility-empty-state">Imported draft cleanup suggestions are already applied.</div>
                    )}
                    {assistantChangeLog.length > 0 && (
                      <div className="utility-audit-log">
                        <strong>Change Summary</strong>
                        <ul className="utility-list">
                          {assistantChangeLog.map((entry) => (
                            <li key={entry.id} className="utility-list-item">
                              <strong>{entry.title}</strong>
                              <span>{entry.summary}</span>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                  </div>
                )}

                {draftDiffSummary && (importMetadata || assistantChangeLog.length > 0 || editorHistory.past.length > 0) && (
                  <div className="utility-card">
                    <div className="utility-card-header">
                      <strong>Draft Diff Review</strong>
                      <span>{draftDiffSummary.changed ? 'Changed' : 'No changes'}</span>
                    </div>
                    <div className="utility-metric-row">
                      <span className="metric-badge">{draftDiffSummary.actionChanges} action</span>
                      <span className="metric-badge">{draftDiffSummary.parameterChanges} parameter</span>
                      <span className="metric-badge">{draftDiffSummary.dataSourceChanges} data</span>
                    </div>
                    {draftDiffSummary.details.length > 0 ? (
                      <ul className="utility-list">
                        {draftDiffSummary.details.map((detail, index) => (
                          <li key={`imported-diff-${index}`} className="utility-list-item">{detail}</li>
                        ))}
                      </ul>
                    ) : (
                      <div className="utility-empty-state">Current draft still matches the review baseline.</div>
                    )}
                    <details className="utility-json-review">
                      <summary>Review baseline vs current JSON</summary>
                      <div className="utility-json-grid">
                        <div>
                          <strong>Review Baseline</strong>
                          {jsonText.length > JSON_SOURCE_MAP_MAX_DOC_CHARS
                            ? <div className="utility-empty-state">Script too large to preview inline.</div>
                            : <pre>{JSON.stringify(reviewBaselineScript, null, 2)}</pre>}
                        </div>
                        <div>
                          <strong>Current Draft</strong>
                          {jsonText.length > JSON_SOURCE_MAP_MAX_DOC_CHARS
                            ? <div className="utility-empty-state">Script too large to preview inline.</div>
                            : <pre>{JSON.stringify(script, null, 2)}</pre>}
                        </div>
                      </div>
                    </details>
                  </div>
                )}

                {importSummary.correlationSuggestions.length > 0 && (
                  <div className="utility-card">
                    <div className="utility-card-header">
                      <strong>Correlation Inspector</strong>
                      <span>{importSummary.correlationSuggestions.length} mappings</span>
                    </div>
                    <div className="utility-stack">
                      {importedCorrelationFocusTargets.map(({ correlation, foundAtActionIndex, foundAtLabel, usedAtTargets }) => (
                        <div key={`${correlation.name}:${correlation.found_at}`} className="utility-suggestion-card">
                          <strong>{correlation.name}</strong>
                          <p>{foundAtLabel}{usedAtTargets.length > 0 ? ` -> ${usedAtTargets.map((target) => target.label).join(', ')}` : ''}</p>
                          <div className="utility-inline-actions">
                            {foundAtActionIndex !== null && (
                              <button type="button" className="btn btn-sm btn-ghost" onClick={() => selectAuthorAction(foundAtActionIndex)}>
                                Open source
                              </button>
                            )}
                            {usedAtTargets.slice(0, 2).map((target) => (
                              <button key={`${correlation.name}:${target.location}`} type="button" className="btn btn-sm btn-ghost" onClick={() => selectAuthorAction(target.actionIndex)}>
                                Open use
                              </button>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Parameter Source Inspector</strong>
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => openCenterTab('parameter-data')}>
                      Open
                    </button>
                  </div>
                  {parameterInspectorItems.length > 0 ? (
                    <ul className="utility-list">
                      {parameterInspectorItems.map((item, index) => (
                        <li key={`parameter-inspector-${index}`} className="utility-list-item">{item}</li>
                      ))}
                    </ul>
                  ) : (
                    <div className="utility-empty-state">No parameters defined yet.</div>
                  )}
                  {importedParameterFileMappings.length > 0 && (
                    <div className="utility-subsection">
                      <strong>Imported Mappings</strong>
                      <ul className="utility-list">
                        {importedParameterFileMappings.map(({ section, sourceName }) => (
                          <li key={`${section.file}:${section.name}`} className="utility-list-item">
                            {section.file}: {section.name}{sourceName ? ` -> ${sourceName}` : ''}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Parameter Data & Extractors</strong>
                    <span>{dataSourceInspectorItems.length} data • {extractorInspectorItems.length} extractor{extractorInspectorItems.length === 1 ? '' : 's'}</span>
                  </div>
                  {dataSourceInspectorItems.length > 0 ? (
                    <ul className="utility-list">
                      {dataSourceInspectorItems.map((item, index) => (
                        <li key={`source-inspector-${index}`} className="utility-list-item">{item}</li>
                      ))}
                    </ul>
                  ) : (
                    <div className="utility-empty-state">No parameter data defined yet.</div>
                  )}
                  {extractorInspectorItems.length > 0 && (
                    <div className="utility-subsection">
                      <strong>Extractors</strong>
                      <ul className="utility-list">
                        {extractorInspectorItems.map((item) => (
                          <li key={item.id} className="utility-list-item">
                            <button
                              type="button"
                              className="utility-link-button"
                              onClick={() => {
                                if (item.phase === 'action') selectAuthorAction(item.actionIndex)
                                else {
                                  setActiveAction(null)
                                  setAuthorCodeFocus({ phase: item.phase, actionIndex: item.actionIndex })
                                  openCenterTab(SCRIPT_JSON_TAB_ID)
                                }
                              }}
                            >
                              {item.label}
                            </button>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
                </>
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
              onAddDataSource={addDataSource}
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

function formatRuntimeSettingStatusLabel(status: string) {
  switch (status) {
    case 'executed':
      return 'Executed'
    case 'partial':
      return 'Partial'
    case 'compatibility_only':
      return 'Compatibility-only'
    case 'blocked':
      return 'Blocked'
    case 'non_applicable':
      return 'Non-applicable'
    default:
      return status.replace(/_/g, ' ')
  }
}

function getDataSourceFileName(source?: DataSourceConfig | null): string | null {
  if (!source) return null
  const path = (source.path || '').trim()
  if (path) {
    const parts = path.split(/[\\/]+/).filter(Boolean)
    return parts[parts.length - 1] || path
  }
  const sourceName = (source.name || '').trim()
  if (!sourceName) return null
  if (/\.[A-Za-z0-9]+$/.test(sourceName)) return sourceName
  const extension = (source.type || '').trim().toLowerCase()
  return extension === 'csv' || extension === 'dat' ? `${sourceName}.${extension}` : sourceName
}

function getSelectedParameterHeaderDetails(
  script: ScriptData,
  selectedParameter: ParameterConfig | null,
  selectedParameterIdx: number | null,
) {
  if (!selectedParameter || selectedParameterIdx === null) {
    return {
      title: 'Parameter Data',
      token: '',
      details: ['Select a parameter from the Parameters sidebar.'],
    }
  }

  const parameterType = selectedParameter.type || 'list'
  const parameterName = selectedParameter.name || `Parameter ${selectedParameterIdx + 1}`
  const selectedSourceIndex = parameterType === 'file' || parameterType === 'list'
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const fileName = getDataSourceFileName(selectedSource)
  const details = [
    fileName,
    selectedSource?.name && selectedSource.name !== fileName ? `Source ${selectedSource.name}` : '',
    selectedParameter.column ? `Column ${selectedParameter.column}` : '',
  ].filter(Boolean)

  if (details.length === 0) {
    details.push(formatParameterChoiceLabel('type', parameterType))
  }

  return {
    title: `Parameter: ${parameterName}`,
    token: selectedParameter.name ? `{{param.${selectedParameter.name}}}` : '',
    details,
  }
}

function formatImportParameterMapping(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.join(' • ') || 'No preserved mapping details'
}

interface ImportedParameterMappingsDialogProps {
  mappings: ImportedParameterFileMapping[]
  onClose: () => void
  onOpenMappedDataSource: (sourceName?: string | null) => void
}

function ImportedParameterMappingsDialog({
  mappings,
  onClose,
  onOpenMappedDataSource,
}: ImportedParameterMappingsDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog imported-parameter-mappings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameter File Mappings"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter File Mappings</h3>
            <p className="editor-dialog-subtitle">
              Review the preserved source parameter definitions without shrinking the source and DSL mapping workspace.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body imported-parameter-mappings-dialog-body">
          <p>These preserved source parameter definitions show how the importer mapped file-backed data into APEX Load data sources.</p>
          <div className="import-correlation-list">
            {mappings.map(({ section, sourceName }) => (
              <div key={`${section.file}:${section.name}`} className="import-correlation-card">
                <strong>{section.file}: {section.name}</strong>
                <span>{formatImportParameterMapping(section.values)}</span>
                {sourceName && <span>Generated source: {sourceName}</span>}
                {sourceName && (
                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                    <button
                      className="btn btn-secondary btn-sm"
                      type="button"
                      onClick={() => {
                        onOpenMappedDataSource(sourceName)
                        onClose()
                      }}
                    >
                      Open Mapped Data Source
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
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
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog replay-screenshot-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Replay Screenshot"
        onClick={event => event.stopPropagation()}
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

interface ParameterDataDialogProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddDataSource: () => void
  onAddParameter: () => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onApplyPendingParameterDraft: () => void
  onAutoFixDataSource: (idx: number) => void
  onClearPendingParameterDraft: () => void
  onClose?: () => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
  onSelectParameter: (idx: number | null) => void
  openDataEditor: number | null
  pendingParameterDraft: ActionEditorParameterDraft | null
  selectedParameterIdx: number | null
  script: ScriptData
  variant?: 'modal' | 'document'
}

function ParameterDataDialog({
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddParameter,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onApplyPendingParameterDraft,
  onAutoFixDataSource,
  onClearPendingParameterDraft,
  onClose,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onSortDataRows,
  onToggleDataEditorMode,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  onUpdateParameter,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
  onSelectParameter,
  openDataEditor,
  pendingParameterDraft,
  selectedParameterIdx,
  script,
  variant = 'modal',
}: ParameterDataDialogProps) {
  const selectedParameter = selectedParameterIdx !== null ? script.parameters?.[selectedParameterIdx] || null : null
  const headerDetails = useMemo(
    () => getSelectedParameterHeaderDetails(script, selectedParameter, selectedParameterIdx),
    [script, selectedParameter, selectedParameterIdx],
  )
  useEffect(() => {
    if (openDataEditor === null) return
    const sourceName = script.data_sources?.[openDataEditor]?.name
    const parameterIndex = findFirstParameterIndexByDataSource(script.parameters, sourceName)
    if (parameterIndex === null) return
    const currentParameter = selectedParameterIdx !== null ? script.parameters?.[selectedParameterIdx] : null
    if (currentParameter && normalizeParameterDataLookup(currentParameter.data_source) === normalizeParameterDataLookup(sourceName)) {
      return
    }
    onSelectParameter(parameterIndex)
  }, [onSelectParameter, openDataEditor, script.data_sources, script.parameters, selectedParameterIdx])
  useEffect(() => {
    if (selectedParameterIdx === null) return
    if (script.parameters?.[selectedParameterIdx]) return
    onSelectParameter(null)
  }, [onSelectParameter, script.parameters, selectedParameterIdx])
  useEffect(() => {
    if (selectedParameterIdx !== null) return
    if ((script.parameters?.length || 0) === 0) return
    onSelectParameter(0)
  }, [onSelectParameter, script.parameters, selectedParameterIdx])

  const dialog = (
      <div
        className={`editor-dialog parameter-data-dialog ${variant === 'document' ? 'parameter-data-document' : ''}`}
        role={variant === 'document' ? 'region' : 'dialog'}
        aria-modal={variant === 'document' ? undefined : true}
        aria-label="Parameter Data"
      >
        <div className="editor-dialog-header">
          <div className="parameter-data-selected-heading">
            <h3 className="panel-title">
              <span>{headerDetails.title}</span>
              {headerDetails.token && <span className="cell-mono">{headerDetails.token}</span>}
            </h3>
            <p className="editor-dialog-subtitle">{headerDetails.details.join(' · ')}</p>
          </div>
          {onClose && (
            <div className="parameter-dialog-actions">
              <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
            </div>
          )}
        </div>
        <div className="editor-dialog-body">
          {pendingParameterDraft && (
            <ParameterDraftPanel
              draft={pendingParameterDraft}
              onApplyPendingParameterDraft={onApplyPendingParameterDraft}
              onClearPendingParameterDraft={onClearPendingParameterDraft}
              onUpdateParameterDraftName={onUpdateParameterDraftName}
              onUpdateParameterDraftValue={onUpdateParameterDraftValue}
            />
          )}
          <ParameterDataDetail
            dataEditorMode={dataEditorMode}
            dataSourceHistory={dataSourceHistory}
            onAddParameter={onAddParameter}
            onAddDataColumn={onAddDataColumn}
            onAddDataRow={onAddDataRow}
            onApplyDedupeHeaders={onApplyDedupeHeaders}
            onApplyFillHeaders={onApplyFillHeaders}
            onApplyNormalizeRows={onApplyNormalizeRows}
            onAutoFixDataSource={onAutoFixDataSource}
            onExportDataSourceFile={onExportDataSourceFile}
            onImportDataSourceFile={onImportDataSourceFile}
            onRedoDataSource={onRedoDataSource}
            onRemoveDataColumn={onRemoveDataColumn}
            onRemoveDataRow={onRemoveDataRow}
            onSortDataRows={onSortDataRows}
            onToggleDataEditorMode={onToggleDataEditorMode}
            onUndoDataSource={onUndoDataSource}
            onUpdateDataCell={onUpdateDataCell}
            onUpdateDataColumnName={onUpdateDataColumnName}
            onUpdateDataSource={onUpdateDataSource}
            onUpdateParameter={(field, value) => {
              if (selectedParameterIdx !== null) onUpdateParameter(selectedParameterIdx, field, value)
            }}
            selectedParameterIdx={selectedParameterIdx}
            selectedParameter={selectedParameter}
            script={script}
          />
        </div>
      </div>
  )

  if (variant === 'document') return dialog

  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div onClick={event => event.stopPropagation()}>
        {dialog}
      </div>
    </div>
  )
}

interface ParameterDataDetailProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddParameter: () => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (field: keyof ParameterConfig, value: unknown) => void
  script: ScriptData
  selectedParameter: ParameterConfig | null
  selectedParameterIdx: number | null
}

function ParameterDataDetail({
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddParameter,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onSortDataRows,
  onToggleDataEditorMode,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  onUpdateParameter,
  script,
  selectedParameter,
  selectedParameterIdx,
}: ParameterDataDetailProps) {
  const [detailTab, setDetailTab] = useState<'info' | 'data'>('info')
  if (!selectedParameter || selectedParameterIdx === null) {
    return (
      <div className="parameter-detail-panel empty">
        <div className="parameter-empty-state">
          Select a parameter from the Parameters sidebar, or add one to start configuring runtime data.
        </div>
        <button type="button" className="btn btn-sm btn-accent" onClick={onAddParameter}>+ Add Parameter</button>
      </div>
    )
  }

  const parameterType = selectedParameter.type || 'list'
  const parameterName = selectedParameter.name || `Parameter ${selectedParameterIdx + 1}`
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const selectedSourceIndex = isDataBacked
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const selectedParsed = selectedSource ? parseInlineData(selectedSource) : null
  const selectedSourceSummary = selectedSource ? summarizeInlineData(selectedSource) : null
  const selectedSourceWarnings = selectedSource ? validateInlineData(selectedSource) : []
  const selectedColumnIndexes = selectedParsed ? getParameterDataColumnIndexes(selectedParameter, selectedParsed.columns) : []
  const hasInlineListValues = parameterType === 'list' && (selectedParameter.values?.length || 0) > 0 && !selectedParameter.data_source?.trim()

  const renderDataSourceDetail = () => {
    if (!isDataBacked) return null
    if (hasInlineListValues) {
      return (
        <div className="parameter-empty-state">
          {parameterName} uses inline list values. Edit those values in the Data Mapping section above.
        </div>
      )
    }
    if (!selectedParameter.data_source?.trim()) {
      return (
        <div className="parameter-empty-state">
          {parameterName} is not mapped to a data source yet. Set Data Source and Column above to display file data.
        </div>
      )
    }
    if (!selectedSource || selectedSourceIndex === null || !selectedParsed || !selectedSourceSummary) {
      return (
        <div className="parameter-empty-state">
          The mapped data source for {parameterName} could not be found. Update the Data Source field above.
        </div>
      )
    }
    if (selectedColumnIndexes.length === 0) {
      return (
        <div className="parameter-empty-state">
          {parameterName} is mapped to {selectedParameter.data_source}, but its Column does not match this file. Update the Column field above.
        </div>
      )
    }

    return (
      <div className="parameter-data-selected-view">
        <ParameterDataEditor
          dataEditorMode={dataEditorMode[selectedSourceIndex] || 'grid'}
          history={dataSourceHistory[selectedSourceIndex]}
          index={selectedSourceIndex}
          key={`${selectedSource.name || 'source'}-${selectedSourceIndex}-${parameterName}`}
          onAddDataColumn={onAddDataColumn}
          onAddDataRow={onAddDataRow}
          onApplyDedupeHeaders={onApplyDedupeHeaders}
          onApplyFillHeaders={onApplyFillHeaders}
          onApplyNormalizeRows={onApplyNormalizeRows}
          onAutoFixDataSource={onAutoFixDataSource}
          onExportDataSourceFile={onExportDataSourceFile}
          onImportDataSourceFile={onImportDataSourceFile}
          onRedoDataSource={onRedoDataSource}
          onRemoveDataColumn={onRemoveDataColumn}
          onRemoveDataRow={onRemoveDataRow}
          onSortDataRows={onSortDataRows}
          onToggleDataEditorMode={onToggleDataEditorMode}
          onUndoDataSource={onUndoDataSource}
          onUpdateDataCell={onUpdateDataCell}
          onUpdateDataColumnName={onUpdateDataColumnName}
          onUpdateDataSource={onUpdateDataSource}
          parsed={selectedParsed}
          selectedParameterName={parameterName}
          showHeaderTitle={false}
          source={selectedSource}
          summary={selectedSourceSummary}
          warnings={selectedSourceWarnings}
        />
      </div>
    )
  }

  const activeTab = detailTab === 'data' && isDataBacked ? 'data' : 'info'

  return (
    <div className="parameter-detail-panel">
      {isDataBacked && (
        <div className="parameter-detail-tabs" role="tablist" aria-label="Parameter detail tabs">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'info'}
            className={activeTab === 'info' ? 'active' : ''}
            onClick={() => setDetailTab('info')}
          >
            Settings
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'data'}
            className={activeTab === 'data' ? 'active' : ''}
            onClick={() => setDetailTab('data')}
          >
            Data
          </button>
        </div>
      )}
      <section className="parameter-detail-info" role="tabpanel" hidden={activeTab !== 'info'}>
        <ParameterInfoPanel
          columnIndexes={selectedColumnIndexes}
          dataSources={script.data_sources || []}
          onUpdateDataSource={onUpdateDataSource}
          onUpdateParameter={onUpdateParameter}
          parameter={selectedParameter}
          parameters={script.parameters || []}
          parsed={selectedParsed}
          selectedParameterIdx={selectedParameterIdx}
          selectedSource={selectedSource}
          selectedSourceIndex={selectedSourceIndex}
          summary={selectedSourceSummary}
        />
      </section>
      {isDataBacked && (
        <section className="parameter-detail-data" role="tabpanel" hidden={activeTab !== 'data'}>
          {renderDataSourceDetail()}
        </section>
      )}
    </div>
  )
}

interface ParameterInfoPanelProps {
  columnIndexes: number[]
  dataSources: DataSourceConfig[]
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
  parameters: ParameterConfig[]
  parsed: { columns: string[]; rows: string[][] } | null
  selectedParameterIdx: number | null
  selectedSource: DataSourceConfig | null
  selectedSourceIndex: number | null
  summary: { rows: number; columns: number } | null
}

function ParameterInfoPanel({
  columnIndexes,
  dataSources,
  onUpdateDataSource,
  onUpdateParameter,
  parameter,
  parameters,
  parsed,
  selectedParameterIdx,
  selectedSource,
  selectedSourceIndex,
  summary,
}: ParameterInfoPanelProps) {
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const isGenerated = ['random_number', 'counter', 'current_iteration', 'random_string', 'uuid'].includes(parameterType)
  const isTime = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const showsDataMapping = isDataBacked || parameterType === 'host'
  const showsTypeOptions = isGenerated || isTime
  const showsOutput = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined
  const hasSource = Boolean(selectedSource && selectedSourceIndex !== null)
  const showsStats = isDataBacked && hasSource && Boolean(parsed && summary)
  const uniqueCount = parsed ? countUniqueColumnValues(parsed, columnIndexes) : 0
  const mappedColumnLabel = parsed && columnIndexes.length
    ? columnIndexes.map(idx => formatDataSourceColumnLabel(parsed.columns[idx], idx)).join(', ')
    : '—'

  const dataSourceNames = (dataSources || []).map(ds => ds.name).filter(Boolean)
  const currentDataSource = parameter.data_source || ''
  const dataSourceOptions = currentDataSource && !dataSourceNames.includes(currentDataSource)
    ? [currentDataSource, ...dataSourceNames]
    : dataSourceNames
  const columnNames = parsed?.columns || []
  const currentColumn = parameter.column || ''
  const columnOptions = currentColumn && !columnNames.includes(currentColumn)
    ? [currentColumn, ...columnNames]
    : columnNames
  const sourceFileLabel = selectedSource?.path || selectedSource?.name || currentDataSource || '—'
  const sameLineOptions = getSameLineParameterOptions(parameters, selectedParameterIdx)
  const sameLineSelectOptions = getSameLineParameterSelectOptions(parameter, sameLineOptions)
  const selectionControlValue = getParameterSelectionControlValue(parameter)

  const handleStrategyChange = (value: string) => {
    if (value === SAME_ROW_SELECTION_VALUE) {
      const nextSameLine = sameLineSelectOptions[0]?.value || ''
      onUpdateParameter('same_line_as', nextSameLine || undefined)
      return
    }
    if (parameter.same_line_as) onUpdateParameter('same_line_as', undefined)
    onUpdateParameter('selection', value)
    if (selectedSourceIndex !== null) onUpdateDataSource(selectedSourceIndex, 'strategy', value, true)
  }

  return (
    <div className="parameter-info-panel">
      <section className="parameter-info-group">
        <h5 className="parameter-info-group-title">Identity</h5>
        <table className="parameter-info-table">
          <tbody>
            <tr>
              <th scope="row">Parameter Name</th>
              <td><input value={parameter.name || ''} onChange={event => onUpdateParameter('name', event.target.value)} placeholder="session_id" /></td>
            </tr>
            <tr>
              <th scope="row">Parameter Type</th>
              <td>
                <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
              </td>
            </tr>
            <tr>
              <th scope="row">Template Token</th>
              <td><span className="parameter-info-readonly cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : 'Name this parameter to generate a template token'}</span></td>
            </tr>
          </tbody>
        </table>
      </section>

      <section className="parameter-info-group">
        <h5 className="parameter-info-group-title">Behavior</h5>
        <table className="parameter-info-table">
          <tbody>
            <tr>
              <th scope="row">Update Cadence</th>
              <td>
                <select aria-label={`Update Cadence for ${parameterName}`} value={getParameterChoiceValue(parameter, 'update')} onChange={event => onUpdateParameter('update', event.target.value)}>
                  {PARAMETER_CHOICE_DEFINITIONS.update.options.map(option => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </td>
            </tr>
            {isDataBacked && (
              <>
                <tr>
                  <th scope="row">Iteration Strategy</th>
                  <td>
                    <select aria-label={`Iteration Strategy for ${parameterName}`} value={selectionControlValue} onChange={event => handleStrategyChange(event.target.value)}>
                      {getParameterSelectionOptions(true).map(option => (
                        <option
                          disabled={option.value === SAME_ROW_SELECTION_VALUE && sameLineSelectOptions.length === 0}
                          key={option.value}
                          value={option.value}
                        >
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </td>
                </tr>
                {selectionControlValue === SAME_ROW_SELECTION_VALUE && (
                  <tr>
                    <th scope="row">Same Row As</th>
                    <td>
                      <select
                        aria-label={`Same Row As for ${parameterName}`}
                        disabled={sameLineSelectOptions.length === 0}
                        value={parameter.same_line_as || ''}
                        onChange={event => onUpdateParameter('same_line_as', event.target.value || undefined)}
                      >
                        {sameLineSelectOptions.length === 0 ? (
                          <option value="">No compatible parameters</option>
                        ) : (
                          sameLineSelectOptions.map(option => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                          ))
                        )}
                      </select>
                      {sameLineSelectOptions.length === 0 && (
                        <p className="parameter-info-help">Add another file or list parameter mapped to this data source to link row selection.</p>
                      )}
                    </td>
                  </tr>
                )}
                <tr>
                  <th scope="row">On Exhaustion</th>
                  <td>
                    <select aria-label={`On Exhaustion for ${parameterName}`} value={getParameterChoiceValue(parameter, 'on_exhaustion')} onChange={event => onUpdateParameter('on_exhaustion', event.target.value)}>
                      {PARAMETER_CHOICE_DEFINITIONS.on_exhaustion.options.map(option => (
                        <option key={option.value} value={option.value}>{option.label}</option>
                      ))}
                    </select>
                  </td>
                </tr>
              </>
            )}
          </tbody>
        </table>
      </section>

      {showsDataMapping && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Data Mapping</h5>
          <table className="parameter-info-table">
            <tbody>
              {isDataBacked && (
                <>
                  <tr>
                    <th scope="row">Data Source</th>
                    <td>
                      {dataSourceOptions.length > 0 ? (
                        <select aria-label={`Data Source for ${parameterName}`} value={currentDataSource} onChange={event => onUpdateParameter('data_source', event.target.value)}>
                          <option value="">— Select source —</option>
                          {dataSourceOptions.map(name => (
                            <option key={name} value={name}>{name}</option>
                          ))}
                        </select>
                      ) : (
                        <input value={currentDataSource} onChange={event => onUpdateParameter('data_source', event.target.value)} placeholder="users" />
                      )}
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">Column</th>
                    <td>
                      {columnOptions.length > 0 ? (
                        <select aria-label={`Column for ${parameterName}`} value={currentColumn} onChange={event => onUpdateParameter('column', event.target.value)}>
                          <option value="">— Select column —</option>
                          {columnOptions.map(name => (
                            <option key={name} value={name}>{name}</option>
                          ))}
                        </select>
                      ) : (
                        <input value={currentColumn} onChange={event => onUpdateParameter('column', event.target.value)} placeholder="column name" />
                      )}
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">Source File</th>
                    <td><span className="parameter-info-readonly cell-mono">{sourceFileLabel}</span></td>
                  </tr>
                </>
              )}
              {(parameterType === 'list' || parameterType === 'host') && (
                <tr>
                  <th scope="row">{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</th>
                  <td>
                    <input
                      value={(parameter.values || []).join(', ')}
                      onChange={event => onUpdateParameter('values', parseParameterListInput(event.target.value))}
                      placeholder={parameterType === 'host' ? 'host-a, host-b' : 'value1, value2'}
                    />
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          {isDataBacked && selectedSource && selectedSourceIndex !== null && (
            <div className="parameter-data-file-controls parameter-info-file-controls">
              <label>
                <span>File Type</span>
                <select aria-label={`File Type for ${parameterName}`} value={selectedSource.type || 'csv'} onChange={event => onUpdateDataSource(selectedSourceIndex, 'type', event.target.value)}>
                  <option value="csv">csv</option>
                  <option value="dat">dat</option>
                </select>
              </label>
              <label>
                <span>Delimiter</span>
                <select aria-label={`Delimiter for ${parameterName}`} value={getDataSourceDelimiterChoice(selectedSource)} onChange={event => onUpdateDataSource(selectedSourceIndex, 'delimiter', getDataSourceDelimiterValue(event.target.value as DataSourceDelimiterChoice), true)}>
                  <option value="auto">Auto ({inferDelimiter(selectedSource) === '\t' ? 'Tab' : 'Comma'})</option>
                  <option value="comma">Comma</option>
                  <option value="tab">Tab</option>
                </select>
              </label>
              <label className="checkbox-label compact parameter-data-header-toggle">
                <input
                  type="checkbox"
                  aria-label={`First row is header for ${parameterName}`}
                  checked={selectedSource.first_row_header !== false}
                  onChange={event => onUpdateDataSource(selectedSourceIndex, 'first_row_header', event.target.checked)}
                />
                <span>{selectedSource.first_row_header !== false ? 'First row is header' : 'No header row'}</span>
              </label>
            </div>
          )}
        </section>
      )}

      {showsTypeOptions && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Type Options</h5>
          <table className="parameter-info-table">
            <tbody>
              {parameterType === 'random_number' && (
                <>
                  <tr><th scope="row">Minimum</th><td><input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdateParameter('min', Number(event.target.value))} placeholder="min" /></td></tr>
                  <tr><th scope="row">Maximum</th><td><input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdateParameter('max', Number(event.target.value))} placeholder="max" /></td></tr>
                </>
              )}
              {(parameterType === 'counter' || parameterType === 'current_iteration') && (
                <>
                  <tr><th scope="row">Start</th><td><input type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdateParameter('start', Number(event.target.value))} placeholder="start" /></td></tr>
                  <tr><th scope="row">End</th><td><input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdateParameter('end', Number(event.target.value))} placeholder="end (optional)" /></td></tr>
                  <tr><th scope="row">Step</th><td><input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdateParameter('step', Number(event.target.value))} placeholder="step" /></td></tr>
                </>
              )}
              {parameterType === 'random_string' && (
                <>
                  <tr><th scope="row">Length</th><td><input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdateParameter('length', Number(event.target.value))} placeholder="length" /></td></tr>
                  <tr><th scope="row">Charset</th><td><input value={parameter.charset || ''} onChange={event => onUpdateParameter('charset', event.target.value)} placeholder="charset override" /></td></tr>
                </>
              )}
              {parameterType === 'uuid' && (
                <tr><td colSpan={2}><p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p></td></tr>
              )}
              {isTime && (
                <>
                  <tr>
                    <th scope="row">Format</th>
                    <td>
                      <ParameterFormatControl parameter={parameter} onUpdate={onUpdateParameter} />
                    </td>
                  </tr>
                  <tr><th scope="row">Timezone</th><td><input value={parameter.timezone || ''} onChange={event => onUpdateParameter('timezone', event.target.value)} placeholder="UTC, America/New_York" /></td></tr>
                  <tr><th scope="row">Offset Seconds</th><td><input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdateParameter('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></td></tr>
                </>
              )}
            </tbody>
          </table>
        </section>
      )}

      {showsOutput && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Output &amp; Reproducibility</h5>
          <table className="parameter-info-table">
            <tbody>
              {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
                <>
                  <tr><th scope="row">Pad Width</th><td><input type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdateParameter('pad_width', Number(event.target.value))} placeholder="pad width" /></td></tr>
                  <tr><th scope="row">Pad Character</th><td><input value={parameter.pad_char || ''} onChange={event => onUpdateParameter('pad_char', event.target.value)} placeholder="pad char" /></td></tr>
                </>
              )}
              <tr>
                <th scope="row">Seed</th>
                <td>
                  <input
                    type="number"
                    value={numberInputValue(parameter.seed, '')}
                    onChange={event => updateOptionalNumber(onUpdateParameter, 'seed', event.target.value)}
                    placeholder="seed (optional)"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      )}

      {showsStats && (
        <section className="parameter-info-group">
          <h5 className="parameter-info-group-title">Data Statistics</h5>
          <table className="parameter-info-table parameter-info-stats-table">
            <tbody>
              <tr><th scope="row">Record Count</th><td className="cell-mono parameter-info-stat-value">{summary?.rows ?? 0}</td></tr>
              <tr><th scope="row">Unique Records</th><td className="cell-mono parameter-info-stat-value">{uniqueCount}</td></tr>
              <tr><th scope="row">Columns</th><td className="cell-mono parameter-info-stat-value">{summary?.columns ?? 0}</td></tr>
              <tr><th scope="row">Mapped Column</th><td className="cell-mono">{mappedColumnLabel}</td></tr>
            </tbody>
          </table>
        </section>
      )}
    </div>
  )
}

interface ParameterSidebarProps {
  mergeSummary: ParameterSourceMergeSummary
  onAddParameter: () => void
  onMergeParameterSources: () => void
  onSelectParameter: (idx: number) => void
  parameters: ParameterConfig[]
  selectedParameterIdx: number | null
}

function ParameterSidebar({
  mergeSummary,
  onAddParameter,
  onMergeParameterSources,
  onSelectParameter,
  parameters,
  selectedParameterIdx,
}: ParameterSidebarProps) {
  const groups = useMemo(() => {
    const orderedTypes = PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => option.value)
    const typeLabels = new Map(PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => [option.value, option.label]))
    const grouped = new Map<string, Array<{ index: number; parameter: ParameterConfig }>>()
    parameters.forEach((parameter, index) => {
      const type = parameter.type || 'list'
      const bucket = grouped.get(type) || []
      bucket.push({ index, parameter })
      grouped.set(type, bucket)
    })
    const knownGroups = orderedTypes
      .map(type => ({ type, label: typeLabels.get(type) || type, parameters: grouped.get(type) || [] }))
      .filter(group => group.parameters.length > 0)
    const unknownGroups = Array.from(grouped.entries())
      .filter(([type]) => !orderedTypes.includes(type))
      .map(([type, items]) => ({ type, label: type, parameters: items }))
    return [...knownGroups, ...unknownGroups]
  }, [parameters])

  return (
    <div className="utility-card parameter-sidebar-card">
      <div className="utility-card-header">
        <strong>Parameters</strong>
      </div>
      <div className="parameter-sidebar-actions">
        <button type="button" className="btn btn-sm btn-accent" onClick={onAddParameter}>+ Add Parameter</button>
        <button
          className="btn btn-sm btn-ghost"
          disabled={!mergeSummary.canMerge}
          onClick={onMergeParameterSources}
          title={mergeSummary.canMerge
            ? `Merge ${mergeSummary.sourceCount} source files for ${mergeSummary.parameterCount} parameters`
            : 'Requires at least two .dat or .csv parameter source files'}
          type="button"
        >
          Merge Source Files
        </button>
      </div>
      {parameters.length === 0 ? (
        <div className="utility-empty-state">No parameters defined yet.</div>
      ) : (
        <div className="parameter-sidebar-list" role="listbox" aria-label="Parameters">
          {groups.map(group => (
            <section className="parameter-sidebar-group" key={group.type}>
              <div className="parameter-sidebar-group-title">
                <strong>{group.label}</strong>
                <span>{group.parameters.length}</span>
              </div>
              {group.parameters.map(({ index, parameter }) => {
                const selected = selectedParameterIdx === index
                const parameterName = parameter.name || `Parameter ${index + 1}`
                const sourceSummary = parameter.type === 'file' || parameter.type === 'list'
                  ? [parameter.data_source, parameter.column].filter(Boolean).join(' / ') || 'No source mapped'
                  : summarizeParameterSettings(parameter)
                return (
                  <button
                    type="button"
                    className={`parameter-sidebar-item ${selected ? 'selected' : ''}`}
                    aria-selected={selected}
                    key={`${parameterName}-${index}`}
                    onClick={() => onSelectParameter(index)}
                    role="option"
                  >
                    <span>
                      <strong>{parameterName}</strong>
                      <small>{sourceSummary}</small>
                    </span>
                    <span className="cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : 'unnamed'}</span>
                  </button>
                )
              })}
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

interface ParameterDraftPanelProps {
  draft: ActionEditorParameterDraft
  onApplyPendingParameterDraft: () => void
  onClearPendingParameterDraft: () => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
}

function ParameterDraftPanel({
  draft,
  onApplyPendingParameterDraft,
  onClearPendingParameterDraft,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
}: ParameterDraftPanelProps) {
  return (
    <div className="parameter-draft-panel">
      <div className="panel-title-row" style={{ marginBottom: 8 }}>
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Parameterize Selection</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
            {draft.source === 'json'
              ? 'This selection came from the Script JSON. Save it, then pick the parameter type (data file, time, random, uuid, …) below.'
              : 'This selection came from the Action List editor. Review it here with the rest of the parameter configuration.'}
          </span>
        </div>
      </div>
      <div className="parameter-draft-grid">
        <label>
          <span>Name</span>
          <input
            value={draft.requestedName}
            onChange={event => onUpdateParameterDraftName(event.target.value)}
            placeholder="param_name"
          />
        </label>
        <label>
          <span>Value</span>
          <input
            value={draft.seedValue}
            onChange={event => onUpdateParameterDraftValue(event.target.value)}
            placeholder="Selected value"
          />
        </label>
      </div>
      <div className="parameter-draft-actions">
        <button type="button" className="btn btn-sm btn-primary" onClick={onApplyPendingParameterDraft} disabled={!draft.requestedName.trim()}>
          Save To Parameter Data
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onClearPendingParameterDraft}>
          Cancel
        </button>
      </div>
    </div>
  )
}

interface AutoParameterizationSidebarProps {
  activeCandidateId: string | null
  activeOccurrenceIndex: number
  activeOccurrenceLabel: string
  candidateMatchCount: number
  onApplyAutoParameterization: () => void
  onNextOccurrence: () => void
  onPreviousOccurrence: () => void
  onSelectCandidate: (id: string) => void
  onUpdateParameterCandidate: (id: string, patch: Partial<ParameterCandidate>) => void
  parameterCandidates: ParameterCandidate[]
  seedRows: number
  onSeedRowsChange: (rows: number) => void
}

// Parameter settings the sidebar edits inline. Choice fields use the shared
// PARAMETER_CHOICE_DEFINITIONS selects; the rest get a type-appropriate input.
// The actual data values (CSV rows for file params) are not edited here.
const PARAM_CHOICE_KEYS = new Set<string>(['update', 'selection', 'on_exhaustion'])
const PARAM_BOOL_KEYS = new Set<string>(['value_for_each_vuser'])
const PARAM_NUMBER_KEYS = new Set<string>(['start_row', 'block_size', 'offset_seconds', 'step', 'start', 'min', 'max', 'length', 'padding'])
// Handled outside the generic Settings loop: data_source/column live under the
// Data file header; same_line_as is a dropdown; name/type render in the header.
const PARAM_SETTINGS_EXCLUDED = new Set<string>(['name', 'type', 'data_source', 'column', 'same_line_as'])

interface SelectedParameterDetailsProps {
  parameter: ParameterConfig
  parameterIndex: number
  referenceLines: number[]
  dataInfo: { fileName: string; rows: number | null; unique: number | null } | null
  sameLineOptions: { label: string; value: string }[]
  saving: boolean
  onRename: (name: string) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onSave: () => void
  onJumpToLine: (line: number) => void
  onEditInDataTab: () => void
}

// The Parameter companion page when a parameter is selected in the left list:
// rename it, see its data file (name/source/column + row counts), edit its
// runtime settings inline, jump to references, and Save.
function SelectedParameterDetails({ parameter, parameterIndex, referenceLines, dataInfo, sameLineOptions, saving, onRename, onUpdateParameter, onSave, onJumpToLine, onEditInDataTab }: SelectedParameterDetailsProps) {
  const settingKeys = Object.keys(parameter).filter(
    key => !PARAM_SETTINGS_EXCLUDED.has(key)
      && (parameter as unknown as Record<string, unknown>)[key] !== undefined
      && (parameter as unknown as Record<string, unknown>)[key] !== null,
  )
  const showSameLine = Boolean(parameter.same_line_as) || sameLineOptions.length > 0
  const renderControl = (key: string) => {
    const value = (parameter as unknown as Record<string, unknown>)[key]
    const field = key as keyof ParameterConfig
    if (PARAM_CHOICE_KEYS.has(key)) {
      const definition = PARAMETER_CHOICE_DEFINITIONS[key as MutableParameterChoiceField]
      return (
        <select value={String(value ?? '')} onChange={event => onUpdateParameter(parameterIndex, field, event.target.value)}>
          {definition.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
      )
    }
    if (PARAM_BOOL_KEYS.has(key)) {
      return <input type="checkbox" checked={Boolean(value)} onChange={event => onUpdateParameter(parameterIndex, field, event.target.checked)} />
    }
    if (PARAM_NUMBER_KEYS.has(key)) {
      return <input type="number" value={value === '' || value === undefined ? '' : Number(value)} onChange={event => onUpdateParameter(parameterIndex, field, event.target.value === '' ? '' : Number(event.target.value))} />
    }
    return <input value={String(value ?? '')} onChange={event => onUpdateParameter(parameterIndex, field, event.target.value)} />
  }
  return (
    <div className="selected-parameter-details">
      <div className="selected-parameter-header">
        <div className="selected-parameter-title">
          <strong title={parameter.name || 'Unnamed parameter'}>{parameter.name || 'Unnamed parameter'}</strong>
          <span className="selected-parameter-type" title="Parameter type (set in Parameter Data)">{parameter.type || 'list'}</span>
        </div>
      </div>

      {dataInfo && (
        <div className="selected-parameter-section">
          <span className="selected-parameter-section-label">Data file</span>
          {dataInfo.rows !== null && (
            <p className="selected-parameter-stats">{dataInfo.rows} row{dataInfo.rows === 1 ? '' : 's'} · {dataInfo.unique} unique</p>
          )}
          <div className="selected-parameter-form">
            <label className="selected-parameter-field"><span>File name</span><span className="selected-parameter-static">{dataInfo.fileName || '—'}</span></label>
            <label className="selected-parameter-field"><span>Data Source</span><span className="selected-parameter-static">{parameter.data_source || '—'}</span></label>
            <label className="selected-parameter-field"><span>Column Name</span><span className="selected-parameter-static">{parameter.column || '—'}</span></label>
          </div>
        </div>
      )}

      <div className="selected-parameter-section">
        <span className="selected-parameter-section-label">Settings</span>
        <div className="selected-parameter-form">
          <label className="selected-parameter-field">
            <span>Parameter Name</span>
            <input className="selected-parameter-name" value={parameter.name || ''} aria-label="Parameter name" placeholder="parameter_name" onChange={event => onRename(event.target.value)} />
          </label>
          {settingKeys.map(key => (
            <label key={key} className={`selected-parameter-field ${PARAM_BOOL_KEYS.has(key) ? 'is-bool' : ''}`}>
              <span>{key.replace(/_/g, ' ')}</span>
              {renderControl(key)}
            </label>
          ))}
          {showSameLine && (
            <label className="selected-parameter-field">
              <span>Same line as</span>
              <select value={parameter.same_line_as || ''} onChange={event => onUpdateParameter(parameterIndex, 'same_line_as', event.target.value || undefined)}>
                <option value="">(none)</option>
                {sameLineOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
              </select>
            </label>
          )}
        </div>
        <p className="selected-parameter-empty">Editing settings only — change the data values in Parameter Data.</p>
      </div>

      <div className="selected-parameter-section">
        <span className="selected-parameter-section-label">References ({referenceLines.length})</span>
        {referenceLines.length === 0 ? (
          <p className="selected-parameter-empty">{`Not referenced yet — use {{param.${parameter.name}}} in an action.`}</p>
        ) : (
          <div className="selected-parameter-refs">
            {referenceLines.map(line => (
              <button key={line} type="button" className="selected-parameter-ref" onClick={() => onJumpToLine(line)} title={`Jump to line ${line}`}>
                Line {line}
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="selected-parameter-actions">
        <button type="button" className="btn btn-sm btn-accent" onClick={onSave} disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onEditInDataTab}>Edit in Parameter Data →</button>
      </div>
    </div>
  )
}

function AutoParameterizationSidebar({
  activeCandidateId,
  activeOccurrenceIndex,
  activeOccurrenceLabel,
  candidateMatchCount,
  onApplyAutoParameterization,
  onNextOccurrence,
  onPreviousOccurrence,
  onSelectCandidate,
  onUpdateParameterCandidate,
  parameterCandidates,
  seedRows,
  onSeedRowsChange,
}: AutoParameterizationSidebarProps) {
  const selectedCount = parameterCandidates.filter(candidate => candidate.selected && sanitizeVarName(candidate.varName).length > 0).length
  const activeCandidate = parameterCandidates.find(candidate => candidate.id === activeCandidateId) || parameterCandidates[0] || null
  const occurrenceCount = activeCandidate?.occurrences.length || 0

  if (parameterCandidates.length === 0) {
    return (
      <div className="utility-card auto-parameter-sidebar-card">
        <div className="utility-card-header">
          <strong>Suggested Fields</strong>
          <span>0</span>
        </div>
        <div className="utility-empty-state">
          No strong parameterization candidates were detected.
        </div>
      </div>
    )
  }

  return (
    <div className="auto-parameter-sidebar">
      <div className="utility-card auto-parameter-sidebar-card">
        <div className="utility-card-header">
          <strong>Suggested Fields</strong>
          <span>{parameterCandidates.length}</span>
        </div>
        <div className="auto-parameter-sidebar-summary">
          <span>{selectedCount} selected</span>
          <span>{parameterCandidates.reduce((total, candidate) => total + candidate.occurrences.length, 0)} occurrence(s)</span>
        </div>
        <div className="auto-parameter-navigation" aria-label="Auto parameterization occurrence navigation">
          <button type="button" className="btn btn-sm btn-ghost" onClick={onPreviousOccurrence} disabled={!activeCandidate || occurrenceCount === 0}>
            Back
          </button>
          <span className="auto-parameter-location">
            {occurrenceCount > 0 ? `${activeOccurrenceIndex + 1} / ${occurrenceCount}` : '0 / 0'}
            <small>{activeOccurrenceLabel}{candidateMatchCount === 0 ? ' · no literal match' : ''}</small>
          </span>
          <button type="button" className="btn btn-sm btn-ghost" onClick={onNextOccurrence} disabled={!activeCandidate || occurrenceCount === 0}>
            Next
          </button>
        </div>
        <label className="auto-parameter-seed-rows">
          <span>Seed rows</span>
          <input
            type="number"
            min={1}
            max={1000}
            value={seedRows}
            onChange={event => {
              const parsed = Number.parseInt(event.target.value, 10)
              onSeedRowsChange(Number.isFinite(parsed) ? Math.min(1000, Math.max(1, parsed)) : 1)
            }}
          />
          <small>First row holds the detected value; fill the rest to vary data per VUser.</small>
        </label>
        <button
          type="button"
          className="btn btn-sm btn-accent auto-parameter-apply"
          onClick={onApplyAutoParameterization}
          disabled={selectedCount === 0}
        >
          Apply Selected
        </button>
      </div>

      <div className="auto-parameter-candidate-list" role="list" aria-label="Suggested parameterization fields">
        {parameterCandidates.map(candidate => {
          const active = candidate.id === activeCandidate?.id
          return (
            <section
              key={candidate.id}
              className={`auto-parameter-candidate ${active ? 'active' : ''}`}
              role="listitem"
            >
              <div className="auto-parameter-candidate-main">
                <label className="auto-parameter-check">
                  <input
                    type="checkbox"
                    checked={candidate.selected}
                    onChange={event => onUpdateParameterCandidate(candidate.id, { selected: event.target.checked })}
                  />
                  <span>Use</span>
                </label>
                <button
                  type="button"
                  className="auto-parameter-candidate-select"
                  onClick={() => onSelectCandidate(candidate.id)}
                >
                  <strong>{candidate.keyHint}</strong>
                  <small>{candidate.occurrences.length} hit{candidate.occurrences.length === 1 ? '' : 's'}</small>
                </button>
              </div>
              <code className="auto-parameter-value">{candidate.value}</code>
              <label className="auto-parameter-name-field">
                <span>Suggested name</span>
                <input
                  value={candidate.varName}
                  onChange={event => onUpdateParameterCandidate(candidate.id, { varName: sanitizeVarName(event.target.value) })}
                  onFocus={() => onSelectCandidate(candidate.id)}
                  placeholder="parameter_name"
                />
              </label>
            </section>
          )
        })}
      </div>
    </div>
  )
}

interface ParameterDefinitionsSectionProps {
  focusedSourceName?: string | null
  isOpen: boolean
  onAddParameter: () => void
  onRemoveParameter: (idx: number) => void
  onSelectParameter: (idx: number, parameter: ParameterConfig) => void
  onToggleOpen: () => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  selectedParameterIdx: number | null
  script: ScriptData
}

interface ParameterDefinitionGroup {
  key: string
  sourceFile: string
  sourceName: string
  sourceType: string
  rowCount: number | null
  parameters: Array<{ index: number; parameter: ParameterConfig }>
}

const PARAMETER_DEFINITION_COLUMN_COUNT = 8

function buildParameterDefinitionGroups(parameters: ParameterConfig[], dataSources: ScriptDataSource[] = []): ParameterDefinitionGroup[] {
  const dataSourceByName = new Map(dataSources.map(source => [source.name, source]))
  const groups: ParameterDefinitionGroup[] = []
  const groupByKey = new Map<string, ParameterDefinitionGroup>()

  parameters.forEach((parameter, index) => {
    const sourceName = (parameter.data_source || '').trim()
    const matchedSource = sourceName ? dataSourceByName.get(sourceName) : undefined
    const sourceFile = (matchedSource?.path || matchedSource?.name || sourceName || formatGeneratedParameterSource(parameter)).trim()
    const sourceType = (matchedSource?.type || parameter.type || 'list').trim()
    const rowCount = matchedSource ? summarizeInlineData(matchedSource).rows : null
    const key = matchedSource?.name || sourceName || `type:${sourceType || sourceFile}`
    let group = groupByKey.get(key)

    if (!group) {
      group = {
        key,
        sourceFile,
        sourceName: matchedSource?.name || sourceName,
        sourceType,
        rowCount,
        parameters: [],
      }
      groupByKey.set(key, group)
      groups.push(group)
    }

    group.parameters.push({ index, parameter })
  })

  return groups
}

function isUserDefinedParameterFileGroup(group: ParameterDefinitionGroup): boolean {
  if (group.rowCount === null) return false
  const sourceType = (group.sourceType || '').trim().toLowerCase()
  const sourceFile = (group.sourceFile || '').trim().toLowerCase()
  return sourceType === 'csv' || sourceType === 'dat' || sourceFile.endsWith('.csv') || sourceFile.endsWith('.dat')
}

interface ParameterDefinitionSectionGroup {
  key: string
  groups: ParameterDefinitionGroup[]
  summary: string
  title: string
}

function buildParameterDefinitionSections(groups: ParameterDefinitionGroup[]): ParameterDefinitionSectionGroup[] {
  const userDefinedFiles = groups.filter(isUserDefinedParameterFileGroup)
  const builtInTypes = groups.filter(group => !isUserDefinedParameterFileGroup(group))
  return [
    {
      key: 'user-defined-files',
      title: 'User-Defined Files (.dat / .csv)',
      summary: `${userDefinedFiles.length} source${userDefinedFiles.length === 1 ? '' : 's'}`,
      groups: userDefinedFiles,
    },
    {
      key: 'built-in-parameters',
      title: 'Built-In / Generated Parameters',
      summary: `${builtInTypes.length} type${builtInTypes.length === 1 ? '' : 's'}`,
      groups: builtInTypes,
    },
  ].filter(section => section.groups.length > 0)
}

function formatParameterRowCount(rowCount?: number | null): string {
  return rowCount === null || rowCount === undefined ? '—' : String(rowCount)
}

function formatParameterTypeLabel(value?: string | null): string {
  const normalized = (value || 'list').trim()
  switch (normalized) {
    case 'date_time':
      return 'Date/Time'
    case 'current_iteration':
      return 'Current Iteration'
    case 'random_number':
      return 'Random Number'
    case 'random_string':
      return 'Random String'
    default:
      return normalized.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
  }
}

function ParameterTypeReadonlyValue({
  parameterName,
  parameterType,
}: {
  parameterName: string
  parameterType: string
}) {
  return (
    <span
      className="parameter-info-readonly parameter-type-readonly"
      aria-label={`Parameter Type for ${parameterName}: ${formatParameterTypeLabel(parameterType)}`}
      title="Parameter type is set when the parameter is created"
    >
      <strong>{formatParameterTypeLabel(parameterType)}</strong>
      <span className="cell-mono">{parameterType}</span>
    </span>
  )
}

function formatGeneratedParameterSource(parameter: ParameterConfig): string {
  return formatParameterTypeLabel(parameter.type)
}

function normalizeParameterSourceIdentity(value?: string | null): string {
  return (value || '').trim().toLowerCase()
}

function findParameterDefinitionGroupBySource(groups: ParameterDefinitionGroup[], sourceName?: string | null): ParameterDefinitionGroup | null {
  const normalizedSource = normalizeParameterSourceIdentity(sourceName)
  if (!normalizedSource) return null
  return groups.find(group => [
    group.key,
    group.sourceName,
    group.sourceFile,
  ].some(candidate => normalizeParameterSourceIdentity(candidate) === normalizedSource)) || null
}

function summarizeParameterValues(values: string[], fallback = '—'): string {
  const unique = Array.from(new Set(values.map(value => value.trim()).filter(Boolean)))
  if (unique.length === 0) return fallback
  if (unique.length === 1) return unique[0]
  return `${unique.length} values`
}

function summarizeParameterGroupSelection(group: ParameterDefinitionGroup): string {
  return summarizeParameterValues(group.parameters.map(({ parameter }) => {
    if (parameter.type !== 'file' && parameter.type !== 'list') return ''
    const sync = parameter.same_line_as ? `same row as ${parameter.same_line_as}` : ''
    return [sync, `${parameter.selection || 'sequential'} / ${parameter.on_exhaustion || 'wrap'}`]
      .filter(Boolean)
      .join(' · ')
  }))
}

function summarizeParameterGroupColumns(group: ParameterDefinitionGroup): string {
  const columns = summarizeParameterValues(group.parameters.map(({ parameter }) => parameter.column || ''))
  return columns === '—' ? '—' : `Columns: ${columns}`
}

interface ParameterSameLineOption {
  label: string
  value: string
}

const SAME_ROW_SELECTION_VALUE = 'same_line_as'
const SAME_ROW_SELECTION_OPTION: ParameterChoiceOption = {
  value: SAME_ROW_SELECTION_VALUE,
  label: 'Same Row As Parameter',
  description: 'Use the row selected by another parameter in this source file, keeping related random or unique values together.',
}

function getSameLineParameterOptions(parameters: ParameterConfig[], idx: number | null): ParameterSameLineOption[] {
  if (idx === null) return []
  const current = parameters[idx]
  const sourceName = normalizeParameterDataLookup(current?.data_source)
  if (!current || !sourceName || (current.type !== 'file' && current.type !== 'list')) return []
  return parameters
    .map((parameter, index) => ({ parameter, index }))
    .filter(({ parameter, index }) =>
      index !== idx
      && Boolean(parameter.name?.trim())
      && (parameter.type === 'file' || parameter.type === 'list')
      && normalizeParameterDataLookup(parameter.data_source) === sourceName,
    )
    .map(({ parameter }) => ({
      label: `${parameter.name}${parameter.column ? ` (${parameter.column})` : ''}`,
      value: parameter.name || '',
    }))
}

function getSameLineParameterSelectOptions(parameter: ParameterConfig, sameLineOptions: ParameterSameLineOption[]): ParameterSameLineOption[] {
  const currentValue = parameter.same_line_as?.trim()
  if (!currentValue || sameLineOptions.some(option => option.value === currentValue)) return sameLineOptions
  return [{ label: `${currentValue} (unmatched)`, value: currentValue }, ...sameLineOptions]
}

function getParameterSelectionOptions(includeSameRow = false): ParameterChoiceOption[] {
  if (!includeSameRow) return PARAMETER_CHOICE_DEFINITIONS.selection.options
  return [...PARAMETER_CHOICE_DEFINITIONS.selection.options, SAME_ROW_SELECTION_OPTION]
}

function getParameterSelectionDefinition(includeSameRow = false): ParameterChoiceDefinition {
  if (!includeSameRow) return PARAMETER_CHOICE_DEFINITIONS.selection
  return {
    ...PARAMETER_CHOICE_DEFINITIONS.selection,
    options: getParameterSelectionOptions(true),
  }
}

function getParameterSelectionControlValue(parameter: ParameterConfig): string {
  return parameter.same_line_as?.trim() ? SAME_ROW_SELECTION_VALUE : getParameterChoiceValue(parameter, 'selection')
}

type ParameterChoiceField = 'type' | 'update' | 'selection' | 'on_exhaustion'
type MutableParameterChoiceField = Exclude<ParameterChoiceField, 'type'>

interface ParameterChoiceOption {
  description: string
  label: string
  value: string
}

interface ParameterChoiceDefinition {
  description: string
  options: ParameterChoiceOption[]
  title: string
}

const PARAMETER_CHOICE_DEFINITIONS: Record<ParameterChoiceField, ParameterChoiceDefinition> = {
  type: {
    title: 'Parameter Type',
    description: 'Choose how this named parameter gets its value during execution.',
    options: [
      { value: 'list', label: 'List', description: 'Uses inline values or a mapped data source column, then advances according to the selection rule.' },
      { value: 'file', label: 'File', description: 'Reads values from a data source file or imported source parameter file mapping.' },
      { value: 'random_number', label: 'Random Number', description: 'Generates a number between the configured minimum and maximum values.' },
      { value: 'date_time', label: 'Date/Time', description: 'Formats the current date and time with the configured layout, timezone, and offset.' },
      { value: 'timestamp', label: 'Timestamp', description: 'Generates a timestamp in a standard format such as Unix, Unix ms, or ISO 8601.' },
      { value: 'counter', label: 'Counter', description: 'Increments from the configured start value by the configured step.' },
      { value: 'current_iteration', label: 'Current Iteration', description: 'Uses the current VUser iteration number, with optional padding and offset settings.' },
      { value: 'host', label: 'Host', description: 'Uses the current host value, or a host override list when one is provided.' },
      { value: 'time', label: 'Time', description: 'Formats the current time of day using the configured time layout.' },
      { value: 'random_string', label: 'Random String', description: 'Generates a string using the configured length and optional character set.' },
      { value: 'uuid', label: 'UUID', description: 'Generates a UUID when the parameter refreshes.' },
    ],
  },
  update: {
    title: 'Update Cadence',
    description: 'Choose when the runtime refreshes this parameter value.',
    options: [
      { value: 'once', label: 'Once', description: 'Resolve the value once and reuse it for the full VUser run.' },
      { value: 'each_iteration', label: 'Each Iteration', description: 'Refresh the value at the start of every VUser iteration.' },
      { value: 'each_occurrence', label: 'Each Occurrence', description: 'Refresh the value every time the parameter token appears in an action.' },
    ],
  },
  selection: {
    title: 'Selection Strategy',
    description: 'Choose how row-backed parameter values are selected from the source.',
    options: [
      { value: 'sequential', label: 'Sequential', description: 'Read rows in order, which is useful for deterministic replay and imported source behavior.' },
      { value: 'random', label: 'Random', description: 'Pick a row at random for each refresh, which spreads usage across the data set.' },
      { value: 'unique', label: 'Unique', description: 'Reserve unique rows so concurrent VUsers do not reuse the same value until the source is exhausted.' },
    ],
  },
  on_exhaustion: {
    title: 'Exhaustion Behavior',
    description: 'Choose what happens when the source runs out of available parameter values.',
    options: [
      { value: 'wrap', label: 'Wrap', description: 'Start over at the beginning of the source when all rows have been used.' },
      { value: 'last', label: 'Continue With Last Row', description: 'Keep resolving to the last available row or value after the source is exhausted.' },
      { value: 'empty', label: 'Empty', description: 'Resolve the parameter as an empty string after the source is exhausted.' },
      { value: 'error', label: 'Error', description: 'Fail the VUser action when no value is available.' },
    ],
  },
}

function getParameterChoiceValue(parameter: ParameterConfig, field: ParameterChoiceField): string {
  if (field === 'type') return parameter.type || 'list'
  if (field === 'update') return parameter.update || 'each_iteration'
  if (field === 'selection') return parameter.selection || 'sequential'
  return parameter.on_exhaustion || 'wrap'
}

function formatParameterChoiceLabel(field: ParameterChoiceField, value: string): string {
  const option = PARAMETER_CHOICE_DEFINITIONS[field].options.find(candidate => candidate.value === value)
    || (field === 'selection' && value === SAME_ROW_SELECTION_VALUE ? SAME_ROW_SELECTION_OPTION : null)
  return option?.label || value
}

export function ParameterDefinitionsSection({
  focusedSourceName,
  isOpen,
  onAddParameter,
  onRemoveParameter,
  onSelectParameter,
  onToggleOpen,
  onUpdateParameter,
  selectedParameterIdx,
  script,
}: ParameterDefinitionsSectionProps) {
  const [expandedParameterSources, setExpandedParameterSources] = useState<Set<string>>(() => new Set())
  const [activeParameterChoice, setActiveParameterChoice] = useState<{ field: MutableParameterChoiceField; idx: number } | null>(null)
  const [activeSelectionSettingsIdx, setActiveSelectionSettingsIdx] = useState<number | null>(null)
  const [activeParameterSettingsIdx, setActiveParameterSettingsIdx] = useState<number | null>(null)
  const previousParameterCountRef = useRef(script.parameters?.length || 0)
  const parameterGroups = useMemo(
    () => buildParameterDefinitionGroups(script.parameters || [], script.data_sources || []),
    [script.data_sources, script.parameters],
  )
  const parameterSections = useMemo(
    () => buildParameterDefinitionSections(parameterGroups),
    [parameterGroups],
  )
  const focusedParameterSourceKey = useMemo(
    () => findParameterDefinitionGroupBySource(parameterGroups, focusedSourceName)?.key || null,
    [focusedSourceName, parameterGroups],
  )
  const selectedParameterSourceKey = useMemo(
    () => parameterGroups.find(group => group.parameters.some(({ index }) => index === selectedParameterIdx))?.key || null,
    [parameterGroups, selectedParameterIdx],
  )
  const activeChoiceParameter = activeParameterChoice ? (script.parameters || [])[activeParameterChoice.idx] : null
  const activeSelectionSettingsParameter = activeSelectionSettingsIdx !== null ? (script.parameters || [])[activeSelectionSettingsIdx] : null
  const activeSameLineOptions = useMemo(
    () => getSameLineParameterOptions(script.parameters || [], activeSelectionSettingsIdx),
    [activeSelectionSettingsIdx, script.parameters],
  )
  const activeParameterSettingsParameter = activeParameterSettingsIdx !== null ? (script.parameters || [])[activeParameterSettingsIdx] : null
  useEffect(() => {
    if (!focusedParameterSourceKey) return
    setExpandedParameterSources(previous => {
      if (previous.has(focusedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(focusedParameterSourceKey)
      return next
    })
  }, [focusedParameterSourceKey])
  useEffect(() => {
    if (!selectedParameterSourceKey) return
    setExpandedParameterSources(previous => {
      if (previous.has(selectedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(selectedParameterSourceKey)
      return next
    })
  }, [selectedParameterSourceKey])
  useEffect(() => {
    const nextCount = script.parameters?.length || 0
    const previousCount = previousParameterCountRef.current
    previousParameterCountRef.current = nextCount
    if (nextCount <= previousCount) return

    const addedParameterIndex = nextCount - 1
    const addedParameterSourceKey = parameterGroups.find(group =>
      group.parameters.some(({ index }) => index === addedParameterIndex),
    )?.key
    if (!addedParameterSourceKey) return

    setExpandedParameterSources(previous => {
      if (previous.has(addedParameterSourceKey)) return previous
      const next = new Set(previous)
      next.add(addedParameterSourceKey)
      return next
    })
  }, [parameterGroups, script.parameters])
  const toggleParameterSource = (sourceKey: string) => {
    setExpandedParameterSources(previous => {
      const next = new Set(previous)
      if (next.has(sourceKey)) next.delete(sourceKey)
      else next.add(sourceKey)
      return next
    })
  }
  const applyParameterChoice = (value: string) => {
    if (!activeParameterChoice) return
    onUpdateParameter(activeParameterChoice.idx, activeParameterChoice.field, value)
    setActiveParameterChoice(null)
  }
  const selectParameter = (idx: number, parameter: ParameterConfig) => {
    onSelectParameter(idx, parameter)
  }
  const handleAddParameter = () => {
    if (!isOpen) onToggleOpen()
    onAddParameter()
  }
  const renderParameterGroup = (group: ParameterDefinitionGroup) => {
    const isExpanded = expandedParameterSources.has(group.key)
    return (
      <Fragment key={group.key}>
        <tr className="parameter-source-row">
          <td>
            <button
              type="button"
              className="parameter-source-toggle"
              aria-expanded={isExpanded}
              aria-label={`${isExpanded ? 'Collapse' : 'Expand'} source ${group.sourceFile}`}
              onClick={() => toggleParameterSource(group.key)}
            >
              <span aria-hidden="true" className="parameter-section-chevron">{isExpanded ? '▾' : '▸'}</span>
              <span className="parameter-source-label">
                <strong>{group.sourceFile}</strong>
                <small>
                  {[group.sourceType, group.sourceName && group.sourceName !== group.sourceFile ? group.sourceName : '']
                    .filter(Boolean)
                    .join(' · ')}
                </small>
              </span>
            </button>
          </td>
          <td>{group.parameters.length} parameter{group.parameters.length === 1 ? '' : 's'}</td>
          <td className="cell-mono">{formatParameterRowCount(group.rowCount)}</td>
          <td>{summarizeParameterValues(group.parameters.map(({ parameter }) => parameter.update || 'each_iteration'))}</td>
          <td>{summarizeParameterGroupSelection(group)}</td>
          <td>{summarizeParameterGroupColumns(group)}</td>
          <td className="cell-mono">—</td>
          <td className="parameter-cell-empty" style={{ textAlign: 'right' }}>{isExpanded ? 'Expanded' : 'Select to edit'}</td>
        </tr>
        {isExpanded && group.parameters.map(({ index: idx, parameter }) => (
          <ParameterDefinitionRow
            idx={idx}
            key={`${group.key}-${parameter.name || 'parameter'}-${idx}`}
            isSelected={selectedParameterIdx === idx}
            onOpenChoice={setActiveParameterChoice}
            onOpenSettings={setActiveParameterSettingsIdx}
            onOpenSelectionSettings={setActiveSelectionSettingsIdx}
            onRemoveParameter={onRemoveParameter}
            onSelectParameter={selectParameter}
            onUpdateParameter={onUpdateParameter}
            parameter={parameter}
            rowCount={group.rowCount}
          />
        ))}
      </Fragment>
    )
  }

  return (
    <>
      <div className="panel-title-row parameter-section-header" style={{ marginBottom: 12 }}>
        <button
          type="button"
          className="parameter-section-toggle"
          aria-expanded={isOpen}
          aria-label={`${isOpen ? 'Collapse' : 'Expand'} Parameters`}
          onClick={onToggleOpen}
        >
          <span aria-hidden="true" className="parameter-section-chevron">{isOpen ? '▾' : '▸'}</span>
          <span>
            <strong style={{ display: 'block', marginBottom: 4 }}>Parameters</strong>
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Define parameters with source-style update cadence and generated values.</span>
          </span>
        </button>
        <button type="button" className="btn btn-sm btn-accent" onClick={handleAddParameter}>+ Add Parameter</button>
      </div>

      {isOpen && ((script.parameters?.length || 0) === 0 ? (
        <div className="parameter-empty-state" style={{ marginBottom: 20 }}>No parameters defined. Add one for random, date/time, host, counter, file, or list-driven values.</div>
      ) : (
        <div className="parameter-table-wrap parameter-definitions-wrap" style={{ marginBottom: 20 }}>
          <table className="parameter-table parameter-definitions-table">
            <thead>
              <tr>
                <th className="parameter-source-type-column">Source File / Type</th>
                <th>Parameter Name</th>
                <th>Rows</th>
                <th>Update</th>
                <th>Selection</th>
                <th>Settings</th>
                <th>Template</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {parameterSections.map(section => (
                <Fragment key={section.key}>
                  <tr className="parameter-category-row">
                    <td colSpan={PARAMETER_DEFINITION_COLUMN_COUNT}>
                      <span>{section.title}</span>
                      <small>{section.summary}</small>
                    </td>
                  </tr>
                  {section.groups.map(renderParameterGroup)}
                </Fragment>
              ))}
            </tbody>
          </table>
          {activeParameterChoice && activeChoiceParameter && (
            <ParameterChoiceDialog
              definition={PARAMETER_CHOICE_DEFINITIONS[activeParameterChoice.field]}
              onClose={() => setActiveParameterChoice(null)}
              onSelect={applyParameterChoice}
              selectedValue={getParameterChoiceValue(activeChoiceParameter, activeParameterChoice.field)}
            />
          )}
          {activeSelectionSettingsIdx !== null && activeSelectionSettingsParameter && (
            <ParameterSelectionSettingsDialog
              onClose={() => setActiveSelectionSettingsIdx(null)}
              onUpdate={(field, value) => onUpdateParameter(activeSelectionSettingsIdx, field, value)}
              parameter={activeSelectionSettingsParameter}
              sameLineOptions={activeSameLineOptions}
            />
          )}
          {activeParameterSettingsIdx !== null && activeParameterSettingsParameter && (
            <ParameterSettingsDialog
              onClose={() => setActiveParameterSettingsIdx(null)}
              onUpdate={(field, value) => onUpdateParameter(activeParameterSettingsIdx, field, value)}
              parameter={activeParameterSettingsParameter}
            />
          )}
        </div>
      ))}
    </>
  )
}

interface ParameterDefinitionRowProps {
  idx: number
  isSelected: boolean
  onOpenChoice: (choice: { field: MutableParameterChoiceField; idx: number }) => void
  onOpenSettings: (idx: number) => void
  onOpenSelectionSettings: (idx: number) => void
  onRemoveParameter: (idx: number) => void
  onSelectParameter: (idx: number, parameter: ParameterConfig) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
  rowCount: number | null
}

function ParameterDefinitionRow({
  idx,
  isSelected,
  onOpenChoice,
  onOpenSettings,
  onOpenSelectionSettings,
  onRemoveParameter,
  onSelectParameter,
  onUpdateParameter,
  parameter,
  rowCount,
}: ParameterDefinitionRowProps) {
  const parameterName = parameter.name || `Parameter ${idx + 1}`
  const selectParameter = () => onSelectParameter(idx, parameter)
  return (
    <tr
      className={`parameter-child-row ${isSelected ? 'selected' : ''}`}
      onClick={selectParameter}
      onFocus={selectParameter}
    >
      <td className="parameter-source-type-cell" aria-label="Source file or type inherited from expanded row" />
      <td>
        <input
          value={parameter.name || ''}
          onChange={e => onUpdateParameter(idx, 'name', e.target.value)}
          placeholder="session_id"
        />
      </td>
      <td className="cell-mono">{formatParameterRowCount(rowCount)}</td>
      <td>
        <ParameterChoiceButton
          field="update"
          onClick={() => onOpenChoice({ field: 'update', idx })}
          parameterName={parameterName}
          value={getParameterChoiceValue(parameter, 'update')}
        />
      </td>
      <td>
        <div className="parameter-selection-controls">
          {(parameter.type === 'file' || parameter.type === 'list') ? (
            <ParameterSelectionSettingsButton
              onClick={() => onOpenSelectionSettings(idx)}
              parameter={parameter}
              parameterName={parameterName}
            />
          ) : (
            <span className="parameter-cell-empty">—</span>
          )}
        </div>
      </td>
      <td>
        <ParameterSettingsButton
          onClick={(event) => {
            event.stopPropagation()
            onOpenSettings(idx)
          }}
          parameter={parameter}
          parameterName={parameterName}
        />
      </td>
      <td className="cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : '—'}</td>
      <td style={{ textAlign: 'right' }}>
        <button
          className="btn btn-sm btn-ghost"
          style={{ color: 'var(--danger)' }}
          onClick={(event: ReactMouseEvent<HTMLButtonElement>) => {
            event.stopPropagation()
            onRemoveParameter(idx)
          }}
        >
          Remove
        </button>
      </td>
    </tr>
  )
}

interface ParameterChoiceButtonProps {
  field: ParameterChoiceField
  onClick: () => void
  parameterName: string
  value: string
}

function ParameterChoiceButton({
  field,
  onClick,
  parameterName,
  value,
}: ParameterChoiceButtonProps) {
  const fieldLabel = PARAMETER_CHOICE_DEFINITIONS[field].title
  return (
    <button
      type="button"
      className="parameter-choice-button"
      aria-label={`Change ${fieldLabel} for ${parameterName}`}
      onClick={onClick}
    >
      <strong>{formatParameterChoiceLabel(field, value)}</strong>
      <small>{value}</small>
    </button>
  )
}

interface ParameterSelectionSettingsButtonProps {
  onClick: () => void
  parameter: ParameterConfig
  parameterName: string
}

function ParameterSelectionSettingsButton({
  onClick,
  parameter,
  parameterName,
}: ParameterSelectionSettingsButtonProps) {
  return (
    <button
      type="button"
      className="parameter-choice-button parameter-settings-button"
      aria-label={`Open Selection settings for ${parameterName}`}
      onClick={onClick}
    >
      <strong>Settings</strong>
      <small>
        {parameter.same_line_as ? 'Same Row As Parameter' : formatParameterChoiceLabel('selection', getParameterChoiceValue(parameter, 'selection'))}
        {' / '}
        {formatParameterChoiceLabel('on_exhaustion', getParameterChoiceValue(parameter, 'on_exhaustion'))}
        {parameter.same_line_as ? ` · Same row as ${parameter.same_line_as}` : ''}
      </small>
    </button>
  )
}

interface ParameterSettingsButtonProps {
  onClick: (event: ReactMouseEvent<HTMLButtonElement>) => void
  parameter: ParameterConfig
  parameterName: string
}

function summarizeParameterSettings(parameter: ParameterConfig): string {
  const details: string[] = []
  if (parameter.data_source) details.push(`Source: ${parameter.data_source}`)
  if (parameter.column) details.push(`Column: ${parameter.column}`)
  if (parameter.values?.length) details.push(`${parameter.values.length} values`)
  if (parameter.type === 'random_number') details.push(`${parameter.min ?? 0}-${parameter.max ?? 100}`)
  if (parameter.type === 'random_string') details.push(`Length ${parameter.length ?? 12}`)
  if (parameter.type === 'counter' || parameter.type === 'current_iteration') details.push(`Step ${parameter.step ?? 1}`)
  if (parameter.format) details.push(`Format: ${parameter.format}`)
  if (parameter.timezone) details.push(parameter.timezone)
  if (parameter.seed !== undefined) details.push(`Seed ${parameter.seed}`)
  return details.slice(0, 2).join(' • ') || 'Configure fields'
}

function ParameterSettingsButton({
  onClick,
  parameter,
  parameterName,
}: ParameterSettingsButtonProps) {
  return (
    <button
      type="button"
      className="parameter-choice-button parameter-settings-button"
      aria-label={`Open settings for ${parameterName}`}
      onClick={onClick}
    >
      <strong>Configure</strong>
      <small>{summarizeParameterSettings(parameter)}</small>
    </button>
  )
}

// Built-in (runtime-generated) parameter types — every type except the data-backed file/list.
const BUILTIN_PARAMETER_TYPE_OPTIONS = PARAMETER_CHOICE_DEFINITIONS.type.options.filter(
  option => option.value !== 'list' && option.value !== 'file',
)

interface ParameterTypeFieldsProps {
  parameter: ParameterConfig
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
}

interface ParameterFormatControlProps {
  parameter: ParameterConfig
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
}

function ParameterFormatControl({ parameter, onUpdate }: ParameterFormatControlProps) {
  const type = parameter.type || 'list'
  const presets = parameterFormatPresetsForType(type)
  const currentFormat = parameter.format || parameterFormatForType(type, '')
  const selectedPreset = presets.find(preset => preset.value === currentFormat)
  const allowsCustom = type === 'date_time' || type === 'time'
  const selectValue = selectedPreset ? selectedPreset.value : CUSTOM_PARAMETER_FORMAT_VALUE

  const applyPreset = (value: string) => {
    if (value === CUSTOM_PARAMETER_FORMAT_VALUE) {
      onUpdate('format', currentFormat || '')
      return
    }
    const preset = presets.find(candidate => candidate.value === value)
    if (!preset) return
    onUpdate('format', preset.value)
    if (preset.timezone !== undefined) {
      onUpdate('timezone', preset.timezone)
    }
  }

  return (
    <div className="parameter-format-control">
      <select value={selectValue} onChange={event => applyPreset(event.target.value)}>
        {presets.map(preset => (
          <option key={preset.value} value={preset.value}>{preset.label}</option>
        ))}
        {allowsCustom && <option value={CUSTOM_PARAMETER_FORMAT_VALUE}>Custom Go layout</option>}
      </select>
      {allowsCustom && selectValue === CUSTOM_PARAMETER_FORMAT_VALUE && (
        <input
          value={parameter.format || ''}
          onChange={event => onUpdate('format', event.target.value)}
          placeholder="Go time layout"
        />
      )}
      <small>{selectedPreset?.description || (allowsCustom ? 'Use any Go time layout compatible with the DSL runtime.' : 'Choose a supported timestamp format.')}</small>
    </div>
  )
}

// ParameterTypeFields renders the key inline configuration inputs for a built-in parameter type
// (min/max, start/step, length/charset, format/timezone/offset). Mirrors the inputs in the
// Parameter Settings dialog so both surfaces stay consistent.
function ParameterTypeFields({ parameter, onUpdate }: ParameterTypeFieldsProps) {
  const type = parameter.type || 'list'
  const isTime = type === 'date_time' || type === 'timestamp' || type === 'time'
  if (type === 'uuid' || type === 'host') {
    return (
      <p className="parameter-settings-empty">
        {type === 'uuid'
          ? 'UUID values are generated automatically — no extra configuration needed.'
          : 'Host uses the runtime host value. Configure optional host overrides later in Parameter Data.'}
      </p>
    )
  }
  return (
    <div className="parameter-settings-grid parameterize-type-fields">
      {type === 'random_number' && (
        <>
          <label><span>Minimum</span><input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" /></label>
          <label><span>Maximum</span><input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" /></label>
        </>
      )}
      {(type === 'counter' || type === 'current_iteration') && (
        <>
          <label><span>Start</span><input type="number" value={numberInputValue(parameter.start, type === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" /></label>
          <label><span>Step</span><input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" /></label>
          <label><span>End</span><input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" /></label>
        </>
      )}
      {type === 'random_string' && (
        <>
          <label><span>Length</span><input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" /></label>
          <label><span>Charset</span><input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" /></label>
        </>
      )}
      {isTime && (
        <>
          <label>
            <span>Format</span>
            <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
          </label>
          <label><span>Timezone</span><input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="UTC, America/New_York" /></label>
          <label><span>Offset Seconds</span><input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></label>
        </>
      )}
    </div>
  )
}

interface ExistingParameterDialogProps {
  draft: ActionEditorParameterDraft
  onClose: () => void
  onSelect: (parameterName: string) => void
  parameters: ParameterConfig[]
}

function ExistingParameterDialog({
  draft,
  onClose,
  onSelect,
  parameters,
}: ExistingParameterDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const [query, setQuery] = useState('')
  const normalizedQuery = query.trim().toLowerCase()
  const filteredParameters = useMemo(() => parameters.filter(parameter => {
    const haystack = [parameter.name, parameter.type, parameter.data_source, parameter.column]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return !normalizedQuery || haystack.includes(normalizedQuery)
  }), [normalizedQuery, parameters])

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-existing-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Use Existing Parameter"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Use Existing Parameter</h3>
            <p className="editor-dialog-subtitle">
              Replace <span className="cell-mono">{draft.selectedText}</span> with an existing parameter token.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <label className="parameterize-name-field">
            <span>Find Parameter</span>
            <input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search by name, type, or source" />
          </label>
          {parameters.length === 0 ? (
            <p className="parameter-settings-empty">No parameters exist yet. Create a new parameter for this selection first.</p>
          ) : filteredParameters.length === 0 ? (
            <p className="parameter-settings-empty">No parameters match this search.</p>
          ) : (
            <div className="parameter-existing-list">
              {filteredParameters.map((parameter, index) => {
                const name = parameter.name || `Parameter ${index + 1}`
                const summary = summarizeParameterSettings(parameter)
                return (
                  <button
                    type="button"
                    className="parameter-choice-option"
                    key={`${name}-${index}`}
                    onClick={() => onSelect(name)}
                  >
                    <span className="parameter-choice-option-heading">
                      <strong>{name}</strong>
                      <span className="cell-mono">{parameter.type || 'list'}</span>
                    </span>
                    <span>{summary}</span>
                    <span className="cell-mono">{`{{param.${name}}}`}</span>
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

interface JsonOccurrenceReviewDialogProps {
  onClose: () => void
  onCreateNew: (ranges: Array<{ from: number; to: number }>) => void
  onUseExisting: (parameterName: string, ranges: Array<{ from: number; to: number }>) => void
  parameters: ParameterConfig[]
  review: JsonParameterOccurrenceReviewState
}

function JsonOccurrenceReviewDialog({
  onClose,
  onCreateNew,
  onUseExisting,
  parameters,
  review,
}: JsonOccurrenceReviewDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const optionalOccurrences = useMemo(
    () => review.occurrences.filter(occurrence => !occurrence.isOriginal),
    [review.occurrences],
  )
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set(optionalOccurrences.map(occurrence => occurrence.id)))
  const [existingName, setExistingName] = useState(() => parameters[0]?.name || '')

  useEffect(() => {
    setSelectedIds(new Set(optionalOccurrences.map(occurrence => occurrence.id)))
  }, [optionalOccurrences])

  useEffect(() => {
    if (!existingName && parameters[0]?.name) {
      setExistingName(parameters[0].name)
    }
  }, [existingName, parameters])

  const selectedRanges = optionalOccurrences
    .filter(occurrence => selectedIds.has(occurrence.id))
    .map(occurrence => ({ from: occurrence.from, to: occurrence.to }))
  const selectedCount = selectedRanges.length + 1

  const toggleOccurrence = (id: string, checked: boolean) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-occurrence-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Review Matching Occurrences"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Review Matching Occurrences</h3>
            <p className="editor-dialog-subtitle">
              The original selection is always replaced. Choose which matching JSON string values should also use the parameter.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <div className="parameter-occurrence-summary">
            <strong className="cell-mono">{review.draft.selectedText}</strong>
            <span>{selectedCount} replacement{selectedCount === 1 ? '' : 's'} selected</span>
          </div>
          {optionalOccurrences.length === 0 ? (
            <p className="parameter-settings-empty">No additional exact string-value matches were found in the Script JSON.</p>
          ) : (
            <div className="parameter-occurrence-list">
              {optionalOccurrences.map(occurrence => (
                <label className="parameter-occurrence-item" key={occurrence.id}>
                  <input
                    type="checkbox"
                    checked={selectedIds.has(occurrence.id)}
                    onChange={event => toggleOccurrence(occurrence.id, event.target.checked)}
                  />
                  <span>
                    <strong>{occurrence.path}</strong>
                    <small>Line {occurrence.line}</small>
                    <code>{occurrence.preview}</code>
                  </span>
                </label>
              ))}
            </div>
          )}
          <div className="parameter-occurrence-actions">
            <button type="button" className="btn btn-sm btn-primary" onClick={() => onCreateNew(selectedRanges)}>
              Create new parameter
            </button>
            <label>
              <span>Existing Parameter</span>
              <select value={existingName} onChange={event => setExistingName(event.target.value)} disabled={parameters.length === 0}>
                {parameters.map((parameter, index) => {
                  const name = parameter.name || `Parameter ${index + 1}`
                  return <option key={`${name}-${index}`} value={name}>{name}</option>
                })}
              </select>
            </label>
            <button
              type="button"
              className="btn btn-sm btn-secondary"
              disabled={!existingName || parameters.length === 0}
              onClick={() => onUseExisting(existingName, selectedRanges)}
            >
              Use existing parameter
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

interface ParameterizeDialogProps {
  draft: ActionEditorParameterDraft
  onClose: () => void
  onChooseFile: () => void
  onConfirmBuiltin: (name: string, type: string, typeConfig: Partial<ParameterConfig>) => void
}

// ParameterizeDialog forks the right-click → Parameterize flow: pick a File/Data parameter (which
// continues to the existing Parameter Data editor) or a Built-in Function, which expands in place
// to a type picker plus that type's key configuration fields.
function ParameterizeDialog({ draft, onClose, onChooseFile, onConfirmBuiltin }: ParameterizeDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const [mode, setMode] = useState<'choose' | 'builtin'>('choose')
  const [name, setName] = useState(() => sanitizeVarName(draft.requestedName || draft.seedValue))
  const [typeParam, setTypeParam] = useState<ParameterConfig | null>(null)

  const selectType = (type: string) => {
    setTypeParam(prev => applyParameterTypeDefaults({ ...(prev || EMPTY_PARAMETER), name, type }, type))
  }
  const updateField = (field: keyof ParameterConfig, value: unknown) => {
    setTypeParam(prev => (prev ? { ...prev, [field]: value } : prev))
  }
  const canCreate = Boolean(name.trim() && typeParam?.type)
  const create = () => {
    if (!typeParam?.type || !name.trim()) return
    onConfirmBuiltin(sanitizeVarName(name), typeParam.type, typeParam)
  }

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameterize-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameterize Selection"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameterize Selection</h3>
            <p className="editor-dialog-subtitle">
              Replace <span className="cell-mono">{draft.selectedText}</span> with a parameter.
            </p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          {mode === 'choose' ? (
            <>
              <button type="button" className="parameter-choice-option" onClick={onChooseFile}>
                <span className="parameter-choice-option-heading"><strong>File / Data</strong><span className="cell-mono">file</span></span>
                <span>Use inline values or a data source file/column, advancing per the selection rule.</span>
              </button>
              <button type="button" className="parameter-choice-option" onClick={() => setMode('builtin')}>
                <span className="parameter-choice-option-heading"><strong>Built-in Function</strong><span className="cell-mono">fn</span></span>
                <span>Generate the value at runtime — random number, iteration number, time, UUID, and more.</span>
              </button>
            </>
          ) : (
            <div className="parameterize-builtin">
              <label className="parameterize-name-field">
                <span>Parameter Name</span>
                <input value={name} onChange={event => setName(sanitizeVarName(event.target.value))} placeholder="param_name" />
              </label>
              <div className="parameterize-type-grid">
                {BUILTIN_PARAMETER_TYPE_OPTIONS.map(option => {
                  const selected = typeParam?.type === option.value
                  return (
                    <button
                      type="button"
                      key={option.value}
                      className={`parameter-choice-option ${selected ? 'selected' : ''}`}
                      aria-pressed={selected}
                      onClick={() => selectType(option.value)}
                    >
                      <span className="parameter-choice-option-heading"><strong>{option.label}</strong><span className="cell-mono">{option.value}</span></span>
                      <span>{option.description}</span>
                    </button>
                  )
                })}
              </div>
              {typeParam && <ParameterTypeFields parameter={typeParam} onUpdate={updateField} />}
              <div className="parameter-draft-actions">
                <button type="button" className="btn btn-sm btn-primary" onClick={create} disabled={!canCreate}>Create Parameter</button>
                <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMode('choose')}>Back</button>
                <button type="button" className="btn btn-sm btn-ghost" onClick={onClose}>Cancel</button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

interface ParameterChoiceDialogProps {
  definition: ParameterChoiceDefinition
  onClose: () => void
  onSelect: (value: string) => void
  selectedValue: string
}

function ParameterChoiceDialog({
  definition,
  onClose,
  onSelect,
  selectedValue,
}: ParameterChoiceDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={definition.title}
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">{definition.title}</h3>
            <p className="editor-dialog-subtitle">{definition.description}</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          {definition.options.map(option => {
            const selected = option.value === selectedValue
            return (
              <button
                type="button"
                className={`parameter-choice-option ${selected ? 'selected' : ''}`}
                aria-pressed={selected}
                key={option.value}
                onClick={() => onSelect(option.value)}
              >
                <span className="parameter-choice-option-heading">
                  <strong>{option.label}</strong>
                  <span className="cell-mono">{option.value}</span>
                </span>
                <span>{option.description}</span>
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}

interface ParameterSettingsDialogProps {
  onClose: () => void
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
}

function parseParameterListInput(value: string): string[] {
  return value.split(',').map(entry => entry.trim()).filter(Boolean)
}

function numberInputValue(value: number | undefined, fallback: number | ''): number | string {
  return value ?? fallback
}

function updateOptionalNumber(onUpdate: (field: keyof ParameterConfig, value: unknown) => void, field: keyof ParameterConfig, value: string) {
  onUpdate(field, value.trim() === '' ? undefined : Number(value))
}

interface ParameterSettingsBodyProps {
  className?: string
  onUpdate: (field: keyof ParameterConfig, value: unknown) => void
  parameter: ParameterConfig
}

export function ParameterSettingsBody({
  className = '',
  onUpdate,
  parameter,
}: ParameterSettingsBodyProps) {
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const isDataBacked = parameterType === 'file' || parameterType === 'list'
  const isGenerated = ['random_number', 'counter', 'current_iteration', 'random_string', 'uuid'].includes(parameterType)
  const isTime = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const showsDataMapping = isDataBacked || parameterType === 'host'
  const showsOutput = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined

  return (
    <div className={`parameter-settings-dialog-body ${className}`}>
      <details className="parameter-settings-accordion" open>
        <summary>
          <strong>General</strong>
          <span>Parameter type and runtime behavior</span>
        </summary>
        <div className="parameter-settings-grid">
          <div className="parameter-settings-field">
            <span>Parameter Type</span>
            <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
          </div>
          <label>
            <span>Update Cadence</span>
            <select
              aria-label={`Update Cadence for ${parameterName}`}
              value={getParameterChoiceValue(parameter, 'update')}
              onChange={event => onUpdate('update', event.target.value)}
            >
              {PARAMETER_CHOICE_DEFINITIONS.update.options.map(option => (
                <option key={option.value} value={option.value}>{option.label}</option>
              ))}
            </select>
          </label>
        </div>
      </details>

      <details className="parameter-settings-accordion" open={showsDataMapping}>
        <summary>
          <strong>Data Mapping</strong>
          <span>Source, column, row selection, and inline values</span>
        </summary>
        {showsDataMapping ? (
          <div className="parameter-settings-grid">
            {isDataBacked && (
              <>
                <label>
                  <span>Data Source</span>
                  <input value={parameter.data_source || ''} onChange={event => onUpdate('data_source', event.target.value)} placeholder="users" />
                </label>
                <label>
                  <span>Column</span>
                  <input value={parameter.column || ''} onChange={event => onUpdate('column', event.target.value)} placeholder="column name" />
                </label>
                <label>
                  <span>Selection</span>
                  <select value={getParameterChoiceValue(parameter, 'selection')} onChange={event => onUpdate('selection', event.target.value)}>
                    {PARAMETER_CHOICE_DEFINITIONS.selection.options.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>On Exhaustion</span>
                  <select value={getParameterChoiceValue(parameter, 'on_exhaustion')} onChange={event => onUpdate('on_exhaustion', event.target.value)}>
                    {PARAMETER_CHOICE_DEFINITIONS.on_exhaustion.options.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <label className="parameter-settings-wide">
                  <span>Same Row As</span>
                  <input value={parameter.same_line_as || ''} onChange={event => onUpdate('same_line_as', event.target.value.trim() || undefined)} placeholder="optional parameter name" />
                </label>
              </>
            )}
            {(parameterType === 'list' || parameterType === 'host') && (
              <label className="parameter-settings-wide">
                <span>{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</span>
                <input
                  value={(parameter.values || []).join(', ')}
                  onChange={event => onUpdate('values', parseParameterListInput(event.target.value))}
                  placeholder={parameterType === 'host' ? 'host-a, host-b' : 'value1, value2'}
                />
              </label>
            )}
          </div>
        ) : (
          <p className="parameter-settings-empty">This parameter type does not use source mapping fields.</p>
        )}
      </details>

      <details className="parameter-settings-accordion" open={isGenerated || isTime}>
        <summary>
          <strong>Type Options</strong>
          <span>Generated values, time formats, and runtime options</span>
        </summary>
        {isGenerated || isTime ? (
          <div className="parameter-settings-grid">
            {parameterType === 'random_number' && (
              <>
                <label><span>Minimum</span><input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" /></label>
                <label><span>Maximum</span><input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" /></label>
              </>
            )}
            {(parameterType === 'counter' || parameterType === 'current_iteration') && (
              <>
                <label><span>Start</span><input type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" /></label>
                <label><span>End</span><input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" /></label>
                <label><span>Step</span><input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" /></label>
              </>
            )}
            {parameterType === 'random_string' && (
              <>
                <label><span>Length</span><input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" /></label>
                <label><span>Charset</span><input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" /></label>
              </>
            )}
            {parameterType === 'uuid' && <p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p>}
            {isTime && (
              <>
                <label>
                  <span>Format</span>
                  <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
                </label>
                <label><span>Timezone</span><input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="UTC, America/New_York" /></label>
                <label><span>Offset Seconds</span><input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" /></label>
              </>
            )}
          </div>
        ) : (
          <p className="parameter-settings-empty">No additional type-specific options are required.</p>
        )}
      </details>

      <details className="parameter-settings-accordion" open={showsOutput}>
        <summary>
          <strong>Output And Reproducibility</strong>
          <span>Padding and optional seed</span>
        </summary>
        <div className="parameter-settings-grid">
          {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
            <>
              <label><span>Pad Width</span><input type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdate('pad_width', Number(event.target.value))} placeholder="pad width" /></label>
              <label><span>Pad Character</span><input value={parameter.pad_char || ''} onChange={event => onUpdate('pad_char', event.target.value)} placeholder="pad char" /></label>
            </>
          )}
          <label>
            <span>Seed</span>
            <input
              type="number"
              value={numberInputValue(parameter.seed, '')}
              onChange={event => updateOptionalNumber(onUpdate, 'seed', event.target.value)}
              placeholder="seed (optional)"
            />
          </label>
        </div>
      </details>
    </div>
  )
}

function ParameterSettingsDialog({
  onClose,
  onUpdate,
  parameter,
}: ParameterSettingsDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const hasDataMappingSettings = parameterType === 'file' || parameterType === 'list' || parameterType === 'host'
  const hasGeneratedValueSettings = ['random_number', 'counter', 'current_iteration', 'random_string', 'uuid'].includes(parameterType)
  const hasTimeSettings = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const hasOutputSettings = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Settings for ${parameterName}`}
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter Settings</h3>
            <p className="editor-dialog-subtitle">Edit advanced settings for {parameterName} without crowding the Parameters table.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-settings-dialog-body">
          <details className="parameter-settings-accordion" open>
            <summary>
              <strong>General</strong>
              <span>Parameter type and runtime behavior</span>
            </summary>
            <div className="parameter-settings-grid">
              <div className="parameter-settings-field">
                <span>Parameter Type</span>
                <ParameterTypeReadonlyValue parameterName={parameterName} parameterType={parameterType} />
              </div>
            </div>
          </details>

          <details className="parameter-settings-accordion" open={hasDataMappingSettings}>
            <summary>
              <strong>Data Mapping</strong>
              <span>Source, column, and inline values</span>
            </summary>
            {hasDataMappingSettings ? (
              <div className="parameter-settings-grid">
                {(parameterType === 'file' || parameterType === 'list') && (
                  <>
                    <label>
                      <span>Data Source</span>
                      <input
                        value={parameter.data_source || ''}
                        onChange={event => onUpdate('data_source', event.target.value)}
                        placeholder="users"
                      />
                    </label>
                    <label>
                      <span>Column</span>
                      <input
                        value={parameter.column || ''}
                        onChange={event => onUpdate('column', event.target.value)}
                        placeholder="column name"
                      />
                    </label>
                  </>
                )}
                {(parameterType === 'list' || parameterType === 'host') && (
                  <label className="parameter-settings-wide">
                    <span>{parameterType === 'host' ? 'Host Overrides' : 'Inline Values'}</span>
                    <input
                      value={(parameter.values || []).join(', ')}
                      onChange={event => onUpdate('values', parseParameterListInput(event.target.value))}
                      placeholder={parameterType === 'host' ? 'host-a, host-b' : 'value1, value2'}
                    />
                  </label>
                )}
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use source mapping fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasGeneratedValueSettings}>
            <summary>
              <strong>Generated Values</strong>
              <span>Ranges, counters, and string generation</span>
            </summary>
            {hasGeneratedValueSettings ? (
              <div className="parameter-settings-grid">
                {parameterType === 'random_number' && (
                  <>
                    <label>
                      <span>Minimum</span>
                      <input type="number" value={numberInputValue(parameter.min, 0)} onChange={event => onUpdate('min', Number(event.target.value))} placeholder="min" />
                    </label>
                    <label>
                      <span>Maximum</span>
                      <input type="number" value={numberInputValue(parameter.max, 100)} onChange={event => onUpdate('max', Number(event.target.value))} placeholder="max" />
                    </label>
                  </>
                )}
                {(parameterType === 'counter' || parameterType === 'current_iteration') && (
                  <>
                    <label>
                      <span>Start</span>
                      <input type="number" value={numberInputValue(parameter.start, parameterType === 'current_iteration' ? 1 : 0)} onChange={event => onUpdate('start', Number(event.target.value))} placeholder="start" />
                    </label>
                    <label>
                      <span>End</span>
                      <input type="number" value={numberInputValue(parameter.end, 0)} onChange={event => onUpdate('end', Number(event.target.value))} placeholder="end (optional)" />
                    </label>
                    <label>
                      <span>Step</span>
                      <input type="number" value={numberInputValue(parameter.step, 1)} onChange={event => onUpdate('step', Number(event.target.value))} placeholder="step" />
                    </label>
                  </>
                )}
                {parameterType === 'random_string' && (
                  <>
                    <label>
                      <span>Length</span>
                      <input type="number" value={numberInputValue(parameter.length, 12)} onChange={event => onUpdate('length', Number(event.target.value))} placeholder="length" />
                    </label>
                    <label>
                      <span>Charset</span>
                      <input value={parameter.charset || ''} onChange={event => onUpdate('charset', event.target.value)} placeholder="charset override" />
                    </label>
                  </>
                )}
                {parameterType === 'uuid' && <p className="parameter-settings-empty">UUID values are generated automatically using the selected update cadence.</p>}
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use generated value fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasTimeSettings}>
            <summary>
              <strong>Time And Format</strong>
              <span>Format, timezone, and offset</span>
            </summary>
            {hasTimeSettings ? (
              <div className="parameter-settings-grid">
                <label>
                  <span>Format</span>
                  <ParameterFormatControl parameter={parameter} onUpdate={onUpdate} />
                </label>
                <label>
                  <span>Timezone</span>
                  <input value={parameter.timezone || ''} onChange={event => onUpdate('timezone', event.target.value)} placeholder="timezone (UTC, America/New_York)" />
                </label>
                <label>
                  <span>Offset Seconds</span>
                  <input type="number" value={numberInputValue(parameter.offset_seconds, 0)} onChange={event => onUpdate('offset_seconds', Number(event.target.value))} placeholder="offset seconds" />
                </label>
              </div>
            ) : (
              <p className="parameter-settings-empty">This parameter type does not use time formatting fields.</p>
            )}
          </details>

          <details className="parameter-settings-accordion" open={hasOutputSettings}>
            <summary>
              <strong>Output And Reproducibility</strong>
              <span>Padding and optional seed</span>
            </summary>
            <div className="parameter-settings-grid">
              {(parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number') && (
                <>
                  <label>
                    <span>Pad Width</span>
                    <input type="number" value={numberInputValue(parameter.pad_width, 0)} onChange={event => onUpdate('pad_width', Number(event.target.value))} placeholder="pad width" />
                  </label>
                  <label>
                    <span>Pad Character</span>
                    <input value={parameter.pad_char || ''} onChange={event => onUpdate('pad_char', event.target.value)} placeholder="pad char" />
                  </label>
                </>
              )}
              <label>
                <span>Seed</span>
                <input
                  type="number"
                  value={numberInputValue(parameter.seed, '')}
                  onChange={event => updateOptionalNumber(onUpdate, 'seed', event.target.value)}
                  placeholder="seed (optional)"
                />
              </label>
            </div>
          </details>
        </div>
      </div>
    </div>
  )
}

interface ParameterSelectionSettingsDialogProps {
  onClose: () => void
  onUpdate: (field: 'selection' | 'on_exhaustion' | 'same_line_as', value: string | undefined) => void
  parameter: ParameterConfig
  sameLineOptions: ParameterSameLineOption[]
}

function ParameterSelectionSettingsDialog({
  onClose,
  onUpdate,
  parameter,
  sameLineOptions,
}: ParameterSelectionSettingsDialogProps) {
  const dialogRef = useEditorDialogRef(onClose)
  const parameterName = parameter.name || 'Parameter'
  const selectionValue = getParameterSelectionControlValue(parameter)
  const exhaustionValue = getParameterChoiceValue(parameter, 'on_exhaustion')
  const sameLineSelectOptions = getSameLineParameterSelectOptions(parameter, sameLineOptions)
  const sameLineDisabledValues = sameLineSelectOptions.length === 0 ? [SAME_ROW_SELECTION_VALUE] : []
  const handleSelectionUpdate = (value: string) => {
    if (value === SAME_ROW_SELECTION_VALUE) {
      const nextSameLine = sameLineSelectOptions[0]?.value || ''
      onUpdate('same_line_as', nextSameLine || undefined)
      return
    }
    if (parameter.same_line_as) onUpdate('same_line_as', undefined)
    onUpdate('selection', value)
  }

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
        ref={dialogRef}
        className="editor-dialog parameter-choice-dialog parameter-selection-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Selection Settings for ${parameterName}`}
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Selection Settings</h3>
            <p className="editor-dialog-subtitle">Configure how this parameter selects rows and what happens when the source runs out of values.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body parameter-choice-dialog-body">
          <ParameterSelectionChoiceGroup
            definition={getParameterSelectionDefinition(true)}
            disabledValues={sameLineDisabledValues}
            onSelect={handleSelectionUpdate}
            selectedValue={selectionValue}
          />
          <ParameterSelectionChoiceGroup
            definition={PARAMETER_CHOICE_DEFINITIONS.on_exhaustion}
            onSelect={value => onUpdate('on_exhaustion', value)}
            selectedValue={exhaustionValue}
          />
          <section className="parameter-selection-choice-group">
            <div>
              <h4>Same Row Sync</h4>
              <p>Use the row selected by another parameter from the same source file, so random or unique row selection keeps related columns together.</p>
            </div>
            <label className="parameter-settings-wide">
              <span>Sync Row With</span>
              <select
                aria-label={`Same Row Sync for ${parameterName}`}
                disabled={sameLineSelectOptions.length === 0}
                value={parameter.same_line_as || ''}
                onChange={event => onUpdate('same_line_as', event.target.value || undefined)}
              >
                <option value="">Independent row selection</option>
                {sameLineSelectOptions.map(option => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
            </label>
            {sameLineSelectOptions.length === 0 && (
              <p className="parameter-settings-empty">No other parameters are mapped to this source file.</p>
            )}
          </section>
        </div>
      </div>
    </div>
  )
}

interface ParameterSelectionChoiceGroupProps {
  definition: ParameterChoiceDefinition
  disabledValues?: string[]
  onSelect: (value: string) => void
  selectedValue: string
}

function ParameterSelectionChoiceGroup({
  definition,
  disabledValues = [],
  onSelect,
  selectedValue,
}: ParameterSelectionChoiceGroupProps) {
  const disabledValueSet = new Set(disabledValues)
  return (
    <section className="parameter-selection-choice-group">
      <div>
        <h4>{definition.title}</h4>
        <p>{definition.description}</p>
      </div>
      <div className="parameter-choice-dialog-body">
        {definition.options.map(option => {
          const selected = option.value === selectedValue
          const disabled = disabledValueSet.has(option.value)
          return (
            <button
              type="button"
              className={`parameter-choice-option ${selected ? 'selected' : ''}`}
              disabled={disabled}
              aria-pressed={selected}
              key={option.value}
              onClick={() => onSelect(option.value)}
            >
              <span className="parameter-choice-option-heading">
                <strong>{option.label}</strong>
                <span className="cell-mono">{option.value}</span>
              </span>
              <span>{option.description}</span>
            </button>
          )
        })}
      </div>
    </section>
  )
}

function getParameterDataColumnIndexes(parameter: ParameterConfig, columns: string[]): number[] {
  const configuredColumn = normalizeParameterDataLookup(parameter.column)
  if (!configuredColumn) {
    return columns.length === 1 ? [0] : []
  }

  const exactColumnIndex = columns.findIndex(column => normalizeParameterDataLookup(column) === configuredColumn)
  if (exactColumnIndex >= 0) return [exactColumnIndex]

  return columns.length === 1 ? [0] : []
}

function stringifyFocusedParameterData(source: DataSourceConfig, parsed: { columns: string[]; rows: string[][] }, columnIndexes: number[]): string {
  const columns = columnIndexes.map(colIdx => parsed.columns[colIdx] || `col_${colIdx + 1}`)
  const rows = parsed.rows.map(row => columnIndexes.map(colIdx => row[colIdx] || ''))
  return stringifyInlineData(source, columns, rows)
}

function mergeFocusedParameterData(
  source: DataSourceConfig,
  parsed: { columns: string[]; rows: string[][] },
  columnIndexes: number[],
  rawValue: string,
): string {
  const viewSource: DataSourceConfig = {
    ...source,
    columns: source.first_row_header === false ? columnIndexes.map(colIdx => parsed.columns[colIdx] || `col_${colIdx + 1}`) : [],
    inline_data: rawValue,
  }
  const focused = parseInlineData(viewSource)
  const nextColumns = [...parsed.columns]
  if (source.first_row_header !== false) {
    columnIndexes.forEach((colIdx, position) => {
      const nextName = focused.columns[position]?.trim()
      if (nextName) nextColumns[colIdx] = nextName
    })
  }

  const rowCount = Math.max(parsed.rows.length, focused.rows.length)
  const nextRows = Array.from({ length: rowCount }, (_, rowIdx) => {
    const nextRow = [...(parsed.rows[rowIdx] || [])]
    while (nextRow.length < nextColumns.length) nextRow.push('')
    columnIndexes.forEach((colIdx, position) => {
      nextRow[colIdx] = focused.rows[rowIdx]?.[position] || ''
    })
    return nextRow.slice(0, nextColumns.length)
  })

  return stringifyInlineData(source, nextColumns, nextRows)
}

interface ParameterSourceMergeCandidate {
  columnIndex: number | null
  parameter: ParameterConfig
  parameterIndex: number
  parsed: { columns: string[]; rows: string[][] }
  source: DataSourceConfig
  sourceIndex: number
  targetColumn: string
}

interface ParameterSourceMergeSummary {
  canMerge: boolean
  parameterCount: number
  sourceCount: number
}

interface ParameterSourceMergeResult extends ParameterSourceMergeSummary {
  mergedSourceIndex: number
  mergedSourceName: string
  rowCount: number
  script: ScriptData
}

function isParameterSourceMergeEligible(source?: DataSourceConfig | null): source is DataSourceConfig {
  if (!source) return false
  const type = (source.type || '').trim().toLowerCase()
  const path = (source.path || source.name || '').trim().toLowerCase()
  return type === 'csv' || type === 'dat' || path.endsWith('.csv') || path.endsWith('.dat')
}

function uniqueParameterMergeColumnName(requested: string, used: Set<string>): string {
  const base = (requested || 'parameter').trim() || 'parameter'
  let candidate = base
  let suffix = 2
  while (used.has(normalizeParameterDataLookup(candidate))) {
    candidate = `${base}_${suffix}`
    suffix += 1
  }
  used.add(normalizeParameterDataLookup(candidate))
  return candidate
}

function uniqueMergedParameterSourceName(sources: DataSourceConfig[]): string {
  const used = new Set(sources.map(source => normalizeParameterDataLookup(source.name)))
  const base = 'merged_parameters'
  let candidate = base
  let suffix = 2
  while (used.has(normalizeParameterDataLookup(candidate))) {
    candidate = `${base}_${suffix}`
    suffix += 1
  }
  return candidate
}

function collectParameterSourceMergeCandidates(script: ScriptData): ParameterSourceMergeCandidate[] {
  const sources = script.data_sources || []
  const sourceByName = new Map<string, { index: number; source: DataSourceConfig }>()
  sources.forEach((source, index) => {
    const key = normalizeParameterDataLookup(source.name)
    if (key) sourceByName.set(key, { index, source })
  })

  const usedColumns = new Set<string>()
  return (script.parameters || []).flatMap((parameter, parameterIndex) => {
    if (parameter.type !== 'file' && parameter.type !== 'list') return []
    const sourceMatch = sourceByName.get(normalizeParameterDataLookup(parameter.data_source))
    if (!sourceMatch || !isParameterSourceMergeEligible(sourceMatch.source)) return []

    const parsed = parseInlineData(sourceMatch.source)
    const columnIndexes = getParameterDataColumnIndexes(parameter, parsed.columns)
    const columnIndex = columnIndexes[0] ?? null
    const sourceColumnName = columnIndex !== null ? parsed.columns[columnIndex] : ''
    const targetColumn = uniqueParameterMergeColumnName(
      parameter.name || parameter.column || sourceColumnName || `parameter_${parameterIndex + 1}`,
      usedColumns,
    )

    return [{
      columnIndex,
      parameter,
      parameterIndex,
      parsed,
      source: sourceMatch.source,
      sourceIndex: sourceMatch.index,
      targetColumn,
    }]
  })
}

function summarizeParameterSourceMerge(script: ScriptData): ParameterSourceMergeSummary {
  const candidates = collectParameterSourceMergeCandidates(script)
  const sourceCount = new Set(candidates.map(candidate => candidate.sourceIndex)).size
  return {
    canMerge: sourceCount > 1 && candidates.length > 0,
    parameterCount: candidates.length,
    sourceCount,
  }
}

function buildMergedParameterSourceScript(script: ScriptData): ParameterSourceMergeResult | null {
  const candidates = collectParameterSourceMergeCandidates(script)
  const sourceIndexes = new Set(candidates.map(candidate => candidate.sourceIndex))
  if (sourceIndexes.size <= 1 || candidates.length === 0) return null

  const sources = script.data_sources || []
  const parameters = script.parameters || []
  const mergedSourceName = uniqueMergedParameterSourceName(sources)
  const targetSourceShell: DataSourceConfig = {
    ...EMPTY_DATA_SOURCE,
    name: mergedSourceName,
    type: 'csv',
    path: `${mergedSourceName}.csv`,
    strategy: 'sequential',
    delimiter: 'comma',
    first_row_header: true,
    columns: [],
  }
  const columns = candidates.map(candidate => candidate.targetColumn)
  const rowCount = candidates.reduce((max, candidate) => Math.max(max, candidate.parsed.rows.length), 0)
  const rows = Array.from({ length: rowCount }, (_, rowIdx) =>
    candidates.map(candidate => {
      if (candidate.columnIndex === null) return ''
      return candidate.parsed.rows[rowIdx]?.[candidate.columnIndex] || ''
    }),
  )
  const mergedSource: DataSourceConfig = {
    ...targetSourceShell,
    inline_data: stringifyInlineData(targetSourceShell, columns, rows),
  }
  const targetColumnByParameterIndex = new Map(candidates.map(candidate => [candidate.parameterIndex, candidate.targetColumn]))
  const nextParameters = parameters.map((parameter, index) => {
    const targetColumn = targetColumnByParameterIndex.get(index)
    if (!targetColumn) return parameter
    return {
      ...parameter,
      data_source: mergedSourceName,
      column: targetColumn,
    }
  })
  const nextSources = sources.filter((_, index) => !sourceIndexes.has(index))
  const mergedSourceIndex = nextSources.length
  nextSources.push(mergedSource)

  return {
    canMerge: true,
    mergedSourceIndex,
    mergedSourceName,
    parameterCount: candidates.length,
    rowCount,
    script: {
      ...script,
      data_sources: nextSources,
      parameters: nextParameters,
    },
    sourceCount: sourceIndexes.size,
  }
}

interface ParameterDataSourcesSectionProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  isOpen: boolean
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onToggleOpen: () => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  selectedParameter: ParameterConfig | null
  script: ScriptData
}

export function ParameterDataSourcesSection({
  dataEditorMode,
  dataSourceHistory,
  isOpen,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onSortDataRows,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onToggleOpen,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  selectedParameter,
  script,
}: ParameterDataSourcesSectionProps) {
  const selectedParameterName = selectedParameter?.name || 'Selected parameter'
  const selectedSourceIndex = selectedParameter
    ? findDataSourceIndexByName(script.data_sources, selectedParameter.data_source)
    : null
  const selectedSource = selectedSourceIndex !== null ? script.data_sources?.[selectedSourceIndex] || null : null
  const selectedParsed = selectedSource ? parseInlineData(selectedSource) : null
  const selectedSourceParameters = selectedParameter && selectedSource
    ? (script.parameters || []).filter(parameter =>
      (parameter.type === 'file' || parameter.type === 'list')
      && normalizeParameterDataLookup(parameter.data_source) === normalizeParameterDataLookup(selectedSource.name),
    )
    : []
  const selectedDataParameters = selectedSourceParameters.length > 0
    ? selectedSourceParameters
    : selectedParameter
      ? [selectedParameter]
      : []
  const selectedParameterNames = selectedDataParameters.map((parameter, idx) => parameter.name || `Parameter ${idx + 1}`)
  const selectedColumnIndexes = selectedParsed
    ? Array.from(new Set(selectedDataParameters.flatMap(parameter => getParameterDataColumnIndexes(parameter, selectedParsed.columns))))
    : []
  const selectedSourceSummary = selectedSource ? summarizeInlineData(selectedSource) : null
  const selectedSourceWarnings = selectedSource ? validateInlineData(selectedSource) : []
  const selectedParameterSummary = selectedParameterNames.length > 1
    ? `${selectedParameterNames.length} params: ${selectedParameterNames.join(', ')}`
    : selectedParameterName

  const renderSelectedParameterData = () => {
    if (!selectedParameter) {
      return (
        <div className="parameter-empty-state">
          Select a parameter in the Parameters table above to view its mapped data here.
        </div>
      )
    }

    if (selectedParameter.type !== 'file' && selectedParameter.type !== 'list') {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is generated at runtime and does not use parameter data.
        </div>
      )
    }

    if (!selectedParameter.data_source?.trim()) {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is not mapped to a data source. Open Settings in the Parameters table to choose a data source and column.
        </div>
      )
    }

    if (!selectedSource || selectedSourceIndex === null || !selectedParsed || !selectedSourceSummary) {
      return (
        <div className="parameter-empty-state">
          The mapped data source for {selectedParameterName} could not be found.
        </div>
      )
    }

    if (selectedColumnIndexes.length === 0) {
      return (
        <div className="parameter-empty-state">
          {selectedParameterName} is mapped to {selectedParameter.data_source}, but no matching parameter columns are selected. Open Settings in the Parameters table to choose the column.
        </div>
      )
    }

    return (
      <div className="parameter-table-wrap parameter-data-selected-view">
        <table className="parameter-table">
          <thead>
            <tr>
              <th>Parameter</th>
              <th>Source</th>
              <th>Type</th>
              <th>Strategy</th>
              <th>Header Row</th>
              <th>Rows</th>
              <th>Column</th>
              <th>Delimiter</th>
              <th>Data File Path</th>
            </tr>
          </thead>
          <tbody>
            <ParameterDataSourceRows
              dataEditorMode={dataEditorMode[selectedSourceIndex] || 'grid'}
              focusedColumnIndexes={selectedColumnIndexes}
              history={dataSourceHistory[selectedSourceIndex]}
              index={selectedSourceIndex}
              isEditorOpen
              key={`${selectedSource.name || 'source'}-${selectedSourceIndex}-${selectedParameterName}`}
              onAddDataColumn={onAddDataColumn}
              onAddDataRow={onAddDataRow}
              onApplyDedupeHeaders={onApplyDedupeHeaders}
              onApplyFillHeaders={onApplyFillHeaders}
              onApplyNormalizeRows={onApplyNormalizeRows}
              onAutoFixDataSource={onAutoFixDataSource}
              onExportDataSourceFile={onExportDataSourceFile}
              onImportDataSourceFile={onImportDataSourceFile}
              onRedoDataSource={onRedoDataSource}
              onRemoveDataColumn={onRemoveDataColumn}
              onRemoveDataRow={onRemoveDataRow}
              onRemoveDataSource={onRemoveDataSource}
              onSortDataRows={onSortDataRows}
              onToggleDataEditorMode={onToggleDataEditorMode}
              onToggleOpenDataEditor={onToggleOpenDataEditor}
              onUndoDataSource={onUndoDataSource}
              onUpdateDataCell={onUpdateDataCell}
              onUpdateDataColumnName={onUpdateDataColumnName}
              onUpdateDataSource={onUpdateDataSource}
              parsed={selectedParsed}
              selectedParameterName={selectedParameterName}
              selectedParameterNames={selectedParameterNames}
              source={selectedSource}
              summary={selectedSourceSummary}
              warnings={selectedSourceWarnings}
            />
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <>
      <div className="panel-title-row parameter-section-header">
        <button
          type="button"
          className="parameter-section-toggle"
          aria-expanded={isOpen}
          aria-label={`${isOpen ? 'Collapse' : 'Expand'} Parameter Data`}
          onClick={onToggleOpen}
        >
          <span aria-hidden="true" className="parameter-section-chevron">{isOpen ? '▾' : '▸'}</span>
          <span>
            <strong style={{ display: 'block', marginBottom: 4 }}>Parameter Data</strong>
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Shows only the mapped data for the parameter selected above.</span>
          </span>
        </button>
        {selectedParameter && (
          <span className="parameter-data-focus-pill">
            {selectedParameterSummary}
          </span>
        )}
      </div>

      {isOpen && renderSelectedParameterData()}
    </>
  )
}

interface ParameterDataSourceRowsProps {
  dataEditorMode: 'grid' | 'raw'
  focusedColumnIndexes?: number[]
  history?: DataSourceHistory
  index: number
  isEditorOpen: boolean
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  parsed: { columns: string[]; rows: string[][] }
  selectedParameterName?: string
  selectedParameterNames?: string[]
  source: DataSourceConfig
  summary: { rows: number; columns: number }
  warnings: string[]
}

function ParameterDataSourceRows({
  dataEditorMode,
  focusedColumnIndexes,
  history,
  index,
  isEditorOpen,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onSortDataRows,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  parsed,
  selectedParameterName,
  selectedParameterNames,
  source,
  summary,
  warnings,
}: ParameterDataSourceRowsProps) {
  const delimiterChoice = getDataSourceDelimiterChoice(source)
  const sourceLabel = source.name || `Source ${index + 1}`
  const isFocusedParameterView = Boolean(focusedColumnIndexes?.length)
  const focusedParameterLabel = selectedParameterNames?.length
    ? selectedParameterNames.join(', ')
    : selectedParameterName || 'Selected parameter'
  const visibleColumnIndexes = isFocusedParameterView
    ? (focusedColumnIndexes || []).filter(colIdx => colIdx >= 0 && colIdx < parsed.columns.length)
    : parsed.columns.map((_, colIdx) => colIdx)
  const updateDelimiterChoice = (choice: DataSourceDelimiterChoice) => {
    onUpdateDataSource(index, 'delimiter', getDataSourceDelimiterValue(choice), true)
  }

  return (
    <>
      <tr>
        <td>
          {isFocusedParameterView ? (
            <span className="cell-mono">{focusedParameterLabel}</span>
          ) : (
            <input
              value={source.name || ''}
              onChange={e => onUpdateDataSource(index, 'name', e.target.value)}
              placeholder="users"
            />
          )}
        </td>
        {isFocusedParameterView && (
          <td>
            <span className="cell-mono">{sourceLabel}</span>
          </td>
        )}
        <td>
          <select value={source.type || 'csv'} onChange={e => onUpdateDataSource(index, 'type', e.target.value)}>
            <option value="csv">csv</option>
            <option value="dat">dat</option>
          </select>
        </td>
        <td>
          <select value={source.strategy || 'sequential'} onChange={e => onUpdateDataSource(index, 'strategy', e.target.value)}>
            <option value="sequential">sequential</option>
            <option value="random">random</option>
            <option value="unique">unique</option>
          </select>
        </td>
        <td>
          <label className="checkbox-label compact">
            <input
              type="checkbox"
              checked={source.first_row_header !== false}
              onChange={e => onUpdateDataSource(index, 'first_row_header', e.target.checked)}
            />
            <span>{source.first_row_header !== false ? 'Yes' : 'No'}</span>
          </label>
        </td>
        <td className="cell-mono">{summary.rows || '—'}</td>
        <td>
          {isFocusedParameterView ? (
            <span className="cell-mono">
              {visibleColumnIndexes.map(colIdx => formatDataSourceColumnLabel(parsed.columns[colIdx], colIdx)).join(', ') || '—'}
            </span>
          ) : (
            <input
              value={(source.columns || []).join(', ')}
              onChange={e => onUpdateDataSource(
                index,
                'columns',
                e.target.value.split(',').map(v => v.trim()).filter(Boolean),
              )}
              placeholder="username, password"
            />
          )}
        </td>
        <td>
          <select
            aria-label={`Delimiter for ${sourceLabel}`}
            value={delimiterChoice}
            onChange={e => updateDelimiterChoice(e.target.value as DataSourceDelimiterChoice)}
          >
            <option value="auto">Auto ({inferDelimiter(source) === '\t' ? 'Tab' : 'Comma'})</option>
            <option value="comma">Comma</option>
            <option value="tab">Tab</option>
          </select>
        </td>
        <td>
          <input
            value={source.path || ''}
            onChange={e => onUpdateDataSource(index, 'path', e.target.value)}
            placeholder="/absolute/path/to/data.csv (optional when inline data is used)"
          />
        </td>
        {!isFocusedParameterView && (
          <td style={{ textAlign: 'right' }}>
            <div className="parameter-actions">
              <button className="btn btn-sm btn-ghost" onClick={() => onToggleOpenDataEditor(index)}>
                {isEditorOpen ? 'Hide Data' : 'Edit Data'}
              </button>
              <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => onRemoveDataSource(index)}>Remove</button>
            </div>
          </td>
        )}
      </tr>
      {isEditorOpen && (
        <tr className="parameter-editor-row">
          <td colSpan={9}>
            <ParameterDataEditor
              dataEditorMode={dataEditorMode}
              focusedColumnIndexes={focusedColumnIndexes}
              history={history}
              index={index}
              onAddDataColumn={onAddDataColumn}
              onAddDataRow={onAddDataRow}
              onApplyDedupeHeaders={onApplyDedupeHeaders}
              onApplyFillHeaders={onApplyFillHeaders}
              onApplyNormalizeRows={onApplyNormalizeRows}
              onAutoFixDataSource={onAutoFixDataSource}
              onExportDataSourceFile={onExportDataSourceFile}
              onImportDataSourceFile={onImportDataSourceFile}
              onRedoDataSource={onRedoDataSource}
              onRemoveDataColumn={onRemoveDataColumn}
              onRemoveDataRow={onRemoveDataRow}
              onSortDataRows={onSortDataRows}
              onToggleDataEditorMode={onToggleDataEditorMode}
              onUndoDataSource={onUndoDataSource}
              onUpdateDataCell={onUpdateDataCell}
              onUpdateDataColumnName={onUpdateDataColumnName}
              onUpdateDataSource={onUpdateDataSource}
              parsed={parsed}
              selectedParameterName={selectedParameterName}
              selectedParameterNames={selectedParameterNames}
              source={source}
              summary={summary}
              warnings={warnings}
            />
          </td>
        </tr>
      )}
    </>
  )
}

interface ParameterDataEditorProps {
  dataEditorMode: 'grid' | 'raw'
  focusedColumnIndexes?: number[]
  history?: DataSourceHistory
  index: number
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  parsed: { columns: string[]; rows: string[][] }
  selectedParameterName?: string
  selectedParameterNames?: string[]
  showHeaderTitle?: boolean
  source: DataSourceConfig
  summary: { rows: number; columns: number }
  warnings: string[]
}

function ParameterDataEditor({
  dataEditorMode,
  focusedColumnIndexes,
  history,
  index,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onSortDataRows,
  onToggleDataEditorMode,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  parsed,
  selectedParameterName,
  selectedParameterNames,
  showHeaderTitle = true,
  source,
  summary,
  warnings,
}: ParameterDataEditorProps) {
  const [sortState, setSortState] = useState<{ column: number; direction: DataSourceSortDirection } | null>(null)
  const [filterText, setFilterText] = useState('')
  const [filterColumn, setFilterColumn] = useState<number | 'all'>('all')
  const sourceLabel = source.name || `Source ${index + 1}`
  const isFocusedParameterView = Boolean(focusedColumnIndexes?.length)
  const focusedParameterLabel = selectedParameterNames?.length
    ? selectedParameterNames.join(', ')
    : selectedParameterName || 'Selected parameter'
  const focusedDataTitle = selectedParameterNames && selectedParameterNames.length > 1
    ? `${sourceLabel} Parameter Data`
    : `${focusedParameterLabel} Data`
  const visibleColumnIndexes = isFocusedParameterView
    ? (focusedColumnIndexes || []).filter(colIdx => colIdx >= 0 && colIdx < parsed.columns.length)
    : parsed.columns.map((_, colIdx) => colIdx)
  const effectiveFilterColumn = filterColumn === 'all' || visibleColumnIndexes.includes(filterColumn) ? filterColumn : 'all'
  const filteredRows = parsed.rows
    .map((row, rowIdx) => ({ row, rowIdx }))
    .filter(({ row }) => dataSourceRowMatchesFilter(row, filterText, effectiveFilterColumn))
  const focusedRawData = isFocusedParameterView
    ? stringifyFocusedParameterData(source, parsed, visibleColumnIndexes)
    : source.inline_data || ''
  const updateRawData = (value: string) => {
    onUpdateDataSource(
      index,
      'inline_data',
      isFocusedParameterView ? mergeFocusedParameterData(source, parsed, visibleColumnIndexes, value) : value,
      true,
    )
  }
  const sortColumn = (colIdx: number) => {
    const nextDirection: DataSourceSortDirection = sortState?.column === colIdx && sortState.direction === 'asc'
      ? 'desc'
      : 'asc'
    setSortState({ column: colIdx, direction: nextDirection })
    onSortDataRows(index, colIdx, nextDirection)
  }

  return (
    <div className="parameter-editor">
      <div className="parameter-editor-header">
        {showHeaderTitle && <strong>{isFocusedParameterView ? focusedDataTitle : sourceLabel}</strong>}
        <div className="parameter-editor-meta">
          <span className="cell-mono">
            {summary.rows} rows · {visibleColumnIndexes.length || summary.columns} {isFocusedParameterView ? 'selected ' : ''}column{(visibleColumnIndexes.length || summary.columns) === 1 ? '' : 's'} · {formatDataSourceDelimiterLabel(source)}
          </span>
          <div className="parameter-editor-controls">
            <button className="btn btn-sm btn-ghost" disabled={!history?.past?.length} onClick={() => onUndoDataSource(index)}>Undo</button>
            <button className="btn btn-sm btn-ghost" disabled={!history?.future?.length} onClick={() => onRedoDataSource(index)}>Redo</button>
            <button className="btn btn-sm btn-ghost" onClick={() => onToggleDataEditorMode(index)}>
              {dataEditorMode === 'grid' ? 'Raw Mode' : 'Grid Mode'}
            </button>
            <label className="btn btn-sm btn-ghost file-btn">
              Import
              <input
                type="file"
                accept=".csv,.dat,.txt"
                onChange={e => {
                  const file = e.target.files?.[0] || null
                  onImportDataSourceFile(index, file)
                  e.currentTarget.value = ''
                }}
              />
            </label>
            {!isFocusedParameterView && (
              <button className="btn btn-sm btn-ghost" onClick={() => onExportDataSourceFile(source, index)}>Export</button>
            )}
          </div>
        </div>
      </div>
      {warnings.length > 0 && (
        <div className="parameter-validation">
          <div className="parameter-validation-header">
            <strong>Data Warnings</strong>
            <div className="parameter-validation-actions">
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyFillHeaders(index)}>Fill Headers</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyDedupeHeaders(index)}>Deduplicate</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onApplyNormalizeRows(index)}>Normalize Rows</button>
              <button className="btn btn-sm btn-ghost" onClick={() => onAutoFixDataSource(index)}>Auto-Fix All</button>
            </div>
          </div>
          {warnings.map((warning, warningIdx) => (
            <div key={`${index}-warn-${warningIdx}`} className="parameter-validation-item">{warning}</div>
          ))}
        </div>
      )}
      {dataEditorMode === 'grid' ? (
        <div className="parameter-grid-wrap">
          <div className="parameter-grid-actions">
            <div className="parameter-grid-action-buttons">
              <button className="btn btn-sm btn-ghost" onClick={() => onAddDataRow(index)}>+ Row</button>
              {!isFocusedParameterView && (
                <button className="btn btn-sm btn-ghost" onClick={() => onAddDataColumn(index)}>+ Column</button>
              )}
            </div>
            <div className="parameter-grid-filters">
              <label>
                <span>Search</span>
                <input
                  aria-label={`Search rows for ${sourceLabel}`}
                  value={filterText}
                  onChange={event => setFilterText(event.target.value)}
                  placeholder="Find row values"
                />
              </label>
              <label>
                <span>Column</span>
                <select
                  aria-label={`Filter column for ${sourceLabel}`}
                  value={effectiveFilterColumn === 'all' ? 'all' : String(effectiveFilterColumn)}
                  onChange={event => setFilterColumn(event.target.value === 'all' ? 'all' : Number(event.target.value))}
                >
                  <option value="all">All columns</option>
                  {visibleColumnIndexes.map((colIdx) => {
                    const column = parsed.columns[colIdx]
                    return (
                    <option key={`${index}-filter-${colIdx}`} value={colIdx}>
                      {formatDataSourceColumnLabel(column, colIdx)}
                    </option>
                    )
                  })}
                </select>
              </label>
              <span className="parameter-grid-filter-count">
                {filteredRows.length}/{parsed.rows.length} rows
              </span>
              {filterText && (
                <button className="btn btn-sm btn-ghost" onClick={() => setFilterText('')}>Clear</button>
              )}
            </div>
          </div>
          <div className="parameter-grid-scroll">
            <table className="parameter-grid">
              <thead>
                <tr>
                  <th className="parameter-grid-row-action" aria-label="Row actions" />
                  <th>#</th>
                  {visibleColumnIndexes.map((colIdx) => {
                    const column = parsed.columns[colIdx]
                    const isSortedColumn = sortState?.column === colIdx
                    const columnLabel = formatDataSourceColumnLabel(column, colIdx)
                    const nextSortDirection: DataSourceSortDirection = isSortedColumn && sortState?.direction === 'asc' ? 'desc' : 'asc'
                    return (
                      <th
                        key={`${index}-h-${colIdx}`}
                        aria-sort={isSortedColumn ? (sortState.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
                      >
                        <div className="parameter-grid-head">
                          {isFocusedParameterView ? (
                            <span>{columnLabel}</span>
                          ) : (
                            <input
                              value={column}
                              onChange={e => onUpdateDataColumnName(index, colIdx, e.target.value)}
                              placeholder={`col_${colIdx + 1}`}
                            />
                          )}
                          <div className="parameter-grid-head-actions">
                            <button
                              className={`btn-icon parameter-grid-sort ${isSortedColumn ? 'active' : ''}`}
                              onClick={() => sortColumn(colIdx)}
                              title={`Sort ${columnLabel} ${nextSortDirection === 'asc' ? 'ascending' : 'descending'}`}
                              aria-label={`Sort ${columnLabel} ${nextSortDirection === 'asc' ? 'ascending' : 'descending'}`}
                            >
                              {isSortedColumn ? (sortState.direction === 'asc' ? '↑' : '↓') : '↕'}
                            </button>
                            {!isFocusedParameterView && (
                              <button className="btn-icon danger" onClick={() => onRemoveDataColumn(index, colIdx)} title="Remove column">✕</button>
                            )}
                          </div>
                        </div>
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {filteredRows.map(({ row, rowIdx }) => (
                  <tr key={`${index}-r-${rowIdx}`}>
                    <td className="parameter-grid-row-action">
                      <button
                        className="btn-icon danger"
                        onClick={() => onRemoveDataRow(index, rowIdx)}
                        title="Remove row"
                        aria-label={`Remove row ${rowIdx + 1}`}
                      >
                        ✕
                      </button>
                    </td>
                    <td className="parameter-grid-index">
                      {rowIdx + 1}
                    </td>
                    {visibleColumnIndexes.map((colIdx) => (
                      <td key={`${index}-c-${rowIdx}-${colIdx}`}>
                        <input
                          value={row[colIdx] || ''}
                          onChange={e => onUpdateDataCell(index, rowIdx, colIdx, e.target.value)}
                        />
                      </td>
                    ))}
                  </tr>
                ))}
                {parsed.rows.length === 0 && (
                  <tr>
                    <td colSpan={visibleColumnIndexes.length + 2} className="parameter-grid-empty">
                      No rows yet. Use + Row to add parameter records.
                    </td>
                  </tr>
                )}
                {parsed.rows.length > 0 && filteredRows.length === 0 && (
                  <tr>
                    <td colSpan={visibleColumnIndexes.length + 2} className="parameter-grid-empty">
                      No rows match the current filter.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <textarea
          aria-label={`Raw data for ${sourceLabel}`}
          value={focusedRawData}
          onChange={e => updateRawData(e.target.value)}
          placeholder={source.first_row_header !== false ? 'username,password\nalice,pass1\nbob,pass2' : 'alice,pass1\nbob,pass2'}
          spellCheck={false}
        />
      )}
    </div>
  )
}

/* Sub-component for editing a single action's details */

/**
 * Snapshot a script for the import draft-diff baseline. The diff only compares
 * config/actions/parameters/data_sources; preserved import metadata can be several MB for
 * large archives and is immutable, so deep-clone only the editable body and share the
 * metadata by reference rather than serializing the whole ~3MB object on every open.
 */
function cloneScriptForImportBaseline(source: ScriptData): ScriptData {
  const { metadata, ...body } = source
  const baseline = structuredClone(body) as ScriptData
  if (metadata !== undefined) baseline.metadata = metadata
  return baseline
}

function useScriptEditorState(initialScript?: ScriptData | null) {
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const baselineJsonRef = useRef(formatScriptJson(initialScript || { ...EMPTY_SCRIPT }))
  const [importBaselineScript, setImportBaselineScript] = useState<ScriptData | null>(
    initialScript?.metadata?.import ? cloneScriptForImportBaseline(initialScript) : null,
  )
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [warnings, setWarnings] = useState<string[]>([])
  const [jsonDirty, setJsonDirty] = useState(false)
  const [jsonText, setJsonText] = useState(() => formatScriptJson(initialScript || { ...EMPTY_SCRIPT }))
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    config: false,
    actions: true,
    runtime: false,
    import: scriptHasImportMetadata(initialScript),
    replay: false,
  })
  const [isParameterDialogOpen, setIsParameterDialogOpen] = useState(false)
  const [pendingParameterDraft, setPendingParameterDraft] = useState<ActionEditorParameterDraft | null>(null)
  const [openDataEditor, setOpenDataEditor] = useState<number | null>(null)
  const [dataEditorMode, setDataEditorMode] = useState<Record<number, 'grid' | 'raw'>>({})
  const [dataSourceHistory, setDataSourceHistory] = useState<Record<number, DataSourceHistory>>({})
  const [parameterCandidates, setParameterCandidates] = useState<ParameterCandidate[]>([])
  const [replaying, setReplaying] = useState(false)
  const [replayReport, setReplayReport] = useState<ReplayReport | null>(null)
  const [expandedReplayEvent, setExpandedReplayEvent] = useState<number | null>(null)
  const [expandedReplayBrowserArtifacts, setExpandedReplayBrowserArtifacts] = useState<Record<string, boolean>>({})
  const [copiedReplaySnapshotSection, setCopiedReplaySnapshotSection] = useState<string | null>(null)
  const [activeReplaySessionId, setActiveReplaySessionId] = useState<string | null>(null)
  const [browserReplaySession, setBrowserReplaySession] = useState<ScriptDebugSession | null>(null)
  const [replayScreenshotSelection, setReplayScreenshotSelection] = useState<ReplayScreenshotSelection | null>(null)
  const [replayActivity, setReplayActivity] = useState<ReplayActivityItem[]>([])
  const [replaySeedVariables, setReplaySeedVariables] = useState<Record<string, string>>({})
  const replayEventRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const [replayOptions, setReplayOptions] = useState<ReplayOptionsState>({
    groupName: '',
    ignoreThinkTime: true,
    startPaused: false,
    breakOnFailure: false,
    logLevel: 'info',
    variablesText: '{\n  \n}',
  })

  const toggleSection = (id: string) => setOpenSections(prev => ({ ...prev, [id]: !prev[id] }))

  const hydrateEditorState = useCallback((nextScript: ScriptData) => {
    setScript(nextScript)
    baselineJsonRef.current = formatScriptJson(nextScript)
    setImportBaselineScript(nextScript.metadata?.import ? cloneScriptForImportBaseline(nextScript) : null)
    if (scriptHasImportMetadata(nextScript)) {
      setOpenSections(prev => ({ ...prev, import: true }))
    }
    setJsonText(formatScriptJson(nextScript))
    setJsonDirty(false)
    setActiveAction(null)
    setIsParameterDialogOpen(false)
    setPendingParameterDraft(null)
    setOpenDataEditor(null)
    setDataEditorMode({})
    setDataSourceHistory({})
    setParameterCandidates([])
    setError('')
    setSuccess('')
    setWarnings([])
    setExpandedReplayBrowserArtifacts({})
    setReplayScreenshotSelection(null)
  }, [])

  const markBaseline = useCallback((nextScript: ScriptData) => {
    baselineJsonRef.current = formatScriptJson(nextScript)
  }, [])

  const closeParameterDialog = () => {
    setIsParameterDialogOpen(false)
    setPendingParameterDraft(null)
  }

  const closeReplayScreenshot = () => {
    setReplayScreenshotSelection(null)
  }

  return {
    activeAction,
    activeReplaySessionId,
    baselineJsonRef,
    browserReplaySession,
    closeParameterDialog,
    copiedReplaySnapshotSection,
    dataEditorMode,
    dataSourceHistory,
    error,
    expandedReplayBrowserArtifacts,
    expandedReplayEvent,
    hydrateEditorState,
    importBaselineScript,
    isParameterDialogOpen,
    jsonDirty,
    jsonText,
    markBaseline,
    openDataEditor,
    openSections,
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
    setActiveAction,
    setActiveReplaySessionId,
    setBrowserReplaySession,
    setCopiedReplaySnapshotSection,
    setDataEditorMode,
    setDataSourceHistory,
    setError,
    setExpandedReplayBrowserArtifacts,
    setExpandedReplayEvent,
    setIsParameterDialogOpen,
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
    toggleSection,
    validating,
    warnings,
    closeReplayScreenshot,
  }
}

function useJsonParameterMenu({
  parameters,
  onBeginParameterEdit,
  onChooseExistingParameter,
  onReviewOccurrences,
}: {
  parameters: ParameterConfig[]
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  onChooseExistingParameter: (draft: ActionEditorParameterDraft) => void
  onReviewOccurrences: (draft: ActionEditorParameterDraft) => void
}) {
  const [selectionState, setSelectionState] = useState<ActionEditorSelectionState | null>(null)

  useEffect(() => {
    if (!selectionState) return
    const closeMenu = () => setSelectionState(null)
    window.addEventListener('click', closeMenu)
    return () => window.removeEventListener('click', closeMenu)
  }, [selectionState])

  const detectSelectedParameter = useCallback((selectedText: string): ParameterConfig | null => {
    const match = selectedText.trim().match(/^{{\s*param\.([a-zA-Z0-9_]+)\s*}}$/)
    if (!match) return null
    const normalizedName = sanitizeVarName(match[1])
    return parameters.find(parameter => sanitizeVarName(parameter.name || '') === normalizedName) || null
  }, [parameters])

  const openSelectionMenu = useCallback((selection: JsonPaneSelectionRequest) => {
    if (!selection.selectedText.trim()) {
      setSelectionState(null)
      return
    }
    const existingParameter = detectSelectedParameter(selection.selectedText)
    setSelectionState({
      start: selection.start,
      end: selection.end,
      selectedText: selection.selectedText,
      x: selection.x,
      y: selection.y,
      existingName: existingParameter?.name || null,
    })
  }, [detectSelectedParameter])

  const buildDraft = useCallback((nextExistingName?: string | null): ActionEditorParameterDraft | null => {
    if (!selectionState) return null
    const existingParameter = nextExistingName
      ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
      : detectSelectedParameter(selectionState.selectedText)

    const requestedName = existingParameter?.name
      || sanitizeVarName(selectionState.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
    const seedValue = existingParameter?.values?.[0] || selectionState.selectedText

    return {
      source: 'json',
      actionIndex: -1,
      start: selectionState.start,
      end: selectionState.end,
      selectedText: selectionState.selectedText,
      existingName: existingParameter?.name || null,
      requestedName,
      seedValue,
    }
  }, [detectSelectedParameter, parameters, selectionState])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    const draft = buildDraft(nextExistingName)
    if (!draft) return
    setSelectionState(null)
    onBeginParameterEdit(draft)
  }, [buildDraft, onBeginParameterEdit])

  const openExistingParameterChooser = useCallback(() => {
    const draft = buildDraft(null)
    if (!draft) return
    setSelectionState(null)
    onChooseExistingParameter(draft)
  }, [buildDraft, onChooseExistingParameter])

  const openOccurrenceReview = useCallback(() => {
    const draft = buildDraft(null)
    if (!draft) return
    setSelectionState(null)
    onReviewOccurrences(draft)
  }, [buildDraft, onReviewOccurrences])

  return {
    openExistingParameterChooser,
    openOccurrenceReview,
    openParameterEditor,
    openSelectionMenu,
    selectionState,
  }
}
