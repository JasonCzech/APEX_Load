import { useEffect, useRef, useState } from 'react'

import { getDesktopBridge } from './desktopBridge'
import { DESKTOP_OPEN_ACTIVATION_EVENT } from './useDesktopShellEvents'

type DesktopRoutePersistenceOptions = {
  enabled: boolean
  route: string
  restoreRoute: (route: string) => void
}

// Restores the prior route before allowing persistence. Without this ordering,
// the initial dashboard render overwrites the saved route before GetLastRoute
// resolves, making desktop session restoration unreliable.
export function useDesktopRoutePersistence({ enabled, route, restoreRoute }: DesktopRoutePersistenceOptions): void {
  const restoreStarted = useRef(false)
  const explicitActivation = useRef(false)
  const persistenceQueue = useRef<Promise<void>>(Promise.resolve())
  const [restoreFinished, setRestoreFinished] = useState(false)

  useEffect(() => {
    if (!enabled || typeof window === 'undefined') return
    const markExplicitActivation = () => { explicitActivation.current = true }
    window.addEventListener(DESKTOP_OPEN_ACTIVATION_EVENT, markExplicitActivation)
    return () => window.removeEventListener(DESKTOP_OPEN_ACTIVATION_EVENT, markExplicitActivation)
  }, [enabled])

  useEffect(() => {
    if (!enabled || restoreStarted.current) return
    restoreStarted.current = true

    if (typeof window !== 'undefined' && window.location.hash) {
      setRestoreFinished(true)
      return
    }

    const bridge = getDesktopBridge()
    if (!bridge?.GetLastRoute) {
      setRestoreFinished(true)
      return
    }

    void bridge.GetLastRoute()
      .then(savedRoute => {
        if (savedRoute && !explicitActivation.current) restoreRoute(savedRoute)
      })
      .catch(() => undefined)
      .finally(() => setRestoreFinished(true))
  }, [enabled, restoreRoute])

  useEffect(() => {
    if (!enabled || !restoreFinished) return
    const setLastRoute = getDesktopBridge()?.SetLastRoute
    if (typeof setLastRoute !== 'function') return
    // Wails runs bridge invocations on independent goroutines. Serialize route
    // writes at the source so rapid navigation cannot persist an older route
    // merely because its Go call acquired routeMu last.
    const operation = persistenceQueue.current.then(() => setLastRoute(route))
    persistenceQueue.current = operation.catch(() => undefined)
  }, [enabled, restoreFinished, route])
}
