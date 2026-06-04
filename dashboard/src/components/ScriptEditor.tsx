import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react'
import { useApi } from '../hooks/useApi'
import type { BrowserToHTTPPreviewResponse } from '../hooks/api/useScriptApi'
import { useWebSocket } from '../hooks/useWebSocket'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import {
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
  BrowserReplayReport,
  ScriptDebugBreakpoint,
  ScriptDebugAction,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
  BrowserReplaySnapshot,
  type ProtocolEvidence,
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
  type ScriptDataSource,
  type ScriptImportMetadata,
  type LoadRunnerSourceReview,
  type ScriptParameterDefinition,
  type ScriptValidationResponse,
  type ReplayPreflight,
} from '../types/scripts'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import ReplayPreflightCard, { replayPreflightStatusLabel } from './ReplayPreflightCard'
import { buildLocalReplayPreflight } from './replayPreflightUtils'
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
  groupIssuesByAction,
  summarizeDataSources,
  summarizeParameters,
  type AuthoringIssue,
  type MigrationAssistantSuggestion,
  type ScriptRepairSuggestion,
} from './scriptAuthoringAssist'
import {
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { createZipBlob, downloadBlob, type ZipFileEntry } from '../utils/fileUtils'
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
  inferDelimiter,
  isLikelyParameterValue,
  mergeReplayVariables,
  normalizeHeaders,
  parseReplayVariablesText,
  parseInlineData,
  sanitizeVarName,
  stringifyInlineData,
  summarizeInlineData,
  validateInlineData,
} from './scriptEditorUtils'
import './ScriptEditor.css'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition
type ScriptData = ScriptDefinition
type ValidationEvidence = Pick<ScriptValidationResponse, 'data_source_fit' | 'ai_guardrails' | 'authoring_health'>

type ReplaySnapshot = BrowserReplaySnapshot
type ReplayActionEvent = BrowserReplayEvent
type ReplayReport = BrowserReplayReport
type WorkspaceTab = 'author' | 'debug' | 'json'
type DebuggerTab = 'request' | 'browser'
type DebuggerBottomTab = 'console' | 'timeline' | 'problems' | 'output'
type DebuggerResizeKind = 'rail' | 'vars' | 'bottom'

type BrowserEngineName = 'chromium' | 'webkit'

type BrowserDeviceProfileOption = {
  name: string
  engine: BrowserEngineName
}

const BROWSER_DEVICE_PROFILES: readonly BrowserDeviceProfileOption[] = [
  { name: 'desktop_1080p', engine: 'chromium' },
  { name: 'galaxy_s24', engine: 'chromium' },
  { name: 'ipad_air', engine: 'webkit' },
  { name: 'iphone_15_pro', engine: 'webkit' },
  { name: 'iphone_se', engine: 'webkit' },
  { name: 'pixel_8', engine: 'chromium' },
]

const AUTHORING_COMPONENT_GROUPS = [
  { kind: 'request_group', label: 'Request Groups' },
  { kind: 'auth_flow', label: 'Auth Flows' },
  { kind: 'login_transaction', label: 'Login Transactions' },
  { kind: 'data_pool', label: 'Data Pools' },
  { kind: 'teardown', label: 'Teardown Blocks' },
] as const

const WEBKIT_INSTALL_ADVISORY = 'WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.'
const DEBUG_LOG_LEVELS: ScriptDebugLogLevel[] = ['error', 'warn', 'info', 'debug', 'trace']
const DEBUGGER_LAYOUT_DEFAULTS: Record<DebuggerResizeKind, number> = {
  rail: 260,
  vars: 320,
  bottom: 220,
}
const DEBUGGER_LAYOUT_LIMITS: Record<DebuggerResizeKind, { max: number; min: number; step: number }> = {
  rail: { min: 180, max: 560, step: 24 },
  vars: { min: 220, max: 600, step: 24 },
  bottom: { min: 140, max: 640, step: 24 },
}

function clampDebuggerPaneSize(kind: DebuggerResizeKind, value: number) {
  const limits = DEBUGGER_LAYOUT_LIMITS[kind]
  return Math.min(limits.max, Math.max(limits.min, Math.round(value)))
}

function defaultExtractorForProtocol(protocol: string) {
  switch (protocol) {
    case 'database':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.rows[0].id' }
    case 'network':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.response' }
    case 'grpc':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    case 'jms-solace':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    case 'websocket':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    default:
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.data.id' }
  }
}

function defaultAssertionForProtocol(protocol: string) {
  switch (protocol) {
    case 'database':
      return { type: 'row_count', expected: 1 }
    case 'network':
      return { type: 'body_contains', value: '' }
    case 'grpc':
      return { type: 'grpc_status', expected: 0 }
    case 'jms-solace':
      return { type: 'message_received' }
    case 'websocket':
      return { type: 'message_received' }
    default:
      return { type: 'status_code', expected: 200 }
  }
}

function formatProtocolEvidenceSummary(evidence?: ProtocolEvidence): string {
  if (!evidence) return ''
  const parts = [
    evidence.protocol,
    evidence.operation,
    evidence.status,
    evidence.target,
  ].filter((value): value is string => typeof value === 'string' && value.trim().length > 0)
  return parts.join(' / ')
}

function lookupBrowserDeviceProfile(name?: string) {
  return BROWSER_DEVICE_PROFILES.find(profile => profile.name === name)
}

function formatBrowserDeviceProfileLabel(profile: BrowserDeviceProfileOption) {
  return profile.engine === 'webkit' ? `${profile.name} (WebKit)` : profile.name
}

function sanitizeDownloadName(value: string | undefined, fallback: string) {
  const normalized = (value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return normalized || fallback
}

function applyValidationRuntimeSettingsMetadata(script: ScriptDefinition, validation: ScriptValidationResponse): ScriptDefinition {
  const importMetadata = script.metadata?.import
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
  const actions = script.actions.map((action, index) => {
    const isBrowserAction = action.type.startsWith('browser_')
    const assertionCount = action.assertions?.length || 0
    const parameterRefs = extractParameterReferences(action)
    const hasTransaction = autoTransactions === 'per_action' || Boolean(action.metadata?.transaction || action.metadata?.transaction_name)
    return {
      index,
      name: action.name || `Action ${index + 1}`,
      type: action.type || action.method || 'action',
      target: action.selector || action.target || action.method || '',
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

function readSourceReview(metadata?: ScriptImportMetadata | null): LoadRunnerSourceReview | null {
  const value = readPreservedImportObject(metadata, 'source_review')
  if (!value) return null
  if (!Array.isArray(value.source_files) && !Array.isArray(value.mappings)) return null
  return value as LoadRunnerSourceReview
}

function scriptHasSourceReview(script?: ScriptData | null): boolean {
  return Boolean(readSourceReview(script?.metadata?.import))
}

function readString(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function readNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function readArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
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

function decodeBase64Bytes(value: string): Uint8Array {
  const decoded = atob(value)
  const bytes = new Uint8Array(decoded.length)
  for (let index = 0; index < decoded.length; index += 1) {
    bytes[index] = decoded.charCodeAt(index)
  }
  return bytes
}

interface ReplayOptionsState {
  groupName: string
  ignoreThinkTime: boolean
  startPaused: boolean
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

const TIMESTAMP_PARAMETER_FORMATS = new Set(['unix', 'unix_ms', 'iso8601', 'rfc3339', 'rfc3339_nano'])

function parameterFormatForType(nextType: string, currentFormat?: string) {
  const format = currentFormat || ''
  if (nextType === 'timestamp') {
    return TIMESTAMP_PARAMETER_FORMATS.has(format) ? format : 'iso8601'
  }
  if (nextType === 'date_time') {
    return format && !TIMESTAMP_PARAMETER_FORMATS.has(format) ? format : '2006-01-02 15:04:05'
  }
  if (nextType === 'time') {
    return format && !TIMESTAMP_PARAMETER_FORMATS.has(format) && format !== '2006-01-02 15:04:05' ? format : '15:04:05'
  }
  return currentFormat
}

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

function debugBreakpointKey(phase: string, actionIndex: number): string {
  return `${phase}:${actionIndex}`
}

function normalizeDebugPhase(phase: string): ScriptDebugBreakpoint['phase'] {
  return phase === 'init' || phase === 'teardown' ? phase : 'action'
}

function formatDebugPhaseLabel(phase: string): string {
  return phase.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
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
}

interface ActionEditorSelectionState {
  start: number
  end: number
  selectedText: string
  x: number
  y: number
  existingName: string | null
}

interface ActionEditorParameterDraft {
  actionIndex: number
  start: number
  end: number
  selectedText: string
  existingName: string | null
  requestedName: string
  seedValue: string
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

function getImportedRuntimeSectionId(area?: string | null): string | null {
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
  const showProtocolSelector = Boolean(scriptId)
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
    hydrateEditorState,
    isParameterDialogOpen,
    jsonMode,
    jsonText,
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
    setIsParameterDialogOpen,
    setJsonMode,
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
    closeParameterDialog,
    closeReplayScreenshot,
  } = useScriptEditorState(initialScript)
  const isBrowserScript = script.protocol === 'browser'
  const isHttpScript = script.protocol === 'http'
  const isJmsScript = script.protocol === 'jms-solace'
  const isJavaHttpScript = script.protocol === 'java_http'
  const isSoapScript = script.protocol === 'soap'
  const isWebSocketScript = script.protocol === 'websocket'
  const isGrpcScript = script.protocol === 'grpc'
  const isDatabaseScript = script.protocol === 'database'
  const isNetworkScript = script.protocol === 'network'
  const isHttpTransportScript = isHttpScript || script.protocol === 'https' || isJavaHttpScript || isSoapScript || isBrowserScript
  const httpProxyMode = script.config.http?.proxy_mode || 'none'
  const httpProxySummary = httpProxyMode === 'system'
    ? 'System proxy'
    : httpProxyMode === 'custom' && script.config.http?.proxy?.url
      ? 'Proxy configured'
      : 'Default'
  const outlineSummary = buildScriptOutlineSummary(script)
  const importMetadata = script.metadata?.import
  const importSummary = getImportSummary(importMetadata)
  const sourceReview = readSourceReview(importMetadata)
  const objectDescriptorReview = buildWeb2ObjectDescriptorReview(script, importMetadata)
  const httpConversionReview = buildBrowserHTTPConversionReview(script, replayReport, browserReplaySession)
  const [httpConversionPreview, setHttpConversionPreview] = useState<BrowserToHTTPPreviewResponse | null>(null)
  const [httpConversionGenerating, setHttpConversionGenerating] = useState(false)
  const [httpConversionError, setHttpConversionError] = useState('')
  const [workspaceTab, setWorkspaceTab] = useState<WorkspaceTab>(() => {
    try {
      const value = window.localStorage.getItem('scriptEditorWorkspaceTab')
      return value === 'debug' || value === 'json' ? value : 'author'
    } catch {
      return 'author'
    }
  })
  const [debugDockCollapsed, setDebugDockCollapsed] = useState(() => {
    try {
      const value = window.localStorage.getItem('scriptEditorDebugDockCollapsed')
      return value === null ? true : value === 'true'
    } catch {
      return true
    }
  })
  const [debugBreakpoints, setDebugBreakpoints] = useState<ScriptDebugBreakpoint[]>([])
  const [debuggerTab, setDebuggerTab] = useState<DebuggerTab>('request')
  const [debuggerMaximized, setDebuggerMaximized] = useState(false)
  const [debuggerSettingsOpen, setDebuggerSettingsOpen] = useState(false)
  const [debuggerBottomTab, setDebuggerBottomTab] = useState<DebuggerBottomTab>('console')
  const [debugRawLevel, setDebugRawLevel] = useState<ScriptDebugLogLevel | 'all'>('all')
  const [debugRawSearch, setDebugRawSearch] = useState('')
  const [debugRawAutoScroll, setDebugRawAutoScroll] = useState(true)
  const [debugRawClearedAt, setDebugRawClearedAt] = useState(0)
  const debugRawOutputRef = useRef<HTMLDivElement | null>(null)
  const [debugRailFilter, setDebugRailFilter] = useState('')
  const [debugRailFailuresOnly, setDebugRailFailuresOnly] = useState(false)
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
  const jsonWorkspaceHydrated = useRef(false)

  useEffect(() => {
    try {
      window.localStorage.setItem('scriptEditorWorkspaceTab', workspaceTab)
      window.localStorage.setItem('scriptEditorDebugDockCollapsed', debugDockCollapsed ? 'true' : 'false')
      window.localStorage.setItem('debuggerRailWidth', debuggerRailWidth.toString())
      window.localStorage.setItem('debuggerVarsWidth', debuggerVarsWidth.toString())
      window.localStorage.setItem('debuggerBottomHeight', debuggerBottomHeight.toString())
    } catch { /* storage may be unavailable */ }
  }, [workspaceTab, debugDockCollapsed, debuggerRailWidth, debuggerVarsWidth, debuggerBottomHeight])
  useEffect(() => {
    if (workspaceTab !== 'json' || jsonMode || jsonWorkspaceHydrated.current) return
    setJsonText(JSON.stringify({ script }, null, 2))
    setJsonMode(true)
    jsonWorkspaceHydrated.current = true
  }, [jsonMode, script, setJsonMode, setJsonText, workspaceTab])
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
  const importedParameterFileMappings = importSummary.parameterFileSections.map((section) => {
    const sourceIndex = findImportedParameterMappingDataSourceIndex(script.data_sources, section.values)
    return {
      section,
      sourceName: sourceIndex !== null ? script.data_sources?.[sourceIndex]?.name || null : null,
    }
  })
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
    setIsParameterDialogOpen(true)
    const sourceIndex = findDataSourceIndexByName(script.data_sources, sourceName)
    if (sourceIndex !== null) {
      setOpenDataEditor(sourceIndex)
    }
  }, [script.data_sources, setIsParameterDialogOpen, setOpenDataEditor])
  const openImportedRuntimeSettings = useCallback((area?: string | null) => {
    const sectionId = getImportedRuntimeSectionId(area)
    setOpenSections(prev => {
      const next: Record<string, boolean> = { ...prev, runtime: true }
      if (sectionId) {
        next[sectionId] = true
      }
      return next
    })
  }, [setOpenSections])
  const selectedBrowserEngine = script.config.browser?.engine || 'chromium'
  const [assistantChangeLog, setAssistantChangeLog] = useState<AssistantChangeLogItem[]>([])
  const [editorHistory, setEditorHistory] = useState<EditorHistory>({ past: [], future: [] })
  const [authoringComponents, setAuthoringComponents] = useState<AuthoringComponent[]>([])
  const [componentLibraryStatus, setComponentLibraryStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [validationEvidence, setValidationEvidence] = useState<ValidationEvidence | null>(null)
  const [validationReplayPreflight, setValidationReplayPreflight] = useState<ReplayPreflight | null>(null)
  const authoringIssues = collectAuthoringIssues(script)
  const actionIssueBuckets = groupIssuesByAction(authoringIssues, script.actions.length)
  const scriptLevelIssues = authoringIssues.filter((issue) => typeof issue.actionIndex !== 'number')
  const errorCount = authoringIssues.filter((issue) => issue.severity === 'error').length
  const warningCount = authoringIssues.filter((issue) => issue.severity === 'warning').length
  const starterTemplates = buildProtocolStarterTemplates(script.protocol, script.actions.length)
  const migrationSuggestions = buildMigrationAssistantSuggestions(script)
  const repairSuggestions = buildScriptRepairSuggestions(script, authoringIssues)
  const reviewBaselineScript = importBaselineScript || editorHistory.past[editorHistory.past.length - 1] || null
  const draftDiffSummary = reviewBaselineScript ? buildImportedDraftDiffSummary(reviewBaselineScript, script) : null
  const parameterInspectorItems = summarizeParameters(script.parameters || [])
  const dataSourceInspectorItems = summarizeDataSources(script.data_sources || [])
  const extractorInspectorItems = script.actions.flatMap((action, actionIndex) =>
    (action.extractors || []).map((extractor, extractorIndex) => ({
      id: `${actionIndex}:${extractorIndex}`,
      label: `${extractor.name || `extractor_${extractorIndex + 1}`} • ${action.name || `Action ${actionIndex + 1}`}`,
      actionIndex,
    })),
  )
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
    if (jsonMode) setJsonText(JSON.stringify({ script: nextScript }, null, 2))
  }, [jsonMode, pushEditorHistory, script, setJsonText, setScript])

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
      if (jsonMode) setJsonText(JSON.stringify({ script: priorScript }, null, 2))
      setSuccess('Undid the last guided authoring change.')
      setError('')
      return {
        past: nextPast,
        future: [current, ...previous.future].slice(0, 30),
      }
    })
  }, [jsonMode, script, setError, setJsonText, setScript, setSuccess])

  const redoEditorChange = useCallback(() => {
    setEditorHistory(previous => {
      const [nextScript, ...remainingFuture] = previous.future
      if (!nextScript) return previous
      const current = JSON.parse(JSON.stringify(script)) as ScriptData
      setScript(nextScript)
      if (jsonMode) setJsonText(JSON.stringify({ script: nextScript }, null, 2))
      setSuccess('Redid the guided authoring change.')
      setError('')
      return {
        past: [...previous.past, current].slice(-30),
        future: remainingFuture,
      }
    })
  }, [jsonMode, script, setError, setJsonText, setScript, setSuccess])

  useEffect(() => {
    if (!isParameterDialogOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeParameterDialog()
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [closeParameterDialog, isParameterDialogOpen])

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

  useEffect(() => {
    let cancelled = false
    setComponentLibraryStatus('loading')
    api.listAuthoringComponents({ protocol: script.protocol })
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
  }, [api, script.protocol])

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
    hydrateEditorState(initialScript)

		const focusedActionIndex = findActionIndexByName(initialScript.actions, initialFocusedActionName)
		if (focusedActionIndex !== null) {
			setActiveAction(focusedActionIndex)
		}

    if (initialFocusedSection === 'runtime') {
      setOpenSections(prev => ({ ...prev, runtime: true }))
    }

    if (initialFocusedRuntimeArea) {
      const sectionId = getImportedRuntimeSectionId(initialFocusedRuntimeArea)
      setOpenSections(prev => {
        const next: Record<string, boolean> = { ...prev, runtime: true }
        if (sectionId) {
          next[sectionId] = true
        }
        return next
      })
    }

    if (initialFocusedSection === 'parameter-data') {
      setIsParameterDialogOpen(true)
    }

    const focusedDataSourceIndex = findDataSourceIndexByName(initialScript.data_sources, initialFocusedDataSourceName)
    if (focusedDataSourceIndex !== null) {
      setIsParameterDialogOpen(true)
      setOpenDataEditor(focusedDataSourceIndex)
    }
  }, [hydrateEditorState, initialFocusedActionName, initialFocusedDataSourceName, initialFocusedRuntimeArea, initialFocusedSection, initialScript, setActiveAction, setIsParameterDialogOpen, setOpenDataEditor, setOpenSections])

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
      }
      return
    }

    if ((msg.type === 'script_debug_paused' || msg.type === 'script_debug_resumed' || msg.type === 'script_debug_breakpoints_updated') && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      if (msg.type === 'script_debug_breakpoints_updated') {
        setDebugBreakpoints(session.breakpoints || [])
      }
      const isPaused = msg.type === 'script_debug_paused'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: msg.type === 'script_debug_breakpoints_updated' ? 'breakpoints' : isPaused ? 'paused' : 'resumed',
        kind: 'event',
        title: msg.type === 'script_debug_breakpoints_updated' ? 'Breakpoints updated' : isPaused ? 'Debugger paused' : 'Debugger resumed',
        detail: session.current_action
          ? `${formatDebugPhaseLabel(session.current_action.phase)} ${session.current_action.action_index + 1}: ${session.current_action.action_name || 'next action'}`
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
      }
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
    }
  }, [activeReplaySessionId, setActiveReplaySessionId, setBrowserReplaySession, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplaying])

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
      if (protocol === 'jms-solace') {
        next.config.jms = {
          provider: 'solace',
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
    if (shouldSelectFirstAction) setActiveAction(0)
  }

  const addAction = () => {
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
    const a = JSON.parse(JSON.stringify({
      ...template,
      name: `Action_${script.actions.length + 1}`,
      extractors: template.extractors || [],
      assertions: template.assertions || [],
    })) as Action
    setScript(prev => ({ ...prev, actions: [...prev.actions, a] }))
    setActiveAction(script.actions.length)
  }

  const addActionTemplate = (templateKey: string) => {
    const template = starterTemplates.find((candidate) => candidate.key === templateKey)
    if (!template) return
    const nextAction = JSON.parse(JSON.stringify(template.action)) as Action
    pushEditorHistory(script)
    setScript(prev => ({ ...prev, actions: [...prev.actions, nextAction] }))
    setActiveAction(script.actions.length)
    setSuccess(`Added starter block: ${template.label}.`)
    setError('')
  }

  const removeAction = (idx: number) => {
    setScript(prev => ({ ...prev, actions: prev.actions.filter((_, i) => i !== idx) }))
    setDebugBreakpoints(prev => prev
      .filter(bp => bp.phase !== 'action' || bp.action_index !== idx)
      .map(bp => bp.phase === 'action' && bp.action_index > idx ? { ...bp, action_index: bp.action_index - 1 } : bp))
    if (activeAction === idx) setActiveAction(null)
    else if (activeAction !== null && activeAction > idx) setActiveAction(activeAction - 1)
  }

  const moveAction = (idx: number, dir: -1 | 1) => {
    const newIdx = idx + dir
    if (newIdx < 0 || newIdx >= script.actions.length) return
    setScript(prev => {
      const actions = [...prev.actions]
      ;[actions[idx], actions[newIdx]] = [actions[newIdx], actions[idx]]
      return { ...prev, actions }
    })
    setDebugBreakpoints(prev => prev.map(bp => {
      if (bp.phase !== 'action') return bp
      if (bp.action_index === idx) return { ...bp, action_index: newIdx }
      if (bp.action_index === newIdx) return { ...bp, action_index: idx }
      return bp
    }))
    setActiveAction(newIdx)
  }

  const updateAction = (idx: number, field: keyof Action, value: unknown) => {
    setScript(prev => {
      const actions = [...prev.actions]
      actions[idx] = { ...actions[idx], [field]: value }
      return { ...prev, actions }
    })
  }

  const addExtractor = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext.push(defaultExtractorForProtocol(prev.protocol))
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const updateExtractor = (actionIdx: number, extIdx: number, field: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext[extIdx] = { ...ext[extIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const removeExtractor = (actionIdx: number, extIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = (actions[actionIdx].extractors || []).filter((_, i) => i !== extIdx)
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const addAssertion = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions.push(defaultAssertionForProtocol(prev.protocol))
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const updateAssertion = (actionIdx: number, aIdx: number, field: keyof Assertion, value: unknown) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions[aIdx] = { ...assertions[aIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const removeAssertion = (actionIdx: number, aIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = (actions[actionIdx].assertions || []).filter((_, i) => i !== aIdx)
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const addHeader = (actionIdx: number) => {
    const key = prompt('Header name:')
    if (!key) return
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}), [key]: '' }
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const updateHeader = (actionIdx: number, oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      if (oldKey !== newKey) delete headers[oldKey]
      headers[newKey] = value
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const removeHeader = (actionIdx: number, key: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      delete headers[key]
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const addMetadata = (actionIdx: number) => {
    const key = prompt('Property name:')
    if (!key) return
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}), [key]: '' }
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
    })
  }

  const updateMetadata = (actionIdx: number, oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}) }
      if (oldKey !== newKey) delete metadata[oldKey]
      metadata[newKey] = value
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
    })
  }

  const removeMetadata = (actionIdx: number, key: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}) }
      delete metadata[key]
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
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

  const resolveCurrentScript = (): ScriptData => {
    if (!jsonMode) return script
    const parsed = JSON.parse(jsonText) as ScriptData | { script?: ScriptData }
    if ('script' in parsed && parsed.script) {
      return parsed.script
    }
    return parsed as ScriptData
  }

  const handleSave = async () => {
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
      if (jsonMode) setScript(currentScript)
      setSuccess('Script saved successfully!')
      onSave?.(savedScript)
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to save script'))
    } finally { setSaving(false) }
  }

  const handleValidateScript = async () => {
    setError('')
    setSuccess('')
    setWarnings([])
    setValidationEvidence(null)
    setValidationReplayPreflight(null)
    setValidating(true)
    try {
      const currentScript = resolveCurrentScript()
      const res = scriptId
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
      setWarnings(validationWarnings)
      if (jsonMode || validationScript !== currentScript) {
        setScript(validationScript)
      }
      if (res.valid) {
        setSuccess(validationWarnings.length > 0 ? 'Script validation passed with warnings.' : 'Script validation passed.')
        return
      }
      setError(res.issues.join(' | ') || 'Script validation failed.')
    } catch (e: unknown) {
      const validationWarnings = normalizeWarnings(getErrorWarnings(e))
      setWarnings(validationWarnings)
      setValidationEvidence(null)
      setValidationReplayPreflight(null)
      setError(getErrorMessage(e, 'Script validation failed'))
    } finally {
      setValidating(false)
    }
  }

  const handleGenerateBrowserHTTPPreview = async () => {
    setHttpConversionError('')
    setSuccess('')
    const currentScript = resolveCurrentScript()
    const networkEntries = replayNetworkEntriesForConversion(replayReport, browserReplaySession)
    setHttpConversionGenerating(true)
    try {
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
    } finally {
      setHttpConversionGenerating(false)
    }
  }

  const activateWorkspaceTab = (nextTab: WorkspaceTab) => {
    if (nextTab === 'json') {
      if (!jsonMode) {
        setJsonText(JSON.stringify({ script }, null, 2))
        setJsonMode(true)
      }
      setWorkspaceTab('json')
      return
    }

    if (jsonMode) {
      try {
        const parsed = JSON.parse(jsonText)
        setScript(parsed.script || parsed)
        setJsonMode(false)
      } catch {
        setError('Invalid JSON')
        setWorkspaceTab('json')
        return
      }
    }

    setWorkspaceTab(nextTab)
    if (nextTab === 'debug') {
      setDebugDockCollapsed(false)
      setDebuggerTab('request')
    }
  }

  const openReplayDebugger = (openSettings = false) => {
    activateWorkspaceTab('debug')
    setDebugDockCollapsed(false)
    setDebuggerTab('request')
    if (openSettings) setDebuggerSettingsOpen(true)
  }

  const handleReplay = async (options?: { startPaused?: boolean }) => {
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
      const payload = {
        ...buildReplayStartPayload(currentScript, {
        projectId,
        groupName: replayOptions.groupName,
        replaySessionId,
        variables,
        ignoreThinkTime: replayOptions.ignoreThinkTime,
        startPaused,
        }),
        group_name: replayOptions.groupName.trim() || `${currentScript.name || 'script'}-debug`,
        start_paused: startPaused,
        breakpoints: debugBreakpoints,
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
      if (jsonMode) setScript(currentScript)
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
              ? `${formatDebugPhaseLabel(res.session.current_action.phase)} ${res.session.current_action.action_index + 1}: ${res.session.current_action.action_name || 'next action'}`
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
  }

  const handleStopReplay = async () => {
    if (!activeReplaySessionId) return
    try {
      await api.stopScriptDebugSession(activeReplaySessionId)
      setError('')
      setSuccess('Stopping debugger…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to stop debugger'))
    }
  }

  const handlePauseResumeReplay = async () => {
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
  }

  const handleStepReplay = async () => {
    if (!activeReplaySessionId || !browserReplaySession) {
      await handleReplay({ startPaused: true })
      return
    }
    if (browserReplaySession.status !== 'paused') return
    try {
      const res = await api.stepScriptDebugSession(activeReplaySessionId)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
      setSuccess('Stepping debugger…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to step debugger'))
    }
  }

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

  const toggleDebugBreakpoint = async (phase: string, actionIndex: number) => {
    const normalized: ScriptDebugBreakpoint = { phase: normalizeDebugPhase(phase), action_index: actionIndex }
    const key = debugBreakpointKey(normalized.phase, normalized.action_index)
    const nextBreakpoints = debugBreakpointKeys.has(key)
      ? debugBreakpoints.filter(bp => debugBreakpointKey(bp.phase, bp.action_index) !== key)
      : [...debugBreakpoints, normalized]
    setDebugBreakpoints(nextBreakpoints)
    if (!activeReplaySessionId || !browserReplaySession || ['completed', 'failed', 'stopped'].includes(browserReplaySession.status)) {
      return
    }
    try {
      const res = await api.updateScriptDebugBreakpoints(activeReplaySessionId, nextBreakpoints)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update debugger breakpoints'))
    }
  }

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

  const handleDownloadBrowserReplayBundle = () => {
    if (!isBrowserScript || !replayReport) return

    const replaySessionId = activeReplaySessionId || browserReplaySession?.id
    const exportedAt = new Date().toISOString()
    const exportPayload = {
      schema_version: 'apexload.browser_replay_export.v1',
      exported_at: exportedAt,
      replay_session_id: replaySessionId,
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
        variables: replaySeedVariables,
      },
      preflight: browserReplaySession?.preflight || replayPreflight,
      artifact_manifest: replayReport.artifact_manifest,
      diagnosis_summary: replayReport.diagnosis_summary,
      repair_suggestions: replayReport.repair_suggestions,
      session: browserReplaySession,
      report: replayReport,
    }
    const replayEvents = replayReport.events.length >= (browserReplaySession?.events?.length || 0)
      ? replayReport.events
      : browserReplaySession?.events || []
    const scriptBase = sanitizeDownloadName(script.name, 'script')
    const replayBase = sanitizeDownloadName(replaySessionId || replayReport.group || 'browser-replay', 'browser-replay')
    const bundleBase = `${scriptBase}-${replayBase}-browser-replay`
    const eventNumberWidth = Math.max(2, String(Math.max(replayEvents.length, 1)).length)
    const entries: ZipFileEntry[] = [
      {
        name: `${bundleBase}/manifest.json`,
        content: JSON.stringify({
          schema_version: 'apexload.browser_replay_bundle.v1',
          exported_at: exportedAt,
          replay_session_id: replaySessionId,
          group_name: replayReport.group,
          status: browserReplaySession?.status || (replayReport.success ? 'completed' : 'failed'),
          success: replayReport.success,
          event_count: replayEvents.length,
          includes_session: Boolean(browserReplaySession),
          includes_report: true,
        }, null, 2),
      },
      {
        name: `${bundleBase}/replay-export.json`,
        content: JSON.stringify(exportPayload, null, 2),
      },
      {
        name: `${bundleBase}/script.json`,
        content: JSON.stringify(script, null, 2),
      },
      {
        name: `${bundleBase}/seed-variables.json`,
        content: JSON.stringify(replaySeedVariables, null, 2),
      },
      {
        name: `${bundleBase}/report.json`,
        content: JSON.stringify(replayReport, null, 2),
      },
    ]

    if (browserReplaySession) {
      entries.push({
        name: `${bundleBase}/session.json`,
        content: JSON.stringify(browserReplaySession, null, 2),
      })
    }

    replayEvents.forEach((event, index) => {
      const eventBase = `${bundleBase}/events/${String(index + 1).padStart(eventNumberWidth, '0')}-${sanitizeDownloadName(event.action_name || event.action_type, 'event')}`
      const replayConsoleEntries = event.console_entries || event.snapshot?.console_entries
      const replayNetworkEntries = event.network_entries || event.snapshot?.network_entries
      const replayHTML = event.html || event.snapshot?.html
      const replayScreenshotBase64 = event.screenshot_base64 || event.snapshot?.screenshot_base64
      const replayDOMSummary = event.dom_summary || event.snapshot?.dom_summary

      entries.push({
        name: `${eventBase}/event.json`,
        content: JSON.stringify(event, null, 2),
      })

      if (hasStructuredReplaySnapshot(event.snapshot)) {
        entries.push(
          {
            name: `${eventBase}/snapshot.json`,
            content: JSON.stringify(event.snapshot, null, 2),
          },
          {
            name: `${eventBase}/request.txt`,
            content: formatSnapshotRequestSection(event.snapshot),
          },
          {
            name: `${eventBase}/response.txt`,
            content: formatSnapshotResponseSection(event.snapshot),
          },
        )
      }

      if (replayDOMSummary) {
        entries.push({
          name: `${eventBase}/dom-summary.json`,
          content: JSON.stringify(replayDOMSummary, null, 2),
        })
      }

      if (replayConsoleEntries?.length) {
        entries.push({
          name: `${eventBase}/console.txt`,
          content: formatConsoleEntries(replayConsoleEntries),
        })
      }

      if (replayNetworkEntries?.length) {
        entries.push({
          name: `${eventBase}/network.txt`,
          content: formatNetworkEntries(replayNetworkEntries),
        })
      }

      if (replayHTML) {
        entries.push({
          name: `${eventBase}/page.html`,
          content: replayHTML,
        })
      }

      if (replayScreenshotBase64) {
        entries.push({
          name: `${eventBase}/screenshot.png`,
          content: decodeBase64Bytes(replayScreenshotBase64),
        })
      }
    })

    downloadBlob(createZipBlob(entries), `${bundleBase}.zip`)
    setError('')
    setSuccess('Exported browser replay bundle ZIP.')
  }

  const toggleJsonMode = () => {
    activateWorkspaceTab(workspaceTab === 'json' ? 'author' : 'json')
  }

  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success)'
      case 'POST': return 'var(--info)'
      case 'PUT': return 'var(--warning)'
      case 'DELETE': return 'var(--danger)'
      case 'PATCH': return 'var(--chart-6)'
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
  const replayVariableDiffs = replayReport
    ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events)
    : []
  const replayConfiguredVariables = safeParseReplayVariables(replayOptions.variablesText)
  const replayPreflight = browserReplaySession?.preflight || validationReplayPreflight || buildLocalReplayPreflight(script, replayConfiguredVariables)

  useEffect(() => {
    setValidationReplayPreflight(null)
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
  const stepReplayDisabled = replaying
    ? (!activeReplaySessionId || !isReplayPaused)
    : (saving || !script.name || script.actions.length === 0)
  const scriptPhases = script as ScriptData & { init?: Action[]; teardown?: Action[] }
  const debugActionRows = [
    ...(scriptPhases.init || []).map((action: Action, index: number) => ({ phase: 'init', actionIndex: index, action })),
    ...(script.actions || []).map((action: Action, index: number) => ({ phase: 'action', actionIndex: index, action })),
    ...(scriptPhases.teardown || []).map((action: Action, index: number) => ({ phase: 'teardown', actionIndex: index, action })),
  ]
  const debugBreakpointKeys = new Set(debugBreakpoints.map(bp => debugBreakpointKey(bp.phase, bp.action_index)))
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
  const selectedReplayEventIndex = replayReport && expandedReplayEvent != null && expandedReplayEvent >= 0 && expandedReplayEvent < replayReport.events.length
    ? expandedReplayEvent
    : null
  const selectedReplayEvent = selectedReplayEventIndex != null && replayReport ? replayReport.events[selectedReplayEventIndex] : null
  const currentDebugActionKey = browserReplaySession?.current_action
    ? debugBreakpointKey(browserReplaySession.current_action.phase, browserReplaySession.current_action.action_index)
    : null
  const debugRowPositionByKey = new Map(debugActionRows.map((row, position) => [debugBreakpointKey(row.phase, row.actionIndex), position]))
  const debugRailFilterText = debugRailFilter.trim().toLowerCase()

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
      if (raw) setDebugBreakpoints(JSON.parse(raw))
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

  const resetDebuggerLayout = () => {
    setDebuggerRailWidth(DEBUGGER_LAYOUT_DEFAULTS.rail)
    setDebuggerVarsWidth(DEBUGGER_LAYOUT_DEFAULTS.vars)
    setDebuggerBottomHeight(DEBUGGER_LAYOUT_DEFAULTS.bottom)
  }

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

  // IDE-style keyboard shortcuts, active on the editor page unless the user is typing.
  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        if (debuggerSettingsOpen) { setDebuggerSettingsOpen(false); return }
        if (workspaceTab === 'debug') {
          setWorkspaceTab('author')
          setDebugDockCollapsed(true)
        }
        return
      }
      const target = event.target as HTMLElement | null
      const typing = !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable)
      if (typing) return
      if (event.key === 'F5') {
        event.preventDefault()
        if (event.shiftKey) {
          if (replaying) void handleStopReplay()
        } else if (replaying) {
          if (isReplayPaused) void handlePauseResumeReplay()
        } else if (!saving && script.name && script.actions.length > 0) {
          void handleReplay()
        }
        return
      }
      if (event.key === 'F10') {
        event.preventDefault()
        if ((replaying && isReplayPaused) || (!replaying && !saving && script.name && script.actions.length > 0)) void handleStepReplay()
        return
      }
      if ((event.metaKey || event.ctrlKey) && (event.key === 'b' || event.key === 'B')) {
        event.preventDefault()
        const row = workspaceTab === 'debug'
          ? debugActionRows[selectedReplayEventIndex ?? 0]
          : activeAction !== null
            ? { phase: 'action', actionIndex: activeAction, action: script.actions[activeAction] }
            : debugActionRows[0]
        if (row) void toggleDebugBreakpoint(row.phase, row.actionIndex)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [activeAction, debuggerSettingsOpen, workspaceTab, replaying, isReplayPaused, saving, script.name, script.actions, selectedReplayEventIndex, debugActionRows, handleReplay, handlePauseResumeReplay, handleStepReplay, handleStopReplay, toggleDebugBreakpoint])

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
      } catch { /* transient; next tick retries */ }
    }
    void reconcile()
    const timer = window.setInterval(reconcile, 1200)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [activeReplaySessionId, replaying, api])

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
  }

  const updateParameter = (idx: number, field: keyof ParameterConfig, value: unknown) => {
    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const current = parameters[idx] || { ...EMPTY_PARAMETER }
      parameters[idx] = { ...current, [field]: value }
      return { ...prev, parameters }
    })
  }

  const updateParameterType = (idx: number, nextType: string) => {
    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const current = parameters[idx] || { ...EMPTY_PARAMETER }
      parameters[idx] = {
        ...current,
        type: nextType,
        update: current.update || 'each_iteration',
        selection: current.selection || 'sequential',
        on_exhaustion: current.on_exhaustion || 'wrap',
        start: nextType === 'current_iteration' ? (current.start ?? 1) : current.start,
        step: (nextType === 'counter' || nextType === 'current_iteration') ? (current.step || 1) : current.step,
        length: nextType === 'random_string' ? (current.length || 12) : current.length,
        format: parameterFormatForType(nextType, current.format),
      }
      return { ...prev, parameters }
    })
  }

  const removeParameter = (idx: number) => {
    setScript(prev => ({
      ...prev,
      parameters: (prev.parameters || []).filter((_, i) => i !== idx),
    }))
  }

  const upsertParameterFromActionEditor = ({ selectedText, requestedName, existingName }: ActionEditorParameterRequest): string => {
    const normalizedExistingName = existingName ? sanitizeVarName(existingName) : ''
    const resolvedName = ensureUniqueParameterName(
      requestedName || selectedText,
      script.parameters || [],
      normalizedExistingName || undefined,
    )
    const seedValue = (selectedText || '').trim()

    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const existingIdx = normalizedExistingName
        ? parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === normalizedExistingName)
        : -1
      const byResolvedNameIdx = parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === resolvedName)
      const targetIdx = existingIdx >= 0 ? existingIdx : byResolvedNameIdx

      if (targetIdx >= 0) {
        const current = { ...(parameters[targetIdx] || EMPTY_PARAMETER) }
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

    setIsParameterDialogOpen(true)
    setError('')
    setSuccess(existingName ? `Updated parameter ${resolvedName}.` : `Created parameter ${resolvedName}.`)
    return resolvedName
  }

  const beginParameterEditFromActionEditor = (draft: ActionEditorParameterDraft) => {
    setPendingParameterDraft(draft)
    setIsParameterDialogOpen(true)
    setError('')
  }

  const applyPendingParameterDraft = () => {
    if (!pendingParameterDraft) return

    pushEditorHistory(script)
    const name = upsertParameterFromActionEditor({
      selectedText: pendingParameterDraft.seedValue,
      requestedName: pendingParameterDraft.requestedName,
      existingName: pendingParameterDraft.existingName,
    })
    const action = script.actions[pendingParameterDraft.actionIndex]
    if (action) {
      const body = action.body || ''
      const replacement = `{{param.${name}}}`
      const nextBody = `${body.slice(0, pendingParameterDraft.start)}${replacement}${body.slice(pendingParameterDraft.end)}`
      updateAction(pendingParameterDraft.actionIndex, 'body', nextBody)
    }
    setPendingParameterDraft(null)
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

  const toggleOpenDataEditor = (idx: number) => {
    setOpenDataEditor(prev => (prev === idx ? null : idx))
  }

  const openDataEditorAt = (idx: number) => {
    setOpenDataEditor(idx)
  }

  const toggleDataEditorModeAt = (idx: number) => {
    setDataEditorMode(prev => ({ ...prev, [idx]: (prev[idx] || 'grid') === 'grid' ? 'raw' : 'grid' }))
  }

  const removeDataSource = (idx: number) => {
    setScript(prev => ({
      ...prev,
      data_sources: (prev.data_sources || []).filter((_, i) => i !== idx),
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
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
    setSuccess(`Merged ${merge.sourceCount} parameter source files into ${merge.mergedSourceName}.`)
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

  const scanForAutoParameters = () => {
    const candidates = buildAutoParameterCandidates(script)
    setIsParameterDialogOpen(true)
    setParameterCandidates(candidates)
    if (candidates.length === 0) {
      setSuccess('No strong parameterization candidates were detected.')
      setError('')
      return
    }
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

      const mergedRow: string[] = mergedColumns.map((column, idx) => {
        const selectedMatch = selected.find(candidate => candidate.varName === column)
        if (selectedMatch) return selectedMatch.value
        return existingParsed.rows[0]?.[idx] || ''
      })

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
          [mergedRow],
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

  const openEditorSection = (section: 'config' | 'replay' | 'runtime' | 'import' | 'parameter-data' | 'object-descriptors' | 'http-conversion') => {
    if (section === 'parameter-data') {
      setIsParameterDialogOpen(true)
      return
    }
    setOpenSections(prev => ({ ...prev, [section]: true }))
  }

  const applyObjectDescriptorCandidate = (row: Web2ObjectDescriptorRow, candidate: Web2SelectorCandidate) => {
    if (row.actionIndex === null) {
      setError('No generated browser action is available for this imported object descriptor.')
      return
    }
    updateAction(row.actionIndex, 'selector', candidate.value)
    setActiveAction(row.actionIndex)
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
    const result = applyAuthoringComponentToScript(script, component)
    replaceScriptWithHistory(result.script)
    recordAssistantChange(`Inserted ${component.name}`, result.summary)
    setActiveAction(result.script.actions.length > 0 ? result.script.actions.length - 1 : null)
    setSuccess(result.summary)
    setError('')
  }

  const debugSessionAvailable = Boolean(replayReport || activeReplaySessionId || browserReplaySession)
  const debugEvidenceStatus = replayReport?.completed_at
    ? (replayReport.success ? 'passed' : 'failed')
    : activeReplaySessionId || browserReplaySession
      ? 'in progress'
      : 'missing'
  const problemCount = errorCount + warningCount + warnings.length + (error ? 1 : 0)
  const actionProblemRows = actionIssueBuckets.flatMap((issues, actionIndex) =>
    issues.map(issue => ({ issue, actionIndex })),
  )
  const openProblemsDock = () => {
    setDebuggerBottomTab('problems')
    setDebugDockCollapsed(false)
  }
  const openDebugOutputDock = () => {
    setDebuggerBottomTab('output')
    setDebugDockCollapsed(false)
  }
  const openVariablesInspector = () => {
    activateWorkspaceTab('debug')
    setDebuggerTab('request')
  }
  const toggleSelectedBreakpoint = () => {
    const selectedRow = workspaceTab === 'debug'
      ? debugActionRows[selectedReplayEventIndex ?? 0]
      : activeAction !== null
        ? { phase: 'action', actionIndex: activeAction, action: script.actions[activeAction] }
        : debugActionRows[0]
    if (selectedRow) void toggleDebugBreakpoint(selectedRow.phase, selectedRow.actionIndex)
  }

  const convergenceItems = [
    {
      key: 'validate',
      label: 'Validate',
      status: validationEvidence ? 'done' : errorCount > 0 ? 'needs attention' : 'ready',
      action: handleValidateScript,
      disabled: saving || replaying || validating || !script.name,
    },
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
      disabled: saving || validating || !script.name,
    },
  ]

  return (
    <div className="script-editor">
      <div className="editor-header">
        <div className="editor-title-row">
          <h2>{scriptId ? 'Edit Script' : 'Create Script'}</h2>
          <div className="editor-actions">
            {importMetadata && (
              <button className="btn btn-ghost" type="button" onClick={() => openEditorSection('import')}>
                Imported Metadata
              </button>
            )}
            <button className="btn btn-ghost" onClick={handleValidateScript} disabled={saving || replaying || validating || !script.name}>
              {validating ? 'Validating…' : (scriptId ? 'Validate Saved Script' : 'Validate Draft')}
            </button>
            <button className="btn btn-primary" onClick={handleSave} disabled={saving || validating || !script.name}>
              {saving ? 'Saving…' : 'Save Script'}
            </button>
            {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
          </div>
        </div>
        <div className="script-ide-command-bar" aria-label="Script IDE command bar">
          <div className="script-ide-tabs" role="tablist" aria-label="Script workspace">
            <button type="button" role="tab" aria-selected={workspaceTab === 'author'} className={workspaceTab === 'author' ? 'active' : ''} onClick={() => activateWorkspaceTab('author')}>
              Author
            </button>
            <button type="button" role="tab" aria-selected={workspaceTab === 'debug'} className={workspaceTab === 'debug' ? 'active' : ''} onClick={() => activateWorkspaceTab('debug')}>
              Debug
            </button>
            <button type="button" role="tab" aria-selected={workspaceTab === 'json'} className={workspaceTab === 'json' ? 'active' : ''} onClick={toggleJsonMode}>
              {'{ } JSON'}
            </button>
          </div>
          <div className="script-ide-quick-actions" role="group" aria-label="Quick actions">
            <button type="button" className="debugger-icon-btn" title={replayStartButtonLabel} onClick={() => void handleReplay()} disabled={replaying || saving || !script.name || script.actions.length === 0}>
              <span className="debugger-icon" aria-hidden="true">&#9654;</span>
              <span>{replaying ? 'Running…' : replayReport ? 'Restart' : 'Run'}</span>
            </button>
            <button type="button" className="debugger-icon-btn" title={isReplayPaused ? 'Continue' : 'Pause'} onClick={handlePauseResumeReplay} disabled={!activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped'}>
              <span className="debugger-icon" aria-hidden="true">{isReplayPaused ? '▶' : '⏸'}</span>
              <span>{isReplayPaused ? 'Continue' : 'Pause'}</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Step to next action" onClick={() => void handleStepReplay()} disabled={stepReplayDisabled}>
              <span className="debugger-icon" aria-hidden="true">&#10551;</span>
              <span>Step</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Stop debugger" onClick={handleStopReplay} disabled={!activeReplaySessionId || !replaying}>
              <span className="debugger-icon" aria-hidden="true">&#9209;</span>
              <span>Stop</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Toggle breakpoint on selected action" onClick={toggleSelectedBreakpoint} disabled={debugActionRows.length === 0}>
              <span className="debugger-icon" aria-hidden="true">●</span>
              <span>Breakpoint</span>
            </button>
            <button type="button" className={`debugger-icon-btn ${debuggerSettingsOpen ? 'active' : ''}`} title="Session overrides" onClick={() => { activateWorkspaceTab('debug'); setDebuggerSettingsOpen(open => !open) }}>
              <span className="debugger-icon" aria-hidden="true">&#9881;</span>
              <span>Settings</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Open Problems" onClick={openProblemsDock}>
              <span className="debugger-icon" aria-hidden="true">!</span>
              <span>Problems{problemCount > 0 ? ` ${problemCount}` : ''}</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Open Variables" onClick={openVariablesInspector}>
              <span className="debugger-icon" aria-hidden="true">{'{}'}</span>
              <span>Variables</span>
            </button>
            <button type="button" className="debugger-icon-btn" title="Open Debug Output" onClick={openDebugOutputDock}>
              <span className="debugger-icon" aria-hidden="true">&gt;_</span>
              <span>Output</span>
            </button>
            {replayReport && (
              <button type="button" className="debugger-icon-btn" title="Download Debug JSON" onClick={handleDownloadBrowserReplayExport}>
                <span className="debugger-icon" aria-hidden="true">&#8675;</span>
                <span>JSON</span>
              </button>
            )}
          </div>
        </div>
        {error && <div className="editor-toast error">{error}</div>}
        {warnings.length > 0 && (
          <div className="editor-toast warning">
            {warnings.map((warning, index) => (
              <div key={`${warning}-${index}`}>{warning}</div>
            ))}
          </div>
        )}
        {success && <div className="editor-toast success">{success}</div>}
        {validationEvidence && <ValidationEvidencePanel evidence={validationEvidence} />}
      </div>

      <div className={`editor-body workspace-${workspaceTab}`}>
          <div className="script-name-panel" aria-label="Script identity">
            <label className="script-name-field">
              <span>Script Name</span>
              <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
            </label>
          </div>

          {/* Script Config Panel */}
          <div className={`config-panel editor-support-dialog support-dialog-config ${openSections['config'] ? 'open' : ''}`} role="dialog" aria-modal="true" aria-label="Script Configuration">
            <button className="panel-header-toggle" onClick={() => toggleSection('config')}>
              <h3 className="panel-title">Script Configuration</h3>
              <span className="chevron">Close</span>
            </button>
            {(
              <div className="panel-body">
                <div className="form-grid">
                  {showProtocolSelector && (
                    <label>
                      <span>Protocol</span>
                      <select value={script.protocol} onChange={e => updateProtocol(e.target.value)}>
                        <option value="http">HTTP API</option>
                        <option value="https">HTTPS API</option>
                        <option value="java_http">Java over HTTP</option>
                        <option value="soap">Web Services (SOAP)</option>
                        <option value="websocket">WebSocket</option>
                        <option value="grpc">gRPC</option>
                        <option value="database">Database</option>
                        <option value="network">Network</option>
                        <option value="browser">Web/HTML Browser</option>
                        <option value="jms-solace">JMS Solace</option>
                      </select>
                    </label>
                  )}
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

          <div
            className={`config-panel replay-panel script-ide-debugger debugger-dialog ${replayReport ? 'has-report' : ''} ${debuggerMaximized ? 'maximized' : ''} ${workspaceTab === 'debug' || !debugDockCollapsed || debugSessionAvailable ? 'open' : ''} ${debugDockCollapsed && workspaceTab !== 'debug' ? 'collapsed' : ''}`}
            role="region"
            aria-label="Script Debugger"
          >
            <div className="debugger-toolbar">
              <div className="debugger-toolbar-group debugger-toolbar-title">
                <span className={`debugger-status-dot ${debuggerStatusState}`} aria-hidden="true" />
                <h3 className="panel-title">Script Debugger</h3>
              </div>
              <div className="debugger-toolbar-group debugger-toolbar-controls" role="group" aria-label="Debugger controls">
                <button
                  type="button"
                  className="debugger-icon-btn"
                  title={replayStartButtonLabel}
                  onClick={() => void handleReplay()}
                  disabled={replaying || saving || !script.name || script.actions.length === 0}
                >
                  <span className="debugger-icon" aria-hidden="true">&#9654;</span>
                  <span>{replaying ? 'Running…' : replayReport ? 'Restart' : 'Run'}</span>
                </button>
                <button
                  type="button"
                  className="debugger-icon-btn"
                  title={isReplayPaused ? 'Continue' : 'Pause'}
                  onClick={handlePauseResumeReplay}
                  disabled={!activeReplaySessionId || !replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped'}
                >
                  <span className="debugger-icon" aria-hidden="true">{isReplayPaused ? '▶' : '⏸'}</span>
                  <span>{isReplayPaused ? 'Continue' : 'Pause'}</span>
                </button>
                <button
                  type="button"
                  className="debugger-icon-btn"
                  title="Step to next action"
                  onClick={() => void handleStepReplay()}
                  disabled={stepReplayDisabled}
                >
                  <span className="debugger-icon" aria-hidden="true">&#10551;</span>
                  <span>Step</span>
                </button>
                <button
                  type="button"
                  className="debugger-icon-btn"
                  title="Stop debugger"
                  onClick={handleStopReplay}
                  disabled={!activeReplaySessionId || !replaying}
                >
                  <span className="debugger-icon" aria-hidden="true">&#9209;</span>
                  <span>Stop</span>
                </button>
              </div>
              <div className="debugger-toolbar-group debugger-toolbar-right">
                {isBrowserScript && replayReport && (
                  <button type="button" className="debugger-icon-btn" title="Download Replay ZIP" onClick={handleDownloadBrowserReplayBundle}>
                    <span className="debugger-icon" aria-hidden="true">&#8675;</span>
                    <span>ZIP</span>
                  </button>
                )}
                {replayReport && (
                  <button type="button" className="debugger-icon-btn" title="Download Debug JSON" onClick={handleDownloadBrowserReplayExport}>
                    <span className="debugger-icon" aria-hidden="true">&#8675;</span>
                    <span>JSON</span>
                  </button>
                )}
                <button
                  ref={debuggerSettingsBtnRef}
                  type="button"
                  className={`debugger-icon-btn ${debuggerSettingsOpen ? 'active' : ''}`}
                  title="Session overrides"
                  aria-pressed={debuggerSettingsOpen}
                  onClick={() => setDebuggerSettingsOpen(open => !open)}
                >
                  <span className="debugger-icon" aria-hidden="true">&#9881;</span>
                  <span>Settings</span>
                </button>
                <button
                  type="button"
                  className="debugger-icon-btn"
                  title="Reset debugger layout"
                  onClick={resetDebuggerLayout}
                >
                  <span className="debugger-icon" aria-hidden="true">&#8635;</span>
                  <span>Layout</span>
                </button>
                <button
                  type="button"
                  className="debugger-icon-btn debugger-icon-btn-square"
                  title={debuggerMaximized ? 'Restore' : 'Maximize'}
                  aria-pressed={debuggerMaximized}
                  onClick={() => setDebuggerMaximized(value => !value)}
                >
                  <span className="debugger-icon" aria-hidden="true">{debuggerMaximized ? '❐' : '⛶'}</span>
                </button>
                {workspaceTab !== 'debug' && (
                  <button
                    type="button"
                    className="debugger-icon-btn"
                    title="Open Debug workspace"
                    onClick={() => activateWorkspaceTab('debug')}
                  >
                    <span className="debugger-icon" aria-hidden="true">↥</span>
                    <span>Debug</span>
                  </button>
                )}
                <button
                  type="button"
                  className="debugger-icon-btn debugger-icon-btn-square"
                  title={workspaceTab === 'debug' ? 'Return to Author' : 'Collapse debug dock'}
                  onClick={() => {
                    setDebuggerSettingsOpen(false)
                    setDebugDockCollapsed(true)
                    if (workspaceTab === 'debug') setWorkspaceTab('author')
                  }}
                >
                  <span className="debugger-icon" aria-hidden="true">&#10005;</span>
                </button>
              </div>
            </div>

            {debuggerSettingsOpen && (
              <div className="debugger-settings-popover" ref={debuggerSettingsRef}>
                <div className="debugger-settings-head">
                  <strong>Session Overrides</strong>
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => setDebuggerSettingsOpen(false)}>Done</button>
                </div>
                <p className="replay-panel-subtitle">Temporary settings for this debugger session. They do not change saved Runtime / RTS settings.</p>
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
                      setDebugDockCollapsed(false)
                      setDebuggerTab('request')
                      setExpandedReplayEvent(replayFailingEventIndex)
                    }}
                  >
                    Inspect
                  </button>
                </div>
              )}

              <div
                className={`debugger-shell ${activeDebuggerResize ? `resizing resizing-${activeDebuggerResize}` : ''}`}
                style={{
                  '--rail-w': `${debuggerRailWidth}px`,
                  '--vars-w': `${debuggerVarsWidth}px`,
                  '--bottom-h': `${debuggerBottomHeight}px`,
                } as React.CSSProperties}
              >
                <aside className="debugger-rail" aria-label="Actions">
                  <div className="debugger-rail-header">
                    <div className="debugger-rail-header-top">
                      <span>Actions</span>
                      <div className="debugger-rail-header-meta">
                        <small>{breakpointSummary}</small>
                        {debugBreakpoints.length > 0 && (
                          <button type="button" className="btn btn-sm btn-ghost" onClick={() => void clearDebugBreakpoints()}>Clear</button>
                        )}
                      </div>
                    </div>
                    <div className="debugger-rail-filter">
                      <input value={debugRailFilter} onChange={e => setDebugRailFilter(e.target.value)} placeholder="Filter actions" aria-label="Filter actions" />
                      <label className="checkbox-label replay-checkbox">
                        <input type="checkbox" checked={debugRailFailuresOnly} onChange={e => setDebugRailFailuresOnly(e.target.checked)} />
                        <span>Failures</span>
                      </label>
                    </div>
                  </div>
                  {debugActionRows.length === 0 ? (
                    <div className="debugger-rail-empty">Add actions to the script to debug.</div>
                  ) : (
                    <div
                      className="debugger-rail-list"
                      tabIndex={0}
                      onKeyDown={e => {
                        if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
                          e.preventDefault()
                          const dir = e.key === 'ArrowDown' ? 1 : -1
                          const current = selectedReplayEventIndex ?? 0
                          const next = Math.max(0, Math.min(debugActionRows.length - 1, current + dir))
                          setExpandedReplayEvent(next)
                        }
                      }}
                    >
                      {(() => {
                        const rows = debugActionRows.map(({ phase, actionIndex, action }, position) => {
                          const rowKey = debugBreakpointKey(phase, actionIndex)
                          const breakpointActive = debugBreakpointKeys.has(rowKey)
                          const rowEvent = replayReport?.events[position]
                          const isCurrentRow = currentDebugActionKey === rowKey
                          const isSelectedRow = selectedReplayEventIndex === position
                          const rowStatus = rowEvent ? (rowEvent.success ? 'pass' : 'fail') : isCurrentRow ? 'current' : 'pending'
                          const haystack = `${action.name || ''} ${action.target || ''} ${action.selector || ''} ${rowEvent?.action_name || ''} ${rowEvent?.target || ''}`.toLowerCase()
                          if (debugRailFilterText && !haystack.includes(debugRailFilterText)) return null
                          if (debugRailFailuresOnly && !(rowEvent && !rowEvent.success)) return null
                          return (
                            <div
                              key={rowKey}
                              className={`debugger-action-row ${rowStatus} ${isSelectedRow ? 'selected' : ''} ${isCurrentRow ? 'current' : ''}`}
                              ref={node => { replayEventRefs.current[position] = node }}
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
                                  if (rowEvent) {
                                    setExpandedReplayEvent(position)
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
                        }).filter(Boolean)
                        return rows.length > 0 ? rows : <div className="debugger-rail-empty">No actions match the filter.</div>
                      })()}
                    </div>
                  )}
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

                <section className="debugger-center" aria-label="Action detail">
                  <div className="debugger-center-tabs" role="tablist">
                    <button type="button" role="tab" aria-selected={debuggerTab === 'request'} className={debuggerTab === 'request' ? 'active' : ''} onClick={() => setDebuggerTab('request')}>Request / Response</button>
                    {isBrowserScript && (
                      <button type="button" role="tab" aria-selected={debuggerTab === 'browser'} className={debuggerTab === 'browser' ? 'active' : ''} onClick={() => setDebuggerTab('browser')}>Browser Evidence</button>
                    )}
                  </div>
                  <div className="debugger-center-body">
                    {debuggerTab === 'browser' && isBrowserScript ? (
                      <BrowserReplayDebugger
                        sessionId={activeReplaySessionId}
                        session={browserReplaySession}
                        report={replayReport}
                        running={replaying}
                        selectedEventIndex={expandedReplayEvent}
                        onSelectEvent={setExpandedReplayEvent}
                      />
                    ) : !replayReport ? (
                      <div className="debugger-empty">No debugger session yet. Place optional breakpoints in the gutter, adjust settings, then press Run.</div>
                    ) : selectedReplayEvent && selectedReplayEventIndex != null ? (
                      (() => {
                        const event = selectedReplayEvent
                        const index = selectedReplayEventIndex
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
                              <span>{event.duration_ms.toFixed(1)} ms</span>
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
                                      <div className="replay-repair-suggestions">
                                        <span className="replay-detail-label">Draft repair suggestions</span>
                                        {(event.repair_suggestions || []).map(suggestion => (
                                          <div key={suggestion.id} className="replay-repair-suggestion">
                                            <strong>{suggestion.title}</strong>
                                            <span>{suggestion.description}</span>
                                          </div>
                                        ))}
                                      </div>
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
                              <span className="replay-event-phase">{formatDebugPhaseLabel(ca.phase)} {ca.action_index + 1}</span>
                              <strong>{ca.action_name || 'Next action'}</strong>
                              <span className="replay-event-target">{ca.method || ca.action_type} {ca.target || ca.selector}</span>
                            </div>
                            {isReplayPaused && <p className="debugger-current-hint">Press Step (F10) to execute this action, or Continue (F5) to run to the next breakpoint.</p>}
                          </div>
                        )
                      })()
                    ) : (
                      <div className="debugger-empty">Select an action from the list to inspect its request and response.</div>
                    )}
                  </div>
                </section>

                <aside className="debugger-variables-panel" aria-label="Variables">
                  <div
                    className={`debugger-resizer debugger-resizer-col debugger-resizer-vars ${activeDebuggerResize === 'vars' ? 'active' : ''}`}
                    onKeyDown={e => handleDebuggerResizeKeyDown('vars', e)}
                    onMouseDown={e => startDebuggerDrag('vars', e)}
                    role="separator"
                    tabIndex={0}
                    title="Resize variables panel"
                    aria-label="Resize variables panel"
                    aria-orientation="vertical"
                    aria-valuemax={DEBUGGER_LAYOUT_LIMITS.vars.max}
                    aria-valuemin={DEBUGGER_LAYOUT_LIMITS.vars.min}
                    aria-valuenow={debuggerVarsWidth}
                  />
                  <div className="debugger-pane-header">{replaying ? 'Live Variables' : 'Variables'}</div>
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
                </aside>

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
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'console'} className={debuggerBottomTab === 'console' ? 'active' : ''} onClick={() => setDebuggerBottomTab('console')}>Console</button>
                    <button type="button" role="tab" aria-selected={debuggerBottomTab === 'timeline'} className={debuggerBottomTab === 'timeline' ? 'active' : ''} onClick={() => setDebuggerBottomTab('timeline')}>Timeline</button>
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
                            const canJump = jumpPosition != null && !!replayReport?.events[jumpPosition]
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
                          {actionProblemRows.map(({ issue, actionIndex }) => (
                            <button
                              key={issue.id}
                              type="button"
                              className={`debugger-problem-row ${issue.severity} action-problem`}
                              onClick={() => {
                                setActiveAction(actionIndex)
                                activateWorkspaceTab('author')
                              }}
                            >
                              <span>Action {actionIndex + 1}</span>
                              <strong>{script.actions[actionIndex]?.name || issue.message}</strong>
                              <small>{script.actions[actionIndex]?.name ? issue.message : ''}</small>
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
                <span className="debugger-statusbar-item">{replayReport ? `${replayReport.events.length} / ${script.actions.length} actions` : `${debugActionRows.length} actions planned`}</span>
                {replayReport?.completed_at && <span className="debugger-statusbar-item">Completed {formatReplayTime(replayReport.completed_at)}</span>}
                {debugBreakpoints.length > 0 && <span className="debugger-statusbar-item">{debugBreakpoints.length} breakpoint{debugBreakpoints.length === 1 ? '' : 's'}</span>}
              </div>
            </div>
          </div>

          {importMetadata && (
            <div className={`import-metadata-panel config-panel editor-support-dialog support-dialog-import ${openSections['import'] ? 'open' : ''}`} style={{ padding: 0 }} role="dialog" aria-modal="true" aria-label="Imported Metadata">
              <button className="panel-header-toggle" onClick={() => toggleSection('import')} style={{ padding: '16px 20px' }}>
                <div className="import-metadata-header" style={{ marginBottom: 0 }}>
                  <div>
                    <h3 className="panel-title">Imported Metadata</h3>
                  </div>
                  <span className="import-source-chip">{importMetadata.source || 'loadrunner'}</span>
                </div>
                <span className="chevron">Close</span>
              </button>

              {(
                <div className="panel-body" style={{ padding: '0 20px 20px' }}>
                  <p style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 14 }}>This script retains source-specific import metadata that APEX Load uses during review but does not edit directly.</p>
                  <div className="import-metadata-grid">
                    <div className="import-metadata-card">
                      <span>Source Script</span>
                      <strong>{importMetadata.source_script_name || script.name}</strong>
                    </div>
                    <div className="import-metadata-card">
                      <span>Project Sections</span>
                      <strong>{importSummary.projectSections.length}</strong>
                      <small>{importSummary.projectSections.join(', ') || 'None captured'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Runtime Files</span>
                      <strong>{importSummary.runtimeFiles.length}</strong>
                      <small>{importSummary.runtimeFiles.join(', ') || 'None found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Unsupported Functions</span>
                      <strong>{importSummary.unsupportedFunctions.length}</strong>
                      <small>{importSummary.unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Parameter Files</span>
                      <strong>{importSummary.parameterFiles.length}</strong>
                      <small>{importSummary.parameterFiles.join(', ') || 'None found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Correlation Suggestions</span>
                      <strong>{importSummary.correlationSuggestions.length}</strong>
                      <small>{importSummary.correlationSuggestions.length > 0 ? 'Imported from the conversion preview' : 'None detected'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Generated Data Sources</span>
                      <strong>{importedDataSourceSummaries.length}</strong>
                      <small>{importedDataSourceSummaries.length > 0 ? importedDataSourceSummaries.map((source) => source.name).join(', ') : 'None generated'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Runtime Setting Parity</span>
                      <strong>{importSummary.runtimeSettingsParity.length}</strong>
                      <small>{runtimeSettingParitySummary || 'No classified runtime settings found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Migration Readiness</span>
                      <strong>{importSummary.runtimeSettingReadiness ? `${importSummary.runtimeSettingReadiness.score_pct}%` : 'N/A'}</strong>
                      <small>{runtimeSettingReadinessSummary || 'No migration readiness score captured'}</small>
                    </div>
                  </div>

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
                      <p>These imported LoadRunner runtime settings were classified against APEX Load&apos;s code-owned support matrix.</p>
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
                                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => setActiveAction(foundAtActionIndex)}>
                                    Open {correlation.found_at}
                                  </button>
                                )}
                                {usedAtTargets.map(({ location, actionIndex }) => (
                                  <button key={`${correlation.name}:${location}`} className="btn btn-secondary btn-sm" type="button" onClick={() => setActiveAction(actionIndex)}>
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

                  {importSummary.parameterFileSections.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Parameter File Mappings</h4>
                      <p>These preserved LoadRunner parameter definitions show how the converter mapped file-backed data into APEX Load data sources.</p>
                      <div className="import-correlation-list">
                        {importedParameterFileMappings.map(({ section, sourceName }) => (
                          <div key={`${section.file}:${section.name}`} className="import-correlation-card">
                            <strong>{section.file}: {section.name}</strong>
                            <span>{formatImportParameterMapping(section.values)}</span>
                            {sourceName && <span>Generated source: {sourceName}</span>}
                            {sourceName && (
                              <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedDataSourceReview(sourceName)}>
                                  Open Mapped Data Source
                                </button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {importedDataSourceSummaries.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Generated Data Sources</h4>
                      <p>These data sources were created from the imported parameter files and are ready for review in the editor.</p>
                      <div className="import-correlation-list">
                        {importedDataSourceSummaries.map((source) => (
                          <div key={source.name} className="import-correlation-card">
                            <strong>{source.name}</strong>
                            <span>{source.type} • {source.rows} row{source.rows === 1 ? '' : 's'}</span>
                            <span>{typeof source.columns === 'number'
                              ? `${source.columns} column${source.columns === 1 ? '' : 's'}`
                              : source.columns.join(', ') || 'No columns'}</span>
                            <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                              <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedDataSourceReview(source.name)}>
                                Open In Parameter Data
                              </button>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {sourceReview && (
                    <SourceMappingReview sourceReview={sourceReview} script={script} title="Source Mapping" />
                  )}

                  <details className="import-metadata-details">
                    <summary>View preserved metadata JSON</summary>
                    <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
                  </details>
                </div>
              )}
            </div>
          )}

          {objectDescriptorReview.rows.length > 0 && (
            <div className={`config-panel editor-support-dialog support-dialog-object ${openSections['object-descriptors'] ? 'open' : ''}`} style={{ padding: 0 }} role="dialog" aria-modal="true" aria-label="Object Descriptors">
              <button className="panel-header-toggle" onClick={() => toggleSection('object-descriptors')} style={{ padding: '16px 20px' }}>
                <div className="panel-title-row" style={{ marginBottom: 0 }}>
                  <div>
                    <h3 className="panel-title">Object Descriptors</h3>
                    <p>Review imported TruClient object IDs, current selectors, and extracted fallback candidates.</p>
                  </div>
                </div>
                <span className="chevron">Close</span>
              </button>
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
                          <button type="button" className="btn btn-sm btn-secondary" onClick={() => setActiveAction(row.actionIndex)}>
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
            </div>
          )}

          {isBrowserScript && (
            <div className={`config-panel editor-support-dialog support-dialog-conversion ${openSections['http-conversion'] ? 'open' : ''}`} style={{ padding: 0 }} role="dialog" aria-modal="true" aria-label="Browser To HTTP Conversion Review">
              <button className="panel-header-toggle" onClick={() => toggleSection('http-conversion')} style={{ padding: '16px 20px' }}>
                <div className="panel-title-row" style={{ marginBottom: 0 }}>
                  <div>
                    <h3 className="panel-title">Browser To HTTP Conversion Review</h3>
                    <p>Gate generated HTTP load scripts behind replay, network, correlation, and approval evidence.</p>
                  </div>
                </div>
                <span className="chevron">Close</span>
              </button>
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
            </div>
          )}

          {/* Runtime Settings Panels */}
          <div className={`runtime-settings config-panel editor-support-dialog support-dialog-runtime ${openSections['runtime'] ? 'open' : ''}`} style={{ padding: 0 }} role="dialog" aria-modal="true" aria-label="Runtime Settings">
            <button className="panel-header-toggle" onClick={() => toggleSection('runtime')} style={{ padding: '16px 20px', borderBottom: openSections['runtime'] ? '1px solid var(--border)' : 'none' }}>
              <h3 className="panel-title">Runtime Settings & RTS</h3>
              <span className="chevron">Close</span>
            </button>
            {(
              <div className="runtime-settings-grid" style={{ padding: 20 }}>
                {/* Think Time */}
                <div className={`settings-section ${openSections['think'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('think')}>
                    <span className="section-icon">{'\u23f1'}</span>
                    <span className="section-label">Think Time</span>
                    <span className="section-summary">
                      {script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} — ${script.config.think_time?.mean_ms || 0}ms`}
                    </span>
                    <span className="chevron">{openSections['think'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['think'] && (
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
                          <span>Mean (ms)</span>
                          <input type="number" value={script.config.think_time?.mean_ms || 0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, mean_ms: Number(e.target.value) })} />
                        </label>
                        {script.config.think_time?.type === 'gaussian' && (
                          <label>
                            <span>Std Dev (ms)</span>
                            <input type="number" value={script.config.think_time?.std_ms || 0}
                              onChange={e => updateField('config.think_time', { ...script.config.think_time, std_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {(script.config.think_time?.type === 'uniform' || script.config.think_time?.type === 'gaussian') && (
                          <>
                            <label>
                              <span>Min (ms)</span>
                              <input type="number" value={script.config.think_time?.min_ms || 0}
                                onChange={e => updateField('config.think_time', { ...script.config.think_time, min_ms: Number(e.target.value) })} />
                            </label>
                            <label>
                              <span>Max (ms)</span>
                              <input type="number" value={script.config.think_time?.max_ms || 0}
                                onChange={e => updateField('config.think_time', { ...script.config.think_time, max_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                        <label>
                          <span>Multiplier</span>
                          <input type="number" step="0.1" value={script.config.think_time?.multiplier || 1.0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, multiplier: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Limit (ms)</span>
                          <input type="number" value={script.config.think_time?.limit_ms || 0} placeholder="0 = no limit"
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, limit_ms: Number(e.target.value) })} />
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.think_time?.ignore || false}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, ignore: e.target.checked })} />
                          <span>Ignore all think times</span>
                        </label>
                      </div>
                    </div>
                  )}
                </div>

                {/* Pacing */}
                <div className={`settings-section ${openSections['pacing'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('pacing')}>
                    <span className="section-icon">{'\ud83d\udd04'}</span>
                    <span className="section-label">Iteration Pacing</span>
                    <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
                    <span className="chevron">{openSections['pacing'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['pacing'] && (
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
                          <label><span>Delay (ms)</span>
                            <input type="number" value={script.config.pacing?.delay_ms || 0}
                              onChange={e => updateField('config.pacing', { ...script.config.pacing, delay_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_delay' && (
                          <>
                            <label><span>Min Delay (ms)</span>
                              <input type="number" value={script.config.pacing?.min_delay_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, min_delay_ms: Number(e.target.value) })} />
                            </label>
                            <label><span>Max Delay (ms)</span>
                              <input type="number" value={script.config.pacing?.max_delay_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, max_delay_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                        {script.config.pacing?.type === 'fixed_interval' && (
                          <label><span>Interval (ms)</span>
                            <input type="number" value={script.config.pacing?.interval_ms || 0}
                              onChange={e => updateField('config.pacing', { ...script.config.pacing, interval_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_interval' && (
                          <>
                            <label><span>Min Interval (ms)</span>
                              <input type="number" value={script.config.pacing?.min_interval_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, min_interval_ms: Number(e.target.value) })} />
                            </label>
                            <label><span>Max Interval (ms)</span>
                              <input type="number" value={script.config.pacing?.max_interval_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, max_interval_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {/* Run Logic */}
                <div className={`settings-section ${openSections['runlogic'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('runlogic')}>
                    <span className="section-icon">{'\ud83d\udd01'}</span>
                    <span className="section-label">Run Logic</span>
                    <span className="section-summary">
                      {script.config.run_logic?.global_iterations
                        ? `${script.config.run_logic.global_iterations} shared iterations`
                        : script.config.run_logic?.max_transactions
                          ? `${script.config.run_logic.max_transactions} max transactions`
                          : script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
                    </span>
                    <span className="chevron">{openSections['runlogic'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['runlogic'] && (
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
                <div className={`settings-section ${openSections['errors'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('errors')}>
                    <span className="section-icon">{'\u26a0\ufe0f'}</span>
                    <span className="section-label">Error Handling</span>
                    <span className="section-summary">
                      {script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}
                    </span>
                    <span className="chevron">{openSections['errors'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['errors'] && (
                    <div className="section-body">
                      <div className="form-grid">
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
                        <label>
                          <span>Max Retries</span>
                          <input type="number" value={script.config.error_handling?.max_retries || 0}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
                        </label>
                        {(script.config.error_handling?.max_retries || 0) > 0 && (
                          <>
                            <label>
                              <span>Retry Delay (ms)</span>
                              <input type="number" value={script.config.error_handling?.retry_delay_ms || 1000}
                                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: Number(e.target.value) })} />
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
                  <div className={`settings-section ${openSections['jms'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('jms')}>
                      <span className="section-icon">JMS</span>
                      <span className="section-label">Solace JMS</span>
                      <span className="section-summary">
                        {script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}
                      </span>
                      <span className="chevron">{openSections['jms'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['jms'] && (
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
                            <label><span>Timeout (ms)</span>
                              <input type="number" value={script.config.jms?.timeout_ms || 5000}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', timeout_ms: Number(e.target.value) })} /></label>
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
                  <div className={`settings-section ${openSections['websocket'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('websocket')}>
                      <span className="section-icon">WS</span>
                      <span className="section-label">WebSocket Runtime</span>
                      <span className="section-summary">{script.config.websocket?.reconnect ? 'Reconnect enabled' : 'Single connection'}</span>
                      <span className="chevron">{openSections['websocket'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['websocket'] && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Read Timeout (ms)</span>
                            <input type="number" value={script.config.websocket?.read_timeout_ms || 30000}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, read_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>Write Timeout (ms)</span>
                            <input type="number" value={script.config.websocket?.write_timeout_ms || 10000}
                              onChange={e => updateField('config.websocket', { ...script.config.websocket, write_timeout_ms: Number(e.target.value) })} /></label>
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
                  <div className={`settings-section ${openSections['grpc'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('grpc')}>
                      <span className="section-icon">gRPC</span>
                      <span className="section-label">gRPC Runtime</span>
                      <span className="section-summary">{script.config.grpc?.use_reflection ? 'Reflection' : script.config.grpc?.proto_files?.length ? 'Proto files' : 'Contract pending'}</span>
                      <span className="chevron">{openSections['grpc'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['grpc'] && (
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
                          <label><span>Timeout (ms)</span>
                            <input type="number" value={script.config.grpc?.timeout_ms || 30000}
                              onChange={e => updateField('config.grpc', { ...script.config.grpc, timeout_ms: Number(e.target.value) })} /></label>
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
                  <div className={`settings-section ${openSections['database'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('database')}>
                      <span className="section-icon">DB</span>
                      <span className="section-label">Database Runtime</span>
                      <span className="section-summary">{script.config.database?.dsn_secret ? 'Secret-backed DSN' : script.config.database?.dsn ? 'DSN configured' : 'Not configured'}</span>
                      <span className="chevron">{openSections['database'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['database'] && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Query Timeout (ms)</span>
                            <input type="number" value={script.config.database?.query_timeout_ms || 30000}
                              onChange={e => updateField('config.database', { ...script.config.database, query_timeout_ms: Number(e.target.value) })} /></label>
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
                  <div className={`settings-section ${openSections['network'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('network')}>
                      <span className="section-icon">NET</span>
                      <span className="section-label">Network Runtime</span>
                      <span className="section-summary">{script.config.network?.subprotocol || 'tcp'} · {script.config.network?.host || script.config.network?.address || 'host pending'}</span>
                      <span className="chevron">{openSections['network'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['network'] && (
                      <div className="section-body">
                        <div className="form-grid">
                          <label><span>Timeout (ms)</span>
                            <input type="number" value={script.config.network?.timeout_ms || 30000}
                              onChange={e => updateField('config.network', { ...script.config.network, timeout_ms: Number(e.target.value) })} /></label>
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
                {isHttpTransportScript && <div className={`settings-section ${openSections['http'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('http')}>
                    <span className="section-icon">{'\ud83c\udf10'}</span>
                    <span className="section-label">HTTP Transport</span>
                    <span className="section-summary">
                      {httpProxySummary !== 'Default' ? httpProxySummary : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
                    </span>
                    <span className="chevron">{openSections['http'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['http'] && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Timeouts</h5>
                        <div className="form-grid">
                          <label><span>Connect (ms)</span>
                            <input type="number" value={script.config.http?.connect_timeout_ms || 30000}
                              onChange={e => updateField('config.http', { ...script.config.http, connect_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>Request (ms)</span>
                            <input type="number" value={script.config.http?.request_timeout_ms || 60000}
                              onChange={e => updateField('config.http', { ...script.config.http, request_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>TLS Handshake (ms)</span>
                            <input type="number" value={script.config.http?.tls_timeout_ms || 10000}
                              onChange={e => updateField('config.http', { ...script.config.http, tls_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>Idle Connection (ms)</span>
                            <input type="number" value={script.config.http?.idle_timeout_ms || 90000}
                              onChange={e => updateField('config.http', { ...script.config.http, idle_timeout_ms: Number(e.target.value) })} /></label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
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
                      </div>
                      <div className="settings-sub-group">
                        <h5>TLS / SSL</h5>
                        <div className="form-grid">
                          <label><span>Min TLS Version</span>
                            <select value={script.config.http?.tls?.min_version || '1.2'}
                              onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                              <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                              <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
                            </select></label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                              onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                            <span>Skip certificate verification</span>
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
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
                      </div>
                      <div className="settings-sub-group">
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
                      </div>
                      <div className="settings-sub-group">
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
                          <label><span>Slow Mo (ms)</span>
                            <input type="number" min="0" value={script.config.browser?.slow_mo_ms ?? ''} placeholder="50"
                              onChange={e => updateField('config.browser', { ...script.config.browser, slow_mo_ms: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0) })} /></label>
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
                    </div>
                  )}
                </div>}

                {isSoapScript && <div className={`settings-section ${openSections['soap'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('soap')}>
                    <span className="section-icon">SOAP</span>
                    <span className="section-label">SOAP Settings</span>
                    <span className="section-summary">
                      {script.config.soap?.ws_security?.username_token?.username
                        ? 'WS-Security configured'
                        : script.config.soap?.version || '1.1'}
                    </span>
                    <span className="chevron">{openSections['soap'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['soap'] && (
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
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.soap?.fault_handling?.fail_on_fault ?? true} onChange={e => updateField('config.soap.fault_handling.fail_on_fault', e.target.checked)} />
                            <span>Fail on SOAP Fault</span>
                          </label>
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

                {isJavaHttpScript && <div className={`settings-section ${openSections['java_http'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('java_http')}>
                    <span className="section-icon">JAVA</span>
                    <span className="section-label">Java HTTP Settings</span>
                    <span className="section-summary">{`${script.config.java_http?.codec || 'java_serialized_object_base64'} · ${script.config.java_http?.helper_mode || 'embedded'}`}</span>
                    <span className="chevron">{openSections['java_http'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['java_http'] && (
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

                {isHttpScript && <div className={`settings-section ${openSections['web_runtime'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('web_runtime')}>
                    <span className="section-icon">RTS</span>
                    <span className="section-label">Web HTTP/HTML Compatibility</span>
                    <span className="section-summary">
                      {httpProxySummary !== 'Default'
                        ? httpProxySummary
                        : script.config.web_runtime?.content_checks?.length
                        ? `${script.config.web_runtime.content_checks.length} content checks`
                        : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
                    </span>
                    <span className="chevron">{openSections['web_runtime'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['web_runtime'] && (
                    <div className="section-body">
                      <div className="cell-muted" style={{ marginBottom: 12 }}>
                        These settings are captured in the draft now so Web HTTP/HTML scripts can mirror VuGen RTS coverage even where full APEX Load execution support is still rolling out.
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
                            <span>Latency (ms)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.latency_ms || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  latency_ms: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Jitter (ms)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.jitter_ms || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  jitter_ms: Number(e.target.value),
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
                              <span>Latency (ms)</span>
                              <input type="number" aria-label="Profile latency" value={profile.latency_ms || 0}
                                onChange={e => updateField('config.web_runtime', {
                                  ...script.config.web_runtime,
                                  network_profiles: (script.config.web_runtime?.network_profiles || []).map((p, idx) => idx === i ? { ...p, latency_ms: Number(e.target.value) } : p),
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
                            <span>Buffer Size (seconds)</span>
                            <input type="number" value={script.config.web_runtime?.streaming?.buffer_size_seconds || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  buffer_size_seconds: Number(e.target.value),
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
                <div className={`settings-section ${openSections['log'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('log')}>
                    <span className="section-icon">{'\ud83d\udccb'}</span>
                    <span className="section-label">Logging</span>
                    <span className="section-summary">{script.config.log?.level || 'standard'}</span>
                    <span className="chevron">{openSections['log'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['log'] && (
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
                        <div className="logging-checkbox-group">
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
            )}
          </div>

          {/* Actions List + Detail */}
          {workspaceTab === 'json' && (
            <div className="json-editor script-ide-json-editor" role="tabpanel" aria-label="JSON script editor">
              <textarea
                value={jsonText}
                onChange={event => setJsonText(event.target.value)}
                spellCheck={false}
                aria-label="Script JSON"
              />
            </div>
          )}

          {workspaceTab === 'author' && (
          <div className="actions-layout" role="tabpanel" aria-label="Authoring workspace">
            <div className="actions-content-grid">
              <div className="actions-sidebar">
                <div className="editor-outline-nav">
                  <div className="editor-outline-header">
                    <strong>Outline</strong>
                    <span>{script.actions.length} actions</span>
                  </div>
                  <div className="editor-outline-list">
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('config')}>Script Configuration</button>
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('runtime')}>Runtime / RTS</button>
                    {importMetadata && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('import')}>Imported Metadata</button>}
                    {objectDescriptorReview.rows.length > 0 && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('object-descriptors')}>Object Descriptors</button>}
                    <button className="editor-outline-link" type="button" onClick={() => openEditorSection('parameter-data')}>Parameter Data</button>
                    {isBrowserScript && <button className="editor-outline-link" type="button" onClick={() => openEditorSection('http-conversion')}>HTTP Conversion Review</button>}
                    {debugSessionAvailable && <button className="editor-outline-link" type="button" onClick={() => activateWorkspaceTab('debug')}>Debug Session</button>}
                  </div>
                  <div className="editor-outline-metrics" aria-label="Script outline summary">
                    <span><strong>{outlineSummary.browserActionCount || outlineSummary.actionCount}</strong> {isBrowserScript ? 'browser' : 'actions'}</span>
                    <span><strong>{outlineSummary.transactionCount}</strong> txn</span>
                    <span><strong>{outlineSummary.parameterRefCount}</strong> params</span>
                    <span><strong>{outlineSummary.waitCount}</strong> waits</span>
                    <span><strong>{outlineSummary.assertionCount}</strong> checks</span>
                  </div>
                  <div className="editor-step-outline" aria-label="Graphical step outline">
                    {outlineSummary.actions.map((item) => (
                      <button
                        key={`${item.index}-${item.name}`}
                        type="button"
                        className={`editor-step-outline-row ${activeAction === item.index ? 'active' : ''}`}
                        onClick={() => setActiveAction(item.index)}
                      >
                        <span className="editor-step-outline-index">{item.index + 1}</span>
                        <span className="editor-step-outline-main">
                          <strong>{item.name}</strong>
                          <small>{item.type}{item.target ? ` • ${item.target}` : ''}</small>
                        </span>
                        <span className="editor-step-outline-badges">
                          {item.hasTransaction && <span className="outline-badge txn">Txn</span>}
                          {item.parameterRefs.length > 0 && <span className="outline-badge param">P{item.parameterRefs.length}</span>}
                          {item.hasWait && <span className="outline-badge wait">Wait</span>}
                          {item.assertionCount > 0 && <span className="outline-badge assert">A{item.assertionCount}</span>}
                        </span>
                      </button>
                    ))}
                    {outlineSummary.actions.length === 0 && (
                      <div className="editor-step-outline-empty">Add actions to build the executable outline.</div>
                    )}
                  </div>
                </div>
                <div className="actions-sidebar-header">
                  <h3 className="panel-title" style={{ fontSize: 11 }}>Action List</h3>
                  <div className="actions-sidebar-controls">
                    <button className="btn btn-sm btn-ghost" onClick={() => setIsParameterDialogOpen(true)}>Parameter Data</button>
                    <button className="btn btn-sm btn-accent" onClick={addAction}>+ Add</button>
                  </div>
                </div>
                <div className="action-list">
                  {script.actions.map((a, i) => {
                    const actionBreakpointActive = debugBreakpointKeys.has(debugBreakpointKey('action', i))
                    const actionBreakpointLabel = `${actionBreakpointActive ? 'Remove' : 'Set'} breakpoint on action ${i + 1}`
                    return (
                      <div key={i} className={`action-item ${activeAction === i ? 'active' : ''} ${actionBreakpointActive ? 'has-breakpoint' : ''}`} onClick={() => setActiveAction(i)}>
                        <button
                          type="button"
                          className={`action-breakpoint-button ${actionBreakpointActive ? 'active' : ''}`}
                          aria-label={actionBreakpointLabel}
                          title={actionBreakpointLabel}
                          onClick={event => {
                            event.stopPropagation()
                            void toggleDebugBreakpoint('action', i)
                          }}
                        >
                          <span aria-hidden="true" />
                        </button>
                        <span className="action-method" style={{ color: methodColor(a.type) }}>{a.type}</span>
                        <span className="action-name-text">{a.name || 'Unnamed'}</span>
                        <div className="action-item-signals">
                          {(a.extractors || []).length > 0 && <span className="action-signal-pill">E{a.extractors?.length || 0}</span>}
                          {actionIssueBuckets[i]?.length > 0 && (
                            <span className={`action-signal-pill ${actionIssueBuckets[i].some((issue) => issue.severity === 'error') ? 'error' : 'warning'}`}>
                              {actionIssueBuckets[i].length}
                            </span>
                          )}
                        </div>
                        <div className="action-item-actions">
                          <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, -1) }} disabled={i === 0}>↑</button>
                          <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, 1) }} disabled={i === script.actions.length - 1}>↓</button>
                          <button className="btn-icon danger" onClick={e => { e.stopPropagation(); removeAction(i) }}>✕</button>
                        </div>
                      </div>
                    )
                  })}
                  {script.actions.length === 0 && (
                    <div className="action-empty">No actions yet. Click "+ Add" to create one.</div>
                  )}
                </div>
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
              </div>

              {/* Action Detail */}
              <div className="action-detail">
                {activeAction !== null && script.actions[activeAction] ? (
                  <ActionEditor
                    action={script.actions[activeAction]}
                    index={activeAction}
                    parameters={script.parameters || []}
                    onUpdate={updateAction}
                    onAddExtractor={addExtractor}
                    onUpdateExtractor={updateExtractor}
                    onRemoveExtractor={removeExtractor}
                    onAddAssertion={addAssertion}
                    onUpdateAssertion={updateAssertion}
                    onRemoveAssertion={removeAssertion}
                    onAddHeader={addHeader}
                    onUpdateHeader={updateHeader}
                    onRemoveHeader={removeHeader}
                    onAddMetadata={addMetadata}
                    onUpdateMetadata={updateMetadata}
                    onRemoveMetadata={removeMetadata}
                    onBeginParameterEdit={beginParameterEditFromActionEditor}
                    methodColor={methodColor}
                    protocol={script.protocol}
                    isBrowserScript={isBrowserScript}
                    isJmsScript={isJmsScript}
                    isSoapScript={isSoapScript}
                    issues={actionIssueBuckets[activeAction] || []}
                  />
                ) : (
                  <div className="action-placeholder">
                    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
                    <p>Select an action to edit its details</p>
                  </div>
                )}
              </div>

              <div className="editor-utility-sidebar">
                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Authoring Signals</strong>
                    <span>{errorCount} error{errorCount === 1 ? '' : 's'} • {warningCount} warning{warningCount === 1 ? '' : 's'}</span>
                  </div>
                  {scriptLevelIssues.length > 0 ? (
                    <ul className="utility-list">
                      {scriptLevelIssues.map((issue) => (
                        <li key={issue.id} className={`utility-list-item ${issue.severity}`}>{issue.message}</li>
                      ))}
                    </ul>
                  ) : (
                    <div className="utility-empty-state">No script-level issues detected.</div>
                  )}
                </div>

                <div className="utility-card debug-readiness-card">
                  <div className="utility-card-header">
                    <strong>Debug Readiness</strong>
                    <span>{replayPreflightStatusLabel(replayPreflight)}</span>
                  </div>
                  <ReplayPreflightCard preflight={replayPreflight} />
                </div>

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Convergence Checklist</strong>
                    <span>validate • repair • debug evidence • save</span>
                  </div>
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
                </div>

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Guided History</strong>
                    <span>{editorHistory.past.length} undo • {editorHistory.future.length} redo</span>
                  </div>
                  <div className="utility-inline-actions">
                    <button type="button" className="btn btn-sm btn-secondary" onClick={undoEditorChange} disabled={editorHistory.past.length === 0}>Undo</button>
                    <button type="button" className="btn btn-sm btn-secondary" onClick={redoEditorChange} disabled={editorHistory.future.length === 0}>Redo</button>
                  </div>
                  <div className="utility-empty-state">Tracks guided component, repair, parameter, and runtime changes.</div>
                </div>

                <div className="utility-card">
                  <div className="utility-card-header">
                    <strong>Flow Assembly</strong>
                    <span>{componentLibraryStatus === 'ready' ? `${authoringComponents.length} blocks` : componentLibraryStatus}</span>
                  </div>
                  {authoringComponents.length > 0 ? (
                    <div className="utility-stack">
                      {AUTHORING_COMPONENT_GROUPS.map(group => {
                        const groupComponents = authoringComponents.filter(component => component.kind === group.kind)
                        if (groupComponents.length === 0) return null
                        return (
                          <div key={group.kind} className="utility-subsection">
                            <strong>{group.label}</strong>
                            <div className="utility-stack">
                              {groupComponents.map(component => (
                                <div key={component.id} className="utility-suggestion-card">
                                  <strong>{component.name}</strong>
                                  <p>{component.summary}</p>
                                  {component.usage_notes?.length ? <p>{component.usage_notes[0]}</p> : null}
                                  <button type="button" className="btn btn-sm btn-secondary" onClick={() => insertAuthoringComponent(component)}>
                                    Insert Block
                                  </button>
                                </div>
                              ))}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  ) : (
                    <div className="utility-empty-state">
                      {componentLibraryStatus === 'error' ? 'Component library is unavailable.' : 'No reusable blocks match this protocol yet.'}
                    </div>
                  )}
                </div>

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
                      <span className="action-signal-pill">{draftDiffSummary.actionChanges} action</span>
                      <span className="action-signal-pill">{draftDiffSummary.parameterChanges} parameter</span>
                      <span className="action-signal-pill">{draftDiffSummary.dataSourceChanges} data</span>
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
                          <pre>{JSON.stringify(reviewBaselineScript, null, 2)}</pre>
                        </div>
                        <div>
                          <strong>Current Draft</strong>
                          <pre>{JSON.stringify(script, null, 2)}</pre>
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
                              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setActiveAction(foundAtActionIndex)}>
                                Open source
                              </button>
                            )}
                            {usedAtTargets.slice(0, 2).map((target) => (
                              <button key={`${correlation.name}:${target.location}`} type="button" className="btn btn-sm btn-ghost" onClick={() => setActiveAction(target.actionIndex)}>
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
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => setIsParameterDialogOpen(true)}>
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
                            <button type="button" className="utility-link-button" onClick={() => setActiveAction(item.actionIndex)}>
                              {item.label}
                            </button>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>
          )}

          {isParameterDialogOpen && (
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
              onApplyAutoParameterization={applyAutoParameterization}
              onApplyPendingParameterDraft={applyPendingParameterDraft}
              onAutoFixDataSource={autoFixDataSource}
              onClearPendingParameterDraft={clearPendingParameterDraft}
              onClose={closeParameterDialog}
              onExportDataSourceFile={exportDataSourceFile}
              onImportDataSourceFile={importDataSourceFile}
              onMergeParameterSources={mergeParameterSourceFiles}
              onRedoDataSource={redoDataSource}
              onRemoveDataColumn={removeDataColumn}
              onRemoveDataRow={removeDataRow}
              onRemoveDataSource={removeDataSource}
              onRemoveParameter={removeParameter}
              onSortDataRows={sortDataRows}
              onOpenDataEditor={openDataEditorAt}
              onToggleDataEditorMode={toggleDataEditorModeAt}
              onToggleOpenDataEditor={toggleOpenDataEditor}
              onUndoDataSource={undoDataSource}
              onUpdateDataCell={updateDataCell}
              onUpdateDataColumnName={updateDataColumnName}
              onUpdateDataSource={updateDataSource}
              onUpdateParameter={updateParameter}
              onUpdateParameterCandidate={updateCandidate}
              onUpdateParameterDraftName={updatePendingParameterDraftName}
              onUpdateParameterDraftValue={updatePendingParameterDraftValue}
              onUpdateParameterType={updateParameterType}
              onScanForAutoParameters={scanForAutoParameters}
              openDataEditor={openDataEditor}
              parameterCandidates={parameterCandidates}
              pendingParameterDraft={pendingParameterDraft}
              script={script}
            />
          )}

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

function formatImportParameterMapping(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.join(' • ') || 'No preserved mapping details'
}

interface ReplayScreenshotDialogProps {
  alt: string
  onClose: () => void
  src: string
}

function ReplayScreenshotDialog({ alt, onClose, src }: ReplayScreenshotDialogProps) {
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
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
  onApplyAutoParameterization: () => void
  onApplyPendingParameterDraft: () => void
  onAutoFixDataSource: (idx: number) => void
  onClearPendingParameterDraft: () => void
  onClose: () => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onMergeParameterSources: () => void
  onOpenDataEditor: (idx: number) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onRemoveParameter: (idx: number) => void
  onSortDataRows: (idx: number, colIdx: number, direction: DataSourceSortDirection) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onUpdateParameterCandidate: (id: string, patch: Partial<ParameterCandidate>) => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
  onUpdateParameterType: (idx: number, nextType: string) => void
  onScanForAutoParameters: () => void
  openDataEditor: number | null
  parameterCandidates: ParameterCandidate[]
  pendingParameterDraft: ActionEditorParameterDraft | null
  script: ScriptData
}

function ParameterDataDialog({
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddDataSource,
  onAddParameter,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onApplyAutoParameterization,
  onApplyPendingParameterDraft,
  onAutoFixDataSource,
  onClearPendingParameterDraft,
  onClose,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onMergeParameterSources,
  onOpenDataEditor,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onRemoveParameter,
  onSortDataRows,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  onUpdateParameter,
  onUpdateParameterCandidate,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
  onUpdateParameterType,
  onScanForAutoParameters,
  openDataEditor,
  parameterCandidates,
  pendingParameterDraft,
  script,
}: ParameterDataDialogProps) {
  const [isNamedParametersOpen, setIsNamedParametersOpen] = useState(true)
  const [isDataSourcesOpen, setIsDataSourcesOpen] = useState(true)
  const mergeSummary = useMemo(() => summarizeParameterSourceMerge(script), [script])
  const [selectedParameterIdx, setSelectedParameterIdx] = useState<number | null>(() => {
    if (openDataEditor === null) return null
    return findFirstParameterIndexByDataSource(script.parameters, script.data_sources?.[openDataEditor]?.name)
  })
  const focusedParameterSourceName = openDataEditor !== null
    ? script.data_sources?.[openDataEditor]?.name || null
    : null
  const selectedParameter = selectedParameterIdx !== null ? script.parameters?.[selectedParameterIdx] || null : null
  useEffect(() => {
    if (selectedParameterIdx === null) return
    if (script.parameters?.[selectedParameterIdx]) return
    setSelectedParameterIdx(null)
  }, [script.parameters, selectedParameterIdx])
  useEffect(() => {
    if (openDataEditor === null) return
    const sourceName = script.data_sources?.[openDataEditor]?.name
    const parameterIndex = findFirstParameterIndexByDataSource(script.parameters, sourceName)
    if (parameterIndex === null) return

    setSelectedParameterIdx(previous => {
      const currentParameter = previous !== null ? script.parameters?.[previous] : null
      if (currentParameter && normalizeParameterDataLookup(currentParameter.data_source) === normalizeParameterDataLookup(sourceName)) {
        return previous
      }
      return parameterIndex
    })
  }, [openDataEditor, script.data_sources, script.parameters])
  const selectParameterData = (idx: number, parameter: ParameterConfig) => {
    setSelectedParameterIdx(idx)
    const sourceIndex = findDataSourceIndexByName(script.data_sources, parameter.data_source)
    setIsDataSourcesOpen(true)
    if (sourceIndex !== null) {
      onOpenDataEditor(sourceIndex)
    }
  }

  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        className="editor-dialog parameter-data-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameter Data"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter Data ({script.data_sources?.length || 0})</h3>
            <p className="editor-dialog-subtitle">Manage parameters and reusable parameter data without leaving the action workspace.</p>
          </div>
          <div className="parameter-dialog-actions">
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
            <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
          </div>
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
          <AutoParameterizationSection
            onApplyAutoParameterization={onApplyAutoParameterization}
            onScanForAutoParameters={onScanForAutoParameters}
            onUpdateParameterCandidate={onUpdateParameterCandidate}
            parameterCandidates={parameterCandidates}
          />
          <ParameterDefinitionsSection
            isOpen={isNamedParametersOpen}
            onAddParameter={onAddParameter}
            onRemoveParameter={onRemoveParameter}
            onSelectParameter={selectParameterData}
            onToggleOpen={() => setIsNamedParametersOpen(open => !open)}
            onUpdateParameter={onUpdateParameter}
            onUpdateParameterType={onUpdateParameterType}
            focusedSourceName={focusedParameterSourceName}
            selectedParameterIdx={selectedParameterIdx}
            script={script}
          />

          <ParameterDataSourcesSection
            dataEditorMode={dataEditorMode}
            dataSourceHistory={dataSourceHistory}
            isOpen={isDataSourcesOpen}
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
            onToggleOpen={() => setIsDataSourcesOpen(open => !open)}
            onUndoDataSource={onUndoDataSource}
            onUpdateDataCell={onUpdateDataCell}
            onUpdateDataColumnName={onUpdateDataColumnName}
            onUpdateDataSource={onUpdateDataSource}
            openDataEditor={openDataEditor}
            selectedParameter={selectedParameter}
            script={script}
          />
        </div>
      </div>
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
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>This selection came from the Action List editor. Review it here with the rest of the parameter configuration.</span>
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

interface AutoParameterizationSectionProps {
  onApplyAutoParameterization: () => void
  onScanForAutoParameters: () => void
  onUpdateParameterCandidate: (id: string, patch: Partial<ParameterCandidate>) => void
  parameterCandidates: ParameterCandidate[]
}

function AutoParameterizationSection({
  onApplyAutoParameterization,
  onScanForAutoParameters,
  onUpdateParameterCandidate,
  parameterCandidates,
}: AutoParameterizationSectionProps) {
  return (
    <div className="auto-parameter-section">
      <div className="panel-title-row" style={{ marginBottom: 0 }}>
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Auto Parameterization</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Scan targets, headers, and body fields for values that can become reusable parameter data.</span>
        </div>
        <button className="btn btn-sm btn-ghost" onClick={onScanForAutoParameters}>Auto Parameterization</button>
      </div>
      {parameterCandidates.length > 0 && (
        <div className="auto-parameter-panel">
          <div className="auto-parameter-header">
            <h4>Review Candidates</h4>
            <button className="btn btn-sm btn-accent" onClick={onApplyAutoParameterization}>Apply Selected</button>
          </div>
          <div className="auto-parameter-table-wrap">
            <table className="auto-parameter-table">
              <thead>
                <tr>
                  <th>Use</th>
                  <th>Field Hint</th>
                  <th>Current Value</th>
                  <th>Variable Name</th>
                  <th>Hits</th>
                </tr>
              </thead>
              <tbody>
                {parameterCandidates.map(candidate => (
                  <tr key={candidate.id}>
                    <td>
                      <input
                        type="checkbox"
                        checked={candidate.selected}
                        onChange={e => onUpdateParameterCandidate(candidate.id, { selected: e.target.checked })}
                      />
                    </td>
                    <td className="cell-mono">{candidate.keyHint}</td>
                    <td className="cell-mono">{candidate.value}</td>
                    <td>
                      <input
                        value={candidate.varName}
                        onChange={e => onUpdateParameterCandidate(candidate.id, { varName: sanitizeVarName(e.target.value) })}
                        placeholder="parameter_name"
                      />
                    </td>
                    <td>{candidate.occurrences.length}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
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
  onUpdateParameterType: (idx: number, nextType: string) => void
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
    const sync = parameter.same_line_as ? `same line as ${parameter.same_line_as}` : ''
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

type ParameterChoiceField = 'type' | 'update' | 'selection' | 'on_exhaustion'

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
      { value: 'file', label: 'File', description: 'Reads values from a data source file or imported VuGen parameter file mapping.' },
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
      { value: 'sequential', label: 'Sequential', description: 'Read rows in order, which is useful for deterministic replay and imported VuGen behavior.' },
      { value: 'random', label: 'Random', description: 'Pick a row at random for each refresh, which spreads usage across the data set.' },
      { value: 'unique', label: 'Unique', description: 'Reserve unique rows so concurrent VUsers do not reuse the same value until the source is exhausted.' },
    ],
  },
  on_exhaustion: {
    title: 'Exhaustion Behavior',
    description: 'Choose what happens when the source runs out of available parameter values.',
    options: [
      { value: 'wrap', label: 'Wrap', description: 'Start over at the beginning of the source when all rows have been used.' },
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
  return option?.label || value
}

function ParameterDefinitionsSection({
  focusedSourceName,
  isOpen,
  onAddParameter,
  onRemoveParameter,
  onSelectParameter,
  onToggleOpen,
  onUpdateParameter,
  onUpdateParameterType,
  selectedParameterIdx,
  script,
}: ParameterDefinitionsSectionProps) {
  const [expandedParameterSources, setExpandedParameterSources] = useState<Set<string>>(() => new Set())
  const [activeParameterChoice, setActiveParameterChoice] = useState<{ field: ParameterChoiceField; idx: number } | null>(null)
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
    if (activeParameterChoice.field === 'type') {
      onUpdateParameterType(activeParameterChoice.idx, value)
    } else {
      onUpdateParameter(activeParameterChoice.idx, activeParameterChoice.field, value)
    }
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
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Define parameters with VuGen-style update cadence and generated values.</span>
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
              onUpdateType={(value) => onUpdateParameterType(activeParameterSettingsIdx, value)}
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
  onOpenChoice: (choice: { field: ParameterChoiceField; idx: number }) => void
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
          onClick={(event: MouseEvent<HTMLButtonElement>) => {
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
        {formatParameterChoiceLabel('selection', getParameterChoiceValue(parameter, 'selection'))}
        {' / '}
        {formatParameterChoiceLabel('on_exhaustion', getParameterChoiceValue(parameter, 'on_exhaustion'))}
        {parameter.same_line_as ? ` · Same line as ${parameter.same_line_as}` : ''}
      </small>
    </button>
  )
}

interface ParameterSettingsButtonProps {
  onClick: (event: MouseEvent<HTMLButtonElement>) => void
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
  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
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
  onUpdateType: (value: string) => void
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

function ParameterSettingsDialog({
  onClose,
  onUpdate,
  onUpdateType,
  parameter,
}: ParameterSettingsDialogProps) {
  const parameterName = parameter.name || 'Parameter'
  const parameterType = parameter.type || 'list'
  const hasDataMappingSettings = parameterType === 'file' || parameterType === 'list' || parameterType === 'host'
  const hasGeneratedValueSettings = ['random_number', 'counter', 'current_iteration', 'random_string', 'uuid'].includes(parameterType)
  const hasTimeSettings = parameterType === 'date_time' || parameterType === 'timestamp' || parameterType === 'time'
  const hasOutputSettings = parameterType === 'counter' || parameterType === 'current_iteration' || parameterType === 'random_number' || parameter.seed !== undefined

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
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
              <label>
                <span>Parameter Type</span>
                <select
                  aria-label={`Parameter Type for ${parameterName}`}
                  value={parameterType}
                  onChange={event => onUpdateType(event.target.value)}
                >
                  {PARAMETER_CHOICE_DEFINITIONS.type.options.map(option => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </label>
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
                  {parameterType === 'timestamp' ? (
                    <select value={parameter.format || 'iso8601'} onChange={event => onUpdate('format', event.target.value)}>
                      <option value="unix">unix</option>
                      <option value="unix_ms">unix_ms</option>
                      <option value="iso8601">iso8601</option>
                      <option value="rfc3339">rfc3339</option>
                      <option value="rfc3339_nano">rfc3339_nano</option>
                    </select>
                  ) : (
                    <input value={parameter.format || ''} onChange={event => onUpdate('format', event.target.value)} placeholder="Go time layout" />
                  )}
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
  const parameterName = parameter.name || 'Parameter'
  const selectionValue = getParameterChoiceValue(parameter, 'selection')
  const exhaustionValue = getParameterChoiceValue(parameter, 'on_exhaustion')

  return (
    <div className="editor-dialog-backdrop parameter-choice-backdrop" onClick={onClose}>
      <div
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
            definition={PARAMETER_CHOICE_DEFINITIONS.selection}
            onSelect={value => onUpdate('selection', value)}
            selectedValue={selectionValue}
          />
          <ParameterSelectionChoiceGroup
            definition={PARAMETER_CHOICE_DEFINITIONS.on_exhaustion}
            onSelect={value => onUpdate('on_exhaustion', value)}
            selectedValue={exhaustionValue}
          />
          <section className="parameter-selection-choice-group">
            <div>
              <h4>Same Line Sync</h4>
              <p>Use the row selected by another parameter from the same source file, so random or unique row selection keeps related columns together.</p>
            </div>
            <label className="parameter-settings-wide">
              <span>Sync Row With</span>
              <select
                aria-label={`Same Line Sync for ${parameterName}`}
                disabled={sameLineOptions.length === 0}
                value={parameter.same_line_as || ''}
                onChange={event => onUpdate('same_line_as', event.target.value || undefined)}
              >
                <option value="">Independent row selection</option>
                {sameLineOptions.map(option => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
            </label>
            {sameLineOptions.length === 0 && (
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
  onSelect: (value: string) => void
  selectedValue: string
}

function ParameterSelectionChoiceGroup({
  definition,
  onSelect,
  selectedValue,
}: ParameterSelectionChoiceGroupProps) {
  return (
    <section className="parameter-selection-choice-group">
      <div>
        <h4>{definition.title}</h4>
        <p>{definition.description}</p>
      </div>
      <div className="parameter-choice-dialog-body">
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
  openDataEditor: number | null
  selectedParameter: ParameterConfig | null
  script: ScriptData
}

function ParameterDataSourcesSection({
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
  openDataEditor,
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
  const [sortState, setSortState] = useState<{ column: number; direction: DataSourceSortDirection } | null>(null)
  const [filterText, setFilterText] = useState('')
  const [filterColumn, setFilterColumn] = useState<number | 'all'>('all')
  const delimiterChoice = getDataSourceDelimiterChoice(source)
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
  const updateDelimiterChoice = (choice: DataSourceDelimiterChoice) => {
    onUpdateDataSource(index, 'delimiter', getDataSourceDelimiterValue(choice), true)
  }
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
          <td colSpan={isFocusedParameterView ? 9 : 9}>
            <div className="parameter-editor">
              <div className="parameter-editor-header">
                <strong>{isFocusedParameterView ? focusedDataTitle : sourceLabel}</strong>
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
          </td>
        </tr>
      )}
    </>
  )
}

/* Sub-component for editing a single action's details */
function ActionEditor({ action, index, parameters, onUpdate, onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion, onAddHeader, onUpdateHeader, onRemoveHeader, onAddMetadata, onUpdateMetadata, onRemoveMetadata, onBeginParameterEdit, methodColor, protocol, isBrowserScript, isJmsScript, isSoapScript, issues }: {
  action: Action; index: number
  parameters: ParameterConfig[]
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onAddHeader: (i: number) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveHeader: (ai: number, k: string) => void
  onAddMetadata: (i: number) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  methodColor: (m: string) => string
  protocol: string
  isBrowserScript: boolean
  isJmsScript: boolean
  isSoapScript: boolean
  issues: AuthoringIssue[]
}) {
  const isWebSocketScript = protocol === 'websocket'
  const isGrpcScript = protocol === 'grpc'
  const isDatabaseScript = protocol === 'database'
  const isNetworkScript = protocol === 'network'
  const { selectionState, openSelectionMenu, openParameterEditor } = useActionParameterMenu({
    index,
    onBeginParameterEdit,
    parameters,
  })

  return (
    <div className="action-editor-panel">
      <ActionEditorHeader action={action} issues={issues} methodColor={methodColor} />

      <ActionCoreFields
        action={action}
        index={index}
        protocol={protocol}
        isBrowserScript={isBrowserScript}
        isJmsScript={isJmsScript}
        onUpdate={onUpdate}
      />

      {isBrowserScript && (
        <BrowserActionFields action={action} index={index} onUpdate={onUpdate} />
      )}

      {isWebSocketScript ? (
        <WebSocketActionFields
          action={action}
          index={index}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onAddHeader={onAddHeader}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onSelectionMenu={openSelectionMenu}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
        />
      ) : isGrpcScript ? (
        <GrpcActionFields
          action={action}
          index={index}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onAddHeader={onAddHeader}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onSelectionMenu={openSelectionMenu}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
        />
      ) : isDatabaseScript ? (
        <DatabaseActionFields
          action={action}
          index={index}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onSelectionMenu={openSelectionMenu}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
        />
      ) : isNetworkScript ? (
        <NetworkActionFields
          action={action}
          index={index}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onSelectionMenu={openSelectionMenu}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
        />
      ) : isJmsScript ? (
        <JmsActionFields
          action={action}
          index={index}
          onAddHeader={onAddHeader}
          onAddMetadata={onAddMetadata}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onRemoveMetadata={onRemoveMetadata}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
          onUpdateMetadata={onUpdateMetadata}
          onSelectionMenu={openSelectionMenu}
        />
      ) : (
        <HttpActionFields
          action={action}
          index={index}
          isBrowserScript={isBrowserScript}
          isSoapScript={isSoapScript}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onAddHeader={onAddHeader}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
          onSelectionMenu={openSelectionMenu}
        />
      )}

      <ActionThinkTimeToggle action={action} index={index} onUpdate={onUpdate} />

      <ActionParameterMenu selectionState={selectionState} onOpenParameterEditor={openParameterEditor} />
    </div>
  )
}

const BROWSER_ACTION_TYPES = ['browser_navigate', 'browser_click', 'browser_fill', 'browser_select', 'browser_wait', 'browser_screenshot', 'browser_tap', 'browser_long_press', 'browser_swipe']
const JMS_ACTION_TYPES = ['jms_publish', 'jms_consume', 'jms_request_reply', 'jms_ack', 'jms_commit', 'jms_rollback']
const WEBSOCKET_ACTION_TYPES = ['ws_connect', 'ws_send', 'ws_receive', 'ws_ping', 'ws_close']
const GRPC_ACTION_TYPES = ['grpc_unary', 'grpc_server_stream', 'grpc_client_stream', 'grpc_bidi_stream']
const DATABASE_ACTION_TYPES = ['db_health', 'db_begin', 'db_query', 'db_exec', 'db_procedure', 'db_commit', 'db_rollback']
const NETWORK_ACTION_TYPES = ['network_health', 'dns_query', 'tcp_request', 'udp_request', 'ftp_command', 'smtp_send', 'pop3_command', 'imap_command', 'ldap_bind', 'ldap_search']
const HTTP_ACTION_TYPES = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

interface ActionEditorHeaderProps {
  action: Action
  issues: AuthoringIssue[]
  methodColor: (method: string) => string
}

function ActionEditorHeader({ action, issues, methodColor }: ActionEditorHeaderProps) {
  return (
    <>
      <div className="action-editor-top">
        <span className="action-badge" style={{ background: methodColor(action.type), color: '#fff' }}>{action.type}</span>
        <h3>{action.name || 'Unnamed Action'}</h3>
        {issues.length > 0 && (
          <div className="action-editor-issue-pills">
            {issues.map((issue) => (
              <span key={issue.id} className={`action-signal-pill ${issue.severity}`}>{issue.severity}</span>
            ))}
          </div>
        )}
      </div>
      {issues.length > 0 && (
        <div className="action-editor-issues">
          {issues.map((issue) => (
            <div key={`${issue.id}:message`} className={`action-editor-issue ${issue.severity}`}>
              {issue.message}
            </div>
          ))}
        </div>
      )}
    </>
  )
}

interface ActionCoreFieldsProps {
  action: Action
  index: number
  protocol: string
  isBrowserScript: boolean
  isJmsScript: boolean
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ActionCoreFields({ action, index, protocol, isBrowserScript, isJmsScript, onUpdate }: ActionCoreFieldsProps) {
  const showSelector = isBrowserScript && ['browser_click', 'browser_fill', 'browser_select', 'browser_wait', 'browser_screenshot', 'browser_tap', 'browser_long_press', 'browser_swipe'].includes(action.type)
  const showValue = isBrowserScript && ['browser_fill', 'browser_select'].includes(action.type)
  const isWebSocketScript = protocol === 'websocket'
  const isGrpcScript = protocol === 'grpc'
  const isDatabaseScript = protocol === 'database'
  const isNetworkScript = protocol === 'network'
  const targetLabel = isJmsScript
    ? 'Destination'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'URL or Path'
      : action.type === 'browser_wait'
        ? 'Wait Target or Duration'
        : 'Target Selector'
    : isWebSocketScript
      ? action.type === 'ws_connect' ? 'Endpoint' : 'Connection or Topic'
      : isGrpcScript
        ? 'Service / Method'
        : isDatabaseScript
          ? action.type === 'db_procedure' ? 'Procedure' : 'SQL'
          : isNetworkScript
            ? 'Endpoint / Host'
    : 'Target (path)'
  const targetPlaceholder = isJmsScript
    ? 'orders.in or {{data.destinations.queue}}'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'https://example.com or /login'
      : action.type === 'browser_wait'
        ? '#ready, text, or 750ms'
        : '#submit'
    : isWebSocketScript
      ? action.type === 'ws_connect' ? '/ws or wss://example.com/ws' : 'Leave blank to use active connection'
      : isGrpcScript
        ? 'package.Service/Method'
        : isDatabaseScript
          ? 'select id from users where id = ?'
          : isNetworkScript
            ? 'localhost:8080, example.com, or dc=example,dc=com'
    : '/api/resource'
  const actionTypes = isBrowserScript
    ? BROWSER_ACTION_TYPES
    : isJmsScript
      ? JMS_ACTION_TYPES
      : isWebSocketScript
        ? WEBSOCKET_ACTION_TYPES
        : isGrpcScript
          ? GRPC_ACTION_TYPES
          : isDatabaseScript
            ? DATABASE_ACTION_TYPES
            : isNetworkScript
              ? NETWORK_ACTION_TYPES
              : HTTP_ACTION_TYPES
  const actionTypeLabel = isBrowserScript || isJmsScript || isWebSocketScript || isGrpcScript || isDatabaseScript || isNetworkScript ? 'Action Type' : 'Method'

  return (
    <div className="form-grid">
      <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
      <label><span>{actionTypeLabel}</span>
        <select value={action.type} onChange={e => onUpdate(index, 'type', e.target.value)}>
          {actionTypes.map(type => <option key={type}>{type}</option>)}
        </select>
      </label>
      <label className="full-width"><span>{targetLabel}</span><input value={action.target} onChange={e => onUpdate(index, 'target', e.target.value)} placeholder={targetPlaceholder} /></label>
      {showSelector && (
        <label className="full-width"><span>Selector Override</span><input value={action.selector || ''} onChange={e => onUpdate(index, 'selector', e.target.value)} placeholder="#submit, input[name=email], [data-testid=save]" /></label>
      )}
      {showValue && (
        <label className="full-width"><span>Value</span><input value={action.value || ''} onChange={e => onUpdate(index, 'value', e.target.value)} placeholder="{{data.users.email}}" /></label>
      )}
    </div>
  )
}

interface ActionThinkTimeToggleProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ActionThinkTimeToggle({ action, index, onUpdate }: ActionThinkTimeToggleProps) {
  return (
    <div className="sub-section">
      <label className="checkbox-label">
        <input type="checkbox" checked={action.think_time_after ?? false} onChange={e => onUpdate(index, 'think_time_after', e.target.checked)} />
        <span>Apply think time after this action</span>
      </label>
    </div>
  )
}

interface ActionParameterMenuProps {
  selectionState: ActionEditorSelectionState | null
  onOpenParameterEditor: (nextExistingName?: string | null) => void
}

function ActionParameterMenu({ selectionState, onOpenParameterEditor }: ActionParameterMenuProps) {
  if (!selectionState) return null

  return (
    <div
      className="action-parameter-menu"
      style={{ left: selectionState.x, top: selectionState.y }}
      onClick={event => event.stopPropagation()}
    >
      <button type="button" className="btn btn-sm" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
        Parameterize selection
      </button>
      {selectionState.existingName && (
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
          Edit parameter
        </button>
      )}
    </div>
  )
}

function ValidationEvidencePanel({ evidence }: { evidence: ValidationEvidence }) {
  const dataFit = evidence.data_source_fit
  const aiGuardrails = evidence.ai_guardrails
  const health = evidence.authoring_health
  if (!dataFit && !aiGuardrails && !health) {
    return null
  }

  return (
    <div className="validation-evidence-panel">
      {health && (
        <div className="validation-evidence-summary">
          <strong>Authoring Health</strong>
          <span className={`validation-evidence-status ${health.status}`}>{formatValidationStatus(health.status)} · {health.score}</span>
        </div>
      )}
      {dataFit && (
        <section className="validation-evidence-section">
          <div className="validation-evidence-section-header">
            <strong>Data-Source Fit</strong>
            <span className={`validation-evidence-status ${dataFit.status}`}>{formatValidationStatus(dataFit.status)}</span>
          </div>
          {dataFit.items.length === 0 ? (
            <p>Data sources match referenced parameters and modeled row usage.</p>
          ) : (
            <ul>
              {dataFit.items.slice(0, 5).map((item, index) => (
                <li key={`${item.data_source || 'source'}:${item.parameter || 'parameter'}:${item.column || 'column'}:${index}`}>
                  <span className={`validation-evidence-status ${item.status}`}>{formatValidationStatus(item.status)}</span>
                  <span>{item.summary}</span>
                  <small>{formatDataSourceFitContext(item)}</small>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
      {aiGuardrails && (
        <section className="validation-evidence-section">
          <div className="validation-evidence-section-header">
            <strong>AI Draft Guardrails</strong>
            <span className={`validation-evidence-status ${aiGuardrails.status}`}>{formatValidationStatus(aiGuardrails.status)}</span>
          </div>
          <ul>
            {aiGuardrails.items.map(item => (
              <li key={item.key}>
                <span className={`validation-evidence-status ${item.status}`}>{formatValidationStatus(item.status)}</span>
                <span>{item.summary}</span>
                <small>{formatValidationStatus(item.key)}</small>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function formatValidationStatus(value: string): string {
  const text = String(value || '').trim()
  if (!text) return 'Unknown'
  return text.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function formatDataSourceFitContext(item: NonNullable<ValidationEvidence['data_source_fit']>['items'][number]): string {
  const parts = [
    item.data_source ? `Source ${item.data_source}` : '',
    item.parameter ? `Parameter ${item.parameter}` : '',
    item.column ? `Column ${item.column}` : '',
    item.required_rows || item.available_rows ? `${item.available_rows || 0}/${item.required_rows || 0} rows` : '',
  ].filter(Boolean)
  return parts.join(' · ')
}

function useScriptEditorState(initialScript?: ScriptData | null) {
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const [importBaselineScript, setImportBaselineScript] = useState<ScriptData | null>(
    initialScript?.metadata?.import ? JSON.parse(JSON.stringify(initialScript)) as ScriptData : null,
  )
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [warnings, setWarnings] = useState<string[]>([])
  const [jsonMode, setJsonMode] = useState(false)
  const [jsonText, setJsonText] = useState('')
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    config: false,
    actions: true,
    runtime: false,
    import: scriptHasSourceReview(initialScript),
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
    logLevel: 'info',
    variablesText: '{\n  \n}',
  })

  const toggleSection = (id: string) => setOpenSections(prev => ({ ...prev, [id]: !prev[id] }))

  const hydrateEditorState = useCallback((nextScript: ScriptData) => {
    setScript(nextScript)
    setImportBaselineScript(nextScript.metadata?.import ? JSON.parse(JSON.stringify(nextScript)) as ScriptData : null)
    if (scriptHasSourceReview(nextScript)) {
      setOpenSections(prev => ({ ...prev, import: true }))
    }
    setJsonText(JSON.stringify({ script: nextScript }, null, 2))
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
    jsonMode,
    jsonText,
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
    setJsonMode,
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

function useActionParameterMenu({
  index,
  onBeginParameterEdit,
  parameters,
}: {
  index: number
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  parameters: ParameterConfig[]
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

  const openSelectionMenu = useCallback((event: MouseEvent<HTMLTextAreaElement>) => {
    const textarea = event.currentTarget
    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? 0
    const selectedText = (textarea.value || '').slice(start, end)
    if (!selectedText.trim()) {
      setSelectionState(null)
      return
    }

    event.preventDefault()
    const existingParameter = detectSelectedParameter(selectedText)
    setSelectionState({
      start,
      end,
      selectedText,
      x: event.clientX,
      y: event.clientY,
      existingName: existingParameter?.name || null,
    })
  }, [detectSelectedParameter])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    if (!selectionState) return
    const existingParameter = nextExistingName
      ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
      : detectSelectedParameter(selectionState.selectedText)

    const requestedName = existingParameter?.name
      || sanitizeVarName(selectionState.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
    const seedValue = existingParameter?.values?.[0] || selectionState.selectedText

    setSelectionState(null)
    onBeginParameterEdit({
      actionIndex: index,
      start: selectionState.start,
      end: selectionState.end,
      selectedText: selectionState.selectedText,
      existingName: existingParameter?.name || null,
      requestedName,
      seedValue,
    })
  }, [detectSelectedParameter, index, onBeginParameterEdit, parameters, selectionState])

  return { openParameterEditor, openSelectionMenu, selectionState }
}

interface ActionBodyTextareaProps {
  onChange: (value: string) => void
  onContextMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  placeholder: string
  rows: number
  value: string
}

function ActionBodyTextarea({ onChange, onContextMenu, placeholder, rows, value }: ActionBodyTextareaProps) {
  return (
    <textarea
      className="body-textarea"
      value={value}
      onChange={event => onChange(event.target.value)}
      onContextMenu={onContextMenu}
      placeholder={placeholder}
      rows={rows}
    />
  )
}

interface BrowserActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function BrowserActionFields({ action, index, onUpdate }: BrowserActionFieldsProps) {
  const waitFor = action.wait_for || {}
  const capture = action.capture || {}
  const params = action.params || {}

  return (
    <>
      {action.type === 'browser_wait' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Wait Condition</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Type</span>
              <select
                value={waitFor.type || 'selector'}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, type: e.target.value })}
              >
                <option value="selector">Selector visible</option>
                <option value="hidden">Selector hidden</option>
                <option value="text">Text appears</option>
                <option value="url_contains">URL contains</option>
                <option value="duration">Duration</option>
              </select>
            </label>
            <label>
              <span>Value</span>
              <input
                value={waitFor.value || ''}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, value: e.target.value })}
                placeholder={waitFor.type === 'duration' ? '1000ms' : '#ready or expected text'}
              />
            </label>
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={waitFor.timeout_ms ?? 10000}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, timeout_ms: Math.max(0, Number(e.target.value) || 0) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_long_press' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Long Press</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Duration (ms)</span>
              <input
                type="number"
                min="1"
                value={typeof params.duration_ms === 'number' ? params.duration_ms : 350}
                onChange={e => onUpdate(index, 'params', { ...params, duration_ms: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_swipe' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Swipe Gesture</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Direction</span>
              <select
                value={typeof params.direction === 'string' ? params.direction : 'left'}
                onChange={e => onUpdate(index, 'params', { ...params, direction: e.target.value })}
              >
                <option value="left">Left</option>
                <option value="right">Right</option>
                <option value="up">Up</option>
                <option value="down">Down</option>
              </select>
            </label>
            <label>
              <span>Distance (px)</span>
              <input
                type="number"
                min="1"
                value={typeof params.distance === 'number' ? params.distance : 180}
                onChange={e => onUpdate(index, 'params', { ...params, distance: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      <div className="sub-section browser-action-section">
        <div className="sub-header"><h4>Capture</h4></div>
        <div className="browser-capture-row">
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.screenshot || action.type === 'browser_screenshot'}
              disabled={action.type === 'browser_screenshot'}
              onChange={e => onUpdate(index, 'capture', { ...capture, screenshot: e.target.checked })}
            />
            <span>Screenshot</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.full_page}
              onChange={e => onUpdate(index, 'capture', { ...capture, full_page: e.target.checked })}
            />
            <span>Full page</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.html}
              onChange={e => onUpdate(index, 'capture', { ...capture, html: e.target.checked })}
            />
            <span>HTML</span>
          </label>
        </div>
      </div>
    </>
  )
}

function formatActionStringList(value: unknown): string {
  if (Array.isArray(value)) return value.map(item => String(item ?? '')).join('\n')
  if (typeof value === 'string') return value
  return ''
}

function parseActionStringList(value: string): string[] {
  return value.split(/\n|,/).map(item => item.trim()).filter(Boolean)
}

interface WebSocketActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function WebSocketActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: WebSocketActionFieldsProps) {
  const params = action.params || {}
  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>WebSocket Operation</h4></div>
        <div className="form-grid">
          {(action.type === 'ws_send' || action.type === 'ws_receive') && (
            <label>
              <span>Message Type</span>
              <select value={typeof params.type === 'string' ? params.type : 'text'} onChange={e => onUpdate(index, 'params', { ...params, type: e.target.value })}>
                <option value="text">Text</option>
                <option value="binary">Binary</option>
                <option value="json">JSON</option>
              </select>
            </label>
          )}
          {(action.type === 'ws_receive' || action.type === 'ws_connect') && (
            <label>
              <span>Timeout (ms)</span>
              <input type="number" value={typeof params.timeout_ms === 'number' ? params.timeout_ms : 30000}
                onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })} />
            </label>
          )}
          {action.type === 'ws_receive' && (
            <label className="full-width">
              <span>Expected Message Text</span>
              <input value={typeof params.expected === 'string' ? params.expected : ''} onChange={e => onUpdate(index, 'params', { ...params, expected: e.target.value })} placeholder="created, pong, or {{var.expected_event}}" />
            </label>
          )}
          {action.type === 'ws_send' && (
            <label className="full-width">
              <span>Message Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"type":"ping","id":"{{param.request_id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      {action.type === 'ws_connect' && (
        <ProtocolHeadersSection action={action} index={index} onAddHeader={onAddHeader} onRemoveHeader={onRemoveHeader} onUpdateHeader={onUpdateHeader} />
      )}

      <ProtocolExtractorsSection action={action} index={index} protocol="websocket" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="websocket" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

interface GrpcActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function GrpcActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: GrpcActionFieldsProps) {
  const params = action.params || {}
  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>gRPC Message</h4></div>
        <div className="form-grid">
          <label>
            <span>Timeout (ms)</span>
            <input type="number" value={typeof params.timeout_ms === 'number' ? params.timeout_ms : 30000}
              onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })} />
          </label>
          {(action.type === 'grpc_server_stream' || action.type === 'grpc_bidi_stream') && (
            <label>
              <span>Max Messages</span>
              <input type="number" value={typeof params.max_messages === 'number' ? params.max_messages : 1}
                onChange={e => onUpdate(index, 'params', { ...params, max_messages: Number(e.target.value) || undefined })} />
            </label>
          )}
          {(action.type === 'grpc_client_stream' || action.type === 'grpc_bidi_stream') && (
            <label className="full-width">
              <span>Stream Messages</span>
              <textarea
                rows={5}
                value={formatActionStringList(params.messages)}
                onChange={e => onUpdate(index, 'params', { ...params, messages: parseActionStringList(e.target.value) })}
                placeholder={'{"id":"1"}\n{"id":"2"}'}
              />
            </label>
          )}
          {(action.type === 'grpc_unary' || action.type === 'grpc_server_stream') && (
            <label className="full-width">
              <span>Request Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"id":"{{param.order_id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      <ProtocolHeadersSection action={action} index={index} onAddHeader={onAddHeader} onRemoveHeader={onRemoveHeader} onUpdateHeader={onUpdateHeader} />
      <ProtocolExtractorsSection action={action} index={index} protocol="grpc" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="grpc" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

interface DatabaseActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

function DatabaseActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onRemoveAssertion,
  onRemoveExtractor,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
}: DatabaseActionFieldsProps) {
  const params = action.params || {}
  const isSQLAction = ['db_query', 'db_exec', 'db_procedure'].includes(action.type)
  return (
    <>
      {isSQLAction && (
        <div className="sub-section">
          <div className="sub-header"><h4>SQL And Binds</h4></div>
          <div className="form-grid">
            <label className="full-width">
              <span>{action.type === 'db_procedure' ? 'Procedure / Call' : 'SQL'}</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'target', value)}
                onContextMenu={onSelectionMenu}
                placeholder={action.type === 'db_query' ? 'select id, name from users where id = ?' : 'update users set last_seen = current_timestamp where id = ?'}
                rows={5}
                value={action.target || ''}
              />
            </label>
            <label className="full-width">
              <span>Bind Parameters</span>
              <textarea
                rows={3}
                value={formatActionStringList(params.binds)}
                onChange={e => onUpdate(index, 'params', { ...params, binds: parseActionStringList(e.target.value) })}
                placeholder={'{{param.user_id}}\n{{data.orders.id}}'}
              />
            </label>
            <label>
              <span>Max Rows</span>
              <input type="number" value={typeof params.max_rows === 'number' ? params.max_rows : ''}
                onChange={e => onUpdate(index, 'params', { ...params, max_rows: Number(e.target.value) || undefined })} placeholder="Use runtime default" />
            </label>
          </div>
        </div>
      )}

      <ProtocolExtractorsSection action={action} index={index} protocol="database" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="database" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

interface NetworkActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

function NetworkActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onRemoveAssertion,
  onRemoveExtractor,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
}: NetworkActionFieldsProps) {
  const params = action.params || {}
  const hasPayload = ['tcp_request', 'udp_request', 'smtp_send'].includes(action.type)
  const hasCommand = ['ftp_command', 'pop3_command', 'imap_command'].includes(action.type)
  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>Protocol Controls</h4></div>
        <div className="form-grid">
          {action.type === 'dns_query' && (
            <label>
              <span>Record Type</span>
              <select value={typeof params.record_type === 'string' ? params.record_type : 'A'} onChange={e => onUpdate(index, 'params', { ...params, record_type: e.target.value })}>
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
                <option value="MX">MX</option>
                <option value="NS">NS</option>
                <option value="TXT">TXT</option>
              </select>
            </label>
          )}
          {(action.type === 'tcp_request' || action.type === 'udp_request') && (
            <>
              <label className="checkbox-label">
                <input type="checkbox" checked={Boolean(params.read)}
                  onChange={e => onUpdate(index, 'params', { ...params, read: e.target.checked })} />
                <span>Read response</span>
              </label>
              <label>
                <span>Read Bytes</span>
                <input type="number" value={typeof params.read_bytes === 'number' ? params.read_bytes : 4096}
                  onChange={e => onUpdate(index, 'params', { ...params, read_bytes: Number(e.target.value) || undefined })} />
              </label>
            </>
          )}
          {hasCommand && (
            <label className="full-width">
              <span>Command</span>
              <input value={typeof params.command === 'string' ? params.command : action.body || ''} onChange={e => onUpdate(index, 'params', { ...params, command: e.target.value })} placeholder="NOOP, STAT, CAPABILITY" />
            </label>
          )}
          {(action.type === 'smtp_send' || action.type === 'ldap_bind' || action.type === 'ldap_search') && (
            <>
              <label>
                <span>Username</span>
                <input value={typeof params.username === 'string' ? params.username : ''} onChange={e => onUpdate(index, 'params', { ...params, username: e.target.value })} />
              </label>
              <label>
                <span>Password</span>
                <input type="password" value={typeof params.password === 'string' ? params.password : ''} onChange={e => onUpdate(index, 'params', { ...params, password: e.target.value })} />
              </label>
            </>
          )}
          {action.type === 'ldap_search' && (
            <>
              <label className="full-width">
                <span>LDAP Filter</span>
                <input value={typeof params.filter === 'string' ? params.filter : '(objectClass=*)'} onChange={e => onUpdate(index, 'params', { ...params, filter: e.target.value })} />
              </label>
              <label>
                <span>Max Entries</span>
                <input type="number" value={typeof params.max_entries === 'number' ? params.max_entries : 100} onChange={e => onUpdate(index, 'params', { ...params, max_entries: Number(e.target.value) || undefined })} />
              </label>
            </>
          )}
          {hasPayload && (
            <label className="full-width">
              <span>Payload</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder={action.type === 'smtp_send' ? 'Subject: Smoke\n\nHello' : 'ping'}
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      <ProtocolExtractorsSection action={action} index={index} protocol="network" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="network" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

interface JmsActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onAddMetadata: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
}

function JmsActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onAddMetadata,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onRemoveMetadata,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
  onUpdateMetadata,
}: JmsActionFieldsProps) {
  const params = action.params || {}

  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>Message</h4></div>
        <div className="form-grid">
          {(action.type === 'jms_publish' || action.type === 'jms_request_reply') && (
            <label className="full-width">
              <span>Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"orderId": "{{data.orders.id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
          <label>
            <span>Correlation ID</span>
            <input
              value={typeof params.correlation_id === 'string' ? params.correlation_id : ''}
              onChange={e => onUpdate(index, 'params', { ...params, correlation_id: e.target.value })}
              placeholder="{{var.jms_message_id}} or {{fn.uuid()}}"
            />
          </label>
          {(action.type === 'jms_request_reply' || action.type === 'jms_publish' || action.type === 'jms_consume') && (
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={typeof params.timeout_ms === 'number' ? params.timeout_ms : ''}
                onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })}
                placeholder="Use script default"
              />
            </label>
          )}
          {action.type === 'jms_request_reply' && (
            <label className="full-width">
              <span>Reply To Override</span>
              <input
                value={typeof params.reply_to === 'string' ? params.reply_to : ''}
                onChange={e => onUpdate(index, 'params', { ...params, reply_to: e.target.value })}
                placeholder="orders.reply"
              />
            </label>
          )}
        </div>
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
        {Object.entries(action.headers || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Header" />
            <input className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Properties</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddMetadata(index)}>+ Add</button></div>
        {Object.entries(action.metadata || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateMetadata(index, key, e.target.value, String(value || ''))} placeholder="Property" />
            <input className="kv-val" value={String(value || '')} onChange={e => onUpdateMetadata(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveMetadata(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <ProtocolExtractorsSection
        action={action}
        index={index}
        protocol="jms-solace"
        onAddExtractor={onAddExtractor}
        onRemoveExtractor={onRemoveExtractor}
        onUpdateExtractor={onUpdateExtractor}
      />

      <ProtocolAssertionsSection
        action={action}
        index={index}
        protocol="jms-solace"
        onAddAssertion={onAddAssertion}
        onRemoveAssertion={onRemoveAssertion}
        onUpdateAssertion={onUpdateAssertion}
      />
    </>
  )
}

interface HttpActionFieldsProps {
  action: Action
  index: number
  isBrowserScript: boolean
  isSoapScript: boolean
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function HttpActionFields({
  action,
  index,
  isBrowserScript,
  isSoapScript,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: HttpActionFieldsProps) {
  return (
    <>
      {isSoapScript && (
        <SoapActionFields action={action} index={index} onUpdate={onUpdate} />
      )}

      {!isBrowserScript && (
        <ProtocolHeadersSection
          action={action}
          index={index}
          onAddHeader={onAddHeader}
          onRemoveHeader={onRemoveHeader}
          onUpdateHeader={onUpdateHeader}
        />
      )}

      {!isBrowserScript && ['POST', 'PUT', 'PATCH'].includes(action.type) && (
        <div className="sub-section">
          <h4>Body</h4>
          <ActionBodyTextarea
            onChange={value => onUpdate(index, 'body', value)}
            onContextMenu={onSelectionMenu}
            placeholder='{"key": "value"}'
            rows={4}
            value={action.body || ''}
          />
        </div>
      )}

      {!isBrowserScript && (
        <ProtocolExtractorsSection
          action={action}
          index={index}
          protocol={isSoapScript ? 'soap' : 'http'}
          onAddExtractor={onAddExtractor}
          onRemoveExtractor={onRemoveExtractor}
          onUpdateExtractor={onUpdateExtractor}
        />
      )}

      {!isBrowserScript && (
        <ProtocolAssertionsSection
          action={action}
          index={index}
          protocol={isSoapScript ? 'soap' : 'http'}
          onAddAssertion={onAddAssertion}
          onRemoveAssertion={onRemoveAssertion}
          onUpdateAssertion={onUpdateAssertion}
        />
      )}
    </>
  )
}

interface SoapActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function SoapActionFields({ action, index, onUpdate }: SoapActionFieldsProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>SOAP Action</h4></div>
      <div className="form-grid">
        <label>
          <span>SOAPAction</span>
          <input value={action.soap?.action || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), action: e.target.value })} placeholder="urn:CreateOrder" />
        </label>
        <label>
          <span>Operation</span>
          <input value={action.soap?.operation || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), operation: e.target.value })} placeholder="CreateOrder" />
        </label>
        <label>
          <span>Body Mode</span>
          <select value={action.soap?.body_mode || 'raw_xml'} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), body_mode: e.target.value })}>
            <option value="raw_xml">raw_xml</option>
            <option value="generated">generated</option>
          </select>
        </label>
      </div>
    </div>
  )
}

interface ProtocolHeadersSectionProps {
  action: Action
  index: number
  onAddHeader: (i: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function ProtocolHeadersSection({ action, index, onAddHeader, onRemoveHeader, onUpdateHeader }: ProtocolHeadersSectionProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
      {Object.entries(action.headers || {}).map(([key, value]) => (
        <div key={key} className="kv-row">
          <input className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Key" />
          <input className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
          <button className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
        </div>
      ))}
    </div>
  )
}

interface ProtocolExtractorsSectionProps {
  action: Action
  index: number
  protocol: string
  onAddExtractor: (i: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

function extractorSourceOptions(protocol: string): Array<[string, string]> {
  switch (protocol) {
    case 'jms-solace':
      return [['body', 'Body'], ['header', 'Header'], ['property', 'Property'], ['message_id', 'Message ID'], ['correlation_id', 'Correlation ID']]
    case 'database':
      return [['body', 'Result Body'], ['rows', 'Rows'], ['scalar', 'Scalar'], ['row_count', 'Row Count'], ['rows_affected', 'Rows Affected'], ['last_insert_id', 'Last Insert ID']]
    case 'network':
      return [['body', 'Body'], ['response', 'Response'], ['records', 'Records'], ['record_count', 'Record Count'], ['entry_count', 'Entry Count'], ['target', 'Target']]
    case 'grpc':
      return [['body', 'Message Body'], ['metadata', 'Metadata'], ['trailer', 'Trailer'], ['status', 'Status']]
    case 'websocket':
      return [['body', 'Message Body'], ['header', 'Handshake Header'], ['message', 'Message'], ['close_code', 'Close Code']]
    default:
      return [['body', 'Body'], ['header', 'Header'], ['cookie', 'Cookie']]
  }
}

function extractorTypeOptions(protocol: string): Array<[string, string]> {
  if (protocol === 'database' || protocol === 'network' || protocol === 'grpc') {
    return [['jsonpath', 'JSONPath'], ['jmespath', 'JMESPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  if (protocol === 'jms-solace' || protocol === 'websocket') {
    return [['jsonpath', 'JSONPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  return [['jsonpath', 'JSONPath'], ['regex', 'Regex'], ['boundary', 'Boundary'], ['xpath', 'XPath']]
}

function extractorExpressionPlaceholder(protocol: string, source: string) {
  if (protocol === 'database') return source === 'scalar' ? '$.scalar' : '$.rows[0].id'
  if (protocol === 'network') return source === 'record_count' ? '$.record_count' : '$.response'
  if (protocol === 'grpc') return '$.id'
  if (protocol === 'websocket') return '$.type'
  if (protocol === 'jms-solace') return '$.orderId'
  return '$.data.id'
}

function ProtocolExtractorsSection({ action, index, protocol, onAddExtractor, onRemoveExtractor, onUpdateExtractor }: ProtocolExtractorsSectionProps) {
  const sourceOptions = extractorSourceOptions(protocol)
  const typeOptions = extractorTypeOptions(protocol)
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Extractors</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddExtractor(index)}>+ Add</button></div>
      {(action.extractors || []).map((extractor, extractorIndex) => (
        <div key={extractorIndex} className="extractor-row">
          <input value={extractor.name} onChange={e => onUpdateExtractor(index, extractorIndex, 'name', e.target.value)} placeholder="Variable name" />
          <select value={extractor.source} onChange={e => onUpdateExtractor(index, extractorIndex, 'source', e.target.value)}>
            {sourceOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <select value={extractor.type} onChange={e => onUpdateExtractor(index, extractorIndex, 'type', e.target.value)}>
            {typeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <input value={extractor.expression} onChange={e => onUpdateExtractor(index, extractorIndex, 'expression', e.target.value)} placeholder={extractorExpressionPlaceholder(protocol, extractor.source)} className="ext-expr" />
          <button className="btn-icon danger" onClick={() => onRemoveExtractor(index, extractorIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}

interface ProtocolAssertionsSectionProps {
  action: Action
  index: number
  protocol: string
  onAddAssertion: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
}

function assertionOptions(protocol: string): Array<[string, string]> {
  switch (protocol) {
    case 'jms-solace':
      return [['message_received', 'Message Received'], ['body_contains', 'Body Contains'], ['property_equals', 'Property Equals'], ['header_equals', 'Header Equals'], ['correlation_match', 'Correlation Match'], ['response_time_ms', 'Response Time (ms)']]
    case 'websocket':
      return [['message_received', 'Message Received'], ['body_contains', 'Message Contains'], ['close_code', 'Close Code'], ['response_time_ms', 'Response Time (ms)']]
    case 'grpc':
      return [['grpc_status', 'gRPC Status'], ['message_count', 'Message Count'], ['body_contains', 'Body Contains'], ['metadata_contains', 'Metadata Contains'], ['response_time_ms', 'Response Time (ms)']]
    case 'database':
      return [['row_count', 'Row Count'], ['rows_affected', 'Rows Affected'], ['scalar_equals', 'Scalar Equals'], ['body_contains', 'Result Contains'], ['body_not_contains', 'Result Excludes'], ['response_time_ms', 'Response Time (ms)']]
    case 'network':
      return [['record_count', 'Record Count'], ['entry_count', 'Entry Count'], ['messages_received', 'Messages Received'], ['body_contains', 'Transcript Contains'], ['body_not_contains', 'Transcript Excludes'], ['status_code', 'Status Code'], ['response_time_ms', 'Response Time (ms)']]
    default:
      return [['status_code', 'Status Code'], ['response_time_ms', 'Response Time (ms)'], ['body_contains', 'Body Contains'], ['header_contains', 'Header Contains']]
  }
}

function assertionUsesNumericExpected(type: string) {
  return ['status_code', 'grpc_status', 'row_count', 'rows_affected', 'record_count', 'entry_count', 'message_count', 'messages_received', 'close_code'].includes(type)
}

function assertionUsesTextValue(type: string) {
  return ['body_contains', 'body_not_contains', 'header_contains', 'metadata_contains', 'correlation_match'].includes(type)
}

function assertionUsesNamedExpected(type: string) {
  return ['property_equals', 'header_equals', 'scalar_equals'].includes(type)
}

function ProtocolAssertionsSection({ action, index, protocol, onAddAssertion, onRemoveAssertion, onUpdateAssertion }: ProtocolAssertionsSectionProps) {
  const options = assertionOptions(protocol)
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Assertions</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddAssertion(index)}>+ Add</button></div>
      {(action.assertions || []).map((assertion, assertionIndex) => (
        <div key={assertionIndex} className="assertion-row">
          <select value={assertion.type} onChange={e => onUpdateAssertion(index, assertionIndex, 'type', e.target.value)}>
            {options.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          {assertionUsesNumericExpected(assertion.type) && <input type="number" value={typeof assertion.expected === 'number' ? assertion.expected : 0} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', Number(e.target.value))} />}
          {assertion.type === 'response_time_ms' && <input type="number" value={assertion.max ?? 5000} onChange={e => onUpdateAssertion(index, assertionIndex, 'max', Number(e.target.value))} placeholder="Max ms" />}
          {assertionUsesTextValue(assertion.type) && <input value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder="Expected text" />}
          {assertionUsesNamedExpected(assertion.type) && assertion.type !== 'scalar_equals' && <input value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder="Name" />}
          {assertionUsesNamedExpected(assertion.type) && <input value={typeof assertion.expected === 'string' || typeof assertion.expected === 'number' ? String(assertion.expected) : ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', e.target.value)} placeholder="Expected value" />}
          <button className="btn-icon danger" onClick={() => onRemoveAssertion(index, assertionIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}
