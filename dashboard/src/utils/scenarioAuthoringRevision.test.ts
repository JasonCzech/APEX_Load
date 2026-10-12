import { describe, expect, it } from 'vitest'
import { ApiError } from '../hooks/apiClient'
import { scenarioAuthoringFixture, withScenarioAuthoring } from '../test/scenarioAuthoringFixtures'
import { authoringChangedPaths, reapplyAuthoringDraft } from './authoringRevision'
import { adoptableAfterBindingsSave, admitScenarioAuthoringBaseline, baselineFromSavedScenario, scenarioAuthoringTag, scenarioBaselineFromRead, scenarioSaveNeedsReconciliation, ScenarioSaveOutcomeUnknown } from './scenarioAuthoringRevision'

const revision = `scn-v1-${'a'.repeat(64)}`

describe('scenario authoring revisions', () => {
  it('derives the strong scenario tag only from a scenario revision', () => {
    expect(scenarioAuthoringTag(revision)).toBe(`"scenario-authoring-v1:${revision}"`)
    expect(() => scenarioAuthoringTag(`scr-v1-${'a'.repeat(64)}`)).toThrow('supported scenario revision')
    expect(() => scenarioAuthoringTag(`scn-v1-${'A'.repeat(64)}`)).toThrow('supported scenario revision')
  })

  it('admits only a complete projection paired with its own tag and identity', () => {
    const baseline = scenarioAuthoringFixture({ scripts: [{ order: 0, script_id: 'script-1', vusers: 5 }] })
    const admitted = admitScenarioAuthoringBaseline(baseline.projection, baseline.etag, 'scenario-1', 'project-1')
    expect(admitted).toEqual(baseline)
    expect(admitted.projection).not.toBe(baseline.projection)

    const projection = baseline.projection as unknown as Record<string, unknown>
    const scenario = baseline.projection.scenario as Record<string, unknown>
    const refused: Array<[unknown, string | null, string, string?]> = [
      [projection, '"scenario-authoring-v1:scn-v1-' + 'b'.repeat(64) + '"', 'scenario-1'],
      [projection, baseline.etag, 'scenario-2'],
      [projection, baseline.etag, 'scenario-1', 'project-2'],
      [{ ...projection, projection_version: 'apexload.script_authoring.v1' }, baseline.etag, 'scenario-1'],
      [{ ...projection, scenario: { ...scenario, project_id: 'project-2' } }, baseline.etag, 'scenario-1'],
      [{ ...projection, scenario: { ...scenario, groups: [{ script_id: 'script-1' }] } }, baseline.etag, 'scenario-1'],
      [{ ...projection, scenario: { ...scenario, data_pools: [{ pool_id: 'pool' }] } }, baseline.etag, 'scenario-1'],
      [{ ...projection, monitor_selection: { sources: [{ source_id: 'source' }] } }, baseline.etag, 'scenario-1'],
      [{ ...projection, library_placement: { application_id: null } }, baseline.etag, 'scenario-1'],
      [null, baseline.etag, 'scenario-1'],
    ]
    for (const [value, etag, id, projectId] of refused) {
      expect(() => admitScenarioAuthoringBaseline(value, etag, id, projectId)).toThrow('supported scenario revision')
    }
  })

  it('reads the baseline a durable read or save carries, and calls a save without one unverified', () => {
    const read = withScenarioAuthoring({ id: 'scenario-1', project_id: 'project-1' }, 'c')
    expect(scenarioBaselineFromRead(read as never, 'project-1').etag).toBe(`"scenario-authoring-v1:scn-v1-${'c'.repeat(64)}"`)
    expect(() => scenarioBaselineFromRead({ ...read, authoring_projection: undefined } as never)).toThrow('supported scenario revision')
    expect(() => scenarioBaselineFromRead({ ...read, content_revision: revision } as never)).toThrow('supported scenario revision')
    expect(() => baselineFromSavedScenario({ ...read, authoring_projection: undefined } as never)).toThrow(ScenarioSaveOutcomeUnknown)
    expect(baselineFromSavedScenario(read as never).projection.content_revision).toBe(read.content_revision)
  })

  it('asks for reconciliation on a stale, missing or unverified outcome only', () => {
    expect(scenarioSaveNeedsReconciliation(new ApiError('stale', 412))).toBe(true)
    expect(scenarioSaveNeedsReconciliation(new ApiError('required', 428))).toBe(true)
    expect(scenarioSaveNeedsReconciliation(new ApiError('unknown', 503, { error: 'x', code: 'scenario_save_outcome_unknown' }))).toBe(true)
    expect(scenarioSaveNeedsReconciliation(new ScenarioSaveOutcomeUnknown())).toBe(true)
    expect(scenarioSaveNeedsReconciliation(new TypeError('Failed to fetch'))).toBe(true)
    expect(scenarioSaveNeedsReconciliation(new ApiError('invalid', 400))).toBe(false)
    expect(scenarioSaveNeedsReconciliation(new ApiError('script changed', 409))).toBe(false)
  })
})

describe('shared authoring reapply and diff', () => {
  it('carries local changes onto the current document and keeps untouched current values', () => {
    const original = { name: 'A', duration: '5m', groups: [{ vusers: 1 }], removed: 'x', nested: { keep: 1, mine: 1 } }
    const draft = { name: 'Local', duration: '5m', groups: [{ vusers: 2 }], nested: { keep: 1, mine: 2 } }
    const current = { name: 'A', duration: '10m', groups: [{ vusers: 9 }, { vusers: 3 }], removed: 'x', nested: { keep: 5, mine: 1 }, added: true }
    expect(reapplyAuthoringDraft(original, draft, current)).toEqual({ name: 'Local', duration: '10m', groups: [{ vusers: 2 }], nested: { keep: 5, mine: 2 }, added: true })
  })

  it('treats JSON keys as data and bounds the walk', () => {
    const merged = reapplyAuthoringDraft<Record<string, unknown>>({}, JSON.parse('{"__proto__":{"polluted":true}}'), {})
    expect(Object.prototype.hasOwnProperty.call(merged, '__proto__')).toBe(true)
    expect(({} as Record<string, unknown>).polluted).toBeUndefined()
    const chain = (leaf: number) => {
      let value: unknown = leaf
      for (let i = 0; i < 80; i++) value = { next: value }
      return value
    }
    expect(() => reapplyAuthoringDraft(chain(1), chain(2), chain(1))).toThrow('bounded review limit')
  })

  it('lists changed field paths without values', () => {
    expect(authoringChangedPaths({ scenario: { name: 'a', groups: [{ vusers: 1 }] } }, { scenario: { name: 'b', groups: [{ vusers: 2 }] } }, 'Scenario'))
      .toEqual(['scenario.groups[0].vusers', 'scenario.name'])
    expect(authoringChangedPaths(1, 2, 'Scenario')).toEqual(['Scenario'])
  })
})

describe('adoptableAfterBindingsSave', () => {
  it('requires reconciliation for live-options-only changes while preserving monitor-only adoption', () => {
    const binding = { pool_id: '', alias: 'work', access_mode: 'live' as const, selection_strategy: 'manual' as const, live_options: { seed_policy: 'empty' as const, columns: ['id'], end_mode: 'discard' as const } }
    const previous = scenarioAuthoringFixture({ data_pools: [binding] }, 'scenario-1', 'project-1', 'a')
    const monitorsOnly = scenarioAuthoringFixture({ data_pools: [binding] }, 'scenario-1', 'project-1', 'b')
    monitorsOnly.projection.monitor_selection = { sources: [{ source_id: 'prom-1', required: true }] }
    expect(adoptableAfterBindingsSave(previous, monitorsOnly)).toBe(monitorsOnly)

    const changed = scenarioAuthoringFixture({ data_pools: [{ ...binding, live_options: { ...binding.live_options, end_mode: 'save_as_new', output_pool_name: 'Retained output' } }] }, 'scenario-1', 'project-1', 'c')
    changed.projection.monitor_selection = monitorsOnly.projection.monitor_selection
    expect(adoptableAfterBindingsSave(previous, changed)).toBeNull()
    expect(authoringChangedPaths(previous.projection.scenario, changed.projection.scenario, 'Scenario')).toEqual([
      'data_pools[0].live_options.end_mode', 'data_pools[0].live_options.output_pool_name',
    ])
    // Neither mutable form inputs nor later server reads can alter the old authority.
    binding.live_options.columns.push('later_edit')
    expect(previous.projection.scenario.data_pools[0].live_options).toMatchObject({ columns: ['id'], end_mode: 'discard' })
  })

  it('adopts the next baseline only when the binding save was the only change', () => {
    const previous = scenarioAuthoringFixture({ name: 'Checkout' }, 'scenario-1', 'project-1', 'a')
    const bindingsOnly = { ...scenarioAuthoringFixture({ name: 'Checkout' }, 'scenario-1', 'project-1', 'b') }
    bindingsOnly.projection = { ...bindingsOnly.projection, monitor_selection: { sources: [{ source_id: 'prom-1' }] } as never }
    expect(adoptableAfterBindingsSave(previous, bindingsOnly)).toBe(bindingsOnly)
    const renamed = scenarioAuthoringFixture({ name: 'Renamed elsewhere' }, 'scenario-1', 'project-1', 'b')
    expect(adoptableAfterBindingsSave(previous, renamed)).toBeNull()
    const placed = scenarioAuthoringFixture({ name: 'Checkout' }, 'scenario-1', 'project-1', 'b')
    placed.projection = { ...placed.projection, library_placement: { application_id: 'app-1', release_ids: [] } }
    expect(adoptableAfterBindingsSave(previous, placed)).toBeNull()
  })
})
