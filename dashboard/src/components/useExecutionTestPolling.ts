import { Dispatch, SetStateAction, useEffect, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

export const EXECUTION_POLL_INTERVAL_MS = 2000

interface ExecutionTestPollingModel {
  test: TestSummary | null
  setTest: Dispatch<SetStateAction<TestSummary | null>>
  error: string
}

export function useExecutionTestPolling(testId: string): ExecutionTestPollingModel {
  const api = useApi()
  const [test, setTest] = useState<TestSummary | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }

      try {
        const data = await api.getTest(testId)
        setTest(data)
      } catch (error: unknown) {
        setError(getErrorMessage(error, 'Unable to load test execution details'))
      }
    }

    load()
    const interval = setInterval(load, EXECUTION_POLL_INTERVAL_MS)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        load()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api, testId])

  return {
    test,
    setTest,
    error,
  }
}