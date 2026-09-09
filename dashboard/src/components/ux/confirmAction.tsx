import { useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { useModalDialog } from '../../hooks/useModalDialog'
import DialogLayer from './DialogLayer'

let pending = false

// eslint-disable-next-line react-refresh/only-export-components
function Confirmation({ message, finish }: { message: string; finish: (accepted: boolean) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  useModalDialog({ dialogRef: ref, initialFocusRef: cancelRef, onClose: () => finish(false) })
  const destructive = /delete|discard|remove|revoke/i.test(message)
  const label = /delete/i.test(message) ? 'Delete' : /discard/i.test(message) ? 'Discard' : /rotate/i.test(message) ? 'Rotate key' : 'Continue'
  return <DialogLayer><div className="modal-overlay"><div ref={ref} className="modal ux-confirmation" role="alertdialog" aria-modal="true" aria-labelledby="ux-confirmation-title" aria-describedby="ux-confirmation-message">
    <header className="modal-header"><h3 id="ux-confirmation-title">{destructive ? 'Confirm destructive action' : 'Confirm action'}</h3></header>
    <p id="ux-confirmation-message">{message}</p>
    <footer className="modal-actions"><button ref={cancelRef} className="btn btn-secondary" onClick={() => finish(false)}>Cancel</button><button className={`btn ${destructive ? 'btn-danger' : 'btn-primary'}`} onClick={() => finish(true)}>{label}</button></footer>
  </div></div></DialogLayer>
}

/** Reject concurrent requests and cancel if navigation makes the action stale. */
export function confirmAction(message: string): Promise<boolean> {
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
    root.render(<Confirmation message={message} finish={finish} />)
  })
}
