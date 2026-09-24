import JMeterBatchRepairs from './JMeterBatchRepairs'
import { importActionEntries } from './importReviewIssues'
import JMeterMessagingPreview from './JMeterMessagingPreview'
import JMeterScenarioRuntimeEditor from './JMeterScenarioRuntimeEditor'
import { ImportConversionDetails } from './ImportConversionDetails'
import ImportWorkflowGuide from './ImportWorkflowGuide'
import ImportSaveConfirmation from './ImportSaveConfirmation'
import { useImportSaveOutcome, type ImportSaveDestination } from './useImportSaveOutcome'
import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { summarizeImportExtractors, summarizeSupportMatrix, summarizeSupportMatrixEntries } from './jmeterImportUtils'
import { type JMeterImportResult as ImportResult, type JMeterSupportMatrixEntry, type JMeterSupportMatrixResponse, type SecretBindingSelection, type SharedValues, type SupportMatrix } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { canUseNativeDialogs, filesFromDroppedPaths, nativeFileReadErrorMessage, pickFilesViaBridge, shouldUseBrowserFileFallback } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import DropIngestOverlay from './DropIngestOverlay'
import { useDropIngestFeedback } from './dropIngestFeedback'
import ImportCoverageButton from './ImportCoverageButton'
import ParameterizationReview from './ParameterizationReview'
import ScriptImportReview from './ScriptImportReview'
import { type SecretBindingReviewHandle } from './SecretBindingReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScripts } from './importSaveUtils'
import { useImportReviewGeneration } from './importReviewGeneration'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import { refreshedSecretBindingRequirements, replaceSecretBindingRequirementsForScript, secretBindingRequirementsForScript } from './secretBindingUtils'
import { credentialPreservingImportDraftRequiresSave, earliestImportDraftExpiry } from './protectedImportSession'
import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'
import './EnterpriseArchiveImport.css'

interface Props {
  initialFile?: File | null
  projectId?: string
  folderId?: string | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
}

type ReviewMode = 'validation' | 'conversion'

const JMETER_ASSET_PAGE_SIZE = 50
const JMETER_EXTRACTOR_PAGE_SIZE = 100

function getPrimaryScript(result: ImportResult | null): ScriptDefinition | null {
  return result?.script || result?.scripts?.[0] || null
}

function buildFallbackPrimaryScript(name: string): ScriptDefinition {
  return {
    name,
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: '' },
    actions: [],
  }
}

function sourceReviewForJMeter(result?: ImportResult | null, script?: ScriptDefinition | null): SourceReview | null {
  const metadataReview = script?.metadata?.import?.preserved?.source_review as SourceReview | undefined
  const review = result?.source_review || metadataReview
  if (!review || (!Array.isArray(review.source_files) && !Array.isArray(review.mappings))) return null
  return review
}

export default function JMeterImport({ initialFile, projectId, folderId, onImported, onEdit, onClose, onOperationBusyChange }: Props) {
  const api = useApi()
  const sourceSecretSessions = useSourceSecretSessionLifecycle(api.discardImportSecretSession)
  const fileRef = useRef<HTMLInputElement>(null)
  const dropzoneRef = useRef<HTMLDivElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  // Destructured for stable callback identity — the native drop handler is a
  // useDesktopFileDrop dependency and must not change every render.
  const { phase: ingestPhase, label: ingestLabel, hostClassName: ingestHostClass, begin: beginIngest, succeed: succeedIngest, fail: failIngest } = useDropIngestFeedback()
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [saving, setSaving] = useState(false)
  const saveInFlightRef = useRef<{
    result: ImportResult
    projectId: string | undefined
    file: File | null
  } | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [supportMatrix, setSupportMatrix] = useState<SupportMatrix | null>(null)
  const [supportEntries, setSupportEntries] = useState<JMeterSupportMatrixEntry[]>([])
  const [supportNotes, setSupportNotes] = useState<string[]>([])
  const [reviewMode, setReviewMode] = useState<ReviewMode | null>(null)
  const [result, setResult] = useState<ImportResult | null>(null)
  const latestResultRef = useRef<ImportResult | null>(result)
  latestResultRef.current = result
  const selectedFileRef = useRef<File | null>(file)
  selectedFileRef.current = file
  const {
    generation: reviewGeneration,
    generationRef: reviewGenerationRef,
    advance: advanceReviewGeneration,
  } = useImportReviewGeneration()
  const reviewScope = `${reviewGeneration}:${projectId || ''}`
  const { savedResult, publish: publishSaved, openSaved } = useImportSaveOutcome(reviewScope, onImported)
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  const [parameterizationApplyingByScript, setParameterizationApplyingByScript] = useState<Record<string, boolean>>({})
  const [parameterizationScriptIndex, setParameterizationScriptIndex] = useState(0)
  const [assetPage, setAssetPage] = useState(0)
  const [extractorPage, setExtractorPage] = useState(0)
  const parameterizationApplying = Object.values(parameterizationApplyingByScript).some(Boolean)
  const [settingsEditing, setSettingsEditing] = useState(false)
  const [credentialActionInProgress, setCredentialActionInProgress] = useState(false)
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const operationBusy = workingMode !== null || saving || parameterizationApplying || credentialActionInProgress
  const sourceMutationLocked = settingsEditing || saving
  const sourceMutationLockedRef = useRef(sourceMutationLocked)
  sourceMutationLockedRef.current = sourceMutationLocked
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const invalidateReview = useCallback(() => {
    sourceSecretSessions.clear('review')
    advanceReviewGeneration()
    setWorkingMode(null)
    // Invalidating the visible review does not cancel an HTTP save already in
    // flight. Its physical fence stays raised until that promise settles.
    setResult(null)
    setReviewMode(null)
    setNotice('')
    setParameterizationApplyingByScript({})
    setParameterizationScriptIndex(0)
    setAssetPage(0)
    setExtractorPage(0)
    setCredentialActionInProgress(false)
  }, [advanceReviewGeneration, sourceSecretSessions])
  useEffect(() => () => {
    // Route transitions can unmount this importer without invoking Back.
    // Fence any completed persistence response from navigating over the next
    // authoring surface while still publishing it for a library refresh.
    reviewGenerationRef.current += 1
  }, [reviewGenerationRef])
  const [options, setOptions] = useState({
    name: '',
    base_url: '',
    time_mode: '',
    timezone: '',
    variable_bindings: {} as Record<string, string>,
    include_assertions: true,
    scenario_draft: true,
  })
  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    repairEvidenceRef.current = null
    invalidateReview()
  }, [invalidateReview, projectId])
  const supportMatrixSections = summarizeSupportMatrix(supportMatrix)
  const coverageSections = summarizeSupportMatrixEntries(supportEntries)
  const extractorSummary = summarizeImportExtractors(result?.scripts || [])
  const primaryScript = getPrimaryScript(result)
  const sourceReview = sourceReviewForJMeter(result, primaryScript)
  const repairEvidenceRef = useRef<ImportResult | null>(null)
  if (result) repairEvidenceRef.current = result
  const unresolvedVariables = [...new Set((repairEvidenceRef.current?.scripts || []).flatMap(script => importActionEntries(script).flatMap(({ action }) => Array.isArray(action.metadata?.missing_variables) ? action.metadata.missing_variables.filter((name): name is string => typeof name === 'string') : [])))]
  const declaredVariables = repairEvidenceRef.current?.summary?.variables_discovered || []
  const parameterization = result?.parameterization
  const resultScripts = result?.scripts || []
  const safeParameterizationScriptIndex = Math.min(parameterizationScriptIndex, Math.max(0, resultScripts.length - 1))
  const parameterizationScript = resultScripts[safeParameterizationScriptIndex]
  const assetPageCount = Math.max(1, Math.ceil(resultScripts.length / JMETER_ASSET_PAGE_SIZE))
  const safeAssetPage = Math.min(assetPage, assetPageCount - 1)
  const assetPageStart = safeAssetPage * JMETER_ASSET_PAGE_SIZE
  const visibleAssets = resultScripts.slice(assetPageStart, assetPageStart + JMETER_ASSET_PAGE_SIZE)
  const extractorPageCount = Math.max(1, Math.ceil(extractorSummary.length / JMETER_EXTRACTOR_PAGE_SIZE))
  const safeExtractorPage = Math.min(extractorPage, extractorPageCount - 1)
  const extractorPageStart = safeExtractorPage * JMETER_EXTRACTOR_PAGE_SIZE
  const visibleExtractors = extractorSummary.slice(extractorPageStart, extractorPageStart + JMETER_EXTRACTOR_PAGE_SIZE)
  const editRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    result,
    primaryScript,
    result?.secret_binding_requirements,
  )
  const desktop = isDesktopSurface()
  const [browserPickerFallback, setBrowserPickerFallback] = useState(false)
  const nativeDialogs = desktop && canUseNativeDialogs() && !browserPickerFallback

  useEffect(() => {
    let cancelled = false

    api.getJMeterSupportMatrix()
      .then((response: JMeterSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(response.support_matrix || null)
          setSupportEntries(Array.isArray(response.support_matrix_entries) ? response.support_matrix_entries : [])
          setSupportNotes(Array.isArray(response.notes) ? response.notes : response.support_matrix?.notes || [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix(null)
          setSupportEntries([])
          setSupportNotes([])
        }
      })

    return () => {
      cancelled = true
    }
  }, [api])

  const acceptFile = useCallback((nextFile?: File): boolean => {
    if (sourceMutationLockedRef.current) return false
    if (!nextFile) return false
    const lowerName = nextFile.name.toLowerCase()
    if (!lowerName.endsWith('.jmx') && !lowerName.endsWith('.zip')) {
      setError('Please upload a JMeter .jmx test plan or a .zip archive containing JMX plans.')
      return false
    }
    invalidateReview()
    repairEvidenceRef.current = null
    setOptions(prev => ({ ...prev, variable_bindings: {} }))
    setFile(nextFile)
    setError('')
    setNotice('')
    return true
  }, [invalidateReview])

  useEffect(() => {
    if (initialFile) {
      acceptFile(initialFile)
    }
  }, [acceptFile, initialFile])

  const handleNativeJMeterPaths = useCallback((paths: string[]) => {
    beginIngest()
    void filesFromDroppedPaths(paths).then(files => {
      setDragOver(false)
      const picked = files.find(next => /\.(?:jmx|zip)$/i.test(next.name)) || files[0]
      if (acceptFile(picked)) succeedIngest(`Added ${picked.name}`)
      else failIngest()
    }).catch(error => {
      setDragOver(false)
      failIngest()
      setError(nativeFileReadErrorMessage(error))
      if (shouldUseBrowserFileFallback(error)) setBrowserPickerFallback(true)
    })
  }, [acceptFile, beginIngest, succeedIngest, failIngest])
  useDesktopFileDrop(dropzoneRef, handleNativeJMeterPaths, desktop)

  const handleBrowse = useCallback(() => {
    if (sourceMutationLockedRef.current) return
    if (nativeDialogs) {
      void pickFilesViaBridge('.jmx,.xml,.zip').then(files => {
        acceptFile(files.find(next => /\.(?:jmx|zip)$/i.test(next.name)) || files[0])
      }).catch(error => {
        setError(nativeFileReadErrorMessage(error))
        if (shouldUseBrowserFileFallback(error)) {
          setBrowserPickerFallback(true)
          fileRef.current?.click()
        }
      })
      return
    }
    fileRef.current?.click()
  }, [acceptFile, nativeDialogs])

  const handleDrop = (event: React.DragEvent) => {
    event.preventDefault()
    setDragOver(false)
    if (sourceMutationLocked) return
    if (desktop) return
    const dropped = event.dataTransfer.files[0]
    if (!dropped) return
    beginIngest()
    if (acceptFile(dropped)) succeedIngest(`Added ${dropped.name}`)
    else failIngest()
  }

  const handleImport = async () => {
    if (!file) return
    if (!projectId) {
      setError('Select a project in the workspace selector, then inspect this file again. Your source and options have been retained.')
      return
    }
    const sourceGeneration = advanceReviewGeneration()
    const sourceProjectId = reviewProjectIdRef.current
    const sourceFile = file
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedFileRef.current === sourceFile
    setWorkingMode('convert')
    setError('')
    setNotice('')
    setResult(null)
    setParameterizationScriptIndex(0)
    setAssetPage(0)
    setExtractorPage(0)
    try {
      const response = await api.importJMeter(file, {
        name: options.name || undefined,
        project_id: projectId,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        scenario_draft: options.scenario_draft,
        time_mode: options.time_mode,
        timezone: options.timezone,
        variable_bindings: Object.fromEntries(Object.entries(options.variable_bindings).filter(([, value]) => value)),
      })
      if (!requestIsCurrent()) {
        sourceSecretSessions.discardUnaccepted(sourceProjectId, response.source_secret_session_id)
        return
      }
      sourceSecretSessions.replace('review', sourceProjectId, response.source_secret_session_id)
      setResult(response)
      setReviewMode('conversion')
      setNotice('Conversion complete. Review the generated scripts, warnings, and assumptions before saving.')
    } catch (error: unknown) {
      if (requestIsCurrent()) {
        sourceSecretSessions.clear('review')
        setError(getErrorMessage(error, 'JMeter import failed'))
      }
    } finally {
      if (requestIsCurrent()) setWorkingMode(null)
    }
  }

  const handleValidate = async () => {
    if (!file) return
    if (!projectId) {
      setError('Select a project in the workspace selector, then inspect this file again. Your source and options have been retained.')
      return
    }
    const sourceGeneration = advanceReviewGeneration()
    const sourceProjectId = reviewProjectIdRef.current
    const sourceFile = file
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedFileRef.current === sourceFile
    setWorkingMode('validate')
    setError('')
    setNotice('')
    setResult(null)
    setParameterizationScriptIndex(0)
    setAssetPage(0)
    setExtractorPage(0)
    try {
      const response = await api.validateJMeterImport(file, {
        name: options.name || undefined,
        project_id: projectId,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        scenario_draft: options.scenario_draft,
        time_mode: options.time_mode,
        timezone: options.timezone,
        variable_bindings: Object.fromEntries(Object.entries(options.variable_bindings).filter(([, value]) => value)),
      })
      if (!requestIsCurrent()) {
        sourceSecretSessions.discardUnaccepted(sourceProjectId, response.source_secret_session_id)
        return
      }
      sourceSecretSessions.replace('review', sourceProjectId, response.source_secret_session_id)
      setResult(response)
      setReviewMode('validation')
      setNotice('Validation complete. Review the generated scripts and warnings before saving.')
    } catch (error: unknown) {
      if (requestIsCurrent()) {
        sourceSecretSessions.clear('review')
        setError(getErrorMessage(error, 'JMeter validation failed'))
      }
    } finally {
      if (requestIsCurrent()) setWorkingMode(null)
    }
  }

  const handleParameterizationApplied = (scriptIndex: number) => (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
    refreshedRequirements?: ImportResult['secret_binding_requirements'],
  ) => {
    advanceReviewGeneration()
    setResult(prev => {
      if (!prev) return prev
      const scripts = (prev.scripts || []).map((script, index) => (index === scriptIndex ? parameterizedScript : script))
      const primary = scriptIndex === 0 ? parameterizedScript : prev.script
      return {
        ...prev,
        ...(refreshedRequirements ? { secret_binding_requirements: replaceSecretBindingRequirementsForScript(prev.secret_binding_requirements, scriptIndex, parameterizedScript, refreshedRequirements.map(requirement => ({ ...requirement, script_index: scriptIndex }))) } : {}),
        scripts,
        script: primary,
        ...(scripts.length === 1 ? { import_draft_expires_at: importDraftExpiresAt } : {}),
      }
    })
  }

  const handleSharedValuesApplied = (
    updatedScripts: ScriptDefinition[],
    _diffs: unknown[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => {
    if (updatedScripts.length === 0) return
    advanceReviewGeneration()
    const rotatedExpiry = earliestImportDraftExpiry(importDraftExpiresAt)
    setResult(prev => {
      if (!prev) return prev
      const next = {
        ...prev,
        script: updatedScripts[0],
        scripts: updatedScripts,
        ...(refreshed ? { shared_values: refreshed } : {}),
        ...(rotatedExpiry ? { import_draft_expires_at: rotatedExpiry } : {}),
      }
      latestResultRef.current = next
      return next
    })
    setNotice('Collection values updated every generated script. Review the changes before saving.')
  }

  const handleSaveConverted = async (destination: ImportSaveDestination = 'stay') => {
    if (savedResult) return
    const publish = (result: ImportedSaveResult) => publishSaved(result, destination)
    const resultToSave = latestResultRef.current
    if (!resultToSave?.scripts?.length || saveInFlightRef.current) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceFile = selectedFileRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedFileRef.current === sourceFile
      && latestResultRef.current === resultToSave
    const saveScope = { result: resultToSave, projectId: sourceProjectId, file: sourceFile }
    saveInFlightRef.current = saveScope
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const scripts = resultToSave.scripts.map(script => ({
        ...script,
        project_id: script.project_id || sourceProjectId,
        folder_id: folderId ?? null,
      }))
      const response = await api.saveJMeterImport({
        scripts,
        scenario_draft: resultToSave.scenario_draft,
      })
      const savedScripts = withFallbackPersistedScripts(response.scripts, resultToSave.scripts)
      const scenarioText = response.scenario ? ' and scenario draft' : ''
      if (savedScripts.length) {
        publish({
          scripts: savedScripts,
          scenario: response.scenario,
          ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
        })
      }
      if (!requestIsCurrent()) return
      setNotice(`Saved ${response.count || resultToSave.scripts.length} script${resultToSave.scripts.length === 1 ? '' : 's'}${scenarioText} to the library.`)
    } catch (error: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(error, 'Failed to save converted scripts'))
    } finally {
      if (saveInFlightRef.current === saveScope) {
        saveInFlightRef.current = null
        setSaving(false)
      }
    }
  }

  const handleEditConverted = () => {
    const currentResult = latestResultRef.current
    const currentScript = getPrimaryScript(currentResult)
    if (!currentScript) return
    if (credentialPreservingImportDraftRequiresSave(
      currentResult,
      currentScript,
      currentResult?.secret_binding_requirements,
    )) {
      if (!onImported) {
        setError('Save this credential-preserving import before opening it in the Visual Editor.')
        return
      }
      void handleSaveConverted('editor')
      return
    }
    onEdit?.(currentScript)
  }

  const applyScenarioRuntimeRule = (scenarioDraft: NonNullable<ImportResult['scenario_draft']>) => {
    setResult(previous => previous ? { ...previous, scenario_draft: scenarioDraft } : previous)
    setNotice('Runtime settings were updated. Existing review findings remain until server-side revalidation resolves them.')
  }

  const handleSecretBindingsApply = async (bindings: SecretBindingSelection[]) => {
    if (!result) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration && reviewProjectIdRef.current === sourceProjectId
    const assertCurrent = () => {
      if (!requestIsCurrent()) throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
    }
    let resolvedResult = result
    for (let scriptIndex = 0; scriptIndex < resolvedResult.scripts.length; scriptIndex += 1) {
      const script = resolvedResult.scripts[scriptIndex]
      const requirements = secretBindingRequirementsForScript(resolvedResult.secret_binding_requirements, scriptIndex, script)
      const requirementIDs = new Set(requirements.map(requirement => requirement.id))
      const scriptBindings = bindings
        .filter(binding => (
          typeof binding.script_index === 'number'
            ? binding.script_index === scriptIndex
            : requirementIDs.has(binding.requirement_id)
        ))
        .map(({ requirement_id, secret_name }) => ({ requirement_id, secret_name }))
      if (scriptBindings.length === 0) continue
      try {
        assertCurrent()
        const resolved = await api.applySecretBindings({
          script,
          script_index: scriptIndex,
          bindings: scriptBindings,
        })
        assertCurrent()
        resolvedResult = {
          ...resolvedResult,
          scripts: resolvedResult.scripts.map((entry, index) => index === scriptIndex ? resolved.script : entry),
          script: scriptIndex === 0 ? resolved.script : resolvedResult.script,
          ...(resolvedResult.scripts.length === 1
            ? { import_draft_expires_at: resolved.import_draft_expires_at }
            : {}),
          secret_binding_requirements: replaceSecretBindingRequirementsForScript(
            resolvedResult.secret_binding_requirements,
            scriptIndex,
            script,
            resolved.secret_binding_requirements,
          ),
        }
      } catch (error: unknown) {
        if (!requestIsCurrent()) throw error
        const refreshed = refreshedSecretBindingRequirements(error)
        if (refreshed) {
          resolvedResult = {
            ...resolvedResult,
            secret_binding_requirements: replaceSecretBindingRequirementsForScript(
              resolvedResult.secret_binding_requirements,
              scriptIndex,
              script,
              refreshed,
            ),
          }
          latestResultRef.current = resolvedResult
          setResult(resolvedResult)
        }
        throw error
      }
    }
    assertCurrent()
    latestResultRef.current = resolvedResult
    setResult(resolvedResult)
  }

  return (
    <div className="lr-import">
      <ImportSaveConfirmation result={savedResult} onImportMore={handleBrowse} onOpen={openSaved} disabled={sourceMutationLocked} />
      <div className="lr-import-header">
        <div>
          <h2>Import JMeter Test Plan</h2>
          <p className="lr-subtitle">
            Upload a <code>.jmx</code> plan or a ZIP containing multiple JMX plans and convert their thread groups into APEX Load reusable scripts. The importer preserves variables, CSV data sources, headers, timers, and review notes where possible.
          </p>
        </div>
        {onClose && <button type="button" className="btn btn-secondary" onClick={() => {
          invalidateReview()
          onClose()
        }}>Back</button>}
      </div>


      <ImportWorkflowGuide stage={savedResult ? 'saved' : result ? 'review' : 'source'} />
      <details className="import-source-setup" open={!result}><summary>Source & conversion options — changing these requires a new review</summary>
      <div
        ref={dropzoneRef}
        className={`lr-dropzone ${ingestHostClass} ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
        style={desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined}
        onDragOver={event => { event.preventDefault(); if (!sourceMutationLocked) setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={sourceMutationLocked ? undefined : handleBrowse}
        role="button"
        tabIndex={sourceMutationLocked ? -1 : 0}
        aria-disabled={sourceMutationLocked}
        aria-label={file ? `Selected JMeter plan ${file.name}. Choose another file` : 'Choose a JMeter test plan'}
        onKeyDown={event => {
          if (sourceMutationLocked || event.currentTarget !== event.target) return
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            handleBrowse()
          }
        }}
      >
        <DropIngestOverlay phase={ingestPhase} label={ingestLabel} />
        <input ref={fileRef} type="file" accept=".jmx,.xml,.zip,application/zip" disabled={sourceMutationLocked} onClick={event => event.stopPropagation()} onChange={event => { acceptFile(event.target.files?.[0]); event.target.value = '' }} hidden />
        {file ? (
          <div className="lr-file-info">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div>
              <span className="lr-filename">{file.name}</span>
              <span className="lr-filesize">{(file.size / 1024).toFixed(1)} KB</span>
            </div>
            <button className="btn btn-sm btn-ghost" disabled={sourceMutationLocked} onClick={event => { event.stopPropagation(); invalidateReview(); setFile(null); if (fileRef.current) fileRef.current.value = '' }}>Change</button>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop a JMeter <code>.jmx</code> file or multi-plan <code>.zip</code> archive here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      <div className="lr-options">
        <h3 className="lr-section-title">Conversion Options</h3>
        <div className="lr-options-grid">
          <label><span>Script Name Override</span>
            <input value={options.name} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, name: event.target.value })) }} placeholder="Default: thread group name" />
          </label>
          <label><span>Base URL Override</span>
            <input value={options.base_url} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, base_url: event.target.value })) }} placeholder="Default: HTTP Request Defaults or first sampler" />
          </label>
          <label><span>Timestamp policy</span>
            <select aria-label="Timestamp policy" value={options.time_mode} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, time_mode: event.target.value })) }}>
              <option value="">Require review for ambiguous timestamps</option>
              <option value="utc">Normalize to UTC with milliseconds</option>
              <option value="source">Preserve wall clock and acknowledge literal Z suffix</option>
            </select>
          </label>
          {options.time_mode === 'source' && <label><span>Source timezone (IANA)</span><input aria-label="Source timezone" value={options.timezone} placeholder="America/New_York" disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, timezone: event.target.value })) }} /></label>}
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.include_assertions} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, include_assertions: event.target.checked })) }} />
            <span>Add default status_code assertions</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.scenario_draft} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, scenario_draft: event.target.checked })) }} />
            <span>Generate scenario draft from thread groups</span>
          </label>
        </div>
      </div>

      <div className="settings-notice">
        HTTP plans convert most completely. Homogeneous JDBC, FTP, and TCP thread groups now convert into native database/network scripts, safe JSR223/BeanShell statements are translated, and anything that needs a provider/adapter (such as JMS) or manual review is classified rather than dropped.
      </div>

      {(unresolvedVariables.length > 0 || Object.keys(options.variable_bindings).length > 0) && <div className="settings-notice">
        <datalist id="jmeter-declared-variables">{declaredVariables.map(name => <option key={name} value={name} />)}</datalist><h4>Variable name repairs</h4><p>Declared source names: {declaredVariables.join(', ') || 'none'}. Select each binding explicitly, then inspect again to regenerate the server-validated draft. Masked values still require a usable secret binding.</p>
        {[...new Set([...unresolvedVariables, ...Object.keys(options.variable_bindings)])].map(name => <label key={name}><span>Unresolved reference: {name}</span><input list="jmeter-declared-variables" placeholder="Declared name or {{secret.NAME}}" aria-label={`Bind ${name}`} value={options.variable_bindings[name] || ''} disabled={sourceMutationLocked} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, variable_bindings: { ...prev.variable_bindings, [name]: event.target.value } })) }} />{options.variable_bindings[name] && <p>Before: {'${' + name + '}'} → After: {options.variable_bindings[name].startsWith('{{secret.') ? options.variable_bindings[name] : '${' + options.variable_bindings[name] + '}'}</p>}</label>)}
      </div>}
      <div className="import-workflow-actions">
        <details><summary>Advanced validation only</summary>        <button className="btn btn-secondary btn-lg" onClick={handleValidate} disabled={!file || workingMode !== null || saving}>
          {workingMode === 'validate' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
        </button></details>
        <button className="btn btn-primary btn-lg" onClick={handleImport} disabled={!file || workingMode !== null || saving}>
          {workingMode === 'convert' ? <><span className="spinner" /> Converting...</> : 'Inspect & review'}
        </button>
      </div>

      </details>
      {error && <div className="lr-error" role="alert">{error}</div>}
      {notice && <div className="settings-notice">{notice}</div>}

      {coverageSections.length > 0 ? (
        <ImportCoverageButton
          title="Import Coverage"
          subtitle="Mapped, review-required, adapter-required, and unsupported constructs"
        >
          <div className="lr-preserved-grid">
            {coverageSections.map(section => (
              <div className={`lr-preserved-card coverage-${section.key}`} key={section.key}>
                <span className="lr-preserved-label">{section.title}</span>
                <strong>{section.items.length} {section.items.length === 1 ? 'construct' : 'constructs'}</strong>
                <small>{section.preview}</small>
              </div>
            ))}
          </div>
          <div className="lr-warnings">
            <h4>Coverage Details</h4>
            {coverageSections.map(section => (
              <details key={section.key} data-coverage={section.key}>
                <summary>{section.title} ({section.items.length})</summary>
                <p className="coverage-hint">{section.hint}</p>
                <ul>
                  {section.items.map(item => (
                    <li key={item.id}>
                      <strong>{item.construct}</strong>
                      {item.notes ? <> — {item.notes}</> : null}
                    </li>
                  ))}
                </ul>
              </details>
            ))}
          </div>
          {supportNotes.length > 0 && (
            <div className="lr-warnings">
              <h4>Importer Notes</h4>
              <ul>{supportNotes.map((note, index) => <li key={index}>{note}</li>)}</ul>
            </div>
          )}
        </ImportCoverageButton>
      ) : supportMatrix && (
        <ImportCoverageButton
          title="Import Coverage"
          subtitle="HTTP-first migration with partial SOAP promotion"
        >
          <div className="lr-preserved-grid">
            {supportMatrixSections.map(section => (
              <div className="lr-preserved-card" key={section.key}>
                <span className="lr-preserved-label">{section.title}</span>
                <strong>{section.items.length} {section.itemLabel}</strong>
                <small>{section.preview}</small>
              </div>
            ))}
          </div>
          <div className="lr-warnings">
            <h4>Coverage Details</h4>
            {supportMatrixSections.map(section => (
              <details key={section.key}>
                <summary>{section.title} ({section.items.length})</summary>
                <ul>{section.items.map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>
              </details>
            ))}
          </div>
          {supportNotes.length > 0 && (
            <div className="lr-warnings">
              <h4>Importer Notes</h4>
              <ul>{supportNotes.map((note, index) => <li key={index}>{note}</li>)}</ul>
            </div>
          )}
        </ImportCoverageButton>
      )}




      {result && (
        <div className="lr-result">
          {result.batch && <JMeterBatchRepairs key={result.batch.revision} result={result} projectId={projectId || result.scripts[0]?.project_id || ''} disabled={operationBusy || Boolean(savedResult)} onBusyChange={setCredentialActionInProgress} onApplied={updated => {
            advanceReviewGeneration()
            setResult(previous => previous ? { ...previous, diagnostics: updated.diagnostics, review_summary: updated.review_summary, warnings: updated.warnings || [], unsupported_elements: updated.unsupported_elements || [], preflight: updated.preflight, batch: updated.batch, scripts: updated.scripts, script: updated.scripts[0], scenario_draft: updated.scenario_draft, secret_binding_requirements: updated.secret_binding_requirements || [] } : previous)
            setNotice('The complete import was updated. Remaining replay blockers are still enforced.')
          }} />}

          <ScriptImportReview
            tabbed
            onSettingsBusyChange={setSettingsEditing}
            onSettingsApplied={updated => handleParameterizationApplied(0)(updated.script, [], [], updated.import_draft_expires_at, updated.secret_binding_requirements || [])}
            settingsDisabled={Boolean(savedResult) || Boolean(result.batch)}
            showCredentials={!result.batch}
            dataContent={!result.batch && (parameterization?.suggestions.length || extractorSummary.length) ? <>
            {parameterization && parameterization.suggestions.length > 0 && parameterizationScript && (
              <>
                {resultScripts.length > 1 && (
                  <nav className="lr-pagination" aria-label="JMeter script parameterization selector">
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      aria-label="Review parameterization for previous generated script"
                      disabled={saving || workingMode !== null || parameterizationApplying || credentialActionInProgress || safeParameterizationScriptIndex === 0}
                      onClick={() => setParameterizationScriptIndex(index => Math.max(0, index - 1))}
                    >
                      Previous script
                    </button>
                    <span role="status" aria-live="polite">
                      Script {safeParameterizationScriptIndex + 1} of {resultScripts.length}: {parameterizationScript.name}
                    </span>
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      aria-label="Review parameterization for next generated script"
                      disabled={saving || workingMode !== null || parameterizationApplying || credentialActionInProgress || safeParameterizationScriptIndex >= resultScripts.length - 1}
                      onClick={() => setParameterizationScriptIndex(index => Math.min(resultScripts.length - 1, index + 1))}
                    >
                      Next script
                    </button>
                  </nav>
                )}
                <ParameterizationReview
                  key={`paramize-${safeParameterizationScriptIndex}-${parameterizationScript.name}`}
                  parameterization={parameterization}
                  script={parameterizationScript}
                  scriptName={parameterizationScript.name}
                  reviewGeneration={reviewScope}
                  disabled={saving || workingMode !== null || parameterizationApplying || credentialActionInProgress}
                  onApplyingChange={applying => setParameterizationApplyingByScript(current => ({ ...current, [safeParameterizationScriptIndex]: applying }))}
                  onApplied={handleParameterizationApplied(safeParameterizationScriptIndex)}
                />
              </>
            )}

            {extractorSummary.length > 0 && (
              <div className="lr-warnings">
                <h4>Captured Variables ({extractorSummary.length})</h4>
                {extractorSummary.length > JMETER_EXTRACTOR_PAGE_SIZE && (
                  <nav className="lr-pagination" aria-label="Captured variable pages">
                    <button type="button" className="btn btn-secondary btn-sm" disabled={safeExtractorPage === 0} onClick={() => setExtractorPage(page => Math.max(0, page - 1))}>Previous variables</button>
                    <span role="status" aria-live="polite">Showing {extractorPageStart + 1}–{Math.min(extractorPageStart + JMETER_EXTRACTOR_PAGE_SIZE, extractorSummary.length)} of {extractorSummary.length}</span>
                    <button type="button" className="btn btn-secondary btn-sm" disabled={safeExtractorPage >= extractorPageCount - 1} onClick={() => setExtractorPage(page => Math.min(extractorPageCount - 1, page + 1))}>Next variables</button>
                  </nav>
                )}
                <ul>
                  {visibleExtractors.map((extractor, index) => (
                    <li key={`${extractor.scriptName}-${extractor.actionName}-${extractor.extractorName}-${extractorPageStart + index}`}>
                      <strong>{extractor.extractorName}</strong> via {extractor.extractorType} from {extractor.actionName} in {extractor.scriptName}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            </> : undefined}
            runtimeContent={<JMeterScenarioRuntimeEditor scenario={result.scenario_draft} onApply={applyScenarioRuntimeRule} disabled={operationBusy || Boolean(savedResult) || Boolean(result.batch)} />}
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Review'}
            reviewGeneration={reviewScope}
            subtitle={savedResult ? 'Saved to the Script Library. Open a saved script above or import more files.' : reviewMode === 'validation'
              ? 'This preview came from the dry-run validator. You can still save the reviewed assets if the output looks correct.'
              : 'This preview came from the converter and is ready for final review before saving.'}
            script={primaryScript || buildFallbackPrimaryScript(result.summary?.file_name || file?.name || 'Imported JMeter Plan')}
            previewData={{ scripts: result.scripts, scenario_draft: result.scenario_draft, summary: result.summary }}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            unsupportedElements={result.unsupported_elements}
            diagnostics={result.diagnostics}
            reviewSummary={result.review_summary}
            reviewSections={result.review_sections}
            secretBindingRequirements={result.secret_binding_requirements}
            onSecretBindingsApply={handleSecretBindingsApply}
            sharedValues={result.batch ? undefined : result.shared_values}
            sharedValueScripts={result.scripts}
            onSharedValuesApplied={handleSharedValuesApplied}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            importDraftExpiresAt={result.import_draft_expires_at}
            credentialReviewRef={credentialReviewRef}
            saving={saving}
            reviewBusy={parameterizationApplying}
            onReviewBusyChange={setCredentialActionInProgress}
            saveDisabled={Boolean(savedResult)}
            saveDisabledReason={savedResult ? 'These scripts are already saved. Open them above or import more files.' : undefined}
            saveLabel={savedResult ? 'Saved to Script Library' : `Save ${result.scripts.length} draft${result.scripts.length === 1 ? '' : 's'}`}
            onSave={handleSaveConverted}
            onEdit={onEdit && primaryScript && result.scripts.length === 1 && !savedResult ? handleEditConverted : undefined}
            editLabel={editRequiresCredentialPreservingSave ? 'Save and Open in Visual Editor' : 'Open Draft in Visual Editor'}
            previewTitle="Import Preview"
            previewAside={sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={primaryScript} title="Source Comparison" /> : undefined}
          >


            {result.assumptions.length > 0 && (
              <div className="lr-warnings">
                <h4>Assumptions</h4>
                <ul>{result.assumptions.map((item: string, index: number) => <li key={index}>{item}</li>)}</ul>
              </div>
            )}

            <div className="lr-preserved">
              <div className="lr-preserved-header">
                <h4>Generated Assets</h4>
                <span>{result.summary?.file_name || file?.name}</span>
              </div>
              {resultScripts.length > JMETER_ASSET_PAGE_SIZE && (
                <nav className="lr-pagination" aria-label="Generated JMeter asset pages">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={safeAssetPage === 0} onClick={() => setAssetPage(page => Math.max(0, page - 1))}>Previous assets</button>
                  <span role="status" aria-live="polite">Showing {assetPageStart + 1}–{Math.min(assetPageStart + JMETER_ASSET_PAGE_SIZE, resultScripts.length)} of {resultScripts.length}</span>
                  <button type="button" className="btn btn-secondary btn-sm" disabled={safeAssetPage >= assetPageCount - 1} onClick={() => setAssetPage(page => Math.min(assetPageCount - 1, page + 1))}>Next assets</button>
                </nav>
              )}
              <div className="lr-preserved-grid">
                {visibleAssets.map((script: ScriptDefinition, scriptIndex) => (
                  <div className="lr-preserved-card" key={`${assetPageStart + scriptIndex}-${script.name}`}>
                    <span className="lr-preserved-label">Script</span>
                    <strong>{script.name}</strong>
                    <small>{script.actions?.length || 0} actions / {script.data_sources?.length || 0} data sources / {script.actions?.reduce((count, action) => count + (action.extractors?.length || 0), 0) || 0} extractors</small>
                  </div>
                ))}
                {result.scenario_draft && (
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Scenario Draft</span>
                    <strong>{result.scenario_draft.groups?.length || 0} groups</strong>
                    <small>{result.scenario_draft.termination_mode === 'iterations' ? 'Iteration completion' : result.scenario_draft.duration || 'Duration not set'}</small>
                  </div>
                )}
              </div>
            </div>
            <JMeterMessagingPreview result={result} />
            <ImportConversionDetails facts={[
              { label: 'Actions', value: result.stats.converted_actions },
              { label: 'Converted Samplers', value: result.stats.samplers },
              { label: 'Total Samplers', value: result.stats.total_samplers ?? result.stats.samplers },
              { label: 'Skipped Samplers', value: result.stats.skipped_samplers ?? 0 },
              { label: 'Thread Groups', value: result.stats.thread_groups },
              { label: 'CSV Sources', value: result.stats.csv_sources },
              { label: 'Variables', value: result.stats.variables },
              { label: 'Assertions', value: result.stats.assertions },
              { label: 'Unsupported', value: result.stats.unsupported }
            ]} />
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
