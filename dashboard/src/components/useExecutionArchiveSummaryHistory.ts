import { useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { IntervalSummaryRecord, MetricsHistoryPoint, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { mapArchiveSummarySamples } from './testExecutionMetricsModel'

const ARCHIVE_SUMMARY_PAGE_SIZE = 1000

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

export function useExecutionArchiveSummaryHistory(test: TestSummary | null): {
  archiveSummaryHistory: MetricsHistoryPoint[]
  archiveSummaryError: string
  archiveSummariesLoading: boolean
} {
  const api = useApi()
  const [summaries, setSummaries] = useState<IntervalSummaryRecord[]>([])
  const [archiveSummaryError, setArchiveSummaryError] = useState('')
  const [archiveSummariesLoading, setArchiveSummariesLoading] = useState(false)

  const testID = test?.id || ''
  const testStatus = test?.status
  const archiveCompletedAt = test?.archive?.completed_at || ''
  const intervalSummaryCount = test?.archive?.interval_summary_count || 0
  const shouldLoadSummaries = Boolean(testID && test?.archive && isTerminalStatus(testStatus) && intervalSummaryCount > 0)

  useEffect(() => {
    if (!shouldLoadSummaries) {
      setSummaries([])
      setArchiveSummaryError('')
      setArchiveSummariesLoading(false)
      return
    }

    let cancelled = false

    const loadArchiveSummaries = async () => {
      setArchiveSummariesLoading(true)
      setArchiveSummaryError('')
      try {
        const loaded: IntervalSummaryRecord[] = []
        let offset = 0
        let total = intervalSummaryCount

        while (!cancelled && loaded.length < total) {
          const response = await api.getTestArchiveSummaries(testID, {
            limit: ARCHIVE_SUMMARY_PAGE_SIZE,
            offset,
          })
          const page = response.summaries || []
          loaded.push(...page)
          total = response.total || loaded.length
          if (page.length === 0) {
            break
          }
          offset += page.length
        }

        if (!cancelled) {
          setSummaries(loaded)
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setSummaries([])
          setArchiveSummaryError(getErrorMessage(error, 'Unable to load archive interval summaries'))
        }
      } finally {
        if (!cancelled) {
          setArchiveSummariesLoading(false)
        }
      }
    }

    loadArchiveSummaries()

    return () => {
      cancelled = true
    }
  }, [api, archiveCompletedAt, intervalSummaryCount, shouldLoadSummaries, testID])

  const archiveSummaryHistory = useMemo(() => mapArchiveSummarySamples(summaries), [summaries])

  return {
    archiveSummaryHistory,
    archiveSummaryError,
    archiveSummariesLoading,
  }
}
