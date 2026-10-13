import { useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { useModalDialog } from '../hooks/useModalDialog'
import type { ValueUse } from './responseSelectionModel'
import DialogLayer from './ux/DialogLayer'
import './ActionChecks.css'

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/**
 * Review before a value replaces its recorded copies: the value is saved on
 * the step that received it, and each chosen later use reads it instead.
 */
export default function CorrelationReviewDialog({ value, sourceName, suggestedName, taken, uses, onApply, onClose }: {
  value: string
  sourceName: string
  suggestedName: string
  /** Names the script already uses for variables. */
  taken: ReadonlySet<string>
  uses: ValueUse[]
  onApply: (name: string, uses: ValueUse[]) => void
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const nameRef = useRef<HTMLInputElement | null>(null)
  useModalDialog({ dialogRef, initialFocusRef: nameRef, onClose })
  const [name, setName] = useState(suggestedName)
  const [chosen, setChosen] = useState<boolean[]>(() => uses.map(() => true))
  const problem = !NAME.test(name.trim()) ? 'Use a letter or underscore, then letters, numbers or underscores.' : taken.has(name.trim()) ? `The script already uses ${name.trim()}.` : ''
  const count = chosen.filter(Boolean).length
  const preview = value.length > 60 ? `${value.slice(0, 60)}…` : value
  return createPortal(<DialogLayer><div className="modal-overlay"><div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="correlation-review-title" className="modal correlation-review-dialog">
    <h3 id="correlation-review-title">Use this value in later steps</h3>
    <p>Save “{preview}” from {sourceName} each time it runs, and send it where later steps send the recorded value.</p>
    <label>Save as<input ref={nameRef} value={name} aria-invalid={Boolean(problem)} aria-describedby={problem ? 'correlation-review-problem' : undefined} onChange={event => setName(event.target.value)} /></label>
    {problem && <p id="correlation-review-problem" role="alert">{problem}</p>}
    <fieldset className="correlation-review-uses"><legend>Replace with <code>{`{{var.${name.trim() || 'name'}}}`}</code></legend>
      {uses.map((use, at) => <label key={at} className="correlation-review-use">
        <input type="checkbox" checked={chosen[at]} onChange={event => setChosen(chosen.map((value, index) => index === at ? event.target.checked : value))} />
        <span><strong>{use.stepName}</strong> · {use.fieldLabel}: <code>{use.before}<mark>{value}</mark>{use.after}</code></span>
      </label>)}
    </fieldset>
    <p role="status">{count} of {uses.length} {uses.length === 1 ? 'use' : 'uses'} will read the saved value. Undo puts the recorded value back.</p>
    <div className="author-edit-toolbar">
      <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
      <button type="button" className="btn btn-primary" disabled={Boolean(problem) || count === 0} onClick={() => onApply(name.trim(), uses.filter((_use, at) => chosen[at]))}>Replace {count} {count === 1 ? 'use' : 'uses'}</button>
    </div>
  </div></div></DialogLayer>, document.body)
}
