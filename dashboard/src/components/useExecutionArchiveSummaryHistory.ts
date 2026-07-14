import { useEffect, useMemo, useState } from 'react'

import { useTestApi } from '../hooks/api/useTestApi'
import { IntervalSummaryRecord, MetricsHistoryPoint, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { ARCHIVE_CHART_MAX_SAMPLES } from './archiveHistorySampling'
import { mapArchiveSummarySamples } from './testExecutionMetricsModel'

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

export function useExecutionArchiveSummaryHistory(test: TestSummary | null): {
  archiveSummaryHistory: MetricsHistoryPoint[]
  archiveSummaryError: string
  archiveSummariesLoading: boolean
} {
  const { getTestArchiveSummaries } = useTestApi()
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
        const response = await getTestArchiveSummaries(testID, { sample_limit: ARCHIVE_CHART_MAX_SAMPLES })
        const loaded = response.summaries || []

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
  }, [archiveCompletedAt, getTestArchiveSummaries, intervalSummaryCount, shouldLoadSummaries, testID])

  const archiveSummaryHistory = useMemo(() => mapArchiveSummarySamples(summaries), [summaries])

  return {
    archiveSummaryHistory,
    archiveSummaryError,
    archiveSummariesLoading,
  }
}
