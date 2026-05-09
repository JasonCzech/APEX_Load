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
interface ScriptData {
  name: string; version: string; protocol: string
  config: { base_url: string; think_time?: { type: string; mean_ms: number; std_ms?: number } }
  actions: Action[]
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
  onSave?: () => void
  onClose?: () => void
}

const EMPTY_SCRIPT: ScriptData = {
  name: '', version: '1.0.0', protocol: 'http',
  config: { base_url: '', think_time: { type: 'fixed', mean_ms: 1000 } },
  actions: []
}

const EMPTY_ACTION: Action = {
  name: '', type: 'GET', target: '', headers: {}, body: '',
  extractors: [], assertions: [], think_time_after: true
}

export default function ScriptEditor({ scriptId, initialScript, onSave, onClose }: Props) {
  const api = useApi()
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [jsonMode, setJsonMode] = useState(false)
  const [jsonText, setJsonText] = useState('')

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
        await api.createScript(script)
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
              <label>
                <span>Think Time Type</span>
                <select value={script.config.think_time?.type || 'fixed'} onChange={e => updateField('config.think_time', { ...script.config.think_time, type: e.target.value })}>
                  <option value="fixed">Fixed</option>
                  <option value="normal">Normal Distribution</option>
                  <option value="uniform">Uniform</option>
                </select>
              </label>
              <label>
                <span>Think Time (ms)</span>
                <input type="number" value={script.config.think_time?.mean_ms || 0} onChange={e => updateField('config.think_time', { ...script.config.think_time, mean_ms: Number(e.target.value) })} />
              </label>
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
