import { useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ScriptDefinition } from '../types/scripts'

interface Preview { value: string; resolved: boolean; masked: boolean; note: string }

export function ParameterValuePreview({ script, name }: { script: ScriptDefinition; name: string }) {
  const [vuser, setVuser] = useState(1)
  const [iteration, setIteration] = useState(1)
  const [occurrence, setOccurrence] = useState(1)
  const [result, setResult] = useState<{ key: string; preview: Preview } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [reveal, setReveal] = useState(false)
  const currentKey = JSON.stringify([name, script.parameters, script.data_sources, vuser, iteration, occurrence])
  const keyRef = useRef(currentKey)
  keyRef.current = currentKey
  const preview = result?.key === currentKey ? result.preview : null
  const runPreview = async () => {
    const key = currentKey
    setBusy(true); setError(''); setReveal(false)
    try {
      const next = await request<Preview>('/dsl/parameter-preview', { method: 'POST', body: JSON.stringify({
        name, parameters: script.parameters || [], data_sources: script.data_sources || [], vuser, iteration, occurrence,
      }) })
      if (keyRef.current === key) setResult({ key, preview: next })
    } catch (cause) { if (keyRef.current === key) setError(cause instanceof Error ? cause.message : 'Preview could not be loaded.') }
    finally { setBusy(false) }
  }
  return <section className="parameter-value-preview" aria-label="Parameter value preview">
    <h4>Value preview</h4><p>Resolve this parameter with the execution engine, without running requests.</p>
    <div className="parameter-workspace-actions">
      <label>Virtual user<input aria-label="Preview virtual user" type="number" min="1" max="100000" value={vuser} onChange={event => setVuser(Number(event.target.value))} /></label>
      <label>Iteration<input aria-label="Preview iteration" type="number" min="1" max="1000" value={iteration} onChange={event => setIteration(Number(event.target.value))} /></label>
      <label>Occurrence<input aria-label="Preview occurrence" type="number" min="1" max="100" value={occurrence} onChange={event => setOccurrence(Number(event.target.value))} /></label>
      <button type="button" className="btn btn-sm btn-secondary" disabled={busy} onClick={() => void runPreview()}>{busy ? 'Resolving…' : 'Preview value'}</button>
    </div>
    {error && <p role="alert">{error}</p>}
    {preview ? <div role="status">
      <output>{preview.resolved ? (preview.masked || !reveal ? '••••••••' : preview.value || '(empty value)') : 'Value unavailable'}</output>
      {preview.resolved && !preview.masked && <button type="button" className="btn btn-sm btn-ghost" onClick={() => setReveal(!reveal)}>{reveal ? 'Hide value' : 'Reveal value'}</button>}
      {preview.masked && <p>Credential-like values stay redacted.</p>}
      <p>{preview.note}</p>
    </div> : <p>Preview updates only when requested. Changing data or allocation invalidates the previous result.</p>}
  </section>
}
