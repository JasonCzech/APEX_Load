import FormField from './ux/FormField'
import DialogLayer from './ux/DialogLayer'
import { useEffect, useId, useRef, useState } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import './ProjectModal.css'

interface Props {
  title?: string
  returnFocusSelector?: string
  initialName?: string
  namePlaceholder?: string
  onClose: () => void
  onSubmit: (name: string) => Promise<void> | void
}

// After this long in flight the save is assumed wedged (the create API has no
// request timeout) and dismissal is re-enabled — an operator must never be
// trapped behind the modal by a black-holed request.
const SLOW_SAVE_MS = 15_000

export default function ProjectModal({ returnFocusSelector, title = 'New Project', initialName = '', namePlaceholder = 'Application or service name', onClose, onSubmit }: Props) {
  const titleId = useId()
  const [name, setName] = useState(initialName)
  const [saving, setSaving] = useState(false)
  const [slowSave, setSlowSave] = useState(false)
  const [error, setError] = useState('')
  const dialogRef = useRef<HTMLFormElement | null>(null)
  const nameInputRef = useRef<HTMLInputElement | null>(null)
  // True once this dialog has been dismissed or unmounted. A save that was
  // abandoned via the slow-save escape can settle much later — its
  // continuation must not fire onClose again, or it would slam shut a NEW
  // dialog the operator has since reopened.
  const closedRef = useRef(false)

  useEffect(() => setName(initialName), [initialName])
  useEffect(() => {
    // React Strict Mode intentionally replays effect setup/cleanup during
    // development. Reset the marker on every setup so that replay does not
    // make a live dialog ignore its successful submit continuation.
    closedRef.current = false
    return () => {
      closedRef.current = true
    }
  }, [])

  useEffect(() => {
    if (!saving) {
      setSlowSave(false)
      return undefined
    }
    const timer = window.setTimeout(() => setSlowSave(true), SLOW_SAVE_MS)
    return () => window.clearTimeout(timer)
  }, [saving])

  // Dismissal is disabled while the create is in flight — closing mid-save
  // would drop the eventual outcome (an error would land on an unmounted
  // dialog, a success would silently switch the workspace) — but a hung
  // request re-enables it after the slow-save watchdog fires.
  const dismissLocked = saving && !slowSave
  const requestClose = () => {
    if (dismissLocked) return
    closedRef.current = true
    onClose()
  }

  useModalDialog({ returnFocusSelector, dialogRef, initialFocusRef: nameInputRef, onClose: dismissLocked ? undefined : requestClose })

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!name.trim()) return
    setSaving(true)
    setError('')
    try {
      await onSubmit(name.trim())
      if (closedRef.current) return
      closedRef.current = true
      onClose()
    } catch (cause: unknown) {
      if (closedRef.current) return
      // Keep the dialog open with the reason; closing silently would leave the
      // operator unsure whether the create committed.
      setError(cause instanceof Error && cause.message ? cause.message : 'Unable to save. Try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <DialogLayer><div className="project-modal-backdrop" {...dialogBackdropProps(requestClose)}>
      <form ref={dialogRef} className="project-modal" role="dialog" aria-modal="true" aria-label={title} onSubmit={submit}>
        <div className="project-modal-header">
          <h3>{title}</h3>
          <button type="button" className="project-modal-close" aria-label={`Close ${title}`} onClick={requestClose} disabled={dismissLocked}>×</button>
        </div>
        <FormField className="project-modal-field" label="Name" htmlFor={`${titleId}-name`}>
          <input id={`${titleId}-name`} ref={nameInputRef} value={name} onChange={(event) => setName(event.target.value)} placeholder={namePlaceholder} />
        </FormField>
        {error && <p className="project-modal-error" role="alert">{error}</p>}
        {saving && slowSave && (
          <p className="project-modal-error" role="status">
            Still saving. You can close this dialog — the create may still complete, so check the list before retrying.
          </p>
        )}
        <div className="project-modal-actions">
          <button type="button" className="btn btn-secondary" onClick={requestClose} disabled={dismissLocked}>Cancel</button>
          <button type="submit" className="btn btn-primary" disabled={!name.trim() || saving}>
            {saving ? 'Saving...' : title.startsWith('New ') ? `Create ${title.slice(4).toLowerCase()}` : 'Save changes'}
          </button>
        </div>
      </form>
    </div></DialogLayer>
  )
}
