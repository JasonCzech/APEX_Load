import { describe, expect, it } from 'vitest'
import { effectiveThinkTimeLabel, runtimeStopConditions, runtimeTimingIssues } from './runtimeWorkspaceModel'

describe('effective runtime workspace', () => {
  it('lists all stop conditions and preserves zero as an absent cap', () => {
    expect(runtimeStopConditions({ base_url: '', run_logic: { iterations: 2, global_iterations: 100, max_transactions: 30 } })).toEqual(['2 iterations per virtual user', '100 shared iterations', '30 transactions', 'Duration set by the scenario or run'])
    expect(runtimeStopConditions({ base_url: '', run_logic: { iterations: 0 } })).toEqual(['Duration set by the scenario or run'])
  })
  it.each([0, -1, Infinity, NaN])('rejects invalid open arrival target %s', tps => {
    expect(runtimeTimingIssues({ base_url: '', run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps } })).toHaveLength(1)
  })
  it('accepts positive arrival rate and explains inactive think-time settings', () => {
    expect(runtimeTimingIssues({ base_url: '', run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 5 } })).toEqual([])
    expect(effectiveThinkTimeLabel({ base_url: '', think_time: { type: 'fixed', ignore: true, mean_ms: 5000 } })).toBe('No think-time delay')
    expect(effectiveThinkTimeLabel({ base_url: '', think_time: { type: 'recorded', multiplier: 0.5, limit_ms: 3000 } })).toBe('Recorded action delays × 0.5, capped at 3 seconds')
  })
})
