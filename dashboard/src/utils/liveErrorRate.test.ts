import { describe, expect, it } from 'vitest'
import type { GroupLiveMetrics, LiveMetrics } from '../types'
import { attemptErrorPercent, errorRateBasisLabel, errorRateLabel, groupRunCounts, groupRunErrorPercent, livePopulation, liveRate, liveRunCounts, liveRunErrorPercent, throughputLabel } from './liveErrorRate'

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
    expect(groupRunErrorPercent(group, 'transactions')).toBe(25)
  })

  it('names the basis and the population in labels', () => {
    expect(errorRateBasisLabel('run')).toBe('run to date')
    expect(errorRateBasisLabel('window')).toBe('last sample')
    expect(errorRateLabel('requests')).toBe('Request error rate')
    expect(errorRateLabel('transactions')).toBe('Transaction error rate')
    expect(throughputLabel('requests')).toBe('Requests/s')
    expect(throughputLabel('transactions')).toBe('TPS')
  })

  it('treats an omitted run error count as zero errors so far', () => {
    const live = { total_transactions: 4, total_errors: 0, run_total_transactions: 900 } as LiveMetrics
    expect(liveRunCounts(live)).toEqual({ attempts: 900, errors: 0, basis: 'run', population: 'transactions' })
    expect(groupRunCounts({ transactions: 3, errors: 1, run_transactions: 50 } as GroupLiveMetrics, 'transactions')).toEqual({ attempts: 50, errors: 0, basis: 'run', population: 'transactions' })
  })
})

// A script without transactions: each iteration's second request fails, so
// every iteration's implicit transaction fails while half the requests pass.
describe('live request population', () => {
  const inventory = {
    name: 'Inventory', vusers: 4, transactions: 0, errors: 3, tps: 0, run_transactions: 0, run_errors: 387,
    requests: 30, request_errors: 15, request_rate: 30, run_requests: 1_729, run_request_errors: 863,
  } as GroupLiveMetrics
  const metrics = window({
    total_transactions: 0, total_errors: 3, tps: 0, run_total_transactions: 0, run_total_errors: 387,
    requests: 30, request_errors: 15, request_rate: 30, run_total_requests: 1_729, run_request_errors: 863,
    groups: { Inventory: inventory },
  })

  it('counts requests once the run has sent any, as the report does', () => {
    expect(livePopulation(metrics)).toBe('requests')
    expect(liveRunCounts(metrics)).toEqual({ attempts: 1_729, errors: 863, basis: 'run', population: 'requests' })
    expect(liveRunErrorPercent(metrics)).toBeCloseTo(863 / 1_729 * 100, 6)
    expect(groupRunErrorPercent(inventory, 'requests')).toBeCloseTo(49.9, 1)
    // The transaction population still reads every iteration as failed.
    expect(liveRunErrorPercent(metrics, 'transactions')).toBe(100)
  })

  it('reports the group as sending traffic, not at 0.0 TPS', () => {
    expect(liveRate(inventory, 'requests')).toBe(30)
    expect(liveRate(inventory, 'transactions')).toBe(0)
    // An omitted request rate is a window with no requests.
    expect(liveRate({ tps: 4 }, 'requests')).toBe(0)
    expect(liveRate(undefined, 'requests')).toBeUndefined()
  })

  it('stays with transactions until the run sends a request, and for older controllers', () => {
    expect(livePopulation(undefined)).toBe('transactions')
    expect(livePopulation(window({ run_total_requests: 0 }))).toBe('transactions')
    expect(livePopulation(window({}))).toBe('transactions')
    // A view whose run counters are absent decides from the window.
    expect(livePopulation(window({ requests: 5 }))).toBe('requests')
    expect(liveRunCounts(window({ requests: 5, request_errors: 1 }))).toEqual({ attempts: 5, errors: 1, basis: 'window', population: 'requests' })
  })

  it('has no group error rate before the group has attempted anything', () => {
    expect(groupRunErrorPercent({ name: 'Idle', transactions: 0, errors: 0, run_requests: 0 } as GroupLiveMetrics, 'requests')).toBeUndefined()
    expect(groupRunErrorPercent(undefined, 'requests')).toBeUndefined()
  })
})
