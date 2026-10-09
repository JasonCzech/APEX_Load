// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AccessPage from './AccessPage'
import { downloadBlob } from '../utils/fileUtils'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import type { AccessView } from './accessDestinations'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listAuthServiceAccounts: vi.fn(),
  createAuthServiceAccount: vi.fn(),
  updateAuthServiceAccount: vi.fn(),
  rotateAuthServiceAccount: vi.fn(),
  deleteAuthServiceAccount: vi.fn(),
  listAuthUsers: vi.fn(),
  getAuthUser: vi.fn(),
  createAuthUser: vi.fn(),
  onboardAuthUser: vi.fn(),
  updateAuthUser: vi.fn(),
  enableAuthUser: vi.fn(),
  disableAuthUser: vi.fn(),
  revokeAuthUserSessions: vi.fn(),
  deleteAuthUser: vi.fn(),
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
    await Promise.resolve()
  })
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
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
  const header = Array.from(container.querySelectorAll<HTMLButtonElement>('th button')).find(button => button.textContent?.includes(label))
  expect(header).toBeDefined()
  if (!header) return
  await act(async () => {
    header.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

function changeInput(input: HTMLInputElement, value: string) {
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function changeSelect(select: HTMLSelectElement, value: string) {
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
    setter?.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
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
      bindings: [
        { id: 'binding-1', user_id: 'user-1', scope_type: 'project', scope_id: 'project-1', role: 'editor', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
      ],
      created_at: '2026-05-16T00:00:00Z',
      updated_at: '2026-05-16T05:00:00Z',
    })
    apiMock.enableAuthUser.mockResolvedValue({})
    apiMock.disableAuthUser.mockResolvedValue({})
    apiMock.revokeAuthUserSessions.mockResolvedValue({})
    apiMock.deleteAuthUser.mockResolvedValue({ deleted: 'user-1' })
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

  it('loads older event history into the same table and exports the complete applied filter, not draft changes', async () => {
    const base = await apiMock.getAuthUserActivity()
    apiMock.getAuthUserActivity.mockResolvedValue({ ...base, has_more: true })
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle={false} canManageUserBindings={false} canManageServiceAccounts={false} canViewUserActivity />))
    await flushPromises()
    const firstQuery = apiMock.getAuthUserActivity.mock.lastCall?.[0]
    expect(firstQuery.offset).toBe(0)
    expect(Array.from(container.querySelectorAll('button')).map(button => button.textContent)).not.toContain('Next events')

    // The older batch overlaps the loaded rows by one event (recorded since the
    // first load); it is appended once, after the rows already shown.
    const older = { ...base.events[0], id: 'event-older', actor_subject: 'zed', actor_display_name: 'Zed Older', created_at: '2026-05-01T05:00:00Z' }
    apiMock.getAuthUserActivity.mockResolvedValueOnce({ ...base, events: [base.events[base.events.length - 1], older], has_more: false })
    await clickButton(container, 'Load older events')
    await flushPromises()
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({offset:base.events.length,created_before:firstQuery.created_before}))
    expect(container.querySelectorAll('.access-data-table tbody tr')).toHaveLength(base.events.length + 1)
    expect(tablePrimaryTexts(container)).toContain('Zed Older')
    expect(container.querySelector('.access-table-wrap .access-load-older')?.textContent).toBe(`All ${base.events.length + 1} matching events loaded`)
    expect(container.querySelector('[data-table-pagination="off"]')).not.toBeNull()
    changeInput(inputByPlaceholder(container,'actor subject')!, 'unapplied-user')
    await clickButton(container, 'Export all matching events CSV')
    expect(apiMock.downloadAuthUserActivity).toHaveBeenCalledWith(expect.objectContaining({all:'true',created_before:firstQuery.created_before}))
    expect(apiMock.downloadAuthUserActivity.mock.lastCall?.[0].actor_subject).toBeUndefined()
    await clickButton(container, 'Apply Filters')
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({offset:0,actor_subject:'unapplied-user'}))
  })

  it('keeps usage filters and exports independent from Events', async () => {
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle={false} canManageUserBindings={false} canManageServiceAccounts={false} canViewUserActivity />))
    await flushPromises()
    changeInput(inputByPlaceholder(container,'actor subject')!, 'event-only')
    await clickButton(container, 'Apply Filters')
    await clickButton(container, 'Usage By User')
    await flushPromises()
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({report:'usage',actor_subject:undefined}))
    expect(container.textContent).toContain('run creation time')
    await clickButton(container,'Export usage CSV')
    expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob),'apexload-usage-by-user.csv')
    expect(apiMock.downloadAuthUserActivity).not.toHaveBeenCalled()
    await clickButton(container,'alice')
    await flushPromises()
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({report:'usage',actor_subject:'alice',offset:0}))
  })

  it('validates usage dates, recovers from errors, and pages matching run links', async () => {
    const base = await apiMock.getAuthUserActivity()
    apiMock.getAuthUserActivity.mockImplementation(async (query) => ({...base,has_more:true,usage_runs:[{id:'run-1',name:'Matching run',actor:'alice',project_id:'project-1',status:'COMPLETE',created_at:'2026-01-01T00:00:00Z'}],query}))
    await act(async () => root.render(<AccessPage accessView="usage" projects={projects} domains={domains} canManageUserLifecycle={false} canManageUserBindings={false} canManageServiceAccounts={false} canViewUserActivity />))
    await flushPromises()
    expect(container.querySelector('a[href="#/test/run-1?projectId=project-1"]')?.textContent).toBe('Matching run')
    await clickButton(container,'Next runs')
    expect(apiMock.getAuthUserActivity).toHaveBeenLastCalledWith(expect.objectContaining({report:'usage',offset:25}))
    const dates=container.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
    changeInput(dates[0],'2026-03-02T12:00'); changeInput(dates[1],'2026-03-01T12:00')
    expect(findButton(container,'Apply usage filters')?.disabled).toBe(true)
    expect(container.textContent).toContain('start date must be before')
    changeInput(dates[1],'2026-03-03T12:00')
    apiMock.getAuthUserActivity.mockRejectedValueOnce(new Error('Usage unavailable'))
    await clickButton(container,'Apply usage filters')
    await flushPromises()
    expect(container.textContent).toContain('Usage unavailable')
    await clickButton(container,'Retry usage')
    await flushPromises()
    expect(container.textContent).toContain('Matching run')
  })

  it('searches and pages role-preview users independently of the main directory', async () => {
    const target={id:'user-later',subject:'later',display_name:'Later directory user',active:true,bindings:[]}
    const base=await apiMock.listAuthUsers()
    apiMock.listAuthUsers.mockImplementation(async (query) => query.offset===50 || query.q==='later' ? {users:[target],count:1,has_more:false} : {...base,has_more:true})
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle canManageUserBindings canManageServiceAccounts canViewUserActivity />))
    await flushPromises()
    await clickButton(container,'Activity',true)
    await clickButton(container,'Access Review')
    await flushPromises()
    await clickButton(container,'More users')
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith(expect.objectContaining({offset:50,limit:50}))
    expect(container.querySelector('.access-simulator')?.textContent).toContain('Later directory user')
    const search=container.querySelector<HTMLInputElement>('.access-simulator input')!
    changeInput(search,'later')
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({q:'later',limit:50,offset:0})
    expect(container.querySelector('.access-simulator')?.textContent).toContain('Later directory user')
  })

  it('refreshes service-account review targets and never substitutes another account', async () => {
    const base=await apiMock.getAccessReview()
    apiMock.getAccessReview.mockResolvedValue({...base,service_accounts:[{id:'svc-later',name:'Later account',active:true,flags:['never_used']}]})
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle canManageUserBindings canManageServiceAccounts canViewUserActivity />))
    await flushPromises()
    await clickButton(container,'Activity',true)
    await clickButton(container,'Access Review')
    await flushPromises()
    await clickButton(container,'Inspect account')
    expect(container.textContent).toContain('Account is no longer available')
    expect(container.querySelector('aside')).toBeNull()
    apiMock.listAuthServiceAccounts.mockResolvedValue({service_accounts:[{id:'svc-later',name:'Later account',active:true,global_roles:['viewer'],scopes:['results:read']}],count:1})
    await clickButton(container,'Retry access review')
    await flushPromises()
    await clickButton(container,'Inspect account')
    expect(container.querySelector('aside')?.textContent).toContain('Later account')
  })

  it('hides existing identities on creation and exposes permission selections', async () => {
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle canManageUserBindings canManageServiceAccounts canViewUserActivity authMode="local" />))
    await flushPromises()
    await clickButton(container,'Onboard Users')
    expect(container.querySelector('aside')).toBeNull()
    expect(container.textContent).toContain('does not create a new local login')
    await clickButton(container,'Service accounts',true)
    await clickButton(container,'Create service account')
    expect(container.querySelector('aside')).toBeNull()
    expect(container.querySelectorAll('button[aria-pressed]').length).toBeGreaterThan(0)
    expect(container.querySelector('select[aria-label="Role for Checkout"]')).not.toBeNull()
    expect(container.textContent).toContain('Rotation interval (days)')
  })

  it('exports review findings and opens the exact user even outside the directory page', async () => {
    const review = await apiMock.getAccessReview()
    apiMock.getAccessReview.mockResolvedValue({...review,users:[{id:'review-user',subject:'review',display_name:'Review Target',active:true,flags:['no_bindings']}]})
    apiMock.getAuthUser.mockResolvedValue({id:'review-user',subject:'review',display_name:'Review Target',active:true,bindings:[]})
    await act(async () => root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle canManageUserBindings canManageServiceAccounts canViewUserActivity />))
    await flushPromises()
    await clickButton(container,'Activity',true)
    await clickButton(container,'Access Review')
    await flushPromises()
    await clickButton(container,'Export access review CSV')
    expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob),'apexload-access-review.csv')
    expect(apiMock.downloadAuthUserActivity).not.toHaveBeenCalled()
    await clickButton(container,'Inspect user')
    await flushPromises()
    expect(apiMock.getAuthUser).toHaveBeenCalledWith('review-user')
    expect(container.querySelector('aside')?.textContent).toContain('Review Target')
  })

  it('explains genuinely empty user scope without suggesting nonexistent filters', async () => {
    apiMock.listAuthUsers.mockResolvedValue({users:[],count:0,total:0})
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()
    expect(container.textContent).toContain('No users in this scope')
    expect(container.textContent).toContain('Open Onboard Users to create the first account.')
    expect(container.textContent).not.toContain('Adjust search or filters')
  })

  it('creates a service-account key from the Create service account subtab', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Service Accounts')
    expect(container.textContent).toContain('Existing Accounts')
    expect(container.textContent).toContain('Nightly CI')

    await clickButton(container, 'Create service account')
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
    await clickButton(container, 'Create service account')

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
    expect(container.textContent).toContain('Role permissions preview')

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
    expect(container.textContent).toContain('token scopes')
  })

  it('hides access review for activity-only project admins', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Activity')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Access Review')).toBe(false)
    expect(apiMock.getAccessReview).not.toHaveBeenCalled()
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
    expect(badge?.querySelector('[aria-hidden="true"]')?.textContent).toBe('3')

    // The banner lives in the service-accounts workspace.
    await clickButton(container, 'Service accounts', true)
    await flushPromises()
    expect(container.textContent).toContain('have review findings')
    expect(container.textContent).toContain('recent access denial')
  })

  it('keeps an untouched onboarding preset clean when workspace catalogs hydrate', async () => {
    const renderAccess = (loaded: boolean) => <NavigationGuardProvider><AccessPage projects={loaded ? projects : []} domains={loaded ? domains : []} activeDomain={loaded ? domains[0] : undefined} activeProject={loaded ? projects[0] : undefined} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts /></NavigationGuardProvider>
    await act(async () => root.render(renderAccess(false)))
    await flushPromises()
    await act(async () => root.render(renderAccess(true)))
    await flushPromises()
    await clickButton(container, 'Onboard Users')
    await clickButton(container, 'Service accounts', true)
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.querySelector('section[aria-label="Service accounts access workspace"]')).not.toBeNull()
  })

  it('guards user onboarding and service-account creation drafts across workspace tabs', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    await clickButton(container, 'Onboard Users')
    const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
    expect(subjectInput).toBeDefined()
    if (!subjectInput) return
    changeInput(subjectInput, 'draft-user')

    await clickButton(container, 'Service accounts', true)
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain('User onboarding form has unsaved changes')
    await clickButton(container, 'Keep editing')
    expect(inputByPlaceholder(container, 'oidc-subject-or-upstream-user')?.value).toBe('draft-user')

    await clickButton(container, 'Service accounts', true)
    await clickButton(container, 'Discard changes')
    expect(container.querySelector('section[aria-label="Service accounts access workspace"]')).not.toBeNull()

    await clickButton(container, 'Create service account')
    // The preset itself is the baseline and should not produce a false-positive guard.
    await clickButton(container, 'Users', true)
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    await clickButton(container, 'Service accounts', true)
    await clickButton(container, 'Create service account')
    const accountNameInput = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Service Account Name'))?.querySelector('input') as HTMLInputElement | undefined
    expect(accountNameInput).toBeDefined()
    if (!accountNameInput) return
    changeInput(accountNameInput, 'Draft automation key')

    await clickButton(container, 'Users', true)
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain('Service-account creation form has unsaved changes')
    await clickButton(container, 'Save changes')
    await flushPromises()
    expect(apiMock.createAuthServiceAccount).toHaveBeenCalledWith(expect.objectContaining({ name: 'Draft automation key' }))
    expect(container.querySelector('section[aria-label="Users access workspace"]')).not.toBeNull()
  })

  // App routes Access sub-tab changes through a guarded view change, which reads
  // the dirty-draft flags synchronously. A successful create must not leave the
  // just-saved draft flagged dirty, or the guard opens right after the create
  // (and stacks over the one-time API-key dialog for service accounts).
  it.each([
    { kind: 'user', initial: 'onboard' as AccessView, submit: 'Create User', settled: 'Users access workspace' },
    { kind: 'service account', initial: 'create-account' as AccessView, submit: 'Create Service Account', settled: 'Service accounts access workspace' },
  ])('does not raise the unsaved-changes guard after creating a $kind through a guarded route', async ({ kind, initial, submit, settled }) => {
    function GuardedAccess() {
      const { runGuarded } = useNavigationGuardRunner()
      const [view, setView] = useState<AccessView>(initial)
      return <AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts accessView={view} onViewChange={next => runGuarded(() => setView(next))} />
    }
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(<NavigationGuardProvider><GuardedAccess /></NavigationGuardProvider>)
    })
    await flushPromises()
    if (kind === 'user') {
      const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
      expect(subjectInput).toBeDefined()
      if (!subjectInput) return
      changeInput(subjectInput, 'cara')
    } else {
      const accountNameInput = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Service Account Name'))?.querySelector('input') as HTMLInputElement | undefined
      expect(accountNameInput).toBeDefined()
      if (!accountNameInput) return
      changeInput(accountNameInput, 'Nightly automation')
    }

    await clickButton(container, submit)
    await flushPromises()

    expect(Array.from(container.querySelectorAll('[role="dialog"], [role="alertdialog"]')).map(dialog => dialog.textContent).join(' ')).not.toContain('unsaved changes')
    expect(container.querySelector(`section[aria-label="${settled}"]`)).not.toBeNull()
  })

  it('guards user and service-account edit drafts when switching rows', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const userManagement = container.querySelector('.access-user-management') as HTMLElement | null
    expect(userManagement).not.toBeNull()
    if (!userManagement) return
    await clickScopedButton(userManagement, 'Edit')
    const editManagement = container.querySelector('.access-user-management') as HTMLElement | null
    const profileInputs = editManagement?.querySelectorAll<HTMLInputElement>('.access-form-grid input')
    expect(profileInputs?.length).toBe(3)
    if (!profileInputs) return
    changeInput(profileInputs[2], 'Alice Draft')

    apiMock.listAuthUsers.mockResolvedValueOnce({
      users: [{
        id: 'user-2', subject: 'bob', email: 'bob@example.com', display_name: 'Bob Viewer', active: false,
        bindings: [{ id: 'binding-2', user_id: 'user-2', scope_type: 'domain', scope_id: 'domain-1', role: 'viewer', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }],
        created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z', disabled_at: '2026-05-16T00:00:00Z',
      }],
      count: 1,
      total: 1,
    })
    const searchInput = inputByPlaceholder(container, 'name, email, role, scope')
    expect(searchInput).toBeDefined()
    if (!searchInput) return
    changeInput(searchInput, 'bob')
    await flushPromises()
    expect(container.querySelectorAll<HTMLInputElement>('.access-user-management .access-form-grid input')[2]?.value).toBe('Alice Draft')

    await clickButton(container, 'Bob Viewer')
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain('User editor has unsaved changes')
    await clickButton(container, 'Save changes')
    await flushPromises()
    expect(apiMock.updateAuthUser).toHaveBeenCalledWith('user-1', {
      subject: 'alice',
      email: 'alice@example.com',
      display_name: 'Alice Draft',
    })
    expect(container.querySelector('aside[aria-label="Selected user details"]')?.textContent).toContain('Bob Viewer')

    await clickButton(container, 'Service accounts', true)
    const accountInspector = container.querySelector('aside[aria-label="Selected service account details"]') as HTMLElement | null
    expect(accountInspector).not.toBeNull()
    if (!accountInspector) return
    await clickScopedButton(accountInspector, 'Edit')
    const accountEditName = container.querySelector('aside[aria-label="Edit service account"] input') as HTMLInputElement | null
    expect(accountEditName).not.toBeNull()
    if (!accountEditName) return
    changeInput(accountEditName, 'Nightly CI Draft')

    await clickButton(container, 'API Reporter')
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain('Service-account editor has unsaved changes')
    await clickButton(container, 'Discard changes')
    expect(container.querySelector('aside[aria-label="Selected service account details"]')?.textContent).toContain('API Reporter')
  })

  it('keeps a guard open when draft validation prevents saving', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await clickButton(container, 'Onboard Users')
    const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
    const emailInput = inputByPlaceholder(container, 'person@example.com')
    expect(subjectInput).toBeDefined()
    expect(emailInput).toBeDefined()
    if (!subjectInput || !emailInput) return
    changeInput(subjectInput, 'invalid-email-user')
    changeInput(emailInput, 'not-an-email')

    await clickButton(container, 'Service accounts', true)
    await clickButton(container, 'Save changes')
    await flushPromises()

    expect(apiMock.onboardAuthUser).not.toHaveBeenCalled()
    expect(container.querySelector('[role="dialog"]')?.textContent).toContain('Save did not complete')
    expect(container.querySelector('section[aria-label="Users access workspace"]')).not.toBeNull()
  })

  it('ignores out-of-order user-list and access-review responses', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts={false} />)
    })
    await flushPromises()

    const aliceRequest = deferred<{ users: Array<{ id: string; subject: string; email: string; display_name: string; active: boolean; bindings: never[]; created_at: string; updated_at: string }>; count: number; total: number }>()
    const bobRequest = deferred<{ users: Array<{ id: string; subject: string; email: string; display_name: string; active: boolean; bindings: never[]; created_at: string; updated_at: string }>; count: number; total: number }>()
    apiMock.listAuthUsers
      .mockReturnValueOnce(aliceRequest.promise)
      .mockReturnValueOnce(bobRequest.promise)
    const searchInput = inputByPlaceholder(container, 'name, email, role, scope')
    expect(searchInput).toBeDefined()
    if (!searchInput) return
    changeInput(searchInput, 'alice')
    changeInput(searchInput, 'bob')
    await act(async () => {
      bobRequest.resolve({
        users: [{ id: 'user-bob-new', subject: 'bob-new', email: 'bob.new@example.com', display_name: 'Bob New', active: true, bindings: [], created_at: '', updated_at: '2026-06-01T00:00:00Z' }],
        count: 1,
        total: 1,
      })
      await bobRequest.promise
    })
    await act(async () => {
      aliceRequest.resolve({
        users: [{ id: 'user-alice-old', subject: 'alice-old', email: 'alice.old@example.com', display_name: 'Alice Old', active: true, bindings: [], created_at: '', updated_at: '2026-05-01T00:00:00Z' }],
        count: 1,
        total: 1,
      })
      await aliceRequest.promise
    })
    await flushPromises()
    expect(tablePrimaryTexts(container)).toEqual(['Bob New'])

    const oldReview = deferred<{ generated_at: string; stale_after_days: number; expiring_within_days: number; users: never[]; service_accounts: never[]; summary: Record<string, number>; recent_denials: number; recent_denial_window_hours: number }>()
    const newReview = deferred<{ generated_at: string; stale_after_days: number; expiring_within_days: number; users: never[]; service_accounts: never[]; summary: Record<string, number>; recent_denials: number; recent_denial_window_hours: number }>()
    apiMock.getAccessReview
      .mockReturnValueOnce(oldReview.promise)
      .mockReturnValueOnce(newReview.promise)
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts={false} accessReviewRefreshSignal={1} />)
    })
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity canManageServiceAccounts={false} accessReviewRefreshSignal={2} />)
    })
    await act(async () => {
      newReview.resolve({ generated_at: '2026-06-02T00:00:00Z', stale_after_days: 90, expiring_within_days: 14, users: [], service_accounts: [], summary: {}, recent_denials: 2, recent_denial_window_hours: 24 })
      await newReview.promise
    })
    await act(async () => {
      oldReview.resolve({ generated_at: '2026-06-01T00:00:00Z', stale_after_days: 90, expiring_within_days: 14, users: [], service_accounts: [], summary: {}, recent_denials: 1, recent_denial_window_hours: 24 })
      await oldReview.promise
    })
    await flushPromises()
    expect(container.querySelector('.access-attention-badge [aria-hidden="true"]')?.textContent).toBe('2')
  })

  it('does not let stale list refreshes revert or resurrect account mutations', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    apiMock.rotateAuthServiceAccount.mockResolvedValueOnce({
      service_account: {
        id: 'svc-1', name: 'Nightly CI Rotated', active: true, global_roles: ['viewer'], scopes: ['tests:run'],
        created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-19T00:00:00Z',
      },
      api_key: 'srk_authoritative',
    })
    const inspector = container.querySelector('aside[aria-label="Selected service account details"]') as HTMLElement | null
    expect(inspector).not.toBeNull()
    if (!inspector) return
    await clickScopedButton(inspector, 'Rotate Key')
    await flushPromises()
    expect(tablePrimaryTexts(container)).toContain('Nightly CI Rotated')

    const rotatedInspector = container.querySelector('aside[aria-label="Selected service account details"]') as HTMLElement | null
    expect(rotatedInspector).not.toBeNull()
    if (!rotatedInspector) return
    await clickScopedButton(rotatedInspector, 'Delete')
    await flushPromises()
    expect(tablePrimaryTexts(container)).not.toContain('Nightly CI')
    expect(tablePrimaryTexts(container)).not.toContain('Nightly CI Rotated')
    expect(tablePrimaryTexts(container)).toContain('API Reporter')
  })

  it('keeps an onboarded user visible while the users list is still stale', async () => {
    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()
    await clickButton(container, 'Onboard Users')
    const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
    const emailInput = inputByPlaceholder(container, 'person@example.com')
    const nameInput = inputByPlaceholder(container, 'Jordan Lee')
    expect(subjectInput).toBeDefined()
    expect(emailInput).toBeDefined()
    expect(nameInput).toBeDefined()
    if (!subjectInput || !emailInput || !nameInput) return
    changeInput(subjectInput, 'cara')
    changeInput(emailInput, 'cara@example.com')
    changeInput(nameInput, 'Cara QA')

    await clickButton(container, 'Create User')
    await flushPromises()

    expect(tablePrimaryTexts(container)).toContain('Cara QA')
    expect(container.textContent).toContain('3 users')
  })

  it('scopes authoritative user reconciliation to domain and project filters', async () => {
    apiMock.listAuthUsers.mockImplementation(async (params?: { domain_id?: string; project_id?: string }) => {
      if (params?.project_id === 'project-2') {
        return { users: [], count: 0, total: 0, limit: 50, offset: 0 }
      }
      const alice = {
        id: 'user-1',
        subject: 'alice',
        email: 'alice@example.com',
        display_name: 'Alice Admin',
        active: true,
        bindings: [
          { id: 'binding-1', user_id: 'user-1', scope_type: 'project', scope_id: 'project-1', role: 'editor', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
        ],
        created_at: '2026-05-16T00:00:00Z',
        updated_at: '2026-05-18T00:00:00Z',
      }
      return { users: [alice], count: 1, total: 1, limit: 50, offset: 0 }
    })

    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    const userFilterSelect = (label: string) => Array.from(container.querySelectorAll<HTMLLabelElement>('.access-table-toolbar label'))
      .find(item => item.querySelector('span')?.textContent === label)
      ?.querySelector('select') as HTMLSelectElement | undefined
    const domainSelect = userFilterSelect('Domain')
    const projectSelect = userFilterSelect('Project')
    expect(domainSelect).toBeDefined()
    expect(projectSelect).toBeDefined()
    if (!domainSelect || !projectSelect) return

    await act(async () => changeSelect(domainSelect, 'domain-1'))
    const projectOneSelect = userFilterSelect('Project')
    expect(projectOneSelect).toBeDefined()
    if (!projectOneSelect) return
    await act(async () => changeSelect(projectOneSelect, 'project-1'))
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, domain_id: 'domain-1', project_id: 'project-1' })

    await clickButton(container, 'Onboard Users')
    const subjectInput = inputByPlaceholder(container, 'oidc-subject-or-upstream-user')
    expect(subjectInput).toBeDefined()
    if (!subjectInput) return
    changeInput(subjectInput, 'cara')
    await clickButton(container, 'Create User')
    await flushPromises()
    expect(tablePrimaryTexts(container)).toContain('Cara QA')

    const projectTwoSelect = userFilterSelect('Project')
    expect(projectTwoSelect).toBeDefined()
    if (!projectTwoSelect) return
    await act(async () => changeSelect(projectTwoSelect, 'project-2'))
    await flushPromises()

    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, domain_id: 'domain-1', project_id: 'project-2' })
    expect(container.querySelector('.access-panel-header')?.textContent).toContain('0 of 0 users')
    expect(tablePrimaryTexts(container)).not.toContain('Cara QA')
  })

  it('reloads the current user scope when filters change during a mutation', async () => {
    const alice = {
      id: 'user-1',
      subject: 'alice',
      email: 'alice@example.com',
      display_name: 'Alice Admin',
      active: true,
      bindings: [
        { id: 'binding-1', user_id: 'user-1', scope_type: 'project', scope_id: 'project-1', role: 'editor', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
      ],
      created_at: '2026-05-16T00:00:00Z',
      updated_at: '2026-05-18T00:00:00Z',
    }
    apiMock.listAuthUsers.mockImplementation(async (params?: { project_id?: string }) => (
      params?.project_id === 'project-2'
        ? { users: [], count: 0, total: 0, limit: 50, offset: 0 }
        : { users: [alice], count: 1, total: 1, limit: 50, offset: 0 }
    ))
    let resolveDisable: ((user: typeof alice) => void) | undefined
    apiMock.disableAuthUser.mockImplementation(() => new Promise(resolve => { resolveDisable = resolve }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle canManageUserBindings canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    const userFilterSelect = (label: string) => Array.from(container.querySelectorAll<HTMLLabelElement>('.access-table-toolbar label'))
      .find(item => item.querySelector('span')?.textContent === label)
      ?.querySelector('select') as HTMLSelectElement | undefined
    const projectSelect = userFilterSelect('Project')
    expect(projectSelect).toBeDefined()
    if (!projectSelect) return

    await act(async () => changeSelect(projectSelect, 'project-1'))
    await flushPromises()
    const removeAccess = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Remove Access')
    expect(removeAccess).toBeDefined()
    if (!removeAccess) return
    await act(async () => removeAccess.click())

    const currentProjectSelect = userFilterSelect('Project')
    expect(currentProjectSelect).toBeDefined()
    if (!currentProjectSelect) return
    await act(async () => changeSelect(currentProjectSelect, 'project-2'))
    await flushPromises()

    await act(async () => resolveDisable?.({ ...alice, active: false, updated_at: '2026-05-19T00:00:00Z' }))
    await flushPromises()
    await flushPromises()

    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, project_id: 'project-2' })
    expect(container.querySelector('.access-panel-header')?.textContent).toContain('0 of 0 users')
    expect(tablePrimaryTexts(container)).not.toContain('Alice Admin')
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

  it('runs lifecycle mutations single-flight and keeps the committed account when its refresh fails', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const rotation = deferred<{
      service_account: {
        id: string
        name: string
        active: boolean
        global_roles: string[]
        scopes: string[]
        created_at: string
        updated_at: string
      }
      api_key: string
    }>()
    apiMock.rotateAuthServiceAccount.mockReturnValueOnce(rotation.promise)

    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings={false} canViewUserActivity={false} canManageServiceAccounts />)
    })
    await flushPromises()

    const inspector = container.querySelector('aside[aria-label="Selected service account details"]') as HTMLElement | null
    const rotateButton = inspector ? findButton(inspector, 'Rotate Key') : undefined
    expect(rotateButton).toBeDefined()
    if (!rotateButton) return

    await act(async () => {
      rotateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rotateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(apiMock.rotateAuthServiceAccount).toHaveBeenCalledTimes(1)
    expect(window.confirm).toHaveBeenCalledTimes(1)

    apiMock.listAuthServiceAccounts.mockRejectedValueOnce(new Error('account refresh failed'))
    await act(async () => {
      rotation.resolve({
        service_account: {
          id: 'svc-1',
          name: 'Nightly CI Rotated',
          active: true,
          global_roles: ['viewer'],
          scopes: ['tests:run'],
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-19T00:00:00Z',
        },
        api_key: 'srk_single_flight',
      })
      await rotation.promise
    })
    await flushPromises()

    expect(container.textContent).toContain('Nightly CI Rotated')
    expect(container.textContent).toContain('account refresh failed')
    expect(findButton(container, 'Retry accounts')).toBeDefined()
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
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, q: 'bob' })
    expect(tablePrimaryTexts(container)).toEqual(['Bob Viewer'])

    await act(async () => {
      changeInput(searchInput, '')
    })
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0 })
    const statusSelect = selectByLabelText(container, 'Status')
    expect(statusSelect).toBeDefined()
    if (!statusSelect) return
    await act(async () => {
      changeSelect(statusSelect, 'disabled')
    })
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, active: 'false' })
    expect(tablePrimaryTexts(container)).toEqual(['Bob Viewer'])
    await act(async () => {
      changeSelect(statusSelect, '')
    })
    await flushPromises()
    const roleSelect = selectByLabelText(container, 'Role')
    expect(roleSelect).toBeDefined()
    if (!roleSelect) return
    await act(async () => {
      changeSelect(roleSelect, 'editor')
    })
    await flushPromises()
    expect(apiMock.listAuthUsers).toHaveBeenLastCalledWith({ limit: 50, offset: 0, role: 'editor' })
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
      aliceButton.focus()
      aliceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const inspector = container.querySelector('aside[aria-label="Selected user details"]') as HTMLElement | null
    expect(inspector?.textContent).toContain('User Details')
    expect(document.activeElement).toBe(inspector?.querySelector('h3'))
    expect(aliceButton.closest('td')?.hasAttribute('aria-label')).toBe(false)
    await act(async () => inspector?.querySelector<HTMLButtonElement>('[aria-label="Collapse user details"]')?.click())
    expect(container.querySelector('[aria-label="Expand user details"]')).not.toBeNull()
    await act(async () => aliceButton.click())
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

  it('reconciles a partially committed binding edit before a safe retry', async () => {
    const replacementBinding = {
      id: 'binding-replacement',
      user_id: 'user-1',
      scope_type: 'project',
      scope_id: 'project-2',
      role: 'viewer',
      created_at: '2026-05-19T00:00:00Z',
      updated_at: '2026-05-19T00:00:00Z',
    }
    apiMock.createAuthUserBinding
      .mockRejectedValueOnce(new Error('replacement failed'))
      .mockResolvedValueOnce(replacementBinding)

    await act(async () => {
      root.render(<AccessPage projects={projects} domains={domains} activeDomain={domains[0]} activeProject={projects[0]} canManageUserLifecycle={false} canManageUserBindings canViewUserActivity={false} canManageServiceAccounts={false} />)
    })
    await flushPromises()

    const editBinding = async () => {
      const management = container.querySelector('.access-user-management') as HTMLElement | null
      expect(management).not.toBeNull()
      if (!management) return
      await clickScopedButton(management, 'Edit')
      const editor = container.querySelector('.access-user-management') as HTMLElement | null
      expect(editor).not.toBeNull()
      if (!editor) return
      const existingRemove = findButton(editor, 'Remove')
      if (existingRemove) await clickScopedButton(editor, 'Remove')
      await clickScopedButton(editor, 'Add Project')
      const projectRow = Array.from(editor.querySelectorAll<HTMLElement>('.access-binding-draft-row')).find(row => {
        const scopeSelect = row.querySelector('select') as HTMLSelectElement | null
        return scopeSelect?.value === 'project'
      })
      const projectSelect = projectRow?.querySelectorAll<HTMLSelectElement>('select')[1]
      expect(projectSelect).toBeDefined()
      if (!projectSelect) return
      changeSelect(projectSelect, 'project-2')
      await clickScopedButton(editor, 'Save Changes')
      await flushPromises()
    }

    await editBinding()

    expect(apiMock.deleteAuthUserBinding).not.toHaveBeenCalled()
    expect(apiMock.createAuthUserBinding).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Some changes were already applied; current access was reconciled before retrying.')

    const retryEditor = container.querySelector('.access-user-management') as HTMLElement | null
    expect(retryEditor).not.toBeNull()
    if (!retryEditor) return
    await clickScopedButton(retryEditor, 'Save Changes')
    await flushPromises()

    expect(apiMock.deleteAuthUserBinding).toHaveBeenCalledTimes(1)
    expect(apiMock.createAuthUserBinding).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('Some changes were already applied; current access was reconciled before retrying.')
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

    vi.mocked(downloadBlob).mockResolvedValueOnce('cancelled')
    await clickButton(container, 'Export all matching events CSV')
    await flushPromises()
    expect(apiMock.downloadAuthUserActivity).toHaveBeenCalledWith(expect.objectContaining({
      actor_subject: 'alice',
      action: 'auth.user.create',
    }))
    expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^apexload-user-activity-\d{4}-\d{2}-\d{2}\.csv$/))
    expect(container.textContent).not.toContain('Activity export downloaded.')

    vi.mocked(downloadBlob).mockResolvedValueOnce('saved')
    await clickButton(container, 'Export all matching events CSV')
    await flushPromises()
    expect(container.textContent).toContain('Activity export downloaded.')

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
    await flushPromises()
    expect(container.textContent).toContain('alice')
    await clickSortHeader(container, 'User')
    const usageRows = Array.from(container.querySelectorAll('.access-usage-table tbody tr')).map(row => row.textContent || '')
    expect(usageRows[0]).toContain('alice')
    await clickSortHeader(container, 'VUser seconds')
    const sortedUsageRows = Array.from(container.querySelectorAll('.access-usage-table tbody tr')).map(row => row.textContent || '')
    expect(sortedUsageRows[0]).toContain('alice')
  })
})
