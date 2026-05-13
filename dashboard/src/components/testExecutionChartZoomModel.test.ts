import { describe, expect, it } from 'vitest'

import {
  executionChartZoomReducer,
  getExecutionChartRefArea,
  initialExecutionChartZoomState,
} from './testExecutionChartZoomModel'

describe('testExecutionChartZoomModel', () => {
  it('tracks drag selection and applies a custom zoom range', () => {
    const afterBegin = executionChartZoomReducer(initialExecutionChartZoomState, {
      type: 'beginSelection',
      activeLabel: '10:00:01',
    })
    const afterMove = executionChartZoomReducer(afterBegin, {
      type: 'updateSelection',
      activeLabel: '10:00:05',
    })
    const afterApply = executionChartZoomReducer(afterMove, { type: 'applySelection' })

    expect(getExecutionChartRefArea(afterMove)).toEqual({
      showRefArea: true,
      refAreaEnd: '10:00:05',
    })
    expect(afterApply).toEqual({
      timeRange: 'custom',
      zoomRange: { left: '10:00:01', right: '10:00:05' },
      refAreaLeft: '',
      refAreaRight: '',
    })
  })

  it('clears a degenerate selection instead of applying zoom', () => {
    const selected = executionChartZoomReducer(initialExecutionChartZoomState, {
      type: 'beginSelection',
      activeLabel: '10:00:01',
    })
    const cleared = executionChartZoomReducer(selected, { type: 'applySelection' })

    expect(cleared).toEqual({
      timeRange: 'all',
      zoomRange: null,
      refAreaLeft: '',
      refAreaRight: '',
    })
  })

  it('allows switching visible range without disturbing the last stored zoom range', () => {
    const customState = {
      timeRange: 'custom' as const,
      zoomRange: { left: '10:00:01', right: '10:00:05' },
      refAreaLeft: '',
      refAreaRight: '',
    }

    expect(executionChartZoomReducer(customState, { type: 'setTimeRange', value: 'all' })).toEqual({
      timeRange: 'all',
      zoomRange: { left: '10:00:01', right: '10:00:05' },
      refAreaLeft: '',
      refAreaRight: '',
    })
  })
})