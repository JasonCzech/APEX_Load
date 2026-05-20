// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AccessPage from './AccessPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listAuthServiceAccounts: vi.fn(),
  createAuthServiceAccount: vi.fn(),
  updateAuthServiceAccount: vi.fn(),
  rotateAuthServiceAccount: vi.fn(),
  deleteAuthServiceAccount: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

const projects = [
  { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  { id: 'project-2', name: 'Search', created_at: '', updated_at: '' },
]

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

describe('AccessPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listAuthServiceAccounts.mockResolvedValue({
      service_accounts: [
        {
          id: 'svc-1',
          name: 'Nightly CI',
          description: 'Runs nightly smoke coverage',
          active: true,
          global_roles: ['viewer'],
          project_roles: { 'project-1': ['editor'] },
          scopes: ['tests:run', 'results:read'],
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        },
      ],
      count: 1,
    })
    apiMock.createAuthServiceAccount.mockResolvedValue({
      service_account: {
        id: 'svc-2',
        name: 'CI Runner',
        description: 'Run tests, read results, and update scripts in the selected project.',
        active: true,
        global_roles: ['viewer'],
        project_roles: { 'project-1': ['editor'] },
        scopes: ['tests:run', 'results:read', 'scripts:write'],
        created_at: '2026-05-16T02:00:00Z',
        updated_at: '2026-05-16T02:00:00Z',
      },
      api_key: 'srk_created_key',
    })
    apiMock.rotateAuthServiceAccount.mockResolvedValue({
      service_account: {
        id: 'svc-1',
        name: 'Nightly CI',
        active: true,
        global_roles: ['viewer'],
        project_roles: { 'project-1': ['editor'] },
        scopes: ['tests:run', 'results:read'],
        created_at: '2026-05-16T00:00:00Z',
        updated_at: '2026-05-16T03:00:00Z',
      },
      api_key: 'srk_rotated_key',
    })
    apiMock.updateAuthServiceAccount.mockResolvedValue({})
    apiMock.deleteAuthServiceAccount.mockResolvedValue({ deleted: 'svc-1' })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('creates a service-account key from preset roles, scopes, and project picker selections', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} activeProject={projects[0]} canManageServiceAccounts />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Access & API Keys')
    expect(container.textContent).toContain('Runtime Secrets Guidance')
    expect(container.textContent).toContain('{{env.API_TOKEN}}')
    expect(container.textContent).toContain('Nightly CI')

    const searchCheckbox = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => {
      const row = input.closest('.access-project-role-row')
      return row?.textContent?.includes('Search')
    }) as HTMLInputElement | undefined
    expect(searchCheckbox).toBeDefined()
    if (!searchCheckbox) return

    await act(async () => {
      searchCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const createButton = findButton(container, 'Create Service Account')
    expect(createButton).toBeDefined()
    if (!createButton) return

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createAuthServiceAccount).toHaveBeenCalledWith(expect.objectContaining({
      name: 'CI Runner',
      global_roles: ['viewer'],
      scopes: ['tests:run', 'results:read', 'scripts:write'],
      project_roles: {
        'project-1': ['editor'],
        'project-2': ['editor'],
      },
      rotation_interval_days: 90,
    }))
    expect(container.textContent).toContain('API Key Created')
    expect(container.textContent).toContain('srk_created_key')
    expect(container.textContent).toContain('python3 mcp/server.py')
    expect(container.textContent).toContain('Authorization: Bearer srk_created_key')
  })

  it('confirms rotate, disable, and delete lifecycle actions', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(<AccessPage projects={projects} activeProject={projects[0]} canManageServiceAccounts />)
    })
    await flushPromises()

    const rotateButton = findButton(container, 'Rotate Key')
    expect(rotateButton).toBeDefined()
    if (!rotateButton) return
    await act(async () => {
      rotateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.rotateAuthServiceAccount).toHaveBeenCalledWith('svc-1')
    expect(container.textContent).toContain('srk_rotated_key')

    const disableButton = findButton(container, 'Disable')
    expect(disableButton).toBeDefined()
    if (!disableButton) return
    await act(async () => {
      disableButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.updateAuthServiceAccount).toHaveBeenCalledWith('svc-1', { active: false })

    const deleteButton = findButton(container, 'Delete')
    expect(deleteButton).toBeDefined()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.deleteAuthServiceAccount).toHaveBeenCalledWith('svc-1')
  })

  it('shows read-only state when the session cannot manage service accounts', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} activeProject={projects[0]} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Read-only session')
    expect(findButton(container, 'Create Service Account')?.disabled).toBe(true)
    expect(findButton(container, 'Rotate Key')?.disabled).toBe(true)
    expect(apiMock.createAuthServiceAccount).not.toHaveBeenCalled()
  })
})
