import { describe, expect, it } from 'vitest'

import type { ScenarioDraft, StoredScenario, TestSummary } from '../types'
import { buildEmptyRunDefaultOverrides, buildGroupsForRunPayload, buildRunLoadProfilePreview, buildRuntimeSettingsForRun } from './newTestRunModel'
import {
  moveScenarioDraftGroup, scenarioCreatePayload, scenarioDraftFromForm, scenarioDraftFromStoredScenario, scenarioEstimateRequest, scenarioLoadPreview,
  scenarioTimelineGroups, scenarioUpsertPayload,
} from './scenarioAssembly'
import { scenarioFormStateFromStoredScenario } from './scenarioFormModel'
import { buildInitialScenarioDraft, buildScenarioDraftFromTest, scenarioDraftReducer } from './scenarioDraftUtils'
import type { CreateTestPayload, ScenarioUpsertPayload } from './scenarioPayloadUtils'

function omit<T extends object>(value: T, keys: string[]): Partial<T> {
  return Object.fromEntries(Object.entries(value).filter(([key]) => !keys.includes(key))) as Partial<T>
}

// A saved scenario authoring every field, with explicit false, zero and
// clear values beside absent (inherited) ones.
function kitchenSinkScenario(): StoredScenario {
  return {
    id: 'scenario-1', name: 'Checkout', project_id: 'proj', workload_shape_id: 'custom', duration: '10m', start_delay: '30s',
    scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: true, created_at: '', updated_at: '', script_count: 2,
    assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090', 'gen-b:9090'], required_labels: { zone: 'east' }, tolerate_generator_loss: true, failover_policy: 'continue', recovery_spare_vusers: 0 },
    goal_config: { p95_latency_ms: 800, max_error_pct: 0, stop_on_sla_breach: false, stop_on_max_errors: true, sla_breach_window: '45s', transaction_rules: [{ transaction: 'Checkout', metric: 'p95_duration_ms', comparator: 'lte', threshold: 900 }] },
    traffic_profile: { version: 1, source: 'csv', value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '10m', target_duration: '10m', mappings: [], refreshed_at: '2026-10-08T00:00:00Z' } as unknown as StoredScenario['traffic_profile'],
    runtime_settings: {
      pacing: { type: 'fixed_delay', delay_ms: 0 },
      think_time: { type: 'fixed', mean_ms: 0, ignore: false },
      error_handling: { continue_on_error: false, snapshot_on_error: true },
      run_logic: { iterations: 3, auto_transactions: 'per_action' },
      log: { level: 'debug' },
      env: { REGION: 'east' },
    },
    data_pools: [{ pool_id: 'pool-1', pool_name: 'Users', alias: 'users', access_mode: 'read_only', selection_strategy: 'sequential' }],
    scripts: [
      { order: 0, name: 'Browse', script_id: 'script-a', vusers: 20, ramp_up: '1m', ramp_down: '30s', start_offset: '0s', initialization: 'at_once', transaction_response_time_sla_ms: 0,
        runtime_settings: { run_logic: { iterations: 0 } }, assignment: { mode: 'auto', required_labels: { tier: 'gold' } } },
      { order: 1, name: 'Watch', script_id: 'script-b', role: 'observer', vusers: 1, initialization: 'at_runtime',
        stages: [{ name: 'Warm', type: 'ramp_up', duration: '1m', target_vusers: 2 }, { name: 'Hold', type: 'hold', duration: '5m', target_vusers: 2 }] },
    ],
  }
}

// What the API stores for a scenario save.
function storedFromUpsert(payload: ScenarioUpsertPayload, id: string): StoredScenario {
  return {
    id, name: payload.name, project_id: payload.project_id, workload_shape_id: payload.workload_shape_id, duration: payload.duration,
    termination_mode: payload.termination_mode, start_delay: payload.start_delay, scenario_mode: payload.scenario_mode,
    script_shakeout: payload.script_shakeout, resource_baseline: payload.resource_baseline, assignment: payload.assignment,
    goal_config: payload.goal_config, traffic_profile: payload.traffic_profile, runtime_settings: payload.runtime_settings as StoredScenario['runtime_settings'],
    data_pools: payload.data_pools, created_at: '', updated_at: '', script_count: payload.groups.length,
    scripts: payload.groups.map((group, order) => ({
      order, name: group.name, script_id: group.script_id, role: group.role, vusers: group.vusers, ramp_up: group.ramp_up, ramp_down: group.ramp_down,
      start_offset: group.start_offset, stages: group.stages, iterations: group.iterations, runtime_settings: group.runtime_settings as StoredScenario['runtime_settings'],
      initialization: group.initialization, assignment: group.assignment, transaction_response_time_sla_ms: group.transaction_response_time_sla_ms,
    })),
  }
}

// What the API returns for a created (queued) run.
function testFromCreate(payload: CreateTestPayload, id: string): TestSummary {
  return {
    id, name: payload.name, duration: payload.duration, termination_mode: payload.termination_mode, start_delay: payload.start_delay,
    scenario_mode: payload.scenario_mode, script_shakeout: payload.script_shakeout, resource_baseline: payload.resource_baseline,
    assignment: payload.assignment, goal_config: payload.goal_config, runtime_settings: payload.runtime_settings, project_id: payload.project_id,
    application_id: payload.application_id, release_id: payload.release_id, scenario_id: payload.scenario_id, data_pools: payload.data_pools,
    groups: (payload.groups || []).map(group => ({
      name: group.name || '', script_id: group.script_id, script_name: '', protocol: 'http', role: group.role, vusers: group.vusers,
      ramp_up: group.ramp_up, ramp_down: group.ramp_down, start_offset: group.start_offset, stages: group.stages,
      iteration_override: group.iterations, iteration_override_known: true, runtime_settings: group.runtime_settings,
      initialization: group.initialization, assignment: group.assignment, transaction_response_time_sla_ms: group.transaction_response_time_sla_ms,
    })),
  } as unknown as TestSummary
}

describe('scenario assembly', () => {
  it('round-trips every field of a saved scenario through save and reload (TestHub)', () => {
    const stored = kitchenSinkScenario()
    const first = scenarioUpsertPayload(scenarioDraftFromStoredScenario(stored, { includeBrowser: true }), { projectId: 'proj', workloadShapeId: 'custom' })
    const reloaded = storedFromUpsert(first, stored.id)
    const second = scenarioUpsertPayload(scenarioDraftFromStoredScenario(reloaded, { includeBrowser: true }), { projectId: 'proj', workloadShapeId: 'custom' })
    expect(second).toEqual(first)
    // Authored values survive, including explicit false and zero.
    expect(first).toMatchObject({
      name: 'Checkout', duration: '10m', start_delay: '30s', script_shakeout: false, resource_baseline: true,
      assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090', 'gen-b:9090'], required_labels: { zone: 'east' }, tolerate_generator_loss: true, failover_policy: 'continue', recovery_spare_vusers: 0 },
      goal_config: { p95_latency_ms: 800, max_error_pct: 0, stop_on_sla_breach: false, stop_on_max_errors: true, sla_breach_window: '45s', transaction_rules: [{ transaction: 'Checkout', metric: 'p95_duration_ms', comparator: 'lte', threshold: 900 }] },
      traffic_profile: stored.traffic_profile,
      data_pools: [{ pool_id: 'pool-1', pool_name: 'Users', alias: 'users', access_mode: 'read_only', selection_strategy: 'sequential' }],
    })
    expect(first.runtime_settings).toMatchObject({
      pacing: { type: 'fixed_delay', delay_ms: 0 }, error_handling: { continue_on_error: false, snapshot_on_error: true },
      run_logic: { iterations: 3, auto_transactions: 'per_action' }, log: { level: 'debug' }, env: { REGION: 'east' },
    })
    expect(first.groups[0]).toMatchObject({ name: 'Browse', vusers: 20, ramp_up: '1m', ramp_down: '30s', initialization: 'at_once', transaction_response_time_sla_ms: 0, runtime_settings: { run_logic: { iterations: 0 } } })
    expect(first.groups[0]).not.toHaveProperty('iterations')
    expect(first.groups[1]).toMatchObject({ name: 'Watch', role: 'observer', stages: [{ name: 'Warm', type: 'ramp_up', duration: '1m', target_vusers: 2 }, { name: 'Hold', type: 'hold', duration: '5m', target_vusers: 2 }] })
  })

  it('assembles TestHub edits the way the form shows them', () => {
    const form = scenarioFormStateFromStoredScenario(kitchenSinkScenario(), '')
    // An edited compact goal field wins over the loaded goal config; a
    // cleared one removes the value.
    const edited = scenarioDraftFromForm({ ...form, goal_p95_latency_ms: 600, goal_max_error_pct: undefined }, { includeBrowser: true })
    expect(edited.goalConfig.p95_latency_ms).toBe(600)
    expect(edited.goalConfig).not.toHaveProperty('max_error_pct')
    // A scenario that ends by iterations has no duration.
    const iterations = scenarioUpsertPayload(scenarioDraftFromForm({ ...form, termination_mode: 'iterations', duration: '10m' }, { includeBrowser: true }))
    expect(iterations).toMatchObject({ termination_mode: 'iterations', duration: '' })
    // An unnamed draft saves under the caller's fallback name.
    expect(scenarioUpsertPayload(scenarioDraftFromForm({ ...form, name: '' }, { includeBrowser: true }), { fallbackName: 'Draft scenario' }).name).toBe('Draft scenario')
  })

  it('keeps absent values absent so they inherit', () => {
    const stored: StoredScenario = { ...kitchenSinkScenario(), goal_config: undefined, runtime_settings: undefined, start_delay: undefined, traffic_profile: undefined, assignment: undefined,
      scripts: [{ order: 0, script_id: 'script-a', vusers: 5 }] }
    const payload = scenarioUpsertPayload(scenarioDraftFromStoredScenario(stored, { includeBrowser: true }))
    expect(payload).not.toHaveProperty('goal_config')
    expect(payload).not.toHaveProperty('runtime_settings')
    expect(payload).not.toHaveProperty('start_delay')
    expect(payload).not.toHaveProperty('traffic_profile')
    expect(payload.groups[0]).not.toHaveProperty('runtime_settings')
    expect(payload.groups[0]).not.toHaveProperty('iterations')
    expect(payload.groups[0]).not.toHaveProperty('assignment')
  })

  it('round-trips a new run through create and its queued revision (CreateTestModal, revision editor)', () => {
    const draft = scenarioDraftFromStoredScenario(kitchenSinkScenario(), { includeBrowser: true })
    const created = scenarioCreatePayload(buildInitialScenarioDraft(draft, []), { projectId: 'proj', folderId: null })
    expect(created).toMatchObject({ project_id: 'proj', folder_id: null, name: 'Checkout' })
    // The queued run reloads into the revision editor; saving it unchanged
    // creates the same run (a new queued copy of it).
    const revision = scenarioCreatePayload(buildScenarioDraftFromTest(testFromCreate(created, 'run-1')))
    // Ownership fields come from the source run; the authored scenario is the same.
    const authored = (payload: CreateTestPayload) => omit({ ...payload, groups: payload.groups?.map(group => omit(group, ['revision_source_group_index'])) },
      ['revision_source_test_id', 'folder_id', 'project_id', 'data_pools', 'traffic_profile'])
    expect(revision.revision_source_test_id).toBe('run-1')
    expect(authored(revision)).toEqual(authored(created))
    expect(revision.groups?.map(group => group.revision_source_group_index)).toEqual([0, 1])
    // A second revision of the revision is stable.
    const again = scenarioCreatePayload(buildScenarioDraftFromTest(testFromCreate(revision, 'run-2')))
    expect({ ...again, revision_source_test_id: 'run-1' }).toEqual({ ...revision, project_id: again.project_id, folder_id: again.folder_id })
  })

  it('sends a revision clear for a source value the draft emptied, and inherits an untouched one', () => {
    const test = testFromCreate(scenarioCreatePayload(scenarioDraftFromStoredScenario(kitchenSinkScenario(), { includeBrowser: true })), 'run-1')
    const draft = buildScenarioDraftFromTest(test)
    const emptied: ScenarioDraft = { ...draft, goalConfig: {}, startDelay: '' }
    const payload = scenarioCreatePayload(emptied)
    expect(payload.goal_config).toEqual({})
    expect(payload.start_delay).toBe('0s')
    // A legacy run whose group iteration ownership is unknown never turns the
    // effective value into a group override.
    const legacy = { ...test, groups: test.groups?.map(group => ({ ...group, iteration_override: 7, iteration_override_known: false, runtime_settings: undefined })) } as TestSummary
    expect(scenarioCreatePayload(buildScenarioDraftFromTest(legacy)).groups?.every(group => group.iterations === undefined)).toBe(true)
  })

  it('keeps group identity across rename, reorder, add and remove (revision)', () => {
    const base = scenarioDraftFromStoredScenario(kitchenSinkScenario(), { includeBrowser: true })
    const three = { ...base, groups: [...base.groups, { ...base.groups[0], name: 'Search', scriptId: 'script-c' }] }
    const test = testFromCreate(scenarioCreatePayload(three), 'run-1')
    let draft = buildScenarioDraftFromTest(test)
    draft = scenarioDraftReducer(draft, { type: 'updateGroup', index: 1, patch: { name: 'Watch renamed' } })
    draft = moveScenarioDraftGroup(draft, 2, 0)
    draft = scenarioDraftReducer(draft, { type: 'removeGroup', index: 1 })
    draft = scenarioDraftReducer(draft, { type: 'addGroup', group: { ...base.groups[0], name: 'Added', scriptId: 'script-d' } })
    const groups = scenarioCreatePayload(draft).groups || []
    expect(groups.map(group => [group.name, group.revision_source_group_index, group.revision_new_group])).toEqual([
      ['Search', 2, undefined],
      ['Watch renamed', 1, undefined],
      ['Added', undefined, true],
    ])
    expect(moveScenarioDraftGroup(draft, 0, 9)).toBe(draft)
  })

  it('estimates the run the draft would create', () => {
    const draft = scenarioDraftFromStoredScenario(kitchenSinkScenario(), { includeBrowser: true })
    expect(scenarioEstimateRequest(draft, { projectId: 'proj' })).toEqual(scenarioCreatePayload(draft, { projectId: 'proj' }))
    // A host selection still being authored is estimated as automatic.
    const pending = { ...draft, assignment: { mode: 'specific_hosts' as const, selected_targets: [] } }
    expect(scenarioEstimateRequest(pending).assignment).toMatchObject({ mode: 'auto' })
    expect(scenarioCreatePayload(pending).assignment).toMatchObject({ mode: 'specific_hosts' })
  })

  it('launches a saved scenario with overrides only, and previews the effective run (NewTestPage)', () => {
    const stored = kitchenSinkScenario()
    const none = buildEmptyRunDefaultOverrides()
    // No override: nothing authored is sent, everything inherits.
    expect(buildRuntimeSettingsForRun(stored, none)).toBeUndefined()
    expect(buildGroupsForRunPayload(stored, {})).toBeUndefined()
    // The displayed preview is the stored scenario's own load profile.
    const preview = scenarioLoadPreview(scenarioDraftFromStoredScenario(stored, { includeBrowser: true }))
    expect(preview).toEqual(buildRunLoadProfilePreview(stored.scripts, stored.duration, stored.start_delay, Boolean(stored.resource_baseline), stored.traffic_profile, stored.scenario_mode, stored.goal_config?.target_average_tps))
    // A scenario without groups previews nothing (no starter group).
    expect(scenarioLoadPreview(scenarioDraftFromStoredScenario({ ...stored, scripts: [] }, { includeBrowser: true })).points).toEqual([])
  })

  it('times the composition timeline from the groups as they would be saved', () => {
    const draft = scenarioDraftFromStoredScenario(kitchenSinkScenario(), { includeBrowser: true })
    expect(scenarioTimelineGroups(draft.groups)).toEqual([
      { name: 'Browse', script_id: 'script-a', start_offset: '0s', ramp_up: '1m', ramp_down: '30s', vusers: 20 },
      { name: 'Watch', script_id: 'script-b', vusers: 2, stages: draft.groups[1].stages, role: 'observer' },
    ])
  })
})
