// Desktop-shell event wiring for the dashboard: native menu events and
// external-link routing. Inert in the browser (no Wails runtime / bridge).

import { useEffect } from 'react'

import { getDesktopBridge } from './desktopBridge'
import { getWailsEventRuntime } from './wailsRuntime'

const EXTERNAL_LINK_RE = /^(https?:|mailto:)/i

export interface DesktopShellEventHandlers {
  onMenuAbout?: () => void
  onMenuAction?: (action: string) => void
  onOpenFile?: (path: string) => void
  onOpenURL?: (url: string) => void
  onOpenRecent?: (route: string) => void
  onRecentsChanged?: () => void
}

// useDesktopShellEvents subscribes to the native menu/open events and routes
// external links to the system browser. Each subscription is opt-in via the
// matching handler so the host page only wires what it can act on.
export function useDesktopShellEvents(handlers: DesktopShellEventHandlers) {
  const { onMenuAbout, onMenuAction, onOpenFile, onOpenURL, onOpenRecent, onRecentsChanged } = handlers

  // Native menu + OS open events.
  useEffect(() => {
    const runtime = getWailsEventRuntime()
    if (!runtime?.EventsOn) return undefined

    const cancels: Array<() => void> = []
    const on = (event: string, cb: (arg?: unknown) => void) => {
      const cancel = runtime.EventsOn?.(event, cb)
      if (typeof cancel === 'function') cancels.push(cancel)
    }

    if (onMenuAbout) on('menu:about', () => onMenuAbout())
    if (onMenuAction) on('menu:action', action => { if (typeof action === 'string') onMenuAction(action) })
    if (onOpenFile) on('desktop:open-file', path => { if (typeof path === 'string') onOpenFile(path) })
    if (onOpenURL) on('desktop:open-url', url => { if (typeof url === 'string') onOpenURL(url) })
    if (onOpenRecent) on('desktop:open-recent', route => { if (typeof route === 'string') onOpenRecent(route) })
    if (onRecentsChanged) on('desktop:recents-changed', () => onRecentsChanged())

    return () => cancels.forEach(cancel => cancel())
  }, [onMenuAbout, onMenuAction, onOpenFile, onOpenURL, onOpenRecent, onRecentsChanged])

  // Route external http(s)/mailto links to the system browser so they never
  // replace the SPA inside the webview.
  useEffect(() => {
    const bridge = getDesktopBridge()
    if (typeof document === 'undefined' || typeof bridge?.OpenURL !== 'function') return undefined

    const handler = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0) return
      const target = event.target as HTMLElement | null
      const anchor = target?.closest?.('a') as HTMLAnchorElement | null
      const href = anchor?.getAttribute('href') || ''
      if (!EXTERNAL_LINK_RE.test(href)) return
      event.preventDefault()
      void bridge.OpenURL?.(href)
    }
    document.addEventListener('click', handler, { capture: true })
    return () => document.removeEventListener('click', handler, { capture: true })
  }, [])
}
