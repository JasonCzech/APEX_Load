import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

/** Keep one credential editor mounted while its placement changes between review tabs. */
export default function ImportCredentialPlacement({ target, visible, children }: { target?: HTMLElement | null; visible: boolean; children: ReactNode }) {
  const fallback = useRef<HTMLDivElement>(null)
  const [host] = useState(() => typeof document === 'undefined' ? null : document.createElement('div'))
  useLayoutEffect(() => {
    if (!host) return
    const parent = target || fallback.current
    parent?.appendChild(host)
    return () => { host.remove() }
  }, [host, target])
  if (!host) return <div hidden={!visible}>{children}</div>
  return <><div ref={fallback} hidden={!visible} />{createPortal(children, host)}</>
}
