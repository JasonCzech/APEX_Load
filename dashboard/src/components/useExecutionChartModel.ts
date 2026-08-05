import { useMemo, useReducer } from 'react'

import { MetricsHistoryPoint, TestSummary } from '../types'
import { isCompletedTestStatus } from '../utils/testStatusUtils'
import {
  executionChartZoomReducer,
  getExecutionChartRefArea,
  initialExecutionChartZoomState,
  type ExecutionChartZoomAnchor,
} from './testExecutionChartZoomModel'
import {
  buildExecutionChartData,
  filterExecutionChartData,
  mapExecutionHistory,
  summarizeExecutionChartData,
} from './testExecutionMetricsModel'

interface UseExecutionChartModelOptions {
  test: TestSummary | null
  history: MetricsHistoryPoint[]
  localMetricsHistory: MetricsHistoryPoint[]
  archiveSummaryHistory?: MetricsHistoryPoint[]
  archiveResourceHistory?: MetricsHistoryPoint[]
}

export function useExecutionChartModel({
  test,
  history,
  localMetricsHistory,
  archiveSummaryHistory = [],
  archiveResourceHistory = [],
}: UseExecutionChartModelOptions) {
  const [chartZoomState, dispatchChartZoom] = useReducer(executionChartZoomReducer, initialExecutionChartZoomState)
  const mappedBackendHistory = useMemo(() => {
    return mapExecutionHistory(test?.history)
  }, [test?.history])
  const useArchivePerformanceHistory = archiveSummaryHistory.length > 0
    && (Boolean(test?.archive) || isCompletedTestStatus(test?.status))

  const chartData = useMemo(() => {
    return buildExecutionChartData({
      mappedBackendHistory: useArchivePerformanceHistory ? [] : mappedBackendHistory,
      localMetricsHistory: useArchivePerformanceHistory ? archiveSummaryHistory : localMetricsHistory,
      fallbackHistory: history,
      archiveResourceHistory,
      status: test?.status,
    })
  }, [archiveResourceHistory, archiveSummaryHistory, history, localMetricsHistory, mappedBackendHistory, test?.status, useArchivePerformanceHistory])

  const filteredData = useMemo(() => {
    return filterExecutionChartData(chartData, chartZoomState.timeRange, chartZoomState.zoomRange)
  }, [chartData, chartZoomState.timeRange, chartZoomState.zoomRange])

  const summary = useMemo(() => {
    return summarizeExecutionChartData(filteredData)
  }, [filteredData])

  const { showRefArea, refAreaLeft, refAreaEnd } = getExecutionChartRefArea(chartZoomState)

  return {
    filteredData,
    summary,
    timeRange: chartZoomState.timeRange,
    refAreaLeft,
    refAreaEnd,
    showRefArea,
    onTimeRangeChange: (value: typeof chartZoomState.timeRange) => {
      if (value === 'custom' && !chartZoomState.zoomRange && chartData.length > 2) {
        const half = Math.floor(chartData.length / 2)
        const startTimestampMs = chartData[half].timestampMs
        const endTimestampMs = chartData[chartData.length - 1].timestampMs
        if (typeof startTimestampMs === 'number' && typeof endTimestampMs === 'number') {
          dispatchChartZoom({
            type: 'setZoomRange',
            zoomRange: {
              startTimestampMs: Math.min(startTimestampMs, endTimestampMs),
              endTimestampMs: Math.max(startTimestampMs, endTimestampMs),
            },
          })
        }
      } else {
        dispatchChartZoom({ type: 'setTimeRange', value })
      }
    },
    onChartMouseDown: (anchor?: ExecutionChartZoomAnchor) => dispatchChartZoom({ type: 'beginSelection', anchor }),
    onChartMouseMove: (anchor?: ExecutionChartZoomAnchor) => dispatchChartZoom({ type: 'updateSelection', anchor }),
    onZoom: () => dispatchChartZoom({ type: 'applySelection' }),
    onResetZoom: () => dispatchChartZoom({ type: 'setZoomRange', zoomRange: null }),
  }
}
