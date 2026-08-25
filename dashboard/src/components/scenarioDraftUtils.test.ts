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
		startOffset: '',
	    iterations: undefined,
	    initialization: 'at_runtime',
	    assignment: undefined,
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
	  expect(draft.runtimeSettings.continue_on_error).toBeUndefined()
	  expect(draft.runtimeSettings.snapshot_on_error).toBeUndefined()
	  expect(draft.runtimeSettings.browser).toBeUndefined()
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
	  expect(draft.runtimeSettings.browser).toEqual({
	    headless: false,
	    device_profile: 'iphone_se',
	    touch_mode: true,
	    orientation: 'portrait',
	  })
	  expect(draft.runtimeSettings.continue_on_error).toBe(true)
	  expect(draft.runtimeSettings.snapshot_on_error).toBe(true)
	  expect(draft.groups).toEqual([{
	    name: 'Checkout',
	    scriptId: 'script-1',
	    revisionSourceGroupIndex: 0,
	    vusers: 30,
	    rampUp: '30s',
	    rampDown: '15s',
		startOffset: '',
	    iterations: undefined,
	    initialization: 'at_once',
	    assignment: undefined,
	  }])
	})

	it('preserves and clones the complete source-run revision context', () => {
	  const binding = {
	    id: 'binding-1',
	    scenario_id: 'scenario-1',
	    pool_id: 'pool-1',
	    pool_name: 'Orders',
	    alias: 'orders',
	    access_mode: 'consume',
	    selection_strategy: 'sequential',
	  }
	  const writeBinding = {
	    ...binding,
	    id: 'binding-2',
	    pool_id: 'pool-2',
	    alias: 'accounts',
	    access_mode: 'read_write',
	  }
	  const canonicalBindings = [binding, writeBinding].map(item => ({ ...item, access_mode: 'read_only' }))
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-context',
	    name: 'Context Run',
	    status: 'QUEUED',
	    application_id: 'application-1',
	    release_id: 'release-1',
	    scenario_id: 'scenario-1',
	    project_id: 'project-1',
	    folder_id: null,
	    data_pools: [binding, writeBinding],
	    total_vusers: 1,
	    duration: '1m',
	    created_at: '2026-07-19T12:00:00Z',
	    groups: [{ name: '', script_id: 'script-1', protocol: 'http', vusers: 1, initialization: 'at_runtime' }],
	  } as never)

	  expect(draft.revisionContext).toEqual({
	    sourceTestId: 'test-context',
	    applicationId: 'application-1',
	    releaseId: 'release-1',
	    scenarioId: 'scenario-1',
	    projectId: 'project-1',
	    folderId: null,
	    dataPools: canonicalBindings,
	  })

	  const initialized = buildInitialScenarioDraft(draft, [])
	  expect(initialized.revisionContext).toEqual(draft.revisionContext)
	  expect(initialized.revisionContext).not.toBe(draft.revisionContext)
	  expect(initialized.revisionContext?.dataPools).not.toBe(draft.revisionContext?.dataPools)
	  initialized.revisionContext!.dataPools[0].alias = 'changed'
	  expect(draft.revisionContext?.dataPools[0].alias).toBe('orders')
	  expect(binding.alias).toBe('orders')
	  expect(binding.access_mode).toBe('consume')
	  expect(writeBinding.access_mode).toBe('read_write')
	})

	it('preserves root and group generator assignment labels in drafts', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-1',
	    name: 'JVM Run',
	    status: 'QUEUED',
	    total_vusers: 10,
	    duration: '5m',
	    created_at: '2026-05-12T20:00:00Z',
	    assignment: { mode: 'load_zone_weighted', load_zone_weights: { zone_a: 100 }, required_labels: { arch: 'amd64' }, tolerate_generator_loss: true },
	    groups: [{
	      name: 'Java',
	      script_id: 'script-1',
	      protocol: 'java_http',
	      vusers: 10,
	      initialization: 'at_runtime',
	      assignment: { mode: 'auto', required_labels: { jvm: '17' } },
	    }],
	  } as never)

	  expect(draft.assignment).toEqual({
	    mode: 'load_zone_weighted',
	    selected_targets: [],
	    load_zone_weights: { zone_a: 100 },
	    required_labels: { arch: 'amd64' },
	    tolerate_generator_loss: true,
	  })
	  expect(draft.groups[0].assignment).toEqual({
	    mode: 'auto',
	    selected_targets: [],
	    required_labels: { jvm: '17' },
	  })
	})

	it('preserves a root fleet-pool placement restriction without adding one to groups', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-pool',
	    name: 'EU Pool Run',
	    status: 'QUEUED',
	    total_vusers: 10,
	    duration: '5m',
	    created_at: '2026-05-12T20:00:00Z',
	    assignment: { mode: 'auto', pool_id: 'pool-eu-west' },
	    groups: [{
	      name: 'HTTP',
	      script_id: 'script-1',
	      protocol: 'http',
	      vusers: 10,
	      initialization: 'at_runtime',
	      assignment: { mode: 'auto' },
	    }],
	  } as never)

	  expect(draft.assignment).toMatchObject({ mode: 'auto', pool_id: 'pool-eu-west' })
	  expect(draft.groups[0].assignment).toEqual({ mode: 'auto', selected_targets: [] })
	})

	it('preserves web_runtime source IPs (IP spoofing) when mapping a test summary into a draft', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-1',
	    name: 'Spoof Run',
	    status: 'QUEUED',
	    total_vusers: 20,
	    duration: '5m',
	    created_at: '2026-05-12T20:00:00Z',
	    runtime_settings: {
	      web_runtime: {
	        source_ips: ['10.0.0.1', '10.0.0.2', '10.0.0.3'],
	        network_simulation: { mode: 'preset', preset: 'dsl' },
	      },
	    },
	    groups: [{
	      name: 'Checkout',
	      script_id: 'script-1',
	      protocol: 'http',
	      vusers: 20,
	      initialization: 'at_runtime',
	    }],
	  } as never)

	  expect(draft.runtimeSettings.web_runtime?.source_ips).toEqual(['10.0.0.1', '10.0.0.2', '10.0.0.3'])
	  // Sibling web_runtime settings survive too (no whole-section drop).
	  expect(draft.runtimeSettings.web_runtime?.network_simulation).toEqual({ mode: 'preset', preset: 'dsl' })
	})

	it('round-trips web_runtime source IPs through buildInitialScenarioDraft', () => {
	  const seeded = buildScenarioDraftFromTest({
	    id: 'test-2',
	    name: 'Spoof Run',
	    status: 'QUEUED',
	    total_vusers: 10,
	    duration: '1m',
	    created_at: '2026-05-12T20:00:00Z',
	    runtime_settings: { web_runtime: { source_ips: ['192.168.0.5'] } },
	    groups: [{ name: '', script_id: 'script-1', protocol: 'http', vusers: 10, initialization: 'at_runtime' }],
	  } as never)

	  const reloaded = buildInitialScenarioDraft(seeded, [
	    { id: 'script-1', name: 'Checkout', protocol: 'http' },
	  ] as never[])

	  expect(reloaded.runtimeSettings.web_runtime?.source_ips).toEqual(['192.168.0.5'])
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
	      iteration_override: 3,
	      iteration_override_known: true,
	      initialization: 'at_once',
	    }],
	  } as never)

	  expect(draft.groups[0].iterations).toBe(3)
	  expect(draft.groups[0].iterationPayloadSource).toBe('top_level')
	})

	it('displays group runtime iterations without duplicating a compact override', () => {
	  const draft = buildScenarioDraftFromTest({
	    id: 'test-runtime-iterations',
	    name: 'Runtime iterations',
	    status: 'QUEUED',
	    total_vusers: 2,
	    duration: '1m',
	    created_at: '2026-07-19T12:00:00Z',
	    groups: [{
	      name: 'Runtime', script_id: 'script-1', protocol: 'http', vusers: 2,
	      iterations: 7, iteration_override_known: true, initialization: 'at_runtime',
	      runtime_settings: { run_logic: { iterations: 7, workload_model: 'closed' } },
	    }],
	  } as never)

	  expect(draft.groups[0].iterations).toBe(7)
	  expect(draft.groups[0].iterationPayloadSource).toBe('runtime')
	})

	it('does not promote inherited or legacy effective iterations into group overrides', () => {
	  const inherited = buildScenarioDraftFromTest({
	    id: 'test-inherited-iterations',
	    name: 'Inherited iterations',
	    status: 'QUEUED',
	    total_vusers: 2,
	    duration: '1m',
	    created_at: '2026-07-19T12:00:00Z',
	    runtime_settings: { run_logic: { iterations: 5 } },
	    groups: [{
	      name: 'Inherited', script_id: 'script-1', protocol: 'http', vusers: 2,
	      iterations: 5, iteration_override_known: true, initialization: 'at_runtime',
	    }],
	  } as never)
	  const legacyUnknown = buildScenarioDraftFromTest({
	    id: 'test-legacy-iterations',
	    name: 'Legacy iterations',
	    status: 'QUEUED',
	    total_vusers: 2,
	    duration: '1m',
	    created_at: '2026-07-19T12:00:00Z',
	    groups: [{
	      name: 'Legacy', script_id: 'script-1', protocol: 'http', vusers: 2,
	      iterations: 7, initialization: 'at_runtime',
	    }],
	  } as never)

	  expect(inherited.groups[0].iterations).toBeUndefined()
	  expect(legacyUnknown.groups[0].iterations).toBeUndefined()
	})

	it('preserves explicit false error handling separately from inherited settings', () => {
	  const inherited = buildScenarioDraftFromTest({
	    id: 'test-inherited',
	    name: 'Inherited Runtime',
	    status: 'QUEUED',
	    total_vusers: 1,
	    duration: '1m',
	    created_at: '2026-05-12T20:00:00Z',
	    groups: [],
	    script_id: 'script-1',
	  } as never)
	  const explicitFalse = buildScenarioDraftFromTest({
	    id: 'test-explicit',
	    name: 'Explicit Runtime',
	    status: 'QUEUED',
	    total_vusers: 1,
	    duration: '1m',
	    created_at: '2026-05-12T20:00:00Z',
	    runtime_settings: {
	      error_handling: { continue_on_error: false, snapshot_on_error: false },
	    },
	    groups: [],
	    script_id: 'script-1',
	  } as never)

	  expect(inherited.runtimeSettings.continue_on_error).toBeUndefined()
	  expect(inherited.runtimeSettings.snapshot_on_error).toBeUndefined()
	  expect(explicitFalse.runtimeSettings.continue_on_error).toBe(false)
	  expect(explicitFalse.runtimeSettings.snapshot_on_error).toBe(false)
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
		const withBrowserRuntime = scenarioDraftReducer(withRuntime, {
			type: 'setRuntimeSettingField',
			field: 'browser',
			value: { device_profile: 'pixel_8', touch_mode: true },
		})
		const withErrorHandling = scenarioDraftReducer(withBrowserRuntime, {
			type: 'setRuntimeSettingField',
			field: 'continue_on_error',
			value: true,
		})

		expect(withErrorHandling.goalConfig.max_error_count).toBe(20)
		expect(withErrorHandling.runtimeSettings.pacing).toEqual({ type: 'fixed_delay', delay_ms: 1000 })
		expect(withErrorHandling.runtimeSettings.browser).toEqual({ device_profile: 'pixel_8', touch_mode: true })
		expect(withErrorHandling.runtimeSettings.continue_on_error).toBe(true)
		expect(withErrorHandling.groups[0].scriptId).toBe('script-1')
		expect(withErrorHandling.duration).toBe('1m')
	})

	it('marks added and script-replaced revision groups as new immutable identities', () => {
		const revision = buildInitialScenarioDraft(buildScenarioDraftFromTest({
			id: 'queued-source',
			name: 'Queued source',
			status: 'QUEUED',
			total_vusers: 3,
			duration: '1m',
			created_at: '2026-07-19T12:00:00Z',
			groups: [{
				name: 'Original',
				script_id: 'script-1',
				protocol: 'http',
				vusers: 3,
				iterations: 7,
				initialization: 'at_runtime',
				runtime_settings: {
					run_logic: { iterations: 7 },
					http: { credentials: { username: '******', password: '******' } },
				},
			}],
		} as never), [])

		const replaced = scenarioDraftReducer(revision, {
			type: 'updateGroup',
			index: 0,
			patch: { scriptId: 'script-2' },
		})
		expect(replaced.groups[0]).toMatchObject({
			scriptId: 'script-2',
			revisionNewGroup: true,
		})
		expect(replaced.groups[0].revisionSourceGroupIndex).toBeUndefined()
		expect(replaced.groups[0].runtimeSettings).toBeUndefined()
		expect(replaced.groups[0].iterations).toBeUndefined()

		const added = scenarioDraftReducer(revision, {
			type: 'addGroup',
			group: buildDefaultScenarioGroup([{ id: 'script-2', name: 'Replacement', protocol: 'http' }] as never[]),
		})
		expect(added.groups[1].revisionSourceGroupIndex).toBeUndefined()
		expect(added.groups[1].revisionNewGroup).toBe(true)
	})
})
