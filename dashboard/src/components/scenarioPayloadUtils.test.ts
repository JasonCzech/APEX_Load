// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'

import { buildCreateTestPayload, buildGoalConfigPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'

describe('scenarioPayloadUtils', () => {
  it('keeps a single unnamed group in the canonical groups payload', () => {
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
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
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
      assignment: { mode: 'auto' },
      groups: [{
        script_id: 'script-1',
        vusers: 25,
        ramp_up: '30s',
        ramp_down: '15s',
        initialization: 'at_runtime',
      }],
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
        max_avg_server_processing_ms: 450,
        max_avg_transfer_ms: 120,
        max_error_count: 25,
        max_lcp_ms: 2500,
        max_inp_ms: 200,
        max_cls: 0.1,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtimeSettings: {
        iterations: 5,
        pacing: { type: 'fixed_interval', interval_ms: 2500 },
        think_time: { type: 'fixed', mean_ms: 750 },
        browser: {
          headless: false,
          device_profile: 'iphone_se',
          touch_mode: true,
          orientation: 'portrait',
        },
        log_level: 'standard',
        auto_transactions: 'per_action',
        continue_on_error: true,
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
      assignment: { mode: 'auto' },
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
        max_avg_server_processing_ms: 450,
        max_avg_transfer_ms: 120,
        max_error_count: 25,
        max_lcp_ms: 2500,
        max_inp_ms: 200,
        max_cls: 0.1,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtime_settings: {
        iterations: 5,
        pacing: { type: 'fixed_interval', interval_ms: 2500 },
        think_time: { type: 'fixed', mean_ms: 750 },
        browser: {
          headless: false,
          device_profile: 'iphone_se',
          touch_mode: true,
          orientation: 'portrait',
        },
        log_level: 'standard',
        auto_transactions: 'per_action',
        error_handling: { continue_on_error: true, snapshot_on_error: true },
      },
      data_pools: [{
        pool_id: 'pool-1',
        alias: 'customers',
        access_mode: 'read_only',
        selection_strategy: 'sequential',
      }],
    })
  })

  it('keeps zero-valued browser and messaging goal thresholds', () => {
    expect(buildGoalConfigPayload('goal_oriented', {
      target_average_tps: 0,
      max_error_pct: 0,
      max_error_count: 0,
      min_vusers: 0,
      max_lcp_ms: 0,
      max_inp_ms: 0,
      max_cls: 0,
      max_throttled_requests: 0,
      max_end_to_end_latency_ms: 0,
      max_partition_skew_pct: 0,
      max_redelivery_rate_pct: 0,
      max_dead_letter_rate_pct: 0,
    })).toEqual({
      target_average_tps: 0,
      max_error_pct: 0,
      max_error_count: 0,
      min_vusers: 0,
      max_lcp_ms: 0,
      max_inp_ms: 0,
      max_cls: 0,
      max_throttled_requests: 0,
      max_end_to_end_latency_ms: 0,
      max_partition_skew_pct: 0,
      max_redelivery_rate_pct: 0,
      max_dead_letter_rate_pct: 0,
    })
  })

  it('keeps the complete group contract for one-group test runs', () => {
    const payload = buildCreateTestPayload({
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: 'script-browser',
        role: 'observer',
        vusers: 2,
        rampUp: '10s',
        rampDown: '20s',
        initialization: 'at_once',
        runtimeSettings: {
          log_level: '',
          auto_transactions: '',
          continue_on_error: false,
          snapshot_on_error: false,
          http: { version: '2' },
          env: { REGION: 'east' },
        },
        assignment: { mode: 'specific_hosts', selected_targets: ['browser-gen'] },
      }],
      goalConfig: {},
      runtimeSettings: {
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.script_id).toBeUndefined()
    expect(payload.groups).toEqual([{
      script_id: 'script-browser',
      role: 'observer',
      vusers: 2,
      ramp_up: '10s',
      ramp_down: '20s',
      initialization: 'at_once',
      runtime_settings: {
        http: { version: '2' },
        env: { REGION: 'east' },
      },
      assignment: { mode: 'specific_hosts', selected_targets: ['browser-gen'] },
    }])
  })

	it('emits per-group runtime_settings only for groups that carry a runtime override', () => {
    const payload = buildScenarioUpsertPayload({
      name: 'Per-group Runtime',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [
        {
          name: 'Group A',
          scriptId: 'script-a',
          vusers: 10,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
          runtimeSettings: {
            iterations: undefined,
            pacing: { type: 'fixed_delay', delay_ms: 250 },
            think_time: undefined,
            log_level: 'debug',
            auto_transactions: '',
            continue_on_error: false,
            snapshot_on_error: false,
          },
        },
        {
          name: 'Group B',
          scriptId: 'script-b',
          vusers: 5,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
        },
      ],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
		})

    expect(payload.groups[0].runtime_settings?.pacing).toEqual({ type: 'fixed_delay', delay_ms: 250 })
    expect(payload.groups[0].runtime_settings?.log_level).toBe('debug')
		expect(payload.groups[1].runtime_settings).toBeUndefined()
	})

	it('preserves every per-group placement mode and generator-loss policy', () => {
		const base = {
			name: 'Placement fidelity',
			duration: '5m',
			startDelay: '',
			scenarioMode: 'vuser_driven' as const,
			scriptShakeout: false,
			resourceBaseline: false,
			goalConfig: {},
			runtimeSettings: {
				log_level: '',
				auto_transactions: '',
				continue_on_error: false,
				snapshot_on_error: false,
			},
		}
		const placementCases = [
			{ assignment: { mode: 'auto' as const, required_labels: { tier: 'api' }, tolerate_generator_loss: true } },
			{ assignment: { mode: 'specific_hosts' as const, selected_targets: [' gen-a ', 'gen-b'], required_labels: { tier: 'api' }, tolerate_generator_loss: true } },
			{ assignment: { mode: 'region_weighted' as const, region_weights: { east: 60, west: 40 }, required_labels: { tier: 'api' }, tolerate_generator_loss: true } },
			{ assignment: { mode: 'load_zone_weighted' as const, load_zone_weights: { blue: 75, green: 25 }, required_labels: { tier: 'api' }, tolerate_generator_loss: true } },
		]

		for (const { assignment } of placementCases) {
			const payload = buildScenarioUpsertPayload({
				...base,
				groups: [{
					name: 'workers',
					scriptId: 'script-1',
					vusers: 10,
					rampUp: '',
					rampDown: '',
					initialization: 'at_runtime',
					assignment,
				}],
			})
			expect(payload.groups[0].assignment).toEqual({
				...assignment,
				...(assignment.mode === 'specific_hosts' ? { selected_targets: ['gen-a', 'gen-b'], shard_strategy: 'capacity_weighted' } : {}),
			})
		}
	})

  it('serializes explicit per-group placement overrides while omitting inherited groups', () => {
    const payload = buildScenarioUpsertPayload({
      name: 'Group Placement',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [
        {
          name: 'Inherited',
          scriptId: 'script-a',
          vusers: 10,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
        },
        {
          name: 'Explicit Auto',
          scriptId: 'script-b',
          vusers: 5,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
          assignment: { mode: 'auto' },
        },
        {
          name: 'Pinned JVM',
          scriptId: 'script-c',
          vusers: 5,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
          assignment: { mode: 'specific_hosts', selected_targets: ['gen-a', 'gen-b'], required_labels: { jvm: '17' } },
        },
      ],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.groups[0].assignment).toBeUndefined()
    expect(payload.groups[1].assignment).toEqual({ mode: 'auto' })
    expect(payload.groups[2].assignment).toEqual({
      mode: 'specific_hosts',
      selected_targets: ['gen-a', 'gen-b'],
      shard_strategy: 'capacity_weighted',
      required_labels: { jvm: '17' },
    })
  })

	it('preserves generator loss tolerance at both root and group scope', () => {
    const payload = buildScenarioUpsertPayload({
      name: 'Loss Tolerant',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: { mode: 'auto', tolerate_generator_loss: true },
      groups: [{
        name: 'Checkout',
        scriptId: 'script-a',
        vusers: 10,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime',
        assignment: { mode: 'auto', tolerate_generator_loss: true },
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.assignment).toEqual({ mode: 'auto', tolerate_generator_loss: true })
		expect(payload.groups[0].assignment).toEqual({ mode: 'auto', tolerate_generator_loss: true })
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
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
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
    expect(payload.assignment).toEqual({ mode: 'auto' })
    expect(payload.script_id).toBeUndefined()
  })

  it('keeps staged single-group drafts in the groups array', () => {
    const payload = buildCreateTestPayload({
      name: '',
      duration: '8m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: 'script-1',
        vusers: 25,
        rampUp: '',
        rampDown: '',
        stages: [
          { name: 'Warmup', type: 'ramp_up', duration: '2m', target_vusers: 25 },
          { name: 'Steady', type: 'hold', duration: '5m', target_vusers: 25 },
          { name: 'Cooldown', type: 'ramp_down', duration: '1m', target_vusers: 1 },
        ],
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.script_id).toBeUndefined()
    expect(payload.groups).toEqual([{
      script_id: 'script-1',
      vusers: 25,
      stages: [
        { name: 'Warmup', type: 'ramp_up', duration: '2m', target_vusers: 25 },
        { name: 'Steady', type: 'hold', duration: '5m', target_vusers: 25 },
        { name: 'Cooldown', type: 'ramp_down', duration: '1m', target_vusers: 1 },
      ],
      initialization: 'at_runtime',
    }])
  })

  it('serializes explicit hostname assignment into scenario and test payloads', () => {
    const payload = buildCreateTestPayload({
      name: 'Explicit Host Run',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: { mode: 'specific_hosts', selected_targets: ['gen-a', 'gen-b'] },
      groups: [{
        name: 'Checkout',
        scriptId: 'script-1',
        vusers: 25,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.assignment).toEqual({
      mode: 'specific_hosts',
      selected_targets: ['gen-a', 'gen-b'],
      shard_strategy: 'capacity_weighted',
    })
  })

  it('preserves explicit empty hostname assignment so validation can reject it', () => {
    const payload = buildScenarioUpsertPayload({
      name: 'Incomplete Host Pinning',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: { mode: 'specific_hosts', selected_targets: [] },
      groups: [
        {
          name: 'Pinned Group',
          scriptId: 'script-1',
          vusers: 25,
          rampUp: '',
          rampDown: '',
          initialization: 'at_runtime',
          assignment: { mode: 'specific_hosts', selected_targets: [] },
        },
      ],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })

    expect(payload.assignment).toEqual({
      mode: 'specific_hosts',
      selected_targets: [],
    })
    expect(payload.groups[0].assignment).toEqual({
      mode: 'specific_hosts',
      selected_targets: [],
    })
  })

  it('round-trips a queued single-group run into a revised create-test payload without losing runtime or goal settings', () => {
    const draft = buildScenarioDraftFromTest({
      id: 'test-1',
      name: 'Queued Checkout Run',
      status: 'QUEUED',
      total_vusers: 25,
      duration: '5m',
      start_delay: '20s',
      created_at: '2026-05-12T20:00:00Z',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      goal_config: {
        target_average_tps: 300,
        max_avg_server_processing_ms: 450,
        max_avg_transfer_ms: 120,
        max_error_count: 12,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtime_settings: {
        iterations: 5,
        pacing: { type: 'fixed_delay', delay_ms: 1200 },
        think_time: { type: 'fixed', mean_ms: 900 },
        browser: {
          headless: false,
          device_profile: 'pixel_8',
          touch_mode: true,
          orientation: 'landscape',
        },
        log_level: 'standard',
        auto_transactions: 'per_action',
        error_handling: { continue_on_error: true, snapshot_on_error: true },
      },
      groups: [],
      script_id: 'script-1',
    } as never)

    const payload = buildCreateTestPayload({
      name: draft.name,
      duration: draft.duration,
      startDelay: draft.startDelay,
      scenarioMode: draft.scenarioMode,
      scriptShakeout: draft.scriptShakeout,
      resourceBaseline: draft.resourceBaseline,
      groups: draft.groups,
      goalConfig: draft.goalConfig,
      runtimeSettings: draft.runtimeSettings,
      projectId: 'project-1',
      folderId: 'folder-1',
    })

    expect(payload).toEqual({
      name: 'Queued Checkout Run',
      duration: '5m',
      start_delay: '20s',
      project_id: 'project-1',
      folder_id: 'folder-1',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      assignment: { mode: 'auto' },
      groups: [{
        script_id: 'script-1',
        vusers: 25,
        initialization: 'at_runtime',
      }],
      goal_config: {
        target_average_tps: 300,
        max_avg_server_processing_ms: 450,
        max_avg_transfer_ms: 120,
        max_error_count: 12,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtime_settings: {
        iterations: 5,
        pacing: { type: 'fixed_delay', delay_ms: 1200 },
        think_time: { type: 'fixed', mean_ms: 900 },
        browser: {
          headless: false,
          device_profile: 'pixel_8',
          touch_mode: true,
          orientation: 'landscape',
        },
        log_level: 'standard',
        auto_transactions: 'per_action',
        error_handling: { continue_on_error: true, snapshot_on_error: true },
      },
    })
  })

  it('serializes richer runtime override fields when provided', () => {
    const payload = buildCreateTestPayload({
      name: 'Runtime Override Test',
      duration: '3m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: '',
        scriptId: 'script-1',
        vusers: 10,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime',
      }],
      goalConfig: {},
      runtimeSettings: {
        iterations: undefined,
        pacing: { type: 'tps', tps: 25 },
        think_time: { type: 'fixed', mean_ms: 500, ignore: false },
        log_level: '',
        auto_transactions: '',
        continue_on_error: true,
        snapshot_on_error: false,
      },
    })

    expect(payload.runtime_settings).toEqual({
      pacing: { type: 'tps', tps: 25 },
      think_time: { type: 'fixed', mean_ms: 500, ignore: false },
      error_handling: { continue_on_error: true },
    })
  })
})
