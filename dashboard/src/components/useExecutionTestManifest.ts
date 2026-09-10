import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestRunManifest, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

const LIVE_MANIFEST_REFRESH_MS = 5000

interface ManifestRequest {
  testID: string
  generation: number
  promise: Promise<void>
}

function shouldPollManifest(status?: string): boolean {
  return status === 'SHAKEOUT' || status === 'RUNNING' || status === 'PAUSED' || status === 'STOPPING' || status === 'INITIALIZING'
}

export function useExecutionTestManifest(test: TestSummary | null): {
  manifest: TestRunManifest | null
  manifestError: string
  manifestLoading: boolean
  reloadManifest: () => Promise<void>
} {
  const api = useApi()
  const [manifest, setManifest] = useState<TestRunManifest | null>(null)
  const [manifestError, setManifestError] = useState('')
  const [manifestLoading, setManifestLoading] = useState(false)

  const testID = test?.id || ''
  const testStatus = test?.status
  const archiveCompletedAt = test?.archive?.completed_at || ''
  const activeTestIDRef = useRef(testID)
  const requestGenerationRef = useRef(0)
  const inFlightRequestRef = useRef<ManifestRequest | null>(null)
  const mountedRef = useRef(true)
  activeTestIDRef.current = testID

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useLayoutEffect(() => {
    requestGenerationRef.current += 1
    // A new layout scope (including StrictMode's remount) must not reuse a
    // request whose generation was just invalidated, or loading never settles.
    inFlightRequestRef.current = null
    setManifest(null)
    setManifestError('')
    setManifestLoading(Boolean(testID))
  }, [testID])

  const requestManifest = useCallback((requestTestID: string, showLoading: boolean, clearManifestOnError: boolean) => {
    const existingRequest = inFlightRequestRef.current
    if (existingRequest?.testID === requestTestID) {
      if (showLoading) setManifestLoading(true)
      return existingRequest.promise
    }

    const requestGeneration = ++requestGenerationRef.current
    if (showLoading) setManifestLoading(true)
    setManifestError('')

    const promise = api.getTestManifest(requestTestID)
      .then(next => {
        if (
          mountedRef.current
          && activeTestIDRef.current === requestTestID
          && requestGenerationRef.current === requestGeneration
        ) setManifest(next)
      })
      .catch((error: unknown) => {
        if (
          mountedRef.current
          && activeTestIDRef.current === requestTestID
          && requestGenerationRef.current === requestGeneration
        ) {
          if (clearManifestOnError) setManifest(null)
          setManifestError(getErrorMessage(error, 'Unable to load test manifest'))
        }
      })
      .finally(() => {
        if (inFlightRequestRef.current?.generation === requestGeneration) {
          inFlightRequestRef.current = null
        }
        if (
          mountedRef.current
          && activeTestIDRef.current === requestTestID
          && requestGenerationRef.current === requestGeneration
        ) setManifestLoading(false)
      })

    inFlightRequestRef.current = { testID: requestTestID, generation: requestGeneration, promise }
    return promise
  }, [api])

  useEffect(() => {
    if (!testID) {
      setManifest(null)
      setManifestError('')
      setManifestLoading(false)
      return undefined
    }

    void requestManifest(testID, true, true)

    if (!shouldPollManifest(testStatus)) {
      return undefined
    }

    const interval = window.setInterval(() => {
      void requestManifest(testID, false, true)
    }, LIVE_MANIFEST_REFRESH_MS)

    return () => {
      window.clearInterval(interval)
    }
  }, [archiveCompletedAt, requestManifest, testID, testStatus])

  const reloadManifest = useCallback(async () => {
    if (!testID) return
    await requestManifest(testID, true, false)
  }, [requestManifest, testID])

  return {
    manifest,
    manifestError,
    manifestLoading,
    reloadManifest,
  }
}
