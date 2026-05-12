import { useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import './LoadRunnerImport.css'

interface ConvertStats {
  thread_groups: number
  samplers: number
  converted_actions: number
  headers: number
  variables: number
  csv_sources: number
  assertions: number
  timers: number
  unsupported: number
}

interface Props {
  onImported?: (script: any) => void
  onClose?: () => void
}

export default function JMeterImport({ onImported, onClose }: Props) {
  const api = useApi()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [importing, setImporting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saveMessage, setSaveMessage] = useState('')
  const [result, setResult] = useState<{
    scripts: any[]
    script?: any
    scenario_draft?: any
    warnings: string[]
    unsupported_elements: string[]
    assumptions: string[]
    stats: ConvertStats
    summary: any
  } | null>(null)
  const [options, setOptions] = useState({
    name: '',
    base_url: '',
    include_assertions: true,
    scenario_draft: true,
  })

  const acceptFile = (nextFile?: File) => {
    if (!nextFile) return
    if (!nextFile.name.toLowerCase().endsWith('.jmx')) {
      setError('Please upload a JMeter .jmx test plan.')
      return
    }
    setFile(nextFile)
    setError('')
    setSaveMessage('')
    setResult(null)
  }

  const handleDrop = (event: React.DragEvent) => {
    event.preventDefault()
    setDragOver(false)
    acceptFile(event.dataTransfer.files[0])
  }

  const handleImport = async () => {
    if (!file) return
    setImporting(true)
    setError('')
    setSaveMessage('')
    setResult(null)
    try {
      const response = await api.importJMeter(file, {
        name: options.name || undefined,
        base_url: options.base_url || undefined,
        include_assertions: options.include_assertions,
        scenario_draft: options.scenario_draft,
      })
      setResult(response)
    } catch (error: any) {
      setError(error.message || 'JMeter import failed')
    } finally {
      setImporting(false)
    }
  }

  const handleSaveConverted = async () => {
    if (!result?.scripts?.length) return
    setSaving(true)
    setError('')
    setSaveMessage('')
    try {
      const response = await api.saveJMeterImport({
        scripts: result.scripts,
        scenario_draft: result.scenario_draft,
      })
      const scenarioText = response.scenario ? ' and scenario draft' : ''
      setSaveMessage(`Saved ${response.count || result.scripts.length} script${result.scripts.length === 1 ? '' : 's'}${scenarioText} to the library.`)
      onImported?.(result.scripts[0])
    } catch (error: any) {
      setError(error.message || 'Failed to save converted scripts')
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
            <button className="btn btn-sm btn-ghost" onClick={event => { event.stopPropagation(); setFile(null); setResult(null) }}>Change</button>
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

      <button className="btn btn-primary btn-lg" onClick={handleImport} disabled={!file || importing}>
        {importing ? <><span className="spinner" /> Converting...</> : 'Convert JMeter Plan'}
      </button>

      {error && <div className="lr-error">{error}</div>}
      {saveMessage && <div className="settings-notice">{saveMessage}</div>}

      {result && (
        <div className="lr-result">
          <div className="lr-result-header">
            <h3>Conversion Review</h3>
            <button className="btn btn-primary" onClick={handleSaveConverted} disabled={saving}>
              {saving ? 'Saving...' : `Save ${result.scripts?.length || 0} Script${(result.scripts?.length || 0) === 1 ? '' : 's'}`}
            </button>
          </div>

          <div className="lr-stats-grid">
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.converted_actions}</span><span className="lr-stat-label">Actions</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.thread_groups}</span><span className="lr-stat-label">Thread Groups</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.csv_sources}</span><span className="lr-stat-label">CSV Sources</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.variables}</span><span className="lr-stat-label">Variables</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.assertions}</span><span className="lr-stat-label">Assertions</span></div>
            <div className="lr-stat"><span className="lr-stat-val">{result.stats.unsupported}</span><span className="lr-stat-label">Unsupported</span></div>
          </div>

          {result.warnings.length > 0 && (
            <div className="lr-warnings">
              <h4>Conversion Warnings ({result.warnings.length})</h4>
              <ul>{result.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
            </div>
          )}

          {result.unsupported_elements.length > 0 && (
            <div className="lr-warnings">
              <h4>Unsupported Elements ({result.unsupported_elements.length})</h4>
              <ul>{result.unsupported_elements.map((item, index) => <li key={index}>{item}</li>)}</ul>
            </div>
          )}

          {result.assumptions.length > 0 && (
            <div className="lr-warnings">
              <h4>Assumptions</h4>
              <ul>{result.assumptions.map((item, index) => <li key={index}>{item}</li>)}</ul>
            </div>
          )}

          <div className="lr-preserved">
            <div className="lr-preserved-header">
              <h4>Generated Assets</h4>
              <span>{result.summary?.file_name || file?.name}</span>
            </div>
            <div className="lr-preserved-grid">
              {(result.scripts || []).map(script => (
                <div className="lr-preserved-card" key={script.name}>
                  <span className="lr-preserved-label">Script</span>
                  <strong>{script.name}</strong>
                  <small>{script.actions?.length || 0} actions / {script.data_sources?.length || 0} data sources</small>
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

          <div className="lr-preview">
            <h4>Import Preview</h4>
            <pre>{JSON.stringify({ scripts: result.scripts, scenario_draft: result.scenario_draft, summary: result.summary }, null, 2)}</pre>
          </div>
        </div>
      )}
    </div>
  )
}
