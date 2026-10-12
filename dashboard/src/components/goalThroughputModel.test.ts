import { describe, expect, it } from 'vitest'
import { formatGapToTarget, goalStanding, isOffTarget, profilePosition, throughputGapToTarget } from './goalThroughputModel'

describe('goalThroughputModel', () => {
  it('measures the gap to target in both directions with the engine sign', () => {
    expect(throughputGapToTarget(17, 20)).toBeCloseTo(15)
    expect(throughputGapToTarget(27.8, 20)).toBeCloseTo(-39)
    expect(throughputGapToTarget(10, 0)).toBeUndefined()
    expect(formatGapToTarget(15)).toBe('15.0% under')
    expect(formatGapToTarget(-39)).toBe('39.0% over')
    expect(formatGapToTarget(0.01)).toBe('on target')
    expect(formatGapToTarget(undefined)).toBeUndefined()
  })

  it('treats 5% either way as off target', () => {
    expect(isOffTarget(4.9)).toBe(false)
    expect(isOffTarget(-5)).toBe(true)
    expect(goalStanding(-12)).toBe('over')
    expect(goalStanding(12)).toBe('under')
    expect(goalStanding(2)).toBe('on')
  })

  it('places a run in its traffic profile', () => {
    const points = [{ offset_ms: 0, target: 1 }, { offset_ms: 12000, target: 1.5 }, { offset_ms: 24000, target: 2.5 }]
    expect(profilePosition(points, 13)).toEqual({ point: 2, points: 3, target: 1.5, next: { target: 2.5, inSeconds: 11 } })
    expect(profilePosition(points, 30)).toEqual({ point: 3, points: 3, target: 2.5, next: undefined })
    expect(profilePosition([], 5)).toBeUndefined()
  })
})
