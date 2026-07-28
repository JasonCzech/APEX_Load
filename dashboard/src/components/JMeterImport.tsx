import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { summarizeImportExtractors, summarizeSupportMatrix, summarizeSupportMatrixEntries } from './jmeterImportUtils'
import { type JMeterImportResult as ImportResult, type JMeterSupportMatrixEntry, type JMeterSupportMatrixResponse, type SecretBindingSelection, type SupportMatrix } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { canUseNativeDialogs, filesFromDroppedPaths, nativeFileReadErrorMessage, pickFilesViaBridge, shouldUseBrowserFileFallback } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import DropIngestOverlay from './DropIngestOverlay'
import { useDropIngestFeedback } from './dropIngestFeedback'
import ParameterizationReview from './ParameterizationReview'
import ScriptImportReview from './ScriptImportReview'
import { type SecretBindingReviewHandle } from './SecretBindingReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScripts } from './importSaveUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import { refreshedSecretBindingRequirements, replaceSecretBindingRequirementsForScript, secretBindingRequirementsForScript } from './secretBindingUtils'
import { credentialPreservingImportDraftRequiresSave } from './protectedImportSession'
import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'
import './EnterpriseArchiveImport.css'

interface Props {
  initialFile?: File | null
  projectId?: string
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'

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

export default function JMeterImport({ initialFile, projectId, onImported, onEdit, onClose }: Props) {
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
  const reviewGenerationRef = useRef(0)
  const [reviewGeneration, setReviewGeneration] = useState(0)
  const advanceReviewGeneration = useCallback(() => {
    const next = reviewGenerationRef.current + 1
    reviewGenerationRef.current = next
    setReviewGeneration(next)
    return next
  }, [])
  const reviewScope = `${reviewGeneration}:${projectId || ''}`
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  const [parameterizationApplyingByScript, setParameterizationApplyingByScript] = useState<Record<string, boolean>>({})
  const parameterizationApplying = Object.values(parameterizationApplyingByScript).some(Boolean)
  const [credentialActionInProgress, setCredentialActionInProgress] = useState(false)
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const invalidateReview = useCallback(() => {
    sourceSecretSessions.clear('review')
    advanceReviewGeneration()
    setWorkingMode(null)
    setSaving(false)
    setResult(null)
    setReviewMode(null)
    setNotice('')
    setParameterizationApplyingByScript({})
    setCredentialActionInProgress(false)
  }, [advanceReviewGeneration, sourceSecretSessions])
  const [options, setOptions] = useState({
    name: '',
    base_url: '',
    include_assertions: true,
    scenario_draft: true,
  })
  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    invalidateReview()
  }, [invalidateReview, projectId])
  const supportMatrixSections = summarizeSupportMatrix(supportMatrix)
  const coverageSections = summarizeSupportMatrixEntries(supportEntries)
  const extractorSummary = summarizeImportExtractors(result?.scripts || [])
  const primaryScript = getPrimaryScript(result)
  const sourceReview = sourceReviewForJMeter(result, primaryScript)
  const parameterization = result?.parameterization
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
    if (!nextFile) return false
    if (!nextFile.name.toLowerCase().endsWith('.jmx')) {
      setError('Please upload a JMeter .jmx test plan.')
      return false
    }
    invalidateReview()
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
      const picked = files.find(next => next.name.toLowerCase().endsWith('.jmx')) || files[0]
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
    if (nativeDialogs) {
      void pickFilesViaBridge('.jmx,.xml').then(files => {
        acceptFile(files.find(next => next.name.toLowerCase().endsWith('.jmx')) || files[0])
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
    if (desktop) return
    const dropped = event.dataTransfer.files[0]
    if (!dropped) return
    beginIngest()
    if (acceptFile(dropped)) succeedIngest(`Added ${dropped.name}`)
    else failIngest()
  }

  const handleImport = async () => {
    if (!file) return
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
    try {
      const response = await api.importJMeter(file, {
        name: options.name || undefined,
        project_id: projectId,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        scenario_draft: options.scenario_draft,
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
    try {
      const response = await api.validateJMeterImport(file, {
        name: options.name || undefined,
        project_id: projectId,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        scenario_draft: options.scenario_draft,
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
  ) => {
    credentialReviewRef.current?.preserveEditsForNextReviewGeneration()
    advanceReviewGeneration()
    setSaving(false)
    setResult(prev => {
      if (!prev) return prev
      const scripts = (prev.scripts || []).map((script, index) => (index === scriptIndex ? parameterizedScript : script))
      const primary = scriptIndex === 0 ? parameterizedScript : prev.script
      return {
        ...prev,
        scripts,
        script: primary,
        ...(scripts.length === 1 ? { import_draft_expires_at: importDraftExpiresAt } : {}),
      }
    })
  }

  const handleSaveConverted = async () => {
    const resultToSave = latestResultRef.current
    if (!resultToSave?.scripts?.length) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceFile = selectedFileRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedFileRef.current === sourceFile
      && latestResultRef.current === resultToSave
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const scripts = resultToSave.scripts.map(script => script.project_id ? script : { ...script, project_id: sourceProjectId })
      const response = await api.saveJMeterImport({
        scripts,
        scenario_draft: resultToSave.scenario_draft,
      })
      const savedScripts = withFallbackPersistedScripts(response.scripts, resultToSave.scripts)
      const scenarioText = response.scenario ? ' and scenario draft' : ''
      if (savedScripts.length) {
        onImported?.({
          scripts: savedScripts,
          scenario: response.scenario,
        })
      }
      if (!requestIsCurrent()) return
      setNotice(`Saved ${response.count || resultToSave.scripts.length} script${resultToSave.scripts.length === 1 ? '' : 's'}${scenarioText} to the library.`)
    } catch (error: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(error, 'Failed to save converted scripts'))
    } finally {
      if (requestIsCurrent()) setSaving(false)
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
      void handleSaveConverted()
      return
    }
    onEdit?.(currentScript)
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
      <div className="lr-import-header">
        <div>
          <h2>Import JMeter Test Plan</h2>
          <p className="lr-subtitle">
            Upload a <code>.jmx</code> plan and convert HTTP thread groups into APEX Load reusable scripts. The importer preserves variables, CSV data sources, headers, timers, and review notes where possible.
          </p>
        </div>
        {onClose && <button type="button" className="btn btn-secondary" onClick={() => { sourceSecretSessions.clear('review'); onClose() }}>Back</button>}
      </div>

      <div
        ref={dropzoneRef}
        className={`lr-dropzone ${ingestHostClass} ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
        style={desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined}
        onDragOver={event => { event.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={handleBrowse}
        role="button"
        tabIndex={0}
        aria-label={file ? `Selected JMeter plan ${file.name}. Choose another file` : 'Choose a JMeter test plan'}
        onKeyDown={event => {
          if (event.currentTarget !== event.target) return
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            handleBrowse()
          }
        }}
      >
        <DropIngestOverlay phase={ingestPhase} label={ingestLabel} />
        <input ref={fileRef} type="file" accept=".jmx,.xml" onClick={event => event.stopPropagation()} onChange={event => acceptFile(event.target.files?.[0])} hidden />
        {file ? (
          <div className="lr-file-info">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div>
              <span className="lr-filename">{file.name}</span>
              <span className="lr-filesize">{(file.size / 1024).toFixed(1)} KB</span>
            </div>
            <button className="btn btn-sm btn-ghost" onClick={event => { event.stopPropagation(); invalidateReview(); setFile(null) }}>Change</button>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop a JMeter <code>.jmx</code> file here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      <div className="lr-options">
        <h3 className="lr-section-title">Conversion Options</h3>
        <div className="lr-options-grid">
          <label><span>Script Name Override</span>
            <input value={options.name} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, name: event.target.value })) }} placeholder="Default: thread group name" />
          </label>
          <label><span>Base URL Override</span>
            <input value={options.base_url} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, base_url: event.target.value })) }} placeholder="Default: HTTP Request Defaults or first sampler" />
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.include_assertions} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, include_assertions: event.target.checked })) }} />
            <span>Add default status_code assertions</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.scenario_draft} onChange={event => { invalidateReview(); setOptions(prev => ({ ...prev, scenario_draft: event.target.checked })) }} />
            <span>Generate scenario draft from thread groups</span>
          </label>
        </div>
      </div>

      <div className="settings-notice">
        HTTP plans convert most completely. Homogeneous JDBC, FTP, and TCP thread groups now convert into native database/network scripts, safe JSR223/BeanShell statements are translated, and anything that needs a provider/adapter (such as JMS) or manual review is classified rather than dropped.
      </div>

      {coverageSections.length > 0 ? (
        <div className="lr-preserved">
          <div className="lr-preserved-header">
            <h4>Import Coverage</h4>
            <span>Mapped, review-required, adapter-required, and unsupported constructs</span>
          </div>
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
        </div>
      ) : supportMatrix && (
        <div className="lr-preserved">
          <div className="lr-preserved-header">
            <h4>Import Coverage</h4>
            <span>HTTP-first migration with partial SOAP promotion</span>
          </div>
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
        </div>
      )}

      <div className="replay-panel-actions">
        <button className="btn btn-secondary btn-lg" onClick={handleValidate} disabled={!file || workingMode !== null}>
          {workingMode === 'validate' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
        </button>
        <button className="btn btn-primary btn-lg" onClick={handleImport} disabled={!file || workingMode !== null}>
          {workingMode === 'convert' ? <><span className="spinner" /> Converting...</> : 'Convert JMeter Plan'}
        </button>
      </div>

      {error && <div className="lr-error" role="alert">{error}</div>}
      {notice && <div className="settings-notice">{notice}</div>}

      {result && (
        <div className="lr-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Review'}
            reviewGeneration={reviewScope}
            subtitle={reviewMode === 'validation'
              ? 'This preview came from the dry-run validator. You can still save the reviewed assets if the output looks correct.'
              : 'This preview came from the converter and is ready for final review before saving.'}
            script={primaryScript || buildFallbackPrimaryScript(result.summary?.file_name || file?.name || 'Imported JMeter Plan')}
            previewData={{ scripts: result.scripts, scenario_draft: result.scenario_draft, summary: result.summary }}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            unsupportedElements={result.unsupported_elements}
            reviewSections={result.review_sections}
            secretBindingRequirements={result.secret_binding_requirements}
            onSecretBindingsApply={handleSecretBindingsApply}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            importDraftExpiresAt={result.import_draft_expires_at}
            credentialReviewRef={credentialReviewRef}
            saving={saving}
            reviewBusy={parameterizationApplying}
            onReviewBusyChange={setCredentialActionInProgress}
            saveLabel={`${reviewMode === 'validation' ? 'Save Reviewed' : 'Save'} ${result.scripts?.length || 0} Script${(result.scripts?.length || 0) === 1 ? '' : 's'}`}
            onSave={handleSaveConverted}
            onEdit={onEdit && primaryScript ? handleEditConverted : undefined}
            editLabel={editRequiresCredentialPreservingSave
              ? (result.scripts.length > 1 ? 'Save All And Open First Script In Visual Editor' : 'Save And Open In Visual Editor')
              : (result.scripts.length > 1 ? 'Open First Script In Visual Editor' : 'Open In Visual Editor')}
            previewTitle="Import Preview"
            previewAside={sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={primaryScript} title="Source Comparison" /> : undefined}
          >
            <div className="lr-stats-grid">
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.samplers}</span><span className="lr-stat-label">HTTP Samplers</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.total_samplers ?? result.stats.samplers}</span><span className="lr-stat-label">Total Samplers</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.skipped_samplers ?? 0}</span><span className="lr-stat-label">Skipped Samplers</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.thread_groups}</span><span className="lr-stat-label">Thread Groups</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.csv_sources}</span><span className="lr-stat-label">CSV Sources</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.variables}</span><span className="lr-stat-label">Variables</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.assertions}</span><span className="lr-stat-label">Assertions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.unsupported}</span><span className="lr-stat-label">Unsupported</span></div>
            </div>

            {result.assumptions.length > 0 && (
              <div className="lr-warnings">
                <h4>Assumptions</h4>
                <ul>{result.assumptions.map((item: string, index: number) => <li key={index}>{item}</li>)}</ul>
              </div>
            )}

            {parameterization && parameterization.suggestions.length > 0 && (result.scripts || []).map((script, scriptIndex) => (
              <ParameterizationReview
                key={`paramize-${scriptIndex}-${script.name}`}
                parameterization={parameterization}
                script={script}
                scriptName={script.name}
                reviewGeneration={reviewScope}
                disabled={saving || workingMode !== null || parameterizationApplying || credentialActionInProgress}
                onApplyingChange={applying => setParameterizationApplyingByScript(current => ({ ...current, [scriptIndex]: applying }))}
                onApplied={handleParameterizationApplied(scriptIndex)}
              />
            ))}

            {extractorSummary.length > 0 && (
              <div className="lr-warnings">
                <h4>Captured Variables ({extractorSummary.length})</h4>
                <ul>
                  {extractorSummary.map((extractor, index) => (
                    <li key={`${extractor.scriptName}-${extractor.actionName}-${extractor.extractorName}-${index}`}>
                      <strong>{extractor.extractorName}</strong> via {extractor.extractorType} from {extractor.actionName} in {extractor.scriptName}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <div className="lr-preserved">
              <div className="lr-preserved-header">
                <h4>Generated Assets</h4>
                <span>{result.summary?.file_name || file?.name}</span>
              </div>
              <div className="lr-preserved-grid">
                {(result.scripts || []).map((script: ScriptDefinition) => (
                  <div className="lr-preserved-card" key={script.name}>
                    <span className="lr-preserved-label">Script</span>
                    <strong>{script.name}</strong>
                    <small>{script.actions?.length || 0} actions / {script.data_sources?.length || 0} data sources / {script.actions?.reduce((count, action) => count + (action.extractors?.length || 0), 0) || 0} extractors</small>
                  </div>
                ))}
                {result.scenario_draft && (
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Scenario Draft</span>
                    <strong>{result.scenario_draft.groups?.length || 0} groups</strong>
                    <small>{result.scenario_draft.duration || 'Duration not set'}</small>
                  </div>
                )}
              </div>
            </div>
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
