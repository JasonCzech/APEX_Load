// @vitest-environment jsdom

import { act, useCallback, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Domain } from '../types'
import { useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { ProjectProvider } from '../hooks/ProjectProvider'
import { useProject } from '../hooks/useProject'
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
	listProjects: vi.fn(),
	createProject: vi.fn(),
	updateProject: vi.fn(),
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
const paymentsDomain = { id: 'domain-2', name: 'Payments', created_at: '', updated_at: '' }
const paymentsRepository = { id: 'repo-2', domain_id: paymentsDomain.id, name: 'Application Repository', created_at: '', updated_at: '' }
const paymentsCategory = { id: 'category-2', domain_id: paymentsDomain.id, repository_id: paymentsRepository.id, name: 'Payments', created_at: '', updated_at: '' }
const paymentsProject = { id: 'project-2', domain_id: paymentsDomain.id, category_id: paymentsCategory.id, name: 'Payments API', created_at: '', updated_at: '' }

function GuardedRepositoryHarness({
  onDomainChange,
  onRepositoryRefresh,
}: {
  onDomainChange: (next: Domain) => void
  onRepositoryRefresh: () => Promise<void>
}) {
  const [selectedDomain, setSelectedDomain] = useState<Domain>(domain)
  const { runGuarded } = useNavigationGuardRunner()
  const handleDomainChange = useCallback((next: Domain) => {
    runGuarded(() => {
      setSelectedDomain(next)
      onDomainChange(next)
    })
  }, [onDomainChange, runGuarded])
  const inPaymentsDomain = selectedDomain.id === paymentsDomain.id

  return (
    <ApplicationRepositoryPage
      domains={[domain, paymentsDomain]}
      activeDomain={selectedDomain}
      activeApplication={inPaymentsDomain ? null : application}
      applicationRepository={inPaymentsDomain ? paymentsRepository : repository}
      categories={inPaymentsDomain ? [paymentsCategory] : [category]}
      projects={[project, paymentsProject]}
      canManageRepository
      canManageProjects
      canManageReleases
      onDomainChange={handleDomainChange}
      onRepositoryRefresh={onRepositoryRefresh}
      onProjectsRefresh={vi.fn()}
      onProjectCreated={vi.fn()}
    />
  )
}

function ProjectProviderRepositoryHarness() {
	const { projects, refreshProjects, createAndSelectProject, deleteProject } = useProject()
	return (
		<>
			<button type="button" onClick={() => { void refreshProjects().catch(() => undefined) }}>Refresh project context</button>
			<ApplicationRepositoryPage
				domains={[domain]}
				activeDomain={domain}
				activeApplication={application}
				applicationRepository={repository}
				categories={[category]}
				projects={projects}
				canManageRepository
				canManageProjects
				canManageReleases
				onDomainChange={vi.fn()}
				onRepositoryRefresh={vi.fn()}
				onProjectsRefresh={refreshProjects}
				onProjectCreated={createAndSelectProject}
				onProjectDelete={deleteProject}
			/>
		</>
	)
}

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
    window.localStorage.removeItem('apexload.repository.tab')
    repositoryApiMock.listProjectReleases.mockResolvedValue({ releases: [{ id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }], count: 1 })
    repositoryApiMock.createRepositoryCategory.mockResolvedValue({ id: 'category-2', domain_id: domain.id, repository_id: repository.id, name: 'Search', created_at: '', updated_at: '' })
    repositoryApiMock.createProjectRelease.mockResolvedValue({ id: 'release-2', application_id: 'app-1', project_id: project.id, name: '2026.06', created_at: '', updated_at: '' })
    repositoryApiMock.archiveRelease.mockResolvedValue({ id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', archived_at: '2026-05-10T00:00:00Z', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' })
    repositoryApiMock.deleteRelease.mockResolvedValue({ deleted: 'release-1' })
    projectApiMock.deleteProject.mockResolvedValue({ deleted: project.id })
		projectApiMock.listProjects.mockResolvedValue({ projects: [project], count: 1 })
		projectApiMock.createProject.mockResolvedValue(project)
		projectApiMock.updateProject.mockResolvedValue(project)
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
    window.localStorage.removeItem('apexload.repository.tab')
  })

  it('renders catalog rows, filters by tab and search, and creates releases from Structure', async () => {
    const onOpenScript = vi.fn()
    const onCreateTestFromScenario = vi.fn()
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
          onCreateTestFromScenario={onCreateTestFromScenario}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('Application Repository')
    expect(container.querySelector('.repository-summary-band')).toBeNull()
    expect(container.textContent).toContain('Projects')
    expect(container.textContent).toContain('Checkout API')
    expect(container.textContent).toContain('2026.05')
    expect(container.textContent).toContain('Checkout Smoke')
    expect(container.textContent).toContain('Checkout Peak')
    expect(container.textContent).toContain('Checkout Run')
    expect(container.textContent).toContain('Checkout Users')
    const assetSortButton = findButton(container, 'Asset', false)
    const activitySortButton = findButton(container, 'Activity', false)
    expect(assetSortButton?.closest('th')?.getAttribute('aria-sort')).toBe('none')
    expect(activitySortButton?.closest('th')?.getAttribute('aria-sort')).toBe('descending')
    await act(async () => {
      assetSortButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(assetSortButton?.closest('th')?.getAttribute('aria-sort')).toBe('ascending')

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
    expect(onOpenScript).toHaveBeenCalledWith('script-1', project.id)

    const scenariosTab = findButton(container, 'Scenarios', false)
    await act(async () => {
      scenariosTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const newTest = findButton(container, 'New Test')
    await act(async () => {
      newTest?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onCreateTestFromScenario).toHaveBeenCalledWith('scenario-1', project.id)

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

  it('drains every bounded page of a project test catalog', async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      id: `test-page-1-${index}`,
      name: `Paged Run ${index}`,
      status: 'COMPLETE',
      application_id: application.id,
      project_id: project.id,
      release_id: 'release-1',
      scenario_id: 'scenario-1',
      groups: [],
      total_vusers: 1,
      duration: '1m',
      created_at: '2026-05-08T00:00:00Z',
    }))
    testApiMock.listTests.mockImplementation(async (params: { project_id?: string; limit?: number; offset?: number }) => {
      if ((params.offset || 0) === 0) {
        return { tests: firstPage, count: firstPage.length, total: 101, limit: params.limit, offset: 0 }
      }
      return {
        tests: [{
          id: 'test-page-2',
          name: 'Late Page Run',
          status: 'COMPLETE',
          application_id: application.id,
          project_id: project.id,
          release_id: 'release-1',
          scenario_id: 'scenario-1',
          groups: [],
          total_vusers: 1,
          duration: '1m',
          created_at: '2026-05-09T00:00:00Z',
        }],
        count: 1,
        total: 101,
        limit: params.limit,
        offset: params.offset,
      }
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

    expect(testApiMock.listTests).toHaveBeenNthCalledWith(1, { project_id: project.id, limit: 100, offset: 0 })
    expect(testApiMock.listTests).toHaveBeenNthCalledWith(2, { project_id: project.id, limit: 100, offset: 100 })
    expect(container.textContent).toContain('Late Page Run')
    expect(container.querySelector('.repository-asset-table-virtualized')?.getAttribute('data-table-pagination')).toBe('off')
    expect(container.querySelectorAll('.repository-asset-table tbody tr:not([aria-hidden="true"])').length).toBeLessThan(101)
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

    expect(onOpenTest).toHaveBeenCalledWith('test-1', project.id)
    expect(onOpenCommandCenter).not.toHaveBeenCalled()

    await act(async () => {
      runDashboardButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onOpenCommandCenter).toHaveBeenCalledWith('test-1', project.id)
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
    expect(scriptApiMock.updateDataPool).toHaveBeenCalledWith('pool-1', {
      name: 'Checkout Users',
      description: 'Seeded user pool',
      schema: { username: 'string' },
      archived_at: expect.any(String),
      updated_at: '2026-05-07T00:00:00Z',
    })
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

    projectApiMock.deleteProject.mockResolvedValueOnce({
      deleted: project.id,
      external_secret_cleanup_required: true,
      external_secret_cleanup_names: ['PAYMENTS_TOKEN'],
      external_secret_cleanup_providers: ['azure_key_vault'],
      secret_provider: 'azure_key_vault',
    })
    await act(async () => {
      findCheckbox(container, 'Select Checkout API')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      findButton(container, 'Delete selected (1)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(projectApiMock.deleteProject).toHaveBeenCalledWith(project.id)
    expect(container.textContent).toContain('External project secrets retained for administrator cleanup: PAYMENTS_TOKEN (azure_key_vault).')

    projectApiMock.deleteProject.mockResolvedValueOnce({
      deleted: project.id,
      external_secret_cleanup_required: true,
      external_secret_cleanup_names: [],
      external_secret_cleanup_providers: ['azure_key_vault'],
      external_secret_cleanup_prefix: 'apexload/projects/project-1/',
      secret_provider: 'azure_key_vault',
    })
    await act(async () => {
      findCheckbox(container, 'Select Checkout API')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      findButton(container, 'Delete selected (1)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('External project secrets retained for administrator cleanup under prefix “apexload/projects/project-1/” (azure_key_vault).')

    confirmSpy.mockRestore()
  })

  it('prunes hidden selections and limits destructive actions to visible tab rows', async () => {
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
        />,
      )
    })
    await flushPromises()

    await act(async () => {
      findCheckbox(container, 'Select Checkout Smoke')?.click()
      findCheckbox(container, 'Select Checkout Run')?.click()
    })
    expect(container.textContent).toContain('2 items selected')

    await act(async () => findButton(container, 'Scripts', false)?.click())
    await flushPromises()
    expect(container.textContent).toContain('1 item selected')
    expect(container.textContent).toContain('Delete selected (1)')

    await act(async () => findButton(container, 'All', false)?.click())
    await flushPromises()
    expect(findCheckbox(container, 'Select Checkout Smoke')?.checked).toBe(true)
    expect(findCheckbox(container, 'Select Checkout Run')?.checked).toBe(false)

    await act(async () => findButton(container, 'Delete selected (1)')?.click())
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalledWith('Delete 1 selected item? This cannot be undone.')
    expect(scriptApiMock.deleteScript).toHaveBeenCalledWith('script-1')
    expect(testApiMock.deleteTest).not.toHaveBeenCalled()
    confirmSpy.mockRestore()
  })

  it('awaits every bulk archive and preserves failed selections after partial success', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    let resolveReleaseArchive: ((value: unknown) => void) | undefined
    repositoryApiMock.archiveRelease.mockImplementationOnce(() => new Promise(resolve => {
      resolveReleaseArchive = resolve
    }))
    scriptApiMock.updateDataPool.mockRejectedValueOnce(new Error('pool archive locked'))

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
    const initialReleaseLoads = repositoryApiMock.listProjectReleases.mock.calls.length

    await act(async () => {
      findCheckbox(container, 'Select 2026.05')?.click()
      findCheckbox(container, 'Select Checkout Users')?.click()
    })
    await act(async () => {
      findButton(container, 'Archive selected (2)')?.click()
    })
    await flushPromises()

    expect(repositoryApiMock.archiveRelease).toHaveBeenCalledWith('release-1')
    expect(scriptApiMock.updateDataPool).toHaveBeenCalled()
    expect(repositoryApiMock.listProjectReleases).toHaveBeenCalledTimes(initialReleaseLoads)
    expect(findButton(container, 'Archive selected (2)')?.disabled).toBe(true)
    expect(container.textContent).not.toContain('Archived 1 selected item')
    expect(container.textContent).not.toContain('pool archive locked')

    await act(async () => {
      resolveReleaseArchive?.({
        id: 'release-1',
        application_id: application.id,
        project_id: project.id,
        name: '2026.05',
        archived_at: '2026-05-10T00:00:00Z',
      })
    })
    await flushPromises()

    expect(repositoryApiMock.listProjectReleases).toHaveBeenCalledTimes(initialReleaseLoads + 1)
    expect(container.textContent).toContain('Archived 1 selected item; 1 item unchanged.')
    expect(container.textContent).toContain('Unable to archive 1 selected item; 1 failed item remains selected: Checkout Users: pool archive locked.')
    expect(findCheckbox(container, 'Select 2026.05')?.checked).toBe(false)
    expect(findCheckbox(container, 'Select Checkout Users')?.checked).toBe(true)
    expect(container.textContent).toContain('1 item selected')
    expect(container.textContent).toContain('Archive selected (1)')

    confirmSpy.mockRestore()
  })

  it('refreshes after an all-failed bulk archive and keeps every failed row selected', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    repositoryApiMock.archiveRelease.mockRejectedValueOnce(new Error('release archive denied'))
    scriptApiMock.updateDataPool.mockRejectedValueOnce(new Error('pool archive denied'))

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
    const initialReleaseLoads = repositoryApiMock.listProjectReleases.mock.calls.length

    await act(async () => {
      findCheckbox(container, 'Select 2026.05')?.click()
      findCheckbox(container, 'Select Checkout Users')?.click()
    })
    await act(async () => {
      findButton(container, 'Archive selected (2)')?.click()
    })
    await flushPromises()

    expect(repositoryApiMock.listProjectReleases).toHaveBeenCalledTimes(initialReleaseLoads + 1)
    expect(container.textContent).not.toContain('Archived 1 selected item')
    expect(container.textContent).not.toContain('Archived 2 selected items')
    expect(container.textContent).toContain('Unable to archive 2 selected items; 2 failed items remain selected')
    expect(container.textContent).toContain('2026.05: release archive denied')
    expect(container.textContent).toContain('Checkout Users: pool archive denied')
    expect(findCheckbox(container, 'Select 2026.05')?.checked).toBe(true)
    expect(findCheckbox(container, 'Select Checkout Users')?.checked).toBe(true)
    expect(container.textContent).toContain('2 items selected')

    confirmSpy.mockRestore()
  })

  it('preserves completed deletion state and external-secret cleanup guidance when a later delete and refresh fail', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const onProjectsRefresh = vi.fn().mockRejectedValue(new Error('refresh offline'))
    projectApiMock.deleteProject.mockRejectedValueOnce(Object.assign(
      new Error('Project deleted, but external cleanup metadata could not be finalized.'),
      {
        payload: {
          deleted: project.id,
          external_secret_cleanup_required: true,
          external_secret_cleanup_names: ['PAYMENTS_TOKEN'],
          external_secret_cleanup_providers: ['azure_key_vault'],
          external_secret_cleanup_prefix: 'apexload/projects/project-1/',
          secret_provider: 'azure_key_vault',
        },
      },
    ))

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
          onProjectsRefresh={onProjectsRefresh}
          onProjectCreated={vi.fn()}
        />,
      )
    })
    await flushPromises()

    const releaseCheckbox = findCheckbox(container, 'Select 2026.05')
    const projectCheckbox = findCheckbox(container, 'Select Checkout API')
    await act(async () => {
      releaseCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      projectCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await act(async () => {
      findButton(container, 'Delete selected (2)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(repositoryApiMock.deleteRelease).toHaveBeenCalledWith('release-1')
    expect(projectApiMock.deleteProject).toHaveBeenCalledWith(project.id)
    expect(onProjectsRefresh).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Deleted 2 selected items.')
    expect(container.textContent).toContain('External project secrets retained for administrator cleanup: PAYMENTS_TOKEN (azure_key_vault).')
    expect(container.textContent).toContain('The repository refresh also failed; reload before continuing.')
    expect(releaseCheckbox?.checked).toBe(false)
    expect(projectCheckbox?.checked).toBe(false)
    expect(container.textContent).not.toContain('2 items selected')

    confirmSpy.mockRestore()
  })

  it('keeps external-secret prefix guidance visible when post-delete refresh fails', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const onProjectsRefresh = vi.fn().mockRejectedValue(new Error('refresh offline'))
    projectApiMock.deleteProject.mockResolvedValueOnce({
      deleted: project.id,
      external_secret_cleanup_required: true,
      external_secret_cleanup_names: [],
      external_secret_cleanup_providers: ['azure_key_vault'],
      external_secret_cleanup_prefix: 'apexload/projects/project-1/',
      secret_provider: 'azure_key_vault',
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
          onProjectsRefresh={onProjectsRefresh}
          onProjectCreated={vi.fn()}
        />,
      )
    })
    await flushPromises()
    const projectCheckbox = findCheckbox(container, 'Select Checkout API')
    await act(async () => projectCheckbox?.click())
    await act(async () => findButton(container, 'Delete selected (1)')?.click())
    await flushPromises()

    expect(container.textContent).toContain('Deleted 1 selected item.')
    expect(container.textContent).toContain('External project secrets retained for administrator cleanup under prefix “apexload/projects/project-1/” (azure_key_vault).')
    expect(container.textContent).toContain('refresh offline')
    expect(projectCheckbox?.checked).toBe(false)

    confirmSpy.mockRestore()
  })

	it('removes a deleted project locally and tombstones it through failed and stale repository refreshes', async () => {
		const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
		await act(async () => {
			root.render(
				<ProjectProvider>
					<ProjectProviderRepositoryHarness />
				</ProjectProvider>,
			)
		})
		await flushPromises()
		await flushPromises()

		projectApiMock.listProjects.mockRejectedValueOnce(new Error('project reconciliation unavailable'))
		await act(async () => findCheckbox(container, 'Select Checkout API')?.click())
		await act(async () => findButton(container, 'Delete selected (1)')?.click())
		await flushPromises()

		expect(projectApiMock.deleteProject).toHaveBeenCalledWith(project.id)
		expect(container.textContent).not.toContain('Checkout API')
		expect(container.textContent).toContain('project reconciliation unavailable')

		projectApiMock.listProjects.mockResolvedValueOnce({ projects: [project], count: 1 })
		await act(async () => findButton(container, 'Refresh project context')?.click())
		await flushPromises()

		expect(container.textContent).not.toContain('Checkout API')
		confirmSpy.mockRestore()
	})

  it('does not carry a discarded category draft into a newly selected domain', async () => {
    const onDomainChange = vi.fn()
    const onRepositoryRefresh = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <GuardedRepositoryHarness
            onDomainChange={onDomainChange}
            onRepositoryRefresh={onRepositoryRefresh}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    await act(async () => findButton(container, 'Structure', false)?.click())
    const categoryInput = container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')
    const domainSelect = container.querySelector<HTMLSelectElement>('.repository-domain-select select')
    expect(categoryInput).toBeTruthy()
    expect(domainSelect).toBeTruthy()
    if (!categoryInput || !domainSelect) return

    await act(async () => setInputValue(categoryInput, 'Retail Orders'))
    await act(async () => setSelectValue(domainSelect, paymentsDomain.id))

    expect(container.textContent).toContain('Repository creation form has unsaved changes.')
    expect(onDomainChange).not.toHaveBeenCalled()
    expect(domainSelect.value).toBe(domain.id)

    await act(async () => findButton(container, 'Keep editing')?.click())
    expect(categoryInput.value).toBe('Retail Orders')
    expect(domainSelect.value).toBe(domain.id)

    await act(async () => setSelectValue(domainSelect, paymentsDomain.id))
    await act(async () => findButton(container, 'Discard changes')?.click())
    await flushPromises()

    const paymentsCategoryInput = container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')
    expect(domainSelect.value).toBe(paymentsDomain.id)
    expect(paymentsCategoryInput?.value).toBe('')
    expect(repositoryApiMock.createRepositoryCategory).not.toHaveBeenCalled()

    if (!paymentsCategoryInput) return
    await act(async () => setInputValue(paymentsCategoryInput, 'Payment Operations'))
    await act(async () => findButton(container, 'Add Category')?.click())
    await flushPromises()

    expect(repositoryApiMock.createRepositoryCategory).toHaveBeenCalledTimes(1)
    expect(repositoryApiMock.createRepositoryCategory).toHaveBeenCalledWith(
      paymentsDomain.id,
      expect.objectContaining({ name: 'Payment Operations' }),
    )
  })

  it('saves a category draft to its original domain before changing scope', async () => {
    const onDomainChange = vi.fn()
    const onRepositoryRefresh = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <GuardedRepositoryHarness
            onDomainChange={onDomainChange}
            onRepositoryRefresh={onRepositoryRefresh}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    await act(async () => findButton(container, 'Structure', false)?.click())
    const categoryInput = container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')
    const domainSelect = container.querySelector<HTMLSelectElement>('.repository-domain-select select')
    expect(categoryInput).toBeTruthy()
    expect(domainSelect).toBeTruthy()
    if (!categoryInput || !domainSelect) return

    await act(async () => setInputValue(categoryInput, 'Retail Orders'))
    await act(async () => setSelectValue(domainSelect, paymentsDomain.id))
    await act(async () => findButton(container, 'Save changes')?.click())
    await flushPromises()

    expect(repositoryApiMock.createRepositoryCategory).toHaveBeenCalledWith(
      domain.id,
      expect.objectContaining({ name: 'Retail Orders' }),
    )
    expect(onRepositoryRefresh).toHaveBeenCalledTimes(1)
    expect(onDomainChange).toHaveBeenCalledWith(paymentsDomain)
    expect(domainSelect.value).toBe(paymentsDomain.id)
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')?.value).toBe('')
  })

  it('commits category creation through the owner before a failed reconciliation read', async () => {
    const created = {
      id: 'category-created',
      domain_id: domain.id,
      repository_id: repository.id,
      name: 'Retail Orders',
      created_at: '2026-08-03T10:00:00Z',
      updated_at: '2026-08-03T10:00:00Z',
    }
    const onRepositoryCategoryCreate = vi.fn().mockResolvedValue(created)
    const onRepositoryRefresh = vi.fn().mockRejectedValue(new Error('category catalog unavailable'))
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
          onRepositoryRefresh={onRepositoryRefresh}
          onRepositoryCategoryCreate={onRepositoryCategoryCreate}
          onProjectsRefresh={vi.fn()}
          onProjectCreated={vi.fn()}
        />,
      )
    })
    await flushPromises()

    await act(async () => findButton(container, 'Structure', false)?.click())
    const categoryInput = container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')
    if (!categoryInput) return
    await act(async () => setInputValue(categoryInput, created.name))
    await act(async () => findButton(container, 'Add Category')?.click())
    await flushPromises()

    expect(onRepositoryCategoryCreate).toHaveBeenCalledWith(
      domain.id,
      expect.objectContaining({ name: created.name }),
    )
    expect(repositoryApiMock.createRepositoryCategory).not.toHaveBeenCalled()
    expect(onRepositoryRefresh).toHaveBeenCalledTimes(1)
    expect(categoryInput.value).toBe('')
    expect(container.textContent).toContain('The mutation succeeded—do not save it again.')
  })

  it('does not replay a committed category when guarded reconciliation fails', async () => {
    const onDomainChange = vi.fn()
    const onRepositoryRefresh = vi.fn()
      .mockRejectedValueOnce(new Error('repository list unavailable'))
      .mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <GuardedRepositoryHarness onDomainChange={onDomainChange} onRepositoryRefresh={onRepositoryRefresh} />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())
    const categoryInput = container.querySelector<HTMLInputElement>('input[placeholder="Payments, Claims, Search"]')
    const domainSelect = container.querySelector<HTMLSelectElement>('.repository-domain-select select')
    if (!categoryInput || !domainSelect) return

    await act(async () => setInputValue(categoryInput, 'Retail Orders'))
    await act(async () => setSelectValue(domainSelect, paymentsDomain.id))
    await act(async () => findButton(container, 'Save changes')?.click())
    await flushPromises()

    expect(repositoryApiMock.createRepositoryCategory).toHaveBeenCalledTimes(1)
    expect(onDomainChange).toHaveBeenCalledWith(paymentsDomain)
    expect(container.textContent).toContain('The mutation succeeded—do not save it again.')

    await act(async () => findButton(container, 'Retry repository synchronization')?.click())
    await flushPromises()
    expect(onRepositoryRefresh).toHaveBeenCalledTimes(2)
    expect(repositoryApiMock.createRepositoryCategory).toHaveBeenCalledTimes(1)
  })

  it('guards and discards project and release creation drafts together', async () => {
    const onDomainChange = vi.fn()
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <GuardedRepositoryHarness onDomainChange={onDomainChange} onRepositoryRefresh={vi.fn()} />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())
    const projectInput = container.querySelector<HTMLInputElement>('input[placeholder="New project name"]')
    const releaseInput = container.querySelector<HTMLInputElement>('input[placeholder="New release name"]')
    const domainSelect = container.querySelector<HTMLSelectElement>('.repository-domain-select select')
    if (!projectInput || !releaseInput || !domainSelect) return
    await act(async () => setInputValue(projectInput, 'Orders API'))
    await act(async () => setInputValue(releaseInput, '2026.08'))
    await act(async () => setSelectValue(domainSelect, paymentsDomain.id))

    expect(container.textContent).toContain('Repository creation form has unsaved changes.')
    expect(onDomainChange).not.toHaveBeenCalled()
    await act(async () => findButton(container, 'Discard changes')?.click())
    await flushPromises()

    expect(onDomainChange).toHaveBeenCalledWith(paymentsDomain)
    expect(repositoryApiMock.createProjectRelease).not.toHaveBeenCalled()
    expect(container.querySelector<HTMLInputElement>('input[placeholder="New project name"]')?.value).toBe('')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="New release name"]')?.value).toBe('')
  })

  it('treats project creation as committed when its list refresh fails', async () => {
    const onProjectCreated = vi.fn().mockResolvedValue({
      id: 'project-created',
      domain_id: domain.id,
      category_id: category.id,
      name: 'Orders API',
      created_at: '',
      updated_at: '',
    })
    const onProjectsRefresh = vi.fn()
      .mockRejectedValueOnce(new Error('project list unavailable'))
      .mockResolvedValue(undefined)
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
          onProjectsRefresh={onProjectsRefresh}
          onProjectCreated={onProjectCreated}
        />,
      )
    })
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())
    const projectInput = container.querySelector<HTMLInputElement>('input[placeholder="New project name"]')
    if (!projectInput) return
    await act(async () => setInputValue(projectInput, 'Orders API'))
    await act(async () => findButton(container, 'Create Project')?.click())
    await flushPromises()

    expect(onProjectCreated).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('The mutation succeeded—do not create the project again.')
    await act(async () => findButton(container, 'Retry project synchronization')?.click())
    await flushPromises()
    expect(onProjectsRefresh).toHaveBeenCalledTimes(2)
    expect(onProjectCreated).toHaveBeenCalledTimes(1)
  })

  it('does not let a late manual refresh from the previous domain replace the active catalog', async () => {
    const secondDomain = { id: 'domain-2', name: 'Payments', created_at: '', updated_at: '' }
    const secondRepository = { id: 'repo-2', domain_id: secondDomain.id, name: 'Application Repository', created_at: '', updated_at: '' }
    const secondCategory = { id: 'category-2', domain_id: secondDomain.id, repository_id: secondRepository.id, name: 'Payments', created_at: '', updated_at: '' }
    const secondProject = { id: 'project-2', domain_id: secondDomain.id, category_id: secondCategory.id, name: 'Payments API', created_at: '', updated_at: '' }
    const stableCallbacks = {
      onDomainChange: vi.fn(),
      onRepositoryRefresh: vi.fn(),
      onProjectsRefresh: vi.fn(),
      onProjectCreated: vi.fn(),
    }

    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain, secondDomain]}
          activeDomain={domain}
          activeApplication={null}
          applicationRepository={repository}
          categories={[category]}
          projects={[project, secondProject]}
          canManageRepository
          canManageProjects
          canManageReleases
          {...stableCallbacks}
        />,
      )
    })
    await flushPromises()

    let resolveStaleScripts: ((value: { scripts: Array<{ id: string; name: string; protocol: string; version: string; project_id: string; created_at: string }>; count: number }) => void) | undefined
    scriptApiMock.listScripts.mockImplementation((projectId: string) => {
      if (projectId === project.id) {
        return new Promise(resolve => {
          resolveStaleScripts = resolve
        })
      }
      return Promise.resolve({
        scripts: [{
          id: 'script-2',
          name: 'Payments Current',
          protocol: 'http',
          version: '1.0.0',
          project_id: secondProject.id,
          created_at: '2026-05-20T00:00:00Z',
        }],
        count: 1,
      })
    })

    const refresh = findButton(container, 'Refresh')
    expect(refresh).toBeTruthy()
    if (!refresh) return
    await act(async () => {
      refresh.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain, secondDomain]}
          activeDomain={secondDomain}
          activeApplication={null}
          applicationRepository={secondRepository}
          categories={[secondCategory]}
          projects={[project, secondProject]}
          canManageRepository
          canManageProjects
          canManageReleases
          {...stableCallbacks}
        />,
      )
    })
    await flushPromises()
    expect(container.textContent).toContain('Payments Current')

    await act(async () => {
      resolveStaleScripts?.({
        scripts: [{
          id: 'script-stale',
          name: 'Retail Stale',
          protocol: 'http',
          version: '1.0.0',
          project_id: project.id,
          created_at: '2026-05-19T00:00:00Z',
        }],
        count: 1,
      })
    })
    await flushPromises()

    expect(container.textContent).toContain('Payments Current')
    expect(container.textContent).not.toContain('Retail Stale')
  })

  it('binds a release draft to the application active at submit time, not at first keystroke', async () => {
    const applicationB = { id: 'app-2', project_id: project.id, name: 'BCC', created_at: '', updated_at: '' }
    const renderPage = (activeApplication: typeof application) => root.render(
      <ApplicationRepositoryPage
        domains={[domain]}
        activeDomain={domain}
        activeApplication={activeApplication}
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
    await act(async () => renderPage(application))
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())
    const releaseInput = container.querySelector<HTMLInputElement>('input[placeholder="New release name"]')
    expect(releaseInput).toBeTruthy()
    if (!releaseInput) return
    await act(async () => setInputValue(releaseInput, '2026.09'))

    // Application switches no longer remount the page, so the draft survives
    // an app-only switch; the binding must follow the visible active app.
    await act(async () => renderPage(applicationB))
    await flushPromises()
    await act(async () => findButton(container, 'Add Release', false)?.click())
    await flushPromises()

    expect(repositoryApiMock.createProjectRelease).toHaveBeenCalledWith(
      project.id,
      expect.objectContaining({ name: '2026.09', application_id: applicationB.id }),
    )
  })

  it('restores the active catalog tab across the remount a scope change causes', async () => {
    const renderPage = () => root.render(
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
    await act(async () => renderPage())
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())
    expect(container.querySelector('input[placeholder="New project name"]')).not.toBeNull()

    // "Set as active" rewrites the workspace scope in the route, which
    // remounts the page; the tab must not reset to All.
    await act(async () => root.unmount())
    root = createRoot(container)
    await act(async () => renderPage())
    await flushPromises()

    expect(container.querySelector('input[placeholder="New project name"]')).not.toBeNull()
  })

  it('makes Structure projects selectable so Open Repository leads somewhere', async () => {
    const onProjectSelect = vi.fn()
    const otherProject = { ...project, id: 'project-other', name: 'Orders API' }
    const renderPage = (activeProject: typeof project | null) => root.render(
      <ApplicationRepositoryPage
        domains={[domain]}
        activeDomain={domain}
        activeApplication={application}
        activeProject={activeProject}
        applicationRepository={repository}
        categories={[category]}
        projects={[project, otherProject]}
        canManageRepository
        canManageProjects
        canManageReleases
        onDomainChange={vi.fn()}
        onProjectSelect={onProjectSelect}
        onRepositoryRefresh={vi.fn()}
        onProjectsRefresh={vi.fn()}
        onProjectCreated={vi.fn()}
      />,
    )

    await act(async () => renderPage(project))
    await flushPromises()
    await act(async () => findButton(container, 'Structure', false)?.click())

    const cards = Array.from(container.querySelectorAll('.repository-project'))
    const activeCard = cards.find(card => card.textContent?.includes('Checkout API'))
    const otherCard = cards.find(card => card.textContent?.includes('Orders API'))
    expect(activeCard?.textContent).toContain('Active project')
    expect(Array.from(activeCard?.querySelectorAll('button') || []).some(button => button.textContent === 'Set as active')).toBe(false)

    const setActive = Array.from(otherCard?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Set as active') as HTMLButtonElement | undefined
    expect(setActive).toBeDefined()
    await act(async () => setActive?.click())

    expect(onProjectSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'project-other' }))
  })
})
