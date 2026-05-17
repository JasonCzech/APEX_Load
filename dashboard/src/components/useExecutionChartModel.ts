import { useMemo, useReducer, useState } from 'react'

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
  const [hoveredLabel, setHoveredLabel] = useState<string | number | null>(null)

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

  const onChartScrollWheel = (event: React.WheelEvent<HTMLDivElement>) => {
    if (chartData.length <= 1) return

    event.preventDefault()
    const isZoomOut = event.deltaY > 0
    
    const getVisibleIndices = () => {
      if (chartZoomState.timeRange === 'custom' && chartZoomState.zoomRange) {
        const left = chartData.findIndex((point) => point.time === chartZoomState.zoomRange!.left)
        const right = chartData.findIndex((point) => point.time === chartZoomState.zoomRange!.right)
        if (left !== -1 && right !== -1) {
          return { start: Math.min(left, right), end: Math.max(left, right) }
        }
      }
      
      if (chartZoomState.timeRange === 'all') {
        return { start: 0, end: chartData.length - 1 }
      }
      
      const windowMs = chartZoomState.timeRange === '3m' ? 180_000 : 600_000
      const newestTimestamp = [...chartData].reverse().find(point => typeof point.timestampMs === 'number')?.timestampMs
      if (newestTimestamp === undefined) {
        const approximatePoints = chartZoomState.timeRange === '3m' ? 180 : 600
        return { start: Math.max(0, chartData.length - approximatePoints), end: chartData.length - 1 }
      }
      
      const cutoff = newestTimestamp - windowMs
      const firstIndex = chartData.findIndex(p => typeof p.timestampMs === 'number' && p.timestampMs >= cutoff)
      return { start: firstIndex === -1 ? 0 : firstIndex, end: chartData.length - 1 }
    }

    const { start, end } = getVisibleIndices()

    let hoverIndex = -1
    if (hoveredLabel !== null) {
      hoverIndex = chartData.findIndex(p => p.time === hoveredLabel)
    }
    if (hoverIndex < start || hoverIndex > end) {
      hoverIndex = Math.round((start + end) / 2)
    }

    const leftDist = hoverIndex - start
    const rightDist = end - hoverIndex

    let newStart = start
    let newEnd = end

    if (isZoomOut) {
      const scale = 0.15
      const padLeft = Math.max(1, Math.round(leftDist * scale))
      const padRight = Math.max(1, Math.round(rightDist * scale))
      
      newStart = Math.max(0, start - padLeft)
      newEnd = Math.min(chartData.length - 1, end + padRight)
      
      if (newStart === 0 && newEnd === chartData.length - 1) {
        dispatchChartZoom({ type: 'setZoomRange', zoomRange: null })
        return
      }
    } else {
      const scale = 0.15
      const shrinkLeft = Math.round(leftDist * scale)
      const shrinkRight = Math.round(rightDist * scale)
      
      newStart = start + shrinkLeft
      newEnd = end - shrinkRight
      
      if (newEnd - newStart < 5) {
        return
      }
    }

    if (newStart !== start || newEnd !== end) {
      dispatchChartZoom({
        type: 'setZoomRange',
        zoomRange: {
          left: chartData[newStart].time,
          right: chartData[newEnd].time,
        }
      })
    }
  }

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
    onChartMouseMove: (activeLabel?: string | number) => {
      if (activeLabel !== undefined) {
        setHoveredLabel(activeLabel)
      }
      dispatchChartZoom({ type: 'updateSelection', activeLabel })
    },
    onZoom: () => dispatchChartZoom({ type: 'applySelection' }),
    onChartScrollWheel,
  }
}