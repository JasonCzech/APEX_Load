import { Dispatch, SetStateAction, useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, useApi } from '../hooks/useApi'
import { CapacityAdvisory, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

interface ExecutionActionBridgeModel {
  controlFeedback: string
  showControlFeedback: (message: string) => void
  startCapacityConflict: CapacityAdvisory | null
  startFeedback: string
  onDismissStartConflict: () => void
  onStart: () => Promise<void>
  onConfirmStartAnyway: () => Promise<void>
  onPause: () => Promise<void>
  onResume: () => Promise<void>
}

export function useExecutionActionBridge(
  testId: string,
  test: TestSummary | null,
  setTest: Dispatch<SetStateAction<TestSummary | null>>,
): ExecutionActionBridgeModel {
  const api = useApi()
  const [controlFeedback, setControlFeedback] = useState('')
  const [startCapacityConflict, setStartCapacityConflict] = useState<CapacityAdvisory | null>(null)
  const [startFeedback, setStartFeedback] = useState('')
  const clearFeedbackTimer = useRef<number | null>(null)

  const showControlFeedback = useCallback((message: string) => {
    setControlFeedback(message)
    if (clearFeedbackTimer.current) {
      clearTimeout(clearFeedbackTimer.current)
    }
    clearFeedbackTimer.current = window.setTimeout(() => {
      setControlFeedback('')
      clearFeedbackTimer.current = null
    }, 3000)
  }, [])

  useEffect(() => {
    if (test?.status && test.status !== 'QUEUED') {
      setStartCapacityConflict(null)
      setStartFeedback('')
    }
  }, [test?.status])

  useEffect(() => {
    return () => {
      if (clearFeedbackTimer.current) {
        clearTimeout(clearFeedbackTimer.current)
      }
    }
  }, [])

  const runStart = useCallback(async (override = false) => {
    setStartFeedback('')
    try {
      await api.startTest(testId, override ? { override_capacity_shortfall: true } : undefined)
      setStartCapacityConflict(null)
    } catch (error: unknown) {
      if (error instanceof ApiError && error.code === 'capacity_shortfall') {
        const capacityAdvisory = typeof error.payload === 'object' && error.payload !== null && 'capacity_advisory' in error.payload
          ? (error.payload as { capacity_advisory?: CapacityAdvisory }).capacity_advisory
          : undefined
        setStartCapacityConflict(capacityAdvisory || test?.capacity_advisory || null)
        setStartFeedback(getErrorMessage(error, 'Capacity advisory requires an explicit override before starting.'))
        return
      }
      setStartFeedback(getErrorMessage(error, 'Unable to start test'))
    }
  }, [api, test?.capacity_advisory, testId])

  const onPause = useCallback(async () => {
    try {
      await api.pauseTest(testId)
      showControlFeedback('Test paused')
      setTest((prev) => (prev ? { ...prev, status: 'PAUSED' } : null))
    } catch (error: unknown) {
      showControlFeedback(`Error pausing: ${getErrorMessage(error, 'Unable to pause test')}`)
    }
  }, [api, setTest, showControlFeedback, testId])

  const onResume = useCallback(async () => {
    try {
      await api.resumeTest(testId)
      showControlFeedback('Test resumed')
      setTest((prev) => (prev ? { ...prev, status: 'RUNNING' } : null))
    } catch (error: unknown) {
      showControlFeedback(`Error resuming: ${getErrorMessage(error, 'Unable to resume test')}`)
    }
  }, [api, setTest, showControlFeedback, testId])

  return {
    controlFeedback,
    showControlFeedback,
    startCapacityConflict,
    startFeedback,
    onDismissStartConflict: () => setStartCapacityConflict(null),
    onStart: () => runStart(false),
    onConfirmStartAnyway: () => runStart(true),
    onPause,
    onResume,
  }
}