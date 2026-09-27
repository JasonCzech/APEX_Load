import { useEffect, useState } from 'react'
import type { LiveMetrics } from '../types'
import { liveObservationState } from '../utils/liveObservation'

export function useLiveObservation(status: string, metrics?: LiveMetrics) {
  const [, tick] = useState(0)
  const state = liveObservationState(status, metrics, Date.now())
  // A stalled socket and failed polls must still age the rendered observation.
  useEffect(() => {
    if (state !== 'current') return
    const timer = window.setInterval(() => tick(value => value + 1), 1000)
    return () => window.clearInterval(timer)
  }, [state])
  return state
}
