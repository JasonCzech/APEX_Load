// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { buildCreateTestPayload, buildRuntimeSettingsPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { buildRuntimeSettingsDraft, buildScenarioDraftFromTest, scenarioDraftReducer } from './scenarioDraftUtils'
import { buildGroupsForRunPayload, mergeRuntimeSettingsForRun } from './newTestRunModel'
import type { RuntimeSettingsOverride, TestSummary } from '../types'

const sections: RuntimeSettingsOverride = {
  results: { message_detail: { mode: 'off', include_resources: false, sample_rate: 0, max_items: 0 }, property_analytics: [{ name: 'category', extraction: 'category', values: ['one'] }] },
  websocket: { callback_interval_ms: 0, read_timeout_ms: 0, write_timeout_ms: 0, max_message_size: 0, compression: false, reconnect: false },
}

describe('scenario authoring fidelity', () => {
  it.each<Array<[string, RuntimeSettingsOverride | undefined]>[number]>([
    ['absent', undefined],
    ['present empty', { results: {}, websocket: {} }],
    ['explicit false and zero', sections],
  ])('retains %s root and group sections through actual scenario and create payload builders', (_, value) => {
    const root = buildRuntimeSettingsDraft(value)
    const group = buildRuntimeSettingsDraft(value)
    const input = { name: 'rename only', workloadShapeId: 'shape', duration: '1m', startDelay: '', scenarioMode: 'vuser_driven' as const, scriptShakeout: false, resourceBaseline: false, goalConfig: {}, runtimeSettings: root, groups: [{ name: 'group', scriptId: 'script', vusers: 1, rampUp: '', rampDown: '', initialization: 'at_runtime' as const, runtimeSettings: group }] }
    const scenario = buildScenarioUpsertPayload(input)
    const run = buildCreateTestPayload(input)
    expect(scenario.workload_shape_id).toBe('shape')
    for (const payload of [scenario, run]) {
      expect(payload.runtime_settings?.results).toEqual(value?.results)
      expect(payload.runtime_settings?.websocket).toEqual(value?.websocket)
      expect(payload.groups?.[0].runtime_settings?.results).toEqual(value?.results)
      expect(payload.groups?.[0].runtime_settings?.websocket).toEqual(value?.websocket)
    }
    if (value?.results?.property_analytics) {
      scenario.runtime_settings!.results!.property_analytics![0].values[0] = 'mutated'
      expect(value.results.property_analytics[0].values).toEqual(['one'])
      expect(run.runtime_settings!.results!.property_analytics![0].values).toEqual(['one'])
    }
  })

  it('retains actual queued-copy workload intent and both section levels through an unrelated edit', () => {
    const goal = { workload_shape_config: { closed_vuser: { scope: 'scenario' as const, scenario: { peak_vusers: 5, ramp_up: '1m', sustain: '2m', ramp_down_enabled: false, ramp_down: '0s' } } } }
    const source = { id: 'queued', name: 'queued', status: 'QUEUED', duration: '3m', total_vusers: 5, created_at: '2026-10-05T00:00:00Z', scenario_mode: 'vuser_driven', goal_config: goal, runtime_settings: sections, groups: [{ name: 'group', script_id: 'script', vusers: 5, runtime_settings: sections, transaction_response_time_sla_ms: 1250 }] } as TestSummary
    const initial = buildScenarioDraftFromTest(source)
    const draft = scenarioDraftReducer(initial, { type: 'setField', field: 'name', value: 'copy' })
    const payload = buildCreateTestPayload({ name: draft.name, duration: draft.duration, startDelay: draft.startDelay, scenarioMode: draft.scenarioMode, scriptShakeout: draft.scriptShakeout, resourceBaseline: draft.resourceBaseline, assignment: draft.assignment, groups: draft.groups, goalConfig: draft.goalConfig, runtimeSettings: draft.runtimeSettings, revisionSourceTestId: source.id })
    expect(payload.revision_source_test_id).toBe(source.id)
    expect(payload.goal_config?.workload_shape_config).toEqual(goal.workload_shape_config)
    expect(payload.runtime_settings?.results).toEqual(sections.results)
    expect(payload.runtime_settings?.websocket).toEqual(sections.websocket)
    expect(payload.groups?.[0].runtime_settings?.websocket).toEqual(sections.websocket)
    expect(payload.groups?.[0].transaction_response_time_sla_ms).toBe(1250)
    expect(source.name).toBe('queued')
    payload.runtime_settings!.results!.property_analytics![0].values[0] = 'copy-only'
    expect(source.runtime_settings!.results!.property_analytics![0].values).toEqual(['one'])
  })

  it.each(['load', 'observer'] as const)('retains %s role semantics through the actual queued-copy draft and compact payload', role => {
    const source = { id: 'queued', name: 'queued', status: 'QUEUED', duration: '1m', total_vusers: 2, created_at: '2026-10-05T00:00:00Z', scenario_mode: 'vuser_driven', groups: [{ name: 'browser', script_id: 'script', protocol: 'browser', role, vusers: 2 }] } as TestSummary
    const draft = buildScenarioDraftFromTest(source)
    const payload = buildCreateTestPayload({ name: draft.name, duration: draft.duration, startDelay: draft.startDelay, scenarioMode: draft.scenarioMode, scriptShakeout: draft.scriptShakeout, resourceBaseline: draft.resourceBaseline, groups: draft.groups, goalConfig: draft.goalConfig, runtimeSettings: draft.runtimeSettings, revisionSourceTestId: source.id })
    expect(draft.groups[0].role).toBe(role)
    expect(payload.groups?.[0].role).toBe(role === 'load' ? undefined : 'observer')
    expect(payload.groups?.[0].role ?? 'load').toBe(role)
    expect(source.groups?.[0].role).toBe(role)
  })

  it('rebuilds an overridden run group without dropping offsets, SLA or other authored group fields', () => {
    const group = { order: 0, name: 'group', script_id: 'script', script_name: 'Script', protocol: 'http', vusers: 2, ramp_up: '3s', ramp_down: '4s', start_offset: '5s', stages: [{ type: 'hold', duration: '20s', target_vusers: 2 }], iterations: 3, initialization: 'at_once', role: 'load', transaction_response_time_sla_ms: 1250, runtime_settings: sections }
    const scenario = { scripts: [group] } as unknown as Parameters<typeof buildGroupsForRunPayload>[0]
    const groups = buildGroupsForRunPayload(scenario, { '0:script': { initialization: 'at_runtime' } })
    expect(JSON.parse(JSON.stringify(groups))).toEqual([{ name: 'group', script_id: 'script', role: 'load', vusers: 2, ramp_up: '3s', ramp_down: '4s', start_offset: '5s', stages: group.stages, iterations: 3, initialization: 'at_runtime', transaction_response_time_sla_ms: 1250, runtime_settings: sections }])
    expect(group.vusers).toBe(2)
    expect(group.start_offset).toBe('5s')
    expect(group.transaction_response_time_sla_ms).toBe(1250)
  })

  it('distinguishes an untouched overlay, whole empty replacement and explicit run-only clear without leaking null', () => {
    const untouched = mergeRuntimeSettingsForRun(sections, {})
    expect(buildRuntimeSettingsPayload(buildRuntimeSettingsDraft(untouched))?.websocket).toEqual(sections.websocket)
    const replaced = mergeRuntimeSettingsForRun(sections, { results: {}, websocket: {} })
    expect(buildRuntimeSettingsPayload(buildRuntimeSettingsDraft(replaced))).toMatchObject({ results: {}, websocket: {} })
    const cleared = mergeRuntimeSettingsForRun(sections, { results: null, websocket: null } as unknown as RuntimeSettingsOverride)
    expect(buildRuntimeSettingsPayload(buildRuntimeSettingsDraft(cleared))).toBeUndefined()
    expect(sections.websocket?.compression).toBe(false)
  })
})
