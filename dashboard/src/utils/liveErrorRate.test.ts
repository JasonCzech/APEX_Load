import { describe, expect, it } from 'vitest'
import type { GroupLiveMetrics, LiveMetrics } from '../types'
import { attemptErrorPercent, errorRateBasisLabel, groupRunCounts, groupRunErrorPercent, liveRunCounts, liveRunErrorPercent } from './liveErrorRate'

const window = (fields: Partial<LiveMetrics>) => ({ total_transactions: 18, total_errors: 0, error_pct: 0, ...fields }) as LiveMetrics

describe('live error rate helpers', () => {
  it('uses run-to-date counters so a 1%-failing run does not read 0.00% from an error-free second', () => {
    const metrics = window({ run_total_transactions: 7_853, run_total_errors: 83, run_error_pct: 1.05 })
    expect(liveRunCounts(metrics)?.basis).toBe('run')
    expect(liveRunErrorPercent(metrics)).toBeCloseTo(83 / 7_936 * 100, 6)
  })

  it('falls back to the window counters for runs recorded before run totals existed', () => {
    const metrics = window({ total_transactions: 9, total_errors: 1 })
    expect(liveRunCounts(metrics)?.basis).toBe('window')
    expect(liveRunErrorPercent(metrics)).toBeCloseTo(10, 6)
    expect(liveRunErrorPercent(undefined)).toBeUndefined()
  })

  it('divides failures by all attempts, not by successes', () => {
    expect(attemptErrorPercent(3, 1)).toBe(25)
    expect(attemptErrorPercent(0, 0)).toBe(0)
    const group = { name: 'Cart', transactions: 0, errors: 2, run_transactions: 6, run_errors: 2 } as GroupLiveMetrics
    expect(groupRunErrorPercent(group)).toBe(25)
  })

  it('names the basis in labels', () => {
    expect(errorRateBasisLabel('run')).toBe('run to date')
    expect(errorRateBasisLabel('window')).toBe('last sample')
  })

  it('treats an omitted run error count as zero errors so far', () => {
    const live = { total_transactions: 4, total_errors: 0, run_total_transactions: 900 } as LiveMetrics
    expect(liveRunCounts(live)).toEqual({ transactions: 900, errors: 0, basis: 'run' })
    expect(groupRunCounts({ transactions: 3, errors: 1, run_transactions: 50 } as GroupLiveMetrics)).toEqual({ transactions: 50, errors: 0, basis: 'run' })
  })
})
