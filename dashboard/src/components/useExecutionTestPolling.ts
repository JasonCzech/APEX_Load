import { Dispatch, SetStateAction, useEffect, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { isCompletedTestStatus } from '../utils/testStatusUtils'

export const EXECUTION_POLL_INTERVAL_MS = 2000
// Archive publication is durably retried by the controller on a one-minute
// sweep. Keep terminal polling alive across two sweep opportunities, then stop
// so a permanently unavailable object store cannot leave the page polling
// forever. A later page visit still performs a fresh load.
export const EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS = 2 * 60_000

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

    setTest(null)
    setError('')
    lastPayloadSignatureRef.current = ''

    let stopped = false
    let inFlight = false
    let archiveFinalizationStartedAt: number | null = null

    const load = async () => {
      if (stopped || inFlight) return
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      inFlight = true

      try {
        const data = await api.getTest(testId)
        if (stopped) return
        const signature = JSON.stringify(data)
        if (signature !== lastPayloadSignatureRef.current) {
          lastPayloadSignatureRef.current = signature
          setTest(data)
        }
        setError('')
        if (stopOnTerminal && isCompletedTestStatus(data.status)) {
          if (data.archive) {
            stopped = true
            clearInterval(interval)
          } else {
            archiveFinalizationStartedAt ??= Date.now()
            if (Date.now() - archiveFinalizationStartedAt >= EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS) {
              stopped = true
              clearInterval(interval)
            }
          }
        } else {
          archiveFinalizationStartedAt = null
        }
      } catch (error: unknown) {
        if (stopped) return
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
