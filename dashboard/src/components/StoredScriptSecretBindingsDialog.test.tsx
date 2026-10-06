// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../hooks/apiClient'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import type { ScriptDefinition } from '../types/scripts'
import StoredScriptSecretBindingsDialog from './StoredScriptSecretBindingsDialog'

const api = vi.hoisted(() => ({ getScriptAuthoring: vi.fn(), updateScript: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
vi.mock('./ScriptSecretBindingsDialog', () => ({ default: ({ script, onRebound, reconciliation, rebindDisabled }: { script: ScriptDefinition; onRebound: (script: ScriptDefinition, paths: string[], outcome?: { createdSecretName?: string }) => Promise<void>; reconciliation?: ReactNode; rebindDisabled?: boolean }) => <div><span data-testid="draft">{script.name}</span><button disabled={rebindDisabled} onClick={() => { void onRebound({ ...script, name: 'Retained rebound draft' }, ['actions[0].headers.Authorization'], { createdSecretName: 'DEDICATED_TOKEN' }).catch(() => undefined) }}>Rebind fixture</button>{reconciliation}</div> }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('stored script secret authoring authority', () => {
  let root: Root
  let container: HTMLDivElement
  beforeEach(() => {
    vi.resetAllMocks()
    container = document.createElement('div'); document.body.append(container); root = createRoot(container)
    api.getScriptAuthoring.mockResolvedValue(authoringFixture())
    api.updateScript.mockImplementation(async (id: string, script: ScriptDefinition) => savedAuthoringFixture(script, id))
  })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })
  const click = async (label: string) => { await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === label)!.click() }) }
  const render = async (onSaved = vi.fn(), onClose = vi.fn()) => { await act(async () => { root.render(<StoredScriptSecretBindingsDialog stored={savedAuthoringFixture()} projectId="project-1" onClose={onClose} onConfigured={vi.fn()} onSaved={onSaved} />) }); return { onSaved, onClose } }

  it('loads one protected baseline before exposing rebind and conditions the write', async () => {
    const { onSaved, onClose } = await render()
    expect(api.getScriptAuthoring).toHaveBeenCalledWith('script-1', 'project-1')
    await click('Rebind fixture')
    expect(api.updateScript).toHaveBeenCalledWith('script-1', expect.objectContaining({ name: 'Retained rebound draft' }), authoringFixture())
    expect(onSaved).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })
  it.each([new ApiError('stale', 412), new ApiError('unknown', 503, { code: 'script_save_outcome_unknown' }), new Error('lost response')])('retains rebound and created-secret provenance until explicit reapply/save: %s', async cause => {
    api.updateScript.mockRejectedValueOnce(cause)
    const { onSaved } = await render()
    await click('Rebind fixture')
    expect(container.textContent).toContain('Retained rebound draft')
    expect(container.textContent).toContain('DEDICATED_TOKEN was created')
    expect(container.querySelector<HTMLButtonElement>('button')!.disabled).toBe(true)
    const newer = authoringFixture({ config: { base_url: '/remote' }, folder_id: 'remote-folder' }, 'script-1', 'project-1', 'c')
    api.getScriptAuthoring.mockResolvedValueOnce(newer)
    await click('Load current baseline')
    expect(api.updateScript).toHaveBeenCalledTimes(1)
    await click('Reapply draft')
    expect(api.updateScript).toHaveBeenCalledTimes(1)
    await click('Save reviewed rebound draft')
    expect(api.updateScript).toHaveBeenLastCalledWith('script-1', expect.objectContaining({ name: 'Retained rebound draft', folder_id: 'remote-folder', config: expect.objectContaining({ base_url: '/remote' }) }), newer)
    expect(onSaved).toHaveBeenCalledTimes(1)
  })
  it('refuses to carry created/rebound credentials across application vault placement', async () => {
    api.updateScript.mockRejectedValueOnce(new ApiError('stale placement', 412))
    await render()
    await click('Rebind fixture')
    const newer = authoringFixture({}, 'script-1', 'project-1', 'c')
    newer.projection.library_placement.application_id = 'other-application'
    api.getScriptAuthoring.mockResolvedValueOnce(newer)
    await click('Load current baseline')
    await click('Reapply draft')
    expect(container.textContent).toContain('cannot be carried across vault ownership')
    expect(api.updateScript).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('DEDICATED_TOKEN was created')
  })
  it('does not publish a late verified save after the dialog unmounts', async () => {
    let complete!: (saved: ReturnType<typeof savedAuthoringFixture>) => void
    api.updateScript.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const { onSaved, onClose } = await render()
    await click('Rebind fixture')
    await act(async () => root.render(<div>Closed dialog</div>))
    await act(async () => { complete(savedAuthoringFixture({ name: 'Retained rebound draft' })) })
    expect(onSaved).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
