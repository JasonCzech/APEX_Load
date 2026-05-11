import { useState, useRef } from 'react'
import { useApi } from '../hooks/useApi'
import './LoadRunnerImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, any>
}

function getImportMetadata(script: any): ImportMetadata | null {
  return script?.metadata?.import || null
}

function getMetadataSummary(metadata: ImportMetadata | null) {
  const preserved = metadata?.preserved || {}

  return {
    runtimeFiles: Object.keys(preserved.runtime_files || {}),
    projectSections: Object.keys(preserved.project_settings || {}),
    unsupportedFunctions: Array.isArray(preserved.unsupported_functions) ? preserved.unsupported_functions : [],
  }
}

interface ConvertStats {
  total_functions: number; converted_actions: number; extractors: number
  think_times: number; transactions: number; skipped: number
  unsupported_funcs?: string[]
}

interface Props {
  onImported?: (script: any) => void
  onClose?: () => void
}

export default function LoadRunnerImport({ onImported, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{
    script: any; warnings: string[]; stats: ConvertStats
  } | null>(null)

  const [options, setOptions] = useState({
    name: '', base_url: '', include_assertions: true,
  })
  const importMetadata = getImportMetadata(result?.script)
  const metadataSummary = getMetadataSummary(importMetadata)

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault(); setDragOver(false)
    const f = e.dataTransfer.files[0]
    if (f && (f.name.endsWith('.zip') || f.type === 'application/zip')) {
      setFile(f); setError(''); setResult(null)
    } else { setError('Please upload a ZIP file containing a LoadRunner script folder.') }
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (f) { setFile(f); setError(''); setResult(null) }
  }

  const handleImport = async () => {
    if (!file) return
    setImporting(true); setError(''); setResult(null)
    try {
      const res = await api.importLoadRunner(file, {
        name: options.name || undefined,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
      })
      setResult(res)
    } catch (e: any) {
      setError(e.message || 'Import failed')
    } finally { setImporting(false) }
  }

  const handleSaveConverted = async () => {
    if (!result?.script) return
    try {
      await api.createScript(result.script)
      onImported?.(result.script)
    } catch (e: any) {
      setError(e.message || 'Failed to save script')
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
        </div>
      </div>

      {/* Import button */}
      <button className="btn btn-primary btn-lg" onClick={handleImport} disabled={!file || importing}>
        {importing ? (
          <><span className="spinner" /> Converting…</>
        ) : 'Convert LoadRunner Script'}
      </button>

      {error && <div className="lr-error">{error}</div>}

      {/* Results */}
      {result && (
        <div className="lr-result">
          <div className="lr-result-header">
            <h3>Conversion Complete</h3>
            <button className="btn btn-primary" onClick={handleSaveConverted}>Save to Script Library</button>
          </div>

          {/* Stats */}
          <div className="lr-stats-grid">
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.extractors}</span><span className="lr-stat-label">Extractors</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.transactions}</span><span className="lr-stat-label">Transactions</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.think_times}</span><span className="lr-stat-label">Think Times</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.total_functions}</span><span className="lr-stat-label">Total LR Functions</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.skipped}</span><span className="lr-stat-label">Skipped</span></div>
          </div>

          {/* Warnings */}
          {result.warnings.length > 0 && (
            <div className="lr-warnings">
              <h4>⚠ Conversion Warnings ({result.warnings.length})</h4>
              <ul>{result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
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
                  <span className="lr-preserved-label">Source</span>
                  <strong>{importMetadata.source || 'loadrunner'}</strong>
                  <small>{importMetadata.source_script_name || result.script.name}</small>
                </div>
                <div className="lr-preserved-card">
                  <span className="lr-preserved-label">Project Sections</span>
                  <strong>{metadataSummary.projectSections.length}</strong>
                  <small>{metadataSummary.projectSections.join(', ') || 'None captured'}</small>
                </div>
                <div className="lr-preserved-card">
                  <span className="lr-preserved-label">Runtime Files</span>
                  <strong>{metadataSummary.runtimeFiles.length}</strong>
                  <small>{metadataSummary.runtimeFiles.join(', ') || 'None found'}</small>
                </div>
                <div className="lr-preserved-card">
                  <span className="lr-preserved-label">Unsupported Functions</span>
                  <strong>{metadataSummary.unsupportedFunctions.length}</strong>
                  <small>{metadataSummary.unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                </div>
              </div>

              <details className="lr-preserved-details">
                <summary>View preserved metadata JSON</summary>
                <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
              </details>
            </div>
          )}

          {/* Preview */}
          <div className="lr-preview">
            <h4>Generated Script Preview</h4>
            <pre>{JSON.stringify({ script: result.script }, null, 2)}</pre>
          </div>
        </div>
      )}
    </div>
  )
}
