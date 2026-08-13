import { useEffect, useMemo, useState } from 'react'

import { useTestApi } from '../hooks/api/useTestApi'
import { IntervalSummaryRecord, MetricsHistoryPoint, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { ARCHIVE_CHART_MAX_SAMPLES } from './archiveHistorySampling'
import { mapArchiveSummarySamples } from './testExecutionMetricsModel'

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

interface ArchiveSummaryState {
  testID: string
  summaries: IntervalSummaryRecord[]
  error: string
  loading: boolean
}

export function useExecutionArchiveSummaryHistory(test: TestSummary | null): {
  archiveSummaryHistory: MetricsHistoryPoint[]
  archiveSummaryError: string
  archiveSummariesLoading: boolean
} {
  const { getTestArchiveSummaries } = useTestApi()
  const [summaryState, setSummaryState] = useState<ArchiveSummaryState>({
    testID: '',
    summaries: [],
    error: '',
    loading: false,
  })

  const testID = test?.id || ''
  const testStatus = test?.status
  const archiveCompletedAt = test?.archive?.completed_at || ''
  const intervalSummaryCount = test?.archive?.interval_summary_count || 0
  const shouldLoadSummaries = Boolean(testID && test?.archive && isTerminalStatus(testStatus) && intervalSummaryCount > 0)

  useEffect(() => {
    if (!shouldLoadSummaries) {
      setSummaryState({ testID: '', summaries: [], error: '', loading: false })
      return
    }

    let cancelled = false

    const loadArchiveSummaries = async () => {
      setSummaryState(current => ({
        testID,
        summaries: current.testID === testID ? current.summaries : [],
        error: '',
        loading: true,
      }))
      try {
        const response = await getTestArchiveSummaries(testID, { sample_limit: ARCHIVE_CHART_MAX_SAMPLES })
        const loaded = response.summaries || []

        if (!cancelled) {
          setSummaryState({ testID, summaries: loaded, error: '', loading: false })
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setSummaryState({
            testID,
            summaries: [],
            error: getErrorMessage(error, 'Unable to load archive interval summaries'),
            loading: false,
          })
        }
      }
    }

    loadArchiveSummaries()

    return () => {
      cancelled = true
    }
  }, [archiveCompletedAt, getTestArchiveSummaries, intervalSummaryCount, shouldLoadSummaries, testID])

  const stateMatchesTest = shouldLoadSummaries && summaryState.testID === testID
  const archiveSummaryHistory = useMemo(
    () => mapArchiveSummarySamples(stateMatchesTest ? summaryState.summaries : []),
    [stateMatchesTest, summaryState.summaries],
  )

  return {
    archiveSummaryHistory,
    archiveSummaryError: stateMatchesTest ? summaryState.error : '',
    archiveSummariesLoading: shouldLoadSummaries && (!stateMatchesTest || summaryState.loading),
  }
}
