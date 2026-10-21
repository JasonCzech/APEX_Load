// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import type { ApiServices } from '../hooks/ApiProvider'
import { ApiError } from '../hooks/apiClient'
import type { StoredScenario } from '../types/storedContent'
import { scenarioAuthoringFixture, withScenarioAuthoring } from '../test/scenarioAuthoringFixtures'
import { persistScenarioAuthoringDraft, persistScenarioProjectDraft, restoreScenarioDraftBaseline } from './scenarioAuthoringWorkflow'
import { AuthoringCreateNeedsNewIntent, type AuthoringCreateIntent } from './authoringCreateIntent'

function intentOwner(initial: AuthoringCreateIntent | null = null) {
  const owner = { current: initial, update: (next: AuthoringCreateIntent | null) => { owner.current = next } }
  return owner
}

const scenario: StoredScenario = withScenarioAuthoring({ id: 'scenario-1', project_id: 'project-1', name: 'Orders', duration: '1m', scripts: [], created_at: '', updated_at: '2026-01-01', script_count: 0 })
const payload = { name: 'Orders', duration: '1m', groups: [] }

describe('scenario authoring persistence workflow', () => {
  function scenarioApi() {
    return {
      createScenarioKeyed: vi.fn<ApiServices['createScenarioKeyed']>().mockResolvedValue({ kind: 'created', resource: scenario }),
      updateScenario: vi.fn<ApiServices['updateScenario']>().mockResolvedValue(scenario),
      getScenario: vi.fn<ApiServices['getScenario']>().mockResolvedValue(scenario),
    }
  }

  it('publishes paired saves only while their originating work is current', async () => {
    const api = scenarioApi()
    const baseline = scenarioAuthoringFixture(scenario)
    const result = await persistScenarioAuthoringDraft({ editingId: scenario.id, projectId: scenario.project_id, payload, baseline, createIntent: intentOwner() }, { api, isCurrent: () => true })
    expect(result.kind).toBe('saved')
    expect(api.updateScenario).toHaveBeenCalledWith(scenario.id, payload, baseline)
    expect(api.createScenarioKeyed).not.toHaveBeenCalled()
    expect(await persistScenarioAuthoringDraft({ editingId: null, payload, baseline: null, createIntent: intentOwner() }, { api, isCurrent: () => false })).toEqual({ kind: 'superseded' })
  })

  it('retries a create under its draft-owned key and refuses a changed draft rather than duplicate it', async () => {
    const api = scenarioApi()
    const owner = intentOwner()
    api.createScenarioKeyed.mockRejectedValueOnce(new ApiError('unknown', 503, { code: 'scenario_save_outcome_unknown', error: 'unknown' }))
    await expect(persistScenarioAuthoringDraft({ editingId: null, projectId: 'project-1', payload, baseline: null, createIntent: owner }, { api, isCurrent: () => true })).rejects.toMatchObject({ status: 503 })
    const key = api.createScenarioKeyed.mock.calls[0][1]
    expect(owner.current).toMatchObject({ key, state: 'pending', purpose: 'scenario_create', projectId: 'project-1' })
    api.createScenarioKeyed.mockRejectedValueOnce(new ApiError('reused', 409, { code: 'idempotency_key_reused', error: 'reused' }))
    await expect(persistScenarioAuthoringDraft({ editingId: null, projectId: 'project-1', payload: { ...payload, name: 'Changed' }, baseline: null, createIntent: owner }, { api, isCurrent: () => true })).rejects.toBeInstanceOf(AuthoringCreateNeedsNewIntent)
    expect(api.createScenarioKeyed.mock.calls[1][1]).toBe(key)
    api.createScenarioKeyed.mockResolvedValueOnce({ kind: 'replayed-original', outcome: { receipt_version: 2, resource_type: 'scenario', resource_id: scenario.id, project_id: 'project-1', content_revision: 'original', created_at: '' } })
    const replayed = await persistScenarioAuthoringDraft({ editingId: null, projectId: 'project-1', payload, baseline: null, createIntent: owner }, { api, isCurrent: () => true })
    expect(api.createScenarioKeyed.mock.calls[2][1]).toBe(key)
    expect(api.getScenario).toHaveBeenCalledWith(scenario.id)
    expect(replayed).toMatchObject({ kind: 'saved', replayed: true, saved: { id: scenario.id } })
    expect(owner.current).toBeNull()
  })

  it('keeps the key through a failed read after a replay and recalls it for the same draft after a reload', async () => {
    window.localStorage.clear()
    const api = scenarioApi()
    const owner = intentOwner()
    api.createScenarioKeyed.mockResolvedValueOnce({ kind: 'replayed-original', outcome: { receipt_version: 2, resource_type: 'scenario', resource_id: scenario.id, project_id: 'project-1', content_revision: 'original', created_at: '' } })
    api.getScenario.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(persistScenarioAuthoringDraft({ editingId: null, projectId: 'project-1', payload, baseline: null, createIntent: owner }, { api, isCurrent: () => true })).rejects.toThrow('Failed to fetch')
    const key = api.createScenarioKeyed.mock.calls[0][1]
    expect(owner.current).toMatchObject({ key, state: 'pending' })
    // The checkpoint is gone (for example, the work moved to a project draft).
    await persistScenarioAuthoringDraft({ editingId: null, projectId: 'project-1', payload, baseline: null, createIntent: intentOwner() }, { api, isCurrent: () => true })
    expect(api.createScenarioKeyed.mock.calls[1][1]).toBe(key)
  })

  it.each([404, 409, 500])('recreates deleted project drafts only for 404 (received %s)', async status => {
    const api = { updateScenarioDraft: vi.fn<ApiServices['updateScenarioDraft']>().mockRejectedValue(new ApiError('failed', status)), createScenarioDraft: vi.fn<ApiServices['createScenarioDraft']>().mockResolvedValue({ id: 'new', project_id: 'project-1', name: 'Orders', revision: 1, created_at: '', updated_at: '', document: {} }) }
    const operation = persistScenarioProjectDraft('project-1', { id: 'old', revision: 3 }, { name: 'Orders', document: {} }, api)
    if (status === 404) { expect((await operation).id).toBe('new'); expect(api.createScenarioDraft).toHaveBeenCalledOnce() }
    else { await expect(operation).rejects.toMatchObject({ status }); expect(api.createScenarioDraft).not.toHaveBeenCalled() }
    expect(api.updateScenarioDraft.mock.calls[0][2].revision).toBe(3)
  })

  it('restores only the checkpoint revision and ignores reads from an earlier scope', async () => {
    const api = { getScenario: vi.fn<ApiServices['getScenario']>().mockResolvedValue(scenario) }
    const baseline = scenarioAuthoringFixture(scenario)
    expect((await restoreScenarioDraftBaseline(scenario.id, scenario.project_id, { baselineTag: baseline.etag, editingUpdatedAt: '' }, { api, isCurrent: () => true })).kind).toBe('baseline')
    expect((await restoreScenarioDraftBaseline(scenario.id, scenario.project_id, { baselineTag: 'old', editingUpdatedAt: '' }, { api, isCurrent: () => true })).kind).toBe('changed')
    expect((await restoreScenarioDraftBaseline(scenario.id, scenario.project_id, { baselineTag: baseline.etag, editingUpdatedAt: '' }, { api, isCurrent: () => false })).kind).toBe('superseded')
    expect((await restoreScenarioDraftBaseline(scenario.id, scenario.project_id, { editingUpdatedAt: scenario.updated_at }, { api, isCurrent: () => true })).kind).toBe('baseline')
  })
})
