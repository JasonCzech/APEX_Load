// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScenarioFormState } from './TestHub'
import { assertDraftHasNoInlineCredentials, clearScenarioAuthoringDraft, readScenarioAuthoringDraft, saveScenarioAuthoringDraft, type ScenarioAuthoringDraft } from './scenarioAuthoringDraft'
const draft = (): ScenarioAuthoringDraft => ({ version: 1, projectId: 'project-a', savedAt: '2026-09-12T12:00:00Z', editingId: null, editingUpdatedAt: '', form: { name: '', duration: '', start_delay: '', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, runtime_pacing_type: '', runtime_ignore_think_time: false, runtime_log_level: '', runtime_auto_transactions: '', assignment_mode: 'auto', assignment_targets: [], groups: [{ name: '', script_id: '', stages: [], vusers: 0, ramp_up: '', ramp_down: '', initialization: 'at_runtime' }] } as ScenarioFormState, source: 'existing', startChoice: 'existing', shapeId: '', templateId: '', cloneId: '', step: 'source', visited: 2 })
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
  it('ignores corrupted, wrong-version and cross-project checkpoints', () => {
    saveScenarioAuthoringDraft(draft())
    const storageKey = localStorage.key(0)!
    for (const invalid of ['{', JSON.stringify({ ...draft(), version: 2 }), JSON.stringify({ ...draft(), projectId: 'project-b' }), JSON.stringify({ ...draft(), form: { groups: [null] } })]) {
      localStorage.setItem(storageKey, invalid)
      expect(readScenarioAuthoringDraft('project-a')).toBeNull()
    }
  })
})
