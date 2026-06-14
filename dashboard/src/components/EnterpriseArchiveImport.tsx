import { useEffect, useMemo, useState, useRef, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { type DetectedSourceProtocol, type ImportRepairSuggestion, type EnterpriseArchiveImportResult, type EnterpriseArchiveImportSummary, type EnterpriseArchiveMigrationReadiness, type EnterpriseArchiveProtocolSupportEntry, type EnterpriseArchiveReviewArtifact, type EnterpriseArchiveSupportMatrixEntry, type EnterpriseArchiveSupportMatrixExample, type EnterpriseArchiveSupportMatrixResponse, type SalesforceReview, type TrueClient2CompatReport } from '../types/scriptImports'
import { type BrowserReplayEvent, type BrowserReplaySession } from '../types'
import { type SourceArchiveReview, type ScriptActionDefinition, type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { buildReplayStartPayload, getImportSummary, summarizeInlineData } from './scriptEditorUtils'
import ScriptImportReview from './ScriptImportReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { salesforceApiSurfaceSummary } from './salesforceReviewSummary'
import { type DraftEditorFocus, type ImportedSaveResult } from './useScriptsAuthoringState'
import './EnterpriseArchiveImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

function getImportMetadata(script?: ScriptDefinition): ImportMetadata | null {
  return script?.metadata?.import || null
}

function getSourceReviewFromMetadata(metadata?: ImportMetadata | null): SourceArchiveReview | null {
  const sourceReview = metadata?.preserved?.source_review as SourceArchiveReview | undefined
  if (!sourceReview) return null
  if (!Array.isArray(sourceReview.source_files) && !Array.isArray(sourceReview.mappings)) return null
  return sourceReview
}

function getSummaryList(values?: string[]) {
  return Array.isArray(values) ? values : []
}

function isBrowserImport(script: ScriptDefinition | null): boolean {
  return script?.protocol === 'browser'
}

function isEnterpriseArchiveZipFile(file: File) {
  const lowerName = file.name.toLowerCase()
  return lowerName.endsWith('.zip') || file.type === 'application/zip' || file.type === 'application/x-zip-compressed'
}

function isHARFile(file: File) {
  const lowerName = file.name.toLowerCase()
  return lowerName.endsWith('.har') || lowerName.endsWith('.json') || file.type === 'application/json'
}

function formatEnterpriseArchiveFileSize(file: File) {
  return `${(file.size / 1024).toFixed(1)} KB`
}

function formatSalesforceCoverage(review?: SalesforceReview | null) {
  const coverage = review?.har_match_coverage
  if (!coverage?.har_provided) return 'No HAR matched'
  return `${coverage.matched_requests}/${coverage.imported_requests} matched (${coverage.coverage_pct.toFixed(1)}%)`
}

function salesforceDomainSummary(review?: SalesforceReview | null) {
  const domains = review?.domains || []
  if (domains.length === 0) return 'No Salesforce domains detected'
  return domains.slice(0, 3).map(domain => domain.host).join(', ')
}

function salesforceTransactionCount(review?: SalesforceReview | null) {
  return review?.transactions?.length || 0
}

function scriptActionCount(script?: ScriptDefinition | null) {
  if (!script) return 0
  const phaseActions = (phase: 'init' | 'actions' | 'teardown') => {
    const value = (script as ScriptDefinition & Record<string, unknown>)[phase]
    return Array.isArray(value) ? value.length : 0
  }
  return phaseActions('init') + phaseActions('actions') + phaseActions('teardown')
}

function orderedImportScriptActions(script?: ScriptDefinition | null): ScriptActionDefinition[] {
  if (!script) return []
  const phasedScript = script as ScriptDefinition & {
    init?: ScriptActionDefinition[]
    teardown?: ScriptActionDefinition[]
  }
  return [
    ...(phasedScript.init || []),
    ...(script.actions || []),
    ...(phasedScript.teardown || []),
  ]
}

function flattenImportScriptActions(actions: ScriptActionDefinition[]): ScriptActionDefinition[] {
  const flattened: ScriptActionDefinition[] = []
  actions.forEach(action => {
    flattened.push(action)
    if (action.control?.steps?.length) {
      flattened.push(...flattenImportScriptActions(action.control.steps))
    }
    if (action.control?.else?.length) {
      flattened.push(...flattenImportScriptActions(action.control.else))
    }
  })
  return flattened
}

function importScriptActionsDeep(script?: ScriptDefinition | null): ScriptActionDefinition[] {
  return flattenImportScriptActions(orderedImportScriptActions(script))
}

function getTrueClient2CompatReport(result: EnterpriseArchiveImportResult | null, metadata?: ImportMetadata | null): TrueClient2CompatReport | null {
  const topLevel = result?.trueclient2_compat_report
  if (topLevel) return topLevel
  const summaryReport = result?.summary?.trueclient2_compat_report
  if (summaryReport) return summaryReport
  const preservedReport = metadata?.preserved?.trueclient2_compat_report
  if (preservedReport && typeof preservedReport === 'object' && !Array.isArray(preservedReport)) {
    return preservedReport as TrueClient2CompatReport
  }
  return null
}

function enterpriseArchiveImportStatusLabel(status: EnterpriseArchiveImportQueueStatus) {
  switch (status) {
    case 'validating':
      return 'Validating'
    case 'validated':
      return 'Validated'
    case 'converting':
      return 'Converting'
    case 'converted':
      return 'Converted'
    case 'failed':
      return 'Failed'
    default:
      return 'Queued'
  }
}

function buildEnterpriseArchiveImportQueueItem(file: File, id: string): EnterpriseArchiveImportQueueItem {
  return {
    id,
    file,
    harFile: null,
    status: 'queued',
    result: null,
    reviewMode: null,
    error: '',
    notice: '',
    replayValidationLoading: false,
    replayValidationError: '',
    replayValidationWarnings: [],
    replayValidationSession: null,
  }
}

function buildReviewArtifactDraft(
  artifact: EnterpriseArchiveReviewArtifact,
  detectedProtocol: DetectedSourceProtocol | null,
  projectId?: string,
  requestedName?: string,
  sourceReview?: SourceArchiveReview | null,
): ScriptDefinition {
  const protocol = detectedProtocol?.apexload_protocol || detectedProtocol?.canonical_source_protocol || 'review'
  const name = requestedName?.trim() || artifact.title || detectedProtocol?.display_name || 'Enterprise Script Archive Protocol Review'
  return {
    name,
    version: '1.0.0',
    protocol,
    project_id: projectId,
    generated_by: 'apexload-enterprise-script-archive-importer',
    generated_from: 'enterprise_script_archive',
    config: { base_url: '' },
    actions: [],
    metadata: {
      import: {
        source: 'enterprise_script_archive',
        source_script_name: name,
        preserved: {
          detected_protocol: detectedProtocol,
          review_artifact: artifact,
          ...(sourceReview ? { source_review: sourceReview } : {}),
        },
      },
    },
  } as ScriptDefinition
}

interface Props {
  projectId?: string
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'
type EnterpriseArchiveImportQueueStatus = 'queued' | 'validating' | 'validated' | 'converting' | 'converted' | 'failed'

interface EnterpriseArchiveImportQueueItem {
  id: string
  file: File
  harFile: File | null
  status: EnterpriseArchiveImportQueueStatus
  result: EnterpriseArchiveImportResult | null
  reviewMode: ReviewMode | null
  error: string
  notice: string
  replayValidationLoading: boolean
  replayValidationError: string
  replayValidationWarnings: string[]
  replayValidationSession: BrowserReplaySession | null
}

type EnterpriseArchiveSupportMatrixSection = {
  key: string
  title: string
  items: EnterpriseArchiveSupportMatrixEntry[]
  preview: string
}

type EnterpriseArchiveProtocolCoverageSection = {
  key: string
  title: string
  items: EnterpriseArchiveProtocolSupportEntry[]
  preview: string
}

type EditorActionButton = {
  key: string
  label: string
  onClick: () => void
}

type ImportOptions = {
  name: string
  base_url: string
  include_assertions: boolean
  preserve_transactions: boolean
  salesforce_native: boolean
  block_partial: boolean
  block_compatibility_only: boolean
  block_non_applicable: boolean
}

type RelaxableMigrationBlockStatus = 'partial' | 'compatibility_only' | 'non_applicable'

const DEFAULT_IMPORT_OPTIONS: ImportOptions = {
  name: '',
  base_url: '',
  include_assertions: true,
  preserve_transactions: true,
  salesforce_native: false,
  block_partial: true,
  block_compatibility_only: true,
  block_non_applicable: false,
}
const EMPTY_REPLAY_VALIDATION_WARNINGS: string[] = []

interface BrowserReplayEvidenceSummary {
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

interface PromotionReviewInputs {
  compatReport?: TrueClient2CompatReport | null
  replaySession?: BrowserReplaySession | null
  replayWarnings?: string[]
  importWarnings?: string[]
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

function formatRuntimeSettingStatusLabel(status?: string | null) {
  const normalized = status?.trim().replace(/[_-]+/g, ' ')
  if (!normalized) return 'Unknown'
  return normalized.replace(/\b\w/g, char => char.toUpperCase())
}

function formatEvidenceCount(count: number, singular: string, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`
}

function getReplaySessionEvents(session?: BrowserReplaySession | null): BrowserReplayEvent[] {
  if (Array.isArray(session?.events) && session.events.length > 0) return session.events
  if (Array.isArray(session?.report?.events) && session.report.events.length > 0) return session.report.events
  return session?.current_event ? [session.current_event] : []
}

function summarizeBrowserReplayEvidence(session?: BrowserReplaySession | null): BrowserReplayEvidenceSummary {
  const events = getReplaySessionEvents(session)
  const manifest = session?.report?.artifact_manifest
  const sourceSteps = new Set<string>()
  const sourceTransactions = new Set<string>()
  let screenshots = 0
  let domHtmlCaptures = 0
  let consoleEntries = 0
  let networkEntries = 0
  let passed = 0
  let failed = 0

  events.forEach((event) => {
    const snapshot = event.snapshot
    if (event.success) {
      passed += 1
    } else {
      failed += 1
    }
    if (event.screenshot_base64 || snapshot?.screenshot_base64) screenshots += 1
    if (event.html || event.dom_summary || snapshot?.html || snapshot?.dom_summary) domHtmlCaptures += 1
    consoleEntries += (event.console_entries || snapshot?.console_entries || []).length
    networkEntries += (event.network_entries || snapshot?.network_entries || []).length
    const sourceStep = event.source_step_id || event.source_action || event.source_action_name || (
      typeof event.source_step_index === 'number' ? `source-step-${event.source_step_index}` : ''
    )
    if (sourceStep) sourceSteps.add(sourceStep)
    if (event.source_transaction) sourceTransactions.add(event.source_transaction)
  })

  return {
    events: events.length,
    passed,
    failed,
    screenshots: Math.max(screenshots, manifest?.screenshots || 0),
    domHtmlCaptures: Math.max(domHtmlCaptures, manifest?.snapshots || 0, manifest?.html_captured ? 1 : 0),
    consoleEntries: Math.max(consoleEntries, manifest?.console_entries || 0),
    networkEntries: Math.max(networkEntries, manifest?.network_entries || 0),
    sourceSteps: sourceSteps.size,
    sourceTransactions: sourceTransactions.size,
  }
}

function buildTrueClientPromotionReview(script: ScriptDefinition, inputs: PromotionReviewInputs): Record<string, unknown> {
  const metadata = getImportMetadata(script)
  const actions = importScriptActionsDeep(script)
  const replayEvidence = summarizeBrowserReplayEvidence(inputs.replaySession)
  const reviewRequiredActions = actions.filter(action => Boolean(action.metadata?.review_required)).length
  const javascriptReviewActions = actions.filter(action => action.type === 'browser_evaluate_js' || action.metadata?.review_gate === 'javascript').length
  const selectorFallbackCandidates = actions.reduce((count, action) => {
    const fallbacks = action.metadata?.source_selector_fallbacks
    return count + (Array.isArray(fallbacks) ? fallbacks.length : 0)
  }, 0)
  const replayAttached = Boolean(inputs.replaySession?.id && inputs.replaySession.status === 'completed' && replayEvidence.failed === 0)

  return {
    schema_version: 1,
    status: replayAttached ? 'replay_evidence_attached' : 'needs_replay_evidence',
    generated_at: new Date().toISOString(),
    source: metadata?.source || 'enterprise_script_archive',
    source_script_name: metadata?.source_script_name || '',
    compatibility: {
      source_profile: inputs.compatReport?.source_profile,
      import_mode: inputs.compatReport?.import_mode,
      promotion_status: inputs.compatReport?.promotion_status,
      promotion_allowed: inputs.compatReport?.promotion_allowed ?? false,
      runtime_parity_status: inputs.compatReport?.runtime_parity_status,
      replay_status: replayAttached ? 'attached' : inputs.compatReport?.replay_status,
      javascript_review_gate: inputs.compatReport?.javascript_review_gate,
      http_conversion_readiness: inputs.compatReport?.http_conversion_readiness,
      selector_confidence_pct: inputs.compatReport?.selector_confidence_pct,
      frame_window_blockers: inputs.compatReport?.frame_window_blockers ?? 0,
    },
    gates: {
      review_required_actions: reviewRequiredActions,
      javascript_review_actions: javascriptReviewActions,
      selector_fallback_candidates: selectorFallbackCandidates,
      replay_required: !replayAttached,
      replay_warnings: inputs.replayWarnings || [],
      import_warnings: inputs.importWarnings || [],
    },
    replay_evidence: {
      session_id: inputs.replaySession?.id || '',
      status: inputs.replaySession?.status || 'not_run',
      started_at: inputs.replaySession?.started_at || '',
      completed_at: inputs.replaySession?.completed_at || inputs.replaySession?.report?.completed_at || '',
      events: replayEvidence.events,
      passed: replayEvidence.passed,
      failed: replayEvidence.failed,
      screenshots: replayEvidence.screenshots,
      dom_html_captures: replayEvidence.domHtmlCaptures,
      console_entries: replayEvidence.consoleEntries,
      network_entries: replayEvidence.networkEntries,
      source_steps: replayEvidence.sourceSteps,
      source_transactions: replayEvidence.sourceTransactions,
    },
    next_actions: replayAttached
      ? ['Review selector fallbacks and JavaScript approvals before production promotion.']
      : ['Run draft browser replay and attach evidence before production promotion.'],
  }
}

function attachTrueClientPromotionReview(script: ScriptDefinition, inputs: PromotionReviewInputs): ScriptDefinition {
  const promotionReview = buildTrueClientPromotionReview(script, inputs)
  const existingMetadata = script.metadata || {}
  const existingImport = existingMetadata.import || {}
  return {
    ...script,
    metadata: {
      ...existingMetadata,
      import: {
        ...existingImport,
        preserved: {
          ...(existingImport.preserved || {}),
          trueclient2_promotion_review: promotionReview,
        },
      },
    },
  }
}

function normalizeReplaySuggestionCopy(value?: string | null) {
  if (!value) return ''

  return value
    .replace(/\bbrowser replay\b/gi, (match) => (
      match[0] === 'B' ? 'Draft Browser Replay' : 'draft browser replay'
    ))
    .replace(/before saving as production-ready/gi, 'before saving it to the library')
}

function formatMigrationBlockPolicySummary(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  return buildMigrationBlockStatuses(options)
    .map((status) => formatRuntimeSettingStatusLabel(status))
    .join(', ')
}

function hasCustomMigrationBlockPolicy(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  return options.block_partial !== DEFAULT_IMPORT_OPTIONS.block_partial
    || options.block_compatibility_only !== DEFAULT_IMPORT_OPTIONS.block_compatibility_only
    || options.block_non_applicable !== DEFAULT_IMPORT_OPTIONS.block_non_applicable
}

function buildMigrationPolicyOptions(statuses?: string[] | null): Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'> {
  const normalizedStatuses = new Set((statuses || []).map((status) => status.trim().toLowerCase()).filter(Boolean))
  return {
    block_partial: normalizedStatuses.has('partial'),
    block_compatibility_only: normalizedStatuses.has('compatibility_only'),
    block_non_applicable: normalizedStatuses.has('non_applicable'),
  }
}

function sameMigrationBlockPolicy(
  left: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
  right: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
) {
  return left.block_partial === right.block_partial
    && left.block_compatibility_only === right.block_compatibility_only
    && left.block_non_applicable === right.block_non_applicable
}

function buildMigrationPolicyRefreshNotice(mode: 'convert' | 'validate', options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  const prefix = mode === 'validate' ? 'Validation refreshed' : 'Conversion refreshed'
  return `${prefix} with migration block policy: ${formatMigrationBlockPolicySummary(options)}.`
}

function findImportedActionName(script?: ScriptDefinition | null, location?: string | null): string | null {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation || !Array.isArray(script?.actions)) return null

  const match = normalizedLocation.match(/^(?:Step|Request)\s+(\d+)$/i)
  if (!match) return null

  const actionIndex = Number.parseInt(match[1], 10) - 1
  if (!Number.isInteger(actionIndex) || actionIndex < 0 || actionIndex >= script.actions.length) {
    return null
  }

  return script.actions[actionIndex]?.name?.trim() || null
}

function findFirstImportedActionTarget(script: ScriptDefinition | null, locations: Array<string | null | undefined>): { location: string; actionName: string } | null {
  for (const location of locations) {
    const normalizedLocation = location?.trim()
    if (!normalizedLocation) continue

    const actionName = findImportedActionName(script, normalizedLocation)
    if (actionName) {
      return { location: normalizedLocation, actionName }
    }
  }

  return null
}

function findFirstImportedRuntimeArea(entries?: Array<{ area?: string | null }> | null): string | null {
  if (!Array.isArray(entries)) return null

  for (const entry of entries) {
    const area = entry?.area?.trim()
    if (area && getImportedRuntimeSectionId(area)) {
      return area
    }
  }

  return null
}

function findUnsupportedRepairSuggestion(repairSuggestions: ImportRepairSuggestion[], unsupportedFunction: string): ImportRepairSuggestion | null {
  const normalizedFunction = unsupportedFunction.trim().toLowerCase()
  if (!normalizedFunction) return null

  return repairSuggestions.find((suggestion) => {
    if (suggestion.category !== 'unsupported' || !suggestion.target_action?.trim()) {
      return false
    }
    return suggestion.title.toLowerCase().includes(normalizedFunction)
  }) || null
}

export default function EnterpriseArchiveImport({ projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const harFileRef = useRef<HTMLInputElement>(null)
  const itemCounterRef = useRef(0)
  const [importItems, setImportItems] = useState<EnterpriseArchiveImportQueueItem[]>([])
  const [selectedItemId, setSelectedItemId] = useState('')
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [supportMatrix, setSupportMatrix] = useState<EnterpriseArchiveSupportMatrixEntry[]>([])
  const [supportMatrixNotes, setSupportMatrixNotes] = useState<string[]>([])
  const [supportMatrixExamples, setSupportMatrixExamples] = useState<EnterpriseArchiveSupportMatrixExample[]>([])
  const [protocolSupportMatrix, setProtocolSupportMatrix] = useState<EnterpriseArchiveProtocolSupportEntry[]>([])
  const [runtimeCoverageOpen, setRuntimeCoverageOpen] = useState(false)
  const [reviewArtifactSaving, setReviewArtifactSaving] = useState(false)

  const [options, setOptions] = useState<ImportOptions>(DEFAULT_IMPORT_OPTIONS)
  const selectedItem = useMemo(
    () => importItems.find(item => item.id === selectedItemId) || importItems[0] || null,
    [importItems, selectedItemId],
  )
  const selectedHARFile = selectedItem?.harFile ?? null
  const result = selectedItem?.result ?? null
  const reviewMode = selectedItem?.reviewMode ?? null
  const replayValidationLoading = selectedItem?.replayValidationLoading ?? false
  const replayValidationError = selectedItem?.replayValidationError ?? ''
  const replayValidationWarnings = selectedItem?.replayValidationWarnings ?? EMPTY_REPLAY_VALIDATION_WARNINGS
  const replayValidationSession = selectedItem?.replayValidationSession ?? null
  const displayError = error || selectedItem?.error || ''
  const displayNotice = notice || selectedItem?.notice || ''
  const queuedFileCount = importItems.length
  const validateButtonLabel = queuedFileCount > 1 ? `Validate ${queuedFileCount} Scripts` : 'Validate Only'
  const convertButtonLabel = queuedFileCount > 1 ? `Convert ${queuedFileCount} Enterprise Script Archives` : 'Convert Enterprise Script Archive'
  const importedScript = result?.script ?? result?.scripts?.[0] ?? null
  const detectedProtocol = result?.detected_protocol ?? result?.summary?.detected_protocol ?? null
  const reviewArtifact = result?.review_artifact ?? result?.summary?.review_artifact ?? null
  const executableImport = !detectedProtocol || detectedProtocol.import_mode === 'executable'
  const importMetadata = getImportMetadata(importedScript || undefined)
  const trueClient2CompatReport = getTrueClient2CompatReport(result, importMetadata)
  const importSummary = getImportSummary(importMetadata)
  const reviewSummary: EnterpriseArchiveImportSummary | null = result?.summary || null
  const salesforceReview = reviewSummary?.salesforce_review || null
  const salesforceNativeScript = salesforceReview?.native_preview?.script || null
  const sourceReview = reviewSummary?.source_review || getSourceReviewFromMetadata(importMetadata)
  const projectSections = getSummaryList(reviewSummary?.project_sections).length > 0
    ? getSummaryList(reviewSummary?.project_sections)
    : importSummary.projectSections
  const runtimeFiles = getSummaryList(reviewSummary?.runtime_files).length > 0
    ? getSummaryList(reviewSummary?.runtime_files)
    : importSummary.runtimeFiles
  const parameterFiles = getSummaryList(reviewSummary?.parameter_files).length > 0
    ? getSummaryList(reviewSummary?.parameter_files)
    : importSummary.parameterFiles
  const unsupportedFunctions = result?.unsupported_elements.length
    ? result.unsupported_elements
    : importSummary.unsupportedFunctions
  const reviewSections = result?.review_sections || []
  const repairSuggestions = result?.repair_suggestions || []
  const supportMatrixSections = summarizeEnterpriseArchiveSupportMatrix(supportMatrix)
  const hasRuntimeCoverage = supportMatrix.length > 0
  const protocolCoverageSections = summarizeEnterpriseArchiveProtocolSupport(protocolSupportMatrix)
  const migrationReadiness: EnterpriseArchiveMigrationReadiness | null = reviewSummary?.migration_readiness || null
  const reviewedMigrationPolicy = migrationReadiness?.policy?.block_statuses
    ? buildMigrationPolicyOptions(migrationReadiness.policy.block_statuses)
    : null
  const hasPendingMigrationPolicyChanges = reviewedMigrationPolicy !== null && !sameMigrationBlockPolicy(options, reviewedMigrationPolicy)
  const saveBlockedByMigrationPolicy = Boolean(migrationReadiness?.blocked)
  const saveBlockedByProtocolGate = Boolean(detectedProtocol && detectedProtocol.import_mode !== 'executable')
  const externalRunnerGate = detectedProtocol?.import_mode === 'external_runner_required'
  const protocolGateName = detectedProtocol?.display_name || detectedProtocol?.canonical_source_protocol || 'This protocol'
  const protocolGateRunnerRequirement = detectedProtocol?.runner_requirement || reviewArtifact?.protocol?.runner_requirement || ''
  const protocolGateRequiredEvidence = Array.from(new Set([
    ...(reviewArtifact?.required_evidence || []),
    ...(detectedProtocol?.required_evidence || []),
  ]))
  const protocolGateDetails = [
    protocolGateRunnerRequirement ? `Runner: ${protocolGateRunnerRequirement}` : '',
    reviewArtifact?.title ? `Review artifact: ${reviewArtifact.title}` : '',
    ...protocolGateRequiredEvidence.map(evidence => `Required evidence: ${evidence}`),
    ...(detectedProtocol?.signals || []).slice(0, 3).map(signal => `Signal: ${signal}`),
  ].filter(Boolean)
  const browserImport = isBrowserImport(importedScript)
  const browserReplayStatus = replayValidationSession?.status || null
  const browserReplayEvidence = useMemo(
    () => summarizeBrowserReplayEvidence(replayValidationSession),
    [replayValidationSession],
  )
  const browserReplayStarting = browserImport && replayValidationLoading
  const browserReplayStatusLabel = browserReplayStarting
    ? 'Starting'
    : formatRuntimeSettingStatusLabel(browserReplayStatus || 'not run')
  const browserReplayWasStarted = browserImport && replayValidationSession !== null
  const browserReplayInProgress = browserImport && (browserReplayStarting || browserReplayStatus === 'queued' || browserReplayStatus === 'running' || browserReplayStatus === 'paused')
  const browserReplayNeedsRerun = browserImport && !browserReplayInProgress && (replayValidationSession === null || browserReplayStatus === 'failed' || browserReplayStatus === 'stopped')
  const saveRequiresBrowserReplay = executableImport && browserReplayNeedsRerun
  const primarySaveDisabled = saveBlockedByProtocolGate
    ? (!reviewArtifact || reviewArtifactSaving)
    : (saveBlockedByMigrationPolicy || replayValidationLoading || browserReplayInProgress)
  const saveActionLabel = saveBlockedByProtocolGate
    ? (reviewArtifactSaving ? 'Saving Review Artifact...' : 'Save Review Artifact Instead')
    : (browserReplayInProgress
    ? 'Draft Browser Replay In Progress'
    : (saveRequiresBrowserReplay
        ? (browserReplayWasStarted ? 'Rerun Draft Browser Replay Before Saving' : 'Start Draft Browser Replay Before Saving')
        : (reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save to Script Library')))
  const replayActionLabel = replayValidationLoading
    ? 'Starting Draft Browser Replay...'
    : (browserReplayInProgress
        ? 'Draft Browser Replay In Progress'
      : (browserReplayWasStarted ? 'Rerun Draft Browser Replay' : 'Start Draft Browser Replay'))
  const replayPanelDescription = browserReplayInProgress
    ? 'Wait for the draft browser replay to finish before saving it to the library.'
    : (saveRequiresBrowserReplay
      ? `${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay against the converted browser script before saving it to the library.`
      : 'The latest draft browser replay completed. Review the draft browser replay results before saving it to the library.')
  const reviewSubtitle = reviewMode === 'validation'
    ? (saveBlockedByMigrationPolicy
        ? 'This preview came from the dry-run validator. Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.'
        : (browserReplayInProgress
        ? 'This preview came from the dry-run validator. Wait for the draft browser replay to finish before saving it to the library.'
            : (saveBlockedByProtocolGate
          ? (externalRunnerGate
              ? `This preview came from the dry-run validator. ${protocolGateName} execution requires ${protocolGateRunnerRequirement || 'an approved external runner'}; save the review artifact before promotion.`
              : 'This preview came from the dry-run validator. Save the review artifact before script promotion.')
              : (saveRequiresBrowserReplay
          ? `This preview came from the dry-run validator. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving it to the library.`
                : 'This preview came from the dry-run validator. You can still save the reviewed script if the output looks correct.'))))
    : (saveBlockedByMigrationPolicy
        ? 'Review the generated script, preserved metadata, and warnings. Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.'
        : (browserReplayInProgress
        ? 'Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving it to the library.'
            : (saveBlockedByProtocolGate
          ? (externalRunnerGate
              ? `Review the generated ${protocolGateName} draft and save the review artifact; execution requires ${protocolGateRunnerRequirement || 'an approved external runner'}.`
              : 'Review the protocol classification and save the review artifact; this import is not executable yet.')
              : (saveRequiresBrowserReplay
          ? `Review the generated script, preserved metadata, and warnings. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving it to the library.`
                : 'Review the generated script, preserved metadata, and warnings before saving.'))))
  const runtimeBlockingSettings = importSummary.runtimeSettingReadiness?.blocking_settings || []
  const generatedDataSources = importedScript
    ? (importedScript.data_sources || []).map((source, index) => {
      const summary = summarizeInlineData(source)
      const columns = source.first_row_header === false
        ? source.columns || []
        : []
      return {
        name: source.name || `source_${index + 1}`,
        type: source.type || 'csv',
        rows: summary.rows,
        columns: columns.length > 0 ? columns : summary.columns,
      }
    })
    : []
  const firstCorrelationSourceTarget = importSummary.correlationSuggestions
    .map((correlation) => findFirstImportedActionTarget(importedScript, [correlation.found_at]))
    .find((target): target is { location: string; actionName: string } => Boolean(target)) || null
  const firstCorrelationUsageTarget = importSummary.correlationSuggestions
    .map((correlation) => findFirstImportedActionTarget(importedScript, correlation.used_at))
    .find((target): target is { location: string; actionName: string } => Boolean(target)) || null
  const firstRuntimeBlockingArea = findFirstImportedRuntimeArea(runtimeBlockingSettings)
  const firstRuntimeParityArea = findFirstImportedRuntimeArea(importSummary.runtimeSettingsParity)
  const firstMappedParameterDataSourceName = importSummary.parameterFileSections
    .map((section) => findImportedParameterMappingDataSourceName(generatedDataSources, section.values))
    .find((sourceName): sourceName is string => Boolean(sourceName)) || null
  const firstGeneratedDataSourceName = generatedDataSources[0]?.name || null
  const firstUnsupportedTargetAction = repairSuggestions
    .find((suggestion) => suggestion.category === 'unsupported' && Boolean(suggestion.target_action?.trim()))
    ?.target_action?.trim() || null
  const actionWrapStyle: CSSProperties = { marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }
  const sectionActionStyle: CSSProperties = { marginBottom: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }

  const updateImportItem = (itemId: string, patch: Partial<EnterpriseArchiveImportQueueItem>) => {
    setImportItems(items => items.map(item => item.id === itemId ? { ...item, ...patch } : item))
  }

  const openImportedScriptInEditor = (focus?: DraftEditorFocus) => {
    if (!importedScript || !onEdit) return
    onEdit(importedScript, focus)
  }

  const openSalesforceNativePreviewInEditor = () => {
    if (!salesforceNativeScript || !onEdit) return
    onEdit(salesforceNativeScript)
  }

  const openImportedRuntimeSettingsInEditor = (area: string) => {
    openImportedScriptInEditor({ targetSection: 'runtime', targetRuntimeArea: area })
  }

  const openImportedDataSourceInEditor = (sourceName?: string | null) => {
    const normalizedSourceName = sourceName?.trim()
    if (!normalizedSourceName) return
    openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: normalizedSourceName })
  }

  const openImportedParameterDataInEditor = (sourceName?: string | null) => {
    const normalizedSourceName = sourceName?.trim()
    openImportedScriptInEditor({
      targetSection: 'parameter-data',
      ...(normalizedSourceName ? { targetDataSource: normalizedSourceName } : {}),
    })
  }

  const buildMigrationPolicyRelaxationButtons = (keyPrefix: string): EditorActionButton[] => {
    if (!migrationReadiness?.blocked || !reviewMode) return []

    return getRelaxableMigrationBlockStatuses(migrationReadiness, options).map((status) => ({
      key: `${keyPrefix}:${status}`,
      label: `Allow ${formatRuntimeSettingStatusLabel(status)} Runtime Settings And Refresh Review`,
      onClick: () => {
        const nextOptions = getRelaxedMigrationBlockOptions(options, status)
        if (!nextOptions) return

        setOptions(nextOptions)
        const nextMode = reviewMode === 'validation' ? 'validate' : 'convert'
        void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions), selectedItem ? [selectedItem.id] : undefined)
      },
    }))
  }

  const restoreRecommendedMigrationPolicy = () => {
    const nextOptions = {
      ...options,
      block_partial: DEFAULT_IMPORT_OPTIONS.block_partial,
      block_compatibility_only: DEFAULT_IMPORT_OPTIONS.block_compatibility_only,
      block_non_applicable: DEFAULT_IMPORT_OPTIONS.block_non_applicable,
    }

    setOptions(nextOptions)
    if (reviewMode) {
      const nextMode = reviewMode === 'validation' ? 'validate' : 'convert'
      void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions), selectedItem ? [selectedItem.id] : undefined)
    }
  }

  const renderEditorActionButtons = (buttons: EditorActionButton[], style: CSSProperties = actionWrapStyle) => {
    const seenKeys = new Set<string>()
    const availableButtons = buttons.filter((button) => {
      if (seenKeys.has(button.key)) return false
      seenKeys.add(button.key)
      return true
    })
    if (availableButtons.length === 0) return null

    return (
      <div className="replay-panel-actions" style={style}>
        {availableButtons.map((button) => (
          <button key={button.key} className="btn btn-secondary btn-sm" onClick={button.onClick}>
            {button.label}
          </button>
        ))}
      </div>
    )
  }

  const getReviewSectionActions = (sectionKey: string): EditorActionButton[] => {
    const buttons: EditorActionButton[] = []
    switch (sectionKey) {
      case 'unsupported_elements':
        if (onEdit && firstUnsupportedTargetAction) {
          buttons.push({
            key: 'review-unsupported-action',
            label: 'Open First Unsupported Action In Editor',
            onClick: () => openImportedScriptInEditor({ targetAction: firstUnsupportedTargetAction }),
          })
        }
        if (onEdit) {
          buttons.push({
            key: 'review-unsupported-script',
            label: 'Open Imported Script In Editor',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      case 'preserved_metadata':
        if (onEdit && (runtimeFiles.length > 0 || reviewSummary?.mapped_runtime_file)) {
          buttons.push({
            key: 'review-preserved-runtime',
            label: 'Open Runtime Settings In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
        }
        if (onEdit && parameterFiles.length > 0) {
          buttons.push({
            key: 'review-preserved-parameter-files',
            label: 'Open Parameter Files In Editor',
            onClick: () => openImportedParameterDataInEditor(firstMappedParameterDataSourceName || firstGeneratedDataSourceName),
          })
        }
        if (onEdit && firstGeneratedDataSourceName) {
          buttons.push({
            key: 'review-preserved-source',
            label: 'Open First Data Source In Editor',
            onClick: () => openImportedDataSourceInEditor(firstGeneratedDataSourceName),
          })
        }
        break
      case 'runtime_settings_parity':
        if (onEdit) {
          buttons.push({
            key: 'review-runtime-parity',
            label: 'Open Runtime Review In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
          if (firstRuntimeParityArea) {
            buttons.push({
              key: 'review-runtime-parity-area',
              label: 'Open First Runtime Setting In Editor',
              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeParityArea),
            })
          }
        }
        break
      case 'runtime_settings_migration_readiness':
        if (onEdit) {
          buttons.push({
            key: 'review-runtime-readiness',
            label: 'Open Runtime Review In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
          if (firstRuntimeBlockingArea) {
            buttons.push({
              key: 'review-runtime-blocking-area',
              label: 'Open First Blocking Setting In Editor',
              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
            })
          }
        }
        buttons.push(...buildMigrationPolicyRelaxationButtons('review-runtime-relax'))
        break
      case 'browser_import_assumptions':
        if (onEdit) {
          buttons.push({
            key: 'review-browser-import-script',
            label: 'Open Imported Script In Editor',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      case 'salesforce_review':
        if (onEdit && salesforceNativeScript) {
          buttons.push({
            key: 'review-salesforce-native-preview',
            label: 'Open Native Preview',
            onClick: openSalesforceNativePreviewInEditor,
          })
        }
        if (onEdit) {
          buttons.push({
            key: 'review-salesforce-exact-replay',
            label: 'Open Exact Replay',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      default:
        break
    }

    return buttons
  }

  const setBrowserReplayStartedNotice = (wasStarted: boolean) => {
    if (!selectedItem) return
    updateImportItem(selectedItem.id, {
      notice: wasStarted
        ? 'Draft browser replay restarted. Review the refreshed draft browser replay session before saving it to the library.'
        : 'Draft browser replay started. Review the draft browser replay session before saving it to the library.',
    })
  }

  useEffect(() => {
    let cancelled = false

    api.getEnterpriseArchiveSupportMatrix()
      .then((response: EnterpriseArchiveSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(Array.isArray(response.support_matrix) ? response.support_matrix : [])
          setSupportMatrixNotes(Array.isArray(response.notes) ? response.notes : [])
          setSupportMatrixExamples(Array.isArray(response.tested_examples) ? response.tested_examples : [])
          setProtocolSupportMatrix(Array.isArray(response.protocol_support_matrix) ? response.protocol_support_matrix : [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix([])
          setSupportMatrixNotes([])
          setSupportMatrixExamples([])
          setProtocolSupportMatrix([])
        }
      })

    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    const sessionId = replayValidationSession?.id
    if (!sessionId || !browserReplayInProgress || replayValidationLoading) {
      return
    }

    let cancelled = false

    const refreshSession = () => {
      void api.getBrowserReplaySession(sessionId)
        .then((response) => {
          if (cancelled || !response?.session) {
            return
          }
          updateImportItem(selectedItem.id, {
            replayValidationSession: response.session,
            replayValidationWarnings: Array.isArray(response.warnings) ? response.warnings : replayValidationWarnings,
            replayValidationError: response.session.status === 'failed' && response.session.error ? response.session.error : replayValidationError,
          })
        })
        .catch((error: unknown) => {
          if (!cancelled) {
            updateImportItem(selectedItem.id, {
              replayValidationError: getErrorMessage(error, 'Draft browser replay status refresh failed'),
            })
          }
        })
    }

    const intervalId = window.setInterval(refreshSession, 2000)
    return () => {
      cancelled = true
      window.clearInterval(intervalId)
    }
  }, [api, browserReplayInProgress, replayValidationError, replayValidationLoading, replayValidationSession?.id, replayValidationWarnings, selectedItem?.id])

  const addFilesToQueue = (fileList?: FileList | File[] | null) => {
    const incomingFiles = Array.from(fileList || [])
    if (incomingFiles.length === 0) return

    const zipFiles = incomingFiles.filter(isEnterpriseArchiveZipFile)
    if (zipFiles.length === 0) {
      setError('Please upload ZIP files containing enterprise script archive folders.')
      return
    }

    const nextItems = zipFiles.map((nextFile) => {
      itemCounterRef.current += 1
      return buildEnterpriseArchiveImportQueueItem(nextFile, `lr-import-${Date.now()}-${itemCounterRef.current}`)
    })
    setImportItems(items => [...items, ...nextItems])
    setSelectedItemId(nextItems[0].id)
    setError(zipFiles.length === incomingFiles.length ? '' : `${incomingFiles.length - zipFiles.length} non-ZIP file${incomingFiles.length - zipFiles.length === 1 ? '' : 's'} skipped.`)
    setNotice('')
  }

  const removeQueuedFile = (itemId: string) => {
    const removingSelectedItem = selectedItemId === itemId
    const nextSelectedItem = removingSelectedItem
      ? importItems.find(item => item.id !== itemId)?.id || ''
      : selectedItemId
    setImportItems(items => {
      const nextItems = items.filter(item => item.id !== itemId)
      return nextItems
    })
    setSelectedItemId(nextSelectedItem)
    setNotice('')
  }

  const clearQueuedFiles = () => {
    setImportItems([])
    setSelectedItemId('')
    setError('')
    setNotice('')
    if (fileRef.current) {
      fileRef.current.value = ''
    }
    if (harFileRef.current) {
      harFileRef.current.value = ''
    }
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    addFilesToQueue(e.dataTransfer.files)
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    addFilesToQueue(e.target.files)
    e.target.value = ''
  }

  const handleHARFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const nextFile = Array.from(e.target.files || [])[0]
    e.target.value = ''
    if (!selectedItem || !nextFile) return
    if (!isHARFile(nextFile)) {
      updateImportItem(selectedItem.id, {
        error: 'Please attach a HAR or JSON file captured from the same Salesforce recording.',
      })
      return
    }
    updateImportItem(selectedItem.id, {
      harFile: nextFile,
      error: '',
      notice: '',
    })
  }

  const clearSelectedHARFile = () => {
    if (!selectedItem) return
    updateImportItem(selectedItem.id, { harFile: null, notice: '', error: '' })
    if (harFileRef.current) {
      harFileRef.current.value = ''
    }
  }

  const runReview = async (
    mode: 'convert' | 'validate',
    reviewOptions: ImportOptions = options,
    completionNotice?: string,
    itemIds?: string[],
  ) => {
    const targetItems = importItems.filter(item => !itemIds || itemIds.includes(item.id))
    if (targetItems.length === 0) return

    const runningStatus = mode === 'validate' ? 'validating' : 'converting'
    const completeStatus = mode === 'validate' ? 'validated' : 'converted'
    const targetIds = new Set(targetItems.map(item => item.id))

    setWorkingMode(mode)
    setError('')
    setNotice('')
    setSelectedItemId(prev => targetIds.has(prev) ? prev : targetItems[0].id)
    setImportItems(items => items.map(item => targetIds.has(item.id)
      ? {
          ...item,
          status: runningStatus,
          result: null,
          reviewMode: null,
          error: '',
          notice: '',
          replayValidationLoading: false,
          replayValidationError: '',
          replayValidationWarnings: [],
          replayValidationSession: null,
        }
      : item))

    let successCount = 0
    let failureCount = 0
    const request = mode === 'validate' ? api.validateEnterpriseArchiveImport : api.importEnterpriseArchive
    const useNameOverride = targetItems.length === 1

    for (const item of targetItems) {
      try {
        const res = await request(item.file, {
          name: useNameOverride ? reviewOptions.name || undefined : undefined,
          project_id: projectId,
          base_url: reviewOptions.base_url || undefined,
          include_assertions: reviewOptions.include_assertions,
          preserve_transactions: reviewOptions.preserve_transactions,
          migration_block_statuses: buildMigrationBlockStatuses(reviewOptions),
          ...(item.harFile ? { har_file: item.harFile } : {}),
          ...(reviewOptions.salesforce_native ? { salesforce_native: true } : {}),
        })
        successCount += 1
        updateImportItem(item.id, {
          status: completeStatus,
          result: res,
          reviewMode: mode === 'validate' ? 'validation' : 'conversion',
          error: '',
          notice: completionNotice || (mode === 'validate'
            ? 'Validation complete. Review the generated script, warnings, and preserved settings before saving.'
            : 'Conversion complete. Review the generated script, warnings, and preserved settings before saving.'),
        })
      } catch (error: unknown) {
        failureCount += 1
        updateImportItem(item.id, {
          status: 'failed',
          error: getErrorMessage(error, mode === 'validate' ? 'Validation failed' : 'Import failed'),
          notice: '',
        })
      }
    }

    setWorkingMode(null)
    if (targetItems.length > 1) {
      const action = mode === 'validate' ? 'Validated' : 'Converted'
      setNotice(`${action} ${successCount} of ${targetItems.length} enterprise script archive${targetItems.length === 1 ? '' : 's'}${failureCount > 0 ? `; ${failureCount} failed` : ''}.`)
    }
  }

  const startBrowserReplayValidation = async () => {
    if (!importedScript || !selectedItem) return
    const script = importedScript

    updateImportItem(selectedItem.id, {
      replayValidationLoading: true,
      replayValidationError: '',
      replayValidationWarnings: [],
    })

    try {
      const res = await api.startDraftBrowserReplaySession(buildReplayStartPayload(script, {
        projectId,
        ignoreThinkTime: true,
      }))
      updateImportItem(selectedItem.id, {
        replayValidationSession: res.session,
        replayValidationWarnings: Array.isArray(res.warnings) ? res.warnings : [],
      })
      return true
    } catch (error: unknown) {
      updateImportItem(selectedItem.id, {
        replayValidationError: getErrorMessage(error, 'Draft browser replay failed to start'),
      })
      return false
    } finally {
      updateImportItem(selectedItem.id, { replayValidationLoading: false })
    }
  }

  const handleSaveConverted = async () => {
    if (!importedScript) return
    if (saveBlockedByProtocolGate) {
      setNotice('This import is gated by protocol classification. Save the review artifact instead of promoting it as an executable script.')
      return
    }
    if (saveBlockedByMigrationPolicy) {
      setError('Saving is disabled while migration readiness is blocked by the active policy. Relax the policy or resolve the blocking runtime settings first.')
      return
    }
    if (browserReplayInProgress) {
      setNotice('Draft browser replay is still running. Wait for it to finish before saving it to the library.')
      return
    }
    if (browserReplayNeedsRerun) {
      const wasStarted = browserReplayWasStarted
      const started = await startBrowserReplayValidation()
      if (started) {
        setBrowserReplayStartedNotice(wasStarted)
      }
      return
    }
    try {
      const scriptToSave = browserImport
        ? attachTrueClientPromotionReview(importedScript, {
          compatReport: trueClient2CompatReport,
          replaySession: replayValidationSession,
          replayWarnings: replayValidationWarnings,
          importWarnings: result?.warnings || [],
        })
        : importedScript
      const savedScript = await api.createScript(scriptToSave, projectId)
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, scriptToSave)] })
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save script'))
    }
  }

  const handleSaveReviewArtifact = async () => {
    if (!reviewArtifact) return
    setReviewArtifactSaving(true)
    setError('')
    try {
      const session = await api.createAuthoringSession({
        project_id: projectId,
        source: 'source_review_artifact',
        name: reviewArtifact.title || detectedProtocol?.display_name || 'enterprise script archive protocol review',
        status: 'reviewing',
        draft: importedScript || buildReviewArtifactDraft(reviewArtifact, detectedProtocol, projectId, options.name, sourceReview),
        review_state: {
          review_artifact: reviewArtifact,
          detected_protocol: detectedProtocol,
          import_summary: result?.summary || {},
          warnings: result?.warnings || [],
          required_evidence: reviewArtifact.required_evidence || detectedProtocol?.required_evidence || [],
        },
      })
      setNotice(`Review artifact saved as authoring session ${session.name || session.id}.`)
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save review artifact'))
    } finally {
      setReviewArtifactSaving(false)
    }
  }

  const handleValidateBrowserReplay = async () => {
    const wasStarted = browserReplayWasStarted
    const started = await startBrowserReplayValidation()
    if (started) {
      setBrowserReplayStartedNotice(wasStarted)
    }
  }

  return (
    <div className="lr-import">
      <div className="lr-import-header">
        <div>
          <h2>Import Enterprise Script Archive</h2>
          <p className="lr-subtitle">Upload a ZIP containing C-based virtual-user scripts, runtime settings, parameter files, and protocol metadata. APEX Load routes executable HTTP, SOAP, browser, database, and network archives while preserving review artifacts for sources that still require evidence or external runners.</p>
        </div>
        {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
      </div>

      {/* Drop zone */}
      <div className={`lr-dropzone ${dragOver ? 'drag-over' : ''} ${queuedFileCount > 0 ? 'has-file' : ''}`}
        onDragOver={e => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={() => fileRef.current?.click()}>
        <input ref={fileRef} type="file" accept=".zip" multiple onChange={handleFileSelect} hidden />
        {queuedFileCount > 0 ? (
          <div className="lr-file-queue">
            <div className="lr-file-info">
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
              <div>
                <span className="lr-filename">{queuedFileCount} ZIP file{queuedFileCount === 1 ? '' : 's'} queued</span>
                <span className="lr-filesize">Click to add more, or remove individual scripts below.</span>
              </div>
              <button className="btn btn-sm btn-ghost" onClick={e => { e.stopPropagation(); clearQueuedFiles() }}>Clear All</button>
            </div>
            <div className="lr-file-list" aria-label="Queued enterprise script archives">
              {importItems.map(item => (
                <div
                  key={item.id}
                  role="button"
                  tabIndex={0}
                  className={`lr-file-row ${selectedItem?.id === item.id ? 'active' : ''} ${item.status === 'failed' ? 'failed' : ''}`}
                  aria-pressed={selectedItem?.id === item.id}
                  onClick={event => {
                    event.stopPropagation()
                    setSelectedItemId(item.id)
                    setError('')
                    setNotice('')
                  }}
                  onKeyDown={event => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      event.stopPropagation()
                      setSelectedItemId(item.id)
                      setError('')
                      setNotice('')
                    }
                  }}
                >
                  <span className="lr-file-row-main">
                    <strong>{item.file.name}</strong>
                    <small>
                      {formatEnterpriseArchiveFileSize(item.file)} · {enterpriseArchiveImportStatusLabel(item.status)}
                      {item.harFile ? ` · HAR: ${item.harFile.name}` : ''}
                    </small>
                  </span>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost lr-file-remove"
                    aria-label={`Remove ${item.file.name}`}
                    onClick={event => {
                      event.stopPropagation()
                      removeQueuedFile(item.id)
                    }}
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop one or more enterprise script archive <code>.zip</code> files here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      {/* Options */}
      <div className="lr-conversion-row">
        <div className="lr-options">
          <div className="lr-options-header">
            <h3 className="lr-section-title">Conversion Options</h3>
            {hasRuntimeCoverage && (
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setRuntimeCoverageOpen(true)}>
                Runtime Settings Coverage
              </button>
            )}
          </div>
          <div className="lr-options-grid">
            <label><span>Script Name (optional)</span>
              <input value={options.name} onChange={e => setOptions(o => ({ ...o, name: e.target.value }))} placeholder="Auto-detected from .usr file" />
            </label>
            <label><span>Base URL Override (optional)</span>
              <input value={options.base_url} onChange={e => setOptions(o => ({ ...o, base_url: e.target.value }))} placeholder="Auto-detected from first URL" />
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.include_assertions} onChange={e => setOptions(o => ({ ...o, include_assertions: e.target.checked }))} />
              <span>Add default status_code assertions</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.preserve_transactions} onChange={e => setOptions(o => ({ ...o, preserve_transactions: e.target.checked }))} />
              <span>Preserve transaction names in generated actions</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.salesforce_native} onChange={e => setOptions(o => ({ ...o, salesforce_native: e.target.checked }))} />
              <span>Build Salesforce native preview</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_partial} onChange={e => setOptions(o => ({ ...o, block_partial: e.target.checked }))} />
              <span>Treat partial runtime settings as blockers</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_compatibility_only} onChange={e => setOptions(o => ({ ...o, block_compatibility_only: e.target.checked }))} />
              <span>Treat preserved-only runtime settings as blockers</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_non_applicable} onChange={e => setOptions(o => ({ ...o, block_non_applicable: e.target.checked }))} />
              <span>Treat non-applicable runtime settings as blockers</span>
            </label>
          </div>
          {queuedFileCount > 1 && (
            <p className="lr-subtitle">Script Name override is ignored for batch conversion so each imported script can keep its detected source name.</p>
          )}
          <p className="lr-subtitle">Selected migration block policy: {formatMigrationBlockPolicySummary(options)}</p>
          {reviewedMigrationPolicy && (
            <p className="lr-subtitle">Reviewed migration block policy: {formatMigrationBlockPolicySummary(reviewedMigrationPolicy)}</p>
          )}
          {hasPendingMigrationPolicyChanges && (
            <p className="lr-subtitle">Refresh review to apply the selected migration block policy.</p>
          )}
          {hasCustomMigrationBlockPolicy(options) && (
            <div className="replay-panel-actions" style={actionWrapStyle}>
              <button className="btn btn-secondary btn-sm" onClick={restoreRecommendedMigrationPolicy}>
                Restore Recommended Block Policy
              </button>
            </div>
          )}
        </div>

        <div className="lr-import-actions-card">
          <div>
            <h3 className="lr-section-title">Validation / Conversion</h3>
            <p className="lr-subtitle">{queuedFileCount} ZIP file{queuedFileCount === 1 ? '' : 's'} queued</p>
          </div>
          <input ref={harFileRef} type="file" accept=".har,.json,application/json" onChange={handleHARFileSelect} hidden />
          <div className="lr-har-panel">
            <div>
              <h4>Salesforce HAR</h4>
              <p>{selectedHARFile ? selectedHARFile.name : 'No HAR attached to the selected script'}</p>
            </div>
            <div className="lr-har-actions">
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => harFileRef.current?.click()} disabled={!selectedItem}>
                {selectedHARFile ? 'Replace HAR' : 'Attach HAR'}
              </button>
              {selectedHARFile && (
                <button type="button" className="btn btn-ghost btn-sm" onClick={clearSelectedHARFile}>
                  Remove
                </button>
              )}
            </div>
          </div>
          <div className="lr-import-action-buttons">
            <button className="btn btn-secondary btn-lg" onClick={() => runReview('validate')} disabled={queuedFileCount === 0 || workingMode !== null}>
              {workingMode === 'validate' ? <><span className="spinner" /> Validating...</> : validateButtonLabel}
            </button>
            <button className="btn btn-primary btn-lg" onClick={() => runReview('convert')} disabled={queuedFileCount === 0 || workingMode !== null}>
              {workingMode === 'convert' ? <><span className="spinner" /> Converting...</> : convertButtonLabel}
            </button>
          </div>
        </div>
      </div>

      {runtimeCoverageOpen && hasRuntimeCoverage && (
        <div className="lr-modal-backdrop" role="presentation" onClick={() => setRuntimeCoverageOpen(false)}>
          <div
            className="lr-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="lr-runtime-coverage-title"
            onClick={event => event.stopPropagation()}
          >
            <div className="lr-modal-header">
              <div>
                <h3 id="lr-runtime-coverage-title">Runtime Settings Coverage</h3>
                <p>Code-owned runtime compatibility support matrix</p>
              </div>
              <button type="button" className="btn btn-ghost" onClick={() => setRuntimeCoverageOpen(false)}>Close</button>
            </div>
            <div className="lr-modal-body">
              <div className="lr-warnings">
                <h4>Coverage Details</h4>
                {supportMatrixSections.map(section => (
                  <details key={section.key}>
                    <summary>{section.title} ({section.items.length})</summary>
                    <ul>
                      {section.items.map(entry => (
                        <li key={entry.id}>{entry.area}: {entry.concept}</li>
                      ))}
                    </ul>
                  </details>
                ))}
              </div>

              {supportMatrixExamples.length > 0 && (
                <div className="lr-warnings">
                  <h4>Tested Examples</h4>
                  {supportMatrixExamples.map(example => (
                    <div key={example.test} className="lr-review-section">
                      <div className="lr-review-section-header">
                        <strong>{example.title}</strong>
                        <span>tested</span>
                      </div>
                      <p>{example.coverage}</p>
                      <ul><li>{example.test}</li></ul>
                    </div>
                  ))}
                </div>
              )}

              {supportMatrixNotes.length > 0 && (
                <div className="lr-warnings">
                  <h4>Known Caveats</h4>
                  <ul>{supportMatrixNotes.map((note, index) => <li key={index}>{note}</li>)}</ul>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      <div className="settings-notice">
        <p>Protocol-aware detection uses .usr metadata plus source signatures. Conflicting evidence is gated for review instead of being saved as an executable script.</p>
        {protocolCoverageSections.length > 0 && (
          <ul className="lr-protocol-route-list" aria-label="source protocol import routes">
            {protocolCoverageSections.map(section => (
              <li key={section.key}>
                <strong>{section.title}</strong>: {section.items.length}
                {section.preview ? <span>{section.preview}</span> : null}
              </li>
            ))}
          </ul>
        )}
      </div>

      {displayError && <div className="lr-error">{displayError}</div>}
      {displayNotice && <div className="settings-notice">{displayNotice}</div>}

      {/* Results */}
      {result && !importedScript && reviewArtifact && (
        <div className="lr-result">
          <div className="lr-result-header">
            <div>
              <h3>{reviewMode === 'validation' ? 'Validation Review' : 'Import Review Artifact'}</h3>
              <p className="lr-subtitle">This archive was classified without creating an executable APEX Load script.</p>
            </div>
            <button className="btn btn-primary" onClick={handleSaveReviewArtifact} disabled={reviewArtifactSaving}>
              {reviewArtifactSaving ? <><span className="spinner" /> Saving Review Artifact...</> : 'Save Review Artifact'}
            </button>
          </div>

          <div className="lr-warnings">
            <div className="lr-review-section">
              <div className="lr-review-section-header">
                <strong>{reviewArtifact.title}</strong>
                <span>{reviewArtifact.status}</span>
              </div>
              <p>{reviewArtifact.summary}</p>
              {reviewArtifact.required_evidence?.length ? (
                <ul>{reviewArtifact.required_evidence.map((evidence, index) => <li key={`artifact-evidence-${index}`}>{evidence}</li>)}</ul>
              ) : null}
            </div>
            {reviewSections.map(section => (
              <div key={section.key} className="lr-review-section">
                <div className="lr-review-section-header">
                  <strong>{section.title}</strong>
                  <span>{section.severity}</span>
                </div>
                {section.summary && <p>{section.summary}</p>}
                {section.items.length > 0 && <ul>{section.items.slice(0, 8).map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>}
              </div>
            ))}
          </div>
          {sourceReview && (
            <SourceMappingReview sourceReview={sourceReview} script={null} />
          )}
        </div>
      )}

      {result && importedScript && (
        <div className="lr-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Complete'}
            subtitle={reviewSubtitle}
            script={importedScript}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            saving={saveBlockedByProtocolGate ? reviewArtifactSaving : false}
            saveDisabled={primarySaveDisabled}
            saveLabel={saveActionLabel}
            onSave={saveBlockedByProtocolGate && reviewArtifact ? handleSaveReviewArtifact : handleSaveConverted}
            onEdit={onEdit ? () => openImportedScriptInEditor() : undefined}
            previewAside={sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={importedScript} /> : undefined}
          >

            {saveBlockedByProtocolGate && detectedProtocol && (
              <div className="lr-warnings lr-protocol-gate">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>{externalRunnerGate ? 'External Runner Required' : 'Protocol Promotion Gate'}</strong>
                    <span>{detectedProtocol.import_mode}</span>
                  </div>
                  <p>
                    {externalRunnerGate
                      ? `${protocolGateName} draft conversion is visible for review, but execution is blocked until the required runner is approved and configured.`
                      : `${protocolGateName} draft conversion is visible for review, but executable promotion is blocked by the protocol classification.`}
                  </p>
                  {protocolGateDetails.length > 0 && (
                    <ul>
                      {protocolGateDetails.map((detail, index) => <li key={`protocol-gate-${index}`}>{detail}</li>)}
                    </ul>
                  )}
                </div>
              </div>
            )}

            {trueClient2CompatReport && (
              <div className="lr-warnings lr-trueclient2-compat">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>TruClient 2 Compatibility</strong>
                    <span>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.promotion_status || 'review')}</span>
                  </div>
                  <p>
                    {trueClient2CompatReport.source_script_type || 'TruClient source'} · {trueClient2CompatReport.import_mode || 'review'} · {trueClient2CompatReport.promotion_allowed ? 'Promotion allowed' : 'Promotion blocked'}
                  </p>
                  <div className="lr-compat-grid">
                    <div>
                      <span>Steps</span>
                      <strong>{trueClient2CompatReport.converted_actions ?? 0}/{trueClient2CompatReport.imported_steps ?? 0}</strong>
                      <small>{trueClient2CompatReport.skipped_review_steps ?? 0} skipped/review</small>
                    </div>
                    <div>
                      <span>Selectors</span>
                      <strong>{trueClient2CompatReport.selector_confidence_pct ?? 0}%</strong>
                      <small>{trueClient2CompatReport.selector_candidate_count ?? 0} candidates</small>
                    </div>
                    <div>
                      <span>Runtime</span>
                      <strong>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.runtime_parity_status || 'unknown')}</strong>
                      <small>{trueClient2CompatReport.fixture_contract_status || 'fixture status unknown'}</small>
                    </div>
                    <div>
                      <span>Replay</span>
                      <strong>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.replay_status || 'required')}</strong>
                      <small>{trueClient2CompatReport.frame_window_blockers ?? 0} frame/window blockers</small>
                    </div>
                    <div>
                      <span>JavaScript</span>
                      <strong>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.javascript_review_gate || 'none')}</strong>
                      <small>review gate</small>
                    </div>
                    <div>
                      <span>HTTP Conversion</span>
                      <strong>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.http_conversion_readiness || 'not ready')}</strong>
                      <small>scale path</small>
                    </div>
                  </div>
                  {trueClient2CompatReport.supported_step_families?.length ? (
                    <p>Supported steps: {trueClient2CompatReport.supported_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.review_only_step_families?.length ? (
                    <p>Review-only steps: {trueClient2CompatReport.review_only_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.unsupported_step_families?.length ? (
                    <p>Unsupported steps: {trueClient2CompatReport.unsupported_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.findings?.length ? (
                    <ul>{trueClient2CompatReport.findings.slice(0, 6).map((finding, index) => <li key={`tc2-finding-${index}`}>{finding}</li>)}</ul>
                  ) : null}
                  {trueClient2CompatReport.next_actions?.length ? (
                    <>
                      <p>Next actions</p>
                      <ul>{trueClient2CompatReport.next_actions.slice(0, 6).map((action, index) => <li key={`tc2-next-${index}`}>{action}</li>)}</ul>
                    </>
                  ) : null}
                  {onEdit && renderEditorActionButtons([
                    {
                      key: 'trueclient2-open-draft',
                      label: 'Open Imported Draft In Editor',
                      onClick: () => openImportedScriptInEditor(),
                    },
                    {
                      key: 'trueclient2-runtime-review',
                      label: 'Open Runtime Review In Editor',
                      onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                    },
                  ], sectionActionStyle)}
                </div>
              </div>
            )}

            {browserImport && (
              <div className="lr-warnings">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>Draft Browser Replay Validation</strong>
                    <span>{browserReplayStatusLabel}</span>
                  </div>
                  <p>{replayPanelDescription}</p>
                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start' }}>
                    <button className="btn btn-secondary" onClick={handleValidateBrowserReplay} disabled={replayValidationLoading || browserReplayInProgress}>
                      {replayValidationLoading ? <><span className="spinner" /> {replayActionLabel}</> : replayActionLabel}
                    </button>
                  </div>
                  {replayValidationSession && !replayValidationLoading && (
                    <>
                      <ul>
                        <li>Replay Session: {replayValidationSession.id}</li>
                        <li>Replay Status: {browserReplayStatusLabel}</li>
                        <li>Replay Group: {replayValidationSession.group}</li>
                      </ul>
                      <div className="lr-replay-evidence">
                        <div className="lr-review-section-header">
                          <strong>Replay Evidence</strong>
                          <span>{browserReplayEvidence.events > 0 ? 'Captured' : 'Pending'}</span>
                        </div>
                        <div className="lr-compat-grid lr-replay-evidence-grid">
                          <div>
                            <span>Actions</span>
                            <strong>{formatEvidenceCount(browserReplayEvidence.events, 'action')}</strong>
                            <small>{browserReplayEvidence.passed} passed / {browserReplayEvidence.failed} failed</small>
                          </div>
                          <div>
                            <span>Captures</span>
                            <strong>{formatEvidenceCount(browserReplayEvidence.screenshots, 'screenshot')}</strong>
                            <small>{formatEvidenceCount(browserReplayEvidence.domHtmlCaptures, 'DOM/HTML capture')}</small>
                          </div>
                          <div>
                            <span>Telemetry</span>
                            <strong>{formatEvidenceCount(browserReplayEvidence.consoleEntries, 'console entry', 'console entries')}</strong>
                            <small>{formatEvidenceCount(browserReplayEvidence.networkEntries, 'network entry', 'network entries')}</small>
                          </div>
                          <div>
                            <span>Source Links</span>
                            <strong>{formatEvidenceCount(browserReplayEvidence.sourceSteps, 'source step')}</strong>
                            <small>{formatEvidenceCount(browserReplayEvidence.sourceTransactions, 'transaction')}</small>
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                  {replayValidationWarnings.length > 0 && (
                    <ul>{replayValidationWarnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
                  )}
                  {replayValidationError && <div className="lr-error">{replayValidationError}</div>}
                </div>
              </div>
            )}

            {salesforceReview && (
              <div className="lr-salesforce-lanes">
                <div className="lr-salesforce-lane">
                  <div className="lr-salesforce-lane-header">
                    <div>
                      <strong>Exact Replay</strong>
                      <span>Primary Output</span>
                    </div>
                    <small>{scriptActionCount(importedScript)} actions</small>
                  </div>
                  <dl>
                    <div>
                      <dt>Domains</dt>
                      <dd>{salesforceDomainSummary(salesforceReview)}</dd>
                    </div>
                    <div>
                      <dt>HAR Coverage</dt>
                      <dd>{formatSalesforceCoverage(salesforceReview)}</dd>
                    </div>
                  </dl>
                  {onEdit && renderEditorActionButtons([
                    {
                      key: 'salesforce-exact-open',
                      label: 'Open Exact Replay',
                      onClick: () => openImportedScriptInEditor(),
                    },
                  ], sectionActionStyle)}
                </div>

                <div className={`lr-salesforce-lane ${salesforceNativeScript ? '' : 'blocked'}`}>
                  <div className="lr-salesforce-lane-header">
                    <div>
                      <strong>Salesforce Native Preview</strong>
                      <span>{salesforceNativeScript ? 'Draft Ready' : 'Needs Evidence'}</span>
                    </div>
                    <small>{salesforceTransactionCount(salesforceReview)} transactions</small>
                  </div>
                  <dl>
                    <div>
                      <dt>Correlation</dt>
                      <dd>{salesforceReview.correlation_confidence || 'unknown'}</dd>
                    </div>
                    <div>
                      <dt>Correlations</dt>
                      <dd>{salesforceReview.native_preview?.correlations?.length || 0}</dd>
                    </div>
                    <div>
                      <dt>API surface</dt>
                      <dd>{salesforceApiSurfaceSummary(salesforceReview)}</dd>
                    </div>
                    <div>
                      <dt>Aura params</dt>
                      <dd>{salesforceReview.parameterizations?.length || 0}</dd>
                    </div>
                  </dl>
                  {salesforceReview.blockers?.length ? (
                    <ul className="lr-salesforce-blockers">
                      {salesforceReview.blockers.slice(0, 4).map((blocker, index) => <li key={index}>{blocker}</li>)}
                    </ul>
                  ) : null}
                  {salesforceReview.parameterizations?.length ? (
                    <ul className="lr-salesforce-blockers">
                      {salesforceReview.parameterizations.slice(0, 4).map((param, index) => (
                        <li key={index}>{param.param_key} ({param.kind}) → {param.template}</li>
                      ))}
                    </ul>
                  ) : null}
                  {onEdit && salesforceNativeScript && renderEditorActionButtons([
                    {
                      key: 'salesforce-native-open',
                      label: 'Open Native Preview',
                      onClick: openSalesforceNativePreviewInEditor,
                    },
                  ], sectionActionStyle)}
                </div>
              </div>
            )}

            {/* Stats */}
            <div className="lr-stats-grid">
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.logical_actions ?? result.stats.converted_actions}</span><span className="lr-stat-label">Logical Actions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.raw_request_actions ?? result.stats.converted_actions}</span><span className="lr-stat-label">Raw Requests</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.collapsed_requests ?? 0}</span><span className="lr-stat-label">Collapsed</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.extractors}</span><span className="lr-stat-label">Extractors</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.transactions}</span><span className="lr-stat-label">Transactions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.transaction_markers ?? 0}</span><span className="lr-stat-label">LR Markers</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.total_functions}</span><span className="lr-stat-label">Total LR Functions</span></div>
            </div>

            {unsupportedFunctions.length > 0 && (
              <div className="lr-warnings">
                <h4>Unsupported Source Functions ({unsupportedFunctions.length})</h4>
                <ul>
                  {unsupportedFunctions.map((fnName, index) => {
                    const repairSuggestion = findUnsupportedRepairSuggestion(repairSuggestions, fnName)
                    return (
                      <li key={`${fnName}-${index}`}>
                        {fnName}
                        {repairSuggestion?.target_action && onEdit && (
                          <>
                            {' '}
                            <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetAction: repairSuggestion.target_action })}>
                              Open Fix In Editor
                            </button>
                          </>
                        )}
                      </li>
                    )
                  })}
                </ul>
                {onEdit && renderEditorActionButtons([
                  firstUnsupportedTargetAction ? {
                    key: 'unsupported-summary-action',
                    label: 'Open First Unsupported Action In Editor',
                    onClick: () => openImportedScriptInEditor({ targetAction: firstUnsupportedTargetAction }),
                  } : null,
                  {
                    key: 'unsupported-summary-script',
                    label: 'Open Imported Script In Editor',
                    onClick: () => openImportedScriptInEditor(),
                  },
                ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
              </div>
            )}

            {(reviewSections.length > 0 || repairSuggestions.length > 0) && (
              <div className="lr-review-row">
                {reviewSections.length > 0 && (
                  <div className="lr-warnings">
                    <h4>Import Review Sections ({reviewSections.length})</h4>
                    {reviewSections.map(section => (
                      <div key={section.key} className="lr-review-section">
                        <div className="lr-review-section-header">
                          <strong>{section.title}</strong>
                          <span>{section.severity}</span>
                        </div>
                        {section.summary && <p>{section.summary}</p>}
                        {renderEditorActionButtons(getReviewSectionActions(section.key), sectionActionStyle)}
                        {section.items.length > 0 && (
                          <ul>{section.items.slice(0, 6).map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>
                        )}
                      </div>
                    ))}
                  </div>
                )}

                {repairSuggestions.length > 0 && (
                  <div className="lr-warnings">
                    <h4>Repair Suggestions ({repairSuggestions.length})</h4>
                    {repairSuggestions.map(suggestion => {
                      const suggestionTitle = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.title)
                        : suggestion.title
                      const suggestionDescription = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.description)
                        : suggestion.description
                      const suggestionAction = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.action)
                        : suggestion.action

                      return (
                      <div key={suggestion.id} className="lr-review-section">
                        <div className="lr-review-section-header">
                          <strong>{suggestionTitle}</strong>
                          <span>{suggestion.severity}</span>
                        </div>
                        {suggestion.target_action && <p>Action: {suggestion.target_action}</p>}
                        {typeof suggestion.selector_stability_score === 'number' && (
                          <p>Selector stability: {suggestion.selector_stability_score}/100{suggestion.selector_signals?.length ? ` - ${suggestion.selector_signals.join(', ')}` : ''}</p>
                        )}
                        {suggestionDescription && <p>{suggestionDescription}</p>}
                        {suggestionAction && <ul><li>{suggestionAction}</li></ul>}
                        {suggestion.target_action && onEdit && (
                          renderEditorActionButtons([
                            {
                              key: `${suggestion.id}:target-action`,
                              label: 'Open Target Action In Editor',
                              onClick: () => openImportedScriptInEditor({ targetAction: suggestion.target_action }),
                            },
                          ])
                        )}
                        {!suggestion.target_action && suggestion.category === 'runtime' && (
                          renderEditorActionButtons([
                            onEdit ? {
                              key: `${suggestion.id}:runtime-review`,
                              label: 'Open Runtime Review In Editor',
                              onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                            } : null,
                            onEdit && firstRuntimeBlockingArea && suggestion.id === 'runtime:migration-readiness' ? {
                              key: `${suggestion.id}:blocking-area`,
                              label: 'Open First Blocking Setting In Editor',
                              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
                            } : null,
                            ...(
                              suggestion.id === 'runtime:migration-readiness'
                                ? buildMigrationPolicyRelaxationButtons(`${suggestion.id}:relax`)
                                : []
                            ),
                          ].filter((button): button is EditorActionButton => Boolean(button)))
                        )}
                        {!suggestion.target_action && suggestion.category === 'replay' && onEdit && (
                          renderEditorActionButtons([
                            {
                              key: `${suggestion.id}:open-script`,
                              label: 'Open Imported Draft In Editor',
                              onClick: () => openImportedScriptInEditor(),
                            },
                          ])
                        )}
                        {!suggestion.target_action && suggestion.category === 'salesforce' && onEdit && (
                          renderEditorActionButtons([
                            salesforceNativeScript ? {
                              key: `${suggestion.id}:native-preview`,
                              label: 'Open Native Preview',
                              onClick: openSalesforceNativePreviewInEditor,
                            } : null,
                            {
                              key: `${suggestion.id}:exact-replay`,
                              label: 'Open Exact Replay',
                              onClick: () => openImportedScriptInEditor(),
                            },
                          ].filter((button): button is EditorActionButton => Boolean(button)))
                        )}
                      </div>
                    )})}
                  </div>
                )}
              </div>
            )}

            {importMetadata && (
              <div className="lr-preserved">
                <div className="lr-preserved-header">
                  <h4>Preserved Source Settings</h4>
                  <span>These values will be saved with the APEX Load script.</span>
                </div>

                <div className="lr-preserved-grid">
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Project File</span>
                    <strong>{reviewSummary?.project_file || importMetadata.source_script_name || importedScript.name}</strong>
                    <small>{importMetadata.source || 'enterprise_script_archive'}</small>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'summary-project-file-script',
                        label: 'Open Imported Script In Editor',
                        onClick: () => openImportedScriptInEditor(),
                      },
                    ])}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Mapped Runtime</span>
                    <strong>{reviewSummary?.mapped_runtime_file || 'None mapped'}</strong>
                    <small>{runtimeFiles.join(', ') || 'No runtime files found'}</small>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'summary-runtime-review',
                        label: 'Open Runtime Settings In Editor',
                        onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                      },
                      firstRuntimeParityArea ? {
                        key: 'summary-runtime-area',
                        label: 'Open First Runtime Setting In Editor',
                        onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeParityArea),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)))}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Parameter Files</span>
                    <strong>{parameterFiles.length}</strong>
                    <small>{parameterFiles.join(', ') || 'No parameter files found'}</small>
                    {onEdit && parameterFiles.length > 0 && renderEditorActionButtons([
                      {
                        key: 'summary-parameter-files',
                        label: 'Open Parameter Files In Editor',
                        onClick: () => openImportedParameterDataInEditor(firstMappedParameterDataSourceName || firstGeneratedDataSourceName),
                      },
                      firstMappedParameterDataSourceName ? {
                        key: 'summary-parameter-source',
                        label: 'Open First Data Source In Editor',
                        onClick: () => openImportedDataSourceInEditor(firstMappedParameterDataSourceName),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)))}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Correlation Suggestions</span>
                    <strong>{importSummary.correlationSuggestions.length}</strong>
                    <small>{importSummary.correlationSuggestions.length > 0 ? 'Imported from preserved metadata' : 'None detected'}</small>
                    {onEdit && importSummary.correlationSuggestions.length > 0 && renderEditorActionButtons([
                      firstCorrelationSourceTarget ? {
                        key: 'summary-correlation-source',
                        label: 'Open First Correlation Source In Editor',
                        onClick: () => openImportedScriptInEditor({ targetAction: firstCorrelationSourceTarget.actionName }),
                      } : null,
                      firstCorrelationUsageTarget ? {
                        key: 'summary-correlation-usage',
                        label: 'Open First Correlation Use In Editor',
                        onClick: () => openImportedScriptInEditor({ targetAction: firstCorrelationUsageTarget.actionName }),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)))}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Project Sections</span>
                    <strong>{projectSections.length}</strong>
                    <small>{projectSections.join(', ') || 'None captured'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Unsupported Functions</span>
                    <strong>{reviewSummary?.unsupported_function_count ?? unsupportedFunctions.length}</strong>
                    <small>{unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                    {onEdit && firstUnsupportedTargetAction && renderEditorActionButtons([
                      {
                        key: 'summary-unsupported-action',
                        label: 'Open First Unsupported Action In Editor',
                        onClick: () => openImportedScriptInEditor({ targetAction: firstUnsupportedTargetAction }),
                      },
                    ])}
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Source</span>
                    <strong>{importMetadata.source || 'enterprise_script_archive'}</strong>
                    <small>{importedScript.name}</small>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'summary-source-script',
                        label: 'Open Imported Script In Editor',
                        onClick: () => openImportedScriptInEditor(),
                      },
                    ])}
                  </div>
                  {migrationReadiness && (
                    <div className="lr-preserved-card">
                      <span className="lr-preserved-label">Migration Readiness</span>
                      <strong>{migrationReadiness.score_pct}%</strong>
                      <small>{migrationReadiness.blocked ? 'Blocked by current policy' : migrationReadiness.summary || 'Ready for review'}</small>
                      {renderEditorActionButtons([
                        onEdit ? {
                          key: 'summary-migration-runtime-review',
                          label: 'Open Runtime Review In Editor',
                          onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                        } : null,
                        onEdit && firstRuntimeBlockingArea ? {
                          key: 'summary-migration-blocking-area',
                          label: 'Open First Blocking Setting In Editor',
                          onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
                        } : null,
                        ...buildMigrationPolicyRelaxationButtons('summary-migration-relax'),
                      ].filter((button): button is EditorActionButton => Boolean(button)))}
                    </div>
                  )}
                </div>

                {importSummary.parameterFileSections.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Parameter File Mappings</strong>
                      <span>{importSummary.parameterFileSections.length}</span>
                    </div>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'parameter-mappings-review',
                        label: 'Open Parameter Files In Editor',
                        onClick: () => openImportedParameterDataInEditor(firstMappedParameterDataSourceName || firstGeneratedDataSourceName),
                      },
                      firstMappedParameterDataSourceName ? {
                        key: 'parameter-mappings-source',
                        label: 'Open First Data Source In Editor',
                        onClick: () => openImportedDataSourceInEditor(firstMappedParameterDataSourceName),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <div className="lr-table-wrap">
                      <table className="lr-parameter-table">
                        <thead>
                          <tr>
                            <th>File</th>
                            <th>Section</th>
                            <th>Mapped Data Source</th>
                            <th>Mapping</th>
                            <th>Action</th>
                          </tr>
                        </thead>
                        <tbody>
                          {importSummary.parameterFileSections.map((section) => {
                            const mappedSourceName = findImportedParameterMappingDataSourceName(generatedDataSources, section.values)
                            return (
                              <tr key={`${section.file}:${section.name}`}>
                                <td><strong>{section.file}</strong></td>
                                <td>{section.name}</td>
                                <td>{mappedSourceName || 'Not generated'}</td>
                                <td>{formatParameterMappingDetails(section.values) || 'No explicit mapping options'}</td>
                                <td>
                                  {mappedSourceName && onEdit ? (
                                    <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: mappedSourceName })}>
                                      Open Mapped Data Source In Editor
                                    </button>
                                  ) : (
                                    <span className="lr-table-muted">None</span>
                                  )}
                                </td>
                              </tr>
                            )
                          })}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                {importSummary.correlationSuggestions.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Correlation Suggestions</strong>
                      <span>{importSummary.correlationSuggestions.length}</span>
                    </div>
                    {onEdit && renderEditorActionButtons([
                      firstCorrelationSourceTarget ? {
                        key: 'correlation-review-source',
                        label: 'Open First Correlation Source In Editor',
                        onClick: () => openImportedScriptInEditor({ targetAction: firstCorrelationSourceTarget.actionName }),
                      } : null,
                      firstCorrelationUsageTarget ? {
                        key: 'correlation-review-usage',
                        label: 'Open First Correlation Use In Editor',
                        onClick: () => openImportedScriptInEditor({ targetAction: firstCorrelationUsageTarget.actionName }),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <ul>
                      {importSummary.correlationSuggestions.map((correlation) => {
                        const sourceActionName = findImportedActionName(importedScript, correlation.found_at)
                        const usedActionTargets = correlation.used_at
                          .map((location) => ({ location, actionName: findImportedActionName(importedScript, location) }))
                          .filter((target): target is { location: string; actionName: string } => Boolean(target.actionName))
                        return (
                        <li key={`${correlation.name}:${correlation.expression}:${correlation.found_at}`}>
                          <strong>{correlation.name}</strong>
                          {`: ${correlation.source} via ${correlation.expression}`}
                          {correlation.sample_value ? ` • ${correlation.sample_value}` : ''}
                          {` • Found in ${correlation.found_at}`}
                          {correlation.used_at.length > 0 ? ` • Used in ${correlation.used_at.join(', ')}` : ''}
                          {sourceActionName && onEdit && (
                            <>
                              {' '}
                              <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetAction: sourceActionName })}>
                                Open Source Action In Editor
                              </button>
                            </>
                          )}
                          {usedActionTargets.map(({ location, actionName }) => (
                            <button key={`${correlation.name}:${location}`} className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetAction: actionName })}>
                              {`Open ${location} In Editor`}
                            </button>
                          ))}
                        </li>
                      )})}
                    </ul>
                  </div>
                )}

                {runtimeBlockingSettings.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Migration Readiness Blocking Settings</strong>
                      <span>{runtimeBlockingSettings.length}</span>
                    </div>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'runtime-blocking-review',
                        label: 'Open Runtime Review In Editor',
                        onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                      },
                      firstRuntimeBlockingArea ? {
                        key: 'runtime-blocking-area',
                        label: 'Open First Blocking Setting In Editor',
                        onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <ul>
                      {runtimeBlockingSettings.map((entry) => (
                        <li key={`blocking-${entry.id}`}>
                          <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                          {`: ${entry.concept}`}
                          {entry.runtime_files.length > 0 ? ` • ${entry.runtime_files.join(', ')}` : ''}
                          {onEdit && getImportedRuntimeSectionId(entry.area) && (
                            <>
                              {' '}
                              <button className="btn btn-secondary btn-sm" onClick={() => openImportedRuntimeSettingsInEditor(entry.area)}>
                                {`Open ${entry.area} Settings In Editor`}
                              </button>
                            </>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {importSummary.runtimeSettingsParity.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Runtime Settings Parity</strong>
                      <span>{importSummary.runtimeSettingsParity.length}</span>
                    </div>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'runtime-parity-review',
                        label: 'Open Runtime Review In Editor',
                        onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                      },
                      firstRuntimeParityArea ? {
                        key: 'runtime-parity-area',
                        label: 'Open First Runtime Setting In Editor',
                        onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeParityArea),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <ul>
                      {importSummary.runtimeSettingsParity.map((entry) => (
                        <li key={`parity-${entry.id}`}>
                          <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                          {`: ${entry.concept}`}
                          {entry.runtime_files.length > 0 ? ` • ${entry.runtime_files.join(', ')}` : ''}
                          {onEdit && getImportedRuntimeSectionId(entry.area) && (
                            <>
                              {' '}
                              <button className="btn btn-secondary btn-sm" onClick={() => openImportedRuntimeSettingsInEditor(entry.area)}>
                                {`Open ${entry.area} Settings In Editor`}
                              </button>
                            </>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {generatedDataSources.length > 0 && (
                  <div className="lr-review-section">
                    <div className="lr-review-section-header">
                      <strong>Generated Data Sources</strong>
                      <span>{generatedDataSources.length}</span>
                    </div>
                    {onEdit && renderEditorActionButtons([
                      {
                        key: 'generated-sources-review',
                        label: 'Open Parameter Data In Editor',
                        onClick: () => openImportedParameterDataInEditor(firstGeneratedDataSourceName),
                      },
                      firstGeneratedDataSourceName ? {
                        key: 'generated-sources-source',
                        label: 'Open First Data Source In Editor',
                        onClick: () => openImportedDataSourceInEditor(firstGeneratedDataSourceName),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <div className="lr-table-wrap">
                      <table className="lr-data-source-table">
                        <thead>
                          <tr>
                            <th>Source</th>
                            <th>Type</th>
                            <th>Rows</th>
                            <th>Columns</th>
                            <th>Action</th>
                          </tr>
                        </thead>
                        <tbody>
                          {generatedDataSources.map((source) => (
                            <tr key={source.name}>
                              <td><strong>{source.name}</strong></td>
                              <td>{source.type}</td>
                              <td>{source.rows}</td>
                              <td>
                                {typeof source.columns === 'number'
                                  ? `${source.columns} column${source.columns === 1 ? '' : 's'}`
                                  : source.columns.join(', ') || 'No columns'}
                              </td>
                              <td>
                                {onEdit ? (
                                  <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: source.name })}>
                                    Open Source In Editor
                                  </button>
                                ) : (
                                  <span className="lr-table-muted">None</span>
                                )}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                <details className="lr-preserved-details">
                  <summary>View preserved metadata JSON</summary>
                  <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
                </details>
              </div>
            )}
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}

function summarizeEnterpriseArchiveSupportMatrix(entries: EnterpriseArchiveSupportMatrixEntry[]): EnterpriseArchiveSupportMatrixSection[] {
  return [
    buildEnterpriseArchiveSupportMatrixSection('executed', 'Executed', entries),
    buildEnterpriseArchiveSupportMatrixSection('partial', 'Partial', entries),
    buildEnterpriseArchiveSupportMatrixSection('compatibility_only', 'Compatibility-only', entries),
    buildEnterpriseArchiveSupportMatrixSection('non_applicable', 'Non-applicable', entries),
    buildEnterpriseArchiveSupportMatrixSection('blocked', 'Blocked', entries),
  ]
}

function summarizeEnterpriseArchiveProtocolSupport(entries: EnterpriseArchiveProtocolSupportEntry[]): EnterpriseArchiveProtocolCoverageSection[] {
  if (entries.length === 0) return []
  return [
    buildEnterpriseArchiveProtocolCoverageSection('executable', 'Executable', entries),
    buildEnterpriseArchiveProtocolCoverageSection('review_only', 'Review-only', entries),
    buildEnterpriseArchiveProtocolCoverageSection('external_runner_required', 'External Runner', entries),
    buildEnterpriseArchiveProtocolCoverageSection('unsupported', 'Unsupported', entries),
  ].filter(section => section.items.length > 0)
}

function buildMigrationBlockStatuses(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  const statuses = ['blocked']
  if (options.block_partial) statuses.push('partial')
  if (options.block_compatibility_only) statuses.push('compatibility_only')
  if (options.block_non_applicable) statuses.push('non_applicable')
  return statuses
}

function normalizeRelaxableMigrationBlockStatus(status?: string | null): RelaxableMigrationBlockStatus | null {
  switch (status?.trim().toLowerCase()) {
    case 'partial':
      return 'partial'
    case 'compatibility_only':
      return 'compatibility_only'
    case 'non_applicable':
      return 'non_applicable'
    default:
      return null
  }
}

function getRelaxableMigrationBlockStatuses(
  readiness: EnterpriseArchiveMigrationReadiness | null,
  options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
): RelaxableMigrationBlockStatus[] {
  if (!readiness?.blocked || !Array.isArray(readiness.blocking_statuses)) return []

  const statuses = readiness.blocking_statuses
    .map((status) => normalizeRelaxableMigrationBlockStatus(status))
    .filter((status): status is RelaxableMigrationBlockStatus => Boolean(status))
    .filter((status) => {
      switch (status) {
        case 'partial':
          return options.block_partial
        case 'compatibility_only':
          return options.block_compatibility_only
        case 'non_applicable':
          return options.block_non_applicable
      }
    })

  return Array.from(new Set(statuses))
}

function getRelaxedMigrationBlockOptions(options: ImportOptions, status: RelaxableMigrationBlockStatus): ImportOptions | null {
  switch (status) {
    case 'partial':
      return options.block_partial ? { ...options, block_partial: false } : null
    case 'compatibility_only':
      return options.block_compatibility_only ? { ...options, block_compatibility_only: false } : null
    case 'non_applicable':
      return options.block_non_applicable ? { ...options, block_non_applicable: false } : null
  }
}

function buildEnterpriseArchiveSupportMatrixSection(status: string, title: string, entries: EnterpriseArchiveSupportMatrixEntry[]): EnterpriseArchiveSupportMatrixSection {
  const items = entries.filter(entry => entry.status === status)
  return {
    key: status,
    title,
    items,
    preview: items.slice(0, 2).map(entry => `${entry.area}: ${entry.concept}`).join(' • '),
  }
}

function buildEnterpriseArchiveProtocolCoverageSection(mode: string, title: string, entries: EnterpriseArchiveProtocolSupportEntry[]): EnterpriseArchiveProtocolCoverageSection {
  const items = entries.filter(entry => protocolImportMode(entry) === mode)
  return {
    key: mode,
    title,
    items,
    preview: items.slice(0, 2).map(entry => entry.display_name || entry.protocol || entry.id).join(' • '),
  }
}

function protocolImportMode(entry: EnterpriseArchiveProtocolSupportEntry): string {
  const status = entry.status || entry.import_mode || ''
  switch (status) {
    case 'supported':
    case 'review_then_executable':
      return 'executable'
    case 'review_only':
      return 'review_only'
    case 'external_runner':
    case 'external_runner_required':
      return 'external_runner_required'
    case 'unsupported':
    case 'out_of_scope':
      return 'unsupported'
    default:
      return entry.runner_requirement ? 'external_runner_required' : 'review_only'
  }
}

function getImportedParameterMappingValue(values: Record<string, string>, aliases: string[]): string {
  const normalizedAliases = aliases.map(alias => alias.toLowerCase())
  const match = Object.entries(values).find(([key]) => normalizedAliases.includes(key.toLowerCase()))
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

function findImportedParameterMappingDataSourceName(
  dataSources: Array<{ name: string }> | undefined,
  values: Record<string, string>,
): string | null {
  const dataFile = getImportedParameterMappingValue(values, ['table', 'filename', 'file', 'datafile', 'sourcefile'])
  if (!dataFile || !dataSources?.length) return null

  const sanitizedName = sanitizeImportedParameterDataSourceName(dataFile)
  const exactMatch = dataSources.find(source => source.name.trim() === sanitizedName)
  if (exactMatch) return exactMatch.name

  const lowerMatch = dataSources.find(source => source.name.trim().toLowerCase() === sanitizedName)
  return lowerMatch?.name || null
}

function formatParameterMappingDetails(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.join(' • ')
}
