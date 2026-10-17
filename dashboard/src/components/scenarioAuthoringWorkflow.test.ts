import { describe, expect, it, vi } from 'vitest'
import type { ApiServices } from '../hooks/ApiProvider'
import { ApiError } from '../hooks/apiClient'
import type { StoredScenario } from '../types/storedContent'
import { scenarioAuthoringFixture, withScenarioAuthoring } from '../test/scenarioAuthoringFixtures'
import { persistScenarioAuthoringDraft, persistScenarioProjectDraft, restoreScenarioDraftBaseline } from './scenarioAuthoringWorkflow'

const scenario: StoredScenario = withScenarioAuthoring({ id: 'scenario-1', project_id: 'project-1', name: 'Orders', duration: '1m', scripts: [], created_at: '', updated_at: '2026-01-01', script_count: 0 })
const payload = { name: 'Orders', duration: '1m', groups: [] }

describe('scenario authoring persistence workflow', () => {
  it('publishes paired saves only while their originating work is current', async () => {
    const api = { createScenario: vi.fn<ApiServices['createScenario']>().mockResolvedValue(scenario), updateScenario: vi.fn<ApiServices['updateScenario']>().mockResolvedValue(scenario) }
    const baseline = scenarioAuthoringFixture(scenario)
    const result = await persistScenarioAuthoringDraft({ editingId: scenario.id, projectId: scenario.project_id, payload, baseline }, { api, isCurrent: () => true })
    expect(result.kind).toBe('saved')
    expect(api.updateScenario).toHaveBeenCalledWith(scenario.id, payload, baseline)
    expect(api.createScenario).not.toHaveBeenCalled()
    expect(await persistScenarioAuthoringDraft({ editingId: null, payload, baseline: null }, { api, isCurrent: () => false })).toEqual({ kind: 'superseded' })
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
