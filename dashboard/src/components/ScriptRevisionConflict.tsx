import { useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { authoringChangedPaths } from '../utils/scriptAuthoringRevision'
import './ScriptRevisionConflict.css'

interface Props {
  reason: string
  current: ScriptAuthoringBaseline | null
  draft: ScriptDefinition
  loading: boolean
  onLoad: () => void
  onReload: () => void
  onReapply: () => void
}

export default function ScriptRevisionConflict({ reason, current, draft, loading, onLoad, onReload, onReapply }: Props) {
  const [showDiff, setShowDiff] = useState(false)
  const paths = current ? authoringChangedPaths(current.projection.script, draft) : []
  return <section className="script-revision-conflict" aria-label="Script save reconciliation">
    <h3>Review the current script before saving</h3>
    <p role="alert">{reason}</p>
    <p>Your editable draft is retained. Loading the current baseline does not retry a save or replace the draft.</p>
    <div className="script-revision-conflict-actions">
      <button type="button" className="btn btn-secondary" disabled={loading} onClick={onLoad}>{loading ? 'Loading current baseline…' : 'Load current baseline'}</button>
      <button type="button" className="btn btn-secondary" disabled={!current || loading} onClick={() => setShowDiff(value => !value)}>Diff</button>
      <button type="button" className="btn btn-secondary" disabled={!current || loading} onClick={onReapply}>Reapply draft</button>
      <button type="button" className="btn btn-ghost" disabled={!current || loading} onClick={onReload}>Reload script and discard draft</button>
    </div>
    {current && <p>Reapply carries local changes onto this reviewed baseline and retains its current placement. Locally changed fields or action arrays take precedence; review them before saving.</p>}
    {showDiff && current && <div aria-label="Script revision field diff"><p>Changed field paths only; values are withheld.</p>{paths.length ? <ul>{paths.map(path => <li key={path}>{path}</li>)}</ul> : <p>No document-field differences. Placement or another save may still have changed the revision.</p>}</div>}
  </section>
}
