import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestRunManifest, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

const LIVE_MANIFEST_REFRESH_MS = 5000

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
  activeTestIDRef.current = testID

  useLayoutEffect(() => {
    requestGenerationRef.current += 1
    setManifest(null)
    setManifestError('')
    setManifestLoading(Boolean(testID))
  }, [testID])

  useEffect(() => {
    if (!testID) {
      setManifest(null)
      setManifestError('')
      setManifestLoading(false)
      return undefined
    }

    let cancelled = false

    const load = async (showLoading: boolean) => {
      const requestGeneration = ++requestGenerationRef.current
      if (showLoading) setManifestLoading(true)
      setManifestError('')
      try {
        const next = await api.getTestManifest(testID)
        if (
          !cancelled
          && activeTestIDRef.current === testID
          && requestGenerationRef.current === requestGeneration
        ) setManifest(next)
      } catch (error: unknown) {
        if (
          !cancelled
          && activeTestIDRef.current === testID
          && requestGenerationRef.current === requestGeneration
        ) {
          setManifest(null)
          setManifestError(getErrorMessage(error, 'Unable to load test manifest'))
        }
      } finally {
        if (
          !cancelled
          && activeTestIDRef.current === testID
          && requestGenerationRef.current === requestGeneration
        ) setManifestLoading(false)
      }
    }

    void load(true)

    if (!shouldPollManifest(testStatus)) {
      return () => {
        cancelled = true
      }
    }

    const interval = window.setInterval(() => {
      void load(false)
    }, LIVE_MANIFEST_REFRESH_MS)

    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [api, archiveCompletedAt, testID, testStatus])

  const reloadManifest = useCallback(async () => {
    if (!testID) return
    const requestTestID = testID
    const requestGeneration = ++requestGenerationRef.current
    setManifestLoading(true)
    setManifestError('')
    try {
      const next = await api.getTestManifest(requestTestID)
      if (
        activeTestIDRef.current === requestTestID
        && requestGenerationRef.current === requestGeneration
      ) setManifest(next)
    } catch (error: unknown) {
      if (
        activeTestIDRef.current === requestTestID
        && requestGenerationRef.current === requestGeneration
      ) setManifestError(getErrorMessage(error, 'Unable to load test manifest'))
    } finally {
      if (
        activeTestIDRef.current === requestTestID
        && requestGenerationRef.current === requestGeneration
      ) setManifestLoading(false)
    }
  }, [api, testID])

  return {
    manifest,
    manifestError,
    manifestLoading,
    reloadManifest,
  }
}
