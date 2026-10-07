import { useState } from 'react'
import type { AuthoringKind } from '../utils/authoringRevision'
import './AuthoringRevisionConflict.css'

export interface AuthoringDiffSection {
  label?: string
  paths: string[]
  empty: string
}

interface Props {
  kind: AuthoringKind
  reason: string
  hasCurrent: boolean
  loading: boolean
  // diff is computed only when shown; it lists bounded field paths, never values.
  diff: () => AuthoringDiffSection[]
  reapplyNote: string
  onLoad: () => void
  onReload: () => void
  onReapply: () => void
}

// AuthoringRevisionConflict is the shared Load / Diff / Reapply panel for a
// conditioned save that needs reconciliation. The draft is never replaced
// without an explicit choice and a save is never retried automatically.
export default function AuthoringRevisionConflict({ kind, reason, hasCurrent, loading, diff, reapplyNote, onLoad, onReload, onReapply }: Props) {
  const [showDiff, setShowDiff] = useState(false)
  const title = kind === 'script' ? 'Script' : 'Scenario'
  const sections = showDiff && hasCurrent ? diff() : []
  return <section className={`authoring-revision-conflict ${kind}-revision-conflict`} aria-label={`${title} save reconciliation`}>
    <h3>Review the current {kind} before saving</h3>
    <p role="alert">{reason}</p>
    <p>Your editable draft is retained. Loading the current baseline does not retry a save or replace the draft.</p>
    <div className="authoring-revision-conflict-actions">
      <button type="button" className="btn btn-secondary" disabled={loading} onClick={onLoad}>{loading ? 'Loading current baseline…' : 'Load current baseline'}</button>
      <button type="button" className="btn btn-secondary" disabled={!hasCurrent || loading} onClick={() => setShowDiff(value => !value)}>Diff</button>
      <button type="button" className="btn btn-secondary" disabled={!hasCurrent || loading} onClick={onReapply}>Reapply draft</button>
      <button type="button" className="btn btn-ghost" disabled={!hasCurrent || loading} onClick={onReload}>Reload {kind} and discard draft</button>
    </div>
    {hasCurrent && <p>{reapplyNote}</p>}
    {showDiff && hasCurrent && <div aria-label={`${title} revision field diff`}><p>Changed field paths only; values are withheld.</p>{sections.map((section, index) => <div key={section.label || index}>
      {section.label && <h4>{section.label}</h4>}
      {section.paths.length ? <ul>{section.paths.map(path => <li key={path}>{path}</li>)}</ul> : <p>{section.empty}</p>}
    </div>)}</div>}
  </section>
}
