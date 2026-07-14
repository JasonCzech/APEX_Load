import { useEffect, useMemo, useState } from 'react'

import { useTestApi } from '../hooks/api/useTestApi'
import { MetricsHistoryPoint, ResourceSample, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { ARCHIVE_CHART_MAX_SAMPLES } from './archiveHistorySampling'
import { mapArchiveResourceSamples } from './testExecutionMetricsModel'

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

export function useExecutionArchiveResourceHistory(test: TestSummary | null): {
  archiveResourceHistory: MetricsHistoryPoint[]
  archiveResourceError: string
  archiveResourcesLoading: boolean
} {
  const { getTestArchiveResources } = useTestApi()
  const [samples, setSamples] = useState<ResourceSample[]>([])
  const [archiveResourceError, setArchiveResourceError] = useState('')
  const [archiveResourcesLoading, setArchiveResourcesLoading] = useState(false)

  const testID = test?.id || ''
  const testStatus = test?.status
  const archiveCompletedAt = test?.archive?.completed_at || ''
  const resourceSampleCount = test?.archive?.resource_sample_count || 0
  const shouldLoadResources = Boolean(testID && test?.archive && isTerminalStatus(testStatus) && resourceSampleCount > 0)

  useEffect(() => {
    if (!shouldLoadResources) {
      setSamples([])
      setArchiveResourceError('')
      setArchiveResourcesLoading(false)
      return
    }

    let cancelled = false

    const loadArchiveResources = async () => {
      setArchiveResourcesLoading(true)
      setArchiveResourceError('')
      try {
        const response = await getTestArchiveResources(testID, { sample_limit: ARCHIVE_CHART_MAX_SAMPLES })
        const loaded = response.resources || []

        if (!cancelled) {
          setSamples(loaded)
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setSamples([])
          setArchiveResourceError(getErrorMessage(error, 'Unable to load archive resource samples'))
        }
      } finally {
        if (!cancelled) {
          setArchiveResourcesLoading(false)
        }
      }
    }

    loadArchiveResources()

    return () => {
      cancelled = true
    }
  }, [archiveCompletedAt, getTestArchiveResources, resourceSampleCount, shouldLoadResources, testID])

  const archiveResourceHistory = useMemo(() => mapArchiveResourceSamples(samples), [samples])

  return {
    archiveResourceHistory,
    archiveResourceError,
    archiveResourcesLoading,
  }
}
