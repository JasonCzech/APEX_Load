import { describe, expect, it } from 'vitest'
import { ApiError } from '../hooks/apiClient'
import { scenarioAuthoringFixture, withScenarioAuthoring } from '../test/scenarioAuthoringFixtures'
import { authoringChangedPaths, reapplyAuthoringDraft } from './authoringRevision'
import { admitScenarioAuthoringBaseline, baselineFromSavedScenario, scenarioAuthoringTag, scenarioBaselineFromRead, scenarioSaveNeedsReconciliation, ScenarioSaveOutcomeUnknown } from './scenarioAuthoringRevision'

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
