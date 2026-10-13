// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScenarioFormState } from './TestHub'
import { assertDraftHasNoInlineCredentials, clearScenarioAuthoringDraft, migrateScenarioAuthoringDraft, readScenarioAuthoringDraft, saveScenarioAuthoringDraft, type ScenarioAuthoringDraft } from './scenarioAuthoringDraft'
const draft = (): ScenarioAuthoringDraft => ({ version: 3, projectId: 'project-a', savedAt: '2026-09-12T12:00:00Z', editingId: null, editingUpdatedAt: '', form: { name: '', duration: '', start_delay: '', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: '', script_id: '', stages: [], vusers: 0, ramp_up: '', ramp_down: '', initialization: 'at_runtime' }] } as ScenarioFormState, source: 'existing', startChoice: 'existing', shapeId: '', templateId: '', cloneId: '', step: 'source', visited: 1 })
afterEach(() => { localStorage.clear(); vi.restoreAllMocks() })
const flatMirrors = () => ({ runtime_pacing_type: '', runtime_ignore_think_time: false, runtime_log_level: '', runtime_auto_transactions: '', assignment_mode: 'auto', assignment_targets: [] as string[] })
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
    // Version 1 and 2 forms carried the flat runtime mirrors.
    const legacy = (step: string, visited: number) => ({ ...draft(), form: { ...draft().form, ...flatMirrors() }, version: 1, step, visited })
    expect(migrateScenarioAuthoringDraft(legacy('start', 0))).toMatchObject({ version: 3, step: 'scripts', visited: 0 })
    expect(migrateScenarioAuthoringDraft(legacy('scripts', 1))).toMatchObject({ version: 3, step: 'scripts', visited: 0 })
    expect(migrateScenarioAuthoringDraft(legacy('source', 2))).toMatchObject({ version: 3, step: 'source', visited: 1 })
    expect(migrateScenarioAuthoringDraft(legacy('review', 5))).toMatchObject({ version: 3, step: 'review', visited: 4 })
    expect(migrateScenarioAuthoringDraft(legacy('source', 6))).toBeNull()
    saveScenarioAuthoringDraft(draft())
    localStorage.setItem(localStorage.key(0)!, JSON.stringify(legacy('run', 3)))
    expect(readScenarioAuthoringDraft('project-a')).toMatchObject({ version: 3, step: 'run', visited: 2 })
  })
  it('composes a version 2 form\'s flat mirrors into its runtime override, goal config and assignment (AUTH-3)', () => {
    const v2 = {
      ...draft(),
      version: 2,
      form: {
        ...draft().form,
        ...flatMirrors(),
        runtime_pacing_type: 'fixed_delay', runtime_pacing_delay_ms: 1500,
        runtime_think_time_type: 'gaussian', runtime_think_time_ms: 800, runtime_think_time_std_ms: 100,
        runtime_iterations: 12, runtime_log_level: 'debug', runtime_auto_transactions: 'per_action',
        continue_on_error: false,
        browser_device_profile: 'iphone_15', browser_headless: 'true',
        // Canonical sections the mirrors compose into, and an advanced one they never touched.
        runtime_settings: { run_logic: { iterations: 1 }, log: { level: 'info' }, http: { request_timeout_ms: 9000 } },
        // A flat bound replaced its goal value and an absent one removed it.
        goal_target_average_tps: 120, goal_p95_latency_ms: 800,
        goal_config: { target_average_tps: 50, max_error_pct: 3, early_stop: { enabled: true } },
        assignment_mode: 'specific_hosts', assignment_targets: ['gen-1'],
        assignment: { mode: 'auto', pool_id: 'pool-a' },
      },
    }
    const migrated = migrateScenarioAuthoringDraft(v2) as unknown as ScenarioAuthoringDraft
    expect(migrated.version).toBe(3)
    expect(migrated.form.runtime_settings).toEqual({
      pacing: { type: 'fixed_delay', delay_ms: 1500 },
      think_time: { type: 'gaussian', mean_ms: 800, std_ms: 100, ignore: false },
      run_logic: { iterations: 12, auto_transactions: 'per_action' },
      log: { level: 'debug' },
      http: { request_timeout_ms: 9000 },
      error_handling: { continue_on_error: false },
      browser: { device_profile: 'iphone_15', headless: true },
    })
    expect(migrated.form.goal_config).toEqual({ target_average_tps: 120, p95_latency_ms: 800, early_stop: { enabled: true } })
    expect(migrated.form.assignment).toEqual({ mode: 'specific_hosts', pool_id: 'pool-a', selected_targets: ['gen-1'] })
    for (const mirror of ['runtime_pacing_type', 'runtime_iterations', 'runtime_log_level', 'continue_on_error', 'browser_device_profile', 'runtime_ignore_think_time', 'goal_target_average_tps', 'goal_p95_latency_ms', 'assignment_mode', 'assignment_targets']) {
      expect(migrated.form, mirror).not.toHaveProperty(mirror)
    }
    // A version 2 form whose mirrors are not the types its reader required is refused.
    expect(migrateScenarioAuthoringDraft({ ...v2, form: { ...v2.form, runtime_log_level: 3 } })).toBeNull()
  })
  it('ignores corrupted, wrong-version and cross-project checkpoints', () => {
    saveScenarioAuthoringDraft(draft())
    const storageKey = localStorage.key(0)!
    for (const invalid of ['{', JSON.stringify({ ...draft(), version: 4 }), JSON.stringify({ ...draft(), step: 'start' }), JSON.stringify({ ...draft(), visited: 5 }), JSON.stringify({ ...draft(), projectId: 'project-b' }), JSON.stringify({ ...draft(), form: { groups: [null] } })]) {
      localStorage.setItem(storageKey, invalid)
      expect(readScenarioAuthoringDraft('project-a')).toBeNull()
    }
  })
  it('keeps the project draft a checkpoint continues, and drops a malformed link', () => {
    saveScenarioAuthoringDraft({ ...draft(), projectDraft: { id: 'scenario-draft-1', revision: 2 } })
    expect(readScenarioAuthoringDraft('project-a')?.projectDraft).toEqual({ id: 'scenario-draft-1', revision: 2 })
    const storageKey = localStorage.key(0)!
    for (const malformed of [{ id: '', revision: 1 }, { id: 'x', revision: 0 }, { id: 'x', revision: 1.5 }, { id: 7, revision: 1 }, 'scenario-draft-1']) {
      localStorage.setItem(storageKey, JSON.stringify({ ...draft(), projectDraft: malformed }))
      const read = readScenarioAuthoringDraft('project-a')
      expect(read, JSON.stringify(malformed)).not.toBeNull()
      expect(read).not.toHaveProperty('projectDraft')
    }
  })
  it('keeps a well-formed recorded baseline and drops a malformed one', () => {
    const recorded = { ...draft(), editingId: 'scenario-1', baselineTag: '"tag-a"', baselineForm: draft().form, baselineShapeId: 'constant' }
    saveScenarioAuthoringDraft(recorded)
    expect(readScenarioAuthoringDraft('project-a')).toMatchObject({ baselineTag: '"tag-a"', baselineShapeId: 'constant', baselineForm: { name: '' } })
    const storageKey = localStorage.key(0)!
    for (const malformed of [{ baselineTag: '' }, { baselineTag: 7 }, { baselineForm: { name: 1, groups: [] } }, { baselineForm: { name: '' } }, { baselineShapeId: undefined }]) {
      localStorage.setItem(storageKey, JSON.stringify({ ...recorded, ...malformed }))
      const read = readScenarioAuthoringDraft('project-a')
      expect(read, JSON.stringify(malformed)).not.toBeNull()
      expect(read).not.toHaveProperty('baselineTag')
      expect(read).not.toHaveProperty('baselineForm')
      expect(read).not.toHaveProperty('baselineShapeId')
      expect(read!.editingId).toBe('scenario-1')
    }
  })
})

it('upgrades legacy first-binding overlays and preserves canonical-only arrays', () => {
 saveScenarioAuthoringDraft(draft())
 const storageKey=localStorage.key(0)!
 const legacy={...draft(),form:{...draft().form,data_pool_id:'pool',data_pool_alias:'customers',data_pool_selection_strategy:'unique'}}
 localStorage.setItem(storageKey,JSON.stringify(legacy))
 const restored=readScenarioAuthoringDraft('project-a')!
 expect(restored.form.data_pools).toEqual([{pool_id:'pool',alias:'customers',access_mode:'read_only',selection_strategy:'unique'}])
 expect(restored.form).not.toHaveProperty('data_pool_id')
 const canonical=[{pool_id:'first',alias:'first',access_mode:'read_only',selection_strategy:'sequential'},{pool_id:'second',alias:'second',access_mode:'read_only',selection_strategy:'random'}]
 // The old in-draft create action changed only the first-slot scalar fields.
 localStorage.setItem(storageKey,JSON.stringify({...legacy,form:{...legacy.form,data_pools:canonical}}))
 expect(readScenarioAuthoringDraft('project-a')!.form.data_pools).toEqual([restored.form.data_pools![0],canonical[1]])
 localStorage.setItem(storageKey,JSON.stringify({...legacy,form:{...legacy.form,data_pool_id:'',data_pools:canonical}}))
 expect(readScenarioAuthoringDraft('project-a')!.form.data_pools).toEqual([canonical[1]])
 // Current checkpoints have no scalar overlay and retain every canonical slot.
 localStorage.setItem(storageKey,JSON.stringify({...draft(),form:{...draft().form,data_pools:canonical}}))
 expect(readScenarioAuthoringDraft('project-a')!.form.data_pools).toEqual(canonical)
 const current=readScenarioAuthoringDraft('project-a')!
 saveScenarioAuthoringDraft(current)
 expect(readScenarioAuthoringDraft('project-a')!.form.data_pools).toEqual(canonical)
})

it('round trips explicit live-table draft settings without reducing them to a read-only snapshot', () => {
 const value = draft()
 value.form.data_pools = [{ pool_id: '', alias: 'work', access_mode: 'live', selection_strategy: 'manual', live_options: { profile: 'native_queue', seed_policy: 'empty', columns: ['id'], durability: 'memory', end_mode: 'discard' } }]
 saveScenarioAuthoringDraft(value)
 expect(readScenarioAuthoringDraft('project-a')?.form.data_pools).toEqual(value.form.data_pools)
})
