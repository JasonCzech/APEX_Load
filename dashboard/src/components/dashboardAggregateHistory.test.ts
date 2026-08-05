import { describe, expect, it } from 'vitest'
import type { MetricsHistoryPoint } from '../types'
import { aggregateDashboardTpsHistory } from './dashboardAggregateHistory'

const point = (timestampMs: number, tps: number): MetricsHistoryPoint => ({
  timestampMs, time: new Date(timestampMs).toISOString(), tps,
  vusers: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0,
})

describe('aggregateDashboardTpsHistory', () => {
  it('sums concurrent test samples into one fleet point', () => {
    const result = aggregateDashboardTpsHistory(['a', 'b'], {
      a: [point(1_000, 10)],
      b: [point(1_050, 20)],
    })
    expect(result).toHaveLength(1)
    expect(result[0].tps).toBe(30)
    expect(result[0].vusers).toBe(2)
  })

  it('retains legacy localized clock labels by relative sample position', () => {
    const legacy = (time: string, tps: number): MetricsHistoryPoint => ({
      time, tps, vusers: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0,
    })
    const result = aggregateDashboardTpsHistory(['a'], {
      a: [legacy('8:00:00 PM', 10), legacy('8:00:03 PM', 20)],
    })
    expect(result.map(point => point.tps)).toEqual([10, 20])
  })

  it('right-aligns legacy histories with different sample counts', () => {
    const legacy = (time: string, tps: number): MetricsHistoryPoint => ({
      time, tps, vusers: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0,
    })
    const result = aggregateDashboardTpsHistory(['a', 'b'], {
      a: [legacy('8:00:00 PM', 10), legacy('8:00:03 PM', 20)],
      b: [legacy('9:15:42 PM', 30)],
    })
    expect(result.map(point => point.tps)).toEqual([10, 50])
  })
})
