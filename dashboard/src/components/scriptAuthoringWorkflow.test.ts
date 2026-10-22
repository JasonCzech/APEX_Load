// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ApiServices } from '../hooks/ApiProvider'
import { ApiError } from '../hooks/apiClient'
import { AuthoringCreateNeedsNewIntent, type AuthoringCreateIntent } from './authoringCreateIntent'
import type { ScriptDefinition } from '../types/scripts'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import { persistScriptAuthoringDraft } from './scriptAuthoringWorkflow'

const draft: ScriptDefinition = { name: 'Orders', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Orders', type: 'request', method: 'GET', target: '/orders' }] }
function apiFixture() {
  return {
    createScriptKeyed: vi.fn<ApiServices['createScriptKeyed']>().mockResolvedValue({ kind: 'created', resource: savedAuthoringFixture(draft) }),
    updateScript: vi.fn<ApiServices['updateScript']>().mockResolvedValue(savedAuthoringFixture(draft)),
    createAuthoringSession: vi.fn<ApiServices['createAuthoringSession']>(),
    getScript: vi.fn<ApiServices['getScript']>(),
    getScriptAuthoring: vi.fn<ApiServices['getScriptAuthoring']>(),
  }
}
function intentOwner(initial: AuthoringCreateIntent | null = null) {
  const owner = { current: initial, updates: [] as Array<AuthoringCreateIntent | null>, update: (next: AuthoringCreateIntent | null) => { owner.current = next; owner.updates.push(next) } }
  return owner
}
let input: { draft: ScriptDefinition; importBaseline: null; baseline: null; projectId: string; createIntent: ReturnType<typeof intentOwner> }
beforeEach(() => {
  window.localStorage.clear()
  input = { draft, importBaseline: null, baseline: null, projectId: 'project-1', createIntent: intentOwner() }
})

describe('script authoring persistence workflow', () => {
  it('creates through the library and returns the paired canonical baseline', async () => {
    const api = apiFixture()
    const result = await persistScriptAuthoringDraft(input, { api, isCurrent: () => true })
    expect(api.createScriptKeyed).toHaveBeenCalledWith(draft, 'project-1', undefined, expect.stringMatching(/^script-create-/))
    expect(result.kind).toBe('saved')
    if (result.kind === 'saved') expect(result.definition).toEqual(result.baseline?.projection.script)
    // A definitive success ends the intent.
    expect(input.createIntent.current).toBeNull()
  })

  it('keeps the key across a lost response, a changed draft and a reload, and rotates it only after a definitive refusal', async () => {
    const api = apiFixture()
    api.createScriptKeyed.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(persistScriptAuthoringDraft(input, { api, isCurrent: () => true })).rejects.toThrow('Failed to fetch')
    const key = api.createScriptKeyed.mock.calls[0][3]
    expect(input.createIntent.current).toMatchObject({ key, state: 'pending' })

    // A changed draft while the first attempt may have committed keeps the key.
    const changed = { ...draft, name: 'Orders v2' }
    api.createScriptKeyed.mockRejectedValueOnce(new ApiError('reused', 409, { code: 'idempotency_key_reused', error: 'reused' }))
    await expect(persistScriptAuthoringDraft({ ...input, draft: changed }, { api, isCurrent: () => true })).rejects.toBeInstanceOf(AuthoringCreateNeedsNewIntent)
    expect(api.createScriptKeyed.mock.calls[1][3]).toBe(key)

    // A reload loses the editor's intent; the same draft finds its pending key.
    const reloaded = { ...input, createIntent: intentOwner() }
    api.createScriptKeyed.mockResolvedValueOnce({ kind: 'replayed', resource: savedAuthoringFixture(draft) })
    const replayed = await persistScriptAuthoringDraft(reloaded, { api, isCurrent: () => true })
    expect(api.createScriptKeyed.mock.calls[2][3]).toBe(key)
    expect(replayed).toMatchObject({ kind: 'saved', replayed: true })

    // A definitive refusal releases the key; a corrected draft gets a new one.
    const refused = { ...input, createIntent: intentOwner() }
    api.createScriptKeyed.mockRejectedValueOnce(new ApiError('invalid', 400))
    await expect(persistScriptAuthoringDraft(refused, { api, isCurrent: () => true })).rejects.toThrow('invalid')
    const refusedKey = api.createScriptKeyed.mock.calls[3][3]
    expect(refused.createIntent.current).toMatchObject({ key: refusedKey, state: 'refused' })
    await persistScriptAuthoringDraft({ ...refused, draft: changed }, { api, isCurrent: () => true })
    expect(api.createScriptKeyed.mock.calls[4][3]).not.toBe(refusedKey)
  })

  it('keeps the key when the created script never reaches the editor', async () => {
    const api = apiFixture()
    const outcome = { receipt_version: 2 as const, resource_type: 'script' as const, resource_id: 'script-1', project_id: 'project-1', content_revision: 'original', created_at: '' }
    api.createScriptKeyed.mockResolvedValue({ kind: 'replayed-original', outcome })
    api.getScript.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(persistScriptAuthoringDraft(input, { api, isCurrent: () => true })).rejects.toThrow('Failed to fetch')
    const key = api.createScriptKeyed.mock.calls[0][3]
    expect(input.createIntent.current).toMatchObject({ key, state: 'pending' })
    // A result superseded by a scope change leaves the key pending too.
    let current = true
    api.createScriptKeyed.mockImplementationOnce(async () => { current = false; return { kind: 'created', resource: savedAuthoringFixture(draft) } })
    expect(await persistScriptAuthoringDraft(input, { api, isCurrent: () => current })).toEqual({ kind: 'superseded', saved: true })
    expect(api.createScriptKeyed.mock.calls[1][3]).toBe(key)
    expect(input.createIntent.current).toMatchObject({ key, state: 'pending' })
  })

  it('opens the current script after an original-outcome replay', async () => {
    const api = apiFixture()
    const current = savedAuthoringFixture({ ...draft, name: 'Edited since' })
    api.createScriptKeyed.mockResolvedValue({ kind: 'replayed-original', outcome: { receipt_version: 2, resource_type: 'script', resource_id: current.id, project_id: 'project-1', content_revision: 'original', created_at: '' } })
    api.getScript.mockResolvedValue(current)
    api.getScriptAuthoring.mockResolvedValue(authoringFixture({ ...draft, name: 'Edited since' }))
    const result = await persistScriptAuthoringDraft(input, { api, isCurrent: () => true })
    expect(api.getScript).toHaveBeenCalledWith(current.id)
    expect(result).toMatchObject({ kind: 'saved', replayed: true, stored: current, definition: { name: 'Edited since' } })
  })

  it('requires the reviewed baseline for updates and never falls back to creating', async () => {
    const api = apiFixture()
    await expect(persistScriptAuthoringDraft({ ...input, scriptId: 'script-1' }, { api, isCurrent: () => true })).rejects.toThrow('baseline')
    expect(api.createScriptKeyed).not.toHaveBeenCalled()
    expect(api.updateScript).not.toHaveBeenCalled()
    const baseline = authoringFixture(draft)
    await persistScriptAuthoringDraft({ ...input, scriptId: 'script-1', baseline }, { api, isCurrent: () => true })
    expect(api.updateScript).toHaveBeenCalledWith('script-1', draft, baseline)
  })

  it('does not send a stale scope and does not publish a response after the scope changes', async () => {
    const api = apiFixture()
    expect(await persistScriptAuthoringDraft(input, { api, isCurrent: () => false })).toEqual({ kind: 'superseded', saved: false })
    expect(api.createScriptKeyed).not.toHaveBeenCalled()
    let current = true
    api.createScriptKeyed.mockImplementation(async () => { current = false; return { kind: 'created', resource: savedAuthoringFixture(draft) } })
    expect(await persistScriptAuthoringDraft(input, { api, isCurrent: () => current })).toEqual({ kind: 'superseded', saved: true })
  })

  it('does not publish a queued receipt after a scope change or without an identity', async () => {
    const api = apiFixture()
    const java = { ...draft, protocol: 'java_vuser', actions: [{ name: 'Run', type: 'java_vuser_run', target: '' }] }
    let current = true
    api.createAuthoringSession.mockImplementation(async () => { current = false; return { id: 'queue-old', name: 'Orders' } as Awaited<ReturnType<ApiServices['createAuthoringSession']>> })
    expect(await persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => current })).toEqual({ kind: 'superseded', saved: true })
    api.createAuthoringSession.mockResolvedValue({ name: 'Orders' } as Awaited<ReturnType<ApiServices['createAuthoringSession']>>)
    await expect(persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => true })).rejects.toThrow('session identity')
    expect(api.createScriptKeyed).not.toHaveBeenCalled()
  })

  it('queues executable Java drafts with a repeatable content identity', async () => {
    const api = apiFixture()
    const java = { ...draft, protocol: 'java_vuser', actions: [{ name: 'Run', type: 'java_vuser_run', target: '' }] }
    api.createAuthoringSession.mockResolvedValue({ id: 'queue-1', name: 'Orders' } as Awaited<ReturnType<ApiServices['createAuthoringSession']>>)
    const first = await persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => true })
    await persistScriptAuthoringDraft({ ...input, draft: java }, { api, isCurrent: () => true })
    expect(first).toMatchObject({ kind: 'approval', approval: { sessionId: 'queue-1', name: 'Orders', projectId: 'project-1', folderId: null } })
    expect(api.createScriptKeyed).not.toHaveBeenCalled()
    expect(api.createAuthoringSession.mock.calls[0][0].idempotency_key).toBe(api.createAuthoringSession.mock.calls[1][0].idempotency_key)
  })
})
