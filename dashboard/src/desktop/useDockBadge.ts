// Reflects the active-run count on the desktop dock/taskbar badge. Inert in the
// browser and on platforms whose bridge lacks SetDockBadge.

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'

// useDockBadge sets the dock/taskbar badge to the active-run count (cleared at
// 0). Only calls the bridge on transitions.
export function useDockBadge(activeRunCount: number) {
  const lastLabel = useRef<string | null>(null)
  const bridgeQueue = useRef<Promise<void>>(Promise.resolve())

  useEffect(() => {
    const bridge = getDesktopBridge()
    if (typeof bridge?.SetDockBadge !== 'function') {
      return
    }
    const label = activeRunCount > 0 ? String(activeRunCount) : ''
    if (lastLabel.current === label) {
      return
    }
    lastLabel.current = label
    // Bound Wails calls run independently on Go goroutines. Preserve renderer
    // transition order so an older badge update cannot finish last and regress
    // the visible count.
    const operation = bridgeQueue.current.then(() => bridge.SetDockBadge!(label))
    bridgeQueue.current = operation.catch(() => undefined)
  }, [activeRunCount])
}
