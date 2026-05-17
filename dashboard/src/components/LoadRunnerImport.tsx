import { useEffect, useState, useRef, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { type ImportRepairSuggestion, type ImportedScript, type LoadRunnerImportResult, type LoadRunnerImportSummary, type LoadRunnerMigrationReadiness, type LoadRunnerSupportMatrixEntry, type LoadRunnerSupportMatrixExample, type LoadRunnerSupportMatrixResponse } from '../types/scriptImports'
import { type BrowserReplaySession } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { buildReplayStartPayload, getImportSummary, summarizeInlineData } from './scriptEditorUtils'
import ScriptImportReview from './ScriptImportReview'
import { type DraftEditorFocus } from './useScriptsAuthoringState'
import './LoadRunnerImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

function getImportMetadata(script?: ImportedScript): ImportMetadata | null {
  return script?.metadata?.import || null
}

function toScriptDefinition(script: ImportedScript): ScriptDefinition {
  return script as unknown as ScriptDefinition
}

function getSummaryList(values?: string[]) {
  return Array.isArray(values) ? values : []
}

function isBrowserImport(script: ImportedScript | null): boolean {
  return script?.protocol === 'browser'
}

interface Props {
  projectId?: string
  onImported?: (script: ImportedScript) => void
  onEdit?: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'

type LoadRunnerSupportMatrixSection = {
  key: string
  title: string
  items: LoadRunnerSupportMatrixEntry[]
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

  return value.replace(/\bbrowser replay\b/gi, (match) => (
    match[0] === 'B' ? 'Draft Browser Replay' : 'draft browser replay'
  ))
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

function findImportedActionName(script?: ImportedScript | null, location?: string | null): string | null {
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

function findFirstImportedActionTarget(script: ImportedScript | null, locations: Array<string | null | undefined>): { location: string; actionName: string } | null {
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
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [result, setResult] = useState<LoadRunnerImportResult | null>(null)
  const [reviewMode, setReviewMode] = useState<ReviewMode | null>(null)
  const [supportMatrix, setSupportMatrix] = useState<LoadRunnerSupportMatrixEntry[]>([])
  const [supportMatrixNotes, setSupportMatrixNotes] = useState<string[]>([])
  const [supportMatrixExamples, setSupportMatrixExamples] = useState<LoadRunnerSupportMatrixExample[]>([])
  const [replayValidationLoading, setReplayValidationLoading] = useState(false)
  const [replayValidationError, setReplayValidationError] = useState('')
  const [replayValidationWarnings, setReplayValidationWarnings] = useState<string[]>([])
  const [replayValidationSession, setReplayValidationSession] = useState<BrowserReplaySession | null>(null)

  const [options, setOptions] = useState<ImportOptions>(DEFAULT_IMPORT_OPTIONS)
  const importedScript = result?.script ?? result?.scripts?.[0] ?? null
  const importMetadata = getImportMetadata(importedScript || undefined)
  const importSummary = getImportSummary(importMetadata)
  const reviewSummary: LoadRunnerImportSummary | null = result?.summary || null
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
  const migrationReadiness: LoadRunnerMigrationReadiness | null = reviewSummary?.migration_readiness || null
  const reviewedMigrationPolicy = migrationReadiness?.policy?.block_statuses
    ? buildMigrationPolicyOptions(migrationReadiness.policy.block_statuses)
    : null
  const hasPendingMigrationPolicyChanges = reviewedMigrationPolicy !== null && !sameMigrationBlockPolicy(options, reviewedMigrationPolicy)
  const saveBlockedByMigrationPolicy = Boolean(migrationReadiness?.blocked)
  const browserImport = isBrowserImport(importedScript)
  const browserReplayStatus = replayValidationSession?.status || null
  const browserReplayStarting = browserImport && replayValidationLoading
  const browserReplayStatusLabel = browserReplayStarting
    ? 'Starting'
    : formatRuntimeSettingStatusLabel(browserReplayStatus || 'not run')
  const browserReplayWasStarted = browserImport && replayValidationSession !== null
  const browserReplayInProgress = browserImport && (browserReplayStarting || browserReplayStatus === 'queued' || browserReplayStatus === 'running' || browserReplayStatus === 'paused')
  const browserReplayNeedsRerun = browserImport && !browserReplayInProgress && (replayValidationSession === null || browserReplayStatus === 'failed' || browserReplayStatus === 'stopped')
  const saveRequiresBrowserReplay = browserReplayNeedsRerun
  const saveActionLabel = browserReplayInProgress
    ? 'Draft Browser Replay In Progress'
    : (saveRequiresBrowserReplay
        ? (browserReplayWasStarted ? 'Rerun Draft Browser Replay Before Saving' : 'Start Draft Browser Replay Before Saving')
        : (reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save to Script Library'))
  const replayActionLabel = replayValidationLoading
    ? 'Starting Draft Browser Replay...'
    : (browserReplayInProgress
        ? 'Draft Browser Replay In Progress'
      : (browserReplayWasStarted ? 'Rerun Draft Browser Replay' : 'Start Draft Browser Replay'))
  const replayPanelDescription = browserReplayInProgress
    ? 'Wait for the draft browser replay to finish before saving it to the library.'
    : (saveRequiresBrowserReplay
      ? `${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay against the converted browser script before saving it to the library.`
      : 'The latest draft browser replay completed. Review the replay results before saving it to the library.')
  const reviewSubtitle = reviewMode === 'validation'
    ? (saveBlockedByMigrationPolicy
        ? 'This preview came from the dry-run validator. Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.'
        : (browserReplayInProgress
            ? 'This preview came from the dry-run validator. Wait for the draft browser replay to finish before saving the reviewed script.'
            : (saveRequiresBrowserReplay
                ? `This preview came from the dry-run validator. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving the reviewed script.`
                : 'This preview came from the dry-run validator. You can still save the reviewed script if the output looks correct.')))
    : (saveBlockedByMigrationPolicy
        ? 'Review the generated script, preserved metadata, and warnings. Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.'
        : (browserReplayInProgress
            ? 'Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving.'
            : (saveRequiresBrowserReplay
                ? `Review the generated script, preserved metadata, and warnings. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving.`
                : 'Review the generated script, preserved metadata, and warnings before saving.')))
  const runtimeBlockingSettings = importSummary.runtimeSettingReadiness?.blocking_settings || []
  const generatedDataSources = importedScript
    ? (toScriptDefinition(importedScript).data_sources || []).map((source, index) => {
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

  const openImportedScriptInEditor = (focus?: DraftEditorFocus) => {
		if (!importedScript || !onEdit) return
    onEdit(toScriptDefinition(importedScript), focus)
	}

  const openImportedRuntimeSettingsInEditor = (area: string) => {
    openImportedScriptInEditor({ targetSection: 'runtime', targetRuntimeArea: area })
  }

  const openImportedDataSourceInEditor = (sourceName?: string | null) => {
    const normalizedSourceName = sourceName?.trim()
    if (!normalizedSourceName) return
    openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: normalizedSourceName })
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
        void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions))
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
      void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions))
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
            onClick: () => openImportedScriptInEditor({ targetSection: 'parameter-data' }),
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

  const resetReplayValidation = () => {
    setReplayValidationLoading(false)
    setReplayValidationError('')
    setReplayValidationWarnings([])
    setReplayValidationSession(null)
  }

  const setBrowserReplayStartedNotice = (wasStarted: boolean) => {
    setNotice(wasStarted
      ? 'Draft browser replay restarted. Review the refreshed draft browser replay session before saving this import.'
      : 'Draft browser replay started. Review the draft browser replay session before saving this import.')
  }

  useEffect(() => {
    let cancelled = false

    api.getLoadRunnerSupportMatrix()
      .then((response: LoadRunnerSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(Array.isArray(response.support_matrix) ? response.support_matrix : [])
          setSupportMatrixNotes(Array.isArray(response.notes) ? response.notes : [])
          setSupportMatrixExamples(Array.isArray(response.tested_examples) ? response.tested_examples : [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix([])
          setSupportMatrixNotes([])
          setSupportMatrixExamples([])
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
          setReplayValidationSession(response.session)
          if (Array.isArray(response.warnings)) {
            setReplayValidationWarnings(response.warnings)
          }
          if (response.session.status === 'failed' && response.session.error) {
            setReplayValidationError(response.session.error)
          }
        })
        .catch((error: unknown) => {
          if (!cancelled) {
            setReplayValidationError(getErrorMessage(error, 'Draft browser replay status refresh failed'))
          }
        })
    }

    const intervalId = window.setInterval(refreshSession, 2000)
    return () => {
      cancelled = true
      window.clearInterval(intervalId)
    }
  }, [api, browserReplayInProgress, replayValidationLoading, replayValidationSession?.id])

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault(); setDragOver(false)
    const f = e.dataTransfer.files[0]
    if (f && (f.name.endsWith('.zip') || f.type === 'application/zip')) {
      setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null); resetReplayValidation()
    } else { setError('Please upload a ZIP file containing a LoadRunner script folder.') }
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (f) { setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null); resetReplayValidation() }
  }

  const runReview = async (mode: 'convert' | 'validate', reviewOptions: ImportOptions = options, completionNotice?: string) => {
    if (!file) return
    setWorkingMode(mode); setError(''); setNotice(''); setResult(null); resetReplayValidation()
    try {
      const request = mode === 'validate' ? api.validateLoadRunnerImport : api.importLoadRunner
      const res = await request(file, {
        name: reviewOptions.name || undefined,
        project_id: projectId,
        base_url: reviewOptions.base_url || undefined,
        include_assertions: reviewOptions.include_assertions,
        preserve_transactions: reviewOptions.preserve_transactions,
        migration_block_statuses: buildMigrationBlockStatuses(reviewOptions),
      })
      setResult(res)
      setReviewMode(mode === 'validate' ? 'validation' : 'conversion')
      setNotice(completionNotice || (mode === 'validate'
        ? 'Validation complete. Review the generated script, warnings, and preserved settings before saving.'
        : 'Conversion complete. Review the generated script, warnings, and preserved settings before saving.'))
    } catch (error: unknown) {
      setError(getErrorMessage(error, mode === 'validate' ? 'Validation failed' : 'Import failed'))
    } finally { setWorkingMode(null) }
  }

  const startBrowserReplayValidation = async () => {
    if (!importedScript) return
    const script = toScriptDefinition(importedScript)

    setReplayValidationLoading(true)
    setReplayValidationError('')
    setReplayValidationWarnings([])

    try {
      const res = await api.startDraftBrowserReplaySession(buildReplayStartPayload(script, {
        projectId,
        ignoreThinkTime: true,
      }))
      setReplayValidationSession(res.session)
      setReplayValidationWarnings(Array.isArray(res.warnings) ? res.warnings : [])
      return true
    } catch (error: unknown) {
      setReplayValidationError(getErrorMessage(error, 'Draft browser replay failed to start'))
      return false
    } finally {
      setReplayValidationLoading(false)
    }
  }

  const handleSaveConverted = async () => {
    if (!importedScript) return
    if (saveBlockedByMigrationPolicy) {
      setError('Saving is disabled while migration readiness is blocked by the active policy. Relax the policy or resolve the blocking runtime settings first.')
      return
    }
    if (browserReplayInProgress) {
      setNotice('Draft browser replay is still running. Wait for it to finish before saving this import.')
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
      await api.createScript(importedScript, projectId)
      onImported?.(importedScript)
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save script'))
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
          <p className="lr-subtitle">Upload a ZIP archive of your LoadRunner Web HTTP/HTML VUser script folder. The converter handles <code>web_url</code>, <code>web_submit_data</code>, <code>web_custom_request</code>, correlations, and think times.</p>
        </div>
        {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
      </div>

      {/* Drop zone */}
      <div className={`lr-dropzone ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
        onDragOver={e => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={() => fileRef.current?.click()}>
        <input ref={fileRef} type="file" accept=".zip" onChange={handleFileSelect} hidden />
        {file ? (
          <div className="lr-file-info">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div>
              <span className="lr-filename">{file.name}</span>
              <span className="lr-filesize">{(file.size / 1024).toFixed(1)} KB</span>
            </div>
            <button className="btn btn-sm btn-ghost" onClick={e => { e.stopPropagation(); setFile(null); setResult(null) }}>Change</button>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop a LoadRunner <code>.zip</code> file here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      {/* Options */}
      <div className="lr-options">
        <h3 className="lr-section-title">Conversion Options</h3>
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

      <div className="settings-notice">
        SOAP-aware detection is enabled for LoadRunner request assets that already resolve to SOAP-style calls or envelopes. Java-over-HTTP import remains unavailable until the dedicated runtime and transport format are implemented.
      </div>

      {supportMatrix.length > 0 && (
        <div className="lr-preserved">
          <div className="lr-preserved-header">
            <h4>Runtime Settings Coverage</h4>
            <span>Code-owned LoadRunner Web HTTP/HTML runtime support matrix</span>
          </div>

          <div className="lr-preserved-grid">
            {supportMatrixSections.map(section => (
              <div className="lr-preserved-card" key={section.key}>
                <span className="lr-preserved-label">{section.title}</span>
                <strong>{section.items.length}</strong>
                <small>{section.preview || 'No entries'}</small>
              </div>
            ))}
          </div>

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
      )}

      <div className="replay-panel-actions">
        <button className="btn btn-secondary btn-lg" onClick={() => runReview('validate')} disabled={!file || workingMode !== null}>
          {workingMode === 'validate' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
        </button>
        <button className="btn btn-primary btn-lg" onClick={() => runReview('convert')} disabled={!file || workingMode !== null}>
          {workingMode === 'convert' ? <><span className="spinner" /> Converting...</> : 'Convert LoadRunner Script'}
        </button>
      </div>

      {error && <div className="lr-error">{error}</div>}
      {notice && <div className="settings-notice">{notice}</div>}

      {/* Results */}
      {result && importedScript && (
        <div className="lr-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Complete'}
            subtitle={reviewSubtitle}
            script={importedScript}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            saveDisabled={saveBlockedByMigrationPolicy || replayValidationLoading || browserReplayInProgress}
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

            {importMetadata && (
              <div className="lr-preserved">
                <div className="lr-preserved-header">
                  <h4>Preserved LoadRunner Settings</h4>
                  <span>These values will be saved with the Stormrunner script.</span>
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
                        onClick: () => openImportedScriptInEditor({ targetSection: 'parameter-data' }),
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
                        onClick: () => openImportedScriptInEditor({ targetSection: 'parameter-data' }),
                      },
                      firstMappedParameterDataSourceName ? {
                        key: 'parameter-mappings-source',
                        label: 'Open First Data Source In Editor',
                        onClick: () => openImportedDataSourceInEditor(firstMappedParameterDataSourceName),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <ul>
                      {importSummary.parameterFileSections.map((section) => {
                        const mappedSourceName = findImportedParameterMappingDataSourceName(generatedDataSources, section.values)
                        return (
                          <li key={`${section.file}:${section.name}`}>
                            <strong>{section.file}</strong>
                            {`: ${section.name}`}
                            {renderParameterMappingDetails(section.values)}
                            {mappedSourceName && onEdit && (
                              <>
                                {' '}
                                <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: mappedSourceName })}>
                                  Open Mapped Data Source In Editor
                                </button>
                              </>
                            )}
                          </li>
                        )
                      })}
                    </ul>
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
                        onClick: () => openImportedScriptInEditor({ targetSection: 'parameter-data' }),
                      },
                      firstGeneratedDataSourceName ? {
                        key: 'generated-sources-source',
                        label: 'Open First Data Source In Editor',
                        onClick: () => openImportedDataSourceInEditor(firstGeneratedDataSourceName),
                      } : null,
                    ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
                    <ul>
                      {generatedDataSources.map((source) => (
                        <li key={source.name}>
                          <strong>{source.name}</strong>
                          {`: ${source.type} • ${source.rows} row${source.rows === 1 ? '' : 's'} • `}
                          {typeof source.columns === 'number'
                            ? `${source.columns} column${source.columns === 1 ? '' : 's'}`
                            : source.columns.join(', ') || 'No columns'}
                          {onEdit && (
                            <>
                              {' '}
                              <button className="btn btn-secondary btn-sm" onClick={() => openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: source.name })}>
                                Open Source In Editor
                              </button>
                            </>
                          )}
                        </li>
                      ))}
                    </ul>
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

function renderParameterMappingDetails(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.length > 0 ? ` • ${details.join(' • ')}` : ''
}
