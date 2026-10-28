import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import { runAnnotationTimestamp } from './runAnnotationModel'
import { confirmAction } from './ux/confirmAction'
import { formatDateTime } from '../utils/dateTime'
import type { TimelineAnnotation } from './runReport/SynchronizedTimeline'

/** "at 0:15 (10/8/2026, 12:14:23 AM)": run time first, wall clock for reference. */
function annotationTime(timestamp: string, startedAt?: string) {
  const at = Date.parse(timestamp)
  if (!Number.isFinite(at)) return timestamp
  const wall = formatDateTime(at, { seconds: true })
  const start = Date.parse(startedAt || '')
  if (!Number.isFinite(start)) return wall
  const seconds = Math.max(0, Math.round((at - start) / 1000))
  const elapsed = seconds >= 3600
    ? `${Math.floor(seconds / 3600)}:${String(Math.floor(seconds % 3600 / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
    : `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
  return `at ${elapsed} (${wall})`
}
interface Annotation { id: string; timestamp: string; label: string; text?: string; created_by: string; deleted_at?: string }
interface AnnotationSet { revision: number; annotations: Annotation[] }
// reloadKey reloads the notes when it changes, as after one is added from a
// root-cause hypothesis.
export default function RunAnnotations({ testId, startedAt, canEdit, onAnnotationsChange, reloadKey = 0 }: { testId: string; startedAt?: string; canEdit: boolean; onAnnotationsChange?: (annotations: TimelineAnnotation[]) => void; reloadKey?: number }) {
  const [set, setAnnotations] = useState<AnnotationSet | null>(null)
  const [label, setLabel] = useState('')
  const [text, setText] = useState('')
  const [elapsed, setElapsed] = useState(0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, refresh] = useState(0)
  const path = `/tests/${encodeURIComponent(testId)}/analysis/annotations`
  // The report draws the notes on its timeline.
  useEffect(() => {
    onAnnotationsChange?.((set?.annotations || []).filter(a => !a.deleted_at).map(({ id, label, timestamp }) => ({ id, label, timestamp })))
  }, [onAnnotationsChange, set])
  useEffect(() => {
    const abort = new AbortController(); setAnnotations(null); setError('')
    request<AnnotationSet>(path, { signal: abort.signal }).then(value => { if (!abort.signal.aborted) setAnnotations(value) }).catch(reason => { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : 'Annotation load failed') })
    return () => abort.abort()
  }, [path, revision, reloadKey])
  const mutate = async (id?: string) => {
    if (!set) return
    const target = id ? set.annotations.find(a => a.id === id) : undefined
    if (id && !(await confirmAction(`Delete the annotation “${target?.label || id}”? Its audit history is kept.`, { title: 'Delete annotation', confirmLabel: 'Delete', destructive: true }))) return
    setBusy(true); setError('')
    try {
      const next = await request<AnnotationSet>(id ? `${path}/${encodeURIComponent(id)}?expected_revision=${set.revision}` : path, { method: id ? 'DELETE' : 'POST', ...(id ? {} : { body: JSON.stringify({ expected_revision: set.revision, timestamp: runAnnotationTimestamp(startedAt || '', elapsed), label, text }) }) })
      setAnnotations(next); setLabel(''); setText('')
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'Annotation save failed') } finally { setBusy(false) }
  }
  const activeAnnotations = (set?.annotations || []).filter(a => !a.deleted_at)
  return <details className="run-report-panel run-annotations-panel">
    <summary>Run timeline annotations</summary>
    <div className="run-annotations-header">
      {set && <p className="run-report-muted">Revision {set.revision}. Notes are separate from immutable measurements; deleted notes retain their audit history.</p>}
      <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => refresh(value => value + 1)}>Reload annotations</button>
    </div>
    {error && <p className="run-report-error" role="alert">{error}</p>}
    {set && <>
      {activeAnnotations.length > 0 && <ul className="run-annotations-list">{activeAnnotations.map(a => <li key={a.id} className="run-annotation-item"><div className="run-annotation-item-header"><span><strong>{a.label}</strong> · {annotationTime(a.timestamp, startedAt)} · {a.created_by}</span>{canEdit && <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void mutate(a.id)}>Delete annotation {a.label}</button>}</div>{a.text && <p>{a.text}</p>}</li>)}</ul>}
      {canEdit && startedAt && <form className="run-annotations-form" onSubmit={event => { event.preventDefault(); void mutate() }}><label>Annotation elapsed seconds<input required type="number" min={0} max={86400} step="0.001" value={elapsed} onChange={event => setElapsed(Number(event.target.value))} /></label><label>Annotation label<input required maxLength={256} value={label} onChange={event => setLabel(event.target.value)} /></label><label className="run-annotations-context-field">Annotation context<textarea maxLength={2048} value={text} onChange={event => setText(event.target.value)} /></label><div className="run-annotations-form-actions"><button type="submit" className="btn btn-secondary btn-sm" disabled={busy || !label.trim()}>Add timeline annotation</button></div></form>}
    </>}
  </details>
}
