// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AccessPage from './AccessPage'
import { downloadBlob } from '../utils/fileUtils'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listAuthServiceAccounts: vi.fn(),
  createAuthServiceAccount: vi.fn(),
  updateAuthServiceAccount: vi.fn(),
  rotateAuthServiceAccount: vi.fn(),
  deleteAuthServiceAccount: vi.fn(),
  listAuthUsers: vi.fn(),
  createAuthUser: vi.fn(),
  updateAuthUser: vi.fn(),
  enableAuthUser: vi.fn(),
  disableAuthUser: vi.fn(),
  createAuthUserBinding: vi.fn(),
  deleteAuthUserBinding: vi.fn(),
  getAuthUserActivity: vi.fn(),
  downloadAuthUserActivity: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

const projects = [
  { id: 'project-1', domain_id: 'domain-1', name: 'Checkout', created_at: '', updated_at: '' },
  { id: 'project-2', domain_id: 'domain-1', name: 'Search', created_at: '', updated_at: '' },
]

const domains = [
  { id: 'domain-1', name: 'Payments', created_at: '', updated_at: '' },
]

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

function changeInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function changeSelect(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
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
    apiMock.listAuthUsers.mockResolvedValue({
      users: [
        {
          id: 'user-1',
          subject: 'alice',
          email: 'alice@example.com',
          display_name: 'Alice Admin',
          active: true,
          bindings: [],
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        },
      ],
      count: 1,
    })
    apiMock.createAuthUser.mockResolvedValue({
      id: 'user-2',
      subject: 'bob',
      email: 'bob@example.com',
      display_name: 'Bob Editor',
      active: true,
      bindings: [],
      created_at: '2026-05-16T04:00:00Z',
      updated_at: '2026-05-16T04:00:00Z',
    })
    apiMock.updateAuthUser.mockResolvedValue({
      id: 'user-1',
      subject: 'alice',
      email: 'alice@example.com',
      display_name: 'Alice Admin',
      active: true,
      bindings: [],
      created_at: '2026-05-16T00:00:00Z',
      updated_at: '2026-05-16T05:00:00Z',
    })
    apiMock.enableAuthUser.mockResolvedValue({})
    apiMock.disableAuthUser.mockResolvedValue({})
    apiMock.createAuthUserBinding.mockResolvedValue({})
    apiMock.deleteAuthUserBinding.mockResolvedValue({ deleted: 'binding-1' })
    apiMock.getAuthUserActivity.mockResolvedValue({
      events: [
        {
          id: 'event-1',
          action: 'auth.user.create',
          actor_type: 'user',
          actor_subject: 'alice',
          domain_id: 'domain-1',
          project_id: 'project-1',
          resource_type: 'user',
          resource_id: 'user-2',
          status: 'success',
          created_at: '2026-05-16T05:00:00Z',
        },
      ],
      usage: {
        generated_at: '2026-05-16T05:00:00Z',
        totals: { run_count: 1, total_vusers: 10, duration_seconds: 60, vuser_seconds: 600 },
        by_user: [{ key: 'alice', run_count: 1, total_vusers: 10, duration_seconds: 60, vuser_seconds: 600 }],
      },
      count: 1,
      limit: 50,
      offset: 0,
    })
    apiMock.downloadAuthUserActivity.mockResolvedValue(new Blob(['activity csv'], { type: 'text/csv' }))
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
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUsers={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Access')
    expect(container.textContent).toContain('Service Accounts')
    expect(container.textContent).toContain('Generate API Key')
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
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUsers={false} canViewUserActivity={false} canManageServiceAccounts />)
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

  it('onboards users and creates scoped role bindings', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUsers canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Users')
    expect(container.textContent).toContain('Alice Admin')

    const subjectInput = container.querySelector('input[placeholder="oidc-subject-or-upstream-user"]') as HTMLInputElement | null
    const emailInput = container.querySelector('input[placeholder="person@example.com"]') as HTMLInputElement | null
    const nameInput = container.querySelector('input[placeholder="Jordan Lee"]') as HTMLInputElement | null
    expect(subjectInput).toBeTruthy()
    expect(emailInput).toBeTruthy()
    expect(nameInput).toBeTruthy()
    if (!subjectInput || !emailInput || !nameInput) return

    await act(async () => {
      changeInput(subjectInput, 'bob')
      changeInput(emailInput, 'bob@example.com')
      changeInput(nameInput, 'Bob Editor')
    })

    const createUserButton = findButton(container, 'Create User')
    expect(createUserButton).toBeDefined()
    if (!createUserButton) return
    await act(async () => {
      createUserButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createAuthUser).toHaveBeenCalledWith({
      subject: 'bob',
      email: 'bob@example.com',
      display_name: 'Bob Editor',
    })

    const scopeSelect = container.querySelector('.access-control-section select') as HTMLSelectElement | null
    expect(scopeSelect).toBeTruthy()
    if (!scopeSelect) return
    await act(async () => {
      changeSelect(scopeSelect, 'domain')
    })

    const addRoleButton = findButton(container, 'Add Role')
    expect(addRoleButton).toBeDefined()
    if (!addRoleButton) return
    await act(async () => {
      addRoleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createAuthUserBinding).toHaveBeenCalledWith('user-1', {
      scope_type: 'domain',
      scope_id: 'domain-1',
      role: 'viewer',
    })
  })

  it('constructs activity filters and exports CSV', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUsers={false} canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Activity')
    expect(container.textContent).toContain('auth.user.create')
    expect(container.textContent).toContain('alice')

    const userInput = container.querySelector('input[placeholder="actor subject"]') as HTMLInputElement | null
    const actionInput = container.querySelector('input[placeholder="auth.user.create"]') as HTMLInputElement | null
    expect(userInput).toBeTruthy()
    expect(actionInput).toBeTruthy()
    if (!userInput || !actionInput) return

    await act(async () => {
      changeInput(userInput, 'alice')
      changeInput(actionInput, 'auth.user.create')
    })

    const applyButton = findButton(container, 'Apply Filters')
    expect(applyButton).toBeDefined()
    if (!applyButton) return
    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({
      actor_subject: 'alice',
      action: 'auth.user.create',
      domain_id: 'domain-1',
      project_id: 'project-1',
    }))

    const exportButton = findButton(container, 'Export CSV')
    expect(exportButton).toBeDefined()
    if (!exportButton) return
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.downloadAuthUserActivity).toHaveBeenCalledWith(expect.objectContaining({
      actor_subject: 'alice',
      action: 'auth.user.create',
    }))
    expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^apexload-user-activity-\d{4}-\d{2}-\d{2}\.csv$/))
  })
})
