// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScenarioFormState } from './TestHub'
import { assertDraftHasNoInlineCredentials, clearScenarioAuthoringDraft, migrateScenarioAuthoringDraft, readScenarioAuthoringDraft, saveScenarioAuthoringDraft, type ScenarioAuthoringDraft } from './scenarioAuthoringDraft'
const draft = (): ScenarioAuthoringDraft => ({ version: 2, projectId: 'project-a', savedAt: '2026-09-12T12:00:00Z', editingId: null, editingUpdatedAt: '', form: { name: '', duration: '', start_delay: '', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, runtime_pacing_type: '', runtime_ignore_think_time: false, runtime_log_level: '', runtime_auto_transactions: '', assignment_mode: 'auto', assignment_targets: [], groups: [{ name: '', script_id: '', stages: [], vusers: 0, ramp_up: '', ramp_down: '', initialization: 'at_runtime' }] } as ScenarioFormState, source: 'existing', startChoice: 'existing', shapeId: '', templateId: '', cloneId: '', step: 'source', visited: 1 })
afterEach(() => { localStorage.clear(); vi.restoreAllMocks() })
describe('explicit scenario authoring checkpoints', () => {
  it('round trips incomplete work only within its project and clears it explicitly', () => {
    const value = draft(); saveScenarioAuthoringDraft(value)
    expect(readScenarioAuthoringDraft('project-a')).toEqual(value)
    expect(readScenarioAuthoringDraft('project-b')).toBeNull()
    clearScenarioAuthoringDraft('project-a')
    expect(readScenarioAuthoringDraft('project-a')).toBeNull()
  })
  it('does not announce success when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Quota exceeded') })
    expect(() => saveScenarioAuthoringDraft(draft())).toThrow('Quota exceeded')
  })
  it.each([{ env: { KEY: 'inline-value' } }, { http: { headers: { Authorization: 'secret-value' } } }, { base_url: 'https://user:password@example.test' }, { base_url: 'https://example.test?token=secret' }, { java_vuser: { additional_vm_parameters: '-Dpassword=x' } }])('rejects inline sensitive configuration without dropping fields: %j', value => {
    expect(() => assertDraftHasNoInlineCredentials(value)).toThrow()
    expect(readScenarioAuthoringDraft('project-a')).toBeNull()
  })
  it('retains project-secret references', () => {
    expect(() => assertDraftHasNoInlineCredentials({ env: { KEY: '{{secret.api_key}}' } })).not.toThrow()
  })
  it('upgrades six-step checkpoints to the scripts-first step order', () => {
    const legacy = (step: string, visited: number) => ({ ...draft(), version: 1, step, visited })
    expect(migrateScenarioAuthoringDraft(legacy('start', 0))).toMatchObject({ version: 2, step: 'scripts', visited: 0 })
    expect(migrateScenarioAuthoringDraft(legacy('scripts', 1))).toMatchObject({ version: 2, step: 'scripts', visited: 0 })
    expect(migrateScenarioAuthoringDraft(legacy('source', 2))).toMatchObject({ version: 2, step: 'source', visited: 1 })
    expect(migrateScenarioAuthoringDraft(legacy('review', 5))).toMatchObject({ version: 2, step: 'review', visited: 4 })
    expect(migrateScenarioAuthoringDraft(legacy('source', 6))).toBeNull()
    saveScenarioAuthoringDraft(draft())
    localStorage.setItem(localStorage.key(0)!, JSON.stringify(legacy('run', 3)))
    expect(readScenarioAuthoringDraft('project-a')).toMatchObject({ version: 2, step: 'run', visited: 2 })
  })
  it('ignores corrupted, wrong-version and cross-project checkpoints', () => {
    saveScenarioAuthoringDraft(draft())
    const storageKey = localStorage.key(0)!
    for (const invalid of ['{', JSON.stringify({ ...draft(), version: 3 }), JSON.stringify({ ...draft(), step: 'start' }), JSON.stringify({ ...draft(), visited: 5 }), JSON.stringify({ ...draft(), projectId: 'project-b' }), JSON.stringify({ ...draft(), form: { groups: [null] } })]) {
      localStorage.setItem(storageKey, invalid)
      expect(readScenarioAuthoringDraft('project-a')).toBeNull()
    }
  })
})
