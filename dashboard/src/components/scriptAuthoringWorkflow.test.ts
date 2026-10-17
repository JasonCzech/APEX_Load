import { describe, expect, it, vi } from 'vitest'
import type { ApiServices } from '../hooks/ApiProvider'
import type { ScriptDefinition } from '../types/scripts'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import { persistScriptAuthoringDraft } from './scriptAuthoringWorkflow'

const draft: ScriptDefinition = { name: 'Orders', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Orders', type: 'request', method: 'GET', target: '/orders' }] }
function apiFixture() {
  return {
    createScript: vi.fn<ApiServices['createScript']>().mockResolvedValue(savedAuthoringFixture(draft)),
    updateScript: vi.fn<ApiServices['updateScript']>().mockResolvedValue(savedAuthoringFixture(draft)),
    createAuthoringSession: vi.fn<ApiServices['createAuthoringSession']>(),
  }
}
const input = { draft, importBaseline: null, baseline: null, projectId: 'project-1' }

describe('script authoring persistence workflow', () => {
  it('creates through the library and returns the paired canonical baseline', async () => {
    const api = apiFixture()
    const result = await persistScriptAuthoringDraft(input, { api, isCurrent: () => true })
    expect(api.createScript).toHaveBeenCalledWith(draft, 'project-1', undefined)
    expect(result.kind).toBe('saved')
    if (result.kind === 'saved') expect(result.definition).toEqual(result.baseline?.projection.script)
  })

  it('requires the reviewed baseline for updates and never falls back to creating', async () => {
    const api = apiFixture()
    await expect(persistScriptAuthoringDraft({ ...input, scriptId: 'script-1' }, { api, isCurrent: () => true })).rejects.toThrow('baseline')
    expect(api.createScript).not.toHaveBeenCalled()
    expect(api.updateScript).not.toHaveBeenCalled()
    const baseline = authoringFixture(draft)
    await persistScriptAuthoringDraft({ ...input, scriptId: 'script-1', baseline }, { api, isCurrent: () => true })
    expect(api.updateScript).toHaveBeenCalledWith('script-1', draft, baseline)
  })

  it('does not send a stale scope and does not publish a response after the scope changes', async () => {
    const api = apiFixture()
    expect(await persistScriptAuthoringDraft(input, { api, isCurrent: () => false })).toEqual({ kind: 'superseded', saved: false })
    expect(api.createScript).not.toHaveBeenCalled()
    let current = true
    api.createScript.mockImplementation(async () => { current = false; return savedAuthoringFixture(draft) })
    expect(await persistScriptAuthoringDraft(input, { api, isCurrent: () => current })).toEqual({ kind: 'superseded', saved: true })
  })

  it('queues executable Java drafts with a repeatable content identity', async () => {
    const api = apiFixture()
    const java = { ...draft, protocol: 'java_vuser', actions: [{ name: 'Run', type: 'java_vuser_run', target: '' }] }
    api.createAuthoringSession.mockResolvedValue({ name: 'Orders' } as Awaited<ReturnType<ApiServices['createAuthoringSession']>>)
    const first = await persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => true })
    await persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => true })
    expect(first.kind).toBe('approval')
    expect(api.createScript).not.toHaveBeenCalled()
    expect(api.createAuthoringSession.mock.calls[0][0].idempotency_key).toBe(api.createAuthoringSession.mock.calls[1][0].idempotency_key)
  })
})
