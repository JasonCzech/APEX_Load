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
	  expect(draft.goalConfig.sla_breach_window).toBe('30s')
	  expect(draft.runtimeSettings.snapshot_on_error).toBe(false)
	})

	it('maps a test summary into a reusable scenario draft', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-1',
	    name: 'Checkout Run',
	    status: 'RUNNING',
	    total_vusers: 30,
	    duration: '5m',
	    created_at: '2026-05-12T20:00:00Z',
	    scenario_mode: 'goal_oriented',
	    script_shakeout: true,
	    resource_baseline: true,
	    goal_config: {
	      target_average_tps: 300,
	      sla_breach_window: '45s',
	    },
	    runtime_settings: {
	      iterations: 5,
	      log_level: 'standard',
	      auto_transactions: 'per_action',
	      error_handling: { snapshot_on_error: true },
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
	  expect(draft.goalConfig.target_average_tps).toBe(300)
	  expect(draft.runtimeSettings.snapshot_on_error).toBe(true)
	  expect(draft.groups).toEqual([{
	    name: 'Checkout',
	    scriptId: 'script-1',
	    vusers: 30,
	    rampUp: '30s',
	    rampDown: '15s',
	    initialization: 'at_once',
	  }])
	})

	it('updates nested goal and runtime settings through one reducer state', () => {
		const initialDraft = buildInitialScenarioDraft(undefined, [
			{ id: 'script-1', name: 'Checkout', protocol: 'http' },
		] as never[])

		const withGoal = scenarioDraftReducer(initialDraft, {
			type: 'setGoalConfigField',
			field: 'target_average_tps',
			value: 300,
		})
		const withRuntime = scenarioDraftReducer(withGoal, {
			type: 'setRuntimeSettingField',
			field: 'snapshot_on_error',
			value: true,
		})

		expect(withRuntime.goalConfig.target_average_tps).toBe(300)
		expect(withRuntime.runtimeSettings.snapshot_on_error).toBe(true)
		expect(withRuntime.groups[0].scriptId).toBe('script-1')
		expect(withRuntime.duration).toBe('1m')
	})
})