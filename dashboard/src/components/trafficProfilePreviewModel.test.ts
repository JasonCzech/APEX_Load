import { describe, expect, it } from 'vitest'

import type { TrafficProfile } from '../types'
import { formatTrafficProfileAxisTime, inferTimestampMode, suggestGroupSeries, suggestTimestampColumn, suggestValueColumns, trafficProfileMappingForGroup, trafficProfilePeak, trafficProfileTargetAt, trafficProfileTimelineSeconds, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'

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
    expect(formatTrafficProfileAxisTime(0)).toBe('0s')
    expect(formatTrafficProfileAxisTime(26)).toBe('26s')
    expect(formatTrafficProfileAxisTime(63)).toBe('1m3s')
    expect(formatTrafficProfileAxisTime(900)).toBe('15m')
    expect(formatTrafficProfileAxisTime(5400)).toBe('1h30m')
  })
})

describe('inferTimestampMode', () => {
  const columns = ['time', 'orders']
  it('reads Go durations and elapsed H:MM[:SS] as relative', () => {
    expect(inferTimestampMode([['0s', '1'], ['1m30s', '2']], columns, 'time')).toBe('relative')
    expect(inferTimestampMode([['0:00:00', '1'], ['1:05:30.5', '2']], columns, 'time')).toBe('relative')
  })
  it('reads dated timestamps as absolute and leaves mixed or bare values undecided', () => {
    expect(inferTimestampMode([['2026-01-01T00:00:00Z', '1']], columns, 'time')).toBe('absolute')
    expect(inferTimestampMode([['0', '1'], ['60', '2']], columns, 'time')).toBeUndefined()
    expect(inferTimestampMode([['0s', '1'], ['2026-01-01T00:01:00Z', '2']], columns, 'time')).toBeUndefined()
    expect(inferTimestampMode([['0s', '1']], columns, 'missing')).toBeUndefined()
    expect(inferTimestampMode(undefined, columns, 'time')).toBeUndefined()
  })
})

describe('trafficProfilePeak', () => {
  it('sums the groups at the moment they peak together', () => {
    const base = profile(4)
    const second = { ...base.mappings[0], series_key: 'search', group_name: 'Search', points: [{ offset_ms: 0, target: 5 }, { offset_ms: 120_000, target: 1 }] }
    // Checkout steps 0,1,2,3 a minute apart; Search holds 5 for two minutes.
    expect(trafficProfilePeak({ ...base, mappings: [base.mappings[0], second] })).toBe(6)
    expect(trafficProfilePeak(base)).toBe(3)
    expect(trafficProfilePeak(undefined)).toBe(0)
    // Computed once per profile object.
    const once = profile(4)
    expect(trafficProfilePeak(once)).toBe(3)
    once.mappings[0].points[3].target = 99
    expect(trafficProfilePeak(once)).toBe(3)
  })
  it('counts points past a short or missing target duration', () => {
    expect(trafficProfilePeak({ ...profile(4), target_duration: '' })).toBe(3)
  })
})

describe('column suggestions', () => {
  const samples = [['2026-01-01T00:00:00Z', '12', '180', 'east', '3'], ['2026-01-01T00:00:10Z', '14', '175', 'west', '']]
  const columns = ['Event Time', 'orders', 'avg_response_ms', 'region', 'refunds']
  it('takes a time-named column over response times, else a column of timestamps', () => {
    expect(suggestTimestampColumn(columns, samples)).toBe('Event Time')
    expect(suggestTimestampColumn(['ResponseTime', 'when', 'orders'], [['120', '2026-01-01T00:00:00Z', '1']])).toBe('when')
    expect(suggestTimestampColumn(['elapsed', 'orders'], [['0:00', '1']])).toBe('elapsed')
    expect(suggestTimestampColumn(['a', 'b'], [['x', '1']])).toBe('')
  })
  it('picks numeric workload columns, never durations, text or excluded ones', () => {
    expect(suggestValueColumns(columns, samples, ['Event Time'])).toEqual(['orders', 'refunds'])
    expect(suggestValueColumns(columns, samples, ['Event Time', 'refunds'])).toEqual(['orders'])
    expect(suggestValueColumns(columns, undefined, [])).toEqual([])
  })
})

describe('suggestGroupSeries', () => {
  const series = [{ key: 'browse', name: 'Browse' }, { key: 'checkout', name: 'Checkout' }]
  it('matches groups to series by name and leaves the rest for the author', () => {
    expect(suggestGroupSeries(['checkout', 'browse'], series, {})).toEqual({ checkout: 'checkout', browse: 'browse' })
    // No guess by position: Cart and Search follow nothing until chosen.
    expect(suggestGroupSeries(['Cart', 'Checkout', 'Search'], series, {})).toEqual({ Checkout: 'checkout' })
  })
  it('keeps choices that still exist and fans a single series out to every group', () => {
    expect(suggestGroupSeries(['A', 'B'], series, { A: 'checkout', B: 'gone' })).toEqual({ A: 'checkout' })
    expect(suggestGroupSeries(['A', 'B'], [{ key: 'all', name: 'Requests' }], {})).toEqual({ A: 'all', B: 'all' })
    // A group the author set to none stays none.
    expect(suggestGroupSeries(['A', 'B'], [{ key: 'all', name: 'Requests' }], { B: '' })).toEqual({ A: 'all', B: '' })
  })
})
