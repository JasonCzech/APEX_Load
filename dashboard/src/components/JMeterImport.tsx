import { useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { summarizeImportExtractors, summarizeSupportMatrix } from './jmeterImportUtils'
import { type ImportedAction, type ImportedScript, type JMeterImportResult as ImportResult, type JMeterSupportMatrixResponse, type SupportMatrix } from '../types/scriptImports'
import { type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ScriptImportReview from './ScriptImportReview'
import { withFallbackPersistedScripts } from './importSaveUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './LoadRunnerImport.css'

interface Props {
  projectId?: string
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'

function toScriptDefinition(script: ImportedScript): ScriptDefinition {
  return script as unknown as ScriptDefinition
}

function getPrimaryScript(result: ImportResult | null): ImportedScript | null {
  return result?.script || result?.scripts?.[0] || null
}

export default function JMeterImport({ projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [supportMatrix, setSupportMatrix] = useState<SupportMatrix | null>(null)
  const [reviewMode, setReviewMode] = useState<ReviewMode | null>(null)
  const [result, setResult] = useState<ImportResult | null>(null)
  const [options, setOptions] = useState({
    name: '',
    base_url: '',
    include_assertions: true,
    scenario_draft: true,
  })
  const supportMatrixSections = summarizeSupportMatrix(supportMatrix)
  const extractorSummary = summarizeImportExtractors(result?.scripts || [])
  const primaryScript = getPrimaryScript(result)

  useEffect(() => {
    let cancelled = false

    api.getJMeterSupportMatrix()
      .then((response: JMeterSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(response.support_matrix || null)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix(null)
        }
      })

    return () => {
      cancelled = true
    }
  }, [api])

  const acceptFile = (nextFile?: File) => {
    if (!nextFile) return
    if (!nextFile.name.toLowerCase().endsWith('.jmx')) {
      setError('Please upload a JMeter .jmx test plan.')
      return
    }
    setFile(nextFile)
    setError('')
    setNotice('')
    setReviewMode(null)
    setResult(null)
  }

  const handleDrop = (event: React.DragEvent) => {
    event.preventDefault()
    setDragOver(false)
    acceptFile(event.dataTransfer.files[0])
  }

  const handleImport = async () => {
    if (!file) return
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
      setResult(response)
      setReviewMode('conversion')
      setNotice('Conversion complete. Review the generated scripts, warnings, and assumptions before saving.')
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'JMeter import failed'))
    } finally {
      setWorkingMode(null)
    }
  }

  const handleValidate = async () => {
    if (!file) return
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
      setResult(response)
      setReviewMode('validation')
      setNotice('Validation complete. Review the generated scripts and warnings before saving.')
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'JMeter validation failed'))
    } finally {
      setWorkingMode(null)
    }
  }

  const handleSaveConverted = async () => {
    if (!result?.scripts?.length) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const scripts = result.scripts.map(script => script.project_id ? script : { ...script, project_id: projectId })
      const response = await api.saveJMeterImport({
        scripts,
        scenario_draft: result.scenario_draft,
      })
      const savedScripts = withFallbackPersistedScripts(response.scripts, result.scripts.map(script => toScriptDefinition(script)))
      const scenarioText = response.scenario ? ' and scenario draft' : ''
      setNotice(`Saved ${response.count || result.scripts.length} script${result.scripts.length === 1 ? '' : 's'}${scenarioText} to the library.`)
      if (savedScripts.length) {
        onImported?.({
          scripts: savedScripts,
          scenario: response.scenario,
        })
      }
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save converted scripts'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="lr-import">
      <div className="lr-import-header">
        <div>
          <h2>Import JMeter Test Plan</h2>
          <p className="lr-subtitle">
            Upload a <code>.jmx</code> plan and convert HTTP thread groups into Stormrunner reusable scripts. The importer preserves variables, CSV data sources, headers, timers, and review notes where possible.
          </p>
        </div>
        {onClose && <button className="btn btn-ghost" onClick={onClose}>x</button>}
      </div>

      <div
        className={`lr-dropzone ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
        onDragOver={event => { event.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={() => fileRef.current?.click()}
      >
        <input ref={fileRef} type="file" accept=".jmx,.xml" onChange={event => acceptFile(event.target.files?.[0])} hidden />
        {file ? (
          <div className="lr-file-info">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div>
              <span className="lr-filename">{file.name}</span>
              <span className="lr-filesize">{(file.size / 1024).toFixed(1)} KB</span>
            </div>
            <button className="btn btn-sm btn-ghost" onClick={event => { event.stopPropagation(); setFile(null); setResult(null); setNotice(''); setReviewMode(null) }}>Change</button>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop a JMeter <code>.jmx</code> file here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      <div className="lr-options">
        <h3 className="lr-section-title">Conversion Options</h3>
        <div className="lr-options-grid">
          <label><span>Script Name Override</span>
            <input value={options.name} onChange={event => setOptions(prev => ({ ...prev, name: event.target.value }))} placeholder="Default: thread group name" />
          </label>
          <label><span>Base URL Override</span>
            <input value={options.base_url} onChange={event => setOptions(prev => ({ ...prev, base_url: event.target.value }))} placeholder="Default: HTTP Request Defaults or first sampler" />
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.include_assertions} onChange={event => setOptions(prev => ({ ...prev, include_assertions: event.target.checked }))} />
            <span>Add default status_code assertions</span>
          </label>
          <label className="lr-checkbox">
            <input type="checkbox" checked={options.scenario_draft} onChange={event => setOptions(prev => ({ ...prev, scenario_draft: event.target.checked }))} />
            <span>Generate scenario draft from thread groups</span>
          </label>
        </div>
      </div>

      <div className="settings-notice">
        JMeter import is still centered on HTTP plans, but SOAP-shaped HTTP Request samplers can now be promoted into SOAP scripts. Dedicated SOAP plugins and Java-over-HTTP style transports remain unsupported until deeper converter and runtime work lands.
      </div>

      {supportMatrix && (
        <div className="lr-preserved">
          <div className="lr-preserved-header">
            <h4>Phase 1 Coverage</h4>
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
          {supportMatrix.notes && supportMatrix.notes.length > 0 && (
            <div className="lr-warnings">
              <h4>Importer Notes</h4>
              <ul>{supportMatrix.notes.map((note: string, index: number) => <li key={index}>{note}</li>)}</ul>
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

      {error && <div className="lr-error">{error}</div>}
      {notice && <div className="settings-notice">{notice}</div>}

      {result && (
        <div className="lr-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Conversion Review'}
            subtitle={reviewMode === 'validation'
              ? 'This preview came from the dry-run validator. You can still save the reviewed assets if the output looks correct.'
              : 'This preview came from the converter and is ready for final review before saving.'}
            script={primaryScript || { name: result.summary?.file_name || file?.name || 'Imported JMeter Plan' }}
            previewData={{ scripts: result.scripts, scenario_draft: result.scenario_draft, summary: result.summary }}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            saving={saving}
            saveLabel={`${reviewMode === 'validation' ? 'Save Reviewed' : 'Save'} ${result.scripts?.length || 0} Script${(result.scripts?.length || 0) === 1 ? '' : 's'}`}
            onSave={handleSaveConverted}
            onEdit={onEdit && primaryScript ? () => onEdit(toScriptDefinition(primaryScript)) : undefined}
            editLabel={result.scripts.length > 1 ? 'Open First Script In Visual Editor' : 'Open In Visual Editor'}
            previewTitle="Import Preview"
          >
            <div className="lr-stats-grid">
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.thread_groups}</span><span className="lr-stat-label">Thread Groups</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.csv_sources}</span><span className="lr-stat-label">CSV Sources</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.variables}</span><span className="lr-stat-label">Variables</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.assertions}</span><span className="lr-stat-label">Assertions</span></div>
              <div className="lr-stat"><span className="lr-stat-val">{result.stats.unsupported}</span><span className="lr-stat-label">Unsupported</span></div>
            </div>

            {result.unsupported_elements.length > 0 && (
              <div className="lr-warnings">
                <h4>Unsupported Elements ({result.unsupported_elements.length})</h4>
                <ul>{result.unsupported_elements.map((item: string, index: number) => <li key={index}>{item}</li>)}</ul>
              </div>
            )}

            {result.assumptions.length > 0 && (
              <div className="lr-warnings">
                <h4>Assumptions</h4>
                <ul>{result.assumptions.map((item: string, index: number) => <li key={index}>{item}</li>)}</ul>
              </div>
            )}

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
                {(result.scripts || []).map((script: ImportedScript) => (
                  <div className="lr-preserved-card" key={script.name}>
                    <span className="lr-preserved-label">Script</span>
                    <strong>{script.name}</strong>
                    <small>{script.actions?.length || 0} actions / {script.data_sources?.length || 0} data sources / {script.actions?.reduce((count: number, action: ImportedAction) => count + (action.extractors?.length || 0), 0) || 0} extractors</small>
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
