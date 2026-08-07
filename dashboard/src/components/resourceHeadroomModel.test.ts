import { describe, expect, it } from 'vitest'

import { headroomTone } from './resourceHeadroomModel'

describe('headroomTone', () => {
  it('treats a missing or zero limit as unlimited', () => {
    expect(headroomTone(5, 0)).toEqual({ pct: 0, cls: 'pp-bar-fill-success' })
    expect(headroomTone(5, -1)).toEqual({ pct: 0, cls: 'pp-bar-fill-success' })
  })

  it('escalates tone at the 70% and 90% boundaries', () => {
    expect(headroomTone(69, 100).cls).toBe('pp-bar-fill-success')
    expect(headroomTone(70, 100).cls).toBe('pp-bar-fill-warning')
    expect(headroomTone(89, 100).cls).toBe('pp-bar-fill-warning')
    expect(headroomTone(90, 100).cls).toBe('pp-bar-fill-danger')
    expect(headroomTone(100, 100).cls).toBe('pp-bar-fill-danger')
  })

  it('clamps the bar width when usage exceeds the limit', () => {
    // A projected over-quota allocation must not render a bar wider than its track.
    expect(headroomTone(250, 100)).toEqual({ pct: 100, cls: 'pp-bar-fill-danger' })
  })
})
