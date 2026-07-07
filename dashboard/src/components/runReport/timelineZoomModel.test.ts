import { describe, expect, it } from 'vitest'

import { initialTimelineZoomState, timelineZoomReducer } from './timelineZoomModel'

describe('timelineZoomReducer', () => {
  it('normalizes brush windows and resets the full-domain selection', () => {
    expect(timelineZoomReducer(initialTimelineZoomState, {
      type: 'set-window',
      startIndex: 8,
      endIndex: 2,
      pointCount: 10,
    })).toEqual({ window: { startIndex: 2, endIndex: 8 } })

    expect(timelineZoomReducer(initialTimelineZoomState, {
      type: 'set-window',
      startIndex: 0,
      endIndex: 9,
      pointCount: 10,
    })).toEqual({ window: null })
  })

  it('clamps out-of-range brush indexes', () => {
    expect(timelineZoomReducer(initialTimelineZoomState, {
      type: 'set-window',
      startIndex: -10,
      endIndex: 99,
      pointCount: 5,
    })).toEqual({ window: null })
  })
})
