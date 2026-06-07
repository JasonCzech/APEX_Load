import { useEffect, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestRunManifest, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

const LIVE_MANIFEST_REFRESH_MS = 5000

function shouldPollManifest(status?: string): boolean {
  return status === 'RUNNING' || status === 'PAUSED' || status === 'STOPPING' || status === 'INITIALIZING'
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

  useEffect(() => {
    if (!testID) {
      setManifest(null)
      setManifestError('')
      setManifestLoading(false)
      return undefined
    }

    let cancelled = false

    const load = async (showLoading: boolean) => {
      if (showLoading) setManifestLoading(true)
      setManifestError('')
      try {
        const next = await api.getTestManifest(testID)
        if (!cancelled) setManifest(next)
      } catch (error: unknown) {
        if (!cancelled) {
          setManifest(null)
          setManifestError(getErrorMessage(error, 'Unable to load test manifest'))
        }
      } finally {
        if (!cancelled && showLoading) setManifestLoading(false)
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

  const reloadManifest = async () => {
    if (!testID) return
    setManifestLoading(true)
    setManifestError('')
    try {
      const next = await api.getTestManifest(testID)
      setManifest(next)
    } catch (error: unknown) {
      setManifestError(getErrorMessage(error, 'Unable to load test manifest'))
    } finally {
      setManifestLoading(false)
    }
  }

  return {
    manifest,
    manifestError,
    manifestLoading,
    reloadManifest,
  }
}
