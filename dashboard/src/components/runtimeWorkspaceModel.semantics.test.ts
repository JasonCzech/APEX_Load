import { describe, expect, it } from 'vitest'

import type { ScriptConfig } from '../types/scripts'
import { canonicalWorkloadModel, runtimeTimingIssues } from './runtimeWorkspaceModel'

const config = (extra: Partial<ScriptConfig>): ScriptConfig => ({ base_url: '', ...extra }) as ScriptConfig

describe('runtimeTimingIssues runtime semantics', () => {
  it('rejects an exponential minimum above max_ms, matching DSL validation (not the 4x engine cap)', () => {
    // validate.go rejects min_ms > max_ms for any type when max_ms > 0.
    expect(runtimeTimingIssues(config({ think_time: { type: 'exponential', mean_ms: 3000, min_ms: 6000, max_ms: 2000 } })))
      .toEqual(['Cap base must be at least the minimum think time.'])
    expect(runtimeTimingIssues(config({ think_time: { type: 'uniform', distribution: 'exponential', mean_ms: 3000, min_ms: 6000, max_ms: 2000 } as ScriptConfig['think_time'] })))
      .toEqual(['Cap base must be at least the minimum think time.'])
    expect(runtimeTimingIssues(config({ think_time: { type: 'exponential', mean_ms: 3000, min_ms: 2000, max_ms: 2000 } }))).toEqual([])
    expect(runtimeTimingIssues(config({ think_time: { type: 'exponential', mean_ms: 3000, min_ms: 6000 } }))).toEqual([])
    expect(runtimeTimingIssues(config({ think_time: { type: 'uniform', min_ms: 6000, max_ms: 2000 } })))
      .toEqual(['Maximum think time must be at least the minimum think time.'])
  })

  it('flags gamma arrivals without a burstiness in (0, 1000], matching DSL validation', () => {
    const gamma = (burstiness?: number) => config({ pacing: { type: 'tps', tps: 5, arrival_process: 'gamma', burstiness } as ScriptConfig['pacing'] })
    const issue = 'Gamma arrivals need a burstiness above 0 and at most 1000.'
    expect(runtimeTimingIssues(gamma(undefined))).toContain(issue)
    expect(runtimeTimingIssues(gamma(0))).toContain(issue)
    expect(runtimeTimingIssues(gamma(1001))).toContain(issue)
    expect(runtimeTimingIssues(gamma(1))).not.toContain(issue)
    expect(runtimeTimingIssues(config({ pacing: { type: 'tps', tps: 5, arrival_process: 'poisson' } as ScriptConfig['pacing'] }))).not.toContain(issue)
  })

  it('canonicalizes workload_model like dsl.CanonicalWorkloadModel', () => {
    expect(canonicalWorkloadModel(undefined)).toBe('closed')
    expect(canonicalWorkloadModel('  ')).toBe('closed')
    expect(canonicalWorkloadModel(' Open ')).toBe('open')
    expect(canonicalWorkloadModel('CLOSED')).toBe('closed')
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'Open' }, pacing: { type: 'none' } })))
      .toContain('Open arrival-rate requires TPS pacing and a target greater than zero.')
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'Closed', termination_mode: 'iterations', iterations: 3 } }))).toEqual([])
  })
})
