import { useEffect, useId, useRef, useState } from 'react'

import type { DesktopStatusResponse } from '../types/library'

/** Remote script libraries this desktop app can reach, in words. */
function formatDesktopRemoteLibraries(status?: DesktopStatusResponse | null): string {
  const count = status?.remote_libraries?.count || 0
  if (count === 0) return 'None connected'
  const healthy = status?.remote_libraries?.statuses?.healthy || 0
  return `${healthy} of ${count} healthy`
}

interface DesktopStatusMenuProps {
  status: DesktopStatusResponse | null
  onOpenBrowser?: () => void
  onOpenLogs?: () => void
  onOpenAppData?: () => void
}

/**
 * The desktop app's own status and shortcuts in one top-bar control, so the
 * single top-bar row keeps room for page actions.
 */
export default function DesktopStatusMenu({ status, onOpenBrowser, onOpenLogs, onOpenAppData }: DesktopStatusMenuProps) {
  const [open, setOpen] = useState(false)
  const panelId = useId()
  const containerRef = useRef<HTMLDivElement>(null)
  const toggleRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      toggleRef.current?.focus()
    }
    window.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const address = status?.browser_url || status?.api_address
  const run = (action?: () => void) => () => {
    setOpen(false)
    action?.()
  }

  return (
    <div className="desktop-status-menu" ref={containerRef}>
      <button
        ref={toggleRef}
        className="btn btn-sm btn-ghost desktop-status-toggle"
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen(value => !value)}
      >
        Desktop <span aria-hidden="true">▾</span>
      </button>
      {open && (
        <div id={panelId} className="desktop-status-popover" role="group" aria-label="Desktop app">
          <dl>
            <div>
              <dt>Local API</dt>
              <dd>{address || 'Not reported'}</dd>
            </div>
            <div>
              <dt>Remote script libraries</dt>
              <dd>{formatDesktopRemoteLibraries(status)}</dd>
            </div>
          </dl>
          <div className="desktop-status-actions">
            {onOpenBrowser && <button className="btn btn-sm btn-secondary" type="button" onClick={run(onOpenBrowser)}>Open in browser</button>}
            {onOpenLogs && <button className="btn btn-sm btn-ghost" type="button" onClick={run(onOpenLogs)}>Open logs folder</button>}
            {onOpenAppData && <button className="btn btn-sm btn-ghost" type="button" onClick={run(onOpenAppData)}>Open app data folder</button>}
          </div>
        </div>
      )}
    </div>
  )
}
