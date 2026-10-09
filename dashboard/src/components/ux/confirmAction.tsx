import { useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { useModalDialog } from '../../hooks/useModalDialog'
import DialogLayer from './DialogLayer'

let pending = false

/**
 * Wording for a confirmation. Destructive actions must pass all three, so the
 * dialog names the action and styles its button as dangerous; without options
 * the dialog is a neutral "Continue".
 */
export interface ConfirmActionOptions {
  title?: string
  confirmLabel?: string
  destructive?: boolean
}

// eslint-disable-next-line react-refresh/only-export-components
function Confirmation({ message, options, finish }: { message: string; options?: ConfirmActionOptions; finish: (accepted: boolean) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  useModalDialog({ dialogRef: ref, initialFocusRef: cancelRef, onClose: () => finish(false) })
  // No wording is inferred from the message: it quotes user-chosen names
  // ("Start 'Delete account flow'?") that would misfire a keyword match.
  const destructive = options?.destructive ?? false
  const label = options?.confirmLabel ?? 'Continue'
  const title = options?.title ?? 'Confirm action'
  return <DialogLayer><div className="modal-overlay"><div ref={ref} className="modal ux-confirmation" role="alertdialog" aria-modal="true" aria-labelledby="ux-confirmation-title" aria-describedby="ux-confirmation-message">
    <header className="modal-header"><h3 id="ux-confirmation-title">{title}</h3></header>
    <p id="ux-confirmation-message">{message}</p>
    <footer className="modal-actions"><button ref={cancelRef} className="btn btn-secondary" onClick={() => finish(false)}>Cancel</button><button className={`btn ${destructive ? 'btn-danger' : 'btn-primary'}`} onClick={() => finish(true)}>{label}</button></footer>
  </div></div></DialogLayer>
}

/** Reject concurrent requests and cancel if navigation makes the action stale. */
export function confirmAction(message: string, options?: ConfirmActionOptions): Promise<boolean> {
  if (pending) return Promise.resolve(false)
  pending = true
  return new Promise(resolve => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    let finished = false
    const finish = (accepted: boolean) => {
      if (finished) return
      finished = true
      window.removeEventListener('hashchange', cancel)
      window.removeEventListener('popstate', cancel)
      root.unmount()
      host.remove()
      pending = false
      resolve(accepted)
    }
    const cancel = () => finish(false)
    window.addEventListener('hashchange', cancel)
    window.addEventListener('popstate', cancel)
    root.render(<Confirmation message={message} options={options} finish={finish} />)
  })
}
