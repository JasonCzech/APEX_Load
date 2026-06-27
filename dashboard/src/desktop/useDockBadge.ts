// Reflects the active-run count on the desktop dock/taskbar badge. Inert in the
// browser and on platforms whose bridge lacks SetDockBadge.

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'

// useDockBadge sets the dock/taskbar badge to the active-run count (cleared at
// 0). Only calls the bridge on transitions.
export function useDockBadge(activeRunCount: number) {
  const lastLabel = useRef('')

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
    void bridge.SetDockBadge(label)
  }, [activeRunCount])
}
