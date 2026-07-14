// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApplicationProvider } from './ApplicationProvider'
import { useApplication } from './useApplication'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const applicationApiMock = {
  listApplications: vi.fn(),
  createApplication: vi.fn(),
  updateApplication: vi.fn(),
  deleteApplication: vi.fn(),
  listReleases: vi.fn(),
  createRelease: vi.fn(),
  updateRelease: vi.fn(),
  archiveRelease: vi.fn(),
  deleteRelease: vi.fn(),
}

const projectState = {
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
}

vi.mock('./api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
}))

vi.mock('./useProject', () => ({
  useProject: () => projectState,
}))

function Probe() {
  const { applications, releases, activeApplication, activeRelease, setActiveApplication, createAndSelectApplication } = useApplication()

  return (
    <div>
      <span data-testid="application-count">{applications.length}</span>
      <span data-testid="active-application">{activeApplication?.name || ''}</span>
      <span data-testid="releases">{releases.map(release => release.name).join(',')}</span>
      <span data-testid="active-release">{activeRelease?.name || ''}</span>
      {applications.map(application => (
        <button key={application.id} type="button" onClick={() => setActiveApplication(application)}>
          Select {application.name}
        </button>
      ))}
      <button type="button" onClick={() => createAndSelectApplication('New App')}>Create</button>
    </div>
  )
}

describe('ApplicationProvider', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    localStorage.clear()
    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }

    applicationApiMock.listApplications.mockResolvedValue({
      applications: [
        {
          id: 'app-1',
          project_id: 'project-1',
          name: 'Payments',
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
      ],
      count: 1,
    })
    applicationApiMock.createApplication.mockResolvedValue({
      id: 'app-2',
      project_id: 'project-1',
      name: 'New App',
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [], count: 0 })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('loads and creates applications within the active project', async () => {
    await act(async () => {
      root.render(
        <ApplicationProvider>
          <Probe />
        </ApplicationProvider>,
      )
    })

    await flushPromises()

    expect(applicationApiMock.listApplications).toHaveBeenCalledWith('project-1')
    expect(container.querySelector('[data-testid="application-count"]')?.textContent).toBe('1')
    expect(container.querySelector('[data-testid="active-application"]')?.textContent).toBe('Payments')

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create')
    expect(createButton).toBeTruthy()
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(applicationApiMock.createApplication).toHaveBeenCalledWith({ name: 'New App', project_id: 'project-1' })
  })

  it('clears the previous project immediately and ignores its late application response', async () => {
    const projectOne = deferred<Awaited<ReturnType<typeof applicationApiMock.listApplications>>>()
    const projectTwo = deferred<Awaited<ReturnType<typeof applicationApiMock.listApplications>>>()
    applicationApiMock.listApplications.mockImplementation((projectId: string) => {
      if (projectId === 'project-1') return projectOne.promise
      return projectTwo.promise
    })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    expect(applicationApiMock.listApplications).toHaveBeenCalledWith('project-1')

    projectState.activeProject = { id: 'project-2', name: 'Catalog', created_at: '', updated_at: '' }
    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })

    expect(readTestId('active-application')).toBe('')
    await act(async () => {
      projectTwo.resolve({ applications: [application('app-2', 'project-2', 'Catalog')], count: 1 })
    })
    await flushPromises()
    expect(applicationApiMock.listApplications).toHaveBeenCalledWith('project-2')
    expect(readTestId('active-application')).toBe('Catalog')

    await act(async () => {
      projectOne.resolve({ applications: [application('app-1', 'project-1', 'Payments')], count: 1 })
    })
    await flushPromises()

    expect(readTestId('active-application')).toBe('Catalog')
    expect(readTestId('application-count')).toBe('1')
  })

  it('ignores a late release response after the selected application changes', async () => {
    const firstReleases = deferred<Awaited<ReturnType<typeof applicationApiMock.listReleases>>>()
    applicationApiMock.listApplications.mockResolvedValue({
      applications: [
        application('app-1', 'project-1', 'Payments'),
        application('app-2', 'project-1', 'Catalog'),
      ],
      count: 2,
    })
    applicationApiMock.listReleases.mockImplementation((applicationId: string) => {
      if (applicationId === 'app-1') return firstReleases.promise
      return Promise.resolve({
        releases: [release('release-2', 'app-2', 'Catalog 1.0')],
        count: 1,
      })
    })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()

    const selectCatalog = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Select Catalog')
    await act(async () => {
      selectCatalog?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(readTestId('releases')).toBe('Catalog 1.0')

    await act(async () => {
      firstReleases.resolve({ releases: [release('release-1', 'app-1', 'Payments 1.0')], count: 1 })
    })
    await flushPromises()

    expect(readTestId('active-application')).toBe('Catalog')
    expect(readTestId('releases')).toBe('Catalog 1.0')
    expect(readTestId('active-release')).toBe('Catalog 1.0')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent || ''
  }
})

function application(id: string, projectId: string, name: string) {
  return {
    id,
    project_id: projectId,
    name,
    created_at: '2026-05-15T00:00:00Z',
    updated_at: '2026-05-15T00:00:00Z',
  }
}

function release(id: string, applicationId: string, name: string) {
  return {
    id,
    application_id: applicationId,
    name,
    created_at: '2026-05-15T00:00:00Z',
    updated_at: '2026-05-15T00:00:00Z',
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}
