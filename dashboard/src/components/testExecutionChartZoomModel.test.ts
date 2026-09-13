import { describe, expect, it } from 'vitest'

import {
  executionChartMaxMinuteRange,
  executionChartMinuteRangeValue,
  executionChartTimeRangeForMinutes,
  executionChartZoomReducer,
  getExecutionChartRefArea,
  initialExecutionChartZoomState,
} from './testExecutionChartZoomModel'

describe('testExecutionChartZoomModel', () => {
  it('tracks drag selection and applies a custom zoom range', () => {
    const afterBegin = executionChartZoomReducer(initialExecutionChartZoomState, {
      type: 'beginSelection',
      anchor: { label: '10:00:01', timestampMs: 1_000 },
    })
    const afterMove = executionChartZoomReducer(afterBegin, {
      type: 'updateSelection',
      anchor: { label: '10:00:05', timestampMs: 5_000 },
    })
    const afterApply = executionChartZoomReducer(afterMove, { type: 'applySelection' })

    expect(getExecutionChartRefArea(afterMove)).toEqual({
      showRefArea: true,
      refAreaLeft: '10:00:01',
      refAreaEnd: '10:00:05',
    })
    expect(afterApply).toEqual({
      timeRange: 'custom',
      zoomRange: { startTimestampMs: 1_000, endTimestampMs: 5_000 },
      refAreaLeft: null,
      refAreaRight: null,
    })
  })

  it('normalizes a right-to-left drag into chronological timestamp bounds', () => {
    const afterBegin = executionChartZoomReducer(initialExecutionChartZoomState, {
      type: 'beginSelection',
      anchor: { label: 'same display time', timestampMs: 5_000 },
    })
    const afterMove = executionChartZoomReducer(afterBegin, {
      type: 'updateSelection',
      anchor: { label: 'same display time', timestampMs: 1_000 },
    })
    const afterApply = executionChartZoomReducer(afterMove, { type: 'applySelection' })

    expect(afterApply.zoomRange).toEqual({ startTimestampMs: 1_000, endTimestampMs: 5_000 })
  })

  it('clears a degenerate selection instead of applying zoom', () => {
    const selected = executionChartZoomReducer(initialExecutionChartZoomState, {
      type: 'beginSelection',
      anchor: { label: '10:00:01', timestampMs: 1_000 },
    })
    const cleared = executionChartZoomReducer(selected, { type: 'applySelection' })

    expect(cleared).toEqual({
      timeRange: 'all',
      zoomRange: null,
      refAreaLeft: null,
      refAreaRight: null,
    })
  })

  it('allows switching visible range without disturbing the last stored zoom range', () => {
    const customState = {
      timeRange: 'custom' as const,
      zoomRange: { startTimestampMs: 1_000, endTimestampMs: 5_000 },
      refAreaLeft: null,
      refAreaRight: null,
    }

    expect(executionChartZoomReducer(customState, { type: 'setTimeRange', value: 'all' })).toEqual({
      timeRange: 'all',
      zoomRange: { startTimestampMs: 1_000, endTimestampMs: 5_000 },
      refAreaLeft: null,
      refAreaRight: null,
    })
  })

  it('maps slider minute values to supported chart ranges', () => {
    expect(executionChartMaxMinuteRange).toBe(60)
    expect(executionChartMinuteRangeValue('1m')).toBe(1)
    expect(executionChartMinuteRangeValue('30m')).toBe(30)
    expect(executionChartMinuteRangeValue('all')).toBeNull()
    expect(executionChartMinuteRangeValue('custom')).toBeNull()
    expect(executionChartTimeRangeForMinutes(0)).toBe('1m')
    expect(executionChartTimeRangeForMinutes(16)).toBe('16m')
    expect(executionChartTimeRangeForMinutes(99)).toBe('60m')
  })
})
