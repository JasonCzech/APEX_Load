// Unsaved work on a native close. The window's close button, Cmd+Q and
// File > Quit never reach the page, so the desktop shell holds the close while
// this guard is registered and sends "desktop:close-requested". The page takes
// the request at once (so the shell waits instead of quitting) and runs it
// through the same prompt that guards in-app navigation: once the work is
// saved or discarded, it confirms the quit; "Keep editing" leaves the app open.
// A quit that has to ask brings the window forward, so a Dock Quit with the
// window minimized or hidden is answered where the person can see it.
// Inert in the browser, which has beforeunload for this, and while disabled
// (a nested guard provider, which leaves quitting to the app's own).

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'
import { getWailsEventRuntime } from './wailsRuntime'

export const DESKTOP_CLOSE_REQUESTED_EVENT = 'desktop:close-requested'

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
    const cancelEvents = runtime.EventsOn(DESKTOP_CLOSE_REQUESTED_EVENT, (requestId?: unknown) => {
      const current = token
      if (!current || typeof requestId !== 'string' || !REQUEST_ID.test(requestId)) return
      void bridge.AcknowledgeCloseRequest?.(current, requestId)?.catch(() => undefined)
      let quitting = false
      runGuardedRef.current(() => {
        quitting = true
        void bridge.ConfirmClose?.(current, requestId)?.catch(() => undefined)
      })
      if (!quitting) {
        runtime.WindowUnminimise?.()
        runtime.WindowShow?.()
      }
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
      if (typeof cancelEvents === 'function') cancelEvents()
      if (token) void bridge.DisableCloseGuard?.(token)?.catch(() => undefined)
    }
  }, [enabled])
}
