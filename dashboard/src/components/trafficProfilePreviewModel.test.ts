import { describe, expect, it } from 'vitest'

import type { TrafficProfile } from '../types'
import { formatTrafficProfileAxisTime, trafficProfileMappingForGroup, trafficProfileTargetAt, trafficProfileTimelineSeconds, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'

function profile(pointCount = 4): TrafficProfile {
  return {
    version: 1,
    source: { kind: 'file', file: { filename: 'traffic.csv', timestamp_column: 'time', value_columns: ['transactions'] } },
    value_semantics: 'tps',
    runtime_target: 'tps',
    interpolation: 'step',
    source_duration: `${pointCount}m`,
    target_duration: `${pointCount}m`,
    mappings: [{
      series_key: 'requests',
      series_name: 'Requests',
      group_name: 'Checkout',
      peak_target: pointCount - 1,
      points: Array.from({ length: pointCount }, (_, index) => ({ offset_ms: index * 60_000, target: index })),
    }],
    refreshed_at: '2026-08-31T00:00:00Z',
  }
}

describe('trafficProfilePreviewModel', () => {
  it('matches groups case-insensitively and resolves step targets', () => {
    const mapping = trafficProfileMappingForGroup(profile(), ' checkout ')
    expect(mapping?.series_key).toBe('requests')
    expect(trafficProfileTargetAt(mapping, 0)).toBe(0)
    expect(trafficProfileTargetAt(mapping, 90)).toBe(1)
    expect(trafficProfileTargetAt(mapping, 180)).toBe(3)
  })

  it('downsamples display points while preserving endpoints and extrema', () => {
    const largeProfile = profile(5760)
    const timeline = trafficProfileTimelineSeconds(largeProfile, 1000)
    expect(timeline).toHaveLength(1000)
    expect(timeline[0]).toBe(0)
    expect(timeline).toContain(5759 * 60)
    expect(timeline[timeline.length - 1]).toBe(5760 * 60)
  })

  it('honors the display cap when many mapped series contribute extrema', () => {
    const manyMappings = profile(4)
    manyMappings.mappings = Array.from({ length: 12 }, (_, index) => ({
      series_key: `series-${index}`,
      series_name: `Series ${index}`,
      group_name: `Group ${index}`,
      peak_target: index + 1,
      points: [
        { offset_ms: index * 1000, target: 0 },
        { offset_ms: (index + 20) * 1000, target: index + 1 },
      ],
    }))

    const timeline = trafficProfileTimelineSeconds(manyMappings, 8)
    expect(timeline.length).toBeLessThanOrEqual(8)
    expect(timeline[0]).toBe(0)
    expect(timeline[timeline.length - 1]).toBe(240)
  })

  it('calculates exact time-weighted aggregate TPS statistics', () => {
    const first = profile().mappings[0]
    first.points = [{ offset_ms: 0, target: 2 }, { offset_ms: 60_000, target: 10 }, { offset_ms: 180_000, target: 4 }]
    const second = {
      ...first,
      series_key: 'search',
      group_name: 'Search',
      points: [{ offset_ms: 0, target: 1 }, { offset_ms: 120_000, target: 5 }],
    }

    expect(trafficProfileTpsStatistics([first, second], 240)).toEqual({ minimum: 3, maximum: 15, average: 9.5 })
    expect(trafficProfileTpsStatistics([], 240)).toEqual({ minimum: 0, maximum: 0, average: 0 })
  })

  it('formats profile axes in minutes and hours', () => {
    expect(formatTrafficProfileAxisTime(0)).toBe('0m')
    expect(formatTrafficProfileAxisTime(30)).toBe('0.5m')
    expect(formatTrafficProfileAxisTime(900)).toBe('15m')
    expect(formatTrafficProfileAxisTime(5400)).toBe('1h 30m')
  })
})
