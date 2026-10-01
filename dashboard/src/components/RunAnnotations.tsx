import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import { runAnnotationTimestamp } from './runAnnotationModel'
interface Annotation { id: string; timestamp: string; label: string; text?: string; created_by: string; deleted_at?: string }
interface AnnotationSet { revision: number; annotations: Annotation[] }
export default function RunAnnotations({ testId, startedAt, canEdit }: { testId: string; startedAt?: string; canEdit: boolean }) {
  const [set, setAnnotations] = useState<AnnotationSet | null>(null)
  const [label, setLabel] = useState('')
  const [text, setText] = useState('')
  const [elapsed, setElapsed] = useState(0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, refresh] = useState(0)
  const path = `/tests/${encodeURIComponent(testId)}/analysis/annotations`
  useEffect(() => {
    const abort = new AbortController(); setAnnotations(null); setError('')
    request<AnnotationSet>(path, { signal: abort.signal }).then(value => { if (!abort.signal.aborted) setAnnotations(value) }).catch(reason => { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : 'Annotation load failed') })
    return () => abort.abort()
  }, [path, revision])
  const mutate = async (id?: string) => {
    if (!set) return
    setBusy(true); setError('')
    try {
      const next = await request<AnnotationSet>(id ? `${path}/${encodeURIComponent(id)}?expected_revision=${set.revision}` : path, { method: id ? 'DELETE' : 'POST', ...(id ? {} : { body: JSON.stringify({ expected_revision: set.revision, timestamp: runAnnotationTimestamp(startedAt || '', elapsed), label, text }) }) })
      setAnnotations(next); setLabel(''); setText('')
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'Annotation save failed') } finally { setBusy(false) }
  }
  return <details><summary>Run timeline annotations</summary><button disabled={busy} onClick={() => refresh(value => value + 1)}>Reload annotations</button>{error && <p role="alert">{error}</p>}{set && <><p>Revision {set.revision}. Notes are separate from immutable measurements; deleted notes retain their audit history.</p><ul>{set.annotations.filter(a => !a.deleted_at).map(a => <li key={a.id}><strong>{a.label}</strong> · {a.timestamp} · {a.created_by}<p>{a.text}</p>{canEdit && <button disabled={busy} onClick={() => void mutate(a.id)}>Delete annotation {a.label}</button>}</li>)}</ul>{canEdit && startedAt && <form onSubmit={event => { event.preventDefault(); void mutate() }}><label>Annotation elapsed seconds<input required type="number" min={0} max={86400} step="0.001" value={elapsed} onChange={event => setElapsed(Number(event.target.value))} /></label><label>Annotation label<input required maxLength={256} value={label} onChange={event => setLabel(event.target.value)} /></label><label>Annotation context<textarea maxLength={2048} value={text} onChange={event => setText(event.target.value)} /></label><button disabled={busy || !label.trim()}>Add timeline annotation</button></form>}</>}</details>
}
