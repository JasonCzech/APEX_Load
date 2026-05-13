// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'

import { buildCreateTestPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'

describe('scenarioPayloadUtils', () => {
  it('flattens a single unnamed group when building a test payload', () => {
    const payload = buildCreateTestPayload({
      name: '',
      duration: '5m',
      startDelay: '20s',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: 'script-1',
        vusers: 25,
        rampUp: '30s',
        rampDown: '15s',
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        log_level: '',
        auto_transactions: '',
        snapshot_on_error: false,
      },
      projectId: 'project-1',
      folderId: 'folder-1',
    })

    expect(payload).toEqual({
      duration: '5m',
      start_delay: '20s',
      project_id: 'project-1',
      folder_id: 'folder-1',
      scenario_mode: 'vuser_driven',
      script_shakeout: false,
      resource_baseline: false,
      script_id: 'script-1',
      vusers: 25,
      ramp_up: '30s',
      ramp_down: '15s',
      initialization: 'at_runtime',
    })
  })

  it('builds a scenario upsert payload with groups, goal config, runtime settings, and data pools', () => {
    const payload = buildScenarioUpsertPayload({
      name: ' Goal Scenario ',
      duration: '10m',
      startDelay: '45s',
      scenarioMode: 'goal_oriented',
      scriptShakeout: true,
      resourceBaseline: true,
      groups: [{
        name: 'Checkout',
        scriptId: 'script-1',
        vusers: 40,
        rampUp: '2m',
        rampDown: '',
        iterations: 3,
        initialization: 'at_once',
      }],
      goalConfig: {
        target_average_tps: 300,
        max_error_count: 25,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtimeSettings: {
        iterations: 5,
        log_level: 'standard',
        auto_transactions: 'per_action',
        snapshot_on_error: true,
      },
      dataPools: [{
        pool_id: 'pool-1',
        alias: 'customers',
        access_mode: 'read_only',
        selection_strategy: 'sequential',
      }],
    })

    expect(payload).toEqual({
      name: 'Goal Scenario',
      duration: '10m',
      start_delay: '45s',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      groups: [{
        name: 'Checkout',
        script_id: 'script-1',
        vusers: 40,
        ramp_up: '2m',
        iterations: 3,
        initialization: 'at_once',
      }],
      goal_config: {
        target_average_tps: 300,
          max_error_count: 25,
          stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtime_settings: {
        iterations: 5,
        log_level: 'standard',
        auto_transactions: 'per_action',
        error_handling: { snapshot_on_error: true },
      },
      data_pools: [{
        pool_id: 'pool-1',
        alias: 'customers',
        access_mode: 'read_only',
        selection_strategy: 'sequential',
      }],
    })
  })

  it('keeps a single unnamed group in the groups array when it has an iterations override', () => {
    const payload = buildCreateTestPayload({
      name: '',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: 'script-1',
        vusers: 25,
        rampUp: '30s',
        rampDown: '',
        iterations: 4,
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        log_level: '',
        auto_transactions: '',
        snapshot_on_error: false,
      },
    })

    expect(payload.groups).toEqual([{
      script_id: 'script-1',
      vusers: 25,
      ramp_up: '30s',
      iterations: 4,
      initialization: 'at_runtime',
    }])
    expect(payload.script_id).toBeUndefined()
  })
})