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

  it('offsets brush windows from the current visible slice on repeated zooms', () => {
    const first = timelineZoomReducer(initialTimelineZoomState, {
      type: 'set-window',
      startIndex: 20,
      endIndex: 40,
      pointCount: 100,
    })

    expect(timelineZoomReducer(first, {
      type: 'set-window',
      startIndex: 5,
      endIndex: 10,
      pointCount: 100,
      baseStartIndex: first.window?.startIndex ?? 0,
    })).toEqual({ window: { startIndex: 25, endIndex: 30 } })
  })
})
