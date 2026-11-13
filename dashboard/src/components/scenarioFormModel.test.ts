import { describe, expect, it } from 'vitest'

import type { ScenarioFormState } from '../types/scenarioAuthoring'
import {
  scenarioFormStateFromStoredScenario,
  runtimeSettingsFromForm,
  replaceScenarioRuntimeDefaults,
  reseedScenarioGroups,
  scenarioErrorHandlingFlag,
  scenarioGroupWithRuntimeSettings,
  seededScenarioRuntime,
  withScenarioErrorHandlingFlag,
} from './scenarioFormModel'
import { setOverrideField } from './runtimeSettingsModel'

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
    // Carrying an error-handling flag into a seed that has no error_handling
    // section writes a partial row: each carried value is its own path, so the
    // scripts' retry settings are not replaced by a flags-only section.
    expect(seeded).toEqual({
      pacing: { type: 'none' },
      think_time: { type: 'ignore' },
      run_logic: { iterations: 7, auto_transactions: true },
      log: { level: 'debug' },
      error_handling: { continue_on_error: false, snapshot_on_error: true },
      override_fields: [
        'pacing',
        'think_time',
        'run_logic.iterations',
        'log.level',
        'run_logic.auto_transactions',
        'error_handling.continue_on_error',
        'error_handling.snapshot_on_error',
      ],
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

it('preserves explicit live binding options through edit and clone without aliasing source columns', () => {
 const scenario = { id: 'scenario', created_at: '', updated_at: '', script_count: 0, name: 'Live', duration: '1m', scripts: [], data_pools: [{ id: 'binding', pool_id: 'pool', alias: 'work', access_mode: 'live', selection_strategy: 'lease', live_options: { profile: 'native_queue', columns: ['id'], end_mode: 'discard' } }, { id: 'project-binding', pool_id: '', scope: 'project', project_table_id: 'shared', alias: 'shared_work', access_mode: 'live', selection_strategy: 'manual' }] } as Parameters<typeof scenarioFormStateFromStoredScenario>[0]
 for (const clone of [false, true]) {
  const form = scenarioFormStateFromStoredScenario(scenario, 'script', clone)
  expect(form.data_pools![0]).toMatchObject({ access_mode: 'live', selection_strategy: 'lease', live_options: { profile: 'native_queue', columns: ['id'] } })
  expect(form.data_pools![1]).toMatchObject({ scope: 'project', project_table_id: 'shared', pool_id: '', access_mode: 'live' })
  form.data_pools![0].live_options!.columns!.push('changed')
  expect(scenario.data_pools![0].live_options!.columns).toEqual(['id'])
 }
})


describe('scenarioErrorHandlingFlag & withScenarioErrorHandlingFlag', () => {
  it('defaults continue_on_error to true when unset and preserves sibling error_handling fields when toggled', () => {
    const emptyForm = { runtime_settings: undefined, groups: [] } as unknown as ScenarioFormState
    expect(scenarioErrorHandlingFlag(emptyForm, 'continue_on_error')).toBe(true)
    expect(scenarioErrorHandlingFlag(emptyForm, 'snapshot_on_error')).toBeUndefined()

    const formWithRetries = {
      runtime_settings: {
        error_handling: {
          max_retries: 3,
          retry_delay_ms: 500,
          retry_backoff: 'exponential',
          retry_on: ['5xx', '429'],
          honor_retry_after: true,
          retry_after_max_ms: 10000,
          fail_transaction_on_error_message: true,
        },
      },
      groups: [],
    } as unknown as ScenarioFormState

    expect(scenarioErrorHandlingFlag(formWithRetries, 'continue_on_error')).toBe(true)

    const optedOut = withScenarioErrorHandlingFlag(formWithRetries, 'continue_on_error', false)
    expect(scenarioErrorHandlingFlag(optedOut, 'continue_on_error')).toBe(false)
    expect(optedOut.runtime_settings?.error_handling).toEqual({
      continue_on_error: false,
      max_retries: 3,
      retry_delay_ms: 500,
      retry_backoff: 'exponential',
      retry_on: ['5xx', '429'],
      honor_retry_after: true,
      retry_after_max_ms: 10000,
      fail_transaction_on_error_message: true,
    })

    const roundTripped = replaceScenarioRuntimeDefaults(optedOut, runtimeSettingsFromForm(optedOut))
    expect(roundTripped.runtime_settings?.error_handling).toEqual(optedOut.runtime_settings?.error_handling)
  })
})

describe('partial runtime overrides in the scenario form', () => {
  const form = (runtime?: object) => ({ name: '', duration: '1m', start_delay: '', groups: [], scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, runtime_settings: runtime }) as unknown as ScenarioFormState

  it('toggles an Advanced error-handling default as its own path, never a flags-only section', () => {
    const disabled = withScenarioErrorHandlingFlag(form(), 'continue_on_error', false)
    expect(disabled.runtime_settings).toEqual({ error_handling: { continue_on_error: false }, override_fields: ['error_handling.continue_on_error'] })
    expect(scenarioErrorHandlingFlag(disabled, 'continue_on_error')).toBe(false)
    const snapshot = withScenarioErrorHandlingFlag(disabled, 'snapshot_on_error', true)
    expect(snapshot.runtime_settings).toEqual({
      error_handling: { continue_on_error: false, snapshot_on_error: true },
      override_fields: ['error_handling.continue_on_error', 'error_handling.snapshot_on_error'],
    })
    // Back to inherit removes the path; continue-on-error is then on by default.
    const inherited = withScenarioErrorHandlingFlag(snapshot, 'continue_on_error', undefined)
    expect(inherited.runtime_settings).toEqual({ error_handling: { snapshot_on_error: true }, override_fields: ['error_handling.snapshot_on_error'] })
    expect(scenarioErrorHandlingFlag(inherited, 'continue_on_error')).toBe(true)
    // A stored v1 row is converted on the toggle without changing what it applies.
    const legacy = withScenarioErrorHandlingFlag(form({ http: { version: '2' } }), 'continue_on_error', false)
    expect(legacy.runtime_settings).toEqual({
      http: { version: '2' },
      error_handling: { continue_on_error: false },
      override_fields: ['http', 'error_handling.continue_on_error'],
    })
  })

  it('seeds a template\'s error-handling flags as their own paths when no section exists', () => {
    expect(seededScenarioRuntime(undefined, { continue_on_error: true, snapshot_on_error: true })).toEqual({
      error_handling: { continue_on_error: true, snapshot_on_error: true },
      override_fields: ['error_handling.continue_on_error', 'error_handling.snapshot_on_error'],
    })
    // A v1 base that already has an error_handling section keeps v1 semantics.
    expect(seededScenarioRuntime({ error_handling: { max_retries: 2 } }, { continue_on_error: true })).toEqual({
      error_handling: { max_retries: 2, continue_on_error: true },
    })
    // A partial base stays partial and lists what the seed writes.
    const partial = { http: { request_timeout_ms: 5 }, override_fields: ['http.request_timeout_ms'] }
    expect(seededScenarioRuntime(partial as never, { pacing: { type: 'fixed_delay', delay_ms: 100 } as never, iterations: 4 })).toEqual({
      http: { request_timeout_ms: 5 },
      pacing: { type: 'fixed_delay', delay_ms: 100 },
      run_logic: { iterations: 4 },
      override_fields: ['http.request_timeout_ms', 'pacing', 'run_logic.iterations'],
    })
  })

  it('keeps a group\'s own iterations when a partial run-logic edit leaves them alone', () => {
    const group = { name: 'g', script_id: 's', vusers: 1, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const, iterations: 3 }
    const next = scenarioGroupWithRuntimeSettings(group, setOverrideField(undefined, 'run_logic.workload_model', 'open'))
    expect(next.iterations).toBeUndefined()
    expect(next.runtimeSettings?.override_fields).toEqual(['run_logic.workload_model', 'run_logic.iterations'])
    expect(next.runtimeSettings?.run_logic).toEqual({ workload_model: 'open', iterations: 3 })
    // An edit outside run_logic leaves the compact field where it is.
    const http = scenarioGroupWithRuntimeSettings(group, setOverrideField(undefined, 'http.version', '2'))
    expect(http.iterations).toBe(3)
    expect(http.runtimeSettings?.override_fields).toEqual(['http.version'])
  })

  it('folds a stored group\'s compact iterations into a partial row as one listed path', () => {
    const stored = {
      id: 'scenario', created_at: '', updated_at: '', script_count: 1, name: 'S', duration: '1m',
      scripts: [{ script_id: 's', vusers: 1, iterations: 6, runtime_settings: { http: { version: '2' }, override_fields: ['http.version'] } }],
    } as unknown as Parameters<typeof scenarioFormStateFromStoredScenario>[0]
    const runtime = scenarioFormStateFromStoredScenario(stored, 's').groups[0].runtimeSettings
    expect(runtime?.override_fields).toEqual(['http.version', 'run_logic.iterations'])
    expect(runtime?.run_logic).toEqual({ iterations: 6 })
  })
})
