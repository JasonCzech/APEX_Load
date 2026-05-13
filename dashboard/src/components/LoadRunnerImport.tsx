import { useState, useRef } from 'react'
import { useApi } from '../hooks/useApi'
import { normalizeScriptImportResult } from './scriptImportUtils'
import { type ImportedScript, type LoadRunnerConvertStats, type LoadRunnerImportResult, type LoadRunnerImportSummary } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ScriptImportReview from './ScriptImportReview'
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

function getObjectKeys(value: unknown): string[] {
  return typeof value === 'object' && value !== null ? Object.keys(value as Record<string, unknown>) : []
}

function getMetadataSummary(metadata: ImportMetadata | null) {
  const preserved = metadata?.preserved || {}

  return {
    runtimeFiles: getObjectKeys(preserved.runtime_files),
    parameterFiles: Array.isArray(preserved.parameter_files) ? preserved.parameter_files as string[] : [],
    projectSections: getObjectKeys(preserved.project_settings),
    unsupportedFunctions: Array.isArray(preserved.unsupported_functions) ? preserved.unsupported_functions : [],
  }
}

function getSummaryList(values?: string[]) {
  return Array.isArray(values) ? values : []
}

interface Props {
  onImported?: (script: ImportedScript) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

type ReviewMode = 'validation' | 'conversion'

export default function LoadRunnerImport({ onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [result, setResult] = useState<LoadRunnerImportResult | null>(null)
  const [reviewMode, setReviewMode] = useState<ReviewMode | null>(null)

  const [options, setOptions] = useState({
    name: '', base_url: '', include_assertions: true, preserve_transactions: true,
  })
  const importedScript = result?.script ?? result?.scripts?.[0] ?? null
  const importMetadata = getImportMetadata(importedScript || undefined)
  const metadataSummary = getMetadataSummary(importMetadata)
  const reviewSummary: LoadRunnerImportSummary | null = result?.summary || null
  const projectSections = getSummaryList(reviewSummary?.project_sections).length > 0
    ? getSummaryList(reviewSummary?.project_sections)
    : metadataSummary.projectSections
  const runtimeFiles = getSummaryList(reviewSummary?.runtime_files).length > 0
    ? getSummaryList(reviewSummary?.runtime_files)
    : metadataSummary.runtimeFiles
  const parameterFiles = getSummaryList(reviewSummary?.parameter_files).length > 0
    ? getSummaryList(reviewSummary?.parameter_files)
    : metadataSummary.parameterFiles
  const unsupportedFunctions = result?.unsupported_elements.length
    ? result.unsupported_elements
    : metadataSummary.unsupportedFunctions

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault(); setDragOver(false)
    const f = e.dataTransfer.files[0]
    if (f && (f.name.endsWith('.zip') || f.type === 'application/zip')) {
      setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null)
    } else { setError('Please upload a ZIP file containing a LoadRunner script folder.') }
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (f) { setFile(f); setError(''); setNotice(''); setResult(null); setReviewMode(null) }
  }

  const runReview = async (mode: 'convert' | 'validate') => {
    if (!file) return
    setWorkingMode(mode); setError(''); setNotice(''); setResult(null)
    try {
      const request = mode === 'validate' ? api.validateLoadRunnerImport : api.importLoadRunner
      const res = await request(file, {
        name: options.name || undefined,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        preserve_transactions: options.preserve_transactions,
      })
      setResult(normalizeScriptImportResult<LoadRunnerConvertStats, unknown, ImportedScript, unknown, LoadRunnerImportSummary>(res))
      setReviewMode(mode === 'validate' ? 'validation' : 'conversion')
      setNotice(mode === 'validate'
        ? 'Validation complete. Review the generated script, warnings, and preserved settings before saving.'
        : 'Conversion complete. Review the generated script, warnings, and preserved settings before saving.')
    } catch (error: unknown) {
      setError(getErrorMessage(error, mode === 'validate' ? 'Validation failed' : 'Import failed'))
    } finally { setWorkingMode(null) }
  }

  const handleSaveConverted = async () => {
    if (!importedScript) return
    try {
      await api.createScript(importedScript)
      onImported?.(importedScript)
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to save script'))
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
        </div>
      </div>

      <div className="settings-notice">
        SOAP-aware detection is enabled for LoadRunner request assets that already resolve to SOAP-style calls or envelopes. Java-over-HTTP import remains unavailable until the dedicated runtime and transport format are implemented.
      </div>

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
            subtitle={reviewMode === 'validation'
              ? 'This preview came from the dry-run validator. You can still save the reviewed script if the output looks correct.'
              : 'Review the generated script, preserved metadata, and warnings before saving.'}
            script={importedScript}
            warnings={result.warnings}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            saveLabel={reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save to Script Library'}
            onSave={handleSaveConverted}
            onEdit={onEdit ? () => onEdit(toScriptDefinition(importedScript)) : undefined}
          >

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
                <ul>{unsupportedFunctions.map((fnName, index) => <li key={`${fnName}-${index}`}>{fnName}</li>)}</ul>
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
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Mapped Runtime</span>
                    <strong>{reviewSummary?.mapped_runtime_file || 'None mapped'}</strong>
                    <small>{runtimeFiles.join(', ') || 'No runtime files found'}</small>
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Parameter Files</span>
                    <strong>{parameterFiles.length}</strong>
                    <small>{parameterFiles.join(', ') || 'No parameter files found'}</small>
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
                  </div>
                  <div className="lr-preserved-card">
                    <span className="lr-preserved-label">Source</span>
                    <strong>{importMetadata.source || 'loadrunner'}</strong>
                    <small>{importedScript.name}</small>
                  </div>
                </div>

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
