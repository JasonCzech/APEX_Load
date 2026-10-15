// Unsaved work on a native close. The window's close button, Cmd+Q and
// File > Quit never reach the page, so the desktop shell holds the close while
// this guard is registered and sends "desktop:close-requested". The page takes
// the request at once (so the shell waits instead of quitting) and runs it
// through the same prompt that guards in-app navigation: once the work is
// saved or discarded, it confirms the quit; "Keep editing" leaves the app open.
// A quit that has to ask brings the window forward, so a Dock Quit with the
// window minimized or hidden is answered where the person can see it.
// View > Reload replaces the page the same way and arrives as
// "desktop:reload-requested": once the work is resolved the page reloads itself.
// Inert in the browser, which has beforeunload for this, and while disabled
// (a nested guard provider, which leaves quitting to the app's own).

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'
import { reloadPage } from './reloadPage'
import { getWailsEventRuntime } from './wailsRuntime'

export const DESKTOP_CLOSE_REQUESTED_EVENT = 'desktop:close-requested'
export const DESKTOP_RELOAD_REQUESTED_EVENT = 'desktop:reload-requested'

const REQUEST_ID = /^[1-9][0-9]{0,19}$/

export function useDesktopCloseGuard(runGuarded: (proceed: () => void) => void, enabled = true) {
  const runGuardedRef = useRef(runGuarded)
  runGuardedRef.current = runGuarded

  useEffect(() => {
    if (!enabled) return undefined
    const bridge = getDesktopBridge()
    const runtime = getWailsEventRuntime()
    if (!bridge?.EnableCloseGuard || !bridge.ConfirmClose || !runtime?.EventsOn) return undefined

    let cancelled = false
    let token = ''
    // Take a shell request (so the shell waits for the answer) and run its
    // action through the unsaved-work prompt.
    const take = (requestId: unknown, action: (current: string, id: string) => void) => {
      const current = token
      if (!current || typeof requestId !== 'string' || !REQUEST_ID.test(requestId)) return
      void bridge.AcknowledgeCloseRequest?.(current, requestId)?.catch(() => undefined)
      let proceeded = false
      runGuardedRef.current(() => {
        proceeded = true
        action(current, requestId)
      })
      if (!proceeded) {
        runtime.WindowUnminimise?.()
        runtime.WindowShow?.()
      }
    }
    const cancelClose = runtime.EventsOn(DESKTOP_CLOSE_REQUESTED_EVENT, (requestId?: unknown) => {
      take(requestId, (current, id) => { void bridge.ConfirmClose?.(current, id)?.catch(() => undefined) })
    })
    const cancelReload = runtime.EventsOn(DESKTOP_RELOAD_REQUESTED_EVENT, (requestId?: unknown) => {
      take(requestId, () => reloadPage())
    })
    void bridge.EnableCloseGuard().then(next => {
      if (cancelled) {
        void bridge.DisableCloseGuard?.(next)?.catch(() => undefined)
        return
      }
      token = next
    }).catch(() => undefined)

    return () => {
      cancelled = true
      if (typeof cancelClose === 'function') cancelClose()
      if (typeof cancelReload === 'function') cancelReload()
      if (token) void bridge.DisableCloseGuard?.(token)?.catch(() => undefined)
    }
  }, [enabled])
}
