// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ApplicationRepositoryPage from './ApplicationRepositoryPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const repositoryApiMock = {
  listProjectReleases: vi.fn(),
  createRepositoryCategory: vi.fn(),
  createProjectRelease: vi.fn(),
  archiveRelease: vi.fn(),
  deleteRelease: vi.fn(),
}

vi.mock('../hooks/api/useRepositoryApi', () => ({
  useRepositoryApi: () => repositoryApiMock,
}))

const projectApiMock = {
  deleteProject: vi.fn(),
}

vi.mock('../hooks/api/useProjectApi', () => ({
  useProjectApi: () => projectApiMock,
}))

const scriptApiMock = {
  listScripts: vi.fn(),
  listScenarios: vi.fn(),
  listDataPools: vi.fn(),
  getDataPool: vi.fn(),
  updateDataPool: vi.fn(),
  deleteScript: vi.fn(),
  deleteScenario: vi.fn(),
  deleteDataPool: vi.fn(),
}

vi.mock('../hooks/api/useScriptApi', () => ({
  useScriptApi: () => scriptApiMock,
}))

const testApiMock = {
  listTests: vi.fn(),
  deleteTest: vi.fn(),
}

vi.mock('../hooks/api/useTestApi', () => ({
  useTestApi: () => testApiMock,
}))

const domain = { id: 'domain-1', name: 'Retail', created_at: '', updated_at: '' }
const repository = { id: 'repo-1', domain_id: domain.id, name: 'Application Repository', created_at: '', updated_at: '' }
const category = { id: 'category-1', domain_id: domain.id, repository_id: repository.id, name: 'Checkout', created_at: '', updated_at: '' }
const project = { id: 'project-1', domain_id: domain.id, category_id: category.id, name: 'Checkout API', created_at: '', updated_at: '' }
const application = { id: 'app-1', project_id: project.id, name: 'ACC', created_at: '', updated_at: '' }

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

function findButton(container: HTMLElement, label: string, exact = true): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => {
    const text = button.textContent?.trim() || ''
    return exact ? text === label : text.includes(label)
  }) as HTMLButtonElement | undefined
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function findCheckbox(container: HTMLElement, label: string): HTMLInputElement | undefined {
  return Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => input.getAttribute('aria-label') === label) as HTMLInputElement | undefined
}

describe('ApplicationRepositoryPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    repositoryApiMock.listProjectReleases.mockResolvedValue({ releases: [{ id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }], count: 1 })
    repositoryApiMock.createRepositoryCategory.mockResolvedValue({ id: 'category-2', domain_id: domain.id, repository_id: repository.id, name: 'Search', created_at: '', updated_at: '' })
    repositoryApiMock.createProjectRelease.mockResolvedValue({ id: 'release-2', application_id: 'app-1', project_id: project.id, name: '2026.06', created_at: '', updated_at: '' })
    repositoryApiMock.archiveRelease.mockResolvedValue({ id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', archived_at: '2026-05-10T00:00:00Z', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' })
    repositoryApiMock.deleteRelease.mockResolvedValue({ deleted: 'release-1' })
    projectApiMock.deleteProject.mockResolvedValue({ deleted: project.id })
    scriptApiMock.listScripts.mockResolvedValue({
      scripts: [{
        id: 'script-1',
        name: 'Checkout Smoke',
        protocol: 'http',
        version: '1.0.0',
        project_id: project.id,
        created_at: '2026-05-03T00:00:00Z',
        scenario_count: 1,
        associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
      }],
      count: 1,
    })
    scriptApiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Checkout Peak',
        duration: '30m',
        scripts: [{ order: 1, script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 10 }],
        created_at: '2026-05-04T00:00:00Z',
        updated_at: '2026-05-05T00:00:00Z',
        script_count: 1,
        result_count: 1,
        associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
      }],
      count: 1,
    })
    scriptApiMock.listDataPools.mockResolvedValue({
      data_pools: [{
        id: 'pool-1',
        name: 'Checkout Users',
        project_id: project.id,
        schema: { username: 'string' },
        created_at: '2026-05-06T00:00:00Z',
        updated_at: '2026-05-07T00:00:00Z',
      }],
      count: 1,
    })
    scriptApiMock.getDataPool.mockResolvedValue({
      data_pool: {
        id: 'pool-1',
        name: 'Checkout Users',
        description: 'Seeded user pool',
        project_id: project.id,
        schema: { username: 'string' },
        created_at: '2026-05-06T00:00:00Z',
        updated_at: '2026-05-07T00:00:00Z',
      },
      rows: [],
    })
    scriptApiMock.updateDataPool.mockResolvedValue({
      id: 'pool-1',
      name: 'Checkout Users',
      description: 'Seeded user pool',
      project_id: project.id,
      schema: { username: 'string' },
      archived_at: '2026-05-10T00:00:00Z',
      created_at: '2026-05-06T00:00:00Z',
      updated_at: '2026-05-07T00:00:00Z',
    })
    scriptApiMock.deleteScript.mockResolvedValue({ deleted: 'script-1' })
    scriptApiMock.deleteScenario.mockResolvedValue({ deleted: 'scenario-1' })
    scriptApiMock.deleteDataPool.mockResolvedValue({ deleted: 'pool-1' })
    testApiMock.listTests.mockResolvedValue({
      tests: [{
        id: 'test-1',
        name: 'Checkout Run',
        status: 'COMPLETE',
        application_id: 'app-1',
        project_id: project.id,
        release_id: 'release-1',
        scenario_id: 'scenario-1',
        groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 10 }],
        total_vusers: 10,
        duration: '30m',
        created_at: '2026-05-08T00:00:00Z',
      }],
      count: 1,
    })
    testApiMock.deleteTest.mockResolvedValue({ deleted: 'test-1' })
  })

  afterEach(() => {
    vi.clearAllMocks()
    act(() => root.unmount())
    container.remove()
  })

  it('renders catalog rows, filters by tab and search, and creates releases from Structure', async () => {
    const onOpenScript = vi.fn()
    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
          activeApplication={application}
          applicationRepository={repository}
          categories={[category]}
          projects={[project]}
          canManageRepository
          canManageProjects
          canManageReleases
          onDomainChange={vi.fn()}
          onRepositoryRefresh={vi.fn()}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
          onOpenScript={onOpenScript}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('Application Repository')
    expect(container.textContent).toContain('Projects')
    expect(container.textContent).toContain('Checkout API')
    expect(container.textContent).toContain('2026.05')
    expect(container.textContent).toContain('Checkout Smoke')
    expect(container.textContent).toContain('Checkout Peak')
    expect(container.textContent).toContain('Checkout Run')
    expect(container.textContent).toContain('Checkout Users')

    const filterSelects = Array.from(container.querySelectorAll('.repository-filter-row select')) as HTMLSelectElement[]
    expect(filterSelects.length).toBeGreaterThanOrEqual(5)
    await act(async () => {
      setSelectValue(filterSelects[1], 'release-1')
    })
    expect(container.textContent).toContain('Checkout Smoke')
    expect(container.textContent).toContain('Checkout Run')
    expect(container.textContent).not.toContain('Checkout Users')

    const scriptsTab = findButton(container, 'Scripts', false)
    await act(async () => {
      scriptsTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Checkout Smoke')
    expect(container.textContent).not.toContain('Checkout Peak')

    const editScript = findButton(container, 'Edit')
    await act(async () => {
      editScript?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onOpenScript).toHaveBeenCalledWith('script-1')

    const searchInput = container.querySelector('.repository-search input') as HTMLInputElement | null
    expect(searchInput).toBeDefined()
    if (!searchInput) return
    await act(async () => {
      setInputValue(searchInput, 'missing asset')
    })
    expect(container.textContent).toContain('No assets match the current filters.')

    await act(async () => {
      setSelectValue(filterSelects[4], 'custom')
    })
    expect(container.textContent).toContain('Activity From')
    expect(container.textContent).toContain('Activity To')

    const structureTab = findButton(container, 'Structure', false)
    await act(async () => {
      structureTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const releaseInput = Array.from(container.querySelectorAll('input')).find(input => input.getAttribute('placeholder') === 'New release name') as HTMLInputElement | undefined
    expect(releaseInput).toBeDefined()
    if (!releaseInput) return
    await act(async () => {
      setInputValue(releaseInput, '2026.06')
    })
    const addRelease = findButton(container, 'Add Release')
    expect(addRelease?.disabled).toBe(false)
    await act(async () => {
      addRelease?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(repositoryApiMock.createProjectRelease).toHaveBeenCalledWith(project.id, { name: '2026.06', application_id: application.id })
    expect(container.textContent).toContain('Release created.')
  })

  it('keeps repository test Open on archive navigation and adds Run Dashboard', async () => {
    const onOpenTest = vi.fn()
    const onOpenCommandCenter = vi.fn()

    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
          activeApplication={application}
          applicationRepository={repository}
          categories={[category]}
          projects={[project]}
          canManageRepository
          canManageProjects
          canManageReleases
          onDomainChange={vi.fn()}
          onRepositoryRefresh={vi.fn()}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
          onOpenTest={onOpenTest}
          onOpenCommandCenter={onOpenCommandCenter}
        />,
      )
    })
    await flushPromises()

    const row = Array.from(container.querySelectorAll('tbody tr')).find(candidate => candidate.textContent?.includes('Checkout Run'))
    expect(row).toBeDefined()
    const buttons = Array.from(row?.querySelectorAll('button') ?? []) as HTMLButtonElement[]
    const openButton = buttons.find(button => button.textContent?.trim() === 'Open')
    const runDashboardButton = buttons.find(button => button.textContent?.trim() === 'Run Dashboard')
    expect(openButton).toBeDefined()
    expect(runDashboardButton).toBeDefined()

    await act(async () => {
      openButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onOpenTest).toHaveBeenCalledWith('test-1')
    expect(onOpenCommandCenter).not.toHaveBeenCalled()

    await act(async () => {
      runDashboardButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onOpenCommandCenter).toHaveBeenCalledWith('test-1')
    expect(onOpenTest).toHaveBeenCalledTimes(1)
  })

  it('scopes scripts, scenarios, tests, and releases to the selected application', async () => {
    repositoryApiMock.listProjectReleases.mockResolvedValueOnce({
      releases: [
        { id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' },
        { id: 'release-foreign', application_id: 'app-2', project_id: project.id, name: 'Foreign Release', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' },
      ],
      count: 2,
    })
    scriptApiMock.listScripts.mockResolvedValueOnce({
      scripts: [
        {
          id: 'script-1',
          name: 'Checkout Smoke',
          protocol: 'http',
          version: '1.0.0',
          project_id: project.id,
          created_at: '2026-05-03T00:00:00Z',
          scenario_count: 1,
          associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
        },
        {
          id: 'script-foreign',
          name: 'Billing Smoke',
          protocol: 'http',
          version: '1.0.0',
          project_id: project.id,
          created_at: '2026-05-03T00:00:00Z',
          scenario_count: 1,
          associated_releases: [{ id: 'release-foreign', application_id: 'app-2', name: 'Foreign Release' }],
        },
      ],
      count: 2,
    })
    scriptApiMock.listScenarios.mockResolvedValueOnce({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Peak',
          duration: '30m',
          scripts: [{ order: 1, script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 10 }],
          created_at: '2026-05-04T00:00:00Z',
          updated_at: '2026-05-05T00:00:00Z',
          script_count: 1,
          associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
        },
        {
          id: 'scenario-foreign',
          name: 'Billing Peak',
          duration: '30m',
          scripts: [{ order: 1, script_id: 'script-foreign', script_name: 'Billing Smoke', protocol: 'http', vusers: 10 }],
          created_at: '2026-05-04T00:00:00Z',
          updated_at: '2026-05-05T00:00:00Z',
          script_count: 1,
          associated_releases: [{ id: 'release-foreign', application_id: 'app-2', name: 'Foreign Release' }],
        },
      ],
      count: 2,
    })
    testApiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'test-1',
          name: 'Checkout Run',
          status: 'COMPLETE',
          application_id: 'app-1',
          project_id: project.id,
          release_id: 'release-1',
          scenario_id: 'scenario-1',
          groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 10 }],
          total_vusers: 10,
          duration: '30m',
          created_at: '2026-05-08T00:00:00Z',
        },
        {
          id: 'test-foreign',
          name: 'Billing Run',
          status: 'COMPLETE',
          application_id: 'app-2',
          project_id: project.id,
          release_id: 'release-foreign',
          scenario_id: 'scenario-foreign',
          groups: [{ name: 'Billing', script_id: 'script-foreign', script_name: 'Billing Smoke', protocol: 'http', vusers: 10 }],
          total_vusers: 10,
          duration: '30m',
          created_at: '2026-05-08T00:00:00Z',
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
          activeApplication={application}
          applicationRepository={repository}
          categories={[category]}
          projects={[project]}
          canManageRepository
          canManageProjects
          canManageReleases
          onDomainChange={vi.fn()}
          onRepositoryRefresh={vi.fn()}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('for ACC')
    expect(container.textContent).toContain('Checkout Smoke')
    expect(container.textContent).toContain('Checkout Peak')
    expect(container.textContent).toContain('Checkout Run')
    expect(container.textContent).toContain('2026.05')
    expect(container.textContent).not.toContain('Billing Smoke')
    expect(container.textContent).not.toContain('Billing Peak')
    expect(container.textContent).not.toContain('Billing Run')
    expect(container.textContent).not.toContain('Foreign Release')
  })

  it('shows asset load failures as non-blocking catalog warnings', async () => {
    scriptApiMock.listScripts.mockRejectedValueOnce(new Error('Forbidden'))
    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
          activeApplication={application}
          applicationRepository={repository}
          categories={[category]}
          projects={[project]}
          canManageRepository={false}
          canManageProjects={false}
          canManageReleases={false}
          onDomainChange={vi.fn()}
          onRepositoryRefresh={vi.fn()}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('Scripts: unable to load 1 project (Forbidden)')
    expect(container.textContent).toContain('Checkout API')
    expect(container.textContent).toContain('2026.05')
  })

  it('selects multiple repository items and bulk archives or deletes eligible rows', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
          activeApplication={application}
          applicationRepository={repository}
          categories={[category]}
          projects={[project]}
          canManageRepository
          canManageProjects
          canManageReleases
          onDomainChange={vi.fn()}
          onRepositoryRefresh={vi.fn()}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
          onOpenScript={vi.fn()}
          onOpenTest={vi.fn()}
          onCreateTestFromScenario={vi.fn()}
        />,
      )
    })
    await flushPromises()

    const releaseCheckbox = findCheckbox(container, 'Select 2026.05')
    const dataPoolCheckbox = findCheckbox(container, 'Select Checkout Users')
    expect(releaseCheckbox).toBeDefined()
    expect(dataPoolCheckbox).toBeDefined()

    await act(async () => {
      releaseCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      dataPoolCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('2 items selected')
    expect(container.textContent).toContain('Archive selected (2)')

    await act(async () => {
      findButton(container, 'Archive selected (2)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalledWith('Archive 2 selected items?')
    expect(repositoryApiMock.archiveRelease).toHaveBeenCalledWith('release-1')
    expect(scriptApiMock.getDataPool).toHaveBeenCalledWith('pool-1')
    expect(scriptApiMock.updateDataPool).toHaveBeenCalledWith('pool-1', expect.objectContaining({
      name: 'Checkout Users',
      description: 'Seeded user pool',
      project_id: project.id,
      schema: { username: 'string' },
      archived_at: expect.any(String),
    }))
    expect(container.textContent).toContain('Archived 2 selected items.')

    await act(async () => {
      findCheckbox(container, 'Select Checkout Smoke')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      findCheckbox(container, 'Select Checkout Run')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Delete selected (2)')
    await act(async () => {
      findButton(container, 'Delete selected (2)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalledWith('Delete 2 selected items? This cannot be undone.')
    expect(testApiMock.deleteTest).toHaveBeenCalledWith('test-1')
    expect(scriptApiMock.deleteScript).toHaveBeenCalledWith('script-1')
    expect(container.textContent).toContain('Deleted 2 selected items.')

    confirmSpy.mockRestore()
  })
})
