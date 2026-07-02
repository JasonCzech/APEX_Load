// Keeps the machine awake (via the desktop bridge) while load tests are running,
// so a long run isn't corrupted by the host sleeping. Inert in the browser.

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'

// useKeepAwakeDuringRuns engages the OS sleep inhibitor while activeRunCount > 0
// and releases it when it returns to 0. It only calls the bridge on transitions.
export function useKeepAwakeDuringRuns(activeRunCount: number) {
  const lastWantAwake = useRef<boolean | null>(null)

  useEffect(() => {
    const bridge = getDesktopBridge()
    if (typeof bridge?.SetKeepAwake !== 'function') {
      return
    }
    const wantAwake = activeRunCount > 0
    if (lastWantAwake.current === wantAwake) {
      return
    }
    lastWantAwake.current = wantAwake
    void bridge.SetKeepAwake(wantAwake)
  }, [activeRunCount])
}
