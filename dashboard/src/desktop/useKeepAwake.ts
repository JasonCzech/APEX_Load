// Keeps the machine awake (via the desktop bridge) while load tests are running,
// so a long run isn't corrupted by the host sleeping. Inert in the browser.

import { useEffect, useRef } from 'react'

import { getDesktopBridge } from './desktopBridge'

// useKeepAwakeDuringRuns engages the OS sleep inhibitor while activeRunCount > 0
// and releases it when it returns to 0. It only calls the bridge on transitions.
export function useKeepAwakeDuringRuns(activeRunCount: number) {
  const lastWantAwake = useRef<boolean | null>(null)
  const bridgeQueue = useRef<Promise<void>>(Promise.resolve())

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
    // Wails dispatches each bound call on its own goroutine. Invoke transitions
    // serially so a rapid 0 → 1 → 0 cannot reach Go in reverse order and
    // leave the machine inhibited after the final run has ended.
    const operation = bridgeQueue.current.then(() => bridge.SetKeepAwake!(wantAwake))
    bridgeQueue.current = operation.catch(() => undefined)
  }, [activeRunCount])
}
