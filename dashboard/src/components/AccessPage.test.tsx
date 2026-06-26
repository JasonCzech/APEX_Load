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
  onboardAuthUser: vi.fn(),
  updateAuthUser: vi.fn(),
  enableAuthUser: vi.fn(),
  disableAuthUser: vi.fn(),
  createAuthUserBinding: vi.fn(),
  deleteAuthUserBinding: vi.fn(),
  getAuthUserActivity: vi.fn(),
  downloadAuthUserActivity: vi.fn(),
  getAccessReview: vi.fn(),
  getAuthEffectiveAccessForPrincipal: vi.fn(),
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

function findButtonContaining(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes(label)) as HTMLButtonElement | undefined
}

async function clickButton(container: HTMLElement, label: string, contains = false) {
  const button = contains ? findButtonContaining(container, label) : findButton(container, label)
  expect(button).toBeDefined()
  if (!button) return
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function clickScopedButton(scope: HTMLElement, label: string) {
  const button = findButton(scope, label)
  expect(button).toBeDefined()
  if (!button) return
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function clickSortHeader(container: HTMLElement, label: string) {
  const header = Array.from(container.querySelectorAll<HTMLButtonElement>('th.sortable button')).find(button => button.textContent?.includes(label))
  expect(header).toBeDefined()
  if (!header) return
  await act(async () => {
    header.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
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

function tablePrimaryTexts(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('.access-data-table tbody tr .access-row-primary'))
    .map(button => button.textContent?.trim() || '')
}

function selectByLabelText(container: HTMLElement, label: string): HTMLSelectElement | undefined {
  return Array.from(container.querySelectorAll('label')).find(item => item.textContent?.includes(label))?.querySelector('select') as HTMLSelectElement | undefined
}

function inputByPlaceholder(container: HTMLElement, placeholder: string): HTMLInputElement | undefined {
  return container.querySelector(`input[placeholder="${placeholder}"]`) as HTMLInputElement | undefined
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
          owner_id: 'team-platform',
          contact_email: 'oncall@example.com',
          active: true,
          global_roles: ['viewer'],
          project_roles: { 'project-1': ['editor'] },
          scopes: ['tests:run', 'results:read'],
          last_used_at: '2026-05-17T02:00:00Z',
          key_rotated_at: '2026-05-16T00:00:00Z',
          rotation_due_at: '2026-08-16T00:00:00Z',
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-18T00:00:00Z',
        },
        {
          id: 'svc-2',
          name: 'API Reporter',
          description: 'Read-only exports',
          active: false,
          global_roles: ['viewer'],
          scopes: ['results:read', 'system:read'],
          created_at: '2026-05-14T00:00:00Z',
          updated_at: '2026-05-14T00:00:00Z',
        },
      ],
      count: 2,
    })
    apiMock.createAuthServiceAccount.mockResolvedValue({
      service_account: {
        id: 'svc-3',
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
          bindings: [
            { id: 'binding-1', user_id: 'user-1', scope_type: 'project', scope_id: 'project-1', role: 'editor', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
          ],
          last_seen_at: '2026-05-17T00:00:00Z',
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-18T00:00:00Z',
        },
        {
          id: 'user-2',
          subject: 'bob',
          email: 'bob@example.com',
          display_name: 'Bob Viewer',
          active: false,
          bindings: [
            { id: 'binding-2', user_id: 'user-2', scope_type: 'domain', scope_id: 'domain-1', role: 'viewer', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' },
          ],
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
          disabled_at: '2026-05-16T00:00:00Z',
        },
      ],
      count: 2,
    })
    apiMock.createAuthUser.mockResolvedValue({
      id: 'user-3',
      subject: 'cara',
      email: 'cara@example.com',
      display_name: 'Cara QA',
      active: true,
      bindings: [],
      created_at: '2026-05-16T04:00:00Z',
      updated_at: '2026-05-16T04:00:00Z',
    })
    apiMock.onboardAuthUser.mockResolvedValue({
      id: 'user-3',
      subject: 'cara',
      email: 'cara@example.com',
      display_name: 'Cara QA',
      active: true,
      bindings: [
        { id: 'binding-3', user_id: 'user-3', scope_type: 'project', scope_id: 'project-1', role: 'viewer', created_at: '2026-05-16T04:00:00Z', updated_at: '2026-05-16T04:00:00Z' },
      ],
      created_at: '2026-05-16T04:00:00Z',
      updated_at: '2026-05-16T04:00:00Z',
    })
    apiMock.updateAuthUser.mockResolvedValue({
      id: 'user-1',
      subject: 'alice-updated',
      email: 'alice.updated@example.com',
      display_name: 'Alice Updated',
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
          actor_display_name: 'Alice Admin',
          domain_id: 'domain-1',
          project_id: 'project-1',
          resource_type: 'user',
          resource_id: 'user-3',
          status: 'success',
          details: { subject: 'cara' },
          created_at: '2026-05-16T05:00:00Z',
        },
        {
          id: 'event-2',
          action: 'auth.service_account.rotate',
          actor_type: 'service_account',
          actor_subject: 'Nightly CI',
          domain_id: 'domain-1',
          project_id: 'project-2',
          resource_type: 'service_account',
          resource_id: 'svc-1',
          status: 'failure',
          details: { reason: 'expired key' },
          created_at: '2026-05-15T05:00:00Z',
        },
      ],
      usage: {
        generated_at: '2026-05-16T05:00:00Z',
        totals: { run_count: 3, total_vusers: 35, duration_seconds: 300, vuser_seconds: 3600 },
        by_user: [
          { key: 'alice', run_count: 2, total_vusers: 30, duration_seconds: 240, vuser_seconds: 3000 },
          { key: 'bob', run_count: 1, total_vusers: 5, duration_seconds: 60, vuser_seconds: 600 },
        ],
      },
      count: 2,
      limit: 50,
      offset: 0,
    })
    apiMock.downloadAuthUserActivity.mockResolvedValue(new Blob(['activity csv'], { type: 'text/csv' }))
    apiMock.getAccessReview.mockResolvedValue({
      generated_at: '2026-06-26T00:00:00Z',
      stale_after_days: 90,
      expiring_within_days: 14,
      users: [],
      service_accounts: [],
      summary: {},
      recent_denials: 0,
      recent_denial_window_hours: 24,
    })
    apiMock.getAuthEffectiveAccessForPrincipal.mockResolvedValue({
      enabled: true,
      authenticated: true,
      simulated: true,
      principal_type: 'service_account',
      principal_id: 'svc-1',
      principal_name: 'Nightly CI',
      scopes_narrow_api_access: true,
      resources: [
        { id: 'tests', name: 'Tests', description: '', operations: [{ id: 'run_tests', label: 'Run tests', allowed: true }, { id: 'manage_tests', label: 'Manage tests', allowed: false }] },
      ],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('creates a service-account key from the Generate Key subtab', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Service Accounts')
    expect(container.textContent).toContain('Existing Accounts')
    expect(container.textContent).toContain('Nightly CI')

    await clickButton(container, 'Generate Key')
    expect(container.textContent).toContain('CI runner')
    expect(container.textContent).toContain('Create Service Account')

    const searchCheckbox = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => {
      const row = input.closest('.access-project-role-row')
      return row?.textContent?.includes('Search')
    }) as HTMLInputElement | undefined
    expect(searchCheckbox).toBeDefined()
    if (!searchCheckbox) return

    await act(async () => {
      searchCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickButton(container, 'Create Service Account')
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
  })

  it('sends owner/contact and domain roles when creating a service account (A11/C12)', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()
    await clickButton(container, 'Generate Key')

    const ownerInput = inputByPlaceholder(container, 'team or person responsible')
    const contactInput = inputByPlaceholder(container, 'oncall@example.com')
    expect(ownerInput).toBeDefined()
    expect(contactInput).toBeDefined()
    if (!ownerInput || !contactInput) return
    changeInput(ownerInput, 'team-sre')
    changeInput(contactInput, 'sre@example.com')

    // Check the Domain Roles row for domain-1 ("Payments").
    const domainCheckbox = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => {
      const row = input.closest('.access-project-role-row')
      return row?.textContent?.includes('Payments')
    }) as HTMLInputElement | undefined
    expect(domainCheckbox).toBeDefined()
    if (!domainCheckbox) return
    await act(async () => {
      domainCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await clickButton(container, 'Create Service Account')
    await flushPromises()

    expect(apiMock.createAuthServiceAccount).toHaveBeenCalledWith(expect.objectContaining({
      owner_id: 'team-sre',
      contact_email: 'sre@example.com',
      domain_roles: { 'domain-1': ['viewer'] },
    }))
  })

  it('edits a service account in place via PATCH (A6)', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    // svc-1 is auto-selected; its inspector shows the owner metadata (C12) and an Edit button.
    expect(container.textContent).toContain('team-platform')
    await clickButton(container, 'Edit')

    const nameInput = Array.from(container.querySelectorAll('label')).find(l => l.textContent?.includes('Service Account Name'))?.querySelector('input') as HTMLInputElement | undefined
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    changeInput(nameInput, 'Nightly CI v2')

    // Add a domain-scoped role in the edit form (the Domain Roles section lists "Payments").
    const domainCheckbox = Array.from(container.querySelectorAll('.access-inspector input[type="checkbox"]')).find(input => {
      const row = input.closest('.access-project-role-row')
      return row?.textContent?.includes('Payments')
    }) as HTMLInputElement | undefined
    expect(domainCheckbox).toBeDefined()
    if (!domainCheckbox) return
    await act(async () => { domainCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    await clickButton(container, 'Save Changes')
    await flushPromises()

    expect(apiMock.updateAuthServiceAccount).toHaveBeenCalledWith('svc-1', expect.objectContaining({
      name: 'Nightly CI v2',
      owner_id: 'team-platform',
      global_roles: ['viewer'],
      scopes: ['tests:run', 'results:read'],
      domain_roles: { 'domain-1': ['viewer'] },
    }))
  })

  it('preserves multi-role-per-scope on edit save instead of dropping extra roles (A6 fidelity)', async () => {
    apiMock.listAuthServiceAccounts.mockResolvedValueOnce({
      service_accounts: [{
        id: 'svc-multi', name: 'Multi Role', active: true,
        global_roles: ['viewer'],
        project_roles: { 'project-1': ['editor', 'project_admin'] },
        scopes: ['results:read'],
        created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-18T00:00:00Z',
      }],
      count: 1,
    })
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()
    await clickButton(container, 'Edit')
    // Save without touching the project role; the form shows only the first role but the
    // full stored array must be preserved on the PATCH.
    await clickButton(container, 'Save Changes')
    await flushPromises()

    expect(apiMock.updateAuthServiceAccount).toHaveBeenCalledWith('svc-multi', expect.objectContaining({
      project_roles: { 'project-1': ['editor', 'project_admin'] },
    }))
  })

  it('blocks an edit that strips all roles or scopes (A6 validation)', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()
    await clickButton(container, 'Edit')

    // Deselect the only global role (viewer) by clicking its active chip.
    const viewerChip = Array.from(container.querySelectorAll('button.access-chip')).find(b => b.textContent?.trim() === 'Viewer' && b.className.includes('active')) as HTMLButtonElement | undefined
    expect(viewerChip).toBeDefined()
    if (!viewerChip) return
    await act(async () => { viewerChip.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    await clickButton(container, 'Save Changes')
    await flushPromises()

    expect(apiMock.updateAuthServiceAccount).not.toHaveBeenCalled()
    expect(container.textContent).toContain('at least one global role')
  })

  it('runs the effective-access simulator for a selected principal (C11)', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings={false} canViewUserActivity canManageServiceAccounts />)
    })
    await flushPromises()

    await clickButton(container, 'Activity', true)
    await clickButton(container, 'Access Review')
    await flushPromises()
    expect(container.textContent).toContain('Effective-Access Simulator')

    const simSelects = () => Array.from(container.querySelectorAll('.access-simulator select')) as HTMLSelectElement[]
    expect(simSelects().length).toBeGreaterThanOrEqual(2)
    changeSelect(simSelects()[0], 'service_account')
    await flushPromises()
    changeSelect(simSelects()[1], 'svc-1')
    await flushPromises()

    await clickButton(container, 'Simulate')
    await flushPromises()

    expect(apiMock.getAuthEffectiveAccessForPrincipal).toHaveBeenCalledWith(expect.objectContaining({
      principal_type: 'service_account',
      principal_id: 'svc-1',
      domain_id: 'domain-1',
    }))
    // The result grid shows allowed operations and the scope-narrowing caveat.
    expect(container.textContent).toContain('Run tests')
    expect(container.textContent).toContain('narrow')
  })

  it('surfaces an attention badge when the access review flags principals (notifications)', async () => {
    apiMock.getAccessReview.mockResolvedValue({
      generated_at: '2026-06-26T00:00:00Z',
      stale_after_days: 90,
      expiring_within_days: 14,
      users: [],
      service_accounts: [{ id: 'svc-2', name: 'API Reporter', active: true, flags: ['overdue_rotation'] }],
      summary: { overdue_rotation: 1 },
      recent_denials: 2,
      recent_denial_window_hours: 24,
    })
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings={false} canViewUserActivity canManageServiceAccounts />)
    })
    await flushPromises()

    const badge = container.querySelector('.access-attention-badge')
    expect(badge).not.toBeNull()
    expect(badge?.textContent).toBe('3')

    // The banner lives in the service-accounts workspace.
    await clickButton(container, 'Service Accounts', true)
    await flushPromises()
    expect(container.textContent).toContain('need attention')
    expect(container.textContent).toContain('recent access denial')
  })

  it('sorts, filters, selects, and performs service-account lifecycle actions', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    await clickSortHeader(container, 'Account')
    expect(tablePrimaryTexts(container)).toEqual(['API Reporter', 'Nightly CI'])
    await clickSortHeader(container, 'Account')
    expect(tablePrimaryTexts(container)).toEqual(['Nightly CI', 'API Reporter'])

    const statusSelect = selectByLabelText(container, 'Status')
    expect(statusSelect).toBeDefined()
    if (!statusSelect) return
    await act(async () => {
      changeSelect(statusSelect, 'disabled')
    })
    expect(tablePrimaryTexts(container)).toEqual(['API Reporter'])

    await act(async () => {
      changeSelect(statusSelect, '')
    })
    const accountButton = findButton(container, 'Nightly CI')
    expect(accountButton).toBeDefined()
    if (!accountButton) return
    await act(async () => {
      accountButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const inspector = container.querySelector('aside[aria-label="Selected service account details"]') as HTMLElement | null
    expect(inspector?.textContent).toContain('Account Details')
    if (!inspector) return

    await clickScopedButton(inspector, 'Rotate Key')
    await flushPromises()
    expect(apiMock.rotateAuthServiceAccount).toHaveBeenCalledWith('svc-1')
    expect(container.textContent).toContain('srk_rotated_key')

    await clickScopedButton(inspector, 'Disable')
    await flushPromises()
    expect(apiMock.updateAuthServiceAccount).toHaveBeenCalledWith('svc-1', { active: false })

    await clickScopedButton(inspector, 'Delete')
    await flushPromises()
    expect(apiMock.deleteAuthServiceAccount).toHaveBeenCalledWith('svc-1')
  })

  it('onboards users with initial access and edits existing user access from the main panel', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Existing Users')
    expect(container.textContent).toContain('Alice Admin')
    expect(container.textContent).toContain('3,000 VUser seconds')

    const searchInput = inputByPlaceholder(container, 'name, email, role, scope')
    expect(searchInput).toBeDefined()
    if (!searchInput) return
    await act(async () => {
      changeInput(searchInput, 'bob')
    })
    expect(tablePrimaryTexts(container)).toEqual(['Bob Viewer'])

    await act(async () => {
      changeInput(searchInput, '')
    })
    const roleSelect = selectByLabelText(container, 'Role')
    expect(roleSelect).toBeDefined()
    if (!roleSelect) return
    await act(async () => {
      changeSelect(roleSelect, 'editor')
    })
    expect(tablePrimaryTexts(container)).toEqual(['Alice Admin'])

    await clickButton(container, 'Onboard Users')
    const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
    const emailInput = inputByPlaceholder(container, 'person@example.com')
    const nameInput = inputByPlaceholder(container, 'Jordan Lee')
    expect(subjectInput).toBeDefined()
    expect(emailInput).toBeDefined()
    expect(nameInput).toBeDefined()
    if (!subjectInput || !emailInput || !nameInput) return
    expect(container.textContent).toContain('Initial Permissions')

    await act(async () => {
      changeInput(subjectInput, 'cara')
      changeInput(emailInput, 'cara@example.com')
      changeInput(nameInput, 'Cara QA')
    })
    await clickButton(container, 'Add Domain')
    await clickButton(container, 'Add Project')
    const onboardProjectRows = Array.from(container.querySelectorAll<HTMLElement>('.access-binding-draft-row')).filter(row => {
      const scopeSelect = row.querySelector('select') as HTMLSelectElement | null
      return scopeSelect?.value === 'project'
    })
    expect(onboardProjectRows.length).toBeGreaterThanOrEqual(2)
    const onboardSecondProjectSelect = onboardProjectRows[1]?.querySelectorAll<HTMLSelectElement>('select')[1]
    expect(onboardSecondProjectSelect).toBeDefined()
    if (!onboardSecondProjectSelect) return
    await act(async () => {
      changeSelect(onboardSecondProjectSelect, 'project-2')
    })
    await clickButton(container, 'Create User')
    await flushPromises()
    expect(apiMock.onboardAuthUser).toHaveBeenCalledWith({
      subject: 'cara',
      email: 'cara@example.com',
      display_name: 'Cara QA',
      bindings: [
        { scope_type: 'project', scope_id: 'project-1', role: 'viewer' },
        { scope_type: 'domain', scope_id: 'domain-1', role: 'viewer' },
        { scope_type: 'project', scope_id: 'project-2', role: 'viewer' },
      ],
    })
    expect(apiMock.createAuthUserBinding.mock.calls.filter(call => call[0] === 'user-3')).toEqual([])

    await clickButton(container, 'Existing Users')
    const aliceButton = findButton(container, 'Alice Admin')
    expect(aliceButton).toBeDefined()
    if (!aliceButton) return
    await act(async () => {
      aliceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const inspector = container.querySelector('aside[aria-label="Selected user details"]') as HTMLElement | null
    expect(inspector?.textContent).toContain('User Details')
    if (!inspector) return
    expect(inspector.querySelectorAll('input, select').length).toBe(0)
    expect(inspector.textContent).not.toContain('Save Changes')
    expect(inspector.textContent).not.toContain('Add Project')

    const userManagement = container.querySelector('.access-user-management') as HTMLElement | null
    expect(userManagement?.textContent).toContain('User Access')
    expect(userManagement?.textContent).toContain('Edit')
    if (!userManagement) return
    expect(userManagement.querySelectorAll('input, select').length).toBe(0)

    await clickScopedButton(userManagement, 'Edit')
    const editManagement = container.querySelector('.access-user-management') as HTMLElement | null
    expect(editManagement?.textContent).toContain('Edit User')
    expect(editManagement?.textContent).toContain('Roles, Domains, and Projects')
    if (!editManagement) return

    const managementInputs = editManagement.querySelectorAll<HTMLInputElement>('input')
    await act(async () => {
      changeInput(managementInputs[0], 'alice-updated')
      changeInput(managementInputs[1], 'alice.updated@example.com')
      changeInput(managementInputs[2], 'Alice Updated')
    })
    await clickScopedButton(editManagement, 'Remove Access')
    await flushPromises()
    expect(apiMock.disableAuthUser).toHaveBeenCalledWith('user-1')

    await clickScopedButton(editManagement, 'Add Domain')
    await clickScopedButton(editManagement, 'Add Project')
    const editProjectRows = Array.from(editManagement.querySelectorAll<HTMLElement>('.access-binding-draft-row')).filter(row => {
      const scopeSelect = row.querySelector('select') as HTMLSelectElement | null
      return scopeSelect?.value === 'project'
    })
    expect(editProjectRows.length).toBeGreaterThanOrEqual(2)
    const editSecondProjectSelect = editProjectRows[1]?.querySelectorAll<HTMLSelectElement>('select')[1]
    expect(editSecondProjectSelect).toBeDefined()
    if (!editSecondProjectSelect) return
    await act(async () => {
      changeSelect(editSecondProjectSelect, 'project-2')
    })
    await clickScopedButton(editManagement, 'Save Changes')
    await flushPromises()
    expect(apiMock.updateAuthUser).toHaveBeenCalledWith('user-1', {
      subject: 'alice-updated',
      email: 'alice.updated@example.com',
      display_name: 'Alice Updated',
    })
    const editBindingCalls = apiMock.createAuthUserBinding.mock.calls.filter(call => call[0] === 'user-1')
    expect(editBindingCalls).toEqual([
      ['user-1', { scope_type: 'domain', scope_id: 'domain-1', role: 'viewer' }],
      ['user-1', { scope_type: 'project', scope_id: 'project-2', role: 'viewer' }],
    ])
    expect(apiMock.deleteAuthUserBinding).not.toHaveBeenCalled()
  })

  it('lets binding-only access users edit scoped bindings without lifecycle controls', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(apiMock.listAuthUsers).toHaveBeenCalledWith({ limit: 50, offset: 0 })
    expect(container.textContent).toContain('Existing Users')
    expect(container.textContent).toContain('Alice Admin')
    expect(container.textContent).not.toContain('Onboard Users')
    expect(container.textContent).not.toContain('Create User')

    const userManagement = container.querySelector('.access-user-management') as HTMLElement | null
    expect(userManagement?.textContent).toContain('User Access')
    if (!userManagement) return
    await clickScopedButton(userManagement, 'Edit')

    const editManagement = container.querySelector('.access-user-management') as HTMLElement | null
    expect(editManagement?.textContent).toContain('Edit User')
    expect(editManagement?.textContent).toContain('Roles, Domains, and Projects')
    expect(editManagement?.textContent).not.toContain('Add Global Role')
    if (!editManagement) return

    const profileInputs = Array.from(editManagement.querySelectorAll<HTMLInputElement>('.access-form-grid input'))
    expect(profileInputs.length).toBe(3)
    expect(profileInputs.every(input => input.disabled)).toBe(true)

    const lifecycleButton = findButton(editManagement, 'Remove Access')
    expect(lifecycleButton?.disabled).toBe(true)

    await clickScopedButton(editManagement, 'Add Project')
    const editProjectRows = Array.from(editManagement.querySelectorAll<HTMLElement>('.access-binding-draft-row')).filter(row => {
      const scopeSelect = row.querySelector('select') as HTMLSelectElement | null
      return scopeSelect?.value === 'project'
    })
    expect(editProjectRows.length).toBeGreaterThanOrEqual(2)
    const editSecondProjectSelect = editProjectRows[1]?.querySelectorAll<HTMLSelectElement>('select')[1]
    expect(editSecondProjectSelect).toBeDefined()
    if (!editSecondProjectSelect) return
    await act(async () => {
      changeSelect(editSecondProjectSelect, 'project-2')
    })
    await clickScopedButton(editManagement, 'Save Changes')
    await flushPromises()

    expect(apiMock.updateAuthUser).not.toHaveBeenCalled()
    expect(apiMock.createAuthUser).not.toHaveBeenCalled()
    expect(apiMock.disableAuthUser).not.toHaveBeenCalled()
    expect(apiMock.enableAuthUser).not.toHaveBeenCalled()
    expect(apiMock.createAuthUserBinding).toHaveBeenCalledWith('user-1', {
      scope_type: 'project',
      scope_id: 'project-2',
      role: 'viewer',
    })
  })

  it('filters, exports, sorts, and selects activity events and usage rows', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Events')
    expect(container.textContent).toContain('auth.user.create')

    const userInput = inputByPlaceholder(container, 'actor subject')
    const actionInput = inputByPlaceholder(container, 'auth.user.create')
    expect(userInput).toBeDefined()
    expect(actionInput).toBeDefined()
    if (!userInput || !actionInput) return

    await act(async () => {
      changeInput(userInput, 'alice')
      changeInput(actionInput, 'auth.user.create')
    })
    await clickButton(container, 'Apply Filters')
    await flushPromises()
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({
      actor_subject: 'alice',
      action: 'auth.user.create',
      domain_id: 'domain-1',
      project_id: 'project-1',
    }))

    await clickButton(container, 'Export CSV')
    await flushPromises()
    expect(apiMock.downloadAuthUserActivity).toHaveBeenCalledWith(expect.objectContaining({
      actor_subject: 'alice',
      action: 'auth.user.create',
    }))
    expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^apexload-user-activity-\d{4}-\d{2}-\d{2}\.csv$/))

    const tableSearch = inputByPlaceholder(container, 'actor, action, resource, details')
    expect(tableSearch).toBeDefined()
    if (!tableSearch) return
    await act(async () => {
      changeInput(tableSearch, 'expired key')
    })
    expect(container.querySelector('.access-data-table tbody')?.textContent).toContain('auth.service_account.rotate')
    expect(container.querySelector('.access-data-table tbody')?.textContent).not.toContain('auth.user.create')

    const rotateEventActor = findButton(container, 'Nightly CI')
    expect(rotateEventActor).toBeDefined()
    if (!rotateEventActor) return
    await act(async () => {
      rotateEventActor.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('aside[aria-label="Selected activity event details"]')?.textContent).toContain('expired key')

    await clickButton(container, 'Usage By User')
    expect(container.textContent).toContain('alice')
    await clickSortHeader(container, 'User')
    const usageRows = Array.from(container.querySelectorAll('.access-usage-table tbody tr')).map(row => row.textContent || '')
    expect(usageRows[0]).toContain('alice')
    await clickSortHeader(container, 'VUser Seconds')
    const sortedUsageRows = Array.from(container.querySelectorAll('.access-usage-table tbody tr')).map(row => row.textContent || '')
    expect(sortedUsageRows[0]).toContain('alice')
  })
})
