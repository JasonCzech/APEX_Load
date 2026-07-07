import { Dispatch, SetStateAction, useEffect, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { isCompletedTestStatus } from '../utils/testStatusUtils'

export const EXECUTION_POLL_INTERVAL_MS = 2000

interface ExecutionTestPollingModel {
  test: TestSummary | null
  setTest: Dispatch<SetStateAction<TestSummary | null>>
  error: string
}

interface ExecutionTestPollingOptions {
  enabled?: boolean
  stopOnTerminal?: boolean
}

export function useExecutionTestPolling(testId: string, options: ExecutionTestPollingOptions = {}): ExecutionTestPollingModel {
  const api = useApi()
  const enabled = options.enabled ?? true
  const stopOnTerminal = options.stopOnTerminal ?? true
  const [test, setTest] = useState<TestSummary | null>(null)
  const [error, setError] = useState('')
  const lastPayloadSignatureRef = useRef('')

  useEffect(() => {
    if (!enabled || !testId) {
      setTest(null)
      setError('')
      lastPayloadSignatureRef.current = ''
      return
    }

    let stopped = false
    let inFlight = false

    const load = async () => {
      if (stopped || inFlight) return
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      inFlight = true

      try {
        const data = await api.getTest(testId)
        const signature = JSON.stringify(data)
        if (signature !== lastPayloadSignatureRef.current) {
          lastPayloadSignatureRef.current = signature
          setTest(data)
        }
        if (stopOnTerminal && isCompletedTestStatus(data.status)) {
          stopped = true
          clearInterval(interval)
        }
      } catch (error: unknown) {
        setError(getErrorMessage(error, 'Unable to load test execution details'))
      } finally {
        inFlight = false
      }
    }

    const interval = setInterval(load, EXECUTION_POLL_INTERVAL_MS)
    load()

    const handleVisibilityChange = () => {
      if (!stopped && typeof document !== 'undefined' && document.visibilityState === 'visible') {
        load()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      stopped = true
      if (interval) clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api, enabled, stopOnTerminal, testId])

  return {
    test,
    setTest,
    error,
  }
}
