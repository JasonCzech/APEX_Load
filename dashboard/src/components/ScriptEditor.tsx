import { useState, useEffect } from 'react'
import { useApi } from '../hooks/useApi'
import './ScriptEditor.css'

interface Action {
  name: string; type: string; method?: string; target: string
  headers?: Record<string, string>; body?: string
  extractors?: Extractor[]; assertions?: Assertion[]
  think_time_after?: boolean
}
interface Extractor { name: string; source: string; type: string; expression: string }
interface Assertion { type: string; expected?: any; value?: string; max?: number }

interface ThinkTimeConfig {
  type: string; mean_ms: number; std_ms?: number; min_ms?: number; max_ms?: number
  multiplier?: number; limit_ms?: number; ignore?: boolean
}
interface PacingConfig {
  type: string; delay_ms?: number; min_delay_ms?: number; max_delay_ms?: number
  interval_ms?: number; min_interval_ms?: number; max_interval_ms?: number; tps?: number
}
interface RunLogicConfig {
  iterations?: number; auto_transactions?: string
}
interface ErrorHandlingConfig {
  continue_on_error?: boolean; max_retries?: number; retry_delay_ms?: number
  retry_backoff?: string; snapshot_on_error?: boolean
}
interface LogConfig {
  level?: string; on_error_only?: boolean
}
interface HTTPConfig {
  connect_timeout_ms?: number; request_timeout_ms?: number; tls_timeout_ms?: number; idle_timeout_ms?: number
  version?: string; max_conns_per_host?: number; keep_alive?: boolean
  user_agent?: string; accept_encoding?: string; accept_language?: string
  max_redirects?: number
  tls?: { min_version?: string; insecure_skip_verify?: boolean; client_cert_file?: string; client_key_file?: string; ca_cert_file?: string }
  proxy?: { url?: string; username?: string; password?: string }
  auth?: { type?: string; username?: string; password?: string; token?: string }
}

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, any>
}

interface ScriptMetadata {
  import?: ImportMetadata
}

interface ScriptConfig {
  base_url: string
  think_time?: ThinkTimeConfig
  pacing?: PacingConfig
  run_logic?: RunLogicConfig
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  http?: HTTPConfig
}

interface ScriptData {
  name: string; version: string; protocol: string
  metadata?: ScriptMetadata
  config: ScriptConfig
  actions: Action[]
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
  projectId?: string
  folderId?: string | null
  onSave?: () => void
  onClose?: () => void
}

const EMPTY_SCRIPT: ScriptData = {
  name: '', version: '1.0.0', protocol: 'http',
  config: {
    base_url: '',
    think_time: { type: 'fixed', mean_ms: 1000 },
    pacing: { type: 'none' },
    run_logic: { iterations: 0, auto_transactions: 'none' },
    error_handling: { continue_on_error: false },
    log: { level: 'standard' },
  },
  actions: []
}

const EMPTY_ACTION: Action = {
  name: '', type: 'GET', target: '', headers: {}, body: '',
  extractors: [], assertions: [], think_time_after: true
}

function getImportSummary(metadata?: ImportMetadata | null) {
  const preserved = metadata?.preserved || {}

  return {
    runtimeFiles: Object.keys(preserved.runtime_files || {}),
    projectSections: Object.keys(preserved.project_settings || {}),
    unsupportedFunctions: Array.isArray(preserved.unsupported_functions) ? preserved.unsupported_functions : [],
  }
}

export default function ScriptEditor({ scriptId, initialScript, projectId, folderId, onSave, onClose }: Props) {
  const api = useApi()
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [jsonMode, setJsonMode] = useState(false)
  const [jsonText, setJsonText] = useState('')
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({})
  const importMetadata = script.metadata?.import
  const importSummary = getImportSummary(importMetadata)

  const toggleSection = (id: string) => setOpenSections(prev => ({ ...prev, [id]: !prev[id] }))

  useEffect(() => {
    if (scriptId && !initialScript) {
      api.getScript(scriptId).then((res: any) => {
        if (res.script) setScript(res.script)
      }).catch(() => {})
    }
  }, [scriptId])

  const updateField = (path: string, value: any) => {
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev))
      const keys = path.split('.')
      let obj: any = next
      for (let i = 0; i < keys.length - 1; i++) obj = obj[keys[i]]
      obj[keys[keys.length - 1]] = value
      return next
    })
  }

  const addAction = () => {
    const a = { ...EMPTY_ACTION, name: `Action_${script.actions.length + 1}`, extractors: [], assertions: [] }
    setScript(prev => ({ ...prev, actions: [...prev.actions, a] }))
    setActiveAction(script.actions.length)
  }

  const removeAction = (idx: number) => {
    setScript(prev => ({ ...prev, actions: prev.actions.filter((_, i) => i !== idx) }))
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
    setActiveAction(newIdx)
  }

  const updateAction = (idx: number, field: string, value: any) => {
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
      ext.push({ name: '', source: 'body', type: 'jsonpath', expression: '' })
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
      assertions.push({ type: 'status_code', expected: 200 })
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const updateAssertion = (actionIdx: number, aIdx: number, field: string, value: any) => {
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

  const handleSave = async () => {
    setError(''); setSuccess(''); setSaving(true)
    try {
      if (scriptId) {
        await api.updateScript(scriptId, script)
      } else {
        await api.createScript(script, projectId, folderId)
      }
      setSuccess('Script saved successfully!')
      onSave?.()
    } catch (e: any) {
      setError(e.message || 'Failed to save script')
    } finally { setSaving(false) }
  }

  const toggleJsonMode = () => {
    if (!jsonMode) {
      setJsonText(JSON.stringify({ script }, null, 2))
    } else {
      try {
        const parsed = JSON.parse(jsonText)
        setScript(parsed.script || parsed)
      } catch { setError('Invalid JSON') ; return }
    }
    setJsonMode(!jsonMode)
  }

  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success)'
      case 'POST': return 'var(--info)'
      case 'PUT': return 'var(--warning)'
      case 'DELETE': return 'var(--danger)'
      case 'PATCH': return 'var(--chart-6)'
      default: return 'var(--text-secondary)'
    }
  }

  return (
    <div className="script-editor">
      <div className="editor-header">
        <div className="editor-title-row">
          <h2>{scriptId ? 'Edit Script' : 'Create Script'}</h2>
          <div className="editor-actions">
            <button className="btn btn-ghost" onClick={toggleJsonMode}>
              {jsonMode ? '◧ Form' : '{ } JSON'}
            </button>
            <button className="btn btn-primary" onClick={handleSave} disabled={saving || !script.name}>
              {saving ? 'Saving…' : 'Save Script'}
            </button>
            {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
          </div>
        </div>
        {error && <div className="editor-toast error">{error}</div>}
        {success && <div className="editor-toast success">{success}</div>}
      </div>

      {jsonMode ? (
        <div className="json-editor">
          <textarea value={jsonText} onChange={e => setJsonText(e.target.value)} spellCheck={false} />
        </div>
      ) : (
        <div className="editor-body">
          {/* Script Config Panel */}
          <div className="config-panel">
            <h3 className="panel-title">Script Configuration</h3>
            <div className="form-grid">
              <label>
                <span>Name</span>
                <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
              </label>
              <label>
                <span>Protocol</span>
                <select value={script.protocol} onChange={e => updateField('protocol', e.target.value)}>
                  <option value="http">HTTP</option>
                  <option value="https">HTTPS</option>
                </select>
              </label>
              <label>
                <span>Version</span>
                <input value={script.version} onChange={e => updateField('version', e.target.value)} placeholder="1.0.0" />
              </label>
              <label>
                <span>Base URL</span>
                <input value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder="https://api.example.com" />
              </label>
            </div>
          </div>

          {importMetadata && (
            <div className="import-metadata-panel">
              <div className="import-metadata-header">
                <div>
                  <h3 className="panel-title">Imported Metadata</h3>
                  <p>This script retains source-specific LoadRunner settings that Stormrunner does not edit directly.</p>
                </div>
                <span className="import-source-chip">{importMetadata.source || 'loadrunner'}</span>
              </div>

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
              </div>

              <details className="import-metadata-details">
                <summary>View preserved metadata JSON</summary>
                <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
              </details>
            </div>
          )}

          {/* Runtime Settings Panels */}
          <div className="runtime-settings">
            <h3 className="settings-heading">Runtime Settings</h3>

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
                  {script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
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
                      <span>Auto-Transactions</span>
                      <select value={script.config.run_logic?.auto_transactions || 'none'}
                        onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
                        <option value="none">None</option>
                        <option value="per_action">Per Action</option>
                        <option value="per_iteration">Per Iteration</option>
                      </select>
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

            {/* HTTP Protocol */}
            <div className={`settings-section ${openSections['http'] ? 'open' : ''}`}>
              <button className="section-toggle" onClick={() => toggleSection('http')}>
                <span className="section-icon">{'\ud83c\udf10'}</span>
                <span className="section-label">HTTP Protocol</span>
                <span className="section-summary">
                  {script.config.http?.proxy?.url ? 'Proxy configured' : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
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
                      <label className="full-width"><span>Proxy URL</span>
                        <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy:8080"
                          onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
                      {script.config.http?.proxy?.url && (
                        <>
                          <label><span>Proxy Username</span>
                            <input value={script.config.http?.proxy?.username || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                          <label><span>Proxy Password</span>
                            <input type="password" value={script.config.http?.proxy?.password || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                        </>
                      )}
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
                      <label className="full-width"><span>User-Agent</span>
                        <input value={script.config.http?.user_agent || ''} placeholder="Mozilla/5.0 ..."
                          onChange={e => updateField('config.http', { ...script.config.http, user_agent: e.target.value })} /></label>
                      <label><span>Accept-Encoding</span>
                        <input value={script.config.http?.accept_encoding || ''} placeholder="gzip, deflate, br"
                          onChange={e => updateField('config.http', { ...script.config.http, accept_encoding: e.target.value })} /></label>
                      <label><span>Accept-Language</span>
                        <input value={script.config.http?.accept_language || ''} placeholder="en-US,en;q=0.9"
                          onChange={e => updateField('config.http', { ...script.config.http, accept_language: e.target.value })} /></label>
                    </div>
                  </div>
                </div>
              )}
            </div>

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
                  <div className="form-grid">
                    <label>
                      <span>Log Level</span>
                      <select value={script.config.log?.level || 'standard'}
                        onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
                        <option value="disabled">Disabled</option>
                        <option value="standard">Standard</option>
                        <option value="extended">Extended (verbose)</option>
                      </select>
                    </label>
                    <label className="checkbox-label">
                      <input type="checkbox" checked={script.config.log?.on_error_only || false}
                        onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
                      <span>Extended log on error only</span>
                    </label>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Actions List + Detail */}
          <div className="actions-layout">
            <div className="actions-sidebar">
              <div className="actions-sidebar-header">
                <h3 className="panel-title">Actions ({script.actions.length})</h3>
                <button className="btn btn-sm btn-accent" onClick={addAction}>+ Add</button>
              </div>
              <div className="action-list">
                {script.actions.map((a, i) => (
                  <div key={i} className={`action-item ${activeAction === i ? 'active' : ''}`} onClick={() => setActiveAction(i)}>
                    <span className="action-method" style={{ color: methodColor(a.type) }}>{a.type}</span>
                    <span className="action-name-text">{a.name || 'Unnamed'}</span>
                    <div className="action-item-actions">
                      <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, -1) }} disabled={i === 0}>↑</button>
                      <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, 1) }} disabled={i === script.actions.length - 1}>↓</button>
                      <button className="btn-icon danger" onClick={e => { e.stopPropagation(); removeAction(i) }}>✕</button>
                    </div>
                  </div>
                ))}
                {script.actions.length === 0 && (
                  <div className="action-empty">No actions yet. Click "+ Add" to create one.</div>
                )}
              </div>
            </div>

            {/* Action Detail */}
            <div className="action-detail">
              {activeAction !== null && script.actions[activeAction] ? (
                <ActionEditor
                  action={script.actions[activeAction]}
                  index={activeAction}
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
                  methodColor={methodColor}
                />
              ) : (
                <div className="action-placeholder">
                  <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
                  <p>Select an action to edit its details</p>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/* Sub-component for editing a single action's details */
function ActionEditor({ action, index, onUpdate, onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion, onAddHeader, onUpdateHeader, onRemoveHeader, methodColor }: {
  action: Action; index: number
  onUpdate: (i: number, f: string, v: any) => void
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: string, v: any) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onAddHeader: (i: number) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveHeader: (ai: number, k: string) => void
  methodColor: (m: string) => string
}) {
  return (
    <div className="action-editor-panel">
      <div className="action-editor-top">
        <span className="action-badge" style={{ background: methodColor(action.type), color: '#fff' }}>{action.type}</span>
        <h3>{action.name || 'Unnamed Action'}</h3>
      </div>

      <div className="form-grid">
        <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
        <label><span>Method</span>
          <select value={action.type} onChange={e => onUpdate(index, 'type', e.target.value)}>
            {['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS'].map(m => <option key={m}>{m}</option>)}
          </select>
        </label>
        <label className="full-width"><span>Target (path)</span><input value={action.target} onChange={e => onUpdate(index, 'target', e.target.value)} placeholder="/api/resource" /></label>
      </div>

      {/* Headers */}
      <div className="sub-section">
        <div className="sub-header"><h4>Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
        {Object.entries(action.headers || {}).map(([k, v]) => (
          <div key={k} className="kv-row">
            <input className="kv-key" value={k} onChange={e => onUpdateHeader(index, k, e.target.value, v)} placeholder="Key" />
            <input className="kv-val" value={v} onChange={e => onUpdateHeader(index, k, k, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveHeader(index, k)}>✕</button>
          </div>
        ))}
      </div>

      {/* Body */}
      {['POST','PUT','PATCH'].includes(action.type) && (
        <div className="sub-section">
          <h4>Body</h4>
          <textarea className="body-textarea" value={action.body || ''} onChange={e => onUpdate(index, 'body', e.target.value)} placeholder='{"key": "value"}' rows={4} />
        </div>
      )}

      {/* Extractors */}
      <div className="sub-section">
        <div className="sub-header"><h4>Extractors</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddExtractor(index)}>+ Add</button></div>
        {(action.extractors || []).map((ext, ei) => (
          <div key={ei} className="extractor-row">
            <input value={ext.name} onChange={e => onUpdateExtractor(index, ei, 'name', e.target.value)} placeholder="Variable name" />
            <select value={ext.source} onChange={e => onUpdateExtractor(index, ei, 'source', e.target.value)}>
              <option value="body">Body</option><option value="header">Header</option><option value="cookie">Cookie</option>
            </select>
            <select value={ext.type} onChange={e => onUpdateExtractor(index, ei, 'type', e.target.value)}>
              <option value="jsonpath">JSONPath</option><option value="regex">Regex</option><option value="boundary">Boundary</option><option value="xpath">XPath</option>
            </select>
            <input value={ext.expression} onChange={e => onUpdateExtractor(index, ei, 'expression', e.target.value)} placeholder="$.data.id" className="ext-expr" />
            <button className="btn-icon danger" onClick={() => onRemoveExtractor(index, ei)}>✕</button>
          </div>
        ))}
      </div>

      {/* Assertions */}
      <div className="sub-section">
        <div className="sub-header"><h4>Assertions</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddAssertion(index)}>+ Add</button></div>
        {(action.assertions || []).map((a, ai) => (
          <div key={ai} className="assertion-row">
            <select value={a.type} onChange={e => onUpdateAssertion(index, ai, 'type', e.target.value)}>
              <option value="status_code">Status Code</option><option value="response_time_ms">Response Time (ms)</option>
              <option value="body_contains">Body Contains</option><option value="header_contains">Header Contains</option>
            </select>
            {a.type === 'status_code' && <input type="number" value={a.expected ?? 200} onChange={e => onUpdateAssertion(index, ai, 'expected', Number(e.target.value))} />}
            {a.type === 'response_time_ms' && <input type="number" value={a.max ?? 5000} onChange={e => onUpdateAssertion(index, ai, 'max', Number(e.target.value))} placeholder="Max ms" />}
            {(a.type === 'body_contains' || a.type === 'header_contains') && <input value={a.value ?? ''} onChange={e => onUpdateAssertion(index, ai, 'value', e.target.value)} placeholder="Expected text" />}
            <button className="btn-icon danger" onClick={() => onRemoveAssertion(index, ai)}>✕</button>
          </div>
        ))}
      </div>

      {/* Think Time */}
      <div className="sub-section">
        <label className="checkbox-label">
          <input type="checkbox" checked={action.think_time_after ?? false} onChange={e => onUpdate(index, 'think_time_after', e.target.checked)} />
          <span>Apply think time after this action</span>
        </label>
      </div>
    </div>
  )
}
