import { useCallback, useRef, useState } from 'react'

import { RunActionConfirmationKind } from './runActionConfirmation'

interface RunActionConfirmationModel {
  pendingAction: RunActionConfirmationKind | null
  /** What the pending action does to this run, said in its dialog. */
  pendingDetail: string
  /** Wraps a run-control handler so it opens the confirmation dialog first. */
  confirmRunAction: (action: RunActionConfirmationKind, run: () => void | Promise<void>, detail?: string) => () => void
  onConfirmRunAction: () => void
  onCancelRunAction: () => void
}

export function useRunActionConfirmation(): RunActionConfirmationModel {
  const [pending, setPending] = useState<{ action: RunActionConfirmationKind; detail: string } | null>(null)
  const pendingRunRef = useRef<(() => void | Promise<void>) | null>(null)

  const confirmRunAction = useCallback((action: RunActionConfirmationKind, run: () => void | Promise<void>, detail = '') => () => {
    pendingRunRef.current = run
    setPending({ action, detail })
  }, [])

  const onConfirmRunAction = useCallback(() => {
    const run = pendingRunRef.current
    pendingRunRef.current = null
    setPending(null)
    void run?.()
  }, [])

  const onCancelRunAction = useCallback(() => {
    pendingRunRef.current = null
    setPending(null)
  }, [])

  return { pendingAction: pending?.action ?? null, pendingDetail: pending?.detail ?? '', confirmRunAction, onConfirmRunAction, onCancelRunAction }
}
