import { describe, expect, it } from 'vitest'

import {
  buildEffectiveScenarioGroups,
  buildGroupsForRunPayload,
  buildGroupEditDraft,
  buildGroupRuntimeTableValues,
  buildRunDefaultCards,
  buildRunLoadProfilePreview,
  buildRuntimeSettingsForRun,
  formatGroupPlacement,
  generatorCanRunGroup,
  generatorDisabledReason,
  getEffectiveRuntimeDefaults,
  groupOverrideKey,
  groupPlacementSummary,
  selectedGeneratorCapacity,
  stagedGroupPeakVUsers,
  type GroupRunOverride,
} from './newTestRunModel'
import { type StoredScenario, type SystemInventoryGenerator } from '../types'

function scenarioFixture(): StoredScenario {
  return {
    id: 'scenario-1',
    name: 'Checkout',
    duration: '10m',
    scenario_mode: 'vuser_driven',
    script_shakeout: false,
    resource_baseline: false,
    runtime_settings: {
      iterations: 3,
      log_level: 'info',
      auto_transactions: 'actions',
      error_handling: {
        continue_on_error: false,
        snapshot_on_error: true,
      },
    },
    scripts: [
      {
        order: 0,
        name: 'Browse',
        script_id: 'script-1',
        script_name: 'Browse Script',
        protocol: 'http',
        vusers: 10,
        ramp_up: '1m',
        ramp_down: '30s',
        initialization: 'at_runtime',
      },
      {
        order: 1,
        name: 'Checkout',
        script_id: 'script-2',
        script_name: 'Checkout Script',
        protocol: 'browser',
        role: 'observer',
        vusers: 5,
        ramp_up: '30s',
        ramp_down: '30s',
        iterations: 7,
        runtime_settings: {
          log_level: 'debug',
        },
      },
    ],
    script_count: 2,
    created_at: '2026-05-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
  }
}

function generatorFixture(overrides: Partial<SystemInventoryGenerator> = {}): SystemInventoryGenerator {
  return {
    target: 'gen-a:9090',
    generator_id: 'gen-a',
    region: 'east',
    status: 'connected',
    supported_protocols: ['http', 'browser'],
    compatibility_state: 'compatible',
    drain_state: 'active',
    capacity_state: 'available',
    health_score: 100,
    available_capacity: 100,
    active_vusers: 0,
    cpu_percent: 0,
    memory_bytes: 0,
    goroutine_count: 1,
    cpu_limit_cores: 4,
    memory_limit_bytes: 8_589_934_592,
    max_vusers: 100,
    ...overrides,
  }
}

describe('newTestRunModel', () => {
  it('applies run defaults without mutating scenario defaults', () => {
    const scenario = scenarioFixture()
    const effective = getEffectiveRuntimeDefaults(scenario, {
      duration: '15m',
      runtime_settings: {
        iterations: 8,
        log_level: 'trace',
        continue_on_error: true,
      },
    })

    expect(effective.iterations).toBe(8)
    expect(effective.log_level).toBe('trace')
    expect(effective.auto_transactions).toBe('actions')
    expect(effective.continue_on_error).toBe(true)
    expect(scenario.runtime_settings?.iterations).toBe(3)
    expect(scenario.runtime_settings?.log_level).toBe('info')
  })

  it('builds runtime settings payload only when run defaults are overridden', () => {
    const scenario = scenarioFixture()

    expect(buildRuntimeSettingsForRun(scenario, { runtime_settings: {} })).toBeUndefined()
    expect(buildRuntimeSettingsForRun(scenario, {
      runtime_settings: {
        iterations: 9,
        snapshot_on_error: false,
      },
    })).toEqual({
      iterations: 9,
      log_level: 'info',
      auto_transactions: 'actions',
      error_handling: {
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })
  })

  it('preserves every advanced root runtime sibling when changing one run setting', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      run_logic: { iterations: 3, auto_transactions: 'per_iteration', workload_model: 'closed', max_transactions: 500 },
      log: { level: 'standard', on_error_only: true, extended_options: ['advanced_trace'] },
      error_handling: { continue_on_error: true, snapshot_on_error: true, max_retries: 4, retry_delay_ms: 250, retry_backoff: 'exponential' },
      http: { version: '2', request_timeout_ms: 45_000, tls: { min_version: '1.3' } },
      web_runtime: { source_ips: ['10.0.0.1'], additional_attributes: { tenant: 'blue' } },
      browser_config: { engine: 'chromium', client_metrics: true, viewport: { width: 1440, height: 900 } },
      base_url: 'https://example.test',
      env: { API_TOKEN: '[REDACTED]' },
      redaction_rules: [{ pattern: 'customer-[0-9]+' }],
    }

    const payload = buildRuntimeSettingsForRun(scenario, {
      runtime_settings: { iterations: 9, snapshot_on_error: false },
    })

    expect(payload).toMatchObject({
      run_logic: { iterations: 9, auto_transactions: 'per_iteration', workload_model: 'closed', max_transactions: 500 },
      log: { level: 'standard', on_error_only: true, extended_options: ['advanced_trace'] },
      error_handling: { continue_on_error: true, snapshot_on_error: false, max_retries: 4, retry_delay_ms: 250, retry_backoff: 'exponential' },
      http: { version: '2', request_timeout_ms: 45_000, tls: { min_version: '1.3' } },
      web_runtime: { source_ips: ['10.0.0.1'], additional_attributes: { tenant: 'blue' } },
      browser_config: { engine: 'chromium', client_metrics: true, viewport: { width: 1440, height: 900 } },
      base_url: 'https://example.test',
      env: { API_TOKEN: '[REDACTED]' },
      redaction_rules: [{ pattern: 'customer-[0-9]+' }],
    })
    expect(payload?.iterations).toBeUndefined()
    expect(scenario.runtime_settings.run_logic?.iterations).toBe(3)
  })

  it('reviews group runtime with backend precedence and canonical section aliases', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      pacing: { type: 'fixed_delay', delay_ms: 100 },
      run_logic: { iterations: 6, auto_transactions: 'per_iteration' },
      log: { level: 'extended' },
      browser_config: { device_profile: 'pixel_8', headless: false },
    }
    scenario.scripts[0].runtime_settings = { pacing: { type: 'fixed_delay', delay_ms: 500 } }
    const runDefaults = { runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 200 } } }

    expect(buildGroupRuntimeTableValues(scenario.scripts[0], scenario, runDefaults).pacing).toBe('Fixed delay · 500ms')
    expect(buildGroupRuntimeTableValues(scenario.scripts[0], scenario, runDefaults, {
      runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 750 } },
    }).pacing).toBe('Fixed delay · 750ms')
    const cards = buildRunDefaultCards(scenario, { runtime_settings: {} })
    expect(cards.find(card => card.key === 'iterations')?.value).toBe('6')
    expect(cards.find(card => card.key === 'auto_transactions')?.value).toBe('per_iteration')
    expect(cards.find(card => card.key === 'log_level')?.value).toBe('extended')
  })

  it('builds effective groups and create payloads from group overrides', () => {
    const scenario = scenarioFixture()
    const firstGroup = scenario.scripts[0]
    const overrides: Record<string, GroupRunOverride> = {
      [groupOverrideKey(firstGroup)]: {
        vusers: 25,
        ramp_up: '2m',
        runtime_settings: {
          iterations: 4,
          pacing: { type: 'fixed_delay', delay_ms: 250 },
        },
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a:9090', 'gen-b:9090'],
          shard_strategy: 'capacity_weighted',
        },
      },
    }

    const effectiveGroups = buildEffectiveScenarioGroups(scenario, overrides)
    const payload = buildGroupsForRunPayload(scenario, overrides)

    expect(effectiveGroups[0].vusers).toBe(25)
    expect(scenario.scripts[0].vusers).toBe(10)
    expect(payload?.[0]).toMatchObject({
      name: 'Browse',
      script_id: 'script-1',
      vusers: 25,
      ramp_up: '2m',
      iterations: undefined,
      initialization: 'at_runtime',
      runtime_settings: {
        iterations: 4,
        pacing: { type: 'fixed_delay', delay_ms: 250 },
      },
      assignment: {
        mode: 'specific_hosts',
        selected_targets: ['gen-a:9090', 'gen-b:9090'],
        shard_strategy: 'capacity_weighted',
      },
    })
    expect(payload?.[1]).toMatchObject({
      name: 'Checkout',
      script_id: 'script-2',
      role: 'observer',
    })
  })

  it('derives staged group VUsers from the immutable peak stage target', () => {
    const scenario = scenarioFixture()
    const group = scenario.scripts[0]
    group.vusers = 30
    group.stages = [
      { type: 'ramp_up', duration: '1m', target_vusers: 10 },
      { type: 'hold', duration: '5m', target_vusers: 30 },
      { type: 'ramp_down', duration: '1m', target_vusers: 0 },
    ]
    const override = { vusers: 99, runtime_settings: { iterations: 4 } }
    const overrides = { [groupOverrideKey(group)]: override }

    expect(stagedGroupPeakVUsers(group)).toBe(30)
    expect(buildEffectiveScenarioGroups(scenario, overrides)[0].vusers).toBe(30)
    expect(buildGroupEditDraft(group, scenario, { runtime_settings: {} }, override).vusers).toBe('30')
    expect(buildGroupsForRunPayload(scenario, overrides)?.[0]).toMatchObject({
      vusers: 30,
      stages: group.stages,
      runtime_settings: { iterations: 4 },
    })
  })

  it('merges a partial per-run group edit over the complete stored group runtime', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].runtime_settings = {
      log: { level: 'extended', extended_options: ['data_returned'] },
      error_handling: { continue_on_error: true, max_retries: 3, retry_backoff: 'fixed' },
      http: { version: '2', tls: { min_version: '1.2' } },
      browser_config: { engine: 'chromium', client_metrics: true },
      env: { TOKEN: '[REDACTED]' },
    }
    const payload = buildGroupsForRunPayload(scenario, {
      [groupOverrideKey(scenario.scripts[0])]: { runtime_settings: { iterations: 12 } },
    })
    expect(payload?.[0].runtime_settings).toMatchObject({
      iterations: 12,
      log: { level: 'extended', extended_options: ['data_returned'] },
      error_handling: { continue_on_error: true, max_retries: 3, retry_backoff: 'fixed' },
      http: { version: '2', tls: { min_version: '1.2' } },
      browser_config: { engine: 'chromium', client_metrics: true },
      env: { TOKEN: '[REDACTED]' },
    })
  })

  it('round-trips weighted placement and generator-loss tolerance in the group edit draft', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].assignment = {
      mode: 'region_weighted',
      region_weights: { east: 70, west: 30 },
      required_labels: { runtime: 'go' },
      tolerate_generator_loss: true,
    }
    const draft = buildGroupEditDraft(scenario.scripts[0], scenario, { runtime_settings: {} })
    expect(draft).toMatchObject({
      assignmentMode: 'region_weighted',
      regionWeights: { east: 70, west: 30 },
      requiredLabels: { runtime: 'go' },
      tolerateGeneratorLoss: true,
    })
    expect(formatGroupPlacement(undefined, scenario.scripts[0])).toContain('Region weighted')
  })

  it('models load profile timing including resource baseline windows', () => {
    const preview = buildRunLoadProfilePreview(scenarioFixture().scripts, '10m', '30s', true)

    expect(preview.steadySeconds).toBe(600)
    expect(preview.preBaselineSeconds).toBe(330)
    expect(preview.postBaselineSeconds).toBe(300)
    expect(preview.totalSeconds).toBe(1260)
    expect(preview.peakVUsers).toBe(15)
    expect(preview.points[0]).toMatchObject({ second: 0, total: 0 })
    expect(preview.points[preview.points.length - 1]).toMatchObject({ second: 1260, total: 0 })
  })

  it('formats placement summaries and generator eligibility', () => {
    const scenario = scenarioFixture()
    const pinned: GroupRunOverride = { assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090'] } }
    const sharded: GroupRunOverride = { assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090', 'gen-b:9090'] } }
    const requiredJvm: GroupRunOverride = { assignment: { mode: 'auto', required_labels: { jvm: '17' } } }

    expect(formatGroupPlacement()).toBe('Auto')
    expect(formatGroupPlacement(pinned)).toBe('Pinned: gen-a:9090')
    expect(formatGroupPlacement(sharded)).toBe('Sharded: 2 hosts, capacity weighted')
    expect(formatGroupPlacement(requiredJvm)).toBe('Capability: jvm=17')
    expect(groupPlacementSummary({ one: pinned, two: sharded })).toBe('1 pinned group, 1 sharded group')
    const savedPinned = { ...scenario.scripts[0], assignment: { mode: 'specific_hosts' as const, selected_targets: ['gen-a:9090'] } }
    expect(groupPlacementSummary({ [groupOverrideKey(savedPinned)]: { assignment: { mode: 'auto' } } }, [
      savedPinned,
      { ...scenario.scripts[1], assignment: { mode: 'auto', required_labels: { jvm: '17' } } },
    ])).toBe('1 capability-constrained group')

    expect(generatorCanRunGroup(generatorFixture(), scenario.scripts[0])).toBe(true)
    expect(generatorCanRunGroup(generatorFixture({ supported_protocols: ['http'] }), scenario.scripts[1])).toBe(false)
    expect(generatorDisabledReason(generatorFixture({ supported_protocols: ['http'] }), scenario.scripts[1])).toBe('Does not support browser')
    expect(selectedGeneratorCapacity([
      generatorFixture({ target: 'gen-a:9090', available_capacity: 20 }),
      generatorFixture({ target: 'gen-b:9090', available_capacity: 30 }),
    ], ['gen-b:9090'])).toBe(30)
  })

  it('treats explicit auto as a run-only group placement override', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].assignment = { mode: 'specific_hosts', selected_targets: ['gen-a:9090'] }
    const override: GroupRunOverride = { assignment: { mode: 'auto' } }

    const effective = buildEffectiveScenarioGroups(scenario, { [groupOverrideKey(scenario.scripts[0])]: override })
    const payload = buildGroupsForRunPayload(scenario, { [groupOverrideKey(scenario.scripts[0])]: override })

    expect(effective[0].assignment).toEqual({ mode: 'auto' })
    expect(payload?.[0].assignment).toEqual({ mode: 'auto' })
    expect(formatGroupPlacement(override, scenario.scripts[0])).toBe('Auto')
  })

  it('builds run default cards with override status', () => {
    const cards = buildRunDefaultCards(scenarioFixture(), {
      duration: '20m',
      resource_baseline: true,
      runtime_settings: {
        log_level: 'debug',
      },
    })

    expect(cards.find(card => card.key === 'duration')).toMatchObject({ value: '20m', isOverridden: true })
    expect(cards.find(card => card.key === 'resource_baseline')).toMatchObject({ value: '5m before and after', isOverridden: true })
    expect(cards.find(card => card.key === 'log_level')).toMatchObject({ value: 'debug', scenarioValue: 'info', isOverridden: true })
  })
})
