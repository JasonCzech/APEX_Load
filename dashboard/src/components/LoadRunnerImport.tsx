import { useEffect, useMemo, useState, useRef, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { type DetectedLoadRunnerProtocol, type ImportRepairSuggestion, type LoadRunnerImportResult, type LoadRunnerImportSummary, type LoadRunnerMigrationReadiness, type LoadRunnerProtocolSupportEntry, type LoadRunnerReviewArtifact, type LoadRunnerSupportMatrixEntry, type LoadRunnerSupportMatrixExample, type LoadRunnerSupportMatrixResponse } from '../types/scriptImports'
import { type BrowserReplaySession } from '../types'
import { type LoadRunnerSourceReview, type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { buildReplayStartPayload, getImportSummary, summarizeInlineData } from './scriptEditorUtils'
import ScriptImportReview from './ScriptImportReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { type DraftEditorFocus, type ImportedSaveResult } from './useScriptsAuthoringState'
import './LoadRunnerImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

function getImportMetadata(script?: ScriptDefinition): ImportMetadata | null {
  return script?.metadata?.import || null
}

function getSourceReviewFromMetadata(metadata?: ImportMetadata | null): LoadRunnerSourceReview | null {
  const sourceReview = metadata?.preserved?.source_review as LoadRunnerSourceReview | undefined
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

function isLoadRunnerZipFile(file: File) {
  const lowerName = file.name.toLowerCase()
  return lowerName.endsWith('.zip') || file.type === 'application/zip' || file.type === 'application/x-zip-compressed'
}

function formatLoadRunnerFileSize(file: File) {
  return `${(file.size / 1024).toFixed(1)} KB`
}

function loadRunnerImportStatusLabel(status: LoadRunnerImportQueueStatus) {
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

function buildLoadRunnerImportQueueItem(file: File, id: string): LoadRunnerImportQueueItem {
  return {
    id,
    file,
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
  artifact: LoadRunnerReviewArtifact,
  detectedProtocol: DetectedLoadRunnerProtocol | null,
  projectId?: string,
  requestedName?: string,
  sourceReview?: LoadRunnerSourceReview | null,
): ScriptDefinition {
  const protocol = detectedProtocol?.apexload_protocol || detectedProtocol?.canonical_loadrunner_protocol || 'review'
  const name = requestedName?.trim() || artifact.title || detectedProtocol?.display_name || 'LoadRunner Protocol Review'
  return {
    name,
    version: '1.0.0',
    protocol,
    project_id: projectId,
    generated_by: 'apexload-loadrunner-import',
    generated_from: 'loadrunner',
    config: { base_url: '' },
    actions: [],
    metadata: {
      import: {
        source: 'loadrunner',
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
type LoadRunnerImportQueueStatus = 'queued' | 'validating' | 'validated' | 'converting' | 'converted' | 'failed'

interface LoadRunnerImportQueueItem {
  id: string
  file: File
  status: LoadRunnerImportQueueStatus
  result: LoadRunnerImportResult | null
  reviewMode: ReviewMode | null
  error: string
  notice: string
  replayValidationLoading: boolean
  replayValidationError: string
  replayValidationWarnings: string[]
  replayValidationSession: BrowserReplaySession | null
}

type LoadRunnerSupportMatrixSection = {
  key: string
  title: string
  items: LoadRunnerSupportMatrixEntry[]
  preview: string
}

type LoadRunnerProtocolCoverageSection = {
  key: string
  title: string
  items: LoadRunnerProtocolSupportEntry[]
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
  block_partial: true,
  block_compatibility_only: true,
  block_non_applicable: false,
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

export default function LoadRunnerImport({ projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const itemCounterRef = useRef(0)
  const [importItems, setImportItems] = useState<LoadRunnerImportQueueItem[]>([])
  const [selectedItemId, setSelectedItemId] = useState('')
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [supportMatrix, setSupportMatrix] = useState<LoadRunnerSupportMatrixEntry[]>([])
  const [supportMatrixNotes, setSupportMatrixNotes] = useState<string[]>([])
  const [supportMatrixExamples, setSupportMatrixExamples] = useState<LoadRunnerSupportMatrixExample[]>([])
  const [protocolSupportMatrix, setProtocolSupportMatrix] = useState<LoadRunnerProtocolSupportEntry[]>([])
  const [runtimeCoverageOpen, setRuntimeCoverageOpen] = useState(false)
  const [reviewArtifactSaving, setReviewArtifactSaving] = useState(false)

  const [options, setOptions] = useState<ImportOptions>(DEFAULT_IMPORT_OPTIONS)
  const selectedItem = useMemo(
    () => importItems.find(item => item.id === selectedItemId) || importItems[0] || null,
    [importItems, selectedItemId],
  )
  const file = selectedItem?.file ?? null
  const result = selectedItem?.result ?? null
  const reviewMode = selectedItem?.reviewMode ?? null
  const replayValidationLoading = selectedItem?.replayValidationLoading ?? false
  const replayValidationError = selectedItem?.replayValidationError ?? ''
  const replayValidationWarnings = selectedItem?.replayValidationWarnings ?? []
  const replayValidationSession = selectedItem?.replayValidationSession ?? null
  const displayError = error || selectedItem?.error || ''
  const displayNotice = notice || selectedItem?.notice || ''
  const queuedFileCount = importItems.length
  const validateButtonLabel = queuedFileCount > 1 ? `Validate ${queuedFileCount} Scripts` : 'Validate Only'
  const convertButtonLabel = queuedFileCount > 1 ? `Convert ${queuedFileCount} LoadRunner Scripts` : 'Convert LoadRunner Script'
  const importedScript = result?.script ?? result?.scripts?.[0] ?? null
  const detectedProtocol = result?.detected_protocol ?? result?.summary?.detected_protocol ?? null
  const reviewArtifact = result?.review_artifact ?? result?.summary?.review_artifact ?? null
  const executableImport = !detectedProtocol || detectedProtocol.import_mode === 'executable'
  const importMetadata = getImportMetadata(importedScript || undefined)
  const importSummary = getImportSummary(importMetadata)
  const reviewSummary: LoadRunnerImportSummary | null = result?.summary || null
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
  const supportMatrixSections = summarizeLoadRunnerSupportMatrix(supportMatrix)
  const hasRuntimeCoverage = supportMatrix.length > 0
  const protocolCoverageSections = summarizeLoadRunnerProtocolSupport(protocolSupportMatrix)
  const migrationReadiness: LoadRunnerMigrationReadiness | null = reviewSummary?.migration_readiness || null
  const reviewedMigrationPolicy = migrationReadiness?.policy?.block_statuses
    ? buildMigrationPolicyOptions(migrationReadiness.policy.block_statuses)
    : null
  const hasPendingMigrationPolicyChanges = reviewedMigrationPolicy !== null && !sameMigrationBlockPolicy(options, reviewedMigrationPolicy)
  const saveBlockedByMigrationPolicy = Boolean(migrationReadiness?.blocked)
  const saveBlockedByProtocolGate = Boolean(detectedProtocol && detectedProtocol.import_mode !== 'executable')
  const browserImport = isBrowserImport(importedScript)
  const browserReplayStatus = replayValidationSession?.status || null
  const browserReplayStarting = browserImport && replayValidationLoading
  const browserReplayStatusLabel = browserReplayStarting
    ? 'Starting'
    : formatRuntimeSettingStatusLabel(browserReplayStatus || 'not run')
  const browserReplayWasStarted = browserImport && replayValidationSession !== null
  const browserReplayInProgress = browserImport && (browserReplayStarting || browserReplayStatus === 'queued' || browserReplayStatus === 'running' || browserReplayStatus === 'paused')
  const browserReplayNeedsRerun = browserImport && !browserReplayInProgress && (replayValidationSession === null || browserReplayStatus === 'failed' || browserReplayStatus === 'stopped')
  const saveRequiresBrowserReplay = executableImport && browserReplayNeedsRerun
  const saveActionLabel = saveBlockedByProtocolGate
    ? 'Save Review Artifact Instead'
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
          ? 'Save Review Artifact Before Script Promotion'
              : (saveRequiresBrowserReplay
          ? `This preview came from the dry-run validator. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving it to the library.`
                : 'This preview came from the dry-run validator. You can still save the reviewed script if the output looks correct.'))))
    : (saveBlockedByMigrationPolicy
        ? 'Review the generated script, preserved metadata, and warnings. Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.'
        : (browserReplayInProgress
        ? 'Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving it to the library.'
            : (saveBlockedByProtocolGate
          ? 'Review the protocol classification and save the review artifact; this import is not executable yet.'
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

  const updateImportItem = (itemId: string, patch: Partial<LoadRunnerImportQueueItem>) => {
    setImportItems(items => items.map(item => item.id === itemId ? { ...item, ...patch } : item))
  }

  const openImportedScriptInEditor = (focus?: DraftEditorFocus) => {
    if (!importedScript || !onEdit) return
    onEdit(importedScript, focus)
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

    api.getLoadRunnerSupportMatrix()
      .then((response: LoadRunnerSupportMatrixResponse) => {
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

    const zipFiles = incomingFiles.filter(isLoadRunnerZipFile)
    if (zipFiles.length === 0) {
      setError('Please upload ZIP files containing LoadRunner script folders.')
      return
    }

    const nextItems = zipFiles.map((nextFile) => {
      itemCounterRef.current += 1
      return buildLoadRunnerImportQueueItem(nextFile, `lr-import-${Date.now()}-${itemCounterRef.current}`)
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
    const request = mode === 'validate' ? api.validateLoadRunnerImport : api.importLoadRunner
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
      setNotice(`${action} ${successCount} of ${targetItems.length} LoadRunner script${targetItems.length === 1 ? '' : 's'}${failureCount > 0 ? `; ${failureCount} failed` : ''}.`)
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
      const savedScript = await api.createScript(importedScript, projectId)
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, importedScript)] })
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
        source: 'loadrunner_review_artifact',
        name: reviewArtifact.title || detectedProtocol?.display_name || 'LoadRunner protocol review',
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
          <h2>Import LoadRunner Script</h2>
          <p className="lr-subtitle">Upload a LoadRunner/VuGen ZIP archive. APEX Load detects the VuGen protocol, routes executable HTTP, SOAP, browser, database, and network imports, and preserves review artifacts for protocols that still require evidence or external runners.</p>
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
            <div className="lr-file-list" aria-label="Queued LoadRunner scripts">
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
                    <small>{formatLoadRunnerFileSize(item.file)} · {loadRunnerImportStatusLabel(item.status)}</small>
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
            <p>Drop one or more LoadRunner <code>.zip</code> files here</p>
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
                <p>Code-owned LoadRunner runtime settings support matrix</p>
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
          <ul className="lr-protocol-route-list" aria-label="VuGen protocol import routes">
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
            saveDisabled={saveBlockedByProtocolGate || saveBlockedByMigrationPolicy || replayValidationLoading || browserReplayInProgress}
            saveLabel={saveActionLabel}
            onSave={handleSaveConverted}
            onEdit={onEdit ? () => openImportedScriptInEditor() : undefined}
          >

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
                    <ul>
                      <li>Replay Session: {replayValidationSession.id}</li>
                      <li>Replay Status: {browserReplayStatusLabel}</li>
                      <li>Replay Group: {replayValidationSession.group}</li>
                    </ul>
                  )}
                  {replayValidationWarnings.length > 0 && (
                    <ul>{replayValidationWarnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
                  )}
                  {replayValidationError && <div className="lr-error">{replayValidationError}</div>}
                </div>
              </div>
            )}

            {/* Stats */}
            <div className="lr-stats-grid">
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.extractors}</span><span className="lr-stat-label">Extractors</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.transactions}</span><span className="lr-stat-label">Transactions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.think_times}</span><span className="lr-stat-label">Think Times</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.total_functions}</span><span className="lr-stat-label">Total LR Functions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.skipped}</span><span className="lr-stat-label">Skipped</span></div>
            </div>

            {unsupportedFunctions.length > 0 && (
              <div className="lr-warnings">
                <h4>Unsupported LoadRunner Functions ({unsupportedFunctions.length})</h4>
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
                      </div>
                    )})}
                  </div>
                )}
              </div>
            )}

            {sourceReview && (
              <SourceMappingReview sourceReview={sourceReview} script={importedScript} />
            )}

            {importMetadata && (
              <div className="lr-preserved">
                <div className="lr-preserved-header">
                  <h4>Preserved LoadRunner Settings</h4>
                  <span>These values will be saved with the APEX Load script.</span>
                </div>

                <div className="lr-preserved-grid">
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Project File</span>
                    <strong>{reviewSummary?.project_file || importMetadata.source_script_name || importedScript.name}</strong>
                    <small>{importMetadata.source || 'loadrunner'}</small>
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
                    <strong>{importMetadata.source || 'loadrunner'}</strong>
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

function summarizeLoadRunnerSupportMatrix(entries: LoadRunnerSupportMatrixEntry[]): LoadRunnerSupportMatrixSection[] {
  return [
    buildLoadRunnerSupportMatrixSection('executed', 'Executed', entries),
    buildLoadRunnerSupportMatrixSection('partial', 'Partial', entries),
    buildLoadRunnerSupportMatrixSection('compatibility_only', 'Compatibility-only', entries),
    buildLoadRunnerSupportMatrixSection('non_applicable', 'Non-applicable', entries),
    buildLoadRunnerSupportMatrixSection('blocked', 'Blocked', entries),
  ]
}

function summarizeLoadRunnerProtocolSupport(entries: LoadRunnerProtocolSupportEntry[]): LoadRunnerProtocolCoverageSection[] {
  if (entries.length === 0) return []
  return [
    buildLoadRunnerProtocolCoverageSection('executable', 'Executable', entries),
    buildLoadRunnerProtocolCoverageSection('review_only', 'Review-only', entries),
    buildLoadRunnerProtocolCoverageSection('external_runner_required', 'External Runner', entries),
    buildLoadRunnerProtocolCoverageSection('unsupported', 'Unsupported', entries),
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
  readiness: LoadRunnerMigrationReadiness | null,
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

function buildLoadRunnerSupportMatrixSection(status: string, title: string, entries: LoadRunnerSupportMatrixEntry[]): LoadRunnerSupportMatrixSection {
  const items = entries.filter(entry => entry.status === status)
  return {
    key: status,
    title,
    items,
    preview: items.slice(0, 2).map(entry => `${entry.area}: ${entry.concept}`).join(' • '),
  }
}

function buildLoadRunnerProtocolCoverageSection(mode: string, title: string, entries: LoadRunnerProtocolSupportEntry[]): LoadRunnerProtocolCoverageSection {
  const items = entries.filter(entry => protocolImportMode(entry) === mode)
  return {
    key: mode,
    title,
    items,
    preview: items.slice(0, 2).map(entry => entry.display_name || entry.protocol || entry.id).join(' • '),
  }
}

function protocolImportMode(entry: LoadRunnerProtocolSupportEntry): string {
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
