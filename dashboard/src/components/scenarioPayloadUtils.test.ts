// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'

import { buildGroupAssignmentPayload, buildCreateTestPayload, buildGoalConfigPayload, buildScenarioGroupPayloads, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { buildInitialScenarioDraft, buildScenarioDraftFromTest, scenarioDraftReducer } from './scenarioDraftUtils'

describe('scenarioPayloadUtils', () => {
	it('round-trips transaction response-time defaults and group overrides in both scenario modes', () => {
		for (const scenarioMode of ['vuser_driven', 'goal_oriented'] as const) {
			const goalConfig = buildGoalConfigPayload(scenarioMode, { transaction_response_time_sla_ms: 3000 })
			expect(goalConfig?.transaction_response_time_sla_ms).toBe(3000)
			const groups = buildScenarioGroupPayloads([
				{name: 'Inherited', scriptId: 'script-1', vusers: 1, rampUp: '', rampDown: '', initialization: 'at_runtime'},
				{name: 'Override', scriptId: 'script-2', vusers: 1, rampUp: '', rampDown: '', initialization: 'at_runtime', transactionResponseTimeSlaMs: 1250},
			])
			expect(groups[0].transaction_response_time_sla_ms).toBeUndefined()
			expect(groups[1].transaction_response_time_sla_ms).toBe(1250)
		}

		const draft = buildScenarioDraftFromTest({
			id: 'queued-run', name: 'Queued run', status: 'QUEUED', groups: [{ name: 'Override', script_id: 'script-2', vusers: 1, transaction_response_time_sla_ms: 1250 }],
			duration: '1m', total_vusers: 1, created_at: '2026-09-10T00:00:00Z', scenario_mode: 'vuser_driven', goal_config: { transaction_response_time_sla_ms: 3000 },
		} as never)
		expect(draft.goalConfig.transaction_response_time_sla_ms).toBe(3000)
		expect(draft.groups[0].transactionResponseTimeSlaMs).toBe(1250)
	})

	it('retains stepped-ramp authoring intent and early-stop settings in VUser-driven payloads', () => {
		const goalConfig = buildGoalConfigPayload('vuser_driven', {
			early_stop: { enabled: true, error_rate_pct: 4.5, breach_window: '45s' },
			workload_shape_config: {
				stepped_ramp: {
					scope: 'scenario',
					scenario: { start_vusers: 0, end_vusers: 60, increment_by: 'step_count', step_count: 6, vusers_per_step: 10 },
				},
			},
		})
		expect(goalConfig).toEqual({
			early_stop: { enabled: true, error_rate_pct: 4.5, breach_window: '45s' },
			workload_shape_config: {
				stepped_ramp: {
					scope: 'scenario',
					scenario: { start_vusers: 0, end_vusers: 60, increment_by: 'step_count', step_count: 6, vusers_per_step: 10 },
				},
			},
		})
	})

	it('retains Closed VUser authoring intent and derived continuous stages', () => {
		const goalConfig = buildGoalConfigPayload('vuser_driven', {
			early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m' },
			workload_shape_config: {
				closed_vuser: {
					scope: 'scenario',
					scenario: { peak_vusers: 5, ramp_up: '2m', sustain: '65m', ramp_down_enabled: false, ramp_down: '2m' },
				},
			},
		})
		expect(goalConfig).toEqual({
			early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m' },
			workload_shape_config: {
				closed_vuser: {
					scope: 'scenario',
					scenario: { peak_vusers: 5, ramp_up: '2m', sustain: '65m', ramp_down_enabled: false, ramp_down: '2m' },
				},
			},
		})

		const groups = buildScenarioGroupPayloads([{
			name: 'checkout', scriptId: 'script-1', vusers: 5, rampUp: '2m', rampDown: '', initialization: 'at_runtime',
			stages: [
				{ type: 'ramp_up', duration: '2m', target_vusers: 5, name: 'Ramp up' },
				{ type: 'hold', duration: '1h5m', target_vusers: 5, name: 'Sustained load' },
			],
		}])
		expect(groups[0].stages).toHaveLength(2)
		expect(groups[0].ramp_up).toBe('2m')
		expect(groups[0].ramp_down).toBeUndefined()
	})

  it('round-trips a full-resolution traffic profile without converting it to stages', () => {
    const trafficProfile = {
      version: 1,
      source: { kind: 'file' as const, file: { filename: 'production.xlsx', timestamp_column: 'time', value_columns: ['orders'] } },
      value_semantics: 'tps' as const,
      runtime_target: 'tps' as const,
      interpolation: 'step' as const,
      source_duration: '24h0m0s',
      target_duration: '24h0m0s',
      gap_policy: 'hold' as const,
      mappings: [{ series_key: 'orders', series_name: 'Orders', group_name: 'checkout', peak_target: 20, points: [{ offset_ms: 0, target: 10 }, { offset_ms: 15000, target: 20 }] }],
      refreshed_at: '2026-08-27T12:00:00Z',
    }
    const payload = buildScenarioUpsertPayload({
      name: 'Production replay', duration: '24h', startDelay: '', scenarioMode: 'goal_oriented', scriptShakeout: false, resourceBaseline: false,
      groups: [{ name: 'checkout', scriptId: 'script-1', vusers: 10, rampUp: '', rampDown: '', stages: [], initialization: 'at_runtime' }],
      goalConfig: { min_vusers: 1, max_vusers: 100 }, runtimeSettings: { log_level: '', auto_transactions: '' }, trafficProfile,
    })
    expect(payload.traffic_profile).toEqual(trafficProfile)
    expect(payload.groups[0].stages).toBeUndefined()
  })

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

  it('does not duplicate canonical group runtime iterations as a compact override', () => {
    const payload = buildCreateTestPayload({
      duration: '1m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{
        name: 'Runtime iterations',
        scriptId: 'script-1',
        vusers: 2,
        rampUp: '',
        rampDown: '',
        iterations: 7,
        iterationPayloadSource: 'runtime',
        initialization: 'at_runtime',
        runtimeSettings: { log_level: '', auto_transactions: '', run_logic: { iterations: 7, workload_model: 'closed' } },
      }],
      goalConfig: {},
      runtimeSettings: { log_level: '', auto_transactions: '' },
    })

    expect(payload.groups?.[0]).not.toHaveProperty('iterations')
    expect(payload.groups?.[0].runtime_settings?.run_logic).toEqual({ iterations: 7, workload_model: 'closed' })
  })

  it('canonicalizes legacy group runtime iterations on edit and removes them on clear', () => {
    const mapped = buildScenarioDraftFromTest({
      id: 'legacy-runtime-iterations',
      name: 'Legacy runtime iterations',
      status: 'QUEUED',
      total_vusers: 2,
      duration: '1m',
      created_at: '2026-07-19T12:00:00Z',
      groups: [{
        name: 'Legacy', script_id: 'script-1', protocol: 'http', vusers: 2,
        iterations: 5, initialization: 'at_runtime',
        runtime_settings: { iterations: 5, http: { version: '2' } },
      }],
    } as never)

    expect(mapped.groups[0].iterationPayloadSource).toBe('runtime')
    const edited = scenarioDraftReducer(mapped, {
      type: 'updateGroup', index: 0, patch: { iterations: 8 },
    })
    const editedGroup = buildScenarioGroupPayloads(edited.groups)[0]
    expect(editedGroup).not.toHaveProperty('iterations')
    expect(editedGroup.runtime_settings).not.toHaveProperty('iterations')
    expect(editedGroup.runtime_settings?.run_logic).toEqual({ iterations: 8 })
    expect(editedGroup.runtime_settings?.http).toEqual({ version: '2' })

    const cleared = scenarioDraftReducer(edited, {
      type: 'updateGroup', index: 0, patch: { iterations: undefined },
    })
    const clearedGroup = buildScenarioGroupPayloads(cleared.groups)[0]
    expect(clearedGroup).not.toHaveProperty('iterations')
    expect(clearedGroup.runtime_settings).not.toHaveProperty('iterations')
    expect(clearedGroup.runtime_settings).not.toHaveProperty('run_logic')
    expect(clearedGroup.runtime_settings?.http).toEqual({ version: '2' })
  })

  it('edits and clears canonical group run-logic iterations without a compact duplicate', () => {
    const mapped = buildScenarioDraftFromTest({
      id: 'canonical-runtime-iterations',
      name: 'Canonical runtime iterations',
      status: 'QUEUED',
      total_vusers: 2,
      duration: '1m',
      created_at: '2026-07-19T12:00:00Z',
      groups: [{
        name: 'Canonical', script_id: 'script-1', protocol: 'http', vusers: 2,
        iterations: 5, initialization: 'at_runtime',
        runtime_settings: {
          iterations: 77,
          run_logic: { iterations: 5, max_transactions: 25, workload_model: 'closed' },
        },
      }],
    } as never)

    const edited = scenarioDraftReducer(mapped, {
      type: 'updateGroup', index: 0, patch: { iterations: 8 },
    })
    const editedGroup = buildScenarioGroupPayloads(edited.groups)[0]
    expect(editedGroup).not.toHaveProperty('iterations')
    expect(editedGroup.runtime_settings).not.toHaveProperty('iterations')
    expect(editedGroup.runtime_settings?.run_logic).toEqual({
      iterations: 8,
      max_transactions: 25,
      workload_model: 'closed',
    })

    const cleared = scenarioDraftReducer(edited, {
      type: 'updateGroup', index: 0, patch: { iterations: undefined },
    })
    const clearedGroup = buildScenarioGroupPayloads(cleared.groups)[0]
    expect(clearedGroup).not.toHaveProperty('iterations')
    expect(clearedGroup.runtime_settings).not.toHaveProperty('iterations')
    expect(clearedGroup.runtime_settings?.run_logic).toEqual({
      max_transactions: 25,
      workload_model: 'closed',
    })
  })

  it('retains runtime provenance for zero run-logic iterations before a later edit', () => {
    const mapped = buildScenarioDraftFromTest({
      id: 'zero-runtime-iterations',
      name: 'Zero runtime iterations',
      status: 'QUEUED',
      total_vusers: 2,
      duration: '1m',
      created_at: '2026-07-19T12:00:00Z',
      groups: [{
        name: 'Zero', script_id: 'script-1', protocol: 'http', vusers: 2,
        iterations: 0, initialization: 'at_runtime',
        runtime_settings: { run_logic: { iterations: 0, max_transactions: 25 } },
      }],
    } as never)

    expect(mapped.groups[0].iterations).toBeUndefined()
    expect(mapped.groups[0].iterationPayloadSource).toBe('runtime')
    const edited = scenarioDraftReducer(mapped, {
      type: 'updateGroup', index: 0, patch: { iterations: 4 },
    })
    const payloadGroup = buildScenarioGroupPayloads(edited.groups)[0]
    expect(payloadGroup).not.toHaveProperty('iterations')
    expect(payloadGroup.runtime_settings?.run_logic).toEqual({ iterations: 4, max_transactions: 25 })
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
          http: { version: '2' },
          env: { REGION: 'east' },
        },
        assignment: { mode: 'specific_hosts', selected_targets: ['browser-gen'] },
      }],
      goalConfig: {},
      runtimeSettings: {
        log_level: '',
        auto_transactions: '',
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
      },
    })

    expect(payload.assignment).toEqual({ mode: 'auto', tolerate_generator_loss: true })
		expect(payload.groups[0].assignment).toEqual({ mode: 'auto', tolerate_generator_loss: true })
  })

  it('preserves a continue failover policy at root scope and omits the fail default', () => {
    const base = {
      name: 'Continuity',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven' as const,
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{ name: 'Checkout', scriptId: 'script-a', vusers: 10, rampUp: '', rampDown: '', initialization: 'at_runtime' as const }],
      goalConfig: {},
      runtimeSettings: { iterations: undefined, pacing: undefined, think_time: undefined, log_level: '', auto_transactions: '' },
    }
    const continuing = buildScenarioUpsertPayload({ ...base, assignment: { mode: 'specific_hosts', selected_targets: ['gen-a'], failover_policy: 'continue', tolerate_generator_loss: true, recovery_spare_vusers: 200 } })
    expect(continuing.assignment).toMatchObject({ mode: 'specific_hosts', failover_policy: 'continue', recovery_spare_vusers: 200 })
    const failing = buildScenarioUpsertPayload({ ...base, assignment: { mode: 'auto', failover_policy: 'fail' } })
    expect(failing.assignment).toEqual({ mode: 'auto' })
    const draft = buildScenarioDraftFromTest({ id: 'run', name: 'Run', status: 'COMPLETE', total_vusers: 1, duration: '1m', created_at: '', assignment: { mode: 'auto', failover_policy: 'continue', tolerate_generator_loss: true, recovery_spare_vusers: 200 } } as never)
    expect(draft.assignment?.failover_policy).toBe('continue')
    expect(draft.assignment?.recovery_spare_vusers).toBe(200)
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
      },
    })

    expect(payload.assignment).toEqual({
      mode: 'specific_hosts',
      selected_targets: ['gen-a', 'gen-b'],
      shard_strategy: 'capacity_weighted',
    })
  })

  it('keeps a fleet-pool restriction on the root assignment only', () => {
    const draft = {
      name: 'EU-only run',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'vuser_driven' as const,
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: { mode: 'auto' as const, pool_id: ' pool-eu-west ' },
      groups: [{
        name: 'Checkout',
        scriptId: 'script-1',
        vusers: 25,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime' as const,
        assignment: { mode: 'auto' as const, pool_id: 'invalid-group-pool' },
      }],
      goalConfig: {},
      runtimeSettings: { log_level: '', auto_transactions: '' },
    }

    const scenarioPayload = buildScenarioUpsertPayload(draft)
    const testPayload = buildCreateTestPayload({
      duration: draft.duration,
      startDelay: draft.startDelay,
      scenarioMode: draft.scenarioMode,
      scriptShakeout: draft.scriptShakeout,
      resourceBaseline: draft.resourceBaseline,
      assignment: draft.assignment,
      groups: draft.groups,
      goalConfig: draft.goalConfig,
      runtimeSettings: draft.runtimeSettings,
    })

    expect(scenarioPayload.assignment).toEqual({ mode: 'auto', pool_id: 'pool-eu-west' })
    expect(testPayload.assignment).toEqual({ mode: 'auto', pool_id: 'pool-eu-west' })
    expect(scenarioPayload.groups[0].assignment).toEqual({ mode: 'auto' })
    expect(testPayload.groups![0].assignment).toEqual({ mode: 'auto' })
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

  it('round-trips the full queued-run hierarchy and data-pool snapshot into a revision payload', () => {
    const sourceBinding = {
      id: 'binding-1',
      scenario_id: 'scenario-1',
      pool_id: 'pool-1',
      pool_name: 'Orders',
      alias: 'orders',
      access_mode: 'read_only',
      selection_strategy: 'unique',
    } as const
    const draft = buildInitialScenarioDraft(buildScenarioDraftFromTest({
      id: 'test-context',
      name: 'Context Run',
      status: 'QUEUED',
      application_id: 'application-1',
      release_id: 'release-1',
      scenario_id: 'scenario-1',
      project_id: 'project-1',
      folder_id: null,
      data_pools: [sourceBinding],
      total_vusers: 1,
      duration: '1m',
      created_at: '2026-07-19T12:00:00Z',
      groups: [{ name: '', script_id: 'script-1', protocol: 'http', vusers: 1, initialization: 'at_runtime' }],
    } as never), [])
    const context = draft.revisionContext!

    const payload = buildCreateTestPayload({
      name: draft.name,
      duration: draft.duration,
      startDelay: draft.startDelay,
      scenarioMode: draft.scenarioMode,
      scriptShakeout: draft.scriptShakeout,
      resourceBaseline: draft.resourceBaseline,
      assignment: draft.assignment,
      groups: draft.groups,
      goalConfig: draft.goalConfig,
      runtimeSettings: draft.runtimeSettings,
      applicationId: context.applicationId,
      releaseId: context.releaseId,
      scenarioId: context.scenarioId,
      revisionSourceTestId: context.sourceTestId,
      projectId: context.projectId,
      folderId: context.folderId,
      dataPools: context.dataPools,
    })

    expect(payload).toEqual(expect.objectContaining({
      application_id: 'application-1',
      release_id: 'release-1',
      scenario_id: 'scenario-1',
      revision_source_test_id: 'test-context',
      project_id: 'project-1',
      folder_id: null,
      data_pools: [sourceBinding],
    }))
    expect(payload.data_pools).not.toBe(context.dataPools)

    const emptySnapshot = buildCreateTestPayload({
      duration: '1m',
      startDelay: '',
      scenarioMode: 'vuser_driven',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: draft.groups,
      goalConfig: {},
      runtimeSettings: { log_level: '', auto_transactions: '' },
      dataPools: [],
    })
    expect(emptySnapshot.data_pools).toEqual([])
  })

  it('round-trips every supported scenario execution setting through revision and create serialization', () => {
    const source = {
      id: 'test-complete',
      name: 'Complete Messaging Revision',
      status: 'QUEUED',
      total_vusers: 12,
      duration: '12m',
      start_delay: '15s',
      created_at: '2026-07-19T12:00:00Z',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      assignment: {
        mode: 'region_weighted',
        region_weights: { east: 60, west: 40 },
        required_labels: { protocol: 'messaging' },
        tolerate_generator_loss: true,
      },
      goal_config: {
        target_average_tps: 500,
        p95_latency_ms: 800,
        max_error_pct: 0,
        max_avg_server_processing_ms: 400,
        max_avg_transfer_ms: 100,
        max_error_count: 10,
        min_vusers: 2,
        max_vusers: 50,
        max_lcp_ms: 2500,
        max_inp_ms: 200,
        max_cls: 0.1,
        max_throttled_requests: 0,
        max_end_to_end_latency_ms: 500,
        max_partition_skew_pct: 0,
        max_redelivery_rate_pct: 0,
        max_dead_letter_rate_pct: 0,
        stop_on_sla_breach: true,
        stop_on_max_errors: true,
        sla_breach_window: '45s',
      },
      runtime_settings: {
        iterations: 3,
        pacing: { type: 'fixed_delay', delay_ms: 250 },
        think_time: { type: 'fixed', mean_ms: 100, ignore: false },
        browser: { headless: false, device_profile: 'pixel_8', touch_mode: false, orientation: 'landscape' },
        log_level: 'extended',
        auto_transactions: 'per_action',
        error_handling: {
          continue_on_error: false,
          snapshot_on_error: false,
          max_retries: 0,
          retry_delay_ms: 0,
          retry_backoff: 'fixed',
        },
        log: { level: 'extended', on_error_only: false, extended_options: ['request_headers'] },
        run_logic: { iterations: 3, max_transactions: 100, auto_transactions: 'per_iteration' },
        http: { version: '2', keep_alive: false, request_timeout_ms: 5000 },
        web_runtime: { source_ips: ['10.0.0.1'], network_simulation: { mode: 'preset', preset: 'dsl' } },
        browser_config: { engine: 'chromium', headless: false, viewport: { width: 1280, height: 720 } },
        base_url: 'https://example.test',
        env: { REGION: 'east', TENANT: 'alpha' },
        redaction_rules: [{ name: 'token', pattern: 'token=[^&]+', replacement: 'token=[redacted]' }],
      },
      groups: [{
        name: 'Messaging observer',
        script_id: 'script-messaging',
        script_name: 'Messaging Script',
        protocol: 'kafka',
        role: 'observer',
        vusers: 12,
        ramp_up: '30s',
        ramp_down: '20s',
        stages: [
          { name: 'Ramp', type: 'ramp_up', duration: '2m', target_vusers: 12, target_tps: 200 },
          { name: 'Hold', type: 'hold', duration: '9m', target_vusers: 12, target_tps: 500 },
        ],
        iterations: 4,
        initialization: 'at_once',
        runtime_settings: {
          iterations: 4,
          pacing: { type: 'tps', tps: 25 },
          error_handling: { continue_on_error: true, snapshot_on_error: false },
          base_url: 'https://group.example.test',
          env: { TOPIC: 'orders' },
          redaction_rules: [{ pattern: 'secret', replacement: '[redacted]' }],
        },
        assignment: { mode: 'auto', required_labels: { broker: 'kafka' }, tolerate_generator_loss: true },
      }],
    } as const

    const mapped = buildScenarioDraftFromTest(source as never)
    const reloaded = buildInitialScenarioDraft(mapped, [])
    const payload = buildCreateTestPayload({
      name: reloaded.name,
      duration: reloaded.duration,
      startDelay: reloaded.startDelay,
      scenarioMode: reloaded.scenarioMode,
      scriptShakeout: reloaded.scriptShakeout,
      resourceBaseline: reloaded.resourceBaseline,
      assignment: reloaded.assignment,
      groups: reloaded.groups,
      goalConfig: reloaded.goalConfig,
      runtimeSettings: reloaded.runtimeSettings,
    })

    expect(payload.goal_config).toEqual(source.goal_config)
    const expectedCanonicalRuntime: Record<string, unknown> = { ...source.runtime_settings }
    delete expectedCanonicalRuntime.iterations
    delete expectedCanonicalRuntime.browser
    delete expectedCanonicalRuntime.log_level
    delete expectedCanonicalRuntime.auto_transactions
    expect(payload.runtime_settings).toEqual(expectedCanonicalRuntime)
    expect(payload.groups).toEqual([{
      name: 'Messaging observer',
      script_id: 'script-messaging',
      revision_source_group_index: 0,
      role: 'observer',
      vusers: 12,
      ramp_up: '30s',
      ramp_down: '20s',
      stages: source.groups[0].stages,
      initialization: 'at_once',
      runtime_settings: source.groups[0].runtime_settings,
      assignment: { mode: 'auto', required_labels: { broker: 'kafka' }, tolerate_generator_loss: true },
    }])

    expect(reloaded.runtimeSettings).not.toBe(mapped.runtimeSettings)
    expect(reloaded.runtimeSettings.env).not.toBe(mapped.runtimeSettings.env)
    expect(reloaded.groups[0].stages).not.toBe(mapped.groups[0].stages)
    expect(reloaded.groups[0].runtimeSettings).not.toBe(mapped.groups[0].runtimeSettings)
    reloaded.runtimeSettings.env!.REGION = 'mutated'
    reloaded.groups[0].stages![0].duration = '99m'
    expect(source.runtime_settings.env.REGION).toBe('east')
    expect(source.groups[0].stages[0].duration).toBe('2m')
  })

  it('serializes a scenario-level java_vuser override into the runtime_settings payload', () => {
    const payload = buildCreateTestPayload({
      name: 'Java scenario',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'load_test',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{ name: 'JavaLoad', script_id: 'j1', vusers: 10 }],
      runtimeSettings: {
        log_level: '',
        auto_transactions: '',
        java_vuser: {
          jdk_selection: 'specified',
          jdk_home: '/opt/jdk-21',
          additional_vm_parameters: ['-Xmx1g'],
          classpath: ['/opt/app.jar'],
          skip_parameter_evaluation: true,
        },
      },
    } as never)
    expect(payload.runtime_settings?.java_vuser).toEqual({
      jdk_selection: 'specified',
      jdk_home: '/opt/jdk-21',
      additional_vm_parameters: ['-Xmx1g'],
      classpath: ['/opt/app.jar'],
      skip_parameter_evaluation: true,
    })
  })

  it('drops an all-empty java_vuser override and omits runtime_settings entirely', () => {
    const payload = buildCreateTestPayload({
      name: 'Empty',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'load_test',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{ name: 'JavaLoad', script_id: 'j1', vusers: 10 }],
      runtimeSettings: { log_level: '', auto_transactions: '', java_vuser: {} },
    } as never)
    expect(payload.runtime_settings).toBeUndefined()
  })

  it('round-trips a stored per-group java_vuser override through revision without loss', () => {
    const source = {
      id: 'java-rev',
      name: 'Java Revision',
      status: 'QUEUED',
      total_vusers: 10,
      duration: '5m',
      created_at: '2026-07-23T12:00:00Z',
      groups: [{
        name: 'JavaLoad',
        script_id: 'j1',
        protocol: 'java_vuser',
        vusers: 10,
        initialization: 'at_runtime',
        runtime_settings: {
          java_vuser: { skip_parameter_evaluation: true, classpath: ['/opt/app.jar'] },
        },
      }],
    } as const

    const mapped = buildScenarioDraftFromTest(source as never)
    const reloaded = buildInitialScenarioDraft(mapped, [])
    const payload = buildCreateTestPayload({
      name: reloaded.name,
      duration: reloaded.duration,
      startDelay: reloaded.startDelay,
      scenarioMode: reloaded.scenarioMode,
      scriptShakeout: reloaded.scriptShakeout,
      resourceBaseline: reloaded.resourceBaseline,
      assignment: reloaded.assignment,
      groups: reloaded.groups,
      goalConfig: reloaded.goalConfig,
      runtimeSettings: reloaded.runtimeSettings,
    })
    expect(payload.groups?.[0].runtime_settings?.java_vuser).toEqual({
      skip_parameter_evaluation: true,
      classpath: ['/opt/app.jar'],
    })
  })

  it('serializes a scenario-level browser_emulation override into the runtime_settings payload', () => {
    const payload = buildCreateTestPayload({
      name: 'BE scenario',
      duration: '5m',
      startDelay: '',
      scenarioMode: 'load_test',
      scriptShakeout: false,
      resourceBaseline: false,
      groups: [{ name: 'HttpLoad', script_id: 'h1', vusers: 10 }],
      runtimeSettings: {
        log_level: '',
        auto_transactions: '',
        browser_emulation: {
          mode: 'specific',
          browser_type: 'chrome',
          browser_version: '120',
          platform: 'windows',
          cache: { simulate_cache: true, download_non_html_resources: true },
        },
      },
    } as never)
    expect(payload.runtime_settings?.browser_emulation).toEqual({
      mode: 'specific',
      browser_type: 'chrome',
      browser_version: '120',
      platform: 'windows',
      cache: { simulate_cache: true, download_non_html_resources: true },
    })
  })

  it('round-trips a stored per-group browser_emulation override through revision without loss', () => {
    const source = {
      id: 'be-rev',
      name: 'BE Revision',
      status: 'QUEUED',
      total_vusers: 10,
      duration: '5m',
      created_at: '2026-07-23T12:00:00Z',
      groups: [{
        name: 'HttpLoad',
        script_id: 'h1',
        protocol: 'http',
        vusers: 10,
        initialization: 'at_runtime',
        runtime_settings: {
          browser_emulation: { mode: 'custom', custom_user_agent: 'MyAgent/2.0', cache: { new_user_each_iteration: true } },
        },
      }],
    } as const

    const mapped = buildScenarioDraftFromTest(source as never)
    const reloaded = buildInitialScenarioDraft(mapped, [])
    const payload = buildCreateTestPayload({
      name: reloaded.name,
      duration: reloaded.duration,
      startDelay: reloaded.startDelay,
      scenarioMode: reloaded.scenarioMode,
      scriptShakeout: reloaded.scriptShakeout,
      resourceBaseline: reloaded.resourceBaseline,
      assignment: reloaded.assignment,
      groups: reloaded.groups,
      goalConfig: reloaded.goalConfig,
      runtimeSettings: reloaded.runtimeSettings,
    })
    expect(payload.groups?.[0].runtime_settings?.browser_emulation).toEqual({
      mode: 'custom',
      custom_user_agent: 'MyAgent/2.0',
      cache: { new_user_each_iteration: true },
    })
  })

  it('applies queued-revision compact edits to canonical runtime sections', () => {
    const mapped = buildScenarioDraftFromTest({
      id: 'canonical-revision',
      name: 'Canonical Revision',
      status: 'QUEUED',
      total_vusers: 5,
      duration: '5m',
      created_at: '2026-07-19T12:00:00Z',
      groups: [{
        name: 'Canonical group',
        script_id: 'script-1',
        protocol: 'browser',
        vusers: 5,
        iterations: 88,
        initialization: 'at_runtime',
        runtime_settings: {
          iterations: 77,
          run_logic: { iterations: 2, max_transactions: 25, workload_model: 'closed' },
        },
      }],
      script_id: 'script-1',
      runtime_settings: {
        iterations: 91,
        auto_transactions: 'none',
        log_level: 'disabled',
        browser: { headless: true, device_profile: 'legacy-device', touch_mode: true, orientation: 'landscape' },
        run_logic: {
          iterations: 3,
          auto_transactions: 'per_iteration',
          max_transactions: 100,
          workload_model: 'open',
          action_sequence: ['login', 'browse'],
        },
        log: {
          level: 'extended',
          on_error_only: true,
          extended_options: ['advanced_trace'],
        },
        browser_config: {
          engine: 'firefox',
          headless: false,
          device_profile: 'pixel_8',
          client_metrics: true,
          viewport: { width: 1440, height: 900 },
        },
      },
    } as never)

    // Canonical sections win during modal hydration even when conflicting legacy
    // aliases are present in the stored queued run.
    expect(mapped.runtimeSettings.iterations).toBe(3)
    expect(mapped.runtimeSettings.auto_transactions).toBe('per_iteration')
    expect(mapped.runtimeSettings.log_level).toBe('extended')
    expect(mapped.runtimeSettings.browser).toEqual({
      headless: false,
      device_profile: 'pixel_8',
      touch_mode: undefined,
      orientation: undefined,
    })
    expect(mapped.groups[0].iterations).toBe(2)

    const edits = [
      { type: 'setRuntimeSettingField' as const, field: 'iterations' as const, value: 7 },
      { type: 'setRuntimeSettingField' as const, field: 'auto_transactions' as const, value: 'per_action' },
      { type: 'setRuntimeSettingField' as const, field: 'log_level' as const, value: 'standard' },
      {
        type: 'setRuntimeSettingField' as const,
        field: 'browser' as const,
        value: { headless: true, device_profile: 'iphone_15_pro', touch_mode: false, orientation: 'portrait' },
      },
    ]
    const rootRevised = edits.reduce(scenarioDraftReducer, mapped)
    const revised = scenarioDraftReducer(rootRevised, {
      type: 'updateGroup',
      index: 0,
      patch: { iterations: 8 },
    })
    const payload = buildCreateTestPayload({
      name: revised.name,
      duration: revised.duration,
      startDelay: revised.startDelay,
      scenarioMode: revised.scenarioMode,
      scriptShakeout: revised.scriptShakeout,
      resourceBaseline: revised.resourceBaseline,
      assignment: revised.assignment,
      groups: revised.groups,
      goalConfig: revised.goalConfig,
      runtimeSettings: revised.runtimeSettings,
    })

    expect(payload.runtime_settings?.run_logic).toEqual({
      iterations: 7,
      auto_transactions: 'per_action',
      max_transactions: 100,
      workload_model: 'open',
      action_sequence: ['login', 'browse'],
    })
    expect(payload.runtime_settings?.log).toEqual({
      level: 'standard',
      on_error_only: true,
      extended_options: ['advanced_trace'],
    })
    expect(payload.runtime_settings?.browser_config).toEqual({
      engine: 'firefox',
      headless: true,
      device_profile: 'iphone_15_pro',
      touch_mode: false,
      orientation: 'portrait',
      client_metrics: true,
      viewport: { width: 1440, height: 900 },
    })
    expect(payload.runtime_settings).not.toHaveProperty('iterations')
    expect(payload.runtime_settings).not.toHaveProperty('auto_transactions')
    expect(payload.runtime_settings).not.toHaveProperty('log_level')
    expect(payload.runtime_settings).not.toHaveProperty('browser')
    expect(payload.groups?.[0]).not.toHaveProperty('iterations')
    expect(payload.groups?.[0].runtime_settings?.run_logic).toEqual({
      iterations: 8,
      max_transactions: 25,
      workload_model: 'closed',
    })
    expect(payload.groups?.[0].runtime_settings).not.toHaveProperty('iterations')
  })

  it('distinguishes inherited error handling from an explicit false override', () => {
    const inherited = buildScenarioDraftFromTest({
      id: 'inherit', name: 'Inherited', status: 'QUEUED', total_vusers: 1,
      duration: '1m', created_at: '2026-07-19T12:00:00Z', groups: [], script_id: 'script-1',
    } as never)
    const explicitFalse = buildScenarioDraftFromTest({
      id: 'false', name: 'Explicit false', status: 'QUEUED', total_vusers: 1,
      duration: '1m', created_at: '2026-07-19T12:00:00Z', groups: [], script_id: 'script-1',
      runtime_settings: { error_handling: { continue_on_error: false, snapshot_on_error: false } },
    } as never)

    const serialize = (draft: ReturnType<typeof buildScenarioDraftFromTest>) => buildCreateTestPayload({
      duration: draft.duration,
      startDelay: draft.startDelay,
      scenarioMode: draft.scenarioMode,
      scriptShakeout: draft.scriptShakeout,
      resourceBaseline: draft.resourceBaseline,
      assignment: draft.assignment,
      groups: draft.groups,
      goalConfig: draft.goalConfig,
      runtimeSettings: draft.runtimeSettings,
    })

    expect(serialize(inherited).runtime_settings).toBeUndefined()
    expect(serialize(explicitFalse).runtime_settings).toEqual({
      error_handling: { continue_on_error: false, snapshot_on_error: false },
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
      error_handling: { continue_on_error: true, snapshot_on_error: false },
    })
  })

  it('adds ELK capture only when a ready connection was selected', () => {
    const common = {
      duration: '1m', startDelay: '', scenarioMode: 'vuser_driven' as const, scriptShakeout: false,
      resourceBaseline: false, groups: [{ name: '', scriptId: 'script-1', vusers: 1, rampUp: '', rampDown: '', initialization: 'at_runtime' as const }],
      goalConfig: {}, runtimeSettings: { log_level: '', auto_transactions: '' },
    }
    expect(buildCreateTestPayload(common).elk_capture).toBeUndefined()
    expect(buildCreateTestPayload({ ...common, elkConnectionId: 'elk-a' }).elk_capture).toEqual({ connection_id: 'elk-a' })
  })

  it('adds Dynatrace capture only when a ready connection was selected', () => {
    const common = {
      duration: '1m', startDelay: '', scenarioMode: 'vuser_driven' as const, scriptShakeout: false,
      resourceBaseline: false, groups: [{ name: '', scriptId: 'script-1', vusers: 1, rampUp: '', rampDown: '', initialization: 'at_runtime' as const }],
      goalConfig: {}, runtimeSettings: { log_level: '', auto_transactions: '' },
    }
    expect(buildCreateTestPayload(common).dynatrace_capture).toBeUndefined()
    expect(buildCreateTestPayload({ ...common, dynatraceConnectionId: 'dt-a' }).dynatrace_capture).toEqual({ connection_id: 'dt-a' })
  })
})

it('retains an explicit Java profile through group payload serialization without aliasing', () => {
  const java_host = { profile_id: 'approved', preparation_ms: 120000, run_lifetime_ms: 3600000 }
  for (const assignment of [{ java_host }, { mode: 'inherit' as const, java_host }, { mode: 'auto' as const, java_host }, { mode: 'specific_hosts' as const, selected_targets: ['gen-a'], java_host }]) {
    const payload = buildGroupAssignmentPayload(assignment)
    expect(payload?.java_host).toEqual(java_host)
    if (assignment.mode === 'inherit') expect(payload?.mode).toBe('inherit')
    if (!('mode' in assignment)) expect(payload?.mode).toBe('auto')
    payload!.java_host!.profile_id = 'changed'
    expect(java_host.profile_id).toBe('approved')
  }
  expect(buildGroupAssignmentPayload({ mode: 'inherit' })).toBeUndefined()
})
