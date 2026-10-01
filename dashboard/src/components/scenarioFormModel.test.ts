import { describe, expect, it } from 'vitest'

import type { ScenarioFormState } from './TestHub'
import { runtimeSettingsFromForm, replaceScenarioRuntimeDefaults, reseedScenarioGroups } from './scenarioFormModel'

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
 expect(runtimeSettingsFromForm(next, false)?.think_time).toMatchObject({ type: 'random_percentage', min_percent: 0, max_percent: 125 })
})

it('preserves group SLA overrides when applying a new load shape', () => {
  const group = { name: 'Checkout', script_id: 'script-a', vusers: 10, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const, transactionResponseTimeSlaMs: 1250 }
  const [next] = reseedScenarioGroups([{ name: 'Peak', vusers: 50 }], [group])
  expect(next.transactionResponseTimeSlaMs).toBe(1250)
  expect(next.vusers).toBe(50)
  expect(group.vusers).toBe(10)
})
