// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import type { AuthoringComponent } from '../types'
import SavedBlocksPanel from './SavedBlocksPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { api } = vi.hoisted(() => ({ api: { listAuthoringComponents: vi.fn(), deleteAuthoringComponent: vi.fn() } }))
vi.mock('../hooks/api/useScriptApi', () => ({ useScriptApi: () => api }))

const builtIn: AuthoringComponent = { id: 'http-core-api-request-group', kind: 'request_group', name: 'Core API Request Group', protocols: ['http'], summary: 'Built in', actions: [{ name: 'Health', type: 'request' }] as AuthoringComponent['actions'] }
const older: AuthoringComponent = { id: 'block-1', kind: 'request_group', name: 'Login steps', protocols: ['*'], summary: 'Signs in', saved_at: '2026-10-01T10:00:00Z', init: [{ name: 'Open session', type: 'request' }] as AuthoringComponent['init'], actions: [{ name: 'Post login', type: 'request' }] as AuthoringComponent['actions'], parameters: [{ name: 'user' }] as AuthoringComponent['parameters'] }
// Half a second after `sameSecond` (below): as text it would sort first ('.' < 'Z'), by time it is newer.
const newer: AuthoringComponent = { id: 'block-2', kind: 'teardown', name: 'Logout', protocols: ['http', 'browser'], summary: '', saved_at: '2026-10-12T10:00:00.5Z', teardown: [{ name: 'Sign out', type: 'request' }] as AuthoringComponent['teardown'] }

const sameSecond: AuthoringComponent = { id: 'block-3', kind: 'request_group', name: 'Search', protocols: ['http'], summary: '', saved_at: '2026-10-12T10:00:00Z', actions: [{ name: 'Find', type: 'request' }] as AuthoringComponent['actions'] }

async function settle() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
}

describe('SavedBlocksPanel', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    api.listAuthoringComponents.mockReset().mockResolvedValue({ components: [builtIn, older, newer, sameSecond], count: 4 })
    api.deleteAuthoringComponent.mockReset().mockResolvedValue({ deleted: 'block-1' })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const titles = () => [...container.querySelectorAll('.pp-row-title-button')].map(button => button.firstChild?.textContent)

  it('lists the project’s saved blocks, newest first, without the built-in catalog', async () => {
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage />))
    await settle()
    expect(api.listAuthoringComponents).toHaveBeenCalledWith({ projectId: 'p1' })
    expect(titles()).toEqual(['Logout', 'Search', 'Login steps'])
    const meta = [...container.querySelectorAll('.pp-row-meta')].map(element => element.textContent)
    expect(meta[0]).toContain('teardown · http, browser · saved')
    expect(meta[2]).toContain('request group · any protocol')
  })

  it('previews a block’s steps by where they run', async () => {
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage />))
    await settle()
    const login = [...container.querySelectorAll<HTMLButtonElement>('.pp-row-title-button')][2]
    expect(login.getAttribute('aria-expanded')).toBe('false')
    await act(async () => login.click())
    const preview = container.querySelector('[aria-label="Steps of Login steps"]')!
    expect(login.getAttribute('aria-expanded')).toBe('true')
    expect(preview.textContent).toContain('Setup')
    expect(preview.textContent).toContain('Open session')
    expect(preview.textContent).toContain('Post login')
    expect(preview.textContent).toContain('Parameters: user')
  })

  it('deletes a block after confirming, saying scripts keep their copies', async () => {
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage />))
    await settle()
    const deleteButtons = [...container.querySelectorAll<HTMLButtonElement>('.pp-row-actions button')]
    await act(async () => deleteButtons[2].click())
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Scripts that inserted it keep their copies of its steps.')
    api.listAuthoringComponents.mockResolvedValue({ components: [builtIn, newer, sameSecond], count: 3 })
    await act(async () => [...dialog.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Delete saved block')!.click())
    await settle()
    expect(api.deleteAuthoringComponent).toHaveBeenCalledWith('block-1')
    expect(titles()).toEqual(['Logout', 'Search'])
    expect(container.textContent).toContain('Deleted saved block “Login steps”.')
  })

  it('shows a viewer the blocks with no way to delete them', async () => {
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage={false} />))
    await settle()
    expect(titles()).toHaveLength(3)
    expect(container.querySelector('.pp-row-actions')).toBeNull()
    expect(container.textContent).toContain('Editors and project admins can delete them.')
  })

  it('says when the domain has turned saved blocks off', async () => {
    api.listAuthoringComponents.mockRejectedValue(new ApiError('domain feature is disabled: script_builder.reusable_blocks', 403))
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage />))
    await settle()
    expect(container.textContent).toContain('Saved blocks are turned off for this domain')
    expect(container.textContent).toContain('Access › Domain feature policy')
    expect(container.textContent).not.toContain('script_builder.reusable_blocks')
  })

  it('says when there are none yet', async () => {
    api.listAuthoringComponents.mockResolvedValue({ components: [builtIn], count: 1 })
    await act(async () => root.render(<SavedBlocksPanel projectId="p1" canManage />))
    await settle()
    expect(container.textContent).toContain('No saved blocks yet.')
  })
})
