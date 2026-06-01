import { Dispatch, SetStateAction, useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, useApi } from '../hooks/useApi'
import { CapacityAdvisory, ScriptShakeoutReport, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

interface ExecutionActionBridgeModel {
  controlFeedback: string
  showControlFeedback: (message: string) => void
  startCapacityConflict: CapacityAdvisory | null
  startShakeoutConflict: ScriptShakeoutReport | null
  startFeedback: string
  onDismissStartConflict: () => void
  onStart: () => Promise<void>
  onConfirmStartAnyway: () => Promise<void>
  onContinueAfterShakeoutFailure: () => Promise<void>
  onStop: () => Promise<void>
  onAbort: () => Promise<void>
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
  const [startShakeoutConflict, setStartShakeoutConflict] = useState<ScriptShakeoutReport | null>(null)
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
    if (test?.status && test.status !== 'QUEUED' && test.status !== 'PENDING' && test.status !== 'SHAKEOUT' && test.status !== 'SHAKEOUT_REVIEW') {
      setStartCapacityConflict(null)
      setStartShakeoutConflict(null)
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

  const runStart = useCallback(async (options?: { overrideCapacity?: boolean; overrideShakeout?: boolean }) => {
    setStartFeedback('')
    try {
      const startOptions = options?.overrideCapacity || options?.overrideShakeout
        ? {
          ...(options.overrideCapacity ? { override_capacity_shortfall: true } : {}),
          ...(options.overrideShakeout ? { override_script_shakeout_failure: true } : {}),
        }
        : undefined
      const response = await api.startTest(testId, startOptions)
      setStartCapacityConflict(null)
      setStartShakeoutConflict(null)
      if (response.status === 'pending') {
        setStartFeedback(response.message || 'Queued until project capacity is available.')
        setTest((prev) => (prev ? {
          ...prev,
          status: 'PENDING',
          pending_reason: response.message || prev.pending_reason,
          pending_since: prev.pending_since || new Date().toISOString(),
        } : null))
        return
      }
      setTest((prev) => (prev ? { ...prev, status: 'INITIALIZING' } : null))
    } catch (error: unknown) {
      if (error instanceof ApiError && error.code === 'capacity_shortfall') {
        const capacityAdvisory = typeof error.payload === 'object' && error.payload !== null && 'capacity_advisory' in error.payload
          ? (error.payload as { capacity_advisory?: CapacityAdvisory }).capacity_advisory
          : undefined
        setStartCapacityConflict(capacityAdvisory || test?.capacity_advisory || null)
        setStartFeedback(getErrorMessage(error, 'Capacity advisory requires an explicit override before starting.'))
        return
      }
      if (error instanceof ApiError && error.code === 'script_shakeout_failed') {
        const report = typeof error.payload === 'object' && error.payload !== null && 'script_shakeout' in error.payload
          ? (error.payload as { script_shakeout?: ScriptShakeoutReport }).script_shakeout
          : undefined
        setStartShakeoutConflict(report || null)
        const message = getErrorMessage(error, 'Script shakeout failed; full load test has not started.')
        setStartFeedback(message)
        setTest((prev) => (prev ? {
          ...prev,
          status: 'SHAKEOUT_REVIEW',
          script_shakeout: true,
          error: report?.summary || message,
        } : null))
        return
      }
      setStartFeedback(getErrorMessage(error, 'Unable to start test'))
    }
  }, [api, setTest, test?.capacity_advisory, testId])

  const onPause = useCallback(async () => {
    try {
      await api.pauseTest(testId)
      showControlFeedback('Test paused')
      setTest((prev) => (prev ? { ...prev, status: 'PAUSED' } : null))
    } catch (error: unknown) {
      showControlFeedback(`Error pausing: ${getErrorMessage(error, 'Unable to pause test')}`)
    }
  }, [api, setTest, showControlFeedback, testId])

  const onStop = useCallback(async () => {
    try {
      await api.stopTest(testId)
      setTest((prev) => (prev ? { ...prev, status: 'STOPPING' } : null))
    } catch (error: unknown) {
      showControlFeedback(`Error stopping: ${getErrorMessage(error, 'Unable to stop test')}`)
    }
  }, [api, setTest, showControlFeedback, testId])

  const onAbort = useCallback(async () => {
    try {
      await api.abortTest(testId)
      setTest((prev) => (prev ? { ...prev, status: 'ABORTED' } : null))
    } catch (error: unknown) {
      showControlFeedback(`Error aborting: ${getErrorMessage(error, 'Unable to abort test')}`)
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
    startShakeoutConflict,
    startFeedback,
    onDismissStartConflict: () => setStartCapacityConflict(null),
    onStart: () => runStart(),
    onConfirmStartAnyway: () => runStart({ overrideCapacity: true }),
    onContinueAfterShakeoutFailure: () => runStart({ overrideShakeout: true }),
    onStop,
    onAbort,
    onPause,
    onResume,
  }
}
