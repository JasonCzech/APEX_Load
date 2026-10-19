import { useEffect, useRef, useState, type ReactNode } from 'react'

import { suggestBoundaries } from './responseSelectionModel'
import './ActionChecks.css'

/** Text selected in a replayed response: where it sits in the body as sent, and the boundaries that save it. */
export interface ResponseSelectionValue { text: string; start: number; end: number; boundaries: { left: string; right: string } | null }

const MAX_SELECTION = 2000

/**
 * A replayed response whose selected text can become a check, a saved value,
 * or a value later steps use in its place. Only text found in the response as
 * it was sent qualifies; a formatted view's added spacing does not.
 */
export default function ResponseSelection({ body, bodyState = 'whole', status, checkedStatus, disabledReason, laterUses, onContains, onSave, onCorrelate, onStatus, children }: {
  body?: string
  /** How much of the body the replay kept: a partial one may go on past the selection, a redacted one hides text. */
  bodyState?: 'whole' | 'partial' | 'redacted'
  status?: number
  /** The status the step already checks for, if any. */
  checkedStatus?: number
  /** Why nothing can be added from this response (the script changed since), or ''. */
  disabledReason: string
  /** How many later requests send the text. */
  laterUses: (text: string) => number
  /** Each adds to the step and says whether it did (false: the step already had it). */
  onContains: (text: string) => boolean
  onSave: (selection: ResponseSelectionValue) => boolean
  onCorrelate: (selection: ResponseSelectionValue) => void
  onStatus: (status: number) => boolean
  children: ReactNode
}) {
  const area = useRef<HTMLDivElement | null>(null)
  const [selected, setSelected] = useState<ResponseSelectionValue | null>(null)
  const [done, setDone] = useState('')
  // The area only watches what is selected in it, by mouse or keyboard.
  useEffect(() => {
    const root = area.current
    if (!root) return undefined
    const capture = () => {
      const selection = window.getSelection()
      if (!selection || selection.isCollapsed || !root.contains(selection.anchorNode) || !root.contains(selection.focusNode)) return
      const text = selection.toString().trim()
      if (!text || text.length > MAX_SELECTION) { setSelected(null); return }
      const start = body ? body.indexOf(text) : -1
      const end = start < 0 ? -1 : start + text.length
      // Saving needs text as it was sent, around which boundaries find exactly it.
      const boundaries = body && start >= 0 && bodyState !== 'redacted' ? suggestBoundaries(body, start, end, bodyState === 'whole') : null
      setSelected({ text, start, end, boundaries })
      setDone('')
    }
    root.addEventListener('mouseup', capture)
    root.addEventListener('keyup', capture)
    return () => {
      root.removeEventListener('mouseup', capture)
      root.removeEventListener('keyup', capture)
    }
  }, [body, bodyState])
  const found = Boolean(selected && selected.start >= 0)
  const boundaries = selected?.boundaries ?? null
  const uses = selected ? laterUses(selected.text) : 0
  const cannotSave = !found ? '' : bodyState === 'redacted' ? 'The replay redacted parts of this response, so a value cannot be saved from it.' : !boundaries ? 'No boundaries around it find exactly this text, so it cannot be saved.' : ''
  const finish = (message: string) => { setDone(message); setSelected(null); window.getSelection()?.removeAllRanges() }
  const preview = selected ? (selected.text.length > 60 ? `${selected.text.slice(0, 60)}…` : selected.text) : ''
  return <div className="response-selection">
    {status ? <div className="response-selection-status">
      {checkedStatus !== undefined && checkedStatus !== status && checkedStatus >= 400
        // A step that expects an error is a negative test, as its source
        // test was; offering to expect another status would undo it.
        ? <p role="note">This step checks for status {checkedStatus}, an error response. The target answered {status}, so check the target and the inputs before changing the check.</p>
        : checkedStatus !== undefined && checkedStatus !== status && status >= 400
          // Nor does an error answer become what a passing step expects.
          ? <p role="note">This step checks for status {checkedStatus}. The target answered {status}, an error response, so fix the request or the target rather than the check.</p>
          : <button type="button" className="btn btn-sm btn-ghost" disabled={Boolean(disabledReason)} onClick={() => setDone(onStatus(status)
          ? checkedStatus !== undefined ? `Changed the status check to “Status is ${status}”.` : `Added the check “Status is ${status}”.`
          : `The step already checks that the status is ${status}.`)}>
          {checkedStatus !== undefined && checkedStatus !== status ? `Change the status check from ${checkedStatus} to ${status}` : `Check that the status is ${status}`}
        </button>}
    </div> : null}
    <div className="response-selection-content" ref={area}>{children}</div>
    {selected && <div className="response-selection-actions" role="group" aria-label="Use the selected text">
      <span>Selected “{preview}”{!found ? ': not in the response as it was sent, so select it in the raw text' : ''}</span>
      {disabledReason ? <span>{disabledReason}</span> : <>
        <button type="button" className="btn btn-sm btn-secondary" disabled={!found} onClick={() => finish(onContains(selected.text) ? `Added the check “Response contains ${preview}”.` : 'The step already checks that the response contains it.')}>Check that the response contains this</button>
        <button type="button" className="btn btn-sm btn-secondary" disabled={!boundaries} onClick={() => finish(onSave(selected) ? 'Saved the value; it is in the step’s Saved values.' : 'The step already saves this value.')}>Save this as a value</button>
        <button type="button" className="btn btn-sm btn-secondary" disabled={!boundaries || uses === 0} onClick={() => { onCorrelate(selected); setSelected(null) }}>Use this value in later steps{uses ? ` (${uses})` : ''}</button>
        {cannotSave && <span>{cannotSave}</span>}
      </>}
    </div>}
    {done && <p role="status" className="response-selection-done">{done}</p>}
  </div>
}
