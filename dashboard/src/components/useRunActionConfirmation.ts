import { useCallback, useRef, useState } from 'react'

import { RunActionConfirmationKind } from './runActionConfirmation'

interface RunActionConfirmationModel {
  pendingAction: RunActionConfirmationKind | null
  /** Wraps a run-control handler so it opens the confirmation dialog first. */
  confirmRunAction: (action: RunActionConfirmationKind, run: () => void | Promise<void>) => () => void
  onConfirmRunAction: () => void
  onCancelRunAction: () => void
}

export function useRunActionConfirmation(): RunActionConfirmationModel {
  const [pendingAction, setPendingAction] = useState<RunActionConfirmationKind | null>(null)
  const pendingRunRef = useRef<(() => void | Promise<void>) | null>(null)

  const confirmRunAction = useCallback((action: RunActionConfirmationKind, run: () => void | Promise<void>) => () => {
    pendingRunRef.current = run
    setPendingAction(action)
  }, [])

  const onConfirmRunAction = useCallback(() => {
    const run = pendingRunRef.current
    pendingRunRef.current = null
    setPendingAction(null)
    void run?.()
  }, [])

  const onCancelRunAction = useCallback(() => {
    pendingRunRef.current = null
    setPendingAction(null)
  }, [])

  return { pendingAction, confirmRunAction, onConfirmRunAction, onCancelRunAction }
}
