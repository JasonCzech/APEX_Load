import { useMemo, useReducer } from 'react'

import { MetricsHistoryPoint, TestSummary } from '../types'
import {
  executionChartZoomReducer,
  getExecutionChartRefArea,
  initialExecutionChartZoomState,
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
}

export function useExecutionChartModel({
  test,
  history,
  localMetricsHistory,
}: UseExecutionChartModelOptions) {
  const [chartZoomState, dispatchChartZoom] = useReducer(executionChartZoomReducer, initialExecutionChartZoomState)
  const mappedBackendHistory = useMemo(() => {
    return mapExecutionHistory(test?.history)
  }, [test?.history])

  const chartData = useMemo(() => {
    return buildExecutionChartData({
      mappedBackendHistory,
      localMetricsHistory,
      fallbackHistory: history,
      status: test?.status,
    })
  }, [history, localMetricsHistory, mappedBackendHistory, test?.status])

  const filteredData = useMemo(() => {
    return filterExecutionChartData(chartData, chartZoomState.timeRange, chartZoomState.zoomRange)
  }, [chartData, chartZoomState.timeRange, chartZoomState.zoomRange])

  const summary = useMemo(() => {
    return summarizeExecutionChartData(filteredData)
  }, [filteredData])

  const { showRefArea, refAreaEnd } = getExecutionChartRefArea(chartZoomState)

  return {
    filteredData,
    summary,
    timeRange: chartZoomState.timeRange,
    refAreaLeft: chartZoomState.refAreaLeft,
    refAreaEnd,
    showRefArea,
    onTimeRangeChange: (value: typeof chartZoomState.timeRange) => {
      if (value === 'custom' && !chartZoomState.zoomRange && chartData.length > 2) {
        const half = Math.floor(chartData.length / 2)
        dispatchChartZoom({
          type: 'setZoomRange',
          zoomRange: {
            left: chartData[half].time,
            right: chartData[chartData.length - 1].time,
          },
        })
      } else {
        dispatchChartZoom({ type: 'setTimeRange', value })
      }
    },
    onChartMouseDown: (activeLabel?: string | number) => dispatchChartZoom({ type: 'beginSelection', activeLabel }),
    onChartMouseMove: (activeLabel?: string | number) => dispatchChartZoom({ type: 'updateSelection', activeLabel }),
    onZoom: () => dispatchChartZoom({ type: 'applySelection' }),
    onResetZoom: () => dispatchChartZoom({ type: 'setZoomRange', zoomRange: null }),
  }
}
