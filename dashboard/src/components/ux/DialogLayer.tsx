import { type ReactNode, useLayoutEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import './ux.css'

/** A body-level layer keeps dialogs outside transformed editors and sticky app chrome. */
export default function DialogLayer({ children }: { children: ReactNode }) {
  const [host] = useState(() => {
    const node = document.createElement('div')
    node.className = 'ux-dialog-layer'
    return node
  })
  useLayoutEffect(() => {
    document.body.appendChild(host)
    return () => host.remove()
  }, [host])
  return createPortal(children, host)
}
