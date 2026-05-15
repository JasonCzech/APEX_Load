import { describe, expect, it } from 'vitest'

import { buildDefaultScenarioGroup, buildInitialScenarioDraft, buildScenarioDraftFromTest, scenarioDraftReducer } from './scenarioDraftUtils'

describe('scenarioDraftUtils', () => {
	it('builds a default scenario group from the first available script', () => {
	  const group = buildDefaultScenarioGroup([
	    { id: 'script-1', name: 'Checkout', protocol: 'http' },
	  ] as never[])

	  expect(group).toEqual({
	    name: '',
	    scriptId: 'script-1',
	    vusers: 10,
	    rampUp: '',
	    rampDown: '',
	    iterations: undefined,
	    initialization: 'at_runtime',
	  })
	})

	it('initializes a default draft when no scenario or groups are provided', () => {
	  const draft = buildInitialScenarioDraft(undefined, [
	    { id: 'script-1', name: 'Checkout', protocol: 'http' },
	  ] as never[])

	  expect(draft.groups).toHaveLength(1)
	  expect(draft.groups[0].scriptId).toBe('script-1')
	  expect(draft.duration).toBe('1m')
	  expect(draft.startDelay).toBe('')
	  expect(draft.assignment).toEqual({ mode: 'auto', selected_targets: [] })
	  expect(draft.goalConfig.sla_breach_window).toBe('30s')
	  expect(draft.goalConfig.stop_on_max_errors).toBe(false)
	  expect(draft.runtimeSettings.continue_on_error).toBe(false)
	  expect(draft.runtimeSettings.snapshot_on_error).toBe(false)
	})

	it('maps a test summary into a reusable scenario draft', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-1',
	    name: 'Checkout Run',
	    status: 'RUNNING',
	    total_vusers: 30,
	    duration: '5m',
	    start_delay: '45s',
	    created_at: '2026-05-12T20:00:00Z',
	    scenario_mode: 'goal_oriented',
	    script_shakeout: true,
	    resource_baseline: true,
	    assignment: {
	      mode: 'specific_hosts',
	      selected_targets: ['gen-a'],
	    },
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
	      pacing: { type: 'fixed_delay', delay_ms: 1500 },
	      think_time: { type: 'fixed', mean_ms: 750 },
	      log_level: 'standard',
	      auto_transactions: 'per_action',
	      error_handling: { continue_on_error: true, snapshot_on_error: true },
	    },
	    groups: [{
	      name: 'Checkout',
	      script_id: 'script-1',
	      script_name: 'Checkout Script',
	      protocol: 'http',
	      vusers: 30,
	      ramp_up: '30s',
	      ramp_down: '15s',
	      initialization: 'at_once',
	    }],
	  } as never)

	  expect(draft.scenarioMode).toBe('goal_oriented')
	  expect(draft.startDelay).toBe('45s')
	  expect(draft.assignment).toEqual({ mode: 'specific_hosts', selected_targets: ['gen-a'] })
	  expect(draft.goalConfig.target_average_tps).toBe(300)
	  expect(draft.goalConfig.max_avg_server_processing_ms).toBe(450)
	  expect(draft.goalConfig.max_avg_transfer_ms).toBe(120)
	  expect(draft.goalConfig.max_error_count).toBe(12)
	  expect(draft.goalConfig.stop_on_max_errors).toBe(true)
	  expect(draft.runtimeSettings.pacing).toEqual({ type: 'fixed_delay', delay_ms: 1500 })
	  expect(draft.runtimeSettings.think_time).toEqual({ type: 'fixed', mean_ms: 750 })
	  expect(draft.runtimeSettings.continue_on_error).toBe(true)
	  expect(draft.runtimeSettings.snapshot_on_error).toBe(true)
	  expect(draft.groups).toEqual([{
	    name: 'Checkout',
	    scriptId: 'script-1',
	    vusers: 30,
	    rampUp: '30s',
	    rampDown: '15s',
	    iterations: undefined,
	    initialization: 'at_once',
	  }])
	})

	it('preserves group iterations when mapping a test summary into a reusable scenario draft', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-1',
	    name: 'Checkout Run',
	    status: 'QUEUED',
	    total_vusers: 30,
	    duration: '5m',
	    created_at: '2026-05-12T20:00:00Z',
	    groups: [{
	      name: 'Checkout',
	      script_id: 'script-1',
	      script_name: 'Checkout Script',
	      protocol: 'http',
	      vusers: 30,
	      iterations: 3,
	      initialization: 'at_once',
	    }],
	  } as never)

	  expect(draft.groups[0].iterations).toBe(3)
	})

	it('updates nested goal and runtime settings through one reducer state', () => {
		const initialDraft = buildInitialScenarioDraft(undefined, [
			{ id: 'script-1', name: 'Checkout', protocol: 'http' },
		] as never[])

		const withGoal = scenarioDraftReducer(initialDraft, {
			type: 'setGoalConfigField',
			field: 'max_error_count',
			value: 20,
		})
		const withRuntime = scenarioDraftReducer(withGoal, {
			type: 'setRuntimeSettingField',
			field: 'pacing',
			value: { type: 'fixed_delay', delay_ms: 1000 },
		})
		const withErrorHandling = scenarioDraftReducer(withRuntime, {
			type: 'setRuntimeSettingField',
			field: 'continue_on_error',
			value: true,
		})

		expect(withErrorHandling.goalConfig.max_error_count).toBe(20)
		expect(withErrorHandling.runtimeSettings.pacing).toEqual({ type: 'fixed_delay', delay_ms: 1000 })
		expect(withErrorHandling.runtimeSettings.continue_on_error).toBe(true)
		expect(withErrorHandling.groups[0].scriptId).toBe('script-1')
		expect(withErrorHandling.duration).toBe('1m')
	})
})