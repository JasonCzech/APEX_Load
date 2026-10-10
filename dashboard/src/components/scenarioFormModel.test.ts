import { describe, expect, it } from 'vitest'

import type { ScenarioFormState } from './TestHub'
import { scenarioFormStateFromStoredScenario, runtimeSettingsFromForm, replaceScenarioRuntimeDefaults, reseedScenarioGroups, seededScenarioRuntime } from './scenarioFormModel'

describe('reseedScenarioGroups', () => {
  it('preserves groups beyond the selected shape and does not invent script mappings', () => {
    const previous = [
      { name: 'Checkout', script_id: 'script-a', vusers: 10, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const },
      { name: 'Search', script_id: 'script-b', vusers: 20, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const },
      { name: 'Observe', script_id: 'script-c', vusers: 1, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const, role: 'observer' as const },
    ]

    const reseeded = reseedScenarioGroups([
      { name: 'Primary', vusers: 50 },
      { name: 'Secondary', vusers: 25 },
      { name: 'New lane', vusers: 5 },
    ], previous.slice(0, 2))
    expect(reseeded.map(group => group.script_id)).toEqual(['script-a', 'script-b', ''])

    const narrowed = reseedScenarioGroups([{ name: 'Primary', vusers: 50 }], previous)
    expect(narrowed).toHaveLength(3)
    expect(narrowed[1]).toMatchObject({ name: 'Search', script_id: 'script-b' })
    expect(narrowed[2]).toMatchObject({ name: 'Observe', script_id: 'script-c', role: 'observer' })
  })
})

it('retains percentage bounds through scenario runtime replacement and form serialization', () => {
 const form = { runtime_settings: {}, groups: [] } as unknown as ScenarioFormState
 const next = replaceScenarioRuntimeDefaults(form, { log_level: '', auto_transactions: '', think_time: { type: 'random_percentage', min_percent: 0, max_percent: 125 } })
 expect(runtimeSettingsFromForm(next).think_time).toMatchObject({ type: 'random_percentage', min_percent: 0, max_percent: 125 })
})

it('preserves group SLA overrides when applying a new load shape', () => {
  const group = { name: 'Checkout', script_id: 'script-a', vusers: 10, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const, transactionResponseTimeSlaMs: 1250 }
  const [next] = reseedScenarioGroups([{ name: 'Peak', vusers: 50 }], [group])
  expect(next.transactionResponseTimeSlaMs).toBe(1250)
  expect(next.vusers).toBe(50)
  expect(group.vusers).toBe(10)
})

it('reopens every pool binding without maintaining first-binding shadow fields', () => {
 const scenario={id:'scenario',created_at:'',updated_at:'',script_count:0,name:'Pools',duration:'1m',scripts:[],data_pools:[{pool_id:'first',alias:'customers',access_mode:'read_only',selection_strategy:'unique'},{pool_id:'second',alias:'orders',access_mode:'read_only',selection_strategy:'random'}]} as Parameters<typeof scenarioFormStateFromStoredScenario>[0]
 const form=scenarioFormStateFromStoredScenario(scenario,'script')
 expect(form.data_pools).toEqual(scenario.data_pools)
 expect(form).not.toHaveProperty('data_pool_id')
 form.data_pools![0].alias='changed'
 expect(scenario.data_pools![0].alias).toBe('customers')
})

describe('seededScenarioRuntime', () => {
  it('keeps the authored iterations, log level, auto transactions and error flags a seed\'s runtime settings leave unset', () => {
    const current = {
      run_logic: { iterations: 7, auto_transactions: true },
      log: { level: 'debug' },
      error_handling: { continue_on_error: false, snapshot_on_error: true },
      pacing: { type: 'fixed', delay_ms: 500 },
    } as const
    const seeded = seededScenarioRuntime(structuredClone(current) as never, { runtime_settings: { pacing: { type: 'none' }, think_time: { type: 'ignore' } } as never })
    expect(seeded).toEqual({
      pacing: { type: 'none' },
      think_time: { type: 'ignore' },
      iterations: 7,
      log_level: 'debug',
      auto_transactions: true,
      error_handling: { continue_on_error: false, snapshot_on_error: true },
    })
  })

  it('keeps the scenario\'s browser settings when the seed\'s runtime sets none', () => {
    const current = { browser_config: { device_profile: 'iphone_15', orientation: 'portrait', headless: true } }
    expect(seededScenarioRuntime(current as never, { runtime_settings: { pacing: { type: 'none' } } as never })).toEqual({
      pacing: { type: 'none' }, browser_config: { device_profile: 'iphone_15', orientation: 'portrait', headless: true },
    })
    // A seed that brings its own browser settings replaces them.
    expect(seededScenarioRuntime(current as never, { runtime_settings: { browser_config: { device_profile: 'desktop' } } as never })).toEqual({ browser_config: { device_profile: 'desktop' } })
  })

  it('lets the seed\'s own values replace them', () => {
    const current = { iterations: 7, log_level: 'debug', error_handling: { continue_on_error: false } }
    const seeded = seededScenarioRuntime(current as never, {
      runtime_settings: { run_logic: { iterations: 1 }, error_handling: { continue_on_error: true } } as never,
      log_level: 'info',
    })
    expect(seeded).toEqual({ run_logic: { iterations: 1 }, log_level: 'info', error_handling: { continue_on_error: true } })
  })
})
