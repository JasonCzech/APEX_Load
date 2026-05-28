import { useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { MetricsHistoryPoint, ResourceSample, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { mapArchiveResourceSamples } from './testExecutionMetricsModel'

const ARCHIVE_RESOURCE_PAGE_SIZE = 1000

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

export function useExecutionArchiveResourceHistory(test: TestSummary | null): {
  archiveResourceHistory: MetricsHistoryPoint[]
  archiveResourceError: string
  archiveResourcesLoading: boolean
} {
  const api = useApi()
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
        const loaded: ResourceSample[] = []
        let offset = 0
        let total = resourceSampleCount

        while (!cancelled && loaded.length < total) {
          const response = await api.getTestArchiveResources(testID, {
            limit: ARCHIVE_RESOURCE_PAGE_SIZE,
            offset,
          })
          const page = response.resources || []
          loaded.push(...page)
          total = response.total || loaded.length
          if (page.length === 0) {
            break
          }
          offset += page.length
        }

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
  }, [api, archiveCompletedAt, resourceSampleCount, shouldLoadResources, testID])

  const archiveResourceHistory = useMemo(() => mapArchiveResourceSamples(samples), [samples])

  return {
    archiveResourceHistory,
    archiveResourceError,
    archiveResourcesLoading,
  }
}
