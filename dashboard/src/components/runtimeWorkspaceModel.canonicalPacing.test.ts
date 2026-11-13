import { describe, expect, it } from 'vitest'

import type { ScriptConfig } from '../types/scripts'
import { canonicalPacingType, exponentialThinkTimeStopMs, runtimeTimingIssues } from './runtimeWorkspaceModel'

const config = (extra: Partial<ScriptConfig>): ScriptConfig => ({ base_url: '', ...extra }) as ScriptConfig
const OPEN_ISSUE = 'Open arrival-rate requires TPS pacing and a target greater than zero.'

describe('canonical pacing type (dsl.CanonicalPacingType)', () => {
  it('trims, lowercases and maps empty to none', () => {
    expect(canonicalPacingType(undefined)).toBe('none')
    expect(canonicalPacingType('  ')).toBe('none')
    expect(canonicalPacingType(' TPS ')).toBe('tps')
    expect(canonicalPacingType('Random_Delay')).toBe('random_delay')
  })
})

describe('open workload timing issue matches dsl.ValidateWorkloadSettingsIssues', () => {
  it('accepts mixed-case TPS pacing with a positive target', () => {
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'Open' }, pacing: { type: 'TPS', tps: 5 } }))).not.toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'open' }, pacing: { type: ' tps ', tps: 5 } }))).not.toContain(OPEN_ISSUE)
  })

  it('accepts tps 0 when a rate stage has a positive target_tps or end_tps', () => {
    const withStages = (stages: unknown[]) => config({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 0, rate_stages: stages } as ScriptConfig['pacing'] })
    expect(runtimeTimingIssues(withStages([{ duration: '1m', target_tps: 10 }]))).not.toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(withStages([{ duration: '1m', target_tps: 0, end_tps: 4 }]))).not.toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(withStages([{ duration: '1m', target_tps: 0, end_tps: 0 }]))).toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(withStages([]))).toContain(OPEN_ISSUE)
  })

  it('still flags non-TPS pacing, empty pacing type, and a non-finite target', () => {
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'open' }, pacing: { type: 'fixed_delay', tps: 5 } }))).toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'open' }, pacing: { type: '', tps: 5 } }))).toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: Number.POSITIVE_INFINITY } }))).toContain(OPEN_ISSUE)
    expect(runtimeTimingIssues(config({ run_logic: { workload_model: 'open' } }))).toContain(OPEN_ISSUE)
  })

  it('applies random-delay bound checks to a mixed-case pacing type', () => {
    expect(runtimeTimingIssues(config({ pacing: { type: 'Random_Delay', min_delay_ms: 5000, max_delay_ms: 1000 } })))
      .toContain('Maximum pacing delay must be at least the minimum delay.')
  })
})

describe('exponential think-time stop (engine computeThinkTime)', () => {
  it('caps at max_ms × 4 and then applies the multiplier', () => {
    expect(exponentialThinkTimeStopMs({ max_ms: 2000 })).toBe(8000)
    expect(exponentialThinkTimeStopMs({ max_ms: 2000, multiplier: 2 })).toBe(16000)
    expect(exponentialThinkTimeStopMs({ max_ms: 2000, multiplier: 0 })).toBe(8000)
    expect(exponentialThinkTimeStopMs({ max_ms: 2000, multiplier: -1 })).toBe(8000)
    expect(exponentialThinkTimeStopMs({ max_ms: 0, multiplier: 2 })).toBe(0)
  })
})
